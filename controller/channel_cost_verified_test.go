package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func setupChannelCostVerifiedTest(t *testing.T) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	var db *gorm.DB
	var err error
	if dsn := os.Getenv("CILAI_TEST_POSTGRES_DSN"); dsn != "" {
		db, err = testutil.OpenPostgres(dsn)
	} else if dsn := os.Getenv("CILAI_TEST_MYSQL_DSN"); dsn != "" {
		config, parseErr := mysqldriver.ParseDSN(dsn)
		require.NoError(t, parseErr)
		require.Equal(t, "tcp", config.Net)
		require.True(t, strings.HasPrefix(config.Addr, "127.0.0.1:"))
		require.True(t, strings.HasPrefix(config.DBName, "cilai_pr3_"))
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	} else {
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	}
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	if os.Getenv("CILAI_TEST_POSTGRES_DSN") != "" {
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
		sqlDB.SetMaxOpenConns(10)
	}
	if os.Getenv("CILAI_TEST_MYSQL_DSN") != "" {
		common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
	}
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.SetDatabaseTypes(previousMainType, previousLogType)
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelCostVersion{}, &model.Ability{}, &model.Log{}))
	if os.Getenv("CILAI_TEST_POSTGRES_DSN") != "" || os.Getenv("CILAI_TEST_MYSQL_DSN") != "" {
		t.Cleanup(func() {
			for _, table := range []string{"channel_cost_versions", "channels", "abilities", "logs"} {
				require.NoError(t, db.Exec("DELETE FROM "+table).Error)
			}
		})
	}
}

func invokeUpdateChannel(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	body, err := common.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel/", bytes.NewReader(body))
	ctx.Set("id", 1)
	ctx.Set("role", common.RoleRootUser)
	UpdateChannel(ctx)
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return response
}

func TestChannelCostVerifiedStampedOnUpstreamCostsUpdate(t *testing.T) {
	setupChannelCostVerifiedTest(t)
	channel := &model.Channel{
		Name: "cost-verified", Type: 1, Key: "sk-cost", Models: "gpt-4o", Group: "default",
		Status: common.ChannelStatusEnabled,
	}
	require.NoError(t, model.DB.Create(channel).Error)

	costs := `{"gpt-4o":{"input":1,"output":2}}`
	response := invokeUpdateChannel(t, map[string]any{
		"id": channel.Id, "name": channel.Name, "type": 1, "key": "sk-cost",
		"models": "gpt-4o", "group": "default", "upstream_costs": costs,
	})
	require.Equal(t, true, response["success"], "update failed: %v", response)

	var after model.Channel
	require.NoError(t, model.DB.First(&after, channel.Id).Error)
	assert.Equal(t, costs, after.UpstreamCosts)
	assert.Greater(t, after.CostVerifiedAt, int64(0), "a cost change must stamp cost_verified_at")
	assert.Equal(t, 1, after.CostVerifiedBy, "the stamp must name the operating admin")
	firstStamp := after.CostVerifiedAt

	// A later edit that does not touch upstream_costs keeps the stamp, even if
	// the client forges cost_verified_* values in the request body.
	response = invokeUpdateChannel(t, map[string]any{
		"id": channel.Id, "name": channel.Name, "type": 1, "key": "sk-cost",
		"models": "gpt-4o", "group": "default",
		"cost_verified_at": 12345, "cost_verified_by": 999,
	})
	require.Equal(t, true, response["success"], "update failed: %v", response)
	require.NoError(t, model.DB.First(&after, channel.Id).Error)
	assert.Equal(t, firstStamp, after.CostVerifiedAt)
	assert.Equal(t, 1, after.CostVerifiedBy)

	// Re-saving the same costs does not move the stamp; a real change does.
	response = invokeUpdateChannel(t, map[string]any{
		"id": channel.Id, "name": channel.Name, "type": 1, "key": "sk-cost",
		"models": "gpt-4o", "group": "default", "upstream_costs": costs,
	})
	require.Equal(t, true, response["success"], "update failed: %v", response)
	require.NoError(t, model.DB.First(&after, channel.Id).Error)
	assert.Equal(t, firstStamp, after.CostVerifiedAt)

	newCosts := `{"gpt-4o":{"input":1.5,"output":2.5}}`
	response = invokeUpdateChannel(t, map[string]any{
		"id": channel.Id, "name": channel.Name, "type": 1, "key": "sk-cost",
		"models": "gpt-4o", "group": "default", "upstream_costs": newCosts,
	})
	require.Equal(t, true, response["success"], "update failed: %v", response)
	require.NoError(t, model.DB.First(&after, channel.Id).Error)
	assert.GreaterOrEqual(t, after.CostVerifiedAt, firstStamp)
	assert.Equal(t, newCosts, after.UpstreamCosts)
}

func TestChannelCostValidationAndStaleEditor(t *testing.T) {
	setupChannelCostVerifiedTest(t)
	channel := &model.Channel{Name: "procurement", Type: 1, Key: "sk-test", Models: "model-a", Group: "default", Status: common.ChannelStatusEnabled, UpstreamCosts: `{"model-a":{"input":1,"cached_input":0,"cache_creation":0,"output":2}}`}
	require.NoError(t, model.DB.Create(channel).Error)
	for _, raw := range []string{`null`, `[]`, `{"model-a":{"input":null,"output":2}}`, `{"model-a":{"input":-1,"output":2}}`, `{"model-a":{"input":1,"cache_creation":null,"output":2}}`} {
		response := invokeUpdateChannel(t, map[string]any{"id": channel.Id, "name": channel.Name, "type": 1, "models": channel.Models, "group": channel.Group, "upstream_costs": raw})
		assert.Equal(t, false, response["success"], raw)
	}
	first := channel.UpstreamCosts
	updated := `{"model-a":{"input":2,"cached_input":0,"cache_creation":0,"output":4}}`
	response := invokeUpdateChannel(t, map[string]any{"id": channel.Id, "type": 1, "models": channel.Models, "group": channel.Group, "upstream_costs": updated, "expected_upstream_costs": first})
	require.Equal(t, true, response["success"])
	response = invokeUpdateChannel(t, map[string]any{"id": channel.Id, "type": 1, "name": "stale editor", "models": channel.Models, "group": channel.Group, "upstream_costs": "{}", "expected_upstream_costs": first})
	require.Equal(t, false, response["success"])
	var after model.Channel
	require.NoError(t, model.DB.First(&after, channel.Id).Error)
	assert.Equal(t, updated, after.UpstreamCosts)
	assert.Equal(t, channel.Name, after.Name)
	response = invokeUpdateChannel(t, map[string]any{"id": channel.Id, "type": 1, "models": channel.Models, "group": channel.Group, "upstream_costs": "{}", "expected_upstream_costs": updated})
	require.Equal(t, true, response["success"])
	require.NoError(t, model.DB.First(&after, channel.Id).Error)
	assert.Equal(t, "{}", after.UpstreamCosts)
	var history []model.ChannelCostVersion
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).Order("id").Find(&history).Error)
	require.Len(t, history, 2)
	assert.Equal(t, updated, history[0].Costs)
	assert.Equal(t, "{}", history[1].Costs)
	require.NoError(t, model.InitializeChannelCostHistory(model.DB))
	require.NoError(t, model.InitializeChannelCostHistory(model.DB))
	var count int64
	require.NoError(t, model.DB.Model(&model.ChannelCostVersion{}).Where("channel_id = ?", channel.Id).Count(&count).Error)
	assert.EqualValues(t, 2, count)
}

func TestChannelCostPeakSchedule(t *testing.T) {
	setupChannelCostVerifiedTest(t)
	raw := `{"deepseek":{"currency":"CNY","input":1,"cached_input":0.1,"cache_creation":0,"output":4,"peak":{"prices":{"input":2,"cached_input":0.2,"cache_creation":0,"output":8},"weekdays":[1,2,3,4,5],"windows":["09:00-12:00","14:00-18:00"],"offpeak_dates":["2026-10-01"]}}}`
	require.NoError(t, validateUpstreamCosts(raw))
	for _, invalid := range []string{strings.Replace(raw, "09:00-12:00", "12:00-09:00", 1), strings.Replace(raw, "2026-10-01", "2026-02-30", 1), strings.Replace(raw, `"currency":"CNY"`, `"currency":"unknown"`, 1), strings.Replace(raw, `"weekdays":[1,2,3,4,5]`, `"weekdays":[0]`, 1)} {
		assert.Error(t, validateUpstreamCosts(invalid))
	}
	channel := model.Channel{Name: "cost-history", Key: "test", Models: "deepseek", Group: "default", UpstreamCosts: raw, CostVerifiedBy: 1}
	require.NoError(t, channel.Insert())
	var rows []model.ChannelCostVersion
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, raw, rows[0].Costs)
	// Existing-price bootstrap is idempotent and does not backdate a contract.
	require.NoError(t, model.InitializeChannelCostHistory(model.DB))
	require.NoError(t, model.InitializeChannelCostHistory(model.DB))
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.NoError(t, channel.Delete())
	require.NoError(t, model.DB.Where("channel_id = ?", channel.Id).Find(&rows).Error)
	require.Len(t, rows, 1, "channel removal must preserve historical costing")
}

func TestChannelCostAtomicHistoryAndConcurrentEditors(t *testing.T) {
	setupChannelCostVerifiedTest(t)
	original := `{"a":{"input":1,"output":2}}`
	channel := model.Channel{Name: "original", Key: "test", Models: "a", Group: "default", UpstreamCosts: original}
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("test:fail_cost_history", func(tx *gorm.DB) {
		if tx.Statement.Table == "channel_cost_versions" {
			tx.AddError(fmt.Errorf("history unavailable"))
		}
	}))
	updated := model.Channel{Id: channel.Id, Name: "must rollback", Models: "a", Group: "default", UpstreamCosts: `{"a":{"input":2,"output":4}}`}
	assert.Error(t, updated.Update(original))
	require.NoError(t, model.DB.Callback().Create().Remove("test:fail_cost_history"))
	var after model.Channel
	require.NoError(t, model.DB.First(&after, channel.Id).Error)
	assert.Equal(t, original, after.UpstreamCosts)
	assert.Equal(t, "original", after.Name)
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, raw := range []string{`{"a":{"input":2,"output":4}}`, `{"a":{"input":3,"output":6}}`} {
		group.Go(func() {
			candidate := model.Channel{Id: channel.Id, Models: "a", Group: "default", UpstreamCosts: raw}
			results <- candidate.Update(original)
		})
	}
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	assert.Equal(t, 1, successes)
	var count int64
	require.NoError(t, model.DB.Model(&model.ChannelCostVersion{}).Where("channel_id = ?", channel.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

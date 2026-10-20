package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.SetDatabaseTypes(previousMainType, previousLogType)
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Log{}))
	if os.Getenv("CILAI_TEST_POSTGRES_DSN") != "" {
		t.Cleanup(func() {
			for _, table := range []string{"channels", "abilities", "logs"} {
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

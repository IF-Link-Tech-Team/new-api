package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openRankingTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousRedis := common.RedisEnabled
	var driver gorm.Dialector
	var versionQuery string
	switch os.Getenv("RANKING_TEST_DIALECT") {
	case "", "sqlite":
		driver = sqlite.Open(filepath.Join(t.TempDir(), "rankings.db"))
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
		versionQuery = "SELECT sqlite_version()"
	case "mysql":
		require.NotEmpty(t, os.Getenv("RANKING_TEST_DSN"))
		driver = mysql.Open(os.Getenv("RANKING_TEST_DSN"))
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
		versionQuery = "SELECT VERSION()"
	case "postgres":
		require.NotEmpty(t, os.Getenv("RANKING_TEST_DSN"))
		driver = postgres.Open(os.Getenv("RANKING_TEST_DSN"))
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
		versionQuery = "SELECT VERSION()"
	default:
		t.Fatal("unsupported RANKING_TEST_DIALECT")
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) || common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		query := "SELECT DATABASE()"
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			query = "SELECT current_database()"
		}
		var database string
		require.NoError(t, db.Raw(query).Scan(&database).Error)
		require.True(t, strings.HasPrefix(database, "rankings_role_"), "only dedicated disposable test databases are allowed")
	}
	require.False(t, db.Migrator().HasTable("quota_data"), "refuse to modify an existing quota database")
	require.NoError(t, db.AutoMigrate(&model.QuotaData{}, &model.Ability{}, &model.Channel{}, &model.Model{}, &model.Vendor{}))
	model.DB = db
	common.RedisEnabled = false
	model.InvalidatePricingCache()
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s", version)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.RedisEnabled = previousRedis
		model.InvalidatePricingCache()
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestRankingsExcludeRoleRestrictedGroups(t *testing.T) {
	db := openRankingTestDatabase(t)
	previousPolicy := ratio_setting.GetGroupRatioSetting().GroupMinRole.MarshalJSONString()
	rankingCacheMu.Lock()
	previousCache := rankingCache
	rankingCache = make(map[string]rankingCacheItem)
	rankingCacheMu.Unlock()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_min_role": previousPolicy}))
		rankingCacheMu.Lock()
		rankingCache = previousCache
		rankingCacheMu.Unlock()
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"group_ratio_setting.group_min_role": `{"内部":10,"root-lab":100}`,
	}))
	rows := []model.QuotaData{
		{ModelName: "shared", UseGroup: "default", CreatedAt: 11000, TokenUsed: 40},
		{ModelName: "shared", UseGroup: "内部", CreatedAt: 11000, TokenUsed: 900},
		{ModelName: "internal-only", UseGroup: "内部", CreatedAt: 11000, TokenUsed: 500},
		{ModelName: "root-only", UseGroup: "root-lab", CreatedAt: 11000, TokenUsed: 300},
		{ModelName: "public", UseGroup: "seu-beta", CreatedAt: 14500, TokenUsed: 20},
		{ModelName: "legacy", CreatedAt: 15000, TokenUsed: 30},
		{ModelName: "legacy-null", CreatedAt: 15100, TokenUsed: 10},
		{ModelName: "outside-range", UseGroup: "default", CreatedAt: 9000, TokenUsed: 777},
		{ModelName: "zero", UseGroup: "default", CreatedAt: 11000},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Model(&model.QuotaData{}).Where("model_name = ?", "legacy-null").Update("use_group", nil).Error)
	totals, err := model.GetRankingQuotaTotals(10000, 20000)
	require.NoError(t, err)
	want := map[string]int64{"shared": 40, "public": 20, "legacy": 30, "legacy-null": 10}
	got := make(map[string]int64)
	for _, row := range totals {
		got[row.ModelName] = row.TotalTokens
	}
	assert.Equal(t, want, got)
	buckets, err := model.GetRankingQuotaBuckets(10000, 20000, 3600)
	require.NoError(t, err)
	got = make(map[string]int64)
	for _, row := range buckets {
		got[row.ModelName] += row.Tokens
		assert.Equal(t, int64(0), row.Bucket%3600)
	}
	assert.Equal(t, want, got)

	// Simulate a ranking cached before a group is made private. The next
	// request must rebuild every derived public series, not wait for the TTL.
	now := time.Now()
	require.NoError(t, db.Create(&[]model.QuotaData{
		{ModelName: "recent-shared", UseGroup: "default", CreatedAt: now.Add(-time.Hour).Unix(), TokenUsed: 100},
		{ModelName: "recent-shared", UseGroup: "内部", CreatedAt: now.Add(-time.Hour).Unix(), TokenUsed: 900},
		{ModelName: "recent-private", UseGroup: "内部", CreatedAt: now.Add(-time.Hour).Unix(), TokenUsed: 500},
		{ModelName: "recent-shared", UseGroup: "default", CreatedAt: now.Add(-8 * 24 * time.Hour).Unix(), TokenUsed: 50},
		{ModelName: "recent-private", UseGroup: "内部", CreatedAt: now.Add(-8 * 24 * time.Hour).Unix(), TokenUsed: 700},
	}).Error)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_min_role": `{}`}))
	before, err := GetRankingsSnapshot("week")
	require.NoError(t, err)
	require.Len(t, before.Models, 2)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"group_ratio_setting.group_min_role": `{"内部":10}`}))
	after, err := GetRankingsSnapshot("week")
	require.NoError(t, err)
	assert.NotSame(t, before, after)
	require.Len(t, after.Models, 1)
	assert.Equal(t, "recent-shared", after.Models[0].ModelName)
	assert.Equal(t, int64(100), after.Models[0].TotalTokens)
	assert.Equal(t, float64(100), after.Models[0].GrowthPct)
	require.Len(t, after.Vendors, 1)
	assert.Equal(t, int64(100), after.Vendors[0].TotalTokens)
	for _, period := range []string{"today", "week", "month", "year"} {
		result, err := GetRankingsSnapshot(period)
		require.NoError(t, err)
		encoded, err := common.Marshal(result)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "recent-private", period)
	}
	var total int64
	require.NoError(t, db.Model(&model.QuotaData{}).Where("use_group = ?", "内部").Select("sum(token_used)").Scan(&total).Error)
	assert.Equal(t, int64(3500), total, "private usage must remain in the underlying accounting data")
}

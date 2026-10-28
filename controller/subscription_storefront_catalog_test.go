package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func setupStorefrontCatalogTest(t *testing.T) *gin.Engine {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	// Initialize the shared identifier quoting through the native connection
	// path, then use the existing disposable test connection below.
	originalMaster, originalSQLitePath := common.IsMasterNode, common.SQLitePath
	common.IsMasterNode = false
	common.SQLitePath = "file:storefront_column_init?mode=memory&cache=shared"
	dsn := os.Getenv("CILAI_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("CILAI_TEST_MYSQL_DSN")
	}
	if dsn == "" {
		dsn = "local"
	}
	t.Setenv("SQL_DSN", dsn)
	require.NoError(t, model.InitDB())
	initDB, err := model.DB.DB()
	require.NoError(t, err)
	require.NoError(t, initDB.Close())
	common.IsMasterNode, common.SQLitePath = originalMaster, originalSQLitePath
	model.DB, model.LOG_DB = previousDB, previousLogDB
	var db *gorm.DB
	if dsn := os.Getenv("CILAI_TEST_POSTGRES_DSN"); dsn != "" {
		db, err = testutil.OpenPostgres(dsn)
		require.NoError(t, err)
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	} else if dsn := os.Getenv("CILAI_TEST_MYSQL_DSN"); dsn != "" {
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		common.SetDatabaseTypes(common.DatabaseTypeMySQL, common.DatabaseTypeMySQL)
	} else {
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	}
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false

	common.OptionMapRWMutex.Lock()
	previousHeaderNav, hadHeaderNav := common.OptionMap["HeaderNavModules"]
	delete(common.OptionMap, "HeaderNavModules")
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.OptionMapRWMutex.Lock()
		if hadHeaderNav {
			common.OptionMap["HeaderNavModules"] = previousHeaderNav
		} else {
			delete(common.OptionMap, "HeaderNavModules")
		}
		common.OptionMapRWMutex.Unlock()
		// Drop any catalog entries cached against this test's database.
		model.InvalidateSubscriptionPlanCache(1)
	})
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}))
	if os.Getenv("CILAI_TEST_POSTGRES_DSN") != "" || os.Getenv("CILAI_TEST_MYSQL_DSN") != "" {
		t.Cleanup(func() {
			require.NoError(t, db.Exec("DELETE FROM subscription_plans").Error)
		})
	}
	// Ensure no stale catalog survives from an earlier test in this process.
	model.InvalidateSubscriptionPlanCache(1)

	router := gin.New()
	router.GET("/api/subscription/public/catalog", middleware.HeaderNavModulePublic("pricing"), GetSubscriptionPublicCatalog)
	return router
}

func createStorefrontCatalogPlan(t *testing.T, plan model.SubscriptionPlan) model.SubscriptionPlan {
	t.Helper()
	// Legacy gorm default:true tags write the DB default back into the struct
	// on Create, so capture the intended draft switches beforehand.
	wantEnabled, wantListed := plan.Enabled, plan.Listed
	require.NoError(t, model.DB.Create(&plan).Error)
	overrides := map[string]interface{}{}
	if !wantEnabled {
		overrides["enabled"] = false
	}
	if !wantListed {
		overrides["listed"] = false
	}
	if len(overrides) > 0 {
		require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", plan.Id).UpdateColumns(overrides).Error)
	}
	model.InvalidateSubscriptionPlanCache(plan.Id)
	return plan
}

func requestStorefrontCatalog(t *testing.T, router *gin.Engine) (int, []byte) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/subscription/public/catalog", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.Bytes()
}

func decodeStorefrontCatalog(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var response struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(body, &response))
	require.True(t, response.Success, string(body))
	return response.Data
}

func TestStorefrontCatalogServesAnonymousRequestsWithoutCostFields(t *testing.T) {
	router := setupStorefrontCatalogTest(t)
	func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if common.OptionMap == nil {
			common.OptionMap = make(map[string]string)
		}
		common.OptionMap["HeaderNavModules"] = `{"pricing":{"enabled":true,"requireAuth":true}}`
	}()
	createStorefrontCatalogPlan(t, model.SubscriptionPlan{
		Title:           "Pro",
		Subtitle:        "适合稳定使用与高频开发",
		PriceAmount:     49.9,
		Currency:        "CNY",
		DurationUnit:    model.SubscriptionDurationDay,
		DurationValue:   30,
		TotalAmount:     24950000,
		Category:        model.SubscriptionCategorySubscription,
		ProductCode:     "pro",
		Listed:          true,
		Enabled:         true,
		SortOrder:       10,
		AllowedChannels: `{"deepseek-v4-flash-ga-260731":[7]}`,
		MaxUpstreamCost: `{"deepseek-v4-flash-ga-260731":{"input":9.99}}`,
		DisplayMetadata: `{"badges":[{"label":"主推","tone":"accent"}],"recommended":true,"internal_note":"do not expose"}`,
	})

	code, body := requestStorefrontCatalog(t, router)
	require.Equal(t, http.StatusOK, code)
	items := decodeStorefrontCatalog(t, body)
	require.Len(t, items, 1)
	item := items[0]
	assert.Equal(t, "pro", item["product_id"])
	assert.Equal(t, "Pro", item["title"])
	assert.Equal(t, "适合稳定使用与高频开发", item["subtitle"])
	assert.Equal(t, 49.9, item["price_amount"])
	assert.Equal(t, "CNY", item["currency"])
	assert.Equal(t, "day", item["duration_unit"])
	assert.Equal(t, float64(30), item["duration_value"])
	assert.Equal(t, float64(24950000), item["total_amount"])
	assert.Equal(t, "subscription", item["category"])
	assert.Equal(t, float64(10), item["sort_order"])
	assert.Equal(t, true, item["enabled"])
	assert.Equal(t, false, item["preview"])
	metadata, ok := item["display_metadata"].(map[string]any)
	require.True(t, ok, "display_metadata must decode to an object: %v", item["display_metadata"])
	assert.Equal(t, true, metadata["recommended"])
	assert.NotContains(t, metadata, "internal_note")

	for _, forbidden := range []string{
		"allowed_channels", "max_upstream_cost", "fallback_allowed",
		"billing_multiplier", "price_version", "product_tier",
		"stripe_price_id", "creem_product_id", "upgrade_group",
		"allow_balance_pay", "allow_wallet_overflow", "redemption_enabled",
	} {
		assert.NotContains(t, string(body), `"`+forbidden+`"`, "catalog DTO must not expose %s", forbidden)
	}
	assert.NotContains(t, string(body), "9.99", "upstream settlement cost must never leak")
}

func TestProductPagesShareCatalogDiscoveryWithoutSellingUnlistedPreviews(t *testing.T) {
	router := setupStorefrontCatalogTest(t)
	router.GET("/plans", GetSubscriptionPlans)
	for _, plan := range []model.SubscriptionPlan{
		{Title: "Listed", Listed: true, Enabled: true},
		{Title: "Preview", Enabled: true, DisplayMetadata: `{"preview":true}`},
		{Title: "Hidden", Enabled: true},
	} {
		createStorefrontCatalogPlan(t, plan)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/plans", nil))
	var result struct {
		Success bool                  `json:"success"`
		Data    []SubscriptionPlanDTO `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	require.True(t, result.Success)
	require.Len(t, result.Data, 2)
	byTitle := map[string]bool{}
	for _, item := range result.Data {
		byTitle[item.Plan.Title] = item.Plan.Enabled
	}
	assert.Equal(t, map[string]bool{"Listed": true, "Preview": false}, byTitle)
	items, err := model.GetStorefrontCatalog()
	require.NoError(t, err)
	for _, item := range items {
		assert.True(t, item.Plan.Enabled, "display DTO must not mutate the shared catalog")
	}
}

func TestPreserveUnsubmittedSubscriptionPlanFields(t *testing.T) {
	existing := model.SubscriptionPlan{
		Currency: "CNY", AllowedChannels: `{"model":[1]}`,
		MaxUpstreamCost: `{"model":{"input":1}}`, DisplayMetadata: `{"recommended":true}`,
	}
	updated := model.SubscriptionPlan{Title: "Updated"}
	preserveUnsubmittedSubscriptionPlanFields(&updated, existing)

	require.Equal(t, existing.Currency, updated.Currency)
	require.Equal(t, existing.AllowedChannels, updated.AllowedChannels)
	require.Equal(t, existing.MaxUpstreamCost, updated.MaxUpstreamCost)
	require.Equal(t, existing.DisplayMetadata, updated.DisplayMetadata)
}

func TestStorefrontCatalogRedactsUnknownDisplayMetadata(t *testing.T) {
	router := setupStorefrontCatalogTest(t)
	createStorefrontCatalogPlan(t, model.SubscriptionPlan{
		Title: "Pro", ProductCode: "pro", Listed: true, Enabled: true,
		DisplayMetadata: `{"button_label":"Buy","internal_note":"secret","allowed_channels":{"model":[7]}}`,
	})

	_, body := requestStorefrontCatalog(t, router)
	items := decodeStorefrontCatalog(t, body)
	require.Len(t, items, 1)
	metadata := items[0]["display_metadata"].(map[string]any)
	require.Equal(t, "Buy", metadata["button_label"])
	assert.NotContains(t, metadata, "internal_note")
	assert.NotContains(t, metadata, "allowed_channels")
}

func TestStorefrontCatalogIncludesPreviewDraftsAndHidesUnlistedDrafts(t *testing.T) {
	router := setupStorefrontCatalogTest(t)
	createStorefrontCatalogPlan(t, model.SubscriptionPlan{
		Title:       "Lite",
		Category:    model.SubscriptionCategorySubscription,
		ProductCode: "lite",
		Listed:      true,
		Enabled:     true,
		SortOrder:   5,
	})
	createStorefrontCatalogPlan(t, model.SubscriptionPlan{
		Title:           "DeepSeek 大肥鱼 Pro",
		Category:        model.SubscriptionCategoryPackage,
		ProductCode:     "deepseek-bigfish-pro-20261017",
		Listed:          false,
		Enabled:         false,
		DisplayMetadata: `{"preview":true,"image":"/branding/bigfish-pro.webp"}`,
	})
	createStorefrontCatalogPlan(t, model.SubscriptionPlan{
		Title:           "Hidden Draft",
		Category:        model.SubscriptionCategoryPackage,
		ProductCode:     "hidden-draft",
		Listed:          false,
		Enabled:         false,
		DisplayMetadata: `{"badges":[]}`,
	})
	createStorefrontCatalogPlan(t, model.SubscriptionPlan{
		Title:           "Broken Metadata Draft",
		Category:        model.SubscriptionCategoryPackage,
		ProductCode:     "broken-metadata-draft",
		Listed:          false,
		Enabled:         false,
		DisplayMetadata: `{"preview":`,
	})

	items := decodeStorefrontCatalog(t, func() []byte {
		_, body := requestStorefrontCatalog(t, router)
		return body
	}())
	require.Len(t, items, 2)
	byID := make(map[string]map[string]any, len(items))
	for _, item := range items {
		byID[item["product_id"].(string)] = item
	}
	require.Contains(t, byID, "lite")
	require.Contains(t, byID, "deepseek-bigfish-pro-20261017")
	assert.Equal(t, false, byID["lite"]["preview"])
	assert.Equal(t, true, byID["deepseek-bigfish-pro-20261017"]["preview"])
	assert.Equal(t, false, byID["deepseek-bigfish-pro-20261017"]["enabled"])
}

func TestStorefrontCatalogCacheInvalidatesWithPlanWrites(t *testing.T) {
	router := setupStorefrontCatalogTest(t)
	createStorefrontCatalogPlan(t, model.SubscriptionPlan{
		Title: "Lite", Category: model.SubscriptionCategorySubscription,
		ProductCode: "lite", Listed: true, Enabled: true,
	})

	items := decodeStorefrontCatalog(t, func() []byte {
		_, body := requestStorefrontCatalog(t, router)
		return body
	}())
	require.Len(t, items, 1)

	// A direct DB write without cache invalidation stays invisible within the TTL.
	require.NoError(t, model.DB.Create(&model.SubscriptionPlan{
		Title: "Max", Category: model.SubscriptionCategorySubscription,
		ProductCode: "max", Listed: true, Enabled: true,
	}).Error)
	items = decodeStorefrontCatalog(t, func() []byte {
		_, body := requestStorefrontCatalog(t, router)
		return body
	}())
	require.Len(t, items, 1, "cached catalog must survive unrelated direct writes within the TTL")

	// The standard plan-write invalidation point refreshes the catalog.
	var max model.SubscriptionPlan
	require.NoError(t, model.DB.Where("product_code = ?", "max").First(&max).Error)
	model.InvalidateSubscriptionPlanCache(max.Id)
	items = decodeStorefrontCatalog(t, func() []byte {
		_, body := requestStorefrontCatalog(t, router)
		return body
	}())
	require.Len(t, items, 2)
}

func TestStorefrontCatalogRejectsMalformedDisplayMetadataOnAdminWrite(t *testing.T) {
	plan := model.SubscriptionPlan{
		Title:           "Broken",
		DisplayMetadata: `{"preview":`,
	}
	require.Error(t, normalizeAdminSubscriptionPlan(&plan))
	plan.DisplayMetadata = `["not","an","object"]`
	require.Error(t, normalizeAdminSubscriptionPlan(&plan))
	plan.DisplayMetadata = `{"preview":true}`
	require.NoError(t, normalizeAdminSubscriptionPlan(&plan))
}

func TestStorefrontListedSaleOverridesHistoricalPreview(t *testing.T) {
	router := setupStorefrontCatalogTest(t)
	createStorefrontCatalogPlan(t, model.SubscriptionPlan{
		Title: "Live fish", ProductCode: "fish", Enabled: true, Listed: true,
		MaxPurchasePerUser: 1, QuotaResetPeriod: "daily",
		DisplayMetadata: `{"preview":true}`,
	})
	_, body := requestStorefrontCatalog(t, router)
	items := decodeStorefrontCatalog(t, body)
	require.Len(t, items, 1)
	assert.Equal(t, true, items[0]["enabled"])
	assert.Equal(t, false, items[0]["preview"], "a listed product is not a display-only draft")
	assert.Equal(t, float64(1), items[0]["max_purchase_per_user"])
	assert.Equal(t, "daily", items[0]["quota_reset_period"])
}

func TestStorefrontModelsOnlyExposePublicLiveSalesAndNativeMetadata(t *testing.T) {
	initModelListColumnNames(t)
	router := setupStorefrontCatalogTest(t)
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	require.NoError(t, model.DB.AutoMigrate(&model.APIChannelCatalog{}, &model.APIChannelPrice{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}))
	require.NoError(t, model.InitializeAPIChannelCatalog(model.DB))
	require.NoError(t, model.DB.Model(&model.APIChannelCatalog{}).Where("id = ?", 1).Update("revision", 1).Error)
	channel := model.Channel{Id: 71, Type: 1, Name: "private channel name", Models: "live-model,unpriced-model,disabled-model,hidden-model", Group: "default", Status: common.ChannelStatusEnabled, Key: "private-upstream-key"}
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	for _, name := range []string{"live-model", "disabled-model", "hidden-model"} {
		require.NoError(t, model.DB.Create(&model.APIChannelPrice{Revision: 1, Model: name, ChannelID: 71, UpstreamModel: name, Enabled: name != "disabled-model", SellingPrice: `{"input":12345}`}).Error)
	}
	require.NoError(t, model.DB.Create(&model.Model{ModelName: "live-model", Status: 1, Description: "Backend description"}).Error)
	require.NoError(t, model.DB.Create(&model.Model{ModelName: "hidden-model", Status: 1}).Error)
	require.NoError(t, model.DB.Model(&model.Model{}).Where("model_name = ?", "hidden-model").Update("status", 0).Error)
	model.InvalidatePricingCache()
	t.Cleanup(model.InvalidatePricingCache)
	router.GET("/api/subscription/public/models", middleware.HeaderNavModulePublic("pricing"), GetSubscriptionPublicModels)
	request := func() []map[string]any {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/subscription/public/models", nil))
		require.Equal(t, http.StatusOK, recorder.Code)
		return decodeStorefrontCatalog(t, recorder.Body.Bytes())
	}
	items := request()
	require.Equal(t, []map[string]any{{"model_name": "live-model", "description": "Backend description"}}, items)
	require.NoError(t, model.DB.Model(&model.APIChannelPrice{}).Where("model = ?", "live-model").Update("enabled", false).Error)
	require.Empty(t, request(), "disabling a sale disappears from the public model projection")
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "invalid")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/subscription/public/models", nil))
	assert.Contains(t, recorder.Body.String(), `"success":false`)
}

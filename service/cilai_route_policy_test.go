package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveChannelSelectionPolicyUsesExactModelAndFailsClosed(t *testing.T) {
	truncate(t)
	price := &model.ProductPrice{
		ProductType: model.ProductTypePackQwen, Model: "qwen3.8-flash", PriceVersion: "qwen-route-v1",
		OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"output":4}`,
		UserSellingPrice:        `{"input":1,"cached_input":0.2,"output":4}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"output":2}`,
		BillingExpression:       `tier("base", p * 1 + cr * 0.2 + c * 4)`, BillingMultiplier: 0.75,
		AllowedChannels: `[14,15]`, MaxUpstreamCost: `{"input":1,"cached_input":0.2,"output":4}`,
		FallbackAllowed: false, Enabled: true,
	}
	require.NoError(t, model.DB.Create(price).Error)
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenProductType, model.ProductTypePackQwen)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceVersion, price.PriceVersion)
	common.SetContextKey(ctx, constant.ContextKeyTokenAllowedChannels, `{"qwen3.8-flash":[14,15]}`)
	common.SetContextKey(ctx, constant.ContextKeyTokenMaxUpstreamCost, `{"qwen3.8-flash":{"input":1,"cached_input":0.2,"output":4}}`)
	common.SetContextKey(ctx, constant.ContextKeyTokenFallbackAllowed, false)

	policy, err := ResolveChannelSelectionPolicy(ctx, "qwen3.8-flash")
	require.NoError(t, err)
	require.Len(t, policy.AllowedChannelIds, 2)
	require.Contains(t, policy.AllowedChannelIds, 14)
	require.True(t, policy.RequireCost)
	require.Equal(t, 0.2, policy.MaxUpstreamCost.CachedInput)
	require.Equal(t, 0.75, ctx.GetFloat64(string(constant.ContextKeyTokenBillingMultiplier)))

	_, err = ResolveChannelSelectionPolicy(ctx, "qwen3.8-flash-preview")
	require.ErrorContains(t, err, "no compliant product route")

	common.SetContextKey(ctx, constant.ContextKeyTokenMaxUpstreamCost, "")
	_, err = ResolveChannelSelectionPolicy(ctx, "qwen3.8-flash")
	require.ErrorContains(t, err, "not configured")
}

func TestResolveChannelSelectionPolicySnapshotsCurrentWalletPriceVersion(t *testing.T) {
	truncate(t)
	price := &model.ProductPrice{
		ProductType: model.ProductTypeWallet, Model: "wallet-route-model", PriceVersion: "wallet-route-v1",
		OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"output":4}`,
		UserSellingPrice:        `{"input":1,"cached_input":0.2,"output":4}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"output":2}`,
		BillingExpression:       `tier("default", p * 1 + cr * 0.2 + c * 4)`,
		BillingMultiplier:       1,
		AllowedChannels:         `[21,22]`,
		MaxUpstreamCost:         `{"input":0.8,"cached_input":0.15,"output":3}`,
		FallbackAllowed:         true,
		Enabled:                 true,
	}
	require.NoError(t, model.DB.Create(price).Error)

	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)
	policy, err := ResolveChannelSelectionPolicy(ctx, price.Model)
	require.NoError(t, err)
	require.Len(t, policy.AllowedChannelIds, 2)
	require.Contains(t, policy.AllowedChannelIds, 21)
	require.Equal(t, 0.15, policy.MaxUpstreamCost.CachedInput)
	require.Equal(t, price.PriceVersion, common.GetContextKeyString(ctx, constant.ContextKeyTokenPriceVersion))
	require.Equal(t, 1.0, ctx.GetFloat64(string(constant.ContextKeyTokenBillingMultiplier)))
	require.True(t, common.GetContextKeyBool(ctx, constant.ContextKeyTokenFallbackAllowed))

	newPrice := *price
	newPrice.Id, newPrice.PriceVersion, newPrice.EffectiveAt = 0, "wallet-route-v2", model.GetDBTimestamp()
	newPrice.AllowedChannels = `[23]`
	require.NoError(t, model.DB.Create(&newPrice).Error)
	retryPolicy, err := ResolveChannelSelectionPolicy(ctx, price.Model)
	require.NoError(t, err)
	require.Equal(t, policy, retryPolicy, "an in-flight request keeps its original routes")
	require.Equal(t, price.PriceVersion, common.GetContextKeyString(ctx, constant.ContextKeyTokenPriceVersion))
	newCtx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(newCtx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)
	newPolicy, err := ResolveChannelSelectionPolicy(newCtx, price.Model)
	require.NoError(t, err)
	require.Contains(t, newPolicy.AllowedChannelIds, 23, "new requests see the newly published version")
	require.Equal(t, newPrice.PriceVersion, common.GetContextKeyString(newCtx, constant.ContextKeyTokenPriceVersion))
	// Also exercise the migrated wallet book, whose current version may change
	// independently between upstream attempts.
	bookPrice := model.PriceBookPrice{
		PriceBook: model.WalletPriceBook, Model: price.Model, PriceVersion: "migrated-v1", Enabled: true,
		OfficialReferencePrice: price.OfficialReferencePrice, UserSellingPrice: price.UserSellingPrice,
		UpstreamSettlementPrice: price.UpstreamSettlementPrice, BillingExpression: price.BillingExpression,
		BillingMultiplier: 1, AllowedChannels: `[24]`, MaxUpstreamCost: price.MaxUpstreamCost,
	}
	require.NoError(t, model.DB.Create(&bookPrice).Error)
	migratedCtx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(migratedCtx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)
	common.SetContextKey(migratedCtx, constant.ContextKeyTokenPriceBook, model.WalletPriceBook)
	migratedPolicy, err := ResolveChannelSelectionPolicy(migratedCtx, price.Model)
	require.NoError(t, err)
	require.Contains(t, migratedPolicy.AllowedChannelIds, 24)
	bookPrice.Id, bookPrice.PriceVersion, bookPrice.AllowedChannels = 0, "migrated-v2", `[25]`
	bookPrice.EffectiveAt = model.GetDBTimestamp()
	require.NoError(t, model.DB.Create(&bookPrice).Error)
	frozenPolicy, err := ResolveChannelSelectionPolicy(migratedCtx, price.Model)
	require.NoError(t, err)
	require.Equal(t, migratedPolicy, frozenPolicy)
	require.Equal(t, "migrated-v1", common.GetContextKeyString(migratedCtx, constant.ContextKeyTokenPriceVersion))
	freshCtx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(freshCtx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)
	common.SetContextKey(freshCtx, constant.ContextKeyTokenPriceBook, model.WalletPriceBook)
	freshPolicy, err := ResolveChannelSelectionPolicy(freshCtx, price.Model)
	require.NoError(t, err)
	require.Contains(t, freshPolicy.AllowedChannelIds, 25)
	require.NoError(t, model.DB.Model(&newPrice).Update("enabled", false).Error)

	require.NoError(t, model.DB.Model(&model.ProductPrice{}).Where("id = ?", price.Id).UpdateColumn("enabled", false).Error)
	retryPolicy, err = ResolveChannelSelectionPolicy(ctx, price.Model)
	require.NoError(t, err)
	require.Equal(t, policy, retryPolicy, "disabling sales does not reprice an in-flight request")
	disabledCtx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(disabledCtx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)
	_, err = ResolveChannelSelectionPolicy(disabledCtx, price.Model)
	require.ErrorContains(t, err, "disabled or not yet effective")
}

func TestResolveChannelSelectionPolicyUsesProductPriceBookSnapshot(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.PriceBookPrice{}))
	price := &model.PriceBookPrice{
		PriceBook: "product:91", Model: "book-route-model", PriceVersion: "book-route-v1",
		OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"output":4}`,
		UserSellingPrice:        `{"input":1,"cached_input":0.2,"output":4}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"output":2}`,
		BillingExpression:       `tier("base", p + cr * 0.2 + c * 4)`, BillingMultiplier: 0.73,
		AllowedChannels: "[14,15]", MaxUpstreamCost: `{"input":1,"cached_input":0.2,"output":4}`,
		FallbackAllowed: false, Enabled: true,
	}
	require.NoError(t, model.DB.Create(price).Error)

	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenProductType, model.ProductTypePackK3)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceBook, price.PriceBook)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceVersion, price.PriceVersion)
	policy, err := ResolveChannelSelectionPolicy(ctx, price.Model)
	require.NoError(t, err)
	require.Len(t, policy.AllowedChannelIds, 2)
	require.Contains(t, policy.AllowedChannelIds, 14)
	require.True(t, policy.RequireCost)
	require.Equal(t, 0.2, policy.MaxUpstreamCost.CachedInput)

	_, err = ResolveChannelSelectionPolicy(ctx, "other-model")
	require.ErrorContains(t, err, "unavailable")
}

func TestProductChannelRatesNoFallbackRejectsPinnedBackup(t *testing.T) {
	truncate(t)
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	require.NoError(t, model.DB.AutoMigrate(&model.PriceBookPrice{}, &model.APIChannelPrice{}, &model.APIChannelCatalog{}, &model.Channel{}, &model.Ability{}))
	oldMemory := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory })
	require.NoError(t, model.InitializeAPIChannelCatalog(model.DB))
	for _, id := range []int{981, 982} {
		channel := model.Channel{Id: id, Name: "no-fallback upstream", Key: "synthetic-key", Models: "no-fallback-model", Group: "default", Status: common.ChannelStatusEnabled}
		require.NoError(t, model.DB.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(model.DB))
	}
	position0, position1 := 0, 1
	prices := []model.APIChannelPrice{
		{Model: "no-fallback-model", ChannelID: 981, UpstreamModel: "no-fallback-model", SellingPrice: `{"input":1,"cached_input":0,"cache_creation":0,"output":1}`, Enabled: true, Position: &position0},
		{Model: "no-fallback-model", ChannelID: 982, UpstreamModel: "no-fallback-model", SellingPrice: `{"input":1,"cached_input":0,"cache_creation":0,"output":1}`, Enabled: true, Position: &position1},
	}
	_, err := model.PublishAPIChannelPrices(model.DB, 0, prices, 1, "route fixture")
	require.NoError(t, err)
	price := model.PriceBookPrice{
		PriceBook: "product:991", Model: "no-fallback-model", PriceVersion: "no-fallback-v1", RouteMode: model.ProductRouteOrdered,
		OfficialReferencePrice: `{"input":1,"cached_input":0,"cache_creation":0,"output":1}`, UserSellingPrice: `{"input":1,"cached_input":0,"cache_creation":0,"output":1}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0,"cache_creation":0,"output":0.5}`, BillingExpression: `tier("base", p + c)`, BillingMultiplier: 1,
		ChannelRates:    `[{"channel_id":981,"multiplier":1,"position":0},{"channel_id":982,"multiplier":1,"position":1}]`,
		AllowedChannels: `[981,982]`, MaxUpstreamCost: `{"input":1,"cached_input":0,"cache_creation":0,"output":1}`, FallbackAllowed: false, Enabled: true,
	}
	require.NoError(t, model.DB.Create(&price).Error)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenProductType, model.ProductTypePackK3)
	common.SetContextKey(ctx, constant.ContextKeyTokenUserSubscriptionId, 77)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceBook, price.PriceBook)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceVersion, price.PriceVersion)
	policy, err := ResolveChannelSelectionPolicy(ctx, price.Model)
	require.NoError(t, err)
	assert.Equal(t, map[int]struct{}{981: {}}, policy.AllowedChannelIds)
	GetChannelConstraints(ctx).AddPin(dto.ChannelPin{ChannelId: 982, Source: dto.PinSourceToken})
	selected, _, failure := SelectChannelForRequest(ctx, price.Model, &RetryParam{Ctx: ctx, ModelName: price.Model, TokenGroup: "default"})
	require.Nil(t, selected)
	require.NotNil(t, failure)
	assert.Equal(t, "product_route_policy_rejected", failure.Message)
}

func TestOrderedProductRoutingUsesFrozenVersionAndAttemptTrail(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.PriceBookPrice{}, &model.Channel{}, &model.Ability{}))
	oldMemory := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory })
	price := model.PriceBookPrice{
		PriceBook: "product:98", Model: "ordered-product-model", PriceVersion: "ordered-v1", RouteMode: model.ProductRouteOrdered,
		OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
		UserSellingPrice:        `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`,
		BillingExpression:       `tier("base", p + cr * 0.2 + cc + c * 4)`, BillingMultiplier: 0.73,
		AllowedChannels: `[981,982]`, MaxUpstreamCost: `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
		FallbackAllowed: true, Enabled: true,
	}
	require.NoError(t, model.DB.Create(&price).Error)
	for index, id := range []int{981, 982} {
		channel := model.Channel{Id: id, Key: "synthetic-key", Models: price.Model, Group: "supplier-only",
			Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(index * 100)),
			UpstreamCosts: `{"ordered-product-model":{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}}`}
		require.NoError(t, model.DB.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(model.DB))
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceBook, price.PriceBook)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceVersion, price.PriceVersion)
	retry := &RetryParam{Ctx: ctx, ModelName: price.Model, TokenGroup: "default"}
	first, _, failure := SelectChannelForRequest(ctx, price.Model, retry)
	require.Nil(t, failure)
	require.NotNil(t, first)
	assert.Equal(t, 981, first.Id)
	assert.Equal(t, []int{981, 982}, retry.Policy.OrderedChannelIds)
	assert.False(t, ProductFallbackDisabled(ctx))
	assert.Equal(t, 0.73, ctx.GetFloat64(string(constant.ContextKeyTokenBillingMultiplier)))
	// Session preference cannot silently move a new ordered request to a backup.
	// Strict binding instead rejects a conflicting order rather than switching it.
	setting := operation_setting.GetChannelAffinitySetting()
	previous := *setting
	t.Cleanup(func() { *setting = previous })
	rule := operation_setting.ChannelAffinityRule{Name: "ordered-product-session", SessionMode: "inherit", ModelRegex: []string{"^ordered-product-model$"},
		KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}}, IncludeRuleName: true}
	setting.Enabled, setting.Rules = true, []operation_setting.ChannelAffinityRule{rule}
	cache := getChannelAffinityCache()
	for _, mode := range []string{"prefer", "strict"} {
		t.Run("session="+mode, func(t *testing.T) {
			setting.SessionMode = mode
			key := t.Name()
			cacheKey := buildChannelAffinityCacheKeySuffix(rule, price.Model, "default", key)
			require.NoError(t, cache.SetWithTTL(cacheKey, 982, time.Minute))
			t.Cleanup(func() { _, err := cache.DeleteMany([]string{cacheKey}); assert.NoError(t, err) })
			session, _ := gin.CreateTestContext(httptest.NewRecorder())
			session.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			session.Request.Header.Set("X-Session", key)
			common.SetContextKey(session, constant.ContextKeyTokenPriceBook, price.PriceBook)
			common.SetContextKey(session, constant.ContextKeyTokenPriceVersion, price.PriceVersion)
			chosen, _, failure := SelectChannelForRequest(session, price.Model, &RetryParam{Ctx: session, ModelName: price.Model, TokenGroup: "default"})
			if mode == "strict" {
				require.NotNil(t, failure)
				assert.Equal(t, "strict_session_binding_unavailable", failure.Message)
				assert.Nil(t, chosen)
			} else {
				require.Nil(t, failure)
				require.NotNil(t, chosen)
				assert.Equal(t, 981, chosen.Id)
			}
		})
	}
	*setting = previous

	// Publication of a different preference order does not change the request's
	// original version. Removing the failed primary must not skip its backup.
	newPrice := price
	newPrice.Id, newPrice.PriceVersion, newPrice.AllowedChannels = 0, "ordered-v2", `[982,981]`
	newPrice.FallbackAllowed = false
	require.NoError(t, model.DB.Create(&newPrice).Error)
	AppendUsedChannel(ctx, first.Id)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", first.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
	retry.SetRetry(1)
	backup, _, failure := SelectChannelForRequest(ctx, price.Model, retry)
	require.Nil(t, failure)
	require.NotNil(t, backup)
	assert.Equal(t, 982, backup.Id)
	AppendUsedChannel(ctx, backup.Id)
	retry.SetRetry(2)
	_, _, failure = SelectChannelForRequest(ctx, price.Model, retry)
	require.NotNil(t, failure, "exhausted ordered routes must not retry a previous supplier")

	fresh, _ := gin.CreateTestContext(httptest.NewRecorder())
	fresh.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(fresh, constant.ContextKeyTokenPriceBook, newPrice.PriceBook)
	common.SetContextKey(fresh, constant.ContextKeyTokenPriceVersion, newPrice.PriceVersion)
	newPolicy, err := ResolveChannelSelectionPolicy(fresh, price.Model)
	require.NoError(t, err)
	assert.Equal(t, []int{982, 981}, newPolicy.OrderedChannelIds)
	assert.True(t, ProductFallbackDisabled(fresh))
	assert.NotContains(t, newPolicy.AllowedChannelIds, 981, "no-backup also constrains explicit pins")
	GetChannelConstraints(fresh).AddPin(dto.ChannelPin{ChannelId: 981, Source: dto.PinSourceToken})
	_, _, failure = SelectChannelForRequest(fresh, price.Model, &RetryParam{Ctx: fresh, ModelName: price.Model, TokenGroup: "default"})
	require.NotNil(t, failure)
}

func TestResolveChannelSelectionPolicyKeepsLegacyWalletWhenCatalogIsAbsent(t *testing.T) {
	truncate(t)
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceBook, model.WalletPriceBook)
	common.SetContextKey(ctx, constant.ContextKeyTokenPriceVersion, "stale-token-version")
	require.NoError(t, model.DB.AutoMigrate(&model.PriceBookPrice{}))

	policy, err := ResolveChannelSelectionPolicy(ctx, "legacy-wallet-model")
	require.NoError(t, err)
	require.Nil(t, policy)
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyTokenPriceVersion))
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyTokenPriceBook))
	price := model.PriceBookPrice{
		PriceBook: model.WalletPriceBook, Model: "legacy-wallet-model", PriceVersion: "new-wallet-override",
		OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"output":4}`,
		UserSellingPrice:        `{"input":1,"cached_input":0.2,"output":4}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"output":2}`,
		BillingExpression:       `tier("base", p + c * 4)`, BillingMultiplier: 1,
		AllowedChannels: `[27]`, MaxUpstreamCost: `{"input":1,"cached_input":0.2,"output":4}`, Enabled: true,
	}
	require.NoError(t, model.DB.Create(&price).Error)
	policy, err = ResolveChannelSelectionPolicy(ctx, price.Model)
	require.NoError(t, err)
	require.Nil(t, policy, "a retry must not switch from global pricing to a new override")
	newCtx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(newCtx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)
	common.SetContextKey(newCtx, constant.ContextKeyTokenPriceBook, model.WalletPriceBook)
	policy, err = ResolveChannelSelectionPolicy(newCtx, price.Model)
	require.NoError(t, err)
	require.Contains(t, policy.AllowedChannelIds, 27)
}

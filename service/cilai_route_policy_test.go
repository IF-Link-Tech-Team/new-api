package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
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

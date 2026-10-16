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

	require.NoError(t, model.DB.Model(&model.ProductPrice{}).Where("id = ?", price.Id).UpdateColumn("enabled", false).Error)
	disabledCtx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(disabledCtx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)
	_, err = ResolveChannelSelectionPolicy(disabledCtx, price.Model)
	require.ErrorContains(t, err, "disabled or not yet effective")
}

func TestResolveChannelSelectionPolicyKeepsLegacyWalletWhenCatalogIsAbsent(t *testing.T) {
	truncate(t)
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenProductType, model.ProductTypeWallet)

	policy, err := ResolveChannelSelectionPolicy(ctx, "legacy-wallet-model")
	require.NoError(t, err)
	require.Nil(t, policy)
	require.Empty(t, common.GetContextKeyString(ctx, constant.ContextKeyTokenPriceVersion))
}

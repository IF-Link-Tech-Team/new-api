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
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyTokenProductType, model.ProductTypePackQwen)
	common.SetContextKey(ctx, constant.ContextKeyTokenAllowedChannels, `{"qwen3.8-flash":[14,15]}`)
	common.SetContextKey(ctx, constant.ContextKeyTokenMaxUpstreamCost, `{"qwen3.8-flash":{"input":1,"cached_input":0.2,"output":4}}`)
	common.SetContextKey(ctx, constant.ContextKeyTokenFallbackAllowed, false)

	policy, err := ResolveChannelSelectionPolicy(ctx, "qwen3.8-flash")
	require.NoError(t, err)
	require.Len(t, policy.AllowedChannelIds, 2)
	require.Contains(t, policy.AllowedChannelIds, 14)
	require.True(t, policy.RequireCost)
	require.Equal(t, 0.2, policy.MaxUpstreamCost.CachedInput)

	_, err = ResolveChannelSelectionPolicy(ctx, "qwen3.8-flash-preview")
	require.ErrorContains(t, err, "no compliant product route")

	common.SetContextKey(ctx, constant.ContextKeyTokenMaxUpstreamCost, "")
	_, err = ResolveChannelSelectionPolicy(ctx, "qwen3.8-flash")
	require.ErrorContains(t, err, "not configured")
}

package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/require"
)

func TestChannelSatisfiesProductPolicyFailsClosedOnWhitelistAndEveryCostLeg(t *testing.T) {
	policy := &ChannelSelectionPolicy{
		AllowedChannelIds: map[int]struct{}{14: {}},
		MaxUpstreamCost:   PriceComponents{Input: 1, CachedInput: 0.2, Output: 4},
		RequireCost:       true,
	}
	compliant := &Channel{Id: 14, UpstreamCosts: `{"qwen3.8-flash":{"input":0.8,"cached_input":0.1,"output":3.5}}`}
	require.True(t, ChannelSatisfiesPolicy(compliant, "qwen3.8-flash", policy))

	wrongChannel := *compliant
	wrongChannel.Id = 15
	require.False(t, ChannelSatisfiesPolicy(&wrongChannel, "qwen3.8-flash", policy))

	for name, costs := range map[string]string{
		"input over cap":  `{"qwen3.8-flash":{"input":1.01,"cached_input":0.1,"output":3.5}}`,
		"cache over cap":  `{"qwen3.8-flash":{"input":0.8,"cached_input":0.21,"output":3.5}}`,
		"output over cap": `{"qwen3.8-flash":{"input":0.8,"cached_input":0.1,"output":4.01}}`,
		"missing model":   `{"kimi-k3":{"input":0.8,"cached_input":0.1,"output":3.5}}`,
		"invalid ledger":  `{broken`,
	} {
		t.Run(name, func(t *testing.T) {
			channel := &Channel{Id: 14, UpstreamCosts: costs}
			require.False(t, ChannelSatisfiesPolicy(channel, "qwen3.8-flash", policy))
		})
	}
}

func TestProductPolicyFiltersBeforeRetryPriorityWithAndWithoutCache(t *testing.T) {
	truncateTables(t)
	original := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = original; InitChannelCache() })
	for i, priority := range []int64{100, 50, 10} {
		channel := Channel{Id: 940001 + i, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "product-route", Models: "route-model", Group: "default", Priority: &priority}
		require.NoError(t, channel.Insert())
	}
	policy := &ChannelSelectionPolicy{AllowedChannelIds: map[int]struct{}{940002: {}, 940003: {}}}
	for _, cached := range []bool{false, true} {
		common.MemoryCacheEnabled = cached
		InitChannelCache()
		for retry, id := range []int{940002, 940003, 940003} {
			selected, err := GetRandomSatisfiedChannelWithPolicy("default", "route-model", retry, nil, policy)
			require.NoError(t, err)
			require.NotNil(t, selected, "cached=%v retry=%d", cached, retry)
			require.Equal(t, id, selected.Id, "cached=%v retry=%d", cached, retry)
		}
		denied, err := GetRandomSatisfiedChannelWithPolicy("default", "route-model", 0, nil, &ChannelSelectionPolicy{AllowedChannelIds: map[int]struct{}{999999: {}}})
		require.NoError(t, err)
		require.Nil(t, denied)
	}
}

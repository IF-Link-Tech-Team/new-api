package model

import (
	"testing"

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

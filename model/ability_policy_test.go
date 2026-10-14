package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestDatabaseChannelSelectionChoosesHighestPriorityInsidePolicy(t *testing.T) {
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	t.Cleanup(func() {
		_ = DB.Exec("DELETE FROM abilities").Error
		_ = DB.Exec("DELETE FROM channels").Error
	})
	oldMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemoryCache })

	allowedCost := `{"wallet-route-model":{"input":0.5,"cached_input":0.1,"output":2}}`
	require.NoError(t, DB.Create(&Channel{
		Id: 15, Name: "allowed-low-priority", Key: "allowed", Status: common.ChannelStatusEnabled,
		Models: "wallet-route-model", Group: "default", UpstreamCosts: allowedCost,
	}).Error)
	require.NoError(t, DB.Create(&Channel{
		Id: 16, Name: "forbidden-high-priority", Key: "forbidden", Status: common.ChannelStatusEnabled,
		Models: "wallet-route-model", Group: "default", UpstreamCosts: allowedCost,
	}).Error)
	low := int64(0)
	high := int64(100)
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "wallet-route-model", ChannelId: 15, Enabled: true, Priority: &low}).Error)
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "wallet-route-model", ChannelId: 16, Enabled: true, Priority: &high}).Error)

	selected, err := GetChannelWithPolicy("default", "wallet-route-model", 0, "/v1/chat/completions", &ChannelSelectionPolicy{
		AllowedChannelIds: map[int]struct{}{15: {}},
		MaxUpstreamCost:   PriceComponents{Input: 1, CachedInput: 0.2, Output: 4},
		RequireCost:       true,
	})
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 15, selected.Id)
}

func TestProductPolicySelectsReviewedChannelOutsideWalletGroup(t *testing.T) {
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
	t.Cleanup(func() {
		_ = DB.Exec("DELETE FROM abilities").Error
		_ = DB.Exec("DELETE FROM channels").Error
	})
	oldMemoryCache := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemoryCache })

	const modelName = "qwen3.8-flash"
	require.NoError(t, DB.Create(&Channel{
		Id: 14, Name: "reviewed-classroom-qwen", Key: "allowed", Status: common.ChannelStatusEnabled,
		Models: modelName, Group: "classroom-upstream", UpstreamCosts: `{"qwen3.8-flash":{"input":0.64,"cached_input":0.08,"cache_creation":1,"output":2.16}}`,
	}).Error)
	priority := int64(0)
	require.NoError(t, DB.Create(&Ability{Group: "classroom-upstream", Model: modelName, ChannelId: 14, Enabled: true, Priority: &priority}).Error)
	policy := &ChannelSelectionPolicy{
		AllowedChannelIds: map[int]struct{}{14: {}},
		MaxUpstreamCost:   PriceComponents{Input: 0.64, CachedInput: 0.08, CacheCreation: 1, Output: 2.16},
		RequireCost:       true,
	}

	common.MemoryCacheEnabled = false
	selected, err := GetRandomSatisfiedChannelWithPolicy("default", modelName, 0, "/v1/chat/completions", policy)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 14, selected.Id)

	common.MemoryCacheEnabled = true
	InitChannelCache()
	selected, err = GetRandomSatisfiedChannelWithPolicy("default", modelName, 0, "/v1/chat/completions", policy)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 14, selected.Id)

	ordinary, err := GetRandomSatisfiedChannel("default", modelName, 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.Nil(t, ordinary, "ordinary wallet routing must not inherit the classroom channel")
}

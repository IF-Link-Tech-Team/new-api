package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
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

	selected, err := GetChannelWithPolicy("default", "wallet-route-model", 0, []dto.ChannelFilter{{Kind: dto.FilterRequestPath, RequestPath: "/v1/chat/completions"}}, &ChannelSelectionPolicy{
		AllowedChannelIds: map[int]struct{}{15: {}},
		MaxUpstreamCost:   PriceComponents{Input: 1, CachedInput: 0.2, Output: 4},
		RequireCost:       true,
	})
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 15, selected.Id)
}

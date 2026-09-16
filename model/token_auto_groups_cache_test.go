package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenAutoGroupsRoundTripThroughRedisHashCache(t *testing.T) {
	useUserCacheMiniRedis(t)
	token := Token{
		Id:         42,
		UserId:     7,
		Key:        "token-auto-groups-cache-key",
		Name:       "auto-cache",
		Group:      "auto",
		AutoGroups: `["vip","default"]`,
	}

	require.NoError(t, cacheSetTokenForTest(token))
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.AutoGroups, cached.AutoGroups)
	groups, err := cached.GetAutoGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"vip", "default"}, groups)
}

func TestTokenAllowUserQuotaQueryRoundTripsThroughRedisHashCache(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	token := Token{Id: 43, UserId: 7, Key: "token-user-quota-cache-key", Name: "user-quota-cache", AllowUserQuotaQuery: true}

	initialized, err := cacheInitToken(token)
	require.NoError(t, err)
	require.Equal(t, 1, initialized)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.True(t, cached.AllowUserQuotaQuery)
	assert.Positive(t, server.TTL(getTokenCacheKey(token.Key)))
}

func TestTokenUpdateSynchronouslyNarrowsPreheatedAutoGroupsCache(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)
	token := Token{
		UserId:          7,
		Key:             "token-auto-groups-update-cache-key",
		Name:            "auto-cache-update",
		Status:          common.TokenStatusEnabled,
		ExpiredTime:     -1,
		UnlimitedQuota:  true,
		Group:           "auto",
		CrossGroupRetry: true,
		AutoGroups:      `["default","vip"]`,
	}
	require.NoError(t, token.Insert())
	require.NoError(t, cacheSetTokenForTest(token))

	preheated, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.JSONEq(t, `["default","vip"]`, preheated.AutoGroups)

	require.NoError(t, token.SetAutoGroups([]string{"vip"}))
	require.NoError(t, token.Update())
	// Update 是限制性变更：写库前删除缓存并设置 fence。缓存不再提供旧的
	// 宽分组值，下一次读取必须看到收紧后的分组。
	_, cacheErr := cacheGetTokenByKey(token.Key)
	require.Error(t, cacheErr, "the pre-update cache entry must be invalidated")
	reloaded, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.JSONEq(t, `["vip"]`, reloaded.AutoGroups)
}

// cacheSetTokenForTest 以测试身份写入完整 token 缓存（含额度字段），
// 模拟“已水合”的缓存状态。
func cacheSetTokenForTest(token Token) error {
	_, err := cacheInitToken(token)
	return err
}

func TestCommerceTokenCachePreservesEntitlementAndRejectsLegacyEntry(t *testing.T) {
	server := useUserCacheMiniRedis(t)
	token := Token{Id: 44, UserId: 7, Key: "commerce-token-cache", UserSubscriptionId: 12,
		EntitlementType: "traffic_pack", ProductType: "traffic_pack", BillingMultiplier: 0.75,
		PriceVersion: "price-v1", AllowedChannels: `{"model":[1]}`, MaxUpstreamCost: `{"model":{"input":1}}`, FallbackAllowed: true}
	_, err := cacheInitToken(token)
	require.NoError(t, err)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.UserSubscriptionId, cached.UserSubscriptionId)
	assert.Equal(t, token.ProductType, cached.ProductType)
	assert.Equal(t, token.EntitlementType, cached.EntitlementType)
	assert.Equal(t, token.BillingMultiplier, cached.BillingMultiplier)
	assert.Equal(t, token.PriceVersion, cached.PriceVersion)
	assert.Equal(t, token.AllowedChannels, cached.AllowedChannels)
	assert.Equal(t, token.MaxUpstreamCost, cached.MaxUpstreamCost)
	assert.True(t, cached.FallbackAllowed)
	server.HDel(getTokenCacheKey(token.Key), "CommerceCacheVersion")
	_, err = cacheGetTokenByKey(token.Key)
	require.Error(t, err)
	assert.False(t, server.Exists(getTokenCacheKey(token.Key)))
	assert.True(t, server.Exists(getTokenCacheFenceKey(token.Key)))
}

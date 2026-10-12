package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
)

func TestValidateAlipayNotifyIdentity(t *testing.T) {
	originalAppID := setting.AlipayAppID
	originalSellerID := setting.AlipaySellerID
	t.Cleanup(func() {
		setting.AlipayAppID = originalAppID
		setting.AlipaySellerID = originalSellerID
	})
	setting.AlipayAppID = "2021007109644192"
	setting.AlipaySellerID = "2088000000000000"

	params := map[string]string{
		"app_id":       setting.AlipayAppID,
		"auth_app_id":  setting.AlipayAppID,
		"seller_id":    setting.AlipaySellerID,
		"currency":     "CNY",
		"total_amount": "79.00",
	}
	require.NoError(t, validateAlipayNotifyIdentity(params))

	params["seller_id"] = "2088000000000001"
	require.Error(t, validateAlipayNotifyIdentity(params))
}

func TestAlipayNotifyAmountMatchesPersistedOrder(t *testing.T) {
	params := map[string]string{"total_amount": "79.00"}
	require.True(t, alipayNotifyAmountMatches(params, 79))
	params["total_amount"] = "78.99"
	require.False(t, alipayNotifyAmountMatches(params, 79))
	params["total_amount"] = "not-a-number"
	require.False(t, alipayNotifyAmountMatches(params, 79))
}

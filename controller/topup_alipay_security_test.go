package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
)

func TestFrozenV1WalletAmounts(t *testing.T) {
	for _, amount := range []float64{10, 30, 50, 10.0001} {
		require.True(t, isFrozenV1WalletAmount(amount), amount)
	}
	for _, amount := range []float64{0.01, 9.99, 20, 100, -10} {
		require.False(t, isFrozenV1WalletAmount(amount), amount)
	}
}

func TestValidateAlipayNotifyIdentityRequiresConfiguredAppAndSeller(t *testing.T) {
	oldAppID, oldSellerID := setting.AlipayAppID, setting.AlipaySellerID
	setting.AlipayAppID = "app-expected"
	setting.AlipaySellerID = "seller-expected"
	t.Cleanup(func() {
		setting.AlipayAppID = oldAppID
		setting.AlipaySellerID = oldSellerID
	})

	valid := map[string]string{
		"app_id": "app-expected", "auth_app_id": "app-expected", "seller_id": "seller-expected",
	}
	require.NoError(t, validateAlipayNotifyIdentity(valid))

	for name, mutate := range map[string]func(map[string]string){
		"missing app":    func(p map[string]string) { delete(p, "app_id"); delete(p, "auth_app_id") },
		"wrong app":      func(p map[string]string) { p["app_id"] = "attacker-app" },
		"wrong auth app": func(p map[string]string) { p["auth_app_id"] = "attacker-app" },
		"wrong seller":   func(p map[string]string) { p["seller_id"] = "attacker-seller" },
	} {
		t.Run(name, func(t *testing.T) {
			params := map[string]string{}
			for key, value := range valid {
				params[key] = value
			}
			mutate(params)
			require.Error(t, validateAlipayNotifyIdentity(params))
		})
	}
}

func TestAlipayPaymentReceiptRequiresExactCNYAndProviderTradeNo(t *testing.T) {
	receipt, err := alipayPaymentReceipt(map[string]string{
		"total_amount": "49.90", "currency": "CNY", "trade_no": "202609160001",
	}, "signed-payload")
	require.NoError(t, err)
	require.Equal(t, "49.9", receipt.Amount.String())
	require.Equal(t, "CNY", receipt.Currency)
	require.Equal(t, "202609160001", receipt.ProviderTradeNo)

	for _, params := range []map[string]string{
		{"total_amount": "0", "trade_no": "x"},
		{"total_amount": "not-a-number", "trade_no": "x"},
		{"total_amount": "49.9", "currency": "USD", "trade_no": "x"},
		{"total_amount": "49.9", "currency": "CNY"},
	} {
		_, err := alipayPaymentReceipt(params, "")
		require.Error(t, err)
	}
}

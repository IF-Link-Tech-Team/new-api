package model

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopUpCallbackAndManualCompletionUseTheSameQuotaUnits(t *testing.T) {
	previousUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousUnit })
	for _, tc := range []struct {
		name, provider string
		amount         int64
		money          float64
		want           int
	}{
		{"epay discounted display amount", PaymentProviderEpay, 2, 1.8, 1_000_000},
		{"alipay fixed SKU", PaymentProviderAlipay, 10, 10, 5_000_000},
		{"iflinkpay one cent", PaymentProviderIFLinkPay, 0, 0.01, 5_000},
		{"iflinkpay rounded down display", PaymentProviderIFLinkPay, 1, 1.49, 745_000},
		{"iflinkpay rounded up display", PaymentProviderIFLinkPay, 2, 1.5, 750_000},
		{"stripe stored monetary units", PaymentProviderStripe, 99, 2.5, 1_250_000},
		{"creem raw quota", PaymentProviderCreem, 12_345, 9.99, 12_345},
		{"waffo display units", PaymentProviderWaffo, 2, 9.99, 1_000_000},
		{"pancake display units", PaymentProviderWaffoPancake, 2, 9.99, 1_000_000},
	} {
		for _, mode := range []string{"callback", "manual", "legacy completion"} {
			if mode == "legacy completion" && tc.provider != PaymentProviderStripe && tc.provider != PaymentProviderCreem && tc.provider != PaymentProviderWaffo && tc.provider != PaymentProviderWaffoPancake {
				continue
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				truncateTables(t)
				insertUserForPaymentGuardTest(t, 101, 100)
				order := &TopUp{UserId: 101, Amount: tc.amount, Money: tc.money, Currency: "CNY",
					TradeNo: "quota-units", PaymentMethod: tc.provider, PaymentProvider: tc.provider, Status: common.TopUpStatusPending}
				require.NoError(t, order.Insert())
				var err error
				if mode == "manual" {
					err = ManualCompleteTopUp(order.TradeNo, "127.0.0.1")
				} else if mode == "callback" {
					var quota int
					_, quota, _, err = CompleteTopUpPayment(order.TradeNo, tc.provider, PaymentReceipt{
						Amount: decimal.NewFromFloat(tc.money), Currency: "CNY", ProviderTradeNo: "test-receipt",
					})
					assert.Equal(t, tc.want, quota)
				} else {
					switch tc.provider {
					case PaymentProviderStripe:
						err = Recharge(order.TradeNo, "customer-test", "127.0.0.1")
					case PaymentProviderCreem:
						err = RechargeCreem(order.TradeNo, "", "", "127.0.0.1")
					case PaymentProviderWaffo:
						err = RechargeWaffo(order.TradeNo, "127.0.0.1")
					case PaymentProviderWaffoPancake:
						err = RechargeWaffoPancake(order.TradeNo)
					}
				}
				require.NoError(t, err)
				assert.Equal(t, 100+tc.want, getUserQuotaForPaymentGuardTest(t, 101))
				assert.Equal(t, common.TopUpStatusSuccess, GetTopUpByTradeNo(order.TradeNo).Status)
				// A manual replay after either completion must never credit again.
				require.NoError(t, ManualCompleteTopUp(order.TradeNo, "127.0.0.1"))
				assert.Equal(t, 100+tc.want, getUserQuotaForPaymentGuardTest(t, 101))
			})
		}
	}
}

func TestTopUpOverflowRollsBackOrderAndWallet(t *testing.T) {
	for _, provider := range []string{PaymentProviderAlipay, PaymentProviderStripe, PaymentProviderCreem, PaymentProviderWaffo, PaymentProviderWaffoPancake} {
		for _, boundary := range []struct {
			name   string
			amount int64
			money  float64
		}{{"quota column overflow", int64(common.MaxQuota) + 1, float64(common.MaxQuota) + 1}, {"integer conversion overflow", math.MaxInt64, 1e30}} {
			t.Run(provider+"/"+boundary.name, func(t *testing.T) {
				truncateTables(t)
				insertUserForPaymentGuardTest(t, 101, 100)
				order := &TopUp{UserId: 101, Amount: boundary.amount, Money: boundary.money, Currency: "CNY",
					TradeNo: "overflow", PaymentMethod: provider, PaymentProvider: provider, Status: common.TopUpStatusPending}
				require.NoError(t, order.Insert())
				require.Error(t, ManualCompleteTopUp(order.TradeNo, "127.0.0.1"))
				assert.Equal(t, 100, getUserQuotaForPaymentGuardTest(t, 101))
				assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
				_, _, _, err := CompleteTopUpPayment(order.TradeNo, provider, PaymentReceipt{
					Amount: decimal.NewFromFloat(order.Money), Currency: "CNY", ProviderTradeNo: "overflow-test",
				})
				require.Error(t, err)
				assert.Equal(t, 100, getUserQuotaForPaymentGuardTest(t, 101))
				assert.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(order.TradeNo).Status)
			})
		}
	}
}

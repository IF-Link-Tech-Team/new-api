package controller

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIFLinkPayBigfishVerifiedPaymentPreservesRetryAndPurchaseLimit(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	require.NoError(t, model.SeedProductTemplates())
	data, err := os.ReadFile("../model/testdata/bigfish-draft.json")
	require.NoError(t, err)
	var catalog struct {
		Prices []struct {
			Price model.ProductPrice `json:"price"`
		} `json:"price_requests"`
		Plans []struct {
			Plan model.SubscriptionPlan `json:"plan"`
		} `json:"plan_requests"`
	}
	require.NoError(t, common.Unmarshal(data, &catalog))
	for _, item := range catalog.Prices {
		item.Price.Enabled = true
		require.NoError(t, model.DB.Create(&item.Price).Error)
	}
	for index := range catalog.Plans {
		plan := &catalog.Plans[index].Plan
		require.NoError(t, model.DB.Create(plan).Error)
		assert.False(t, plan.Enabled)
		assert.False(t, plan.Listed)
		assert.False(t, plan.RedemptionEnabled)
		require.NoError(t, model.DB.Model(plan).Update("enabled", true).Error)
		model.InvalidateSubscriptionPlanCache(plan.Id)
		planID := plan.Id
		t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(planID) })
	}

	first, _, err := model.CreateSubscriptionOrder(user.Id, catalog.Plans[0].Plan.Id,
		"SUB-IFP-bigfish-original", model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true)
	require.NoError(t, err)
	repeated, _, err := model.CreateSubscriptionOrder(user.Id, catalog.Plans[0].Plan.Id,
		"SUB-IFP-bigfish-retry", model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true)
	require.NoError(t, err)
	assert.Equal(t, first.Id, repeated.Id)
	assert.Equal(t, first.TradeNo, repeated.TradeNo)
	assert.Equal(t, 9.9, first.Money)
	_, _, err = model.CreateSubscriptionOrder(user.Id, catalog.Plans[1].Plan.Id,
		"SUB-IFP-bigfish-other-tier", model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true)
	require.ErrorContains(t, err, "待支付")

	// This is a trusted, independently verified payment fixture. It deliberately
	// bypasses the disabled public MD5 callback rather than treating a browser
	// redirect signature as evidence that money was paid.
	receipt := model.PaymentReceipt{
		Amount: decimal.NewFromFloat(first.Money), Currency: "CNY",
		ProviderTradeNo: "verified-bigfish-payment", ProviderPayload: `{"verification":"isolated-test"}`,
	}
	for range 2 {
		require.NoError(t, model.CompleteSubscriptionOrderWithReceipt(first.TradeNo,
			model.PaymentProviderIFLinkPay, model.PaymentMethodIFLinkPay, receipt))
	}
	completed := model.GetSubscriptionOrderByTradeNo(first.TradeNo)
	require.NotNil(t, completed)
	assert.Equal(t, common.TopUpStatusSuccess, completed.Status)
	assert.Equal(t, model.ProvisioningStatusProvisioned, completed.ProvisioningStatus)
	assert.Equal(t, 9.9, completed.PaidMoney)
	require.NotNil(t, completed.UserSubscriptionId)
	var subscription model.UserSubscription
	require.NoError(t, model.DB.First(&subscription, *completed.UserSubscriptionId).Error)
	assert.Equal(t, model.ProductTypePackDeepSeekBigfish, subscription.ProductType)
	assert.Equal(t, catalog.Plans[0].Plan.TotalAmount, subscription.AmountTotal)
	assert.EqualValues(t, 15*24*60*60, subscription.EndTime-subscription.StartTime)
	for _, table := range []any{&model.SubscriptionOrder{}, &model.UserSubscription{}, &model.Token{}, &model.SubscriptionKeyBinding{}} {
		var count int64
		require.NoError(t, model.DB.Model(table).Where("user_id = ?", user.Id).Count(&count).Error)
		assert.EqualValues(t, 1, count, "verified payment replay must not duplicate an order, entitlement, or key")
	}
	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	assert.Equal(t, user.Quota, after.Quota)
	_, _, err = model.CreateSubscriptionOrder(user.Id, catalog.Plans[1].Plan.Id,
		"SUB-IFP-bigfish-second-purchase", model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true)
	require.Error(t, err, "fulfillment must retain the one-purchase-per-activity constraint")
}

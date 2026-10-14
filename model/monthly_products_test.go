package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func setupMonthlyProductsTest(t *testing.T) *MonthlyBootstrapResult {
	t.Helper()
	truncateTables(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	require.NoError(t, DB.Create(&Channel{
		Id: 15, Name: "DeepSeek audited", Type: 14, Key: "test-deepseek", Status: common.ChannelStatusEnabled,
		Models: MonthlyModelDeepSeekV41 + "," + MonthlyModelDeepSeek, Group: "default,词来",
		UpstreamCosts: `{"deepseek-v4-1-flash-260910":{"input":1,"cached_input":0.02,"output":4},"deepseek-v4-flash-ga-260731":{"input":1.5,"cached_input":0.05,"output":4.5}}`,
	}).Error)
	require.NoError(t, DB.Create(&Channel{
		Id: 14, Name: "Unified audited", Type: 1, Key: "test-unified", Status: common.ChannelStatusEnabled,
		Models: MonthlyModelGLM52 + "," + MonthlyModelKimiK3 + "," + MonthlyModelQwen, Group: "词来,default",
		UpstreamCosts: `{"glm-5.2":{"input":4.4,"cached_input":1.1,"output":15.4},"kimi-k3":{"input":16,"cached_input":1.6,"output":80},"qwen3.8-flash":{"input":0.64,"cached_input":0.08,"cache_creation":1,"output":2.16}}`,
	}).Error)

	result, err := BootstrapMonthlyProducts(true, true, true, 0)
	require.NoError(t, err)
	require.Len(t, result.Plans, 3)
	return result
}

func TestMonthlyBootstrapCreatesUnifiedTiersAndMixedPriceSnapshots(t *testing.T) {
	result := setupMonthlyProductsTest(t)
	require.Equal(t, MonthlyPriceVersion, result.PriceVersion)
	require.Equal(t, []string{MonthlyModelQwen, MonthlyModelDeepSeekV41, MonthlyModelDeepSeek, MonthlyModelGLM52, MonthlyModelKimiK3}, result.Models)

	wantPlans := map[string]struct {
		price  float64
		quota  int64
		tier   int
		points int64
	}{
		"lite": {price: 19.9, quota: 9_950_000, tier: 1, points: 39_800},
		"pro":  {price: 49.9, quota: 24_950_000, tier: 2, points: 99_800},
		"max":  {price: 99.9, quota: 49_950_000, tier: 3, points: 199_800},
	}
	for code, want := range wantPlans {
		var plan SubscriptionPlan
		require.NoError(t, DB.Where("product_code = ?", code).First(&plan).Error)
		require.Equal(t, want.price, plan.PriceAmount)
		require.Equal(t, want.quota, plan.TotalAmount)
		require.Equal(t, want.points, plan.TotalAmount/250)
		require.Equal(t, want.tier, plan.ProductTier)
		require.Equal(t, 30, plan.DurationValue)
		require.Equal(t, SubscriptionResetNever, plan.QuotaResetPeriod)
		require.Equal(t, MonthlyPriceVersion, plan.PriceVersion)
		require.Equal(t, 1.0, plan.BillingMultiplier)
		require.False(t, plan.CourseEligible)
		require.False(t, *plan.AllowWalletOverflow)
		require.False(t, *plan.AllowBalancePay)
		require.ElementsMatch(t, result.Models, explicitModelIds(plan.ModelScope))
		require.NoError(t, plan.ValidateV1Contract())
	}

	wantQuota := map[string]int{
		MonthlyModelDeepSeek: 4_235_000,
		MonthlyModelGLM52:    13_300_000,
		MonthlyModelKimiK3:   57_950_000,
		MonthlyModelQwen:     2_425_000,
	}
	var timedDeepSeek ProductPrice
	require.NoError(t, DB.Where("product_type = ? AND model = ? AND price_version = ?", ProductTypeMonthlySubscription, MonthlyModelDeepSeekV41, MonthlyPriceVersion).First(&timedDeepSeek).Error)
	require.Equal(t, 0.70, timedDeepSeek.BillingMultiplier)
	require.Contains(t, timedDeepSeek.BillingExpression, `weekday("Asia/Shanghai")`)
	params := billingexpr.TokenParams{P: 1_000_000, CR: 1_000_000, CC: 1_000_000, C: 1_000_000, Len: 2_000_000}
	for modelName, expectedQuota := range wantQuota {
		var price ProductPrice
		require.NoError(t, DB.Where("product_type = ? AND model = ? AND price_version = ?", ProductTypeMonthlySubscription, modelName, MonthlyPriceVersion).First(&price).Error)
		expectedMultiplier, ok := monthlyModelBillingMultiplier(modelName)
		require.True(t, ok)
		require.InDelta(t, expectedMultiplier, price.BillingMultiplier, 0.000001)
		computed, err := billingexpr.ComputeTieredQuota(&billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr", ModelName: modelName,
			ExprString: price.BillingExpression, ExprHash: billingexpr.ExprHashString(price.BillingExpression),
			GroupRatio: 1, QuotaPerUnit: common.QuotaPerUnit,
			ProductType: price.ProductType, ProductMultiplier: price.BillingMultiplier, PriceVersion: price.PriceVersion,
		}, params)
		require.NoError(t, err)
		// The official expression receives exactly one model-level multiplier.
		require.Equal(t, expectedQuota, computed.ActualQuotaAfterGroup)
	}

	legacyMultiplier, ok := ExpectedProductPriceBillingMultiplier(ProductTypeMonthlySubscription, MonthlyModelDeepSeek, "legacy-monthly-v1")
	require.True(t, ok)
	require.Equal(t, 0.85, legacyMultiplier)
}

func TestMonthlyIFLinkPayOrderProvisionsOneBoundKeyIdempotently(t *testing.T) {
	setupMonthlyProductsTest(t)
	var lite SubscriptionPlan
	require.NoError(t, DB.Where("product_code = ?", "lite").First(&lite).Error)
	user := createV1ContractUser(t, "monthly-iflinkpay-user")

	order, _, err := CreateSubscriptionOrder(user.Id, lite.Id, "SUB-MONTHLY-IFLINKPAY-TEST", PaymentMethodIFLinkPay, PaymentProviderIFLinkPay, true)
	require.NoError(t, err)
	require.NoError(t, CompleteSubscriptionOrderWithReceipt(order.TradeNo, PaymentProviderIFLinkPay, PaymentMethodIFLinkPay, PaymentReceipt{
		Amount: decimal.RequireFromString("19.90"), Currency: "CNY", ProviderTradeNo: "iflinkpay-monthly-test-1",
	}))
	require.NoError(t, CompleteSubscriptionOrderWithReceipt(order.TradeNo, PaymentProviderIFLinkPay, PaymentMethodIFLinkPay, PaymentReceipt{
		Amount: decimal.RequireFromString("19.90"), Currency: "CNY", ProviderTradeNo: "iflinkpay-monthly-test-1",
	}))

	completed := GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.Equal(t, common.TopUpStatusSuccess, completed.Status)
	require.Equal(t, ProvisioningStatusProvisioned, completed.ProvisioningStatus)
	require.NotNil(t, completed.UserSubscriptionId)
	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, *completed.UserSubscriptionId).Error)
	require.Equal(t, int64(9_950_000), subscription.AmountTotal)
	require.Zero(t, subscription.AmountUsed)
	require.Equal(t, MonthlyPriceVersion, subscription.PriceVersion)
	require.Equal(t, lite.ModelScope, subscription.ModelScope)
	require.WithinDuration(t, time.Unix(subscription.StartTime, 0).Add(30*24*time.Hour), time.Unix(subscription.EndTime, 0), time.Second)

	var bindings []SubscriptionKeyBinding
	require.NoError(t, DB.Where("user_subscription_id = ?", subscription.Id).Find(&bindings).Error)
	require.Len(t, bindings, 1)
	var token Token
	require.NoError(t, DB.First(&token, bindings[0].TokenId).Error)
	require.Equal(t, subscription.Id, token.UserSubscriptionId)
	require.Equal(t, lite.ModelScope, token.ModelLimits)
	require.Equal(t, MonthlyPriceVersion, token.PriceVersion)
	require.NotEmpty(t, token.Key)

	var subscriptionCount, bindingCount, courseCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	require.NoError(t, DB.Model(&SubscriptionKeyBinding{}).Where("user_id = ?", user.Id).Count(&bindingCount).Error)
	require.NoError(t, DB.Model(&CourseEntitlement{}).Where("user_id = ?", user.Id).Count(&courseCount).Error)
	require.Equal(t, int64(1), subscriptionCount)
	require.Equal(t, int64(1), bindingCount)
	require.Zero(t, courseCount)
}

func TestMonthlyBootstrapFailsClosedOnUnverifiedChannelCost(t *testing.T) {
	setupMonthlyProductsTest(t)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 14).Update("upstream_costs", `{"glm-5.2":{"input":4.4,"cached_input":1.1,"output":99}}`).Error)
	_, err := BootstrapMonthlyProducts(false, true, true, 0)
	require.ErrorContains(t, err, "differs from the audited snapshot")
}

func TestMonthlyBootstrapFailsClosedOnUnverifiedModelMapping(t *testing.T) {
	setupMonthlyProductsTest(t)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 14).Update("model_mapping", `{"kimi-k3":"tokenhub-kimi-k3"}`).Error)
	_, err := BootstrapMonthlyProducts(false, true, true, 0)
	require.ErrorContains(t, err, "unaudited upstream model")
}

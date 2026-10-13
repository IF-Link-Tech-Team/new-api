package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func v1MonthlyPlanFixture(t *testing.T, code string, tier int, price float64) *SubscriptionPlan {
	t.Helper()
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	courseEligible := tier >= 2
	plan := &SubscriptionPlan{
		Title:               code,
		PriceAmount:         price,
		Currency:            "CNY",
		DurationUnit:        SubscriptionDurationDay,
		DurationValue:       30,
		Enabled:             true,
		AllowBalancePay:     common.GetPointer(true),
		AllowWalletOverflow: common.GetPointer(false),
		TotalAmount:         int64(price * common.QuotaPerUnit),
		QuotaResetPeriod:    SubscriptionResetNever,
		ModelScope:          "kimi-k3,qwen3.8-flash",
		Category:            SubscriptionCategorySubscription,
		ProductType:         ProductTypeMonthlySubscription,
		ProductCode:         code,
		ProductFamily:       "monthly-v1",
		ProductTier:         tier,
		BillingMultiplier:   0.85,
		PriceVersion:        "v1-test",
		AllowedChannels:     `{"kimi-k3":[14],"qwen3.8-flash":[14]}`,
		MaxUpstreamCost:     `{"kimi-k3":{"input":14,"cached_input":1,"output":70},"qwen3.8-flash":{"input":0.48,"cached_input":0.1,"output":1.62}}`,
		FallbackAllowed:     false,
		CourseEligible:      courseEligible,
	}
	require.NoError(t, plan.ValidateV1Contract())
	policy, err := ParseProductRoutePolicy(plan.AllowedChannels, plan.MaxUpstreamCost, plan.FallbackAllowed)
	require.NoError(t, err)
	for _, modelName := range explicitModelIds(plan.ModelScope) {
		maxCost := policy.MaxUpstreamCost[modelName]
		price := &ProductPrice{
			ProductType: plan.ProductType, Model: modelName, PriceVersion: plan.PriceVersion,
			OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"output":4}`,
			UserSellingPrice:        `{"input":1,"cached_input":0.2,"output":4}`,
			UpstreamSettlementPrice: common.GetJsonString(maxCost),
			BillingExpression:       `tier("default", p * 1 + cr * 0.2 + c * 4)`,
			BillingMultiplier:       plan.BillingMultiplier,
			AllowedChannels:         `[14]`,
			MaxUpstreamCost:         common.GetJsonString(maxCost),
			FallbackAllowed:         false,
			Enabled:                 true,
		}
		require.NoError(t, DB.Where("product_type = ? AND model = ? AND price_version = ?", price.ProductType, price.Model, price.PriceVersion).
			FirstOrCreate(price).Error)
	}
	return plan
}

func v1PackK3PlanFixture(t *testing.T, code string, price float64) *SubscriptionPlan {
	t.Helper()
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	plan := &SubscriptionPlan{
		Title: code, PriceAmount: price, Currency: "CNY",
		DurationUnit: SubscriptionDurationDay, DurationValue: 30, Enabled: true,
		AllowBalancePay: common.GetPointer(true), AllowWalletOverflow: common.GetPointer(false),
		TotalAmount: int64(price * common.QuotaPerUnit), QuotaResetPeriod: SubscriptionResetNever,
		ModelScope: "kimi-k3", Category: SubscriptionCategoryPackage,
		ProductType: ProductTypePackK3, ProductCode: code, ProductFamily: "pack-k3-v1",
		BillingMultiplier: 0.95, PriceVersion: "v1-k3-test",
		AllowedChannels: `{"kimi-k3":[14]}`,
		MaxUpstreamCost: `{"kimi-k3":{"input":14,"cached_input":1,"output":70}}`,
		FallbackAllowed: false,
	}
	require.NoError(t, plan.ValidateV1Contract())
	priceSnapshot := &ProductPrice{
		ProductType: plan.ProductType, Model: "kimi-k3", PriceVersion: plan.PriceVersion,
		OfficialReferencePrice:  `{"input":20,"cached_input":2,"output":100}`,
		UserSellingPrice:        `{"input":19,"cached_input":1.9,"output":95}`,
		UpstreamSettlementPrice: `{"input":14,"cached_input":1,"output":70}`,
		BillingExpression:       `tier("default", p * 20 + cr * 2 + c * 100)`,
		BillingMultiplier:       plan.BillingMultiplier, AllowedChannels: `[14]`,
		MaxUpstreamCost: `{"input":14,"cached_input":1,"output":70}`,
		FallbackAllowed: false, Enabled: true,
	}
	require.NoError(t, DB.Where("product_type = ? AND model = ? AND price_version = ?", priceSnapshot.ProductType, priceSnapshot.Model, priceSnapshot.PriceVersion).
		FirstOrCreate(priceSnapshot).Error)
	return plan
}

func createV1ContractUser(t *testing.T, username string) *User {
	t.Helper()
	user := &User{Username: username, Status: common.UserStatusEnabled, Group: "default", AffCode: username}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func TestValidateV1ContractRejectsFrozenRuleDrift(t *testing.T) {
	plan := v1MonthlyPlanFixture(t, "monthly-49", 2, 49.9)

	broken := *plan
	broken.ProductType = ProductTypeWallet
	require.ErrorContains(t, broken.ValidateV1Contract(), "unsupported product_type")

	broken = *plan
	broken.BillingMultiplier = 0.8
	require.ErrorContains(t, broken.ValidateV1Contract(), "must be 0.85")

	broken = *plan
	broken.DurationUnit = SubscriptionDurationMonth
	require.ErrorContains(t, broken.ValidateV1Contract(), "fixed 30-day")

	broken = *plan
	broken.ModelScope = ""
	require.ErrorContains(t, broken.ValidateV1Contract(), "explicit allowed_models")

	broken = *plan
	broken.AllowedChannels = `{"kimi-k3":[14]}`
	require.ErrorContains(t, broken.ValidateV1Contract(), "qwen3.8-flash")

	broken = *plan
	broken.CourseEligible = false
	require.ErrorContains(t, broken.ValidateV1Contract(), "course eligibility")

	broken = *plan
	broken.ModelScope = "qwen*"
	require.ErrorContains(t, broken.ValidateV1Contract(), "exact model IDs")

	broken = *plan
	broken.FallbackAllowed = true
	require.ErrorContains(t, broken.ValidateV1Contract(), "fail closed")
}

func TestWalletProductPriceResolvesCurrentImmutableVersion(t *testing.T) {
	truncateTables(t)
	now := GetDBTimestamp()
	base := ProductPrice{
		ProductType: ProductTypeWallet, Model: "wallet-versioned-model",
		OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"output":4}`,
		UserSellingPrice:        `{"input":1,"cached_input":0.2,"output":4}`,
		UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"output":2}`,
		BillingExpression:       `tier("default", p * 1 + cr * 0.2 + c * 4)`,
		BillingMultiplier:       1,
		AllowedChannels:         `[14,15]`,
		MaxUpstreamCost:         `{"input":0.8,"cached_input":0.15,"output":3}`,
		FallbackAllowed:         true,
		Enabled:                 true,
	}
	v1 := base
	v1.PriceVersion = "wallet-v1"
	v1.EffectiveAt = now - 10
	require.NoError(t, DB.Create(&v1).Error)
	v2 := base
	v2.PriceVersion = "wallet-v2"
	v2.EffectiveAt = now
	require.NoError(t, DB.Create(&v2).Error)
	future := base
	future.PriceVersion = "wallet-future"
	future.EffectiveAt = now + 3600
	require.NoError(t, DB.Create(&future).Error)

	current, err := GetCurrentProductPriceSnapshot(ProductTypeWallet, base.Model)
	require.NoError(t, err)
	require.Equal(t, "wallet-v2", current.PriceVersion)
	require.True(t, current.FallbackAllowed)

	current.BillingExpression = `tier("tampered", p * 2 + c * 8)`
	require.ErrorContains(t, DB.Save(current).Error, "immutable")
}

func TestMigrateCilaiV1ContractsBackfillsWalletAndActiveEntitlementBindings(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-migration-user")
	walletKey := &Token{
		UserId: user.Id, Key: "migration-wallet", Name: "wallet", Status: common.TokenStatusEnabled,
		CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(), ExpiredTime: -1,
	}
	require.NoError(t, DB.Create(walletKey).Error)
	plan := v1PackK3PlanFixture(t, "k3-migration", 49.9)
	require.NoError(t, DB.Create(plan).Error)
	sub := &UserSubscription{
		UserId: user.Id, PlanId: plan.Id, AmountTotal: plan.TotalAmount,
		StartTime: common.GetTimestamp() - 60, EndTime: common.GetTimestamp() + 3600, Status: "active",
		ProductType: plan.ProductType, ProductCode: plan.ProductCode, ProductFamily: plan.ProductFamily,
		BillingMultiplier: plan.BillingMultiplier, PriceVersion: plan.PriceVersion,
		ModelScope: plan.ModelScope, AllowedChannels: plan.AllowedChannels, MaxUpstreamCost: plan.MaxUpstreamCost,
		PriceAmountSnapshot: plan.PriceAmount, CurrencySnapshot: plan.Currency,
	}
	require.NoError(t, DB.Create(sub).Error)

	require.NoError(t, MigrateCilaiV1Contracts())
	var walletAfter Token
	require.NoError(t, DB.First(&walletAfter, walletKey.Id).Error)
	require.Equal(t, ProductTypeWallet, walletAfter.ProductType)
	require.Equal(t, 1.0, walletAfter.BillingMultiplier)

	var binding SubscriptionKeyBinding
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).First(&binding).Error)
	var entitlementKey Token
	require.NoError(t, DB.First(&entitlementKey, binding.TokenId).Error)
	require.Equal(t, sub.Id, entitlementKey.UserSubscriptionId)
	require.Equal(t, "kimi-k3", entitlementKey.ModelLimits)
	require.Equal(t, sub.EndTime, entitlementKey.ExpiredTime)
}

func TestProductPriceVersionsAreImmutableAndRouteSnapshotsMustMatch(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-price-contract-user")
	plan := v1MonthlyPlanFixture(t, "monthly-19", 1, 19.9)
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	price, err := GetProductPriceSnapshot(plan.ProductType, "qwen3.8-flash", plan.PriceVersion)
	require.NoError(t, err)
	price.BillingExpression = `tier("tampered", p * 99 + c * 99)`
	require.ErrorContains(t, DB.Save(price).Error, "immutable")

	require.NoError(t, DB.Model(&ProductPrice{}).
		Where("product_type = ? AND model = ? AND price_version = ?", plan.ProductType, "qwen3.8-flash", plan.PriceVersion).
		UpdateColumn("allowed_channels", `[99]`).Error)
	_, _, err = CreateSubscriptionOrder(user.Id, plan.Id, "v1-route-snapshot-mismatch", PaymentMethodAlipay, PaymentProviderAlipay, true)
	require.ErrorContains(t, err, "allowed channel snapshot mismatch")

	require.NoError(t, DB.Model(&ProductPrice{}).
		Where("product_type = ? AND model = ? AND price_version = ?", plan.ProductType, "qwen3.8-flash", plan.PriceVersion).
		UpdateColumns(map[string]interface{}{"allowed_channels": `[14]`, "enabled": false}).Error)
	_, _, err = CreateSubscriptionOrder(user.Id, plan.Id, "v1-disabled-price", PaymentMethodAlipay, PaymentProviderAlipay, true)
	require.ErrorContains(t, err, "product price for model qwen3.8-flash is disabled")
	_, err = GetProductPriceSnapshot(plan.ProductType, "qwen3.8-flash", plan.PriceVersion)
	require.NoError(t, err, "disabled sale switch must not erase a paid entitlement's historical price snapshot")
}

func TestCompleteSubscriptionOrderAtomicallyLinksEntitlementAndUniqueKey(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-order-user")
	plan := v1MonthlyPlanFixture(t, "monthly-49", 2, 49.9)
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	order := &SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         "v1-order-atomic",
		PaymentMethod:   PaymentProviderAlipay,
		PaymentProvider: PaymentProviderAlipay,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())
	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, `{"verified":true}`, PaymentProviderAlipay, "alipay"))

	completed := GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, completed)
	require.NotNil(t, completed.UserSubscriptionId)
	assert.Equal(t, ProvisioningStatusProvisioned, completed.ProvisioningStatus)
	assert.Equal(t, "monthly-49", completed.ProductCode)

	var subscription UserSubscription
	require.NoError(t, DB.First(&subscription, *completed.UserSubscriptionId).Error)
	assert.Equal(t, ProductTypeMonthlySubscription, subscription.ProductType)
	assert.Equal(t, 0.85, subscription.BillingMultiplier)
	assert.Equal(t, 2, subscription.ProductTier)
	assert.NotEmpty(t, subscription.PeriodId)

	var binding SubscriptionKeyBinding
	require.NoError(t, DB.Where("user_subscription_id = ?", subscription.Id).First(&binding).Error)
	var token Token
	require.NoError(t, DB.First(&token, binding.TokenId).Error)
	assert.Equal(t, subscription.Id, token.UserSubscriptionId)
	assert.Equal(t, ProductTypeMonthlySubscription, token.EntitlementType)
	assert.Equal(t, 0.85, token.BillingMultiplier)
	assert.Equal(t, subscription.ModelScope, token.ModelLimits)

	var courseCount int64
	require.NoError(t, DB.Model(&CourseEntitlement{}).Where("user_id = ?", user.Id).Count(&courseCount).Error)
	assert.EqualValues(t, 1, courseCount)

	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, `{"duplicate":true}`, PaymentProviderAlipay, "alipay"))
	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	assert.EqualValues(t, 1, subscriptionCount)
	var tokenCount int64
	require.NoError(t, DB.Model(&Token{}).Where("user_subscription_id = ?", subscription.Id).Count(&tokenCount).Error)
	assert.EqualValues(t, 1, tokenCount)

	rotatedKey, err := RotateBoundTokenKey(token.Id, user.Id)
	require.NoError(t, err)
	require.NotEqual(t, token.Key, rotatedKey)
	var rotatedToken Token
	require.NoError(t, DB.First(&rotatedToken, token.Id).Error)
	assert.Equal(t, rotatedKey, rotatedToken.Key)
	assert.Equal(t, token.Id, rotatedToken.Id)
	assert.Equal(t, token.UserSubscriptionId, rotatedToken.UserSubscriptionId)
	assert.Equal(t, token.ExpiredTime, rotatedToken.ExpiredTime)
	assert.Equal(t, token.ModelLimits, rotatedToken.ModelLimits)
	var bindingAfterRotation SubscriptionKeyBinding
	require.NoError(t, DB.Where("user_subscription_id = ?", subscription.Id).First(&bindingAfterRotation).Error)
	assert.Equal(t, binding.TokenId, bindingAfterRotation.TokenId)
}

func TestCompleteSubscriptionOrderRollsBackWhenKeyProvisioningFails(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-key-failure-user")
	plan := v1MonthlyPlanFixture(t, "monthly-19", 1, 19.9)
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	order := &SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         "v1-order-key-failure",
		PaymentMethod:   PaymentProviderAlipay,
		PaymentProvider: PaymentProviderAlipay,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, order.Insert())

	callbackName := fmt.Sprintf("test:fail-token-provisioning-%s", t.Name())
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "Token" {
			tx.AddError(errors.New("forced token provisioning failure"))
		}
	}))
	t.Cleanup(func() { DB.Callback().Create().Remove(callbackName) })

	err := CompleteSubscriptionOrder(order.TradeNo, "", PaymentProviderAlipay, "alipay")
	require.ErrorContains(t, err, "forced token provisioning failure")
	pending := GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, pending)
	assert.Equal(t, common.TopUpStatusPending, pending.Status)
	assert.Nil(t, pending.UserSubscriptionId)
	var subscriptionCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	assert.Zero(t, subscriptionCount)
}

func TestMonthlyTiersShareOneConcurrentSafePurchaseWindow(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-cycle-user")
	starter := v1MonthlyPlanFixture(t, "monthly-19", 1, 19.9)
	standard := v1MonthlyPlanFixture(t, "monthly-49", 2, 49.9)
	require.NoError(t, DB.Create(starter).Error)
	require.NoError(t, DB.Create(standard).Error)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		_, _, err := createProvisionedSubscriptionFromPlanTx(tx, user.Id, starter, "test")
		return err
	}))
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, _, createErr := createProvisionedSubscriptionFromPlanTx(tx, user.Id, standard, "test")
		return createErr
	})
	require.ErrorContains(t, err, "只允许补差升级")

	var cycleCount int64
	require.NoError(t, DB.Model(&MonthlySubscriptionCycle{}).Where("user_id = ?", user.Id).Count(&cycleCount).Error)
	assert.EqualValues(t, 1, cycleCount)
}

func TestMonthlyUpgradeKeepsSubscriptionKeyAndExpiryAndOnlyAddsDifference(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-upgrade-user")
	starter := v1MonthlyPlanFixture(t, "monthly-19", 1, 19.9)
	advanced := v1MonthlyPlanFixture(t, "monthly-99", 3, 99.9)
	require.NoError(t, DB.Create(starter).Error)
	require.NoError(t, DB.Create(advanced).Error)
	InvalidateSubscriptionPlanCache(starter.Id)
	InvalidateSubscriptionPlanCache(advanced.Id)

	purchase, _, err := CreateSubscriptionOrder(user.Id, starter.Id, "v1-upgrade-purchase", PaymentMethodAlipay, PaymentProviderAlipay, true)
	require.NoError(t, err)
	require.NoError(t, CompleteSubscriptionOrder(purchase.TradeNo, "", PaymentProviderAlipay, PaymentMethodAlipay))
	require.NotNil(t, purchase)
	purchase = GetSubscriptionOrderByTradeNo(purchase.TradeNo)
	require.NotNil(t, purchase.UserSubscriptionId)

	var beforeSub UserSubscription
	require.NoError(t, DB.First(&beforeSub, *purchase.UserSubscriptionId).Error)
	require.NoError(t, DB.Model(&beforeSub).Update("amount_used", int64(1_234_567)).Error)
	var beforeBinding SubscriptionKeyBinding
	require.NoError(t, DB.Where("user_subscription_id = ?", beforeSub.Id).First(&beforeBinding).Error)
	var beforeToken Token
	require.NoError(t, DB.First(&beforeToken, beforeBinding.TokenId).Error)

	upgrade, _, err := CreateSubscriptionOrder(user.Id, advanced.Id, "v1-upgrade-order", PaymentMethodAlipay, PaymentProviderAlipay, true)
	require.NoError(t, err)
	require.Equal(t, SubscriptionOrderTypeUpgrade, upgrade.OrderType)
	require.Equal(t, 80.0, upgrade.Money)
	require.Equal(t, beforeSub.Id, *upgrade.UserSubscriptionId)

	// Mutating the live plan after checkout must not alter the paid order's
	// quota/price/duration truth at completion.
	require.NoError(t, DB.Model(advanced).Updates(map[string]interface{}{
		"total_amount": int64(1), "duration_value": 1, "price_amount": 1.0,
	}).Error)
	InvalidateSubscriptionPlanCache(advanced.Id)

	require.NoError(t, CompleteSubscriptionOrder(upgrade.TradeNo, `{"verified":true}`, PaymentProviderAlipay, PaymentMethodAlipay))

	var afterSub UserSubscription
	require.NoError(t, DB.First(&afterSub, beforeSub.Id).Error)
	assert.Equal(t, beforeSub.Id, afterSub.Id)
	assert.Equal(t, beforeSub.StartTime, afterSub.StartTime)
	assert.Equal(t, beforeSub.EndTime, afterSub.EndTime)
	assert.Equal(t, beforeSub.PeriodId, afterSub.PeriodId)
	assert.EqualValues(t, 1_234_567, afterSub.AmountUsed)
	assert.EqualValues(t, int64(99.9*common.QuotaPerUnit), afterSub.AmountTotal)
	assert.Equal(t, 3, afterSub.ProductTier)
	assert.Equal(t, 99.9, afterSub.PriceAmountSnapshot)

	var afterBinding SubscriptionKeyBinding
	require.NoError(t, DB.Where("user_subscription_id = ?", afterSub.Id).First(&afterBinding).Error)
	assert.Equal(t, beforeBinding.TokenId, afterBinding.TokenId)
	var afterToken Token
	require.NoError(t, DB.First(&afterToken, afterBinding.TokenId).Error)
	assert.Equal(t, beforeToken.Key, afterToken.Key)
	assert.Equal(t, beforeToken.ExpiredTime, afterToken.ExpiredTime)
	assert.Equal(t, afterSub.Id, afterToken.UserSubscriptionId)

	var courseCount int64
	require.NoError(t, DB.Model(&CourseEntitlement{}).Where("user_id = ?", user.Id).Count(&courseCount).Error)
	assert.EqualValues(t, 1, courseCount)
	var cycle MonthlySubscriptionCycle
	require.NoError(t, DB.Where("user_id = ? AND product_family = ?", user.Id, advanced.ProductFamily).First(&cycle).Error)
	assert.Equal(t, 3, cycle.CurrentTier)
	assert.Contains(t, cycle.UpgradeHistory, upgrade.TradeNo)

	// A duplicate callback is a no-op; a same-tier/down-tier purchase is denied.
	require.NoError(t, CompleteSubscriptionOrder(upgrade.TradeNo, `{"duplicate":true}`, PaymentProviderAlipay, PaymentMethodAlipay))
	_, _, err = CreateSubscriptionOrder(user.Id, starter.Id, "v1-upgrade-downgrade", PaymentMethodAlipay, PaymentProviderAlipay, true)
	require.ErrorContains(t, err, "不可重复购买或降档")
}

func TestRepeatedPackPurchasesProvisionIndependentPoolsAndKeys(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-repeat-pack-user")
	plan := v1PackK3PlanFixture(t, "k3-49", 49.9)
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	var subscriptionIds []int
	var tokenIds []int
	var keys []string
	for index := 1; index <= 2; index++ {
		tradeNo := fmt.Sprintf("v1-repeat-pack-%d", index)
		order, _, err := CreateSubscriptionOrder(user.Id, plan.Id, tradeNo, PaymentMethodAlipay, PaymentProviderAlipay, true)
		require.NoError(t, err)
		require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, "", PaymentProviderAlipay, PaymentMethodAlipay))
		order = GetSubscriptionOrderByTradeNo(order.TradeNo)
		require.NotNil(t, order.UserSubscriptionId)
		subscriptionIds = append(subscriptionIds, *order.UserSubscriptionId)
		var binding SubscriptionKeyBinding
		require.NoError(t, DB.Where("user_subscription_id = ?", *order.UserSubscriptionId).First(&binding).Error)
		var token Token
		require.NoError(t, DB.First(&token, binding.TokenId).Error)
		tokenIds = append(tokenIds, token.Id)
		keys = append(keys, token.Key)
	}
	require.NotEqual(t, subscriptionIds[0], subscriptionIds[1])
	require.NotEqual(t, tokenIds[0], tokenIds[1])
	require.NotEqual(t, keys[0], keys[1])
}

func TestPaymentReceiptRejectsUnderAndOverPaymentBeforeProvisioning(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-payment-amount-user")
	plan := v1MonthlyPlanFixture(t, "monthly-19", 1, 19.9)
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	order, _, err := CreateSubscriptionOrder(user.Id, plan.Id, "v1-payment-amount", PaymentMethodAlipay, PaymentProviderAlipay, true)
	require.NoError(t, err)

	for _, amount := range []string{"19.89", "19.91"} {
		err = CompleteSubscriptionOrderWithReceipt(order.TradeNo, PaymentProviderAlipay, PaymentMethodAlipay, PaymentReceipt{
			Amount: decimal.RequireFromString(amount), Currency: "CNY", ProviderTradeNo: "provider-" + amount,
		})
		require.ErrorContains(t, err, "payment amount mismatch")
		pending := GetSubscriptionOrderByTradeNo(order.TradeNo)
		require.Equal(t, common.TopUpStatusPending, pending.Status)
		require.Nil(t, pending.UserSubscriptionId)
	}

	require.NoError(t, CompleteSubscriptionOrderWithReceipt(order.TradeNo, PaymentProviderAlipay, PaymentMethodAlipay, PaymentReceipt{
		Amount: decimal.RequireFromString("19.90"), Currency: "CNY", ProviderTradeNo: "provider-exact",
	}))
	completed := GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.Equal(t, common.TopUpStatusSuccess, completed.Status)
	require.Equal(t, "provider-exact", completed.ProviderTradeNo)
	require.Equal(t, "CNY", completed.PaidCurrency)
}

func TestCompleteTopUpPaymentIsAtomicStrictAndIdempotent(t *testing.T) {
	truncateTables(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	user := createV1ContractUser(t, "v1-wallet-payment-user")
	topup := &TopUp{
		UserId: user.Id, Amount: 10, Money: 10, Currency: "CNY", TradeNo: "v1-wallet-payment",
		PaymentMethod: PaymentMethodAlipay, PaymentProvider: PaymentProviderAlipay, Status: common.TopUpStatusPending,
	}
	require.NoError(t, topup.Insert())

	receipt := PaymentReceipt{Amount: decimal.NewFromInt(10), Currency: "CNY", ProviderTradeNo: "ali-wallet-1"}
	completed, quota, credited, err := CompleteTopUpPayment(topup.TradeNo, PaymentProviderAlipay, receipt)
	require.NoError(t, err)
	require.True(t, credited)
	require.Equal(t, 5_000_000, quota)
	require.Equal(t, common.TopUpStatusSuccess, completed.Status)

	completed, quota, credited, err = CompleteTopUpPayment(topup.TradeNo, PaymentProviderAlipay, receipt)
	require.NoError(t, err)
	require.False(t, credited)
	require.Zero(t, quota)
	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, 5_000_000, after.Quota)

	bad := &TopUp{
		UserId: user.Id, Amount: 30, Money: 30, Currency: "CNY", TradeNo: "v1-wallet-bad-payment",
		PaymentMethod: PaymentMethodAlipay, PaymentProvider: PaymentProviderAlipay, Status: common.TopUpStatusPending,
	}
	require.NoError(t, bad.Insert())
	_, _, _, err = CompleteTopUpPayment(bad.TradeNo, PaymentProviderAlipay, PaymentReceipt{
		Amount: decimal.RequireFromString("30.01"), Currency: "CNY", ProviderTradeNo: "ali-overpay",
	})
	require.ErrorContains(t, err, "payment amount mismatch")
	require.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(bad.TradeNo).Status)
}

func TestCompleteTopUpPaymentRollsBackStatusWhenQuotaCreditFails(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "v1-wallet-rollback-user")
	topup := &TopUp{
		UserId: user.Id, Amount: 10, Money: 10, Currency: "CNY", TradeNo: "v1-wallet-rollback",
		PaymentMethod: PaymentMethodAlipay, PaymentProvider: PaymentProviderAlipay, Status: common.TopUpStatusPending,
	}
	require.NoError(t, topup.Insert())

	callbackName := fmt.Sprintf("test:fail-wallet-credit-%s", t.Name())
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "User" {
			tx.AddError(errors.New("forced wallet credit failure"))
		}
	}))
	t.Cleanup(func() { DB.Callback().Update().Remove(callbackName) })

	_, _, _, err := CompleteTopUpPayment(topup.TradeNo, PaymentProviderAlipay, PaymentReceipt{
		Amount: decimal.NewFromInt(10), Currency: "CNY", ProviderTradeNo: "ali-rollback",
	})
	require.ErrorContains(t, err, "forced wallet credit failure")
	require.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo(topup.TradeNo).Status)
	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Zero(t, after.Quota)
}

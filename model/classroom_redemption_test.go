package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupClassroomRedemptionTest(t *testing.T) (*SubscriptionPlan, []Redemption) {
	t.Helper()
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	for _, table := range []string{
		"product_redemption_claims", "redemptions", "subscription_key_bindings", "tokens",
		"subscription_orders", "user_subscriptions", "subscription_plans", "price_book_prices", "product_prices", "abilities", "channels", "logs", "users",
	} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{
			"product_redemption_claims", "redemptions", "subscription_key_bindings", "tokens",
			"subscription_orders", "user_subscriptions", "subscription_plans", "price_book_prices", "product_prices", "abilities", "channels", "logs", "users",
		} {
			_ = DB.Exec("DELETE FROM " + table).Error
		}
	})

	channel := Channel{
		Id: 15, Name: "果盒-豆包", Type: 14, Key: "test-key", Status: common.ChannelStatusEnabled,
		Models: ClassroomModelV41 + "," + ClassroomModelV4GA, Group: "default",
	}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Create(&Channel{
		Id: 14, Name: "果核-Qwen", Type: 1, Key: "test-qwen-key", Status: common.ChannelStatusEnabled,
		Models: ClassroomModelQwen, Group: "default",
	}).Error)
	bootstrap, err := BootstrapClassroomProduct(true, true, true, 0)
	require.NoError(t, err)
	require.Equal(t, int64(2_500_000), bootstrap.TotalAmount)
	require.Positive(t, bootstrap.PlanId)

	plan, err := GetSubscriptionPlanById(bootstrap.PlanId)
	require.NoError(t, err)
	require.Equal(t, 10, plan.DurationValue)
	require.Equal(t, 0.60, plan.BillingMultiplier)
	require.False(t, *plan.AllowWalletOverflow)

	summary, err := CreateProductRedemptionBatch(1, ProductRedemptionBatchRequest{
		Name: "10月14日课堂", BatchCode: "CLASSROOM-200", TargetPlanId: plan.Id,
		PrimaryCount: 200, ReserveCount: 20, ExpiredTime: common.GetTimestamp() + 86400,
	})
	require.NoError(t, err)
	require.Equal(t, 200, summary.UnusedPrimaryCount)
	require.Equal(t, 20, summary.UnusedReserveCount)
	var codes []Redemption
	require.NoError(t, DB.Where("batch_code = ?", summary.BatchCode).Order("id asc").Find(&codes).Error)
	require.Len(t, codes, 220)
	unique := map[string]struct{}{}
	for _, code := range codes {
		unique[code.Key] = struct{}{}
	}
	require.Len(t, unique, 220)
	return plan, codes
}

func TestClassroomProductPricingAndBatchContract(t *testing.T) {
	plan, codes := setupClassroomRedemptionTest(t)
	assert.Equal(t, ProductTypePackDeepSeekClassroom, plan.ProductType)
	assert.Equal(t, "deepseek_classroom", plan.ProductFamily)
	assert.Equal(t, int64(5*500_000), plan.TotalAmount)
	assert.False(t, plan.FallbackAllowed)
	assert.False(t, *plan.AllowBalancePay)

	reserveCount := 0
	for _, code := range codes {
		if code.IsReserve {
			reserveCount++
		}
	}
	assert.Equal(t, 20, reserveCount)

	var v41 ProductPrice
	require.NoError(t, DB.Where("product_type = ? AND model = ?", ProductTypePackDeepSeekClassroom, ClassroomModelV41).First(&v41).Error)
	assert.Contains(t, v41.BillingExpression, `weekday("Asia/Shanghai")`)
	assert.Contains(t, v41.BillingExpression, `cr * 0.04`)
	assert.Contains(t, v41.BillingExpression, `cr * 0.02`)
	var ga ProductPrice
	require.NoError(t, DB.Where("product_type = ? AND model = ?", ProductTypePackDeepSeekClassroom, ClassroomModelV4GA).First(&ga).Error)
	assert.Contains(t, ga.BillingExpression, `cr * 0.10`)
	var qwen ProductPrice
	require.NoError(t, DB.Where("product_type = ? AND model = ?", ProductTypePackDeepSeekClassroom, ClassroomModelQwen).First(&qwen).Error)
	assert.Equal(t, 1.0, qwen.BillingMultiplier)
	assert.Equal(t, ClassroomPriceVersion, qwen.PriceVersion)
	assert.Contains(t, qwen.BillingExpression, `cc * 1.25`)
	assert.Equal(t, `[14]`, qwen.AllowedChannels)
}

func TestClassroomBootstrapCreatesHiddenPlanAndKeepsRedemptionEnabled(t *testing.T) {
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	for _, table := range []string{
		"product_redemption_claims", "redemptions", "subscription_key_bindings", "tokens",
		"subscription_orders", "user_subscriptions", "subscription_plans", "product_prices", "abilities", "channels", "logs", "users",
	} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	channel := Channel{
		Id: 15, Name: "果盒-豆包", Type: 14, Key: "test-key", Status: common.ChannelStatusEnabled,
		Models: ClassroomModelV41 + "," + ClassroomModelV4GA, Group: "default",
	}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Create(&Channel{
		Id: 14, Name: "果核-Qwen", Type: 1, Key: "test-qwen-key", Status: common.ChannelStatusEnabled,
		Models: ClassroomModelQwen, Group: "default",
	}).Error)

	result, err := BootstrapClassroomProduct(true, true, false, 0)
	require.NoError(t, err)
	var plan SubscriptionPlan
	require.NoError(t, DB.First(&plan, result.PlanId).Error)
	require.True(t, plan.Enabled)
	require.False(t, plan.Listed)
	require.True(t, plan.RedemptionEnabled)
}

func TestClassroomProductPricesBothModelsIncludingCacheAndPeakWindows(t *testing.T) {
	setupClassroomRedemptionTest(t)
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	params := billingexpr.TokenParams{P: 1_000_000, C: 1_000_000, CR: 1_000_000, CC: 1_000_000, Len: 2_000_000}

	loadSnapshot := func(modelName string) *billingexpr.BillingSnapshot {
		var price ProductPrice
		require.NoError(t, DB.Where("product_type = ? AND model = ?", ProductTypePackDeepSeekClassroom, modelName).First(&price).Error)
		return &billingexpr.BillingSnapshot{
			BillingMode:       "tiered_expr",
			ModelName:         modelName,
			ExprString:        price.BillingExpression,
			ExprHash:          billingexpr.ExprHashString(price.BillingExpression),
			GroupRatio:        1,
			QuotaPerUnit:      common.QuotaPerUnit,
			ProductType:       price.ProductType,
			ProductMultiplier: price.BillingMultiplier,
			PriceVersion:      price.PriceVersion,
		}
	}

	ga, err := billingexpr.ComputeTieredQuota(loadSnapshot(ClassroomModelV4GA), params)
	require.NoError(t, err)
	require.Equal(t, "base", ga.MatchedTier)
	// (3 input + 0.10 cached + 9 output) * 0.60 * 500,000 quota/CNY.
	require.Equal(t, 3_630_000, ga.ActualQuotaAfterGroup)

	v41Snapshot := loadSnapshot(ClassroomModelV41)
	peak, err := billingexpr.ComputeTieredQuotaWithRequest(v41Snapshot, params, billingexpr.RequestInput{
		EvaluationUnix: time.Date(2026, 10, 12, 9, 0, 0, 0, location).Unix(),
	})
	require.NoError(t, err)
	require.Equal(t, "peak", peak.MatchedTier)
	// (2 input + 0.04 cached + 8 output) * 0.60 * 500,000 quota/CNY.
	require.Equal(t, 3_012_000, peak.ActualQuotaAfterGroup)

	offpeak, err := billingexpr.ComputeTieredQuotaWithRequest(v41Snapshot, params, billingexpr.RequestInput{
		EvaluationUnix: time.Date(2026, 10, 12, 12, 0, 0, 0, location).Unix(),
	})
	require.NoError(t, err)
	require.Equal(t, "offpeak", offpeak.MatchedTier)
	// (1 input + 0.02 cached + 4 output) * 0.60 * 500,000 quota/CNY.
	require.Equal(t, 1_506_000, offpeak.ActualQuotaAfterGroup)

	weekend, err := billingexpr.ComputeTieredQuotaWithRequest(v41Snapshot, params, billingexpr.RequestInput{
		EvaluationUnix: time.Date(2026, 10, 17, 10, 0, 0, 0, location).Unix(),
	})
	require.NoError(t, err)
	require.Equal(t, "offpeak", weekend.MatchedTier)
	require.Equal(t, offpeak.ActualQuotaAfterGroup, weekend.ActualQuotaAfterGroup)

	qwen, err := billingexpr.ComputeTieredQuota(loadSnapshot(ClassroomModelQwen), params)
	require.NoError(t, err)
	require.Equal(t, "base", qwen.MatchedTier)
	// (0.80 input + 0.10 cache hit + 1.25 cache creation + 2.70 output)
	// * 1.00 * 500,000 quota/CNY.
	require.Equal(t, 2_425_000, qwen.ActualQuotaAfterGroup)

	// The ordinary wallet catalog shares the audited official expressions but
	// freezes a 1.00 multiplier, so the classroom discount cannot leak to a
	// normal API key.
	for _, modelName := range []string{ClassroomModelV4GA, ClassroomModelV41} {
		var classroomPrice, walletPrice ProductPrice
		require.NoError(t, DB.Where("product_type = ? AND model = ?", ProductTypePackDeepSeekClassroom, modelName).First(&classroomPrice).Error)
		require.NoError(t, DB.Where("product_type = ? AND model = ?", ProductTypeWallet, modelName).First(&walletPrice).Error)
		require.Equal(t, classroomPrice.OfficialReferencePrice, walletPrice.OfficialReferencePrice)
		require.Equal(t, classroomPrice.BillingExpression, walletPrice.BillingExpression)
		require.Equal(t, 0.60, classroomPrice.BillingMultiplier)
		require.Equal(t, 1.00, walletPrice.BillingMultiplier)
		require.Equal(t, WalletDeepSeekPriceVersion, walletPrice.PriceVersion)
		require.False(t, walletPrice.FallbackAllowed)
	}
}

func TestClassroomProductCanStopNewRedemptionsWithoutBreakingPriceSnapshots(t *testing.T) {
	plan, _ := setupClassroomRedemptionTest(t)
	result, err := BootstrapClassroomProduct(true, false, false, 0)
	require.NoError(t, err)
	require.Equal(t, plan.Id, result.PlanId)

	stoppedPlan, err := GetSubscriptionPlanById(plan.Id)
	require.NoError(t, err)
	require.False(t, stoppedPlan.Enabled)
	require.False(t, stoppedPlan.Listed)

	var disabledPrices int64
	require.NoError(t, DB.Model(&ProductPrice{}).
		Where("product_type = ? AND enabled = ?", ProductTypePackDeepSeekClassroom, false).
		Count(&disabledPrices).Error)
	require.Equal(t, int64(3), disabledPrices)

	// Existing entitlements resolve by immutable version, independent of the
	// current sale switch.
	_, err = GetProductPriceSnapshot(ProductTypePackDeepSeekClassroom, ClassroomModelV4GA, ClassroomPriceVersion)
	require.NoError(t, err)
}

func TestClassroomBootstrapMigratesExistingEntitlementWithoutResettingLedgerOrDates(t *testing.T) {
	plan, codes := setupClassroomRedemptionTest(t)
	user := &User{Username: "classroom-migrate", AffCode: "classroom-migrate-aff", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	claim, err := RedeemProductCode(codes[0].Key, user.Id, plan.Id)
	require.NoError(t, err)
	var originalToken Token
	require.NoError(t, DB.First(&originalToken, claim.TokenId).Error)
	legacyScope, legacyAllowed, legacyCosts, err := classroomLegacySnapshot()
	require.NoError(t, err)
	startTime := common.GetTimestamp() - 300
	endTime := common.GetTimestamp() + 7200
	require.NoError(t, DB.Model(&UserSubscription{}).Where("id = ?", claim.UserSubscriptionId).Updates(map[string]interface{}{
		"amount_total": int64(2_500_000), "amount_used": int64(12_345), "start_time": startTime, "end_time": endTime,
		"price_version": ClassroomLegacyPriceVersion, "model_scope": legacyScope,
		"allowed_channels": legacyAllowed, "max_upstream_cost": legacyCosts,
	}).Error)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", claim.TokenId).Updates(map[string]interface{}{
		"used_quota": 12_345, "expired_time": endTime, "price_version": ClassroomLegacyPriceVersion,
		"model_limits": legacyScope, "allowed_channels": legacyAllowed, "max_upstream_cost": legacyCosts,
	}).Error)

	result, err := BootstrapClassroomProduct(true, true, true, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.MigratedSubscriptions)
	require.EqualValues(t, 1, result.MigratedTokens)

	var sub UserSubscription
	require.NoError(t, DB.First(&sub, claim.UserSubscriptionId).Error)
	require.Equal(t, int64(2_500_000), sub.AmountTotal)
	require.Equal(t, int64(12_345), sub.AmountUsed)
	require.Equal(t, startTime, sub.StartTime)
	require.Equal(t, endTime, sub.EndTime)
	require.Equal(t, ClassroomPriceVersion, sub.PriceVersion)
	require.ElementsMatch(t, []string{ClassroomModelQwen, ClassroomModelV41, ClassroomModelV4GA}, explicitModelIds(sub.ModelScope))

	var token Token
	require.NoError(t, DB.First(&token, claim.TokenId).Error)
	require.Equal(t, 12_345, token.UsedQuota)
	require.Equal(t, endTime, token.ExpiredTime)
	require.Equal(t, ClassroomPriceVersion, token.PriceVersion)
	require.Equal(t, ClassroomPriceVersion, sub.PriceVersion)
	require.Equal(t, originalToken.Key, token.Key)

	idempotent, err := BootstrapClassroomProduct(true, true, true, 0)
	require.NoError(t, err)
	require.Zero(t, idempotent.MigratedSubscriptions)
	require.Zero(t, idempotent.MigratedTokens)
}

func TestClassroomRedemptionIsAtomicIdempotentAndOnePerAccount(t *testing.T) {
	plan, codes := setupClassroomRedemptionTest(t)
	user := &User{Username: "classroom-one", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)

	first, err := RedeemProductCode(codes[0].Key, user.Id, plan.Id)
	require.NoError(t, err)
	require.Positive(t, first.UserSubscriptionId)
	require.Positive(t, first.TokenId)
	assert.False(t, first.Idempotent)

	retry, err := RedeemProductCode(codes[0].Key, user.Id, plan.Id)
	require.NoError(t, err)
	assert.True(t, retry.Idempotent)
	assert.Equal(t, first.UserSubscriptionId, retry.UserSubscriptionId)
	assert.Equal(t, first.TokenId, retry.TokenId)

	_, err = RedeemProductCode(codes[1].Key, user.Id, plan.Id)
	require.ErrorContains(t, err, "最多领取一份")
	var untouched Redemption
	require.NoError(t, DB.First(&untouched, codes[1].Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, untouched.Status)

	var subCount, tokenCount, claimCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", user.Id).Count(&subCount).Error)
	require.NoError(t, DB.Model(&Token{}).Where("user_id = ?", user.Id).Count(&tokenCount).Error)
	require.NoError(t, DB.Model(&ProductRedemptionClaim{}).Where("user_id = ?", user.Id).Count(&claimCount).Error)
	assert.Equal(t, int64(1), subCount)
	assert.Equal(t, int64(1), tokenCount)
	assert.Equal(t, int64(1), claimCount)
}

func TestRefundSubscriptionPreConsumeIsAtomicAndIdempotent(t *testing.T) {
	plan, codes := setupClassroomRedemptionTest(t)
	user := &User{Username: "classroom-refund", AffCode: "class-refund-aff", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	claim, err := RedeemProductCode(codes[0].Key, user.Id, plan.Id)
	require.NoError(t, err)

	reserved, err := PreConsumeUserSubscription(
		"classroom-refund-idempotency", user.Id, ClassroomModelV4GA, 0, 1_000, claim.UserSubscriptionId,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1_000), reserved.PreConsumed)

	require.NoError(t, RefundSubscriptionPreConsume("classroom-refund-idempotency"))
	require.NoError(t, RefundSubscriptionPreConsume("classroom-refund-idempotency"))
	var entitlement UserSubscription
	require.NoError(t, DB.First(&entitlement, claim.UserSubscriptionId).Error)
	require.Zero(t, entitlement.AmountUsed)
	var record SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", "classroom-refund-idempotency").First(&record).Error)
	require.Equal(t, "refunded", record.Status)
}

func TestClassroomRedemptionTwoHundredConcurrentAccounts(t *testing.T) {
	plan, codes := setupClassroomRedemptionTest(t)
	users := make([]User, 200)
	for index := range users {
		users[index] = User{
			Username: fmt.Sprintf("classroom-%03d", index), AffCode: fmt.Sprintf("class-aff-%03d", index),
			Status: common.UserStatusEnabled, Group: "default",
		}
	}
	require.NoError(t, DB.CreateInBatches(&users, 50).Error)

	errorsByIndex := make([]error, 200)
	var wg sync.WaitGroup
	wg.Add(200)
	for index := 0; index < 200; index++ {
		go func(i int) {
			defer wg.Done()
			_, errorsByIndex[i] = RedeemProductCode(codes[i].Key, users[i].Id, plan.Id)
		}(index)
	}
	wg.Wait()
	for index, redeemErr := range errorsByIndex {
		require.NoErrorf(t, redeemErr, "redemption %d failed", index)
	}

	var usedCodes, subscriptions, tokens, claims int64
	require.NoError(t, DB.Model(&Redemption{}).Where("batch_code = ? AND status = ?", "CLASSROOM-200", common.RedemptionCodeStatusUsed).Count(&usedCodes).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("product_type = ?", ProductTypePackDeepSeekClassroom).Count(&subscriptions).Error)
	require.NoError(t, DB.Model(&Token{}).Where("product_type = ?", ProductTypePackDeepSeekClassroom).Count(&tokens).Error)
	require.NoError(t, DB.Model(&ProductRedemptionClaim{}).Count(&claims).Error)
	assert.Equal(t, int64(200), usedCodes)
	assert.Equal(t, int64(200), subscriptions)
	assert.Equal(t, int64(200), tokens)
	assert.Equal(t, int64(200), claims)

	var duplicateKeys int64
	require.NoError(t, DB.Raw(`SELECT COUNT(*) FROM (SELECT "key" FROM tokens GROUP BY "key" HAVING COUNT(*) > 1) AS duplicate_keys`).Scan(&duplicateKeys).Error)
	assert.Zero(t, duplicateKeys)

	// Ensure no code can be consumed without a complete entitlement and key.
	var incomplete int64
	require.NoError(t, DB.Model(&Redemption{}).
		Where("batch_code = ? AND status = ? AND (claimed_subscription_id = 0 OR claimed_token_id = 0)", "CLASSROOM-200", common.RedemptionCodeStatusUsed).
		Count(&incomplete).Error)
	assert.Zero(t, incomplete)
}

func TestClassroomRedemptionRollsBackWhenProvisioningFails(t *testing.T) {
	plan, codes := setupClassroomRedemptionTest(t)
	user := &User{Username: "rollback-user", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, DB.Session(&gorm.Session{SkipHooks: true}).Model(&ProductPrice{}).
		Where("product_type = ?", ProductTypePackDeepSeekClassroom).Update("enabled", false).Error)

	_, err := RedeemProductCode(codes[0].Key, user.Id, plan.Id)
	require.Error(t, err)
	var code Redemption
	require.NoError(t, DB.First(&code, codes[0].Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
	var count int64
	require.NoError(t, DB.Model(&ProductRedemptionClaim{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
}

func TestUnifiedProductAndWalletRedemptionContracts(t *testing.T) {
	setupClassroomRedemptionTest(t)
	noWalletFallback := false
	plans := []SubscriptionPlan{
		{
			Title: "Lite", PriceAmount: 19, Currency: "CNY", DurationUnit: SubscriptionDurationDay,
			DurationValue: 30, Enabled: true, Listed: true, RedemptionEnabled: true,
			TotalAmount: 9_500_000, Category: SubscriptionCategorySubscription,
			ProductType: ProductTypeLegacySubscription, ProductCode: "lite", ProductFamily: "legacy-monthly",
			BillingMultiplier: 1, AllowWalletOverflow: &noWalletFallback,
		},
		{
			Title: "Pro", PriceAmount: 49, Currency: "CNY", DurationUnit: SubscriptionDurationDay,
			DurationValue: 30, Enabled: true, Listed: true, RedemptionEnabled: true,
			TotalAmount: 24_500_000, Category: SubscriptionCategorySubscription,
			ProductType: ProductTypeLegacySubscription, ProductCode: "pro", ProductFamily: "legacy-monthly",
			BillingMultiplier: 1, AllowWalletOverflow: &noWalletFallback,
		},
		{
			Title: "普通流量包", PriceAmount: 10, Currency: "CNY", DurationUnit: SubscriptionDurationDay,
			DurationValue: 30, Enabled: false, Listed: false, RedemptionEnabled: true,
			TotalAmount: 5_000_000, Category: SubscriptionCategoryPackage, ModelScope: "glm-5.2",
			ProductType: ProductTypeLegacySubscription, ProductCode: "ordinary-pack", ProductFamily: "ordinary-pack",
			BillingMultiplier: 1, AllowWalletOverflow: &noWalletFallback,
		},
	}
	require.NoError(t, DB.Create(&plans).Error)
	for _, plan := range plans {
		InvalidateSubscriptionPlanCache(plan.Id)
	}
	user := &User{Username: "unified-redemption", AffCode: "unified-redemption-aff", Status: common.UserStatusEnabled, Group: "default", Quota: 100}
	require.NoError(t, DB.Create(user).Error)

	codes := []Redemption{
		{UserId: 1, Name: "lite", Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, TargetPlanId: plans[0].Id},
		{UserId: 1, Name: "pro", Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, TargetPlanId: plans[1].Id},
		{UserId: 1, Name: "pack", Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, TargetPlanId: plans[2].Id},
		{UserId: 1, Name: "wallet", Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, Quota: 900},
	}
	require.NoError(t, DB.Create(&codes).Error)

	_, err := RedeemProductCode(codes[1].Key, user.Id, plans[0].Id)
	require.ErrorContains(t, err, "不适用于所选商品")
	var untouchedProCode Redemption
	require.NoError(t, DB.First(&untouchedProCode, codes[1].Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, untouchedProCode.Status)

	lite, err := RedeemProductCode(codes[0].Key, user.Id, plans[0].Id)
	require.NoError(t, err)
	assert.Positive(t, lite.UserSubscriptionId)
	assert.Positive(t, lite.TokenId)
	retry, err := RedeemProductCode(codes[0].Key, user.Id, plans[0].Id)
	require.NoError(t, err)
	assert.True(t, retry.Idempotent)
	assert.Equal(t, lite.UserSubscriptionId, retry.UserSubscriptionId)

	pack, err := RedeemProductCode(codes[2].Key, user.Id, plans[2].Id)
	require.NoError(t, err)
	var packToken Token
	require.NoError(t, DB.First(&packToken, pack.TokenId).Error)
	assert.Equal(t, "glm-5.2", packToken.ModelLimits)
	assert.Equal(t, pack.UserSubscriptionId, packToken.UserSubscriptionId)

	// The wallet page may keep its legacy redemption entry, but a product-bound
	// code must still delegate to the unified atomic entitlement + key path.
	legacyEntryAmount, err := Redeem(codes[1].Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, int(plans[1].TotalAmount), legacyEntryAmount)
	var redeemedProCode Redemption
	require.NoError(t, DB.First(&redeemedProCode, codes[1].Id).Error)
	assert.Positive(t, redeemedProCode.ClaimedSubscriptionId)
	assert.Positive(t, redeemedProCode.ClaimedTokenId)

	credited, err := Redeem(codes[3].Key, user.Id)
	require.NoError(t, err)
	assert.Equal(t, 900, credited)
	var refreshed User
	require.NoError(t, DB.First(&refreshed, user.Id).Error)
	assert.Equal(t, 1_000, refreshed.Quota)

	var paymentOrders int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).Count(&paymentOrders).Error)
	assert.Zero(t, paymentOrders, "redemption must never fabricate a payment order")
}

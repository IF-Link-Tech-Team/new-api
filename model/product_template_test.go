package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeedProductTemplatesIsIdempotentAndPreservesOperatorEdits(t *testing.T) {
	require.NoError(t, SeedProductTemplates())
	require.NoError(t, SeedProductTemplates())
	var count int64
	require.NoError(t, DB.Model(&ProductTemplate{}).Count(&count).Error)
	assert.EqualValues(t, 7, count)

	// An operational edit must survive re-seeding.
	require.NoError(t, DB.Model(&ProductTemplate{}).
		Where("product_type = ?", ProductTypePackQwen).Update("display_name", "运营手改名称").Error)
	InvalidateProductTemplateCache(ProductTypePackQwen)
	t.Cleanup(func() {
		DB.Model(&ProductTemplate{}).Where("product_type = ?", ProductTypePackQwen).Update("display_name", "Qwen 流量包")
		InvalidateProductTemplateCache(ProductTypePackQwen)
	})

	require.NoError(t, SeedProductTemplates())
	edited, err := GetProductTemplateByType(ProductTypePackQwen)
	require.NoError(t, err)
	assert.Equal(t, "运营手改名称", edited.DisplayName)
	require.NoError(t, SeedProductTemplates())
	require.NoError(t, DB.Model(&ProductTemplate{}).Count(&count).Error)
	assert.EqualValues(t, 7, count)
}

func TestProductTemplateSeedDefinitionsMatchFrozenContractConstants(t *testing.T) {
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	templates, err := desiredProductTemplates()
	require.NoError(t, err)
	require.Len(t, templates, 7)
	byType := map[string]ProductTemplate{}
	for _, template := range templates {
		byType[template.ProductType] = template
		assert.True(t, template.Locked, "core template %s must be locked", template.ProductType)
		expectedMultiplier, ok := frozenV1BillingMultipliers[template.ProductType]
		require.True(t, ok, "template %s must come from the frozen multiplier map", template.ProductType)
		assert.Equal(t, expectedMultiplier, template.BillingMultiplier, "template %s multiplier drifted", template.ProductType)
		rules, err := template.priceRuleList()
		require.NoError(t, err)
		for _, rule := range rules {
			expectedTotal, err := templateRuleTotalAmount(rule.PriceAmount)
			require.NoError(t, err)
			assert.Equal(t, expectedTotal, rule.TotalAmount, "template %s rule total drifted", template.ProductType)
		}
	}

	monthly := byType[ProductTypeMonthlySubscription]
	assert.Equal(t, SubscriptionCategorySubscription, monthly.Category)
	assert.Equal(t, 30, monthly.DurationDays)
	assert.Equal(t, MonthlyProductFamily, monthly.ProductFamily)
	assert.Equal(t, MonthlyPriceVersion, monthly.PriceVersion)
	monthlyRules, err := monthly.priceRuleList()
	require.NoError(t, err)
	require.Len(t, monthlyRules, 3)
	for _, rule := range monthlyRules {
		assert.Equal(t, monthlyTemplatePriceRules[rule.Tier], rule.PriceAmount)
	}

	classroom := byType[ProductTypePackDeepSeekClassroom]
	assert.Equal(t, 10, classroom.DurationDays)
	assert.Equal(t, "deepseek_classroom", classroom.ProductFamily)
	assert.Equal(t, ClassroomPriceVersion, classroom.PriceVersion)
	assert.Equal(t, 1, classroom.MaxPurchasePerUser)
	classroomRules, err := classroom.priceRuleList()
	require.NoError(t, err)
	require.Len(t, classroomRules, 1)
	assert.Equal(t, 5.0, classroomRules[0].PriceAmount)

	bigfish := byType[ProductTypePackDeepSeekBigfish]
	assert.Equal(t, 15, bigfish.DurationDays)
	assert.Equal(t, BigfishProductFamily, bigfish.ProductFamily)
	assert.Equal(t, BigfishPriceVersion, bigfish.PriceVersion)
	assert.False(t, bigfish.RedemptionAllowed)
	assert.False(t, bigfish.AllowBalancePay)
	bigfishRules, err := bigfish.priceRuleList()
	require.NoError(t, err)
	require.Len(t, bigfishRules, 3)
	for _, rule := range bigfishRules {
		assert.Equal(t, bigfishTemplatePriceRules[rule.ProductCode], rule.PriceAmount)
	}

	for _, productType := range []string{ProductTypePackDSGLM, ProductTypePackQwen} {
		pack := byType[productType]
		rules, err := pack.priceRuleList()
		require.NoError(t, err)
		require.Len(t, rules, 1)
		assert.Equal(t, 19.9, rules[0].PriceAmount, "%s price rule drifted", productType)
	}
	k3 := byType[ProductTypePackK3]
	k3Rules, err := k3.priceRuleList()
	require.NoError(t, err)
	require.Len(t, k3Rules, 3)

	wallet := byType[ProductTypeWallet]
	assert.Equal(t, 0, wallet.DurationDays)
	walletRules, err := wallet.priceRuleList()
	require.NoError(t, err)
	assert.Empty(t, walletRules)
}

func TestSeededTemplatesAcceptExistingCatalogProducts(t *testing.T) {
	setupMonthlyProductsTest(t)
	monthlyTemplate, err := GetProductTemplateByType(ProductTypeMonthlySubscription)
	require.NoError(t, err)
	var monthlyPlans []SubscriptionPlan
	require.NoError(t, DB.Where("product_type = ?", ProductTypeMonthlySubscription).Order("product_tier").Find(&monthlyPlans).Error)
	require.Len(t, monthlyPlans, 3)
	for index := range monthlyPlans {
		require.NoError(t, monthlyPlans[index].ValidateV1Contract())
		assert.Equal(t, int64(monthlyTemplate.Id), monthlyPlans[index].TemplateId)
	}

	classroom, err := classroomDesiredPlan(false, false, 2_500_000)
	require.NoError(t, err)
	classroomTemplate, err := GetProductTemplateByType(ProductTypePackDeepSeekClassroom)
	require.NoError(t, err)
	assert.Equal(t, int64(classroomTemplate.Id), classroom.TemplateId)
}

func TestSeededTemplatesAcceptBigfishCatalogProducts(t *testing.T) {
	plans := setupBigfishProductsTest(t)
	require.Len(t, plans, 3)
	for index := range plans {
		require.NoError(t, plans[index].ValidateV1Contract())
	}
}

func TestValidateV1ContractRejectsUnknownProductTypeWithoutTemplate(t *testing.T) {
	truncateTables(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	require.NoError(t, DB.Where("product_type = ?", ProductTypePackK3).Delete(&ProductTemplate{}).Error)
	InvalidateProductTemplateCache(ProductTypePackK3)
	t.Cleanup(func() {
		require.NoError(t, SeedProductTemplates())
		InvalidateProductTemplateCache(ProductTypePackK3)
	})

	plan := &SubscriptionPlan{
		Title: "k3-no-template", PriceAmount: 49.9, Currency: "CNY",
		DurationUnit: SubscriptionDurationDay, DurationValue: 30,
		AllowWalletOverflow: common.GetPointer(false),
		TotalAmount:         int64(49.9 * common.QuotaPerUnit), QuotaResetPeriod: SubscriptionResetNever,
		ModelScope: "kimi-k3", Category: SubscriptionCategoryPackage,
		ProductType: ProductTypePackK3, ProductCode: "k3-no-template", ProductFamily: "pack-k3-v1",
		BillingMultiplier: 0.95, PriceVersion: "v1-k3-test",
		AllowedChannels: `{"kimi-k3":[14]}`,
		MaxUpstreamCost: `{"kimi-k3":{"input":14,"cached_input":1,"output":70}}`,
	}
	require.ErrorContains(t, plan.ValidateV1Contract(), "unknown product_type")
}

func TestValidateV1ContractRejectsPriceOutsideTemplatePriceRules(t *testing.T) {
	truncateTables(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	classroom, err := classroomDesiredPlan(false, false, 2_500_000)
	require.NoError(t, err)
	classroom.PriceAmount = 4.9
	require.ErrorContains(t, classroom.ValidateV1Contract(), "price rules")

	monthly, err := monthlyDesiredPlans(false, false)
	require.NoError(t, err)
	require.Len(t, monthly, 3)
	monthly[0].PriceAmount = 18.9
	require.ErrorContains(t, monthly[0].ValidateV1Contract(), "price rules")

	// A tier that has no rule at all is rejected too.
	monthly[1].ProductTier = 4
	require.ErrorContains(t, monthly[1].ValidateV1Contract(), "price rules")
}

func TestValidateV1ContractRejectsTemplateMultiplierDrift(t *testing.T) {
	truncateTables(t)
	classroom, err := classroomDesiredPlan(false, false, 2_500_000)
	require.NoError(t, err)
	classroom.BillingMultiplier = 0.7
	require.ErrorContains(t, classroom.ValidateV1Contract(), "billing_multiplier")
}

func TestSubscriptionPlanTemplateIdPinnedCheckedAndBackfilled(t *testing.T) {
	truncateTables(t)
	plan := v1PackK3PlanFixture(t, "k3-template-link", 49.9)
	k3Template, err := GetProductTemplateByType(ProductTypePackK3)
	require.NoError(t, err)
	require.Equal(t, int64(k3Template.Id), plan.TemplateId, "validation must pin the template link")

	monthlyTemplate, err := GetProductTemplateByType(ProductTypeMonthlySubscription)
	require.NoError(t, err)
	mismatched := *plan
	mismatched.TemplateId = int64(monthlyTemplate.Id)
	require.ErrorContains(t, mismatched.ValidateV1Contract(), "does not match product_type")

	legacy := *plan
	legacy.ProductType = ProductTypeLegacySubscription
	require.ErrorContains(t, legacy.ValidateV1Contract(), "template_id requires a V1 product_type")

	require.NoError(t, DB.Create(plan).Error)
	require.NoError(t, DB.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).UpdateColumn("template_id", 0).Error)
	require.NoError(t, BackfillSubscriptionPlanTemplateIds())
	var reloaded SubscriptionPlan
	require.NoError(t, DB.First(&reloaded, plan.Id).Error)
	assert.Equal(t, int64(k3Template.Id), reloaded.TemplateId)
}

func TestLockedProductTemplateRequiresMigrationPath(t *testing.T) {
	template, err := GetProductTemplateByType(ProductTypePackQwen)
	require.NoError(t, err)
	require.True(t, template.Locked)

	edited := *template
	edited.DisplayName = "高权限迁移改名"
	require.ErrorContains(t, UpdateProductTemplate(&edited), "locked")
	require.NoError(t, MigrateLockedProductTemplate(&edited))
	InvalidateProductTemplateCache(ProductTypePackQwen)
	t.Cleanup(func() {
		_ = MigrateLockedProductTemplate(template)
		InvalidateProductTemplateCache(ProductTypePackQwen)
	})

	reloaded, err := GetProductTemplateByType(ProductTypePackQwen)
	require.NoError(t, err)
	assert.Equal(t, "高权限迁移改名", reloaded.DisplayName)

	// product_type is the template identity and cannot be reassigned.
	relabeled := *template
	relabeled.ProductType = ProductTypePackK3
	require.NoError(t, MigrateLockedProductTemplate(&relabeled))
	reloaded, err = GetProductTemplateByType(ProductTypePackQwen)
	require.NoError(t, err)
	assert.Equal(t, ProductTypePackQwen, reloaded.ProductType)
}

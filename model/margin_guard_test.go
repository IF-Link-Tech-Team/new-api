package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTemplateMinMarginForTest(t *testing.T, productType string, margin float64) {
	t.Helper()
	template, err := GetProductTemplateByType(productType)
	require.NoError(t, err)
	original := *template
	updated := *template
	updated.MinMarginPercent = margin
	require.NoError(t, MigrateLockedProductTemplate(&updated))
	t.Cleanup(func() {
		require.NoError(t, MigrateLockedProductTemplate(&original))
	})
}

// marginGuardK3PlanFixture builds an enabled pack_k3 plan plus its immutable
// price row with explicit selling and cost components, so margin tests control
// the exact floor distance. The price row is created first because the margin
// check reads it during plan validation.
func marginGuardK3PlanFixture(t *testing.T, code string, priceVersion string, sale PriceComponents, cost PriceComponents) *SubscriptionPlan {
	t.Helper()
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	saleJSON, err := classroomJSON(sale)
	require.NoError(t, err)
	costJSON, err := classroomJSON(cost)
	require.NoError(t, err)
	priceRow := &ProductPrice{
		ProductType: ProductTypePackK3, Model: "kimi-k3", PriceVersion: priceVersion,
		OfficialReferencePrice: saleJSON, UserSellingPrice: saleJSON,
		UpstreamSettlementPrice: costJSON,
		BillingExpression:       `tier("default", p * 1 + c * 1)`,
		BillingMultiplier:       0.95, AllowedChannels: `[14]`,
		MaxUpstreamCost: costJSON, FallbackAllowed: false, Enabled: true,
	}
	require.NoError(t, DB.Where("product_type = ? AND model = ? AND price_version = ?", priceRow.ProductType, priceRow.Model, priceRow.PriceVersion).
		FirstOrCreate(priceRow).Error)
	plan := &SubscriptionPlan{
		Title: code, PriceAmount: 49.9, Currency: "CNY",
		DurationUnit: SubscriptionDurationDay, DurationValue: 30, Enabled: true,
		AllowBalancePay: common.GetPointer(true), AllowWalletOverflow: common.GetPointer(false),
		TotalAmount: int64(49.9 * common.QuotaPerUnit), QuotaResetPeriod: SubscriptionResetNever,
		ModelScope: "kimi-k3", Category: SubscriptionCategoryPackage,
		ProductType: ProductTypePackK3, ProductCode: code, ProductFamily: "pack-k3-v1",
		BillingMultiplier: 0.95, PriceVersion: priceVersion,
		AllowedChannels: `{"kimi-k3":[14]}`,
		MaxUpstreamCost: fmt.Sprintf(`{"kimi-k3":%s}`, costJSON),
		FallbackAllowed: false,
	}
	return plan
}

func TestMarginGuardRejectsPlanBelowTemplateFloor(t *testing.T) {
	truncateTables(t)
	setTemplateMinMarginForTest(t, ProductTypePackK3, 10)

	thin := marginGuardK3PlanFixture(t, "k3-margin-thin", "margin-thin-v1",
		PriceComponents{Input: 15, CachedInput: 1.5, Output: 75},
		PriceComponents{Input: 14, CachedInput: 1, Output: 70})
	err := thin.ValidateV1Contract()
	require.ErrorContains(t, err, "gross margin")
	require.ErrorContains(t, err, "kimi-k3")
	require.ErrorContains(t, err, "input")

	healthy := marginGuardK3PlanFixture(t, "k3-margin-healthy", "margin-healthy-v1",
		PriceComponents{Input: 19, CachedInput: 1.9, Output: 95},
		PriceComponents{Input: 14, CachedInput: 1, Output: 70})
	require.NoError(t, healthy.ValidateV1Contract())
}

func TestMarginGuardAcceptsCostExactlyAtFloor(t *testing.T) {
	truncateTables(t)
	setTemplateMinMarginForTest(t, ProductTypePackK3, 10)

	// cost == sale x (1 - 10%) exactly must pass: the floor is inclusive.
	plan := marginGuardK3PlanFixture(t, "k3-margin-exact", "margin-exact-v1",
		PriceComponents{Input: 100, CachedInput: 10, Output: 100},
		PriceComponents{Input: 90, CachedInput: 9, Output: 90})
	require.NoError(t, plan.ValidateV1Contract())
}

func TestMarginGuardSkipsZeroFloorTemplate(t *testing.T) {
	truncateTables(t)
	setTemplateMinMarginForTest(t, ProductTypePackK3, 0)

	plan := marginGuardK3PlanFixture(t, "k3-margin-zero", "margin-zero-v1",
		PriceComponents{Input: 15, CachedInput: 1.5, Output: 75},
		PriceComponents{Input: 14, CachedInput: 1, Output: 70})
	require.NoError(t, plan.ValidateV1Contract())
}

func TestMarginGuardSkipsDisabledPlan(t *testing.T) {
	truncateTables(t)
	setTemplateMinMarginForTest(t, ProductTypePackK3, 10)

	plan := marginGuardK3PlanFixture(t, "k3-margin-draft", "margin-draft-v1",
		PriceComponents{Input: 15, CachedInput: 1.5, Output: 75},
		PriceComponents{Input: 14, CachedInput: 1, Output: 70})
	plan.Enabled = false
	require.NoError(t, plan.ValidateV1Contract())
}

func TestMarginGuardOrderRecheckRejectsThinMargin(t *testing.T) {
	truncateTables(t)
	setTemplateMinMarginForTest(t, ProductTypePackK3, 10)
	user := createV1ContractUser(t, "margin-order-user")

	plan := marginGuardK3PlanFixture(t, "k3-margin-order", "margin-order-v1",
		PriceComponents{Input: 19, CachedInput: 1.9, Output: 95},
		PriceComponents{Input: 14, CachedInput: 1, Output: 70})
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	_, _, err := CreateSubscriptionOrder(user.Id, plan.Id, "margin-order-ok", PaymentMethodAlipay, PaymentProviderAlipay, true)
	require.NoError(t, err)

	// The immutable price row is replaced below the floor through a hookless
	// column update, simulating a catalog drift between plan save and checkout.
	thinSale, err := classroomJSON(PriceComponents{Input: 15, CachedInput: 1.5, Output: 75})
	require.NoError(t, err)
	require.NoError(t, DB.Model(&ProductPrice{}).
		Where("product_type = ? AND model = ? AND price_version = ?", ProductTypePackK3, "kimi-k3", "margin-order-v1").
		UpdateColumn("user_selling_price", thinSale).Error)

	_, _, err = CreateSubscriptionOrder(user.Id, plan.Id, "margin-order-blocked", PaymentMethodAlipay, PaymentProviderAlipay, true)
	require.ErrorContains(t, err, "gross margin")
	require.ErrorContains(t, err, "kimi-k3")
}

func TestMarginGuardTemplateMigrationSetsFloors(t *testing.T) {
	templates, err := ListProductTemplates()
	require.NoError(t, err)
	originals := map[string]ProductTemplate{}
	for _, template := range templates {
		originals[template.ProductType] = template
	}
	t.Cleanup(func() {
		for _, template := range originals {
			current, err := GetProductTemplateByType(template.ProductType)
			if err != nil || current.MinMarginPercent == template.MinMarginPercent {
				continue
			}
			restore := *current
			restore.MinMarginPercent = template.MinMarginPercent
			require.NoError(t, MigrateLockedProductTemplate(&restore))
		}
	})

	require.NoError(t, MigrateProductTemplates())
	for _, productType := range []string{
		ProductTypeMonthlySubscription, ProductTypePackDSGLM, ProductTypePackQwen,
		ProductTypePackK3, ProductTypePackDeepSeekClassroom, ProductTypePackDeepSeekBigfish,
	} {
		template, err := GetProductTemplateByType(productType)
		require.NoError(t, err)
		assert.Equal(t, float64(10), template.MinMarginPercent, "%s floor drifted", productType)
	}
	wallet, err := GetProductTemplateByType(ProductTypeWallet)
	require.NoError(t, err)
	assert.Equal(t, float64(0), wallet.MinMarginPercent, "wallet must keep the margin check disabled")

	// The migration converges locked rows on every boot and stays idempotent.
	require.NoError(t, MigrateProductTemplates())
	monthly, err := GetProductTemplateByType(ProductTypeMonthlySubscription)
	require.NoError(t, err)
	assert.Equal(t, float64(10), monthly.MinMarginPercent)
}

func TestChannelCostVerifiedColumnsExist(t *testing.T) {
	assert.True(t, DB.Migrator().HasColumn(&Channel{}, "cost_verified_at"))
	assert.True(t, DB.Migrator().HasColumn(&Channel{}, "cost_verified_by"))
}

func TestChannelCostExclusionAlert(t *testing.T) {
	costPolicyExclusionAlerts = sync.Map{}
	t.Cleanup(func() { costPolicyExclusionAlerts = sync.Map{} })

	policy := &ChannelSelectionPolicy{
		AllowedChannelIds: map[int]struct{}{7: {}, 8: {}},
		MaxUpstreamCost:   PriceComponents{Input: 1, CachedInput: 1, Output: 1},
		RequireCost:       true,
		ProductType:       ProductTypePackK3,
	}
	overCost := &Channel{Id: 7, UpstreamCosts: `{"kimi-k3":{"input":2,"output":2}}`}
	require.False(t, ChannelSatisfiesPolicy(overCost, "kimi-k3", policy))

	// First exclusion alerts; an immediate repeat of the same
	// product/model/channel triple is throttled.
	require.True(t, noteCostPolicyExclusion(overCost, "kimi-k3", policy))
	require.False(t, noteCostPolicyExclusion(overCost, "kimi-k3", policy))

	// A policy-allowed channel with no cost data for the model alerts too.
	noCost := &Channel{Id: 8}
	require.True(t, noteCostPolicyExclusion(noCost, "kimi-k3", policy))

	// Policies without a cost requirement never alert.
	openPolicy := &ChannelSelectionPolicy{AllowedChannelIds: map[int]struct{}{7: {}}, RequireCost: false}
	require.False(t, noteCostPolicyExclusion(overCost, "kimi-k3", openPolicy))
}

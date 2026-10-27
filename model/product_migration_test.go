package model

import (
	"os"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Restore a production dump into an isolated database before opting in. This
// path deliberately does not call BackfillProductPriceBooks: sold snapshots
// and keys must remain byte-for-byte unchanged by the live-product conversion.
func TestProductionExplicitProductMigration(t *testing.T) {
	if os.Getenv("PRICE_BOOK_PRODUCTION_REHEARSAL") != "1" {
		t.Skip("requires isolated production restore")
	}
	var plans []SubscriptionPlan
	var ordersBefore, ordersAfter []SubscriptionOrder
	var subsBefore, subsAfter []UserSubscription
	var keysBefore, keysAfter []Token
	require.NoError(t, DB.Order("id").Find(&plans).Error)
	require.NoError(t, DB.Order("id").Find(&ordersBefore).Error)
	require.NoError(t, DB.Order("id").Find(&subsBefore).Error)
	require.NoError(t, DB.Unscoped().Order("id").Find(&keysBefore).Error)
	converted, legacy := 0, 0
	for _, plan := range plans {
		if !RequiresStrictV1RoutePolicy(plan.ProductType) {
			legacy++
			continue
		}
		preview, err := MigrateSubscriptionPlanToProduct(DB, plan.Id, plan.ConfigRevision, "", true)
		require.True(t, err == nil, "production product preview failed; do not log private data")
		_, err = MigrateSubscriptionPlanToProduct(DB, plan.Id, plan.ConfigRevision, preview.Fingerprint, false)
		require.True(t, err == nil, "production product conversion failed; do not log private data")
		var actual SubscriptionPlan
		require.NoError(t, DB.First(&actual, plan.Id).Error)
		plan.PriceBook, _ = ProductPriceBook(plan.Id)
		plan.ProductType, plan.TemplateId, plan.ProductFamily, plan.ProductTier = "", 0, "", 0
		plan.BillingMultiplier = 1
		plan.AllowedChannels, plan.MaxUpstreamCost, plan.FallbackAllowed = "", "", false
		plan.ConfigRevision++
		plan.UpdatedAt = actual.UpdatedAt
		require.True(t, reflect.DeepEqual(plan, actual), "commercial product terms changed unexpectedly")
		for _, name := range explicitModelIds(actual.ModelScope) {
			price, err := ResolvePriceBookPrice(DB, actual.PriceBook, name, actual.PriceVersion)
			require.NoError(t, err)
			require.NotNil(t, price)
		}
		converted++
	}
	require.Positive(t, converted)
	require.NoError(t, DB.Order("id").Find(&ordersAfter).Error)
	require.NoError(t, DB.Order("id").Find(&subsAfter).Error)
	require.NoError(t, DB.Unscoped().Order("id").Find(&keysAfter).Error)
	require.True(t, reflect.DeepEqual(ordersBefore, ordersAfter), "orders changed")
	require.True(t, reflect.DeepEqual(subsBefore, subsAfter), "sold entitlements changed")
	require.True(t, reflect.DeepEqual(keysBefore, keysAfter), "keys changed")
	t.Logf("converted=%d legacy-dynamic=%d; historical orders/entitlements/keys identical", converted, legacy)
}

// Called from the existing disposable three-database matrix.
func testExplicitProductMigration(t *testing.T, db *gorm.DB) {
	source := ProductPrice{
		ProductType: ProductTypePackK3, Model: "migration-model", PriceVersion: "migration-v1",
		OfficialReferencePrice: `{"input":2,"output":8}`, UserSellingPrice: `{"input":1.9,"output":7.6}`,
		UpstreamSettlementPrice: `{"input":1,"output":4}`, MaxUpstreamCost: `{"input":1,"output":4}`,
		BillingExpression: `tier("default",p*2+c*8)`, BillingMultiplier: 0.95, AllowedChannels: `[8,9]`, Enabled: true,
	}
	require.NoError(t, db.Create(&source).Error)
	defer db.Delete(&source)
	plan := SubscriptionPlan{
		Id:    6001,
		Title: "Migratable product", ProductType: source.ProductType, ProductCode: "migration-product",
		Currency: "CNY", PriceAmount: 19.9, TotalAmount: 9950000, DurationUnit: SubscriptionDurationDay, DurationValue: 30,
		QuotaResetPeriod: SubscriptionResetNever, ModelScope: source.Model, PriceVersion: source.PriceVersion,
		BillingMultiplier: 0.95, TemplateId: 3, Enabled: true, Listed: true, RedemptionEnabled: false,
		AllowBalancePay: common.GetPointer(false), AllowWalletOverflow: common.GetPointer(false),
	}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&plan).Error)
	defer db.Delete(&plan)
	book, _ := ProductPriceBook(plan.Id)
	defer db.Where("price_book = ?", book).Delete(&PriceBookPrice{})
	order := SubscriptionOrder{PlanId: plan.Id, TradeNo: "migration-frozen-order"}
	order.SnapshotPlan(&plan)
	require.NoError(t, db.Create(&order).Error)
	defer db.Delete(&order)
	sub := UserSubscription{PlanId: plan.Id, UserId: 9981, ProductType: source.ProductType, PriceVersion: source.PriceVersion, ModelScope: source.Model, AmountTotal: 9950000, AmountUsed: 123456}
	require.NoError(t, db.Create(&sub).Error)
	defer db.Delete(&sub)
	key := Token{UserId: sub.UserId, Key: "migration-frozen-key", UserSubscriptionId: sub.Id, ProductType: sub.ProductType, PriceVersion: sub.PriceVersion, UsedQuota: 123456}
	require.NoError(t, db.Create(&key).Error)
	defer db.Unscoped().Delete(&key)

	preview, err := MigrateSubscriptionPlanToProduct(db, plan.Id, 0, "", true)
	require.NoError(t, err)
	require.NotEmpty(t, preview.Fingerprint)
	var count int64
	require.NoError(t, db.Model(&PriceBookPrice{}).Where("price_book = ?", book).Count(&count).Error)
	require.Zero(t, count, "preview must not materialize prices")
	var unchanged SubscriptionPlan
	require.NoError(t, db.First(&unchanged, plan.Id).Error)
	require.Equal(t, plan, unchanged)
	_, err = MigrateSubscriptionPlanToProduct(db, plan.Id, 0, "wrong", false)
	require.ErrorIs(t, err, ErrProductPlanConflict)
	require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).UpdateColumn("title", "concurrent legacy edit").Error)
	_, err = MigrateSubscriptionPlanToProduct(db, plan.Id, 0, preview.Fingerprint, false)
	require.ErrorIs(t, err, ErrProductPlanConflict, "legacy editors do not increment revision")
	require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).UpdateColumn("title", plan.Title).Error)
	require.NoError(t, db.Model(&ProductPrice{}).Where("id = ?", source.Id).UpdateColumn("billing_multiplier", 0.9).Error)
	_, err = MigrateSubscriptionPlanToProduct(db, plan.Id, 0, preview.Fingerprint, false)
	require.ErrorIs(t, err, ErrProductPlanConflict, "a price edit invalidates the reviewed preview")
	require.NoError(t, db.Model(&ProductPrice{}).Where("id = ?", source.Id).UpdateColumn("billing_multiplier", source.BillingMultiplier).Error)
	preview, err = MigrateSubscriptionPlanToProduct(db, plan.Id, 0, "", true)
	require.NoError(t, err)
	result, err := MigrateSubscriptionPlanToProduct(db, plan.Id, 0, preview.Fingerprint, false)
	require.NoError(t, err)
	require.Equal(t, preview, result)
	var migrated SubscriptionPlan
	require.NoError(t, db.First(&migrated, plan.Id).Error)
	require.Equal(t, book, migrated.PriceBook)
	require.Empty(t, migrated.ProductType)
	require.Zero(t, migrated.TemplateId)
	require.EqualValues(t, 1, migrated.ConfigRevision)
	require.Equal(t, plan.PriceAmount, migrated.PriceAmount)
	require.Equal(t, plan.TotalAmount, migrated.TotalAmount)
	require.Equal(t, plan.PriceVersion, migrated.PriceVersion)
	require.Equal(t, plan.RedemptionEnabled, migrated.RedemptionEnabled)
	require.NoError(t, validateProductPlanPrices(db, &migrated, true))
	var actualOrder SubscriptionOrder
	var actualSub UserSubscription
	var actualKey Token
	require.NoError(t, db.First(&actualOrder, order.Id).Error)
	require.NoError(t, db.First(&actualSub, sub.Id).Error)
	require.NoError(t, db.First(&actualKey, key.Id).Error)
	require.Equal(t, order, actualOrder, "pending orders retain their frozen original terms")
	require.Equal(t, sub, actualSub, "migration must not alter sold entitlements")
	require.Equal(t, key, actualKey, "migration must not reset or replace keys")
	require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
		return UpdateLegacySubscriptionPlan(tx, plan.Id, map[string]any{"title": "stale editor"})
	}), ErrProductPlanConflict)
	_, err = MigrateSubscriptionPlanToProduct(db, plan.Id, 0, preview.Fingerprint, false)
	require.Error(t, err, "repeating migration must never duplicate or overwrite")
	require.NoError(t, db.Model(&PriceBookPrice{}).Where("price_book = ?", book).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

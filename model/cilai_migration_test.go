package model

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestPreflightRejectsDirtyLegacyDataWithoutTouchingSchema exercises the
// upgrade gate against a deliberately old-shaped database. No CILAI table or
// column may be created when duplicate active entitlement keys are present.
func TestPreflightRejectsDirtyLegacyDataWithoutTouchingSchema(t *testing.T) {
	oldDB := DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	name := fmt.Sprintf("file:cilai-preflight-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(name), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = oldDB
		common.SetDatabaseTypes(oldMain, oldLog)
	})
	require.NoError(t, db.Exec(`CREATE TABLE user_subscriptions (id INTEGER PRIMARY KEY, status TEXT, end_time INTEGER)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE subscription_plans (id INTEGER PRIMARY KEY, title TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE subscription_orders (id INTEGER PRIMARY KEY, plan_id INTEGER, status TEXT)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE tokens (id INTEGER PRIMARY KEY, user_subscription_id INTEGER, status INTEGER, key TEXT)`).Error)
	now := common.GetTimestamp()
	require.NoError(t, db.Exec(`INSERT INTO user_subscriptions(id,status,end_time) VALUES (1,'active',?)`, now+3600).Error)
	require.NoError(t, db.Exec(`INSERT INTO subscription_plans(id,title) VALUES (1,'legacy')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO subscription_orders(id,plan_id,status) VALUES (1,1,'pending')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO tokens(id,user_subscription_id,status,key) VALUES (1,1,1,'old-a'),(2,1,1,'old-b')`).Error)

	var before int64
	require.NoError(t, db.Raw(`SELECT COUNT(1) FROM sqlite_master WHERE type='table'`).Scan(&before).Error)
	_, err = PreflightCilaiV1Migration()
	require.ErrorIs(t, err, ErrCilaiV1MigrationPreflight)
	var after int64
	require.NoError(t, db.Raw(`SELECT COUNT(1) FROM sqlite_master WHERE type='table'`).Scan(&after).Error)
	require.Equal(t, before, after)
	require.False(t, cilaiTableExists(db, "cilai_v1_migration_states"))
	require.False(t, cilaiColumnExists(db, "tokens", "entitlement_key_lock"))
}

func TestPreflightRejectsDirtyLegacyPostgreSQL(t *testing.T) {
	if common.MainDatabaseType() != common.DatabaseTypePostgreSQL {
		t.Skip("requires CILAI_TEST_POSTGRES_DSN for PostgreSQL old-schema validation")
	}
	// This test is intended for a disposable database supplied through
	// CILAI_TEST_POSTGRES_DSN. Remove only the fixture tables, then recreate the
	// minimal old shape; no application data is in that database.
	require.NoError(t, DB.Exec(`DROP TABLE IF EXISTS cilai_v1_migration_states, subscription_orders, subscription_plans, tokens, user_subscriptions CASCADE`).Error)
	require.NoError(t, DB.Exec(`CREATE TABLE user_subscriptions (id INTEGER PRIMARY KEY, status TEXT, end_time BIGINT)`).Error)
	require.NoError(t, DB.Exec(`CREATE TABLE subscription_plans (id INTEGER PRIMARY KEY, title TEXT)`).Error)
	require.NoError(t, DB.Exec(`CREATE TABLE subscription_orders (id INTEGER PRIMARY KEY, plan_id INTEGER, status TEXT)`).Error)
	require.NoError(t, DB.Exec(`CREATE TABLE tokens (id INTEGER PRIMARY KEY, user_subscription_id INTEGER, status INTEGER, key TEXT)`).Error)
	now := common.GetTimestamp()
	require.NoError(t, DB.Exec(`INSERT INTO user_subscriptions(id,status,end_time) VALUES (1,'active',?)`, now+3600).Error)
	require.NoError(t, DB.Exec(`INSERT INTO subscription_plans(id,title) VALUES (1,'legacy')`).Error)
	require.NoError(t, DB.Exec(`INSERT INTO subscription_orders(id,plan_id,status) VALUES (1,1,'pending')`).Error)
	require.NoError(t, DB.Exec(`INSERT INTO tokens(id,user_subscription_id,status,key) VALUES (1,1,1,'old-a'),(2,1,1,'old-b')`).Error)
	_, err := PreflightCilaiV1Migration()
	require.ErrorIs(t, err, ErrCilaiV1MigrationPreflight)
	require.False(t, cilaiTableExists(DB, "cilai_v1_migration_states"))
	require.False(t, cilaiColumnExists(DB, "tokens", "entitlement_key_lock"))
}

func TestLegacyZeroKeySubscriptionIsNeverBackfilledAsWallet(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "legacy-zero-key-user")
	plan := &SubscriptionPlan{
		Title: "old monthly plan", PriceAmount: 19.9, Currency: "CNY",
		DurationUnit: SubscriptionDurationDay, DurationValue: 30,
		TotalAmount: 500000, ModelScope: "legacy-model-*", Category: SubscriptionCategorySubscription,
		Enabled: true,
	}
	require.NoError(t, DB.Create(plan).Error)
	sub := &UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: plan.TotalAmount,
		StartTime: common.GetTimestamp() - 60, EndTime: common.GetTimestamp() + 3600, Status: "active"}
	require.NoError(t, DB.Create(sub).Error)
	require.NoError(t, MigrateCilaiV1Contracts())
	var token Token
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).First(&token).Error)
	require.Equal(t, ProductTypeLegacySubscription, token.ProductType)
	require.Equal(t, ProductTypeLegacySubscription, token.EntitlementType)
	require.NotEqual(t, ProductTypeWallet, token.ProductType)
	var binding SubscriptionKeyBinding
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).First(&binding).Error)
	var beforeTokens int64
	require.NoError(t, DB.Model(&Token{}).Where("user_subscription_id = ?", sub.Id).Count(&beforeTokens).Error)
	require.NoError(t, MigrateCilaiV1Contracts())
	var afterTokens int64
	require.NoError(t, DB.Model(&Token{}).Where("user_subscription_id = ?", sub.Id).Count(&afterTokens).Error)
	require.Equal(t, beforeTokens, afterTokens)
}

func TestLegacyUnknownOrderStatusIsParkedForManualReview(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "legacy-unknown-order-user")
	plan := &SubscriptionPlan{Title: "old plan", PriceAmount: 9.9, Currency: "CNY", DurationUnit: SubscriptionDurationDay, DurationValue: 30, TotalAmount: 100, Enabled: true}
	require.NoError(t, DB.Create(plan).Error)
	order := &SubscriptionOrder{UserId: user.Id, PlanId: plan.Id, Money: plan.PriceAmount, TradeNo: "legacy-unknown-status", Status: "mystery"}
	require.NoError(t, DB.Create(order).Error)
	require.NoError(t, MigrateCilaiV1Contracts())
	var migrated SubscriptionOrder
	require.NoError(t, DB.First(&migrated, order.Id).Error)
	require.Equal(t, LegacyOrderStateReview, migrated.LegacyCompatibilityState)
	require.Equal(t, ProvisioningStatusLegacyReview, migrated.ProvisioningStatus)
	require.Contains(t, migrated.LegacyReviewReason, "unknown status")
}

func TestLegacySuccessfulOrderWithoutLinkIsAcknowledgedForReview(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "legacy-success-review-user")
	order := &SubscriptionOrder{UserId: user.Id, Money: 10, TradeNo: "legacy-success-review", Status: common.TopUpStatusSuccess,
		ProductType: ProductTypeLegacySubscription, LegacyCompatibilityState: LegacyOrderStateReview,
		ProvisioningStatus: ProvisioningStatusLegacyReview, PaymentProvider: PaymentProviderAlipay, PaymentMethod: PaymentMethodAlipay}
	require.NoError(t, DB.Create(order).Error)
	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, `{"legacy_callback":true}`, PaymentProviderAlipay, PaymentMethodAlipay))
	var after SubscriptionOrder
	require.NoError(t, DB.First(&after, order.Id).Error)
	require.Nil(t, after.UserSubscriptionId)
	require.Equal(t, ProvisioningStatusLegacyReview, after.ProvisioningStatus)
}

func TestEntitlementKeyBoundaryRejectsSecondActiveKey(t *testing.T) {
	truncateTables(t)
	user := createV1ContractUser(t, "one-key-boundary-user")
	plan := v1PackK3PlanFixture(t, "one-key-pack", 49.9)
	require.NoError(t, DB.Create(plan).Error)
	sub := &UserSubscription{UserId: user.Id, PlanId: plan.Id, AmountTotal: plan.TotalAmount,
		StartTime: common.GetTimestamp() - 60, EndTime: common.GetTimestamp() + 3600, Status: "active",
		ProductType: plan.ProductType, ProductCode: plan.ProductCode, ProductFamily: plan.ProductFamily,
		BillingMultiplier: plan.BillingMultiplier, PriceVersion: plan.PriceVersion, ModelScope: plan.ModelScope,
		AllowedChannels: plan.AllowedChannels, MaxUpstreamCost: plan.MaxUpstreamCost}
	require.NoError(t, DB.Create(sub).Error)
	first := &Token{UserId: user.Id, UserSubscriptionId: sub.Id, Key: "bound-one", Status: common.TokenStatusEnabled}
	require.NoError(t, DB.Create(first).Error)
	second := &Token{UserId: user.Id, UserSubscriptionId: sub.Id, Key: "bound-two", Status: common.TokenStatusEnabled}
	require.Error(t, DB.Create(second).Error)
	require.NoError(t, EnsureEntitlementKeyUniqueIndex())
	require.Error(t, DB.Exec(`INSERT INTO tokens(user_id,user_subscription_id,status,key,entitlement_key_lock) VALUES (?,?,?,?,?)`, user.Id, sub.Id, 1, "bound-three", fmt.Sprintf("subscription:%d", sub.Id)).Error)
	require.NoError(t, DB.Exec(`INSERT INTO tokens(user_id,user_subscription_id,status,key,entitlement_key_lock) VALUES (?,?,?,?,?)`, user.Id, sub.Id, 1, "rogue-unbound", "rogue-lock").Error)
	_, err := ValidateUserToken("rogue-unbound")
	require.ErrorIs(t, err, ErrTokenInvalid)
}

func TestLegacyModelScopeRetainsPrefixSemantics(t *testing.T) {
	require.True(t, modelScopeMatchesForProduct("legacy-model-*", "legacy-model-v2", ProductTypeLegacySubscription))
	require.False(t, modelScopeMatchesForProduct("legacy-model-*", "legacy-model-v2", ProductTypeMonthlySubscription))
}

func TestMonthlyPurchaseConcurrencyPostgreSQL(t *testing.T) {
	if common.MainDatabaseType() != common.DatabaseTypePostgreSQL {
		t.Skip("requires CILAI_TEST_POSTGRES_DSN for real row-lock concurrency")
	}
	truncateTables(t)
	user := createV1ContractUser(t, "postgres-concurrent-monthly")
	plan := v1MonthlyPlanFixture(t, "postgres-monthly", 1, 19.9)
	require.NoError(t, DB.Create(plan).Error)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- DB.Transaction(func(tx *gorm.DB) error {
				_, _, err := createProvisionedSubscriptionFromPlanTx(tx, user.Id, plan, "concurrency")
				return err
			})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	var cycleCount int64
	require.NoError(t, DB.Model(&MonthlySubscriptionCycle{}).Where("user_id = ?", user.Id).Count(&cycleCount).Error)
	require.EqualValues(t, 1, cycleCount)
}

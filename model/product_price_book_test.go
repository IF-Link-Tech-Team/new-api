package model

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The server DSNs must name disposable databases dedicated to this test. The
// normal model TestMain database is never used or reset by this matrix.
func TestProductPriceBookMigration(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var dialect gorm.Dialector
			switch engine {
			case "sqlite":
				dialect = sqlite.Open(filepath.Join(t.TempDir(), "books.db"))
			case "mysql":
				dsn := os.Getenv("PRICE_BOOK_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("PRICE_BOOK_TEST_MYSQL_DSN not configured")
				}
				dialect = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("PRICE_BOOK_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("PRICE_BOOK_TEST_POSTGRES_DSN not configured")
				}
				// Production migrations use simple protocol: cached SELECT * plans
				// cannot survive the deliberate old-schema expansion in this test.
				dialect = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(engine))
			t.Cleanup(func() {
				DB = oldDB
				common.SetMainDatabaseType(oldType)
			})
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			tables := []any{&PriceBookPrice{}, &ProductPrice{}, &SubscriptionPlan{}, &SubscriptionOrder{}, &UserSubscription{}, &Token{}}
			require.NoError(t, db.Migrator().DropTable(tables...))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })

			// Start with the released catalog/order/entitlement schema, then add
			// only the new table. No existing columns or indexes are replaced.
			require.NoError(t, db.AutoMigrate(tables[1:]...))
			wallet := ProductPrice{
				ProductType: ProductTypeWallet, Model: "operator-model", PriceVersion: "v1-test",
				OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
				UserSellingPrice:        `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
				UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`,
				BillingExpression:       `tier("base", p + cr * 0.2 + cc + c * 4)`,
				BillingMultiplier:       1, AllowedChannels: `[8,9]`,
				MaxUpstreamCost: `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`,
				EffectiveAt:     12345, Enabled: true,
			}
			require.NoError(t, db.Create(&wallet).Error)
			require.NoError(t, db.Model(&wallet).UpdateColumns(map[string]any{"created_at": 0, "updated_at": 0}).Error)
			require.NoError(t, db.First(&wallet, wallet.Id).Error)
			pack := wallet
			pack.Id, pack.ProductType, pack.BillingMultiplier = 0, ProductTypePackK3, 0.95
			require.NoError(t, db.Create(&pack).Error)
			historical := pack
			historical.Id, historical.PriceVersion, historical.Enabled = 0, "old-disabled", false
			require.NoError(t, db.Create(&historical).Error)
			unrelated := pack
			unrelated.Id, unrelated.Model = 0, "not-in-this-product-scope"
			require.NoError(t, db.Create(&unrelated).Error)
			for _, planId := range []int{41, 42} {
				plan := SubscriptionPlan{Id: planId, Title: "configured product", ProductType: ProductTypePackK3,
					TotalAmount: 9_000_000_000_000, PriceVersion: pack.PriceVersion, ModelScope: pack.Model}
				require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&plan).Error)
			}
			// Deleted/changed plans cannot make their paid historical versions
			// disappear. Both orders and entitlements supply their own identity.
			sub := UserSubscription{PlanId: 43, ProductType: ProductTypePackK3, PriceVersion: historical.PriceVersion,
				ModelScope: pack.Model, AmountTotal: 9_000_000_000_000, AmountUsed: 1_234_567}
			require.NoError(t, db.Create(&sub).Error)
			boundToken := Token{UserId: 1, Key: "entitlement-key", UserSubscriptionId: sub.Id, ProductType: ProductTypePackK3}
			require.NoError(t, db.Create(&boundToken).Error)
			walletToken := Token{UserId: 2, Key: "wallet-key", ProductType: ProductTypeWallet}
			require.NoError(t, db.Create(&walletToken).Error)
			order := SubscriptionOrder{PlanId: 44, ProductType: ProductTypePackK3, PriceVersion: historical.PriceVersion,
				ModelScope: pack.Model, TradeNo: "price-book-history", Money: 19.9}
			require.NoError(t, db.Create(&order).Error)

			// Emulate the released schema while retaining the representative
			// rows, then expand twice. These additions may not mutate old data.
			require.NoError(t, db.Migrator().DropColumn(&SubscriptionPlan{}, "PriceBook"))
			require.NoError(t, db.Migrator().DropColumn(&SubscriptionPlan{}, "ConfigRevision"))
			require.NoError(t, db.Migrator().DropColumn(&SubscriptionPlan{}, "ReservationTTLSeconds"))
			require.NoError(t, db.Migrator().DropColumn(&SubscriptionOrder{}, "ProductPlanSnapshot"))
			require.NoError(t, db.Migrator().DropColumn(&SubscriptionOrder{}, "ReservationExpiresAt"))
			for range 2 {
				require.NoError(t, db.AutoMigrate(&PriceBookPrice{}, &SubscriptionPlan{}, &SubscriptionOrder{}))
				require.NoError(t, BackfillProductPriceBooks(db))
			}
			var entries []PriceBookPrice
			require.NoError(t, db.Order("price_book, model, price_version").Find(&entries).Error)
			require.Len(t, entries, 9)
			for _, entry := range entries {
				expected := pack
				if entry.PriceBook == ProductTypeWallet {
					expected = wallet
				} else if entry.PriceVersion == historical.PriceVersion {
					expected = historical
				}
				expected.Id, expected.ProductType = 0, ""
				assert.Equal(t, expected, entry.productPrice(), "all price/route/time fields must survive")
			}
			var afterSub UserSubscription
			require.NoError(t, db.First(&afterSub, sub.Id).Error)
			sub.PriceBook = "product:43"
			assert.Equal(t, sub, afterSub)
			var afterOrder SubscriptionOrder
			require.NoError(t, db.First(&afterOrder, order.Id).Error)
			order.PriceBook = "product:44"
			assert.Equal(t, order, afterOrder)
			var afterBoundToken, afterWalletToken Token
			require.NoError(t, db.First(&afterBoundToken, boundToken.Id).Error)
			require.NoError(t, db.First(&afterWalletToken, walletToken.Id).Error)
			assert.Equal(t, "product:43", afterBoundToken.PriceBook)
			assert.Equal(t, WalletPriceBook, afterWalletToken.PriceBook)
			var legacyCount int64
			require.NoError(t, db.Model(&ProductPrice{}).Count(&legacyCount).Error)
			assert.EqualValues(t, 4, legacyCount)
			assert.True(t, db.Migrator().HasIndex(&ProductPrice{}, "idx_product_price_version"))
			assert.True(t, db.Migrator().HasIndex(&PriceBookPrice{}, "idx_price_book_version"))
			require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", 41).UpdateColumn("price_version", "missing-version").Error)
			require.ErrorContains(t, BackfillProductPriceBooks(db), "migration missing price")
			require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", 41).UpdateColumn("price_version", pack.PriceVersion).Error)
			missingWallet, err := ResolvePriceBookPrice(db, WalletPriceBook, "unconfigured-wallet-model", "")
			require.NoError(t, err)
			assert.Nil(t, missingWallet, "only an unconfigured wallet model may use global pricing")
			currentWallet, err := ResolvePriceBookPrice(db, WalletPriceBook, wallet.Model, "")
			require.NoError(t, err)
			require.NotNil(t, currentWallet)
			assert.Equal(t, wallet.PriceVersion, currentWallet.PriceVersion)
			paidPrice, err := ResolvePriceBookPrice(db, "product:43", pack.Model, historical.PriceVersion)
			require.NoError(t, err)
			assert.False(t, paidPrice.Enabled, "paid snapshots stay readable after disabling sales")
			t.Run("explicit live product conversion preserves historical business data", func(t *testing.T) { testExplicitProductMigration(t, db) })
			_, err = ResolvePriceBookPrice(db, "product:43", pack.Model, "")
			require.ErrorContains(t, err, "frozen version")
			_, err = ResolvePriceBookPrice(db, "product:43", pack.Model, "missing-version")
			require.ErrorContains(t, err, "unavailable")
			future := *currentWallet
			future.Id, future.PriceVersion, future.EffectiveAt = 0, "wallet-future", GetDBTimestamp()+3600
			require.NoError(t, db.Create(&future).Error)
			currentWallet, err = ResolvePriceBookPrice(db, WalletPriceBook, wallet.Model, "")
			require.NoError(t, err)
			assert.Equal(t, wallet.PriceVersion, currentWallet.PriceVersion)
			require.NoError(t, db.Model(currentWallet).Update("enabled", false).Error)
			_, err = ResolvePriceBookPrice(db, WalletPriceBook, wallet.Model, "")
			require.ErrorContains(t, err, "unavailable", "disabled/future-only wallet prices must not fall back")
			require.NoError(t, db.Model(currentWallet).Update("enabled", true).Error)

			duplicate := entries[0]
			duplicate.Id = 0
			require.Error(t, db.Create(&duplicate).Error)
			immutable := entries[0]
			immutable.BillingExpression = `tier("changed", p * 2 + c * 8)`
			require.ErrorContains(t, db.Save(&immutable).Error, "immutable")
			require.ErrorContains(t, db.Model(&entries[0]).Updates(map[string]any{"billing_multiplier": 0.1}).Error, "immutable")
			require.ErrorContains(t, db.Model(&PriceBookPrice{}).Where("id = ?", entries[0].Id).Update("enabled", false).Error, "individual version id")

			t.Run("transactional administrative dual write", func(t *testing.T) {
				newPrice := pack
				newPrice.Id, newPrice.PriceVersion = 0, "admin-v2"
				require.NoError(t, CreateLegacyProductPrice(db, &newPrice))
				for _, book := range []string{"product:41", "product:42", "product:43", "product:44"} {
					price, err := ResolvePriceBookPrice(db, book, pack.Model, newPrice.PriceVersion)
					require.NoError(t, err)
					assert.Equal(t, pack.BillingExpression, price.BillingExpression)
				}
				newPlan := SubscriptionPlan{Id: 45, ProductType: pack.ProductType, ModelScope: pack.Model, PriceVersion: newPrice.PriceVersion}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Session(&gorm.Session{SkipHooks: true}).Create(&newPlan).Error; err != nil {
						return err
					}
					return SyncSubscriptionPlanPriceBook(tx, &newPlan)
				}))
				var newPlanRows []PriceBookPrice
				require.NoError(t, db.Where("price_book = ?", "product:45").Find(&newPlanRows).Error)
				require.Len(t, newPlanRows, 3)
				for _, price := range newPlanRows {
					assert.Equal(t, pack.Model, price.Model, "new plans cannot inherit other models of the same old type")
				}
				incompletePlan := newPlan
				incompletePlan.Id, incompletePlan.PriceVersion = 46, "missing-version"
				require.ErrorContains(t, db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Session(&gorm.Session{SkipHooks: true}).Create(&incompletePlan).Error; err != nil {
						return err
					}
					return SyncSubscriptionPlanPriceBook(tx, &incompletePlan)
				}), "missing price")
				var incompleteCount int64
				require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", incompletePlan.Id).Count(&incompleteCount).Error)
				assert.Zero(t, incompleteCount)
				require.NoError(t, db.Model(&PriceBookPrice{}).Where("price_book = ?", "product:46").Count(&incompleteCount).Error)
				assert.Zero(t, incompleteCount, "failed plan saves must not leave partially materialized prices")
				// Only a sale switch may be mirrored into an existing version.
				if engine != "sqlite" {
					start, results := make(chan struct{}), make(chan error, 2)
					for _, enabled := range []bool{true, false} {
						go func() {
							<-start
							results <- SetLegacyProductPriceEnabled(db, newPrice.Id, enabled)
						}()
					}
					close(start)
					for range 2 {
						require.NoError(t, <-results)
					}
					var source ProductPrice
					require.NoError(t, db.First(&source, newPrice.Id).Error)
					var targets []PriceBookPrice
					require.NoError(t, db.Where("model = ? AND price_version = ?", newPrice.Model, newPrice.PriceVersion).Find(&targets).Error)
					require.Len(t, targets, 5)
					for _, target := range targets {
						assert.Equal(t, source.Enabled, target.Enabled, "competing administrators must leave both catalogs consistent")
					}
				}
				require.NoError(t, SetLegacyProductPriceEnabled(db, newPrice.Id, false))
				paid, err := ResolvePriceBookPrice(db, "product:45", pack.Model, newPrice.PriceVersion)
				require.NoError(t, err)
				assert.False(t, paid.Enabled)
				assert.Equal(t, pack.BillingExpression, paid.BillingExpression)
				// A conflict in one target must roll back every source/target write.
				require.NoError(t, db.Model(&PriceBookPrice{}).Where("price_book = ? AND price_version = ?", "product:42", newPrice.PriceVersion).
					UpdateColumn("billing_expression", `tier("conflict", p * 9)`).Error)
				require.ErrorContains(t, SetLegacyProductPriceEnabled(db, newPrice.Id, true), "dual-write conflict")
				var afterPrice ProductPrice
				require.NoError(t, db.First(&afterPrice, newPrice.Id).Error)
				assert.False(t, afterPrice.Enabled)
				paid, err = ResolvePriceBookPrice(db, "product:41", pack.Model, newPrice.PriceVersion)
				require.NoError(t, err)
				assert.False(t, paid.Enabled)
				// Restore the deliberate conflict for the remaining backfill checks.
				require.NoError(t, db.Model(&PriceBookPrice{}).Where("price_book = ? AND price_version = ?", "product:42", newPrice.PriceVersion).
					UpdateColumn("billing_expression", pack.BillingExpression).Error)
			})

			t.Run("product scoped version publication", func(t *testing.T) {
				var plansBefore []SubscriptionPlan
				require.NoError(t, db.Order("id").Find(&plansBefore).Error)
				var sourcesBefore []ProductPrice
				require.NoError(t, db.Order("id").Find(&sourcesBefore).Error)
				generic := entries[0]
				generic.PriceBook, generic.Model, generic.PriceVersion = "", "new-provider-model", "operator-scoped-v1"
				generic.BillingMultiplier, generic.FallbackAllowed = 0.73, true
				generic.Id, generic.CreatedAt, generic.UpdatedAt = 99, 1, 1
				require.NoError(t, CreateProductPriceBookVersion(db, 41, &generic))
				assert.Equal(t, "product:41", generic.PriceBook)
				assert.NotEqual(t, 99, generic.Id)
				assert.Greater(t, generic.CreatedAt, int64(1))
				duplicate := generic
				duplicate.BillingMultiplier = 0.99
				require.ErrorIs(t, CreateProductPriceBookVersion(db, 41, &duplicate), ErrPriceBookVersionExists)
				stored, err := ResolvePriceBookPrice(db, generic.PriceBook, generic.Model, generic.PriceVersion)
				require.NoError(t, err)
				assert.Equal(t, generic, *stored, "duplicate publication must not replace price or route terms")
				other := generic
				other.PriceBook, other.BillingMultiplier = "", 0.81
				require.NoError(t, CreateProductPriceBookVersion(db, 42, &other))
				assert.Equal(t, "product:42", other.PriceBook)
				assert.NotEqual(t, generic.Id, other.Id)
				for _, book := range []string{"wallet", "product:42", "product:041"} {
					invalid := generic
					invalid.PriceBook = book
					require.Error(t, CreateProductPriceBookVersion(db, 41, &invalid), book)
				}
				missing := generic
				missing.PriceBook = ""
				require.ErrorIs(t, CreateProductPriceBookVersion(db, 999, &missing), gorm.ErrRecordNotFound)
				assert.Empty(t, missing.PriceBook, "failed writes must not mutate the caller's snapshot")
				require.Error(t, CreateProductPriceBookVersion(db, 0, &missing))
				require.Error(t, CreateProductPriceBookVersion(db, 41, nil))
				if engine != "sqlite" {
					start, results := make(chan struct{}), make(chan error, 2)
					for range 2 {
						go func() {
							candidate := generic
							candidate.PriceVersion = "competing-publish"
							<-start
							results <- CreateProductPriceBookVersion(db, 41, &candidate)
						}()
					}
					close(start)
					first, second := <-results, <-results
					if first == nil {
						require.ErrorIs(t, second, ErrPriceBookVersionExists)
					} else {
						require.ErrorIs(t, first, ErrPriceBookVersionExists)
						require.NoError(t, second)
					}
				}
				var plansAfter []SubscriptionPlan
				require.NoError(t, db.Order("id").Find(&plansAfter).Error)
				assert.Equal(t, plansBefore, plansAfter, "publishing must not select a new sales version")
				var sourcesAfter []ProductPrice
				require.NoError(t, db.Order("id").Find(&sourcesAfter).Error)
				assert.Equal(t, sourcesBefore, sourcesAfter, "product-specific prices must not change the legacy type-wide catalog")
			})

			t.Run("transactional plans bypass display cache", func(t *testing.T) {
				require.NoError(t, db.AutoMigrate(&User{}))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&User{})) })
				require.NoError(t, db.Create(&User{Id: 123, Username: "display-cache-buyer", Status: common.UserStatusEnabled, Group: "default"}).Error)
				for _, id := range []int{51, 52} {
					InvalidateSubscriptionPlanCache(id)
					t.Cleanup(func() { InvalidateSubscriptionPlanCache(id) })
					plan := SubscriptionPlan{Id: id, Title: "committed plan", Enabled: true, PriceAmount: 10}
					require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&plan).Error)
				}
				cached, err := GetSubscriptionPlanById(51)
				require.NoError(t, err)
				require.True(t, cached.Enabled)
				// Model cache invalidation can lag a write from another process.
				require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", 51).UpdateColumns(map[string]any{
					"enabled": false, "price_amount": 12,
				}).Error)
				order, _, err := CreateSubscriptionOrder(123, 51, "cached-disabled-plan", "alipay", "alipay", true)
				require.ErrorContains(t, err, "套餐未启用")
				assert.Nil(t, order, "a stale enabled cache must not permit checkout")
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
					fresh, err := getSubscriptionPlanByIdTx(tx, 51)
					require.NoError(t, err)
					assert.False(t, fresh.Enabled, "transactional sales must not trust the display cache")
					assert.Equal(t, float64(12), fresh.PriceAmount)
					return nil
				}))
				rollback := assert.AnError
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					if err := tx.Model(&SubscriptionPlan{}).Where("id = ?", 52).UpdateColumn("title", "uncommitted plan").Error; err != nil {
						return err
					}
					inTransaction, err := getSubscriptionPlanByIdTx(tx, 52)
					require.NoError(t, err)
					assert.Equal(t, "uncommitted plan", inTransaction.Title)
					return rollback
				}), rollback)
				committed, err := GetSubscriptionPlanById(52)
				require.NoError(t, err)
				assert.Equal(t, "committed plan", committed.Title, "rolled-back data must never enter shared caches")
			})

			t.Run("generic product purchase and immutable issuance", func(t *testing.T) {
				extra := []any{&SubscriptionKeyBinding{}, &TopUp{}, &User{}}
				require.NoError(t, db.AutoMigrate(extra...))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(extra...)) })
				user := User{Id: 901, Username: "generic-product-buyer", Status: common.UserStatusEnabled, Group: "default"}
				require.NoError(t, db.Create(&user).Error)
				plan := SubscriptionPlan{
					Title: "Operator configured promotion", Currency: "CNY", PriceAmount: 17.25,
					TotalAmount: 20_000_000, DurationUnit: SubscriptionDurationDay, DurationValue: 37,
					QuotaResetPeriod: SubscriptionResetDaily, MaxPurchasePerUser: 0,
					ModelScope: "arbitrary-provider-model", BillingMultiplier: 1,
				}
				require.NoError(t, CreateProductPlan(db, &plan))
				assert.False(t, plan.Enabled)
				assert.False(t, plan.Listed)
				assert.False(t, plan.RedemptionEnabled, "GORM defaults must not make a draft redeemable")
				assert.EqualValues(t, 1, plan.ConfigRevision)
				assert.Empty(t, plan.ProductType)
				assert.Zero(t, plan.TemplateId)
				var stored SubscriptionPlan
				require.NoError(t, db.First(&stored, plan.Id).Error)
				assert.Equal(t, plan, stored)
				invalid := plan
				invalid.PriceBook = WalletPriceBook
				require.Error(t, invalid.ValidateProductPlan())
				invalid = plan
				invalid.ProductType = ProductTypePackK3
				require.Error(t, invalid.ValidateProductPlan())
				// A draft cannot be activated without all of its model prices.
				plan.Enabled, plan.PriceVersion = true, "generic-sale-v1"
				require.Error(t, UpdateProductPlan(db, &plan, 1))
				price := entries[0]
				price.Id, price.PriceBook, price.Model, price.PriceVersion = 0, "", plan.ModelScope, plan.PriceVersion
				price.Enabled, price.EffectiveAt, price.BillingMultiplier = true, 0, 0.73
				require.NoError(t, CreateProductPriceBookVersion(db, plan.Id, &price))
				require.NoError(t, UpdateProductPlan(db, &plan, 1))
				assert.EqualValues(t, 2, plan.ConfigRevision)
				unsafeInsert := SubscriptionOrder{PlanId: plan.Id, UserId: user.Id, TradeNo: "generic-unsafe-" + engine}
				require.ErrorContains(t, unsafeInsert.Insert(), "transactional checkout")
				stale := stored
				stale.Title = "stale editor must not win"
				require.ErrorIs(t, UpdateProductPlan(db, &stale, 1), ErrProductPlanConflict)
				order, _, err := CreateSubscriptionOrder(user.Id, plan.Id, "generic-purchase-"+engine, "alipay", "alipay", true)
				require.NoError(t, err)
				assert.Equal(t, plan.PriceBook, order.PriceBook)
				require.NotEmpty(t, order.ProductPlanSnapshot)
				// Change the live quota, validity, models, overflow and limit after
				// payment starts. Issuance must use the complete original contract.
				require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).Updates(map[string]any{
					"total_amount": 1, "duration_value": 1, "model_scope": "different-model",
					"allow_wallet_overflow": true, "max_purchase_per_user": 1, "enabled": false,
				}).Error)
				require.NoError(t, db.Model(&price).Update("enabled", false).Error)
				for range 2 {
					require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, "", "alipay", "alipay"))
				}
				var subscriptions []UserSubscription
				require.NoError(t, db.Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).Find(&subscriptions).Error)
				require.Len(t, subscriptions, 1)
				sub := subscriptions[0]
				assert.Equal(t, plan.TotalAmount, sub.AmountTotal)
				assert.Equal(t, plan.ModelScope, sub.ModelScope)
				assert.Equal(t, plan.PriceBook, sub.PriceBook)
				assert.Equal(t, plan.PriceVersion, sub.PriceVersion)
				assert.False(t, sub.AllowWalletOverflow)
				assert.EqualValues(t, 37*24*60*60, sub.EndTime-sub.StartTime)
				var bindings []SubscriptionKeyBinding
				require.NoError(t, db.Where("user_subscription_id = ?", sub.Id).Find(&bindings).Error)
				require.Len(t, bindings, 1)
				var token Token
				require.NoError(t, db.First(&token, bindings[0].TokenId).Error)
				assert.Equal(t, sub.Id, token.UserSubscriptionId)
				assert.Equal(t, plan.PriceBook, token.PriceBook)
				assert.Equal(t, plan.PriceVersion, token.PriceVersion)
				// Re-running startup must not reinterpret generic orders as legacy.
				require.NoError(t, backfillCilaiV1Contracts())
				var after SubscriptionOrder
				require.NoError(t, db.First(&after, order.Id).Error)
				assert.Empty(t, after.LegacyCompatibilityState)
				assert.Equal(t, order.ProductPlanSnapshot, after.ProductPlanSnapshot)
				// Unlimited means actual multiple purchases, each with its own
				// entitlement and key, rather than one shared quota pool.
				require.NoError(t, db.Model(&price).Update("enabled", true).Error)
				require.NoError(t, UpdateProductPlan(db, &plan, 2))
				second, _, err := CreateSubscriptionOrder(user.Id, plan.Id, "generic-second-"+engine, "alipay", "alipay", true)
				require.NoError(t, err)
				require.NoError(t, CompleteSubscriptionOrder(second.TradeNo, "", "alipay", "alipay"))
				require.NoError(t, db.Where("user_id = ? AND plan_id = ?", user.Id, plan.Id).Find(&subscriptions).Error)
				require.Len(t, subscriptions, 2)
				var tokens []Token
				require.NoError(t, db.Where("user_subscription_id IN ?", []int{subscriptions[0].Id, subscriptions[1].Id}).Find(&tokens).Error)
				require.Len(t, tokens, 2)
				assert.NotEqual(t, tokens[0].Key, tokens[1].Key)
				assert.NotEqual(t, tokens[0].UserSubscriptionId, tokens[1].UserSubscriptionId)
				if engine != "sqlite" {
					start, results := make(chan struct{}), make(chan error, 2)
					for _, title := range []string{"admin A", "admin B"} {
						candidate := plan
						candidate.Title = title
						go func() {
							<-start
							results <- UpdateProductPlan(db, &candidate, 3)
						}()
					}
					close(start)
					first, second := <-results, <-results
					if first == nil {
						require.ErrorIs(t, second, ErrProductPlanConflict)
					} else {
						require.ErrorIs(t, first, ErrProductPlanConflict)
						require.NoError(t, second)
					}
				}
				// Sale switches share the product revision with the editor. They
				// cannot mutate another product or race a stale configuration save.
				require.NoError(t, db.First(&plan, plan.Id).Error)
				_, _, err = SetProductPriceBookVersionEnabled(db, 41, entries[0].Id, false, 1)
				require.ErrorContains(t, err, "explicit migration")
				_, _, err = SetProductPriceBookVersionEnabled(db, plan.Id, entries[0].Id, false, plan.ConfigRevision)
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				_, _, err = SetProductPriceBookVersionEnabled(db, plan.Id, price.Id, false, plan.ConfigRevision-1)
				require.ErrorIs(t, err, ErrProductPlanConflict)
				// A failure after updating the price must roll the entire change
				// back, including the sale switch and the configuration revision.
				const callback = "test:price-switch-plan-failure"
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "subscription_plans" {
						tx.AddError(errors.New("injected revision write failure"))
					}
				}))
				_, _, err = SetProductPriceBookVersionEnabled(db, plan.Id, price.Id, false, plan.ConfigRevision)
				require.NoError(t, db.Callback().Update().Remove(callback))
				require.ErrorContains(t, err, "injected revision write failure")
				var unchanged PriceBookPrice
				require.NoError(t, db.First(&unchanged, price.Id).Error)
				assert.True(t, unchanged.Enabled)
				disabled, revision, err := SetProductPriceBookVersionEnabled(db, plan.Id, price.Id, false, plan.ConfigRevision)
				require.NoError(t, err)
				assert.False(t, disabled.Enabled)
				assert.Equal(t, plan.ConfigRevision+1, revision)
				_, _, err = CreateSubscriptionOrder(user.Id, plan.Id, "generic-disabled-"+engine, "alipay", "alipay", true)
				require.ErrorContains(t, err, "disabled or not yet effective")
				historicalPrice, err := ResolvePriceBookPrice(db, sub.PriceBook, sub.ModelScope, sub.PriceVersion)
				require.NoError(t, err)
				assert.Equal(t, price.BillingExpression, historicalPrice.BillingExpression)
				assert.False(t, historicalPrice.Enabled, "paid entitlement retains access to its immutable price")
				if engine != "sqlite" {
					start, results := make(chan struct{}), make(chan error, 2)
					for _, enabled := range []bool{true, false} {
						go func() {
							<-start
							_, _, err := SetProductPriceBookVersionEnabled(db, plan.Id, price.Id, enabled, revision)
							results <- err
						}()
					}
					close(start)
					first, second := <-results, <-results
					if first == nil {
						require.ErrorIs(t, second, ErrProductPlanConflict)
					} else {
						require.ErrorIs(t, first, ErrProductPlanConflict)
						require.NoError(t, second)
					}
				}
				t.Run("currency-safe checkout and wallet debit", func(t *testing.T) {
					oldRate, oldUnit := operation_setting.USDExchangeRate, common.QuotaPerUnit
					t.Cleanup(func() { operation_setting.USDExchangeRate, common.QuotaPerUnit = oldRate, oldUnit })
					operation_setting.USDExchangeRate, common.QuotaPerUnit = 7.3, 500000
					product := SubscriptionPlan{Title: "Currency-safe promotion", PriceAmount: 14.6, Currency: "CNY", DurationUnit: SubscriptionDurationDay, DurationValue: 30, TotalAmount: 1000000, ModelScope: "operator-model"}
					require.NoError(t, CreateProductPlan(db, &product))
					for _, amount := range []float64{0.001, 10000} {
						invalid := product
						invalid.PriceAmount = amount
						require.ErrorContains(t, invalid.ValidateProductPlan(), "two decimal places")
					}
					product.PriceVersion, product.Enabled = "balance-v1", true
					version := entries[0]
					version.Id, version.PriceBook, version.Model, version.PriceVersion = 0, "", product.ModelScope, product.PriceVersion
					version.Enabled, version.EffectiveAt = true, 0
					require.NoError(t, CreateProductPriceBookVersion(db, product.Id, &version))
					require.NoError(t, UpdateProductPlan(db, &product, 1))
					_, _, err := CreateSubscriptionOrder(user.Id, product.Id, "cny-waffo-"+engine, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake, false)
					require.ErrorContains(t, err, "USD product")
					require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", 10000000).Error)
					for _, rate := range []float64{0, -1, math.NaN(), math.Inf(1)} {
						operation_setting.USDExchangeRate = rate
						require.ErrorContains(t, PurchaseSubscriptionWithBalance(user.Id, product.Id), "exchange rate")
					}
					operation_setting.USDExchangeRate = 7.3
					for _, unit := range []float64{0, -1, math.NaN(), math.Inf(1)} {
						common.QuotaPerUnit = unit
						require.ErrorContains(t, PurchaseSubscriptionWithBalance(user.Id, product.Id), "Quota unit")
					}
					common.QuotaPerUnit = 500000
					var count int64
					require.NoError(t, db.Model(&SubscriptionOrder{}).Where("plan_id = ?", product.Id).Count(&count).Error)
					assert.Zero(t, count, "failed conversion must roll back the pending order too")
					operation_setting.USDExchangeRate = 7.3
					require.NoError(t, PurchaseSubscriptionWithBalance(user.Id, product.Id))
					var buyer User
					require.NoError(t, db.First(&buyer, user.Id).Error)
					assert.Equal(t, 9000000, buyer.Quota, "14.60 CNY is 2 USD, not 14.60 USD of wallet quota")
					var purchase SubscriptionOrder
					require.NoError(t, db.Where("plan_id = ?", product.Id).First(&purchase).Error)
					assert.Equal(t, "CNY", purchase.CurrencySnapshot)
					assert.JSONEq(t, `{"charged_quota":1000000,"cash_amount":14.6,"cash_currency":"CNY","cash_per_usd":7.3}`, purchase.ProviderPayload)
					product.Currency, product.PriceAmount = "USD", 2
					require.NoError(t, UpdateProductPlan(db, &product, 2))
					for _, provider := range []string{PaymentProviderAlipay, PaymentProviderIFLinkPay, PaymentProviderEpay} {
						_, _, err := CreateSubscriptionOrder(user.Id, product.Id, "usd-"+provider+"-"+engine, provider, provider, true)
						require.ErrorContains(t, err, "CNY product")
					}
					require.NoError(t, PurchaseSubscriptionWithBalance(user.Id, product.Id))
					require.NoError(t, db.First(&buyer, user.Id).Error)
					assert.Equal(t, 8000000, buyer.Quota, "USD wallet purchase must not apply the CNY exchange rate")
				})
				t.Run("reservation expiry releases limit and late receipts remain durable", func(t *testing.T) {
					product := SubscriptionPlan{Title: "Limited promotion", Currency: "CNY", PriceAmount: 17.25,
						DurationUnit: SubscriptionDurationDay, DurationValue: 3, TotalAmount: 1000000,
						ModelScope: "reserved-model", MaxPurchasePerUser: 1, ReservationTTLSeconds: 47}
					require.NoError(t, CreateProductPlan(db, &product))
					product.Enabled, product.PriceVersion = true, "reservation-v1"
					version := entries[0]
					version.Id, version.PriceBook, version.Model, version.PriceVersion = 0, "", product.ModelScope, product.PriceVersion
					version.Enabled, version.EffectiveAt = true, 0
					require.NoError(t, CreateProductPriceBookVersion(db, product.Id, &version))
					require.NoError(t, UpdateProductPlan(db, &product, 1))
					first, _, err := CreateSubscriptionOrder(user.Id, product.Id, "reserve-first-"+engine, "alipay", "alipay", true)
					require.NoError(t, err)
					assert.Equal(t, first.CreateTime+47, first.ReservationExpiresAt)
					_, _, err = CreateSubscriptionOrder(user.Id, product.Id, "reserve-blocked-"+engine, "alipay", "alipay", true)
					require.ErrorContains(t, err, "含待支付订单")
					// A deadline is authoritative without a background cleanup write.
					require.NoError(t, db.Model(first).UpdateColumn("reservation_expires_at", getDBTimestampTx(db)-1).Error)
					var second *SubscriptionOrder
					if engine == "sqlite" {
						second, _, err = CreateSubscriptionOrder(user.Id, product.Id, "reserve-second-"+engine, "alipay", "alipay", true)
						require.NoError(t, err)
					} else {
						// Two new checkouts race the released slot while a late payment
						// callback is replayed. Exactly one new reservation may win.
						type checkoutResult struct {
							order *SubscriptionOrder
							err   error
						}
						start, results, callbacks := make(chan struct{}), make(chan checkoutResult, 2), make(chan error, 2)
						for i := range 2 {
							go func() {
								<-start
								order, _, err := CreateSubscriptionOrder(user.Id, product.Id, fmt.Sprintf("reserve-race-%s-%d", engine, i), "alipay", "alipay", true)
								results <- checkoutResult{order, err}
							}()
							go func() {
								<-start
								callbacks <- CompleteSubscriptionOrderWithReceipt(first.TradeNo, "alipay", "alipay", PaymentReceipt{
									Amount: decimal.RequireFromString("17.25"), Currency: "CNY",
									ProviderTradeNo: "synthetic-" + first.TradeNo, ProviderPayload: "synthetic-receipt",
								})
							}()
						}
						close(start)
						var successful, rejected int
						for range 2 {
							result := <-results
							if result.err == nil {
								successful++
								second = result.order
							} else {
								require.ErrorContains(t, result.err, "含待支付订单")
								rejected++
							}
						}
						for range 2 {
							require.NoError(t, <-callbacks)
						}
						require.Equal(t, 1, successful)
						require.Equal(t, 1, rejected)
					}
					require.NoError(t, ExpireSubscriptionOrder(second.TradeNo, "alipay"))
					third, _, err := CreateSubscriptionOrder(user.Id, product.Id, "reserve-third-"+engine, "alipay", "alipay", true)
					require.NoError(t, err, "explicit provider cancellation also releases the slot")
					for _, late := range []*SubscriptionOrder{first, second} {
						receipt := PaymentReceipt{Amount: decimal.RequireFromString("17.25"), Currency: "CNY", ProviderTradeNo: "synthetic-" + late.TradeNo, ProviderPayload: "synthetic-receipt"}
						underpaid := receipt
						underpaid.Amount = decimal.RequireFromString("17.24")
						require.Error(t, CompleteSubscriptionOrderWithReceipt(late.TradeNo, "alipay", "alipay", underpaid))
						require.ErrorIs(t, CompleteSubscriptionOrderWithReceipt(late.TradeNo, "wrong-provider", "alipay", receipt), ErrPaymentMethodMismatch)
						for range 2 {
							require.NoError(t, CompleteSubscriptionOrderWithReceipt(late.TradeNo, "alipay", "alipay", receipt))
						}
						var parked SubscriptionOrder
						require.NoError(t, db.First(&parked, late.Id).Error)
						assert.Equal(t, SubscriptionOrderStatusPaidReview, parked.Status)
						assert.Equal(t, ProvisioningStatusCompensationPending, parked.ProvisioningStatus)
						assert.Equal(t, "reservation_released", parked.ProvisioningError)
						assert.Equal(t, 17.25, parked.PaidMoney)
						assert.Equal(t, "CNY", parked.PaidCurrency)
						assert.Equal(t, receipt.ProviderTradeNo, parked.ProviderTradeNo)
						assert.Equal(t, receipt.ProviderPayload, parked.ProviderPayload)
						assert.Nil(t, parked.UserSubscriptionId)
					}
					_, _, err = RetryPaidProductOrder(first.TradeNo, 1, "synthetic compensation decision")
					require.ErrorContains(t, err, "consume another reservation")
					var issued int64
					require.NoError(t, db.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, product.Id).Count(&issued).Error)
					assert.Zero(t, issued, "late callbacks must not consume another reservation or issue quota")
					require.NoError(t, CompleteSubscriptionOrder(third.TradeNo, "", "alipay", "alipay"))
					require.NoError(t, db.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, product.Id).Count(&issued).Error)
					assert.EqualValues(t, 1, issued)
					t.Run("verified payment survives partial provisioning failure", func(t *testing.T) {
						product.MaxPurchasePerUser = 0
						require.NoError(t, UpdateProductPlan(db, &product, product.ConfigRevision))
						failed, _, err := CreateSubscriptionOrder(user.Id, product.Id, "reserve-failed-"+engine, "alipay", "alipay", true)
						require.NoError(t, err)
						const callback = "test:paid-product-token-failure"
						require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
							if tx.Statement.Table == "tokens" {
								// A real SQL error aborts PostgreSQL's transaction until
								// rollback to the savepoint, unlike a Go-only hook error.
								tx.AddError(tx.Exec("SELECT id FROM product_issuance_failure_missing_table").Error)
							}
						}))
						receipt := PaymentReceipt{Amount: decimal.RequireFromString("17.25"), Currency: "CNY",
							ProviderTradeNo: "synthetic-" + failed.TradeNo, ProviderPayload: "synthetic-failed-receipt"}
						err = CompleteSubscriptionOrderWithReceipt(failed.TradeNo, "alipay", "alipay", receipt)
						require.NoError(t, db.Callback().Create().Remove(callback))
						require.NoError(t, err, "a verified receipt must remain durable when token issuance fails")
						var parked SubscriptionOrder
						require.NoError(t, db.First(&parked, failed.Id).Error)
						assert.Equal(t, SubscriptionOrderStatusPaidReview, parked.Status)
						assert.Equal(t, "provisioning_failed", parked.ProvisioningError)
						assert.Equal(t, receipt.ProviderTradeNo, parked.ProviderTradeNo)
						assert.Equal(t, 17.25, parked.PaidMoney)
						assert.Nil(t, parked.UserSubscriptionId)
						var after int64
						require.NoError(t, db.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, product.Id).Count(&after).Error)
						assert.Equal(t, issued, after, "partial entitlement creation must roll back")
						require.NoError(t, db.Model(&TopUp{}).Where("trade_no = ?", failed.TradeNo).Count(&after).Error)
						assert.Zero(t, after, "failed issuance cannot appear as a successful top-up")
						require.NoError(t, CompleteSubscriptionOrderWithReceipt(failed.TradeNo, "alipay", "alipay", receipt))
						for _, invalid := range []struct {
							actor int
							note  string
						}{{0, "synthetic decision"}, {1, " "}} {
							_, _, err := RetryPaidProductOrder(failed.TradeNo, invalid.actor, invalid.note)
							require.ErrorContains(t, err, "invalid product compensation")
						}
						// Duplicate administrator requests are serialized by the
						// order lock, and keep the first durable decision.
						if engine != "sqlite" {
							start, results := make(chan struct{}), make(chan bool, 2)
							errors := make(chan error, 2)
							for range 2 {
								go func() {
									<-start
									_, changed, err := RetryPaidProductOrder(failed.TradeNo, 1, "synthetic operator approved issuance")
									results <- changed
									errors <- err
								}()
							}
							close(start)
							firstChanged, secondChanged := <-results, <-results
							require.NoError(t, <-errors)
							require.NoError(t, <-errors)
							assert.NotEqual(t, firstChanged, secondChanged, "exactly one administrator request issues")
						} else {
							_, changed, err := RetryPaidProductOrder(failed.TradeNo, 1, "synthetic operator approved issuance")
							require.NoError(t, err)
							assert.True(t, changed)
						}
						resolved, changed, err := RetryPaidProductOrder(failed.TradeNo, 2, "duplicate decision must not overwrite")
						require.NoError(t, err)
						assert.False(t, changed)
						assert.Equal(t, common.TopUpStatusSuccess, resolved.Status)
						assert.Equal(t, 1, resolved.CompensationActorId)
						assert.Equal(t, "synthetic operator approved issuance", resolved.CompensationNote)
						assert.Positive(t, resolved.CompensationTime)
						require.NotNil(t, resolved.UserSubscriptionId)
						require.NoError(t, db.Model(&Token{}).Where("user_subscription_id = ?", *resolved.UserSubscriptionId).Count(&after).Error)
						assert.EqualValues(t, 1, after)
						require.NoError(t, db.Model(&TopUp{}).Where("trade_no = ?", failed.TradeNo).Count(&after).Error)
						assert.EqualValues(t, 1, after)
						require.NoError(t, CompleteSubscriptionOrderWithReceipt(failed.TradeNo, "alipay", "alipay", receipt))
						require.NoError(t, db.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, product.Id).Count(&after).Error)
						assert.Equal(t, issued+1, after)
					})
					t.Run("receipt write failure is not acknowledged and leaves no partial issuance", func(t *testing.T) {
						order, _, err := CreateSubscriptionOrder(user.Id, product.Id, "receipt-write-failed-"+engine, "alipay", "alipay", true)
						require.NoError(t, err)
						const callback = "test:paid-product-order-write-failure"
						require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
							if tx.Statement.Table == "subscription_orders" {
								tx.AddError(tx.Exec("SELECT id FROM product_receipt_failure_missing_table").Error)
							}
						}))
						receipt := PaymentReceipt{Amount: decimal.RequireFromString("17.25"), Currency: "CNY", ProviderTradeNo: "synthetic-" + order.TradeNo}
						err = CompleteSubscriptionOrderWithReceipt(order.TradeNo, "alipay", "alipay", receipt)
						require.NoError(t, db.Callback().Update().Remove(callback))
						require.Error(t, err, "a non-durable receipt must never be acknowledged")
						var stored SubscriptionOrder
						require.NoError(t, db.First(&stored, order.Id).Error)
						assert.Equal(t, common.TopUpStatusPending, stored.Status)
						assert.Nil(t, stored.UserSubscriptionId)
						var count int64
						require.NoError(t, db.Model(&TopUp{}).Where("trade_no = ?", order.TradeNo).Count(&count).Error)
						assert.Zero(t, count)
						require.NoError(t, db.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", user.Id, product.Id).Count(&count).Error)
						assert.Equal(t, issued+1, count, "all partial issuance must roll back")
						require.NoError(t, CompleteSubscriptionOrderWithReceipt(order.TradeNo, "alipay", "alipay", receipt))
						require.NoError(t, db.First(&stored, order.Id).Error)
						assert.Equal(t, common.TopUpStatusSuccess, stored.Status)
					})
					t.Run("verified receipts survive a failed checkout or removed customer", func(t *testing.T) {
						for i, scenario := range []string{"failed_checkout", "removed_customer"} {
							buyer := User{Username: "receipt-" + scenario, AffCode: fmt.Sprintf("edge%d", i), Status: common.UserStatusEnabled, Group: "default"}
							require.NoError(t, db.Create(&buyer).Error)
							order, _, err := CreateSubscriptionOrder(buyer.Id, product.Id, "receipt-"+scenario+"-"+engine, "alipay", "alipay", true)
							require.NoError(t, err)
							if i == 0 {
								require.NoError(t, db.Model(order).UpdateColumn("status", common.TopUpStatusFailed).Error)
							} else {
								require.NoError(t, db.Delete(&buyer).Error)
							}
							receipt := PaymentReceipt{Amount: decimal.RequireFromString("17.25"), Currency: "CNY", ProviderTradeNo: "synthetic-" + order.TradeNo, ProviderPayload: "synthetic-receipt"}
							for range 2 {
								require.NoError(t, CompleteSubscriptionOrderWithReceipt(order.TradeNo, "alipay", "alipay", receipt))
							}
							var stored SubscriptionOrder
							require.NoError(t, db.First(&stored, order.Id).Error)
							assert.Equal(t, SubscriptionOrderStatusPaidReview, stored.Status)
							assert.Equal(t, receipt.ProviderTradeNo, stored.ProviderTradeNo)
							assert.Equal(t, 17.25, stored.PaidMoney)
							assert.Nil(t, stored.UserSubscriptionId)
						}
					})
				})
			})

			t.Run("converted products fence old writers and preserve legacy orders", func(t *testing.T) {
				legacy := SubscriptionPlan{Id: 91, Title: "Frozen legacy terms", ProductType: ProductTypeLegacySubscription,
					ProductCode: "stable-checkout-code", PriceAmount: 19.9, Currency: "USD",
					DurationUnit: SubscriptionDurationDay, DurationValue: 7, TotalAmount: 1000000,
					ModelScope: pack.Model, PriceVersion: pack.PriceVersion, BillingMultiplier: 1}
				order := SubscriptionOrder{PlanId: legacy.Id, TradeNo: "pre-conversion-order"}
				order.SnapshotPlan(&legacy)
				converted := legacy
				converted.PriceBook, converted.ProductType, converted.ConfigRevision = "product:91", "", 1
				converted.Title, converted.TotalAmount = "New generic terms", 3000000
				require.NoError(t, db.Create(&converted).Error)
				for _, book := range []string{"", converted.PriceBook} {
					order.PriceBook = book // backfilled old orders are still legacy
					frozen, err := provisionPlanFromOrder(db, &order, &converted)
					require.NoError(t, err)
					assert.Empty(t, frozen.PriceBook)
					assert.Equal(t, legacy.ProductType, frozen.ProductType)
					assert.Equal(t, legacy.TotalAmount, frozen.TotalAmount)
					assert.Equal(t, legacy.Title, frozen.Title)
				}
				unfrozen := SubscriptionOrder{PlanId: legacy.Id}
				_, err := provisionPlanFromOrder(db, &unfrozen, &converted)
				require.ErrorContains(t, err, "immutable plan snapshot", "an incomplete historical order cannot inherit new commercial terms")
				require.ErrorIs(t, db.Transaction(func(tx *gorm.DB) error {
					return UpdateLegacySubscriptionPlan(tx, converted.Id, map[string]any{"title": "stale overwrite", "enabled": false})
				}), ErrProductPlanConflict)
				var stored SubscriptionPlan
				require.NoError(t, db.First(&stored, converted.Id).Error)
				assert.Equal(t, converted.Title, stored.Title)
				assert.Equal(t, converted.ConfigRevision, stored.ConfigRevision)

				// Old orders/entitlements keep the book in the legacy sync scope.
				// They must not give that old catalog control of generic sales.
				order.ProductType = pack.ProductType
				require.NoError(t, db.Create(&order).Error)
				owned := entries[0]
				owned.Id, owned.PriceBook, owned.Model, owned.PriceVersion = 0, converted.PriceBook, pack.Model, pack.PriceVersion
				owned.Enabled, owned.BillingExpression = true, `tier("owned", p * 3 + c * 5)`
				require.NoError(t, db.Create(&owned).Error)
				oldPrice := pack
				oldPrice.Enabled = false
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return SyncLegacyProductPriceBooks(tx, oldPrice) }))
				var after PriceBookPrice
				require.NoError(t, db.First(&after, owned.Id).Error)
				assert.Equal(t, owned, after, "legacy toggles must not modify generic prices")
				oldPrice.PriceVersion = "legacy-after-conversion"
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return SyncLegacyProductPriceBooks(tx, oldPrice) }))
				var count int64
				require.NoError(t, db.Model(&PriceBookPrice{}).Where("price_book = ? AND price_version = ?", converted.PriceBook, oldPrice.PriceVersion).Count(&count).Error)
				assert.Zero(t, count, "legacy publication must not add versions to a converted product")
			})
			t.Run("generic sale cost checks preserve paid versions", func(t *testing.T) {
				testGenericProductCostFloor(t, db)
			})

			// A conflicting migrated row must never be overwritten or leave a
			// partly copied wallet catalog behind.
			require.NoError(t, db.Where("price_book = ?", ProductTypeWallet).Delete(&PriceBookPrice{}).Error)
			require.NoError(t, db.Model(&PriceBookPrice{}).Where("price_book = ?", "product:41").UpdateColumn("billing_expression", `tier("corrupt", p * 2 + c * 8)`).Error)
			require.ErrorContains(t, BackfillProductPriceBooks(db), "backfill conflict")
			var walletCount int64
			require.NoError(t, db.Model(&PriceBookPrice{}).Where("price_book = ?", ProductTypeWallet).Count(&walletCount).Error)
			assert.Zero(t, walletCount, "the complete backfill transaction must roll back")

			// Commercial choices no longer depend on the product's family name,
			// a preselected vendor/model, or a frozen discount multiplier.
			generic := entries[0]
			generic.Id, generic.PriceBook, generic.PriceVersion = 0, "product:42", "operator-v2"
			generic.Model, generic.BillingMultiplier, generic.FallbackAllowed = "new-provider-model", 0.73, true
			generic.RouteMode = ProductRouteOrdered
			require.NoError(t, db.Create(&generic).Error)
			// Upgrade an already populated expansion table; old rows keep global
			// behavior, while new preference orders survive subsequent restarts.
			require.NoError(t, db.Migrator().DropColumn(&PriceBookPrice{}, "RouteMode"))
			for range 2 {
				require.NoError(t, db.AutoMigrate(&PriceBookPrice{}))
			}
			var upgraded PriceBookPrice
			require.NoError(t, db.First(&upgraded, generic.Id).Error)
			assert.Empty(t, upgraded.RouteMode)
			require.NoError(t, db.Model(&PriceBookPrice{}).Where("id = ?", generic.Id).UpdateColumn("route_mode", ProductRouteOrdered).Error)
			require.NoError(t, db.Model(&generic).Update("enabled", false).Error)
			var disabled PriceBookPrice
			require.NoError(t, db.First(&disabled, generic.Id).Error)
			assert.False(t, disabled.Enabled)
			assert.Equal(t, generic.BillingExpression, disabled.BillingExpression)
			assert.Equal(t, ProductRouteOrdered, disabled.RouteMode)
			assert.Equal(t, generic.AllowedChannels, disabled.AllowedChannels)
			require.ErrorContains(t, db.Model(&disabled).Update("route_mode", ProductRouteGlobal).Error, "immutable")
			invalidMode := generic
			invalidMode.RouteMode = "typo"
			require.ErrorContains(t, invalidMode.Validate(), "route mode")
			invalidMode.RouteMode, invalidMode.PriceBook = ProductRouteOrdered, WalletPriceBook
			require.ErrorContains(t, invalidMode.Validate(), "wallet")
			for _, book := range []string{"", "wallet ", "product:0", "product:-1", "product:01", "41", "monthly_subscription"} {
				invalid := generic
				invalid.PriceBook = book
				require.Error(t, invalid.Validate(), book)
			}

			t.Run("ordered routes retain safety and do not skip shrinking candidates", func(t *testing.T) {
				require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
				t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&Ability{}, &Channel{})) })
				for index, id := range []int{801, 802, 803} {
					channel := Channel{Id: id, Name: "ordered synthetic route", Key: "synthetic-key", Models: generic.Model,
						Group: "supplier-only", Status: common.ChannelStatusEnabled, Priority: common.GetPointer(int64(index * 100)),
						UpstreamCosts: `{"new-provider-model":{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}}`}
					require.NoError(t, db.Create(&channel).Error)
					require.NoError(t, channel.AddAbilities(db))
				}
				oldCache := common.MemoryCacheEnabled
				channelSyncLock.RLock()
				oldGroups, oldChannels, oldCustom := group2model2channels, channelsIDM, channel2advancedCustomConfig
				channelSyncLock.RUnlock()
				t.Cleanup(func() {
					common.MemoryCacheEnabled = oldCache
					channelSyncLock.Lock()
					group2model2channels, channelsIDM, channel2advancedCustomConfig = oldGroups, oldChannels, oldCustom
					channelSyncLock.Unlock()
				})
				for _, cache := range []bool{false, true} {
					t.Run(fmt.Sprintf("cache=%v", cache), func(t *testing.T) {
						common.MemoryCacheEnabled = cache
						if cache {
							InitChannelCache()
						}
						policy := &ChannelSelectionPolicy{
							AllowedChannelIds: map[int]struct{}{801: {}, 802: {}, 803: {}},
							OrderedChannelIds: []int{801, 802, 803}, FallbackAllowed: true, RequireCost: true,
							MaxUpstreamCost: PriceComponents{Input: 1, CachedInput: 0.2, CacheCreation: 1, Output: 4},
						}
						chosen, err := GetOrderedSatisfiedChannel(generic.Model, nil, policy, nil)
						require.NoError(t, err)
						require.NotNil(t, chosen)
						assert.Equal(t, 801, chosen.Id, "product preference beats global priority")
						attempted := map[int]struct{}{801: {}}
						chosen, err = GetOrderedSatisfiedChannel(generic.Model, nil, policy, attempted)
						require.NoError(t, err)
						require.NotNil(t, chosen)
						assert.Equal(t, 802, chosen.Id)
						policy.FallbackAllowed = false
						chosen, err = GetOrderedSatisfiedChannel(generic.Model, nil, policy, attempted)
						require.NoError(t, err)
						assert.Nil(t, chosen, "no backup means no second channel")
						policy.FallbackAllowed = true
						delete(policy.AllowedChannelIds, 801)
						chosen, err = GetOrderedSatisfiedChannel(generic.Model, nil, policy, nil)
						require.NoError(t, err)
						require.NotNil(t, chosen)
						assert.Equal(t, 802, chosen.Id, "a shorter eligible set must not skip the first backup")
						policy.MaxUpstreamCost.Output = 1
						chosen, err = GetOrderedSatisfiedChannel(generic.Model, nil, policy, nil)
						require.NoError(t, err)
						assert.Nil(t, chosen, "over-cost backups must fail closed")
						policy.MaxUpstreamCost.Output = 4
						policy.RequireCost = false
						chosen, err = GetOrderedSatisfiedChannel("unsupported-model", nil, policy, nil)
						require.NoError(t, err)
						assert.Nil(t, chosen, "an allowlist cannot grant an unsupported model even without cost enforcement")
						chosen, err = GetOrderedSatisfiedChannel(generic.Model, []dto.ChannelFilter{{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "unavailable-plugin"}}, policy, nil)
						require.NoError(t, err)
						assert.Nil(t, chosen, "product preference cannot bypass request capability filters")
					})
				}
			})
		})
	}
}

// This opt-in check runs only against an isolated restored production copy.
// TestMain performs the real startup migration before this test is entered.
func TestProductionPriceBookBackfill(t *testing.T) {
	if os.Getenv("PRICE_BOOK_PRODUCTION_REHEARSAL") != "1" {
		t.Skip("requires isolated production restore")
	}
	// TestMain initializes schema but not runtime options. The shape check
	// must see the restored site's groups, rather than only compiled defaults.
	originalGroups := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroups)) })
	for _, key := range []string{"GroupRatio", "group_ratio_setting.group_ratio"} {
		var option Option
		require.NoError(t, DB.Where(map[string]any{"key": key}).Find(&option).Error)
		if option.Value != "" {
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(option.Value))
		}
	}
	var legacyBefore []ProductPrice
	var plansBefore []SubscriptionPlan
	var ordersBefore []SubscriptionOrder
	var subscriptionsBefore []UserSubscription
	require.NoError(t, DB.Order("id").Find(&legacyBefore).Error)
	require.NotEmpty(t, legacyBefore)
	require.NoError(t, DB.Order("id").Find(&plansBefore).Error)
	require.NoError(t, DB.Order("id").Find(&ordersBefore).Error)
	require.NoError(t, DB.Order("id").Find(&subscriptionsBefore).Error)
	for range 2 {
		require.NoError(t, migrateDB())
		require.NoError(t, BackfillProductPriceBooks(DB))
	}
	var legacyAfter []ProductPrice
	var plansAfter []SubscriptionPlan
	var ordersAfter []SubscriptionOrder
	var subscriptionsAfter []UserSubscription
	require.NoError(t, DB.Order("id").Find(&legacyAfter).Error)
	require.NoError(t, DB.Order("id").Find(&plansAfter).Error)
	require.NoError(t, DB.Order("id").Find(&ordersAfter).Error)
	require.NoError(t, DB.Order("id").Find(&subscriptionsAfter).Error)
	// Never log production records (including statements expanded by assert).
	require.True(t, reflect.DeepEqual(legacyBefore, legacyAfter), "legacy catalog changed")
	require.True(t, reflect.DeepEqual(plansBefore, plansAfter), "plans changed")
	for i := range ordersBefore {
		ordersBefore[i].PriceBook = ordersAfter[i].PriceBook
	}
	for i := range subscriptionsBefore {
		subscriptionsBefore[i].PriceBook = subscriptionsAfter[i].PriceBook
	}
	require.True(t, reflect.DeepEqual(ordersBefore, ordersAfter), "orders changed beyond the new price_book snapshot")
	require.True(t, reflect.DeepEqual(subscriptionsBefore, subscriptionsAfter), "entitlements changed beyond the new price_book snapshot")
	var count int64
	require.NoError(t, DB.Model(&PriceBookPrice{}).Count(&count).Error)
	require.Positive(t, count)
	t.Logf("legacy prices=%d plans=%d orders=%d entitlements=%d copied prices=%d; originals unchanged; migration twice passed",
		len(legacyBefore), len(plansBefore), len(ordersBefore), len(subscriptionsBefore), count)
	// Exercise real historical snapshots against a simulated converted live
	// plan, without writing that conversion or issuing any entitlement.
	plansByID := make(map[int]SubscriptionPlan, len(plansAfter))
	var shapeCompatible, specialHandling, missingStableCode int
	shapeBlockers := make(map[string]int)
	for _, plan := range plansAfter {
		plansByID[plan.Id] = plan
		candidate := plan
		candidate.PriceBook, _ = ProductPriceBook(plan.Id)
		candidate.ProductType, candidate.TemplateId, candidate.BillingMultiplier = "", 0, 1
		if err := candidate.ValidateProductPlan(); err == nil {
			shapeCompatible++
		} else {
			specialHandling++
			shapeBlockers[err.Error()]++
		}
		if plan.ProductCode == "" {
			missingStableCode++
		}
	}
	var checked, invalidHistorical, missingLivePlan int
	for _, order := range ordersAfter {
		if order.ProductType == "" || order.ProductPlanSnapshot != "" {
			continue
		}
		live, found := plansByID[order.PlanId]
		if !found {
			missingLivePlan++
			continue
		}
		before, err := provisionPlanFromOrder(DB, &order, &live)
		if err != nil {
			invalidHistorical++
			continue
		}
		converted := live
		converted.PriceBook, _ = ProductPriceBook(live.Id)
		converted.ProductType, converted.TemplateId, converted.ConfigRevision = "", 0, 1
		after, err := provisionPlanFromOrder(DB, &order, &converted)
		require.NoError(t, err, "historical order reconstruction failed after simulated conversion")
		require.True(t, reflect.DeepEqual(before, after), "historical fulfillment terms changed after simulated conversion")
		checked++
	}
	t.Logf("read-only conversion preflight: shape-compatible=%d special-handling=%d missing-stable-code=%d historical-snapshots-checked=%d historical-invalid=%d missing-live-plan=%d; cost verification and route translation NOT certified; no conversion applied",
		shapeCompatible, specialHandling, missingStableCode, checked, invalidHistorical, missingLivePlan)
	t.Logf("conversion shape blocker counts (no product/customer records): %v", shapeBlockers)
}

func TestGenericProductCostFloorFailsClosed(t *testing.T) {
	testGenericProductCostFloor(t, DB)
}

func testGenericProductCostFloor(t *testing.T, db *gorm.DB) {
	modelName := "generic-cost-floor-test-model"
	product := &SubscriptionPlan{
		Title: "Generic cost floor test", PriceAmount: 10, Currency: "CNY",
		DurationUnit: SubscriptionDurationDay, DurationValue: 1,
		TotalAmount: 1000000, ModelScope: modelName,
	}
	require.NoError(t, CreateProductPlan(db, product))
	t.Cleanup(func() {
		require.NoError(t, db.Where("price_book = ?", product.PriceBook).Delete(&PriceBookPrice{}).Error)
		require.NoError(t, db.Delete(&SubscriptionPlan{}, product.Id).Error)
	})
	price := PriceBookPrice{
		Model: modelName, PriceVersion: "generic-cost-floor-v1", Enabled: true,
		BillingExpression: `tier("base", p + c)`, BillingMultiplier: 1,
		AllowedChannels:         `[1]`,
		OfficialReferencePrice:  `{"input":1,"output":1}`,
		UserSellingPrice:        `{"input":1,"output":1}`,
		UpstreamSettlementPrice: `{"input":2,"output":1}`,
		MaxUpstreamCost:         `{"input":2,"output":1}`,
	}
	require.ErrorContains(t, CreateProductPriceBookVersion(db, product.Id, &price), "cost floor")

	price.Enabled = false
	require.NoError(t, CreateProductPriceBookVersion(db, product.Id, &price))
	_, _, err := SetProductPriceBookVersionEnabled(db, product.Id, price.Id, true, product.ConfigRevision)
	require.ErrorContains(t, err, "cost floor")
	product.PriceVersion, product.Enabled = price.PriceVersion, true
	require.ErrorContains(t, UpdateProductPlan(db, product, product.ConfigRevision), "cost floor")
	// Taking a version off sale or adding a new economic gate cannot deny a
	// previously paid immutable order its historical version.
	require.NoError(t, validateProductPlanPrices(db, product, false))
	price.UserSellingPrice = `{"input":1.9999999999,"output":1}`
	require.ErrorContains(t, validateProductPriceBookCostFloor(&price), "cost floor")
	price.UserSellingPrice = price.MaxUpstreamCost
	require.NoError(t, validateProductPriceBookCostFloor(&price))
}

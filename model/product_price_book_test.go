package model

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
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
				dialect = postgres.Open(dsn)
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

			for range 2 {
				require.NoError(t, db.AutoMigrate(&PriceBookPrice{}))
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
			require.NoError(t, db.Create(&generic).Error)
			require.NoError(t, db.Model(&generic).Update("enabled", false).Error)
			var disabled PriceBookPrice
			require.NoError(t, db.First(&disabled, generic.Id).Error)
			assert.False(t, disabled.Enabled)
			assert.Equal(t, generic.BillingExpression, disabled.BillingExpression)
			for _, book := range []string{"", "wallet ", "product:0", "product:-1", "product:01", "41", "monthly_subscription"} {
				invalid := generic
				invalid.PriceBook = book
				require.Error(t, invalid.Validate(), book)
			}
		})
	}
}

// This opt-in check runs only against an isolated restored production copy.
// TestMain performs the real startup migration before this test is entered.
func TestProductionPriceBookBackfill(t *testing.T) {
	if os.Getenv("PRICE_BOOK_PRODUCTION_REHEARSAL") != "1" {
		t.Skip("requires isolated production restore")
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
}

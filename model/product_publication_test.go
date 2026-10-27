package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestProductPublicationDatabaseMatrix(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var dialect gorm.Dialector
			switch engine {
			case "sqlite":
				dialect = sqlite.Open(filepath.Join(t.TempDir(), "publication.db"))
			case "mysql":
				if os.Getenv("API_CHANNEL_TEST_MYSQL_DSN") == "" {
					t.Skip("API_CHANNEL_TEST_MYSQL_DSN not configured")
				}
				dialect = mysql.Open(os.Getenv("API_CHANNEL_TEST_MYSQL_DSN"))
			case "postgres":
				if os.Getenv("API_CHANNEL_TEST_POSTGRES_DSN") == "" {
					t.Skip("API_CHANNEL_TEST_POSTGRES_DSN not configured")
				}
				dialect = postgres.New(postgres.Config{DSN: os.Getenv("API_CHANNEL_TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(engine))
			initCol()
			sqlDB, err := db.DB()
			require.NoError(t, err)
			if engine == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			}
			t.Cleanup(func() {
				DB = oldDB
				common.SetMainDatabaseType(oldType)
				initCol()
				require.NoError(t, sqlDB.Close())
			})
			t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
			tables := []any{&SubscriptionOrder{}, &SubscriptionKeyBinding{}, &ProductPublicationItem{}, &ProductPublication{}, &Token{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &BillingRefund{}, &SubscriptionPlan{}, &PriceBookPrice{}, &APIChannelPrice{}, &APIChannelCatalog{}, &Ability{}, &Channel{}}
			require.NoError(t, db.Migrator().DropTable(tables...))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })
			for _, upgrade := range []bool{false, true} {
				t.Run(fmt.Sprintf("upgrade=%t", upgrade), func(t *testing.T) {
					require.NoError(t, db.Migrator().DropTable(tables...))
					require.NoError(t, db.AutoMigrate(tables...))
					if upgrade {
						sentinel := UserSubscription{UserId: 991, PlanId: 991, AmountTotal: 900, AmountUsed: 123, PriceVersion: "old-sentinel", Status: "expired"}
						require.NoError(t, db.Create(&sentinel).Error)
						for _, column := range []string{"ResetPolicySnapshot", "QuotaResetPeriodSnapshot", "QuotaResetSecondsSnapshot", "ProductRevision"} {
							require.NoError(t, db.Migrator().DropColumn(&UserSubscription{}, column))
						}
						require.NoError(t, db.Migrator().DropColumn(&SubscriptionPlan{}, "LatestPublicationRevision"))
						require.NoError(t, db.Migrator().DropTable(&ProductPublicationItem{}, &ProductPublication{}))
					}
					for range 2 {
						require.NoError(t, db.AutoMigrate(tables...))
						require.NoError(t, InitializeAPIChannelCatalog(db))
					}
					if upgrade {
						var sentinel UserSubscription
						require.NoError(t, db.Where("user_id = ?", 991).First(&sentinel).Error)
						require.EqualValues(t, 123, sentinel.AmountUsed)
						require.Equal(t, "old-sentinel", sentinel.PriceVersion)
					}
					channel := Channel{Id: 71, Name: "publication upstream", Status: common.ChannelStatusEnabled, Models: "shared-model", Group: "default", Key: "synthetic-private-key"}
					require.NoError(t, db.Create(&channel).Error)
					_, err := PublishAPIChannelPrices(db, 0, []APIChannelPrice{{Model: "shared-model", ChannelID: 71, UpstreamModel: "shared-model", Enabled: true, SellingPrice: aPriceFixture}}, 1, "fixture")
					require.NoError(t, err)
					plan := SubscriptionPlan{Title: "Publication product", PriceAmount: 10, Currency: "USD", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000, ModelScope: "shared-model"}
					require.NoError(t, CreateProductPlan(db, &plan))
					plan.ConfigRevision, err = StageProductChannelVersion(db, plan.Id, plan.ConfigRevision, 1, "v1", []ProductChannelModel{{Model: "shared-model", Rates: []ProductChannelRate{{ChannelID: 71, Multiplier: 1}}}}, true)
					require.NoError(t, err)
					plan.PriceVersion, plan.Enabled = "v1", true
					require.NoError(t, UpdateProductPlan(db, &plan, plan.ConfigRevision))
					oldOrderPlan := plan
					var subscriptions []*UserSubscription
					var tokens []*Token
					for user := range 2 {
						sub, err := CreateUserSubscriptionFromPlanTx(db, user+101, &plan, "test")
						require.NoError(t, err)
						require.NoError(t, db.Model(sub).Update("amount_used", (user+1)*100).Error)
						token, _, err := CreateBoundTokenForSubscription(sub.UserId, sub.Id)
						require.NoError(t, err)
						subscriptions, tokens = append(subscriptions, sub), append(tokens, token)
					}
					plan.ConfigRevision, err = StageProductChannelVersion(db, plan.Id, plan.ConfigRevision, 1, "v2", []ProductChannelModel{{Model: "shared-model", Rates: []ProductChannelRate{{ChannelID: 71, Multiplier: 0.5}}}}, true)
					require.NoError(t, err)
					plan.PriceVersion, plan.TotalAmount, plan.PriceAmount, plan.QuotaResetPeriod = "v2", 800, 20, SubscriptionResetDaily
					req := ProductPublicationRequest{Plan: plan, ExpectedRevision: plan.ConfigRevision, Fields: []string{"pricing", "quota", "expiry", "reset"}, Reason: "operator publication", RequestKey: "publication-fixture-key"}
					preview, err := PreviewProductPublication(db, req)
					require.NoError(t, err)
					require.Equal(t, 2, preview.Affected)
					require.Zero(t, preview.Blocked)
					req.PreviewHash = preview.Hash
					publication, err := PublishProductAndSync(db, req, 1)
					require.NoError(t, err)
					replayed, err := PublishProductAndSync(db, req, 1)
					require.NoError(t, err)
					require.Equal(t, publication.ID, replayed.ID)
					conflicting := req
					conflicting.Reason = "different payload under the same idempotency key"
					_, err = PublishProductAndSync(db, conflicting, 1)
					require.ErrorIs(t, err, ErrProductPublicationConflict)
					var items []ProductPublicationItem
					require.NoError(t, db.Where("publication_id = ?", publication.ID).Order("subscription_id").Find(&items).Error)
					require.Len(t, items, 2)
					// A publication waits for asset-changing in-flight requests;
					// settled historical "consumed" records must not block forever.
					_, err = PreConsumeUserSubscription("publication-flight", 101, "shared-model", 0, 50, subscriptions[0].Id)
					require.NoError(t, err)
					receipt := BillingRefund{RequestID: "publication-flight", SubscriptionID: subscriptions[0].Id, UserID: 101, Status: BillingRefundReserved}
					require.NoError(t, db.Create(&receipt).Error)
					require.NoError(t, ProcessProductPublicationItem(context.Background(), db, items[0].ID))
					var waiting ProductPublicationItem
					require.NoError(t, db.First(&waiting, items[0].ID).Error)
					require.Equal(t, "retry", waiting.Status)
					require.NoError(t, PostConsumeUserSubscriptionDelta(subscriptions[0].Id, -20))
					require.NoError(t, db.Model(&receipt).Update("status", BillingRefundSettled).Error)
					require.NoError(t, ProcessProductPublicationItem(context.Background(), db, items[0].ID))
					var wg sync.WaitGroup
					results := make(chan error, 2)
					start := make(chan struct{})
					wg.Go(func() { <-start; results <- PostConsumeUserSubscriptionDelta(subscriptions[1].Id, 30) })
					wg.Go(func() { <-start; results <- ProcessProductPublicationItem(context.Background(), db, items[1].ID) })
					close(start)
					wg.Wait()
					close(results)
					for err := range results {
						require.NoError(t, err)
					}
					for i, item := range items {
						require.NoError(t, ProcessProductPublicationItem(context.Background(), db, item.ID))
						var sub UserSubscription
						require.NoError(t, db.First(&sub, item.SubscriptionID).Error)
						require.EqualValues(t, 800, sub.AmountTotal)
						require.EqualValues(t, 130+i*100, sub.AmountUsed)
						require.Equal(t, "v2", sub.PriceVersion)
						require.Equal(t, SubscriptionResetDaily, sub.QuotaResetPeriodSnapshot)
						require.EqualValues(t, publication.Revision, sub.ProductRevision)
						require.NoError(t, db.First(&item, item.ID).Error)
						require.Equal(t, "applied", item.Status)
						require.NotEmpty(t, item.BeforeState)
						require.NotEmpty(t, item.AfterState)
						// Even an old token policy cannot select the old pricebook.
						require.NoError(t, db.Model(tokens[i]).Updates(map[string]any{"price_version": "v1", "model_limits": "old-model"}).Error)
						validated, err := ValidateUserToken(tokens[i].Key)
						require.NoError(t, err)
						require.Equal(t, "v2", validated.PriceVersion)
						require.Equal(t, "shared-model", validated.ModelLimits)
					}
					late, err := CreateUserSubscriptionFromPlanTx(db, 103, &oldOrderPlan, "order")
					require.NoError(t, err)
					require.Equal(t, "v2", late.PriceVersion)
					require.EqualValues(t, 800, late.AmountTotal)
					require.EqualValues(t, 10, late.PriceAmountSnapshot, "cash evidence remains the old order's price")
					require.Equal(t, SubscriptionResetDaily, late.QuotaResetPeriodSnapshot)
					var pending int64
					require.NoError(t, db.Model(&ProductPublicationItem{}).Where("publication_id = ?", publication.ID).Count(&pending).Error)
					require.EqualValues(t, 3, pending)
					var latestPlan SubscriptionPlan
					require.NoError(t, db.First(&latestPlan, plan.Id).Error)
					latestPlan.TotalAmount = 50
					blockedPreview, err := PreviewProductPublication(db, ProductPublicationRequest{Plan: latestPlan, ExpectedRevision: latestPlan.ConfigRevision, Fields: []string{"quota"}})
					require.NoError(t, err)
					assert.Equal(t, 2, blockedPreview.Blocked)
					// Consumption after preview cannot be overwritten or made
					// negative by a later lower-quota publication.
					latestPlan.TotalAmount = 240
					lower := ProductPublicationRequest{Plan: latestPlan, ExpectedRevision: latestPlan.ConfigRevision, Fields: []string{"quota"}, Reason: "lower quota", RequestKey: "publication-lower-key"}
					lowerPreview, err := PreviewProductPublication(db, lower)
					require.NoError(t, err)
					require.Zero(t, lowerPreview.Blocked)
					lower.PreviewHash = lowerPreview.Hash
					lowerPublication, err := PublishProductAndSync(db, lower, 1)
					require.NoError(t, err)
					require.NoError(t, PostConsumeUserSubscriptionDelta(subscriptions[1].Id, 20))
					var lowerItem ProductPublicationItem
					require.NoError(t, db.Where("publication_id = ? AND subscription_id = ?", lowerPublication.ID, subscriptions[1].Id).First(&lowerItem).Error)
					require.NoError(t, ProcessProductPublicationItem(context.Background(), db, lowerItem.ID))
					require.NoError(t, db.First(&lowerItem, lowerItem.ID).Error)
					require.Equal(t, "blocked", lowerItem.Status)
					var usedSub UserSubscription
					require.NoError(t, db.First(&usedSub, subscriptions[1].Id).Error)
					assert.EqualValues(t, 250, usedSub.AmountUsed)
					assert.EqualValues(t, 800, usedSub.AmountTotal)
					// Expired/paused/invalidated targets are skipped, not revived.
					lowerItem = ProductPublicationItem{}
					require.NoError(t, db.Where("publication_id = ? AND subscription_id = ?", lowerPublication.ID, subscriptions[0].Id).First(&lowerItem).Error)
					require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", subscriptions[0].Id).Update("status", "cancelled").Error)
					require.NoError(t, ProcessProductPublicationItem(context.Background(), db, lowerItem.ID))
					require.NoError(t, db.First(&lowerItem, lowerItem.ID).Error)
					assert.Equal(t, "skipped", lowerItem.Status)
					assert.True(t, db.Migrator().HasIndex(&ProductPublicationItem{}, "idx_publication_entitlement"))
					if engine != "mysql" {
						var previousPending []ProductPublicationItem
						activePublications := db.Model(&ProductPublication{}).Select("id").Where("plan_id = ?", plan.Id)
						require.NoError(t, db.Where("publication_id IN (?) AND status IN ?", activePublications, []string{"pending", "retry", "cache_pending"}).Find(&previousPending).Error)
						for _, item := range previousPending {
							require.NoError(t, ProcessProductPublicationItem(context.Background(), db, item.ID))
						}
						var batchPlan SubscriptionPlan
						require.NoError(t, db.First(&batchPlan, plan.Id).Error)
						now := getDBTimestampTx(db)
						batchSubscriptions := make([]UserSubscription, 501)
						for index := range batchSubscriptions {
							batchSubscriptions[index] = UserSubscription{UserId: 10000 + index, PlanId: plan.Id, AmountTotal: 240,
								StartTime: now, EndTime: now + 3600, Status: "active", ModelScope: "shared-model", PriceBook: plan.PriceBook,
								PriceVersion: "v2", BillingMultiplier: 1, AllowedChannels: "[71]", MaxUpstreamCost: `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`}
						}
						require.NoError(t, db.CreateInBatches(&batchSubscriptions, 100).Error)
						batchPlan.ConfigRevision, err = StageProductChannelVersion(db, batchPlan.Id, batchPlan.ConfigRevision, 1, "v3-large", []ProductChannelModel{{Model: "shared-model", Rates: []ProductChannelRate{{ChannelID: 71, Multiplier: 0.75}}}}, true)
						require.NoError(t, err)
						batchPlan.PriceVersion = "v3-large"
						require.NoError(t, UpdateProductPlan(db, &batchPlan, batchPlan.ConfigRevision))
						batchRequest := ProductPublicationRequest{Plan: batchPlan, ExpectedRevision: batchPlan.ConfigRevision, Fields: []string{"pricing"}, Reason: "large batch regression", RequestKey: "publication-large-batch-key"}
						batchPreview, err := PreviewProductPublication(db, batchRequest)
						require.NoError(t, err)
						require.GreaterOrEqual(t, batchPreview.Affected, 501)
						batchRequest.PreviewHash = batchPreview.Hash
						batchPublication, err := PublishProductAndSync(db, batchRequest, 1)
						require.NoError(t, err)
						for range 2 {
							var batchIDs []int64
							require.NoError(t, db.Model(&ProductPublicationItem{}).Where("publication_id = ? AND status IN ?", batchPublication.ID, []string{"pending", "retry", "cache_pending"}).Order("id").Limit(500).Pluck("id", &batchIDs).Error)
							if len(batchIDs) == 0 {
								break
							}
							for _, itemID := range batchIDs {
								require.NoError(t, ProcessProductPublicationItem(context.Background(), db, itemID))
							}
						}
						var unapplied int64
						require.NoError(t, db.Model(&ProductPublicationItem{}).Where("publication_id = ? AND status IN ?", batchPublication.ID, []string{"pending", "retry", "cache_pending"}).Count(&unapplied).Error)
						require.Zero(t, unapplied)
						var applied int64
						require.NoError(t, db.Model(&ProductPublicationItem{}).Where("publication_id = ? AND status = ?", batchPublication.ID, "applied").Count(&applied).Error)
						require.EqualValues(t, batchPreview.Affected, applied)
						var batchSub UserSubscription
						require.NoError(t, db.Where("user_id = ? AND plan_id = ?", 10000, plan.Id).First(&batchSub).Error)
						require.Equal(t, "v3-large", batchSub.PriceVersion)
						batchItem := ProductPublicationItem{}
						require.NoError(t, db.Where("publication_id = ? AND subscription_id = ?", batchPublication.ID, batchSub.Id).First(&batchItem).Error)
						require.NoError(t, ProcessProductPublicationItem(context.Background(), db, batchItem.ID))
						require.NoError(t, db.First(&batchSub, batchSub.Id).Error)
						require.Equal(t, "v3-large", batchSub.PriceVersion, "replaying an applied item must not reapply entitlement changes")
					}
					// A disabled product with no active pools can still owe an
					// unresolved paid/pending order its frozen usable route.
					require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).Updates(map[string]any{"enabled": false, "redemption_enabled": false}).Error)
					require.NoError(t, db.Model(&UserSubscription{}).Where("plan_id = ?", plan.Id).Update("status", "cancelled").Error)
					order := SubscriptionOrder{UserId: 104, PlanId: plan.Id, TradeNo: "unresolved-publication-order", Status: common.TopUpStatusPending, PriceBook: plan.PriceBook, PriceVersion: "v1", ModelScope: "shared-model"}
					require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&order).Error)
					_, err = PublishAPIChannelPrices(db, 1, []APIChannelPrice{{Model: "shared-model", ChannelID: 71, UpstreamModel: "shared-model", Enabled: false, SellingPrice: aPriceFixture}}, 1, "retire channel")
					require.ErrorContains(t, err, "unresolved order")
				})
			}
		})
	}
}

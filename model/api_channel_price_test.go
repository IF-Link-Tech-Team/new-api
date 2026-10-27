package model

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAPIChannelCatalogDatabaseMatrix(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(engine, func(t *testing.T) {
			var dialect gorm.Dialector
			switch engine {
			case "sqlite":
				dialect = sqlite.Open(filepath.Join(t.TempDir(), "catalog.db"))
			case "mysql":
				dsn := os.Getenv("API_CHANNEL_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("API_CHANNEL_TEST_MYSQL_DSN not configured")
				}
				dialect = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("API_CHANNEL_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("API_CHANNEL_TEST_POSTGRES_DSN not configured")
				}
				dialect = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(engine))
			initCol()
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() {
				DB = oldDB
				common.SetMainDatabaseType(oldType)
				initCol()
				require.NoError(t, sqlDB.Close())
			})
			tables := []any{&APIChannelPrice{}, &APIChannelCatalog{}, &Channel{}, &Ability{}, &PriceBookPrice{}, &SubscriptionPlan{}, &UserSubscription{}}
			require.NoError(t, db.Migrator().DropTable(tables...))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })
			for _, upgrade := range []bool{false, true} {
				t.Run(fmt.Sprintf("upgrade=%t", upgrade), func(t *testing.T) {
					require.NoError(t, db.Migrator().DropTable(tables...))
					require.NoError(t, db.AutoMigrate(tables[2:]...))
					primary := Channel{Id: 71, Name: "primary", Models: "shared-model", Group: "default", Status: common.ChannelStatusEnabled, Key: "must-stay-private", ModelMapping: common.GetPointer(`{"shared-model":"provider-model-a"}`)}
					require.NoError(t, db.Create(&primary).Error)
					backup := primary
					backup.Id, backup.Name, backup.ModelMapping = 72, "backup", common.GetPointer(`{"shared-model":"provider-model-b"}`)
					require.NoError(t, db.Create(&backup).Error)
					require.NoError(t, primary.AddAbilities(nil))
					require.NoError(t, backup.AddAbilities(nil))
					old := PriceBookPrice{PriceBook: "wallet", Model: "shared-model", PriceVersion: "released", BillingExpression: `tier("base", p * 100)`, BillingMultiplier: 1, Enabled: true}
					old.OfficialReferencePrice = aPriceFixture
					old.UserSellingPrice = aPriceFixture
					old.UpstreamSettlementPrice = aPriceFixture
					old.MaxUpstreamCost = aPriceFixture
					old.AllowedChannels = "[71,72]"
					if upgrade {
						require.NoError(t, db.Create(&old).Error)
						require.NoError(t, db.Migrator().DropColumn(&PriceBookPrice{}, "ChannelRates"))
						require.NoError(t, db.AutoMigrate(&APIChannelCatalog{}, &APIChannelPrice{}))
						require.NoError(t, db.Migrator().DropColumn(&APIChannelPrice{}, "PriceExpression"))
					}
					// Exactly the new startup expansion, twice; no reconcile or
					// implicit activation. Existing source rows stay unchanged.
					for range 2 {
						require.NoError(t, db.AutoMigrate(&APIChannelCatalog{}, &APIChannelPrice{}, &PriceBookPrice{}))
						require.NoError(t, InitializeAPIChannelCatalog(db))
					}
					var actual Channel
					require.NoError(t, db.First(&actual, primary.Id).Error)
					require.Equal(t, primary.Key, actual.Key)
					if upgrade {
						var kept PriceBookPrice
						require.NoError(t, db.First(&kept, old.Id).Error)
						require.Equal(t, old.BillingExpression, kept.BillingExpression)
					}
					t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
					a := APIChannelPrice{Model: "shared-model", ChannelID: 71, UpstreamModel: "provider-model-a", Enabled: true, Position: common.GetPointer(0), SellingPrice: `{"input":1,"cached_input":0.1,"cache_creation":1.25,"output":4}`}
					b := a
					b.ChannelID, b.UpstreamModel, b.Position = 72, "provider-model-b", common.GetPointer(1)
					b.SellingPrice = `{"input":2,"cached_input":0.2,"cache_creation":2.5,"output":8}`
					revision, err := PublishAPIChannelPrices(db, 0, []APIChannelPrice{a, b}, 1, "initial catalog")
					require.NoError(t, err)
					require.EqualValues(t, 1, revision)
					product := SubscriptionPlan{Title: "Channel rate product", PriceAmount: 10, Currency: "USD", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, TotalAmount: 1000000, ModelScope: "shared-model"}
					require.NoError(t, CreateProductPlan(db, &product))
					require.EqualError(t, CreateProductPriceBookVersion(db, product.Id, &old), "Use model and channel rates for new product versions")
					revisionAfterStage, err := StageProductChannelVersion(db, product.Id, product.ConfigRevision, revision, "channel-v1", []ProductChannelModel{{
						Model: "shared-model",
						Rates: []ProductChannelRate{{ChannelID: 71, Multiplier: 0.5, Position: common.GetPointer(0)}, {ChannelID: 72, Multiplier: 1.5, Position: common.GetPointer(1)}},
					}}, true)
					require.NoError(t, err)
					product.ConfigRevision = revisionAfterStage
					product.PriceVersion, product.Enabled = "channel-v1", true
					require.NoError(t, UpdateProductPlan(db, &product, product.ConfigRevision))
					productPrice, err := ResolvePriceBookPrice(db, product.PriceBook, "shared-model", "channel-v1")
					require.NoError(t, err)
					rates, err := productPrice.ProductChannelRates()
					require.NoError(t, err)
					require.Equal(t, []ProductChannelRate{{ChannelID: 71, Multiplier: 0.5, Position: common.GetPointer(0)}, {ChannelID: 72, Multiplier: 1.5, Position: common.GetPointer(1)}}, rates)
					assert.EqualError(t, func() error {
						_, err := StageProductChannelVersion(db, product.Id, product.ConfigRevision, revision-1, "channel-v2", []ProductChannelModel{{
							Model: "shared-model", Rates: []ProductChannelRate{{ChannelID: 71, Multiplier: 1}},
						}}, true)
						return err
					}(), ErrAPIChannelPriceConflict.Error())
					public, err := APIChannelModelsForGroups(db, []string{"default"})
					require.NoError(t, err)
					require.True(t, public["shared-model"])
					private, err := APIChannelModelsForGroups(db, []string{"unrelated-group"})
					require.NoError(t, err)
					require.Empty(t, private, "sales rows must not expand model-list group authorization")
					_, err = PublishAPIChannelPrices(db, 0, []APIChannelPrice{a}, 2, "stale administrator")
					require.ErrorIs(t, err, ErrAPIChannelPriceConflict)
					ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
					frozen, err := RequestAPIChannelPrices(ctx, "shared-model")
					require.NoError(t, err)
					require.Len(t, frozen.Rows, 2)
					expression, err := frozen.Rows[0].BillingExpression()
					require.NoError(t, err)
					value, _, err := billingexpr.RunExprByHashWithRequest(expression, billingexpr.ExprHashString(expression), billingexpr.TokenParams{P: 800, CR: 200, CC: 100, CC1h: 100, C: 100}, billingexpr.RequestInput{})
					require.NoError(t, err)
					require.InDelta(t, 1470, value, 1e-8)
					a.Enabled = false
					revision, err = PublishAPIChannelPrices(db, revision, []APIChannelPrice{a}, 1, "stop selling primary")
					require.NoError(t, err)
					require.NoError(t, validateProductPlanPrices(db, &product, true), "a remaining API channel keeps the product saleable")
					b.Enabled = false
					_, err = PublishAPIChannelPrices(db, revision, []APIChannelPrice{b}, 1, "remove final active product route")
					require.ErrorContains(t, err, "no usable API channel")
					require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", product.Id).Updates(map[string]any{"enabled": false, "redemption_enabled": true}).Error)
					_, err = PublishAPIChannelPrices(db, revision, []APIChannelPrice{b}, 1, "remove final redemption route")
					require.ErrorContains(t, err, "no usable API channel")
					require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", product.Id).Update("redemption_enabled", false).Error)
					sold := UserSubscription{UserId: 9, PlanId: product.Id, PriceBook: product.PriceBook, PriceVersion: "channel-v1", ModelScope: "shared-model", Status: "active", EndTime: GetDBTimestamp() + 3600}
					require.NoError(t, db.Create(&sold).Error)
					_, err = PublishAPIChannelPrices(db, revision, []APIChannelPrice{b}, 1, "remove sold product route")
					require.ErrorContains(t, err, "active entitlement")
					b.Enabled = true
					stillFrozen, err := RequestAPIChannelPrices(ctx, "shared-model")
					require.NoError(t, err)
					require.Len(t, stillFrozen.Rows, 2, "in-flight request keeps candidate prices")
					fresh, _ := gin.CreateTestContext(httptest.NewRecorder())
					latest, err := RequestAPIChannelPrices(fresh, "shared-model")
					require.NoError(t, err)
					require.Len(t, latest.Rows, 1, "disabled latest must not resurrect earlier price")
					require.Equal(t, 72, latest.Rows[0].ChannelID)
					b.UpstreamModel = "stale-provider-model"
					_, err = PublishAPIChannelPrices(db, revision, []APIChannelPrice{b}, 1, "stale mapping")
					require.ErrorContains(t, err, "mapping changed")
					b.UpstreamModel = "provider-model-b"
					// Two real connections race the same CAS. SQLite serializes
					// these writers before they read the immutable history.
					var wg sync.WaitGroup
					results := make(chan error, 2)
					start := make(chan struct{})
					for actor := range 2 {
						wg.Go(func() {
							<-start
							_, err := PublishAPIChannelPrices(db, revision, []APIChannelPrice{b}, actor+1, "concurrent publication")
							results <- err
						})
					}
					close(start)
					wg.Wait()
					close(results)
					successes, conflicts := 0, 0
					for err := range results {
						if err == nil {
							successes++
						} else {
							require.ErrorIs(t, err, ErrAPIChannelPriceConflict)
							conflicts++
						}
					}
					require.Equal(t, 1, successes)
					require.Equal(t, 1, conflicts)
					require.NoError(t, db.Model(&Channel{}).Where("id = ?", backup.Id).Update("model_mapping", `{"shared-model":"changed-provider-model"}`).Error)
					mapped, _ := gin.CreateTestContext(httptest.NewRecorder())
					unavailable, err := RequestAPIChannelPrices(mapped, "shared-model")
					require.NoError(t, err)
					require.Empty(t, unavailable.Rows, "mapping drift must not silently reuse confirmed sales prices")
					public, err = APIChannelModelsForGroups(db, []string{"default"})
					require.NoError(t, err)
					require.Empty(t, public)
					// Product enable and final-route retirement share the catalog
					// fence. Neither may validate against the other's old state.
					require.NoError(t, db.Model(&Channel{}).Where("id = ?", backup.Id).Update("model_mapping", `{"shared-model":"provider-model-b"}`).Error)
					require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", sold.Id).Update("status", "expired").Error)
					product.Enabled, product.RedemptionEnabled = true, false
					b.Enabled = false
					start = make(chan struct{})
					raceResults := make(chan error, 2)
					wg.Go(func() {
						<-start
						raceResults <- UpdateProductPlan(db, &product, product.ConfigRevision)
					})
					wg.Go(func() {
						<-start
						_, err := PublishAPIChannelPrices(db, revision+1, []APIChannelPrice{b}, 1, "retire final route concurrently with enable")
						raceResults <- err
					})
					close(start)
					wg.Wait()
					close(raceResults)
					successes = 0
					for err := range raceResults {
						if err == nil {
							successes++
						} else {
							require.Contains(t, err.Error(), "API channel")
						}
					}
					require.Equal(t, 1, successes)
					var stored SubscriptionPlan
					require.NoError(t, db.First(&stored, product.Id).Error)
					if stored.Enabled {
						require.NoError(t, validateProductPlanPrices(db, &stored, true))
					}
				})
			}
		})
	}
}

func TestAPIChannelPriceExpressionRetainsTimePricing(t *testing.T) {
	price := APIChannelPrice{PriceExpression: `weekday("Asia/Shanghai") >= 1 && weekday("Asia/Shanghai") <= 5 && (hour("Asia/Shanghai") < 12 || hour("Asia/Shanghai") >= 14) ? tier("peak", p * 2 + cr * 0.1 + cc * 1.25 + cc1h * 1.25 + c * 8) : tier("off_peak", p + cr * 0.02 + cc * 0.2 + cc1h * 0.2 + c * 4)`}
	expression, err := price.BillingExpression()
	require.NoError(t, err)
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	for _, test := range []struct {
		name string
		at   time.Time
		want float64
	}{
		{name: "weekday peak", at: time.Date(2026, time.October, 26, 10, 0, 0, 0, loc), want: 100*2 + 10*0.1 + 5*1.25 + 5*1.25 + 20*8},
		{name: "weekday off peak", at: time.Date(2026, time.October, 26, 13, 0, 0, 0, loc), want: 100 + 10*0.02 + 5*0.2 + 5*0.2 + 20*4},
		{name: "weekend off peak", at: time.Date(2026, time.October, 25, 10, 0, 0, 0, loc), want: 100 + 10*0.02 + 5*0.2 + 5*0.2 + 20*4},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, _, err := billingexpr.RunExprByHashWithRequest(expression, billingexpr.ExprHashString(expression), billingexpr.TokenParams{P: 100, CR: 10, CC: 5, CC1h: 5, C: 20}, billingexpr.RequestInput{EvaluationUnix: test.at.Unix()})
			require.NoError(t, err)
			require.InDelta(t, test.want, value, 1e-8)
		})
	}
	price.PriceExpression = `hour("Not/A_Time_Zone") > 1 ? tier("bad", p) : tier("base", p)`
	_, err = price.BillingExpression()
	require.NoError(t, err, "the native evaluator owns runtime timezone fallback semantics")
	price.PriceExpression = `tier("base", fixed(0.01))`
	_, err = price.BillingExpression()
	require.Error(t, err, "API token prices must not accept request-priced expressions")
}

const aPriceFixture = `{"input":1,"cached_input":0.1,"cache_creation":1.25,"output":4}`

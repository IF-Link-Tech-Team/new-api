package model

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
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
			tables := []any{&APIChannelPrice{}, &APIChannelCatalog{}, &Channel{}, &Ability{}, &PriceBookPrice{}}
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
					}
					// Exactly the new startup expansion, twice; no reconcile or
					// implicit activation. Existing source rows stay unchanged.
					for range 2 {
						require.NoError(t, db.AutoMigrate(&APIChannelCatalog{}, &APIChannelPrice{}))
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
				})
			}
		})
	}
}

const aPriceFixture = `{"input":1,"cached_input":0.1,"cache_creation":1.25,"output":4}`

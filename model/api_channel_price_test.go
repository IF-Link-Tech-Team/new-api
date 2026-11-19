package model

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
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
	originalRegistry := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() { jsplugin.DefaultRegistry = originalRegistry })
	h3, err := jsplugin.DefaultRegistry.Register(`
export const meta = {apiVersion: 1, key: "jiliu-h3", name: "H3", version: "1.0.0", author: {name: "Test"}, models: ["MiniMax-H3"], fetchMode: "per_task", usageSchema: {seconds: {type: "number", unit: "second"}}};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
`, jsplugin.Options{})
	require.NoError(t, err)
	vod, err := jsplugin.DefaultRegistry.Register(`
export const meta = {apiVersion: 1, key: "tencent-vod", name: "VOD", version: "1.0.0", author: {name: "Test"}, models: ["wand-vega-video-1.5-pro", "wand-vega-image-1.0-flash"], fetchMode: "per_task", usageSchema: {seconds: {type: "number", unit: "second"}}, routes: [{method:"POST",path:"/vod/v1/videos",type:"submit",decode:"create",render:"render"}]};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
export const native = { create() { return {}; }, render() { return {}; } };
`, jsplugin.Options{})
	require.NoError(t, err)
	media, err := jsplugin.DefaultRegistry.Register(`
export const meta = {apiVersion: 1, key: "tokenhub-media", name: "TokenHub Media", version: "1.0.0", author: {name: "Test"}, models: ["minimax-video-h3", "seedream-image-v5.0-pro"], fetchMode: "per_task", usageSchema: {tokens: {type: "number", unit: "token"}}, usageExamples:[{label:"Media",facts:{tokens:10000}}], routes:[{method:"POST",path:"/tokenhub-media/v1/audio/generations",type:"submit",decode:"create",render:"render"}]};
export function buildSubmitRequest() { return {}; }
export function parseSubmitResponse() { return {}; }
export function buildQueryRequest() { return {}; }
export function parseTaskResult() { return {}; }
export const native = { create() { return {}; }, render() { return {}; } };
`, jsplugin.Options{})
	require.NoError(t, err)
	savedSettings := config.GlobalConfig.ExportAllConfigs()
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(savedSettings)) })
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
			tables := []any{&APIChannelPrice{}, &APIChannelCatalog{}, &Channel{}, &Ability{}, &PriceBookPrice{}, &SubscriptionPlan{}, &UserSubscription{}, &Model{}, &Vendor{}, &GroupModelPrice{}, &GroupWallet{}, &GroupChannel{}}
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
					InvalidatePricingCache()
					square, err := GetGroupPricing([]string{"default"})
					require.NoError(t, err)
					require.Len(t, square, 1)
					require.Len(t, square[0].GroupPricing, 2, "retain distinct eligible channel prices")
					assert.Equal(t, []string{"default"}, square[0].EnableGroup)
					assert.NotContains(t, square[0].BillingExpr, "p * 100", "do not use legacy price-book expressions")
					hidden, err := GetGroupPricing([]string{"unrelated-group"})
					require.NoError(t, err)
					require.Empty(t, hidden)
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
					t.Run("native H3 discovery and routing", func(t *testing.T) {
						require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
							billing_setting.PluginBillingExprOption: `{"jiliu-h3::MiniMax-H3":"u(\"seconds\") * 0.06","tencent-vod::wand-vega-video-1.5-pro":"u(\"seconds\") * 0.1","tencent-vod::wand-vega-image-1.0-flash":"u(\"seconds\") * 0.2","tokenhub-media::minimax-video-h3":"u(\"tokens\") * 10 / 1000000","tokenhub-media::seedream-image-v5.0-pro":"u(\"tokens\") * 10 / 1000000"}`,
						}))
						for _, fixture := range []struct {
							id, status                  int
							name, group, plugin, mapped string
						}{
							{81, common.ChannelStatusEnabled, "h3-alias", "default", "jiliu-h3", "MiniMax-H3"},
							{82, common.ChannelStatusManuallyDisabled, "h3-disabled", "default", "jiliu-h3", "MiniMax-H3"},
							{83, common.ChannelStatusEnabled, "h3-other", "default", "hailuo", "MiniMax-H3"},
							{84, common.ChannelStatusEnabled, "h3-private", "内部", "jiliu-h3", "MiniMax-H3"},
							{85, common.ChannelStatusEnabled, "h3-invalid", "default", "jiliu-h3", "unknown-model"},
							{86, common.ChannelStatusEnabled, "vod-video", "default", "tencent-vod", "wand-vega-video-1.5-pro"},
							{87, common.ChannelStatusEnabled, "vod-image", "default", "tencent-vod", "wand-vega-image-1.0-flash"},
							{88, common.ChannelStatusEnabled, "media-video", "default", "tokenhub-media", "minimax-video-h3"},
							{89, common.ChannelStatusEnabled, "media-image", "default", "tokenhub-media", "seedream-image-v5.0-pro"},
						} {
							setting, marshalErr := common.Marshal(map[string]string{"task_plugin_key": fixture.plugin})
							require.NoError(t, marshalErr)
							mapping, marshalErr := common.Marshal(map[string]string{fixture.name: fixture.mapped})
							require.NoError(t, marshalErr)
							channel := Channel{Id: fixture.id, Type: constant.ChannelTypeTaskPlugin, Name: fixture.name, Models: fixture.name, Group: fixture.group, Status: fixture.status, Key: "synthetic", Setting: common.GetPointer(string(setting)), ModelMapping: common.GetPointer(string(mapping))}
							require.NoError(t, db.Create(&channel).Error)
							require.NoError(t, channel.AddAbilities(db))
						}
						visible, err := APIChannelModelsForGroups(db, []string{"default"})
						require.NoError(t, err)
						assert.True(t, visible["h3-alias"])
						assert.True(t, visible["vod-video"])
						assert.True(t, visible["vod-image"])
						assert.True(t, visible["media-video"])
						assert.True(t, visible["media-image"])
						for _, name := range []string{"h3-disabled", "h3-other", "h3-private", "h3-invalid"} {
							assert.False(t, visible[name], name)
						}
						private, err := APIChannelModelsForGroups(db, []string{"内部"})
						require.NoError(t, err)
						assert.True(t, private["h3-private"])
						InvalidatePricingCache()
						square, err := GetGroupPricing([]string{"default"})
						require.NoError(t, err)
						foundH3 := false
						for _, item := range square {
							assert.NotContains(t, item.EnableGroup, "内部")
							assert.NotEqual(t, "h3-private", item.ModelName)
							if item.ModelName == "h3-alias" {
								foundH3 = true
								assert.Equal(t, `u("seconds") * 0.06`, item.BillingExpr)
							}
						}
						require.True(t, foundH3, "native task sales keep their measured-usage prices")
						require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: `{}`}))
						visible, err = APIChannelModelsForGroups(db, []string{"default"})
						require.NoError(t, err)
						assert.False(t, visible["h3-alias"], "unpriced H3 must not be advertised")
						assert.False(t, visible["vod-video"], "unpriced VOD must not be advertised")
						assert.False(t, visible["vod-image"], "unpriced VOD must not be advertised")
						assert.False(t, visible["media-video"], "unpriced TokenHub must not be advertised")
						assert.False(t, visible["media-image"], "unpriced TokenHub must not be advertised")

						for _, fixture := range []struct {
							name, path, expected string
							pinned               bool
							native               bool
							shared               bool
						}{
							{"H3", "/v1/videos", "jiliu-h3", true, true, false},
							{"unvalidated identity", "/v1/videos", "jiliu-h3", false, false, false},
							{"other task", "/v1/videos", "hailuo", true, false, false},
							{"text path", "/v1/chat/completions", "jiliu-h3", true, false, false},
							{"VOD video", "/v1/videos", "tencent-vod", true, true, false},
							{"VOD images", "/v1/images/generations", "tencent-vod", true, true, false},
							{"VOD edit", "/v1/images/edits", "tencent-vod", true, true, false},
							{"VOD text stays token-priced", "/v1/chat/completions", "tencent-vod", true, false, false},
							{"VOD unpinned", "/v1/videos", "tencent-vod", false, false, false},
							{"VOD native pinned", "/vod/v1/videos", "tencent-vod", true, true, false},
							{"H3 MiniMax", "/v2/video_generation", "jiliu-h3", true, true, true},
							{"VOD MiniMax", "/v2/video_generation", "tencent-vod", true, true, true},
							{"TokenHub MiniMax", "/v2/video_generation", "tokenhub-media", true, true, true},
							{"TokenHub image", "/v1/images/generations", "tokenhub-media", true, true, false},
							{"TokenHub image edit", "/v1/images/edits", "tokenhub-media", true, true, false},
							{"TokenHub native", "/tokenhub-media/v1/audio/generations", "tokenhub-media", true, true, false},
							{"TokenHub chat remains catalog priced", "/v1/chat/completions", "tokenhub-media", true, false, false},
							{"MiniMax without endpoint pin", "/v2/video_generation", "tencent-vod", true, false, false},
						} {
							t.Run(fixture.name, func(t *testing.T) {
								ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
								ctx.Request = httptest.NewRequest("POST", fixture.path, nil)
								ctx.Set("expected_task_plugin_key", fixture.expected)
								if fixture.pinned {
									plugin := h3
									if fixture.expected == "tencent-vod" {
										plugin = vod
									} else if fixture.expected == "tokenhub-media" {
										plugin = media
									}
									ctx.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Plugin: plugin})
									if fixture.shared {
										_, op, ok := jsplugin.LookupHostProtocolOperation("POST", fixture.path)
										require.True(t, ok)
										ctx.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{Plugin: plugin, Protocol: jsplugin.ProtocolMiniMaxVideo, Operation: op})
									}
									if strings.HasPrefix(fixture.path, "/vod/") || strings.HasPrefix(fixture.path, "/tokenhub-media/") {
										route, ok := jsplugin.DefaultRegistry.Generation().LookupDeclaredRoute("POST", fixture.path)
										require.True(t, ok)
										ctx.Set(jsplugin.ContextKeyPinnedRoute, jsplugin.PinnedRoute{Plugin: route.Plugin, Route: route.Route, Generation: jsplugin.DefaultRegistry.Generation()})
									}
								}
								prices, err := RequestAPIChannelPrices(ctx, "MiniMax-H3")
								require.NoError(t, err)
								if fixture.native {
									assert.Nil(t, prices)
								} else {
									assert.NotNil(t, prices)
								}
							})
						}
					})
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

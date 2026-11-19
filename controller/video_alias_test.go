package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPublicVideoAliasesPreserveH3AndProviderBilling(t *testing.T) {
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	db, dialect := openPublicAliasDatabase(t, &model.User{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.Log{}, &model.BillingRefund{}, &model.APIChannelCatalog{}, &model.APIChannelPrice{}, &model.Model{}, &model.Vendor{}, &model.GroupChannel{}, &model.GroupModelPrice{})
	oldDB, oldLogDB, oldRegistry := model.DB, model.LOG_DB, pluginruntime.DefaultRegistry
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	oldGroups, oldFactory := ratio_setting.GroupRatio2JSONString(), service.GetTaskAdaptorFunc
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, true, false, true
	pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor { return relay.GetTaskAdaptor(platform) }
	t.Cleanup(func() {
		model.DB, model.LOG_DB, pluginruntime.DefaultRegistry = oldDB, oldLogDB, oldRegistry
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldMemory, oldBatch, oldConsume
		service.GetTaskAdaptorFunc = oldFactory
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
		if oldDB != nil {
			model.InitChannelCache()
		}
	})
	withTieredBillingConfig(t, map[string]string{}, map[string]string{})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"seu-beta":1,"vip":1}`))
	require.NoError(t, model.InitializeAPIChannelCatalog(db))
	for _, key := range []string{"jiliu-h3", "tencent-vod", "tokenhub-media"} {
		source, err := builtinplugins.Source(key)
		require.NoError(t, err)
		_, err = pluginruntime.DefaultRegistry.RegisterFactory(source, pluginruntime.Options{Key: key})
		require.NoError(t, err)
	}
	expressions, err := common.Marshal(map[string]string{
		"jiliu-h3::MiniMax-H3":                  `tier("base", u("seconds") * 0.14)`,
		"tencent-vod::MiniMax-H3":               `tier("base", u("seconds") * 0.375)`,
		"tencent-vod::wan-3.0":                  `tier("base", u("seconds") * 1.08)`,
		"tokenhub-media::MiniMax-H3":            `tier("base", u("tokens") * 10 / 1000000)`,
		"tokenhub-media::wan-3.0":               `tier("base", u("tokens") * 10 / 1000000)`,
		"tokenhub-media::minimax-video-h3-fast": `tier("base", u("tokens") * 10 / 1000000)`,
		"tokenhub-media::pixverse-video-v6.0":   `tier("base", u("tokens") * 10 / 1000000)`,
		"tokenhub-media::pixverse-video-c1":     `tier("base", u("tokens") * 10 / 1000000)`,
		"tokenhub-media::pixverse-video-v5.6":   `tier("base", u("tokens") * 10 / 1000000)`,
	})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: string(expressions)}))
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/wand/pixverse/text-to-video":
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Contains(t, string(body), `"model":"pixverse-video-v6.0"`)
			assert.Contains(t, string(body), `"quality":"720p"`)
			assert.NotContains(t, string(body), `"content"`)
			_, _ = io.WriteString(w, `{"ErrCode":0,"Resp":{"video_id":"vendor-job"}}`)
		case "/v1/wand/pixverse/tasks/vendor-job":
			_, _ = io.WriteString(w, `{"ErrCode":0,"Resp":{"id":"vendor-job","status":1,"url":"https://example.com/pixverse.mp4"},"tokenhub_usage":{"total_tokens":102655}}`)
		case "/v1/wand/vega-videos/tasks":
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Contains(t, string(body), `"model":"wand-vega-video-1.5-lite"`)
			_, _ = io.WriteString(w, `{"id":"vendor-job","status":"queued"}`)
		case "/v1/wand/minimax-video-v2/generation":
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Contains(t, string(body), `"model":"minimax-video-h3-max"`)
			assert.NotContains(t, string(body), "minimax-video-h3-fast")
			_, _ = io.WriteString(w, `{"task":{"id":"vendor-job","status":"queued"}}`)
		case "/v1/wand/minimax-video-v2/tasks/vendor-job":
			_, _ = io.WriteString(w, `{"task":{"id":"vendor-job","status":"succeeded","content":{"video_url":"https://example.com/fast.mp4"}},"tokenhub_usage":{"total_tokens":200000}}`)
		case "/v1/wand/vega-videos/tasks/vendor-job":
			_, _ = io.WriteString(w, `{"id":"vendor-job","status":"succeeded","content":{"video_url":"https://example.com/video.mp4"},"tokenhub_usage":{"total_tokens":540000}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	channels := []model.Channel{
		{Id: 19, Name: "Original H3", Type: 61, Status: 1, Key: "synthetic-key", BaseURL: common.GetPointer(server.URL), Models: "MiniMax-H3", Group: "default,seu-beta,vip", Priority: common.GetPointer(int64(200)), Setting: common.GetPointer(`{"task_plugin_key":"jiliu-h3"}`)},
		{Id: 24, Name: "VOD", Type: 61, Status: 1, Key: "1|test-id|test-secret", BaseURL: common.GetPointer(server.URL), Models: "MiniMax-H3,wan-3.0", Group: "default", Priority: common.GetPointer(int64(0)), ModelMapping: common.GetPointer(`{"MiniMax-H3":"wand-vega-video-1.0-lite","wan-3.0":"wand-vega-video-1.5-lite"}`), Setting: common.GetPointer(`{"task_plugin_key":"tencent-vod"}`)},
		{Id: 25, Name: "Media", Type: 61, Status: 1, Key: "synthetic-key", BaseURL: common.GetPointer(server.URL), Models: "MiniMax-H3,wan-3.0,minimax-video-h3-fast,pixverse-video-v6.0,pixverse-video-c1,pixverse-video-v5.6", Group: "default,seu-beta", Priority: common.GetPointer(int64(100)), ModelMapping: common.GetPointer(`{"MiniMax-H3":"wand-vega-video-1.0-lite","wan-3.0":"wand-vega-video-1.5-lite","minimax-video-h3-fast":"minimax-video-h3-max"}`), Setting: common.GetPointer(`{"task_plugin_key":"tokenhub-media"}`)},
	}
	for _, ch := range channels {
		require.NoError(t, db.Create(&ch).Error)
		require.NoError(t, ch.AddAbilities(nil))
	}
	model.InitChannelCache()
	for _, group := range []string{"default", "seu-beta", "vip"} {
		selected, err := model.GetRandomSatisfiedChannel(group, "MiniMax-H3", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, selected)
		assert.Equal(t, 19, selected.Id)
	}
	visible, err := model.APIChannelModelsForGroups(db, []string{"default"})
	require.NoError(t, err)
	assert.True(t, visible["MiniMax-H3"])
	assert.True(t, visible["wan-3.0"])
	assert.True(t, visible["minimax-video-h3-fast"])
	assert.False(t, visible["minimax-video-h3"])
	assert.False(t, visible["minimax-video-h3-max"])
	assert.False(t, visible["minimax-video-v2.3"])
	assert.False(t, visible["minimax-video-v2.3-fast"])
	assert.False(t, visible["wand-vega-video-1.0-lite"])
	assert.False(t, visible["wand-vega-video-1.5-lite"])
	square, err := model.GetGroupPricing([]string{"default"})
	require.NoError(t, err)
	for _, name := range []string{"MiniMax-H3", "wan-3.0", "minimax-video-h3-fast", "pixverse-video-v6.0", "pixverse-video-c1", "pixverse-video-v5.6"} {
		found := false
		for _, row := range square {
			if row.ModelName == name {
				found = true
				assert.Contains(t, row.SupportedEndpointTypes, constant.EndpointType("minimax-video"))
				require.NotEmpty(t, row.GroupPricing)
			}
		}
		assert.True(t, found, name)
	}
	initial := common.QuotaRound(100 * common.QuotaPerUnit)
	user := model.User{Username: "video-alias", AffCode: "video-alias", Quota: initial}
	require.NoError(t, db.Create(&user).Error)
	var charged float64
	for _, tc := range []struct {
		name, group, resolution, plugin string
		channel, duration               int
		charge                          float64
	}{
		{"MiniMax-H3", "default", "768P", "jiliu-h3", 19, 5, 0},
		{"MiniMax-H3", "default", "2K", "tokenhub-media", 25, 5, 0},
		{"wan-3.0", "default", "1080P", "tokenhub-media", 25, 5, 5.4},
		{"wan-3.0", "default", "720P", "tokenhub-media", 25, 5, 0},
		{"wan-3.0", "default", "720P", "tencent-vod", 24, 20, 0},
		{"minimax-video-h3-fast", "default", "768P", "tokenhub-media", 25, 5, 2},
		{"pixverse-video-v6.0", "default", "720P", "tokenhub-media", 25, 5, 1.02655},
		{"pixverse-video-c1", "default", "360P", "tokenhub-media", 25, 1, 0},
		{"pixverse-video-v5.6", "default", "1080P", "tokenhub-media", 25, 8, 0},
	} {
		t.Run(fmt.Sprintf("%s-%s-%ds", tc.name, tc.resolution, tc.duration), func(t *testing.T) {
			body := fmt.Sprintf(`{"model":%q,"content":[{"type":"text","text":"A cat"}],"duration":%d,"resolution":%q,"ratio":"16:9"}`, tc.name, tc.duration, tc.resolution)
			var c *gin.Context
			router := gin.New()
			router.POST("/v2/video_generation", func(ctx *gin.Context) {
				c = ctx
				c.Set("group", tc.group)
				common.SetContextKey(c, constant.ContextKeyUsingGroup, tc.group)
				c.Set("username", user.Username)
				c.Set("id", user.Id)
				c.Next()
			}, middleware.PinTaskPluginEndpoint(), middleware.RequireTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(), func(ctx *gin.Context) {
				assert.Equal(t, tc.channel, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
				assert.Equal(t, tc.plugin, ctx.GetString("task_plugin_key"))
				info := taskSubmissionRelayInfo(nil)
				info.UserId, info.OriginModelName, info.UserGroup, info.UsingGroup = user.Id, tc.name, tc.group, tc.group
				info.IsPlayground = true
				info.UserSetting.BillingPreference = "wallet_only"
				info.PublicTaskID = model.GenerateTaskID()
				for _, ch := range channels {
					if ch.Id == tc.channel {
						selected := ch
						info.LockedChannel = &selected
					}
				}
				if tc.charge == 0 {
					ctx.JSON(200, gin.H{"selected": tc.channel})
					return
				}
				outcome, taskErr := executeTaskSubmission(ctx, info)
				require.Nil(t, taskErr)
				require.NotNil(t, outcome)
				assert.Equal(t, tc.name, outcome.Task.Properties.OriginModelName)
				wantUpstream := "wand-vega-video-1.5-lite"
				if tc.name == "minimax-video-h3-fast" {
					wantUpstream = "minimax-video-h3-max"
				} else if strings.HasPrefix(tc.name, "pixverse-") {
					wantUpstream = tc.name
				}
				assert.Equal(t, wantUpstream, outcome.Task.Properties.UpstreamModelName)
				service.DispatchPlatformUpdate(context.Background(), outcome.Task.Platform, map[int][]string{tc.channel: {"vendor-job"}}, map[string]*model.Task{"vendor-job": outcome.Task})
				require.NoError(t, db.First(outcome.Task, outcome.Task.ID).Error)
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), outcome.Task.Status)
				require.NoError(t, db.First(&user, user.Id).Error)
				charged += tc.charge
				assert.Equal(t, initial-common.QuotaRound(charged*common.QuotaPerUnit), user.Quota)
				retrieve := gin.New()
				retrieve.GET("/v2/query/video_generation/:task_id", func(q *gin.Context) {
					common.SetContextKey(q, constant.ContextKeyUserId, user.Id)
					RetrieveMiniMaxVideo(q)
				})
				got := httptest.NewRecorder()
				retrieve.ServeHTTP(got, httptest.NewRequest("GET", "/v2/query/video_generation/"+outcome.Task.TaskID, nil))
				assert.Equal(t, 200, got.Code)
				assert.Contains(t, got.Body.String(), fmt.Sprintf(`"model":%q`, tc.name))
				assert.NotContains(t, got.Body.String(), "minimax-video-h3-max")
				assert.NotContains(t, got.Body.String(), "wand-vega")
				ctx.JSON(200, gin.H{"selected": tc.channel})
			})
			req := httptest.NewRequest("POST", "/v2/video_generation", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			got := httptest.NewRecorder()
			router.ServeHTTP(got, req)
			require.Equal(t, 200, got.Code, got.Body.String())
			require.NotNil(t, c)
			common.CleanupBodyStorage(c)
		})
	}
	assert.Equal(t, 1, calls["/v1/wand/vega-videos/tasks"])
	assert.Equal(t, 1, calls["/v1/wand/minimax-video-v2/generation"])
	assert.Equal(t, 1, calls["/v1/wand/pixverse/text-to-video"])
}

// These DSNs point only to the disposable alias-test databases. Unprefixed
// tables exercise the same correlated catalog queries as production.
func openPublicAliasDatabase(t *testing.T, tables ...any) (*gorm.DB, common.DatabaseType) {
	t.Helper()
	tables = append(tables, &model.GroupWallet{}, &model.GroupWalletMember{}, &model.GroupWalletCharge{}, &model.GroupWalletEntry{})
	dialect := common.DatabaseType(os.Getenv("TEST_TASK_DB_DIALECT"))
	var driver gorm.Dialector
	switch dialect {
	case "", common.DatabaseTypeSQLite:
		dialect = common.DatabaseTypeSQLite
		driver = sqlite.Open(filepath.Join(t.TempDir(), "aliases.db"))
	case common.DatabaseTypeMySQL:
		require.NotEmpty(t, os.Getenv("TEST_MYSQL_DSN"))
		driver = mysql.Open(os.Getenv("TEST_MYSQL_DSN"))
	case common.DatabaseTypePostgreSQL:
		require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
		driver = postgres.New(postgres.Config{DSN: os.Getenv("TEST_POSTGRES_DSN"), PreferSimpleProtocol: true})
	default:
		t.Fatalf("unsupported test dialect %q", dialect)
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(tables...))
	t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(tables...)) })
	var version string
	query := "SELECT version()"
	if dialect == common.DatabaseTypeSQLite {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("database: %s %s", dialect, version)
	return db, dialect
}

func TestMiniMaxV2PublicVideoMinimumRequests(t *testing.T) {
	for _, tc := range []struct {
		model, upstream, plugin, path, resolution string
		duration                                  int
	}{
		{"MiniMax-H3", "MiniMax-H3", "jiliu-h3", "/v1/videos", "480P", 4},
		{"wan-3.0", "wand-vega-video-1.5-lite", "tokenhub-media", "/v1/wand/vega-videos/tasks", "480P", 4},
		{"minimax-video-h3-fast", "minimax-video-h3-max", "tokenhub-media", "/v1/wand/minimax-video-v2/generation", "480P", 5},
		{"kling-video-v4-flash", "kling-video-v4-flash", "tokenhub-media", "/v1/wand/kling/text-to-video", "720P", 3},
		{"pixverse-video-v6.0", "pixverse-video-v6.0", "tokenhub-media", "/v1/wand/pixverse/text-to-video", "360P", 1},
		{"pixverse-video-c1", "pixverse-video-c1", "tokenhub-media", "/v1/wand/pixverse/text-to-video", "360P", 1},
		{"pixverse-video-v5.6", "pixverse-video-v5.6", "tokenhub-media", "/v1/wand/pixverse/text-to-video", "360P", 5},
	} {
		t.Run(tc.model, func(t *testing.T) {
			source, err := builtinplugins.Source(tc.plugin)
			require.NoError(t, err)
			plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
			require.NoError(t, err)
			body := map[string]any{
				"model": tc.model, "duration": tc.duration, "resolution": tc.resolution, "ratio": "16:9",
				"content": []any{map[string]any{"type": "text", "text": "A red ball slowly rolls across a white table, static camera."}},
			}
			decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"minimax_video", "decodeRequest"}, map[string]any{
				"model": tc.model, "upstreamModel": tc.upstream, "body": map[string]any{"kind": "json", "value": body},
			})
			require.NoError(t, err)
			intent := decoded.(map[string]any)
			assert.Equal(t, tc.model, intent["model"])
			request, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
				"model": tc.model, "upstreamModel": tc.upstream, "baseUrl": "https://provider.example",
				"apiKey": "synthetic-key", "publicTaskId": "task_public", "requestBody": intent["requestBody"],
			})
			require.NoError(t, err)
			descriptor := request.(map[string]any)
			assert.Equal(t, "POST", descriptor["method"])
			assert.Equal(t, "https://provider.example"+tc.path, descriptor["url"])
			assert.Equal(t, tc.upstream, descriptor["body"].(map[string]any)["model"])

			for _, status := range []struct{ internal, public string }{{"QUEUED", "queued"}, {"IN_PROGRESS", "running"}, {"FAILURE", "failed"}} {
				rendered, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"minimax_video", "render"},
					map[string]any{"model": tc.model, "upstreamModel": tc.upstream},
					map[string]any{"task_id": "task_public", "status": status.internal, "fail_reason": "Rejected"})
				require.NoError(t, err)
				task := rendered.(map[string]any)["task"].(map[string]any)
				assert.Equal(t, "task_public", task["id"])
				assert.Equal(t, tc.model, task["model"])
				assert.Equal(t, status.public, task["status"])
			}
		})
	}
}

func TestPixVerseVideoProtocolContract(t *testing.T) {
	source, err := builtinplugins.Source("tokenhub-media")
	require.NoError(t, err)
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	base := map[string]any{"content": []any{map[string]any{"type": "text", "text": "A cat"}}, "duration": 5, "resolution": "720P", "ratio": "16:9"}
	for _, name := range []string{"pixverse-video-v6.0", "pixverse-video-c1", "pixverse-video-v5.6"} {
		for _, mode := range []string{"text-to-video", "image-to-video", "start-end-to-video", "reference-to-video"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				body := map[string]any{}
				for k, v := range base {
					body[k] = v
				}
				content := []any{map[string]any{"type": "text", "text": "A cat"}}
				if mode == "image-to-video" || mode == "start-end-to-video" {
					content = append(content, map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "https://example.com/first.png"}})
				}
				if mode == "start-end-to-video" {
					content = append(content, map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "https://example.com/last.png"}})
				}
				if mode == "reference-to-video" {
					content = append(content, map[string]any{"type": "image_url", "role": "reference_image", "ref_name": "cat", "reference_type": "subject", "image_url": map[string]any{"url": "https://example.com/cat.png"}})
				}
				body["content"] = content
				body["extra"] = map[string]any{"generate_audio": true, "seed": 0}
				ctx := map[string]any{"model": name, "upstreamModel": name, "baseUrl": "https://tokenhub.tencentmaas.com", "apiKey": "test-only", "requestBody": body}
				intent, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"minimax_video", "decodeRequest"}, map[string]any{"model": name, "body": map[string]any{"kind": "json", "value": body}})
				require.NoError(t, err)
				assert.Equal(t, name, intent.(map[string]any)["model"])
				got, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
				require.NoError(t, err)
				descriptor := got.(map[string]any)
				assert.Equal(t, "https://tokenhub.tencentmaas.com/v1/wand/pixverse/"+mode, descriptor["url"])
				payload := descriptor["body"].(map[string]any)
				assert.Equal(t, name, payload["model"])
				assert.Equal(t, true, payload["generate_audio_switch"])
				if mode == "text-to-video" || mode == "reference-to-video" {
					assert.Equal(t, "16:9", payload["aspect_ratio"])
				} else {
					assert.NotContains(t, payload, "aspect_ratio")
				}
				if mode == "reference-to-video" {
					ref := payload["image_references"].([]any)[0].(map[string]any)
					assert.Equal(t, "cat", ref["ref_name"])
					assert.Equal(t, "https://example.com/cat.png", ref["img_id"])
				}
				parsed, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", ctx, map[string]any{"body": map[string]any{"ErrCode": 0, "Resp": map[string]any{"video_id": "upstream-pixverse"}}})
				require.NoError(t, err)
				state := parsed.(map[string]any)["state"]
				queryCtx := map[string]any{"model": name, "baseUrl": "https://tokenhub.tencentmaas.com", "taskId": "upstream-pixverse", "state": state}
				query, err := plugin.Engine.Call(t.Context(), "buildQueryRequest", queryCtx)
				require.NoError(t, err)
				assert.Equal(t, "https://tokenhub.tencentmaas.com/v1/wand/pixverse/tasks/upstream-pixverse", query.(map[string]any)["url"])
				for _, status := range []struct {
					code int
					want string
				}{{5, "IN_PROGRESS"}, {6, "FAILURE"}, {7, "FAILURE"}, {8, "FAILURE"}, {99, "UNKNOWN"}} {
					result, err := plugin.Engine.Call(t.Context(), "parseTaskResult", queryCtx, map[string]any{"ErrCode": 0, "Resp": map[string]any{"status": status.code}})
					require.NoError(t, err)
					assert.Equal(t, status.want, result.(map[string]any)["status"])
				}
			})
		}
	}
	for _, tc := range []struct {
		model         string
		silent, audio []int
	}{
		{"pixverse-video-v6.0", []int{14665, 20531, 26397, 52794}, []int{20531, 26397, 35196, 67459}},
		{"pixverse-video-c1", []int{17598, 23464, 29330, 55727}, []int{23464, 29330, 38129, 70392}},
		{"pixverse-video-v5.6", []int{20531, 20531, 26397, 43995}, []int{46928, 46928, 52794, 87990}},
	} {
		for index, resolution := range []string{"360P", "540P", "720P", "1080P"} {
			for _, audio := range []bool{false, true} {
				body := map[string]any{"prompt": "cat", "duration": 5, "resolution": resolution, "extra": map[string]any{"generate_audio": audio}}
				usage, err := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"model": tc.model, "requestBody": body})
				require.NoError(t, err)
				rate := tc.silent[index]
				if audio {
					rate = tc.audio[index]
				}
				assert.EqualValues(t, rate*5, usage.(map[string]any)["tokens"], "%s %s audio=%v", tc.model, resolution, audio)
			}
		}
	}
	for _, tc := range []struct {
		name    string
		content []any
	}{
		{"text", []any{map[string]any{"type": "text", "text": "cat"}}},
		{"image", []any{map[string]any{"type": "text", "text": "cat"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}}}},
	} {
		body := map[string]any{"content": tc.content, "extra": map[string]any{"generate_multi_clip": true, "camera_movement": "zoom_in", "seed": 0}}
		got, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"model": "pixverse-video-v6.0", "requestBody": body, "baseUrl": "https://tokenhub.tencentmaas.com"})
		require.NoError(t, err)
		payload := got.(map[string]any)["body"].(map[string]any)
		assert.Equal(t, true, payload["generate_multi_clip_switch"])
		assert.Equal(t, "zoom_in", payload["camera_movement"])
		assert.EqualValues(t, 0, payload["seed"])
	}
	for _, tc := range []struct {
		name, model string
		patch       map[string]any
		want        string
	}{
		{"negative duration", "pixverse-video-v6.0", map[string]any{"duration": -1}, "duration"},
		{"excess duration", "pixverse-video-v6.0", map[string]any{"duration": 16}, "duration"},
		{"legacy 1080p 10s", "pixverse-video-v5.6", map[string]any{"duration": 10, "resolution": "1080P"}, "V5.6"},
		{"legacy wide ratio", "pixverse-video-v5.6", map[string]any{"ratio": "21:9"}, "ratio"},
		{"unsupported multi clip", "pixverse-video-c1", map[string]any{"extra": map[string]any{"generate_multi_clip": true}}, "Multi-clip"},
		{"negative seed", "pixverse-video-v6.0", map[string]any{"extra": map[string]any{"seed": -1}}, "seed"},
		{"wrong audio type", "pixverse-video-v6.0", map[string]any{"extra": map[string]any{"generate_audio": "true"}}, "boolean"},
		{"video reference", "pixverse-video-v6.0", map[string]any{"content": []any{map[string]any{"type": "text", "text": "cat"}, map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://example.com/v.mp4"}}}}, "Unsupported field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{}
			for k, v := range base {
				body[k] = v
			}
			for k, v := range tc.patch {
				body[k] = v
			}
			_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"model": tc.model, "requestBody": body})
			require.ErrorContains(t, err, tc.want)
		})
	}
	ctx := map[string]any{"model": "pixverse-video-v6.0"}
	_, err = plugin.Engine.Call(t.Context(), "parseTaskResult", ctx, map[string]any{"ErrCode": 0, "Resp": map[string]any{"status": 1, "url": "https://example.com/v.mp4"}})
	require.ErrorContains(t, err, "measured token usage is missing")
	result, err := plugin.Engine.Call(t.Context(), "parseTaskResult", ctx, map[string]any{"ErrCode": 401, "ErrMsg": "rejected"})
	require.NoError(t, err)
	assert.Equal(t, "FAILURE", result.(map[string]any)["status"])
	complete := map[string]any{"ErrCode": 0, "Resp": map[string]any{"status": 1, "url": "https://example.com/v.mp4"}, "tokenhub_usage": map[string]any{"total_tokens": 0}}
	usage, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", ctx, map[string]any{"status": "SUCCESS"}, complete)
	require.NoError(t, err)
	assert.EqualValues(t, 0, usage.(map[string]any)["tokens"])
	artifacts, err := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"model": "pixverse-video-v6.0", "status": "SUCCESS", "data": complete})
	require.NoError(t, err)
	assert.Len(t, artifacts, 1)
	content, err := plugin.Engine.Call(t.Context(), "buildContentRequest", map[string]any{"model": "pixverse-video-v6.0", "data": complete, "artifactKey": "video", "clientRequest": map[string]any{"method": "GET"}})
	require.NoError(t, err)
	assert.Equal(t, true, content.(map[string]any)["credentialless"])
	assert.Equal(t, "https://example.com/v.mp4", content.(map[string]any)["url"])
}

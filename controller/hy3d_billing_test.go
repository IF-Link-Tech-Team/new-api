package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hy3dTestExpression = `u("generate_type") == "Geometry" ? tier("geometry", u("model_count") * 1.8 + u("custom_face_count") * 1.2 + u("multi_view") * 1.2 + u("extra_format") * 0.6) : tier("textured", u("model_count") * 2.4 + u("pbr") * 1.2 + u("custom_face_count") * 1.2 + u("multi_view") * 1.2 + u("extra_format") * 0.6)`

func TestHY3DSubmitPollBillAndRefund(t *testing.T) {
	t.Setenv("IFLINK_API_CHANNEL_PRICING", "enabled")
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Channel{}, &model.Ability{}, &model.Task{}, &model.Log{}, &model.BillingRefund{})
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch, oldConsume := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	oldFactory := service.GetTaskAdaptorFunc
	oldGroups := ratio_setting.GroupRatio2JSONString()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, false, true
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor { return relay.GetTaskAdaptor(platform) }
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldMemory, oldBatch, oldConsume
		service.GetTaskAdaptorFunc = oldFactory
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroups))
	})
	withTieredBillingConfig(t, map[string]string{}, map[string]string{})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"hy3d-discount":0.5,"hy3d-free":0}`))
	source, err := builtinplugins.Source("tokenhub-media")
	require.NoError(t, err)
	plugin, err := pluginruntime.CompilePlugin(source, pluginruntime.Options{})
	require.NoError(t, err)
	schema, _ := plugin.Meta.UsageForModel("hy-3d-3.1")
	require.NoError(t, billing_setting.SmokeTestTaskExpr(hy3dTestExpression, schema))
	expressions, err := common.Marshal(map[string]string{"tokenhub-media::hy-3d-3.1": hy3dTestExpression})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: string(expressions)}))
	generation := pluginruntime.DefaultRegistry.Generation()
	binding, found := generation.LookupDeclaredRoute("POST", "/tokenhub-media/v1/3d/generations")
	require.True(t, found)
	for index, tc := range []struct {
		name, body, group string
		cost              float64
		fail              bool
	}{
		{"textured", `{"prompt":"cat"}`, "default", 2.4, false},
		{"geometry", `{"prompt":"cat","generate_type":"Geometry","enable_pbr":true}`, "default", 1.8, false},
		{"all extras", `{"image_url":"https://example.com/a.png","enable_pbr":true,"face_count":100000,"multi_view_images":[{"view_type":"back","view_image_url":"https://example.com/b.png"}],"result_format":"fbx"}`, "default", 6.6, false},
		{"default faces", `{"image_url":"https://example.com/a.png","face_count":500000}`, "default", 2.4, false},
		{"discount", `{"prompt":"cat","enable_pbr":true}`, "hy3d-discount", 1.8, false},
		{"zero multiplier", `{"prompt":"cat"}`, "hy3d-free", 0, false},
		{"failed", `{"prompt":"cat","enable_pbr":true}`, "default", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "Bearer synthetic-key", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/api/3d/submit" {
					_, _ = io.WriteString(w, `{"id":"vendor-3d","status":"queued"}`)
					return
				}
				assert.Equal(t, "/v1/api/3d/query", r.URL.Path)
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Contains(t, string(body), `"id":"vendor-3d"`)
				if tc.fail {
					_, _ = io.WriteString(w, `{"status":"failed","error":{"message":"rejected"}}`)
				} else {
					_, _ = io.WriteString(w, `{"status":"completed","data":[{"type":"glb","url":"https://example.com/cat.glb","preview_image_url":"https://example.com/cat.png"}]}`)
				}
			}))
			defer server.Close()
			initial := common.QuotaRound(100 * common.QuotaPerUnit)
			user := model.User{Username: fmt.Sprintf("hy3d-%d", index), AffCode: fmt.Sprintf("hy3d-%d", index), Quota: initial}
			require.NoError(t, db.Create(&user).Error)
			baseURL, setting := server.URL, `{"task_plugin_key":"tokenhub-media"}`
			ch := model.Channel{Name: tc.name, Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled, Key: "synthetic-key", BaseURL: &baseURL, Setting: &setting, Group: tc.group, Models: "hy-3d-3.1"}
			require.NoError(t, db.Create(&ch).Error)
			c := taskSubmissionTestContext()
			c.Request = httptest.NewRequest("POST", "/tokenhub-media/v1/3d/generations", strings.NewReader(`{"model":"hy-3d-3.1",`+strings.TrimPrefix(tc.body, "{")))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("group", tc.group)
			c.Set("username", user.Username)
			c.Set(pluginruntime.ContextKeyPinnedRoute, pluginruntime.PinnedRoute{Generation: generation, Plugin: binding.Plugin, Route: binding.Route})
			middleware.PrepareTaskPluginRoute()(c)
			require.False(t, c.IsAborted())
			policy, err := service.ResolveChannelSelectionPolicy(c, "hy-3d-3.1")
			require.NoError(t, err)
			require.Nil(t, policy)
			require.Nil(t, middleware.SetupContextForSelectedChannel(c, &ch, "hy-3d-3.1"))
			info := taskSubmissionRelayInfo(nil)
			info.UserId, info.OriginModelName, info.UserGroup, info.UsingGroup = user.Id, "hy-3d-3.1", tc.group, tc.group
			info.IsPlayground = true
			info.UserSetting.BillingPreference = "wallet_only"
			info.PublicTaskID, info.LockedChannel = model.GenerateTaskID(), &ch
			outcome, taskErr := executeTaskSubmission(c, info)
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			task := outcome.Task
			service.DispatchPlatformUpdate(context.Background(), task.Platform, map[int][]string{ch.Id: {"vendor-3d"}}, map[string]*model.Task{"vendor-3d": task})
			require.NoError(t, db.First(task, task.ID).Error)
			if tc.fail {
				assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), task.Status)
			} else {
				assert.Equal(t, model.TaskStatus(model.TaskStatusSuccess), task.Status)
			}
			var updated model.User
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-common.QuotaRound(tc.cost*common.QuotaPerUnit), updated.Quota)
			// Terminal replay must not charge or refund twice.
			service.DispatchPlatformUpdate(context.Background(), task.Platform, map[int][]string{ch.Id: {"vendor-3d"}}, map[string]*model.Task{"vendor-3d": task})
			require.NoError(t, db.First(&updated, user.Id).Error)
			assert.Equal(t, initial-common.QuotaRound(tc.cost*common.QuotaPerUnit), updated.Quota)
			assert.GreaterOrEqual(t, calls, 2)
			if !tc.fail {
				artifacts, err := projectTaskArtifacts(task)
				require.NoError(t, err)
				assert.Len(t, artifacts, 2)
			}
			common.CleanupBodyStorage(c)
		})
	}
}

package plugins_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/relay/channel"
	taskplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadVODPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("tencent-vod")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "tencent-vod", Now: func() time.Time { return time.Unix(1551113065, 0) }})
	require.NoError(t, err)
	return plugin
}

func vodVideoContext(model string) map[string]any {
	return map[string]any{"model": model, "upstreamModel": model, "apiKey": "123456|test-id|test-secret", "baseUrl": "https://vod.tencentcloudapi.com", "publicTaskId": "task_public", "requestBody": map[string]any{
		"Prompt": "海边与风", "OutputConfig": map[string]any{"Duration": 5, "Resolution": "480P"},
	}}
}

func vodFinishedTask(model string, inputSeconds, outputSeconds int) map[string]any {
	return map[string]any{"Response": map[string]any{"AigcVideoTask": map[string]any{
		"Status": "FINISH", "ErrCode": 0, "ErrCodeExt": "", "TaskId": "upstream-private",
		"Input": map[string]any{"ModelVersion": strings.TrimPrefix(model, "wand-vega-video-"), "OutputConfig": map[string]any{"Resolution": "480P"}},
		"Output": map[string]any{
			"Usage":     map[string]any{"InputSeconds": inputSeconds, "OutputSeconds": outputSeconds, "InputImageCount": 2},
			"FileInfos": []any{map[string]any{"FileUrl": "https://cdn.example/result.mp4", "UsageType": ""}},
		},
	}}}
}

func TestVODProtocolsAndSignedSubmission(t *testing.T) {
	p := loadVODPlugin(t)
	for _, name := range p.Meta.Models {
		t.Run(name, func(t *testing.T) {
			ctx := vodVideoContext(name)
			isImage := strings.Contains(name, "image")
			protocol := "openai_video"
			body := map[string]any{"model": name, "prompt": "海边与风", "resolution": "480P", "seconds": 5, "generate_audio": false}
			if isImage {
				protocol = "openai_image"
				body = map[string]any{"model": name, "prompt": "海边与风", "size": "2K", "n": 1}
			}
			intent, err := p.Engine.CallPath(t.Context(), "protocols", []string{protocol, "decodeRequest"}, map[string]any{"model": name, "body": map[string]any{"kind": "json", "value": body}})
			require.NoError(t, err)
			ctx["requestBody"] = intent.(map[string]any)["requestBody"]
			got, err := p.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.NoError(t, err)
			descriptor := got.(map[string]any)
			assert.Equal(t, "https://vod.tencentcloudapi.com/", descriptor["url"])
			headers := descriptor["headers"].(map[string]string)
			expectedAction := "CreateAigcVideoTask"
			if isImage {
				expectedAction = "CreateAigcImageTask"
			}
			assert.Equal(t, expectedAction, headers["X-TC-Action"])
			assert.Equal(t, "2018-07-17", headers["X-TC-Version"])
			assert.Equal(t, "1551113065", headers["X-TC-Timestamp"])
			assert.Contains(t, headers["Authorization"], "test-id/2019-02-25/vod/tc3_request")
			assert.NotContains(t, descriptor["body"], "test-secret")
			var outbound map[string]any
			require.NoError(t, common.UnmarshalJsonStr(descriptor["body"].(string), &outbound))
			assert.Equal(t, float64(123456), outbound["SubAppId"])
			assert.Equal(t, "task_public", outbound["SessionId"])
			assert.Equal(t, "海边与风", outbound["Prompt"])
			if !isImage {
				assert.Equal(t, "Disabled", outbound["OutputConfig"].(map[string]any)["AudioGeneration"])
				var ext map[string]string
				require.NoError(t, common.UnmarshalJsonStr(outbound["ExtInfo"].(string), &ext))
				assert.Contains(t, ext["WandVegaParameters"], `"ReturnVegaUsage":true`)
			}
		})
	}
}

func TestVODTC3SignatureExactBodyAndCredentialBoundary(t *testing.T) {
	engine, err := jsplugin.Compile(`export function sign(v) { return utils.tencentSignTC3(v); }`, jsplugin.Options{Now: func() time.Time { return time.Unix(1551113065, 0) }})
	require.NoError(t, err)
	request := map[string]any{"url": "https://vod.tencentcloudapi.com", "body": `{"Prompt":"海边与风","SubAppId":123456}`, "secretId": "test-id", "secretKey": "test-secret", "action": "CreateAigcVideoTask", "version": "2018-07-17", "token": "temporary-token"}
	got, err := engine.Call(t.Context(), "sign", request)
	require.NoError(t, err)
	headers := got.(map[string]string)
	// Independently calculated with Python hashlib/hmac from the Tencent TC3 reference.
	assert.Equal(t, "TC3-HMAC-SHA256 Credential=test-id/2019-02-25/vod/tc3_request, SignedHeaders=content-type;host;x-tc-action, Signature=f82787864837372711479e1aae845ce9d14d4f58f401604e2620c7121b8bd8af", headers["Authorization"])
	assert.Equal(t, "temporary-token", headers["X-TC-Token"])
	request["body"] = `{"Prompt":"海边与风", "SubAppId":123456}`
	changed, err := engine.Call(t.Context(), "sign", request)
	require.NoError(t, err)
	assert.NotEqual(t, headers["Authorization"], changed.(map[string]string)["Authorization"])
	for _, address := range []string{"http://vod.tencentcloudapi.com", "https://user:pass@vod.tencentcloudapi.com", "https://vod.tencentcloudapi.com/?key=secret", "https://vod.tencentcloudapi.com/elsewhere"} {
		request["url"] = address
		_, err := engine.Call(t.Context(), "sign", request)
		require.Error(t, err)
	}
	request["url"] = "https://vod.tencentcloudapi.com"
	request["secretId"] = "test-id\r\nInjected: value"
	_, err = engine.Call(t.Context(), "sign", request)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "Injected")
}

func TestVODNativeAndReferenceValidation(t *testing.T) {
	p := loadVODPlugin(t)
	for _, tc := range []struct {
		name    string
		change  func(map[string]any)
		message string
	}{
		{"duration overflow", func(b map[string]any) { b["OutputConfig"] = map[string]any{"Duration": 3601} }, "Duration"},
		{"unsupported resolution", func(b map[string]any) { b["OutputConfig"] = map[string]any{"Resolution": "8K"} }, "Resolution"},
		{"audio only", func(b map[string]any) {
			b["FileInfos"] = []any{map[string]any{"Category": "Audio", "Type": "Url", "Url": "https://cdn.example/a.mp3"}}
		}, "audio references"},
		{"last frame only", func(b map[string]any) {
			b["FileInfos"] = []any{map[string]any{"Category": "Image", "Type": "Url", "Url": "https://cdn.example/a.png", "Usage": "LastFrame"}}
		}, "first frame"},
		{"local URL", func(b map[string]any) {
			b["FileInfos"] = []any{map[string]any{"Category": "Video", "Type": "Url", "Url": "file:///etc/passwd"}}
		}, "reference Url"},
		{"invalid ext", func(b map[string]any) { b["ExtInfo"] = `{"AdditionalParameters":42}` }, "AdditionalParameters"},
		{"invalid model", func(b map[string]any) { b["model"] = "constructor" }, "unsupported WAND"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model": "wand-vega-video-1.5-pro", "Prompt": "ocean"}
			tc.change(body)
			_, err := p.Engine.CallMember(t.Context(), "native", "createVideo", map[string]any{"body": map[string]any{"kind": "json", "value": body}})
			require.ErrorContains(t, err, tc.message)
		})
	}
	intent, err := p.Engine.CallMember(t.Context(), "native", "createVideo", map[string]any{"body": map[string]any{"kind": "json", "value": map[string]any{
		"ModelName": "wand-vega-video", "ModelVersion": "1.5-pro", "Prompt": "ocean", "FileInfos": []any{map[string]any{"Type": "Url", "Url": "asset://asset-safe", "Category": "Image", "Usage": "Reference"}},
	}}})
	require.NoError(t, err)
	assert.Equal(t, "wand-vega-video-1.5-pro", intent.(map[string]any)["model"])
	ctx := vodVideoContext("wand-vega-video-1.5-pro")
	ctx["apiKey"] = "bad-format"
	_, err = p.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.ErrorContains(t, err, "channel key")
}

func TestVODMeasuredBillingAndTerminalStates(t *testing.T) {
	p := loadVODPlugin(t)
	for _, tc := range []struct {
		name, model   string
		input, output int
		hasVideo      bool
		wantInput     float64
	}{
		{"text", "wand-vega-video-1.5-pro", 0, 5, false, 0},
		{"lite", "wand-vega-video-1.0-lite", 2, 5, true, 2},
		{"pro reference minimum", "wand-vega-video-1.5-pro", 2, 5, true, 4},
		{"pro long reference", "wand-vega-video-1.0-pro", 12, 5, true, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := vodFinishedTask(tc.model, tc.input, tc.output)
			ctx := map[string]any{"model": tc.model, "state": map[string]any{"hasVideo": tc.hasVideo, "inputImages": 2, "resolution": "480P"}}
			got, err := p.Engine.Call(t.Context(), "parseTaskResult", ctx, body)
			require.NoError(t, err)
			assert.Equal(t, "SUCCESS", got.(map[string]any)["status"])
			got, err = p.Engine.Call(t.Context(), "extractUsageOnComplete", ctx, map[string]any{"status": "SUCCESS"}, body)
			require.NoError(t, err)
			encoded, err := common.Marshal(got)
			require.NoError(t, err)
			var facts map[string]any
			require.NoError(t, common.Unmarshal(encoded, &facts))
			assert.Equal(t, tc.wantInput, facts["input_video_seconds"])
			for _, ratio := range []float64{1, 0} {
				snap := &billingexpr.BillingSnapshot{ExprString: `(u("seconds") + u("input_video_seconds")) * 0.1`, ExprVersion: 1, TaskUsageBilling: true, QuotaPerUnit: 500000, GroupRatio: ratio, UsageFacts: map[string]any{}}
				settled, _, err := service.EvaluateTaskCompletionUsage(snap, facts)
				require.NoError(t, err)
				assert.Equal(t, common.QuotaRound((float64(tc.output)+tc.wantInput)*0.1*500000*ratio), settled.ActualQuotaAfterGroup)
			}
			if tc.hasVideo {
				delete(body["Response"].(map[string]any)["AigcVideoTask"].(map[string]any)["Output"].(map[string]any)["Usage"].(map[string]any), "InputSeconds")
				_, err = p.Engine.Call(t.Context(), "parseTaskResult", ctx, body)
				require.ErrorContains(t, err, "InputSeconds")
			}
		})
	}
	ctx := map[string]any{"model": "wand-vega-video-1.5-pro"}
	for _, tc := range []struct {
		status  string
		errCode int
		want    string
	}{{"PROCESSING", 0, "IN_PROGRESS"}, {"WAITING", 0, "QUEUED"}, {"FINISH", 70000, "FAILURE"}, {"mystery", 0, "UNKNOWN"}} {
		got, err := p.Engine.Call(t.Context(), "parseTaskResult", ctx, map[string]any{"Response": map[string]any{"AigcVideoTask": map[string]any{"Status": tc.status, "ErrCode": tc.errCode, "Message": "provider failure"}}})
		require.NoError(t, err)
		assert.Equal(t, tc.want, got.(map[string]any)["status"])
	}
	_, err := p.Engine.Call(t.Context(), "parseTaskResult", ctx, map[string]any{"Response": map[string]any{"Error": map[string]any{"Code": "AuthFailure", "Message": "denied"}}})
	require.ErrorContains(t, err, "AuthFailure")
}

func TestVODImageDeliveryAndUsage(t *testing.T) {
	p := loadVODPlugin(t)
	body := map[string]any{"Response": map[string]any{"AigcImageTask": map[string]any{
		"Status": "FINISH", "ErrCode": 0, "Input": map[string]any{"OutputConfig": map[string]any{"Resolution": "2K"}},
		"Output": map[string]any{"FileInfos": []any{map[string]any{"FileUrl": "https://cdn.example/1.png"}, map[string]any{"FileUrl": "https://cdn.example/2.png"}, map[string]any{"FileUrl": "https://cdn.example/2.png"}, map[string]any{"FileId": "no-content"}}},
	}}}
	ctx := map[string]any{"model": "wand-vega-image-1.0-flash", "state": map[string]any{"inputImages": 1}}
	got, err := p.Engine.Call(t.Context(), "extractUsageOnComplete", ctx, map[string]any{"status": "SUCCESS"}, body)
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.(map[string]any)["image_count"])
	rendered, err := p.Engine.CallPath(t.Context(), "protocols", []string{"openai_image", "render"}, map[string]any{}, map[string]any{"data": body})
	require.NoError(t, err)
	assert.Len(t, rendered.(map[string]any)["data"], 2)
	content, err := p.Engine.Call(t.Context(), "buildContentRequest", map[string]any{"model": "wand-vega-image-1.0-flash", "data": body, "artifactKey": "image_1", "clientRequest": map[string]any{"method": "GET"}})
	require.NoError(t, err)
	assert.Equal(t, true, content.(map[string]any)["credentialless"])
	assert.NotContains(t, content.(map[string]any), "headers")
}

func TestVODDraftOwnershipMappingAndEstimates(t *testing.T) {
	p := loadVODPlugin(t)
	ctx := vodVideoContext("wand-vega-video-1.5-pro")
	ctx["requestBody"].(map[string]any)["OutputConfig"] = map[string]any{"Duration": -1, "Resolution": "480P"}
	got, err := p.Engine.Call(t.Context(), "extractUsage", ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(30), got.(map[string]any)["seconds"])
	body := map[string]any{"model": "wand-vega-video-1.5-pro", "OutputConfig": map[string]any{"Resolution": "1080P"}, "ExtInfo": `{"AdditionalParameters":"{\"draft_task_id\":\"task_draft\"}"}`}
	intent, err := p.Engine.CallMember(t.Context(), "native", "createVideo", map[string]any{"body": map[string]any{"kind": "json", "value": body}})
	require.NoError(t, err)
	assert.Equal(t, []any{"task_draft"}, intent.(map[string]any)["originTaskIds"])
	ctx["requestBody"] = intent.(map[string]any)["requestBody"]
	_, err = p.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.ErrorContains(t, err, "owned draft")
	draft := vodFinishedTask("wand-vega-video-1.5-pro", 0, 5)
	draft["Response"].(map[string]any)["AigcVideoTask"].(map[string]any)["Input"].(map[string]any)["ExtInfo"] = `{"AdditionalParameters":"{\"draft\":true}"}`
	ctx["originTasks"] = []any{map[string]any{"taskId": "task_draft", "upstreamTaskId": "upstream-draft-id", "status": "SUCCESS", "data": draft}}
	got, err = p.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	assert.Contains(t, got.(map[string]any)["body"], "upstream-draft-id")
	assert.NotContains(t, got.(map[string]any)["body"], "task_draft")
	got, err = p.Engine.Call(t.Context(), "extractUsage", ctx)
	require.NoError(t, err)
	assert.Equal(t, "1080P", got.(map[string]any)["resolution"])
}

func TestVODHostRequestAndArtifactAdapters(t *testing.T) {
	p := loadVODPlugin(t)
	name := "wand-vega-video-1.5-pro"
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeTaskPlugin, ChannelBaseUrl: "https://vod.tencentcloudapi.com", ApiKey: "123456|test-id|test-secret", UpstreamModelName: name},
		OriginModelName: name, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	a := taskplugin.New(p)
	a.Init(info)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/vod/v1/videos", nil)
	c.Set("task_request", vodVideoContext(name)["requestBody"])
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	url, err := a.BuildRequestURL(info)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, nil)
	require.NoError(t, err)
	require.NoError(t, a.BuildRequestHeader(c, req, info))
	assert.Equal(t, "CreateAigcVideoTask", req.Header.Get("X-TC-Action"))
	assert.Contains(t, string(body), `"Prompt":"海边与风"`)
	assert.NotContains(t, string(body), "test-secret")

	video := vodFinishedTask(name, 0, 5)
	data, err := common.Marshal(video)
	require.NoError(t, err)
	task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: name, UpstreamModelName: name}, Data: data}
	items, err := a.ListArtifacts(task)
	require.NoError(t, err)
	assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video"}}, items)
	content, err := a.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodHead})
	require.NoError(t, err)
	assert.True(t, content.Credentialless)
	assert.Empty(t, content.Headers)
	assert.Equal(t, "https://cdn.example/result.mp4", content.URL)
	public, err := a.ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	assert.Contains(t, string(public), "task_public")
	assert.NotContains(t, string(public), "upstream-private")

	image := map[string]any{"Response": map[string]any{"AigcImageTask": map[string]any{"Output": map[string]any{"FileInfos": []any{map[string]any{"FileUrl": "https://cdn.example/image.png"}}}}}}
	task.Data, err = common.Marshal(image)
	require.NoError(t, err)
	items, err = a.ListArtifacts(task)
	require.NoError(t, err)
	assert.Equal(t, []channel.TaskArtifact{{Key: "image_0", Type: "image"}}, items)
	content, err = a.BuildContentRequest(t.Context(), task, "image_0", channel.TaskArtifactClientRequest{Method: http.MethodGet})
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example/image.png", content.URL)
	assert.True(t, content.Credentialless)
}

func TestVODAliasesReferencesAndGatewayHop(t *testing.T) {
	p := loadVODPlugin(t)
	name := "wand-vega-video-1.5-pro"
	decoded, err := p.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
		"model": "my-video", "upstreamModel": name,
		"body": map[string]any{"kind": "json", "value": map[string]any{
			"model": "my-video", "prompt": "scene", "resolution": "720P", "seconds": "5",
			"content": []any{
				map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "https://cdn.example/first.png"}},
				map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "https://cdn.example/last.png"}},
			},
		}},
	})
	require.NoError(t, err)
	intent := decoded.(map[string]any)
	assert.Equal(t, "my-video", intent["model"])
	ctx := vodVideoContext(name)
	ctx["model"], ctx["requestBody"] = "my-video", intent["requestBody"]
	value, err := p.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	var outbound map[string]any
	require.NoError(t, common.UnmarshalJsonStr(value.(map[string]any)["body"].(string), &outbound))
	assert.Equal(t, "wand-vega-video", outbound["ModelName"])
	assert.Equal(t, "FirstFrame", outbound["FileInfos"].([]any)[0].(map[string]any)["Usage"])
	assert.Equal(t, "LastFrame", outbound["FileInfos"].([]any)[1].(map[string]any)["Usage"])
	ctx["upstream"] = map[string]any{"kind": "new_api"}
	ctx["baseUrl"], ctx["apiKey"] = "https://another.example", "gateway-key"
	value, err = p.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://another.example/vod/v1/videos", value.(map[string]any)["url"])
	assert.Equal(t, "Bearer gateway-key", value.(map[string]any)["headers"].(map[string]any)["Authorization"])
	value, err = p.Engine.Call(t.Context(), "buildQueryRequest", map[string]any{"upstream": ctx["upstream"], "baseUrl": ctx["baseUrl"], "apiKey": ctx["apiKey"], "taskId": "task/a"})
	require.NoError(t, err)
	assert.Equal(t, "https://another.example/vod/v1/tasks/task%2Fa", value.(map[string]any)["url"])
}

func TestVODMiniMaxV2Conversion(t *testing.T) {
	p := loadVODPlugin(t)
	for _, name := range p.Meta.Models {
		if strings.Contains(name, "image") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			resolution := "720P"
			if name == "wand-vega-video-1.0-lite" || name == "MiniMax-H3" {
				resolution = "768P"
			}
			request := map[string]any{"model": name, "duration": 5, "resolution": resolution, "ratio": "16:9", "content": []any{map[string]any{"type": "text", "text": "海边与风"}}}
			got, err := p.Engine.CallMember(t.Context(), "native", "createMiniMaxVideo", map[string]any{"body": map[string]any{"kind": "json", "value": request}})
			require.NoError(t, err)
			native, err := p.Engine.CallMember(t.Context(), "native", "createVideo", map[string]any{"body": map[string]any{"kind": "json", "value": map[string]any{
				"model": name, "Prompt": "海边与风", "FileInfos": []any{}, "OutputConfig": map[string]any{"Duration": 5, "Resolution": resolution, "AspectRatio": "16:9"},
			}}})
			require.NoError(t, err)
			assert.Equal(t, native, got, "both protocols must use the same VOD request and billing facts")
			aliasRequest := make(map[string]any, len(request))
			for k, v := range request {
				aliasRequest[k] = v
			}
			aliasRequest["model"] = "H3-VOD"
			shared, err := p.Engine.CallPath(t.Context(), "protocols", []string{"minimax_video", "decodeRequest"}, map[string]any{"model": "H3-VOD", "upstreamModel": name, "body": map[string]any{"kind": "json", "value": aliasRequest}})
			require.NoError(t, err)
			assert.Equal(t, "H3-VOD", shared.(map[string]any)["model"])
			assert.Equal(t, got.(map[string]any)["requestBody"], shared.(map[string]any)["requestBody"])

			ctx := vodVideoContext(name)
			ctx["requestBody"] = got.(map[string]any)["requestBody"]
			built, err := p.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.NoError(t, err)
			assert.Equal(t, "CreateAigcVideoTask", built.(map[string]any)["headers"].(map[string]string)["X-TC-Action"])
			var outbound map[string]any
			require.NoError(t, common.UnmarshalJsonStr(built.(map[string]any)["body"].(string), &outbound))
			version := strings.TrimPrefix(name, "wand-vega-video-")
			if name == "MiniMax-H3" {
				version = "1.0-lite"
			}
			if name == "wan-3.0" {
				version = "1.5-lite"
			}
			assert.Equal(t, version, outbound["ModelVersion"])
			assert.Equal(t, "task_public", outbound["SessionId"])
			assert.NotContains(t, outbound, "content")
		})
	}
	for _, tc := range []struct {
		name    string
		content []any
		ratio   string
		want    []any
	}{
		{"first and last frame", []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example/first.png"}},
			map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "https://cdn.example/last.png"}},
		}, "adaptive", []any{
			map[string]any{"Type": "Url", "Url": "https://cdn.example/first.png", "Category": "Image", "Usage": "FirstFrame"},
			map[string]any{"Type": "Url", "Url": "https://cdn.example/last.png", "Category": "Image", "Usage": "LastFrame"},
		}},
		{"image video and audio references", []any{
			map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "https://cdn.example/image.png"}},
			map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://cdn.example/video.mp4"}},
			map[string]any{"type": "audio_url", "audio_url": map[string]any{"url": "https://cdn.example/audio.mp3"}},
		}, "16:9", []any{
			map[string]any{"Type": "Url", "Url": "https://cdn.example/image.png", "Category": "Image", "Usage": "Reference"},
			map[string]any{"Type": "Url", "Url": "https://cdn.example/video.mp4", "Category": "Video", "Usage": "Reference"},
			map[string]any{"Type": "Url", "Url": "https://cdn.example/audio.mp3", "Category": "Audio", "Usage": "Reference"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := append([]any{map[string]any{"type": "text", "text": "Follow <Picture 1>"}}, tc.content...)
			got, err := p.Engine.CallMember(t.Context(), "native", "createMiniMaxVideo", map[string]any{"body": map[string]any{"kind": "json", "value": map[string]any{
				"model": "wand-vega-video-1.0-lite", "duration": 5, "resolution": "768P", "ratio": "16:9", "content": content,
			}}})
			require.NoError(t, err)
			body := got.(map[string]any)["requestBody"].(map[string]any)
			assert.Equal(t, tc.want, body["FileInfos"])
			assert.Equal(t, tc.ratio, body["OutputConfig"].(map[string]any)["AspectRatio"])
			assert.Equal(t, "Follow <Picture 1>", body["Prompt"])
		})
	}
}

func TestVODMiniMaxV2RejectsUnsupportedRequests(t *testing.T) {
	p := loadVODPlugin(t)
	for _, tc := range []struct {
		name, field string
		value       any
		message     string
	}{
		{"image model", "model", "wand-vega-image-1.0-lite", "WAND-Vega video model"},
		{"unrelated model", "model", "unsupported-model", "WAND-Vega video model"},
		{"missing resolution", "resolution", nil, "resolution is required"},
		{"wrong resolution", "resolution", "8K", "Resolution"},
		{"fractional duration", "duration", 5.5, "positive integer"},
		{"string duration", "duration", "5", "positive integer"},
		{"automatic duration", "duration", -1, "positive integer"},
		{"oversized duration", "duration", 3601, "Duration"},
		{"callback", "callback_url", "https://client.example/callback", "callbacks"},
		{"watermark", "aigc_watermark", false, "aigc_watermark"},
		{"unmapped option", "extra", map[string]any{"prompt_expansion_mode": "balanced"}, "extra"},
		{"adaptive text", "ratio", "adaptive", "explicit ratio"},
		{"empty content", "content", []any{}, "non-empty array"},
		{"missing prompt", "content", []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example/first.png"}}}, "text item"},
		{"wrong media role", "content", []any{map[string]any{"type": "text", "text": "scene"}, map[string]any{"type": "video_url", "role": "first_frame", "video_url": map[string]any{"url": "https://cdn.example/video.mp4"}}}, "unsupported role"},
		{"last frame alone", "content", []any{map[string]any{"type": "text", "text": "scene"}, map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "https://cdn.example/last.png"}}}, "first frame"},
		{"inline video", "content", []any{map[string]any{"type": "text", "text": "scene"}, map[string]any{"type": "video_url", "video_url": map[string]any{"url": "data:video/mp4;base64,AAAA"}}}, "reference Url"},
		{"mixed frames", "content", []any{map[string]any{"type": "text", "text": "scene"}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://cdn.example/first.png"}}, map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://cdn.example/video.mp4"}}}, "cannot be mixed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"model": "wand-vega-video-1.0-lite", "duration": 5, "resolution": "768P", "ratio": "16:9", "content": []any{map[string]any{"type": "text", "text": "scene"}}}
			body[tc.field] = tc.value
			_, err := p.Engine.CallMember(t.Context(), "native", "createMiniMaxVideo", map[string]any{"body": map[string]any{"kind": "json", "value": body}})
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestVODMiniMaxV2TaskPresentation(t *testing.T) {
	p := loadVODPlugin(t)
	for _, tc := range []struct{ status, want string }{{"QUEUED", "queued"}, {"IN_PROGRESS", "running"}, {"FAILURE", "failed"}, {"unknown", "unknown"}} {
		t.Run(tc.status, func(t *testing.T) {
			got, err := p.Engine.CallMember(t.Context(), "native", "miniMaxStatus", map[string]any{}, map[string]any{"task_id": "task_public", "status": tc.status, "created_at": 123, "fail_reason": "Provider rejected input", "properties": map[string]any{"origin_model_name": "wand-vega-video-1.0-lite"}})
			require.NoError(t, err)
			result := got.(map[string]any)["task"].(map[string]any)
			assert.Equal(t, tc.want, result["status"])
			assert.Equal(t, "task_public", result["id"])
			assert.NotContains(t, result, "content")
			if tc.status == "FAILURE" {
				assert.Equal(t, "Provider rejected input", result["error"].(map[string]any)["message"])
			}
		})
	}
	body := vodFinishedTask("wand-vega-video-1.5-pro", 1, 5)
	node := body["Response"].(map[string]any)["AigcVideoTask"].(map[string]any)
	node["Input"].(map[string]any)["FileInfos"] = []any{map[string]any{"Category": "Video"}}
	node["Input"].(map[string]any)["OutputConfig"] = map[string]any{"Resolution": "720P", "AspectRatio": "16:9"}
	got, err := p.Engine.CallMember(t.Context(), "native", "miniMaxStatus", map[string]any{}, map[string]any{
		"task_id": "task_public", "status": "SUCCESS", "created_at": 123, "updated_at": 456, "data": body,
		"properties": map[string]any{"origin_model_name": "video-alias", "upstream_model_name": "wand-vega-video-1.5-pro"},
	})
	require.NoError(t, err)
	result := got.(map[string]any)["task"].(map[string]any)
	assert.Equal(t, "video-alias", result["model"])
	assert.Equal(t, "succeeded", result["status"])
	assert.Equal(t, "16:9", result["ratio"])
	assert.Equal(t, int64(5), result["duration"])
	assert.Equal(t, map[string]any{"url": "https://cdn.example/result.mp4"}, result["content"])
	assert.Equal(t, map[string]any{"input_seconds": int64(4), "output_seconds": int64(5), "total_seconds": int64(9), "input_image_count": int64(0)}, result["usage"])
	encoded, err := common.Marshal(got)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "upstream-private")
	created, err := p.Engine.CallMember(t.Context(), "native", "miniMaxCreated", map[string]any{}, map[string]any{"task_id": "task_public", "data": body})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"task_id": "task_public"}, created)

	for _, path := range []string{"/vod/v2/video_generation", "/vod/v1/videos"} {
		got, err := p.Engine.CallMember(t.Context(), "native", "error", map[string]any{"path": path}, map[string]any{"code": "invalid_request", "message": "Invalid request", "httpStatus": 400, "requestId": "request-1"})
		require.NoError(t, err)
		if path == "/vod/v1/videos" {
			assert.Equal(t, map[string]any{"code": "invalid_request", "message": "Invalid request (request id: request-1)", "data": nil}, got)
		} else {
			assert.Equal(t, map[string]any{"type": "error", "request_id": "request-1", "error": map[string]any{"type": "invalid_request", "message": "Invalid request", "http_code": "400"}}, got)
		}
	}
}

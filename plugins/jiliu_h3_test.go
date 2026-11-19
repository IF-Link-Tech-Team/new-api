package plugins_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
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

func loadJiliuH3Plugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("jiliu-h3")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "jiliu-h3"})
	require.NoError(t, err)
	return plugin
}

func jiliuH3Request(task string, conditions ...any) map[string]any {
	request := map[string]any{
		"model": "MiniMax-H3", "task": task, "prompt": "A calm ocean with natural wave sounds",
		"target": map[string]any{"duration_seconds": 5, "short_edge": 768, "aspect_ratio": "16:9"},
	}
	if len(conditions) > 0 {
		request["conditions"] = conditions
	}
	return request
}

func TestJiliuH3Registration(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	assert.Equal(t, "https://api.jiliu.cn", plugin.Meta.BaseURL)
	assert.Empty(t, plugin.Meta.ChannelTypes)
	assert.Equal(t, []string{"MiniMax-H3"}, plugin.Meta.Models)
	registry := jsplugin.NewRegistry()
	for _, key := range []string{"hailuo", "jiliu-h3"} {
		source, err := builtinplugins.Source(key)
		require.NoError(t, err)
		_, err = registry.RegisterFactory(source, jsplugin.Options{Key: key})
		require.NoError(t, err, "the standalone adapter must coexist with official MiniMax")
	}
	binding, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", "MiniMax-H3")
	require.True(t, found)
	assert.Equal(t, "openai_video", binding.Protocol)
	for _, route := range plugin.Meta.Routes {
		binding, found := registry.Generation().LookupDeclaredRoute(route.Method, route.Path)
		require.True(t, found)
		assert.Equal(t, "jiliu-h3", binding.Plugin.Meta.Key)
	}
}

func jiliuH3OfficialRequest() map[string]any {
	return map[string]any{
		"model": "MiniMax-H3", "content": []any{map[string]any{"type": "text", "text": "A calm ocean with natural wave sounds"}},
		"duration": 5, "resolution": "768P", "ratio": "16:9",
	}
}

func TestJiliuH3OfficialFormatTranslation(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	for _, tc := range []struct {
		name  string
		items []any
		want  map[string]any
	}{
		{"text", nil, jiliuH3Request("t2va")},
		{"video defaults to reference", []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://assets.example/motion.mp4"}}},
			jiliuH3Request("ref2va", map[string]any{"type": "video", "role": "reference", "uri": "https://assets.example/motion.mp4"})},
		{"references", []any{
			map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "https://assets.example/product.jpg"}},
			map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "https://assets.example/motion.mp4"}},
		}, jiliuH3Request("ref2va",
			map[string]any{"type": "image", "role": "reference", "uri": "https://assets.example/product.jpg"},
			map[string]any{"type": "video", "role": "reference", "uri": "https://assets.example/motion.mp4"})},
		{"first frame", []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://assets.example/first.jpg"}}},
			jiliuH3Request("fl2va", map[string]any{"type": "image", "role": "keyframe", "frame_index": 0, "uri": "https://assets.example/first.jpg"})},
		{"first and last", []any{
			map[string]any{"type": "image_url", "role": "first_frame", "image_url": map[string]any{"url": "https://assets.example/first.jpg"}},
			map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "https://assets.example/last.jpg"}},
		}, jiliuH3Request("fl2va",
			map[string]any{"type": "image", "role": "keyframe", "frame_index": 0, "uri": "https://assets.example/first.jpg"},
			map[string]any{"type": "image", "role": "keyframe", "frame_index": -1, "uri": "https://assets.example/last.jpg"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := jiliuH3OfficialRequest()
			request["content"] = append(request["content"].([]any), tc.items...)
			decoded, err := plugin.Engine.CallMember(t.Context(), "native", "createTask", map[string]any{"body": map[string]any{"kind": "json", "value": request}})
			require.NoError(t, err)
			intent := decoded.(map[string]any)
			want, err := common.Marshal(tc.want)
			require.NoError(t, err)
			actual, err := common.Marshal(intent["requestBody"])
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(actual))
			ctx := map[string]any{"baseUrl": "https://api.jiliu.cn", "apiKey": "test-key", "model": "MiniMax-H3", "upstreamModel": "MiniMax-H3", "publicTaskId": "task_public", "requestBody": intent["requestBody"]}
			descriptor, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
			require.NoError(t, err)
			assert.Equal(t, "https://api.jiliu.cn/v1/videos", descriptor.(map[string]any)["url"])
			assert.Equal(t, "task_public", descriptor.(map[string]any)["headers"].(map[string]any)["Idempotency-Key"])
			newFacts, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
			require.NoError(t, err)
			ctx["requestBody"] = tc.want
			oldFacts, err := plugin.Engine.Call(t.Context(), "extractUsage", ctx)
			require.NoError(t, err)
			assert.Equal(t, oldFacts, newFacts, "both interfaces must use the same billing quantities")
		})
	}
}

func TestJiliuH3OfficialFormatRejectsUnsupportedSemantics(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"2K", func(r map[string]any) { r["resolution"] = "2K" }, "2K is not supported"},
		{"adaptive", func(r map[string]any) { r["ratio"] = "adaptive" }, "explicit ratio"},
		{"missing ratio", func(r map[string]any) { delete(r, "ratio") }, "explicit ratio"},
		{"missing duration", func(r map[string]any) { delete(r, "duration") }, "duration_seconds"},
		{"fractional duration", func(r map[string]any) { r["duration"] = 4.5 }, "duration_seconds"},
		{"overflow duration", func(r map[string]any) { r["duration"] = 1e20 }, "duration_seconds"},
		{"different model", func(r map[string]any) { r["model"] = "MiniMax-H3-Max" }, "model must be"},
		{"callback", func(r map[string]any) { r["callback_url"] = "https://example.com/callback" }, "does not support callback_url"},
		{"watermark", func(r map[string]any) { r["aigc_watermark"] = true }, "does not support aigc_watermark"},
		{"unmapped field", func(r map[string]any) { r["extra"] = map[string]any{} }, "does not support the field"},
		{"audio", func(r map[string]any) {
			r["content"] = append(r["content"].([]any), map[string]any{"type": "audio_url"})
		}, "audio references"},
		{"no prompt", func(r map[string]any) {
			r["content"] = []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}}}
		}, "text item"},
		{"last only", func(r map[string]any) {
			r["content"] = append(r["content"].([]any), map[string]any{"type": "image_url", "role": "last_frame", "image_url": map[string]any{"url": "https://example.com/a.png"}})
		}, "requires a first frame"},
		{"mixed modes", func(r map[string]any) {
			for _, role := range []string{"first_frame", "reference_image"} {
				r["content"] = append(r["content"].([]any), map[string]any{"type": "image_url", "role": role, "image_url": map[string]any{"url": "https://example.com/a.png"}})
			}
		}, "cannot be mixed"},
		{"unknown role", func(r map[string]any) {
			r["content"] = append(r["content"].([]any), map[string]any{"type": "image_url", "role": "middle_frame", "image_url": map[string]any{"url": "https://example.com/a.png"}})
		}, "unsupported media role"},
		{"unknown media field", func(r map[string]any) {
			r["content"] = append(r["content"].([]any), map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "https://example.com/a.mp4", "duration": 1}})
		}, "url only"},
		{"local media", func(r map[string]any) {
			r["content"] = append(r["content"].([]any), map[string]any{"type": "image_url", "image_url": map[string]any{"url": "file:///tmp/a.png"}})
		}, "absolute HTTP"},
		{"too many references", func(r map[string]any) {
			for range 10 {
				r["content"] = append(r["content"].([]any), map[string]any{"type": "image_url", "role": "reference_image", "image_url": map[string]any{"url": "https://example.com/a.png"}})
			}
		}, "at most 9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := jiliuH3OfficialRequest()
			tc.change(request)
			_, err := plugin.Engine.CallMember(t.Context(), "native", "createTask", map[string]any{"body": map[string]any{"kind": "json", "value": request}})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestJiliuH3OfficialTaskPresentation(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	ctx := map[string]any{"serverAddress": "https://gateway.example/"}
	task := map[string]any{"task_id": "task_public", "created_at": 10, "updated_at": 20, "status": "SUCCESS", "data": map[string]any{
		"id": "private-upstream-id", "seconds": "5", "metadata": map[string]any{"resolution": "768p", "url": "http://localhost/private-upstream-id"},
		"input_video_duration_status": "ready", "input_video_seconds": 7.25, "input_videos": []any{map[string]any{"duration_seconds": 7.25}},
	}}
	created, err := plugin.Engine.CallMember(t.Context(), "native", "taskCreated", ctx, task)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"task_id": "task_public"}, created)
	result, err := plugin.Engine.CallMember(t.Context(), "native", "taskStatus", ctx, task)
	require.NoError(t, err)
	encoded, err := common.Marshal(result)
	require.NoError(t, err)
	assert.JSONEq(t, `{"task":{"id":"task_public","model":"MiniMax-H3","status":"succeeded","created_at":10,"updated_at":20,"task_type":"generation","modality":"video","duration":5,"resolution":"768P","usage":{"output_seconds":5,"input_seconds":7.25,"total_seconds":12.25},"content":{"url":"https://gateway.example/v1/videos/task_public/content"}}}`, string(encoded))
	for _, state := range []struct{ status, want string }{{"QUEUED", "queued"}, {"IN_PROGRESS", "running"}, {"UNKNOWN", "unknown"}, {"FAILURE", "failed"}} {
		task["status"] = state.status
		task["fail_reason"] = "provider rejected the task"
		result, err = plugin.Engine.CallMember(t.Context(), "native", "taskStatus", ctx, task)
		require.NoError(t, err)
		public := result.(map[string]any)["task"].(map[string]any)
		assert.Equal(t, state.want, public["status"])
		assert.NotContains(t, public, "content")
		assert.NotContains(t, public, "usage", "unfinished tasks must not claim measured usage")
	}
}

func TestJiliuH3SubmitAndUsage(t *testing.T) {
	service.InitHttpClient()
	plugin := loadJiliuH3Plugin(t)
	for _, tc := range []struct {
		name       string
		request    map[string]any
		wantAction string
	}{
		{"text", jiliuH3Request("t2va"), "text_to_video"},
		{"references", jiliuH3Request("ref2va",
			map[string]any{"type": "image", "role": "reference", "uri": "https://assets.example/product.jpg"},
			map[string]any{"type": "video", "role": "reference", "uri": "https://assets.example/motion.mp4"}), "reference_to_video"},
		{"first frame", jiliuH3Request("fl2va", map[string]any{"type": "image", "role": "keyframe", "frame_index": 0, "uri": "https://assets.example/first.jpg"}), "first_tail_to_video"},
		{"first and last frames", jiliuH3Request("fl2va",
			map[string]any{"type": "image", "role": "keyframe", "frame_index": 0, "uri": "https://assets.example/first.jpg"},
			map[string]any{"type": "image", "role": "keyframe", "frame_index": -1, "uri": "https://assets.example/last.jpg"}), "first_tail_to_video"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/v1/videos", r.URL.Path)
				assert.Equal(t, "Bearer test-customer-key", r.Header.Get("Authorization"))
				assert.Equal(t, "task_public_h3", r.Header.Get("Idempotency-Key"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				want, err := common.Marshal(tc.request)
				require.NoError(t, err)
				assert.JSONEq(t, string(want), string(body))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"id":"jiliu-original-prefix_123","status":"queued"}`))
			}))
			defer upstream.Close()
			decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
				"body": map[string]any{"kind": "json", "value": tc.request}, "model": "MiniMax-H3",
			})
			require.NoError(t, err)
			intent := decoded.(map[string]any)
			assert.Equal(t, tc.wantAction, intent["action"])
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType: constant.ChannelTypeTaskPlugin, ChannelBaseUrl: upstream.URL,
					ApiKey: "test-customer-key", UpstreamModelName: "MiniMax-H3",
				},
				OriginModelName: "MiniMax-H3", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public_h3"},
			}
			adaptor := taskplugin.New(plugin)
			adaptor.Init(info)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			c.Set("task_request", intent["requestBody"])
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			facts, err := adaptor.ExtractUsageFactsValidated(c, info)
			require.NoError(t, err)
			inputImages := 0
			inputVideos := 0
			if conditions, ok := tc.request["conditions"].([]any); ok {
				for _, condition := range conditions {
					if condition.(map[string]any)["type"] == "image" {
						inputImages++
					} else if condition.(map[string]any)["type"] == "video" {
						inputVideos++
					}
				}
			}
			assert.Equal(t, map[string]any{"seconds": float64(5), "resolution": "768P", "task": tc.request["task"], "input_images": float64(inputImages), "input_video_seconds": float64(inputVideos * 5)}, facts)
			ratios, err := adaptor.EstimateBillingValidated(c, info)
			require.NoError(t, err)
			assert.Equal(t, map[string]float64{"seconds": 5}, ratios, "resolution must not multiply the legacy per-second price")
			body, err := adaptor.BuildRequestBody(c, info)
			require.NoError(t, err)
			response, err := adaptor.DoRequest(c, info, body)
			require.NoError(t, err)
			defer response.Body.Close()
			submission, taskErr := adaptor.ParseResponse(c, response, info)
			require.Nil(t, taskErr)
			require.NotNil(t, submission)
			assert.Equal(t, "jiliu-original-prefix_123", submission.UpstreamTaskID)
		})
	}
}

func TestJiliuH3RejectsInvalidRequests(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"unknown task", func(r map[string]any) { r["task"] = "constructor" }, "task must be"},
		{"missing prompt", func(r map[string]any) { r["prompt"] = " " }, "prompt is required"},
		{"missing target", func(r map[string]any) { delete(r, "target") }, "target is required"},
		{"zero duration", func(r map[string]any) { r["target"].(map[string]any)["duration_seconds"] = 0 }, "duration_seconds"},
		{"fractional duration", func(r map[string]any) { r["target"].(map[string]any)["duration_seconds"] = 1.5 }, "duration_seconds"},
		{"below provider minimum", func(r map[string]any) { r["target"].(map[string]any)["duration_seconds"] = 3 }, "duration_seconds"},
		{"above provider maximum", func(r map[string]any) { r["target"].(map[string]any)["duration_seconds"] = 16 }, "duration_seconds"},
		{"oversized duration", func(r map[string]any) { r["target"].(map[string]any)["duration_seconds"] = 3601 }, "duration_seconds"},
		{"string duration", func(r map[string]any) { r["target"].(map[string]any)["duration_seconds"] = "5" }, "duration_seconds"},
		{"negative resolution", func(r map[string]any) { r["target"].(map[string]any)["short_edge"] = -768 }, "short_edge"},
		{"unsafe resolution", func(r map[string]any) { r["target"].(map[string]any)["short_edge"] = 1e20 }, "short_edge"},
		{"invalid ratio", func(r map[string]any) { r["target"].(map[string]any)["aspect_ratio"] = "0:9" }, "aspect_ratio"},
		{"unexpected text reference", func(r map[string]any) { r["conditions"] = []any{map[string]any{}} }, "t2va does not accept"},
		{"missing reference", func(r map[string]any) { r["task"] = "ref2va" }, "conditions are required"},
		{"local reference", func(r map[string]any) {
			r["task"], r["conditions"] = "ref2va", []any{map[string]any{"type": "image", "role": "reference", "uri": "file:///tmp/image.jpg"}}
		}, "absolute HTTP"},
		{"wrong reference role", func(r map[string]any) {
			r["task"], r["conditions"] = "ref2va", []any{map[string]any{"type": "image", "role": "keyframe", "uri": "https://assets.example/first.jpg"}}
		}, "image or video references"},
		{"last frame without first", func(r map[string]any) {
			r["task"], r["conditions"] = "fl2va", []any{map[string]any{"type": "image", "role": "keyframe", "frame_index": -1, "uri": "https://assets.example/last.jpg"}}
		}, "requires a first frame"},
		{"duplicate first frame", func(r map[string]any) {
			frame := map[string]any{"type": "image", "role": "keyframe", "frame_index": 0, "uri": "https://assets.example/first.jpg"}
			r["task"], r["conditions"] = "fl2va", []any{frame, frame}
		}, "indices must be unique"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := jiliuH3Request("t2va")
			tc.change(request)
			_, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
				"body": map[string]any{"kind": "json", "value": request}, "model": "MiniMax-H3",
			})
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestJiliuH3PollingAndArtifacts(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	adaptor := taskplugin.New(plugin)
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.jiliu.cn", ApiKey: "test-key"}})
	for _, tc := range []struct {
		status string
		want   string
	}{
		{"queued", "QUEUED"}, {"running", "IN_PROGRESS"}, {"in_progress", "IN_PROGRESS"}, {"completed", "SUCCESS"},
		{"failed", "FAILURE"}, {"expired", "FAILURE"}, {"cancelled", "FAILURE"},
		{"unexpected", "UNKNOWN"}, {"constructor", "UNKNOWN"}, {"", "UNKNOWN"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			value, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, map[string]any{
				"status": tc.status, "seconds": "4", "input_videos": []any{}, "input_video_seconds": 0, "error": map[string]any{"message": "provider error"},
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, value.(map[string]any)["status"])
			if tc.want == "FAILURE" {
				assert.Equal(t, "provider error", value.(map[string]any)["reason"])
			}
		})
	}
	task := &model.Task{
		TaskID: "task_public", Status: model.TaskStatusSuccess,
		Properties:  model.Properties{OriginModelName: "MiniMax-H3", UpstreamModelName: "MiniMax-H3"},
		PrivateData: model.TaskPrivateData{UpstreamTaskID: "original_prefix/a b?x=1"},
		Data:        []byte(`{"id":"original_prefix/a b?x=1","status":"completed","metadata":{"url":"http://localhost:3000/v1/videos/private/content"}}`),
	}
	artifacts, err := adaptor.ListArtifacts(task)
	require.NoError(t, err)
	assert.Equal(t, []channel.TaskArtifact{{Key: "video", Type: "video", MimeType: "video/mp4"}}, artifacts)
	content, err := adaptor.BuildContentRequest(t.Context(), task, "video", channel.TaskArtifactClientRequest{Method: http.MethodHead})
	require.NoError(t, err)
	assert.Equal(t, "https://api.jiliu.cn/v1/videos/original_prefix%2Fa%20b%3Fx%3D1/content", content.URL)
	assert.Equal(t, http.MethodGet, content.Method)
	assert.Equal(t, "Bearer test-key", content.Headers["Authorization"])
	assert.False(t, content.Credentialless)
	query, err := plugin.Engine.Call(t.Context(), "buildQueryRequest", map[string]any{
		"baseUrl": "https://api.jiliu.cn", "apiKey": "test-key", "taskId": task.PrivateData.UpstreamTaskID,
	})
	require.NoError(t, err)
	assert.Equal(t, "https://api.jiliu.cn/v1/videos/original_prefix%2Fa%20b%3Fx%3D1", query.(map[string]any)["url"])
	public, err := adaptor.ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var rendered map[string]any
	require.NoError(t, common.Unmarshal(public, &rendered))
	assert.Equal(t, "task_public", rendered["id"])
	assert.Equal(t, "completed", rendered["status"])
	assert.NotContains(t, string(public), "original_prefix")
	assert.NotContains(t, string(public), "localhost")
	assert.Equal(t, "/v1/videos/task_public/content", rendered["metadata"].(map[string]any)["url"])
	task.Status = model.TaskStatusInProgress
	artifacts, err = adaptor.ListArtifacts(task)
	require.NoError(t, err)
	assert.Empty(t, artifacts)
}

func TestJiliuH3MappingAndOrderKey(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	request := jiliuH3Request("t2va")
	request["model"] = "my-h3"
	decoded, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
		"body": map[string]any{"kind": "json", "value": request}, "model": "my-h3",
	})
	require.NoError(t, err)
	intent := decoded.(map[string]any)
	assert.Equal(t, "my-h3", intent["model"])
	ctx := map[string]any{
		"baseUrl": "https://alternate.example", "apiKey": "without-sk-prefix",
		"model": "my-h3", "upstreamModel": "MiniMax-H3", "publicTaskId": "task_unique",
		"requestBody": intent["requestBody"],
	}
	first, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	second, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	assert.Equal(t, first, second, "rebuilding the same host task retains its order key")
	descriptor := first.(map[string]any)
	assert.Equal(t, "https://alternate.example/v1/videos", descriptor["url"])
	assert.Equal(t, "Bearer without-sk-prefix", descriptor["headers"].(map[string]any)["Authorization"])
	assert.Equal(t, "MiniMax-H3", descriptor["body"].(map[string]any)["model"])
	ctx["publicTaskId"] = "task_another"
	third, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	assert.NotEqual(t, descriptor["headers"].(map[string]any)["Idempotency-Key"], third.(map[string]any)["headers"].(map[string]any)["Idempotency-Key"])
	delete(ctx, "publicTaskId")
	_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.ErrorContains(t, err, "submission order key")
}

func TestJiliuH3InvalidSubmissionResponses(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	for _, body := range []any{nil, "bad response", map[string]any{}, map[string]any{"id": " "}, map[string]any{"id": 123}, map[string]any{"task_id": "wrong-contract"}} {
		_, err := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{}, map[string]any{"body": body})
		require.ErrorContains(t, err, "did not return a task id")
	}
}

func TestJiliuH3MeasuredCompletionUsage(t *testing.T) {
	plugin := loadJiliuH3Plugin(t)
	ctx := map[string]any{"state": map[string]any{"inputVideoCount": 1}}
	body := map[string]any{
		"id": "provider-task", "status": "completed", "seconds": "4",
		"metadata":            map[string]any{"resolution": "480p"},
		"input_videos":        []any{map[string]any{"index": 0, "duration_seconds": 7.25}},
		"input_video_seconds": 7.25, "input_video_duration_status": "ready",
	}
	facts, err := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", ctx, map[string]any{"status": "SUCCESS"}, body)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"seconds": int64(4), "input_video_seconds": 7.25, "resolution": "480P"}, facts)
	for _, value := range []any{nil, -1, 0, 3601, "7.25"} {
		body["input_video_seconds"] = value
		_, err := plugin.Engine.Call(t.Context(), "parseTaskResult", ctx, body)
		require.ErrorContains(t, err, "input video duration is invalid")
	}
	body["input_video_seconds"] = 7.25
	body["input_video_duration_status"] = "pending"
	_, err = plugin.Engine.Call(t.Context(), "parseTaskResult", ctx, body)
	require.ErrorContains(t, err, "input video duration is not ready")
}

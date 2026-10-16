package claude

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeToolSchemaPreservedAcrossClientFormats(t *testing.T) {
	for _, schema := range []string{
		`null`,
		`{}`,
		`{"type":"object","properties":{"city":{"type":"string"}}}`,
		`{"type":"object","properties":{},"required":[]}`,
		`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`,
		`{"type":"object","properties":{"location":{"$ref":"#/$defs/location"}},"$defs":{"location":{"type":"object","properties":{"city":{"enum":["Nanjing"]}},"required":["city"]}}}`,
		`{"type":"object"}`,
	} {
		t.Run(schema, func(t *testing.T) {
			var original map[string]any
			require.NoError(t, common.UnmarshalJsonStr(schema, &original))
			for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses} {
				info := &relaycommon.RelayInfo{RelayFormat: format}
				var converted any
				var err error
				if format == types.RelayFormatOpenAI {
					var request dto.GeneralOpenAIRequest
					require.NoError(t, common.UnmarshalJsonStr(`{"model":"deepseek-v4-1-flash-260910","max_tokens":256,"messages":[{"role":"user","content":"weather"}],"tools":[{"type":"function","function":{"name":"weather","parameters":`+schema+`}}]}`, &request))
					converted, err = (&Adaptor{}).ConvertOpenAIRequest(nil, info, &request)
				} else {
					var request dto.OpenAIResponsesRequest
					require.NoError(t, common.UnmarshalJsonStr(`{"model":"deepseek-v4-1-flash-260910","max_output_tokens":256,"input":"weather","tools":[{"type":"function","name":"weather","parameters":`+schema+`}]}`, &request))
					converted, err = (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, request)
				}
				require.NoError(t, err)
				wire, err := common.Marshal(converted)
				require.NoError(t, err)
				var upstream struct {
					Tools []dto.Tool `json:"tools"`
				}
				require.NoError(t, common.Unmarshal(wire, &upstream))
				require.Len(t, upstream.Tools, 1)
				for key, value := range original {
					assert.Equal(t, value, upstream.Tools[0].InputSchema[key], "%s: %s", format, key)
				}
				if _, present := original["required"]; !present {
					assert.NotContains(t, upstream.Tools[0].InputSchema, "required")
				}
				assert.NotNil(t, upstream.Tools[0].InputSchema["properties"])
			}
		})
	}
}

func TestClaudeResponsesNonStreamToolRoundTripAndUsage(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-1-flash-260910"}}
	upstream := `{"id":"msg_tools","type":"message","role":"assistant","model":"deepseek-v4-1-flash-260910","content":[{"type":"text","text":"Checking"},{"type":"tool_use","id":"call_weather","name":"weather","input":{"city":"Nanjing"}}],"stop_reason":"tool_use","usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":40,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":6,"ephemeral_1h_input_tokens":4}}}`
	usageValue, apiErr := (&Adaptor{}).DoResponse(c, claudeTestResponse(upstream), info)
	require.Nil(t, apiErr)
	usage := usageValue.(*dto.Usage)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Equal(t, "anthropic", usage.UsageSemantic)
	assert.Equal(t, 40, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 10, usage.PromptTokensDetails.CachedCreationTokens)
	require.NotNil(t, usage.BillingUsage)
	var response dto.OpenAIResponsesResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "response", response.Object)
	require.Len(t, response.Output, 2)
	assert.Equal(t, "call_weather", response.Output[1].CallId)
	assert.Equal(t, "weather", response.Output[1].Name)
	assert.Equal(t, 150, response.Usage.InputTokens)
	assert.Equal(t, 20, response.Usage.OutputTokens)
	// Send the emitted function_call back with its matching output, as a
	// stateless Responses client does on the next turn.
	call, err := common.Marshal(response.Output[1])
	require.NoError(t, err)
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(`{"model":"deepseek-v4-1-flash-260910","max_output_tokens":256,"input":[{"role":"user","content":"weather"},{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Checking weather"}]},`+string(call)+`,{"type":"function_call_output","call_id":"call_weather","output":"Sunny"}]}`, &request))
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(c, info, request)
	require.NoError(t, err)
	claudeRequest := converted.(*dto.ClaudeRequest)
	require.Len(t, claudeRequest.Messages, 3)
	toolUse, err := claudeRequest.Messages[1].ParseContent()
	require.NoError(t, err)
	toolResult, err := claudeRequest.Messages[2].ParseContent()
	require.NoError(t, err)
	assert.Equal(t, "call_weather", toolUse[0].Id)
	assert.Equal(t, "call_weather", toolResult[0].ToolUseId)
	assert.Equal(t, "Sunny", toolResult[0].Content)
}

const claudeResponsesStreamStart = `data: {"type":"message_start","message":{"id":"msg_stream","model":"deepseek-v4-1-flash-260910","role":"assistant","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":40,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":6,"ephemeral_1h_input_tokens":4}}}}

`

func TestClaudeResponsesSSEToolsAndBillingUsage(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 10
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	stream := claudeResponsesStreamStart + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Checking"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_weather","name":"weather","input":{}}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"Nanjing\"}"}}

data: {"type":"content_block_stop","index":1}

data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_clock","name":"clock","input":{}}}

data: {"type":"content_block_stop","index":2}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}

data: {"type":"message_stop"}

`
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-1-flash-260910"}}
	usageValue, apiErr := (&Adaptor{}).DoResponse(c, claudeTestResponse(stream), info)
	require.Nil(t, apiErr)
	usage := usageValue.(*dto.Usage)
	assert.Equal(t, "anthropic", usage.UsageSemantic)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Equal(t, 20, usage.CompletionTokens)
	assert.Equal(t, 40, usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 6, usage.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 4, usage.ClaudeCacheCreation1hTokens)
	require.NotNil(t, usage.BillingUsage)
	events := claudeResponsesEvents(t, recorder.Body.String())
	require.NotEmpty(t, events)
	assert.Equal(t, "response.created", events[0].Type)
	final := events[len(events)-1]
	assert.Equal(t, "response.completed", final.Type)
	require.NotNil(t, final.Response)
	require.Len(t, final.Response.Output, 3)
	assert.Equal(t, "Checking", final.Response.Output[0].Content[0].Text)
	assert.Equal(t, "call_weather", final.Response.Output[1].CallId)
	assert.JSONEq(t, `{"city":"Nanjing"}`, claudeToolArguments(t, final.Response.Output[1]))
	assert.Equal(t, "call_clock", final.Response.Output[2].CallId)
	assert.JSONEq(t, `{}`, claudeToolArguments(t, final.Response.Output[2]))
	assert.Equal(t, 150, final.Response.Usage.InputTokens)
	assert.Equal(t, 20, final.Response.Usage.OutputTokens)
	assert.Contains(t, recorder.Body.String(), "event: response.function_call_arguments.delta")
	for _, event := range events {
		if event.Type == "response.function_call_arguments.done" {
			require.NotNil(t, event.Arguments)
			if event.ItemID == "call_weather" {
				assert.JSONEq(t, `{"city":"Nanjing"}`, *event.Arguments)
			}
		}
		if event.Type == "response.output_text.done" {
			require.NotNil(t, event.Text)
			assert.Equal(t, "Checking", *event.Text)
		}
	}
	assert.NotContains(t, recorder.Body.String(), "event: message_start")
}

func TestClaudeResponsesSSEReasoningAndTokenLimit(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 10
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for _, reason := range []string{"end_turn", "max_tokens"} {
		t.Run(reason, func(t *testing.T) {
			stream := claudeResponsesStreamStart + `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Consider carefully"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"` + reason + `"},"usage":{"output_tokens":20}}

data: {"type":"message_stop"}

`
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-1-flash-260910"}}
			_, apiErr := (&Adaptor{}).DoResponse(c, claudeTestResponse(stream), info)
			require.Nil(t, apiErr)
			events := claudeResponsesEvents(t, recorder.Body.String())
			require.GreaterOrEqual(t, len(events), 6)
			assert.Equal(t, "response.output_item.added", events[1].Type)
			require.NotNil(t, events[1].Item.Summary)
			assert.Empty(t, *events[1].Item.Summary)
			assert.Equal(t, "response.reasoning_summary_part.added", events[2].Type)
			assert.Equal(t, "response.reasoning_summary_text.delta", events[3].Type)
			final := events[len(events)-1]
			require.NotNil(t, final.Response)
			require.Len(t, final.Response.Output, 1)
			require.NotNil(t, final.Response.Output[0].Summary)
			assert.Equal(t, "Consider carefully", (*final.Response.Output[0].Summary)[0].Text)
			if reason == "max_tokens" {
				assert.Equal(t, "response.incomplete", final.Type)
				require.NotNil(t, final.Response.IncompleteDetails)
				assert.Equal(t, "max_output_tokens", final.Response.IncompleteDetails.Reason)
			} else {
				assert.Equal(t, "response.completed", final.Type)
			}
		})
	}
}

func TestClaudeResponsesSSEFailureIsNotCompleted(t *testing.T) {
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 10
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	for name, tail := range map[string]string{
		"truncated":          "",
		"upstream_error":     "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n",
		"invalid_json":       "data: {bad\n\n",
		"invalid_tool_delta": "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\"}}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses, IsStream: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-1-flash-260910"}}
			_, apiErr := (&Adaptor{}).DoResponse(c, claudeTestResponse(claudeResponsesStreamStart+tail), info)
			require.NotNil(t, apiErr)
			assert.NotContains(t, recorder.Body.String(), "response.completed")
		})
	}
}

func claudeTestResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func claudeResponsesEvents(t *testing.T, stream string) []dto.ResponsesStreamResponse {
	t.Helper()
	var events []dto.ResponsesStreamResponse
	scanner := bufio.NewScanner(strings.NewReader(stream))
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "data: ") {
			continue
		}
		var event dto.ResponsesStreamResponse
		require.NoError(t, common.UnmarshalJsonStr(strings.TrimPrefix(scanner.Text(), "data: "), &event))
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	return events
}

func claudeToolArguments(t *testing.T, output dto.ResponsesOutput) string {
	t.Helper()
	var arguments string
	require.NoError(t, common.Unmarshal(output.Arguments, &arguments))
	return arguments
}

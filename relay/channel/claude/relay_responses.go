package claude

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ClaudeResponsesStreamHandler keeps Anthropic usage for settlement and uses the
// existing Chat -> Responses bridge only for the client-facing event stream.
func ClaudeResponsesStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	claudeInfo := &ClaudeResponseInfo{
		ResponseId: helper.GetResponseID(c), Created: common.GetTimestamp(),
		Model: info.UpstreamModelName, Usage: &dto.Usage{},
	}
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID: claudeInfo.ResponseId, Model: claudeInfo.Model, Created: claudeInfo.Created,
	})
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	var streamErr *types.NewAPIError
	messageStopped := false
	toolArguments := make(map[int]bool)
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var upstream dto.ClaudeResponse
		if err := common.UnmarshalJsonStr(data, &upstream); err != nil {
			streamErr = types.NewError(err, types.ErrorCodeBadResponseBody)
			sr.Stop(streamErr)
			return
		}
		if apiErr := upstream.GetClaudeError(); apiErr != nil && apiErr.Type != "" {
			streamErr = types.WithClaudeError(*apiErr, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		if upstream.Type == "message_stop" {
			messageStopped = true
			sr.Done()
			return
		}
		if upstream.Delta != nil && upstream.Delta.StopReason != nil {
			maybeMarkClaudeRefusal(c, *upstream.Delta.StopReason)
		}
		// Claude starts argument-less tools with input:{} and no JSON delta.
		// Emit {} at block_stop rather than handing clients an empty JSON string.
		if upstream.Type == "content_block_stop" && upstream.Index != nil {
			if hasArguments, tool := toolArguments[*upstream.Index]; tool && !hasArguments {
				upstream.Type = "content_block_delta"
				emptyObject := "{}"
				upstream.Delta = &dto.ClaudeMediaMessage{Type: "input_json_delta", PartialJson: &emptyObject}
			}
		}
		if upstream.Delta != nil && upstream.Delta.Type == "input_json_delta" && upstream.Delta.PartialJson == nil {
			streamErr = types.NewError(fmt.Errorf("tool argument delta is missing partial_json"), types.ErrorCodeBadResponseBody)
			sr.Stop(streamErr)
			return
		}
		chunk := StreamResponseClaude2OpenAI(&upstream)
		FormatClaudeResponseInfo(&upstream, chunk, claudeInfo)
		countClaudeStreamBillableTools(c, info, &upstream)
		if chunk == nil {
			return
		}
		for choiceIndex := range chunk.Choices {
			for toolIndex := range chunk.Choices[choiceIndex].Delta.ToolCalls {
				tool := &chunk.Choices[choiceIndex].Delta.ToolCalls[toolIndex]
				if tool.Index != nil {
					if _, exists := toolArguments[*tool.Index]; !exists {
						toolArguments[*tool.Index] = false
					}
					if upstream.Type == "content_block_start" && upstream.ContentBlock != nil {
						if input, ok := upstream.ContentBlock.Input.(map[string]any); ok && len(input) > 0 {
							arguments, marshalErr := common.Marshal(input)
							if marshalErr != nil {
								streamErr = types.NewError(marshalErr, types.ErrorCodeBadResponseBody)
								sr.Stop(streamErr)
								return
							}
							tool.Function.Arguments = string(arguments)
						}
					}
					if tool.Function.Arguments != "" {
						toolArguments[*tool.Index] = true
					}
				}
			}
		}
		results, convertErr := relayconvert.ConvertStreamResponseChunk(c, info, state, chunk)
		if convertErr == nil {
			convertErr = writeClaudeResponsesEvents(c, results)
		}
		if convertErr != nil {
			streamErr = types.NewError(convertErr, types.ErrorCodeBadResponseBody)
			sr.Stop(streamErr)
		}
	})
	if streamErr != nil {
		return claudeInfo.Usage, streamErr
	}
	if !messageStopped || !claudeInfo.Done || !info.StreamStatus.IsNormalEnd() || info.StreamStatus.HasErrors() {
		return claudeInfo.Usage, types.NewError(fmt.Errorf("incomplete Anthropic stream: %s", info.StreamStatus.Summary()), types.ErrorCodeBadResponseBody)
	}
	HandleStreamFinalResponse(c, info, claudeInfo)
	clientUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
	state.SetUsage(&clientUsage)
	results, err := relayconvert.FinalizeStreamResponse(c, info, state)
	if err == nil {
		err = writeClaudeResponsesEvents(c, results)
	}
	if err != nil {
		return claudeInfo.Usage, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	return claudeInfo.Usage, nil
}

func writeClaudeResponsesEvents(c *gin.Context, results []relayconvert.ResponseResult) error {
	for _, result := range results {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			return fmt.Errorf("expected Responses stream event, got %T", result.Value)
		}
		data, err := common.Marshal(event.Payload)
		if err != nil {
			return err
		}
		if err := helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: event.Type}, strings.TrimSpace(string(data))); err != nil {
			return err
		}
	}
	return nil
}

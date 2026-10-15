package claude

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Opt-in only: runs the installed Codex client against a loopback adapter and a
// real Anthropic-compatible upstream. No production channel/DB is written.
func TestClaudeResponsesLiveCodexToolRoundTrip(t *testing.T) {
	if os.Getenv("IFLINK_CLAUDE_LIVE") != "1" {
		t.Skip("set IFLINK_CLAUDE_LIVE=1 and supply upstream credentials plus IFLINK_CODEX_EXE")
	}
	upstreamURL := os.Getenv("IFLINK_CLAUDE_UPSTREAM_URL")
	upstreamKey := os.Getenv("IFLINK_CLAUDE_UPSTREAM_KEY")
	clientPath := os.Getenv("IFLINK_CODEX_EXE")
	require.NotEmpty(t, upstreamURL)
	require.NotEmpty(t, upstreamKey)
	require.NotEmpty(t, clientPath)
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 120
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	service.InitHttpClient()
	var mu sync.Mutex
	var requests, toolResultRequests int
	var upstreamUsages []dto.Usage
	router := gin.New()
	router.POST("/v1/responses", func(c *gin.Context) {
		mu.Lock()
		requests++
		attempt := requests
		mu.Unlock()
		if attempt > 4 {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "acceptance request budget exhausted"}})
			return
		}
		var request dto.OpenAIResponsesRequest
		if err := common.DecodeJson(c.Request.Body, &request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
		if bytes.Contains(request.Input, []byte(`"function_call_output"`)) || bytes.Contains(request.Input, []byte(`"custom_tool_call_output"`)) {
			mu.Lock()
			toolResultRequests++
			mu.Unlock()
		}
		// Bound this acceptance run without changing the production channel.
		limit := uint(2048)
		request.MaxOutputTokens = &limit
		info := &relaycommon.RelayInfo{
			RelayFormat: types.RelayFormatOpenAIResponses, IsStream: true,
			StartTime: time.Now(), OriginModelName: request.Model,
			ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 15, ChannelType: 14,
				ChannelBaseUrl: upstreamURL, ApiKey: upstreamKey, UpstreamModelName: request.Model},
		}
		adaptor := &Adaptor{}
		converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
		if err != nil {
			t.Logf("request %d conversion failed: %v", attempt, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
		body, err := common.Marshal(converted)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		upstream, err := adaptor.DoRequest(c, info, bytes.NewReader(body))
		if err != nil {
			t.Logf("request %d upstream transport failed: %v", attempt, err)
			c.Status(http.StatusBadGateway)
			return
		}
		response := upstream.(*http.Response)
		if response.StatusCode != http.StatusOK {
			t.Logf("request %d upstream HTTP %d", attempt, response.StatusCode)
			service.CloseResponseBodyGracefully(response)
			c.Status(response.StatusCode)
			return
		}
		usage, apiErr := adaptor.DoResponse(c, response, info)
		if apiErr != nil {
			t.Logf("request %d response conversion failed: %v", attempt, apiErr)
			return
		}
		mu.Lock()
		upstreamUsages = append(upstreamUsages, *usage.(*dto.Usage))
		mu.Unlock()
	})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	workDir := t.TempDir()
	command := exec.CommandContext(ctx, clientPath, "exec", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--skip-git-repo-check", "--json", "-C", workDir,
		"-s", "workspace-write", "-c", `windows.sandbox="unelevated"`, "-c", `model_provider="iflink-compat"`, "-c", `model="deepseek-v4-1-flash-260910"`,
		"-c", fmt.Sprintf(`model_providers.iflink-compat={name="IFLink compatibility acceptance",base_url="%s/v1",wire_api="responses",env_key="IFLINK_COMPAT_TEST_KEY",request_max_retries=0,stream_max_retries=0}`, server.URL),
		"-c", `model_reasoning_effort="none"`, "-c", `web_search="disabled"`,
		"Use the exec_command tool to run exactly Write-Output 'IFLINK_TOOL_OK' once, then reply IFLINK_TOOL_OK. Do not inspect files, edit files, access the network, or call any other tool.")
	// Never pass the actual upstream credential to the client/tool subprocess.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "IFLINK_CLAUDE_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "IFLINK_COMPAT_TEST_KEY=local-acceptance-only")
	output, err := command.CombinedOutput()
	t.Logf("Codex acceptance output:\n%s", output)
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, requests, 2, "must include the tool-result follow-up request")
	assert.GreaterOrEqual(t, toolResultRequests, 1)
	assert.GreaterOrEqual(t, len(upstreamUsages), 2)
	assert.Contains(t, string(output), "IFLINK_TOOL_OK")
	assert.NotContains(t, string(output), "blocked by policy")
	assert.NotContains(t, string(output), "ReasoningSummaryDelta without active item")
	assert.Contains(t, string(output), `"exit_code":0`)
	for _, usage := range upstreamUsages {
		assert.Equal(t, "anthropic", usage.UsageSemantic)
		require.NotNil(t, usage.BillingUsage)
		t.Logf("upstream usage: input=%d output=%d cache_read=%d cache_create=%d", usage.PromptTokens, usage.CompletionTokens, usage.PromptTokensDetails.CachedTokens, usage.PromptTokensDetails.CachedCreationTokens)
	}
}

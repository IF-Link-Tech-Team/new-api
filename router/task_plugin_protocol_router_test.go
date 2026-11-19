package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHostProtocolRegistryDrivesProtocolRoutesOnce(t *testing.T) {
	engine := gin.New()
	SetTaskPluginProtocolRouter(engine)

	expected := []string{
		"POST /v1/responses",
		"GET /v1/responses/:response_id",
		"POST /v1/videos",
		"POST /v2/video_generation",
		"GET /v2/query/video_generation/:task_id",
		"GET /v1/videos/:task_id",
		"GET /v1/videos/:task_id/content",
		"HEAD /v1/videos/:task_id/content",
		"POST /v1/images/generations",
		"POST /v1/images/edits",
	}
	actual := make([]string, 0, len(engine.Routes()))
	for _, route := range engine.Routes() {
		actual = append(actual, fmt.Sprintf("%s %s", route.Method, route.Path))
	}
	sort.Strings(expected)
	sort.Strings(actual)
	assert.Equal(t, expected, actual)
}

func TestMiniMaxSharedRoutesRequireAuthentication(t *testing.T) {
	engine := gin.New()
	SetTaskPluginProtocolRouter(engine)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v2/video_generation", strings.NewReader(`{"model":"MiniMax-H3"}`)),
		httptest.NewRequest(http.MethodGet, "/v2/query/video_generation/task_other", nil),
	} {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, request)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), `"http_code":"401"`)
	}
}

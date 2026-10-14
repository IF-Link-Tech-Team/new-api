package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestClassroomRedemptionHTTPConcurrentAccounts(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("CILAI_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("real PostgreSQL HTTP concurrency test is opt-in")
	}

	previousMaster := common.IsMasterNode
	previousQuotaPerUnit := common.QuotaPerUnit
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	previousCriticalEnabled := common.CriticalRateLimitEnable
	previousCriticalNum := common.CriticalRateLimitNum
	previousCriticalDuration := common.CriticalRateLimitDuration
	previousDSN, hadDSN := os.LookupEnv("SQL_DSN")
	t.Cleanup(func() {
		common.IsMasterNode = previousMaster
		common.QuotaPerUnit = previousQuotaPerUnit
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
		common.CriticalRateLimitEnable = previousCriticalEnabled
		common.CriticalRateLimitNum = previousCriticalNum
		common.CriticalRateLimitDuration = previousCriticalDuration
		if hadDSN {
			_ = os.Setenv("SQL_DSN", previousDSN)
		} else {
			_ = os.Unsetenv("SQL_DSN")
		}
	})

	common.IsMasterNode = false
	common.QuotaPerUnit = 500_000
	require.NoError(t, os.Setenv("SQL_DSN", dsn))
	require.NoError(t, model.InitDB())
	model.LOG_DB = model.DB
	require.NoError(t, model.DB.AutoMigrate(
		&model.User{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.Ability{},
		&model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{},
		&model.ProductPrice{}, &model.Redemption{}, &model.ProductRedemptionClaim{},
		&model.SubscriptionKeyBinding{},
	))
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(20)
	t.Cleanup(func() { _ = sqlDB.Close() })

	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	require.NoError(t, redisClient.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = redisClient.Close() })
	common.RedisEnabled = true
	common.RDB = redisClient
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = 1_000
	common.CriticalRateLimitDuration = 60

	channel := model.Channel{
		Id: 15, Name: "果盒-豆包", Type: 14, Key: "mock-upstream-key", Status: common.ChannelStatusEnabled,
		Models: model.ClassroomModelV41 + "," + model.ClassroomModelV4GA, Group: "default",
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id: 14, Name: "果核-Qwen", Type: 1, Key: "mock-qwen-key", Status: common.ChannelStatusEnabled,
		Models: model.ClassroomModelQwen, Group: "default",
	}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{
		Id: 1, Name: "seu-tokenhub-01", Type: 1, Key: "mock-tokenhub-key", Status: common.ChannelStatusEnabled,
		Models: model.ClassroomModelTokenHub, Group: "default",
	}).Error)
	bootstrap, err := model.BootstrapClassroomProduct(true, true, true, 0)
	require.NoError(t, err)
	summary, err := model.CreateProductRedemptionBatch(1, model.ProductRedemptionBatchRequest{
		Name: "HTTP 200并发", BatchCode: "CLASSROOM-HTTP-200", TargetPlanId: bootstrap.PlanId,
		PrimaryCount: 200, ExpiredTime: common.GetTimestamp() + 3_600,
	})
	require.NoError(t, err)
	var codes []model.Redemption
	require.NoError(t, model.DB.Where("batch_code = ?", summary.BatchCode).Order("id asc").Find(&codes).Error)
	require.Len(t, codes, 200)
	users := make([]model.User, 200)
	for index := range users {
		users[index] = model.User{
			Username: fmt.Sprintf("http-classroom-%03d", index),
			AffCode:  fmt.Sprintf("http-class-aff-%03d", index),
			Status:   common.UserStatusEnabled, Group: "default",
		}
	}
	require.NoError(t, model.DB.CreateInBatches(&users, 50).Error)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.POST("/api/subscription/redeem",
		func(c *gin.Context) {
			userID, parseErr := strconv.Atoi(c.GetHeader("X-Test-User"))
			if parseErr != nil || userID <= 0 {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			c.Set("id", userID)
			c.Next()
		},
		middleware.CriticalRateLimit(), middleware.DisableCache(), RedeemProductCode,
	)

	type requestResult struct {
		status  int
		success bool
		latency time.Duration
		err     error
	}
	results := make([]requestResult, 200)
	startedAt := time.Now()
	var waitGroup sync.WaitGroup
	waitGroup.Add(200)
	for index := range users {
		go func(i int) {
			defer waitGroup.Done()
			body := fmt.Sprintf(`{"key":%q,"product_id":%q}`, codes[i].Key, model.ClassroomProductCode)
			request := httptest.NewRequest(http.MethodPost, "/api/subscription/redeem", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Test-User", strconv.Itoa(users[i].Id))
			request.RemoteAddr = fmt.Sprintf("198.51.100.%d:12345", i+1)
			recorder := httptest.NewRecorder()
			requestStartedAt := time.Now()
			router.ServeHTTP(recorder, request)
			results[i].latency = time.Since(requestStartedAt)
			results[i].status = recorder.Code
			var envelope struct {
				Success bool `json:"success"`
			}
			results[i].err = json.Unmarshal(recorder.Body.Bytes(), &envelope)
			results[i].success = envelope.Success
		}(index)
	}
	waitGroup.Wait()
	totalElapsed := time.Since(startedAt)

	latencies := make([]time.Duration, 0, len(results))
	successCount := 0
	serverErrorCount := 0
	for index, result := range results {
		require.NoErrorf(t, result.err, "response %d was not JSON", index)
		if result.status >= http.StatusInternalServerError {
			serverErrorCount++
		}
		if result.status == http.StatusOK && result.success {
			successCount++
		}
		latencies = append(latencies, result.latency)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)*50/100]
	p95 := latencies[len(latencies)*95/100]

	rateLimitKeys := 0
	for _, key := range redisServer.Keys() {
		if strings.HasPrefix(key, "rateLimit:v2:ip:CT:") {
			rateLimitKeys++
		}
	}
	require.Equal(t, 200, rateLimitKeys)
	require.Equal(t, 200, successCount)
	require.Zero(t, serverErrorCount)

	var usedCodes, subscriptions, tokens, claims int64
	require.NoError(t, model.DB.Model(&model.Redemption{}).Where("batch_code = ? AND status = ?", summary.BatchCode, common.RedemptionCodeStatusUsed).Count(&usedCodes).Error)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("product_type = ?", model.ProductTypePackDeepSeekClassroom).Count(&subscriptions).Error)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("product_type = ?", model.ProductTypePackDeepSeekClassroom).Count(&tokens).Error)
	require.NoError(t, model.DB.Model(&model.ProductRedemptionClaim{}).Count(&claims).Error)
	require.Equal(t, int64(200), usedCodes)
	require.Equal(t, int64(200), subscriptions)
	require.Equal(t, int64(200), tokens)
	require.Equal(t, int64(200), claims)

	t.Logf("http_redemption success=%d/200 5xx=%d total=%s p50=%s p95=%s redis_rate_limit_keys=%d", successCount, serverErrorCount, totalElapsed, p50, p95, rateLimitKeys)
}

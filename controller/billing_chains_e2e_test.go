package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClassroomAndWalletBillingChainsHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldMemoryCache := common.MemoryCacheEnabled
	oldRedis := common.RedisEnabled
	oldBatchUpdate := common.BatchUpdateEnabled
	oldLogConsume := common.LogConsumeEnabled
	oldQuotaPerUnit := common.QuotaPerUnit
	oldRetryTimes := common.RetryTimes
	oldSQLitePath := common.SQLitePath
	oldMasterNode := common.IsMasterNode
	oldSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	oldGroups := setting.UserUsableGroups2JSONString()
	oldRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.MemoryCacheEnabled = oldMemoryCache
		common.RedisEnabled = oldRedis
		common.BatchUpdateEnabled = oldBatchUpdate
		common.LogConsumeEnabled = oldLogConsume
		common.QuotaPerUnit = oldQuotaPerUnit
		common.RetryTimes = oldRetryTimes
		common.SQLitePath = oldSQLitePath
		common.IsMasterNode = oldMasterNode
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", oldSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
	})

	common.SQLitePath = "file:billing-chains-e2e?mode=memory&cache=shared"
	common.IsMasterNode = false
	require.NoError(t, os.Unsetenv("SQL_DSN"))
	require.NoError(t, model.InitDB())
	require.NoError(t, model.InitLogDB())
	db := model.DB
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	common.QuotaPerUnit = 500_000
	common.RetryTimes = 0
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.Ability{},
		&model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.BillingRefund{},
		&model.ProductPrice{}, &model.Redemption{}, &model.ProductRedemptionClaim{},
		&model.SubscriptionKeyBinding{},
	))
	require.NoError(t, model.EnsureEntitlementKeyUniqueIndex())

	var upstreamCalls atomic.Int32
	var forbiddenChannelCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		if strings.Contains(string(body), "force-upstream-failure") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"mock upstream failure","type":"server_error"}}`))
			return
		}
		var request struct {
			Model string `json:"model"`
		}
		require.NoError(t, json.Unmarshal(body, &request))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"chatcmpl-isolated","object":"chat.completion","created":1,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":100,"total_tokens":1100,"prompt_tokens_details":{"cached_tokens":200}}}`, request.Model)
	}))
	defer upstream.Close()
	forbiddenUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forbiddenChannelCalls.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer forbiddenUpstream.Close()

	autoBanOff := 0
	primary := model.Channel{
		Id: 15, Name: "果盒-豆包-隔离测试", Type: constant.ChannelTypeOpenAI,
		Key: "mock-channel-15", BaseURL: common.GetPointer(upstream.URL), Status: common.ChannelStatusEnabled,
		Models: model.ClassroomModelV41 + "," + model.ClassroomModelV4GA, Group: "default", AutoBan: &autoBanOff,
	}
	forbidden := model.Channel{
		Id: 16, Name: "不得回退的隔离渠道", Type: constant.ChannelTypeOpenAI,
		Key: "mock-channel-16", BaseURL: common.GetPointer(forbiddenUpstream.URL), Status: common.ChannelStatusEnabled,
		Models: model.ClassroomModelV41 + "," + model.ClassroomModelV4GA, Group: "default", AutoBan: &autoBanOff,
	}
	require.NoError(t, db.Create(&primary).Error)
	require.NoError(t, db.Create(&forbidden).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id: 14, Name: "Qwen 隔离测试", Type: constant.ChannelTypeOpenAI,
		Key: "mock-channel-14", BaseURL: common.GetPointer(upstream.URL), Status: common.ChannelStatusEnabled,
		Models: model.ClassroomModelQwen, Group: "default", AutoBan: &autoBanOff,
	}).Error)
	for _, modelName := range []string{model.ClassroomModelV4GA, model.ClassroomModelV41} {
		lowPriority := int64(0)
		highPriority := int64(100)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: modelName, ChannelId: 15, Enabled: true, Priority: &lowPriority}).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: modelName, ChannelId: 16, Enabled: true, Priority: &highPriority}).Error)
	}

	bootstrap, err := model.BootstrapClassroomProduct(true, true, false, 0)
	require.NoError(t, err)
	require.False(t, bootstrap.Listed, "the isolated-test product must stay hidden")
	require.Equal(t, model.WalletDeepSeekPriceVersion, bootstrap.WalletPriceVersion)

	classroomUser := model.User{Username: "classroom-e2e", AffCode: "classroom-e2e-aff", Status: common.UserStatusEnabled, Group: "default", Quota: 1_000_000}
	walletUser := model.User{Username: "wallet-e2e", AffCode: "wallet-e2e-aff", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&classroomUser).Error)
	require.NoError(t, db.Create(&walletUser).Error)

	batch, err := model.CreateProductRedemptionBatch(1, model.ProductRedemptionBatchRequest{
		Name: "隔离闭环单码", BatchCode: "ISOLATED-E2E-ONE", TargetPlanId: bootstrap.PlanId,
		PrimaryCount: 1, ExpiredTime: common.GetTimestamp() + 3600,
	})
	require.NoError(t, err)
	var code model.Redemption
	require.NoError(t, db.Where("batch_code = ?", batch.BatchCode).First(&code).Error)

	redeemRouter := gin.New()
	redeemRouter.POST("/api/subscription/redeem", func(c *gin.Context) {
		c.Set("id", classroomUser.Id)
		c.Next()
	}, RedeemProductCode)
	redeemBody := fmt.Sprintf(`{"key":%q,"plan_id":%d}`, code.Key, bootstrap.PlanId)
	redeemRequest := httptest.NewRequest(http.MethodPost, "/api/subscription/redeem", strings.NewReader(redeemBody))
	redeemRequest.Header.Set("Content-Type", "application/json")
	redeemRecorder := httptest.NewRecorder()
	redeemRouter.ServeHTTP(redeemRecorder, redeemRequest)
	require.Equal(t, http.StatusOK, redeemRecorder.Code, redeemRecorder.Body.String())
	var redeemEnvelope struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(redeemRecorder.Body.Bytes(), &redeemEnvelope))
	require.True(t, redeemEnvelope.Success)

	// Exercise the existing administrator quota grant handler instead of
	// bypassing it with a direct wallet update.
	adminRouter := gin.New()
	adminRouter.POST("/api/user/manage", func(c *gin.Context) {
		c.Set("role", common.RoleRootUser)
		c.Next()
	}, ManageUser)
	grantBody := fmt.Sprintf(`{"id":%d,"action":"add_quota","mode":"add","value":5000000}`, walletUser.Id)
	grantRequest := httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(grantBody))
	grantRequest.Header.Set("Content-Type", "application/json")
	grantRecorder := httptest.NewRecorder()
	adminRouter.ServeHTTP(grantRecorder, grantRequest)
	require.Equal(t, http.StatusOK, grantRecorder.Code, grantRecorder.Body.String())
	require.Contains(t, grantRecorder.Body.String(), `"success":true`)

	var classroomToken model.Token
	require.NoError(t, db.Where("user_id = ? AND product_type = ?", classroomUser.Id, model.ProductTypePackDeepSeekClassroom).First(&classroomToken).Error)
	walletToken := model.Token{
		UserId: walletUser.Id, Key: "walletisolatedkey", Name: "钱包闭环测试 Key",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true,
		EntitlementType: model.ProductTypeWallet, ProductType: model.ProductTypeWallet, BillingMultiplier: 1,
	}
	require.NoError(t, walletToken.Insert())

	relayRouter := gin.New()
	relayRouter.Use(middleware.BodyStorageCleanup())
	relayRouter.POST("/v1/chat/completions", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) {
		Relay(c, types.RelayFormatOpenAI)
	})
	call := func(tokenKey string, modelName string, prompt string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"max_tokens":100}`, modelName, prompt)
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer sk-"+tokenKey)
		recorder := httptest.NewRecorder()
		relayRouter.ServeHTTP(recorder, request)
		return recorder
	}

	classGA := call(classroomToken.Key, model.ClassroomModelV4GA, "classroom-ga")
	require.Equal(t, http.StatusOK, classGA.Code, classGA.Body.String())
	classV41 := call(classroomToken.Key, model.ClassroomModelV41, "classroom-v41")
	require.Equal(t, http.StatusOK, classV41.Code, classV41.Body.String())
	walletGA := call(walletToken.Key, model.ClassroomModelV4GA, "wallet-ga")
	require.Equal(t, http.StatusOK, walletGA.Code, walletGA.Body.String())

	var classroomLogs []model.Log
	require.NoError(t, db.Where("token_id = ? AND type = ?", classroomToken.Id, model.LogTypeConsume).Order("id asc").Find(&classroomLogs).Error)
	require.Len(t, classroomLogs, 2)
	var walletLogs []model.Log
	require.NoError(t, db.Where("token_id = ? AND type = ?", walletToken.Id, model.LogTypeConsume).Order("id asc").Find(&walletLogs).Error)
	require.Len(t, walletLogs, 1)

	classGAQuota := 0
	classV41Quota := 0
	for _, log := range classroomLogs {
		require.Equal(t, 15, log.ChannelId)
		var other map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
		require.Equal(t, "subscription", other["billing_source"])
		require.Equal(t, model.ProductTypePackDeepSeekClassroom, other["product_type"])
		require.Equal(t, model.ClassroomPriceVersion, other["price_version"])
		require.InDelta(t, 0.60, other["billing_multiplier"], 0.000001)
		if log.ModelName == model.ClassroomModelV4GA {
			classGAQuota = log.Quota
		} else if log.ModelName == model.ClassroomModelV41 {
			classV41Quota = log.Quota
		}
	}
	require.Equal(t, 996, classGAQuota)
	require.Contains(t, []int{361, 722}, classV41Quota)

	walletLog := walletLogs[0]
	require.Equal(t, 15, walletLog.ChannelId)
	require.Equal(t, 1660, walletLog.Quota)
	var walletOther map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(walletLog.Other), &walletOther))
	require.Equal(t, "wallet", walletOther["billing_source"])
	require.Equal(t, model.ProductTypeWallet, walletOther["product_type"])
	require.Equal(t, model.WalletDeepSeekPriceVersion, walletOther["price_version"])
	require.InDelta(t, 1.00, walletOther["billing_multiplier"], 0.000001)
	require.InDelta(t, float64(walletLog.Quota)*0.60, float64(classGAQuota), 0.5)

	var entitlement model.UserSubscription
	require.NoError(t, db.First(&entitlement, classroomToken.UserSubscriptionId).Error)
	require.Equal(t, int64(classGAQuota+classV41Quota), entitlement.AmountUsed)
	var classroomAfterSuccess, walletAfterSuccess model.User
	require.NoError(t, db.First(&classroomAfterSuccess, classroomUser.Id).Error)
	require.NoError(t, db.First(&walletAfterSuccess, walletUser.Id).Error)
	require.Equal(t, 1_000_000, classroomAfterSuccess.Quota, "classroom requests must not touch wallet quota")
	require.Equal(t, 5_000_000-walletLog.Quota, walletAfterSuccess.Quota)

	classUsedBeforeFailure := entitlement.AmountUsed
	classWalletBeforeFailure := classroomAfterSuccess.Quota
	walletBeforeFailure := walletAfterSuccess.Quota
	classFailure := call(classroomToken.Key, model.ClassroomModelV4GA, "force-upstream-failure")
	require.GreaterOrEqual(t, classFailure.Code, http.StatusBadRequest)
	walletFailure := call(walletToken.Key, model.ClassroomModelV4GA, "force-upstream-failure")
	require.GreaterOrEqual(t, walletFailure.Code, http.StatusBadRequest)

	require.Eventually(t, func() bool {
		var currentEntitlement model.UserSubscription
		var currentClassroomUser, currentWalletUser model.User
		if db.First(&currentEntitlement, classroomToken.UserSubscriptionId).Error != nil ||
			db.First(&currentClassroomUser, classroomUser.Id).Error != nil ||
			db.First(&currentWalletUser, walletUser.Id).Error != nil {
			return false
		}
		return currentEntitlement.AmountUsed == classUsedBeforeFailure &&
			currentClassroomUser.Quota == classWalletBeforeFailure &&
			currentWalletUser.Quota == walletBeforeFailure
	}, 2*time.Second, 10*time.Millisecond, "asynchronous failure refunds must reach durable balances")

	require.NoError(t, db.First(&entitlement, classroomToken.UserSubscriptionId).Error)
	require.Equal(t, classUsedBeforeFailure, entitlement.AmountUsed, "failed classroom call must refund its pre-consume")
	require.NoError(t, db.First(&classroomAfterSuccess, classroomUser.Id).Error)
	require.NoError(t, db.First(&walletAfterSuccess, walletUser.Id).Error)
	require.Equal(t, classWalletBeforeFailure, classroomAfterSuccess.Quota)
	require.Equal(t, walletBeforeFailure, walletAfterSuccess.Quota, "failed wallet call must refund its pre-consume")
	require.Equal(t, int32(5), upstreamCalls.Load())
	require.Zero(t, forbiddenChannelCalls.Load(), "neither billing mode may escape channel 15")

	t.Logf("isolated_billing classroom_ga_quota=%d classroom_v41_quota=%d classroom_remaining=%d wallet_ga_quota=%d wallet_remaining=%d upstream_failures_refunded=2 channel15_calls=%d channel16_calls=%d",
		classGAQuota, classV41Quota, entitlement.AmountTotal-entitlement.AmountUsed,
		walletLog.Quota, walletAfterSuccess.Quota, upstreamCalls.Load(), forbiddenChannelCalls.Load())
}

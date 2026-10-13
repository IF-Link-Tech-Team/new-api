package controller

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IFLinkPay 签名是与第三方定稿的跨方协议: md5(uid + amount(分) + order + ts + secret),
// 直接拼接无分隔符、UTF-8、32 位小写 hex。这里的期望值由独立工具(md5 命令行)算出,
// 任何拼接顺序/编码改动都会破坏对接。
func TestIFLinkPaySignMatchesProtocolVectors(t *testing.T) {
	cases := []struct {
		name      string
		uid       string
		amountFen int64
		order     string
		ts        int64
		secret    string
		expect    string
	}{
		{
			name:      "basic order",
			uid:       "1",
			amountFen: 500,
			order:     "TU-IFP-ABCDEFGH",
			ts:        1700000000,
			secret:    "topsecret",
			expect:    "5e00acf42c5bd40c7242c246dc0e9793",
		},
		{
			name:      "larger amount different secret",
			uid:       "42",
			amountFen: 9900,
			order:     "ORDER-XYZ-99",
			ts:        1234567890,
			secret:    "another-secret",
			expect:    "fcda4ac208b72b628cdbaa54b3e09a62",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, ifLinkPaySign(tc.uid, tc.amountFen, tc.order, tc.ts, tc.secret))
		})
	}
}

func TestIFLinkPaySignChangesWithEveryField(t *testing.T) {
	base := ifLinkPaySign("1", 500, "TU-IFP-ABCDEFGH", 1700000000, "topsecret")
	assert.NotEqual(t, base, ifLinkPaySign("2", 500, "TU-IFP-ABCDEFGH", 1700000000, "topsecret"))
	assert.NotEqual(t, base, ifLinkPaySign("1", 501, "TU-IFP-ABCDEFGH", 1700000000, "topsecret"))
	assert.NotEqual(t, base, ifLinkPaySign("1", 500, "TU-IFP-ABCDEFGI", 1700000000, "topsecret"))
	assert.NotEqual(t, base, ifLinkPaySign("1", 500, "TU-IFP-ABCDEFGH", 1700000001, "topsecret"))
	assert.NotEqual(t, base, ifLinkPaySign("1", 500, "TU-IFP-ABCDEFGH", 1700000000, "wrong"))
}

func TestIFLinkPaySubscriptionNotifyIsValidatedAndIdempotent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldSQLitePath := common.SQLitePath
	oldMasterNode := common.IsMasterNode
	oldQuotaPerUnit := common.QuotaPerUnit
	oldLogConsume := common.LogConsumeEnabled
	oldMemoryCache := common.MemoryCacheEnabled
	oldRedis := common.RedisEnabled
	oldSQLDSN, hadSQLDSN := os.LookupEnv("SQL_DSN")
	oldPaymentSetting := *operation_setting.GetPaymentSetting()
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SQLitePath = oldSQLitePath
		common.IsMasterNode = oldMasterNode
		common.QuotaPerUnit = oldQuotaPerUnit
		common.LogConsumeEnabled = oldLogConsume
		common.MemoryCacheEnabled = oldMemoryCache
		common.RedisEnabled = oldRedis
		*operation_setting.GetPaymentSetting() = oldPaymentSetting
		if hadSQLDSN {
			require.NoError(t, os.Setenv("SQL_DSN", oldSQLDSN))
		} else {
			require.NoError(t, os.Unsetenv("SQL_DSN"))
		}
	})

	common.SQLitePath = "file:iflinkpay-subscription-notify?mode=memory&cache=shared"
	common.IsMasterNode = false
	common.QuotaPerUnit = 500_000
	common.LogConsumeEnabled = true
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	require.NoError(t, os.Unsetenv("SQL_DSN"))
	require.NoError(t, model.InitDB())
	require.NoError(t, model.InitLogDB())
	require.NoError(t, model.DB.AutoMigrate(
		&model.User{}, &model.Token{}, &model.Log{}, &model.Channel{}, &model.Ability{},
		&model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.UserSubscription{},
		&model.SubscriptionPreConsumeRecord{}, &model.ProductPrice{}, &model.TopUp{},
		&model.SubscriptionKeyBinding{}, &model.CourseEntitlement{}, &model.MonthlySubscriptionCycle{},
	))
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	settings := operation_setting.GetPaymentSetting()
	settings.IFLinkPaySecret = "iflinkpay-test-secret"
	settings.ComplianceConfirmed = true
	settings.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	channel := model.Channel{
		Id: 15, Name: "IFLinkPay test channel", Key: "test", Status: common.ChannelStatusEnabled,
		Models: model.ClassroomModelV41 + "," + model.ClassroomModelV4GA, Group: "default",
	}
	require.NoError(t, model.DB.Create(&channel).Error)

	bootstrap, err := model.BootstrapClassroomProduct(true, true, true, 0)
	require.NoError(t, err)
	user := model.User{Username: "iflinkpay-subscription-user", AffCode: "iflinkpay-subscription-aff", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, model.DB.Create(&user).Error)
	tradeNo := "SUB-IFP-0123456789abcdef0123456789abcdef"
	order, _, err := model.CreateSubscriptionOrder(user.Id, bootstrap.PlanId, tradeNo, model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true)
	require.NoError(t, err)
	amountFen := int64(math.Round(order.Money * 100))

	router := gin.New()
	router.POST("/api/topup/iflinkpay/notify", IFLinkPayNotify)
	sendNotify := func(amount int64) (int, string) {
		t.Helper()
		ts := time.Now().Unix()
		body := fmt.Sprintf(`{"uid":%q,"amount":%d,"order":%q,"ts":%d,"sign":%q}`,
			tradeNo, amount, tradeNo, ts, ifLinkPaySign(tradeNo, amount, tradeNo, ts, settings.IFLinkPaySecret))
		request := httptest.NewRequest(http.MethodPost, "/api/topup/iflinkpay/notify", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}

	status, body := sendNotify(amountFen + 1)
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"code":1}`, body)
	require.Equal(t, common.TopUpStatusPending, model.GetSubscriptionOrderByTradeNo(tradeNo).Status)

	for range 2 {
		status, body = sendNotify(amountFen)
		require.Equal(t, http.StatusOK, status)
		require.JSONEq(t, `{"code":0}`, body)
	}

	completed := model.GetSubscriptionOrderByTradeNo(tradeNo)
	require.Equal(t, common.TopUpStatusSuccess, completed.Status)
	require.Equal(t, model.PaymentProviderIFLinkPay, completed.PaymentProvider)
	require.Equal(t, model.PaymentMethodIFLinkPay, completed.PaymentMethod)
	require.NotNil(t, completed.UserSubscriptionId)
	var subscriptionCount, keyCount int64
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptionCount).Error)
	require.NoError(t, model.DB.Model(&model.SubscriptionKeyBinding{}).Where("user_id = ?", user.Id).Count(&keyCount).Error)
	require.Equal(t, int64(1), subscriptionCount)
	require.Equal(t, int64(1), keyCount)

	encoded, err := common.Marshal(completed)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), settings.IFLinkPaySecret)
}

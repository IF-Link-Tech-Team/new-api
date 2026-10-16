package controller

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
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

func TestNewIFLinkPayTopUpTradeNoIsOpaque(t *testing.T) {
	first, err := newIFLinkPayTopUpTradeNo()
	require.NoError(t, err)
	second, err := newIFLinkPayTopUpTradeNo()
	require.NoError(t, err)

	pattern := regexp.MustCompile(`^TU-IFP-[0-9a-f]{32}$`)
	assert.True(t, pattern.MatchString(first))
	assert.True(t, pattern.MatchString(second))
	assert.NotEqual(t, first, second)
}

func TestIFLinkPayWalletUIDCompatibility(t *testing.T) {
	topUp := &model.TopUp{UserId: 42, TradeNo: "TU-IFP-0123456789abcdef0123456789abcdef"}

	assert.True(t, ifLinkPayWalletUIDMatches(topUp, topUp.TradeNo), "new opaque uid must be accepted")
	assert.True(t, ifLinkPayWalletUIDMatches(topUp, "42"), "legacy pending order uid must remain accepted")
	assert.False(t, ifLinkPayWalletUIDMatches(topUp, "43"))
	assert.False(t, ifLinkPayWalletUIDMatches(nil, topUp.TradeNo))
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
	require.NoError(t, model.DB.Create(&model.Channel{
		Id: 14, Name: "IFLinkPay Qwen test channel", Type: 1, Key: "test-qwen", Status: common.ChannelStatusEnabled,
		Models: model.ClassroomModelQwen, Group: "default",
	}).Error)

	bootstrap, err := model.BootstrapClassroomProduct(true, true, true, 0)
	require.NoError(t, err)
	user := model.User{Username: "iflinkpay-subscription-user", AffCode: "iflinkpay-subscription-aff", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, model.DB.Create(&user).Error)
	tradeNo := "SUB-IFP-0123456789abcdef0123456789abcdef"
	order, _, err := model.CreateSubscriptionOrder(user.Id, bootstrap.PlanId, tradeNo, model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true)
	require.NoError(t, err)
	amountFen := int64(math.Round(order.Money * 100))

	router := gin.New()
	router.POST("/api/user/iflinkpay", func(c *gin.Context) {
		c.Set("id", user.Id)
		RequestIFLinkPay(c)
	})
	router.POST("/api/topup/iflinkpay/notify", IFLinkPayNotify)
	sendNotify := func(uid string, order string, amount int64) (int, string) {
		t.Helper()
		ts := time.Now().Unix()
		body := fmt.Sprintf(`{"uid":%q,"amount":%d,"order":%q,"ts":%d,"sign":%q}`,
			uid, amount, order, ts, ifLinkPaySign(uid, amount, order, ts, settings.IFLinkPaySecret))
		request := httptest.NewRequest(http.MethodPost, "/api/topup/iflinkpay/notify", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}

	status, body := sendNotify(tradeNo, tradeNo, amountFen+1)
	require.Equal(t, http.StatusOK, status)
	require.JSONEq(t, `{"code":1}`, body)
	require.Equal(t, common.TopUpStatusPending, model.GetSubscriptionOrderByTradeNo(tradeNo).Status)

	for range 2 {
		status, body = sendNotify(tradeNo, tradeNo, amountFen)
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

	// A newly created wallet order must expose only its opaque order reference
	// to IFLinkPay. Replaying the signed callback must not credit it twice.
	request := httptest.NewRequest(http.MethodPost, "/api/user/iflinkpay", strings.NewReader(`{"amount":1.25}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var payResponse struct {
		Success bool `json:"success"`
		Data    struct {
			PayURL  string `json:"pay_url"`
			TradeNo string `json:"trade_no"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payResponse))
	require.True(t, payResponse.Success)
	payURL, err := url.Parse(payResponse.Data.PayURL)
	require.NoError(t, err)
	require.Equal(t, payResponse.Data.TradeNo, payURL.Query().Get("uid"))
	require.Equal(t, payResponse.Data.TradeNo, payURL.Query().Get("order"))
	require.NotEqual(t, strconv.Itoa(user.Id), payURL.Query().Get("uid"))

	for range 2 {
		status, body = sendNotify(payResponse.Data.TradeNo, payResponse.Data.TradeNo, 125)
		require.Equal(t, http.StatusOK, status)
		require.JSONEq(t, `{"code":0}`, body)
	}
	newWalletOrder := model.GetTopUpByTradeNo(payResponse.Data.TradeNo)
	require.NotNil(t, newWalletOrder)
	require.Equal(t, common.TopUpStatusSuccess, newWalletOrder.Status)
	var walletUser model.User
	require.NoError(t, model.DB.First(&walletUser, user.Id).Error)
	require.Equal(t, 625_000, walletUser.Quota)

	// Pending orders created by the previous release used the numeric local
	// user ID as uid. They must remain payable after the privacy migration.
	legacyTradeNo := "TU-IFP-legacy-pending-order"
	legacyTopUp := model.TopUp{
		UserId: user.Id, Amount: 2, Money: 2, TradeNo: legacyTradeNo,
		PaymentMethod: model.PaymentMethodIFLinkPay, PaymentProvider: model.PaymentProviderIFLinkPay,
		CreateTime: time.Now().Unix(), Status: common.TopUpStatusPending, Currency: "CNY",
	}
	require.NoError(t, legacyTopUp.Insert())
	for range 2 {
		status, body = sendNotify(strconv.Itoa(user.Id), legacyTradeNo, 200)
		require.Equal(t, http.StatusOK, status)
		require.JSONEq(t, `{"code":0}`, body)
	}
	legacyCompleted := model.GetTopUpByTradeNo(legacyTradeNo)
	require.NotNil(t, legacyCompleted)
	require.Equal(t, common.TopUpStatusSuccess, legacyCompleted.Status)
	require.NoError(t, model.DB.First(&walletUser, user.Id).Error)
	require.Equal(t, 1_625_000, walletUser.Quota)
}

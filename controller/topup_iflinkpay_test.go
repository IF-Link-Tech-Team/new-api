package controller

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This fixture reproduces the old browser-visible redirect, not a verified
// provider receipt. All its signed fields were available to the purchaser.
func legacyIFLinkPayCallbackFromPayURL(t *testing.T, uid string, amountFen int64, tradeNo string) []byte {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	amount := strconv.FormatInt(amountFen, 10)
	digest := md5.Sum([]byte(uid + amount + tradeNo + timestamp + operation_setting.GetIFLinkPaySecret()))
	redirect := "https://payments.example.invalid?" + url.Values{
		"uid": {uid}, "amount": {amount}, "order": {tradeNo}, "ts": {timestamp},
		"sign": {hex.EncodeToString(digest[:])},
	}.Encode()
	parsed, err := url.Parse(redirect)
	require.NoError(t, err)
	query := parsed.Query()
	replayAmount, err := strconv.ParseInt(query.Get("amount"), 10, 64)
	require.NoError(t, err)
	replayTimestamp, err := strconv.ParseInt(query.Get("ts"), 10, 64)
	require.NoError(t, err)
	body, err := common.Marshal(struct {
		UID    string `json:"uid"`
		Amount int64  `json:"amount"`
		Order  string `json:"order"`
		TS     int64  `json:"ts"`
		Sign   string `json:"sign"`
	}{query.Get("uid"), replayAmount, query.Get("order"), replayTimestamp, query.Get("sign")})
	require.NoError(t, err)
	return body
}

func TestIFLinkPayLegacyConfigurationCannotEnableOrAdvertisePayment(t *testing.T) {
	settings := operation_setting.GetPaymentSetting()
	previousSettings, previousMethods := *settings, operation_setting.PayMethods
	t.Cleanup(func() {
		*settings = previousSettings
		operation_setting.PayMethods = previousMethods
	})
	settings.ComplianceConfirmed = true
	settings.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	settings.IFLinkPaySecret = "legacy-configured-secret"
	settings.IFLinkPayBaseURL = "https://payments.example.invalid"
	operation_setting.PayMethods = []map[string]string{
		{"name": "legacy IFLinkPay", "type": "iflinkpay"},
		{"name": "legacy alias", "type": " IFLinkPay "},
		{"name": "unrelated method", "type": "unrelated-method"},
	}

	assert.False(t, isIFLinkPayTopUpEnabled(), "legacy credentials cannot establish verified payment")
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/topup/info", nil)
	GetTopUpInfo(context)
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Enabled    bool                `json:"enable_iflinkpay_topup"`
			PayMethods []map[string]string `json:"pay_methods"`
		} `json:"data"`
	}
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	require.True(t, result.Success)
	assert.False(t, result.Data.Enabled)
	foundUnrelated := false
	for _, method := range result.Data.PayMethods {
		assert.False(t, strings.EqualFold(strings.TrimSpace(method["type"]), model.PaymentMethodIFLinkPay))
		foundUnrelated = foundUnrelated || method["type"] == "unrelated-method"
	}
	assert.True(t, foundUnrelated, "pausing this provider must preserve unrelated configured methods")
	assert.Len(t, operation_setting.PayMethods, 3, "discovery must not rewrite stored configuration")
}

func TestIFLinkPayVerifiedSubscriptionCompletionRemainsIdempotent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
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
		common.SetDatabaseTypes(oldMainType, oldLogType)
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
		&model.SubscriptionPreConsumeRecord{}, &model.ProductPrice{}, &model.PriceBookPrice{}, &model.ProductTemplate{}, &model.TopUp{},
		&model.SubscriptionKeyBinding{}, &model.CourseEntitlement{}, &model.MonthlySubscriptionCycle{},
	))
	require.NoError(t, model.SeedProductTemplates())
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

	// An isolated, explicitly verified payment exercises existing atomic model
	// fulfillment. Browser redirects and public callbacks are never proof here.
	receipt := model.PaymentReceipt{
		Amount: decimal.NewFromFloat(order.Money), Currency: "CNY",
		ProviderTradeNo: "verified-classroom-payment", ProviderPayload: `{"verification":"isolated-test"}`,
	}
	for range 2 {
		require.NoError(t, model.CompleteSubscriptionOrderWithReceipt(tradeNo,
			model.PaymentProviderIFLinkPay, model.PaymentMethodIFLinkPay, receipt))
	}
	completed := model.GetSubscriptionOrderByTradeNo(tradeNo)
	require.NotNil(t, completed)
	assert.Equal(t, common.TopUpStatusSuccess, completed.Status)
	assert.Equal(t, model.ProvisioningStatusProvisioned, completed.ProvisioningStatus)
	assert.Equal(t, order.Money, completed.PaidMoney)
	require.NotNil(t, completed.UserSubscriptionId)
	var subscriptions, bindings, tokens int64
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("user_id = ?", user.Id).Count(&subscriptions).Error)
	require.NoError(t, model.DB.Model(&model.SubscriptionKeyBinding{}).Where("user_id = ?", user.Id).Count(&bindings).Error)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&tokens).Error)
	assert.EqualValues(t, 1, subscriptions)
	assert.EqualValues(t, 1, bindings)
	assert.EqualValues(t, 1, tokens)
	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	assert.Zero(t, after.Quota, "subscription fulfillment must not credit the wallet")
	encoded, err := common.Marshal(completed)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), settings.IFLinkPaySecret)
}

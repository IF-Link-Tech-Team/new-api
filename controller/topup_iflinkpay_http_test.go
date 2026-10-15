package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupIFLinkPayHTTPTest(t *testing.T) *model.User {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis, previousUnit := common.RedisEnabled, common.QuotaPerUnit
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	settings := operation_setting.GetPaymentSetting()
	previousSettings := *settings
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.QuotaPerUnit = false, 500_000
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	settings.ComplianceConfirmed, settings.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
	settings.IFLinkPaySecret, settings.IFLinkPayBaseURL = "isolated-payment-secret", "https://payments.example.invalid"
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.QuotaPerUnit = previousRedis, previousUnit
		common.SetDatabaseTypes(previousMainType, previousLogType)
		*settings = previousSettings
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}))
	user := &model.User{Username: "iflinkpay-http", AffCode: "iflinkpay-http", Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, db.Create(user).Error)
	return user
}

func TestIFLinkPayHTTPFractionalPurchaseAndCallbackReplay(t *testing.T) {
	for _, tc := range []struct {
		amount float64
		fen    int64
		quota  int
	}{{0.01, 1, 5_000}, {1.49, 149, 745_000}, {1.5, 150, 750_000}} {
		t.Run(strconv.FormatFloat(tc.amount, 'f', 2, 64), func(t *testing.T) {
			user := setupIFLinkPayHTTPTest(t)
			body, err := common.Marshal(TopUpIFLinkPayRequest{Amount: tc.amount})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/iflinkpay", bytes.NewReader(body))
			ctx.Set("id", user.Id)
			RequestIFLinkPay(ctx)
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					PayURL  string `json:"pay_url"`
					TradeNo string `json:"trade_no"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			require.True(t, response.Success, recorder.Body.String())
			payURL, err := url.Parse(response.Data.PayURL)
			require.NoError(t, err)
			assert.Equal(t, strconv.FormatInt(tc.fen, 10), payURL.Query().Get("amount"))
			// Taking new sales offline must still allow existing paid orders to complete.
			operation_setting.GetPaymentSetting().ComplianceConfirmed = false
			notify := IFLinkPayNotifyRequest{UID: strconv.Itoa(user.Id), Amount: tc.fen, Order: response.Data.TradeNo, TS: time.Now().Unix()}
			notify.Sign = ifLinkPaySign(notify.UID, notify.Amount, notify.Order, notify.TS, operation_setting.GetIFLinkPaySecret())
			for replay := 0; replay < 2; replay++ {
				assert.Equal(t, 0, invokeIFLinkPayNotify(t, notify))
			}
			var after model.User
			require.NoError(t, model.DB.First(&after, user.Id).Error)
			assert.Equal(t, 100+tc.quota, after.Quota)
			completed := model.GetTopUpByTradeNo(notify.Order)
			require.NotNil(t, completed)
			assert.Equal(t, common.TopUpStatusSuccess, completed.Status)
			assert.Equal(t, tc.amount, completed.PaidMoney)
			assert.Equal(t, notify.Order, completed.ProviderTradeNo)
			var logs int64
			require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeTopup).Count(&logs).Error)
			assert.EqualValues(t, 1, logs, "a callback replay must not create a second financial log")
		})
	}
}

func invokeIFLinkPayNotify(t *testing.T, notify IFLinkPayNotifyRequest) int {
	t.Helper()
	body, err := common.Marshal(notify)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/topup/iflinkpay/notify", bytes.NewReader(body))
	IFLinkPayNotify(ctx)
	var result struct {
		Code int `json:"code"`
	}
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	return result.Code
}

func TestIFLinkPayHTTPRejectsInvalidSignedReceiptsBeforeCrediting(t *testing.T) {
	for _, scenario := range []string{"wrong signature", "wrong user", "wrong amount", "expired timestamp", "wrong provider"} {
		t.Run(scenario, func(t *testing.T) {
			user := setupIFLinkPayHTTPTest(t)
			order := &model.TopUp{UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: "receipt-guard", Currency: "CNY",
				PaymentProvider: model.PaymentProviderIFLinkPay, PaymentMethod: model.PaymentMethodIFLinkPay, Status: common.TopUpStatusPending}
			if scenario == "wrong provider" {
				order.PaymentProvider = model.PaymentProviderAlipay
			}
			require.NoError(t, order.Insert())
			notify := IFLinkPayNotifyRequest{UID: strconv.Itoa(user.Id), Amount: 149, Order: order.TradeNo, TS: time.Now().Unix()}
			switch scenario {
			case "wrong user":
				notify.UID = strconv.Itoa(user.Id + 1)
			case "wrong amount":
				notify.Amount = 150
			case "expired timestamp":
				notify.TS -= ifLinkPayNotifyTimestampToleranceSeconds + 60
			}
			notify.Sign = ifLinkPaySign(notify.UID, notify.Amount, notify.Order, notify.TS, operation_setting.GetIFLinkPaySecret())
			if scenario == "wrong signature" {
				notify.Sign = "invalid-signature"
			}
			assert.NotZero(t, invokeIFLinkPayNotify(t, notify))
			assert.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo(order.TradeNo).Status)
			var after model.User
			require.NoError(t, model.DB.First(&after, user.Id).Error)
			assert.Equal(t, 100, after.Quota)
		})
	}
}

func TestIFLinkPayHTTPRejectsAmountThatCannotBeCreditedBeforeCreatingOrder(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	require.NoError(t, model.DB.Model(user).Update("quota", common.MaxWalletQuota).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/user/iflinkpay", bytes.NewBufferString(`{"amount":10000}`))
	ctx.Set("id", user.Id)
	RequestIFLinkPay(ctx)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success, "do not issue a payment link when the wallet has no credit capacity")
	var orders int64
	require.NoError(t, model.DB.Model(&model.TopUp{}).Count(&orders).Error)
	assert.Zero(t, orders)
}

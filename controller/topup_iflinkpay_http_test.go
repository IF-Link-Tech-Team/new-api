package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
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
	previousMemory, previousConsume := common.MemoryCacheEnabled, common.LogConsumeEnabled
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	settings := operation_setting.GetPaymentSetting()
	previousSettings := *settings
	var db *gorm.DB
	var err error
	postgresDSN := os.Getenv("CILAI_TEST_POSTGRES_DSN")
	if postgresDSN != "" {
		db, err = testutil.OpenPostgres(postgresDSN)
	} else {
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	}
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.QuotaPerUnit = false, 500_000
	common.MemoryCacheEnabled, common.LogConsumeEnabled = false, true
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	if postgresDSN != "" {
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
		sqlDB.SetMaxOpenConns(10)
	}
	settings.ComplianceConfirmed, settings.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
	settings.IFLinkPaySecret, settings.IFLinkPayBaseURL = "isolated-payment-secret", "https://payments.example.invalid"
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.QuotaPerUnit = previousRedis, previousUnit
		common.MemoryCacheEnabled, common.LogConsumeEnabled = previousMemory, previousConsume
		common.SetDatabaseTypes(previousMainType, previousLogType)
		*settings = previousSettings
	})
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.TopUp{}, &model.Log{}, &model.Token{},
		&model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.UserSubscription{},
		&model.SubscriptionKeyBinding{}, &model.CourseEntitlement{}, &model.MonthlySubscriptionCycle{},
		&model.ProductPrice{}, &model.PriceBookPrice{}, &model.ProductTemplate{},
	))
	if postgresDSN != "" {
		t.Cleanup(func() {
			for _, table := range []string{
				"course_entitlements", "subscription_key_bindings", "tokens", "monthly_subscription_cycles",
				"user_subscriptions", "subscription_orders", "subscription_plans", "product_prices",
				"price_book_prices", "product_templates", "top_ups", "logs", "users",
			} {
				require.NoError(t, db.Exec("DELETE FROM "+table).Error)
			}
		})
	}
	user := &model.User{Username: "iflinkpay-http", AffCode: "iflinkpay-http", Status: common.UserStatusEnabled, Quota: 100}
	require.NoError(t, db.Create(user).Error)
	return user
}

func TestIFLinkPayHTTPPausedCallbacksDoNotRequireBodyParsingOrDatabase(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	settings := operation_setting.GetPaymentSetting()
	previousSettings := *settings
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		*settings = previousSettings
	})
	model.DB, model.LOG_DB = nil, nil
	settings.IFLinkPaySecret = "legacy-configured-secret"
	bodies := []struct {
		name string
		body []byte
	}{
		{name: "empty"},
		{name: "malformed", body: []byte(`{"uid":`)},
		{name: "legacy signed redirect", body: legacyIFLinkPayCallbackFromPayURL(t,
			"TU-IFP-no-database", 149, "TU-IFP-no-database")},
	}
	router := gin.New()
	paths := []string{"/api/topup/iflinkpay/notify", "/api/pay/callback"}
	for _, path := range paths {
		router.POST(path, IFLinkPayNotify)
	}
	for _, path := range paths {
		for _, body := range bodies {
			t.Run(path+"/"+body.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body.body))
				request.Header.Set("Content-Type", "application/json")
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
				assert.Equal(t, "300", recorder.Header().Get("Retry-After"))
				assert.JSONEq(t, `{"code":1}`, recorder.Body.String())
			})
		}
	}
}

func TestIFLinkPayHTTPBrowserRedirectReplayCannotFulfillEitherOrderType(t *testing.T) {
	cases := []struct {
		name         string
		subscription bool
		numericUID   bool
		removeSecret bool
	}{
		{name: "wallet opaque uid"},
		{name: "wallet legacy numeric uid", numericUID: true},
		{name: "subscription", subscription: true},
		{name: "wallet after secret removal", removeSecret: true},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			user := setupIFLinkPayHTTPTest(t)
			tradeNo := "TU-IFP-browser-redirect"
			if scenario.subscription {
				tradeNo = "SUB-IFP-browser-redirect"
				plan := &model.SubscriptionPlan{
					Title: "Existing pending subscription", Currency: "CNY", PriceAmount: 1.49,
					DurationUnit: model.SubscriptionDurationDay, DurationValue: 10,
					TotalAmount: 745_000, QuotaResetPeriod: model.SubscriptionResetNever,
					Category: model.SubscriptionCategoryPackage, Enabled: true,
				}
				require.NoError(t, model.DB.Create(plan).Error)
				_, _, err := model.CreateSubscriptionOrder(user.Id, plan.Id, tradeNo,
					model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true)
				require.NoError(t, err)
				t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
			} else {
				order := &model.TopUp{UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: tradeNo, Currency: "CNY",
					PaymentProvider: model.PaymentProviderIFLinkPay, PaymentMethod: model.PaymentMethodIFLinkPay, Status: common.TopUpStatusPending}
				require.NoError(t, order.Insert())
			}
			uid := tradeNo
			if scenario.numericUID {
				uid = strconv.Itoa(user.Id)
			}
			body := legacyIFLinkPayCallbackFromPayURL(t, uid, 149, tradeNo)
			if scenario.removeSecret {
				operation_setting.GetPaymentSetting().IFLinkPaySecret = ""
			}
			router := gin.New()
			for _, path := range []string{"/api/topup/iflinkpay/notify", "/api/pay/callback"} {
				router.POST(path, IFLinkPayNotify)
			}
			for _, path := range []string{"/api/topup/iflinkpay/notify", "/api/pay/callback"} {
				for range 2 {
					request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, request)
					assert.Equal(t, http.StatusServiceUnavailable, recorder.Code, path)
					assert.Equal(t, "300", recorder.Header().Get("Retry-After"), path)
					assert.JSONEq(t, `{"code":1}`, recorder.Body.String())
					var after model.User
					require.NoError(t, model.DB.First(&after, user.Id).Error)
					assert.Equal(t, 100, after.Quota, "browser-visible fields cannot authorize a wallet credit")
					if scenario.subscription {
						order := model.GetSubscriptionOrderByTradeNo(tradeNo)
						require.NotNil(t, order)
						assert.Equal(t, common.TopUpStatusPending, order.Status)
						assert.Nil(t, order.UserSubscriptionId)
						assert.Zero(t, order.PaidMoney)
						assert.Zero(t, order.CompleteTime)
						assert.Empty(t, order.ProviderPayload)
						assert.Empty(t, order.ProviderTradeNo)
					} else {
						order := model.GetTopUpByTradeNo(tradeNo)
						require.NotNil(t, order)
						assert.Equal(t, common.TopUpStatusPending, order.Status)
						assert.Zero(t, order.PaidMoney)
						assert.Zero(t, order.CompleteTime)
						assert.Empty(t, order.ProviderPayload)
						assert.Empty(t, order.ProviderTradeNo)
					}
					for _, table := range []any{&model.UserSubscription{}, &model.Token{}, &model.SubscriptionKeyBinding{}, &model.Log{}} {
						var count int64
						require.NoError(t, model.DB.Model(table).Where("user_id = ?", user.Id).Count(&count).Error)
						assert.Zero(t, count, "an unverified callback must not issue entitlements, keys, or financial logs")
					}
				}
			}
		})
	}
}

func TestIFLinkPayHTTPPurchasesStayUnavailableWithLegacyCredentials(t *testing.T) {
	for _, endpoint := range []string{"/api/user/iflinkpay", "/api/subscription/iflinkpay/pay"} {
		t.Run(endpoint, func(t *testing.T) {
			user := setupIFLinkPayHTTPTest(t)
			plan := &model.SubscriptionPlan{
				Title: "Enabled test subscription", Currency: "CNY", PriceAmount: 1.49,
				DurationUnit: model.SubscriptionDurationDay, DurationValue: 10,
				TotalAmount: 745_000, QuotaResetPeriod: model.SubscriptionResetNever,
				Category: model.SubscriptionCategoryPackage, Enabled: true,
			}
			require.NoError(t, model.DB.Create(plan).Error)
			t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
			body, err := common.Marshal(map[string]any{"amount": 1.49, "plan_id": plan.Id})
			require.NoError(t, err)
			router := gin.New()
			setUser := func(c *gin.Context) { c.Set("id", user.Id); c.Next() }
			router.POST("/api/user/iflinkpay", setUser, RequestIFLinkPay)
			router.POST("/api/subscription/iflinkpay/pay", setUser, SubscriptionRequestIFLinkPay)
			request := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			assert.Equal(t, "300", recorder.Header().Get("Retry-After"))
			var result struct {
				Success    bool   `json:"success"`
				MessageKey string `json:"message_key"`
				Data       struct {
					PayURL  string `json:"pay_url"`
					TradeNo string `json:"trade_no"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
			assert.False(t, result.Success)
			assert.Equal(t, "Online payment is not enabled. Please contact the administrator.", result.MessageKey)
			assert.Empty(t, result.Data.PayURL)
			assert.Empty(t, result.Data.TradeNo)
			for _, table := range []any{&model.TopUp{}, &model.SubscriptionOrder{}, &model.UserSubscription{}, &model.Token{}} {
				var count int64
				require.NoError(t, model.DB.Model(table).Where("user_id = ?", user.Id).Count(&count).Error)
				assert.Zero(t, count)
			}
		})
	}
}

func TestIFLinkPayHTTPRedirectReplayDoesNotDuplicateVerifiedManualCredit(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	order := &model.TopUp{UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: "verified-manual-credit", Currency: "CNY",
		PaymentProvider: model.PaymentProviderIFLinkPay, PaymentMethod: model.PaymentMethodIFLinkPay, Status: common.TopUpStatusPending}
	require.NoError(t, order.Insert())
	browserCallback := legacyIFLinkPayCallbackFromPayURL(t, order.TradeNo, 149, order.TradeNo)
	// An administrator has independently verified payment in this fixture.
	require.NoError(t, model.ManualCompleteTopUp(order.TradeNo, "127.0.0.1"))
	require.NoError(t, model.ManualCompleteTopUp(order.TradeNo, "127.0.0.1"))
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/pay/callback", bytes.NewReader(browserCallback))
	IFLinkPayNotify(context)
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.JSONEq(t, `{"code":1}`, recorder.Body.String())
	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	assert.Equal(t, 745_100, after.Quota)
	assert.Equal(t, common.TopUpStatusSuccess, model.GetTopUpByTradeNo(order.TradeNo).Status)
	var logs int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeTopup).Count(&logs).Error)
	assert.EqualValues(t, 1, logs)
}

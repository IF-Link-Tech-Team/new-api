package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
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
	// Every IFLinkPay entry point 閳?purchase and verified re-check alike 閳?must
	// stay closed while the verified-payment switch is off. Without this, a
	// deployment that merely configured legacy credentials would expose a path
	// that could settle an order.
	for _, endpoint := range []string{
		"/api/user/iflinkpay",
		"/api/subscription/iflinkpay/pay",
		"/api/user/iflinkpay/verify",
		"/api/subscription/iflinkpay/verify",
	} {
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
			body, err := common.Marshal(map[string]any{"amount": 1.49, "plan_id": plan.Id, "trade_no": "TU-IFP-verify-probe"})
			require.NoError(t, err)
			router := gin.New()
			setUser := func(c *gin.Context) { c.Set("id", user.Id); c.Next() }
			router.POST("/api/user/iflinkpay", setUser, RequestIFLinkPay)
			router.POST("/api/subscription/iflinkpay/pay", setUser, SubscriptionRequestIFLinkPay)
			router.POST("/api/user/iflinkpay/verify", setUser, VerifyIFLinkPayPayment)
			router.POST("/api/subscription/iflinkpay/verify", setUser, VerifyIFLinkPaySubscriptionPayment)
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

// TestIFLinkPayReconcilerWalksPastAFullPageOfAbandonedOrders is the regression
// that keeps a full page of never-paid orders from hiding real payments.
//
// Both pages of abandoned orders sort before the paid order by create_time, so a
// reconciler that always re-read the oldest page would never reach it. Consecutive
// passes must walk the whole window and settle it.
func TestIFLinkPayReconcilerWalksPastAFullPageOfAbandonedOrders(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	const amountFen int64 = 149

	provider := fakeIFLinkPayProvider(t, "walk-paid", amountFen)
	defer provider.Close()
	t.Setenv("IFLINK_PAY_V2_ENABLED", "true")
	t.Setenv("IFLINK_PAY_CLIENT_ID", "iflink")
	t.Setenv("IFLINK_PAY_QUERY_KEY_ID", "query-test")
	t.Setenv("IFLINK_PAY_QUERY_SECRET", "query-secret-test")
	t.Setenv("IFLINK_PAY_SECRET", "link-secret-test")
	t.Setenv("IFLINK_PAY_API_BASE_URL", provider.URL)
	ifLinkPayTopUpCursor.advance(nil)

	now := common.GetTimestamp()
	// Two full pages of abandoned orders, all older than the order that is paid.
	for i := range ifLinkPayReconcileBatch * 2 {
		require.NoError(t, model.DB.Create(&model.TopUp{
			UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: "walk-abandoned-" + strconv.Itoa(i), Currency: "CNY",
			PaymentProvider: model.PaymentProviderIFLinkPay,
			PaymentMethod:   model.PaymentMethodIFLinkPay,
			Status:          common.TopUpStatusPending,
			CreateTime:      now - 60 + int64(i),
		}).Error)
	}
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: "walk-paid", Currency: "CNY",
		PaymentProvider: model.PaymentProviderIFLinkPay,
		PaymentMethod:   model.PaymentMethodIFLinkPay,
		Status:          common.TopUpStatusPending,
		CreateTime:      now,
	}).Error)

	var before model.User
	require.NoError(t, model.DB.First(&before, user.Id).Error)

	// The first pass sees only the oldest page and must not settle anything.
	reconcileIFLinkPayPending()
	assert.Equal(t, common.TopUpStatusPending, model.GetTopUpByTradeNo("walk-paid").Status,
		"the paid order is behind a full page and cannot be reached in one pass")

	// Consecutive passes continue where the previous one stopped.
	for range 3 {
		reconcileIFLinkPayPending()
	}

	paid := model.GetTopUpByTradeNo("walk-paid")
	require.NotNil(t, paid)
	assert.Equal(t, common.TopUpStatusSuccess, paid.Status,
		"walking the window must reach the paid order behind the abandoned ones")

	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	assert.Equal(t, before.Quota+745_000, after.Quota, "exactly one order is credited")
}

// TestIFLinkPayWalletReusesPendingOrderForTheSameAmount keeps a payer who goes
// back and re-picks the amount from leaving another abandoned order behind, and
// proves a different amount still gets its own order.
func TestIFLinkPayWalletReusesPendingOrderForTheSameAmount(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	t.Setenv("IFLINK_PAY_V2_ENABLED", "true")
	t.Setenv("IFLINK_PAY_CLIENT_ID", "iflink")
	t.Setenv("IFLINK_PAY_QUERY_KEY_ID", "query-test")
	t.Setenv("IFLINK_PAY_QUERY_SECRET", "query-secret-test")
	t.Setenv("IFLINK_PAY_SECRET", "link-secret-test")
	t.Setenv("IFLINK_PAY_API_BASE_URL", "https://payments.example.invalid")

	router := gin.New()
	setUser := func(c *gin.Context) { c.Set("id", user.Id); c.Next() }
	router.POST("/api/user/iflinkpay", setUser, RequestIFLinkPay)

	post := func(amount float64) (tradeNo string) {
		body, err := common.Marshal(map[string]any{"amount": amount})
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/api/user/iflinkpay", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				PayURL  string  `json:"pay_url"`
				TradeNo string  `json:"trade_no"`
				Amount  float64 `json:"amount"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
		require.True(t, result.Success)
		assert.NotEmpty(t, result.Data.PayURL)
		return result.Data.TradeNo
	}
	countPending := func() int64 {
		var count int64
		require.NoError(t, model.DB.Model(&model.TopUp{}).
			Where("user_id = ? AND status = ?", user.Id, common.TopUpStatusPending).Count(&count).Error)
		return count
	}

	first := post(1.49)
	assert.Equal(t, int64(1), countPending())

	// The same payer picks the same amount again: one order, not two.
	assert.Equal(t, first, post(1.49))
	assert.Equal(t, int64(1), countPending(), "re-picking the same amount must not add an abandoned order")

	// Each attempt still gets a usable cashier URL for the reused order.
	reused := post(1.49)
	assert.Equal(t, first, reused)

	// A different amount is a different purchase and gets its own order.
	different := post(2.00)
	assert.NotEqual(t, first, different)
	assert.Equal(t, int64(2), countPending())
}

// TestIFLinkPayReconcilerRetiresOnlyProviderTerminalOrders is the regression that
// keeps retirement from becoming a new way to lose a payment.
//
// An order may leave the queue only when the provider itself reports that the
// payment can no longer succeed. A status that is merely not paid yet, or one the
// provider does not document, must leave the order pending so the walk keeps
// checking it: retiring on our own clock could abandon a payment the payer is
// about to complete.
func TestIFLinkPayReconcilerRetiresOnlyProviderTerminalOrders(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		wantStatus string
	}{
		{name: "provider expired retires", status: "EXPIRED", wantStatus: common.TopUpStatusExpired},
		{name: "provider closed retires", status: "CLOSED", wantStatus: common.TopUpStatusExpired},
		{name: "provider failed retires", status: "FAILED", wantStatus: common.TopUpStatusExpired},
		{name: "prepay stays pending", status: "PREPAY", wantStatus: common.TopUpStatusPending},
		{name: "verifying stays pending", status: "VERIFYING", wantStatus: common.TopUpStatusPending},
		{name: "unknown status stays pending", status: "SOMETHING_NEW", wantStatus: common.TopUpStatusPending},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := setupIFLinkPayHTTPTest(t)
			const amountFen int64 = 149
			tradeNo := "retire-" + tc.status

			provider := fakeIFLinkPayProvider(t, "", amountFen)
			defer provider.Close()
			t.Setenv("IFLINK_PAY_V2_ENABLED", "true")
			t.Setenv("IFLINK_PAY_CLIENT_ID", "iflink")
			t.Setenv("IFLINK_PAY_QUERY_KEY_ID", "query-test")
			t.Setenv("IFLINK_PAY_QUERY_SECRET", "query-secret-test")
			t.Setenv("IFLINK_PAY_SECRET", "link-secret-test")
			t.Setenv("IFLINK_PAY_API_BASE_URL", provider.URL)
			ifLinkPayTopUpCursor.advance(nil)

			require.NoError(t, model.DB.Create(&model.TopUp{
				UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: tradeNo, Currency: "CNY",
				PaymentProvider: model.PaymentProviderIFLinkPay,
				PaymentMethod:   model.PaymentMethodIFLinkPay,
				Status:          common.TopUpStatusPending,
				CreateTime:      common.GetTimestamp(),
			}).Error)

			// The provider reports this order with the status under test.
			provider.setStatus(tradeNo, tc.status, amountFen)

			var before model.User
			require.NoError(t, model.DB.First(&before, user.Id).Error)
			reconcileIFLinkPayPending()

			order := model.GetTopUpByTradeNo(tradeNo)
			require.NotNil(t, order)
			assert.Equal(t, tc.wantStatus, order.Status)

			var after model.User
			require.NoError(t, model.DB.First(&after, user.Id).Error)
			assert.Equal(t, before.Quota, after.Quota, "retirement must never credit quota")
		})
	}
}

// TestIFLinkPayRetiredOrderStaysRecoverableByAnAdministrator covers the safety net
// behind retirement: an administrator must still be able to complete an expired
// order, and completing it twice must not double-credit.
func TestIFLinkPayRetiredOrderStaysRecoverableByAnAdministrator(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	order := &model.TopUp{
		UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: "retired-manual-recovery", Currency: "CNY",
		PaymentProvider: model.PaymentProviderIFLinkPay,
		PaymentMethod:   model.PaymentMethodIFLinkPay,
		Status:          common.TopUpStatusExpired,
		CreateTime:      common.GetTimestamp(),
	}
	require.NoError(t, order.Insert())

	require.NoError(t, model.ManualCompleteTopUp(order.TradeNo, "127.0.0.1"),
		"an administrator must be able to recover an expired order")
	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	assert.Equal(t, 100+745_000, after.Quota)

	// Idempotent, and a settled order is final.
	require.NoError(t, model.ManualCompleteTopUp(order.TradeNo, "127.0.0.1"))
	var again model.User
	require.NoError(t, model.DB.First(&again, user.Id).Error)
	assert.Equal(t, after.Quota, again.Quota, "recovering twice must not credit twice")

	// A failed order is still refused: only pending and expired are recoverable.
	failed := &model.TopUp{
		UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: "retired-failed-refused", Currency: "CNY",
		PaymentProvider: model.PaymentProviderIFLinkPay,
		PaymentMethod:   model.PaymentMethodIFLinkPay,
		Status:          common.TopUpStatusFailed,
		CreateTime:      common.GetTimestamp(),
	}
	require.NoError(t, failed.Insert())
	assert.Error(t, model.ManualCompleteTopUp(failed.TradeNo, "127.0.0.1"))
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

// fakeIFLinkPayProvider emulates the provider's cashier endpoints so the
// reconciler can be exercised without touching the real provider. Only the order
// named in paidOrder is reported PAID; everything else stays PREPAY. Orders
// registered with setStatus report exactly the status they were given.
func fakeIFLinkPayProvider(t *testing.T, paidOrder string, amountFen int64) *fakeIFLinkPayServer {
	t.Helper()
	server := &fakeIFLinkPayServer{paidOrder: paidOrder, amountFen: amountFen, statuses: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/pay/iflink/checkout", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Order string `json:"order"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session": "sess-" + body.Order,
			"payment": map[string]any{
				"status": "PREPAY", "amount": amountFen, "order": body.Order,
			},
		})
	})
	mux.HandleFunc("/api/pay/iflink/status", func(w http.ResponseWriter, r *http.Request) {
		order := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer sess-")
		status := "PREPAY"
		if order == server.paidOrder {
			status = "PAID"
		}
		server.mu.Lock()
		if override, ok := server.statuses[order]; ok {
			status = override
		}
		server.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": status, "amount": amountFen, "order": order,
		})
	})
	server.Server = httptest.NewServer(mux)
	return server
}

// fakeIFLinkPayServer is the httptest server plus the ability to pin a status for
// one order.
type fakeIFLinkPayServer struct {
	*httptest.Server
	mu        sync.Mutex
	paidOrder string
	amountFen int64
	statuses  map[string]string
}

// setStatus makes the provider report status for one order.
func (s *fakeIFLinkPayServer) setStatus(order string, status string, amountFen int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[order] = status
	s.amountFen = amountFen
}

// TestIFLinkPayReconcilerSettlesOnlyProviderConfirmedOrders proves the
// reconciliation path that replaces the (currently unauthenticated) callback:
// a payer who closes the browser is still credited, but only when the provider
// itself reports PAID for that exact order and amount.
func TestIFLinkPayReconcilerSettlesOnlyProviderConfirmedOrders(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	const amountFen int64 = 149

	provider := fakeIFLinkPayProvider(t, "reconcile-paid", amountFen)
	defer provider.Close()
	t.Setenv("IFLINK_PAY_V2_ENABLED", "true")
	t.Setenv("IFLINK_PAY_CLIENT_ID", "iflink")
	t.Setenv("IFLINK_PAY_QUERY_KEY_ID", "query-test")
	t.Setenv("IFLINK_PAY_QUERY_SECRET", "query-secret-test")
	t.Setenv("IFLINK_PAY_SECRET", "link-secret-test")
	t.Setenv("IFLINK_PAY_API_BASE_URL", provider.URL)

	now := common.GetTimestamp()
	makeOrder := func(tradeNo string) {
		require.NoError(t, model.DB.Create(&model.TopUp{
			UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: tradeNo, Currency: "CNY",
			PaymentProvider: model.PaymentProviderIFLinkPay,
			PaymentMethod:   model.PaymentMethodIFLinkPay,
			Status:          common.TopUpStatusPending,
			CreateTime:      now,
		}).Error)
	}
	makeOrder("reconcile-paid")
	makeOrder("reconcile-unpaid")

	var before model.User
	require.NoError(t, model.DB.First(&before, user.Id).Error)

	reconcileIFLinkPayPending()

	paid := model.GetTopUpByTradeNo("reconcile-paid")
	require.NotNil(t, paid)
	assert.Equal(t, common.TopUpStatusSuccess, paid.Status, "provider-confirmed order must be settled")
	assert.Equal(t, "reconcile-paid", paid.ProviderTradeNo)
	assert.InDelta(t, 1.49, paid.PaidMoney, 0.001)

	unpaid := model.GetTopUpByTradeNo("reconcile-unpaid")
	require.NotNil(t, unpaid)
	assert.Equal(t, common.TopUpStatusPending, unpaid.Status, "unpaid order must stay pending")

	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	// 1.49 CNY at QuotaPerUnit 500000 credits exactly 745000, once.
	assert.Equal(t, before.Quota+745_000, after.Quota,
		"quota is credited for exactly one order and only once")

	// Idempotent: a second pass must not credit again.
	reconcileIFLinkPayPending()
	var again model.User
	require.NoError(t, model.DB.First(&again, user.Id).Error)
	assert.Equal(t, after.Quota, again.Quota)
}

// TestIFLinkPayReconcilerDoesNothingWhenSwitchIsOff guards the fail-closed
// posture: with the verified-payment switch off, no provider call and no credit.
func TestIFLinkPayReconcilerDoesNothingWhenSwitchIsOff(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	t.Setenv("IFLINK_PAY_V2_ENABLED", "false")
	t.Setenv("IFLINK_PAY_API_BASE_URL", "https://payments.example.invalid")

	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId: user.Id, Amount: 1, Money: 1.49, TradeNo: "reconcile-switch-off", Currency: "CNY",
		PaymentProvider: model.PaymentProviderIFLinkPay,
		PaymentMethod:   model.PaymentMethodIFLinkPay,
		Status:          common.TopUpStatusPending,
		CreateTime:      common.GetTimestamp(),
	}).Error)

	var before model.User
	require.NoError(t, model.DB.First(&before, user.Id).Error)

	reconcileIFLinkPayPending()

	order := model.GetTopUpByTradeNo("reconcile-switch-off")
	require.NotNil(t, order)
	assert.Equal(t, common.TopUpStatusPending, order.Status)

	var after model.User
	require.NoError(t, model.DB.First(&after, user.Id).Error)
	assert.Equal(t, before.Quota, after.Quota, "no credit may happen while the switch is off")
}

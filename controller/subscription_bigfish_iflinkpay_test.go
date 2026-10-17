package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIFLinkPayBigfishRetryKeepsSignedOrderAndCannotPaySecondTier(t *testing.T) {
	user := setupIFLinkPayHTTPTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.UserSubscription{}, &model.ProductPrice{}))
	db := model.DB
	t.Cleanup(func() {
		require.NoError(t, db.Where("product_type = ?", model.ProductTypePackDeepSeekBigfish).Delete(&model.SubscriptionOrder{}).Error)
		require.NoError(t, db.Where("product_type = ?", model.ProductTypePackDeepSeekBigfish).Delete(&model.SubscriptionPlan{}).Error)
		require.NoError(t, db.Where("product_type = ?", model.ProductTypePackDeepSeekBigfish).Delete(&model.ProductPrice{}).Error)
	})
	data, err := os.ReadFile("../model/testdata/bigfish-draft.json")
	require.NoError(t, err)
	var catalog struct {
		Prices []struct {
			Price model.ProductPrice `json:"price"`
		} `json:"price_requests"`
		Plans []struct {
			Plan model.SubscriptionPlan `json:"plan"`
		} `json:"plan_requests"`
	}
	require.NoError(t, common.Unmarshal(data, &catalog))
	for _, item := range catalog.Prices {
		item.Price.Enabled = true
		require.NoError(t, model.DB.Create(&item.Price).Error)
	}
	for index := range catalog.Plans {
		plan := &catalog.Plans[index].Plan
		require.NoError(t, model.DB.Create(plan).Error)
		require.False(t, plan.Enabled)
		require.False(t, plan.Listed)
		require.False(t, plan.RedemptionEnabled)
		require.NoError(t, model.DB.Model(plan).Update("enabled", true).Error)
		model.InvalidateSubscriptionPlanCache(plan.Id)
		planId := plan.Id
		t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(planId) })
	}
	router := gin.New()
	router.POST("/api/subscription/iflinkpay/pay", func(c *gin.Context) { c.Set("id", user.Id); c.Next() }, SubscriptionRequestIFLinkPay)
	firstTrade := ""
	for range 2 {
		body := `{"plan_id":` + strconv.Itoa(catalog.Plans[0].Plan.Id) + `}`
		request := httptest.NewRequest(http.MethodPost, "/api/subscription/iflinkpay/pay", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				PayURL  string `json:"pay_url"`
				TradeNo string `json:"trade_no"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
		require.True(t, result.Success, recorder.Body.String())
		if firstTrade == "" {
			firstTrade = result.Data.TradeNo
		}
		require.Equal(t, firstTrade, result.Data.TradeNo)
		target, err := url.Parse(result.Data.PayURL)
		require.NoError(t, err)
		query := target.Query()
		require.Equal(t, firstTrade, query.Get("order"))
		require.Equal(t, firstTrade, query.Get("uid"))
		require.Equal(t, "990", query.Get("amount"))
		ts, err := strconv.ParseInt(query.Get("ts"), 10, 64)
		require.NoError(t, err)
		require.Equal(t, ifLinkPaySign(firstTrade, 990, firstTrade, ts, operation_setting.GetIFLinkPaySecret()), query.Get("sign"))
	}
	request := httptest.NewRequest(http.MethodPost, "/api/subscription/iflinkpay/pay", strings.NewReader(`{"plan_id":`+strconv.Itoa(catalog.Plans[1].Plan.Id)+`}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	var rejected struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &rejected))
	require.False(t, rejected.Success)
	require.Contains(t, rejected.Message, "待支付")
	var count int64
	require.NoError(t, model.DB.Model(&model.SubscriptionOrder{}).Where("user_id = ? AND product_type = ?", user.Id, model.ProductTypePackDeepSeekBigfish).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

package controller

// 支付宝原生集成 - 订阅路径 (用户下单购买套餐)
//
// 链路:
//   POST /api/subscription/alipay/pay    -> 创建 subscription_orders (pending) -> 返回支付 URL
//   用户在浏览器完成付款
//   POST /api/subscription/alipay/notify <- 支付宝异步通知 (与 topup notify 共用 AlipayNotify 路径)
//   GET  /api/subscription/alipay/return <- 支付宝同步跳转
//
// 关键点:
//   - notify endpoint 与 topup notify 复用同一个 AlipayNotify;  handler 内通过
//     model.CompleteSubscriptionOrder 优先尝试,然后 fallback 到 topup 路径
//   - subscription_orders.payment_provider = 'alipay'

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/thanhpk/randstr"
)

type SubscriptionAlipayPayRequest struct {
	PlanId int `json:"plan_id"`
}

func SubscriptionRequestAlipayPay(c *gin.Context) {
	if !isAlipayTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "支付宝支付尚未启用,请管理员先配置 Alipay 凭据",
		})
		return
	}
	if !requirePaymentCompliance(c) {
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "read body error"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var req SubscriptionAlipayPayRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil || req.PlanId <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "套餐未启用"})
		return
	}
	if plan.PriceAmount < 0.01 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "套餐金额过低"})
		return
	}

	userId := c.GetInt("id")
	user, err := model.GetUserById(userId, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	if plan.MaxPurchasePerUser > 0 {
		count, err := model.CountUserSubscriptionsByPlan(userId, plan.Id)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": "已达到该套餐购买上限"})
			return
		}
	}

	tradeNo := "SUB-ALI-" + randstr.String(8)
	order := &model.SubscriptionOrder{
		UserId:          userId,
		PlanId:          plan.Id,
		Money:           plan.PriceAmount,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodAlipay,
		PaymentProvider: model.PaymentProviderAlipay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := order.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建订单失败"})
		return
	}

	callBack := service.GetCallbackAddress()
	notifyURL := callBack + "/api/subscription/alipay/notify"
	returnURL := callBack + "/api/subscription/alipay/return?trade_no=" + tradeNo

	biz := map[string]string{
		"out_trade_no": tradeNo,
		"product_code": "FAST_INSTANT_TRADE_PAY",
		"total_amount":  fmt.Sprintf("%.2f", plan.PriceAmount),
		"subject":       fmt.Sprintf("IF.Link 订阅 - %s", plan.Title),
	}
	bizJSON, _ := json.Marshal(biz)
	form, err := alipayBuildSignedForm("alipay.trade.page.pay", string(bizJSON), notifyURL, returnURL, nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Alipay 订阅支付签名失败 trade_no=%s error=%q", tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "签名失败"})
		return
	}

	payURL := alipayGateway() + "?" + form

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Alipay 订阅支付链接创建成功 trade_no=%s plan_id=%d user_id=%d amount=%.2f", tradeNo, plan.Id, userId, plan.PriceAmount))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"pay_url":  payURL,
			"trade_no": tradeNo,
		},
	})
}

// SubscriptionAlipayReturn 同步跳转(GET)
func SubscriptionAlipayReturn(c *gin.Context) {
	tradeNo := c.Query("trade_no")
	c.Redirect(http.StatusFound, "/console/subscription?alipay_return="+url.QueryEscape(tradeNo))
}
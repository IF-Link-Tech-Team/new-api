package controller

// IFLinkPay subscription payment reuses the existing subscription order
// snapshot and atomic provisioning pipeline. The payment provider receives an
// opaque random order reference as uid, never the local database user ID.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type SubscriptionIFLinkPayRequest struct {
	PlanId int `json:"plan_id"`
}

func newIFLinkPaySubscriptionTradeNo() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "SUB-IFP-" + hex.EncodeToString(random), nil
}

func SubscriptionRequestIFLinkPay(c *gin.Context) {
	if !isIFLinkPayTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "IFLinkPay 支付尚未启用"})
		return
	}
	if !requirePaymentCompliance(c) {
		return
	}

	var req SubscriptionIFLinkPayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
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
	if !strings.EqualFold(strings.TrimSpace(plan.Currency), "CNY") {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "IFLinkPay 仅支持人民币套餐"})
		return
	}

	tradeNo, err := newIFLinkPaySubscriptionTradeNo()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("IFLinkPay 订阅订单号生成失败 error=%q", err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建支付订单失败"})
		return
	}
	order, frozenPlan, err := model.CreateSubscriptionOrder(
		c.GetInt("id"), plan.Id, tradeNo,
		model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true,
	)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	amountFen := int64(math.Round(order.Money * 100))
	if amountFen <= 0 {
		_ = model.ExpireSubscriptionOrder(tradeNo, model.PaymentProviderIFLinkPay)
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "套餐金额必须大于 0"})
		return
	}

	returnTarget := "/plans"
	if frozenPlan.Category == model.SubscriptionCategoryPackage {
		returnTarget = "/traffic-packs"
	}
	returnURL := paymentReturnPath(returnTarget + "?iflinkpay_return=" + url.QueryEscape(tradeNo))
	ts := time.Now().Unix()
	secret := strings.TrimSpace(operation_setting.GetIFLinkPaySecret())
	// The provider contract names this field uid, but it is an opaque external
	// transaction reference for subscription purchases.
	externalUID := tradeNo
	sign := ifLinkPaySign(externalUID, amountFen, tradeNo, ts, secret)
	payURL := fmt.Sprintf("%s?uid=%s&amount=%d&order=%s&ts=%d&sign=%s&return=%s",
		operation_setting.GetIFLinkPayBaseURL(),
		url.QueryEscape(externalUID), amountFen, url.QueryEscape(tradeNo), ts,
		sign, url.QueryEscape(returnURL),
	)

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("IFLinkPay 订阅支付链接创建成功 trade_no=%s plan_id=%d user_id=%d amount=%.2f", tradeNo, frozenPlan.Id, c.GetInt("id"), order.Money))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "success",
		"data":    gin.H{"pay_url": payURL, "trade_no": tradeNo},
	})
}

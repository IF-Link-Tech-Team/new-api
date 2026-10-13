package controller

// 支付宝原生集成 - 通用钱包 topup 路径
//
// 链路:
//   POST /api/user/topup            -> 创建 top_up(pending) -> 返回支付宝支付 URL/form
//   用户在浏览器完成付款
//   POST /api/topup/alipay/notify   <- 支付宝异步通知(同步返回 "success")
//   POST /api/topup/alipay/return   <- 支付宝同步跳转(GET,给用户看)

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

type TopUpAlipayPayRequest struct {
	Amount    float64 `json:"amount"`      // 元(支持小数,沙箱测试 ¥0.01 必须 float64)
	TopUpCode string  `json:"top_up_code"` // 兼容老 topup 字段
}

func isFrozenV1WalletAmount(amount float64) bool {
	value := decimal.NewFromFloat(amount).Round(2)
	return value.Equal(decimal.NewFromInt(10)) || value.Equal(decimal.NewFromInt(30)) || value.Equal(decimal.NewFromInt(50))
}

func validateAlipayNotifyIdentity(params map[string]string) error {
	expectedAppID := strings.TrimSpace(setting.AlipayAppID)
	if expectedAppID == "" {
		return errors.New("Alipay AppID is not configured")
	}
	appID := strings.TrimSpace(params["app_id"])
	authAppID := strings.TrimSpace(params["auth_app_id"])
	if appID == "" && authAppID == "" {
		return errors.New("Alipay notify is missing app_id/auth_app_id")
	}
	if appID != "" && appID != expectedAppID {
		return errors.New("Alipay app_id mismatch")
	}
	if authAppID != "" && authAppID != expectedAppID {
		return errors.New("Alipay auth_app_id mismatch")
	}
	expectedSellerID := strings.TrimSpace(setting.AlipaySellerID)
	if expectedSellerID == "" {
		return errors.New("Alipay seller_id is not configured")
	}
	if strings.TrimSpace(params["seller_id"]) != expectedSellerID {
		return errors.New("Alipay seller_id mismatch")
	}
	return nil
}

func alipayPaymentReceipt(params map[string]string, payload string) (model.PaymentReceipt, error) {
	amountText := strings.TrimSpace(params["total_amount"])
	amount, err := decimal.NewFromString(amountText)
	if err != nil || !amount.IsPositive() {
		return model.PaymentReceipt{}, errors.New("Alipay total_amount is invalid")
	}
	currency := strings.ToUpper(strings.TrimSpace(params["currency"]))
	if currency == "" {
		// Domestic Alipay page-pay settles in CNY and does not always include a
		// currency field in asynchronous notifications.
		currency = "CNY"
	}
	if currency != "CNY" {
		return model.PaymentReceipt{}, errors.New("Alipay currency must be CNY")
	}
	providerTradeNo := strings.TrimSpace(params["trade_no"])
	if providerTradeNo == "" {
		return model.PaymentReceipt{}, errors.New("Alipay trade_no is missing")
	}
	return model.PaymentReceipt{
		Amount: amount, Currency: currency, ProviderTradeNo: providerTradeNo, ProviderPayload: payload,
	}, nil
}

func RequestAlipayPay(c *gin.Context) {
	if !isAlipayTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "支付宝支付尚未启用,请管理员先在 iflink 后台配置 AlipayAppID / 私钥 / 公钥",
		})
		return
	}
	id := c.GetInt("id")
	lock := getTopUpLock(id)
	if !lock.TryLock() {
		common.ApiErrorT(c, "Top-up is processing, please try again later")
		return
	}
	defer lock.Unlock()

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "read body error"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var req TopUpAlipayPayRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	// Frozen V1 wallet SKUs are CNY ¥10/¥30/¥50, face value 1:1, permanent.
	// Native Alipay must not inherit the legacy USD display conversion or accept
	// a caller-controlled custom amount.
	if !isFrozenV1WalletAmount(req.Amount) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "V1 钱包仅支持 ¥10、¥30、¥50 三档充值",
		})
		return
	}
	payMoney, _ := decimal.NewFromFloat(req.Amount).Round(2).Float64()
	if payMoney < setting.AlipayMinTopUp {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("最低充值金额 %.2f 元", setting.AlipayMinTopUp)})
		return
	}

	tradeNo := "TU-ALI-" + randstr.String(8) + "-" + strconv.FormatInt(time.Now().Unix(), 10)

	// Amount 存「展示金额」、Money 存「实付人民币」,与 epay 的存储口径一致。
	// 入账额度 = Amount × QuotaPerUnit(见 AlipayNotify),因此这里不能把 quota 塞进 Amount。
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          int64(req.Amount),
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodAlipay,
		PaymentProvider: model.PaymentProviderAlipay,
		CreateTime:      time.Now().Unix(),
		CompleteTime:    0,
		Status:          common.TopUpStatusPending,
		Currency:        "CNY",
	}
	if err := topUp.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建充值订单失败"})
		return
	}

	callBack := service.GetCallbackAddress()
	notifyURL := callBack + "/api/topup/alipay/notify"
	returnURL := callBack + "/api/topup/alipay/return?trade_no=" + tradeNo

	// biz_content: 支付宝"电脑网站支付"业务参数。
	// total_amount 必须是「实付人民币」(payMoney),而不是展示金额。
	biz := map[string]string{
		"out_trade_no": tradeNo,
		"product_code": "FAST_INSTANT_TRADE_PAY",
		"total_amount": fmt.Sprintf("%.2f", payMoney),
		"subject":      fmt.Sprintf("IF.Link 钱包充值 - %s", user.Username),
	}
	bizJSON, _ := json.Marshal(biz)
	form, err := alipayBuildSignedForm("alipay.trade.page.pay", string(bizJSON), notifyURL, returnURL, nil)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "签名失败: " + err.Error()})
		return
	}

	payURL := alipayGateway() + "?" + form

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"pay_url":   payURL,
			"trade_no":  tradeNo,
			"amount":    req.Amount, // 展示金额
			"pay_money": payMoney,   // 实付人民币
			"is_mobile": false,      // MVP 暂用 trade.page.pay(PC); 后续可加 trade.wap.pay(手机)
		},
	})
}

// AlipayNotify 接收支付宝异步通知
// 文档:https://opendocs.alipay.com/support/FAQ/api/notification
func AlipayNotify(c *gin.Context) {
	if !isAlipayWebhookEnabled() {
		// 直接返回 success 让支付宝停止重试
		c.String(http.StatusOK, "success")
		return
	}
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Alipay notify 读取请求体失败 error=%q", err.Error()))
		c.String(http.StatusOK, "fail")
		return
	}
	params, ok := alipayVerifyNotifyParams(string(bodyBytes))
	if !ok {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Alipay notify 验签失败 raw=%q", string(bodyBytes)))
		c.String(http.StatusOK, "fail")
		return
	}
	tradeNo := params["out_trade_no"]
	if tradeNo == "" {
		c.String(http.StatusOK, "fail")
		return
	}
	if params["trade_status"] != "TRADE_SUCCESS" && params["trade_status"] != "TRADE_FINISHED" {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Alipay notify 非成功状态 trade_no=%s status=%s", tradeNo, params["trade_status"]))
		c.String(http.StatusOK, "success") // 仍返回 success 避免重复通知
		return
	}
	if err := validateAlipayNotifyIdentity(params); err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Alipay notify 商户身份不匹配 trade_no=%s error=%q", tradeNo, err.Error()))
		c.String(http.StatusOK, "fail")
		return
	}
	receipt, err := alipayPaymentReceipt(params, string(bodyBytes))
	if err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Alipay notify 支付凭据无效 trade_no=%s error=%q", tradeNo, err.Error()))
		c.String(http.StatusOK, "fail")
		return
	}

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	// Try complete subscription order first
	if err := model.CompleteSubscriptionOrderWithReceipt(tradeNo, model.PaymentProviderAlipay, model.PaymentMethodAlipay, receipt); err == nil {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Alipay 订阅订单处理成功 trade_no=%s", tradeNo))
		c.String(http.StatusOK, "success")
		return
	} else if err != nil && err != model.ErrSubscriptionOrderNotFound {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Alipay 订阅订单处理失败 trade_no=%s error=%q", tradeNo, err.Error()))
		c.String(http.StatusInternalServerError, "fail")
		return
	}

	// Wallet status and quota credit commit in one database transaction.
	topUp, quotaToAdd, credited, err := model.CompleteTopUpPayment(tradeNo, model.PaymentProviderAlipay, receipt)
	if err != nil {
		if errors.Is(err, model.ErrTopUpNotFound) {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Alipay 充值订单不存在 trade_no=%s", tradeNo))
			c.String(http.StatusOK, "fail")
			return
		}
		logger.LogError(c.Request.Context(), fmt.Sprintf("Alipay 充值原子入账失败 trade_no=%s error=%q", tradeNo, err.Error()))
		c.String(http.StatusInternalServerError, "fail")
		return
	}
	if credited {
		model.RecordTopupLog(topUp.UserId,
			common.NewMessage("{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"provider": "Alipay", "quota": logger.FormatQuota(quotaToAdd), "amount": fmt.Sprintf("%.2f", topUp.Money)}),
			c.ClientIP(), topUp.PaymentMethod, model.PaymentProviderAlipay)
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Alipay 充值成功 trade_no=%s user_id=%d quota=%d money=%.2f", tradeNo, topUp.UserId, quotaToAdd, topUp.Money))
	c.String(http.StatusOK, "success")
}

// AlipayReturn 支付宝同步跳转(GET)
// 用户付款成功后浏览器跳转到此页,仅用于展示结果(实际入账以 notify 为准)
func AlipayReturn(c *gin.Context) {
	tradeNo := c.Query("trade_no")
	c.Redirect(http.StatusFound, paymentReturnPath("/wallet?alipay_return="+url.QueryEscape(tradeNo)))
}

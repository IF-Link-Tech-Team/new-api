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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/thanhpk/randstr"
)

type TopUpAlipayPayRequest struct {
	Amount    int    `json:"amount"`     // 元
	TopUpCode string `json:"top_up_code"` // 兼容老 topup 字段
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
		common.ApiErrorI18n(c, i18n.MsgUserTopUpProcessing)
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
	if req.Amount < int(setting.AlipayMinTopUp) {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("最低充值金额 %d 元", int(setting.AlipayMinTopUp)),
		})
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "用户不存在"})
		return
	}

	tradeNo := "TU-ALI-" + randstr.String(8) + "-" + strconv.FormatInt(time.Now().Unix(), 10)

	// amount 元 -> quota (1元 = 500000 quota)
	quota := int64(req.Amount) * 500000

	topUp := &model.TopUp{
		UserId:        id,
		Amount:        quota,
		Money:         float64(req.Amount),
		TradeNo:       tradeNo,
		PaymentMethod: model.PaymentMethodAlipay,
		CreateTime:    time.Now().Unix(),
		CompleteTime:  0,
		Status:        common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建充值订单失败"})
		return
	}

	callBack := service.GetCallbackAddress()
	notifyURL := callBack + "/api/topup/alipay/notify"
	returnURL := callBack + "/api/topup/alipay/return?trade_no=" + tradeNo

	// biz_content: 支付宝"电脑网站支付"业务参数
	biz := map[string]string{
		"out_trade_no": tradeNo,
		"product_code": "FAST_INSTANT_TRADE_PAY",
		"total_amount":  fmt.Sprintf("%.2f", float64(req.Amount)),
		"subject":       fmt.Sprintf("IF.Link 钱包充值 - %s", user.Username),
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
			"amount":    req.Amount,
			"is_mobile": false, // MVP 暂用 trade.page.pay(PC); 后续可加 trade.wap.pay(手机)
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

	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	// Try complete subscription order first
	if err := model.CompleteSubscriptionOrder(tradeNo, string(bodyBytes), model.PaymentProviderAlipay, params["trade_status"]); err == nil {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Alipay 订阅订单处理成功 trade_no=%s", tradeNo))
		c.String(http.StatusOK, "success")
		return
	} else if err != nil && err != model.ErrSubscriptionOrderNotFound {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Alipay 订阅订单处理失败 trade_no=%s error=%q", tradeNo, err.Error()))
		c.String(http.StatusInternalServerError, "fail")
		return
	}

	// 通用 topup 路径
	topUp := model.GetTopUpByTradeNo(tradeNo)
	if topUp == nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Alipay 充值订单不存在 trade_no=%s", tradeNo))
		c.String(http.StatusOK, "fail")
		return
	}
	if topUp.Status != common.TopUpStatusPending {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Alipay 充值订单已处理过,忽略重复通知 trade_no=%s status=%s", tradeNo, topUp.Status))
		c.String(http.StatusOK, "success")
		return
	}

	// 校验金额(防止伪造)
	expectedMoney := topUp.Money
	paidMoney, _ := strconv.ParseFloat(params["total_amount"], 64)
	if paidMoney+0.001 < expectedMoney {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Alipay 通知金额不匹配 trade_no=%s expected=%.2f paid=%.2f", tradeNo, expectedMoney, paidMoney))
		c.String(http.StatusOK, "fail")
		return
	}

	// 完成 topup
	if err := model.Recharge(tradeNo, strconv.Itoa(topUp.UserId), c.ClientIP()); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Alipay 充值处理失败 trade_no=%s error=%q", tradeNo, err.Error()))
		c.String(http.StatusInternalServerError, "fail")
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Alipay 充值成功 trade_no=%s amount=%.2f quota=%d", tradeNo, paidMoney, topUp.Amount))
	c.String(http.StatusOK, "success")
}

// AlipayReturn 支付宝同步跳转(GET)
// 用户付款成功后浏览器跳转到此页,仅用于展示结果(实际入账以 notify 为准)
func AlipayReturn(c *gin.Context) {
	tradeNo := c.Query("trade_no")
	c.Redirect(http.StatusFound, "/console/topup?alipay_return="+url.QueryEscape(tradeNo))
}
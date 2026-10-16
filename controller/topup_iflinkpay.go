package controller

// IFLinkPay 临时跳转第三方支付页集成
//
// 链路:
//   POST /api/user/iflinkpay          -> 创建 top_up(pending) -> 返回签名跳转 URL
//   用户在第三方支付页完成付款
//   POST /api/topup/iflinkpay/notify  <- 对方异步回调(JSON, 匿名可达, 失败按 1/5/15/30min 重试)
//
// 签名算法(双方已定稿): sign = md5(uid + amount + order + ts + secret),
// 各值直接拼接无分隔符、转字符串、UTF-8、32 位小写 hex。amount 单位为分。
// secret 只存在于服务端配置(payment_setting.iflink_pay_secret),不下发前端。

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// ifLinkPayMaxTopUpAmount 单次充值上限(元)
const ifLinkPayMaxTopUpAmount = 10000.0

// ifLinkPayNotifyTimestampToleranceSeconds 回调时间戳容忍窗口(秒)
const ifLinkPayNotifyTimestampToleranceSeconds = 600

// ifLinkPaySign 计算 IFLinkPay 签名: md5(uid + amount(分) + order + ts + secret), 32 位小写 hex
func ifLinkPaySign(uid string, amountFen int64, order string, ts int64, secret string) string {
	raw := uid + strconv.FormatInt(amountFen, 10) + order + strconv.FormatInt(ts, 10) + secret
	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func newIFLinkPayTopUpTradeNo() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "TU-IFP-" + hex.EncodeToString(random), nil
}

// New wallet orders use the opaque order reference as IFLinkPay's external
// uid. Numeric uids remain valid only for callbacks from legacy pending orders
// created before the privacy migration.
func ifLinkPayWalletUIDMatches(topUp *model.TopUp, uid string) bool {
	if topUp == nil {
		return false
	}
	return uid == topUp.TradeNo || uid == strconv.Itoa(topUp.UserId)
}

type TopUpIFLinkPayRequest struct {
	Amount float64 `json:"amount"` // 元, 支持任意金额(最多两位小数)
}

func RequestIFLinkPay(c *gin.Context) {
	if !isIFLinkPayTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "IFLinkPay 支付尚未启用,请管理员先在后台配置 payment_setting.iflink_pay_secret",
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
	var req TopUpIFLinkPayRequest
	if err := common.Unmarshal(bodyBytes, &req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "参数错误"})
		return
	}

	payMoneyDec := decimal.NewFromFloat(req.Amount)
	if !payMoneyDec.IsPositive() || !payMoneyDec.Equal(payMoneyDec.Round(2)) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值金额必须大于 0 且最多两位小数"})
		return
	}
	if payMoneyDec.GreaterThan(decimal.NewFromFloat(ifLinkPayMaxTopUpAmount)) {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("单次充值金额不能超过 %.0f 元", ifLinkPayMaxTopUpAmount)})
		return
	}
	payMoney, _ := payMoneyDec.Round(2).Float64()
	amountFen := int64(math.Round(payMoney * 100))

	tradeNo, err := newIFLinkPayTopUpTradeNo()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("IFLinkPay 充值订单号生成失败 error=%q", err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建充值订单失败"})
		return
	}

	// Money 保存精确到分的实付人民币，IFLinkPay 按 Money × QuotaPerUnit 入账。
	// Amount 保留历史整数展示口径，不参与 IFLinkPay 额度计算。
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          int64(math.Round(payMoney)),
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodIFLinkPay,
		PaymentProvider: model.PaymentProviderIFLinkPay,
		CreateTime:      time.Now().Unix(),
		CompleteTime:    0,
		Status:          common.TopUpStatusPending,
		Currency:        "CNY",
	}
	quota, quotaErr := topUp.WalletQuota()
	if quotaErr != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "充值金额超出可入账额度，请减少单次充值金额"})
		return
	}
	if err := model.ValidateTopUpQuotaCapacity(id, quota); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("IFLinkPay 创建充值订单失败 user_id=%d trade_no=%s amount=%.2f error=%q", id, tradeNo, payMoney, err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "创建充值订单失败"})
		return
	}

	secret := strings.TrimSpace(operation_setting.GetIFLinkPaySecret())
	ts := time.Now().Unix()
	// The provider contract calls this field uid, but it is an opaque external
	// transaction reference. Never expose the local database user ID.
	externalUID := tradeNo
	returnURL := paymentReturnPath("/wallet")
	sign := ifLinkPaySign(externalUID, amountFen, tradeNo, ts, secret)
	payURL := fmt.Sprintf("%s?uid=%s&amount=%d&order=%s&ts=%d&sign=%s&return=%s",
		operation_setting.GetIFLinkPayBaseURL(),
		url.QueryEscape(externalUID),
		amountFen,
		url.QueryEscape(tradeNo),
		ts,
		sign,
		url.QueryEscape(returnURL),
	)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"pay_url":   payURL,
			"trade_no":  tradeNo,
			"amount":    req.Amount, // 展示金额
			"pay_money": payMoney,   // 实付人民币
		},
	})
}

type IFLinkPayNotifyRequest struct {
	UID    string `json:"uid"`
	Amount int64  `json:"amount"` // 分
	Order  string `json:"order"`
	TS     int64  `json:"ts"`
	Sign   string `json:"sign"`
}

func ifLinkPayNotifyReply(c *gin.Context, code int) {
	c.JSON(http.StatusOK, gin.H{"code": code})
}

// IFLinkPayNotify 接收 IFLinkPay 异步回调。必须幂等:重复回调/已入账订单返回 code:0,
// 任何失败返回非 0 code(对方会按 1/5/15/30min 重试)。
func IFLinkPayNotify(c *gin.Context) {
	ctx := c.Request.Context()
	secret := strings.TrimSpace(operation_setting.GetIFLinkPaySecret())
	if secret == "" {
		// 未配置 secret 时无法验签,也不可能存在该渠道订单,直接确认避免对方无限重试
		ifLinkPayNotifyReply(c, 0)
		return
	}
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("IFLinkPay notify 读取请求体失败 error=%q", err.Error()))
		ifLinkPayNotifyReply(c, 1)
		return
	}
	var req IFLinkPayNotifyRequest
	if err := common.Unmarshal(bodyBytes, &req); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay notify 参数解析失败 raw=%q", string(bodyBytes)))
		ifLinkPayNotifyReply(c, 1)
		return
	}
	if req.UID == "" || req.Order == "" || req.Sign == "" || req.Amount <= 0 || req.TS <= 0 {
		logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay notify 参数缺失 order=%s uid=%s", req.Order, req.UID))
		ifLinkPayNotifyReply(c, 1)
		return
	}
	now := time.Now().Unix()
	if req.TS < now-ifLinkPayNotifyTimestampToleranceSeconds || req.TS > now+ifLinkPayNotifyTimestampToleranceSeconds {
		logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay notify 时间戳超出容忍窗口 order=%s ts=%d now=%d", req.Order, req.TS, now))
		ifLinkPayNotifyReply(c, 1)
		return
	}
	expectedSign := ifLinkPaySign(req.UID, req.Amount, req.Order, req.TS, secret)
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(strings.TrimSpace(req.Sign))), []byte(expectedSign)) != 1 {
		logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay notify 验签失败 order=%s uid=%s", req.Order, req.UID))
		ifLinkPayNotifyReply(c, 1)
		return
	}

	LockOrder(req.Order)
	defer UnlockOrder(req.Order)

	// Subscription purchases share this signed callback endpoint. Detect them
	// first so a valid subscription callback never falls through to wallet
	// crediting.
	if order := model.GetSubscriptionOrderByTradeNo(req.Order); order != nil {
		if order.PaymentProvider != model.PaymentProviderIFLinkPay {
			logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay 订阅订单渠道不匹配 order=%s provider=%s", req.Order, order.PaymentProvider))
			ifLinkPayNotifyReply(c, 1)
			return
		}
		if req.UID != req.Order {
			logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay 订阅订单外部标识不匹配 order=%s", req.Order))
			ifLinkPayNotifyReply(c, 1)
			return
		}
		expectedFen := int64(math.Round(order.Money * 100))
		if req.Amount != expectedFen {
			logger.LogError(ctx, fmt.Sprintf("IFLinkPay 订阅订单金额不匹配 order=%s expected_fen=%d notify_fen=%d", req.Order, expectedFen, req.Amount))
			ifLinkPayNotifyReply(c, 1)
			return
		}
		receipt := model.PaymentReceipt{
			Amount:          decimal.NewFromInt(req.Amount).Div(decimal.NewFromInt(100)),
			Currency:        "CNY",
			ProviderTradeNo: req.Order,
			ProviderPayload: string(bodyBytes),
		}
		if err := model.CompleteSubscriptionOrderWithReceipt(req.Order, model.PaymentProviderIFLinkPay, model.PaymentMethodIFLinkPay, receipt); err != nil {
			logger.LogError(ctx, fmt.Sprintf("IFLinkPay 订阅权益发放失败 order=%s error=%q", req.Order, err.Error()))
			ifLinkPayNotifyReply(c, 1)
			return
		}
		logger.LogInfo(ctx, fmt.Sprintf("IFLinkPay 订阅支付完成 order=%s user_id=%d amount=%.2f", req.Order, order.UserId, order.Money))
		ifLinkPayNotifyReply(c, 0)
		return
	}

	topUp := model.GetTopUpByTradeNo(req.Order)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderIFLinkPay {
		logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay 充值订单不存在或渠道不匹配 order=%s", req.Order))
		ifLinkPayNotifyReply(c, 1)
		return
	}
	if !ifLinkPayWalletUIDMatches(topUp, req.UID) {
		logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay 订单用户不匹配 order=%s order_uid=%d notify_uid=%s", req.Order, topUp.UserId, req.UID))
		ifLinkPayNotifyReply(c, 1)
		return
	}
	expectedFen := int64(math.Round(topUp.Money * 100))
	if req.Amount != expectedFen {
		logger.LogError(ctx, fmt.Sprintf("IFLinkPay 订单金额不匹配,拒绝入账 order=%s expected_fen=%d notify_fen=%d", req.Order, expectedFen, req.Amount))
		ifLinkPayNotifyReply(c, 1)
		return
	}

	// 对方协议没有独立的支付流水号,以我方订单号充当 ProviderTradeNo(必填)。
	receipt := model.PaymentReceipt{
		Amount:          decimal.NewFromInt(req.Amount).Div(decimal.NewFromInt(100)),
		Currency:        "CNY",
		ProviderTradeNo: req.Order,
		ProviderPayload: string(bodyBytes),
	}
	completed, quotaToAdd, credited, err := model.CompleteTopUpPayment(req.Order, model.PaymentProviderIFLinkPay, receipt)
	if err != nil {
		if errors.Is(err, model.ErrTopUpNotFound) {
			logger.LogWarn(ctx, fmt.Sprintf("IFLinkPay 充值订单不存在 order=%s", req.Order))
			ifLinkPayNotifyReply(c, 1)
			return
		}
		logger.LogError(ctx, fmt.Sprintf("IFLinkPay 充值原子入账失败 order=%s error=%q", req.Order, err.Error()))
		ifLinkPayNotifyReply(c, 1)
		return
	}
	if credited {
		model.RecordTopupLog(completed.UserId,
			common.NewMessage("{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}", map[string]any{"provider": "IFLinkPay", "quota": logger.FormatQuota(quotaToAdd), "amount": fmt.Sprintf("%.2f", completed.Money)}),
			c.ClientIP(), completed.PaymentMethod, model.PaymentProviderIFLinkPay)
	}

	logger.LogInfo(ctx, fmt.Sprintf("IFLinkPay 充值成功 order=%s user_id=%d quota=%d money=%.2f credited=%t", req.Order, completed.UserId, quotaToAdd, completed.Money, credited))
	ifLinkPayNotifyReply(c, 0)
}

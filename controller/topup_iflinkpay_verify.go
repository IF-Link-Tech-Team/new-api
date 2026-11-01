package controller

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

// IFLinkPay verified settlement.
//
// The legacy integration credited an order as soon as it saw the md5 signature
// that also travelled to the payer inside the payment link, so a payer could
// acknowledge an unpaid order. Here the backend asks the provider for the order's
// server-side verified state and settles only on an explicit PAID result whose
// order number and amount match the immutable local order.
//
// Nothing in this file trusts a client-supplied amount, status, or signature.
// The only thing the browser can do is ask the backend to re-check.

// ifLinkPayMaxTopUpAmount caps one wallet top-up in yuan.
const ifLinkPayMaxTopUpAmount = 10000.0

// ifLinkPayCashierURL builds the provider cashier URL for an order.
//
// The signature authenticates the CREATION of a payment request only; it is
// never accepted as evidence that money moved.
func ifLinkPayCashierURL(tradeNo string, amountFen int64, returnPath string, linkSecret string) string {
	ts := time.Now().Unix()
	sign := service.IFLinkPayLinkSign(tradeNo, amountFen, tradeNo, ts, linkSecret)
	return operation_setting.GetIFLinkPayBaseURL() +
		"?uid=" + tradeNo +
		"&amount=" + strconv.FormatInt(amountFen, 10) +
		"&order=" + tradeNo +
		"&ts=" + strconv.FormatInt(ts, 10) +
		"&sign=" + sign +
		"&return=" + url.QueryEscape(returnPath)
}

// ifLinkPayVerifyRequest identifies the order the caller wants re-checked.
type ifLinkPayVerifyRequest struct {
	TradeNo string `json:"trade_no"`
}

// ifLinkPayVerifyUnavailable reports that verification is switched off or not
// fully configured. Callers always fail closed in that case.
func ifLinkPayVerifyUnavailable(c *gin.Context) {
	c.Header("Retry-After", "300")
	common.ApiErrorStatus(c, http.StatusServiceUnavailable,
		common.NewMessage("Online payment is not enabled. Please contact the administrator."))
}

// ifLinkPayLinkSecretOrFail returns the payment-request signing secret, or
// reports the unavailable response and returns ok=false.
func ifLinkPayLinkSecretOrFail(c *gin.Context) (string, bool) {
	if !operation_setting.IFLinkPayV2QueryReady() {
		ifLinkPayVerifyUnavailable(c)
		return "", false
	}
	secret := strings.TrimSpace(operation_setting.GetIFLinkPayLinkSecret())
	if secret == "" {
		ifLinkPayVerifyUnavailable(c)
		return "", false
	}
	return secret, true
}

// RequestIFLinkPayVerified creates a wallet top-up order and returns the cashier
// URL. The order is immutable: amount, currency and ownership live in the
// database row and the verifier compares the provider's answer against it.
func RequestIFLinkPayVerified(c *gin.Context) {
	linkSecret, ok := ifLinkPayLinkSecretOrFail(c)
	if !ok {
		return
	}

	id := c.GetInt("id")
	lock := getTopUpLock(id)
	if !lock.TryLock() {
		common.ApiErrorT(c, "Top-up is processing, please try again later")
		return
	}
	defer lock.Unlock()

	var req struct {
		Amount float64 `json:"amount"`
	}
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, common.NewMessage("Invalid request"))
		return
	}

	payMoneyDec := decimal.NewFromFloat(req.Amount)
	if !payMoneyDec.IsPositive() || !payMoneyDec.Equal(payMoneyDec.Round(2)) {
		common.ApiError(c, common.NewMessage("Top-up amount must be positive with at most two decimals"))
		return
	}
	if payMoneyDec.GreaterThan(decimal.NewFromFloat(ifLinkPayMaxTopUpAmount)) {
		common.ApiError(c, common.NewMessage("Top-up amount exceeds the single-order limit"))
		return
	}
	payMoney, _ := payMoneyDec.Round(2).Float64()
	amountFen := int64(math.Round(payMoney * 100))

	tradeNo := "TU-IFP-" + randstr.String(16)
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          int64(math.Round(payMoney)),
		Money:           payMoney,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodIFLinkPay,
		PaymentProvider: model.PaymentProviderIFLinkPay,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
		Currency:        "CNY",
	}
	quota, quotaErr := topUp.WalletQuota()
	if quotaErr != nil {
		common.ApiError(c, common.NewMessage("Top-up amount exceeds the creditable quota"))
		return
	}
	if err := model.ValidateTopUpQuotaCapacity(id, quota); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), "IFLinkPay wallet order creation failed")
		common.ApiError(c, common.NewMessage("Failed to create the top-up order"))
		return
	}

	logger.LogInfo(c.Request.Context(), "IFLinkPay wallet order created trade_no="+tradeNo)
	common.ApiSuccess(c, gin.H{
		"pay_url":   ifLinkPayCashierURL(tradeNo, amountFen, paymentReturnPath("/wallet"), linkSecret),
		"trade_no":  tradeNo,
		"amount":    req.Amount,
		"pay_money": payMoney,
	})
}

// verifyIFLinkPayOrder asks the provider whether this order was actually paid.
//
// It returns (receipt, true) only when the provider confirms PAID and the order
// number and amount match. Any other outcome returns false with an error, so the
// caller leaves the order unconfirmed and no entitlement is issued.
func verifyIFLinkPayOrder(order *model.TopUp) (model.PaymentReceipt, bool, error) {
	if order == nil {
		return model.PaymentReceipt{}, false, errors.New("order is required")
	}
	linkSecret := strings.TrimSpace(operation_setting.GetIFLinkPayLinkSecret())
	if linkSecret == "" {
		return model.PaymentReceipt{}, false, errors.New("payment link secret is not configured")
	}

	expectedFen := int64(order.Money*100 + 0.5)
	if expectedFen <= 0 {
		return model.PaymentReceipt{}, false, errors.New("order amount is not payable")
	}

	verifier := service.NewIFLinkPayVerifier(operation_setting.GetIFLinkPayV2APIBaseURL())
	// OpenSession is idempotent per order: the provider's own cashier page calls
	// it again on every page load. The session is used immediately and never
	// persisted or sent to the browser.
	session, _, err := verifier.OpenSession(order.TradeNo, expectedFen, "", linkSecret)
	if err != nil {
		return model.PaymentReceipt{}, false, err
	}
	payment, err := verifier.VerifyPaid(session, order.TradeNo, expectedFen)
	if err != nil {
		return model.PaymentReceipt{}, false, err
	}

	return model.PaymentReceipt{
		Amount:          decimal.NewFromInt(payment.Amount).Div(decimal.NewFromInt(100)),
		Currency:        strings.ToUpper(strings.TrimSpace(order.Currency)),
		ProviderTradeNo: ifLinkPayReceiptTradeNo(payment, order.TradeNo),
		ProviderPayload: "",
	}, true, nil
}

// ifLinkPayReceiptTradeNo returns the highest-entropy identifier the provider
// gave us for this payment. When the provider only tracks our order number that
// number is the single-use key.
func ifLinkPayReceiptTradeNo(payment service.IFLinkPayPayment, orderTradeNo string) string {
	if trimmed := strings.TrimSpace(payment.ChannelTradeNo); trimmed != "" {
		return trimmed
	}
	return orderTradeNo
}

// VerifyIFLinkPayPayment re-checks one of the caller's own orders against the
// provider and settles it when the provider confirms payment.
//
// The client can only trigger a query; it can never assert success.
func VerifyIFLinkPayPayment(c *gin.Context) {
	if !operation_setting.IFLinkPayV2QueryReady() {
		ifLinkPayVerifyUnavailable(c)
		return
	}

	var req ifLinkPayVerifyRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiError(c, common.NewMessage("Invalid request"))
		return
	}
	tradeNo := strings.TrimSpace(req.TradeNo)
	if tradeNo == "" {
		common.ApiError(c, common.NewMessage("Order number is required"))
		return
	}

	userID := c.GetInt("id")
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	order := model.GetTopUpByTradeNo(tradeNo)
	if order == nil || order.PaymentProvider != model.PaymentProviderIFLinkPay || order.UserId != userID {
		common.ApiError(c, common.NewMessage("Order not found"))
		return
	}
	if order.Status == common.TopUpStatusSuccess {
		// Already settled; report the confirmed state without crediting again.
		common.ApiSuccess(c, gin.H{"status": "success", "trade_no": order.TradeNo})
		return
	}
	if order.Status != common.TopUpStatusPending {
		common.ApiError(c, common.NewMessage("Order can no longer be confirmed"))
		return
	}

	receipt, verified, err := verifyIFLinkPayOrder(order)
	if err != nil || !verified {
		// Unknown, unpaid, mismatched or unreachable: stay unconfirmed. This is a
		// normal outcome while the payer has not finished, so it is not an error
		// response; the client should keep polling.
		logger.LogInfo(c.Request.Context(), "IFLinkPay order not confirmed yet trade_no="+tradeNo)
		common.ApiSuccess(c, gin.H{"status": "pending", "trade_no": tradeNo})
		return
	}

	completed, quotaToAdd, credited, err := model.CompleteTopUpPayment(
		tradeNo, model.PaymentProviderIFLinkPay, receipt)
	if err != nil {
		logger.LogError(c.Request.Context(), "IFLinkPay verified settlement failed trade_no="+tradeNo)
		common.ApiError(c, common.NewMessage("Payment confirmation failed, please retry"))
		return
	}
	if credited {
		model.RecordTopupLog(completed.UserId,
			common.NewMessage("{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}",
				map[string]any{
					"provider": "IFLinkPay",
					"quota":    logger.FormatQuota(quotaToAdd),
					"amount":   decimal.NewFromFloat(completed.Money).StringFixed(2),
				}),
			c.ClientIP(), completed.PaymentMethod, model.PaymentProviderIFLinkPay)
	}
	common.ApiSuccess(c, gin.H{"status": "success", "trade_no": completed.TradeNo})
}

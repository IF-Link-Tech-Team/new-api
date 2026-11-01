package controller

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

// IFLinkPay subscription (plan / traffic pack) purchases on the verified path.
//
// Same trust model as the wallet path in topup_iflinkpay_verify.go: the
// payment-link signature only starts a payment, and settlement happens only
// after the provider's own status endpoint reports PAID for this exact order
// number and amount. The client can trigger a re-check but can never assert
// that a payment succeeded.

const ifLinkPaySubscriptionTradeNoPrefix = "SUB-IFP-"

type subscriptionIFLinkPayPayRequest struct {
	PlanId int `json:"plan_id"`
}

// SubscriptionRequestIFLinkPayVerified creates a plan or traffic-pack order and
// returns the cashier URL. It is only reachable while the verified-payment
// switch is on and the query credentials are configured.
func SubscriptionRequestIFLinkPayVerified(c *gin.Context) {
	if !operation_setting.IFLinkPayV2QueryReady() {
		ifLinkPayVerifyUnavailable(c)
		return
	}
	linkSecret := strings.TrimSpace(operation_setting.GetIFLinkPayLinkSecret())
	if linkSecret == "" {
		ifLinkPayVerifyUnavailable(c)
		return
	}

	var req subscriptionIFLinkPayPayRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil || req.PlanId <= 0 {
		common.ApiError(c, common.NewMessage("Invalid request"))
		return
	}

	plan, err := model.GetSubscriptionPlanById(req.PlanId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !plan.Enabled {
		common.ApiError(c, common.NewMessage("Subscription plan is not enabled"))
		return
	}
	if plan.PriceAmount < 0.01 {
		common.ApiError(c, common.NewMessage("Subscription plan amount is too low"))
		return
	}

	userId := c.GetInt("id")
	tradeNo := ifLinkPaySubscriptionTradeNoPrefix + randstr.String(16)
	order, _, err := model.CreateSubscriptionOrder(userId, plan.Id, tradeNo,
		model.PaymentMethodIFLinkPay, model.PaymentProviderIFLinkPay, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	amountFen := int64(order.Money*100 + 0.5)
	if amountFen <= 0 {
		_ = model.ExpireSubscriptionOrder(tradeNo, model.PaymentProviderIFLinkPay)
		common.ApiError(c, common.NewMessage("Subscription order amount is invalid"))
		return
	}

	returnPath := paymentReturnPath("/plans")
	if plan.Category == model.SubscriptionCategoryPackage {
		returnPath = paymentReturnPath("/traffic-packs")
	}

	logger.LogInfo(c.Request.Context(), "IFLinkPay subscription order created trade_no="+tradeNo)
	common.ApiSuccess(c, gin.H{
		"pay_url":   ifLinkPayCashierURL(tradeNo, amountFen, returnPath, linkSecret),
		"trade_no":  tradeNo,
		"pay_money": order.Money,
	})
}

// verifyIFLinkPaySubscriptionOrder asks the provider whether a subscription
// order was paid. It returns a receipt only on an explicit PAID match.
func verifyIFLinkPaySubscriptionOrder(order *model.SubscriptionOrder) (model.PaymentReceipt, bool, error) {
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
		Currency:        "CNY",
		ProviderTradeNo: ifLinkPayReceiptTradeNo(payment, order.TradeNo),
	}, true, nil
}

// VerifyIFLinkPaySubscriptionPayment re-checks one of the caller's own
// subscription orders and settles it when the provider confirms payment.
//
// The client supplies only the order number. Amount and ownership come from the
// immutable database order, and success comes only from the provider.
func VerifyIFLinkPaySubscriptionPayment(c *gin.Context) {
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

	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil || order.PaymentProvider != model.PaymentProviderIFLinkPay || order.UserId != userID {
		common.ApiError(c, common.NewMessage("Order not found"))
		return
	}
	if order.Status == common.TopUpStatusSuccess {
		// Already settled; report the confirmed state without issuing again.
		common.ApiSuccess(c, gin.H{"status": "success", "trade_no": order.TradeNo})
		return
	}
	if order.Status != common.TopUpStatusPending {
		common.ApiError(c, common.NewMessage("Order can no longer be confirmed"))
		return
	}

	receipt, verified, err := verifyIFLinkPaySubscriptionOrder(order)
	if err != nil || !verified {
		// Unpaid, unknown, mismatched or unreachable: stay unconfirmed. This is a
		// normal intermediate state while the payer has not finished.
		logger.LogInfo(c.Request.Context(), "IFLinkPay subscription not confirmed yet trade_no="+tradeNo)
		common.ApiSuccess(c, gin.H{"status": "pending", "trade_no": tradeNo})
		return
	}

	if err := model.CompleteSubscriptionOrderWithReceipt(tradeNo,
		model.PaymentProviderIFLinkPay, model.PaymentMethodIFLinkPay, receipt); err != nil {
		logger.LogError(c.Request.Context(), "IFLinkPay subscription settlement failed trade_no="+tradeNo)
		common.ApiError(c, common.NewMessage("Payment confirmation failed, please retry"))
		return
	}

	logger.LogInfo(c.Request.Context(), "IFLinkPay subscription settled trade_no="+tradeNo)
	common.ApiSuccess(c, gin.H{"status": "success", "trade_no": tradeNo})
}

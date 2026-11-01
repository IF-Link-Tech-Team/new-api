package controller

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
)

// IFLinkPay reconciliation.
//
// Settlement must not depend on the payer staying on the return page. The
// provider's own cashier page promises "closing the page does not affect
// delivery", and the legacy integration achieved that with a callback. Since no
// inbound callback can be authenticated yet, this reconciler plays that role: it
// periodically asks the provider about recent pending orders and settles the ones
// the provider confirms as PAID.
//
// It reuses exactly the same verification gate as the on-demand endpoint, so it
// cannot credit anything the provider has not confirmed, and it never runs when
// the verified-payment switch is off.

const (
	// ifLinkPayReconcileInterval is how often pending orders are re-checked.
	ifLinkPayReconcileInterval = 20 * time.Second
	// ifLinkPayReconcileLookback bounds which orders are re-checked. Older orders
	// have passed the provider's payment window.
	ifLinkPayReconcileLookback = 2 * time.Hour
	// ifLinkPayReconcileBatch caps provider calls per run per order type.
	ifLinkPayReconcileBatch = 10
)

// StartIFLinkPayReconciler runs the settled-credit reconciler in the background.
func StartIFLinkPayReconciler() {
	go func() {
		ticker := time.NewTicker(ifLinkPayReconcileInterval)
		defer ticker.Stop()
		for range ticker.C {
			reconcileIFLinkPayPending()
		}
	}()
}

// reconcileIFLinkPayPending performs one reconciliation pass over both order
// kinds. Failures are logged and never propagate: an unreachable provider or an
// unpaid order simply leaves the order pending for the next pass.
func reconcileIFLinkPayPending() {
	if !operation_setting.IFLinkPayV2QueryReady() {
		return
	}
	since := time.Now().Add(-ifLinkPayReconcileLookback).Unix()

	topUps, err := model.ListPendingIFLinkPayTopUps(since, ifLinkPayReconcileBatch)
	if err != nil {
		logger.LogError(context.Background(), "IFLinkPay reconciler: cannot list pending top-ups")
	} else {
		for i := range topUps {
			reconcileIFLinkPayTopUp(topUps[i].TradeNo)
		}
	}

	orders, err := model.ListPendingIFLinkPaySubscriptionOrders(since, ifLinkPayReconcileBatch)
	if err != nil {
		logger.LogError(context.Background(), "IFLinkPay reconciler: cannot list pending subscription orders")
		return
	}
	for i := range orders {
		reconcileIFLinkPaySubscriptionOrder(orders[i].TradeNo)
	}
}

// reconcileIFLinkPayTopUp settles one wallet order if the provider confirms it.
func reconcileIFLinkPayTopUp(tradeNo string) {
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	// Re-read under the lock: the payer's own request may have settled it first.
	order := model.GetTopUpByTradeNo(tradeNo)
	if order == nil || order.Status != common.TopUpStatusPending ||
		order.PaymentProvider != model.PaymentProviderIFLinkPay {
		return
	}

	receipt, verified, err := verifyIFLinkPayOrder(order)
	if err != nil || !verified {
		return
	}
	completed, quotaToAdd, credited, err := model.CompleteTopUpPayment(
		tradeNo, model.PaymentProviderIFLinkPay, receipt)
	if err != nil {
		logger.LogError(context.Background(), "IFLinkPay reconciler: wallet settlement failed trade_no="+tradeNo)
		return
	}
	if !credited {
		return
	}
	model.RecordTopupLog(completed.UserId,
		common.NewMessage("{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}",
			map[string]any{
				"provider": "IFLinkPay",
				"quota":    logger.FormatQuota(quotaToAdd),
				"amount":   decimal.NewFromFloat(completed.Money).StringFixed(2),
			}),
		"", completed.PaymentMethod, model.PaymentProviderIFLinkPay)
	logger.LogInfo(context.Background(), "IFLinkPay reconciler: wallet order settled trade_no="+tradeNo)
}

// reconcileIFLinkPaySubscriptionOrder settles one subscription order if the
// provider confirms it.
func reconcileIFLinkPaySubscriptionOrder(tradeNo string) {
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil || order.Status != common.TopUpStatusPending ||
		order.PaymentProvider != model.PaymentProviderIFLinkPay {
		return
	}

	receipt, verified, err := verifyIFLinkPaySubscriptionOrder(order)
	if err != nil || !verified {
		return
	}
	if err := model.CompleteSubscriptionOrderWithReceipt(tradeNo,
		model.PaymentProviderIFLinkPay, model.PaymentMethodIFLinkPay, receipt); err != nil {
		logger.LogError(context.Background(), "IFLinkPay reconciler: subscription settlement failed trade_no="+tradeNo)
		return
	}
	logger.LogInfo(context.Background(), "IFLinkPay reconciler: subscription order settled trade_no="+tradeNo)
}

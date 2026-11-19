package controller

import (
	"context"
	"sync"
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
//
// Walking the window page by page matters more than the page size. Every click on
// "pay" leaves a pending row behind, and an order nobody pays stays pending
// forever. Re-reading only the oldest page would let a full page of abandoned
// orders hide every real payment behind it, until the lookback window dropped
// those rows and the payment was lost. The cursors below make consecutive passes
// cover the whole window: each order is checked once per pass, and a pass starts
// over from the oldest order after the window has been walked.

const (
	// ifLinkPayReconcileInterval is how often pending orders are re-checked.
	ifLinkPayReconcileInterval = 20 * time.Second
	// ifLinkPayReconcileLookback bounds which orders are re-checked. Older orders
	// have passed the provider's payment window.
	ifLinkPayReconcileLookback = 2 * time.Hour
	// ifLinkPayReconcileBatch caps provider calls per page per order type.
	ifLinkPayReconcileBatch = 10
)

// ifLinkPayCursor remembers where the previous pass stopped for one order type.
//
// It is process-local on purpose: a restarted process starts a fresh pass from
// the oldest order, which is safe because settlement is idempotent.
type ifLinkPayCursor struct {
	mu sync.Mutex
	at model.PendingOrderCursor
}

// current returns the position the next query should start after.
func (c *ifLinkPayCursor) current() model.PendingOrderCursor {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// advance records the page that was just processed. A short page means the
// window has been walked to its end, so the next pass starts over from the
// oldest order instead of stalling at the end of the window.
func (c *ifLinkPayCursor) advance(page []model.PendingOrderCursor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(page) < ifLinkPayReconcileBatch {
		c.at = model.PendingOrderCursor{}
		return
	}
	c.at = page[len(page)-1]
}

var (
	// ifLinkPayTopUpCursor walks pending wallet orders.
	ifLinkPayTopUpCursor ifLinkPayCursor
	// ifLinkPayOrderCursor walks pending subscription orders.
	ifLinkPayOrderCursor ifLinkPayCursor
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
//
// Each pass processes at most one page per order type and continues where the
// previous pass stopped, so a page full of abandoned orders cannot starve the
// orders behind it.
func reconcileIFLinkPayPending() {
	if !operation_setting.IFLinkPayV2QueryReady() {
		return
	}
	since := time.Now().Add(-ifLinkPayReconcileLookback).Unix()

	topUps, err := model.ListPendingIFLinkPayTopUps(since, ifLinkPayTopUpCursor.current(), ifLinkPayReconcileBatch)
	if err != nil {
		logger.LogError(context.Background(), "IFLinkPay reconciler: cannot list pending top-ups")
	} else if len(topUps) > 0 {
		for i := range topUps {
			reconcileIFLinkPayTopUp(topUps[i].TradeNo)
		}
		page := make([]model.PendingOrderCursor, 0, len(topUps))
		for i := range topUps {
			page = append(page, model.PendingOrderCursor{CreateTime: topUps[i].CreateTime, Id: topUps[i].Id})
		}
		ifLinkPayTopUpCursor.advance(page)
	}

	orders, err := model.ListPendingIFLinkPaySubscriptionOrders(since, ifLinkPayOrderCursor.current(), ifLinkPayReconcileBatch)
	if err != nil {
		logger.LogError(context.Background(), "IFLinkPay reconciler: cannot list pending subscription orders")
		return
	}
	if len(orders) == 0 {
		return
	}
	for i := range orders {
		reconcileIFLinkPaySubscriptionOrder(orders[i].TradeNo)
	}
	page := make([]model.PendingOrderCursor, 0, len(orders))
	for i := range orders {
		page = append(page, model.PendingOrderCursor{CreateTime: orders[i].CreateTime, Id: orders[i].Id})
	}
	ifLinkPayOrderCursor.advance(page)
}

// reconcileIFLinkPayTopUp settles one wallet order if the provider confirms it,
// and retires it when the provider says the payment can no longer happen.
//
// Retiring on the provider's own terminal answer is what keeps the window from
// filling up with orders nobody will pay. A local timeout would not be safe: the
// provider's payment link keeps working after we create the row, so an order
// marked expired on our clock could still be paid, and the payment would then be
// abandoned. Only EXPIRED/CLOSED/FAILED from the provider may retire an order.
func reconcileIFLinkPayTopUp(tradeNo string) {
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	// Re-read under the lock: the payer's own request may have settled it first.
	order := model.GetTopUpByTradeNo(tradeNo)
	if order == nil || order.Status != common.TopUpStatusPending ||
		order.PaymentProvider != model.PaymentProviderIFLinkPay {
		return
	}

	outcome := ifLinkPayOrderOutcomeFor(order.TradeNo, order.Money, order.Currency)
	if outcome.TerminalUnpaid {
		if err := model.UpdatePendingTopUpStatus(order.TradeNo,
			model.PaymentProviderIFLinkPay, common.TopUpStatusExpired); err != nil {
			logger.LogError(context.Background(), "IFLinkPay reconciler: wallet order retirement failed trade_no="+tradeNo)
			return
		}
		logger.LogInfo(context.Background(), "IFLinkPay reconciler: wallet order retired as expired trade_no="+tradeNo)
		return
	}
	if !outcome.Paid {
		return
	}

	completed, quotaToAdd, credited, err := model.CompleteTopUpPayment(
		tradeNo, model.PaymentProviderIFLinkPay, outcome.Receipt)
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
// provider confirms it, and retires it when the provider says the payment can no
// longer happen.
func reconcileIFLinkPaySubscriptionOrder(tradeNo string) {
	LockOrder(tradeNo)
	defer UnlockOrder(tradeNo)

	order := model.GetSubscriptionOrderByTradeNo(tradeNo)
	if order == nil || order.Status != common.TopUpStatusPending ||
		order.PaymentProvider != model.PaymentProviderIFLinkPay {
		return
	}

	// Subscription orders keep no currency column of their own; the subscription
	// verification path has always settled them as CNY.
	outcome := ifLinkPayOrderOutcomeFor(order.TradeNo, order.Money, "CNY")
	if outcome.TerminalUnpaid {
		if err := model.ExpireSubscriptionOrder(order.TradeNo, model.PaymentProviderIFLinkPay); err != nil {
			logger.LogError(context.Background(), "IFLinkPay reconciler: subscription order retirement failed trade_no="+tradeNo)
			return
		}
		logger.LogInfo(context.Background(), "IFLinkPay reconciler: subscription order retired as expired trade_no="+tradeNo)
		return
	}
	if !outcome.Paid {
		return
	}

	if err := model.CompleteSubscriptionOrderWithReceipt(tradeNo,
		model.PaymentProviderIFLinkPay, model.PaymentMethodIFLinkPay, outcome.Receipt); err != nil {
		logger.LogError(context.Background(), "IFLinkPay reconciler: subscription settlement failed trade_no="+tradeNo)
		return
	}
	logger.LogInfo(context.Background(), "IFLinkPay reconciler: subscription order settled trade_no="+tradeNo)
}

package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// RequestIFLinkPay creates neither an order nor a browser-visible payment URL.
// The legacy redirect signature also authenticated notifications, so it could
// never establish that a customer paid. Keep sales offline until independent
// server-side payment verification is integrated.
func RequestIFLinkPay(c *gin.Context) {
	c.Header("Retry-After", "300")
	common.ApiErrorStatus(c, http.StatusServiceUnavailable,
		common.NewMessage("Online payment is not enabled. Please contact the administrator."))
}

// IFLinkPayNotify fails closed for both route aliases and all previously issued
// checkout signatures. Never acknowledge payment success or touch accounting.
// The retryable response preserves a chance to reconcile actual payments after
// the provider supplies an independent payment-verification protocol.
func IFLinkPayNotify(c *gin.Context) {
	c.Header("Retry-After", "300")
	c.JSON(http.StatusServiceUnavailable, gin.H{"code": 1})
}

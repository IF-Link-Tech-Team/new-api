package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequestIFLinkPay is the wallet purchase entry point.
//
// It no longer decides anything itself: it delegates to the verified path, which
// fails closed unless the verified-payment switch is on and the backend-only
// credentials are present. Creating an order never credits it -- settlement
// requires the provider to confirm the payment server-side.
func RequestIFLinkPay(c *gin.Context) {
	RequestIFLinkPayVerified(c)
}

// IFLinkPayNotify fails closed for both route aliases and all previously issued
// checkout signatures. Never acknowledge payment success or touch accounting.
// The retryable response preserves a chance to reconcile actual payments after
// the provider supplies an independent callback-verification protocol.
func IFLinkPayNotify(c *gin.Context) {
	c.Header("Retry-After", "300")
	c.JSON(http.StatusServiceUnavailable, gin.H{"code": 1})
}

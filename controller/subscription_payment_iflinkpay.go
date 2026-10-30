package controller

import "github.com/gin-gonic/gin"

// Subscription purchases share the wallet security hold. Never reserve an order
// or issue checkout credentials for the legacy payment protocol.
func SubscriptionRequestIFLinkPay(c *gin.Context) {
	RequestIFLinkPay(c)
}

package controller

import "github.com/gin-gonic/gin"

// SubscriptionRequestIFLinkPay is the plan / traffic-pack purchase entry point.
//
// It delegates to the verified path, which fails closed unless the
// verified-payment switch is on and the backend-only credentials are present.
func SubscriptionRequestIFLinkPay(c *gin.Context) {
	SubscriptionRequestIFLinkPayVerified(c)
}

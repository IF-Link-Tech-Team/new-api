package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Default is a SELECT-only preview. Applying binds the reviewed fingerprint,
// an operator reason and the legacy revision (which may be zero).
func AdminMigrateSubscriptionPlanToProduct(c *gin.Context) {
	planID, err := strconv.Atoi(c.Param("id"))
	var req struct {
		ExpectedRevision *int64 `json:"expected_revision"`
		Fingerprint      string `json:"fingerprint"`
		Apply            bool   `json:"apply"`
		Reason           string `json:"reason"`
	}
	if err != nil || planID <= 0 || c.ShouldBindJSON(&req) != nil || req.ExpectedRevision == nil || *req.ExpectedRevision < 0 || (req.Apply && (strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 2048)) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !requirePaymentCompliance(c) {
		return
	}
	result, err := model.MigrateSubscriptionPlanToProduct(model.DB, planID, *req.ExpectedRevision, req.Fingerprint, !req.Apply)
	if errors.Is(err, model.ErrProductPlanConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if req.Apply {
		recordManageAudit(c, "subscription.product_migrate", map[string]any{
			"plan_id": result.PlanID, "price_book": result.PriceBook, "config_revision": result.ConfigRevision,
			"fingerprint": result.Fingerprint, "reason": req.Reason, "copied_prices": result.CopiedPrices,
		})
	}
	common.ApiSuccess(c, result)
}

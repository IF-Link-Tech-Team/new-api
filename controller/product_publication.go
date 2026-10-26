package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func AdminPreviewProductPublication(c *gin.Context) {
	adminProductPublication(c, true)
}

func AdminPublishProductAndSync(c *gin.Context) {
	adminProductPublication(c, false)
}

func adminProductPublication(c *gin.Context, previewOnly bool) {
	id, err := strconv.Atoi(c.Param("id"))
	var req model.ProductPublicationRequest
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if bindErr := c.ShouldBindJSON(&req); bindErr != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !requirePaymentCompliance(c) {
		return
	}
	enabled, err := model.APIChannelPricingMode()
	if err != nil || !enabled {
		common.ApiErrorMsg(c, "Product publication requires API channel pricing")
		return
	}
	req.Plan.Id = id
	var result any
	if previewOnly {
		result, err = model.PreviewProductPublication(model.DB, req)
	} else {
		var publication *model.ProductPublication
		publication, err = model.PublishProductAndSync(model.DB, req, c.GetInt("id"))
		result = publication
		if err == nil {
			recordManageAudit(c, "subscription.product_publish", map[string]any{
				"plan_id": id, "publication_id": publication.ID, "revision": publication.Revision,
				"fields": publication.Fields, "reason": publication.Reason,
			})
		}
	}
	if errors.Is(err, model.ErrProductPlanConflict) || errors.Is(err, model.ErrProductPublicationConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

// List bounded publication and per-entitlement results; no token/secret data
// is stored in the outbox or loaded for these operator responses.
func AdminListProductPublications(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var publications []model.ProductPublication
	if err := model.DB.Where("plan_id = ?", id).Order("id desc").Limit(20).Find(&publications).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	type statusCount struct {
		PublicationID int64  `json:"publication_id"`
		Status        string `json:"status"`
		Count         int64  `json:"count"`
	}
	counts := make([]statusCount, 0)
	ids := make([]int64, 0, len(publications))
	for _, publication := range publications {
		ids = append(ids, publication.ID)
	}
	if len(ids) > 0 {
		if err := model.DB.Model(&model.ProductPublicationItem{}).Select("publication_id, status, COUNT(*) AS count").Where("publication_id IN ?", ids).Group("publication_id, status").Scan(&counts).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}
	items := make([]model.ProductPublicationItem, 0)
	if value := c.Query("publication_id"); value != "" {
		publicationID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || publicationID <= 0 {
			common.ApiErrorT(c, "Invalid ID")
			return
		}
		var publication model.ProductPublication
		if err := model.DB.Where("id = ? AND plan_id = ?", publicationID, id).First(&publication).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		after, err := strconv.ParseInt(c.DefaultQuery("after_id", "0"), 10, 64)
		if err != nil || after < 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		if err := model.DB.Where("publication_id = ? AND id > ?", publicationID, after).Order("id").Limit(100).Find(&items).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}
	common.ApiSuccess(c, gin.H{"publications": publications, "counts": counts, "items": items})
}

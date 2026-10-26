package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type adminCreateProductPriceRequest struct {
	Price model.ProductPrice `json:"price"`
}

type adminSetProductPriceEnabledRequest struct {
	Enabled *bool `json:"enabled"`
}

func AdminStageProductChannelVersion(c *gin.Context) {
	planID, err := strconv.Atoi(c.Param("id"))
	var req struct {
		ExpectedRevision    int64                       `json:"expected_revision"`
		ExpectedAPIRevision int64                       `json:"expected_api_revision"`
		Version             string                      `json:"version"`
		Models              []model.ProductChannelModel `json:"models"`
		FallbackAllowed     *bool                       `json:"fallback_allowed"`
		Reason              string                      `json:"reason"`
	}
	if err != nil || planID <= 0 || c.ShouldBindJSON(&req) != nil || req.FallbackAllowed == nil || strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 2000 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !requirePaymentCompliance(c) {
		return
	}
	revision, err := model.StageProductChannelVersion(model.DB, planID, req.ExpectedRevision, req.ExpectedAPIRevision, req.Version, req.Models, *req.FallbackAllowed)
	if errors.Is(err, model.ErrProductPlanConflict) || errors.Is(err, model.ErrAPIChannelPriceConflict) || errors.Is(err, model.ErrPriceBookVersionExists) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "subscription.channel_version_stage", map[string]any{"plan_id": planID, "version": req.Version, "revision": revision, "reason": req.Reason})
	common.ApiSuccess(c, gin.H{"config_revision": revision})
}

// Product-scoped endpoints are the new price-book contract. The legacy
// product-type API remains separate during expansion; these endpoints never
// write it or silently activate a version for existing plans/entitlements.
func AdminListProductPriceBookVersions(c *gin.Context) {
	planId, err := strconv.Atoi(c.Param("id"))
	book, bookErr := model.ProductPriceBook(planId)
	if err != nil || bookErr != nil {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var plan model.SubscriptionPlan
	if err := model.DB.First(&plan, planId).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if plan.ProductType == model.ProductTypeWallet {
		common.ApiErrorMsg(c, "wallet is not a saleable product")
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 200 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	query := model.DB.Where("price_book = ?", book)
	if cursor := c.Query("before_id"); cursor != "" {
		beforeId, err := strconv.Atoi(cursor)
		if err != nil || beforeId <= 0 {
			common.ApiErrorT(c, "Invalid parameters")
			return
		}
		query = query.Where("id < ?", beforeId)
	}
	if value := strings.TrimSpace(c.Query("model")); value != "" {
		query = query.Where("model = ?", value)
	}
	if value := strings.TrimSpace(c.Query("price_version")); value != "" {
		query = query.Where("price_version = ?", value)
	}
	var prices []model.PriceBookPrice
	if err := query.Order("id desc").Limit(limit + 1).Find(&prices).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	nextId := 0
	if len(prices) > limit {
		prices = prices[:limit]
		nextId = prices[len(prices)-1].Id
	}
	common.ApiSuccess(c, gin.H{"items": prices, "next_id": nextId})
}

func AdminCreateProductPriceBookVersion(c *gin.Context) {
	planId, err := strconv.Atoi(c.Param("id"))
	if err != nil || planId <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req struct {
		Price model.PriceBookPrice `json:"price"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !requirePaymentCompliance(c) {
		return
	}
	if err := model.CreateProductPriceBookVersion(model.DB, planId, &req.Price); err != nil {
		if errors.Is(err, model.ErrPriceBookVersionExists) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
			return
		}
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "subscription.price_book_version_create", map[string]any{
		"plan_id": planId, "price_book": req.Price.PriceBook,
		"model": req.Price.Model, "price_version": req.Price.PriceVersion, "version_id": req.Price.Id,
	})
	common.ApiSuccess(c, req.Price)
}

func AdminSetProductPriceBookVersionEnabled(c *gin.Context) {
	planId, planErr := strconv.Atoi(c.Param("id"))
	priceId, priceErr := strconv.Atoi(c.Param("price_id"))
	if planErr != nil || priceErr != nil || planId <= 0 || priceId <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req struct {
		Enabled          *bool  `json:"enabled"`
		ExpectedRevision *int64 `json:"expected_revision"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil || req.ExpectedRevision == nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !requirePaymentCompliance(c) {
		return
	}
	price, revision, err := model.SetProductPriceBookVersionEnabled(model.DB, planId, priceId, *req.Enabled, *req.ExpectedRevision)
	if errors.Is(err, model.ErrProductPlanConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "subscription.price_book_version_status", map[string]any{
		"plan_id": planId, "price_book": price.PriceBook, "model": price.Model,
		"price_version": price.PriceVersion, "version_id": price.Id, "enabled": price.Enabled,
		"previous_revision": *req.ExpectedRevision, "config_revision": revision,
	})
	common.ApiSuccess(c, gin.H{"price": price, "config_revision": revision})
}

// AdminListProductPrices exposes the versioned product/model pricing catalog.
// The rows are immutable snapshots; filters make it practical to audit a
// single SKU or model before enabling sales.
func AdminListProductPrices(c *gin.Context) {
	query := model.DB.Model(&model.ProductPrice{})
	if value := strings.TrimSpace(c.Query("product_type")); value != "" {
		query = query.Where("product_type = ?", value)
	}
	if value := strings.TrimSpace(c.Query("model")); value != "" {
		query = query.Where("model = ?", value)
	}
	if value := strings.TrimSpace(c.Query("price_version")); value != "" {
		query = query.Where("price_version = ?", value)
	}
	var prices []model.ProductPrice
	if err := query.Order("effective_at desc, id desc").Limit(500).Find(&prices).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, prices)
}

func AdminCreateProductPrice(c *gin.Context) {
	var req adminCreateProductPriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	if !operation_setting.IsPaymentComplianceConfirmed() && req.Price.ProductType != model.ProductTypePackDeepSeekClassroom {
		if !requirePaymentCompliance(c) {
			return
		}
	}
	req.Price.Id = 0
	if err := req.Price.ValidateV1(); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.CreateLegacyProductPrice(model.DB, &req.Price); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, req.Price)
}

// AdminSetProductPriceEnabled is the only mutation permitted on an existing
// price version. Repricing requires a new price_version, while this switch is
// the sale switch for a model/product combination. Paid entitlements retain
// their historical price version; this switch must not erase purchased rights.
func AdminSetProductPriceEnabled(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	if !operation_setting.IsPaymentComplianceConfirmed() {
		var price model.ProductPrice
		if err := model.DB.Where("id = ?", id).First(&price).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if price.ProductType != model.ProductTypePackDeepSeekClassroom {
			if !requirePaymentCompliance(c) {
				return
			}
		}
	}
	var req adminSetProductPriceEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	err = model.SetLegacyProductPriceEnabled(model.DB, id, *req.Enabled)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorMsg(c, "价格版本不存在")
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

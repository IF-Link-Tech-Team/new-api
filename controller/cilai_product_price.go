package controller

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type adminCreateProductPriceRequest struct {
	Price model.ProductPrice `json:"price"`
}

type adminSetProductPriceEnabledRequest struct {
	Enabled *bool `json:"enabled"`
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
	if !requirePaymentCompliance(c) {
		return
	}
	var req adminCreateProductPriceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	req.Price.Id = 0
	if err := req.Price.ValidateV1(); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Create(&req.Price).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, req.Price)
}

// AdminSetProductPriceEnabled is the only mutation permitted on an existing
// price version. Repricing requires a new price_version, while this switch is
// the fail-closed emergency stop for a model/product combination.
func AdminSetProductPriceEnabled(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var req adminSetProductPriceEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	result := model.DB.Session(&gorm.Session{SkipHooks: true}).Model(&model.ProductPrice{}).Where("id = ?", id).Updates(map[string]interface{}{
		"enabled": *req.Enabled, "updated_at": common.GetTimestamp(),
	})
	if result.Error != nil {
		common.ApiError(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		common.ApiErrorMsg(c, "价格版本不存在")
		return
	}
	common.ApiSuccess(c, nil)
}

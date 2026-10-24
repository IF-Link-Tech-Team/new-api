package controller

import (
	"errors"
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

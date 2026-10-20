package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// AdminListProductTemplates exposes every product template so the admin UI can
// drive plan creation off the template contract (price rules, model scope,
// duration) without hardcoding the frozen V1 constants.
func AdminListProductTemplates(c *gin.Context) {
	templates, err := model.ListProductTemplates()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, templates)
}

func AdminGetProductTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var template model.ProductTemplate
	if err := model.DB.Where("id = ?", id).First(&template).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, template)
}

// productTemplateKeyFieldGetters maps the request JSON keys of contract-key
// fields to their current values. These fields define what is sold and how it
// bills; changing them through the operator write path would silently rewrite
// the contract of already-sold plans, so they only move via a code migration.
var productTemplateKeyFieldGetters = map[string]func(*model.ProductTemplate) interface{}{
	"product_type":          func(t *model.ProductTemplate) interface{} { return t.ProductType },
	"category":              func(t *model.ProductTemplate) interface{} { return t.Category },
	"currency":              func(t *model.ProductTemplate) interface{} { return t.Currency },
	"duration_days":         func(t *model.ProductTemplate) interface{} { return t.DurationDays },
	"billing_multiplier":    func(t *model.ProductTemplate) interface{} { return t.BillingMultiplier },
	"model_scope":           func(t *model.ProductTemplate) interface{} { return t.ModelScope },
	"allowed_channels":      func(t *model.ProductTemplate) interface{} { return t.AllowedChannels },
	"max_upstream_cost":     func(t *model.ProductTemplate) interface{} { return t.MaxUpstreamCost },
	"fallback_allowed":      func(t *model.ProductTemplate) interface{} { return t.FallbackAllowed },
	"max_purchase_per_user": func(t *model.ProductTemplate) interface{} { return t.MaxPurchasePerUser },
	"allow_balance_pay":     func(t *model.ProductTemplate) interface{} { return t.AllowBalancePay },
	"allow_wallet_overflow": func(t *model.ProductTemplate) interface{} { return t.AllowWalletOverflow },
	"course_eligible":       func(t *model.ProductTemplate) interface{} { return t.CourseEligible },
	"redemption_allowed":    func(t *model.ProductTemplate) interface{} { return t.RedemptionAllowed },
	"price_rules":           func(t *model.ProductTemplate) interface{} { return t.PriceRules },
	"product_family":        func(t *model.ProductTemplate) interface{} { return t.ProductFamily },
	"price_version":         func(t *model.ProductTemplate) interface{} { return t.PriceVersion },
	"locked":                func(t *model.ProductTemplate) interface{} { return t.Locked },
}

// changedProductTemplateKeyField returns the first key field whose submitted
// value differs from the stored template. Submitting a key field unchanged
// (e.g. a full-object editor echoing the row back) is accepted and ignored.
func changedProductTemplateKeyField(existing *model.ProductTemplate, raw map[string]json.RawMessage) (string, error) {
	for field, getter := range productTemplateKeyFieldGetters {
		rawValue, present := raw[field]
		if !present {
			continue
		}
		currentJSON, err := common.Marshal(getter(existing))
		if err != nil {
			return "", err
		}
		var currentValue, submittedValue interface{}
		if err := common.Unmarshal(currentJSON, &currentValue); err != nil {
			return "", err
		}
		if err := common.Unmarshal(rawValue, &submittedValue); err != nil {
			return field, nil
		}
		if !reflect.DeepEqual(currentValue, submittedValue) {
			return field, nil
		}
	}
	return "", nil
}

// AdminUpdateProductTemplate is the operator write path for a template's
// operator-facing copy only: display_name, display_metadata and
// min_margin_percent. Contract-key fields and locked core templates answer
// 403 and must go through MigrateLockedProductTemplate inside a migration.
func AdminUpdateProductTemplate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的ID")
		return
	}
	var raw map[string]json.RawMessage
	if err := common.DecodeJson(c.Request.Body, &raw); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	var existing model.ProductTemplate
	if err := model.DB.Where("id = ?", id).First(&existing).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	keyField, err := changedProductTemplateKeyField(&existing, raw)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if keyField != "" {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": fmt.Sprintf("关键字段 %s 不可通过后台修改，请通过代码迁移流程变更", keyField),
		})
		return
	}
	updated := existing
	if rawValue, present := raw["display_name"]; present {
		if err := common.Unmarshal(rawValue, &updated.DisplayName); err != nil {
			common.ApiErrorMsg(c, "参数错误")
			return
		}
	}
	if rawValue, present := raw["display_metadata"]; present {
		if err := common.Unmarshal(rawValue, &updated.DisplayMetadata); err != nil {
			common.ApiErrorMsg(c, "参数错误")
			return
		}
	}
	if rawValue, present := raw["min_margin_percent"]; present {
		if err := common.Unmarshal(rawValue, &updated.MinMarginPercent); err != nil {
			common.ApiErrorMsg(c, "参数错误")
			return
		}
		if updated.MinMarginPercent < 0 || updated.MinMarginPercent > 100 {
			common.ApiErrorMsg(c, "min_margin_percent 必须在 0 到 100 之间")
			return
		}
	}
	// UpdateProductTemplate is the authoritative locked guard; the branch only
	// translates its rejection into the 403 migration guidance.
	if err := model.UpdateProductTemplate(&updated); err != nil {
		if existing.Locked {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "该模板为锁定核心模板，只能通过代码迁移流程变更",
			})
			return
		}
		common.ApiError(c, err)
		return
	}
	model.InvalidateProductTemplateCache(existing.ProductType)
	common.ApiSuccess(c, updated)
}

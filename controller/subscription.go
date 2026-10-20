package controller

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---- Shared types ----

type SubscriptionPlanDTO struct {
	Plan               model.SubscriptionPlan `json:"plan"`
	ProductID          string                 `json:"product_id"`
	BillingMultipliers map[string]float64     `json:"billing_multipliers,omitempty"`
}

func subscriptionBillingMultipliers(productType string, modelScope string, priceVersion string) map[string]float64 {
	productType = strings.TrimSpace(productType)
	priceVersion = strings.TrimSpace(priceVersion)
	if productType == "" || priceVersion == "" {
		return nil
	}

	result := make(map[string]float64)
	seen := make(map[string]struct{})
	for _, rawModel := range strings.Split(modelScope, ",") {
		modelName := strings.TrimSpace(rawModel)
		if modelName == "" {
			continue
		}
		if _, exists := seen[modelName]; exists {
			continue
		}
		seen[modelName] = struct{}{}
		price, err := model.GetProductPriceSnapshot(productType, modelName, priceVersion)
		if err != nil {
			continue
		}
		result[modelName] = price.BillingMultiplier
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// subscriptionPlanProductID is the stable identifier exposed in Checkout
// URLs. V1 SKUs use their immutable product_code. The title-based fallback is
// only for older configured plans that predate product_code.
func subscriptionPlanProductID(plan model.SubscriptionPlan) string {
	if productCode := strings.ToLower(strings.TrimSpace(plan.ProductCode)); productCode != "" {
		return productCode
	}
	value := strings.ToLower(strings.TrimSpace(plan.Title + " " + plan.Subtitle + " " + plan.ModelScope))
	words := strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]struct{}, len(words))
	for _, word := range words {
		seen[word] = struct{}{}
	}
	has := func(word string) bool {
		_, ok := seen[word]
		return ok
	}
	switch {
	case has("deepseek") && has("pro"):
		return "deepseek-v4-pro"
	case has("deepseek") && has("flash"):
		return "deepseek-v4-flash"
	case has("glm"):
		return "glm-general"
	case has("kimi"):
		return "kimi-code"
	case has("qwen"):
		return "qwen-general"
	case has("lite"):
		return "lite"
	case has("max"):
		return "max"
	case has("pro"):
		return "pro"
	case plan.Id > 0:
		return strconv.Itoa(plan.Id)
	default:
		return ""
	}
}

func subscriptionPlanMatchesPublicID(plan model.SubscriptionPlan, productID string) bool {
	if subscriptionPlanProductID(plan) == productID {
		return true
	}
	switch plan.ProductType {
	case model.ProductTypeMonthlySubscription:
		return map[int]string{1: "monthly-starter", 2: "monthly-standard", 3: "monthly-advanced"}[plan.ProductTier] == productID
	case model.ProductTypePackDSGLM:
		return productID == "ds-glm-value"
	case model.ProductTypePackQwen:
		return productID == "qwen-pack"
	case model.ProductTypePackK3:
		return productID == fmt.Sprintf("kimi-k3-%.0f", plan.PriceAmount)
	default:
		return false
	}
}

func getSubscriptionPlanByProductID(productID string) (*model.SubscriptionPlan, error) {
	productID = strings.ToLower(strings.TrimSpace(productID))
	if productID == "" || len(productID) > 120 {
		return nil, fmt.Errorf("无效的商品编号")
	}
	var plans []model.SubscriptionPlan
	if err := model.DB.Order("id desc").Find(&plans).Error; err != nil {
		return nil, err
	}
	var matched *model.SubscriptionPlan
	for i := range plans {
		if !subscriptionPlanMatchesPublicID(plans[i], productID) {
			continue
		}
		if matched != nil {
			return nil, fmt.Errorf("商品编号配置冲突")
		}
		plans[i].NormalizeDefaults()
		matched = &plans[i]
	}
	if matched == nil {
		return nil, fmt.Errorf("商品不存在")
	}
	return matched, nil
}

type BillingPreferenceRequest struct {
	BillingPreference string `json:"billing_preference"`
}

type SubscriptionBalancePayRequest struct {
	PlanId int `json:"plan_id"`
}

type ClassroomBootstrapRequest struct {
	Apply       bool  `json:"apply"`
	Enabled     bool  `json:"enabled"`
	Listed      bool  `json:"listed"`
	EffectiveAt int64 `json:"effective_at"`
}

type MonthlyBootstrapRequest struct {
	Apply       bool  `json:"apply"`
	Enabled     bool  `json:"enabled"`
	Listed      bool  `json:"listed"`
	EffectiveAt int64 `json:"effective_at"`
}

func AdminBootstrapClassroomProduct(c *gin.Context) {
	var req ClassroomBootstrapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	result, err := model.BootstrapClassroomProduct(req.Apply, req.Enabled, req.Listed, req.EffectiveAt)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func AdminBootstrapMonthlyProducts(c *gin.Context) {
	var req MonthlyBootstrapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "参数错误")
		return
	}
	result, err := model.BootstrapMonthlyProducts(req.Apply, req.Enabled, req.Listed, req.EffectiveAt)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

type SubscriptionOrderStatusDTO struct {
	TradeNo            string  `json:"trade_no"`
	Status             string  `json:"status"`
	ProvisioningStatus string  `json:"provisioning_status"`
	ProvisioningError  string  `json:"provisioning_error,omitempty"`
	OrderType          string  `json:"order_type"`
	ProductType        string  `json:"product_type"`
	ProductCode        string  `json:"product_code"`
	ProductFamily      string  `json:"product_family"`
	SourceTier         int     `json:"source_tier"`
	TargetTier         int     `json:"target_tier"`
	PlanTitle          string  `json:"plan_title"`
	Money              float64 `json:"money"`
	Currency           string  `json:"currency"`
	PaymentMethod      string  `json:"payment_method"`
	UserSubscriptionId *int    `json:"user_subscription_id,omitempty"`
	CreateTime         int64   `json:"create_time"`
	CompleteTime       int64   `json:"complete_time"`
}

// ---- User APIs ----

func GetSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	query := model.DB.Where("listed = ?", true)
	if err := query.Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		p.NormalizeDefaults()
		result = append(result, SubscriptionPlanDTO{
			Plan:               p,
			ProductID:          subscriptionPlanProductID(p),
			BillingMultipliers: subscriptionBillingMultipliers(p.ProductType, p.ModelScope, p.PriceVersion),
		})
	}
	common.ApiSuccess(c, result)
}

func GetSubscriptionProduct(c *gin.Context) {
	plan, err := getSubscriptionPlanByProductID(c.Param("product_id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !(plan.RedemptionEnabled || (plan.Listed && plan.Enabled)) {
		common.ApiErrorMsg(c, "商品当前不可用")
		return
	}
	common.ApiSuccess(c, SubscriptionPlanDTO{
		Plan:               *plan,
		ProductID:          subscriptionPlanProductID(*plan),
		BillingMultipliers: subscriptionBillingMultipliers(plan.ProductType, plan.ModelScope, plan.PriceVersion),
	})
}

func GetSubscriptionSelf(c *gin.Context) {
	userId := c.GetInt("id")
	settingMap, _ := model.GetUserSetting(userId, false)
	pref := common.NormalizeBillingPreference(settingMap.BillingPreference)

	// Get all subscriptions (including expired)
	allSubscriptions, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		allSubscriptions = []model.SubscriptionSummary{}
	}

	// Get active subscriptions for backward compatibility
	activeSubscriptions, err := model.GetAllActiveUserSubscriptions(userId)
	if err != nil {
		activeSubscriptions = []model.SubscriptionSummary{}
	}

	// Attach plan metadata so user-facing pages can render package value
	// (price-based CNY display) without hitting admin endpoints.
	enrich := func(summaries []model.SubscriptionSummary) []gin.H {
		items := make([]gin.H, 0, len(summaries))
		planCache := map[int]*model.SubscriptionPlan{}
		for _, summary := range summaries {
			item := gin.H{"subscription": summary.Subscription}
			if summary.Subscription != nil {
				item["billing_multipliers"] = subscriptionBillingMultipliers(
					summary.Subscription.ProductType,
					summary.Subscription.ModelScope,
					summary.Subscription.PriceVersion,
				)
				planId := summary.Subscription.PlanId
				plan := planCache[planId]
				if plan == nil {
					if loaded, planErr := model.GetSubscriptionPlanById(planId); planErr == nil && loaded != nil {
						plan = loaded
						planCache[planId] = plan
					}
				}
				if plan != nil {
					item["plan_title"] = plan.Title
					item["plan_subtitle"] = plan.Subtitle
					item["plan_price_amount"] = plan.PriceAmount
					item["plan_currency"] = plan.Currency
					item["plan_category"] = model.NormalizeSubscriptionCategory(plan.Category)
				}
			}
			items = append(items, item)
		}
		return items
	}

	common.ApiSuccess(c, gin.H{
		"billing_preference": pref,
		"subscriptions":      enrich(activeSubscriptions), // all active subscriptions
		"all_subscriptions":  enrich(allSubscriptions),    // all subscriptions including expired
	})
}

// GetSubscriptionSelfOrders is the recovery source for browser returns and
// interrupted payment flows. It deliberately exposes neither provider payload
// nor provider identifiers.
func GetSubscriptionSelfOrders(c *gin.Context) {
	userId := c.GetInt("id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	orders, err := model.GetUserSubscriptionOrders(userId, c.Query("trade_no"), limit)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionOrderStatusDTO, 0, len(orders))
	for _, order := range orders {
		result = append(result, SubscriptionOrderStatusDTO{
			TradeNo: order.TradeNo, Status: order.Status,
			ProvisioningStatus: order.ProvisioningStatus, ProvisioningError: order.ProvisioningError,
			OrderType: order.OrderType, ProductType: order.ProductType, ProductCode: order.ProductCode,
			ProductFamily: order.ProductFamily, SourceTier: order.SourceTier, TargetTier: order.TargetTier,
			PlanTitle: order.PlanTitleSnapshot, Money: order.Money, Currency: order.CurrencySnapshot,
			PaymentMethod: order.PaymentMethod, UserSubscriptionId: order.UserSubscriptionId,
			CreateTime: order.CreateTime, CompleteTime: order.CompleteTime,
		})
	}
	common.ApiSuccess(c, result)
}

func UpdateSubscriptionPreference(c *gin.Context) {
	userId := c.GetInt("id")
	var req BillingPreferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	pref := common.NormalizeBillingPreference(req.BillingPreference)

	user, err := model.GetUserById(userId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	current := user.GetSetting()
	current.BillingPreference = pref
	if err := model.UpdateUserSetting(user.Id, current); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"billing_preference": pref})
}

func SubscriptionRequestBalancePay(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	userId := c.GetInt("id")
	var req SubscriptionBalancePayRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}

	if err := model.PurchaseSubscriptionWithBalance(userId, req.PlanId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// ---- Admin APIs ----

func AdminListSubscriptionPlans(c *gin.Context) {
	var plans []model.SubscriptionPlan
	if err := model.DB.Order("sort_order desc, id desc").Find(&plans).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]SubscriptionPlanDTO, 0, len(plans))
	for _, p := range plans {
		p.NormalizeDefaults()
		result = append(result, SubscriptionPlanDTO{
			Plan:      p,
			ProductID: subscriptionPlanProductID(p),
		})
	}
	common.ApiSuccess(c, result)
}

type AdminUpsertSubscriptionPlanRequest struct {
	Plan model.SubscriptionPlan `json:"plan"`
}

func normalizeAdminSubscriptionPlan(plan *model.SubscriptionPlan) error {
	if plan == nil {
		return fmt.Errorf("套餐不能为空")
	}
	plan.ProductType = strings.TrimSpace(plan.ProductType)
	plan.ProductCode = strings.TrimSpace(plan.ProductCode)
	plan.ProductFamily = strings.TrimSpace(plan.ProductFamily)
	plan.PriceVersion = strings.TrimSpace(plan.PriceVersion)
	plan.AllowedChannels = strings.TrimSpace(plan.AllowedChannels)
	plan.MaxUpstreamCost = strings.TrimSpace(plan.MaxUpstreamCost)
	plan.ModelScope = strings.TrimSpace(plan.ModelScope)
	plan.Category = model.NormalizeSubscriptionCategory(plan.Category)
	plan.QuotaResetPeriod = model.NormalizeResetPeriod(plan.QuotaResetPeriod)
	plan.DisplayMetadata = strings.TrimSpace(plan.DisplayMetadata)
	if plan.DisplayMetadata != "" {
		if len(plan.DisplayMetadata) > 16384 {
			return fmt.Errorf("display_metadata 过长")
		}
		var metadata map[string]any
		if err := common.UnmarshalJsonStr(plan.DisplayMetadata, &metadata); err != nil {
			return fmt.Errorf("display_metadata 必须是合法的 JSON 对象")
		}
	}
	if plan.ProductType != "" {
		if plan.Currency == "" {
			plan.Currency = "CNY"
		}
		plan.Currency = strings.ToUpper(strings.TrimSpace(plan.Currency))
		if plan.AllowWalletOverflow == nil {
			plan.AllowWalletOverflow = common.GetPointer(false)
		}
		if plan.BillingMultiplier <= 0 {
			if multiplier, ok := model.ExpectedV1BillingMultiplier(plan.ProductType); ok {
				plan.BillingMultiplier = multiplier
			}
		}
		if plan.ProductType == model.ProductTypeLegacySubscription {
			plan.TemplateId = 0
		} else if template, err := model.GetProductTemplateByType(plan.ProductType); err == nil {
			plan.TemplateId = int64(template.Id)
		}
	} else {
		plan.TemplateId = 0
		if plan.Currency == "" {
			plan.Currency = "USD"
		}
		plan.Currency = "USD"
		if plan.AllowWalletOverflow == nil {
			plan.AllowWalletOverflow = common.GetPointer(true)
		}
	}
	if plan.AllowBalancePay == nil {
		plan.AllowBalancePay = common.GetPointer(true)
	}
	if plan.DurationUnit == "" {
		plan.DurationUnit = model.SubscriptionDurationMonth
	}
	if plan.DurationValue <= 0 && plan.DurationUnit != model.SubscriptionDurationCustom {
		plan.DurationValue = 1
	}
	if plan.QuotaResetPeriod == model.SubscriptionResetCustom && plan.QuotaResetCustomSeconds <= 0 {
		return fmt.Errorf("自定义重置周期需大于0秒")
	}
	return plan.ValidateV1Contract()
}

func ensureUniqueProductCode(planId int, productCode string) error {
	productCode = strings.TrimSpace(productCode)
	if productCode == "" {
		return nil
	}
	var count int64
	query := model.DB.Model(&model.SubscriptionPlan{}).Where("product_code = ?", productCode)
	if planId > 0 {
		query = query.Where("id <> ?", planId)
	}
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("product_code 已存在")
	}
	return nil
}

func AdminCreateSubscriptionPlan(c *gin.Context) {
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if !operation_setting.IsPaymentComplianceConfirmed() && req.Plan.ProductType != model.ProductTypePackDeepSeekClassroom {
		if !requirePaymentCompliance(c) {
			return
		}
	}
	req.Plan.Id = 0
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorT(c, "Subscription plan title cannot be empty")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorT(c, "Price cannot be negative")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorT(c, "Price cannot exceed 9999")
		return
	}
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorT(c, "Purchase limit cannot be negative")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorT(c, "Total quota cannot be negative")
		return
	}
	req.Plan.UpgradeGroup = strings.TrimSpace(req.Plan.UpgradeGroup)
	if req.Plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.UpgradeGroup]; !ok {
			common.ApiErrorT(c, "Upgrade group does not exist")
			return
		}
	}
	req.Plan.DowngradeGroup = strings.TrimSpace(req.Plan.DowngradeGroup)
	if req.Plan.DowngradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.DowngradeGroup]; !ok {
			common.ApiErrorT(c, "Downgrade group does not exist")
			return
		}
	}
	if err := normalizeAdminSubscriptionPlan(&req.Plan); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := ensureUniqueProductCode(0, req.Plan.ProductCode); err != nil {
		common.ApiError(c, err)
		return
	}
	err := model.DB.Create(&req.Plan).Error
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(req.Plan.Id)
	common.ApiSuccess(c, req.Plan)
}

func AdminUpdateSubscriptionPlan(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req AdminUpsertSubscriptionPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var existingPlan model.SubscriptionPlan
	if err := model.DB.Where("id = ?", id).First(&existingPlan).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if !operation_setting.IsPaymentComplianceConfirmed() &&
		(existingPlan.ProductType != model.ProductTypePackDeepSeekClassroom || req.Plan.ProductType != model.ProductTypePackDeepSeekClassroom) {
		if !requirePaymentCompliance(c) {
			return
		}
	}
	if strings.TrimSpace(req.Plan.Title) == "" {
		common.ApiErrorT(c, "Subscription plan title cannot be empty")
		return
	}
	if req.Plan.PriceAmount < 0 {
		common.ApiErrorT(c, "Price cannot be negative")
		return
	}
	if req.Plan.PriceAmount > 9999 {
		common.ApiErrorT(c, "Price cannot exceed 9999")
		return
	}
	req.Plan.Id = id
	if req.Plan.MaxPurchasePerUser < 0 {
		common.ApiErrorT(c, "Purchase limit cannot be negative")
		return
	}
	if req.Plan.TotalAmount < 0 {
		common.ApiErrorT(c, "Total quota cannot be negative")
		return
	}
	req.Plan.UpgradeGroup = strings.TrimSpace(req.Plan.UpgradeGroup)
	if req.Plan.UpgradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.UpgradeGroup]; !ok {
			common.ApiErrorT(c, "Upgrade group does not exist")
			return
		}
	}
	req.Plan.DowngradeGroup = strings.TrimSpace(req.Plan.DowngradeGroup)
	if req.Plan.DowngradeGroup != "" {
		if _, ok := ratio_setting.GetGroupRatioCopy()[req.Plan.DowngradeGroup]; !ok {
			common.ApiErrorT(c, "Downgrade group does not exist")
			return
		}
	}
	if err := normalizeAdminSubscriptionPlan(&req.Plan); err != nil {
		common.ApiError(c, err)
		return
	}
	if existingPlan.ProductCode != "" && existingPlan.ProductCode != req.Plan.ProductCode {
		common.ApiErrorMsg(c, "product_code 创建后不可修改")
		return
	}
	if err := ensureUniqueProductCode(id, req.Plan.ProductCode); err != nil {
		common.ApiError(c, err)
		return
	}

	err := model.DB.Transaction(func(tx *gorm.DB) error {
		// update plan (allow zero values updates with map)
		updateMap := map[string]any{
			"title":          req.Plan.Title,
			"subtitle":       req.Plan.Subtitle,
			"price_amount":   req.Plan.PriceAmount,
			"currency":       req.Plan.Currency,
			"duration_unit":  req.Plan.DurationUnit,
			"duration_value": req.Plan.DurationValue,
			"custom_seconds": req.Plan.CustomSeconds,
			"enabled":        req.Plan.Enabled,
			"listed":         req.Plan.Listed,
			// The full plan editor predates the independent redemption switch.
			// Preserve it here; the PATCH status endpoint is authoritative.
			"redemption_enabled":         existingPlan.RedemptionEnabled,
			"sort_order":                 req.Plan.SortOrder,
			"stripe_price_id":            req.Plan.StripePriceId,
			"creem_product_id":           req.Plan.CreemProductId,
			"waffo_pancake_product_id":   req.Plan.WaffoPancakeProductId,
			"max_purchase_per_user":      req.Plan.MaxPurchasePerUser,
			"total_amount":               req.Plan.TotalAmount,
			"upgrade_group":              req.Plan.UpgradeGroup,
			"downgrade_group":            req.Plan.DowngradeGroup,
			"model_scope":                req.Plan.ModelScope,
			"quota_reset_period":         req.Plan.QuotaResetPeriod,
			"quota_reset_custom_seconds": req.Plan.QuotaResetCustomSeconds,
			"category":                   model.NormalizeSubscriptionCategory(req.Plan.Category),
			"product_type":               req.Plan.ProductType,
			"product_code":               req.Plan.ProductCode,
			"product_family":             req.Plan.ProductFamily,
			"product_tier":               req.Plan.ProductTier,
			"billing_multiplier":         req.Plan.BillingMultiplier,
			"price_version":              req.Plan.PriceVersion,
			"allowed_channels":           req.Plan.AllowedChannels,
			"max_upstream_cost":          req.Plan.MaxUpstreamCost,
			"fallback_allowed":           req.Plan.FallbackAllowed,
			"course_eligible":            req.Plan.CourseEligible,
			"template_id":                req.Plan.TemplateId,
			"display_metadata":           req.Plan.DisplayMetadata,
			"updated_at":                 common.GetTimestamp(),
		}
		if req.Plan.AllowBalancePay != nil {
			updateMap["allow_balance_pay"] = *req.Plan.AllowBalancePay
		}
		if req.Plan.AllowWalletOverflow != nil {
			updateMap["allow_wallet_overflow"] = *req.Plan.AllowWalletOverflow
		}
		if err := tx.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Updates(updateMap).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminUpdateSubscriptionPlanStatusRequest struct {
	Enabled           *bool `json:"enabled"`
	Listed            *bool `json:"listed"`
	RedemptionEnabled *bool `json:"redemption_enabled"`
}

func AdminUpdateSubscriptionPlanStatus(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req AdminUpdateSubscriptionPlanStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil || (req.Enabled == nil && req.Listed == nil && req.RedemptionEnabled == nil) {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	var plan model.SubscriptionPlan
	if err := model.DB.Where("id = ?", id).First(&plan).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	// Redemption availability is deliberately independent from paid-sale
	// availability. When payment compliance is still closed, admins may pause
	// or resume redemption for any configured product, but may not change the
	// paid-sale/listing switches for non-classroom products.
	changesPaidSaleState := req.Enabled != nil || req.Listed != nil
	if !operation_setting.IsPaymentComplianceConfirmed() && changesPaidSaleState && plan.ProductType != model.ProductTypePackDeepSeekClassroom {
		if !requirePaymentCompliance(c) {
			return
		}
	}
	updates := map[string]any{}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.Listed != nil {
		updates["listed"] = *req.Listed
	}
	if req.RedemptionEnabled != nil {
		updates["redemption_enabled"] = *req.RedemptionEnabled
	}
	if err := model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	model.InvalidateSubscriptionPlanCache(id)
	common.ApiSuccess(c, nil)
}

type AdminBindSubscriptionRequest struct {
	UserId int `json:"user_id"`
	PlanId int `json:"plan_id"`
}

func AdminBindSubscription(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	var req AdminBindSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.UserId <= 0 || req.PlanId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	msg, err := model.AdminBindSubscription(req.UserId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subscriptionChangeSuccess(c, msg)
}

// subscriptionChangeSuccess replies to an admin subscription change. notice
// tells the admin how the user's group changes, when it does.
func subscriptionChangeSuccess(c *gin.Context, notice *common.Message) {
	if notice == nil {
		common.ApiSuccess(c, nil)
		return
	}
	data := notice.Fields()
	data["message"] = notice.Error()
	common.ApiSuccess(c, data)
}

// ---- Admin: user subscription management ----

func AdminListUserSubscriptions(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	subs, err := model.GetAllUserSubscriptions(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, subs)
}

type AdminCreateUserSubscriptionRequest struct {
	PlanId int `json:"plan_id"`
}

type AdminResetSubscriptionRequest struct {
	PlanId           int   `json:"plan_id"`
	AdvanceResetTime *bool `json:"advance_reset_time"`
}

func resolveAdvanceResetTime(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func recordSubscriptionResetUserLogs(c *gin.Context, result *model.SubscriptionResetResult, adminInfo *model.AuditAdminInfo) {
	if result == nil || result.ResetCount == 0 {
		return
	}
	const action = "subscription.user_quota_reset"
	params := map[string]any{"plan_id": result.PlanId, "plan_title": result.PlanTitle}
	operation := model.AuditOperation{Action: action, Params: params}
	content := auditContentEN(action, params)
	for _, userId := range result.AffectedUserIds {
		model.RecordLogWithAdminInfo(userId, model.LogTypeManage, content, adminInfo, &operation, c)
	}
}

// AdminCreateUserSubscription creates a new user subscription from a plan (no payment).
func AdminCreateUserSubscription(c *gin.Context) {
	if !requirePaymentCompliance(c) {
		return
	}

	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	var req AdminCreateUserSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	msg, err := model.AdminBindSubscription(userId, req.PlanId, "")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subscriptionChangeSuccess(c, msg)
}

func AdminResetUserSubscriptionsByPlan(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Param("id"))
	if userId <= 0 {
		common.ApiErrorT(c, "Invalid user ID")
		return
	}
	var req AdminResetSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	if req.PlanId <= 0 {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	advanceResetTime := resolveAdvanceResetTime(req.AdvanceResetTime)
	result, err := model.AdminResetUserSubscriptionsByPlan(userId, req.PlanId, advanceResetTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordSubscriptionResetUserLogs(c, result, auditOperatorInfo(c))
	recordManageAuditFor(c, userId, "subscription.user_plan_reset", map[string]any{
		"target_user_id":     userId,
		"plan_id":            result.PlanId,
		"plan_title":         result.PlanTitle,
		"reset_count":        result.ResetCount,
		"user_count":         result.UserCount,
		"advance_reset_time": result.AdvanceResetTime,
	})
	common.ApiSuccess(c, result)
}

func AdminResetPlanSubscriptions(c *gin.Context) {
	planId, _ := strconv.Atoi(c.Param("id"))
	if planId <= 0 {
		common.ApiErrorT(c, "Invalid ID")
		return
	}
	var req AdminResetSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorT(c, "Invalid parameters")
		return
	}
	advanceResetTime := resolveAdvanceResetTime(req.AdvanceResetTime)
	result, err := model.AdminResetPlanSubscriptions(planId, advanceResetTime)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordSubscriptionResetUserLogs(c, result, auditOperatorInfo(c))
	common.SysLog(common.LogText("admin reset subscription plan %d quota: reset_count=%d user_count=%d advance_reset_time=%t",
		result.PlanId, result.ResetCount, result.UserCount, result.AdvanceResetTime))
	recordManageAudit(c, "subscription.plan_reset", map[string]any{
		"plan_id":            result.PlanId,
		"plan_title":         result.PlanTitle,
		"reset_count":        result.ResetCount,
		"user_count":         result.UserCount,
		"advance_reset_time": result.AdvanceResetTime,
	})
	common.ApiSuccess(c, result)
}

// AdminInvalidateUserSubscription cancels a user subscription immediately.
func AdminInvalidateUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorT(c, "Invalid subscription ID")
		return
	}
	msg, err := model.AdminInvalidateUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subscriptionChangeSuccess(c, msg)
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
func AdminDeleteUserSubscription(c *gin.Context) {
	subId, _ := strconv.Atoi(c.Param("id"))
	if subId <= 0 {
		common.ApiErrorT(c, "Invalid subscription ID")
		return
	}
	msg, err := model.AdminDeleteUserSubscription(subId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	subscriptionChangeSuccess(c, msg)
}

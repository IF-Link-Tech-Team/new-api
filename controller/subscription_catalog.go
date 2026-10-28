package controller

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// StorefrontCatalogItemDTO is the anonymous landing-page view of a plan. It
// deliberately omits cost and routing fields (allowed_channels,
// max_upstream_cost, billing_multiplier, gateway ids, ...) — only display and
// purchase-relevant fields cross the trust boundary.
type StorefrontCatalogItemDTO struct {
	ProductID               string          `json:"product_id"`
	Title                   string          `json:"title"`
	Subtitle                string          `json:"subtitle"`
	PriceAmount             float64         `json:"price_amount"`
	Currency                string          `json:"currency"`
	DurationUnit            string          `json:"duration_unit"`
	DurationValue           int             `json:"duration_value"`
	TotalAmount             int64           `json:"total_amount"`
	Category                string          `json:"category"`
	MaxPurchasePerUser      int             `json:"max_purchase_per_user"`
	QuotaResetPeriod        string          `json:"quota_reset_period"`
	QuotaResetCustomSeconds int64           `json:"quota_reset_custom_seconds"`
	CustomSeconds           int64           `json:"custom_seconds"`
	SortOrder               int             `json:"sort_order"`
	ModelScope              string          `json:"model_scope"`
	Enabled                 bool            `json:"enabled"`
	Preview                 bool            `json:"preview"`
	DisplayMetadata         json.RawMessage `json:"display_metadata"`
}

// GetSubscriptionPublicCatalog serves the landing-page product catalog to
// anonymous visitors. Listed products and display_metadata.preview drafts are
// included; paid checkout enforces Enabled server-side while Listed controls
// catalog discovery and RedemptionEnabled independently controls code claims.
func GetSubscriptionPublicCatalog(c *gin.Context) {
	items, err := model.GetStorefrontCatalog()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]StorefrontCatalogItemDTO, 0, len(items))
	for _, item := range items {
		plan := item.Plan
		metadata := storefrontDisplayMetadata(plan.DisplayMetadata)
		result = append(result, StorefrontCatalogItemDTO{
			ProductID:               subscriptionPlanProductID(plan),
			Title:                   plan.Title,
			Subtitle:                plan.Subtitle,
			PriceAmount:             plan.PriceAmount,
			Currency:                plan.Currency,
			DurationUnit:            plan.DurationUnit,
			DurationValue:           plan.DurationValue,
			TotalAmount:             plan.TotalAmount,
			Category:                model.NormalizeSubscriptionCategory(plan.Category),
			SortOrder:               plan.SortOrder,
			ModelScope:              plan.ModelScope,
			Enabled:                 plan.Enabled && plan.Listed,
			Preview:                 item.Preview && !plan.Listed,
			MaxPurchasePerUser:      plan.MaxPurchasePerUser,
			QuotaResetPeriod:        plan.QuotaResetPeriod,
			QuotaResetCustomSeconds: plan.QuotaResetCustomSeconds,
			CustomSeconds:           plan.CustomSeconds,
			DisplayMetadata:         metadata,
		})
	}
	common.ApiSuccess(c, result)
}

func storefrontDisplayMetadata(raw string) json.RawMessage {
	var input map[string]any
	if err := common.Unmarshal([]byte(strings.TrimSpace(raw)), &input); err != nil {
		return json.RawMessage("{}")
	}
	allowed := map[string]struct{}{
		"badges": {}, "benefits": {}, "button_label": {}, "recommended": {},
		"featured": {}, "reference_price": {}, "models_display": {}, "zone": {},
		"image": {}, "image_alt": {}, "preview": {},
	}
	filtered := make(map[string]any, len(input))
	for key, value := range input {
		if _, ok := allowed[key]; ok {
			filtered[key] = value
		}
	}
	encoded, err := common.Marshal(filtered)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(encoded)
}

// GetSubscriptionPublicModels exposes presentation metadata only. The sales
// projection is shared with /v1/models, so importing an upstream model alone
// never advertises it as sold. Native metadata still controls descriptions and
// visibility; no prices, procurement costs, channel IDs or keys cross this API.
func GetSubscriptionPublicModels(c *gin.Context) {
	enabled, err := model.APIChannelPricingMode()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	usableGroups := service.GetUserUsableGroups("")
	var sales map[string]bool
	if enabled {
		sales, err = model.APIChannelModelsForGroups(model.DB, slices.Collect(maps.Keys(usableGroups)))
		if err != nil {
			common.ApiError(c, err)
			return
		}
	}
	type publicModel struct {
		ModelName   string `json:"model_name"`
		Description string `json:"description"`
	}
	result := make([]publicModel, 0)
	for _, item := range filterPricingByUsableGroups(model.GetPricing(), usableGroups) {
		if enabled && !sales[item.ModelName] {
			continue
		}
		result = append(result, publicModel{ModelName: item.ModelName, Description: item.Description})
	}
	slices.SortFunc(result, func(a, b publicModel) int { return strings.Compare(a.ModelName, b.ModelName) })
	common.ApiSuccess(c, result)
}

package controller

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// StorefrontCatalogItemDTO is the anonymous landing-page view of a plan. It
// deliberately omits cost and routing fields (allowed_channels,
// max_upstream_cost, billing_multiplier, gateway ids, ...) — only display and
// purchase-relevant fields cross the trust boundary.
type StorefrontCatalogItemDTO struct {
	ProductID       string          `json:"product_id"`
	Title           string          `json:"title"`
	Subtitle        string          `json:"subtitle"`
	PriceAmount     float64         `json:"price_amount"`
	Currency        string          `json:"currency"`
	DurationUnit    string          `json:"duration_unit"`
	DurationValue   int             `json:"duration_value"`
	TotalAmount     int64           `json:"total_amount"`
	Category        string          `json:"category"`
	SortOrder       int             `json:"sort_order"`
	ModelScope      string          `json:"model_scope"`
	Enabled         bool            `json:"enabled"`
	Preview         bool            `json:"preview"`
	DisplayMetadata json.RawMessage `json:"display_metadata"`
}

// GetSubscriptionPublicCatalog serves the landing-page product catalog to
// anonymous visitors. Listed products and display_metadata.preview drafts are
// included; checkout keeps enforcing enabled/listed server-side.
func GetSubscriptionPublicCatalog(c *gin.Context) {
	items, err := model.GetStorefrontCatalog()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result := make([]StorefrontCatalogItemDTO, 0, len(items))
	for _, item := range items {
		plan := item.Plan
		metadata := json.RawMessage("{}")
		if trimmed := strings.TrimSpace(plan.DisplayMetadata); trimmed != "" {
			metadata = json.RawMessage(trimmed)
		}
		result = append(result, StorefrontCatalogItemDTO{
			ProductID:       subscriptionPlanProductID(plan),
			Title:           plan.Title,
			Subtitle:        plan.Subtitle,
			PriceAmount:     plan.PriceAmount,
			Currency:        plan.Currency,
			DurationUnit:    plan.DurationUnit,
			DurationValue:   plan.DurationValue,
			TotalAmount:     plan.TotalAmount,
			Category:        model.NormalizeSubscriptionCategory(plan.Category),
			SortOrder:       plan.SortOrder,
			ModelScope:      plan.ModelScope,
			Enabled:         plan.Enabled,
			Preview:         item.Preview,
			DisplayMetadata: metadata,
		})
	}
	common.ApiSuccess(c, result)
}

package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func AdminListAPIChannelPrices(c *gin.Context) {
	revision, prices, err := model.ReadAPIChannelCatalog(model.DB, strings.TrimSpace(c.Query("model")))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// Never return channel records: they contain usable provider credentials.
	type binding struct {
		ChannelID      int                    `json:"channel_id"`
		ChannelName    string                 `json:"channel_name"`
		ChannelStatus  int                    `json:"channel_status"`
		Model          string                 `json:"model"`
		UpstreamModel  string                 `json:"upstream_model"`
		MappingValid   bool                   `json:"mapping_valid"`
		Cost           *model.PriceComponents `json:"cost,omitempty"`
		CostVerifiedAt int64                  `json:"cost_verified_at"`
	}
	var channels []model.Channel
	if err := model.DB.Select("id", "name", "status", "models", "model_mapping", "upstream_costs", "cost_verified_at").Order("id").Find(&channels).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	bindings := make([]binding, 0)
	filter := strings.TrimSpace(c.Query("model"))
	for _, channel := range channels {
		for _, name := range channel.GetModels() {
			if filter != "" && filter != name {
				continue
			}
			mapped, err := model.ResolveMappedModelName(channel.GetModelMapping(), name)
			item := binding{ChannelID: channel.Id, ChannelName: channel.Name, ChannelStatus: channel.Status, Model: name, UpstreamModel: mapped, MappingValid: err == nil && mapped != "", CostVerifiedAt: channel.CostVerifiedAt}
			if cost, ok := channel.GetUpstreamCost(name); ok {
				item.Cost = &cost
			}
			bindings = append(bindings, item)
		}
	}
	enabled, modeErr := model.APIChannelPricingMode()
	if modeErr != nil {
		common.ApiError(c, modeErr)
		return
	}
	common.ApiSuccess(c, gin.H{"revision": revision, "prices": prices, "bindings": bindings, "active": enabled})
}

func AdminPublishAPIChannelPrices(c *gin.Context) {
	var request struct {
		ExpectedRevision *int64                  `json:"expected_revision"`
		Changes          []model.APIChannelPrice `json:"changes"`
		Reason           string                  `json:"reason"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.ExpectedRevision == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid API channel price publication"})
		return
	}
	revision, err := model.PublishAPIChannelPrices(model.DB, *request.ExpectedRevision, request.Changes, c.GetInt("id"), request.Reason)
	if errors.Is(err, model.ErrAPIChannelPriceConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "api_channel_prices.publish", map[string]any{"previous_revision": *request.ExpectedRevision, "revision": revision, "rows": len(request.Changes), "reason": request.Reason})
	common.ApiSuccess(c, gin.H{"revision": revision})
}

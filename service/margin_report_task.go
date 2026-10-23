package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type marginReportHandler struct{}

func init()                                         { RegisterSystemTaskHandler(marginReportHandler{}) }
func (marginReportHandler) Type() string            { return model.SystemTaskTypeMarginReport }
func (marginReportHandler) Enabled() bool           { return true }
func (marginReportHandler) Interval() time.Duration { return 24 * time.Hour }
func (marginReportHandler) NewPayload() any         { return nil }

// MarginReportGroup is one product_type's coarse 24h gross-margin estimate.
type MarginReportGroup struct {
	ProductType      string  `json:"product_type"`
	Requests         int64   `json:"requests"`
	RevenueQuota     int64   `json:"revenue_quota"`
	Revenue          float64 `json:"revenue"`
	EstimatedCost    float64 `json:"estimated_cost"`
	CostCovered      int64   `json:"cost_covered_requests"`
	MarginPercent    float64 `json:"margin_percent"`
	MinMarginPercent float64 `json:"min_margin_percent"`
	Breached         bool    `json:"breached"`
}

type MarginReportResult struct {
	WindowStart int64               `json:"window_start"`
	WindowEnd   int64               `json:"window_end"`
	Groups      []MarginReportGroup `json:"groups"`
	Breaches    int                 `json:"breaches"`
}

const marginReportBatchSize = 500

// RunMarginReport aggregates the last 24h of consume logs per product_type
// (parsed from the log's other.product_type, empty means wallet) and compares
// an estimated gross margin against each template's min_margin_percent floor.
//
// Precision boundary (deliberately coarse, see the Phase 2 spec): revenue is
// the charged quota converted with the runtime QuotaPerUnit (1 currency unit =
// QuotaPerUnit quota, as the V1 contract uses for face values); upstream cost
// is estimated per request as tokens x the serving channel's current
// upstream_costs components (input/output plus cached/cache-creation when the
// log carries those counters). It ignores historical channel-cost drift,
// refunds, non-token billing modes, and requests whose channel has no cost for
// the model (those are excluded from the cost side, which biases the estimate
// upward, i.e. towards missing a breach rather than a false alarm). It is an
// early-warning report, not an accounting ledger.
func RunMarginReport(ctx context.Context) (MarginReportResult, error) {
	now := common.GetTimestamp()
	result := MarginReportResult{WindowStart: now - 24*3600, WindowEnd: now}

	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		return result, err
	}
	channelsById := make(map[int]*model.Channel, len(channels))
	for _, channel := range channels {
		channelsById[channel.Id] = channel
	}

	type aggregate struct {
		requests     int64
		revenueQuota int64
		cost         float64
		costCovered  int64
	}
	groups := map[string]*aggregate{}

	afterId := 0
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		rows, err := model.FindConsumeLogBatchForMarginReport(ctx, result.WindowStart, afterId, marginReportBatchSize)
		if err != nil {
			return result, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			afterId = row.Id
			productType, cacheTokens, cacheCreationTokens := parseMarginReportOther(row.Other)
			group := groups[productType]
			if group == nil {
				group = &aggregate{}
				groups[productType] = group
			}
			group.requests++
			group.revenueQuota += int64(row.Quota)
			channel := channelsById[row.ChannelId]
			if channel == nil {
				continue
			}
			cost, ok := channel.GetUpstreamCost(row.ModelName)
			if !ok {
				continue
			}
			group.cost += (float64(row.PromptTokens)*cost.Input +
				float64(row.CompletionTokens)*cost.Output +
				float64(cacheTokens)*cost.CachedInput +
				float64(cacheCreationTokens)*cost.CacheCreation) / 1e6
			group.costCovered++
		}
		if len(rows) < marginReportBatchSize {
			break
		}
	}

	productTypes := make([]string, 0, len(groups))
	for productType := range groups {
		productTypes = append(productTypes, productType)
	}
	sort.Strings(productTypes)

	var breaches []string
	for _, productType := range productTypes {
		group := groups[productType]
		out := MarginReportGroup{
			ProductType:   productType,
			Requests:      group.requests,
			RevenueQuota:  group.revenueQuota,
			Revenue:       float64(group.revenueQuota) / common.QuotaPerUnit,
			EstimatedCost: group.cost,
			CostCovered:   group.costCovered,
		}
		template, err := model.GetProductTemplateByType(productType)
		if err == nil && template != nil {
			out.MinMarginPercent = template.MinMarginPercent
		}
		if out.Revenue > 0 {
			out.MarginPercent = (out.Revenue - out.EstimatedCost) / out.Revenue * 100
		}
		// Only alert when the margin is actually computable: a floor is
		// configured, revenue exists, and at least one request carried
		// usable channel cost data.
		if out.MinMarginPercent > 0 && out.Revenue > 0 && out.CostCovered > 0 && out.MarginPercent < out.MinMarginPercent {
			out.Breached = true
			breaches = append(breaches, fmt.Sprintf("%s: margin %.2f%% < floor %.2f%% (revenue %.4f, estimated cost %.4f, %d/%d requests cost-covered)",
				productType, out.MarginPercent, out.MinMarginPercent, out.Revenue, out.EstimatedCost, out.CostCovered, out.Requests))
		}
		result.Groups = append(result.Groups, out)
	}
	result.Breaches = len(breaches)
	for _, breach := range breaches {
		common.SysError("daily margin report breach: " + breach)
	}
	if len(breaches) > 0 {
		return result, fmt.Errorf("daily margin report found %d product(s) below their gross-margin floor: %s",
			len(breaches), strings.Join(breaches, "; "))
	}
	return result, nil
}

// parseMarginReportOther extracts the billing fields the consume log's other
// JSON carries (see appendBillingInfo / GenerateTextOtherInfo). An absent
// product_type means the request pre-dates V1 tagging or used an untagged key;
// those are attributed to the wallet bucket.
func parseMarginReportOther(raw string) (productType string, cacheTokens int, cacheCreationTokens int) {
	productType = model.ProductTypeWallet
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return productType, 0, 0
	}
	var other map[string]any
	if err := common.UnmarshalJsonStr(trimmed, &other); err != nil {
		return productType, 0, 0
	}
	if value, ok := other["product_type"].(string); ok && strings.TrimSpace(value) != "" {
		productType = strings.TrimSpace(value)
	}
	cacheTokens = marginReportOtherInt(other["cache_tokens"])
	cacheCreationTokens = marginReportOtherInt(other["cache_creation_tokens"])
	return productType, cacheTokens, cacheCreationTokens
}

func marginReportOtherInt(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return 0
	}
}

func (marginReportHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := RunMarginReport(ctx)
	status, message := model.SystemTaskStatusSucceeded, ""
	if err != nil {
		status, message = model.SystemTaskStatusFailed, err.Error()
		logger.LogError(ctx, message)
	}
	if finishErr := model.FinishSystemTask(task.TaskID, runnerID, status, result, message); finishErr != nil {
		logger.LogError(ctx, fmt.Sprintf("margin report task finish failed: %v", finishErr))
		return
	}
	// Save the durable report before attempting any external delivery.
	if err != nil {
		notifyAdmins("margin_report_breach", func(role int, _ dto.UserSetting) bool {
			return role == common.RoleRootUser
		}, func(_ string) (string, string) {
			return "Daily margin report: gross margin below floor", message
		})
	}
}

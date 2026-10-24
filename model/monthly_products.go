package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	MonthlyPriceVersion  = "monthly-unified-20261014-v2"
	MonthlyProductFamily = "monthly-unified-20261014"

	MonthlyModelDeepSeekV41 = "deepseek-v4-1-flash-260910"
	MonthlyModelDeepSeek    = "deepseek-v4-flash-ga-260731"
	MonthlyModelGLM52       = "glm-5.2"
	MonthlyModelKimiK3      = "kimi-k3"
	MonthlyModelQwen        = "qwen3.8-flash"
)

type MonthlyBootstrapPlanResult struct {
	PlanId      int     `json:"plan_id"`
	ProductCode string  `json:"product_code"`
	Title       string  `json:"title"`
	PriceAmount float64 `json:"price_amount"`
	TotalAmount int64   `json:"total_amount"`
}

type MonthlyBootstrapResult struct {
	Applied      bool                         `json:"applied"`
	Enabled      bool                         `json:"enabled"`
	Listed       bool                         `json:"listed"`
	PriceVersion string                       `json:"price_version"`
	QuotaPerUnit float64                      `json:"quota_per_unit"`
	Models       []string                     `json:"models"`
	Plans        []MonthlyBootstrapPlanResult `json:"plans"`
}

func monthlyModelBillingMultiplier(modelName string) (float64, bool) {
	switch strings.TrimSpace(modelName) {
	case MonthlyModelDeepSeekV41, MonthlyModelDeepSeek, MonthlyModelGLM52:
		return 0.70, true
	case MonthlyModelKimiK3:
		return 0.95, true
	case MonthlyModelQwen:
		return 1.00, true
	default:
		return 0, false
	}
}

func monthlyDesiredPrices(enabled bool, effectiveAt int64) ([]ProductPrice, error) {
	definitions := []struct {
		model      string
		channelId  int
		official   PriceComponents
		selling    PriceComponents
		upstream   PriceComponents
		expression string
		multiplier float64
	}{
		{
			model: MonthlyModelDeepSeekV41, channelId: 15,
			official:   PriceComponents{Input: 2, CachedInput: 0.04, Output: 8},
			selling:    PriceComponents{Input: 1.4, CachedInput: 0.028, Output: 5.6},
			upstream:   PriceComponents{Input: 1, CachedInput: 0.02, Output: 4},
			expression: `weekday("Asia/Shanghai") >= 1 && weekday("Asia/Shanghai") <= 5 && ((hour("Asia/Shanghai") >= 9 && hour("Asia/Shanghai") < 12) || (hour("Asia/Shanghai") >= 14 && hour("Asia/Shanghai") < 18)) ? tier("peak", p * 2 + cr * 0.04 + c * 8) : tier("offpeak", p * 1 + cr * 0.02 + c * 4)`,
			multiplier: 0.70,
		},
		{
			model: MonthlyModelDeepSeek, channelId: 15,
			official:   PriceComponents{Input: 3, CachedInput: 0.10, Output: 9},
			selling:    PriceComponents{Input: 2.1, CachedInput: 0.07, Output: 6.3},
			upstream:   PriceComponents{Input: 1.5, CachedInput: 0.05, Output: 4.5},
			expression: `tier("base", p * 3 + cr * 0.10 + c * 9)`,
			multiplier: 0.70,
		},
		{
			model: MonthlyModelGLM52, channelId: 14,
			official:   PriceComponents{Input: 8, CachedInput: 2, Output: 28},
			selling:    PriceComponents{Input: 5.6, CachedInput: 1.4, Output: 19.6},
			upstream:   PriceComponents{Input: 4.4, CachedInput: 1.1, Output: 15.4},
			expression: `tier("base", p * 8 + cr * 2 + c * 28)`,
			multiplier: 0.70,
		},
		{
			model: MonthlyModelKimiK3, channelId: 14,
			official:   PriceComponents{Input: 20, CachedInput: 2, Output: 100},
			selling:    PriceComponents{Input: 19, CachedInput: 1.9, Output: 95},
			upstream:   PriceComponents{Input: 16, CachedInput: 1.6, Output: 80},
			expression: `tier("base", p * 20 + cr * 2 + c * 100)`,
			multiplier: 0.95,
		},
		{
			model: MonthlyModelQwen, channelId: 14,
			official:   PriceComponents{Input: 0.80, CachedInput: 0.10, CacheCreation: 1.25, Output: 2.70},
			selling:    PriceComponents{Input: 0.80, CachedInput: 0.10, CacheCreation: 1.25, Output: 2.70},
			upstream:   PriceComponents{Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16},
			expression: `tier("base", p * 0.80 + cr * 0.10 + cc * 1.25 + c * 2.70)`,
			multiplier: 1.00,
		},
	}

	prices := make([]ProductPrice, 0, len(definitions))
	for _, definition := range definitions {
		channels, err := classroomJSON([]int{definition.channelId})
		if err != nil {
			return nil, err
		}
		official, err := classroomJSON(definition.official)
		if err != nil {
			return nil, err
		}
		selling, err := classroomJSON(definition.selling)
		if err != nil {
			return nil, err
		}
		upstream, err := classroomJSON(definition.upstream)
		if err != nil {
			return nil, err
		}
		prices = append(prices, ProductPrice{
			ProductType: ProductTypeMonthlySubscription, Model: definition.model,
			PriceVersion: MonthlyPriceVersion, EffectiveAt: effectiveAt,
			OfficialReferencePrice: official, UserSellingPrice: selling,
			UpstreamSettlementPrice: upstream, BillingExpression: definition.expression,
			BillingMultiplier: definition.multiplier, AllowedChannels: channels,
			MaxUpstreamCost: upstream, FallbackAllowed: false, Enabled: enabled,
		})
	}
	return prices, nil
}

func monthlyDesiredPlans(enabled bool, listed bool) ([]SubscriptionPlan, error) {
	allowed, err := classroomJSON(map[string][]int{
		MonthlyModelDeepSeekV41: {15},
		MonthlyModelDeepSeek:    {15},
		MonthlyModelGLM52:       {14},
		MonthlyModelKimiK3:      {14},
		MonthlyModelQwen:        {14},
	})
	if err != nil {
		return nil, err
	}
	costs, err := classroomJSON(map[string]PriceComponents{
		MonthlyModelDeepSeekV41: {Input: 1, CachedInput: 0.02, Output: 4},
		MonthlyModelDeepSeek:    {Input: 1.5, CachedInput: 0.05, Output: 4.5},
		MonthlyModelGLM52:       {Input: 4.4, CachedInput: 1.1, Output: 15.4},
		MonthlyModelKimiK3:      {Input: 16, CachedInput: 1.6, Output: 80},
		MonthlyModelQwen:        {Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16},
	})
	if err != nil {
		return nil, err
	}
	allowBalance := false
	allowWallet := false
	modelScope := strings.Join([]string{MonthlyModelQwen, MonthlyModelDeepSeekV41, MonthlyModelDeepSeek, MonthlyModelGLM52, MonthlyModelKimiK3}, ",")
	definitions := []struct {
		title       string
		productCode string
		tier        int
		price       float64
		points      int64
		sortOrder   int
	}{
		{title: "Lite 套餐", productCode: "lite", tier: 1, price: 19.9, points: 39800, sortOrder: 700},
		{title: "Pro 套餐", productCode: "pro", tier: 2, price: 49.9, points: 99800, sortOrder: 800},
		{title: "Max 套餐", productCode: "max", tier: 3, price: 99.9, points: 199800, sortOrder: 900},
	}
	plans := make([]SubscriptionPlan, 0, len(definitions))
	for _, definition := range definitions {
		quota, err := common.QuotaFromFloatStrict(definition.price * common.QuotaPerUnit)
		if err != nil {
			return nil, fmt.Errorf("invalid quota for %s: %w", definition.title, err)
		}
		if int64(quota)/250 != definition.points {
			return nil, fmt.Errorf("%s quota does not equal %d computing points", definition.title, definition.points)
		}
		plan := SubscriptionPlan{
			Title:       definition.title,
			Subtitle:    fmt.Sprintf("%s 算力点 · 30 天固定额度 · 周期内不重置", formatMonthlyPoints(definition.points)),
			PriceAmount: definition.price, Currency: "CNY",
			DurationUnit: SubscriptionDurationDay, DurationValue: 30,
			Enabled: enabled, Listed: listed, RedemptionEnabled: enabled, SortOrder: definition.sortOrder,
			AllowBalancePay: &allowBalance, AllowWalletOverflow: &allowWallet,
			TotalAmount: int64(quota), QuotaResetPeriod: SubscriptionResetNever,
			ModelScope: modelScope, Category: SubscriptionCategorySubscription,
			ProductType: ProductTypeMonthlySubscription, ProductCode: definition.productCode,
			ProductFamily: MonthlyProductFamily, ProductTier: definition.tier,
			BillingMultiplier: 1.00, PriceVersion: MonthlyPriceVersion,
			AllowedChannels: allowed, MaxUpstreamCost: costs, FallbackAllowed: false,
			CourseEligible: false,
		}
		if err := plan.ValidateV1Contract(); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func formatMonthlyPoints(points int64) string {
	if points < 1000 {
		return fmt.Sprintf("%d", points)
	}
	return fmt.Sprintf("%d,%03d", points/1000, points%1000)
}

func validateMonthlyChannel(channelId int, expectedModels map[string]PriceComponents) error {
	var channel Channel
	if err := DB.Where("id = ?", channelId).First(&channel).Error; err != nil {
		return fmt.Errorf("channel %d is not configured: %w", channelId, err)
	}
	if channel.Status != common.ChannelStatusEnabled {
		return fmt.Errorf("channel %d is disabled", channelId)
	}
	hasDefaultGroup := false
	for _, groupName := range strings.Split(channel.Group, ",") {
		if strings.TrimSpace(groupName) == "default" {
			hasDefaultGroup = true
			break
		}
	}
	if !hasDefaultGroup {
		return fmt.Errorf("channel %d does not belong to default group", channelId)
	}
	models := map[string]struct{}{}
	for _, modelName := range channel.GetModels() {
		models[strings.TrimSpace(modelName)] = struct{}{}
	}
	mapping := map[string]string{}
	if rawMapping := strings.TrimSpace(channel.GetModelMapping()); rawMapping != "" {
		if err := common.UnmarshalJsonStr(rawMapping, &mapping); err != nil {
			return fmt.Errorf("channel %d has an invalid model mapping: %w", channelId, err)
		}
	}
	for modelName, expectedCost := range expectedModels {
		if _, ok := models[modelName]; !ok {
			return fmt.Errorf("channel %d does not expose %s", channelId, modelName)
		}
		actualCost, ok := channel.GetUpstreamCost(modelName)
		if !ok || !samePriceComponents(actualCost, expectedCost) {
			return fmt.Errorf("channel %d upstream cost for %s differs from the audited snapshot", channelId, modelName)
		}
		if mappedName := strings.TrimSpace(mapping[modelName]); mappedName != "" && mappedName != modelName {
			return fmt.Errorf("channel %d maps %s to unaudited upstream model %s", channelId, modelName, mappedName)
		}
	}
	return nil
}

// BootstrapMonthlyProducts validates and optionally installs the unified
// Lite/Pro/Max catalog. It never changes channel configuration or any existing
// entitlement; historical plans retain their frozen price version.
func BootstrapMonthlyProducts(apply bool, enabled bool, listed bool, effectiveAt int64) (*MonthlyBootstrapResult, error) {
	if err := validateMonthlyChannel(15, map[string]PriceComponents{
		MonthlyModelDeepSeekV41: {Input: 1, CachedInput: 0.02, Output: 4},
		MonthlyModelDeepSeek:    {Input: 1.5, CachedInput: 0.05, Output: 4.5},
	}); err != nil {
		return nil, err
	}
	if err := validateMonthlyChannel(14, map[string]PriceComponents{
		MonthlyModelGLM52:  {Input: 4.4, CachedInput: 1.1, Output: 15.4},
		MonthlyModelKimiK3: {Input: 16, CachedInput: 1.6, Output: 80},
		MonthlyModelQwen:   {Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16},
	}); err != nil {
		return nil, err
	}
	prices, err := monthlyDesiredPrices(enabled, effectiveAt)
	if err != nil {
		return nil, err
	}
	for index := range prices {
		if err := prices[index].ValidateV1(); err != nil {
			return nil, err
		}
	}
	plans, err := monthlyDesiredPlans(enabled, listed)
	if err != nil {
		return nil, err
	}
	result := &MonthlyBootstrapResult{
		Applied: apply, Enabled: enabled, Listed: listed,
		PriceVersion: MonthlyPriceVersion, QuotaPerUnit: common.QuotaPerUnit,
		Models: []string{MonthlyModelQwen, MonthlyModelDeepSeekV41, MonthlyModelDeepSeek, MonthlyModelGLM52, MonthlyModelKimiK3},
		Plans:  make([]MonthlyBootstrapPlanResult, len(plans)),
	}
	for index := range plans {
		result.Plans[index] = MonthlyBootstrapPlanResult{
			ProductCode: plans[index].ProductCode, Title: plans[index].Title,
			PriceAmount: plans[index].PriceAmount, TotalAmount: plans[index].TotalAmount,
		}
	}
	if !apply {
		return result, nil
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		for index := range prices {
			desired := prices[index]
			var existing ProductPrice
			find := tx.Where("product_type = ? AND model = ? AND price_version = ?", desired.ProductType, desired.Model, desired.PriceVersion).Limit(1).Find(&existing)
			if find.Error != nil {
				return find.Error
			}
			if find.RowsAffected == 0 {
				if err := CreateLegacyProductPrice(tx, &desired); err != nil {
					return err
				}
				continue
			}
			if !sameProductPrice(existing, desired) {
				return fmt.Errorf("immutable monthly product price %s differs from billing contract", desired.Model)
			}
			if err := SetLegacyProductPriceEnabled(tx, existing.Id, desired.Enabled); err != nil {
				return err
			}
		}

		for index := range plans {
			plan := &plans[index]
			var existing SubscriptionPlan
			find := tx.Where("product_code = ?", plan.ProductCode).Limit(1).Find(&existing)
			if find.Error != nil {
				return find.Error
			}
			if find.RowsAffected == 0 {
				if err := tx.Create(plan).Error; err != nil {
					return err
				}
			} else {
				if existing.ProductType != ProductTypeMonthlySubscription || existing.ProductFamily != MonthlyProductFamily || existing.ProductTier != plan.ProductTier {
					return fmt.Errorf("product code %s is already used by a different product identity", plan.ProductCode)
				}
				plan.Id = existing.Id
				plan.CreatedAt = existing.CreatedAt
				if err := tx.Save(plan).Error; err != nil {
					return err
				}
			}
			if plan.Id <= 0 {
				return errors.New("monthly product plan was not assigned an id")
			}
			if err := tx.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).Updates(map[string]interface{}{
				"enabled": enabled, "listed": listed, "redemption_enabled": enabled,
			}).Error; err != nil {
				return err
			}
			result.Plans[index].PlanId = plan.Id
			if err := SyncSubscriptionPlanPriceBook(tx, plan); err != nil {
				return err
			}
			if enabled {
				if err := validateV1ProductPriceCatalogTx(tx, plan); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for index := range result.Plans {
		InvalidateSubscriptionPlanCache(result.Plans[index].PlanId)
	}
	return result, nil
}

package model

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	ClassroomProductCode       = "deepseek-classroom-5-20261014"
	ClassroomPriceVersion      = "deepseek-classroom-20261014-v1"
	WalletDeepSeekPriceVersion = "wallet-deepseek-20261014-v1"
	ClassroomModelV41          = "deepseek-v4-1-flash-260910"
	ClassroomModelV4GA         = "deepseek-v4-flash-ga-260731"
)

type ClassroomBootstrapResult struct {
	Applied            bool     `json:"applied"`
	Enabled            bool     `json:"enabled"`
	Listed             bool     `json:"listed"`
	ChannelId          int      `json:"channel_id"`
	ChannelName        string   `json:"channel_name"`
	PlanId             int      `json:"plan_id"`
	ProductCode        string   `json:"product_code"`
	PriceVersion       string   `json:"price_version"`
	WalletPriceVersion string   `json:"wallet_price_version"`
	TotalAmount        int64    `json:"total_amount"`
	QuotaPerUnit       float64  `json:"quota_per_unit"`
	Models             []string `json:"models"`
}

func classroomJSON(value interface{}) (string, error) {
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func classroomDesiredPrices(enabled bool, effectiveAt int64) ([]ProductPrice, error) {
	channels, err := classroomJSON([]int{15})
	if err != nil {
		return nil, err
	}
	definitions := []struct {
		model      string
		official   PriceComponents
		selling    PriceComponents
		upstream   PriceComponents
		expression string
	}{
		{
			model:      ClassroomModelV4GA,
			official:   PriceComponents{Input: 3, CachedInput: 0.10, Output: 9},
			selling:    PriceComponents{Input: 1.8, CachedInput: 0.06, Output: 5.4},
			upstream:   PriceComponents{Input: 1.5, CachedInput: 0.05, Output: 4.5},
			expression: `tier("base", p * 3 + cr * 0.10 + c * 9)`,
		},
		{
			model:      ClassroomModelV41,
			official:   PriceComponents{Input: 2, CachedInput: 0.04, Output: 8},
			selling:    PriceComponents{Input: 1.2, CachedInput: 0.024, Output: 4.8},
			upstream:   PriceComponents{Input: 1, CachedInput: 0.02, Output: 4},
			expression: `weekday("Asia/Shanghai") >= 1 && weekday("Asia/Shanghai") <= 5 && ((hour("Asia/Shanghai") >= 9 && hour("Asia/Shanghai") < 12) || (hour("Asia/Shanghai") >= 14 && hour("Asia/Shanghai") < 18)) ? tier("peak", p * 2 + cr * 0.04 + c * 8) : tier("offpeak", p * 1 + cr * 0.02 + c * 4)`,
		},
	}
	prices := make([]ProductPrice, 0, len(definitions))
	for _, definition := range definitions {
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
			ProductType: ProductTypePackDeepSeekClassroom, Model: definition.model,
			PriceVersion: ClassroomPriceVersion, EffectiveAt: effectiveAt,
			OfficialReferencePrice: official, UserSellingPrice: selling,
			UpstreamSettlementPrice: upstream, BillingExpression: definition.expression,
			BillingMultiplier: 0.60, AllowedChannels: channels, MaxUpstreamCost: upstream,
			FallbackAllowed: false, Enabled: enabled,
		})
	}
	return prices, nil
}

// walletDeepSeekDesiredPrices uses the same audited official reference prices
// as the classroom product, but deliberately freezes a 1.00 multiplier. This
// keeps ordinary API keys on wallet billing and prevents them from inheriting
// the classroom discount.
func walletDeepSeekDesiredPrices(effectiveAt int64) ([]ProductPrice, error) {
	prices, err := classroomDesiredPrices(true, effectiveAt)
	if err != nil {
		return nil, err
	}
	for index := range prices {
		prices[index].ProductType = ProductTypeWallet
		prices[index].PriceVersion = WalletDeepSeekPriceVersion
		prices[index].UserSellingPrice = prices[index].OfficialReferencePrice
		prices[index].BillingMultiplier = 1
		prices[index].FallbackAllowed = false
		prices[index].Enabled = true
	}
	return prices, nil
}

func classroomDesiredPlan(enabled bool, listed bool, totalAmount int64) (*SubscriptionPlan, error) {
	allowed, err := classroomJSON(map[string][]int{
		ClassroomModelV41: {15}, ClassroomModelV4GA: {15},
	})
	if err != nil {
		return nil, err
	}
	costs, err := classroomJSON(map[string]PriceComponents{
		ClassroomModelV41:  {Input: 1, CachedInput: 0.02, Output: 4},
		ClassroomModelV4GA: {Input: 1.5, CachedInput: 0.05, Output: 4.5},
	})
	if err != nil {
		return nil, err
	}
	allowBalance := false
	allowWallet := false
	plan := &SubscriptionPlan{
		Title:       "DeepSeek 课堂专享六折包",
		Subtitle:    "课堂专享 · 限时六折 · 兑换后 10 天有效",
		PriceAmount: 5, Currency: "CNY", DurationUnit: SubscriptionDurationDay, DurationValue: 10,
		Enabled: enabled, Listed: listed, SortOrder: 1000,
		AllowBalancePay: &allowBalance, AllowWalletOverflow: &allowWallet,
		MaxPurchasePerUser: 1, TotalAmount: totalAmount, QuotaResetPeriod: SubscriptionResetNever,
		ModelScope: ClassroomModelV41 + "," + ClassroomModelV4GA,
		Category:   SubscriptionCategoryPackage, ProductType: ProductTypePackDeepSeekClassroom,
		ProductCode: ClassroomProductCode, ProductFamily: "deepseek_classroom",
		BillingMultiplier: 0.60, PriceVersion: ClassroomPriceVersion,
		AllowedChannels: allowed, MaxUpstreamCost: costs, FallbackAllowed: false,
	}
	return plan, plan.ValidateV1Contract()
}

func sameProductPrice(existing ProductPrice, desired ProductPrice) bool {
	return existing.ProductType == desired.ProductType && existing.Model == desired.Model &&
		existing.PriceVersion == desired.PriceVersion && existing.EffectiveAt == desired.EffectiveAt &&
		existing.OfficialReferencePrice == desired.OfficialReferencePrice &&
		existing.UserSellingPrice == desired.UserSellingPrice &&
		existing.UpstreamSettlementPrice == desired.UpstreamSettlementPrice &&
		existing.BillingExpression == desired.BillingExpression &&
		math.Abs(existing.BillingMultiplier-desired.BillingMultiplier) < 0.000001 &&
		existing.AllowedChannels == desired.AllowedChannels && existing.MaxUpstreamCost == desired.MaxUpstreamCost &&
		existing.FallbackAllowed == desired.FallbackAllowed
}

// BootstrapClassroomProduct validates and optionally applies the frozen
// classroom SKU. Quota is calculated from the runtime QuotaPerUnit setting;
// it is never treated as a token count and no CNY conversion is hard-coded.
func BootstrapClassroomProduct(apply bool, enabled bool, listed bool, effectiveAt int64) (*ClassroomBootstrapResult, error) {
	quota, err := common.QuotaFromFloatStrict(5 * common.QuotaPerUnit)
	if err != nil {
		return nil, fmt.Errorf("invalid runtime QuotaPerUnit: %w", err)
	}
	prices, err := classroomDesiredPrices(enabled, effectiveAt)
	if err != nil {
		return nil, err
	}
	for index := range prices {
		if err := prices[index].ValidateV1(); err != nil {
			return nil, err
		}
	}
	walletPrices, err := walletDeepSeekDesiredPrices(effectiveAt)
	if err != nil {
		return nil, err
	}
	for index := range walletPrices {
		if err := walletPrices[index].ValidateV1(); err != nil {
			return nil, err
		}
	}
	plan, err := classroomDesiredPlan(enabled, listed, int64(quota))
	if err != nil {
		return nil, err
	}
	var channel Channel
	if err := DB.Where("id = ?", 15).First(&channel).Error; err != nil {
		return nil, fmt.Errorf("channel 15 is not configured: %w", err)
	}
	if channel.Status != common.ChannelStatusEnabled {
		return nil, errors.New("channel 15 is disabled")
	}
	channelModels := map[string]struct{}{}
	for _, name := range channel.GetModels() {
		channelModels[strings.TrimSpace(name)] = struct{}{}
	}
	for _, name := range []string{ClassroomModelV41, ClassroomModelV4GA} {
		if _, ok := channelModels[name]; !ok {
			return nil, fmt.Errorf("channel 15 does not expose %s", name)
		}
	}
	result := &ClassroomBootstrapResult{
		Applied: apply, Enabled: enabled, Listed: listed, ChannelId: channel.Id, ChannelName: channel.Name,
		ProductCode: ClassroomProductCode, PriceVersion: ClassroomPriceVersion,
		WalletPriceVersion: WalletDeepSeekPriceVersion,
		TotalAmount:        int64(quota), QuotaPerUnit: common.QuotaPerUnit,
		Models: []string{ClassroomModelV4GA, ClassroomModelV41},
	}
	if !apply {
		return result, nil
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		costs := map[string]PriceComponents{}
		if strings.TrimSpace(channel.UpstreamCosts) != "" {
			if err := common.UnmarshalJsonStr(channel.UpstreamCosts, &costs); err != nil {
				return err
			}
		}
		costs[ClassroomModelV41] = PriceComponents{Input: 1, CachedInput: 0.02, Output: 4}
		costs[ClassroomModelV4GA] = PriceComponents{Input: 1.5, CachedInput: 0.05, Output: 4.5}
		encodedCosts, err := classroomJSON(costs)
		if err != nil {
			return err
		}
		if err := tx.Model(&Channel{}).Where("id = ?", 15).Update("upstream_costs", encodedCosts).Error; err != nil {
			return err
		}

		allPrices := append(prices, walletPrices...)
		for index := range allPrices {
			desired := allPrices[index]
			var existing ProductPrice
			find := tx.Where("product_type = ? AND model = ? AND price_version = ?", desired.ProductType, desired.Model, desired.PriceVersion).Limit(1).Find(&existing)
			if find.Error != nil {
				return find.Error
			}
			if find.RowsAffected == 0 {
				if err := tx.Create(&desired).Error; err != nil {
					return err
				}
				continue
			}
			if !sameProductPrice(existing, desired) {
				return fmt.Errorf("immutable product price %s/%s differs from billing contract", desired.ProductType, desired.Model)
			}
			desiredEnabled := desired.Enabled
			if err := tx.Session(&gorm.Session{SkipHooks: true}).Model(&ProductPrice{}).Where("id = ?", existing.Id).
				Updates(map[string]interface{}{"enabled": desiredEnabled, "updated_at": common.GetTimestamp()}).Error; err != nil {
				return err
			}
		}

		var existingPlan SubscriptionPlan
		findPlan := tx.Where("product_code = ?", ClassroomProductCode).Limit(1).Find(&existingPlan)
		if findPlan.Error != nil {
			return findPlan.Error
		}
		if findPlan.RowsAffected == 0 {
			if err := tx.Create(plan).Error; err != nil {
				return err
			}
			result.PlanId = plan.Id
		} else {
			plan.Id = existingPlan.Id
			plan.CreatedAt = existingPlan.CreatedAt
			if err := tx.Save(plan).Error; err != nil {
				return err
			}
			result.PlanId = plan.Id
		}
		if !enabled {
			// Disabling the sale/redemption switch must not invalidate existing
			// entitlements, which continue using their frozen price snapshots.
			return nil
		}
		return validateV1ProductPriceCatalogTx(tx, plan)
	})
	if err != nil {
		return nil, err
	}
	InvalidateSubscriptionPlanCache(result.PlanId)
	InitChannelCache()
	return result, nil
}

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
	ClassroomProductCode        = "deepseek-classroom-5-20261014"
	ClassroomLegacyPriceVersion = "deepseek-classroom-20261014-v1"
	ClassroomPriceVersion       = "iflearn-qwen38-20261014-v2"
	WalletDeepSeekPriceVersion  = "wallet-deepseek-20261014-v1"
	ClassroomModelV41           = "deepseek-v4-1-flash-260910"
	ClassroomModelV4GA          = "deepseek-v4-flash-ga-260731"
	ClassroomModelQwen          = "qwen3.8-flash"
)

type ClassroomBootstrapResult struct {
	Applied               bool     `json:"applied"`
	Enabled               bool     `json:"enabled"`
	Listed                bool     `json:"listed"`
	ChannelId             int      `json:"channel_id"`
	ChannelName           string   `json:"channel_name"`
	PlanId                int      `json:"plan_id"`
	ProductCode           string   `json:"product_code"`
	PriceVersion          string   `json:"price_version"`
	WalletPriceVersion    string   `json:"wallet_price_version"`
	TotalAmount           int64    `json:"total_amount"`
	QuotaPerUnit          float64  `json:"quota_per_unit"`
	Models                []string `json:"models"`
	MigratedSubscriptions int64    `json:"migrated_subscriptions"`
	MigratedTokens        int64    `json:"migrated_tokens"`
}

func classroomJSON(value interface{}) (string, error) {
	encoded, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func classroomDesiredPrices(enabled bool, effectiveAt int64) ([]ProductPrice, error) {
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
			model: ClassroomModelV4GA, channelId: 15,
			official:   PriceComponents{Input: 3, CachedInput: 0.10, Output: 9},
			selling:    PriceComponents{Input: 1.8, CachedInput: 0.06, Output: 5.4},
			upstream:   PriceComponents{Input: 1.5, CachedInput: 0.05, Output: 4.5},
			expression: `tier("base", p * 3 + cr * 0.10 + c * 9)`,
			multiplier: 0.60,
		},
		{
			model: ClassroomModelV41, channelId: 15,
			official:   PriceComponents{Input: 2, CachedInput: 0.04, Output: 8},
			selling:    PriceComponents{Input: 1.2, CachedInput: 0.024, Output: 4.8},
			upstream:   PriceComponents{Input: 1, CachedInput: 0.02, Output: 4},
			expression: `weekday("Asia/Shanghai") >= 1 && weekday("Asia/Shanghai") <= 5 && ((hour("Asia/Shanghai") >= 9 && hour("Asia/Shanghai") < 12) || (hour("Asia/Shanghai") >= 14 && hour("Asia/Shanghai") < 18)) ? tier("peak", p * 2 + cr * 0.04 + c * 8) : tier("offpeak", p * 1 + cr * 0.02 + c * 4)`,
			multiplier: 0.60,
		},
		{
			model: ClassroomModelQwen, channelId: 14,
			official:   PriceComponents{Input: 0.80, CachedInput: 0.10, CacheCreation: 1.25, Output: 2.70},
			selling:    PriceComponents{Input: 0.80, CachedInput: 0.10, CacheCreation: 1.25, Output: 2.70},
			upstream:   PriceComponents{Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16},
			expression: `tier("base", p * 0.80 + cr * 0.10 + cc * 1.25 + c * 2.70)`,
			multiplier: 1,
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
			ProductType: ProductTypePackDeepSeekClassroom, Model: definition.model,
			PriceVersion: ClassroomPriceVersion, EffectiveAt: effectiveAt,
			OfficialReferencePrice: official, UserSellingPrice: selling,
			UpstreamSettlementPrice: upstream, BillingExpression: definition.expression,
			BillingMultiplier: definition.multiplier, AllowedChannels: channels, MaxUpstreamCost: upstream,
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
	walletPrices := make([]ProductPrice, 0, 2)
	for index := range prices {
		if prices[index].Model == ClassroomModelQwen {
			continue
		}
		prices[index].ProductType = ProductTypeWallet
		prices[index].PriceVersion = WalletDeepSeekPriceVersion
		prices[index].UserSellingPrice = prices[index].OfficialReferencePrice
		prices[index].BillingMultiplier = 1
		prices[index].FallbackAllowed = false
		prices[index].Enabled = true
		walletPrices = append(walletPrices, prices[index])
	}
	return walletPrices, nil
}

func classroomDesiredPlan(enabled bool, listed bool, totalAmount int64) (*SubscriptionPlan, error) {
	allowed, err := classroomJSON(map[string][]int{
		ClassroomModelV41: {15}, ClassroomModelV4GA: {15}, ClassroomModelQwen: {14},
	})
	if err != nil {
		return nil, err
	}
	costs, err := classroomJSON(map[string]PriceComponents{
		ClassroomModelV41:  {Input: 1, CachedInput: 0.02, Output: 4},
		ClassroomModelV4GA: {Input: 1.5, CachedInput: 0.05, Output: 4.5},
		ClassroomModelQwen: {Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16},
	})
	if err != nil {
		return nil, err
	}
	allowBalance := false
	allowWallet := false
	plan := &SubscriptionPlan{
		Title:       "IF.Learn 启动包",
		Subtitle:    "¥5 三款课堂模型调用额度 · 10,000 算力点 · 兑换后 10 天有效",
		PriceAmount: 5, Currency: "CNY", DurationUnit: SubscriptionDurationDay, DurationValue: 10,
		Enabled: enabled, Listed: listed, RedemptionEnabled: enabled, SortOrder: 1000,
		AllowBalancePay: &allowBalance, AllowWalletOverflow: &allowWallet,
		MaxPurchasePerUser: 1, TotalAmount: totalAmount, QuotaResetPeriod: SubscriptionResetNever,
		ModelScope: ClassroomModelQwen + "," + ClassroomModelV41 + "," + ClassroomModelV4GA,
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

func classroomLegacySnapshot() (string, string, string, error) {
	allowed, err := classroomJSON(map[string][]int{
		ClassroomModelV41: {15}, ClassroomModelV4GA: {15},
	})
	if err != nil {
		return "", "", "", err
	}
	costs, err := classroomJSON(map[string]PriceComponents{
		ClassroomModelV41:  {Input: 1, CachedInput: 0.02, Output: 4},
		ClassroomModelV4GA: {Input: 1.5, CachedInput: 0.05, Output: 4.5},
	})
	if err != nil {
		return "", "", "", err
	}
	return ClassroomModelV41 + "," + ClassroomModelV4GA, allowed, costs, nil
}

func validateClassroomSnapshot(version string, scope string, allowed string, costs string, multiplier float64, fallback bool, desired *SubscriptionPlan) error {
	if desired == nil {
		return errors.New("classroom desired plan is nil")
	}
	if math.Abs(multiplier-0.60) > 0.000001 || fallback {
		return errors.New("classroom entitlement has an unexpected billing or fallback snapshot")
	}
	expectedScope, expectedAllowed, expectedCosts := desired.ModelScope, desired.AllowedChannels, desired.MaxUpstreamCost
	switch strings.TrimSpace(version) {
	case ClassroomPriceVersion:
	case ClassroomLegacyPriceVersion:
		var err error
		expectedScope, expectedAllowed, expectedCosts, err = classroomLegacySnapshot()
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("classroom entitlement has unsupported price version %q", version)
	}
	if !sameStringSet(explicitModelIds(scope), explicitModelIds(expectedScope)) {
		return errors.New("classroom entitlement has an unexpected model scope")
	}
	actualPolicy, err := ParseProductRoutePolicy(allowed, costs, fallback)
	if err != nil {
		return err
	}
	expectedPolicy, err := ParseProductRoutePolicy(expectedAllowed, expectedCosts, false)
	if err != nil {
		return err
	}
	for _, modelName := range explicitModelIds(expectedScope) {
		if !sameChannelSet(actualPolicy.AllowedChannels[modelName], expectedPolicy.AllowedChannels[modelName]) ||
			!samePriceComponents(actualPolicy.MaxUpstreamCost[modelName], expectedPolicy.MaxUpstreamCost[modelName]) {
			return fmt.Errorf("classroom entitlement route snapshot differs for model %s", modelName)
		}
	}
	return nil
}

// migrateClassroomEntitlementsTx extends only the frozen routing/model metadata
// of the existing IF.Learn entitlement and its bound key. Quota, consumption,
// dates, status and key material are intentionally absent from the update map.
func migrateClassroomEntitlementsTx(tx *gorm.DB, planId int, desired *SubscriptionPlan) (int64, int64, error) {
	if tx == nil || planId <= 0 || desired == nil {
		return 0, 0, errors.New("invalid classroom entitlement migration arguments")
	}
	var subscriptions []UserSubscription
	if err := tx.Where("plan_id = ? OR (product_type = ? AND product_code = ?)", planId, ProductTypePackDeepSeekClassroom, ClassroomProductCode).
		Order("id asc").Find(&subscriptions).Error; err != nil {
		return 0, 0, err
	}
	var migratedSubscriptions int64
	var migratedTokens int64
	for index := range subscriptions {
		sub := &subscriptions[index]
		if sub.ProductType != ProductTypePackDeepSeekClassroom || sub.ProductCode != ClassroomProductCode || sub.ProductFamily != "deepseek_classroom" {
			return 0, 0, fmt.Errorf("subscription %d does not match the IF.Learn product identity", sub.Id)
		}
		if err := validateClassroomSnapshot(sub.PriceVersion, sub.ModelScope, sub.AllowedChannels, sub.MaxUpstreamCost, sub.BillingMultiplier, sub.FallbackAllowed, desired); err != nil {
			return 0, 0, fmt.Errorf("subscription %d cannot be migrated safely: %w", sub.Id, err)
		}
		if sub.PriceVersion != ClassroomPriceVersion {
			if err := tx.Model(&UserSubscription{}).Where("id = ?", sub.Id).Updates(map[string]interface{}{
				"model_scope": desired.ModelScope, "price_version": desired.PriceVersion,
				"allowed_channels": desired.AllowedChannels, "max_upstream_cost": desired.MaxUpstreamCost,
				"fallback_allowed": false, "updated_at": common.GetTimestamp(),
			}).Error; err != nil {
				return 0, 0, err
			}
			migratedSubscriptions++
		}

		var tokens []Token
		if err := tx.Unscoped().Where("user_subscription_id = ?", sub.Id).Order("id asc").Find(&tokens).Error; err != nil {
			return 0, 0, err
		}
		for tokenIndex := range tokens {
			token := &tokens[tokenIndex]
			if token.ProductType != ProductTypePackDeepSeekClassroom || token.EntitlementType != ProductTypePackDeepSeekClassroom {
				return 0, 0, fmt.Errorf("token %d does not match the IF.Learn entitlement identity", token.Id)
			}
			if err := validateClassroomSnapshot(token.PriceVersion, token.ModelLimits, token.AllowedChannels, token.MaxUpstreamCost, token.BillingMultiplier, token.FallbackAllowed, desired); err != nil {
				return 0, 0, fmt.Errorf("token %d cannot be migrated safely: %w", token.Id, err)
			}
			if token.PriceVersion == ClassroomPriceVersion {
				continue
			}
			if err := tx.Unscoped().Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]interface{}{
				"model_limits_enabled": true, "model_limits": desired.ModelScope,
				"price_version": desired.PriceVersion, "allowed_channels": desired.AllowedChannels,
				"max_upstream_cost": desired.MaxUpstreamCost, "fallback_allowed": false,
			}).Error; err != nil {
				return 0, 0, err
			}
			migratedTokens++
		}
	}
	return migratedSubscriptions, migratedTokens, nil
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
	var deepSeekChannel Channel
	if err := DB.Where("id = ?", 15).First(&deepSeekChannel).Error; err != nil {
		return nil, fmt.Errorf("channel 15 is not configured: %w", err)
	}
	if deepSeekChannel.Status != common.ChannelStatusEnabled {
		return nil, errors.New("channel 15 is disabled")
	}
	channelModels := map[string]struct{}{}
	for _, name := range deepSeekChannel.GetModels() {
		channelModels[strings.TrimSpace(name)] = struct{}{}
	}
	for _, name := range []string{ClassroomModelV41, ClassroomModelV4GA} {
		if _, ok := channelModels[name]; !ok {
			return nil, fmt.Errorf("channel 15 does not expose %s", name)
		}
	}
	var qwenChannel Channel
	if err := DB.Where("id = ?", 14).First(&qwenChannel).Error; err != nil {
		return nil, fmt.Errorf("channel 14 is not configured: %w", err)
	}
	if qwenChannel.Status != common.ChannelStatusEnabled {
		return nil, errors.New("channel 14 is disabled")
	}
	qwenModels := map[string]struct{}{}
	for _, name := range qwenChannel.GetModels() {
		qwenModels[strings.TrimSpace(name)] = struct{}{}
	}
	if _, ok := qwenModels[ClassroomModelQwen]; !ok {
		return nil, fmt.Errorf("channel 14 does not expose %s", ClassroomModelQwen)
	}
	result := &ClassroomBootstrapResult{
		Applied: apply, Enabled: enabled, Listed: listed, ChannelId: deepSeekChannel.Id, ChannelName: deepSeekChannel.Name,
		ProductCode: ClassroomProductCode, PriceVersion: ClassroomPriceVersion,
		WalletPriceVersion: WalletDeepSeekPriceVersion,
		TotalAmount:        int64(quota), QuotaPerUnit: common.QuotaPerUnit,
		Models: []string{ClassroomModelQwen, ClassroomModelV4GA, ClassroomModelV41},
	}
	if !apply {
		return result, nil
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		costs := map[string]PriceComponents{}
		if strings.TrimSpace(deepSeekChannel.UpstreamCosts) != "" {
			if err := common.UnmarshalJsonStr(deepSeekChannel.UpstreamCosts, &costs); err != nil {
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
		qwenCosts := map[string]PriceComponents{}
		if strings.TrimSpace(qwenChannel.UpstreamCosts) != "" {
			if err := common.UnmarshalJsonStr(qwenChannel.UpstreamCosts, &qwenCosts); err != nil {
				return err
			}
		}
		qwenCosts[ClassroomModelQwen] = PriceComponents{Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16}
		encodedQwenCosts, err := classroomJSON(qwenCosts)
		if err != nil {
			return err
		}
		if err := tx.Model(&Channel{}).Where("id = ?", 14).Update("upstream_costs", encodedQwenCosts).Error; err != nil {
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
		// GORM applies `default:true` to zero-valued bools during INSERT, so a
		// newly-created hidden/disabled SKU must have its switches enforced with
		// an explicit map update. This also keeps re-bootstrap behavior exact.
		if err := tx.Model(&SubscriptionPlan{}).Where("id = ?", result.PlanId).Updates(map[string]interface{}{
			"enabled": enabled, "listed": listed, "redemption_enabled": enabled,
		}).Error; err != nil {
			return err
		}
		migratedSubscriptions, migratedTokens, err := migrateClassroomEntitlementsTx(tx, result.PlanId, plan)
		if err != nil {
			return err
		}
		result.MigratedSubscriptions = migratedSubscriptions
		result.MigratedTokens = migratedTokens
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

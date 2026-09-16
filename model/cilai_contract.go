package model

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"gorm.io/gorm"
)

const (
	ProductTypeWallet              = "wallet"
	ProductTypeMonthlySubscription = "monthly_subscription"
	ProductTypePackDSGLM           = "pack_ds_glm"
	ProductTypePackQwen            = "pack_qwen"
	ProductTypePackK3              = "pack_k3"
	ProductTypeActivationCredit    = "activation_credit"
	ProductTypeLegacySubscription  = "legacy_subscription"

	EntitlementTypeWallet              = ProductTypeWallet
	EntitlementTypeMonthlySubscription = ProductTypeMonthlySubscription
	EntitlementTypePackDSGLM           = ProductTypePackDSGLM
	EntitlementTypePackQwen            = ProductTypePackQwen
	EntitlementTypePackK3              = ProductTypePackK3
	EntitlementTypeActivationCredit    = ProductTypeActivationCredit

	SubscriptionOrderTypePurchase = "purchase"
	SubscriptionOrderTypeUpgrade  = "upgrade"

	ProvisioningStatusPending     = "pending"
	ProvisioningStatusProvisioned = "provisioned"
	ProvisioningStatusFailed      = "failed"

	CourseCodeV1Foundation = "cilai_v1_foundation_first_term"
)

var frozenV1BillingMultipliers = map[string]float64{
	ProductTypeWallet:              1.00,
	ProductTypeMonthlySubscription: 0.85,
	ProductTypePackDSGLM:           0.70,
	ProductTypePackQwen:            0.75,
	ProductTypePackK3:              0.95,
}

type PriceComponents struct {
	Input       float64 `json:"input"`
	CachedInput float64 `json:"cached_input"`
	Output      float64 `json:"output"`
}

type ProductRoutePolicy struct {
	AllowedChannels map[string][]int           `json:"allowed_channels"`
	MaxUpstreamCost map[string]PriceComponents `json:"max_upstream_cost"`
	FallbackAllowed bool                       `json:"fallback_allowed"`
}

func ParseProductRoutePolicy(allowedChannels string, maxUpstreamCost string, fallbackAllowed bool) (*ProductRoutePolicy, error) {
	policy := &ProductRoutePolicy{
		AllowedChannels: map[string][]int{},
		MaxUpstreamCost: map[string]PriceComponents{},
		FallbackAllowed: fallbackAllowed,
	}
	if err := common.UnmarshalJsonStr(strings.TrimSpace(allowedChannels), &policy.AllowedChannels); err != nil {
		return nil, fmt.Errorf("invalid allowed_channels: %w", err)
	}
	if err := common.UnmarshalJsonStr(strings.TrimSpace(maxUpstreamCost), &policy.MaxUpstreamCost); err != nil {
		return nil, fmt.Errorf("invalid max_upstream_cost: %w", err)
	}
	return policy, nil
}

func explicitModelIds(scope string) []string {
	seen := map[string]struct{}{}
	models := make([]string, 0)
	for _, value := range strings.Split(scope, ",") {
		modelName := strings.TrimSpace(value)
		if modelName == "" {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		models = append(models, modelName)
	}
	sort.Strings(models)
	return models
}

func validPositivePriceComponents(value PriceComponents) bool {
	return value.Input > 0 && value.Output > 0 && value.CachedInput >= 0 &&
		!math.IsNaN(value.Input) && !math.IsNaN(value.CachedInput) && !math.IsNaN(value.Output) &&
		!math.IsInf(value.Input, 0) && !math.IsInf(value.CachedInput, 0) && !math.IsInf(value.Output, 0)
}

func IsV1ProductType(productType string) bool {
	productType = strings.TrimSpace(productType)
	_, ok := frozenV1BillingMultipliers[productType]
	return ok || productType == ProductTypeActivationCredit
}

func IsSubscriptionFundedProduct(productType string) bool {
	productType = strings.TrimSpace(productType)
	return productType != "" && productType != ProductTypeWallet
}

func RequiresStrictV1RoutePolicy(productType string) bool {
	switch strings.TrimSpace(productType) {
	case ProductTypeMonthlySubscription, ProductTypePackDSGLM, ProductTypePackQwen, ProductTypePackK3:
		return true
	default:
		return false
	}
}

func ExpectedV1BillingMultiplier(productType string) (float64, bool) {
	multiplier, ok := frozenV1BillingMultipliers[strings.TrimSpace(productType)]
	return multiplier, ok
}

// ValidateV1Contract validates only plans that opt in to the frozen CILAI V1
// contract by setting product_type. Legacy upstream plans remain readable, but
// cannot accidentally masquerade as a V1 product.
func (p *SubscriptionPlan) ValidateV1Contract() error {
	if p == nil {
		return errors.New("plan is nil")
	}
	p.ProductType = strings.TrimSpace(p.ProductType)
	if p.ProductType == "" {
		return nil
	}
	if !IsV1ProductType(p.ProductType) {
		return fmt.Errorf("unsupported product_type %q", p.ProductType)
	}
	if strings.TrimSpace(p.ProductCode) == "" {
		return errors.New("product_code is required for a V1 product")
	}
	if strings.TrimSpace(p.ProductFamily) == "" {
		return errors.New("product_family is required for a V1 product")
	}
	if strings.TrimSpace(p.PriceVersion) == "" {
		return errors.New("price_version is required for a V1 product")
	}
	if p.Currency != "CNY" {
		return errors.New("V1 products must use CNY")
	}
	if p.DurationUnit != SubscriptionDurationDay || p.DurationValue != 30 {
		return errors.New("V1 subscription and pack products must have a fixed 30-day duration")
	}
	if strings.TrimSpace(p.ModelScope) == "" {
		return errors.New("V1 products require an explicit allowed_models list")
	}
	for _, modelName := range explicitModelIds(p.ModelScope) {
		if strings.ContainsAny(modelName, "*?") {
			return fmt.Errorf("allowed_models must use exact model IDs, got %q", modelName)
		}
	}
	if strings.TrimSpace(p.AllowedChannels) == "" {
		return errors.New("V1 products require an allowed_channels policy snapshot")
	}
	if strings.TrimSpace(p.MaxUpstreamCost) == "" {
		return errors.New("V1 products require a max_upstream_cost policy snapshot")
	}
	if p.FallbackAllowed {
		return errors.New("V1 products must fail closed and cannot enable route fallback")
	}
	policy, err := ParseProductRoutePolicy(p.AllowedChannels, p.MaxUpstreamCost, p.FallbackAllowed)
	if err != nil {
		return err
	}
	for _, modelName := range explicitModelIds(p.ModelScope) {
		channels := policy.AllowedChannels[modelName]
		if len(channels) == 0 {
			return fmt.Errorf("allowed_channels must explicitly configure model %q", modelName)
		}
		seenChannels := make(map[int]struct{}, len(channels))
		for _, channelId := range channels {
			if channelId <= 0 {
				return fmt.Errorf("allowed_channels for model %q contains invalid channel id", modelName)
			}
			if _, exists := seenChannels[channelId]; exists {
				return fmt.Errorf("allowed_channels for model %q contains duplicate channel id %d", modelName, channelId)
			}
			seenChannels[channelId] = struct{}{}
		}
		cost, ok := policy.MaxUpstreamCost[modelName]
		if !ok || !validPositivePriceComponents(cost) {
			return fmt.Errorf("max_upstream_cost must explicitly configure positive input/output costs for model %q", modelName)
		}
	}
	if expected, ok := ExpectedV1BillingMultiplier(p.ProductType); ok && math.Abs(p.BillingMultiplier-expected) > 0.000001 {
		return fmt.Errorf("billing_multiplier for %s must be %.2f", p.ProductType, expected)
	}
	if p.AllowWalletOverflow == nil || *p.AllowWalletOverflow {
		return errors.New("V1 products must disable wallet overflow")
	}
	if p.ProductType == ProductTypeMonthlySubscription {
		if p.Category != SubscriptionCategorySubscription {
			return errors.New("monthly_subscription must use subscription category")
		}
		if p.ProductTier <= 0 {
			return errors.New("monthly_subscription requires a positive product_tier")
		}
		expectedPrice := map[int]float64{1: 19.9, 2: 49.9, 3: 99.9}[p.ProductTier]
		if expectedPrice == 0 || math.Abs(p.PriceAmount-expectedPrice) > 0.000001 {
			return errors.New("monthly_subscription tier/price must be 1=19.9, 2=49.9, or 3=99.9")
		}
		if p.CourseEligible != (p.ProductTier >= 2) {
			return errors.New("monthly course eligibility is required for tiers 2/3 and forbidden for tier 1")
		}
	} else if p.Category != SubscriptionCategoryPackage {
		return errors.New("V1 packs must use package category")
	} else {
		if p.CourseEligible {
			return errors.New("course eligibility is only available on monthly tiers 2/3")
		}
		switch p.ProductType {
		case ProductTypePackDSGLM, ProductTypePackQwen:
			if math.Abs(p.PriceAmount-19.9) > 0.000001 {
				return fmt.Errorf("%s price must be 19.9", p.ProductType)
			}
		case ProductTypePackK3:
			if math.Abs(p.PriceAmount-49.9) > 0.000001 && math.Abs(p.PriceAmount-99.9) > 0.000001 && math.Abs(p.PriceAmount-199.9) > 0.000001 {
				return errors.New("pack_k3 price must be 49.9, 99.9, or 199.9")
			}
		}
	}
	if NormalizeResetPeriod(p.QuotaResetPeriod) != SubscriptionResetNever {
		return errors.New("V1 products cannot reset quota inside their 30-day entitlement")
	}
	expectedQuota, err := common.QuotaFromFloatStrict(p.PriceAmount * common.QuotaPerUnit)
	if err != nil {
		return fmt.Errorf("invalid V1 face-value quota: %w", err)
	}
	if p.TotalAmount != int64(expectedQuota) {
		return fmt.Errorf("V1 total_amount must equal the paid face value (%d quota units)", expectedQuota)
	}
	return nil
}

// ProductPrice stores versioned, configurable product/model pricing and cost
// data. Price component fields contain JSON objects so input, cached input,
// output, context tiers, and time windows can evolve as data without code rules.
type ProductPrice struct {
	Id int `json:"id"`

	ProductType  string `json:"product_type" gorm:"type:varchar(32);not null;uniqueIndex:idx_product_price_version,priority:1"`
	Model        string `json:"model" gorm:"type:varchar(255);not null;uniqueIndex:idx_product_price_version,priority:2"`
	PriceVersion string `json:"price_version" gorm:"type:varchar(64);not null;uniqueIndex:idx_product_price_version,priority:3"`
	EffectiveAt  int64  `json:"effective_at" gorm:"bigint;index"`

	OfficialReferencePrice  string  `json:"official_reference_price" gorm:"type:text;not null"`
	UserSellingPrice        string  `json:"user_selling_price" gorm:"type:text;not null"`
	UpstreamSettlementPrice string  `json:"upstream_settlement_price" gorm:"type:text;not null"`
	BillingExpression       string  `json:"billing_expression" gorm:"type:text"`
	BillingMultiplier       float64 `json:"billing_multiplier" gorm:"type:decimal(8,6);not null"`
	AllowedChannels         string  `json:"allowed_channels" gorm:"type:text;not null"`
	MaxUpstreamCost         string  `json:"max_upstream_cost" gorm:"type:text;not null"`
	FallbackAllowed         bool    `json:"fallback_allowed"`
	Enabled                 bool    `json:"enabled"`
	CreatedAt               int64   `json:"created_at" gorm:"bigint"`
	UpdatedAt               int64   `json:"updated_at" gorm:"bigint"`
}

func (p *ProductPrice) ValidateV1() error {
	if p == nil {
		return errors.New("product price is nil")
	}
	p.ProductType = strings.TrimSpace(p.ProductType)
	p.Model = strings.TrimSpace(p.Model)
	p.PriceVersion = strings.TrimSpace(p.PriceVersion)
	if !RequiresStrictV1RoutePolicy(p.ProductType) || p.Model == "" || p.PriceVersion == "" {
		return errors.New("product_type, exact model, and price_version are required for a V1 product price")
	}
	if strings.ContainsAny(p.Model, "*?") {
		return errors.New("product price model must be an exact model ID")
	}
	if p.FallbackAllowed {
		return errors.New("V1 product prices must fail closed and cannot enable route fallback")
	}
	expectedMultiplier, ok := ExpectedV1BillingMultiplier(p.ProductType)
	if !ok || math.Abs(expectedMultiplier-p.BillingMultiplier) > 0.000001 {
		return fmt.Errorf("product price billing_multiplier for %s must be %.2f", p.ProductType, expectedMultiplier)
	}
	for field, raw := range map[string]string{
		"official_reference_price":  p.OfficialReferencePrice,
		"user_selling_price":        p.UserSellingPrice,
		"upstream_settlement_price": p.UpstreamSettlementPrice,
	} {
		var components PriceComponents
		if err := common.UnmarshalJsonStr(strings.TrimSpace(raw), &components); err != nil || !validPositivePriceComponents(components) {
			return fmt.Errorf("%s must contain positive input/output and non-negative cached_input components", field)
		}
	}
	if strings.TrimSpace(p.BillingExpression) == "" {
		return errors.New("billing_expression is required for V1 product pricing")
	}
	if _, err := billingexpr.CompileFromCache(p.BillingExpression); err != nil {
		return fmt.Errorf("invalid billing_expression: %w", err)
	}
	var channelIds []int
	if err := common.UnmarshalJsonStr(strings.TrimSpace(p.AllowedChannels), &channelIds); err != nil || len(channelIds) == 0 {
		return errors.New("allowed_channels must be a non-empty JSON channel ID list")
	}
	seen := map[int]struct{}{}
	for _, channelId := range channelIds {
		if channelId <= 0 {
			return errors.New("allowed_channels contains an invalid channel ID")
		}
		if _, exists := seen[channelId]; exists {
			return errors.New("allowed_channels contains a duplicate channel ID")
		}
		seen[channelId] = struct{}{}
	}
	var cost PriceComponents
	if err := common.UnmarshalJsonStr(strings.TrimSpace(p.MaxUpstreamCost), &cost); err != nil || !validPositivePriceComponents(cost) {
		return errors.New("max_upstream_cost must contain valid price components")
	}
	var upstream PriceComponents
	if err := common.UnmarshalJsonStr(strings.TrimSpace(p.UpstreamSettlementPrice), &upstream); err != nil {
		return errors.New("upstream_settlement_price must contain valid price components")
	}
	if upstream.Input > cost.Input || upstream.CachedInput > cost.CachedInput || upstream.Output > cost.Output {
		return errors.New("upstream_settlement_price exceeds max_upstream_cost")
	}
	return nil
}

func (p *ProductPrice) BeforeCreate(tx *gorm.DB) error {
	if err := p.ValidateV1(); err != nil {
		return err
	}
	now := common.GetTimestamp()
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

func (p *ProductPrice) BeforeUpdate(tx *gorm.DB) error {
	var existing ProductPrice
	if p.Id <= 0 || tx.Session(&gorm.Session{SkipHooks: true}).Where("id = ?", p.Id).First(&existing).Error != nil {
		return errors.New("cannot verify immutable product price")
	}
	if existing.ProductType != p.ProductType || existing.Model != p.Model || existing.PriceVersion != p.PriceVersion ||
		existing.EffectiveAt != p.EffectiveAt || existing.OfficialReferencePrice != p.OfficialReferencePrice ||
		existing.UserSellingPrice != p.UserSellingPrice || existing.UpstreamSettlementPrice != p.UpstreamSettlementPrice ||
		existing.BillingExpression != p.BillingExpression || math.Abs(existing.BillingMultiplier-p.BillingMultiplier) > 0.000001 ||
		existing.AllowedChannels != p.AllowedChannels || existing.MaxUpstreamCost != p.MaxUpstreamCost ||
		existing.FallbackAllowed != p.FallbackAllowed {
		return errors.New("product price versions are immutable; create a new price_version")
	}
	if err := p.ValidateV1(); err != nil {
		return err
	}
	p.UpdatedAt = common.GetTimestamp()
	return nil
}

func GetProductPriceSnapshot(productType string, modelName string, priceVersion string) (*ProductPrice, error) {
	var price ProductPrice
	if err := DB.Where("product_type = ? AND model = ? AND price_version = ?", strings.TrimSpace(productType), strings.TrimSpace(modelName), strings.TrimSpace(priceVersion)).
		First(&price).Error; err != nil {
		return nil, fmt.Errorf("product price is not configured: %w", err)
	}
	// Enabled is a sale switch checked while creating a new order. Existing
	// paid entitlements must keep settling against their immutable historical
	// price version after a SKU is taken off sale.
	if err := price.ValidateV1(); err != nil {
		return nil, err
	}
	return &price, nil
}

func validateV1ProductPriceCatalogTx(tx *gorm.DB, plan *SubscriptionPlan) error {
	if tx == nil || plan == nil || !RequiresStrictV1RoutePolicy(plan.ProductType) {
		return nil
	}
	for _, modelName := range explicitModelIds(plan.ModelScope) {
		var price ProductPrice
		if err := tx.Where("product_type = ? AND model = ? AND price_version = ?", plan.ProductType, modelName, plan.PriceVersion).
			First(&price).Error; err != nil {
			return fmt.Errorf("cannot sell %s: product price for model %s version %s is not configured", plan.ProductCode, modelName, plan.PriceVersion)
		}
		if !price.Enabled {
			return fmt.Errorf("cannot sell %s: product price for model %s is disabled", plan.ProductCode, modelName)
		}
		if err := price.ValidateV1(); err != nil {
			return fmt.Errorf("cannot sell %s: %w", plan.ProductCode, err)
		}
		if math.Abs(price.BillingMultiplier-plan.BillingMultiplier) > 0.000001 {
			return fmt.Errorf("cannot sell %s: billing multiplier mismatch for model %s", plan.ProductCode, modelName)
		}
		policy, err := ParseProductRoutePolicy(plan.AllowedChannels, plan.MaxUpstreamCost, plan.FallbackAllowed)
		if err != nil {
			return fmt.Errorf("cannot sell %s: %w", plan.ProductCode, err)
		}
		var priceChannels []int
		if err := common.UnmarshalJsonStr(price.AllowedChannels, &priceChannels); err != nil {
			return fmt.Errorf("cannot sell %s: invalid product price channel policy for model %s", plan.ProductCode, modelName)
		}
		if !sameChannelSet(policy.AllowedChannels[modelName], priceChannels) {
			return fmt.Errorf("cannot sell %s: allowed channel snapshot mismatch for model %s", plan.ProductCode, modelName)
		}
		var priceCost PriceComponents
		if err := common.UnmarshalJsonStr(price.MaxUpstreamCost, &priceCost); err != nil {
			return fmt.Errorf("cannot sell %s: invalid product price cost policy for model %s", plan.ProductCode, modelName)
		}
		if !samePriceComponents(policy.MaxUpstreamCost[modelName], priceCost) || price.FallbackAllowed != plan.FallbackAllowed {
			return fmt.Errorf("cannot sell %s: upstream cost policy snapshot mismatch for model %s", plan.ProductCode, modelName)
		}
	}
	return nil
}

func sameChannelSet(left []int, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[int]struct{}, len(left))
	for _, id := range left {
		values[id] = struct{}{}
	}
	for _, id := range right {
		if _, ok := values[id]; !ok {
			return false
		}
	}
	return true
}

func samePriceComponents(left PriceComponents, right PriceComponents) bool {
	return math.Abs(left.Input-right.Input) <= 0.000001 &&
		math.Abs(left.CachedInput-right.CachedInput) <= 0.000001 &&
		math.Abs(left.Output-right.Output) <= 0.000001
}

// SubscriptionKeyBinding is the cross-database-safe one-to-one constraint
// between a paid entitlement instance and its API key. Wallet keys intentionally
// do not appear here because multiple keys may address the single wallet pool.
type SubscriptionKeyBinding struct {
	Id                 int   `json:"id"`
	UserId             int   `json:"user_id" gorm:"not null;index"`
	UserSubscriptionId int   `json:"user_subscription_id" gorm:"not null;uniqueIndex"`
	TokenId            int   `json:"token_id" gorm:"not null;uniqueIndex"`
	CreatedAt          int64 `json:"created_at" gorm:"bigint"`

	UserSubscription UserSubscription `json:"-" gorm:"foreignKey:UserSubscriptionId;references:Id;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
	Token            Token            `json:"-" gorm:"foreignKey:TokenId;references:Id;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (b *SubscriptionKeyBinding) BeforeCreate(tx *gorm.DB) error {
	b.CreatedAt = common.GetTimestamp()
	return nil
}

// CourseEntitlement is deliberately small for V1. Its unique user/course key
// guarantees that the first-term course is granted once and survives token or
// subscription expiry.
type CourseEntitlement struct {
	Id                 int    `json:"id"`
	UserId             int    `json:"user_id" gorm:"not null;uniqueIndex:idx_user_course,priority:1"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"not null;index"`
	CourseCode         string `json:"course_code" gorm:"type:varchar(64);not null;uniqueIndex:idx_user_course,priority:2"`
	Source             string `json:"source" gorm:"type:varchar(32);not null"`
	GrantedAt          int64  `json:"granted_at" gorm:"bigint;not null"`
	Status             string `json:"status" gorm:"type:varchar(16);not null"`

	UserSubscription UserSubscription `json:"-" gorm:"foreignKey:UserSubscriptionId;references:Id;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

// MonthlySubscriptionCycle serializes the shared 30-day purchase window for
// all tiers in one monthly product family. The unique user/family key is stable
// across renewals; the row points at the currently active period instance.
type MonthlySubscriptionCycle struct {
	Id             int    `json:"id"`
	UserId         int    `json:"user_id" gorm:"not null;uniqueIndex:idx_user_monthly_family,priority:1"`
	ProductFamily  string `json:"product_family" gorm:"type:varchar(64);not null;uniqueIndex:idx_user_monthly_family,priority:2"`
	PeriodId       string `json:"period_id" gorm:"type:varchar(64);not null;uniqueIndex"`
	SubscriptionId int    `json:"subscription_id" gorm:"not null;index"`
	StartedAt      int64  `json:"started_at" gorm:"bigint;not null"`
	ExpiresAt      int64  `json:"expires_at" gorm:"bigint;not null;index"`
	CurrentTier    int    `json:"current_tier" gorm:"not null"`
	UpgradeHistory string `json:"upgrade_history" gorm:"type:text"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt      int64  `json:"updated_at" gorm:"bigint"`
}

func (c *MonthlySubscriptionCycle) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	c.CreatedAt = now
	c.UpdatedAt = now
	return nil
}

func (c *MonthlySubscriptionCycle) BeforeUpdate(tx *gorm.DB) error {
	c.UpdatedAt = common.GetTimestamp()
	return nil
}

// MigrateCilaiV1Contracts performs only deterministic metadata backfills. It
// never guesses a V1 product for an unclassified legacy plan. Existing bound
// subscriptions are made one-to-one; conflicting duplicate keys abort the
// migration for explicit operator review instead of deleting user data.
func MigrateCilaiV1Contracts() error {
	if DB == nil {
		return errors.New("database is nil")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Token{}).
			Where("user_subscription_id = 0 AND (entitlement_type = '' OR entitlement_type IS NULL)").
			Updates(map[string]interface{}{
				"entitlement_type":   ProductTypeWallet,
				"product_type":       ProductTypeWallet,
				"billing_multiplier": 1.0,
			}).Error; err != nil {
			return err
		}

		var orders []SubscriptionOrder
		if err := tx.Where("product_type <> ''").Order("id asc").Find(&orders).Error; err != nil {
			return err
		}
		for i := range orders {
			order := &orders[i]
			if order.TargetPriceAmount > 0 && strings.TrimSpace(order.CurrencySnapshot) != "" && order.TotalAmountSnapshot > 0 {
				continue
			}
			var plan SubscriptionPlan
			if err := tx.Where("id = ?", order.PlanId).First(&plan).Error; err != nil {
				return fmt.Errorf("cannot backfill V1 order #%d snapshot: %w", order.Id, err)
			}
			order.SnapshotPlan(&plan)
			if err := tx.Save(order).Error; err != nil {
				return err
			}
		}

		var subscriptions []UserSubscription
		if err := tx.Order("id asc").Find(&subscriptions).Error; err != nil {
			return err
		}
		for i := range subscriptions {
			sub := &subscriptions[i]
			if sub.ProductType == "" {
				var plan SubscriptionPlan
				if err := tx.Where("id = ?", sub.PlanId).First(&plan).Error; err == nil && plan.ProductType != "" {
					sub.ProductType = plan.ProductType
					sub.ProductCode = plan.ProductCode
					sub.ProductFamily = plan.ProductFamily
					sub.ProductTier = plan.ProductTier
					sub.BillingMultiplier = plan.BillingMultiplier
					sub.PriceVersion = plan.PriceVersion
					sub.ModelScope = plan.ModelScope
					sub.AllowedChannels = plan.AllowedChannels
					sub.MaxUpstreamCost = plan.MaxUpstreamCost
					sub.FallbackAllowed = plan.FallbackAllowed
					sub.CourseEligible = plan.CourseEligible
					sub.PriceAmountSnapshot = plan.PriceAmount
					sub.CurrencySnapshot = plan.Currency
					if err := tx.Save(sub).Error; err != nil {
						return err
					}
				}
			} else if sub.PriceAmountSnapshot <= 0 || strings.TrimSpace(sub.CurrencySnapshot) == "" {
				var plan SubscriptionPlan
				if err := tx.Where("id = ?", sub.PlanId).First(&plan).Error; err == nil {
					sub.PriceAmountSnapshot = plan.PriceAmount
					sub.CurrencySnapshot = plan.Currency
					if err := tx.Save(sub).Error; err != nil {
						return err
					}
				}
			}
			if sub.CourseEligible {
				if err := grantV1CourseEntitlementTx(tx, sub, "migration"); err != nil {
					return err
				}
			}

			var tokens []Token
			if err := tx.Where("user_subscription_id = ?", sub.Id).Order("id asc").Find(&tokens).Error; err != nil {
				return err
			}
			if len(tokens) > 1 {
				return fmt.Errorf("subscription #%d has %d active keys; resolve before V1 migration", sub.Id, len(tokens))
			}
			if len(tokens) == 0 {
				if sub.Status == "active" && sub.EndTime > getDBTimestampTx(tx) {
					if _, _, err := createBoundTokenForSubscriptionTx(tx, sub.UserId, sub.Id); err != nil {
						return err
					}
				}
				continue
			}

			token := &tokens[0]
			productType := sub.ProductType
			if productType == "" {
				productType = ProductTypeLegacySubscription
			}
			multiplier := sub.BillingMultiplier
			if multiplier <= 0 {
				multiplier = 1
			}
			if err := tx.Model(token).Updates(map[string]interface{}{
				"entitlement_type":   productType,
				"product_type":       productType,
				"billing_multiplier": multiplier,
				"price_version":      sub.PriceVersion,
				"allowed_channels":   sub.AllowedChannels,
				"max_upstream_cost":  sub.MaxUpstreamCost,
				"fallback_allowed":   sub.FallbackAllowed,
			}).Error; err != nil {
				return err
			}
			binding := SubscriptionKeyBinding{
				UserId:             sub.UserId,
				UserSubscriptionId: sub.Id,
				TokenId:            token.Id,
			}
			if err := tx.Where("user_subscription_id = ?", sub.Id).FirstOrCreate(&binding).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

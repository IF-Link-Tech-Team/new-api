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
	ProductTypeWallet                = "wallet"
	ProductTypeMonthlySubscription   = "monthly_subscription"
	ProductTypePackDSGLM             = "pack_ds_glm"
	ProductTypePackQwen              = "pack_qwen"
	ProductTypePackK3                = "pack_k3"
	ProductTypePackDeepSeekClassroom = "pack_deepseek_classroom"
	ProductTypePackDeepSeekBigfish   = "pack_deepseek_bigfish"
	ProductTypeActivationCredit      = "activation_credit"
	ProductTypeLegacySubscription    = "legacy_subscription"

	EntitlementTypeWallet                = ProductTypeWallet
	EntitlementTypeMonthlySubscription   = ProductTypeMonthlySubscription
	EntitlementTypePackDSGLM             = ProductTypePackDSGLM
	EntitlementTypePackQwen              = ProductTypePackQwen
	EntitlementTypePackK3                = ProductTypePackK3
	EntitlementTypePackDeepSeekClassroom = ProductTypePackDeepSeekClassroom
	EntitlementTypeActivationCredit      = ProductTypeActivationCredit

	SubscriptionOrderTypePurchase = "purchase"
	SubscriptionOrderTypeUpgrade  = "upgrade"

	ProvisioningStatusPending      = "pending"
	ProvisioningStatusProvisioned  = "provisioned"
	ProvisioningStatusFailed       = "failed"
	ProvisioningStatusLegacyReview = "legacy_review"

	LegacyOrderStateNone   = ""
	LegacyOrderStateReview = "legacy_review"
	LegacyOrderStateReady  = "legacy_ready"

	CourseCodeV1Foundation = "cilai_v1_foundation_first_term"
)

var frozenV1BillingMultipliers = map[string]float64{
	ProductTypeWallet:                1.00,
	ProductTypeMonthlySubscription:   0.85,
	ProductTypePackDSGLM:             0.70,
	ProductTypePackQwen:              0.75,
	ProductTypePackK3:                0.95,
	ProductTypePackDeepSeekClassroom: 0.60,
	ProductTypePackDeepSeekBigfish:   0.60,
}

type PriceComponents struct {
	Input         float64 `json:"input"`
	CachedInput   float64 `json:"cached_input"`
	CacheCreation float64 `json:"cache_creation,omitempty"`
	Output        float64 `json:"output"`
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
	return value.Input > 0 && value.Output > 0 && value.CachedInput >= 0 && value.CacheCreation >= 0 &&
		!math.IsNaN(value.Input) && !math.IsNaN(value.CachedInput) && !math.IsNaN(value.CacheCreation) && !math.IsNaN(value.Output) &&
		!math.IsInf(value.Input, 0) && !math.IsInf(value.CachedInput, 0) && !math.IsInf(value.CacheCreation, 0) && !math.IsInf(value.Output, 0)
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
	case ProductTypeMonthlySubscription, ProductTypePackDSGLM, ProductTypePackQwen, ProductTypePackK3, ProductTypePackDeepSeekClassroom, ProductTypePackDeepSeekBigfish:
		return true
	default:
		return false
	}
}

// UsesVersionedProductPricing identifies products that can settle from the
// immutable product_type + model + price_version catalog. Wallet requests may
// opt into this catalog per request while legacy wallet installations remain
// readable until their V1 price rows are configured.
func UsesVersionedProductPricing(productType string) bool {
	return strings.TrimSpace(productType) == ProductTypeWallet || RequiresStrictV1RoutePolicy(productType)
}

func ExpectedV1BillingMultiplier(productType string) (float64, bool) {
	multiplier, ok := frozenV1BillingMultipliers[strings.TrimSpace(productType)]
	return multiplier, ok
}

// ExpectedProductPriceBillingMultiplier keeps the plan-level multiplier as a
// backwards-compatible entitlement default while allowing an immutable price
// version to define a different multiplier for one exact model. Current
// IF.Learn and unified monthly versions both use mixed model-level pricing.
func ExpectedProductPriceBillingMultiplier(productType string, modelName string, priceVersion ...string) (float64, bool) {
	version := ""
	if len(priceVersion) > 0 {
		version = strings.TrimSpace(priceVersion[0])
	}
	if strings.TrimSpace(productType) == ProductTypeMonthlySubscription && version == MonthlyPriceVersion {
		return monthlyModelBillingMultiplier(modelName)
	}
	if strings.TrimSpace(productType) == ProductTypePackDeepSeekClassroom && strings.TrimSpace(modelName) == ClassroomModelQwen {
		return 1, true
	}
	return ExpectedV1BillingMultiplier(productType)
}

// ValidateV1Contract validates only plans that opt in to the frozen CILAI V1
// contract by setting product_type. Legacy upstream plans remain readable, but
// cannot accidentally masquerade as a V1 product. Strict product types are
// validated against their product_templates row, which now carries the
// contract numbers that used to be frozen code constants.
func (p *SubscriptionPlan) ValidateV1Contract() error {
	return p.validateV1ContractTx(nil)
}

// validateV1ContractTx is the transaction-aware form; callers already holding
// a transaction must pass it so the template lookup reuses its connection.
func (p *SubscriptionPlan) validateV1ContractTx(tx *gorm.DB) error {
	if p == nil {
		return errors.New("plan is nil")
	}
	if p.PriceBook != "" {
		return p.ValidateProductPlan()
	}
	p.ProductType = strings.TrimSpace(p.ProductType)
	if p.ProductType == "" || p.ProductType == ProductTypeLegacySubscription {
		// Legacy plans retain their historical model scope and billing path. They
		// are explicitly classified, but never opt into strict V1 validation and
		// must not carry a template link.
		if p.TemplateId != 0 {
			return errors.New("template_id requires a V1 product_type")
		}
		return nil
	}
	if !RequiresStrictV1RoutePolicy(p.ProductType) {
		return fmt.Errorf("unsupported product_type %q", p.ProductType)
	}
	template, err := getProductTemplateByTypeTx(tx, p.ProductType)
	if err != nil {
		return fmt.Errorf("unknown product_type %q: product template is not configured", p.ProductType)
	}
	if p.TemplateId == 0 {
		// Pin the template link while validating so every save path (bootstrap,
		// admin upsert, order snapshot rebuild) carries it.
		p.TemplateId = int64(template.Id)
	} else if p.TemplateId != int64(template.Id) {
		return fmt.Errorf("template_id %d does not match product_type %s", p.TemplateId, p.ProductType)
	}
	templateModels, err := template.modelSet()
	if err != nil {
		return err
	}
	templateChannels, err := template.channelPolicy()
	if err != nil {
		return err
	}
	priceRules, err := template.priceRuleList()
	if err != nil {
		return err
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
	if p.Currency != template.Currency {
		return fmt.Errorf("%s must use %s", p.ProductType, template.Currency)
	}
	if template.DurationDays > 0 && (p.DurationUnit != SubscriptionDurationDay || p.DurationValue != template.DurationDays) {
		return fmt.Errorf("%s must have a fixed %d-day duration", p.ProductType, template.DurationDays)
	}
	if strings.TrimSpace(p.ModelScope) == "" {
		return errors.New("V1 products require an explicit allowed_models list")
	}
	planModels := explicitModelIds(p.ModelScope)
	for _, modelName := range planModels {
		if strings.ContainsAny(modelName, "*?") {
			return fmt.Errorf("allowed_models must use exact model IDs, got %q", modelName)
		}
	}
	if len(templateModels) > 0 {
		allowedModels := make(map[string]struct{}, len(templateModels))
		for _, modelName := range templateModels {
			allowedModels[modelName] = struct{}{}
		}
		for _, modelName := range planModels {
			if _, ok := allowedModels[modelName]; !ok {
				return fmt.Errorf("%s model %q is not allowed by its product template", p.ProductType, modelName)
			}
		}
	}
	if strings.TrimSpace(p.AllowedChannels) == "" {
		return errors.New("V1 products require an allowed_channels policy snapshot")
	}
	if strings.TrimSpace(p.MaxUpstreamCost) == "" {
		return errors.New("V1 products require a max_upstream_cost policy snapshot")
	}
	if p.FallbackAllowed && !template.FallbackAllowed {
		return errors.New("V1 products must fail closed and cannot enable route fallback")
	}
	policy, err := ParseProductRoutePolicy(p.AllowedChannels, p.MaxUpstreamCost, p.FallbackAllowed)
	if err != nil {
		return err
	}
	for _, modelName := range planModels {
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
		if allowed, ok := templateChannels[modelName]; ok && len(allowed) > 0 {
			allowedSet := make(map[int]struct{}, len(allowed))
			for _, channelId := range allowed {
				allowedSet[channelId] = struct{}{}
			}
			for _, channelId := range channels {
				if _, ok := allowedSet[channelId]; !ok {
					return fmt.Errorf("%s model %q channel %d is not allowed by its product template", p.ProductType, modelName, channelId)
				}
			}
		}
		cost, ok := policy.MaxUpstreamCost[modelName]
		if !ok || !validPositivePriceComponents(cost) {
			return fmt.Errorf("max_upstream_cost must explicitly configure positive input/output costs for model %q", modelName)
		}
	}
	if template.BillingMultiplier > 0 {
		expectedPlanMultiplier := template.BillingMultiplier
		if p.ProductType == ProductTypeMonthlySubscription && p.PriceVersion == template.PriceVersion {
			// A mixed-model monthly entitlement has no meaningful single discount.
			// Keep the plan/token snapshot neutral; the exact immutable ProductPrice
			// row is authoritative and must exist before the product can be enabled.
			expectedPlanMultiplier = 1
		}
		if math.Abs(p.BillingMultiplier-expectedPlanMultiplier) > 0.000001 {
			return fmt.Errorf("billing_multiplier for %s must be %.2f", p.ProductType, expectedPlanMultiplier)
		}
	}
	if !template.AllowWalletOverflow && (p.AllowWalletOverflow == nil || *p.AllowWalletOverflow) {
		return errors.New("V1 products must disable wallet overflow")
	}
	if !template.RedemptionAllowed && p.RedemptionEnabled {
		return fmt.Errorf("%s does not allow redemption", p.ProductType)
	}
	if template.MaxPurchasePerUser > 0 && p.MaxPurchasePerUser != template.MaxPurchasePerUser {
		return fmt.Errorf("%s max_purchase_per_user must be %d", p.ProductType, template.MaxPurchasePerUser)
	}
	if p.ProductType == ProductTypeMonthlySubscription {
		if p.Category != template.Category {
			return errors.New("monthly_subscription must use subscription category")
		}
		if p.ProductTier <= 0 {
			return errors.New("monthly_subscription requires a positive product_tier")
		}
		if !matchTemplatePriceRule(priceRules, p) {
			return fmt.Errorf("monthly_subscription tier/price must match the product template price rules")
		}
		if p.PriceVersion == template.PriceVersion {
			if p.CourseEligible {
				return errors.New("monthly unified tiers cannot attach differentiated course eligibility")
			}
		} else if p.CourseEligible != (p.ProductTier >= 2) {
			return errors.New("legacy monthly course eligibility is required for tiers 2/3 and forbidden for tier 1")
		}
	} else {
		if p.Category != template.Category {
			return fmt.Errorf("%s must use %s category", p.ProductType, template.Category)
		}
		if !template.CourseEligible && p.CourseEligible {
			return errors.New("course eligibility is only available on monthly tiers 2/3")
		}
		if !matchTemplatePriceRule(priceRules, p) {
			return fmt.Errorf("%s price is not allowed by the product template price rules", p.ProductType)
		}
		switch p.ProductType {
		case ProductTypePackDeepSeekClassroom:
			if template.ProductFamily != "" && p.ProductFamily != template.ProductFamily {
				return fmt.Errorf("pack_deepseek_classroom product_family must be %s", template.ProductFamily)
			}
			expectedModels := templateModels
			if p.PriceVersion == ClassroomLegacyPriceVersion {
				expectedModels = []string{ClassroomModelV41, ClassroomModelV4GA}
			}
			if !sameStringSet(planModels, expectedModels) {
				return errors.New("pack_deepseek_classroom model scope does not match its price version")
			}
			for _, modelName := range expectedModels {
				if !sameChannelSet(policy.AllowedChannels[modelName], templateChannels[modelName]) {
					return fmt.Errorf("pack_deepseek_classroom model %s channel binding differs from its product template", modelName)
				}
			}
		case ProductTypePackDeepSeekBigfish:
			if err := validateBigfishPlan(p, policy, template, templateModels, templateChannels); err != nil {
				return err
			}
		}
	}
	if NormalizeResetPeriod(p.QuotaResetPeriod) != SubscriptionResetNever {
		return errors.New("V1 products cannot reset quota inside their entitlement")
	}
	expectedQuota, err := common.QuotaFromFloatStrict(p.PriceAmount * common.QuotaPerUnit)
	if err != nil {
		return fmt.Errorf("invalid V1 face-value quota: %w", err)
	}
	if p.TotalAmount != int64(expectedQuota) {
		return fmt.Errorf("V1 total_amount must equal the paid face value (%d quota units)", expectedQuota)
	}
	return p.validatePlanMarginTx(tx, template, policy)
}

// validatePlanMarginTx enforces the product template's gross-margin floor on
// plans that are currently on sale: for every model in scope, each cost
// component of the plan's max_upstream_cost snapshot must not exceed the
// corresponding user-selling component of the immutable price row scaled by
// (1 - min_margin_percent/100). Templates with a zero floor skip the check
// entirely (wallet), and disabled draft plans are never blocked.
//
// A missing or sale-disabled price row skips the model here instead of
// failing: bootstrap validates desired plans before their price rows are
// inserted, and the order path (createSubscriptionOrderTx) runs
// validateV1ProductPriceCatalogTx first, which fails closed on absent or
// disabled rows before any sale — so this check is strict exactly where a
// sellable configuration already exists.
func (p *SubscriptionPlan) validatePlanMarginTx(tx *gorm.DB, template *ProductTemplate, policy *ProductRoutePolicy) error {
	if p == nil || template == nil || policy == nil {
		return nil
	}
	if !p.Enabled || !(template.MinMarginPercent > 0) {
		return nil
	}
	query := DB
	if tx != nil {
		query = tx
	}
	if query == nil {
		return errors.New("database is nil")
	}
	floor := 1 - template.MinMarginPercent/100
	const epsilon = 1e-9
	var violations []string
	for _, modelName := range explicitModelIds(p.ModelScope) {
		cost, ok := policy.MaxUpstreamCost[modelName]
		if !ok {
			continue // the route policy checks above already require this entry
		}
		var price ProductPrice
		err := query.Where("product_type = ? AND model = ? AND price_version = ?", p.ProductType, modelName, p.PriceVersion).
			First(&price).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return fmt.Errorf("cannot verify gross margin for model %q: %w", modelName, err)
		}
		if !price.Enabled {
			continue
		}
		var sale PriceComponents
		if err := common.UnmarshalJsonStr(strings.TrimSpace(price.UserSellingPrice), &sale); err != nil {
			return fmt.Errorf("cannot verify gross margin for model %q: invalid user_selling_price: %w", modelName, err)
		}
		components := []struct {
			name      string
			costValue float64
			saleValue float64
		}{
			{"input", cost.Input, sale.Input},
			{"cached_input", cost.CachedInput, sale.CachedInput},
			{"cache_creation", cost.CacheCreation, sale.CacheCreation},
			{"output", cost.Output, sale.Output},
		}
		for _, component := range components {
			if component.costValue > component.saleValue*floor+epsilon {
				violations = append(violations, fmt.Sprintf("model %q component %s: cost %.6g exceeds sale %.6g x (1 - %.2f%%)",
					modelName, component.name, component.costValue, component.saleValue, template.MinMarginPercent))
			}
		}
	}
	if len(violations) > 0 {
		return fmt.Errorf("%s gross margin below the %.2f%% template floor: %s",
			p.ProductType, template.MinMarginPercent, strings.Join(violations, "; "))
	}
	return nil
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]struct{}, len(left))
	for _, value := range left {
		values[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
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
	if !UsesVersionedProductPricing(p.ProductType) || p.Model == "" || p.PriceVersion == "" {
		return errors.New("product_type, exact model, and price_version are required for a V1 product price")
	}
	if strings.ContainsAny(p.Model, "*?") {
		return errors.New("product price model must be an exact model ID")
	}
	if RequiresStrictV1RoutePolicy(p.ProductType) && p.FallbackAllowed {
		return errors.New("V1 product prices must fail closed and cannot enable route fallback")
	}
	expectedMultiplier, ok := ExpectedProductPriceBillingMultiplier(p.ProductType, p.Model, p.PriceVersion)
	if !ok || math.Abs(expectedMultiplier-p.BillingMultiplier) > 0.000001 {
		return fmt.Errorf("product price billing_multiplier for %s must be %.2f", p.ProductType, expectedMultiplier)
	}
	if p.ProductType == ProductTypePackDeepSeekClassroom || p.ProductType == ProductTypePackDeepSeekBigfish {
		var channelIds []int
		if err := common.UnmarshalJsonStr(strings.TrimSpace(p.AllowedChannels), &channelIds); err != nil {
			return errors.New("allowed_channels must be a JSON channel ID list")
		}
		if p.ProductType == ProductTypePackDeepSeekClassroom {
			if p.Model != ClassroomModelV41 && p.Model != ClassroomModelV4GA && p.Model != ClassroomModelQwen {
				return errors.New("classroom product price model is not allowed")
			}
			expectedChannel := 15
			if p.Model == ClassroomModelQwen {
				expectedChannel = 14
			}
			if len(channelIds) != 1 || channelIds[0] != expectedChannel {
				return fmt.Errorf("classroom product price for %s must be restricted to channel %d", p.Model, expectedChannel)
			}
		}
		if p.ProductType == ProductTypePackDeepSeekBigfish {
			if p.PriceVersion != BigfishPriceVersion || (p.Model != ClassroomModelV4GA && p.Model != ClassroomModelV41) || len(channelIds) != 1 || channelIds[0] != 15 {
				return errors.New("bigfish prices require the audited Flash IDs, channel 15 and independent price version")
			}
		}
	}
	return p.validatePriceTerms()
}

// validatePriceTerms checks economic and routing data without selecting a
// product template, model family, channel number, or commercial multiplier.
func (p *ProductPrice) validatePriceTerms() error {
	if p.Model == "" || p.PriceVersion == "" || strings.ContainsAny(p.Model, "*?") {
		return errors.New("exact model and price_version are required")
	}
	if p.BillingMultiplier <= 0 || math.IsNaN(p.BillingMultiplier) || math.IsInf(p.BillingMultiplier, 0) {
		return errors.New("billing_multiplier must be finite and positive")
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
	if upstream.Input > cost.Input || upstream.CachedInput > cost.CachedInput || upstream.CacheCreation > cost.CacheCreation || upstream.Output > cost.Output {
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

// GetCurrentProductPriceSnapshot resolves the newest enabled version that is
// already effective. It is used by wallet requests because wallet balance is
// permanent and future calls must follow the current official-price version;
// the selected immutable version is then copied into the request/log snapshot.
func GetCurrentProductPriceSnapshot(productType string, modelName string) (*ProductPrice, error) {
	var price ProductPrice
	now := GetDBTimestamp()
	if err := DB.Where("product_type = ? AND model = ? AND enabled = ? AND (effective_at = 0 OR effective_at <= ?)",
		strings.TrimSpace(productType), strings.TrimSpace(modelName), true, now).
		Order("effective_at desc, id desc").First(&price).Error; err != nil {
		return nil, fmt.Errorf("current product price is not configured: %w", err)
	}
	if err := price.ValidateV1(); err != nil {
		return nil, err
	}
	return &price, nil
}

func HasProductPriceConfiguration(productType string, modelName string) (bool, error) {
	var count int64
	if err := DB.Model(&ProductPrice{}).
		Where("product_type = ? AND model = ?", strings.TrimSpace(productType), strings.TrimSpace(modelName)).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func validateV1ProductPriceCatalogTx(tx *gorm.DB, plan *SubscriptionPlan) error {
	if plan != nil && plan.PriceBook != "" {
		return validateProductPlanPrices(tx, plan, true)
	}
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
		expectedMultiplier, ok := ExpectedProductPriceBillingMultiplier(plan.ProductType, modelName, plan.PriceVersion)
		if !ok || math.Abs(price.BillingMultiplier-expectedMultiplier) > 0.000001 {
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
		math.Abs(left.CacheCreation-right.CacheCreation) <= 0.000001 &&
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

// MigrateCilaiV1Contracts records an explicit state around the deterministic
// backfill. The read-only preflight is performed by migrateDB before any DDL;
// this wrapper makes a successful run idempotent and leaves a failed run
// visible for operator recovery.
func MigrateCilaiV1Contracts() error {
	if _, err := PreflightCilaiV1Migration(); err != nil {
		return err
	}
	state, err := getCilaiV1MigrationState()
	if err != nil {
		return err
	}
	if state != nil && state.State == CilaiV1MigrationApplied {
		return nil
	}
	if err := setCilaiV1MigrationState(CilaiV1MigrationPreflightPassed, nil); err != nil {
		return err
	}
	if err := backfillCilaiV1Contracts(); err != nil {
		_ = setCilaiV1MigrationState(CilaiV1MigrationFailed, err)
		return err
	}
	return setCilaiV1MigrationState(CilaiV1MigrationApplied, nil)
}

// backfillCilaiV1Contracts performs only deterministic metadata backfills. It
// never guesses a V1 product for an unclassified legacy plan. Existing bound
// subscriptions are made one-to-one; conflicting duplicate keys abort the
// migration for explicit operator review instead of deleting user data.
func backfillCilaiV1Contracts() error {
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
		if err := tx.Order("id asc").Find(&orders).Error; err != nil {
			return err
		}
		for i := range orders {
			order := &orders[i]
			switch strings.ToLower(strings.TrimSpace(order.Status)) {
			case common.TopUpStatusPending, common.TopUpStatusSuccess, common.TopUpStatusFailed, common.TopUpStatusExpired, "cancelled", "canceled":
			default:
				order.LegacyCompatibilityState = LegacyOrderStateReview
				order.LegacyReviewReason = fmt.Sprintf("legacy order has unknown status %q", order.Status)
				order.ProvisioningStatus = ProvisioningStatusLegacyReview
				if err := tx.Save(order).Error; err != nil {
					return err
				}
				continue
			}
			if order.PriceBook != "" && order.ProductPlanSnapshot != "" {
				continue // Generic orders already carry complete immutable terms.
			}
			if order.ProductType == "" {
				if err := backfillLegacySubscriptionOrderTx(tx, order); err != nil {
					return err
				}
				continue
			}
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
			if sub.ProductType == "" && sub.PriceBook == "" {
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
			if err := tx.Where("user_subscription_id = ? AND status = ?", sub.Id, common.TokenStatusEnabled).Order("id asc").Find(&tokens).Error; err != nil {
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
				"entitlement_type":     productType,
				"product_type":         productType,
				"entitlement_key_lock": fmt.Sprintf("subscription:%d", sub.Id),
				"billing_multiplier":   multiplier,
				"price_version":        sub.PriceVersion,
				"allowed_channels":     sub.AllowedChannels,
				"max_upstream_cost":    sub.MaxUpstreamCost,
				"fallback_allowed":     sub.FallbackAllowed,
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

func backfillLegacySubscriptionOrderTx(tx *gorm.DB, order *SubscriptionOrder) error {
	if tx == nil || order == nil {
		return errors.New("invalid legacy subscription order")
	}
	switch strings.ToLower(strings.TrimSpace(order.Status)) {
	case common.TopUpStatusPending, common.TopUpStatusSuccess, common.TopUpStatusFailed, common.TopUpStatusExpired, "cancelled", "canceled":
		// Known historical states are handled deterministically below.
	default:
		order.LegacyCompatibilityState = LegacyOrderStateReview
		order.LegacyReviewReason = fmt.Sprintf("legacy order has unknown status %q", order.Status)
		order.ProvisioningStatus = ProvisioningStatusLegacyReview
		return tx.Save(order).Error
	}
	var plan SubscriptionPlan
	if order.PlanId <= 0 || tx.Where("id = ?", order.PlanId).First(&plan).Error != nil {
		order.LegacyCompatibilityState = LegacyOrderStateReview
		order.LegacyReviewReason = "legacy order has no resolvable subscription plan"
		order.ProvisioningStatus = ProvisioningStatusLegacyReview
		return tx.Save(order).Error
	}
	if strings.TrimSpace(plan.ProductType) == "" {
		if err := order.SnapshotLegacyPlan(&plan); err != nil {
			order.LegacyCompatibilityState = LegacyOrderStateReview
			order.LegacyReviewReason = err.Error()
			order.ProvisioningStatus = ProvisioningStatusLegacyReview
			return tx.Save(order).Error
		}
	} else {
		order.SnapshotPlan(&plan)
		order.LegacyCompatibilityState = LegacyOrderStateReady
		order.LegacyReviewReason = ""
	}

	if order.Status == common.TopUpStatusSuccess && (order.UserSubscriptionId == nil || *order.UserSubscriptionId <= 0) {
		var subscriptions []UserSubscription
		if err := tx.Where("user_id = ? AND plan_id = ?", order.UserId, order.PlanId).Order("id asc").Find(&subscriptions).Error; err != nil {
			return err
		}
		if len(subscriptions) == 1 {
			order.UserSubscriptionId = &subscriptions[0].Id
			order.ProvisioningStatus = ProvisioningStatusProvisioned
		} else {
			order.LegacyCompatibilityState = LegacyOrderStateReview
			order.LegacyReviewReason = fmt.Sprintf("legacy successful order has %d matching subscriptions; manual linkage required", len(subscriptions))
			order.ProvisioningStatus = ProvisioningStatusLegacyReview
		}
	}
	return tx.Save(order).Error
}

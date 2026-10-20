package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
	"gorm.io/gorm"
)

// ProductTemplatePriceRule is one sellable price point of a product template.
// Monthly tiers key rules by Tier, fixed SKUs (classroom/bigfish) key by
// ProductCode, and open traffic packs leave both empty so any product code may
// sell at the listed price.
type ProductTemplatePriceRule struct {
	ProductCode string  `json:"product_code,omitempty"`
	Tier        int     `json:"tier,omitempty"`
	PriceAmount float64 `json:"price_amount"`
	TotalAmount int64   `json:"total_amount"`
}

// ProductTemplate carries the CILAI V1 contract rules that used to be frozen
// code constants. ValidateV1Contract resolves the template row by product_type
// and validates a plan against it, so operations can tune products without a
// code release. Locked rows are core templates: their key fields may only
// change through a code migration (MigrateLockedProductTemplate), never through
// the operator write path.
type ProductTemplate struct {
	Id int `json:"id"`

	ProductType string `json:"product_type" gorm:"type:varchar(32);not null;uniqueIndex"`
	DisplayName string `json:"display_name" gorm:"type:varchar(128);not null;default:''"`
	Category    string `json:"category" gorm:"type:varchar(16);not null;default:'package'"`
	Currency    string `json:"currency" gorm:"type:varchar(8);not null;default:'CNY'"`

	// DurationDays is the fixed entitlement duration in days; 0 means the
	// template does not constrain duration.
	DurationDays int `json:"duration_days" gorm:"type:int;not null;default:0"`

	// BillingMultiplier is the plan-level multiplier. 0 means the plan-level
	// snapshot stays neutral and pricing is carried per model by the immutable
	// price catalog (the monthly-unified special case).
	BillingMultiplier float64 `json:"billing_multiplier" gorm:"type:decimal(8,6);not null;default:0"`

	// ModelScope is a JSON array of the exact model IDs the template allows.
	ModelScope string `json:"model_scope" gorm:"type:text"`
	// AllowedChannels is a JSON map[model][]int of allowed channel IDs.
	AllowedChannels string `json:"allowed_channels" gorm:"type:text"`
	// MaxUpstreamCost is a JSON map[model]PriceComponents default cost ceiling.
	MaxUpstreamCost string `json:"max_upstream_cost" gorm:"type:text"`

	FallbackAllowed     bool `json:"fallback_allowed"`
	MaxPurchasePerUser  int  `json:"max_purchase_per_user" gorm:"type:int;not null;default:0"`
	AllowBalancePay     bool `json:"allow_balance_pay"`
	AllowWalletOverflow bool `json:"allow_wallet_overflow"`
	CourseEligible      bool `json:"course_eligible"`
	RedemptionAllowed   bool `json:"redemption_allowed"`

	// PriceRules is a JSON array of ProductTemplatePriceRule. A plan's price
	// must hit exactly one rule for its product_code/tier.
	PriceRules string `json:"price_rules" gorm:"type:text"`

	ProductFamily string `json:"product_family" gorm:"type:varchar(64);not null;default:''"`
	PriceVersion  string `json:"price_version" gorm:"type:varchar(64);not null;default:''"`

	// MinMarginPercent is reserved for the Phase 2 gross-margin check.
	MinMarginPercent float64 `json:"min_margin_percent" gorm:"type:decimal(5,2);not null;default:0"`
	// DisplayMetadata is reserved JSON (card copy, images, badges, ordering,
	// zones) for the Phase 4 storefront.
	DisplayMetadata string `json:"display_metadata" gorm:"type:text"`

	Locked    bool  `json:"locked"`
	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (t *ProductTemplate) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	t.CreatedAt = now
	t.UpdatedAt = now
	return nil
}

func (t *ProductTemplate) BeforeUpdate(tx *gorm.DB) error {
	t.UpdatedAt = common.GetTimestamp()
	return nil
}

func (t *ProductTemplate) modelSet() ([]string, error) {
	var models []string
	if err := common.UnmarshalJsonStr(strings.TrimSpace(t.ModelScope), &models); err != nil {
		return nil, fmt.Errorf("product template %s has invalid model_scope: %w", t.ProductType, err)
	}
	return models, nil
}

func (t *ProductTemplate) channelPolicy() (map[string][]int, error) {
	channels := map[string][]int{}
	if strings.TrimSpace(t.AllowedChannels) == "" {
		return channels, nil
	}
	if err := common.UnmarshalJsonStr(strings.TrimSpace(t.AllowedChannels), &channels); err != nil {
		return nil, fmt.Errorf("product template %s has invalid allowed_channels: %w", t.ProductType, err)
	}
	return channels, nil
}

func (t *ProductTemplate) priceRuleList() ([]ProductTemplatePriceRule, error) {
	var rules []ProductTemplatePriceRule
	if err := common.UnmarshalJsonStr(strings.TrimSpace(t.PriceRules), &rules); err != nil {
		return nil, fmt.Errorf("product template %s has invalid price_rules: %w", t.ProductType, err)
	}
	return rules, nil
}

// matchTemplatePriceRule requires the plan price to hit one rule whose
// product_code/tier keys (when present) match the plan.
func matchTemplatePriceRule(rules []ProductTemplatePriceRule, plan *SubscriptionPlan) bool {
	for _, rule := range rules {
		if rule.ProductCode != "" && rule.ProductCode != strings.TrimSpace(plan.ProductCode) {
			continue
		}
		if rule.Tier > 0 && rule.Tier != plan.ProductTier {
			continue
		}
		if math.Abs(rule.PriceAmount-plan.PriceAmount) <= 0.000001 {
			return true
		}
	}
	return false
}

func validateProductTemplate(t *ProductTemplate) error {
	if t == nil {
		return errors.New("product template is nil")
	}
	t.ProductType = strings.TrimSpace(t.ProductType)
	if t.ProductType == "" {
		return errors.New("product template requires a product_type")
	}
	if NormalizeSubscriptionCategory(t.Category) != t.Category {
		return fmt.Errorf("product template %s has invalid category %q", t.ProductType, t.Category)
	}
	if strings.TrimSpace(t.Currency) == "" {
		return fmt.Errorf("product template %s requires a currency", t.ProductType)
	}
	if t.DurationDays < 0 || t.MaxPurchasePerUser < 0 {
		return fmt.Errorf("product template %s has negative duration or purchase limit", t.ProductType)
	}
	if t.BillingMultiplier < 0 || math.IsNaN(t.BillingMultiplier) || math.IsInf(t.BillingMultiplier, 0) {
		return fmt.Errorf("product template %s has invalid billing_multiplier", t.ProductType)
	}
	if _, err := t.modelSet(); err != nil {
		return err
	}
	if _, err := t.channelPolicy(); err != nil {
		return err
	}
	if strings.TrimSpace(t.MaxUpstreamCost) != "" {
		var costs map[string]PriceComponents
		if err := common.UnmarshalJsonStr(strings.TrimSpace(t.MaxUpstreamCost), &costs); err != nil {
			return fmt.Errorf("product template %s has invalid max_upstream_cost: %w", t.ProductType, err)
		}
	}
	if _, err := t.priceRuleList(); err != nil {
		return err
	}
	if strings.TrimSpace(t.DisplayMetadata) != "" {
		var metadata map[string]interface{}
		if err := common.UnmarshalJsonStr(strings.TrimSpace(t.DisplayMetadata), &metadata); err != nil {
			return fmt.Errorf("product template %s has invalid display_metadata: %w", t.ProductType, err)
		}
	}
	return nil
}

const productTemplateCacheNamespace = "new-api:product_template:v1"

var (
	productTemplateCacheOnce sync.Once
	productTemplateCache     *cachex.HybridCache[ProductTemplate]
)

func productTemplateCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("PRODUCT_TEMPLATE_CACHE_TTL", 300)
	if ttlSeconds <= 0 {
		ttlSeconds = 300
	}
	return time.Duration(ttlSeconds) * time.Second
}

func getProductTemplateCache() *cachex.HybridCache[ProductTemplate] {
	productTemplateCacheOnce.Do(func() {
		ttl := productTemplateCacheTTL()
		productTemplateCache = cachex.NewHybridCache[ProductTemplate](cachex.HybridCacheConfig[ProductTemplate]{
			Namespace: cachex.Namespace(productTemplateCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[ProductTemplate]{},
			Memory: func() *hot.HotCache[string, ProductTemplate] {
				return hot.NewHotCache[string, ProductTemplate](hot.LRU, 256).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return productTemplateCache
}

// InvalidateProductTemplateCache drops one cached template after a write.
func InvalidateProductTemplateCache(productType string) {
	productType = strings.TrimSpace(productType)
	if productType == "" {
		return
	}
	_, _ = getProductTemplateCache().DeleteMany([]string{productType})
}

// GetProductTemplateByType resolves the template row for a product_type,
// cached in-process (and in Redis when enabled) like the plan cache. Cache
// misses are never cached so a template appearing later is picked up
// immediately.
func GetProductTemplateByType(productType string) (*ProductTemplate, error) {
	return getProductTemplateByTypeTx(nil, productType)
}

// getProductTemplateByTypeTx mirrors getSubscriptionPlanByIdTx: callers inside
// a transaction must pass it so the lookup reuses the transaction's
// connection instead of blocking on the pool (SQLite runs single-connection).
func getProductTemplateByTypeTx(tx *gorm.DB, productType string) (*ProductTemplate, error) {
	productType = strings.TrimSpace(productType)
	if productType == "" {
		return nil, errors.New("product_type is required")
	}
	if cached, found, err := getProductTemplateCache().Get(productType); err == nil && found {
		return &cached, nil
	}
	query := DB
	if tx != nil {
		query = tx
	}
	if query == nil {
		return nil, errors.New("database is nil")
	}
	var template ProductTemplate
	if err := query.Where("product_type = ?", productType).First(&template).Error; err != nil {
		return nil, err
	}
	_ = getProductTemplateCache().SetWithTTL(productType, template, productTemplateCacheTTL())
	return &template, nil
}

func ListProductTemplates() ([]ProductTemplate, error) {
	var templates []ProductTemplate
	if err := DB.Order("id asc").Find(&templates).Error; err != nil {
		return nil, err
	}
	return templates, nil
}

// UpdateProductTemplate is the operator write path for editable templates.
// Locked core templates are read-only here; their key fields may only change
// through MigrateLockedProductTemplate inside a code migration.
func UpdateProductTemplate(template *ProductTemplate) error {
	if template == nil || template.Id <= 0 {
		return errors.New("invalid product template")
	}
	var existing ProductTemplate
	if err := DB.Where("id = ?", template.Id).First(&existing).Error; err != nil {
		return err
	}
	if existing.Locked {
		return fmt.Errorf("product template %s is locked and can only be changed by a migration", existing.ProductType)
	}
	return saveProductTemplate(template, existing)
}

// MigrateLockedProductTemplate is the high-privilege write path reserved for
// code migrations. It may update locked core templates.
func MigrateLockedProductTemplate(template *ProductTemplate) error {
	if template == nil || template.Id <= 0 {
		return errors.New("invalid product template")
	}
	var existing ProductTemplate
	if err := DB.Where("id = ?", template.Id).First(&existing).Error; err != nil {
		return err
	}
	return saveProductTemplate(template, existing)
}

func saveProductTemplate(template *ProductTemplate, existing ProductTemplate) error {
	// product_type is the template identity and is never reassigned.
	template.ProductType = existing.ProductType
	template.CreatedAt = existing.CreatedAt
	if err := validateProductTemplate(template); err != nil {
		return err
	}
	if err := DB.Save(template).Error; err != nil {
		return err
	}
	InvalidateProductTemplateCache(existing.ProductType)
	return nil
}

// monthlyTemplatePriceRules is the frozen tier -> price contract of the
// monthly subscription, kept as the seed definition source.
var monthlyTemplatePriceRules = map[int]float64{1: 19.9, 2: 49.9, 3: 99.9}

// bigfishTemplatePriceRules is the frozen product_code -> price contract of
// the bigfish promotion, kept as the seed definition source.
var bigfishTemplatePriceRules = map[string]float64{
	"deepseek-bigfish-lite-20261017": 9.9,
	"deepseek-bigfish-pro-20261017":  19.9,
	"deepseek-bigfish-max-20261017":  39.9,
}

func templateRuleTotalAmount(price float64) (int64, error) {
	quota, err := common.QuotaFromFloatStrict(price * common.QuotaPerUnit)
	if err != nil {
		return 0, fmt.Errorf("invalid template rule price %v: %w", price, err)
	}
	return int64(quota), nil
}

// desiredProductTemplates derives the seven core template rows from the frozen
// contract constants. It is the single seed definition; SeedProductTemplates
// only inserts rows that do not exist yet.
func desiredProductTemplates() ([]ProductTemplate, error) {
	encode := func(value interface{}) (string, error) {
		return classroomJSON(value)
	}
	ruleFromPrice := func(code string, tier int, price float64) (ProductTemplatePriceRule, error) {
		total, err := templateRuleTotalAmount(price)
		if err != nil {
			return ProductTemplatePriceRule{}, err
		}
		return ProductTemplatePriceRule{ProductCode: code, Tier: tier, PriceAmount: price, TotalAmount: total}, nil
	}

	walletScope, err := encode([]string{ClassroomModelV41, ClassroomModelV4GA})
	if err != nil {
		return nil, err
	}
	walletChannels, err := encode(map[string][]int{
		ClassroomModelV41: {15}, ClassroomModelV4GA: {15},
	})
	if err != nil {
		return nil, err
	}
	walletCosts, err := encode(map[string]PriceComponents{
		ClassroomModelV41: {Input: 1, CachedInput: 0.02, Output: 4},
		ClassroomModelV4GA: {Input: 1.5, CachedInput: 0.05, Output: 4.5},
	})
	if err != nil {
		return nil, err
	}

	monthlyScope, err := encode([]string{
		MonthlyModelDeepSeekV41, MonthlyModelDeepSeek, MonthlyModelGLM52, MonthlyModelKimiK3, MonthlyModelQwen,
	})
	if err != nil {
		return nil, err
	}
	monthlyChannels, err := encode(map[string][]int{
		MonthlyModelDeepSeekV41: {15}, MonthlyModelDeepSeek: {15},
		MonthlyModelGLM52: {14}, MonthlyModelKimiK3: {14}, MonthlyModelQwen: {14},
	})
	if err != nil {
		return nil, err
	}
	monthlyCosts, err := encode(map[string]PriceComponents{
		MonthlyModelDeepSeekV41: {Input: 1, CachedInput: 0.02, Output: 4},
		MonthlyModelDeepSeek:    {Input: 1.5, CachedInput: 0.05, Output: 4.5},
		MonthlyModelGLM52:       {Input: 4.4, CachedInput: 1.1, Output: 15.4},
		MonthlyModelKimiK3:      {Input: 16, CachedInput: 1.6, Output: 80},
		MonthlyModelQwen:        {Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16},
	})
	if err != nil {
		return nil, err
	}
	monthlyRules := make([]ProductTemplatePriceRule, 0, len(monthlyTemplatePriceRules))
	for _, tier := range []int{1, 2, 3} {
		rule, err := ruleFromPrice("", tier, monthlyTemplatePriceRules[tier])
		if err != nil {
			return nil, err
		}
		monthlyRules = append(monthlyRules, rule)
	}
	monthlyRulesJSON, err := encode(monthlyRules)
	if err != nil {
		return nil, err
	}

	packChannels := func(pairs map[string]int) (string, error) {
		channels := map[string][]int{}
		for modelName, channelId := range pairs {
			channels[modelName] = []int{channelId}
		}
		return encode(channels)
	}
	packDSGLMScope, err := encode([]string{MonthlyModelDeepSeekV41, MonthlyModelDeepSeek, MonthlyModelGLM52})
	if err != nil {
		return nil, err
	}
	packDSGLMChannels, err := packChannels(map[string]int{
		MonthlyModelDeepSeekV41: 15, MonthlyModelDeepSeek: 15, MonthlyModelGLM52: 14,
	})
	if err != nil {
		return nil, err
	}
	packDSGLMCosts, err := encode(map[string]PriceComponents{
		MonthlyModelDeepSeekV41: {Input: 1, CachedInput: 0.02, Output: 4},
		MonthlyModelDeepSeek:    {Input: 1.5, CachedInput: 0.05, Output: 4.5},
		MonthlyModelGLM52:       {Input: 4.4, CachedInput: 1.1, Output: 15.4},
	})
	if err != nil {
		return nil, err
	}
	packQwenScope, err := encode([]string{MonthlyModelQwen})
	if err != nil {
		return nil, err
	}
	packQwenChannels, err := packChannels(map[string]int{MonthlyModelQwen: 14})
	if err != nil {
		return nil, err
	}
	packQwenCosts, err := encode(map[string]PriceComponents{
		MonthlyModelQwen: {Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16},
	})
	if err != nil {
		return nil, err
	}
	packK3Scope, err := encode([]string{MonthlyModelKimiK3})
	if err != nil {
		return nil, err
	}
	packK3Channels, err := packChannels(map[string]int{MonthlyModelKimiK3: 14})
	if err != nil {
		return nil, err
	}
	packK3Costs, err := encode(map[string]PriceComponents{
		MonthlyModelKimiK3: {Input: 16, CachedInput: 1.6, Output: 80},
	})
	if err != nil {
		return nil, err
	}
	openPackRules := func(prices ...float64) (string, error) {
		rules := make([]ProductTemplatePriceRule, 0, len(prices))
		for _, price := range prices {
			rule, err := ruleFromPrice("", 0, price)
			if err != nil {
				return "", err
			}
			rules = append(rules, rule)
		}
		return encode(rules)
	}
	smallPackRules, err := openPackRules(19.9)
	if err != nil {
		return nil, err
	}
	k3PackRules, err := openPackRules(49.9, 99.9, 199.9)
	if err != nil {
		return nil, err
	}

	classroomScope, err := encode([]string{ClassroomModelV41, ClassroomModelV4GA, ClassroomModelQwen})
	if err != nil {
		return nil, err
	}
	classroomChannels, err := encode(map[string][]int{
		ClassroomModelV41: {15}, ClassroomModelV4GA: {15}, ClassroomModelQwen: {14},
	})
	if err != nil {
		return nil, err
	}
	classroomCosts, err := encode(map[string]PriceComponents{
		ClassroomModelV41:  {Input: 1, CachedInput: 0.02, Output: 4},
		ClassroomModelV4GA: {Input: 1.5, CachedInput: 0.05, Output: 4.5},
		ClassroomModelQwen: {Input: 0.64, CachedInput: 0.08, CacheCreation: 1.00, Output: 2.16},
	})
	if err != nil {
		return nil, err
	}
	classroomRule, err := ruleFromPrice("", 0, 5.0)
	if err != nil {
		return nil, err
	}
	classroomRules, err := encode([]ProductTemplatePriceRule{classroomRule})
	if err != nil {
		return nil, err
	}

	bigfishScope, err := encode([]string{ClassroomModelV4GA, ClassroomModelV41})
	if err != nil {
		return nil, err
	}
	bigfishChannels, err := encode(map[string][]int{
		ClassroomModelV4GA: {15}, ClassroomModelV41: {15},
	})
	if err != nil {
		return nil, err
	}
	bigfishCosts, err := encode(map[string]PriceComponents{
		ClassroomModelV4GA: {Input: 1.5, CachedInput: 0.05, Output: 4.5},
		ClassroomModelV41:  {Input: 1, CachedInput: 0.02, Output: 4},
	})
	if err != nil {
		return nil, err
	}
	bigfishRules := make([]ProductTemplatePriceRule, 0, len(bigfishTemplatePriceRules))
	for _, code := range []string{
		"deepseek-bigfish-lite-20261017", "deepseek-bigfish-pro-20261017", "deepseek-bigfish-max-20261017",
	} {
		rule, err := ruleFromPrice(code, 0, bigfishTemplatePriceRules[code])
		if err != nil {
			return nil, err
		}
		bigfishRules = append(bigfishRules, rule)
	}
	bigfishRulesJSON, err := encode(bigfishRules)
	if err != nil {
		return nil, err
	}
	emptyRules, err := encode([]ProductTemplatePriceRule{})
	if err != nil {
		return nil, err
	}

	templates := []ProductTemplate{
		{
			ProductType: ProductTypeWallet, DisplayName: "钱包余额",
			Category: SubscriptionCategoryPackage, Currency: "CNY",
			DurationDays: 0, BillingMultiplier: frozenV1BillingMultipliers[ProductTypeWallet],
			ModelScope: walletScope, AllowedChannels: walletChannels, MaxUpstreamCost: walletCosts,
			FallbackAllowed: false, MaxPurchasePerUser: 0,
			AllowBalancePay: true, AllowWalletOverflow: false, CourseEligible: false, RedemptionAllowed: true,
			PriceRules: emptyRules, PriceVersion: WalletDeepSeekPriceVersion,
			DisplayMetadata: "{}", Locked: true,
		},
		{
			ProductType: ProductTypeMonthlySubscription, DisplayName: "月付套餐",
			Category: SubscriptionCategorySubscription, Currency: "CNY",
			DurationDays: 30, BillingMultiplier: frozenV1BillingMultipliers[ProductTypeMonthlySubscription],
			ModelScope: monthlyScope, AllowedChannels: monthlyChannels, MaxUpstreamCost: monthlyCosts,
			FallbackAllowed: false, MaxPurchasePerUser: 0,
			AllowBalancePay: false, AllowWalletOverflow: false, CourseEligible: true, RedemptionAllowed: true,
			PriceRules: monthlyRulesJSON,
			ProductFamily: MonthlyProductFamily, PriceVersion: MonthlyPriceVersion,
			DisplayMetadata: "{}", Locked: true,
		},
		{
			ProductType: ProductTypePackDSGLM, DisplayName: "DeepSeek + GLM 流量包",
			Category: SubscriptionCategoryPackage, Currency: "CNY",
			DurationDays: 30, BillingMultiplier: frozenV1BillingMultipliers[ProductTypePackDSGLM],
			ModelScope: packDSGLMScope, AllowedChannels: packDSGLMChannels, MaxUpstreamCost: packDSGLMCosts,
			FallbackAllowed: false, MaxPurchasePerUser: 0,
			AllowBalancePay: true, AllowWalletOverflow: false, CourseEligible: false, RedemptionAllowed: true,
			PriceRules: smallPackRules,
			DisplayMetadata: "{}", Locked: true,
		},
		{
			ProductType: ProductTypePackQwen, DisplayName: "Qwen 流量包",
			Category: SubscriptionCategoryPackage, Currency: "CNY",
			DurationDays: 30, BillingMultiplier: frozenV1BillingMultipliers[ProductTypePackQwen],
			ModelScope: packQwenScope, AllowedChannels: packQwenChannels, MaxUpstreamCost: packQwenCosts,
			FallbackAllowed: false, MaxPurchasePerUser: 0,
			AllowBalancePay: true, AllowWalletOverflow: false, CourseEligible: false, RedemptionAllowed: true,
			PriceRules: smallPackRules,
			DisplayMetadata: "{}", Locked: true,
		},
		{
			ProductType: ProductTypePackK3, DisplayName: "Kimi K3 流量包",
			Category: SubscriptionCategoryPackage, Currency: "CNY",
			DurationDays: 30, BillingMultiplier: frozenV1BillingMultipliers[ProductTypePackK3],
			ModelScope: packK3Scope, AllowedChannels: packK3Channels, MaxUpstreamCost: packK3Costs,
			FallbackAllowed: false, MaxPurchasePerUser: 0,
			AllowBalancePay: true, AllowWalletOverflow: false, CourseEligible: false, RedemptionAllowed: true,
			PriceRules: k3PackRules,
			DisplayMetadata: "{}", Locked: true,
		},
		{
			ProductType: ProductTypePackDeepSeekClassroom, DisplayName: "IF.Learn 启动包",
			Category: SubscriptionCategoryPackage, Currency: "CNY",
			DurationDays: 10, BillingMultiplier: frozenV1BillingMultipliers[ProductTypePackDeepSeekClassroom],
			ModelScope: classroomScope, AllowedChannels: classroomChannels, MaxUpstreamCost: classroomCosts,
			FallbackAllowed: false, MaxPurchasePerUser: 1,
			AllowBalancePay: false, AllowWalletOverflow: false, CourseEligible: false, RedemptionAllowed: true,
			PriceRules: classroomRules,
			ProductFamily: "deepseek_classroom", PriceVersion: ClassroomPriceVersion,
			DisplayMetadata: "{}", Locked: true,
		},
		{
			ProductType: ProductTypePackDeepSeekBigfish, DisplayName: "DeepSeek 大肥鱼",
			Category: SubscriptionCategoryPackage, Currency: "CNY",
			DurationDays: 15, BillingMultiplier: frozenV1BillingMultipliers[ProductTypePackDeepSeekBigfish],
			ModelScope: bigfishScope, AllowedChannels: bigfishChannels, MaxUpstreamCost: bigfishCosts,
			FallbackAllowed: false, MaxPurchasePerUser: 1,
			AllowBalancePay: false, AllowWalletOverflow: false, CourseEligible: false, RedemptionAllowed: false,
			PriceRules: bigfishRulesJSON,
			ProductFamily: BigfishProductFamily, PriceVersion: BigfishPriceVersion,
			DisplayMetadata: "{}", Locked: true,
		},
	}
	for index := range templates {
		if err := validateProductTemplate(&templates[index]); err != nil {
			return nil, err
		}
	}
	return templates, nil
}

// SeedProductTemplates inserts the core template rows that do not exist yet.
// Existing rows are never touched, so operational edits survive restarts.
func SeedProductTemplates() error {
	if DB == nil {
		return errors.New("database is nil")
	}
	templates, err := desiredProductTemplates()
	if err != nil {
		return err
	}
	for index := range templates {
		template := &templates[index]
		var count int64
		if err := DB.Model(&ProductTemplate{}).Where("product_type = ?", template.ProductType).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := DB.Create(template).Error; err != nil {
			return err
		}
		InvalidateProductTemplateCache(template.ProductType)
	}
	return nil
}

// BackfillSubscriptionPlanTemplateIds pins template_id on existing plans from
// their product_type. It also repairs drifted links; template rows themselves
// are never modified here.
func BackfillSubscriptionPlanTemplateIds() error {
	if DB == nil {
		return errors.New("database is nil")
	}
	templates, err := ListProductTemplates()
	if err != nil {
		return err
	}
	for index := range templates {
		template := &templates[index]
		if err := DB.Model(&SubscriptionPlan{}).
			Where("product_type = ? AND (template_id IS NULL OR template_id <> ?)", template.ProductType, template.Id).
			Update("template_id", template.Id).Error; err != nil {
			return err
		}
	}
	return nil
}

// MigrateProductTemplates runs at the end of the main migration, after
// MigrateCilaiV1Contracts: seed the core templates, then backfill plan links.
func MigrateProductTemplates() error {
	if err := SeedProductTemplates(); err != nil {
		return err
	}
	return BackfillSubscriptionPlanTemplateIds()
}

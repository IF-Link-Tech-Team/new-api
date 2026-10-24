package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/samber/hot"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Subscription duration units
const (
	SubscriptionDurationYear   = "year"
	SubscriptionDurationMonth  = "month"
	SubscriptionDurationDay    = "day"
	SubscriptionDurationHour   = "hour"
	SubscriptionDurationCustom = "custom"
)

// Subscription quota reset period
const (
	SubscriptionResetNever   = "never"
	SubscriptionResetDaily   = "daily"
	SubscriptionResetWeekly  = "weekly"
	SubscriptionResetMonthly = "monthly"
	SubscriptionResetCustom  = "custom"
)

var (
	ErrSubscriptionOrderNotFound      = errors.New("subscription order not found")
	ErrSubscriptionOrderStatusInvalid = errors.New("subscription order status invalid")
)

const (
	subscriptionPlanCacheNamespace     = "new-api:subscription_plan:v1"
	subscriptionPlanInfoCacheNamespace = "new-api:subscription_plan_info:v1"
)

var (
	subscriptionPlanCacheOnce     sync.Once
	subscriptionPlanInfoCacheOnce sync.Once

	subscriptionPlanCache     *cachex.HybridCache[SubscriptionPlan]
	subscriptionPlanInfoCache *cachex.HybridCache[SubscriptionPlanInfo]
)

func subscriptionPlanCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_CACHE_TTL", 300)
	if ttlSeconds <= 0 {
		ttlSeconds = 300
	}
	return time.Duration(ttlSeconds) * time.Second
}

func subscriptionPlanInfoCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_INFO_CACHE_TTL", 120)
	if ttlSeconds <= 0 {
		ttlSeconds = 120
	}
	return time.Duration(ttlSeconds) * time.Second
}

func subscriptionPlanCacheCapacity() int {
	capacity := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_CACHE_CAP", 5000)
	if capacity <= 0 {
		capacity = 5000
	}
	return capacity
}

func subscriptionPlanInfoCacheCapacity() int {
	capacity := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_INFO_CACHE_CAP", 10000)
	if capacity <= 0 {
		capacity = 10000
	}
	return capacity
}

func getSubscriptionPlanCache() *cachex.HybridCache[SubscriptionPlan] {
	subscriptionPlanCacheOnce.Do(func() {
		ttl := subscriptionPlanCacheTTL()
		subscriptionPlanCache = cachex.NewHybridCache[SubscriptionPlan](cachex.HybridCacheConfig[SubscriptionPlan]{
			Namespace: cachex.Namespace(subscriptionPlanCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[SubscriptionPlan]{},
			Memory: func() *hot.HotCache[string, SubscriptionPlan] {
				return hot.NewHotCache[string, SubscriptionPlan](hot.LRU, subscriptionPlanCacheCapacity()).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return subscriptionPlanCache
}

func getSubscriptionPlanInfoCache() *cachex.HybridCache[SubscriptionPlanInfo] {
	subscriptionPlanInfoCacheOnce.Do(func() {
		ttl := subscriptionPlanInfoCacheTTL()
		subscriptionPlanInfoCache = cachex.NewHybridCache[SubscriptionPlanInfo](cachex.HybridCacheConfig[SubscriptionPlanInfo]{
			Namespace: cachex.Namespace(subscriptionPlanInfoCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[SubscriptionPlanInfo]{},
			Memory: func() *hot.HotCache[string, SubscriptionPlanInfo] {
				return hot.NewHotCache[string, SubscriptionPlanInfo](hot.LRU, subscriptionPlanInfoCacheCapacity()).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return subscriptionPlanInfoCache
}

func subscriptionPlanCacheKey(id int) string {
	if id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

func InvalidateSubscriptionPlanCache(planId int) {
	if planId <= 0 {
		return
	}
	cache := getSubscriptionPlanCache()
	_, _ = cache.DeleteMany([]string{subscriptionPlanCacheKey(planId)})
	infoCache := getSubscriptionPlanInfoCache()
	_ = infoCache.Purge()
	invalidateStorefrontCatalogCache()
}

// Subscription plan
type SubscriptionPlan struct {
	Id int `json:"id"`

	Title    string `json:"title" gorm:"type:varchar(128);not null"`
	Subtitle string `json:"subtitle" gorm:"type:varchar(255);default:''"`

	// Display money amount (follow existing code style: float64 for money)
	PriceAmount float64 `json:"price_amount" gorm:"type:decimal(10,6);not null;default:0"`
	Currency    string  `json:"currency" gorm:"type:varchar(8);not null;default:'USD'"`

	DurationUnit  string `json:"duration_unit" gorm:"type:varchar(16);not null;default:'month'"`
	DurationValue int    `json:"duration_value" gorm:"type:int;not null;default:1"`
	CustomSeconds int64  `json:"custom_seconds" gorm:"type:bigint;not null;default:0"`

	Enabled bool `json:"enabled" gorm:"default:true"`
	// Listed controls catalog visibility independently from whether new paid
	// purchases or redemptions are accepted. Existing entitlements never consult it.
	Listed bool `json:"listed" gorm:"default:true;index"`
	// RedemptionEnabled is independent from paid-sale availability and catalog
	// visibility. This allows a hidden classroom SKU to accept authorized codes
	// without opening payment, while disabling redemption never invalidates an
	// entitlement that has already been provisioned.
	RedemptionEnabled bool `json:"redemption_enabled" gorm:"default:true;index"`
	SortOrder         int  `json:"sort_order" gorm:"type:int;default:0"`

	AllowBalancePay *bool `json:"allow_balance_pay"`

	// Allow falling back to wallet balance after subscription quota is exhausted (empty = true)
	AllowWalletOverflow *bool `json:"allow_wallet_overflow"`

	StripePriceId         string `json:"stripe_price_id" gorm:"type:varchar(128);default:''"`
	CreemProductId        string `json:"creem_product_id" gorm:"type:varchar(128);default:''"`
	WaffoPancakeProductId string `json:"waffo_pancake_product_id" gorm:"type:varchar(128);default:''"`

	// Max purchases per user (0 = unlimited)
	MaxPurchasePerUser int `json:"max_purchase_per_user" gorm:"type:int;default:0"`
	// New product orders freeze this deadline. Zero preserves the default for
	// clients created before this field existed; historical orders keep no TTL.
	ReservationTTLSeconds int64 `json:"reservation_ttl_seconds" gorm:"bigint;not null;default:0"`

	// Upgrade user group after purchase (empty = no change)
	UpgradeGroup string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`

	// Downgrade user group on expiry (empty = revert to the group held before purchase)
	DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`

	// Total quota (amount in quota units, 0 = unlimited)
	TotalAmount int64 `json:"total_amount" gorm:"type:bigint;not null;default:0"`

	// Quota reset period for plan
	QuotaResetPeriod        string `json:"quota_reset_period" gorm:"type:varchar(16);default:'never'"`
	QuotaResetCustomSeconds int64  `json:"quota_reset_custom_seconds" gorm:"type:bigint;default:0"`

	// ModelScope restricts which models may consume this plan (comma-separated
	// exact model names or prefixes, e.g. "deepseek-v4-flash,deepseek-v4-pro").
	// Empty = every model. Snapshotted onto UserSubscription at creation.
	ModelScope string `json:"model_scope" gorm:"type:varchar(255);default:''"`

	// Category distinguishes long-running subscriptions ("subscription", i.e.
	// 套餐) from one-shot traffic packs ("package", i.e. 流量包). UI uses
	// this to render different tabs and behavior. Defaults to "package" so
	// legacy one-shot plans (e.g. DeepSeek V4 Pro 限时流量包) stay classified
	// correctly even before admin sets it explicitly.
	Category string `json:"category" gorm:"type:varchar(16);default:'package';index"`

	// Frozen CILAI V1 product contract. Empty product_type keeps legacy plans
	// readable but excludes them from the strict V1 purchase path.
	ProductType       string  `json:"product_type" gorm:"type:varchar(32);default:'';index"`
	ProductCode       string  `json:"product_code" gorm:"type:varchar(64);default:'';index"`
	ProductFamily     string  `json:"product_family" gorm:"type:varchar(64);default:'';index"`
	ProductTier       int     `json:"product_tier" gorm:"type:int;default:0"`
	BillingMultiplier float64 `json:"billing_multiplier" gorm:"type:decimal(8,6);not null;default:1"`
	PriceVersion      string  `json:"price_version" gorm:"type:varchar(64);default:''"`
	// PriceBook opts a plan into product-owned pricing without a template or
	// commercial product type. Empty preserves the pre-migration contract.
	PriceBook       string `json:"price_book" gorm:"type:varchar(64);default:''"`
	ConfigRevision  int64  `json:"config_revision" gorm:"bigint;not null;default:0"`
	AllowedChannels string `json:"allowed_channels" gorm:"type:text"`
	MaxUpstreamCost string `json:"max_upstream_cost" gorm:"type:text"`
	FallbackAllowed bool   `json:"fallback_allowed"`
	CourseEligible  bool   `json:"course_eligible"`

	// TemplateId links the plan to its product_templates row. It is pinned
	// during ValidateV1Contract and backfilled for existing plans by the
	// product template migration.
	TemplateId int64 `json:"template_id" gorm:"type:bigint;not null;default:0;index"`

	// DisplayMetadata is reserved JSON for storefront rendering (card copy,
	// badges, benefits, images, featured/recommended markers, preview flag).
	// It is display-only: purchase gates keep reading enabled/listed.
	DisplayMetadata string `json:"display_metadata" gorm:"type:text"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

// Subscription plan categories. 套餐 vs 流量包.
const (
	SubscriptionCategorySubscription = "subscription" // 套餐：长期订阅
	SubscriptionCategoryPackage      = "package"      // 流量包：一次性配额
)

// NormalizeSubscriptionCategory coerces empty/invalid values to the package
// default so admin-side bugs don't break UI rendering.
func NormalizeSubscriptionCategory(c string) string {
	switch c {
	case SubscriptionCategorySubscription:
		return SubscriptionCategorySubscription
	default:
		return SubscriptionCategoryPackage
	}
}

func (p *SubscriptionPlan) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.ProductType == ProductTypePackDeepSeekBigfish {
		p.NormalizeDefaults()
		return p.validateV1ContractTx(tx)
	}
	return nil
}

func (p *SubscriptionPlan) AfterCreate(tx *gorm.DB) error {
	if p.ProductType != ProductTypePackDeepSeekBigfish {
		return nil
	}
	// GORM's legacy default:true tags would otherwise turn explicit false into
	// a live/listed/redeemable SKU. New Bigfish products always start as drafts;
	// reviewed activation must be an explicit, separate admin update.
	p.Enabled, p.Listed, p.RedemptionEnabled = false, false, false
	return tx.Model(p).UpdateColumns(map[string]interface{}{
		"enabled": false, "listed": false, "redemption_enabled": false,
	}).Error
}

func (p *SubscriptionPlan) BeforeUpdate(tx *gorm.DB) error {
	p.UpdatedAt = common.GetTimestamp()
	return nil
}

func (p *SubscriptionPlan) NormalizeDefaults() {
	if p.AllowBalancePay == nil {
		p.AllowBalancePay = common.GetPointer(true)
	}
	if p.AllowWalletOverflow == nil {
		p.AllowWalletOverflow = common.GetPointer(true)
	}
	if p.BillingMultiplier <= 0 && strings.TrimSpace(p.ProductType) == "" {
		p.BillingMultiplier = 1
	}
}

// Subscription order (payment -> webhook -> create UserSubscription)
type SubscriptionOrder struct {
	Id     int     `json:"id"`
	UserId int     `json:"user_id" gorm:"index"`
	PlanId int     `json:"plan_id" gorm:"index"`
	Money  float64 `json:"money"`

	TradeNo              string `json:"trade_no" gorm:"unique;type:varchar(255);index"`
	PaymentMethod        string `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider      string `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	Status               string `json:"status"`
	CreateTime           int64  `json:"create_time"`
	CompleteTime         int64  `json:"complete_time"`
	ReservationExpiresAt int64  `json:"reservation_expires_at" gorm:"bigint;not null;default:0"`
	// Compensation decisions persist with issuance, independently of log DB.
	CompensationActorId int    `json:"compensation_actor_id" gorm:"not null;default:0"`
	CompensationTime    int64  `json:"compensation_time" gorm:"bigint;not null;default:0"`
	CompensationNote    string `json:"compensation_note" gorm:"type:varchar(512);not null;default:''"`

	ProviderPayload string  `json:"provider_payload" gorm:"type:text"`
	PaidMoney       float64 `json:"paid_money" gorm:"type:decimal(10,6);not null;default:0"`
	PaidCurrency    string  `json:"paid_currency" gorm:"type:varchar(8);default:''"`
	ProviderTradeNo string  `json:"provider_trade_no" gorm:"type:varchar(255);default:'';index"`

	OrderType          string  `json:"order_type" gorm:"type:varchar(16);default:'purchase';index"`
	UserSubscriptionId *int    `json:"user_subscription_id" gorm:"index"`
	ProductType        string  `json:"product_type" gorm:"type:varchar(32);default:'';index"`
	ProductCode        string  `json:"product_code" gorm:"type:varchar(64);default:''"`
	ProductFamily      string  `json:"product_family" gorm:"type:varchar(64);default:'';index"`
	SourceTier         int     `json:"source_tier" gorm:"type:int;default:0"`
	TargetTier         int     `json:"target_tier" gorm:"type:int;default:0"`
	BillingMultiplier  float64 `json:"billing_multiplier" gorm:"type:decimal(8,6);not null;default:1"`
	PriceVersion       string  `json:"price_version" gorm:"type:varchar(64);default:''"`
	PriceBook          string  `json:"-" gorm:"type:varchar(64);default:'';index"`
	// The complete generic plan freezes non-price terms too, including limits,
	// group changes and wallet overflow. It never contains credentials.
	ProductPlanSnapshot string `json:"-" gorm:"type:text"`
	ModelScope          string `json:"model_scope" gorm:"type:text"`
	AllowedChannels     string `json:"allowed_channels" gorm:"type:text"`
	MaxUpstreamCost     string `json:"max_upstream_cost" gorm:"type:text"`
	FallbackAllowed     bool   `json:"fallback_allowed"`
	CourseEligible      bool   `json:"course_eligible"`
	ProvisioningStatus  string `json:"provisioning_status" gorm:"type:varchar(32);default:'pending';index"`
	ProvisioningError   string `json:"provisioning_error" gorm:"type:text"`

	// Immutable commercial snapshot captured before the customer is sent to a
	// payment provider. Completion must never read mutable price/quota/duration
	// fields from the live plan for a V1 order.
	PlanTitleSnapshot        string  `json:"plan_title_snapshot" gorm:"type:varchar(128);default:''"`
	TargetPriceAmount        float64 `json:"target_price_amount" gorm:"type:decimal(10,6);not null;default:0"`
	CurrencySnapshot         string  `json:"currency_snapshot" gorm:"type:varchar(8);default:''"`
	DurationUnitSnapshot     string  `json:"duration_unit_snapshot" gorm:"type:varchar(16);default:''"`
	DurationValueSnapshot    int     `json:"duration_value_snapshot" gorm:"type:int;default:0"`
	CustomSecondsSnapshot    int64   `json:"custom_seconds_snapshot" gorm:"type:bigint;default:0"`
	TotalAmountSnapshot      int64   `json:"total_amount_snapshot" gorm:"type:bigint;not null;default:0"`
	QuotaResetPeriodSnapshot string  `json:"quota_reset_period_snapshot" gorm:"type:varchar(16);default:''"`
	QuotaResetCustomSnapshot int64   `json:"quota_reset_custom_snapshot" gorm:"type:bigint;default:0"`
	CategorySnapshot         string  `json:"category_snapshot" gorm:"type:varchar(16);default:''"`
	// Legacy orders are explicitly classified during migration. They never
	// masquerade as a V1 SKU, but pending paid orders remain recoverable.
	LegacyCompatibilityState string `json:"legacy_compatibility_state" gorm:"type:varchar(32);default:'';index"`
	LegacyReviewReason       string `json:"legacy_review_reason" gorm:"type:text"`
}

func (o *SubscriptionOrder) Insert() error {
	if o.CreateTime == 0 {
		o.CreateTime = common.GetTimestamp()
	}
	if o.OrderType == "" {
		o.OrderType = SubscriptionOrderTypePurchase
	}
	if o.ProvisioningStatus == "" {
		o.ProvisioningStatus = ProvisioningStatusPending
	}
	if o.PlanId > 0 && o.ProductType == "" && o.PriceBook == "" {
		plan, err := GetSubscriptionPlanById(o.PlanId)
		if err != nil {
			return err
		}
		if plan.PriceBook != "" {
			return errors.New("product orders require transactional checkout")
		}
		if err := plan.ValidateV1Contract(); err != nil {
			return err
		}
		if err := validateV1ProductPriceCatalogTx(DB, plan); err != nil {
			return err
		}
		if strings.TrimSpace(plan.ProductType) == "" {
			if err := o.SnapshotLegacyPlan(plan); err != nil {
				return err
			}
		} else {
			o.SnapshotPlan(plan)
		}
	}
	return DB.Create(o).Error
}

func (o *SubscriptionOrder) SnapshotPlan(plan *SubscriptionPlan) {
	if o == nil || plan == nil {
		return
	}
	o.ProductType = strings.TrimSpace(plan.ProductType)
	o.ProductCode = strings.TrimSpace(plan.ProductCode)
	o.ProductFamily = strings.TrimSpace(plan.ProductFamily)
	o.TargetTier = plan.ProductTier
	o.BillingMultiplier = plan.BillingMultiplier
	o.PriceVersion = strings.TrimSpace(plan.PriceVersion)
	o.PriceBook = plan.PriceBook
	if RequiresStrictV1RoutePolicy(plan.ProductType) && plan.Id > 0 {
		o.PriceBook = fmt.Sprintf("product:%d", plan.Id)
	}
	o.ModelScope = strings.TrimSpace(plan.ModelScope)
	o.AllowedChannels = strings.TrimSpace(plan.AllowedChannels)
	o.MaxUpstreamCost = strings.TrimSpace(plan.MaxUpstreamCost)
	o.FallbackAllowed = plan.FallbackAllowed
	o.CourseEligible = plan.CourseEligible
	o.PlanTitleSnapshot = strings.TrimSpace(plan.Title)
	o.TargetPriceAmount = plan.PriceAmount
	o.CurrencySnapshot = strings.TrimSpace(plan.Currency)
	o.DurationUnitSnapshot = strings.TrimSpace(plan.DurationUnit)
	o.DurationValueSnapshot = plan.DurationValue
	o.CustomSecondsSnapshot = plan.CustomSeconds
	o.TotalAmountSnapshot = plan.TotalAmount
	o.QuotaResetPeriodSnapshot = NormalizeResetPeriod(plan.QuotaResetPeriod)
	o.QuotaResetCustomSnapshot = plan.QuotaResetCustomSeconds
	o.CategorySnapshot = NormalizeSubscriptionCategory(plan.Category)
}

// SnapshotLegacyPlan preserves the amount/currency/model semantics of a
// pre-V1 order while making its non-V1 status explicit. No V1 product is
// invented from an empty historical product_type.
func (o *SubscriptionOrder) SnapshotLegacyPlan(plan *SubscriptionPlan) error {
	if o == nil || plan == nil || plan.Id <= 0 {
		return errors.New("invalid legacy order snapshot")
	}
	if strings.TrimSpace(plan.Currency) == "" {
		return errors.New("legacy plan currency is missing")
	}
	o.ProductType = ProductTypeLegacySubscription
	o.ProductCode = fmt.Sprintf("legacy-plan-%d", plan.Id)
	o.ProductFamily = fmt.Sprintf("legacy-plan-%d", plan.Id)
	o.SourceTier = 0
	o.TargetTier = 0
	o.BillingMultiplier = 1
	o.PriceVersion = "legacy"
	o.ModelScope = strings.TrimSpace(plan.ModelScope)
	o.AllowedChannels = strings.TrimSpace(plan.AllowedChannels)
	o.MaxUpstreamCost = strings.TrimSpace(plan.MaxUpstreamCost)
	o.FallbackAllowed = true
	o.CourseEligible = false
	o.PlanTitleSnapshot = strings.TrimSpace(plan.Title)
	o.TargetPriceAmount = plan.PriceAmount
	o.CurrencySnapshot = strings.TrimSpace(plan.Currency)
	o.DurationUnitSnapshot = strings.TrimSpace(plan.DurationUnit)
	o.DurationValueSnapshot = plan.DurationValue
	o.CustomSecondsSnapshot = plan.CustomSeconds
	o.TotalAmountSnapshot = plan.TotalAmount
	o.QuotaResetPeriodSnapshot = NormalizeResetPeriod(plan.QuotaResetPeriod)
	o.QuotaResetCustomSnapshot = plan.QuotaResetCustomSeconds
	o.CategorySnapshot = NormalizeSubscriptionCategory(plan.Category)
	o.LegacyCompatibilityState = LegacyOrderStateReady
	o.LegacyReviewReason = ""
	return nil
}

func (o *SubscriptionOrder) Update() error {
	return DB.Save(o).Error
}

func GetSubscriptionOrderByTradeNo(tradeNo string) *SubscriptionOrder {
	if tradeNo == "" {
		return nil
	}
	var order SubscriptionOrder
	if err := DB.Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
		return nil
	}
	return &order
}

// GetUserSubscriptionOrders returns only orders owned by the user. The
// controller projects these rows to a public DTO so provider payloads never
// leave the server. tradeNo is optional and is used by payment-return polling.
func GetUserSubscriptionOrders(userId int, tradeNo string, limit int) ([]SubscriptionOrder, error) {
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	query := DB.Where("user_id = ?", userId)
	if strings.TrimSpace(tradeNo) != "" {
		query = query.Where("trade_no = ?", strings.TrimSpace(tradeNo))
	}
	var orders []SubscriptionOrder
	if err := query.Order("id desc").Limit(limit).Find(&orders).Error; err != nil {
		return nil, err
	}
	return orders, nil
}

// CreateSubscriptionOrder freezes a plan and classifies the operation as a
// new purchase or an in-period monthly upgrade before any payment page is
// opened. Providers with a fixed external product price must pass
// supportsAmountOverride=false; they are rejected for upgrades because they
// cannot safely charge the locally calculated difference.
func CreateSubscriptionOrder(userId int, planId int, tradeNo string, paymentMethod string, paymentProvider string, supportsAmountOverride bool) (*SubscriptionOrder, *SubscriptionPlan, error) {
	var order *SubscriptionOrder
	var plan *SubscriptionPlan
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		order, plan, err = createSubscriptionOrderTx(tx, userId, planId, tradeNo, paymentMethod, paymentProvider, supportsAmountOverride)
		return err
	})
	return order, plan, err
}

func createSubscriptionOrderTx(tx *gorm.DB, userId int, planId int, tradeNo string, paymentMethod string, paymentProvider string, supportsAmountOverride bool) (*SubscriptionOrder, *SubscriptionPlan, error) {
	if tx == nil || userId <= 0 || planId <= 0 || strings.TrimSpace(tradeNo) == "" {
		return nil, nil, errors.New("invalid subscription order args")
	}
	// Acquire the user serialization lock before any consistent table read.
	// MySQL REPEATABLE READ would otherwise retain a snapshot taken before a
	// competing checkout commits, even after waiting for this user's row lock.
	var lockedUser User
	if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&lockedUser).Error; err != nil {
		return nil, nil, err
	}
	plan, err := getSubscriptionPlanByIdTx(tx, planId)
	if err != nil {
		return nil, nil, err
	}
	if plan.PriceBook != "" {
		// Serialize checkout with generic plan edits and price publication.
		// Display cache state never decides whether a new sale is valid.
		if err := lockForUpdate(tx).First(plan, planId).Error; err != nil {
			return nil, nil, err
		}
	}
	if !plan.Enabled {
		return nil, nil, errors.New("套餐未启用")
	}
	if plan.PriceAmount < 0 {
		return nil, nil, errors.New("套餐价格不能为负数")
	}
	if plan.PriceBook != "" {
		// Check the locked plan, rather than the controller's display-cache
		// copy. These gateways have a fixed settlement currency.
		switch paymentProvider {
		case PaymentProviderAlipay, PaymentProviderIFLinkPay, PaymentProviderEpay:
			if plan.Currency != "CNY" {
				return nil, nil, errors.New("this payment provider requires a CNY product")
			}
		case PaymentProviderWaffoPancake:
			if plan.Currency != "USD" {
				return nil, nil, errors.New("this payment provider requires a USD product")
			}
		}
	}
	if err := plan.validateV1ContractTx(tx); err != nil {
		return nil, nil, err
	}
	if err := validateV1ProductPriceCatalogTx(tx, plan); err != nil {
		return nil, nil, err
	}

	if plan.ProductType == ProductTypePackDeepSeekBigfish {
		if err := checkBigfishPurchaseHistoryTx(tx, userId); err != nil {
			return nil, nil, err
		}
		var pending SubscriptionOrder
		result := tx.Where("user_id = ? AND product_type = ? AND product_family = ? AND status = ?", userId, ProductTypePackDeepSeekBigfish, BigfishProductFamily, common.TopUpStatusPending).Order("id").Limit(1).Find(&pending)
		if result.Error != nil {
			return nil, nil, result.Error
		}
		if result.RowsAffected > 0 {
			if pending.PlanId == plan.Id && pending.PaymentProvider == PaymentProviderIFLinkPay && paymentProvider == PaymentProviderIFLinkPay && pending.PaymentMethod == paymentMethod {
				frozen, err := provisionPlanFromOrder(tx, &pending, plan)
				return &pending, frozen, err
			}
			return nil, nil, errors.New("已有待支付的大肥鱼活动订单，请完成原订单或联系管理员取消后再选档")
		}
	}
	if plan.ProductType == ProductTypeMonthlySubscription {
		var pendingCount int64
		if err := tx.Model(&SubscriptionOrder{}).
			Where("user_id = ? AND product_family = ? AND order_type = ? AND status = ?", userId, plan.ProductFamily, SubscriptionOrderTypePurchase, common.TopUpStatusPending).
			Count(&pendingCount).Error; err != nil {
			return nil, nil, err
		}
		if pendingCount > 0 {
			return nil, nil, errors.New("已有待支付的月套餐订单")
		}
	}

	order := &SubscriptionOrder{
		UserId:             userId,
		PlanId:             plan.Id,
		Money:              plan.PriceAmount,
		TradeNo:            strings.TrimSpace(tradeNo),
		PaymentMethod:      strings.TrimSpace(paymentMethod),
		PaymentProvider:    strings.TrimSpace(paymentProvider),
		Status:             common.TopUpStatusPending,
		CreateTime:         common.GetTimestamp(),
		OrderType:          SubscriptionOrderTypePurchase,
		ProvisioningStatus: ProvisioningStatusPending,
	}
	order.SnapshotPlan(plan)
	if plan.PriceBook != "" {
		ttl := plan.ReservationTTLSeconds
		if ttl == 0 {
			ttl = DefaultProductReservationTTL
		}
		order.CreateTime, err = productReservationTimeTx(tx)
		if err != nil {
			return nil, nil, err
		}
		if ttl < 0 || ttl > math.MaxInt64-order.CreateTime {
			return nil, nil, errors.New("invalid product reservation lifetime")
		}
		order.ReservationExpiresAt = order.CreateTime + ttl
		data, err := common.Marshal(plan)
		if err != nil {
			return nil, nil, err
		}
		order.ProductPlanSnapshot = string(data)
	}

	if plan.ProductType == ProductTypeMonthlySubscription {
		now := getDBTimestampTx(tx)
		var cycle MonthlySubscriptionCycle
		result := lockForUpdate(tx).Where("user_id = ? AND product_family = ?", userId, plan.ProductFamily).Limit(1).Find(&cycle)
		if result.Error != nil {
			return nil, nil, result.Error
		}
		if result.RowsAffected > 0 && cycle.SubscriptionId > 0 && cycle.ExpiresAt > now {
			var current UserSubscription
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", cycle.SubscriptionId, userId).First(&current).Error; err != nil {
				return nil, nil, err
			}
			if current.Status != "active" || current.EndTime <= now || current.ProductFamily != plan.ProductFamily {
				return nil, nil, errors.New("月套餐周期记录与权益状态不一致")
			}
			if plan.ProductTier <= current.ProductTier {
				return nil, nil, errors.New("当前周期不可重复购买或降档，只能补差升级")
			}
			if !supportsAmountOverride {
				return nil, nil, errors.New("该支付方式不支持月套餐补差升级，请使用支付宝、易支付或余额")
			}
			currentPrice := current.PriceAmountSnapshot
			if currentPrice <= 0 {
				var sourcePlan SubscriptionPlan
				if err := tx.Where("id = ?", current.PlanId).First(&sourcePlan).Error; err != nil {
					return nil, nil, errors.New("原月套餐缺少不可变价格快照，无法安全计算升级差价")
				}
				currentPrice = sourcePlan.PriceAmount
			}
			delta := decimal.NewFromFloat(plan.PriceAmount).Sub(decimal.NewFromFloat(currentPrice))
			if !delta.IsPositive() {
				return nil, nil, errors.New("月套餐升级差价必须大于零")
			}
			order.Money, _ = delta.Float64()
			order.OrderType = SubscriptionOrderTypeUpgrade
			order.SourceTier = current.ProductTier
			order.TargetTier = plan.ProductTier
			order.UserSubscriptionId = &current.Id

			var pendingCount int64
			if err := tx.Model(&SubscriptionOrder{}).
				Where("user_id = ? AND product_family = ? AND order_type = ? AND status = ?", userId, plan.ProductFamily, SubscriptionOrderTypeUpgrade, common.TopUpStatusPending).
				Count(&pendingCount).Error; err != nil {
				return nil, nil, err
			}
			if pendingCount > 0 {
				return nil, nil, errors.New("已有待支付的月套餐升级订单")
			}
		}
	}

	if order.OrderType == SubscriptionOrderTypePurchase && plan.MaxPurchasePerUser > 0 && plan.ProductType != ProductTypePackDeepSeekBigfish {
		var count int64
		if err := tx.Model(&UserSubscription{}).
			Where("user_id = ? AND plan_id = ?", userId, plan.Id).
			Count(&count).Error; err != nil {
			return nil, nil, err
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			return nil, nil, errors.New("已达到该套餐购买上限")
		}
		var pendingCount int64
		pendingQuery := tx.Model(&SubscriptionOrder{}).
			Where("user_id = ? AND order_type = ? AND status = ?", userId, SubscriptionOrderTypePurchase, common.TopUpStatusPending)
		if plan.PriceBook != "" {
			// Expiry is effective at the deadline, even if no cleanup worker has
			// run. Do not update order rows while holding the user lock: callbacks
			// take the order lock first, so that would invert their lock order.
			pendingQuery = pendingQuery.Where("reservation_expires_at = 0 OR reservation_expires_at > ?", order.CreateTime)
		}
		if plan.ProductType == ProductTypeMonthlySubscription {
			pendingQuery = pendingQuery.Where("product_family = ?", plan.ProductFamily)
		} else {
			pendingQuery = pendingQuery.Where("plan_id = ?", plan.Id)
		}
		if err := pendingQuery.Count(&pendingCount).Error; err != nil {
			return nil, nil, err
		}
		if plan.PriceBook != "" && pendingCount >= int64(plan.MaxPurchasePerUser)-count {
			return nil, nil, errors.New("已达到该套餐购买上限（含待支付订单）")
		}
		if plan.PriceBook == "" && pendingCount > 0 {
			return nil, nil, errors.New("该套餐已有待支付订单")
		}
	}
	if err := tx.Create(order).Error; err != nil {
		return nil, nil, err
	}
	return order, plan, nil
}

// User subscription instance
type UserSubscription struct {
	Id     int `json:"id"`
	UserId int `json:"user_id" gorm:"index;index:idx_user_sub_active,priority:1"`
	PlanId int `json:"plan_id" gorm:"index"`

	AmountTotal int64 `json:"amount_total" gorm:"type:bigint;not null;default:0"`
	AmountUsed  int64 `json:"amount_used" gorm:"type:bigint;not null;default:0"`

	StartTime int64  `json:"start_time" gorm:"bigint"`
	EndTime   int64  `json:"end_time" gorm:"bigint;index;index:idx_user_sub_active,priority:3"`
	Status    string `json:"status" gorm:"type:varchar(32);index;index:idx_user_sub_active,priority:2"` // active/expired/cancelled

	Source string `json:"source" gorm:"type:varchar(32);default:'order'"` // order/admin

	LastResetTime int64 `json:"last_reset_time" gorm:"type:bigint;default:0"`
	NextResetTime int64 `json:"next_reset_time" gorm:"type:bigint;default:0;index"`

	UpgradeGroup  string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`
	PrevUserGroup string `json:"prev_user_group" gorm:"type:varchar(64);default:''"`

	// Downgrade target group on expiry (snapshot from plan; empty = revert to PrevUserGroup)
	DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`

	// Legacy policy snapshot retained for API/data compatibility. The current
	// key-selected billing path never falls back from a bound pool to the wallet.
	AllowWalletOverflow bool `json:"allow_wallet_overflow"`

	// ModelScope snapshot from the plan: when non-empty, only requests whose
	// model matches an entry may consume this subscription. A bound key rejects
	// every other model without searching another pool or the wallet.
	ModelScope string `json:"model_scope" gorm:"type:varchar(255);default:''"`

	// DailyQuotaLimit caps this subscription's reservations per UTC calendar
	// day in raw quota units. 0 = no cap. Refunded records are excluded. This is
	// a reservation cap, not a sum of actual settled charges. Both bound and
	// legacy selectors enforce it; a bound key cannot fall back to the wallet.
	DailyQuotaLimit int64 `json:"daily_quota_limit" gorm:"type:bigint;not null;default:0"`

	ProductType         string  `json:"product_type" gorm:"type:varchar(32);default:'';index"`
	ProductCode         string  `json:"product_code" gorm:"type:varchar(64);default:''"`
	ProductFamily       string  `json:"product_family" gorm:"type:varchar(64);default:'';index:idx_user_product_cycle,priority:2"`
	ProductTier         int     `json:"product_tier" gorm:"type:int;default:0"`
	BillingMultiplier   float64 `json:"billing_multiplier" gorm:"type:decimal(8,6);not null;default:1"`
	PriceVersion        string  `json:"price_version" gorm:"type:varchar(64);default:''"`
	PriceBook           string  `json:"-" gorm:"type:varchar(64);default:'';index"`
	AllowedChannels     string  `json:"allowed_channels" gorm:"type:text"`
	MaxUpstreamCost     string  `json:"max_upstream_cost" gorm:"type:text"`
	FallbackAllowed     bool    `json:"fallback_allowed"`
	CourseEligible      bool    `json:"course_eligible"`
	PeriodId            string  `json:"period_id" gorm:"type:varchar(64);default:'';index"`
	PriceAmountSnapshot float64 `json:"price_amount_snapshot" gorm:"type:decimal(10,6);not null;default:0"`
	CurrencySnapshot    string  `json:"currency_snapshot" gorm:"type:varchar(8);default:''"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (s *UserSubscription) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	s.CreatedAt = now
	s.UpdatedAt = now
	return nil
}

func (s *UserSubscription) BeforeUpdate(tx *gorm.DB) error {
	s.UpdatedAt = common.GetTimestamp()
	return nil
}

type SubscriptionSummary struct {
	Subscription *UserSubscription `json:"subscription"`
}

type SubscriptionResetResult struct {
	PlanId           int    `json:"plan_id"`
	MatchedCount     int    `json:"matched_count"`
	ResetCount       int    `json:"reset_count"`
	UserCount        int    `json:"user_count"`
	AdvanceResetTime bool   `json:"advance_reset_time"`
	PlanTitle        string `json:"-"`
	AffectedUserIds  []int  `json:"-"`
}

func calcPlanEndTime(start time.Time, plan *SubscriptionPlan) (int64, error) {
	if plan == nil {
		return 0, errors.New("plan is nil")
	}
	if plan.DurationValue <= 0 && plan.DurationUnit != SubscriptionDurationCustom {
		return 0, errors.New("duration_value must be > 0")
	}
	switch plan.DurationUnit {
	case SubscriptionDurationYear:
		return start.AddDate(plan.DurationValue, 0, 0).Unix(), nil
	case SubscriptionDurationMonth:
		return start.AddDate(0, plan.DurationValue, 0).Unix(), nil
	case SubscriptionDurationDay:
		return start.Add(time.Duration(plan.DurationValue) * 24 * time.Hour).Unix(), nil
	case SubscriptionDurationHour:
		return start.Add(time.Duration(plan.DurationValue) * time.Hour).Unix(), nil
	case SubscriptionDurationCustom:
		if plan.CustomSeconds <= 0 {
			return 0, errors.New("custom_seconds must be > 0")
		}
		return start.Add(time.Duration(plan.CustomSeconds) * time.Second).Unix(), nil
	default:
		return 0, fmt.Errorf("invalid duration_unit: %s", plan.DurationUnit)
	}
}

func NormalizeResetPeriod(period string) string {
	switch strings.TrimSpace(period) {
	case SubscriptionResetDaily, SubscriptionResetWeekly, SubscriptionResetMonthly, SubscriptionResetCustom:
		return strings.TrimSpace(period)
	default:
		return SubscriptionResetNever
	}
}

func calcNextResetTime(base time.Time, plan *SubscriptionPlan, endUnix int64) int64 {
	if plan == nil {
		return 0
	}
	period := NormalizeResetPeriod(plan.QuotaResetPeriod)
	if period == SubscriptionResetNever {
		return 0
	}
	var next time.Time
	switch period {
	case SubscriptionResetDaily:
		next = time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location()).
			AddDate(0, 0, 1)
	case SubscriptionResetWeekly:
		// Align to next Monday 00:00
		weekday := int(base.Weekday()) // Sunday=0
		// Convert to Monday=1..Sunday=7
		if weekday == 0 {
			weekday = 7
		}
		daysUntil := 8 - weekday
		next = time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location()).
			AddDate(0, 0, daysUntil)
	case SubscriptionResetMonthly:
		// Align to first day of next month 00:00
		next = time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, base.Location()).
			AddDate(0, 1, 0)
	case SubscriptionResetCustom:
		if plan.QuotaResetCustomSeconds <= 0 {
			return 0
		}
		next = base.Add(time.Duration(plan.QuotaResetCustomSeconds) * time.Second)
	default:
		return 0
	}
	if endUnix > 0 && next.Unix() > endUnix {
		return 0
	}
	return next.Unix()
}

func GetSubscriptionPlanById(id int) (*SubscriptionPlan, error) {
	return getSubscriptionPlanByIdTx(nil, id)
}

func getSubscriptionPlanByIdTx(tx *gorm.DB, id int) (*SubscriptionPlan, error) {
	if id <= 0 {
		return nil, errors.New("invalid plan id")
	}
	key := subscriptionPlanCacheKey(id)
	// Transactions must read their own database snapshot. A display cache may
	// still contain a plan that was just disabled or repriced, and caching an
	// uncommitted plan would leak a change even if its transaction rolls back.
	if tx == nil && key != "" {
		if cached, found, err := getSubscriptionPlanCache().Get(key); err == nil && found {
			cached.NormalizeDefaults()
			return &cached, nil
		}
	}
	var plan SubscriptionPlan
	query := DB
	if tx != nil {
		query = tx
	}
	if err := query.Where("id = ?", id).First(&plan).Error; err != nil {
		return nil, err
	}
	plan.NormalizeDefaults()
	if tx == nil {
		_ = getSubscriptionPlanCache().SetWithTTL(key, plan, subscriptionPlanCacheTTL())
	}
	return &plan, nil
}

func CountUserSubscriptionsByPlan(userId int, planId int) (int64, error) {
	if userId <= 0 || planId <= 0 {
		return 0, errors.New("invalid userId or planId")
	}
	var count int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", userId, planId).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func getUserGroupByIdTx(tx *gorm.DB, userId int) (string, error) {
	if userId <= 0 {
		return "", errors.New("invalid userId")
	}
	if tx == nil {
		tx = DB
	}
	var group string
	if err := lockForUpdate(tx).Model(&User{}).Where("id = ?", userId).Select(commonGroupCol).Find(&group).Error; err != nil {
		return "", err
	}
	return group, nil
}

func downgradeUserGroupForSubscriptionTx(tx *gorm.DB, sub *UserSubscription, now int64) (string, error) {
	if tx == nil || sub == nil {
		return "", errors.New("invalid downgrade args")
	}
	downgradeGroup := strings.TrimSpace(sub.DowngradeGroup)
	upgradeGroup := strings.TrimSpace(sub.UpgradeGroup)
	// Nothing to do if neither an explicit downgrade target nor an upgrade snapshot exists.
	if downgradeGroup == "" && upgradeGroup == "" {
		return "", nil
	}
	currentGroup, err := getUserGroupByIdTx(tx, sub.UserId)
	if err != nil {
		return "", err
	}
	// If another active upgraded subscription exists, keep the current group.
	var activeSub UserSubscription
	activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND id <> ? AND upgrade_group <> ''",
		sub.UserId, "active", now, sub.Id).
		Order("end_time desc, id desc").
		Limit(1).
		Find(&activeSub)
	if activeQuery.Error == nil && activeQuery.RowsAffected > 0 {
		return "", nil
	}
	// Determine the downgrade target: an explicit downgrade group takes precedence,
	// otherwise revert to the group held before purchase (legacy behavior).
	target := downgradeGroup
	if target == "" {
		// Legacy behavior: only revert when the subscription actually elevated the user.
		if currentGroup != upgradeGroup {
			return "", nil
		}
		target = strings.TrimSpace(sub.PrevUserGroup)
	}
	if target == "" || target == currentGroup {
		return "", nil
	}
	if err := tx.Model(&User{}).Where("id = ?", sub.UserId).
		Update("group", target).Error; err != nil {
		return "", err
	}
	return target, nil
}

func CreateUserSubscriptionFromPlanTx(tx *gorm.DB, userId int, plan *SubscriptionPlan, source string) (*UserSubscription, error) {
	if tx == nil {
		return nil, errors.New("tx is nil")
	}
	if plan == nil || plan.Id == 0 {
		return nil, errors.New("invalid plan")
	}
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	// Validation normalizes fields and pins the template. A plan may be shared
	// by concurrent purchases; mutate only this issuance's local snapshot.
	planSnapshot := *plan
	plan = &planSnapshot
	plan.NormalizeDefaults()
	if err := plan.validateV1ContractTx(tx); err != nil {
		return nil, err
	}
	if plan.PriceBook != "" {
		if err := validateProductPlanPrices(tx, plan, false); err != nil {
			return nil, err
		}
	} else if err := validateV1ProductPriceCatalogTx(tx, plan); err != nil {
		return nil, err
	}
	nowUnix := getDBTimestampTx(tx)
	if plan.ProductType == ProductTypePackDeepSeekBigfish {
		var lockedUser User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&lockedUser).Error; err != nil {
			return nil, err
		}
		if err := checkBigfishPurchaseHistoryTx(tx, userId); err != nil {
			return nil, err
		}
	}
	var monthlyCycle *MonthlySubscriptionCycle
	periodId := ""
	if plan.ProductType == ProductTypeMonthlySubscription {
		// Lock the user row first. This serializes all monthly tiers for the user
		// and closes the cross-SKU concurrent-purchase race on every supported DB.
		var lockedUser User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&lockedUser).Error; err != nil {
			return nil, err
		}
		var cycle MonthlySubscriptionCycle
		result := tx.Where("user_id = ? AND product_family = ?", userId, plan.ProductFamily).Limit(1).Find(&cycle)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected > 0 && cycle.ExpiresAt > nowUnix && cycle.SubscriptionId > 0 {
			return nil, errors.New("当前 30 天周期已有月套餐，只允许补差升级")
		}
		monthlyCycle = &cycle
		periodId = fmt.Sprintf("M%d-%d-%s", userId, nowUnix, common.GetRandomString(8))
	}
	if plan.MaxPurchasePerUser > 0 {
		var count int64
		if err := tx.Model(&UserSubscription{}).
			Where("user_id = ? AND plan_id = ?", userId, plan.Id).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			return nil, common.NewMessage("Purchase limit for this plan has been reached")
		}
	}
	now := time.Unix(nowUnix, 0)
	endUnix, err := calcPlanEndTime(now, plan)
	if err != nil {
		return nil, err
	}
	resetBase := now
	nextReset := calcNextResetTime(resetBase, plan, endUnix)
	lastReset := int64(0)
	if nextReset > 0 {
		lastReset = now.Unix()
	}
	upgradeGroup := strings.TrimSpace(plan.UpgradeGroup)
	prevGroup := ""
	if upgradeGroup != "" {
		currentGroup, err := getUserGroupByIdTx(tx, userId)
		if err != nil {
			return nil, err
		}
		if currentGroup != upgradeGroup {
			prevGroup = currentGroup
			if err := tx.Model(&User{}).Where("id = ?", userId).
				Update("group", upgradeGroup).Error; err != nil {
				return nil, err
			}
		}
	}
	allowWalletOverflow := true
	if plan.AllowWalletOverflow != nil {
		allowWalletOverflow = *plan.AllowWalletOverflow
	}
	priceBook := plan.PriceBook
	if RequiresStrictV1RoutePolicy(plan.ProductType) {
		priceBook, err = ProductPriceBook(plan.Id)
		if err != nil {
			return nil, err
		}
	}
	sub := &UserSubscription{
		UserId:              userId,
		PlanId:              plan.Id,
		AmountTotal:         plan.TotalAmount,
		AmountUsed:          0,
		StartTime:           now.Unix(),
		EndTime:             endUnix,
		Status:              "active",
		Source:              source,
		LastResetTime:       lastReset,
		NextResetTime:       nextReset,
		UpgradeGroup:        upgradeGroup,
		PrevUserGroup:       prevGroup,
		DowngradeGroup:      strings.TrimSpace(plan.DowngradeGroup),
		AllowWalletOverflow: allowWalletOverflow,
		ModelScope:          strings.TrimSpace(plan.ModelScope),
		ProductType:         strings.TrimSpace(plan.ProductType),
		ProductCode:         strings.TrimSpace(plan.ProductCode),
		ProductFamily:       strings.TrimSpace(plan.ProductFamily),
		ProductTier:         plan.ProductTier,
		BillingMultiplier:   plan.BillingMultiplier,
		PriceVersion:        strings.TrimSpace(plan.PriceVersion),
		PriceBook:           priceBook,
		AllowedChannels:     strings.TrimSpace(plan.AllowedChannels),
		MaxUpstreamCost:     strings.TrimSpace(plan.MaxUpstreamCost),
		FallbackAllowed:     plan.FallbackAllowed,
		CourseEligible:      plan.CourseEligible,
		PeriodId:            periodId,
		PriceAmountSnapshot: plan.PriceAmount,
		CurrencySnapshot:    strings.TrimSpace(plan.Currency),
		CreatedAt:           common.GetTimestamp(),
		UpdatedAt:           common.GetTimestamp(),
	}
	if err := tx.Create(sub).Error; err != nil {
		return nil, err
	}
	if monthlyCycle != nil {
		monthlyCycle.UserId = userId
		monthlyCycle.ProductFamily = plan.ProductFamily
		monthlyCycle.PeriodId = periodId
		monthlyCycle.SubscriptionId = sub.Id
		monthlyCycle.StartedAt = sub.StartTime
		monthlyCycle.ExpiresAt = sub.EndTime
		monthlyCycle.CurrentTier = sub.ProductTier
		monthlyCycle.UpgradeHistory = "[]"
		if monthlyCycle.Id == 0 {
			if err := tx.Create(monthlyCycle).Error; err != nil {
				return nil, err
			}
		} else if err := tx.Save(monthlyCycle).Error; err != nil {
			return nil, err
		}
	}
	if sub.CourseEligible {
		if err := grantV1CourseEntitlementTx(tx, sub, source); err != nil {
			return nil, err
		}
	}
	return sub, nil
}

func grantV1CourseEntitlementTx(tx *gorm.DB, sub *UserSubscription, source string) error {
	if tx == nil || sub == nil || sub.Id <= 0 || sub.UserId <= 0 {
		return errors.New("invalid course entitlement args")
	}
	grant := CourseEntitlement{
		UserId:             sub.UserId,
		UserSubscriptionId: sub.Id,
		CourseCode:         CourseCodeV1Foundation,
		Source:             strings.TrimSpace(source),
		GrantedAt:          common.GetTimestamp(),
		Status:             "active",
	}
	return tx.Where("user_id = ? AND course_code = ?", grant.UserId, grant.CourseCode).
		FirstOrCreate(&grant).Error
}

// CreateBoundTokenForSubscription 在已落库的 UserSubscription 上, 给用户生成一个
// 强绑定的 API token: 该 token 仅能扣此订阅, 不能串其他订阅、不能回退钱包;
// model_limits 取自订阅的 model_scope(空表示全部);key 自动生成;token 过期
// 时间与订阅 end_time 一致(订阅到期后 token 自动失效)。
//
// 调用方必须保证 subscriptionId 是已 commit 的、status=active 的、属于 userId 的订阅。
func createBoundTokenForSubscriptionTx(tx *gorm.DB, userId int, subscriptionId int) (*Token, string, error) {
	if tx == nil {
		return nil, "", errors.New("tx is nil")
	}
	if userId <= 0 || subscriptionId <= 0 {
		return nil, "", errors.New("invalid args")
	}
	var sub UserSubscription
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ? AND status = ?", subscriptionId, userId, "active").
		First(&sub).Error; err != nil {
		return nil, "", fmt.Errorf("subscription #%d not found / not active: %w", subscriptionId, err)
	}
	var existingBinding SubscriptionKeyBinding
	result := tx.Where("user_subscription_id = ?", subscriptionId).Limit(1).Find(&existingBinding)
	if result.Error != nil {
		return nil, "", result.Error
	}
	if result.RowsAffected > 0 {
		var existingToken Token
		if err := tx.Where("id = ? AND user_id = ?", existingBinding.TokenId, userId).First(&existingToken).Error; err != nil {
			return nil, "", fmt.Errorf("subscription #%d has an invalid key binding: %w", subscriptionId, err)
		}
		return &existingToken, existingToken.Key, nil
	}
	key, err := common.GenerateKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate key: %w", err)
	}
	now := common.GetTimestamp()
	productType := strings.TrimSpace(sub.ProductType)
	if productType == "" {
		productType = ProductTypeLegacySubscription
	}
	namePrefix := "权益"
	if productType == ProductTypeMonthlySubscription {
		namePrefix = "月套餐"
	} else if strings.HasPrefix(productType, "pack_") {
		namePrefix = "流量包"
	}
	token := &Token{
		UserId:             userId,
		Key:                key,
		Status:             1,
		Name:               fmt.Sprintf("%s #%d 专属 Key", namePrefix, subscriptionId),
		CreatedTime:        now,
		AccessedTime:       now,
		ExpiredTime:        sub.EndTime,
		RemainQuota:        0,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: sub.ModelScope != "",
		ModelLimits:        sub.ModelScope,
		AllowIps:           func() *string { s := ""; return &s }(),
		UsedQuota:          0,
		Group:              "",
		CrossGroupRetry:    false,
		AutoGroups:         "",
		UserSubscriptionId: subscriptionId,
		EntitlementType:    productType,
		ProductType:        productType,
		BillingMultiplier: func() float64 {
			if sub.BillingMultiplier > 0 {
				return sub.BillingMultiplier
			}
			return 1
		}(),
		PriceVersion:    sub.PriceVersion,
		PriceBook:       sub.PriceBook,
		AllowedChannels: sub.AllowedChannels,
		MaxUpstreamCost: sub.MaxUpstreamCost,
		FallbackAllowed: sub.FallbackAllowed,
	}
	if err := tx.Create(token).Error; err != nil {
		return nil, "", err
	}
	binding := &SubscriptionKeyBinding{
		UserId:             userId,
		UserSubscriptionId: subscriptionId,
		TokenId:            token.Id,
	}
	if err := tx.Create(binding).Error; err != nil {
		return nil, "", err
	}
	return token, key, nil
}

func CreateBoundTokenForSubscription(userId int, subscriptionId int) (*Token, string, error) {
	var token *Token
	var key string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		token, key, err = createBoundTokenForSubscriptionTx(tx, userId, subscriptionId)
		return err
	})
	return token, key, err
}

func createProvisionedSubscriptionFromPlanTx(tx *gorm.DB, userId int, plan *SubscriptionPlan, source string) (*UserSubscription, *Token, error) {
	sub, err := CreateUserSubscriptionFromPlanTx(tx, userId, plan, source)
	if err != nil {
		return nil, nil, err
	}
	token, _, err := createBoundTokenForSubscriptionTx(tx, userId, sub.Id)
	if err != nil {
		return nil, nil, err
	}
	return sub, token, nil
}

func refreshSubscriptionUserGroupCache(userId int, operation string) {
	if err := RefreshUserGroupCache(userId); err != nil {
		common.SysError(common.LogText("failed to refresh user group cache after %s for user %d: %v", operation, userId, err))
	}
}

func provisionPlanFromOrder(tx *gorm.DB, order *SubscriptionOrder, livePlan *SubscriptionPlan) (*SubscriptionPlan, error) {
	if order == nil || livePlan == nil {
		return nil, errors.New("invalid order plan snapshot")
	}
	plan := *livePlan
	if order.ProductPlanSnapshot != "" {
		var frozen SubscriptionPlan
		if err := common.UnmarshalJsonStr(order.ProductPlanSnapshot, &frozen); err != nil {
			return nil, fmt.Errorf("invalid product plan snapshot: %w", err)
		}
		plan = frozen
		if plan.Id != order.PlanId || plan.PriceBook == "" || plan.PriceBook != order.PriceBook || plan.PriceVersion != order.PriceVersion {
			return nil, errors.New("product plan snapshot identity mismatch")
		}
		if err := plan.ValidateProductPlan(); err != nil {
			return nil, err
		}
		return &plan, nil
	}
	if order.PriceBook != "" && order.ProductType == "" {
		return nil, errors.New("product order is missing its immutable plan snapshot")
	}
	if order.ProductType == "" {
		return &plan, nil
	}
	plan.Title = order.PlanTitleSnapshot
	plan.PriceAmount = order.TargetPriceAmount
	plan.Currency = order.CurrencySnapshot
	plan.DurationUnit = order.DurationUnitSnapshot
	plan.DurationValue = order.DurationValueSnapshot
	plan.CustomSeconds = order.CustomSecondsSnapshot
	plan.TotalAmount = order.TotalAmountSnapshot
	plan.QuotaResetPeriod = order.QuotaResetPeriodSnapshot
	plan.QuotaResetCustomSeconds = order.QuotaResetCustomSnapshot
	plan.Category = order.CategorySnapshot
	plan.ProductType = order.ProductType
	plan.ProductCode = order.ProductCode
	plan.ProductFamily = order.ProductFamily
	plan.ProductTier = order.TargetTier
	plan.BillingMultiplier = order.BillingMultiplier
	plan.PriceVersion = order.PriceVersion
	plan.ModelScope = order.ModelScope
	plan.AllowedChannels = order.AllowedChannels
	plan.MaxUpstreamCost = order.MaxUpstreamCost
	plan.FallbackAllowed = order.FallbackAllowed
	plan.CourseEligible = order.CourseEligible
	allowWalletOverflow := false
	plan.AllowWalletOverflow = &allowWalletOverflow
	if err := plan.validateV1ContractTx(tx); err != nil {
		return nil, fmt.Errorf("invalid immutable order snapshot: %w", err)
	}
	return &plan, nil
}

type monthlyUpgradeHistoryItem struct {
	TradeNo     string  `json:"trade_no"`
	SourceTier  int     `json:"source_tier"`
	TargetTier  int     `json:"target_tier"`
	Amount      float64 `json:"amount"`
	CompletedAt int64   `json:"completed_at"`
}

func upgradeMonthlySubscriptionTx(tx *gorm.DB, order *SubscriptionOrder, targetPlan *SubscriptionPlan) (*UserSubscription, error) {
	if tx == nil || order == nil || targetPlan == nil || order.UserSubscriptionId == nil || *order.UserSubscriptionId <= 0 {
		return nil, errors.New("invalid monthly upgrade args")
	}
	if order.OrderType != SubscriptionOrderTypeUpgrade || targetPlan.ProductType != ProductTypeMonthlySubscription {
		return nil, errors.New("order is not a monthly upgrade")
	}
	priceBook, err := ProductPriceBook(targetPlan.Id)
	if err != nil {
		return nil, err
	}
	now := getDBTimestampTx(tx)
	var sub UserSubscription
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", *order.UserSubscriptionId, order.UserId).First(&sub).Error; err != nil {
		return nil, err
	}
	if sub.Status != "active" || sub.EndTime <= now {
		return nil, errors.New("月套餐已失效，不能补差升级")
	}
	if sub.ProductType != ProductTypeMonthlySubscription || sub.ProductFamily != order.ProductFamily || sub.PeriodId == "" {
		return nil, errors.New("升级订单与月套餐权益不匹配")
	}
	if sub.ProductTier != order.SourceTier || targetPlan.ProductTier != order.TargetTier || targetPlan.ProductTier <= sub.ProductTier {
		return nil, errors.New("月套餐档位已变化，升级订单已失效")
	}
	quotaDelta := targetPlan.TotalAmount - sub.AmountTotal
	if quotaDelta <= 0 {
		return nil, errors.New("月套餐升级额度差必须大于零")
	}

	var cycle MonthlySubscriptionCycle
	if err := lockForUpdate(tx).Where("user_id = ? AND product_family = ?", order.UserId, order.ProductFamily).First(&cycle).Error; err != nil {
		return nil, err
	}
	if cycle.SubscriptionId != sub.Id || cycle.PeriodId != sub.PeriodId || cycle.ExpiresAt != sub.EndTime || cycle.CurrentTier != sub.ProductTier {
		return nil, errors.New("月套餐周期状态已变化，升级订单已失效")
	}

	// Only the face-value difference is appended. AmountUsed, StartTime,
	// EndTime, PeriodId and the bound key's identity remain untouched.
	sub.AmountTotal += quotaDelta
	sub.PlanId = targetPlan.Id
	sub.ModelScope = strings.TrimSpace(targetPlan.ModelScope)
	sub.ProductCode = strings.TrimSpace(targetPlan.ProductCode)
	sub.ProductTier = targetPlan.ProductTier
	sub.BillingMultiplier = targetPlan.BillingMultiplier
	sub.PriceVersion = strings.TrimSpace(targetPlan.PriceVersion)
	sub.PriceBook = priceBook
	sub.AllowedChannels = strings.TrimSpace(targetPlan.AllowedChannels)
	sub.MaxUpstreamCost = strings.TrimSpace(targetPlan.MaxUpstreamCost)
	sub.FallbackAllowed = targetPlan.FallbackAllowed
	sub.CourseEligible = targetPlan.CourseEligible
	sub.PriceAmountSnapshot = targetPlan.PriceAmount
	sub.CurrencySnapshot = strings.TrimSpace(targetPlan.Currency)
	if err := tx.Save(&sub).Error; err != nil {
		return nil, err
	}

	var binding SubscriptionKeyBinding
	if err := lockForUpdate(tx).Where("user_subscription_id = ?", sub.Id).First(&binding).Error; err != nil {
		return nil, fmt.Errorf("monthly subscription key binding missing: %w", err)
	}
	var token Token
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ? AND user_subscription_id = ?", binding.TokenId, sub.UserId, sub.Id).First(&token).Error; err != nil {
		return nil, fmt.Errorf("monthly subscription key missing: %w", err)
	}
	keyBefore := token.Key
	expiresBefore := token.ExpiredTime
	token.Name = fmt.Sprintf("%s 专属 Key", targetPlan.Title)
	token.ModelLimits = strings.TrimSpace(targetPlan.ModelScope)
	token.ModelLimitsEnabled = token.ModelLimits != ""
	token.EntitlementType = ProductTypeMonthlySubscription
	token.ProductType = ProductTypeMonthlySubscription
	token.BillingMultiplier = targetPlan.BillingMultiplier
	token.PriceVersion = strings.TrimSpace(targetPlan.PriceVersion)
	token.PriceBook = sub.PriceBook
	token.AllowedChannels = strings.TrimSpace(targetPlan.AllowedChannels)
	token.MaxUpstreamCost = strings.TrimSpace(targetPlan.MaxUpstreamCost)
	token.FallbackAllowed = targetPlan.FallbackAllowed
	if err := tx.Save(&token).Error; err != nil {
		return nil, err
	}
	if token.Key != keyBefore || token.ExpiredTime != expiresBefore || token.UserSubscriptionId != sub.Id {
		return nil, errors.New("monthly upgrade attempted to change key identity or expiry")
	}

	var history []monthlyUpgradeHistoryItem
	if strings.TrimSpace(cycle.UpgradeHistory) != "" {
		if err := common.UnmarshalJsonStr(cycle.UpgradeHistory, &history); err != nil {
			return nil, fmt.Errorf("invalid monthly upgrade history: %w", err)
		}
	}
	history = append(history, monthlyUpgradeHistoryItem{
		TradeNo: order.TradeNo, SourceTier: order.SourceTier, TargetTier: order.TargetTier,
		Amount: order.Money, CompletedAt: now,
	})
	cycle.CurrentTier = targetPlan.ProductTier
	cycle.UpgradeHistory = common.GetJsonString(history)
	if err := tx.Save(&cycle).Error; err != nil {
		return nil, err
	}
	if sub.CourseEligible {
		if err := grantV1CourseEntitlementTx(tx, &sub, "upgrade"); err != nil {
			return nil, err
		}
	}
	return &sub, nil
}

// Complete a subscription order (idempotent). Creates a UserSubscription snapshot from the plan.
// expectedPaymentProvider guards against cross-gateway callback attacks (empty skips the check).
// actualPaymentMethod updates the order's PaymentMethod to reflect the real payment type used (empty skips update).
func CompleteSubscriptionOrder(tradeNo string, providerPayload string, expectedPaymentProvider string, actualPaymentMethod string) error {
	return completeSubscriptionOrder(tradeNo, providerPayload, expectedPaymentProvider, actualPaymentMethod, nil)
}

func CompleteSubscriptionOrderWithReceipt(tradeNo string, expectedPaymentProvider string, actualPaymentMethod string, receipt PaymentReceipt) error {
	return completeSubscriptionOrder(tradeNo, receipt.ProviderPayload, expectedPaymentProvider, actualPaymentMethod, &receipt)
}

func completeSubscriptionOrder(tradeNo string, providerPayload string, expectedPaymentProvider string, actualPaymentMethod string, receipt *PaymentReceipt) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}
	var logUserId int
	var logPlanTitle string
	var logMoney float64
	var logPaymentMethod string
	var upgradeGroup string
	var compensationReason string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if receipt != nil {
			if err := validatePaymentReceipt(order.Money, order.CurrencySnapshot, receipt); err != nil {
				return err
			}
		}
		if order.Status == SubscriptionOrderStatusPaidReview {
			// The verified receipt is durable; repeated callbacks must neither
			// issue an entitlement nor erase the compensation evidence.
			return nil
		}
		if order.Status == common.TopUpStatusSuccess {
			// Idempotent recovery is safe because new orders persist the exact
			// order -> entitlement link before becoming successful.
			if order.UserSubscriptionId == nil || *order.UserSubscriptionId <= 0 {
				if order.LegacyCompatibilityState != LegacyOrderStateNone {
					// A historical success without a deterministic entitlement link
					// is explicitly parked for manual review. Replayed notifies are
					// acknowledged without issuing a second entitlement.
					return nil
				}
				return errors.New("successful subscription order is missing entitlement linkage")
			}
			if _, _, err := createBoundTokenForSubscriptionTx(tx, order.UserId, *order.UserSubscriptionId); err != nil {
				return err
			}
			if order.ProvisioningStatus != ProvisioningStatusProvisioned {
				order.ProvisioningStatus = ProvisioningStatusProvisioned
				order.ProvisioningError = ""
				return tx.Save(&order).Error
			}
			return nil
		}
		failedProduct := order.ProductPlanSnapshot != "" && order.Status == common.TopUpStatusFailed
		if order.Status != common.TopUpStatusPending && order.Status != common.TopUpStatusExpired && !failedProduct {
			return ErrSubscriptionOrderStatusInvalid
		}
		if failedProduct {
			// A checkout request can time out after the provider created its
			// session. A later verified payment is real even if creation failed
			// locally; retain it without taking an already released slot.
			if receipt == nil {
				return ErrSubscriptionOrderStatusInvalid
			}
			if err := parkPaidProductOrderTx(tx, &order, actualPaymentMethod, *receipt, "reservation_released"); err != nil {
				return err
			}
			logUserId, compensationReason = order.UserId, order.ProvisioningError
			return nil
		}
		// Lock before the first consistent table read, as checkout does. This
		// keeps the issuance limit check fresh on MySQL REPEATABLE READ too.
		var userRow User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", order.UserId).First(&userRow).Error; err != nil {
			if order.ProductPlanSnapshot != "" && receipt != nil && errors.Is(err, gorm.ErrRecordNotFound) {
				// Deleted customers cannot receive keys, but their verified cash
				// receipt must remain visible to finance for resolution.
				if err := parkPaidProductOrderTx(tx, &order, actualPaymentMethod, *receipt, "provisioning_failed"); err != nil {
					return err
				}
				logUserId, compensationReason = order.UserId, order.ProvisioningError
				return nil
			}
			return err
		}
		var reservationReleased bool
		if order.ProductPlanSnapshot != "" {
			now, err := productReservationTimeTx(tx)
			if err != nil {
				return err
			}
			reservationReleased = order.Status == common.TopUpStatusExpired ||
				(order.ReservationExpiresAt > 0 && order.ReservationExpiresAt <= now)
		}
		if reservationReleased {
			if receipt == nil {
				return ErrSubscriptionOrderStatusInvalid
			}
			if err := parkPaidProductOrderTx(tx, &order, actualPaymentMethod, *receipt, "reservation_released"); err != nil {
				return err
			}
			logUserId, compensationReason = order.UserId, order.ProvisioningError
			return nil
		}
		if order.Status != common.TopUpStatusPending {
			return ErrSubscriptionOrderStatusInvalid
		}
		durableReceipt := order.ProductPlanSnapshot != "" && receipt != nil
		if durableReceipt {
			if err := tx.SavePoint("product_fulfillment").Error; err != nil {
				return err
			}
		}
		beforeIssuance := order
		provisionPlan, subscription, err := fulfillSubscriptionOrderTx(tx, &order, providerPayload, actualPaymentMethod, receipt)
		if err != nil && durableReceipt {
			// Undo every partial entitlement/key/top-up write before retaining
			// the verified receipt. Never acknowledge if rollback or parking fails.
			if rollbackErr := tx.RollbackTo("product_fulfillment").Error; rollbackErr != nil {
				return rollbackErr
			}
			order = beforeIssuance
			if err := parkPaidProductOrderTx(tx, &order, actualPaymentMethod, *receipt, "provisioning_failed"); err != nil {
				return err
			}
			logUserId, compensationReason = order.UserId, order.ProvisioningError
			return nil
		}
		if err != nil {
			return err
		}
		if subscription.PrevUserGroup != "" {
			upgradeGroup = strings.TrimSpace(subscription.UpgradeGroup)
		}
		logUserId = order.UserId
		logPlanTitle = provisionPlan.Title
		logMoney = order.Money
		logPaymentMethod = order.PaymentMethod
		return nil
	})
	if err != nil {
		return err
	}
	if compensationReason != "" {
		message := "Product payment received after reservation release; compensation required"
		if compensationReason == "provisioning_failed" {
			message = "Product payment received but provisioning failed; compensation required"
		}
		RecordLog(logUserId, LogTypeSystem, common.NewMessage(message))
		return nil
	}
	if upgradeGroup != "" && logUserId > 0 {
		refreshSubscriptionUserGroupCache(logUserId, common.LogText("subscription payment completion"))
	}
	if logUserId > 0 {
		RecordLog(logUserId, LogTypeTopup, common.NewMessage("Subscription purchased, plan: {{plan}}, payment amount: {{amount}}, payment method: {{method}}", map[string]any{
			"plan":   logPlanTitle,
			"amount": fmt.Sprintf("%.2f", logMoney),
			"method": logPaymentMethod,
		}))
	}
	return nil
}

// fulfillSubscriptionOrderTx atomically links the entitlement, its dedicated
// key, the top-up ledger and the successful order. The caller holds order and
// user locks; both payment callbacks and explicit compensation retry use it.
func fulfillSubscriptionOrderTx(tx *gorm.DB, order *SubscriptionOrder, providerPayload string, method string, receipt *PaymentReceipt) (*SubscriptionPlan, *UserSubscription, error) {
	plan, err := getSubscriptionPlanByIdTx(tx, order.PlanId)
	if err != nil {
		return nil, nil, err
	}
	provisionPlan, err := provisionPlanFromOrder(tx, order, plan)
	if err != nil {
		return nil, nil, err
	}
	var subscription *UserSubscription
	if order.OrderType == SubscriptionOrderTypeUpgrade {
		subscription, err = upgradeMonthlySubscriptionTx(tx, order, provisionPlan)
	} else {
		subscription, _, err = createProvisionedSubscriptionFromPlanTx(tx, order.UserId, provisionPlan, "order")
	}
	if err != nil {
		return nil, nil, err
	}
	if method != "" {
		order.PaymentMethod = method
	}
	if err := upsertSubscriptionTopUpTx(tx, order); err != nil {
		return nil, nil, err
	}
	order.Status = common.TopUpStatusSuccess
	order.UserSubscriptionId = &subscription.Id
	order.ProvisioningStatus = ProvisioningStatusProvisioned
	order.ProvisioningError = ""
	order.CompleteTime = common.GetTimestamp()
	if providerPayload != "" {
		order.ProviderPayload = providerPayload
	}
	if receipt != nil {
		order.PaidMoney, _ = receipt.Amount.Float64()
		order.PaidCurrency = strings.ToUpper(strings.TrimSpace(receipt.Currency))
		order.ProviderTradeNo = strings.TrimSpace(receipt.ProviderTradeNo)
	}
	if err := tx.Save(order).Error; err != nil {
		return nil, nil, err
	}
	return provisionPlan, subscription, nil
}

func upsertSubscriptionTopUpTx(tx *gorm.DB, order *SubscriptionOrder) error {
	if tx == nil || order == nil {
		return errors.New("invalid subscription order")
	}
	now := common.GetTimestamp()
	var topup TopUp
	if err := tx.Where("trade_no = ?", order.TradeNo).First(&topup).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			topup = TopUp{
				UserId:        order.UserId,
				Amount:        0,
				Money:         order.Money,
				TradeNo:       order.TradeNo,
				PaymentMethod: order.PaymentMethod,
				CreateTime:    order.CreateTime,
				CompleteTime:  now,
				Status:        common.TopUpStatusSuccess,
			}
			return tx.Create(&topup).Error
		}
		return err
	}
	topup.Money = order.Money
	if topup.PaymentMethod == "" {
		topup.PaymentMethod = order.PaymentMethod
	} else if topup.PaymentMethod != order.PaymentMethod {
		return ErrPaymentMethodMismatch
	}
	if topup.CreateTime == 0 {
		topup.CreateTime = order.CreateTime
	}
	topup.CompleteTime = now
	topup.Status = common.TopUpStatusSuccess
	return tx.Save(&topup).Error
}

func ExpireSubscriptionOrder(tradeNo string, expectedPaymentProvider string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := lockForUpdate(tx).Where(refCol+" = ?", tradeNo).First(&order).Error; err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status != common.TopUpStatusPending {
			return nil
		}
		order.Status = common.TopUpStatusExpired
		order.CompleteTime = common.GetTimestamp()
		return tx.Save(&order).Error
	})
}

// Admin bind (no payment). Creates a UserSubscription from a plan.
func AdminBindSubscription(userId int, planId int, sourceNote string) (*common.Message, error) {
	if userId <= 0 || planId <= 0 {
		return nil, errors.New("invalid userId or planId")
	}
	plan, err := GetSubscriptionPlanById(planId)
	if err != nil {
		return nil, err
	}
	groupChanged := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		// 与 CompleteSubscriptionOrder 一致：先锁用户行，再做购买次数检查。
		var userRow User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&userRow).Error; err != nil {
			return err
		}
		subscription, _, err := createProvisionedSubscriptionFromPlanTx(tx, userId, plan, "admin")
		if err == nil {
			groupChanged = subscription.PrevUserGroup != ""
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if groupChanged {
		refreshSubscriptionUserGroupCache(userId, common.LogText("admin subscription creation"))
		return common.NewMessage("User group will be upgraded to {{group}}", map[string]any{"group": plan.UpgradeGroup}), nil
	}
	return nil, nil
}

func calcSubscriptionBalanceQuota(priceAmount float64) (int, error) {
	if priceAmount <= 0 {
		return 0, nil
	}
	if common.QuotaPerUnit <= 0 {
		return 0, common.NewMessage("Quota unit is misconfigured")
	}
	quota := decimal.NewFromFloat(priceAmount).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Ceil()
	return common.WalletQuotaFromDecimalStrict(quota)
}

// PurchaseSubscriptionWithBalance creates a subscription by deducting the user's wallet quota.
func PurchaseSubscriptionWithBalance(userId int, planId int) error {
	if userId <= 0 || planId <= 0 {
		return errors.New("invalid userId or planId")
	}

	var logPlanTitle string
	var logMoney float64
	var chargedQuota int
	var upgradeGroup string
	err := DB.Transaction(func(tx *gorm.DB) error {
		now := common.GetTimestamp()
		tradeNo := fmt.Sprintf("SUBBALUSR%dNO%s%d", userId, common.GetRandomString(6), time.Now().UnixNano())
		order, plan, err := createSubscriptionOrderTx(tx, userId, planId, tradeNo, PaymentMethodBalance, PaymentProviderBalance, true)
		if err != nil {
			return err
		}
		if !plan.Enabled {
			return common.NewMessage("Subscription plan is not enabled")
		}
		if plan.PriceAmount < 0 {
			return common.NewMessage("Price cannot be negative")
		}
		if plan.AllowBalancePay != nil && !*plan.AllowBalancePay {
			return common.NewMessage("This plan does not allow balance redemption")
		}

		var requiredQuota int
		var balancePayload string
		if plan.PriceBook != "" {
			// Wallet quota is USD-denominated. Product cash price keeps its own
			// currency; never debit a CNY amount as though it were USD. Persist
			// the exact rate and debit with the successful order for accounting.
			if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
				return common.NewMessage("Quota unit is misconfigured")
			}
			quotaAmount := decimal.NewFromFloat(order.Money).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
			rate := 1.0
			if plan.Currency == "CNY" {
				rate = operation_setting.USDExchangeRate
				if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
					return errors.New("product balance payment requires a valid CNY per USD exchange rate")
				}
				whole, remainder := quotaAmount.QuoRem(decimal.NewFromFloat(rate), 0)
				if remainder.IsPositive() {
					whole = whole.Add(decimal.NewFromInt(1))
				}
				quotaAmount = whole
			}
			requiredQuota, err = common.WalletQuotaFromDecimalStrict(quotaAmount.Ceil())
			if err != nil {
				return err
			}
			payload, err := common.Marshal(map[string]any{
				"charged_quota": requiredQuota, "cash_amount": order.Money,
				"cash_currency": plan.Currency, "cash_per_usd": rate,
			})
			if err != nil {
				return err
			}
			balancePayload = string(payload)
		} else {
			requiredQuota, err = calcSubscriptionBalanceQuota(order.Money)
			if err != nil {
				return err
			}
			balancePayload = fmt.Sprintf("charged_quota=%d", requiredQuota)
		}

		var user User
		if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		if requiredQuota > 0 && user.Quota < requiredQuota {
			return common.NewMessage("Insufficient balance")
		}
		if requiredQuota > 0 {
			if err := tx.Model(&User{}).Where("id = ?", userId).
				Update("quota", gorm.Expr("quota - ?", requiredQuota)).Error; err != nil {
				return err
			}
		}

		order.ProviderPayload = balancePayload
		var subscription *UserSubscription
		if order.OrderType == SubscriptionOrderTypeUpgrade {
			subscription, err = upgradeMonthlySubscriptionTx(tx, order, plan)
		} else {
			subscription, _, err = createProvisionedSubscriptionFromPlanTx(tx, userId, plan, PaymentMethodBalance)
		}
		if err != nil {
			return err
		}
		order.UserSubscriptionId = &subscription.Id
		order.Status = common.TopUpStatusSuccess
		order.CompleteTime = now
		order.ProvisioningStatus = ProvisioningStatusProvisioned
		if err := tx.Save(order).Error; err != nil {
			return err
		}

		logPlanTitle = plan.Title
		logMoney = order.Money
		chargedQuota = requiredQuota
		if subscription.PrevUserGroup != "" {
			upgradeGroup = strings.TrimSpace(subscription.UpgradeGroup)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if chargedQuota > 0 {
		if err := cacheDecrUserQuota(userId, int64(chargedQuota)); err != nil {
			common.SysLog(common.LogText("failed to decrease user quota cache after subscription balance purchase: %s", err.Error()))
		}
	}
	if upgradeGroup != "" {
		refreshSubscriptionUserGroupCache(userId, common.LogText("subscription balance purchase"))
	}
	RecordLog(userId, LogTypeTopup, common.NewMessage("Subscription purchased with balance, plan: {{plan}}, payment amount: {{amount}}, quota deducted: {{quota}}", map[string]any{
		"plan":   logPlanTitle,
		"amount": fmt.Sprintf("%.2f", logMoney),
		"quota":  chargedQuota,
	}))
	return nil
}

// GetAllActiveUserSubscriptions returns all active subscriptions for a user.
func GetAllActiveUserSubscriptions(userId int) ([]SubscriptionSummary, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var subs []UserSubscription
	err := DB.Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Order("end_time desc, id desc").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return buildSubscriptionSummaries(subs), nil
}

// HasActiveUserSubscription returns whether the user has any active subscription.
// This is a lightweight existence check to avoid heavy pre-consume transactions.
// modelScopeMatches reports whether a V1 modelName is covered by a
// comma-separated model scope. Entries match exact model IDs only. Legacy
// entitlements use modelScopeMatchesForProduct and retain their historical
// prefix behavior.
func modelScopeMatches(scope string, modelName string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return true
	}
	for _, entry := range strings.Split(scope, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if modelName == entry {
			return true
		}
	}
	return false
}

func modelScopeMatchesForProduct(scope string, modelName string, productType string) bool {
	productType = strings.TrimSpace(productType)
	if productType == "" || productType == ProductTypeLegacySubscription {
		return legacyModelScopeMatches(scope, modelName)
	}
	return modelScopeMatches(scope, modelName)
}

func legacyModelScopeMatches(scope string, modelName string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return true
	}
	for _, entry := range strings.Split(scope, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// Some legacy admin rows used a trailing glob marker while the
		// historical matcher treated every scope entry as a prefix. Normalize
		// the marker so both encodings retain their old reach.
		entry = strings.TrimSuffix(entry, "*")
		if modelName == entry || strings.HasPrefix(modelName, entry) {
			return true
		}
	}
	return false
}

// HasActiveUserSubscription reports whether the user holds any active
// subscription regardless of model scope.
func HasActiveUserSubscription(userId int) (bool, error) {
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var count int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// HasActiveUserSubscriptionForModel reports whether the user holds an active
// subscription whose model scope covers modelName. Subscriptions with an
// empty scope cover every model.
func HasActiveUserSubscriptionForModel(userId int, modelName string) (bool, error) {
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var subs []UserSubscription
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Find(&subs).Error; err != nil {
		return false, err
	}
	for _, sub := range subs {
		if modelScopeMatchesForProduct(sub.ModelScope, modelName, sub.ProductType) {
			return true, nil
		}
	}
	return false, nil
}

// UserActiveSubscriptionsAllowWalletOverflow returns whether wallet balance may be used
// after the user's subscription quota is exhausted. A single active subscription that
// disallows wallet overflow (allow_wallet_overflow = false) blocks the fallback.
func UserActiveSubscriptionsAllowWalletOverflow(userId int) (bool, error) {
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var strictCount int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ? AND end_time > ? AND allow_wallet_overflow = ?",
			userId, "active", now, false).
		Count(&strictCount).Error; err != nil {
		return false, err
	}
	return strictCount == 0, nil
}

// GetAllUserSubscriptions returns all subscriptions (active and expired) for a user.
func GetAllUserSubscriptions(userId int) ([]SubscriptionSummary, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	var subs []UserSubscription
	err := DB.Where("user_id = ?", userId).
		Order("end_time desc, id desc").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return buildSubscriptionSummaries(subs), nil
}

func buildSubscriptionSummaries(subs []UserSubscription) []SubscriptionSummary {
	if len(subs) == 0 {
		return []SubscriptionSummary{}
	}
	result := make([]SubscriptionSummary, 0, len(subs))
	for _, sub := range subs {
		subCopy := sub
		result = append(result, SubscriptionSummary{
			Subscription: &subCopy,
		})
	}
	return result
}

// AdminInvalidateUserSubscription marks a user subscription as cancelled and ends it immediately.
func AdminInvalidateUserSubscription(userSubscriptionId int) (*common.Message, error) {
	if userSubscriptionId <= 0 {
		return nil, errors.New("invalid userSubscriptionId")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	downgradeGroup := ""
	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
			return err
		}
		userId = sub.UserId
		if err := tx.Model(&sub).Updates(map[string]any{
			"status":     "cancelled",
			"end_time":   now,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		if target != "" {
			cacheGroup = target
			downgradeGroup = target
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if cacheGroup != "" && userId > 0 {
		refreshSubscriptionUserGroupCache(userId, common.LogText("admin subscription update"))
	}
	if downgradeGroup != "" {
		return common.NewMessage("User group will be reverted to {{group}}", map[string]any{"group": downgradeGroup}), nil
	}
	return nil, nil
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
func AdminDeleteUserSubscription(userSubscriptionId int) (*common.Message, error) {
	if userSubscriptionId <= 0 {
		return nil, errors.New("invalid userSubscriptionId")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	downgradeGroup := ""
	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sub UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
			return err
		}
		userId = sub.UserId
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		if target != "" {
			cacheGroup = target
			downgradeGroup = target
		}
		if err := tx.Where("id = ?", userSubscriptionId).Delete(&UserSubscription{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if cacheGroup != "" && userId > 0 {
		refreshSubscriptionUserGroupCache(userId, common.LogText("admin subscription deletion"))
	}
	if downgradeGroup != "" {
		return common.NewMessage("User group will be reverted to {{group}}", map[string]any{"group": downgradeGroup}), nil
	}
	return nil, nil
}

func resetUserSubscriptionTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan, now int64, advanceResetTime bool) error {
	if tx == nil || sub == nil || plan == nil {
		return errors.New("invalid reset args")
	}
	sub.AmountUsed = 0
	if advanceResetTime {
		nextReset := calcNextResetTime(time.Unix(now, 0), plan, sub.EndTime)
		sub.NextResetTime = nextReset
		if nextReset > 0 {
			sub.LastResetTime = now
		} else {
			sub.LastResetTime = 0
		}
	}
	return tx.Save(sub).Error
}

func buildSubscriptionResetResult(plan *SubscriptionPlan, subs []UserSubscription, advanceResetTime bool) *SubscriptionResetResult {
	userIds := make([]int, 0, len(subs))
	seenUsers := make(map[int]struct{}, len(subs))
	for _, sub := range subs {
		if _, ok := seenUsers[sub.UserId]; ok {
			continue
		}
		seenUsers[sub.UserId] = struct{}{}
		userIds = append(userIds, sub.UserId)
	}
	return &SubscriptionResetResult{
		PlanId:           plan.Id,
		MatchedCount:     len(subs),
		ResetCount:       len(subs),
		UserCount:        len(userIds),
		AdvanceResetTime: advanceResetTime,
		PlanTitle:        plan.Title,
		AffectedUserIds:  userIds,
	}
}

func adminResetUserSubscriptionsByPlanTx(tx *gorm.DB, userId int, plan *SubscriptionPlan, now int64, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if tx == nil || plan == nil {
		return nil, errors.New("invalid reset args")
	}
	var subs []UserSubscription
	if err := lockForUpdate(tx).
		Where("user_id = ? AND plan_id = ? AND status = ? AND end_time > ?", userId, plan.Id, "active", now).
		Order("end_time asc, id asc").
		Find(&subs).Error; err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, common.NewMessage("This user has no active subscription to this plan")
	}
	for i := range subs {
		if err := resetUserSubscriptionTx(tx, &subs[i], plan, now, advanceResetTime); err != nil {
			return nil, err
		}
	}
	return buildSubscriptionResetResult(plan, subs, advanceResetTime), nil
}

func adminResetPlanSubscriptionsTx(tx *gorm.DB, plan *SubscriptionPlan, now int64, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if tx == nil || plan == nil {
		return nil, errors.New("invalid reset args")
	}
	var subs []UserSubscription
	if err := lockForUpdate(tx).
		Where("plan_id = ? AND status = ? AND end_time > ?", plan.Id, "active", now).
		Order("user_id asc, end_time asc, id asc").
		Find(&subs).Error; err != nil {
		return nil, err
	}
	for i := range subs {
		if err := resetUserSubscriptionTx(tx, &subs[i], plan, now, advanceResetTime); err != nil {
			return nil, err
		}
	}
	return buildSubscriptionResetResult(plan, subs, advanceResetTime), nil
}

func AdminResetUserSubscriptionsByPlan(userId int, planId int, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if userId <= 0 || planId <= 0 {
		return nil, errors.New("invalid userId or planId")
	}
	var result *SubscriptionResetResult
	now := GetDBTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := getSubscriptionPlanByIdTx(tx, planId)
		if err != nil {
			return err
		}
		result, err = adminResetUserSubscriptionsByPlanTx(tx, userId, plan, now, advanceResetTime)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func AdminResetPlanSubscriptions(planId int, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if planId <= 0 {
		return nil, errors.New("invalid planId")
	}
	var result *SubscriptionResetResult
	now := GetDBTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := getSubscriptionPlanByIdTx(tx, planId)
		if err != nil {
			return err
		}
		result, err = adminResetPlanSubscriptionsTx(tx, plan, now, advanceResetTime)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type SubscriptionPreConsumeResult struct {
	UserSubscriptionId int
	PreConsumed        int64
	AmountTotal        int64
	AmountUsedBefore   int64
	AmountUsedAfter    int64
}

// ExpireDueSubscriptions marks expired subscriptions and handles group downgrade.
func ExpireDueSubscriptions(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	if err := DB.Where("status = ? AND end_time > 0 AND end_time <= ?", "active", now).
		Order("end_time asc, id asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	expiredCount := 0
	userIds := make(map[int]struct{}, len(subs))
	for _, sub := range subs {
		if sub.UserId > 0 {
			userIds[sub.UserId] = struct{}{}
		}
	}
	for userId := range userIds {
		cacheGroup := ""
		err := DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&UserSubscription{}).
				Where("user_id = ? AND status = ? AND end_time > 0 AND end_time <= ?", userId, "active", now).
				Updates(map[string]any{
					"status":     "expired",
					"updated_at": common.GetTimestamp(),
				})
			if res.Error != nil {
				return res.Error
			}
			expiredCount += int(res.RowsAffected)

			// If there's an active upgraded subscription, keep current group.
			var activeSub UserSubscription
			activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND upgrade_group <> ''",
				userId, "active", now).
				Order("end_time desc, id desc").
				Limit(1).
				Find(&activeSub)
			if activeQuery.Error == nil && activeQuery.RowsAffected > 0 {
				return nil
			}

			// Find the most recently expired subscription that defines a group transition
			// (an explicit downgrade target or an upgrade snapshot to revert).
			var lastExpired UserSubscription
			expiredQuery := tx.Where("user_id = ? AND status = ? AND (downgrade_group <> '' OR upgrade_group <> '')",
				userId, "expired").
				Order("end_time desc, id desc").
				Limit(1).
				Find(&lastExpired)
			if expiredQuery.Error != nil || expiredQuery.RowsAffected == 0 {
				return nil
			}
			currentGroup, err := getUserGroupByIdTx(tx, userId)
			if err != nil {
				return err
			}
			// An explicit downgrade group takes precedence; otherwise revert to the
			// group held before purchase (legacy behavior, only when the subscription
			// actually elevated the user).
			target := strings.TrimSpace(lastExpired.DowngradeGroup)
			if target == "" {
				upgradeGroup := strings.TrimSpace(lastExpired.UpgradeGroup)
				prevGroup := strings.TrimSpace(lastExpired.PrevUserGroup)
				if upgradeGroup == "" || prevGroup == "" {
					return nil
				}
				if currentGroup != upgradeGroup {
					return nil
				}
				target = prevGroup
			}
			if target == "" || target == currentGroup {
				return nil
			}
			if err := tx.Model(&User{}).Where("id = ?", userId).
				Update("group", target).Error; err != nil {
				return err
			}
			cacheGroup = target
			return nil
		})
		if err != nil {
			return expiredCount, err
		}
		if cacheGroup != "" {
			refreshSubscriptionUserGroupCache(userId, common.LogText("subscription expiration"))
		}
	}
	return expiredCount, nil
}

// SubscriptionPreConsumeRecord stores idempotent pre-consume operations per request.
type SubscriptionPreConsumeRecord struct {
	Id                 int    `json:"id"`
	RequestId          string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	UserId             int    `json:"user_id" gorm:"index"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"index"`
	PreConsumed        int64  `json:"pre_consumed" gorm:"type:bigint;not null;default:0"`
	Status             string `json:"status" gorm:"type:varchar(32);index"` // consumed/refunded
	CreatedAt          int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint;index"`
}

func (r *SubscriptionPreConsumeRecord) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

func (r *SubscriptionPreConsumeRecord) BeforeUpdate(tx *gorm.DB) error {
	r.UpdatedAt = common.GetTimestamp()
	return nil
}

func maybeResetUserSubscriptionWithPlanTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan, now int64) error {
	if tx == nil || sub == nil || plan == nil {
		return errors.New("invalid reset args")
	}
	if sub.NextResetTime > 0 && sub.NextResetTime > now {
		return nil
	}
	if NormalizeResetPeriod(plan.QuotaResetPeriod) == SubscriptionResetNever {
		return nil
	}
	baseUnix := sub.LastResetTime
	if baseUnix <= 0 {
		baseUnix = sub.StartTime
	}
	base := time.Unix(baseUnix, 0)
	next := calcNextResetTime(base, plan, sub.EndTime)
	advanced := false
	for next > 0 && next <= now {
		advanced = true
		base = time.Unix(next, 0)
		next = calcNextResetTime(base, plan, sub.EndTime)
	}
	if !advanced {
		if sub.NextResetTime == 0 && next > 0 {
			sub.NextResetTime = next
			sub.LastResetTime = base.Unix()
			return tx.Save(sub).Error
		}
		return nil
	}
	sub.AmountUsed = 0
	sub.LastResetTime = base.Unix()
	sub.NextResetTime = next
	return tx.Save(sub).Error
}

// RefundSubscriptionPreConsume is idempotent and refunds pre-consumed subscription quota by requestId.
func RefundSubscriptionPreConsume(requestId string) error {
	if strings.TrimSpace(requestId) == "" {
		return errors.New("requestId is empty")
	}
	return DB.Transaction(func(tx *gorm.DB) error { return refundSubscriptionPreConsumeTx(tx, requestId) })
}

func refundSubscriptionPreConsumeTx(tx *gorm.DB, requestId string) error {
	var record SubscriptionPreConsumeRecord
	if err := tx.Where("request_id = ?", requestId).First(&record).Error; err != nil {
		return err
	}
	if record.Status == "refunded" {
		return nil
	}
	// Match reservation's pool -> record lock order. The first read only
	// locates the immutable pool ID; re-read status under the record lock.
	if record.PreConsumed > 0 {
		var sub UserSubscription
		if err := lockForUpdate(tx).Where("id = ?", record.UserSubscriptionId).First(&sub).Error; err != nil {
			return err
		}
	}
	if err := lockForUpdate(tx).
		Where("request_id = ?", requestId).First(&record).Error; err != nil {
		return err
	}
	if record.Status == "refunded" {
		return nil
	}
	if record.PreConsumed <= 0 {
		record.Status = "refunded"
		return tx.Save(&record).Error
	}
	if err := postConsumeUserSubscriptionDeltaTx(tx, record.UserSubscriptionId, -record.PreConsumed); err != nil {
		return err
	}
	record.Status = "refunded"
	return tx.Save(&record).Error
}

// ResetDueSubscriptions resets subscriptions whose next_reset_time has passed.
func ResetDueSubscriptions(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	if err := DB.Where("next_reset_time > 0 AND next_reset_time <= ? AND status = ?", now, "active").
		Order("next_reset_time asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	resetCount := 0
	for _, sub := range subs {
		subCopy := sub
		plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
		if err != nil || plan == nil {
			continue
		}
		err = DB.Transaction(func(tx *gorm.DB) error {
			var locked UserSubscription
			if err := lockForUpdate(tx).
				Where("id = ? AND next_reset_time > 0 AND next_reset_time <= ?", subCopy.Id, now).
				First(&locked).Error; err != nil {
				return nil
			}
			if err := maybeResetUserSubscriptionWithPlanTx(tx, &locked, plan, now); err != nil {
				return err
			}
			resetCount++
			return nil
		})
		if err != nil {
			return resetCount, err
		}
	}
	return resetCount, nil
}

// CleanupSubscriptionPreConsumeRecords removes old idempotency records to keep table small.
func CleanupSubscriptionPreConsumeRecords(olderThanSeconds int64) (int64, error) {
	if olderThanSeconds <= 0 {
		olderThanSeconds = 7 * 24 * 3600
	}
	cutoff := GetDBTimestamp() - olderThanSeconds
	// Legacy consumed rows have no durable settlement outcome and cannot be
	// safely discarded. Pending/review refunds retain their idempotency proof.
	settled := DB.Model(&BillingRefund{}).Select("request_id").Where("subscription_id > 0 AND status = ?", BillingRefundSettled)
	protected := DB.Model(&BillingRefund{}).Select("request_id").Where("subscription_id > 0 AND status NOT IN ?", []string{BillingRefundSettled, BillingRefundSucceeded})
	res := DB.Where("updated_at < ? AND (status = ? OR request_id IN (?)) AND request_id NOT IN (?)", cutoff, "refunded", settled, protected).Delete(&SubscriptionPreConsumeRecord{})
	return res.RowsAffected, res.Error
}

type SubscriptionPlanInfo struct {
	PlanId    int
	PlanTitle string
}

func GetSubscriptionPlanInfoByUserSubscriptionId(userSubscriptionId int) (*SubscriptionPlanInfo, error) {
	if userSubscriptionId <= 0 {
		return nil, errors.New("invalid userSubscriptionId")
	}
	cacheKey := fmt.Sprintf("sub:%d", userSubscriptionId)
	if cached, found, err := getSubscriptionPlanInfoCache().Get(cacheKey); err == nil && found {
		return &cached, nil
	}
	var sub UserSubscription
	if err := DB.Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
		return nil, err
	}
	plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
	if err != nil {
		return nil, err
	}
	info := &SubscriptionPlanInfo{
		PlanId:    sub.PlanId,
		PlanTitle: plan.Title,
	}
	_ = getSubscriptionPlanInfoCache().SetWithTTL(cacheKey, *info, subscriptionPlanInfoCacheTTL())
	return info, nil
}

// Update subscription used amount by delta (positive consume more, negative refund).
func PostConsumeUserSubscriptionDelta(userSubscriptionId int, delta int64) error {
	if userSubscriptionId <= 0 {
		return errors.New("invalid userSubscriptionId")
	}
	if delta == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return postConsumeUserSubscriptionDeltaTx(tx, userSubscriptionId, delta)
	})
}

func postConsumeUserSubscriptionDeltaTx(tx *gorm.DB, userSubscriptionId int, delta int64) error {
	if tx == nil {
		return errors.New("transaction is nil")
	}
	var sub UserSubscription
	if err := lockForUpdate(tx).
		Where("id = ?", userSubscriptionId).
		First(&sub).Error; err != nil {
		return err
	}
	newUsed := sub.AmountUsed + delta
	if newUsed < 0 {
		newUsed = 0
	}
	if sub.AmountTotal > 0 && newUsed > sub.AmountTotal {
		return fmt.Errorf("%w: subscription used exceeds total, used=%d total=%d", ErrSubscriptionQuotaInsufficient, newUsed, sub.AmountTotal)
	}
	sub.AmountUsed = newUsed
	return tx.Save(&sub).Error
}

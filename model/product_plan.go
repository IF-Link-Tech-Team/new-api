package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

var ErrProductPlanConflict = errors.New("product configuration changed; reload before saving")

// CreateProductPlan starts an explicit product-owned draft. Price publication
// requires its generated ID, so activation is always a later operation.
func CreateProductPlan(db *gorm.DB, plan *SubscriptionPlan) error {
	if plan == nil || plan.ProductType != "" || plan.TemplateId != 0 || plan.PriceBook != "" {
		return errors.New("new product must not specify a type, template or price book")
	}
	candidate := *plan
	candidate.Id, candidate.ConfigRevision = 0, 1
	candidate.Enabled, candidate.Listed, candidate.RedemptionEnabled = false, false, false
	if candidate.Currency == "" {
		candidate.Currency = "CNY"
	}
	if candidate.DurationUnit == "" {
		candidate.DurationUnit, candidate.DurationValue = SubscriptionDurationMonth, 1
	}
	if candidate.QuotaResetPeriod == "" {
		candidate.QuotaResetPeriod = SubscriptionResetNever
	}
	if candidate.AllowWalletOverflow == nil {
		candidate.AllowWalletOverflow = common.GetPointer(false)
	}
	candidate.Category = NormalizeSubscriptionCategory(candidate.Category)
	candidate.NormalizeDefaults()
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&candidate).Error; err != nil {
			return err
		}
		candidate.PriceBook, _ = ProductPriceBook(candidate.Id)
		candidate.Enabled, candidate.Listed, candidate.RedemptionEnabled = false, false, false
		if err := candidate.ValidateProductPlan(); err != nil {
			return err
		}
		return tx.Model(&candidate).Updates(map[string]any{
			"price_book": candidate.PriceBook, "enabled": false, "listed": false, "redemption_enabled": false,
		}).Error
	})
	if err == nil {
		*plan = candidate
		InvalidateSubscriptionPlanCache(candidate.Id)
	}
	return err
}

// UpdateProductPlan compares the revision under the same lock used to publish
// prices. It cannot silently convert an old plan or change paid entitlements.
func UpdateProductPlan(db *gorm.DB, plan *SubscriptionPlan, expectedRevision int64) error {
	if plan == nil {
		return errors.New("plan is nil")
	}
	candidate := *plan
	err := db.Transaction(func(tx *gorm.DB) error {
		var current SubscriptionPlan
		if err := lockForUpdate(tx).First(&current, candidate.Id).Error; err != nil {
			return err
		}
		if current.PriceBook == "" || candidate.PriceBook != current.PriceBook {
			return errors.New("product price book conversion requires an explicit migration")
		}
		if expectedRevision <= 0 || expectedRevision == math.MaxInt64 || expectedRevision != current.ConfigRevision {
			return ErrProductPlanConflict
		}
		candidate.CreatedAt = current.CreatedAt
		candidate.UpdatedAt = common.GetTimestamp()
		candidate.ConfigRevision = current.ConfigRevision + 1
		if candidate.AllowWalletOverflow == nil {
			candidate.AllowWalletOverflow = current.AllowWalletOverflow
		}
		if candidate.AllowBalancePay == nil {
			candidate.AllowBalancePay = current.AllowBalancePay
		}
		if err := candidate.ValidateProductPlan(); err != nil {
			return err
		}
		if candidate.Enabled || candidate.RedemptionEnabled {
			if err := validateProductPlanPrices(tx, &candidate, true); err != nil {
				return err
			}
		}
		// The predicate also protects SQLite, whose dialect skips FOR UPDATE.
		result := tx.Model(&SubscriptionPlan{}).Where("id = ? AND config_revision = ?", current.Id, expectedRevision).
			Select("*").Omit("id").Updates(&candidate)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrProductPlanConflict
		}
		return nil
	})
	if err == nil {
		*plan = candidate
		InvalidateSubscriptionPlanCache(candidate.Id)
	}
	return err
}

// ValidateProductPlan enforces storage and accounting boundaries, never a
// vendor, product family, fixed discount, face-value quota or template.
func (p *SubscriptionPlan) ValidateProductPlan() error {
	if p == nil {
		return errors.New("plan is nil")
	}
	book, err := ProductPriceBook(p.Id)
	if err != nil || p.PriceBook != book {
		return errors.New("price book must match the requested product")
	}
	if p.ProductType != "" || p.TemplateId != 0 {
		return errors.New("product-owned pricing does not use a product type or template")
	}
	if len(p.DisplayMetadata) > 16384 {
		return errors.New("product display metadata is too long")
	}
	if p.DisplayMetadata != "" {
		var metadata map[string]any
		if err := common.UnmarshalJsonStr(p.DisplayMetadata, &metadata); err != nil || metadata == nil {
			return errors.New("product display metadata must be a JSON object")
		}
	}
	if strings.TrimSpace(p.Title) == "" || len(p.Title) > 128 || len(p.ModelScope) > 255 || len(p.PriceVersion) > 64 {
		return errors.New("invalid product title, model scope or price version")
	}
	if p.PriceAmount < 0 || math.IsNaN(p.PriceAmount) || math.IsInf(p.PriceAmount, 0) || p.TotalAmount < 0 || p.TotalAmount > common.MaxWalletQuota || p.MaxPurchasePerUser < 0 {
		return errors.New("invalid product price, quota or purchase limit")
	}
	if p.Currency != "CNY" && p.Currency != "USD" {
		return errors.New("product currency must be CNY or USD")
	}
	for _, group := range []string{p.UpgradeGroup, p.DowngradeGroup} {
		if group != "" {
			if _, ok := ratio_setting.GetGroupRatioCopy()[group]; !ok {
				return errors.New("product group does not exist")
			}
		}
	}
	if p.BillingMultiplier != 1 || p.CourseEligible {
		return errors.New("product model multipliers belong to price versions; course grants require an explicit separate operation")
	}
	// Bound arithmetic to representable durations, rather than imposing a
	// commercial lifetime. AddDate's year/month arguments need their own bound.
	switch p.DurationUnit {
	case SubscriptionDurationDay:
		if int64(p.DurationValue) > math.MaxInt64/int64(24*time.Hour) {
			return errors.New("product duration overflows")
		}
	case SubscriptionDurationHour:
		if int64(p.DurationValue) > math.MaxInt64/int64(time.Hour) {
			return errors.New("product duration overflows")
		}
	case SubscriptionDurationCustom:
		if p.CustomSeconds > math.MaxInt64/int64(time.Second) {
			return errors.New("product duration overflows")
		}
	case SubscriptionDurationYear, SubscriptionDurationMonth:
		if p.DurationValue > 9999 {
			return errors.New("product calendar duration is out of range")
		}
	}
	if _, err := calcPlanEndTime(time.Now(), p); err != nil {
		return err
	}
	switch p.QuotaResetPeriod {
	case SubscriptionResetNever, SubscriptionResetDaily, SubscriptionResetWeekly, SubscriptionResetMonthly:
	case SubscriptionResetCustom:
		if p.QuotaResetCustomSeconds <= 0 {
			return errors.New("custom reset interval must be positive")
		}
	default:
		return errors.New("invalid product quota reset period")
	}
	for _, name := range explicitModelIds(p.ModelScope) {
		if strings.ContainsAny(name, "*?") {
			return errors.New("product price books require exact model IDs")
		}
	}
	return nil
}

// validateProductPlanPrices separates new sales from paid issuance: sale
// switches and effective times gate checkout; a paid immutable order retains
// its version even if the operator later takes that price off sale.
func validateProductPlanPrices(tx *gorm.DB, p *SubscriptionPlan, forSale bool) error {
	if tx == nil {
		return errors.New("transaction is nil")
	}
	if err := p.ValidateProductPlan(); err != nil {
		return err
	}
	models := explicitModelIds(p.ModelScope)
	if len(models) == 0 || strings.TrimSpace(p.PriceVersion) == "" {
		return errors.New("product requires explicit models and a frozen price version")
	}
	for _, name := range models {
		query := tx
		if forSale {
			query = lockForUpdate(tx)
		}
		price, err := ResolvePriceBookPrice(query, p.PriceBook, name, p.PriceVersion)
		if err != nil {
			return fmt.Errorf("product model %s: %w", name, err)
		}
		if forSale && (!price.Enabled || price.EffectiveAt > getDBTimestampTx(tx)) {
			return fmt.Errorf("product price for model %s is disabled or not yet effective", name)
		}
	}
	return nil
}

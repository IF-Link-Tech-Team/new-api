package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ProductMigrationResult contains configuration only, never customer data.
type ProductMigrationResult struct {
	PlanID         int    `json:"plan_id"`
	PriceBook      string `json:"price_book"`
	CopiedPrices   int    `json:"copied_prices"`
	ConfigRevision int64  `json:"config_revision"`
	Fingerprint    string `json:"fingerprint"`
}

// MigrateSubscriptionPlanToProduct changes the live product for future sales.
// Paid entitlements, keys and pending orders keep their original frozen terms
// and legacy settlement path. No quota, cash price, lifetime, visibility or
// model scope is changed. The old catalog remains for historical settlement.
// Preview performs SELECTs only; applying requires that exact configuration
// fingerprint, because legacy editors do not advance config_revision.
func MigrateSubscriptionPlanToProduct(db *gorm.DB, planID int, expectedRevision int64, fingerprint string, preview bool) (ProductMigrationResult, error) {
	result := ProductMigrationResult{PlanID: planID}
	if db == nil || planID <= 0 || expectedRevision < 0 || expectedRevision == math.MaxInt64 {
		return result, errors.New("invalid product migration request")
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var current SubscriptionPlan
		query := tx
		if !preview {
			query = lockForUpdate(tx)
		}
		if err := query.First(&current, planID).Error; err != nil {
			return err
		}
		if current.PriceBook != "" {
			return errors.New("product is already migrated")
		}
		if current.ConfigRevision != expectedRevision {
			return ErrProductPlanConflict
		}
		if !RequiresStrictV1RoutePolicy(current.ProductType) || strings.TrimSpace(current.PriceVersion) == "" {
			return errors.New("legacy product requires explicit pricing migration; no prices will be inferred")
		}
		if current.CourseEligible {
			return errors.New("course entitlement must be migrated explicitly")
		}
		models := explicitModelIds(current.ModelScope)
		if len(models) == 0 {
			return errors.New("migration requires explicit model scope")
		}
		book, _ := ProductPriceBook(planID)
		var sources []ProductPrice
		sourceQuery := tx
		if !preview {
			sourceQuery = lockForUpdate(tx)
		}
		if err := sourceQuery.Where("product_type = ? AND model IN ?", current.ProductType, models).Order("id").Find(&sources).Error; err != nil {
			return err
		}
		candidate := current
		candidate.PriceBook, candidate.ProductType, candidate.TemplateId = book, "", 0
		candidate.ProductFamily, candidate.ProductTier = "", 0
		candidate.BillingMultiplier = 1
		candidate.AllowedChannels, candidate.MaxUpstreamCost, candidate.FallbackAllowed = "", "", false
		candidate.ConfigRevision++
		if err := candidate.ValidateProductPlan(); err != nil {
			return err
		}
		encoded, err := common.Marshal(struct {
			Plan   SubscriptionPlan
			Prices []ProductPrice
		}{current, sources})
		if err != nil {
			return err
		}
		digest := sha256.Sum256(encoded)
		result = ProductMigrationResult{planID, book, len(sources), candidate.ConfigRevision, hex.EncodeToString(digest[:])}
		if !preview && (fingerprint == "" || fingerprint != result.Fingerprint) {
			return ErrProductPlanConflict
		}
		available := map[string]bool{}
		for _, source := range sources {
			if err := source.ValidateV1(); err != nil {
				return err
			}
			entry := priceBookFromLegacy(source, book)
			if err := checkMigrationPrice(tx, entry); err != nil {
				return err
			}
			if source.PriceVersion == current.PriceVersion {
				if current.Enabled || current.RedemptionEnabled {
					if !source.Enabled || source.EffectiveAt > getDBTimestampTx(tx) {
						return fmt.Errorf("model %s sale price unavailable", source.Model)
					}
					if err := validateProductPriceBookCostFloor(&entry); err != nil {
						return err
					}
				}
				available[source.Model] = true
			}
			if !preview {
				var count int64
				if err := tx.Model(&PriceBookPrice{}).Where("price_book = ? AND model = ? AND price_version = ?", book, entry.Model, entry.PriceVersion).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					// Preserve historical timestamps, including legitimate zeroes.
					if err := tx.Session(&gorm.Session{SkipHooks: true}).Create(&entry).Error; err != nil {
						return err
					}
				}
			}
		}
		for _, modelName := range models {
			if !available[modelName] {
				return fmt.Errorf("model %s has no frozen sale price", modelName)
			}
		}
		if preview {
			return nil
		}
		if candidate.Enabled || candidate.RedemptionEnabled {
			if err := validateProductPlanPrices(tx, &candidate, true); err != nil {
				return err
			}
		}
		candidate.UpdatedAt = common.GetTimestamp()
		update := tx.Model(&SubscriptionPlan{}).Where("id = ? AND config_revision = ? AND (price_book = '' OR price_book IS NULL)", planID, expectedRevision).Select("*").Omit("id").Updates(&candidate)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrProductPlanConflict
		}
		return nil
	})
	if err == nil && !preview {
		InvalidateSubscriptionPlanCache(planID)
	}
	return result, err
}

func priceBookFromLegacy(source ProductPrice, book string) PriceBookPrice {
	return PriceBookPrice{
		PriceBook: book, Model: source.Model, PriceVersion: source.PriceVersion, EffectiveAt: source.EffectiveAt,
		OfficialReferencePrice: source.OfficialReferencePrice, UserSellingPrice: source.UserSellingPrice,
		UpstreamSettlementPrice: source.UpstreamSettlementPrice, BillingExpression: source.BillingExpression,
		BillingMultiplier: source.BillingMultiplier, AllowedChannels: source.AllowedChannels,
		MaxUpstreamCost: source.MaxUpstreamCost, FallbackAllowed: source.FallbackAllowed,
		Enabled: source.Enabled, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}
}

func checkMigrationPrice(tx *gorm.DB, price PriceBookPrice) error {
	if err := price.Validate(); err != nil {
		return err
	}
	var existing PriceBookPrice
	err := tx.Where("price_book = ? AND model = ? AND price_version = ?", price.PriceBook, price.Model, price.PriceVersion).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	price.Id = existing.Id
	if price != existing {
		return fmt.Errorf("price book conflict for %s/%s/%s", price.PriceBook, price.Model, price.PriceVersion)
	}
	return nil
}

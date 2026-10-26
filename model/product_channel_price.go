package model

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ProductChannelRate contains sales policy only. APIChannelPrice remains the
// sole source of the four base prices; no expression is copied into a product.
type ProductChannelRate struct {
	ChannelID  int     `json:"channel_id"`
	Multiplier float64 `json:"multiplier"`
	Position   *int    `json:"position"`
}

func (p *PriceBookPrice) ProductChannelRates() ([]ProductChannelRate, error) {
	var rates []ProductChannelRate
	if p == nil || p.ChannelRates == "" {
		return nil, nil
	}
	if err := common.UnmarshalJsonStr(p.ChannelRates, &rates); err != nil {
		return nil, err
	}
	if len(rates) == 0 || len(rates) > 500 {
		return nil, errors.New("product model requires at least one channel")
	}
	ids, positions := map[int]bool{}, map[int]bool{}
	ordered, inherited := false, false
	for _, rate := range rates {
		if rate.ChannelID <= 0 || ids[rate.ChannelID] || math.IsNaN(rate.Multiplier) || math.IsInf(rate.Multiplier, 0) || rate.Multiplier <= 0 || rate.Multiplier > 99.999999 {
			return nil, errors.New("product channels must be unique and multipliers finite, positive and at most 99.999999")
		}
		ids[rate.ChannelID] = true
		if rate.Position == nil {
			inherited = true
			continue
		}
		ordered = true
		if *rate.Position < 0 || *rate.Position > 10000 || positions[*rate.Position] {
			return nil, errors.New("product route positions must be distinct non-negative integers")
		}
		positions[*rate.Position] = true
	}
	if ordered && inherited {
		return nil, errors.New("set a position for every product channel, or inherit for all")
	}
	slices.SortStableFunc(rates, func(a, b ProductChannelRate) int {
		if a.Position != nil && b.Position != nil {
			return *a.Position - *b.Position
		}
		return a.ChannelID - b.ChannelID
	})
	return rates, nil
}

// RequestProductPrice freezes the entitlement version once for the whole retry
// chain, including its multipliers. A later publication cannot change it.
func RequestProductPrice(ctx *gin.Context, modelName string) (*PriceBookPrice, error) {
	key := "iflink_product_price:" + modelName
	if value, ok := ctx.Get(key); ok {
		return value.(*PriceBookPrice), nil
	}
	book := common.GetContextKeyString(ctx, constant.ContextKeyTokenPriceBook)
	version := common.GetContextKeyString(ctx, constant.ContextKeyTokenPriceVersion)
	price, err := ResolvePriceBookPrice(DB, book, modelName, version)
	if err != nil {
		return nil, fmt.Errorf("product price version unavailable: %w", err)
	}
	if price == nil {
		return nil, errors.New("product price version unavailable")
	}
	ctx.Set(key, price)
	return price, nil
}

// validateProductChannelSales checks the exact current API catalog and mapping.
// Callers that publish take the API catalog lock before the plan lock, matching
// API-price publication and preventing a concurrent removal from escaping.
func validateProductChannelSales(tx *gorm.DB, price *PriceBookPrice, apiRows []APIChannelPrice, requireAll bool) error {
	rates, err := price.ProductChannelRates()
	if err != nil || len(rates) == 0 {
		return errors.New("product has no channel rates")
	}
	usable := 0
	for _, rate := range rates {
		index := slices.IndexFunc(apiRows, func(row APIChannelPrice) bool {
			return row.Model == price.Model && row.ChannelID == rate.ChannelID && row.Enabled
		})
		if index < 0 {
			if requireAll {
				return fmt.Errorf("product model %s channel %d has no active API price", price.Model, rate.ChannelID)
			}
			continue
		}
		row := apiRows[index]
		if _, err := row.BillingExpression(); err != nil {
			return err
		}
		var channel Channel
		if err := tx.Select("id", "status", "models", "model_mapping").First(&channel, rate.ChannelID).Error; err != nil {
			if !requireAll && errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		if channel.Status != common.ChannelStatusEnabled {
			if requireAll {
				return fmt.Errorf("product channel %d is disabled", rate.ChannelID)
			}
			continue
		}
		mapped, err := ResolveMappedModelName(channel.GetModelMapping(), price.Model)
		if err != nil || !slices.Contains(channel.GetModels(), price.Model) || mapped != row.UpstreamModel {
			if requireAll {
				return errors.New("product channel mapping changed; reload before saving")
			}
			continue
		}
		usable++
	}
	if usable == 0 {
		return fmt.Errorf("product model %s has no active API channel", price.Model)
	}
	return nil
}

type ProductChannelModel struct {
	Model string               `json:"model"`
	Rates []ProductChannelRate `json:"rates"`
}

// StageProductChannelVersion writes one complete immutable draft. It cannot
// select a sale version or change already-sold entitlements. The plan CAS also
// fences stale editors and serializes duplicate submissions on SQLite.
func StageProductChannelVersion(db *gorm.DB, planID int, expectedRevision, expectedAPIRevision int64, version string, models []ProductChannelModel, fallbackAllowed bool) (int64, error) {
	if planID <= 0 || expectedRevision <= 0 || expectedRevision == math.MaxInt64 || strings.TrimSpace(version) != version || version == "" || len(version) > 64 || len(models) == 0 || len(models) > 100 {
		return 0, errors.New("invalid product channel draft")
	}
	var revision int64
	err := db.Transaction(func(tx *gorm.DB) error {
		// Acquire a database write lock with a CAS before reading the plan.
		// SQLite cannot upgrade a deferred read transaction reliably under
		// contention; the epoch update also serializes this with catalog writes.
		lock := tx.Model(&APIChannelCatalog{}).
			Where("id = ? AND revision = ?", 1, expectedAPIRevision).
			UpdateColumn("write_epoch", gorm.Expr("write_epoch + 1"))
		if lock.Error != nil {
			return lock.Error
		}
		if lock.RowsAffected != 1 {
			return ErrAPIChannelPriceConflict
		}
		var plan SubscriptionPlan
		if err := lockForUpdate(tx).First(&plan, planID).Error; err != nil {
			return err
		}
		book, _ := ProductPriceBook(planID)
		if plan.PriceBook != book || plan.ProductType != "" || plan.ConfigRevision != expectedRevision {
			return ErrProductPlanConflict
		}
		var count int64
		if err := tx.Model(&PriceBookPrice{}).Where("price_book = ? AND price_version = ?", book, version).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrPriceBookVersionExists
		}
		apiRows, err := APIChannelPricesAt(tx, expectedAPIRevision, "")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		prices := make([]PriceBookPrice, 0, len(models))
		for _, item := range models {
			if seen[item.Model] {
				return errors.New("duplicate product model")
			}
			seen[item.Model] = true
			encoded, err := common.Marshal(item.Rates)
			if err != nil {
				return err
			}
			price := PriceBookPrice{
				PriceBook:         book,
				Model:             item.Model,
				PriceVersion:      version,
				ChannelRates:      string(encoded),
				BillingMultiplier: 1,
				FallbackAllowed:   fallbackAllowed,
				Enabled:           true,
				EffectiveAt:       common.GetTimestamp(),
			}
			if err := price.Validate(); err != nil {
				return err
			}
			if err := validateProductChannelSales(tx, &price, apiRows, true); err != nil {
				return err
			}
			prices = append(prices, price)
		}
		revision = expectedRevision + 1
		update := tx.Model(&SubscriptionPlan{}).Where("id = ? AND config_revision = ?", planID, expectedRevision).Update("config_revision", revision)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrProductPlanConflict
		}
		return tx.Create(&prices).Error
	})
	if err == nil {
		InvalidateSubscriptionPlanCache(planID)
	}
	return revision, err
}

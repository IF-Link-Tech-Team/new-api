package model

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const WalletPriceBook = "wallet"

const (
	ProductRouteGlobal  = "global"
	ProductRouteOrdered = "ordered"
)

var ErrPriceBookVersionExists = errors.New("price book version already exists")

// PriceBookPrice is the expansion-stage successor of ProductPrice. The old
// catalog stays intact until orders, keys and relay reads have been migrated.
// Price and route data share the same immutable version.
type PriceBookPrice struct {
	Id           int    `json:"id"`
	PriceBook    string `json:"price_book" gorm:"type:varchar(64);not null;uniqueIndex:idx_price_book_version,priority:1"`
	Model        string `json:"model" gorm:"type:varchar(255);not null;uniqueIndex:idx_price_book_version,priority:2"`
	PriceVersion string `json:"price_version" gorm:"type:varchar(64);not null;uniqueIndex:idx_price_book_version,priority:3"`
	EffectiveAt  int64  `json:"effective_at" gorm:"bigint;index"`

	OfficialReferencePrice  string  `json:"official_reference_price" gorm:"type:text;not null"`
	UserSellingPrice        string  `json:"user_selling_price" gorm:"type:text;not null"`
	UpstreamSettlementPrice string  `json:"upstream_settlement_price" gorm:"type:text;not null"`
	BillingExpression       string  `json:"billing_expression" gorm:"type:text"`
	BillingMultiplier       float64 `json:"billing_multiplier" gorm:"type:decimal(8,6);not null"`
	// Non-empty selects the model/channel contract. Legacy price fields remain
	// readable for historical versions, but are never a fallback for this mode.
	ChannelRates    string `json:"channel_rates" gorm:"type:text"`
	AllowedChannels string `json:"allowed_channels" gorm:"type:text;not null"`
	// Empty is the historical global-priority behavior. In ordered mode the
	// existing channel list is the immutable preference order, not a second list.
	RouteMode       string `json:"route_mode" gorm:"type:varchar(16)"`
	MaxUpstreamCost string `json:"max_upstream_cost" gorm:"type:text;not null"`
	FallbackAllowed bool   `json:"fallback_allowed"`
	Enabled         bool   `json:"enabled"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint;autoCreateTime:false"`
	UpdatedAt       int64  `json:"updated_at" gorm:"bigint;autoUpdateTime:false"`
}

func ProductPriceBook(planId int) (string, error) {
	if planId <= 0 {
		return "", errors.New("price book requires a positive plan id")
	}
	return "product:" + strconv.Itoa(planId), nil
}

func legacyPriceBook(productType string, planId int) (string, error) {
	if productType == ProductTypeWallet {
		return WalletPriceBook, nil
	}
	if !RequiresStrictV1RoutePolicy(productType) {
		return "", nil
	}
	return ProductPriceBook(planId)
}

func (p *PriceBookPrice) Validate() error {
	if p == nil {
		return errors.New("price book price is nil")
	}
	if p.RouteMode != "" && p.RouteMode != ProductRouteGlobal && p.RouteMode != ProductRouteOrdered {
		return errors.New("product route mode must be global or ordered")
	}
	if p.PriceBook == WalletPriceBook && p.RouteMode == ProductRouteOrdered {
		return errors.New("wallet overrides do not support product route ordering")
	}
	if strings.TrimSpace(p.Model) != p.Model || len(p.Model) > 255 ||
		strings.TrimSpace(p.PriceVersion) != p.PriceVersion || len(p.PriceVersion) > 64 {
		return errors.New("model and price_version must fit their storage limits without surrounding whitespace")
	}
	if p.PriceBook != WalletPriceBook {
		id, err := strconv.Atoi(strings.TrimPrefix(p.PriceBook, "product:"))
		if err != nil || id <= 0 || p.PriceBook != "product:"+strconv.Itoa(id) {
			return errors.New("price_book must be wallet or product:<plan_id>")
		}
	}
	if p.ChannelRates != "" {
		if p.PriceBook == WalletPriceBook || p.Model == "" || p.PriceVersion == "" || strings.ContainsAny(p.Model, "*?,\r\n") {
			return errors.New("channel rates require an exact product model and version")
		}
		_, err := p.ProductChannelRates()
		return err
	}
	terms := p.productPrice()
	return terms.validatePriceTerms()
}

func (p *PriceBookPrice) productPrice() ProductPrice {
	return ProductPrice{
		Model: p.Model, PriceVersion: p.PriceVersion, EffectiveAt: p.EffectiveAt,
		OfficialReferencePrice: p.OfficialReferencePrice, UserSellingPrice: p.UserSellingPrice,
		UpstreamSettlementPrice: p.UpstreamSettlementPrice, BillingExpression: p.BillingExpression,
		BillingMultiplier: p.BillingMultiplier, AllowedChannels: p.AllowedChannels,
		MaxUpstreamCost: p.MaxUpstreamCost, FallbackAllowed: p.FallbackAllowed,
		Enabled: p.Enabled, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// validateProductPriceBookCostFloor compares the configured unit-price fields
// for new sales. This is only one sale gate, not proof of package profitability:
// the executed expression/multipliers, quota exposure, net cash and verified
// channel costs need separate verification. Paid issuance never runs this gate.
func validateProductPriceBookCostFloor(p *PriceBookPrice) error {
	if p == nil {
		return errors.New("price book price is nil")
	}
	if p.ChannelRates != "" {
		// These versions reference live API channel prices. Their sale validation
		// is performed against that catalog, not obsolete duplicated price JSON.
		_, err := p.ProductChannelRates()
		return err
	}
	var sale, cost PriceComponents
	if err := common.UnmarshalJsonStr(strings.TrimSpace(p.UserSellingPrice), &sale); err != nil {
		return fmt.Errorf("invalid user_selling_price: %w", err)
	}
	if err := common.UnmarshalJsonStr(strings.TrimSpace(p.MaxUpstreamCost), &cost); err != nil {
		return fmt.Errorf("invalid max_upstream_cost: %w", err)
	}
	violations := []struct {
		name string
		cost float64
		sale float64
	}{
		{"input", cost.Input, sale.Input},
		{"cached_input", cost.CachedInput, sale.CachedInput},
		{"cache_creation", cost.CacheCreation, sale.CacheCreation},
		{"output", cost.Output, sale.Output},
	}
	for _, component := range violations {
		if component.cost > component.sale {
			return fmt.Errorf("generic product cost floor violated for %s: max upstream cost %.6g exceeds user selling price %.6g", component.name, component.cost, component.sale)
		}
	}
	return nil
}

func (p *PriceBookPrice) BeforeCreate(tx *gorm.DB) error {
	if err := p.Validate(); err != nil {
		return err
	}
	now := common.GetTimestamp()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	if p.UpdatedAt == 0 {
		p.UpdatedAt = now
	}
	return nil
}

// ResolvePriceBookPrice returns nil only for an unconfigured wallet model,
// which retains upstream global pricing. A configured but unavailable wallet
// model and every missing product version fail closed. Historical versions
// remain readable after their sale switch is disabled.
func ResolvePriceBookPrice(db *gorm.DB, priceBook, modelName, priceVersion string) (*PriceBookPrice, error) {
	if modelName == "" || strings.TrimSpace(modelName) != modelName || strings.ContainsAny(modelName, "*?") {
		return nil, errors.New("price book requires an exact model id")
	}
	key := PriceBookPrice{PriceBook: priceBook}
	if priceBook != WalletPriceBook {
		id, err := strconv.Atoi(strings.TrimPrefix(priceBook, "product:"))
		if err != nil || id <= 0 || priceBook != "product:"+strconv.Itoa(id) {
			return nil, errors.New("invalid price book")
		}
		if strings.TrimSpace(priceVersion) == "" {
			return nil, errors.New("product price book requires a frozen version")
		}
	}
	query := db.Where("price_book = ? AND model = ?", priceBook, modelName)
	if priceVersion == "" {
		var count int64
		if err := query.Model(&PriceBookPrice{}).Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, nil
		}
		query = query.Where("enabled = ? AND (effective_at = 0 OR effective_at <= ?)", true, GetDBTimestamp()).Order("effective_at desc, id desc")
	} else {
		query = query.Where("price_version = ?", priceVersion)
	}
	if err := query.First(&key).Error; err != nil {
		return nil, fmt.Errorf("price book version is unavailable: %w", err)
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	return &key, nil
}

// Published versions may only change their sale switch. Disabled historical
// rows remain available to already-paid entitlements.
func (p *PriceBookPrice) BeforeUpdate(tx *gorm.DB) error {
	if p.Id <= 0 {
		return errors.New("price book update requires an individual version id")
	}
	var existing PriceBookPrice
	if err := lockForUpdate(tx).First(&existing, p.Id).Error; err != nil {
		return err
	}
	if updates, ok := tx.Statement.Dest.(map[string]any); ok {
		for field := range updates {
			if field != "enabled" && field != "Enabled" {
				return errors.New("price book versions are immutable; create a new version")
			}
		}
	} else {
		candidate := *p
		candidate.Enabled = existing.Enabled
		candidate.UpdatedAt = existing.UpdatedAt
		if !reflect.DeepEqual(candidate, existing) {
			return errors.New("price book versions are immutable; create a new version")
		}
	}
	tx.Statement.SetColumn("UpdatedAt", common.GetTimestamp())
	return nil
}

// CreateProductPriceBookVersion stages a new immutable version for a concrete
// product. Selecting it for new sales is a separate plan change; existing
// orders, entitlements and other products are never changed here.
func CreateProductPriceBookVersion(db *gorm.DB, planId int, price *PriceBookPrice) error {
	channelPricing, err := APIChannelPricingMode()
	if err != nil {
		return err
	}
	if channelPricing {
		return errors.New("Use model and channel rates for new product versions")
	}
	book, err := ProductPriceBook(planId)
	if err != nil {
		return err
	}
	if price == nil || (price.PriceBook != "" && price.PriceBook != book) {
		return errors.New("price book must match the requested product")
	}
	if price.ChannelRates != "" {
		return errors.New("channel-rate versions must use the complete product draft endpoint")
	}
	candidate := *price
	candidate.PriceBook = book
	candidate.Id, candidate.CreatedAt, candidate.UpdatedAt = 0, 0, 0
	if err := candidate.Validate(); err != nil {
		return err
	}
	if candidate.Enabled {
		if err := validateProductPriceBookCostFloor(&candidate); err != nil {
			return err
		}
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var plan SubscriptionPlan
		if err := lockForUpdate(tx).First(&plan, planId).Error; err != nil {
			return err
		}
		if plan.ProductType == ProductTypeWallet {
			return errors.New("wallet is not a saleable product")
		}
		var count int64
		if err := tx.Model(&PriceBookPrice{}).Where("price_book = ? AND model = ? AND price_version = ?",
			book, candidate.Model, candidate.PriceVersion).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrPriceBookVersionExists
		}
		return tx.Create(&candidate).Error
	})
	if err == nil {
		*price = candidate
	}
	return err
}

// SetProductPriceBookVersionEnabled changes only the new-sale switch. Legacy
// plans remain owned by the legacy dual-write API until explicitly migrated.
// Advancing the plan revision prevents an older editor from restoring a sale
// state it loaded before this operation. Paid immutable versions stay readable.
func SetProductPriceBookVersionEnabled(db *gorm.DB, planId, priceId int, enabled bool, expectedRevision int64) (*PriceBookPrice, int64, error) {
	book, err := ProductPriceBook(planId)
	if err != nil || priceId <= 0 {
		return nil, 0, errors.New("price book requires a positive product and price id")
	}
	var price PriceBookPrice
	var revision int64
	err = db.Transaction(func(tx *gorm.DB) error {
		var plan SubscriptionPlan
		if err := lockForUpdate(tx).First(&plan, planId).Error; err != nil {
			return err
		}
		if plan.PriceBook != book || plan.ProductType != "" {
			return errors.New("product price book conversion requires an explicit migration")
		}
		if expectedRevision <= 0 || expectedRevision == math.MaxInt64 || expectedRevision != plan.ConfigRevision {
			return ErrProductPlanConflict
		}
		if err := lockForUpdate(tx).Where("id = ? AND price_book = ?", priceId, book).First(&price).Error; err != nil {
			return err
		}
		if enabled {
			if err := validateProductPriceBookCostFloor(&price); err != nil {
				return err
			}
		}
		if err := tx.Model(&price).Update("enabled", enabled).Error; err != nil {
			return err
		}
		revision = plan.ConfigRevision + 1
		result := tx.Model(&SubscriptionPlan{}).Where("id = ? AND config_revision = ?", planId, expectedRevision).
			Updates(map[string]any{"config_revision": revision, "updated_at": common.GetTimestamp()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrProductPlanConflict
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	InvalidateSubscriptionPlanCache(planId)
	return &price, revision, nil
}

func CreateLegacyProductPrice(db *gorm.DB, price *ProductPrice) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(price).Error; err != nil {
			return err
		}
		return SyncLegacyProductPriceBooks(tx, *price)
	})
}

func SetLegacyProductPriceEnabled(db *gorm.DB, id int, enabled bool) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var price ProductPrice
		if err := lockForUpdate(tx).First(&price, id).Error; err != nil {
			return err
		}
		price.Enabled, price.UpdatedAt = enabled, common.GetTimestamp()
		if err := tx.Session(&gorm.Session{SkipHooks: true}).Model(&ProductPrice{}).Where("id = ?", id).
			Updates(map[string]any{"enabled": price.Enabled, "updated_at": price.UpdatedAt}).Error; err != nil {
			return err
		}
		return SyncLegacyProductPriceBooks(tx, price)
	})
}

// SyncLegacyProductPriceBooks mirrors an administrative legacy-price write in
// the same transaction. The old catalog remains the write authority during
// expansion. Frozen economics cannot be replaced; only its sale switch changes.
func SyncLegacyProductPriceBooks(tx *gorm.DB, price ProductPrice) error {
	if err := price.ValidateV1(); err != nil {
		return err
	}
	books := map[string]bool{}
	if price.ProductType == ProductTypeWallet {
		books[WalletPriceBook] = true
	} else if RequiresStrictV1RoutePolicy(price.ProductType) {
		type scope struct {
			PlanId     int
			ModelScope string
		}
		for _, source := range []struct {
			model any
			id    string
		}{
			{&SubscriptionPlan{}, "id"},
			{&SubscriptionOrder{}, "plan_id"},
			{&UserSubscription{}, "plan_id"},
		} {
			var scopes []scope
			if err := tx.Model(source.model).Select(source.id+" AS plan_id, model_scope").Distinct().Where("product_type = ?", price.ProductType).Find(&scopes).Error; err != nil {
				return err
			}
			for _, item := range scopes {
				for _, modelName := range explicitModelIds(item.ModelScope) {
					if modelName == price.Model {
						book, err := ProductPriceBook(item.PlanId)
						if err != nil {
							return err
						}
						books[book] = true
						break
					}
				}
			}
		}
	}
	for _, book := range slices.Sorted(maps.Keys(books)) {
		if book != WalletPriceBook {
			id, _ := strconv.Atoi(strings.TrimPrefix(book, "product:"))
			var plan SubscriptionPlan
			if err := lockForUpdate(tx).Where("id = ?", id).Find(&plan).Error; err != nil {
				return err
			}
			// Historical orders still reference the old type. Once the live
			// product owns this book, legacy admin writes lose authority over
			// all its versions, including their sale switches.
			if plan.PriceBook != "" {
				continue
			}
		}
		if err := syncLegacyPriceBookPrice(tx, price, book); err != nil {
			return err
		}
	}
	return nil
}

func syncLegacyPriceBookPrice(tx *gorm.DB, price ProductPrice, book string) error {
	entry := PriceBookPrice{
		PriceBook: book, Model: price.Model, PriceVersion: price.PriceVersion, EffectiveAt: price.EffectiveAt,
		OfficialReferencePrice: price.OfficialReferencePrice, UserSellingPrice: price.UserSellingPrice,
		UpstreamSettlementPrice: price.UpstreamSettlementPrice, BillingExpression: price.BillingExpression,
		BillingMultiplier: price.BillingMultiplier, AllowedChannels: price.AllowedChannels,
		MaxUpstreamCost: price.MaxUpstreamCost, FallbackAllowed: price.FallbackAllowed,
		Enabled: price.Enabled, CreatedAt: price.CreatedAt, UpdatedAt: price.UpdatedAt,
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	// A unique-key insert also serializes concurrent publication of the
	// same target. Never use an upsert that overwrites frozen economics.
	if err := tx.Session(&gorm.Session{SkipHooks: true}).Clauses(clause.OnConflict{DoNothing: true}).Create(&entry).Error; err != nil {
		return err
	}
	var existing PriceBookPrice
	if err := lockForUpdate(tx).Where("price_book = ? AND model = ? AND price_version = ?", book, price.Model, price.PriceVersion).First(&existing).Error; err != nil {
		return err
	}
	entry.Id, entry.CreatedAt = existing.Id, existing.CreatedAt
	entry.Enabled, entry.UpdatedAt = existing.Enabled, existing.UpdatedAt
	if !reflect.DeepEqual(entry, existing) {
		return fmt.Errorf("price book dual-write conflict: %s/%s/%s", book, price.Model, price.PriceVersion)
	}
	if err := tx.Session(&gorm.Session{SkipHooks: true}).Model(&PriceBookPrice{}).Where("id = ?", existing.Id).
		Updates(map[string]any{"enabled": price.Enabled, "updated_at": price.UpdatedAt}).Error; err != nil {
		return err
	}
	return nil
}

// SyncSubscriptionPlanPriceBook materializes prices before an administrator's
// new/updated legacy plan becomes visible. Historical rows are never deleted.
func SyncSubscriptionPlanPriceBook(tx *gorm.DB, plan *SubscriptionPlan) error {
	if !RequiresStrictV1RoutePolicy(plan.ProductType) {
		return nil
	}
	book, err := ProductPriceBook(plan.Id)
	if err != nil {
		return err
	}
	models := explicitModelIds(plan.ModelScope)
	slices.Sort(models)
	for _, modelName := range models {
		var prices []ProductPrice
		if err := lockForUpdate(tx).Where("product_type = ? AND model = ?", plan.ProductType, modelName).Order("id").Find(&prices).Error; err != nil {
			return err
		}
		foundVersion := false
		for _, price := range prices {
			if price.PriceVersion == plan.PriceVersion {
				foundVersion = true
			}
			if err := syncLegacyPriceBookPrice(tx, price, book); err != nil {
				return err
			}
		}
		if !foundVersion {
			return fmt.Errorf("plan price book missing price: %s/%s", modelName, plan.PriceVersion)
		}
	}
	return nil
}

// BackfillProductPriceBooks copies the old catalog without changing its read
// path. Run with application writers stopped, then verify again immediately
// before read cutover. It is deliberately not a startup migration: until the
// dual-write release, old administrators still write only ProductPrice.
// Historical order/entitlement types are included even if a plan was changed or
// deleted. Conflicting frozen versions abort the whole data transaction.
func BackfillProductPriceBooks(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		type assignment struct {
			PlanId       int
			ProductType  string
			ModelScope   string
			PriceVersion string
		}
		var assignments []assignment
		for _, source := range []struct {
			model         any
			selectColumns string
		}{
			{&SubscriptionPlan{}, "id AS plan_id, product_type, model_scope, price_version"},
			{&SubscriptionOrder{}, "plan_id, product_type, model_scope, price_version"},
			{&UserSubscription{}, "plan_id, product_type, model_scope, price_version"},
		} {
			var rows []assignment
			if err := tx.Model(source.model).Select(source.selectColumns).Distinct().Find(&rows).Error; err != nil {
				return err
			}
			assignments = append(assignments, rows...)
		}
		// Each product keeps the union of its current and historical model
		// scopes. A type-wide catalog must not silently broaden its model set.
		books := map[string]map[string]map[string]bool{ProductTypeWallet: {WalletPriceBook: nil}}
		for _, item := range assignments {
			if item.ProductType == ProductTypeWallet {
				continue
			}
			if !RequiresStrictV1RoutePolicy(item.ProductType) {
				continue // upstream/global-price legacy plans need explicit conversion later
			}
			book, err := ProductPriceBook(item.PlanId)
			if err != nil {
				return err
			}
			if books[item.ProductType] == nil {
				books[item.ProductType] = map[string]map[string]bool{}
			}
			if books[item.ProductType][book] == nil {
				books[item.ProductType][book] = map[string]bool{}
			}
			if strings.TrimSpace(item.PriceVersion) == "" || len(explicitModelIds(item.ModelScope)) == 0 {
				return fmt.Errorf("price book migration requires explicit model/version for plan %d", item.PlanId)
			}
			for _, modelName := range explicitModelIds(item.ModelScope) {
				books[item.ProductType][book][modelName] = true
				var count int64
				if err := tx.Model(&ProductPrice{}).Where("product_type = ? AND model = ? AND price_version = ?",
					item.ProductType, modelName, item.PriceVersion).Count(&count).Error; err != nil {
					return err
				}
				if count != 1 {
					return fmt.Errorf("price book migration missing price: %s/%s/%s", book, modelName, item.PriceVersion)
				}
			}
		}
		var prices []ProductPrice
		if err := tx.Order("id").Find(&prices).Error; err != nil {
			return err
		}
		for _, price := range prices {
			if err := price.ValidateV1(); err != nil {
				return fmt.Errorf("legacy price %d: %w", price.Id, err)
			}
			for book, models := range books[price.ProductType] {
				if book != WalletPriceBook && !models[price.Model] {
					continue
				}
				entry := PriceBookPrice{
					PriceBook: book, Model: price.Model, PriceVersion: price.PriceVersion, EffectiveAt: price.EffectiveAt,
					OfficialReferencePrice: price.OfficialReferencePrice, UserSellingPrice: price.UserSellingPrice,
					UpstreamSettlementPrice: price.UpstreamSettlementPrice, BillingExpression: price.BillingExpression,
					BillingMultiplier: price.BillingMultiplier, AllowedChannels: price.AllowedChannels,
					MaxUpstreamCost: price.MaxUpstreamCost, FallbackAllowed: price.FallbackAllowed,
					Enabled: price.Enabled, CreatedAt: price.CreatedAt, UpdatedAt: price.UpdatedAt,
				}
				var existing PriceBookPrice
				err := tx.Where("price_book = ? AND model = ? AND price_version = ?", book, price.Model, price.PriceVersion).First(&existing).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					if err := entry.Validate(); err != nil {
						return err
					}
					if err := tx.Session(&gorm.Session{SkipHooks: true}).Create(&entry).Error; err != nil {
						return err
					}
					continue
				}
				if err != nil {
					return err
				}
				entry.Id = existing.Id
				if !reflect.DeepEqual(entry, existing) {
					return fmt.Errorf("price book backfill conflict: %s/%s/%s", book, price.Model, price.PriceVersion)
				}
			}
		}
		if err := backfillPriceBookSnapshots(tx, &SubscriptionOrder{}, "id", "plan_id", "product_type"); err != nil {
			return err
		}
		if err := backfillPriceBookSnapshots(tx, &UserSubscription{}, "id", "plan_id", "product_type"); err != nil {
			return err
		}
		var subscriptions []UserSubscription
		if err := tx.Select("id, price_book").Find(&subscriptions).Error; err != nil {
			return err
		}
		for _, sub := range subscriptions {
			if sub.PriceBook == "" {
				continue
			}
			if err := tx.Model(&Token{}).Where("user_subscription_id = ? AND (price_book = '' OR price_book IS NULL)", sub.Id).
				UpdateColumn("price_book", sub.PriceBook).Error; err != nil {
				return err
			}
			var mismatches int64
			if err := tx.Model(&Token{}).Where("user_subscription_id = ? AND price_book <> ?", sub.Id, sub.PriceBook).Count(&mismatches).Error; err != nil {
				return err
			}
			if mismatches > 0 {
				return fmt.Errorf("token price book conflicts with subscription %d", sub.Id)
			}
		}
		if err := tx.Model(&Token{}).Where("user_subscription_id = 0 AND (price_book = '' OR price_book IS NULL) AND product_type = ?", ProductTypeWallet).
			UpdateColumn("price_book", WalletPriceBook).Error; err != nil {
			return err
		}
		return nil
	})
}

func backfillPriceBookSnapshots(tx *gorm.DB, record any, idColumn, planColumn, productTypeColumn string) error {
	type snapshot struct {
		Id          int
		PlanId      int
		ProductType string
		PriceBook   string
	}
	var rows []snapshot
	if err := tx.Model(record).Select(idColumn + " AS id, " + planColumn + " AS plan_id, " + productTypeColumn + " AS product_type, price_book").
		Order(idColumn).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		book, err := legacyPriceBook(row.ProductType, row.PlanId)
		if err != nil {
			return fmt.Errorf("snapshot %d price book: %w", row.Id, err)
		}
		if book == "" {
			continue
		}
		if row.PriceBook != "" && row.PriceBook != book {
			return fmt.Errorf("snapshot %d price book conflicts with its legacy product identity", row.Id)
		}
		if row.PriceBook == "" {
			if err := tx.Model(record).Where(idColumn+" = ? AND (price_book = '' OR price_book IS NULL)", row.Id).
				UpdateColumn("price_book", book).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

package model

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var ErrProductPlanConflict = errors.New("product configuration changed; reload before saving")

const DefaultProductReservationTTL int64 = 30 * 60
const SubscriptionOrderStatusPaidReview = "paid_review"
const ProvisioningStatusCompensationPending = "compensation_pending"

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
	if candidate.ReservationTTLSeconds == 0 {
		candidate.ReservationTTLSeconds = DefaultProductReservationTTL
	}
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

// UpdateLegacySubscriptionPlan fences an editor opened before conversion.
// Call inside the transaction that also synchronizes the legacy price book.
func UpdateLegacySubscriptionPlan(tx *gorm.DB, id int, updates map[string]any) error {
	var current SubscriptionPlan
	if err := lockForUpdate(tx).First(&current, id).Error; err != nil {
		return err
	}
	if current.PriceBook != "" {
		return ErrProductPlanConflict
	}
	return tx.Model(&SubscriptionPlan{}).Where("id = ? AND (price_book = ? OR price_book IS NULL)", id, "").Updates(updates).Error
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
	if p.ReservationTTLSeconds < 0 || p.ReservationTTLSeconds > common.MaxWalletQuota {
		return errors.New("invalid product reservation lifetime")
	}
	// Both supported currencies settle in cents. Keep the order amount equal
	// to the external charge and within the existing DECIMAL(10,6) column.
	if p.PriceAmount > 9999.99 || !decimal.NewFromFloat(p.PriceAmount).Equal(decimal.NewFromFloat(p.PriceAmount).Round(2)) {
		return errors.New("product cash price must fit its storage limit and use at most two decimal places")
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

// productReservationTimeTx uses current database wall time, including after
// waiting for row locks. PostgreSQL NOW() freezes at transaction start; using
// it here could revive a reservation that expired while a callback waited.
func productReservationTimeTx(tx *gorm.DB) (int64, error) {
	query := "SELECT UNIX_TIMESTAMP()"
	switch tx.Dialector.Name() {
	case "postgres":
		query = "SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))::bigint"
	case "sqlite":
		query = "SELECT CAST(strftime('%s','now') AS INTEGER)"
	}
	var now int64
	if err := tx.Raw(query).Scan(&now).Error; err != nil {
		return 0, err
	}
	if now <= 0 {
		return 0, errors.New("invalid database reservation time")
	}
	return now, nil
}

// parkPaidProductOrderTx acknowledges a verified payment only after persisting
// it. It does not claim a cash refund happened or issue quota after a released
// reservation. Finance must explicitly resolve this durable compensation state.
func parkPaidProductOrderTx(tx *gorm.DB, order *SubscriptionOrder, method string, receipt PaymentReceipt, reason string) error {
	order.Status = SubscriptionOrderStatusPaidReview
	order.ProvisioningStatus = ProvisioningStatusCompensationPending
	order.ProvisioningError = reason
	order.CompleteTime = getDBTimestampTx(tx)
	order.PaidMoney, _ = receipt.Amount.Float64()
	order.PaidCurrency = strings.ToUpper(strings.TrimSpace(receipt.Currency))
	order.ProviderTradeNo = strings.TrimSpace(receipt.ProviderTradeNo)
	order.ProviderPayload = receipt.ProviderPayload
	if method != "" {
		order.PaymentMethod = method
	}
	return tx.Save(order).Error
}

// RetryPaidProductOrder explicitly resolves a verified, unfulfilled payment.
// It preserves the original order terms and cannot take a newer order's slot.
// A repeated request returns the first committed decision without reissuing.
func RetryPaidProductOrder(tradeNo string, actorId int, note string) (*SubscriptionOrder, bool, error) {
	note = strings.TrimSpace(note)
	if tradeNo == "" || actorId <= 0 || note == "" || utf8.RuneCountInString(note) > 512 {
		return nil, false, errors.New("invalid product compensation decision")
	}
	var order SubscriptionOrder
	var changed, groupChanged bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
			return err
		}
		if order.ProductPlanSnapshot == "" {
			return ErrSubscriptionOrderStatusInvalid
		}
		if order.Status == common.TopUpStatusSuccess && order.CompensationActorId > 0 && order.UserSubscriptionId != nil {
			return nil
		}
		if order.Status != SubscriptionOrderStatusPaidReview || order.ProvisioningStatus != ProvisioningStatusCompensationPending || order.UserSubscriptionId != nil {
			return ErrSubscriptionOrderStatusInvalid
		}
		receipt := PaymentReceipt{Amount: decimal.NewFromFloat(order.PaidMoney), Currency: order.PaidCurrency,
			ProviderTradeNo: order.ProviderTradeNo, ProviderPayload: order.ProviderPayload}
		if err := validatePaymentReceipt(order.Money, order.CurrencySnapshot, &receipt); err != nil {
			return err
		}
		var user User
		if err := lockForUpdate(tx).Select("id").First(&user, order.UserId).Error; err != nil {
			return err
		}
		livePlan, err := getSubscriptionPlanByIdTx(tx, order.PlanId)
		if err != nil {
			return err
		}
		plan, err := provisionPlanFromOrder(tx, &order, livePlan)
		if err != nil {
			return err
		}
		now, err := productReservationTimeTx(tx)
		if err != nil {
			return err
		}
		if plan.MaxPurchasePerUser > 0 {
			var issued, reserved int64
			if err := tx.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", order.UserId, order.PlanId).Count(&issued).Error; err != nil {
				return err
			}
			if err := tx.Model(&SubscriptionOrder{}).
				Where("user_id = ? AND plan_id = ? AND status = ?", order.UserId, order.PlanId, common.TopUpStatusPending).
				Where("reservation_expires_at = 0 OR reservation_expires_at > ?", now).Count(&reserved).Error; err != nil {
				return err
			}
			if issued+reserved >= int64(plan.MaxPurchasePerUser) {
				return errors.New("product compensation would exceed purchase limit or consume another reservation")
			}
		}
		_, subscription, err := fulfillSubscriptionOrderTx(tx, &order, order.ProviderPayload, order.PaymentMethod, &receipt)
		if err != nil {
			return err
		}
		order.CompensationActorId, order.CompensationTime, order.CompensationNote = actorId, now, note
		if err := tx.Save(&order).Error; err != nil {
			return err
		}
		changed, groupChanged = true, subscription.PrevUserGroup != ""
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if groupChanged {
		refreshSubscriptionUserGroupCache(order.UserId, common.LogText("subscription payment completion"))
	}
	return &order, changed, nil
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
		if forSale {
			if err := validateProductPriceBookCostFloor(price); err != nil {
				return fmt.Errorf("product model %s: %w", name, err)
			}
		}
		if forSale && (!price.Enabled || price.EffectiveAt > getDBTimestampTx(tx)) {
			return fmt.Errorf("product price for model %s is disabled or not yet effective", name)
		}
	}
	return nil
}

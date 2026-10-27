package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrProductPublicationConflict = errors.New("product publication preview changed; preview again")

// ProductPublication and its items are a transactional outbox. They contain
// business snapshots, never API credentials. A scheduler may replay any item.
type ProductPublication struct {
	ID          int64  `json:"id"`
	RequestKey  string `json:"request_key" gorm:"type:varchar(64);not null;uniqueIndex"`
	PlanID      int    `json:"plan_id" gorm:"index"`
	Revision    int64  `json:"revision" gorm:"bigint"`
	ActorID     int    `json:"actor_id"`
	Reason      string `json:"reason" gorm:"type:text"`
	RequestHash string `json:"-" gorm:"type:varchar(64)"`
	Fields      string `json:"fields" gorm:"type:text"`
	BeforePlan  string `json:"before_plan" gorm:"type:text"`
	AfterPlan   string `json:"after_plan" gorm:"type:text"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint;autoCreateTime:false"`
}

type ProductPublicationItem struct {
	ID             int64  `json:"id"`
	PublicationID  int64  `json:"publication_id" gorm:"not null;uniqueIndex:idx_publication_entitlement,priority:1;index"`
	SubscriptionID int    `json:"subscription_id" gorm:"not null;uniqueIndex:idx_publication_entitlement,priority:2"`
	UserID         int    `json:"user_id"`
	Status         string `json:"status" gorm:"type:varchar(24);index"`
	Attempts       int    `json:"attempts"`
	NextRetryAt    int64  `json:"next_retry_at" gorm:"bigint;index"`
	LastError      string `json:"last_error" gorm:"type:text"`
	BeforeState    string `json:"before_state" gorm:"type:text"`
	AfterState     string `json:"after_state" gorm:"type:text"`
	UpdatedAt      int64  `json:"updated_at" gorm:"bigint;autoUpdateTime:false"`
}

type ProductPublicationRequest struct {
	Plan             SubscriptionPlan `json:"plan"`
	ExpectedRevision int64            `json:"expected_revision"`
	Fields           []string         `json:"fields"`
	Reason           string           `json:"reason"`
	RequestKey       string           `json:"request_key"`
	PreviewHash      string           `json:"preview_hash"`
}

type ProductEntitlementState struct {
	ID                int     `json:"id"`
	UserID            int     `json:"user_id"`
	AmountTotal       int64   `json:"amount_total"`
	AmountUsed        int64   `json:"amount_used"`
	EndTime           int64   `json:"end_time"`
	ModelScope        string  `json:"model_scope"`
	PriceBook         string  `json:"price_book"`
	PriceVersion      string  `json:"price_version"`
	BillingMultiplier float64 `json:"billing_multiplier"`
	AllowedChannels   string  `json:"allowed_channels"`
	MaxUpstreamCost   string  `json:"max_upstream_cost"`
	FallbackAllowed   bool    `json:"fallback_allowed"`
	ResetSnapshot     bool    `json:"reset_policy_snapshot"`
	ResetPeriod       string  `json:"reset_period"`
	ResetSeconds      int64   `json:"reset_seconds"`
	LastResetTime     int64   `json:"last_reset_time"`
	NextResetTime     int64   `json:"next_reset_time"`
	Revision          int64   `json:"revision"`
}

func productEntitlementState(sub UserSubscription) ProductEntitlementState {
	return ProductEntitlementState{
		ID: sub.Id, UserID: sub.UserId, AmountTotal: sub.AmountTotal, AmountUsed: sub.AmountUsed,
		EndTime: sub.EndTime, ModelScope: sub.ModelScope, PriceBook: sub.PriceBook,
		PriceVersion: sub.PriceVersion, BillingMultiplier: sub.BillingMultiplier,
		AllowedChannels: sub.AllowedChannels, MaxUpstreamCost: sub.MaxUpstreamCost,
		FallbackAllowed: sub.FallbackAllowed, ResetSnapshot: sub.ResetPolicySnapshot,
		ResetPeriod: sub.QuotaResetPeriodSnapshot, ResetSeconds: sub.QuotaResetSecondsSnapshot,
		LastResetTime: sub.LastResetTime, NextResetTime: sub.NextResetTime, Revision: sub.ProductRevision,
	}
}

type ProductPublicationChange struct {
	Before  ProductEntitlementState `json:"before"`
	After   ProductEntitlementState `json:"after"`
	Blocker string                  `json:"blocker,omitempty"`
}

type ProductPublicationPreview struct {
	Hash        string                     `json:"hash"`
	APIRevision int64                      `json:"api_revision"`
	Affected    int                        `json:"affected"`
	Blocked     int                        `json:"blocked"`
	BeforePlan  SubscriptionPlan           `json:"before_plan"`
	AfterPlan   SubscriptionPlan           `json:"after_plan"`
	Fields      []string                   `json:"fields"`
	Changes     []ProductPublicationChange `json:"changes"`
}

func productPublicationFields(fields []string) ([]string, error) {
	result := slices.Clone(fields)
	slices.Sort(result)
	for i, field := range result {
		if !slices.Contains([]string{"pricing", "quota", "expiry", "reset"}, field) || i > 0 && result[i-1] == field {
			return nil, errors.New("invalid product publication fields")
		}
	}
	return result, nil
}

// applyProductPublicationTerms never resets or increments usage. Asset changes
// wait for reservations to settle; pricing-only changes preserve in-flight
// request contexts and their immutable price-version snapshots.
func applyProductPublicationTerms(sub *UserSubscription, plan *SubscriptionPlan, fields []string, now int64) error {
	if slices.Contains(fields, "pricing") {
		sub.ModelScope, sub.PriceBook, sub.PriceVersion = plan.ModelScope, plan.PriceBook, plan.PriceVersion
		sub.BillingMultiplier = 1
		sub.AllowedChannels, sub.MaxUpstreamCost = plan.AllowedChannels, plan.MaxUpstreamCost
		sub.FallbackAllowed = plan.FallbackAllowed
	}
	if slices.Contains(fields, "quota") {
		if plan.TotalAmount > 0 && plan.TotalAmount < sub.AmountUsed {
			return errors.New("new quota is below used or reserved quota")
		}
		sub.AmountTotal = plan.TotalAmount
	}
	if slices.Contains(fields, "expiry") {
		end, err := calcPlanEndTime(time.Unix(sub.StartTime, 0), plan)
		if err != nil {
			return err
		}
		if end <= now {
			return errors.New("new duration would already be expired")
		}
		sub.EndTime = end
	}
	if slices.Contains(fields, "reset") {
		sub.ResetPolicySnapshot = true
		sub.QuotaResetPeriodSnapshot, sub.QuotaResetSecondsSnapshot = plan.QuotaResetPeriod, plan.QuotaResetCustomSeconds
	}
	if slices.Contains(fields, "reset") || slices.Contains(fields, "expiry") {
		resetPlan := *plan
		resetPlan.QuotaResetPeriod, resetPlan.QuotaResetCustomSeconds = sub.QuotaResetPeriodSnapshot, sub.QuotaResetSecondsSnapshot
		// Schedule the next new boundary from publication time. No existing use
		// is forgiven or retroactively charged by a reset-policy change.
		base := now
		if !slices.Contains(fields, "reset") {
			base = sub.LastResetTime
			if base <= 0 {
				base = sub.StartTime
			}
		}
		sub.NextResetTime = calcNextResetTime(time.Unix(base, 0), &resetPlan, sub.EndTime)
		if slices.Contains(fields, "reset") {
			sub.LastResetTime = now
		}
	}
	return nil
}

func previewProductPublicationTx(tx *gorm.DB, req ProductPublicationRequest) (*ProductPublicationPreview, []UserSubscription, error) {
	fields, err := productPublicationFields(req.Fields)
	if err != nil {
		return nil, nil, err
	}
	var current SubscriptionPlan
	if err := lockForUpdate(tx).First(&current, req.Plan.Id).Error; err != nil {
		return nil, nil, err
	}
	if req.ExpectedRevision <= 0 || req.ExpectedRevision != current.ConfigRevision || current.PriceBook == "" || req.Plan.PriceBook != current.PriceBook {
		return nil, nil, ErrProductPlanConflict
	}
	candidate := req.Plan
	candidate.CreatedAt, candidate.UpdatedAt = current.CreatedAt, current.UpdatedAt
	candidate.ConfigRevision = current.ConfigRevision + 1
	if candidate.AllowBalancePay == nil {
		candidate.AllowBalancePay = current.AllowBalancePay
	}
	if candidate.AllowWalletOverflow == nil {
		candidate.AllowWalletOverflow = current.AllowWalletOverflow
	}
	if err := candidate.ValidateProductPlan(); err != nil {
		return nil, nil, err
	}
	if candidate.Enabled || candidate.RedemptionEnabled || slices.Contains(fields, "pricing") {
		if err := validateProductPlanPrices(tx, &candidate, true); err != nil {
			return nil, nil, err
		}
	}
	var catalog APIChannelCatalog
	if err := tx.First(&catalog, 1).Error; err != nil {
		return nil, nil, err
	}
	now := getDBTimestampTx(tx)
	var targets []UserSubscription
	if len(fields) > 0 {
		if err := lockForUpdate(tx).Where("plan_id = ? AND status = ? AND end_time > ?", current.Id, "active", now).Order("id").Find(&targets).Error; err != nil {
			return nil, nil, err
		}
	}
	preview := &ProductPublicationPreview{APIRevision: catalog.Revision, BeforePlan: current, AfterPlan: candidate, Fields: fields, Affected: len(targets), Changes: make([]ProductPublicationChange, 0)}
	targetIDs := make([]int, 0, len(targets))
	for _, target := range targets {
		targetIDs = append(targetIDs, target.Id)
		if !target.ResetPolicySnapshot {
			target.ResetPolicySnapshot = true
			target.QuotaResetPeriodSnapshot, target.QuotaResetSecondsSnapshot = current.QuotaResetPeriod, current.QuotaResetCustomSeconds
		}
		change := ProductPublicationChange{Before: productEntitlementState(target)}
		if err := applyProductPublicationTerms(&target, &candidate, fields, now); err != nil {
			change.Blocker = err.Error()
			preview.Blocked++
		}
		change.After = productEntitlementState(target)
		if len(preview.Changes) < 100 {
			preview.Changes = append(preview.Changes, change)
		}
	}
	// Usage may change between preview and publish. It is revalidated under
	// the entitlement lock by the worker, never included in an overwrite.
	hashInput, err := common.Marshal([]any{current, candidate, fields, catalog.Revision, targetIDs})
	if err != nil {
		return nil, nil, err
	}
	preview.Hash = fmt.Sprintf("%x", sha256.Sum256(hashInput))
	return preview, targets, nil
}

func PreviewProductPublication(db *gorm.DB, req ProductPublicationRequest) (*ProductPublicationPreview, error) {
	var preview *ProductPublicationPreview
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		preview, _, err = previewProductPublicationTx(tx, req)
		return err
	})
	return preview, err
}

func PublishProductAndSync(db *gorm.DB, req ProductPublicationRequest, actorID int) (*ProductPublication, error) {
	if actorID <= 0 || strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 2000 || len(req.RequestKey) < 16 || len(req.RequestKey) > 64 || len(req.PreviewHash) != 64 {
		return nil, errors.New("invalid product publication request")
	}
	requestData, err := common.Marshal(req)
	if err != nil {
		return nil, err
	}
	requestHash := fmt.Sprintf("%x", sha256.Sum256(requestData))
	var publication ProductPublication
	err = db.Transaction(func(tx *gorm.DB) error {
		// Catalog -> plan -> entitlement is the publication lock order. Write
		// first so SQLite does not upgrade a stale deferred read transaction.
		fence := tx.Model(&APIChannelCatalog{}).Where("id = ?", 1).UpdateColumn("write_epoch", gorm.Expr("write_epoch + 1"))
		if fence.Error != nil {
			return fence.Error
		}
		if fence.RowsAffected != 1 {
			return errors.New("API channel catalog is unavailable")
		}
		found := tx.Where("request_key = ?", req.RequestKey).Limit(1).Find(&publication)
		if found.Error != nil {
			return found.Error
		}
		if found.RowsAffected > 0 {
			if publication.RequestHash != requestHash || publication.ActorID != actorID {
				return ErrProductPublicationConflict
			}
			return nil
		}
		var current SubscriptionPlan
		if err := lockForUpdate(tx).First(&current, req.Plan.Id).Error; err != nil {
			return err
		}
		activeJobs := tx.Model(&ProductPublication{}).Select("id").Where("plan_id = ?", current.Id)
		var pending int64
		if err := tx.Model(&ProductPublicationItem{}).Where("publication_id IN (?) AND status IN ?", activeJobs, []string{"pending", "retry", "cache_pending"}).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return errors.New("previous product publication is still synchronizing")
		}
		preview, targets, err := previewProductPublicationTx(tx, req)
		if err != nil {
			return err
		}
		if preview.Hash != req.PreviewHash {
			return ErrProductPublicationConflict
		}
		if preview.Blocked > 0 {
			return errors.New("product publication has blocked entitlements; adjust quota, duration or selected fields")
		}
		// Freeze the old reset policy before changing the shared plan. Native
		// resets must not apply new rules to rows still waiting in the outbox.
		if err := tx.Model(&UserSubscription{}).Where("plan_id = ? AND reset_policy_snapshot = ?", current.Id, false).Updates(map[string]any{
			"reset_policy_snapshot": true, "quota_reset_period_snapshot": current.QuotaResetPeriod, "quota_reset_seconds_snapshot": current.QuotaResetCustomSeconds,
		}).Error; err != nil {
			return err
		}
		candidate := preview.AfterPlan
		if err := UpdateProductPlan(tx, &candidate, req.ExpectedRevision); err != nil {
			return err
		}
		if err := tx.Model(&SubscriptionPlan{}).Where("id = ?", current.Id).UpdateColumn("latest_publication_revision", candidate.ConfigRevision).Error; err != nil {
			return err
		}
		before, err := common.Marshal(current)
		if err != nil {
			return err
		}
		after, err := common.Marshal(candidate)
		if err != nil {
			return err
		}
		fields, err := common.Marshal(preview.Fields)
		if err != nil {
			return err
		}
		publication = ProductPublication{RequestKey: req.RequestKey, PlanID: current.Id, Revision: candidate.ConfigRevision,
			ActorID: actorID, Reason: strings.TrimSpace(req.Reason), RequestHash: requestHash,
			Fields: string(fields), BeforePlan: string(before), AfterPlan: string(after), CreatedAt: getDBTimestampTx(tx)}
		if err := tx.Create(&publication).Error; err != nil {
			return err
		}
		if err := tx.Model(&ProductPublicationItem{}).Where("publication_id IN (?) AND status = ?", activeJobs, "blocked").Updates(map[string]any{
			"status": "superseded", "last_error": "superseded by a newer product publication", "updated_at": publication.CreatedAt,
		}).Error; err != nil {
			return err
		}
		items := make([]ProductPublicationItem, 0, len(targets))
		for _, target := range targets {
			items = append(items, ProductPublicationItem{PublicationID: publication.ID, SubscriptionID: target.Id, UserID: target.UserId, Status: "pending", UpdatedAt: publication.CreatedAt})
		}
		if len(items) > 0 {
			return tx.CreateInBatches(&items, 100).Error
		}
		return nil
	})
	if err == nil {
		InvalidateSubscriptionPlanCache(req.Plan.Id)
	}
	return &publication, err
}

// An old paid order can finish after one or more publications. Replay only the
// selected business fields in revision order inside issuance, before a key is
// created. This leaves the order and its cash-price evidence unchanged.
func synchronizeIssuedProductTx(tx *gorm.DB, sub *UserSubscription, latestRevision int64) error {
	var publications []ProductPublication
	if err := lockForUpdate(tx).Where("plan_id = ? AND revision > ? AND revision <= ?", sub.PlanId, sub.ProductRevision, latestRevision).Order("revision").Find(&publications).Error; err != nil {
		return err
	}
	for _, publication := range publications {
		var plan SubscriptionPlan
		var fields []string
		if err := common.UnmarshalJsonStr(publication.AfterPlan, &plan); err != nil {
			return err
		}
		if err := common.UnmarshalJsonStr(publication.Fields, &fields); err != nil {
			return err
		}
		before, err := common.Marshal(productEntitlementState(*sub))
		if err != nil {
			return err
		}
		now := getDBTimestampTx(tx)
		if err := applyProductPublicationTerms(sub, &plan, fields, now); err != nil {
			return err
		}
		sub.ProductRevision = publication.Revision
		after, err := common.Marshal(productEntitlementState(*sub))
		if err != nil {
			return err
		}
		item := ProductPublicationItem{PublicationID: publication.ID, SubscriptionID: sub.Id, UserID: sub.UserId,
			Status: "applied", BeforeState: string(before), AfterState: string(after), UpdatedAt: now}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
	}
	return tx.Save(sub).Error
}

// Sales-only edits can retain the simple PUT endpoint. Changes that can affect
// a sold pool must go through preview/publication even when no users exist yet.
func ProductEntitlementTermsChanged(before, after SubscriptionPlan) bool {
	return before.TotalAmount != after.TotalAmount || before.ModelScope != after.ModelScope || before.PriceVersion != after.PriceVersion ||
		before.DurationUnit != after.DurationUnit || before.DurationValue != after.DurationValue || before.CustomSeconds != after.CustomSeconds ||
		before.QuotaResetPeriod != after.QuotaResetPeriod || before.QuotaResetCustomSeconds != after.QuotaResetCustomSeconds ||
		before.AllowedChannels != after.AllowedChannels || before.MaxUpstreamCost != after.MaxUpstreamCost || before.FallbackAllowed != after.FallbackAllowed
}

// ProcessProductPublicationItem commits SQL once, then retries only cache
// eviction. Authentication reads bound entitlement policy authoritatively, so
// even a Redis outage cannot resurrect the previous price version or scope.
func ProcessProductPublicationItem(ctx context.Context, db *gorm.DB, id int64) error {
	var item ProductPublicationItem
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fence := tx.Model(&ProductPublicationItem{}).Where("id = ? AND status IN ?", id, []string{"pending", "retry", "cache_pending"}).UpdateColumn("attempts", gorm.Expr("attempts + 1"))
		if fence.Error != nil || fence.RowsAffected == 0 {
			return fence.Error
		}
		if err := lockForUpdate(tx).First(&item, id).Error; err != nil {
			return err
		}
		if item.Status == "cache_pending" {
			return nil
		}
		var publication ProductPublication
		if err := tx.First(&publication, item.PublicationID).Error; err != nil {
			return err
		}
		var plan SubscriptionPlan
		var fields []string
		if err := common.UnmarshalJsonStr(publication.AfterPlan, &plan); err != nil {
			return err
		}
		if err := common.UnmarshalJsonStr(publication.Fields, &fields); err != nil {
			return err
		}
		var sub UserSubscription
		lookup := lockForUpdate(tx).Where("id = ? AND user_id = ? AND plan_id = ?", item.SubscriptionID, item.UserID, publication.PlanID).Limit(1).Find(&sub)
		if lookup.Error != nil {
			return lookup.Error
		}
		now := getDBTimestampTx(tx)
		item.UpdatedAt, item.LastError = now, ""
		if lookup.RowsAffected == 0 || sub.Status != "active" || sub.EndTime <= now || sub.ProductRevision >= publication.Revision {
			item.Status, item.LastError = "skipped", "entitlement is no longer eligible or already has this revision"
			return tx.Save(&item).Error
		}
		before, err := common.Marshal(productEntitlementState(sub))
		if err != nil {
			return err
		}
		item.BeforeState = string(before)
		if slices.Contains(fields, "quota") || slices.Contains(fields, "expiry") || slices.Contains(fields, "reset") {
			settled := tx.Model(&BillingRefund{}).Select("request_id").Where("subscription_id = ? AND status IN ?", sub.Id, []string{BillingRefundSettled, BillingRefundSucceeded})
			protected := tx.Model(&BillingRefund{}).Select("request_id").Where("subscription_id = ? AND status NOT IN ?", sub.Id, []string{BillingRefundSettled, BillingRefundSucceeded})
			var inFlight int64
			if err := tx.Model(&SubscriptionPreConsumeRecord{}).Where("user_subscription_id = ? AND status = ?", sub.Id, "consumed").Where("request_id NOT IN (?) OR request_id IN (?)", settled, protected).Count(&inFlight).Error; err != nil {
				return err
			}
			if inFlight > 0 {
				item.Status, item.LastError, item.NextRetryAt = "retry", "waiting for in-flight reservations or refund reconciliation", now+60
				return tx.Save(&item).Error
			}
		}
		if err := applyProductPublicationTerms(&sub, &plan, fields, now); err != nil {
			item.Status, item.LastError = "blocked", err.Error()
			return tx.Save(&item).Error
		}
		sub.ProductRevision = publication.Revision
		if err := tx.Save(&sub).Error; err != nil {
			return err
		}
		if err := tx.Model(&Token{}).Where("user_subscription_id = ? AND user_id = ?", sub.Id, sub.UserId).Updates(map[string]any{
			"expired_time": sub.EndTime, "model_limits_enabled": sub.ModelScope != "", "model_limits": sub.ModelScope,
			"price_book": sub.PriceBook, "price_version": sub.PriceVersion, "billing_multiplier": sub.BillingMultiplier,
			"allowed_channels": sub.AllowedChannels, "max_upstream_cost": sub.MaxUpstreamCost, "fallback_allowed": sub.FallbackAllowed,
		}).Error; err != nil {
			return err
		}
		after, err := common.Marshal(productEntitlementState(sub))
		if err != nil {
			return err
		}
		item.AfterState, item.Status = string(after), "cache_pending"
		return tx.Save(&item).Error
	})
	if err != nil || item.Status != "cache_pending" {
		return err
	}
	var tokens []Token
	if err := db.WithContext(ctx).Select("key").Where("user_subscription_id = ? AND user_id = ?", item.SubscriptionID, item.UserID).Find(&tokens).Error; err != nil {
		return err
	}
	for _, token := range tokens {
		if err := invalidateTokenCacheForMutation(token.Key); err != nil {
			return err
		}
	}
	return db.WithContext(ctx).Model(&ProductPublicationItem{}).Where("id = ? AND status = ?", id, "cache_pending").Updates(map[string]any{"status": "applied", "last_error": "", "updated_at": common.GetTimestamp()}).Error
}

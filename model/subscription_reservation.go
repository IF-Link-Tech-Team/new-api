package model

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrNoActiveSubscription          = errors.New("no active subscription")
	ErrBoundSubscriptionUnavailable  = errors.New("bound subscription not found / not active / expired")
	ErrSubscriptionModelUnsupported  = errors.New("subscription does not support model")
	ErrSubscriptionQuotaInsufficient = errors.New("subscription quota insufficient")
)

// PreConsumeUserSubscription reserves one subscription's quota atomically.
// A bound key can only consume its own pool. The legacy unbound selector keeps
// earliest-expiry ordering, but uses the same validation and accounting path.
// quotaType remains in the signature for compatibility with existing callers.
func PreConsumeUserSubscription(requestId string, userId int, modelName string, quotaType int, amount int64, boundSubscriptionId int) (*SubscriptionPreConsumeResult, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	if strings.TrimSpace(requestId) == "" {
		return nil, errors.New("requestId is empty")
	}
	if amount <= 0 || amount > int64(common.MaxQuota) {
		return nil, errors.New("amount must be > 0 and within the per-request quota limit")
	}
	if boundSubscriptionId < 0 {
		return nil, errors.New("invalid boundSubscriptionId")
	}
	now := GetDBTimestamp()
	var result *SubscriptionPreConsumeResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = findSubscriptionReservationTx(tx, requestId, userId, boundSubscriptionId)
		if err != nil || result != nil {
			return err
		}

		query := lockForUpdate(tx).Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now)
		if boundSubscriptionId > 0 {
			query = query.Where("id = ?", boundSubscriptionId)
		}
		var subs []UserSubscription
		if err := query.Order("end_time asc, id asc").Find(&subs).Error; err != nil {
			return err
		}
		// Another reservation may have committed while the subscription lock was
		// acquired. Re-read before checking remaining quota, even if it is now zero.
		// A locking read also sees the latest record under MySQL REPEATABLE READ.
		result, err = findSubscriptionReservationTx(lockForUpdate(tx), requestId, userId, boundSubscriptionId)
		if err != nil || result != nil {
			return err
		}
		if len(subs) == 0 {
			if boundSubscriptionId > 0 {
				return fmt.Errorf("%w: #%d", ErrBoundSubscriptionUnavailable, boundSubscriptionId)
			}
			return ErrNoActiveSubscription
		}

		for _, sub := range subs {
			if !modelScopeMatchesForProduct(sub.ModelScope, modelName, sub.ProductType) {
				if boundSubscriptionId > 0 {
					return fmt.Errorf("%w: #%d, model=%q", ErrSubscriptionModelUnsupported, sub.Id, modelName)
				}
				continue
			}
			plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
			if err != nil {
				return err
			}
			if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, now); err != nil {
				return err
			}
			if sub.AmountUsed > math.MaxInt64-amount || (sub.AmountTotal > 0 && sub.AmountTotal-sub.AmountUsed < amount) {
				continue
			}
			if sub.DailyQuotaLimit > 0 {
				var dailyUsed int64
				if err := tx.Model(&SubscriptionPreConsumeRecord{}).
					Where("user_subscription_id = ? AND status = ? AND created_at >= ?", sub.Id, "consumed", now-now%86400).
					Select("COALESCE(SUM(pre_consumed),0)").Scan(&dailyUsed).Error; err != nil {
					return fmt.Errorf("查询每日用量失败: %w", err)
				}
				if dailyUsed > sub.DailyQuotaLimit-amount {
					continue
				}
			}
			record := &SubscriptionPreConsumeRecord{
				RequestId: requestId, UserId: userId, UserSubscriptionId: sub.Id,
				PreConsumed: amount, Status: "consumed",
			}
			// The subscription lock serializes reservations for this pool. A
			// request-id collision from an unrelated pool must fail and roll back;
			// querying after a failed INSERT would use an aborted PostgreSQL tx.
			if err := tx.Create(record).Error; err != nil {
				return err
			}
			usedBefore := sub.AmountUsed
			sub.AmountUsed += amount
			if err := tx.Save(&sub).Error; err != nil {
				return err
			}
			result = &SubscriptionPreConsumeResult{
				UserSubscriptionId: sub.Id, PreConsumed: amount, AmountTotal: sub.AmountTotal,
				AmountUsedBefore: usedBefore, AmountUsedAfter: sub.AmountUsed,
			}
			return nil
		}
		return fmt.Errorf("%w, need=%d", ErrSubscriptionQuotaInsufficient, amount)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// findSubscriptionReservationTx only replays the original owner's reservation
// in the original bound pool. Estimates may change on retry; the persisted
// reservation amount remains authoritative until explicit reserve/settle.
func findSubscriptionReservationTx(tx *gorm.DB, requestId string, userId, boundSubscriptionId int) (*SubscriptionPreConsumeResult, error) {
	var record SubscriptionPreConsumeRecord
	// Isolate this query from an initialized FOR UPDATE statement. Reusing that
	// statement for a different model would retain its table and predicates.
	query := tx.Session(&gorm.Session{}).Where("request_id = ?", requestId).Limit(1).Find(&record)
	if query.Error != nil {
		return nil, query.Error
	}
	if query.RowsAffected == 0 {
		return nil, nil
	}
	if record.UserId != userId || (boundSubscriptionId > 0 && record.UserSubscriptionId != boundSubscriptionId) {
		return nil, errors.New("subscription reservation belongs to a different user or bound pool")
	}
	if record.Status == "refunded" {
		return nil, errors.New("subscription pre-consume already refunded")
	}
	var sub UserSubscription
	// The pool was locked before the locking record recheck. This metadata read
	// must not acquire another pool lock after the record (e.g. an expired pool
	// outside the active selection), which would invert the refund lock order.
	if err := tx.Session(&gorm.Session{NewDB: true}).Where("id = ? AND user_id = ?", record.UserSubscriptionId, userId).First(&sub).Error; err != nil {
		return nil, err
	}
	return &SubscriptionPreConsumeResult{
		UserSubscriptionId: sub.Id, PreConsumed: record.PreConsumed, AmountTotal: sub.AmountTotal,
		AmountUsedBefore: sub.AmountUsed, AmountUsedAfter: sub.AmountUsed,
	}, nil
}

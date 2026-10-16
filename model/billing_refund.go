package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	BillingRefundPrepared     = "prepared"
	BillingRefundReserved     = "reserved"
	BillingRefundSettling     = "settling"
	BillingRefundPending      = "pending"
	BillingRefundFailed       = "failed"
	BillingRefundCachePending = "cache_pending"
	BillingRefundSucceeded    = "succeeded"
	BillingRefundSettled      = "settled"
	BillingRefundReview       = "review"
)

// BillingRefund is a durable receipt, not a second balance. Prepared/settling
// receipts are ambiguous after a crash and require review; only explicit refund
// intent authorizes the worker to credit balances. No API key is persisted here.
type BillingRefund struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	RequestID         string `json:"request_id" gorm:"type:varchar(64);index"`
	UserID            int    `json:"user_id" gorm:"index"`
	TokenID           int    `json:"token_id"`
	SubscriptionID    int    `json:"subscription_id"`
	WalletQuota       int64  `json:"wallet_quota" gorm:"type:bigint"`
	SubscriptionQuota int64  `json:"subscription_quota" gorm:"type:bigint"`
	ExtraQuota        int64  `json:"extra_quota" gorm:"type:bigint"`
	TokenQuota        int64  `json:"token_quota" gorm:"type:bigint"`
	Status            string `json:"status" gorm:"type:varchar(32);index"`
	Attempts          int    `json:"attempts"`
	NextRetryAt       int64  `json:"next_retry_at" gorm:"type:bigint;index"`
	LastError         string `json:"last_error" gorm:"type:text"`
	ReviewReason      string `json:"review_reason" gorm:"type:text"`
	ReviewedBy        int    `json:"reviewed_by"`
	CreatedAt         int64  `json:"created_at" gorm:"type:bigint;index"`
	UpdatedAt         int64  `json:"updated_at" gorm:"type:bigint;index"`
}

func CreateBillingRefundReceipt(receipt *BillingRefund) error {
	if receipt.UserID <= 0 {
		return errors.New("invalid refund owner")
	}
	receipt.Status = BillingRefundPrepared
	receipt.CreatedAt, receipt.UpdatedAt = common.GetTimestamp(), common.GetTimestamp()
	return DB.Create(receipt).Error
}

func SaveBillingRefundReservation(receipt *BillingRefund) error {
	result := DB.Model(&BillingRefund{}).Where("id = ? AND status IN ?", receipt.ID, []string{BillingRefundPrepared, BillingRefundReserved}).Updates(map[string]any{
		"subscription_id": receipt.SubscriptionID, "wallet_quota": receipt.WalletQuota,
		"subscription_quota": receipt.SubscriptionQuota, "extra_quota": receipt.ExtraQuota,
		"token_quota": receipt.TokenQuota, "status": BillingRefundReserved, "updated_at": common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("refund receipt is not reservable")
	}
	return nil
}

// TransitionBillingRefund serializes session transitions against durable refund
// intent. Call before changing any funds, never while holding a balance lock.
func TransitionBillingRefund(id int64, from []string, to string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var receipt BillingRefund
		if err := lockForUpdate(tx).Where("id = ? AND status IN ?", id, from).First(&receipt).Error; err != nil {
			return err
		}
		if receipt.Status == to {
			return nil
		}
		return tx.Model(&receipt).Updates(map[string]any{"status": to, "updated_at": common.GetTimestamp()}).Error
	})
}

func QueueBillingRefund(id int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var receipt BillingRefund
		if err := lockForUpdate(tx).First(&receipt, id).Error; err != nil {
			return err
		}
		switch receipt.Status {
		case BillingRefundPending, BillingRefundFailed, BillingRefundCachePending, BillingRefundSucceeded:
			return nil
		case BillingRefundReserved:
			return tx.Model(&receipt).Updates(map[string]any{"status": BillingRefundPending, "next_retry_at": 0, "updated_at": common.GetTimestamp()}).Error
		default:
			return errors.New("receipt requires reconciliation before refund")
		}
	})
}

func FinishBillingRefundSettlement(id int64, failure string) error {
	status := BillingRefundSettled
	if failure != "" {
		status = BillingRefundReview
	}
	result := DB.Model(&BillingRefund{}).Where("id = ? AND status = ?", id, BillingRefundSettling).Updates(map[string]any{"status": status, "last_error": failure, "updated_at": common.GetTimestamp()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("billing settlement receipt state conflict")
	}
	return nil
}

// ProcessBillingRefund commits all SQL credits and their idempotency marker in
// one transaction. Cache invalidation is an idempotent second phase: retries of
// cache_pending never credit funds again, including after an ambiguous COMMIT.
func ProcessBillingRefund(id int64) error {
	var receipt BillingRefund
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&receipt, id).Error; err != nil {
			return err
		}
		if receipt.Status == BillingRefundSucceeded || receipt.Status == BillingRefundCachePending {
			return nil
		}
		if receipt.Status != BillingRefundPending && receipt.Status != BillingRefundFailed {
			return errors.New("no durable refund intent")
		}
		if receipt.WalletQuota < 0 || receipt.SubscriptionQuota < 0 || receipt.ExtraQuota < 0 || receipt.TokenQuota < 0 || receipt.WalletQuota > int64(common.MaxQuota) || receipt.TokenQuota > int64(common.MaxQuota) || receipt.SubscriptionQuota > int64(common.MaxQuota) || receipt.ExtraQuota > int64(common.MaxQuota) {
			return errors.New("invalid refund amounts")
		}
		if (receipt.WalletQuota > 0 && receipt.SubscriptionQuota > 0) || (receipt.ExtraQuota > 0 && receipt.SubscriptionQuota == 0) {
			return errors.New("invalid refund funding source")
		}
		if receipt.SubscriptionQuota > 0 {
			var record SubscriptionPreConsumeRecord
			if err := tx.Where("request_id = ?", receipt.RequestID).First(&record).Error; err != nil {
				return err
			}
			if record.UserId != receipt.UserID || record.UserSubscriptionId != receipt.SubscriptionID || record.PreConsumed != receipt.SubscriptionQuota {
				return errors.New("refund reservation ownership/amount mismatch")
			}
			if err := refundSubscriptionPreConsumeTx(tx, receipt.RequestID); err != nil {
				return err
			}
		}
		if receipt.ExtraQuota > 0 {
			if err := postConsumeUserSubscriptionDeltaTx(tx, receipt.SubscriptionID, -receipt.ExtraQuota); err != nil {
				return err
			}
		}
		if receipt.WalletQuota > 0 {
			result := tx.Unscoped().Model(&User{}).Where("id = ? AND quota <= ?", receipt.UserID, int64(common.MaxWalletQuota)-receipt.WalletQuota).Update("quota", gorm.Expr("quota + ?", receipt.WalletQuota))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("refund wallet missing or quota overflow")
			}
		}
		if receipt.TokenQuota > 0 {
			result := tx.Unscoped().Model(&Token{}).Where("id = ? AND user_id = ? AND remain_quota <= ?", receipt.TokenID, receipt.UserID, int64(common.MaxWalletQuota)-receipt.TokenQuota).Updates(map[string]any{
				"remain_quota": gorm.Expr("remain_quota + ?", receipt.TokenQuota), "used_quota": gorm.Expr("used_quota - ?", receipt.TokenQuota), "accessed_time": common.GetTimestamp(),
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("refund token missing, owner mismatch or quota overflow")
			}
		}
		if err := tx.Model(&receipt).Updates(map[string]any{"status": BillingRefundCachePending, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		receipt.Status = BillingRefundCachePending
		return nil
	})
	if err != nil {
		return err
	}
	if receipt.Status == BillingRefundSucceeded {
		return nil
	}
	if err := invalidateUserCache(receipt.UserID); err != nil {
		return err
	}
	if common.RedisEnabled && receipt.TokenID > 0 {
		var token Token
		if err := DB.Unscoped().Select("key").First(&token, receipt.TokenID).Error; err != nil {
			return err
		}
		if err := invalidateTokenCacheForMutation(token.Key); err != nil {
			return err
		}
	}
	return DB.Model(&BillingRefund{}).Where("id = ? AND status = ?", id, BillingRefundCachePending).Updates(map[string]any{
		"status": BillingRefundSucceeded, "last_error": "", "next_retry_at": 0, "updated_at": common.GetTimestamp(),
	}).Error
}

func RecordBillingRefundFailure(id int64, failure error) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var receipt BillingRefund
		if err := lockForUpdate(tx).First(&receipt, id).Error; err != nil {
			return err
		}
		if receipt.Status != BillingRefundPending && receipt.Status != BillingRefundFailed && receipt.Status != BillingRefundCachePending {
			return nil
		}
		status := BillingRefundFailed
		if receipt.Status == BillingRefundCachePending {
			status = BillingRefundCachePending
		}
		delay := int64(60) << min(receipt.Attempts, 6)
		if delay > 3600 {
			delay = 3600
		}
		return tx.Model(&receipt).Updates(map[string]any{"status": status, "attempts": receipt.Attempts + 1,
			"next_retry_at": common.GetTimestamp() + delay, "last_error": failure.Error(), "updated_at": common.GetTimestamp(),
		}).Error
	})
}

func FindDueBillingRefunds(now int64, limit int) ([]BillingRefund, error) {
	var receipts []BillingRefund
	err := DB.Where("status IN ? AND next_retry_at <= ?", []string{BillingRefundPending, BillingRefundFailed, BillingRefundCachePending}, now).Order("next_retry_at asc, id asc").Limit(limit).Find(&receipts).Error
	return receipts, err
}

func ListBillingRefunds(status string, limit, offset int) ([]BillingRefund, int64, error) {
	query := DB.Model(&BillingRefund{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var receipts []BillingRefund
	err := query.Order("id desc").Limit(limit).Offset(offset).Find(&receipts).Error
	return receipts, total, err
}

// ReviewBillingRefund is an explicit, audited operator decision for receipts
// that cannot safely be inferred after a crash. Never auto-refund old consumed
// reservations or assume an interrupted pre-consume/settlement was committed.
func ReviewBillingRefund(id int64, reviewer int, action, reason string) error {
	if reviewer <= 0 || len(reason) < 8 || len(reason) > 1000 {
		return errors.New("reviewer and an 8-1000 byte reconciliation reason required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var receipt BillingRefund
		if err := lockForUpdate(tx).First(&receipt, id).Error; err != nil {
			return err
		}
		status := BillingRefundPending
		switch action {
		case "retry":
			if receipt.Status != BillingRefundFailed && receipt.Status != BillingRefundPending && receipt.Status != BillingRefundCachePending {
				return errors.New("only queued refunds may be retried")
			}
			status = receipt.Status
		case "confirm_refund", "confirm_settled":
			if receipt.Status != BillingRefundPrepared && receipt.Status != BillingRefundReserved && receipt.Status != BillingRefundSettling && receipt.Status != BillingRefundReview {
				return errors.New("receipt is not awaiting reconciliation")
			}
			if action == "confirm_refund" && receipt.Status != BillingRefundReserved {
				return errors.New("ambiguous debits/settlements require independent balance reconciliation before confirm_settled")
			}
			if action == "confirm_settled" {
				status = BillingRefundSettled
			}
		default:
			return errors.New("invalid reconciliation action")
		}
		return tx.Model(&receipt).Updates(map[string]any{"status": status, "reviewed_by": reviewer, "review_reason": reason, "next_retry_at": 0, "updated_at": common.GetTimestamp()}).Error
	})
}

func CountBillingRefundAlerts(now int64) (int64, error) {
	var count int64
	err := DB.Model(&BillingRefund{}).Where("(status IN ? AND attempts >= 3) OR (status IN ? AND updated_at < ?) OR status = ?",
		[]string{BillingRefundFailed, BillingRefundCachePending}, []string{BillingRefundPrepared, BillingRefundReserved, BillingRefundSettling}, now-6*3600, BillingRefundReview).Count(&count).Error
	return count, err
}

func CountPendingBillingRefunds() (int64, error) {
	var count int64
	err := DB.Model(&BillingRefund{}).Where("status IN ?", []string{BillingRefundPending, BillingRefundFailed, BillingRefundCachePending}).Count(&count).Error
	return count, err
}

func CleanupBillingRefunds(olderThanSeconds int64) (int64, error) {
	if olderThanSeconds < 30*24*3600 {
		return 0, fmt.Errorf("refund receipts require at least 30 days retention")
	}
	consumed := DB.Model(&SubscriptionPreConsumeRecord{}).Select("request_id").Where("status = ?", "consumed")
	result := DB.Where("status IN ? AND updated_at < ? AND (subscription_id = 0 OR request_id NOT IN (?))", []string{BillingRefundSucceeded, BillingRefundSettled}, common.GetTimestamp()-olderThanSeconds, consumed).Delete(&BillingRefund{})
	return result.RowsAffected, result.Error
}

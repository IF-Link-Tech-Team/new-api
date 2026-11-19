package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTaskIdempotencyConflict      = errors.New("idempotency key was already used with a different request")
	ErrTaskIdempotencyBusy          = errors.New("a submission with this idempotency key is still in progress")
	ErrTaskIdempotencyLease         = errors.New("task submission lease was lost")
	ErrTaskIdempotencyBillingReview = errors.New("the previous billing reservation requires reconciliation")
)

const TaskIdempotencyLeaseTTL = 2 * time.Minute

// TaskIdempotency retains the order identity even after an ambiguous upstream
// failure. Neither the client order key, request body nor credentials are stored.
type TaskIdempotency struct {
	ScopeKey         string `gorm:"type:varchar(64);primaryKey"`
	RequestHash      string `gorm:"type:varchar(64);not null"`
	RequestSignature string `gorm:"type:varchar(64);not null"`
	PublicTaskID     string `gorm:"type:varchar(191);not null"`
	PluginKey        string `gorm:"type:varchar(30);not null"`
	ChannelID        int
	ChannelKeyHash   string `gorm:"type:varchar(64);not null"`
	BaseURL          string `gorm:"type:varchar(191);not null"`
	UpstreamModel    string `gorm:"type:varchar(191);not null"`
	Group            string `gorm:"type:varchar(50);not null"`
	UserID           int
	TaskRowID        int64
	BillingRequestID string `gorm:"type:varchar(64);not null"`
	LeaseOwner       string `gorm:"type:varchar(64);not null"`
	LeaseUntil       int64
	CreatedAt        int64
}

func TaskIdempotencyScope(userID, tokenID int, key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", userID, tokenID, key))))
}

func TaskIdempotencyDigest(value []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(value))
}

func GetTaskIdempotency(ctx context.Context, scope string) (*TaskIdempotency, error) {
	var row TaskIdempotency
	result := DB.WithContext(ctx).Where("scope_key = ?", scope).Limit(1).Find(&row)
	if result.RowsAffected == 0 {
		return nil, result.Error
	}
	return &row, result.Error
}

func ClaimTaskIdempotency(ctx context.Context, candidate TaskIdempotency, owner string) (*TaskIdempotency, error) {
	var row TaskIdempotency
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		candidate.CreatedAt = time.Now().Unix()
		// Always attempt the insert first: besides the unique constraint, this
		// obtains SQLite's write lock before the read/claim transaction.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("scope_key = ?", candidate.ScopeKey).First(&row).Error; err != nil {
			return err
		}
		if row.RequestHash != candidate.RequestHash {
			return ErrTaskIdempotencyConflict
		}
		if row.TaskRowID != 0 {
			return nil
		}
		if row.PluginKey != candidate.PluginKey || row.ChannelID != candidate.ChannelID || row.ChannelKeyHash != candidate.ChannelKeyHash || row.BaseURL != candidate.BaseURL || row.UpstreamModel != candidate.UpstreamModel {
			return ErrTaskIdempotencyConflict
		}
		if row.LeaseUntil >= time.Now().Unix() && row.LeaseOwner != owner {
			return ErrTaskIdempotencyBusy
		}
		row.LeaseOwner, row.LeaseUntil = owner, time.Now().Add(TaskIdempotencyLeaseTTL).Unix()
		return tx.Model(&TaskIdempotency{}).Where("scope_key = ?", row.ScopeKey).
			Updates(map[string]any{"lease_owner": row.LeaseOwner, "lease_until": row.LeaseUntil}).Error
	})
	return &row, err
}

func RenewTaskIdempotency(ctx context.Context, scope, owner string) error {
	now := time.Now().Unix()
	query := DB.WithContext(ctx).Model(&TaskIdempotency{}).
		Where("scope_key = ? AND lease_owner = ? AND lease_until >= ?", scope, owner, now)
	result := query.Update("lease_until", time.Now().Add(TaskIdempotencyLeaseTTL).Unix())
	if result.Error != nil {
		return result.Error
	}
	// MySQL may report zero changed rows when a renewal lands in the same second.
	var held int64
	if err := query.Count(&held).Error; err != nil {
		return err
	}
	if held == 0 {
		return ErrTaskIdempotencyLease
	}
	return nil
}

func ReleaseTaskIdempotency(ctx context.Context, scope, owner string) error {
	return DB.WithContext(ctx).Model(&TaskIdempotency{}).Where("scope_key = ? AND lease_owner = ?", scope, owner).
		Updates(map[string]any{"lease_owner": "", "lease_until": 0}).Error
}

func VerifyTaskIdempotencyRequest(ctx context.Context, scope, owner, signature string) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row TaskIdempotency
		if err := lockForUpdate(tx).Where("scope_key = ?", scope).First(&row).Error; err != nil {
			return err
		}
		if row.LeaseOwner != owner || row.LeaseUntil < time.Now().Unix() || row.TaskRowID != 0 {
			return ErrTaskIdempotencyLease
		}
		if row.RequestSignature != "" && row.RequestSignature != signature {
			return ErrTaskIdempotencyConflict
		}
		return tx.Model(&TaskIdempotency{}).Where("scope_key = ?", scope).Update("request_signature", signature).Error
	})
}

// The new lease fences the old worker from publishing a task. Known complete
// reservations can therefore be refunded through the existing durable refund
// path before charging the retry. Partial/settling receipts stay review-gated.
func RecoverTaskOrderReservations(ctx context.Context, order *TaskIdempotency, owner string, tokenID int) error {
	if err := RenewTaskIdempotency(ctx, order.ScopeKey, owner); err != nil {
		return err
	}
	if order.BillingRequestID == "" {
		return nil
	}
	var receipts []BillingRefund
	if err := DB.WithContext(ctx).Where("request_id = ? AND user_id = ? AND token_id = ? AND status NOT IN ?",
		order.BillingRequestID, order.UserID, tokenID, []string{BillingRefundSettled, BillingRefundSucceeded}).Find(&receipts).Error; err != nil {
		return err
	}
	for _, receipt := range receipts {
		switch receipt.Status {
		case BillingRefundReserved, BillingRefundPending, BillingRefundFailed, BillingRefundCachePending:
			if err := RenewTaskIdempotency(ctx, order.ScopeKey, owner); err != nil {
				return err
			}
			if err := QueueBillingRefund(receipt.ID); err != nil {
				return err
			}
			if err := ProcessBillingRefund(receipt.ID); err != nil {
				return err
			}
		default:
			return ErrTaskIdempotencyBillingReview
		}
	}
	return nil
}

func StartTaskOrderBillingAttempt(ctx context.Context, order *TaskIdempotency, owner string) error {
	result := DB.WithContext(ctx).Model(&TaskIdempotency{}).
		Where("scope_key = ? AND lease_owner = ? AND lease_until >= ?", order.ScopeKey, owner, time.Now().Unix()).
		Update("billing_request_id", owner)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTaskIdempotencyLease
	}
	order.BillingRequestID = owner
	return nil
}

func TaskIdempotencyResult(ctx context.Context, row *TaskIdempotency) (*Task, error) {
	var task Task
	if err := DB.WithContext(ctx).Where("id = ? AND user_id = ?", row.TaskRowID, row.UserID).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

func TaskIdempotencyChannelAllowed(ctx context.Context, row *TaskIdempotency, modelName string) (bool, error) {
	var count int64
	err := DB.WithContext(ctx).Model(&Ability{}).Where(&Ability{Group: row.Group, Model: modelName, ChannelId: row.ChannelID, Enabled: true}).Count(&count).Error
	return count > 0, err
}

// InsertTaskIdempotent fences expired workers and publishes the task and order
// mapping atomically. A replay never inserts a second task or settles twice.
func InsertTaskIdempotent(ctx context.Context, task *Task, scope, owner string, omitColumns ...string) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row TaskIdempotency
		if err := lockForUpdate(tx).Where("scope_key = ?", scope).First(&row).Error; err != nil {
			return err
		}
		if row.LeaseOwner != owner || row.LeaseUntil < time.Now().Unix() || row.TaskRowID != 0 || row.PublicTaskID != task.TaskID || row.UserID != task.UserId {
			return ErrTaskIdempotencyLease
		}
		if err := tx.Omit(omitColumns...).Create(task).Error; err != nil {
			return err
		}
		return tx.Model(&TaskIdempotency{}).Where("scope_key = ?", scope).Update("task_row_id", task.ID).Error
	})
}

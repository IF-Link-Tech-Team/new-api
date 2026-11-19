package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrGroupWalletInsufficient = errors.New("Group wallet allocation is insufficient")
var ErrGroupWalletUnavailable = errors.New("Group wallet is unavailable for this member")

const (
	GroupWalletPrepaid     = "prepaid"
	GroupWalletIndependent = "independent"
)

// Quota is the unallocated pool. Member allocations remain in this wallet,
// never in User.Quota, and can only fund requests made through GroupName.
type GroupWallet struct {
	ID        int    `json:"id"`
	GroupName string `json:"group_name" gorm:"size:64;uniqueIndex"`
	Mode      string `json:"mode" gorm:"size:16"`
	Enabled   bool   `json:"enabled"`
	Activated bool   `json:"activated"`
	Quota     int64  `json:"quota" gorm:"type:bigint"`
}

func (wallet *GroupWallet) IsIndependent() bool {
	return wallet != nil && wallet.Mode == GroupWalletIndependent
}

func IndependentGroupNames() ([]string, error) {
	var groups []string
	err := DB.Model(&GroupWallet{}).Where("mode = ?", GroupWalletIndependent).Pluck("group_name", &groups).Error
	return groups, err
}

func GroupWalletHasActivity(tx *gorm.DB, wallet *GroupWallet) (bool, error) {
	if wallet.Quota != 0 {
		return true, nil
	}
	var count int64
	if err := tx.Model(&GroupWalletEntry{}).Where("wallet_id = ?", wallet.ID).Count(&count).Error; err != nil || count > 0 {
		return count > 0, err
	}
	err := tx.Model(&GroupWalletCharge{}).Where("wallet_id = ?", wallet.ID).Count(&count).Error
	return count > 0, err
}

type GroupWalletMember struct {
	ID        int   `json:"id"`
	WalletID  int   `json:"wallet_id" gorm:"uniqueIndex:idx_group_wallet_member,priority:1"`
	UserID    int   `json:"user_id" gorm:"uniqueIndex:idx_group_wallet_member,priority:2"`
	Quota     int64 `json:"quota" gorm:"type:bigint"`
	UsedQuota int64 `json:"used_quota" gorm:"type:bigint"`
}

type GroupWalletEntry struct {
	ID          int64   `json:"id"`
	WalletID    int     `json:"wallet_id" gorm:"index"`
	UserID      int     `json:"user_id"`
	ActorID     int     `json:"actor_id"`
	Kind        string  `json:"kind" gorm:"size:32"`
	Delta       int64   `json:"delta" gorm:"type:bigint"`
	PoolAfter   int64   `json:"pool_after" gorm:"type:bigint"`
	MemberAfter int64   `json:"member_after" gorm:"type:bigint"`
	RequestID   string  `json:"request_id" gorm:"size:191;index"`
	OperationID *string `json:"operation_id,omitempty" gorm:"size:64;uniqueIndex"`
	Reason      string  `json:"reason" gorm:"size:500"`
	CreatedAt   int64   `json:"created_at" gorm:"autoCreateTime"`
}

// A request owns one durable charge. Absolute targets make reservation,
// settlement and task polling safe to repeat after a lost database response.
type GroupWalletCharge struct {
	ID        int64  `json:"id"`
	RequestID string `json:"request_id" gorm:"size:191;uniqueIndex"`
	WalletID  int    `json:"wallet_id" gorm:"index"`
	UserID    int    `json:"user_id"`
	Quota     int64  `json:"quota" gorm:"type:bigint"`
	Refunded  bool   `json:"refunded"`
}

func FindGroupWallet(group string) (*GroupWallet, error) {
	if group == "" {
		return nil, nil
	}
	var wallet GroupWallet
	err := DB.Where("group_name = ?", group).First(&wallet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &wallet, err
}

func LockGroupWallet(tx *gorm.DB, id int) (*GroupWallet, error) {
	var wallet GroupWallet
	err := lockForUpdate(tx).First(&wallet, id).Error
	return &wallet, err
}

func EnsureGroupWallet(tx *gorm.DB, group string) (*GroupWallet, error) {
	wallet := GroupWallet{GroupName: group}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&wallet).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).Where("group_name = ?", group).First(&wallet).Error; err != nil {
		return nil, err
	}
	return &wallet, nil
}

func AdjustGroupWalletPool(tx *gorm.DB, wallet *GroupWallet, actorID int, delta int64, reason, operationID string) error {
	if done, err := groupWalletTransferExists(tx, wallet.ID, actorID, 0, delta, "fund", operationID); done || err != nil {
		return err
	}
	if delta == 0 || delta > int64(common.MaxWalletQuota) || delta < -int64(common.MaxWalletQuota) || wallet.Quota+delta < 0 || wallet.Quota+delta > int64(common.MaxWalletQuota) {
		return errors.New("Invalid group wallet adjustment")
	}
	var allocated int64
	if err := tx.Model(&GroupWalletMember{}).Where("wallet_id = ?", wallet.ID).Select("COALESCE(SUM(quota), 0)").Scan(&allocated).Error; err != nil {
		return err
	}
	if wallet.Quota+allocated+delta > int64(common.MaxWalletQuota) {
		return errors.New("group wallet quota limit exceeded")
	}
	wallet.Quota += delta
	if err := tx.Model(wallet).Update("quota", wallet.Quota).Error; err != nil {
		return err
	}
	return tx.Create(&GroupWalletEntry{WalletID: wallet.ID, ActorID: actorID, Kind: "fund", Delta: delta, PoolAfter: wallet.Quota, Reason: reason, OperationID: &operationID}).Error
}

func AllocateGroupWallet(tx *gorm.DB, wallet *GroupWallet, actorID, userID int, delta int64, reason, operationID string) error {
	if done, err := groupWalletTransferExists(tx, wallet.ID, actorID, userID, delta, "allocate", operationID); done || err != nil {
		return err
	}
	if delta == 0 || delta > int64(common.MaxWalletQuota) || delta < -int64(common.MaxWalletQuota) {
		return errors.New("Invalid group wallet adjustment")
	}
	member := GroupWalletMember{WalletID: wallet.ID, UserID: userID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&member).Error; err != nil {
		return err
	}
	if err := lockForUpdate(tx).Where("wallet_id = ? AND user_id = ?", wallet.ID, userID).First(&member).Error; err != nil {
		return err
	}
	if wallet.Quota-delta < 0 || member.Quota+delta < 0 || wallet.Quota-delta > int64(common.MaxWalletQuota) || member.Quota+delta > int64(common.MaxWalletQuota) {
		return ErrGroupWalletInsufficient
	}
	wallet.Quota -= delta
	member.Quota += delta
	if err := tx.Model(wallet).Update("quota", wallet.Quota).Error; err != nil {
		return err
	}
	if err := tx.Model(&member).Update("quota", member.Quota).Error; err != nil {
		return err
	}
	return tx.Create(&GroupWalletEntry{WalletID: wallet.ID, UserID: userID, ActorID: actorID, Kind: "allocate", Delta: delta, PoolAfter: wallet.Quota, MemberAfter: member.Quota, Reason: reason, OperationID: &operationID}).Error
}

func groupWalletTransferExists(tx *gorm.DB, walletID, actorID, userID int, delta int64, kind, operationID string) (bool, error) {
	if operationID == "" || len(operationID) > 64 {
		return false, errors.New("Invalid group wallet operation ID")
	}
	var entry GroupWalletEntry
	err := tx.Where("operation_id = ?", operationID).First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if entry.WalletID != walletID || entry.ActorID != actorID || entry.UserID != userID || entry.Delta != delta || entry.Kind != kind {
		return false, errors.New("Group wallet operation ID was reused with different details")
	}
	return true, nil
}

// Reserve checks current membership, enabled state and allocation under the
// same lock ordering as membership management (user, wallet, allocation).
func ReserveGroupWallet(walletID, userID int, requestID string, target int) error {
	if target < 0 || target > common.MaxQuota || requestID == "" || len(requestID) > 191 {
		return errors.New("Invalid group wallet charge")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		user, err := LockGroupUser(tx, userID)
		if err != nil {
			return err
		}
		wallet, err := LockGroupWallet(tx, walletID)
		if err != nil {
			return err
		}
		if !wallet.Enabled || user.Status != common.UserStatusEnabled || user.Group != wallet.GroupName {
			return ErrGroupWalletUnavailable
		}
		var charge GroupWalletCharge
		err = tx.Where("request_id = ?", requestID).First(&charge).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			charge = GroupWalletCharge{WalletID: walletID, UserID: userID, RequestID: requestID}
			if err := tx.Create(&charge).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		return changeGroupWalletCharge(tx, wallet, &charge, userID, int64(target), true, "reserve")
	})
}

func SettleGroupWallet(walletID, userID int, requestID string, target int) error {
	if target < 0 || target > common.MaxQuota {
		return errors.New("Invalid group wallet charge")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		wallet, err := LockGroupWallet(tx, walletID)
		if err != nil {
			return err
		}
		var charge GroupWalletCharge
		if err := tx.Where("request_id = ?", requestID).First(&charge).Error; err != nil {
			return err
		}
		// Already-running requests settle actual usage even after pause/removal.
		// Any overrun stays on this allocation; it never spills to another fund.
		return changeGroupWalletCharge(tx, wallet, &charge, userID, int64(target), false, "settle")
	})
}

func RefundGroupWalletTx(tx *gorm.DB, walletID, userID int, requestID string) error {
	wallet, err := LockGroupWallet(tx, walletID)
	if err != nil {
		return err
	}
	var charge GroupWalletCharge
	if err := tx.Where("request_id = ?", requestID).First(&charge).Error; err != nil {
		return err
	}
	if charge.Refunded {
		if charge.WalletID != walletID || charge.UserID != userID {
			return ErrGroupWalletUnavailable
		}
		return nil
	}
	if err := changeGroupWalletCharge(tx, wallet, &charge, userID, 0, false, "refund"); err != nil {
		return err
	}
	return tx.Model(&charge).Update("refunded", true).Error
}

func RefundGroupWallet(walletID, userID int, requestID string) error {
	return DB.Transaction(func(tx *gorm.DB) error { return RefundGroupWalletTx(tx, walletID, userID, requestID) })
}

func changeGroupWalletCharge(tx *gorm.DB, wallet *GroupWallet, charge *GroupWalletCharge, userID int, target int64, reserve bool, kind string) error {
	if charge.WalletID != wallet.ID || charge.UserID != userID || charge.Refunded {
		return ErrGroupWalletUnavailable
	}
	var member GroupWalletMember
	if err := lockForUpdate(tx).Where("wallet_id = ? AND user_id = ?", wallet.ID, userID).First(&member).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) || target != 0 {
			return ErrGroupWalletInsufficient
		}
		member = GroupWalletMember{WalletID: wallet.ID, UserID: userID}
		if err := tx.Create(&member).Error; err != nil {
			return err
		}
	}
	delta := target - charge.Quota
	if reserve && (member.Quota < delta || member.Quota <= 0 && delta > 0) {
		return ErrGroupWalletInsufficient
	}
	if member.Quota-delta > int64(common.MaxWalletQuota) || member.Quota-delta < -int64(common.MaxWalletQuota) || member.UsedQuota+delta > int64(common.MaxWalletQuota) || member.UsedQuota+delta < 0 {
		return fmt.Errorf("group wallet quota limit exceeded")
	}
	if delta == 0 {
		return nil
	}
	member.Quota -= delta
	member.UsedQuota += delta
	if err := tx.Model(&member).Updates(map[string]any{"quota": member.Quota, "used_quota": member.UsedQuota}).Error; err != nil {
		return err
	}
	charge.Quota = target
	if err := tx.Model(charge).Update("quota", target).Error; err != nil {
		return err
	}
	return tx.Create(&GroupWalletEntry{WalletID: wallet.ID, UserID: userID, Kind: kind, Delta: -delta, PoolAfter: wallet.Quota, MemberAfter: member.Quota, RequestID: charge.RequestID}).Error
}

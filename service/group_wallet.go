package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GroupWalletMemberView struct {
	UserID      int    `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Active      bool   `json:"active"`
	Quota       int64  `json:"quota"`
	UsedQuota   int64  `json:"used_quota"`
}

type GroupWalletView struct {
	Wallet        model.GroupWallet        `json:"wallet"`
	Allocated     int64                    `json:"allocated"`
	Used          int64                    `json:"used"`
	Members       []GroupWalletMemberView  `json:"members"`
	Total         int64                    `json:"total"`
	Entries       []model.GroupWalletEntry `json:"entries"`
	CanChangeMode bool                     `json:"can_change_mode"`
}

func GetGroupWallet(actorID int, group string, page int) (GroupWalletView, error) {
	view := GroupWalletView{Wallet: model.GroupWallet{GroupName: group}, Members: []GroupWalletMemberView{}, Entries: []model.GroupWalletEntry{}}
	err := withGroupAccess(actorID, group, authz.GroupWalletRead, func(tx *gorm.DB, actor *model.User) error {
		err := tx.Where("group_name = ?", group).First(&view.Wallet).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			view.Wallet = model.GroupWallet{GroupName: group}
		} else if err != nil {
			return err
		}
		if view.Wallet.Mode == "" {
			view.Wallet.Mode = model.GroupWalletPrepaid
		}
		if actor.Role == common.RoleRootUser {
			active, err := model.GroupWalletHasActivity(tx, &view.Wallet)
			if err != nil {
				return err
			}
			view.CanChangeMode = !active
		}
		var totals struct{ Quota, Used int64 }
		if err := tx.Model(&model.GroupWalletMember{}).Select("COALESCE(SUM(quota), 0) AS quota, COALESCE(SUM(used_quota), 0) AS used").Where("wallet_id = ?", view.Wallet.ID).Scan(&totals).Error; err != nil {
			return err
		}
		view.Allocated, view.Used = totals.Quota, totals.Used
		// Retain former members with balances so managers can reclaim unused funds.
		allocated := tx.Model(&model.GroupWalletMember{}).Select("user_id").Where("wallet_id = ?", view.Wallet.ID)
		users := tx.Unscoped().Model(&model.User{}).Where(tx.Where("id IN (?)", allocated).Or(tx.Where(clause.Eq{Column: "group", Value: group}).Where("role = ? AND deleted_at IS NULL", common.RoleCommonUser)))
		if err := users.Count(&view.Total).Error; err != nil {
			return err
		}
		var rows []model.User
		if err := users.Select("id", "username", "display_name", "status", "group", "deleted_at").Order("id").Limit(50).Offset((max(page, 1) - 1) * 50).Find(&rows).Error; err != nil {
			return err
		}
		var balances []model.GroupWalletMember
		if err := tx.Where("wallet_id = ?", view.Wallet.ID).Find(&balances).Error; err != nil {
			return err
		}
		byUser := make(map[int]model.GroupWalletMember, len(balances))
		for _, balance := range balances {
			byUser[balance.UserID] = balance
		}
		for _, user := range rows {
			balance := byUser[user.Id]
			view.Members = append(view.Members, GroupWalletMemberView{UserID: user.Id, Username: user.Username, DisplayName: user.DisplayName, Active: !user.DeletedAt.Valid && user.Group == group && user.Status == common.UserStatusEnabled, Quota: balance.Quota, UsedQuota: balance.UsedQuota})
		}
		return tx.Where("wallet_id = ?", view.Wallet.ID).Order("id DESC").Limit(30).Find(&view.Entries).Error
	})
	return view, err
}

// A group type cannot convert an existing ledger between prepaid money and
// independently issued credits. Configure it before the first balance activity.
func SetGroupWalletMode(actorID int, group, mode string) error {
	if mode != model.GroupWalletPrepaid && mode != model.GroupWalletIndependent {
		return errors.New("Invalid group wallet type")
	}
	return withGroupAccess(actorID, group, authz.GroupWalletRead, func(tx *gorm.DB, actor *model.User) error {
		if actor.Role != common.RoleRootUser {
			return authz.ErrGroupAccess
		}
		wallet, err := model.EnsureGroupWallet(tx, group)
		if err != nil {
			return err
		}
		if wallet.Mode == mode || wallet.Mode == "" && mode == model.GroupWalletPrepaid {
			return nil
		}
		active, err := model.GroupWalletHasActivity(tx, wallet)
		if err != nil {
			return err
		}
		if active {
			return errors.New("Group type cannot change after balance activity")
		}
		values := map[string]any{"mode": mode}
		if mode == model.GroupWalletIndependent {
			values["activated"] = true
		}
		return tx.Model(wallet).Updates(values).Error
	})
}

func SetGroupWalletEnabled(actorID int, group string, enabled bool) error {
	return withGroupAccess(actorID, group, authz.GroupWalletRead, func(tx *gorm.DB, actor *model.User) error {
		wallet, err := model.EnsureGroupWallet(tx, group)
		if err != nil {
			return err
		}
		if actor.Role != common.RoleRootUser && (!wallet.IsIndependent() || authz.GroupAllows(tx, actor, group, authz.GroupWalletManage) != nil) {
			return authz.ErrGroupAccess
		}
		values := map[string]any{"enabled": enabled}
		if enabled {
			values["activated"] = true
		}
		return tx.Model(wallet).Updates(values).Error
	})
}

func FundGroupWallet(actorID int, group string, delta int64, reason, operationID string) error {
	return withGroupAccess(actorID, group, authz.GroupWalletRead, func(tx *gorm.DB, actor *model.User) error {
		if strings.TrimSpace(reason) == "" || len(reason) > 500 {
			return errors.New("A reason is required for group wallet changes")
		}
		wallet, err := model.EnsureGroupWallet(tx, group)
		if err != nil {
			return err
		}
		if actor.Role != common.RoleRootUser && (!wallet.IsIndependent() || authz.GroupAllows(tx, actor, group, authz.GroupWalletManage) != nil) {
			return authz.ErrGroupAccess
		}
		return model.AdjustGroupWalletPool(tx, wallet, actorID, delta, strings.TrimSpace(reason), operationID)
	})
}

func AllocateGroupWallet(actorID int, group string, userID int, delta int64, reason, operationID string) error {
	return withGroupAccess(actorID, group, authz.GroupWalletAllocate, func(tx *gorm.DB, actor *model.User) error {
		userQuery := tx
		if delta < 0 {
			userQuery = tx.Unscoped()
		}
		user, err := model.LockGroupUser(userQuery, userID)
		if err != nil {
			return authz.ErrGroupAccess
		}
		if delta > 0 && (user.Group != group || user.Status != common.UserStatusEnabled || actor.Role != common.RoleRootUser && user.Role != common.RoleCommonUser) {
			return authz.ErrGroupAccess
		}
		if len(reason) > 500 {
			return errors.New("Invalid parameters")
		}
		wallet, err := model.EnsureGroupWallet(tx, group)
		if err != nil {
			return err
		}
		return model.AllocateGroupWallet(tx, wallet, actorID, userID, delta, strings.TrimSpace(reason), operationID)
	})
}

func GetOwnGroupWallet(userID int) (GroupWalletMemberView, *model.GroupWallet, error) {
	user, err := model.GetUserById(userID, false)
	if err != nil {
		return GroupWalletMemberView{}, nil, err
	}
	wallet, err := model.FindGroupWallet(user.Group)
	view := GroupWalletMemberView{UserID: userID, Active: true}
	if err != nil || wallet == nil {
		return view, wallet, err
	}
	var member model.GroupWalletMember
	err = model.DB.Where("wallet_id = ? AND user_id = ?", wallet.ID, userID).First(&member).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	view.Quota, view.UsedQuota = member.Quota, member.UsedQuota
	return view, wallet, err
}

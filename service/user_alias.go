package service

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"gorm.io/gorm"
)

func SetPrivateUserAlias(ownerID, userID int, alias, group string) error {
	alias = strings.TrimSpace(alias)
	if userID <= 0 || !utf8.ValidString(alias) || utf8.RuneCountInString(alias) > 64 || strings.ContainsFunc(alias, unicode.IsControl) {
		return errors.New("Private aliases must be a single line of up to 64 characters")
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		actor, err := model.LockGroupUser(tx, ownerID)
		if err != nil || actor.Status != common.UserStatusEnabled {
			return authz.ErrGroupAccess
		}
		// Removing your own annotation remains possible after access is revoked.
		if alias == "" {
			return model.SaveUserAlias(tx, ownerID, userID, alias)
		}
		var target model.User
		if err := tx.Unscoped().Select("id", "role", "group", "deleted_at").First(&target, userID).Error; err != nil {
			return authz.ErrGroupAccess
		}
		allowed := ownerID == userID || actor.Role >= common.RoleAdminUser && authz.Can(actor.Id, actor.Role, authz.GlobalAdminAccess)
		if !allowed && group != "" {
			if target.Role == common.RoleCommonUser && !target.DeletedAt.Valid && authz.GroupAllows(tx, actor, group, authz.GroupMembersRead) == nil {
				allowed = target.Group == group
				if !allowed && authz.GroupAllows(tx, actor, group, authz.GroupMembersManage) == nil {
					var count int64
					if err := tx.Model(&model.GroupJoinRequest{}).Where("user_id = ? AND group_name = ?", userID, group).Count(&count).Error; err != nil {
						return err
					}
					allowed = count > 0
				}
			}
			if !allowed && authz.GroupAllows(tx, actor, group, authz.GroupWalletRead) == nil {
				var count int64
				if err := tx.Model(&model.GroupWalletMember{}).Joins("JOIN group_wallets ON group_wallets.id = group_wallet_members.wallet_id").Where("group_wallet_members.user_id = ? AND group_wallets.group_name = ?", userID, group).Count(&count).Error; err != nil {
					return err
				}
				allowed = count > 0 || target.Group == group && target.Role == common.RoleCommonUser && !target.DeletedAt.Valid
			}
		}
		if !allowed {
			return authz.ErrGroupAccess
		}
		return model.SaveUserAlias(tx, ownerID, userID, alias)
	})
}

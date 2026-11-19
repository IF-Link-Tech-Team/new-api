package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"gorm.io/gorm"
)

func RequestGroupMembership(userID int, group string) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		user, err := model.LockGroupUser(tx, userID)
		if err != nil || user.Role != common.RoleCommonUser || user.Status != common.UserStatusEnabled || !authz.ValidManagedGroup(group, user.Role) {
			return authz.ErrGroupAccess
		}
		if user.Group == group {
			return errors.New("Already a member of this group")
		}
		// One pending request per account; explicit replacement is intentional.
		if err := tx.Where("user_id = ?", userID).Delete(&model.GroupJoinRequest{}).Error; err != nil {
			return err
		}
		return tx.Create(&model.GroupJoinRequest{UserID: userID, GroupName: group, FromGroup: user.Group}).Error
	})
}

func ReviewGroupMembership(actorID, userID int, group string, approve bool) error {
	err := withGroupAccess(actorID, group, authz.GroupMembersManage, func(tx *gorm.DB, _ *model.User) error {
		user, err := model.LockGroupUser(tx, userID)
		if err != nil || user.Role != common.RoleCommonUser {
			return authz.ErrGroupAccess
		}
		var request model.GroupJoinRequest
		if err := tx.Where("user_id = ? AND group_name = ?", userID, group).First(&request).Error; err != nil {
			return authz.ErrGroupAccess
		}
		if approve {
			if user.Status != common.UserStatusEnabled || user.Group != request.FromGroup || !authz.ValidManagedGroup(group, user.Role) {
				return errors.New("The membership request is no longer valid")
			}
			if err := tx.Model(user).Update("group", group).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&request).Error
	})
	if err == nil && approve {
		return model.RefreshUserGroupCache(userID)
	}
	return err
}

func RemoveGroupMember(actorID, userID int, group string) error {
	err := withGroupAccess(actorID, group, authz.GroupMembersManage, func(tx *gorm.DB, _ *model.User) error {
		user, err := model.LockGroupUser(tx, userID)
		if err != nil || user.Role != common.RoleCommonUser || user.Group != group {
			return authz.ErrGroupAccess
		}
		if group == "default" {
			return errors.New("Users cannot be removed from the default group")
		}
		// Leave the account, credentials, balance and historical usage intact.
		if err := tx.Model(user).Update("group", "default").Error; err != nil {
			return err
		}
		return tx.Where("user_id = ?", userID).Delete(&model.GroupJoinRequest{}).Error
	})
	if err == nil {
		return model.RefreshUserGroupCache(userID)
	}
	return err
}

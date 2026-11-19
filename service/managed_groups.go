package service

import (
	"errors"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ManagedGroup struct {
	Name        string   `json:"name"`
	Mode        string   `json:"mode"`
	Permissions []string `json:"permissions"`
}

func ManagedGroups(userID int) ([]ManagedGroup, error) {
	user, err := model.GetUserById(userID, false)
	if err != nil {
		return nil, err
	}
	groups := make([]ManagedGroup, 0)
	if user.Status != common.UserStatusEnabled {
		return groups, nil
	}
	independent, err := model.IndependentGroupNames()
	if err != nil {
		return nil, err
	}
	var grants []model.GroupGrant
	if user.Role != common.RoleRootUser {
		if err := model.DB.Where("user_id = ?", userID).Find(&grants).Error; err != nil {
			return nil, err
		}
	}
	for name := range ratio_setting.GetGroupRatioCopy() {
		if !authz.ValidManagedGroup(name, user.Role) {
			continue
		}
		permissions := make([]string, 0)
		for _, item := range authz.GroupPermissionCatalog {
			if user.Role == common.RoleRootUser || slices.ContainsFunc(grants, func(g model.GroupGrant) bool { return g.GroupName == name && g.Permission == item.Key }) {
				permissions = append(permissions, item.Key)
			}
		}
		if len(permissions) > 0 {
			mode := model.GroupWalletPrepaid
			if slices.Contains(independent, name) {
				mode = model.GroupWalletIndependent
			}
			groups = append(groups, ManagedGroup{Name: name, Mode: mode, Permissions: permissions})
		}
	}
	slices.SortFunc(groups, func(a, b ManagedGroup) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return groups, nil
}

func withGroupAccess(actorID int, group, permission string, action func(*gorm.DB, *model.User) error) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		actor, err := model.LockGroupUser(tx, actorID)
		if err != nil {
			return authz.ErrGroupAccess
		}
		if err := authz.GroupAllows(tx, actor, group, permission); err != nil {
			return err
		}
		return action(tx, actor)
	})
}

func SetGroupGrants(actorID, userID int, group string, permissions []string) error {
	if len(permissions) > len(authz.GroupPermissionCatalog) {
		return errors.New("Invalid group permissions")
	}
	for _, permission := range permissions {
		if !authz.KnownGroupPermission(permission) {
			return errors.New("Invalid group permissions")
		}
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		actor, err := model.LockGroupUser(tx, actorID)
		if err != nil || actor.Role != common.RoleRootUser || actor.Status != common.UserStatusEnabled {
			return authz.ErrGroupAccess
		}
		user, err := model.LockGroupUser(tx, userID)
		if err != nil {
			return authz.ErrGroupAccess
		}
		// Revocation remains possible after a user's role is lowered.
		if len(permissions) > 0 && !authz.ValidManagedGroup(group, user.Role) {
			return authz.ErrGroupAccess
		}
		if !authz.ValidManagedGroup(group, actor.Role) {
			return authz.ErrGroupAccess
		}
		if err := tx.Where("user_id = ? AND group_name = ?", userID, group).Delete(&model.GroupGrant{}).Error; err != nil {
			return err
		}
		slices.Sort(permissions)
		for _, permission := range slices.Compact(permissions) {
			if err := tx.Create(&model.GroupGrant{UserID: userID, GroupName: group, Permission: permission}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

type GroupMember struct {
	ID          int    `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Status      int    `json:"status"`
}

type GroupMembersPage struct {
	Items    []GroupMember `json:"items"`
	Total    int64         `json:"total"`
	Requests []GroupMember `json:"requests"`
}

func ListGroupMembers(actorID int, group string, page int) (GroupMembersPage, error) {
	result := GroupMembersPage{Items: []GroupMember{}, Requests: []GroupMember{}}
	err := withGroupAccess(actorID, group, authz.GroupMembersRead, func(tx *gorm.DB, actor *model.User) error {
		query := tx.Model(&model.User{}).Where(clause.Eq{Column: "group", Value: group}).Where("role = ?", common.RoleCommonUser)
		if err := query.Count(&result.Total).Error; err != nil {
			return err
		}
		if err := query.Select("id", "username", "display_name", "status").Order("id").Limit(50).Offset((max(1, page) - 1) * 50).Scan(&result.Items).Error; err != nil {
			return err
		}
		if authz.GroupAllows(tx, actor, group, authz.GroupMembersManage) != nil {
			return nil
		}
		return tx.Model(&model.User{}).Select("users.id", "users.username", "users.display_name", "users.status").Joins("JOIN group_join_requests ON group_join_requests.user_id = users.id").Where("group_join_requests.group_name = ? AND users.role = ?", group, common.RoleCommonUser).Order("users.id").Limit(100).Scan(&result.Requests).Error
	})
	return result, err
}

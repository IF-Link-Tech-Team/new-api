package authz

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

const (
	GroupMembersRead    = "members.read"
	GroupMembersManage  = "members.manage"
	GroupChannelsRead   = "channels.read"
	GroupChannelsCreate = "channels.create"
	GroupChannelsManage = "channels.manage"
	GroupChannelsAttach = "channels.attach"
	GroupWalletRead     = "wallet.read"
	GroupWalletAllocate = "wallet.allocate"
	GroupWalletManage   = "wallet.manage"
)

type GroupPermissionDefinition struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

var GroupPermissionCatalog = []GroupPermissionDefinition{
	{GroupMembersRead, "View group members"},
	{GroupMembersManage, "Approve and remove group members"},
	{GroupChannelsRead, "View group channels"},
	{GroupChannelsCreate, "Create dedicated group channels"},
	{GroupChannelsManage, "Manage dedicated group channels"},
	{GroupChannelsAttach, "Attach approved shared channels"},
	{GroupWalletRead, "View group balance and ledger"},
	{GroupWalletAllocate, "Allocate and reclaim group balance"},
	{GroupWalletManage, "Adjust credits and pause independent groups"},
}

var ErrGroupAccess = errors.New("Group permission denied")

func ValidManagedGroup(group string, role int) bool {
	if strings.ContainsAny(group, ",/\x00\r\n") || utf8.RuneCountInString(group) > 64 {
		return false
	}
	minimum, _ := ratio_setting.GetGroupRatioSetting().GroupMinRole.Get(group)
	return group != "" && group != "auto" && ratio_setting.ContainsGroupRatio(group) && role >= minimum
}

func KnownGroupPermission(permission string) bool {
	return slices.ContainsFunc(GroupPermissionCatalog, func(item GroupPermissionDefinition) bool { return item.Key == permission })
}

// GroupAllows never falls back to the global admin role. Ordinary administrators
// also need an explicit group grant; root keeps its existing full authority.
func GroupAllows(db *gorm.DB, user *model.User, group, permission string) error {
	if user.Status != common.UserStatusEnabled || !ValidManagedGroup(group, user.Role) || !KnownGroupPermission(permission) {
		return ErrGroupAccess
	}
	if user.Role == common.RoleRootUser {
		return nil
	}
	permissions := []string{permission}
	if permission == GroupWalletRead {
		permissions = append(permissions, GroupWalletAllocate, GroupWalletManage)
	}
	if permission == GroupMembersRead {
		permissions = append(permissions, GroupMembersManage)
	}
	if permission == GroupChannelsRead {
		permissions = append(permissions, GroupChannelsCreate, GroupChannelsManage, GroupChannelsAttach)
	}
	var count int64
	err := db.Model(&model.GroupGrant{}).Where("user_id = ? AND group_name = ? AND permission IN ?", user.Id, group, permissions).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrGroupAccess
	}
	return nil
}

package service

import (
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GroupChannelView struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Models    string `json:"models"`
	Status    int    `json:"status"`
	Owned     bool   `json:"owned"`
	Reviewed  bool   `json:"reviewed"`
	Available bool   `json:"available"`
	Attached  bool   `json:"attached"`
	CanDetach bool   `json:"can_detach"`
}

func ListGroupChannels(actorID int, group string) ([]GroupChannelView, error) {
	result := make([]GroupChannelView, 0)
	err := withGroupAccess(actorID, group, authz.GroupChannelsRead, func(tx *gorm.DB, _ *model.User) error {
		var bindings []model.GroupChannel
		if err := tx.Where("group_name = ?", group).Find(&bindings).Error; err != nil {
			return err
		}
		ids := make([]int, 0, len(bindings))
		for _, binding := range bindings {
			if binding.Owned || binding.AllowAttach {
				ids = append(ids, binding.ChannelID)
			}
		}
		groupChannels := tx.Model(&model.Ability{}).Select("channel_id").Where(clause.Eq{Column: "group", Value: group})
		query := tx.Model(&model.Channel{}).Where("id IN (?)", groupChannels)
		if len(ids) > 0 {
			query = query.Or("id IN ?", ids)
		}
		var channels []model.Channel
		if err := query.Select("id", "name", "models", "status", "group").Order("id").Find(&channels).Error; err != nil {
			return err
		}
		for _, channel := range channels {
			// A global edit may have added a restricted group after an offer.
			if !groupCanShareChannel(group, &channel) {
				continue
			}
			view := GroupChannelView{ID: channel.Id, Name: channel.Name, Models: channel.Models, Status: channel.Status, Attached: slices.Contains(channel.GetGroups(), group)}
			for _, binding := range bindings {
				if binding.ChannelID != channel.Id {
					continue
				}
				view.Owned, view.Reviewed = binding.Owned, binding.Reviewed
				view.Available, view.CanDetach = binding.AllowAttach, binding.Attached && !binding.Owned
			}
			result = append(result, view)
		}
		return nil
	})
	return result, err
}

func groupCanShareChannel(group string, channel *model.Channel) bool {
	targetMinimum, _ := ratio_setting.GetGroupRatioSetting().GroupMinRole.Get(group)
	for _, sourceGroup := range channel.GetGroups() {
		minimum, _ := ratio_setting.GetGroupRatioSetting().GroupMinRole.Get(sourceGroup)
		if minimum > targetMinimum {
			return false
		}
	}
	return true
}

func OfferGroupChannel(actorID int, group string, channelID int, allowed bool) error {
	return withGroupAccess(actorID, group, authz.GroupChannelsAttach, func(tx *gorm.DB, actor *model.User) error {
		if actor.Role != common.RoleRootUser {
			return authz.ErrGroupAccess
		}
		channel, err := model.LockGroupChannel(tx, channelID)
		if err != nil || !groupCanShareChannel(group, channel) {
			return authz.ErrGroupAccess
		}
		var owners int64
		if err := tx.Model(&model.GroupChannel{}).Where("channel_id = ? AND owned = ?", channelID, true).Count(&owners).Error; err != nil {
			return err
		}
		if owners > 0 {
			return errors.New("Dedicated channels cannot be shared with other groups")
		}
		var binding model.GroupChannel
		err = tx.Where("group_name = ? AND channel_id = ?", group, channelID).First(&binding).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		binding.GroupName, binding.ChannelID = group, channelID
		if !allowed && binding.Attached {
			groups := slices.DeleteFunc(channel.GetGroups(), func(name string) bool { return name == group })
			channel.Group = strings.Join(groups, ",")
			if err := tx.Model(channel).Update("group", channel.Group).Error; err != nil {
				return err
			}
			if err := channel.UpdateAbilities(tx); err != nil {
				return err
			}
			binding.Attached = false
		}
		binding.AllowAttach = allowed
		return tx.Save(&binding).Error
	})
}

func SetGroupChannelEnabled(actorID int, group string, channelID int, enabled bool) error {
	return withGroupAccess(actorID, group, authz.GroupChannelsRead, func(tx *gorm.DB, actor *model.User) error {
		channel, err := model.LockGroupChannel(tx, channelID)
		if err != nil || !groupCanShareChannel(group, channel) {
			return authz.ErrGroupAccess
		}
		var binding model.GroupChannel
		if err := tx.Where("group_name = ? AND channel_id = ?", group, channelID).First(&binding).Error; err != nil {
			return authz.ErrGroupAccess
		}
		if binding.Owned {
			if err := authz.GroupAllows(tx, actor, group, authz.GroupChannelsManage); err != nil {
				return err
			}
			if channel.Group != group {
				return authz.ErrGroupAccess
			}
			if enabled {
				if !binding.Reviewed {
					return errors.New("This channel needs super administrator approval")
				}
				if err := groupChannelHasPrices(tx, channel); err != nil {
					return err
				}
			}
			channel.Status = common.ChannelStatusManuallyDisabled
			if enabled {
				channel.Status = common.ChannelStatusEnabled
			}
			if err := tx.Model(channel).Update("status", channel.Status).Error; err != nil {
				return err
			}
			return channel.UpdateAbilities(tx)
		}
		if err := authz.GroupAllows(tx, actor, group, authz.GroupChannelsAttach); err != nil {
			return err
		}
		if !binding.AllowAttach {
			return authz.ErrGroupAccess
		}
		groups := channel.GetGroups()
		if enabled && !slices.Contains(groups, group) {
			groups = append(groups, group)
			binding.Attached = true
		} else if !enabled {
			if !binding.Attached {
				return authz.ErrGroupAccess
			}
			groups = slices.DeleteFunc(groups, func(name string) bool { return name == group })
			binding.Attached = false
		}
		channel.Group = strings.Join(groups, ",")
		if err := tx.Model(channel).Update("group", channel.Group).Error; err != nil {
			return err
		}
		if err := channel.UpdateAbilities(tx); err != nil {
			return err
		}
		return tx.Save(&binding).Error
	})
}

type NewGroupChannel struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Key     string `json:"key"`
	Models  string `json:"models"`
}

func CreateGroupChannel(actorID int, group string, input NewGroupChannel) (int, error) {
	input.Name, input.Models = strings.TrimSpace(input.Name), strings.TrimSpace(input.Models)
	parsed, err := url.Parse(input.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(input.BaseURL) > 2048 || len(input.Name) == 0 || len(input.Name) > 128 || strings.TrimSpace(input.Key) == "" || len(input.Key) > 65536 {
		return 0, errors.New("Enter a name, an HTTPS base URL and an API key")
	}
	models := strings.Split(input.Models, ",")
	if len(models) > 500 {
		return 0, errors.New("Invalid model list")
	}
	for i, name := range models {
		models[i] = strings.TrimSpace(name)
		if models[i] == "" || len(models[i]) > 191 {
			return 0, errors.New("Invalid model list")
		}
	}
	channel := model.Channel{Name: input.Name, BaseURL: &input.BaseURL, Key: input.Key, Models: strings.Join(models, ","), Type: 1, Group: group, Status: common.ChannelStatusManuallyDisabled, CreatedTime: common.GetTimestamp()}
	err = withGroupAccess(actorID, group, authz.GroupChannelsCreate, func(tx *gorm.DB, _ *model.User) error {
		if err := tx.Create(&channel).Error; err != nil {
			return err
		}
		if err := channel.AddAbilities(tx); err != nil {
			return err
		}
		return tx.Create(&model.GroupChannel{GroupName: group, ChannelID: channel.Id, Owned: true}).Error
	})
	return channel.Id, err
}

// New upstream credentials are reviewed by root before any network request.
// Delegated users never receive existing credentials or change reviewed URLs.
func ApproveGroupChannel(actorID int, group string, channelID int) error {
	return withGroupAccess(actorID, group, authz.GroupChannelsManage, func(tx *gorm.DB, actor *model.User) error {
		if actor.Role != common.RoleRootUser {
			return authz.ErrGroupAccess
		}
		channel, err := model.LockGroupChannel(tx, channelID)
		if err != nil || channel.Group != group {
			return authz.ErrGroupAccess
		}
		var binding model.GroupChannel
		if err := tx.Where("group_name = ? AND channel_id = ? AND owned = ?", group, channelID, true).First(&binding).Error; err != nil {
			return authz.ErrGroupAccess
		}
		if err := groupChannelHasPrices(tx, channel); err != nil {
			return err
		}
		return tx.Model(&binding).Update("reviewed", true).Error
	})
}

func groupChannelHasPrices(tx *gorm.DB, channel *model.Channel) error {
	enabled, err := model.APIChannelPricingMode()
	if err != nil || !enabled {
		return err
	}
	_, prices, err := model.ReadAPIChannelCatalog(tx, "")
	if err != nil {
		return err
	}
	local, err := model.ReadGroupModelPrices(tx, []string{channel.Group}, "")
	if err != nil {
		return err
	}
	for _, name := range channel.GetModels() {
		if slices.ContainsFunc(local, func(price model.GroupModelPrice) bool { return price.ChannelID == channel.Id && price.Model == name }) {
			continue
		}
		if !slices.ContainsFunc(prices, func(price model.APIChannelPrice) bool {
			return price.ChannelID == channel.Id && price.Model == name && price.Enabled && price.UpstreamModel == name
		}) {
			return errors.New("Publish channel prices before enabling this channel")
		}
	}
	return nil
}

func GroupGrantRows(userID int, group string) ([]model.GroupGrant, error) {
	rows := make([]model.GroupGrant, 0)
	err := model.DB.Where("user_id = ? AND group_name = ?", userID, group).Find(&rows).Error
	return rows, err
}

func GroupChannelCatalog(group string) ([]model.Channel, error) {
	if !authz.ValidManagedGroup(group, common.RoleRootUser) {
		return nil, authz.ErrGroupAccess
	}
	channels := make([]model.Channel, 0)
	err := model.DB.Select("id", "name", "group").Where("id NOT IN (?)", model.DB.Model(&model.GroupChannel{}).Select("channel_id").Where("owned = ?", true)).Order("id").Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(channels, func(channel model.Channel) bool { return !groupCanShareChannel(group, &channel) }), nil
}

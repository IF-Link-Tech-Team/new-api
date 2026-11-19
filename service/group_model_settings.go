package service

import (
	"errors"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GroupModelSetting struct {
	ChannelID       int                    `json:"channel_id"`
	ChannelName     string                 `json:"channel_name"`
	Model           string                 `json:"model"`
	Revision        int64                  `json:"revision"`
	Prices          *model.PriceComponents `json:"prices"`
	InheritedPrices *model.PriceComponents `json:"inherited_prices"`
}

func GetGroupModelSettings(actorID int, group string) ([]GroupModelSetting, error) {
	result := make([]GroupModelSetting, 0)
	err := withGroupAccess(actorID, group, authz.GroupChannelsRead, func(tx *gorm.DB, _ *model.User) error {
		var channels []model.Channel
		owned := tx.Model(&model.GroupChannel{}).Select("channel_id").Where("group_name = ? AND owned = ?", group, true)
		if err := tx.Select("id", "name", "models", "model_mapping").Where("id IN (?)", owned).Where(clause.Eq{Column: "group", Value: group}).Order("id").Find(&channels).Error; err != nil {
			return err
		}
		local, err := model.ReadGroupModelPrices(tx, []string{group}, "")
		if err != nil {
			return err
		}
		_, global, err := model.ReadAPIChannelCatalog(tx, "")
		if err != nil {
			return err
		}
		for _, channel := range channels {
			for _, name := range channel.GetModels() {
				item := GroupModelSetting{ChannelID: channel.Id, ChannelName: channel.Name, Model: name}
				for _, price := range local {
					if price.ChannelID == channel.Id && price.Model == name {
						row, err := price.APIPrice(channel)
						if err != nil {
							return err
						}
						values, err := row.Prices()
						if err != nil {
							return err
						}
						item.Prices, item.Revision = &values, price.Revision
					}
				}
				for _, row := range global {
					if row.ChannelID == channel.Id && row.Model == name && row.Enabled && row.PriceExpression == "" {
						values, err := row.Prices()
						if err != nil {
							return err
						}
						item.InheritedPrices = &values
					}
				}
				result = append(result, item)
			}
		}
		return nil
	})
	return result, err
}

type GroupModelRateInput struct {
	Model            string              `json:"model"`
	Prices           map[string]*float64 `json:"prices"`
	ExpectedRevision int64               `json:"expected_revision"`
}

func SaveGroupModelRate(actorID int, group string, channelID int, input GroupModelRateInput) error {
	return withGroupAccess(actorID, group, authz.GroupChannelsManage, func(tx *gorm.DB, actor *model.User) error {
		if err := authz.GroupAllows(tx, actor, group, authz.GroupWalletManage); err != nil {
			return err
		}
		var wallet model.GroupWallet
		if err := tx.Where("group_name = ? AND mode = ?", group, model.GroupWalletIndependent).First(&wallet).Error; err != nil {
			return authz.ErrGroupAccess
		}
		channel, err := lockOwnedGroupChannel(tx, group, channelID)
		if err != nil {
			return err
		}
		if !slices.Contains(channel.GetModels(), input.Model) {
			return errors.New("Model is not registered on this group channel")
		}
		raw, err := common.Marshal(input.Prices)
		if err != nil {
			return err
		}
		rate := model.APIChannelPrice{SellingPrice: string(raw)}
		if _, err = rate.BillingExpression(); err != nil {
			return err
		}
		if input.ExpectedRevision < 0 || input.ExpectedRevision >= common.MaxWalletQuota {
			return errors.New("Invalid rate revision")
		}
		var current model.GroupModelPrice
		err = tx.Where("group_name = ? AND channel_id = ? AND model = ?", group, channelID, input.Model).First(&current).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if current.Revision != input.ExpectedRevision {
			return errors.New("Group model rate changed; reload before saving")
		}
		current.GroupName, current.ChannelID, current.Model = group, channelID, input.Model
		current.SellingPrice, current.Revision, current.ActorID, current.UpdatedAt = string(raw), current.Revision+1, actorID, common.GetTimestamp()
		return tx.Save(&current).Error
	})
}

func lockOwnedGroupChannel(tx *gorm.DB, group string, channelID int) (*model.Channel, error) {
	channel, err := model.LockGroupChannel(tx, channelID)
	if err != nil || channel.Group != group {
		return nil, authz.ErrGroupAccess
	}
	var binding model.GroupChannel
	if err := tx.Where("group_name = ? AND channel_id = ? AND owned = ?", group, channelID, true).First(&binding).Error; err != nil {
		return nil, authz.ErrGroupAccess
	}
	return channel, nil
}

func SetGroupChannelModels(actorID int, group string, channelID int, expected, names string) error {
	models := strings.Split(names, ",")
	if len(models) > 500 {
		return errors.New("Invalid model list")
	}
	for i, name := range models {
		models[i] = strings.TrimSpace(name)
		if models[i] == "" || len(models[i]) > 191 || strings.ContainsAny(models[i], "\r\n\x00") || slices.Contains(models[:i], models[i]) {
			return errors.New("Invalid model list")
		}
	}
	return withGroupAccess(actorID, group, authz.GroupChannelsManage, func(tx *gorm.DB, _ *model.User) error {
		channel, err := lockOwnedGroupChannel(tx, group, channelID)
		if err != nil {
			return err
		}
		if channel.Models != expected {
			return errors.New("Group channel models changed; reload before saving")
		}
		channel.Models = strings.Join(models, ",")
		if err := tx.Model(channel).Update("models", channel.Models).Error; err != nil {
			return err
		}
		return channel.UpdateAbilities(tx)
	})
}

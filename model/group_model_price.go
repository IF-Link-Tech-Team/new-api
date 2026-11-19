package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GroupModelPrice is a group-local override; it never edits the global catalog.
// Requests copy the rate and revision before reserving funds.
type GroupModelPrice struct {
	ID           int64  `json:"id"`
	GroupName    string `json:"group_name" gorm:"size:64;uniqueIndex:idx_group_model_price,priority:1"`
	ChannelID    int    `json:"channel_id" gorm:"uniqueIndex:idx_group_model_price,priority:2"`
	Model        string `json:"model" gorm:"size:191;uniqueIndex:idx_group_model_price,priority:3"`
	SellingPrice string `json:"selling_price" gorm:"type:text"`
	Revision     int64  `json:"revision"`
	ActorID      int    `json:"actor_id"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Only autonomous wallets and exclusively owned channels can supply a local
// rate. Changing ownership or wallet type immediately invalidates old overrides.
func ReadGroupModelPrices(db *gorm.DB, groups []string, name string) ([]GroupModelPrice, error) {
	rows := make([]GroupModelPrice, 0)
	if len(groups) == 0 {
		return rows, nil
	}
	owners := db.Model(&GroupChannel{}).Select("channel_id").Where("group_name = group_model_prices.group_name AND owned = ?", true)
	wallets := db.Model(&GroupWallet{}).Select("group_name").Where("mode = ?", GroupWalletIndependent)
	channels := db.Model(&Channel{}).Select("id").Where(clause.Eq{Column: "group", Value: clause.Column{Table: "group_model_prices", Name: "group_name"}})
	query := db.Where("group_name IN ? AND group_name IN (?) AND channel_id IN (?) AND channel_id IN (?)", groups, wallets, owners, channels)
	if name != "" {
		query = query.Where("model = ?", name)
	}
	err := query.Order("id").Find(&rows).Error
	return rows, err
}

func (p GroupModelPrice) APIPrice(channel Channel) (APIChannelPrice, error) {
	mapped, err := ResolveMappedModelName(channel.GetModelMapping(), p.Model)
	return APIChannelPrice{ID: p.ID, Revision: p.Revision, Model: p.Model, ChannelID: p.ChannelID, UpstreamModel: mapped, SellingPrice: p.SellingPrice, Enabled: true, GroupName: p.GroupName}, err
}

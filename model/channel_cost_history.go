package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ChannelCostVersion is append-only. EffectiveAt is server time, never a
// backdated claim about supplier invoices. Keep versions after channel deletion.
type ChannelCostVersion struct {
	ID          int64  `json:"id"`
	ChannelID   int    `json:"channel_id" gorm:"index:idx_channel_cost_time,priority:1"`
	EffectiveAt int64  `json:"effective_at" gorm:"bigint;index:idx_channel_cost_time,priority:2"`
	ActorID     int    `json:"actor_id"`
	Costs       string `json:"costs" gorm:"type:text"`
}

func RecordChannelCostVersion(tx *gorm.DB, channel *Channel) error {
	return tx.Create(&ChannelCostVersion{ChannelID: channel.Id, EffectiveAt: common.GetTimestamp(), ActorID: channel.CostVerifiedBy, Costs: channel.UpstreamCosts}).Error
}

// Seed existing configurations once, from observation time only. We cannot
// reconstruct earlier procurement contracts from today's channel configuration.
func InitializeChannelCostHistory(db *gorm.DB) error {
	var channels []Channel
	if err := db.Select("id").Where("upstream_costs IS NOT NULL AND upstream_costs <> ? AND upstream_costs <> ?", "", "{}").Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		if err := db.Transaction(func(tx *gorm.DB) error {
			var current Channel
			if err := lockForUpdate(tx).Select("id", "upstream_costs", "cost_verified_by").First(&current, channel.Id).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&ChannelCostVersion{}).Where("channel_id = ?", current.Id).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return RecordChannelCostVersion(tx, &current)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

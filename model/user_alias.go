package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UserAlias belongs to the viewer, never to the displayed user's public profile.
type UserAlias struct {
	ID      int    `json:"-"`
	OwnerID int    `json:"-" gorm:"uniqueIndex:idx_user_alias_owner_target,priority:1"`
	UserID  int    `json:"user_id" gorm:"uniqueIndex:idx_user_alias_owner_target,priority:2"`
	Alias   string `json:"alias" gorm:"size:256"`
}

func GetUserAliases(ownerID int) ([]UserAlias, error) {
	aliases := make([]UserAlias, 0)
	err := DB.Select("user_id", "alias").Where("owner_id = ?", ownerID).Order("user_id").Find(&aliases).Error
	return aliases, err
}

func SaveUserAlias(tx *gorm.DB, ownerID, userID int, alias string) error {
	if alias == "" {
		return tx.Where("owner_id = ? AND user_id = ?", ownerID, userID).Delete(&UserAlias{}).Error
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "owner_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"alias"}),
	}).Create(&UserAlias{OwnerID: ownerID, UserID: userID, Alias: alias}).Error
}

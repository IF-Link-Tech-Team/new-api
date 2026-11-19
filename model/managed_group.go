package model

import "gorm.io/gorm"

// Group operations lock the actor too, so revoking a grant cannot race a write.
func LockGroupUser(tx *gorm.DB, id int) (*User, error) {
	var user User
	err := lockForUpdate(tx).Select("id", "role", "status", "group").First(&user, id).Error
	return &user, err
}

func LockGroupChannel(tx *gorm.DB, id int) (*Channel, error) {
	var channel Channel
	err := lockForUpdate(tx).First(&channel, id).Error
	return &channel, err
}

// Management grants are independent of a user's billing group and system role.
// Each grant is read from the database when used, including after revocation.
type GroupGrant struct {
	ID         int    `json:"id"`
	UserID     int    `json:"user_id" gorm:"uniqueIndex:idx_group_grant,priority:1"`
	GroupName  string `json:"group_name" gorm:"size:64;uniqueIndex:idx_group_grant,priority:2"`
	Permission string `json:"permission" gorm:"size:64;uniqueIndex:idx_group_grant,priority:3"`
}

// An offer grants permission to attach a shared channel, not to edit it.
// Attached tracks only routing added here; legacy routing is never removed.
type GroupChannel struct {
	ID          int    `json:"id"`
	GroupName   string `json:"group_name" gorm:"size:64;uniqueIndex:idx_group_channel,priority:1"`
	ChannelID   int    `json:"channel_id" gorm:"uniqueIndex:idx_group_channel,priority:2"`
	Owned       bool   `json:"owned"`
	AllowAttach bool   `json:"allow_attach"`
	Attached    bool   `json:"attached"`
	Reviewed    bool   `json:"reviewed"`
}

// Joining a billing group needs the account holder's request and a manager's
// approval. FromGroup prevents approval of an obsolete transfer request.
type GroupJoinRequest struct {
	ID        int    `json:"id"`
	UserID    int    `json:"user_id" gorm:"uniqueIndex"`
	GroupName string `json:"group_name" gorm:"size:64;index"`
	FromGroup string `json:"from_group" gorm:"size:64"`
	CreatedAt int64  `json:"created_at" gorm:"autoCreateTime"`
}

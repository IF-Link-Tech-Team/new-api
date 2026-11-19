package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GroupUsageRow struct {
	ID               int    `json:"id" gorm:"column:id"`
	CreatedAt        int64  `json:"created_at"`
	UserID           int    `json:"user_id"`
	Username         string `json:"username"`
	ModelName        string `json:"model_name"`
	Quota            int64  `json:"quota"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	RequestID        string `json:"request_id"`
}
type GroupUsagePage struct {
	Items   []GroupUsageRow `json:"items"`
	HasMore bool            `json:"has_more"`
}

// Usage is not the money ledger: zero-price calls still appear here. Only safe
// columns are selected; prompts, credentials, IPs and other groups never leave.
func GetGroupUsage(actorID int, group string, page int) (GroupUsagePage, error) {
	result := GroupUsagePage{Items: make([]GroupUsageRow, 0)}
	err := withGroupAccess(actorID, group, authz.GroupWalletRead, func(_ *gorm.DB, _ *model.User) error {
		order := "created_at DESC, id DESC"
		if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
			order = "created_at DESC, request_id DESC"
		}
		return model.LOG_DB.Model(&model.Log{}).Select("id", "created_at", "user_id", "username", "model_name", "quota", "prompt_tokens", "completion_tokens", "request_id").Where(clause.Eq{Column: "group", Value: group}).Where("type = ?", model.LogTypeConsume).Order(order).Offset((max(page, 1) - 1) * 50).Limit(51).Scan(&result.Items).Error
	})
	if len(result.Items) > 50 {
		result.HasMore = true
		result.Items = result.Items[:50]
	}
	return result, err
}

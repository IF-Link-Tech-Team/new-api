package service

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

type productPublicationHandler struct{}

func init()                                               { RegisterSystemTaskHandler(productPublicationHandler{}) }
func (productPublicationHandler) Type() string            { return "product_publication" }
func (productPublicationHandler) Enabled() bool           { return true }
func (productPublicationHandler) Interval() time.Duration { return 15 * time.Second }
func (productPublicationHandler) NewPayload() any         { return nil }

func (productPublicationHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	var items []model.ProductPublicationItem
	err := model.DB.WithContext(ctx).Select("id").Where("status IN ? AND next_retry_at <= ?", []string{"pending", "retry", "cache_pending"}, common.GetTimestamp()).Order("next_retry_at, id").Limit(500).Find(&items).Error
	completed, failed := 0, 0
	if err == nil {
		for _, item := range items {
			if err = ctx.Err(); err != nil {
				break
			}
			if itemErr := model.ProcessProductPublicationItem(ctx, model.DB, item.ID); itemErr != nil {
				failed++
				// Keep the durable item for the next run. No committed SQL terms
				// are reapplied if cache eviction alone failed.
				updateErr := model.DB.WithContext(ctx).Model(&model.ProductPublicationItem{}).Where("id = ? AND status IN ?", item.ID, []string{"pending", "retry", "cache_pending"}).Updates(map[string]any{
					"last_error": itemErr.Error(), "next_retry_at": common.GetTimestamp() + 60,
				}).Error
				if updateErr != nil {
					err = updateErr
					break
				}
			} else {
				completed++
			}
		}
	}
	status, message := model.SystemTaskStatusSucceeded, ""
	if err != nil || failed > 0 {
		status, message = model.SystemTaskStatusFailed, fmt.Sprintf("product publication worker: failed=%d error=%v", failed, err)
	}
	if finishErr := model.FinishSystemTask(task.TaskID, runnerID, status, map[string]int{"processed": completed, "failed": failed}, message); finishErr != nil {
		logger.LogError(ctx, fmt.Sprintf("product publication task finish failed: %v", finishErr))
	}
}

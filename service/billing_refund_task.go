package service

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type billingRefundHandler struct{}

func init()                                          { RegisterSystemTaskHandler(billingRefundHandler{}) }
func (billingRefundHandler) Type() string            { return "billing_refund" }
func (billingRefundHandler) Enabled() bool           { return true }
func (billingRefundHandler) Interval() time.Duration { return time.Minute }
func (billingRefundHandler) NewPayload() any         { return nil }

type BillingRefundReconciliation struct {
	Completed int   `json:"completed"`
	Failed    int   `json:"failed"`
	Alerts    int64 `json:"alerts"`
	Pending   int64 `json:"pending"`
}

func processBillingRefund(id int64) error {
	if err := model.ProcessBillingRefund(id); err != nil {
		if recordErr := model.RecordBillingRefundFailure(id, err); recordErr != nil {
			common.SysError(fmt.Sprintf("refund failure could not be recorded: receipt=%d error=%v", id, recordErr))
		}
		common.SysError(fmt.Sprintf("billing refund pending: receipt=%d error=%v", id, err))
		return err
	}
	return nil
}

func ReconcileBillingRefunds(ctx context.Context) (BillingRefundReconciliation, error) {
	var result BillingRefundReconciliation
	receipts, err := model.FindDueBillingRefunds(common.GetTimestamp(), 100)
	if err != nil {
		return result, err
	}
	for _, receipt := range receipts {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := processBillingRefund(receipt.ID); err != nil {
			result.Failed++
		} else {
			result.Completed++
		}
	}
	result.Alerts, err = model.CountBillingRefundAlerts(common.GetTimestamp())
	if err != nil {
		return result, err
	}
	result.Pending, err = model.CountPendingBillingRefunds(common.GetTimestamp())
	if err != nil {
		return result, err
	}
	if result.Failed > 0 || result.Alerts > 0 || result.Pending > 0 {
		return result, fmt.Errorf("billing refund reconciliation requires attention: failed=%d alerts=%d pending=%d; inspect /api/billing-refund", result.Failed, result.Alerts, result.Pending)
	}
	_, err = model.CleanupBillingRefunds(30 * 24 * 3600)
	return result, err
}

func (billingRefundHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := ReconcileBillingRefunds(ctx)
	status, message := model.SystemTaskStatusSucceeded, ""
	if err != nil {
		status, message = model.SystemTaskStatusFailed, err.Error()
		logger.LogError(ctx, message)
	}
	if finishErr := model.FinishSystemTask(task.TaskID, runnerID, status, result, message); finishErr != nil {
		logger.LogError(ctx, fmt.Sprintf("refund reconciliation task finish failed: %v", finishErr))
		return
	}
	// Save the durable operator alert before attempting any external delivery.
	if err != nil {
		if root := model.GetRootUser(); root != nil && root.Id > 0 {
			if notifyErr := NotifyUser(root.Id, root.Email, root.GetSetting(), dto.NewNotify("billing_refund_failure", "Billing refund reconciliation requires attention", message, nil)); notifyErr != nil {
				logger.LogError(ctx, fmt.Sprintf("refund alert delivery failed: task=%s error=%v", task.TaskID, notifyErr))
			}
		}
	}
}

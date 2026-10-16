package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedWalletRefundSession(t *testing.T) (*BillingSession, *gin.Context) {
	t.Helper()
	setupBillingLifecycleDB(t)
	seedUser(t, 701, 1000)
	seedToken(t, 701, 701, "durable-wallet", 1000)
	c, _ := gin.CreateTestContext(nil)
	session, apiErr := NewBillingSession(c, &relaycommon.RelayInfo{UserId: 701, TokenId: 701, TokenKey: "durable-wallet", RequestId: "durable-wallet", ForcePreConsume: true}, 100)
	require.Nil(t, apiErr)
	return session, c
}

func TestBillingRefundRestartReplaysOnlyDurableIntentWithBatchMode(t *testing.T) {
	previous := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = previous })
	session, _ := seedWalletRefundSession(t)
	assert.Equal(t, 900, getUserQuota(t, 701), "refundable debits cannot remain in a volatile batch")
	assert.Equal(t, 900, getTokenRemainQuota(t, 701))
	id := session.refundReceipt.ID
	require.NoError(t, model.QueueBillingRefund(id))
	// No Refund goroutine or session object survives this boundary. The worker
	// recovers only from SQL, as after a restart immediately after enqueue.
	session = nil
	result, err := ReconcileBillingRefunds(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Completed)
	require.NoError(t, model.ProcessBillingRefund(id))
	assert.Equal(t, 1000, getUserQuota(t, 701))
	assert.Equal(t, 1000, getTokenRemainQuota(t, 701))
}

func TestBillingRefundTokenFailureRollsBackPoolExtraAndIdempotencyMarker(t *testing.T) {
	session, _, subID := seedSubscriptionBillingSession(t, "token-refund-outage")
	require.NoError(t, session.Reserve(160))
	id := session.refundReceipt.ID
	require.NoError(t, model.QueueBillingRefund(id))
	const callback = "test:refund-token-failure"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("synthetic token update failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
	require.Error(t, processBillingRefund(id))
	var sub model.UserSubscription
	require.NoError(t, model.DB.First(&sub, subID).Error)
	assert.EqualValues(t, 160, sub.AmountUsed)
	assert.Equal(t, 840, getTokenRemainQuota(t, 601))
	var reservation model.SubscriptionPreConsumeRecord
	require.NoError(t, model.DB.Where("request_id = ?", "token-refund-outage").First(&reservation).Error)
	assert.Equal(t, "consumed", reservation.Status)
	require.NoError(t, model.DB.Callback().Update().Remove(callback))
	require.NoError(t, model.ProcessBillingRefund(id))
	require.NoError(t, model.ProcessBillingRefund(id))
	require.NoError(t, model.DB.First(&sub, subID).Error)
	assert.Zero(t, sub.AmountUsed)
	assert.Equal(t, 1000, getTokenRemainQuota(t, 601))
}

func TestBillingRefundCacheFailureAfterCommitDoesNotCreditAgain(t *testing.T) {
	session, _ := seedWalletRefundSession(t)
	id := session.refundReceipt.ID
	require.NoError(t, model.QueueBillingRefund(id))
	server := miniredis.RunT(t)
	previousClient, previousEnabled := common.RDB, common.RedisEnabled
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = previousClient, previousEnabled
		require.NoError(t, client.Close())
	})
	server.SetError("synthetic cache outage")
	require.Error(t, processBillingRefund(id))
	var receipt model.BillingRefund
	require.NoError(t, model.DB.First(&receipt, id).Error)
	assert.Equal(t, model.BillingRefundCachePending, receipt.Status)
	var user model.User
	var token model.Token
	require.NoError(t, model.DB.First(&user, 701).Error)
	require.NoError(t, model.DB.First(&token, 701).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, token.RemainQuota)
	server.SetError("")
	require.NoError(t, model.ProcessBillingRefund(id))
	require.NoError(t, model.ProcessBillingRefund(id))
	require.NoError(t, model.DB.First(&user, 701).Error)
	require.NoError(t, model.DB.First(&token, 701).Error)
	assert.Equal(t, 1000, user.Quota)
	assert.Equal(t, 1000, token.RemainQuota)
	require.NoError(t, model.DB.First(&receipt, id).Error)
	assert.Equal(t, model.BillingRefundSucceeded, receipt.Status)
}

func TestBillingRefundIntentWriteFailureKeepsRecoverableReservation(t *testing.T) {
	session, c := seedWalletRefundSession(t)
	const callback = "test:refund-intent-failure"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "billing_refunds" {
			tx.AddError(errors.New("synthetic intent write outage"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
	session.Refund(c)
	assert.True(t, session.NeedsRefund())
	assert.Equal(t, 900, getUserQuota(t, 701))
	var receipt model.BillingRefund
	require.NoError(t, model.DB.First(&receipt, session.refundReceipt.ID).Error)
	assert.Equal(t, model.BillingRefundReserved, receipt.Status)
	alerts, err := model.CountBillingRefundAlerts(time.Now().Unix() + 7*3600)
	require.NoError(t, err)
	assert.EqualValues(t, 1, alerts, "an unrecorded intent leaves a durable stale reservation alert")
	require.NoError(t, model.DB.Callback().Update().Remove(callback))
	require.NoError(t, model.ReviewBillingRefund(receipt.ID, 999, "confirm_refund", "verified failed request, full reservation remains"))
	_, err = ReconcileBillingRefunds(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1000, getUserQuota(t, 701))
	assert.Equal(t, 1000, getTokenRemainQuota(t, 701))
}

func TestBillingRefundRejectedReserveOrSettlementStillRefundsOriginalReservation(t *testing.T) {
	for _, operation := range []string{"reserve", "settle"} {
		t.Run(operation, func(t *testing.T) {
			session, c, subID := seedSubscriptionBillingSession(t, "rejected-"+operation)
			if operation == "reserve" {
				require.Error(t, session.Reserve(1200))
			} else {
				require.Error(t, session.Settle(1200))
			}
			session.Refund(c)
			require.EventuallyWithT(t, func(collect *assert.CollectT) {
				var sub model.UserSubscription
				assert.NoError(collect, model.DB.First(&sub, subID).Error)
				assert.Zero(collect, sub.AmountUsed)
				assert.Equal(collect, 1000, getTokenRemainQuota(t, 601))
			}, 3*time.Second, 5*time.Millisecond)
		})
	}
}

func TestBillingRefundPartialSettlementRetainsErrorAndCannotRefundSettledFunds(t *testing.T) {
	session, c := seedWalletRefundSession(t)
	const callback = "test:settlement-key-outage"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("synthetic settlement key outage"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
	require.ErrorContains(t, session.Settle(60), "settlement key outage")
	session.Refund(c)
	assert.False(t, session.NeedsRefund())
	assert.Equal(t, 940, getUserQuota(t, 701))
	assert.Equal(t, 900, getTokenRemainQuota(t, 701))
	var receipt model.BillingRefund
	require.NoError(t, model.DB.First(&receipt, session.refundReceipt.ID).Error)
	assert.Equal(t, model.BillingRefundReview, receipt.Status)
	assert.Contains(t, receipt.LastError, "settlement key outage")
	require.Error(t, model.QueueBillingRefund(receipt.ID))
	alerts, err := model.CountBillingRefundAlerts(time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 1, alerts)
}

func TestBillingRefundReconciliationUsesExistingTaskAndRecordsFailure(t *testing.T) {
	setupBillingLifecycleDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.SystemTask{}, &model.SystemTaskLock{}))
	receipt := &model.BillingRefund{UserID: 701, WalletQuota: 100}
	require.NoError(t, model.CreateBillingRefundReceipt(receipt))
	require.NoError(t, model.DB.Model(receipt).Update("updated_at", time.Now().Unix()-7*3600).Error)
	withSystemTaskRegistry(t, billingRefundHandler{})
	runSystemTaskScheduler()
	task, err := model.GetActiveSystemTask("billing_refund")
	require.NoError(t, err)
	require.NotNil(t, task)
	claimed, ok, err := model.ClaimSystemTask(task.ID, "billing_refund", "synthetic-runner", time.Now().Unix()+60)
	require.NoError(t, err)
	require.True(t, ok)
	billingRefundHandler{}.Run(context.Background(), claimed, "synthetic-runner")
	after, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	assert.Equal(t, model.SystemTaskStatusFailed, after.Status)
	assert.Contains(t, after.Error, "alerts=1")
	assert.Contains(t, after.Result, "\"alerts\":1")
	var users int64
	require.NoError(t, model.DB.Model(&model.User{}).Count(&users).Error)
	assert.Zero(t, users, "ambiguous reservations must not create accounts or credit money")
	t.Cleanup(func() { model.DB.Exec("DELETE FROM system_tasks"); model.DB.Exec("DELETE FROM system_task_locks") })
}

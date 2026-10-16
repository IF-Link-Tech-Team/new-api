package service

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/internal/testutil"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingLifecycleDB(t *testing.T) {
	t.Helper()
	previousDB, previousPath, previousMaster := model.DB, common.SQLitePath, common.IsMasterNode
	previousLogDB := model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	if dsn := os.Getenv("CILAI_TEST_POSTGRES_DSN"); dsn != "" {
		db, err := testutil.OpenPostgres(dsn)
		require.NoError(t, err)
		model.DB = db
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	} else {
		common.SQLitePath, common.IsMasterNode = t.TempDir()+"/billing.db", false
		require.NoError(t, model.InitDB())
	}
	require.NoError(t, model.InitLogDB()) // initialize the dialect-specific quoted columns too
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	if os.Getenv("CILAI_TEST_POSTGRES_DSN") != "" {
		sqlDB.SetMaxOpenConns(10)
	}
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB, common.SQLitePath, common.IsMasterNode = previousDB, previousPath, previousMaster
		common.SetDatabaseTypes(previousMainType, previousLogType)
		require.NoError(t, model.InitLogDB())
		model.LOG_DB = previousLogDB
	})
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Token{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}, &model.BillingRefund{}))
	t.Cleanup(func() {
		for _, table := range []string{"billing_refunds", "subscription_pre_consume_records", "user_subscriptions", "subscription_plans", "tokens", "users"} {
			require.NoError(t, model.DB.Exec("DELETE FROM "+table).Error)
		}
	})
}

func seedSubscriptionBillingSession(t *testing.T, requestID string) (*BillingSession, *gin.Context, int) {
	t.Helper()
	setupBillingLifecycleDB(t)
	seedUser(t, 601, 1000)
	seedToken(t, 601, 601, requestID, 1000)
	plan := &model.SubscriptionPlan{Title: "concurrent billing", QuotaResetPeriod: model.SubscriptionResetNever}
	require.NoError(t, model.DB.Create(plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	sub := &model.UserSubscription{UserId: 601, PlanId: plan.Id, AmountTotal: 1000, Status: "active",
		ModelScope: "billing-model", StartTime: time.Now().Unix() - 60, EndTime: time.Now().Unix() + 3600}
	require.NoError(t, model.DB.Create(sub).Error)
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{UserId: 601, TokenId: 601, TokenKey: requestID, TokenUserSubscriptionId: sub.Id,
		RequestId: requestID, OriginModelName: "billing-model", ForcePreConsume: true}
	session, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, apiErr)
	return session, c, sub.Id
}

func TestSubscriptionBillingSessionConcurrentSettlementAndRefund(t *testing.T) {
	session, c, subID := seedSubscriptionBillingSession(t, "settlement-refund-race")
	start, settleDone, refundDone := make(chan struct{}), make(chan error, 1), make(chan struct{})
	go func() { <-start; settleDone <- session.Settle(60) }()
	go func() { <-start; session.Refund(c); close(refundDone) }()
	close(start)
	settleErr := <-settleDone
	<-refundDone
	wantUsed, wantKey := int64(60), 940
	wantStatus := model.BillingRefundSettled
	if settleErr != nil {
		require.ErrorContains(t, settleErr, "already refunded")
		wantUsed, wantKey = 0, 1000
		wantStatus = model.BillingRefundSucceeded
	}
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var sub model.UserSubscription
		assert.NoError(collect, model.DB.First(&sub, subID).Error)
		assert.Equal(collect, wantUsed, sub.AmountUsed)
		assert.Equal(collect, wantKey, getTokenRemainQuota(t, 601))
		var receipt model.BillingRefund
		assert.NoError(collect, model.DB.Where("id = ?", session.refundReceipt.ID).Find(&receipt).Error)
		assert.Equal(collect, wantStatus, receipt.Status)
	}, 3*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1000, getUserQuota(t, 601), "bound keys must not touch wallet balance")
	assert.False(t, session.NeedsRefund())
}

func TestSubscriptionBillingSessionFailedRefundIsDurableAndRecoveredByWorker(t *testing.T) {
	session, c, subID := seedSubscriptionBillingSession(t, "exhausted-refund")
	var attempts atomic.Int32
	const callback = "test:refund-storage-outage"
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_subscriptions" {
			attempts.Add(1)
			tx.AddError(errors.New("synthetic subscription storage outage"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
	session.Refund(c)
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		var receipt model.BillingRefund
		assert.NoError(collect, model.DB.Where("id = ?", session.refundReceipt.ID).Find(&receipt).Error)
		assert.Equal(collect, model.BillingRefundFailed, receipt.Status)
		assert.Equal(collect, 1, receipt.Attempts)
	}, 3*time.Second, 5*time.Millisecond)
	require.Error(t, processBillingRefund(session.refundReceipt.ID))
	require.Error(t, processBillingRefund(session.refundReceipt.ID))
	assert.EqualValues(t, 3, attempts.Load())
	alerts, err := model.CountBillingRefundAlerts(time.Now().Unix())
	require.NoError(t, err)
	assert.EqualValues(t, 1, alerts)
	assert.Equal(t, 900, getTokenRemainQuota(t, 601), "failed SQL refunds roll back Key and pool together")
	require.NoError(t, model.DB.Callback().Update().Remove(callback))
	session.Refund(c)
	assert.False(t, session.NeedsRefund(), "the durable worker now owns the refund")
	var sub model.UserSubscription
	require.NoError(t, model.DB.First(&sub, subID).Error)
	assert.EqualValues(t, 100, sub.AmountUsed, "failed source refund retains the reservation")
	var record model.SubscriptionPreConsumeRecord
	require.NoError(t, model.DB.Where("request_id = ?", "exhausted-refund").First(&record).Error)
	assert.Equal(t, "consumed", record.Status)
	require.NoError(t, model.ReviewBillingRefund(session.refundReceipt.ID, 999, "retry", "synthetic outage resolved and verified"))
	result, err := ReconcileBillingRefunds(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Completed)
	require.NoError(t, model.ProcessBillingRefund(session.refundReceipt.ID))
	require.NoError(t, model.DB.First(&sub, subID).Error)
	assert.Zero(t, sub.AmountUsed)
	assert.Equal(t, 1000, getTokenRemainQuota(t, 601))
}

func TestBillingSessionRefundIsTerminalForSettlement(t *testing.T) {
	setupBillingLifecycleDB(t)
	seedUser(t, 501, 1000)
	seedToken(t, 501, 501, "refund-terminal", 1000)
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{UserId: 501, TokenId: 501, TokenKey: "refund-terminal", ForcePreConsume: true}
	session, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, apiErr)
	session.Refund(c)
	settleErr := session.Settle(0)
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		assert.Equal(collect, 1000, getUserQuota(t, 501))
		assert.Equal(collect, 1000, getTokenRemainQuota(t, 501))
		var receipt model.BillingRefund
		assert.NoError(collect, model.DB.Where("id = ?", session.refundReceipt.ID).Find(&receipt).Error)
		assert.Equal(collect, model.BillingRefundSucceeded, receipt.Status)
	}, time.Second, 5*time.Millisecond)
	assert.Error(t, settleErr, "a late settlement must not refund a failed request twice")
}

func TestBillingSessionRejectsNegativeActualQuota(t *testing.T) {
	truncate(t)
	seedUser(t, 502, 1000)
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{UserId: 502, IsPlayground: true, ForcePreConsume: true}
	session, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, apiErr)
	require.Error(t, session.Settle(-1))
	assert.Equal(t, 900, getUserQuota(t, 502))
	require.NoError(t, session.Settle(50))
	require.NoError(t, session.Settle(50), "repeated settlement must not credit again")
	assert.Equal(t, 950, getUserQuota(t, 502))
}

func TestBillingSessionStorageErrorIsNotClassifiedByMessage(t *testing.T) {
	setupBillingLifecycleDB(t)
	seedUser(t, 503, 1000)
	seedToken(t, 503, 503, "storage-error", 1000)
	databaseErr := errors.New("quota insufficient table read failed")
	const callback = "test:billing_storage_failure"
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_subscriptions" {
			tx.AddError(databaseErr)
		}
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
	c, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{UserId: 503, TokenId: 503, TokenKey: "storage-error", TokenUserSubscriptionId: 1, RequestId: "storage-failure", ForcePreConsume: true}
	session, apiErr := NewBillingSession(c, info, 100)
	require.Nil(t, session)
	require.NotNil(t, apiErr)
	require.ErrorIs(t, apiErr, databaseErr)
	assert.Equal(t, types.ErrorCodeUpdateDataError, apiErr.GetErrorCode())
	assert.Equal(t, 1000, getTokenRemainQuota(t, 503), "failed pre-consumption must roll back the key reservation")
}

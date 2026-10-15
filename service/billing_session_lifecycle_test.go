package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
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
	t.Setenv("SQL_DSN", "")
	t.Setenv("LOG_SQL_DSN", "")
	common.SQLitePath, common.IsMasterNode = t.TempDir()+"/billing.db", false
	require.NoError(t, model.InitDB())
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
		model.DB, common.SQLitePath, common.IsMasterNode = previousDB, previousPath, previousMaster
	})
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Token{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
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
		assert.Equal(t, 1000, getUserQuota(t, 501))
		assert.Equal(t, 1000, getTokenRemainQuota(t, 501))
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

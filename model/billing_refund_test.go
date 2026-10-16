package model

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedWalletRefundReceipt(t *testing.T) *BillingRefund {
	t.Helper()
	truncateTables(t)
	insertUserForPaymentGuardTest(t, 101, 900)
	require.NoError(t, DB.Create(&Token{Id: 101, UserId: 101, Key: "synthetic-refund-token", RemainQuota: 900, UsedQuota: 100}).Error)
	receipt := &BillingRefund{RequestID: "wallet-refund", UserID: 101, TokenID: 101, WalletQuota: 100, TokenQuota: 100}
	require.NoError(t, CreateBillingRefundReceipt(receipt))
	require.NoError(t, SaveBillingRefundReservation(receipt))
	require.NoError(t, QueueBillingRefund(receipt.ID))
	return receipt
}

func TestBillingRefundConcurrentReplayCreditsOnce(t *testing.T) {
	receipt := seedWalletRefundReceipt(t)
	start, done := make(chan struct{}), make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; done <- ProcessBillingRefund(receipt.ID) }()
	}
	close(start)
	require.NoError(t, <-done)
	require.NoError(t, <-done)
	require.NoError(t, ProcessBillingRefund(receipt.ID))
	assert.Equal(t, 1000, getUserQuotaForPaymentGuardTest(t, 101))
	var token Token
	require.NoError(t, DB.First(&token, 101).Error)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	require.NoError(t, DB.First(receipt, receipt.ID).Error)
	assert.Equal(t, BillingRefundSucceeded, receipt.Status)
}

func TestBillingRefundCompletionMarkerFailureRollsBackBothCredits(t *testing.T) {
	receipt := seedWalletRefundReceipt(t)
	const callback = "test:refund-marker-outage"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if values, ok := tx.Statement.Dest.(map[string]any); ok && tx.Statement.Table == "billing_refunds" && values["status"] == BillingRefundCachePending {
			tx.AddError(errors.New("synthetic marker write failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callback)) })
	require.ErrorContains(t, ProcessBillingRefund(receipt.ID), "marker write failure")
	assert.Equal(t, 900, getUserQuotaForPaymentGuardTest(t, 101))
	var token Token
	require.NoError(t, DB.First(&token, 101).Error)
	assert.Equal(t, 900, token.RemainQuota)
	require.NoError(t, DB.First(receipt, receipt.ID).Error)
	assert.Equal(t, BillingRefundPending, receipt.Status)
	require.NoError(t, DB.Callback().Update().Remove(callback))
	require.NoError(t, ProcessBillingRefund(receipt.ID))
	assert.Equal(t, 1000, getUserQuotaForPaymentGuardTest(t, 101))
}

func TestBillingRefundCleanupRetainsFailuresAndAmbiguousReservations(t *testing.T) {
	truncateTables(t)
	old := time.Now().Unix() - 40*24*3600
	for _, status := range []string{BillingRefundPrepared, BillingRefundReserved, BillingRefundSettling, BillingRefundPending, BillingRefundFailed, BillingRefundCachePending, BillingRefundReview, BillingRefundSucceeded, BillingRefundSettled} {
		receipt := &BillingRefund{RequestID: "cleanup-" + status, UserID: 101, SubscriptionID: 1}
		require.NoError(t, CreateBillingRefundReceipt(receipt))
		require.NoError(t, DB.Model(receipt).Updates(map[string]any{"status": status, "updated_at": old}).Error)
		if status == BillingRefundPending || status == BillingRefundSettled {
			record := &SubscriptionPreConsumeRecord{RequestId: receipt.RequestID, UserId: 101, UserSubscriptionId: 1, PreConsumed: 100, Status: "consumed"}
			require.NoError(t, DB.Create(record).Error)
			require.NoError(t, DB.Model(record).Update("updated_at", old).Error)
		}
	}
	legacy := &SubscriptionPreConsumeRecord{RequestId: "legacy-ambiguous", UserId: 101, Status: "consumed"}
	require.NoError(t, DB.Create(legacy).Error)
	require.NoError(t, DB.Model(legacy).Update("updated_at", old).Error)
	deleted, err := CleanupBillingRefunds(30 * 24 * 3600)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted, "settlement evidence survives until its consumed record is cleaned")
	deleted, err = CleanupSubscriptionPreConsumeRecords(7 * 24 * 3600)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted, "only a durably settled reservation is removable")
	var reservations int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Count(&reservations).Error)
	assert.EqualValues(t, 2, reservations, "pending and legacy unknown outcomes retain evidence")
	deleted, err = CleanupBillingRefunds(30 * 24 * 3600)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	var remaining int64
	require.NoError(t, DB.Model(&BillingRefund{}).Count(&remaining).Error)
	assert.EqualValues(t, 7, remaining)
	_, err = CleanupBillingRefunds(1)
	require.Error(t, err)
}

func TestBillingRefundCannotReplaySettlementOrGuessAmbiguousDebit(t *testing.T) {
	truncateTables(t)
	for _, status := range []string{BillingRefundPrepared, BillingRefundSettling, BillingRefundSettled, BillingRefundReview} {
		receipt := &BillingRefund{UserID: 101, WalletQuota: 100}
		require.NoError(t, CreateBillingRefundReceipt(receipt))
		require.NoError(t, DB.Model(receipt).Update("status", status).Error)
		require.Error(t, QueueBillingRefund(receipt.ID))
		require.Error(t, ProcessBillingRefund(receipt.ID))
		require.Error(t, ReviewBillingRefund(receipt.ID, 999, "confirm_refund", "not enough evidence to authorize refund"))
	}
}

func TestBillingRefundQuotaOverflowRemainsPendingWithoutPartialCredit(t *testing.T) {
	receipt := seedWalletRefundReceipt(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 101).Update("quota", common.MaxQuota).Error)
	require.Error(t, ProcessBillingRefund(receipt.ID))
	assert.Equal(t, common.MaxQuota, getUserQuotaForPaymentGuardTest(t, 101))
	var token Token
	require.NoError(t, DB.First(&token, 101).Error)
	assert.Equal(t, 900, token.RemainQuota)
}

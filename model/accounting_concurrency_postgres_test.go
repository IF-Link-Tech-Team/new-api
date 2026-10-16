package model

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func requirePostgresAccountingTestDB(t *testing.T) {
	t.Helper()
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires the isolated PostgreSQL accounting test database")
	}
	var identity struct{ Database, Version string }
	require.NoError(t, DB.Raw("SELECT current_database() AS database, current_setting('server_version') AS version").Scan(&identity).Error)
	require.True(t, strings.HasPrefix(identity.Database, "cilai_pr3_"), "accounting concurrency tests require a disposable cilai_pr3_* database")
	require.True(t, strings.HasPrefix(identity.Version, "16."), identity.Version)
}

func TestPostgresReservationQuotaCompetitionDoesNotLoseOrOverdrawQuota(t *testing.T) {
	requirePostgresAccountingTestDB(t)
	truncateTables(t)
	sub := seedReservationSubscription(t, 101, 100, 0)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, requestID := range []string{"quota-competition-a", "quota-competition-b"} {
		workers.Add(1)
		go func(requestID string) {
			defer workers.Done()
			<-start
			_, err := PreConsumeUserSubscription(requestID, 101, "reservation-model", 0, 80, sub.Id)
			results <- err
		}(requestID)
	}
	close(start)
	workers.Wait()
	close(results)
	succeeded, rejected := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			require.ErrorIs(t, err, ErrSubscriptionQuotaInsufficient)
			rejected++
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, rejected)
	var after UserSubscription
	require.NoError(t, DB.First(&after, sub.Id).Error)
	assert.EqualValues(t, 80, after.AmountUsed)
	var records int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Count(&records).Error)
	assert.EqualValues(t, 1, records)
}

func TestPostgresReservationReplayAndRefundDoNotDeadlock(t *testing.T) {
	requirePostgresAccountingTestDB(t)
	truncateTables(t)
	sub := seedReservationSubscription(t, 101, 1000, 0)
	const requestID = "reservation-refund-lock-order"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	missed, subscriptionLocked, refundRecordLocked := make(chan struct{}), make(chan struct{}), make(chan struct{})
	allowSubscriptionLock, allowRecordRecheck, allowRefundContinue := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := func() {
		for _, gate := range []chan struct{}{allowSubscriptionLock, allowRecordRecheck, allowRefundContinue} {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	}
	defer release()
	type transactionProbe struct{ connection gorm.ConnPool }
	var replay atomic.Pointer[transactionProbe]
	var missedOnce, subscriptionOnce, refundFirstOnce, refundRecordOnce, refundStarted atomic.Bool
	refundFirstLock := make(chan string, 1)
	const beforeCallback, afterCallback = "test:accounting-lock-before", "test:accounting-lock-after"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(beforeCallback, func(tx *gorm.DB) {
		probe := replay.Load()
		_, locking := tx.Statement.Clauses["FOR"]
		if refundStarted.Load() && probe != nil && tx.Statement.ConnPool != probe.connection && locking && refundFirstOnce.CompareAndSwap(false, true) {
			refundFirstLock <- tx.Statement.Table
		}
	}))
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(afterCallback, func(tx *gorm.DB) {
		if tx.Error != nil {
			return
		}
		if tx.Statement.Table == "subscription_pre_consume_records" && tx.RowsAffected == 0 && missedOnce.CompareAndSwap(false, true) {
			replay.Store(&transactionProbe{tx.Statement.ConnPool})
			close(missed)
			select {
			case <-allowSubscriptionLock:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
			return
		}
		probe := replay.Load()
		if probe == nil {
			return
		}
		if tx.Statement.ConnPool == probe.connection && tx.Statement.Table == "user_subscriptions" && subscriptionOnce.CompareAndSwap(false, true) {
			close(subscriptionLocked)
			select {
			case <-allowRecordRecheck:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		}
		_, locking := tx.Statement.Clauses["FOR"]
		if refundStarted.Load() && tx.Statement.ConnPool != probe.connection && tx.Statement.Table == "subscription_pre_consume_records" && locking && refundRecordOnce.CompareAndSwap(false, true) {
			close(refundRecordLocked)
			select {
			case <-allowRefundContinue:
			case <-ctx.Done():
				tx.AddError(ctx.Err())
			}
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(beforeCallback))
		require.NoError(t, DB.Callback().Query().Remove(afterCallback))
	})
	replayDone := make(chan error, 1)
	go func() {
		_, err := PreConsumeUserSubscription(requestID, 101, "reservation-model", 0, 100, sub.Id)
		replayDone <- err
	}()
	select {
	case <-missed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Commit the original reservation after the other transaction's first lookup.
	_, err := PreConsumeUserSubscription(requestID, 101, "reservation-model", 0, 100, sub.Id)
	require.NoError(t, err)
	// Let the replay acquire its pool lock before starting the actual refund.
	close(allowSubscriptionLock)
	select {
	case <-subscriptionLocked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	refundStarted.Store(true)
	refundDone := make(chan error, 1)
	go func() { refundDone <- RefundSubscriptionPreConsume(requestID) }()
	var first string
	select {
	case first = <-refundFirstLock:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if first == "subscription_pre_consume_records" {
		// On the broken implementation the refund now owns the record while
		// the replay owns the pool. Releasing both reproduces the lock cycle.
		select {
		case <-refundRecordLocked:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(allowRecordRecheck)
	close(allowRefundContinue)
	var replayErr, refundErr error
	select {
	case replayErr = <-replayDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case refundErr = <-refundDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var pgErr *pgconn.PgError
	assert.False(t, errors.As(replayErr, &pgErr), "reservation replay must not return an unhandled PostgreSQL error: %v", replayErr)
	assert.NoError(t, refundErr)
	if replayErr != nil {
		assert.ErrorContains(t, replayErr, "already refunded")
	}
	var after UserSubscription
	require.NoError(t, DB.First(&after, sub.Id).Error)
	assert.Zero(t, after.AmountUsed)
	var record SubscriptionPreConsumeRecord
	require.NoError(t, DB.Where("request_id = ?", requestID).First(&record).Error)
	assert.Equal(t, "refunded", record.Status)
}

func TestPostgresCallbackAndManualCompletionCreditOrderOnlyOnce(t *testing.T) {
	requirePostgresAccountingTestDB(t)
	truncateTables(t)
	previousUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousUnit })
	insertUserForPaymentGuardTest(t, 101, 100)
	order := &TopUp{UserId: 101, Amount: 1, Money: 1.49, Currency: "CNY", TradeNo: "payment-concurrent-completion",
		PaymentMethod: PaymentMethodIFLinkPay, PaymentProvider: PaymentProviderIFLinkPay, Status: common.TopUpStatusPending}
	require.NoError(t, order.Insert())
	start, done := make(chan struct{}), make(chan error, 2)
	go func() {
		<-start
		_, _, _, err := CompleteTopUpPayment(order.TradeNo, PaymentProviderIFLinkPay, PaymentReceipt{
			Amount: decimal.RequireFromString("1.49"), Currency: "CNY", ProviderTradeNo: "synthetic-receipt",
		})
		done <- err
	}()
	go func() { <-start; done <- ManualCompleteTopUp(order.TradeNo, "127.0.0.1") }()
	close(start)
	require.NoError(t, <-done)
	require.NoError(t, <-done)
	assert.Equal(t, 745_100, getUserQuotaForPaymentGuardTest(t, 101))
	assert.Equal(t, common.TopUpStatusSuccess, GetTopUpByTradeNo(order.TradeNo).Status)
	_, _, credited, err := CompleteTopUpPayment(order.TradeNo, PaymentProviderIFLinkPay, PaymentReceipt{
		Amount: decimal.RequireFromString("1.49"), Currency: "CNY", ProviderTradeNo: "synthetic-receipt",
	})
	require.NoError(t, err)
	assert.False(t, credited)
	assert.Equal(t, 745_100, getUserQuotaForPaymentGuardTest(t, 101))
}

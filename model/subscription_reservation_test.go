package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedReservationSubscription(t *testing.T, userID int, total, dailyLimit int64) *UserSubscription {
	t.Helper()
	plan := &SubscriptionPlan{Title: "reservation", QuotaResetPeriod: SubscriptionResetNever}
	require.NoError(t, DB.Create(plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	sub := &UserSubscription{
		UserId: userID, PlanId: plan.Id, AmountTotal: total, DailyQuotaLimit: dailyLimit,
		Status: "active", StartTime: GetDBTimestamp() - 60, EndTime: GetDBTimestamp() + 3600,
		ModelScope: "reservation-model",
	}
	require.NoError(t, DB.Create(sub).Error)
	return sub
}

func TestSubscriptionReservationDailyLimitAppliesToBothSelectors(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy selection", true: "bound key"}[bound], func(t *testing.T) {
			truncateTables(t)
			sub := seedReservationSubscription(t, 101, 10_000, 100)
			boundID := 0
			if bound {
				boundID = sub.Id
			}
			_, err := PreConsumeUserSubscription("daily-first", 101, "reservation-model", 0, 100, boundID)
			require.NoError(t, err, "the exact daily limit remains usable")
			_, err = PreConsumeUserSubscription("daily-excess", 101, "reservation-model", 0, 1, boundID)
			require.Error(t, err, "both selectors must enforce the same daily reservation cap")
			var after UserSubscription
			require.NoError(t, DB.First(&after, sub.Id).Error)
			assert.EqualValues(t, 100, after.AmountUsed)
			var count int64
			require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)
			require.NoError(t, RefundSubscriptionPreConsume("daily-first"))
			_, err = PreConsumeUserSubscription("daily-after-refund", 101, "reservation-model", 0, 100, boundID)
			require.NoError(t, err, "a refunded reservation must not consume the daily limit")
		})
	}
}

func TestSubscriptionReservationReplayCannotCrossUserOrBoundPool(t *testing.T) {
	for _, otherUser := range []bool{true, false} {
		t.Run(map[bool]string{true: "different user", false: "different bound pool"}[otherUser], func(t *testing.T) {
			truncateTables(t)
			first := seedReservationSubscription(t, 101, 1000, 0)
			userID := 101
			if otherUser {
				userID = 102
			}
			second := seedReservationSubscription(t, userID, 1000, 0)
			_, err := PreConsumeUserSubscription("shared-request", 101, "reservation-model", 0, 100, first.Id)
			require.NoError(t, err)
			_, err = PreConsumeUserSubscription("shared-request", userID, "reservation-model", 0, 100, second.Id)
			require.Error(t, err, "idempotency must not authenticate an unrelated reservation")
			var after UserSubscription
			require.NoError(t, DB.First(&after, second.Id).Error)
			assert.Zero(t, after.AmountUsed)
		})
	}
}

func TestSubscriptionReservationDatabaseFailureIsPreserved(t *testing.T) {
	truncateTables(t)
	seedReservationSubscription(t, 101, 1000, 0)
	databaseErr := errors.New("reservation storage unavailable")
	const callback = "test:reservation_read_failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_subscriptions" {
			tx.AddError(databaseErr)
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Query().Remove(callback)) })
	_, err := PreConsumeUserSubscription("storage-failure", 101, "reservation-model", 0, 100, 0)
	require.ErrorIs(t, err, databaseErr, "a database outage is not missing subscription quota")
}

func TestSubscriptionReservationLegacySelectionKeepsExpiryOrderAndModelScope(t *testing.T) {
	truncateTables(t)
	wrongModel := seedReservationSubscription(t, 101, 1000, 0)
	require.NoError(t, DB.Model(wrongModel).Updates(map[string]interface{}{"model_scope": "other-model", "end_time": GetDBTimestamp() + 100}).Error)
	exhausted := seedReservationSubscription(t, 101, 99, 0)
	require.NoError(t, DB.Model(exhausted).Update("end_time", GetDBTimestamp()+200).Error)
	eligible := seedReservationSubscription(t, 101, 1000, 0)
	require.NoError(t, DB.Model(eligible).Update("end_time", GetDBTimestamp()+300).Error)
	later := seedReservationSubscription(t, 101, 1000, 0)
	result, err := PreConsumeUserSubscription("legacy-selection", 101, "reservation-model", 0, 100, 0)
	require.NoError(t, err)
	assert.Equal(t, eligible.Id, result.UserSubscriptionId)
	assert.EqualValues(t, 0, result.AmountUsedBefore)
	assert.EqualValues(t, 100, result.AmountUsedAfter)
	for _, sub := range []*UserSubscription{wrongModel, exhausted, later} {
		var after UserSubscription
		require.NoError(t, DB.First(&after, sub.Id).Error)
		assert.Zero(t, after.AmountUsed)
	}
}

func TestSubscriptionReservationConcurrentReplayChargesOnce(t *testing.T) {
	truncateTables(t)
	sub := seedReservationSubscription(t, 101, 100, 0)
	start := make(chan struct{})
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := PreConsumeUserSubscription("concurrent-replay", 101, "reservation-model", 0, 100, sub.Id)
			if err == nil && (result.UserSubscriptionId != sub.Id || result.PreConsumed != 100) {
				err = assert.AnError
			}
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var after UserSubscription
	require.NoError(t, DB.First(&after, sub.Id).Error)
	assert.EqualValues(t, 100, after.AmountUsed)
	var count int64
	require.NoError(t, DB.Model(&SubscriptionPreConsumeRecord{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, RefundSubscriptionPreConsume("concurrent-replay"))
	_, err := PreConsumeUserSubscription("concurrent-replay", 101, "reservation-model", 0, 100, sub.Id)
	require.ErrorContains(t, err, "already refunded")
}

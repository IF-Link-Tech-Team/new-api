package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	CilaiV1MigrationKey             = "cilai_v1_contracts"
	CilaiV1MigrationNotStarted      = "not_started"
	CilaiV1MigrationPreflightPassed = "preflight_passed"
	CilaiV1MigrationApplied         = "applied"
	CilaiV1MigrationFailed          = "failed"
)

// CilaiV1MigrationState is deliberately small. It records the state of this
// migration only; it is not intended to become a general migration framework.
type CilaiV1MigrationState struct {
	Id           int    `json:"id"`
	MigrationKey string `json:"migration_key" gorm:"type:varchar(64);not null;uniqueIndex"`
	Version      int    `json:"version" gorm:"not null;default:1"`
	State        string `json:"state" gorm:"type:varchar(32);not null"`
	Error        string `json:"error" gorm:"type:text"`
	PreflightAt  int64  `json:"preflight_at" gorm:"bigint"`
	AppliedAt    int64  `json:"applied_at" gorm:"bigint"`
	UpdatedAt    int64  `json:"updated_at" gorm:"bigint"`
}

// CilaiV1PreflightReport is a read-only description of the legacy data seen
// before any CILAI V1 DDL is attempted.
type CilaiV1PreflightReport struct {
	DuplicateSubscriptionKeyCounts map[int]int
	ZeroKeySubscriptionIDs         []int
	OneKeySubscriptionIDs          []int
	LegacyPendingOrderIDs          []int
	LegacySuccessOrderIDs          []int
	LegacyFailedOrderIDs           []int
	LegacyUnknownOrderIDs          []int
	OrdersMissingPlanIDs           []int
}

var ErrCilaiV1MigrationPreflight = errors.New("CILAI V1 migration preflight failed")

func (r *CilaiV1PreflightReport) hasBlockingIssue() bool {
	// Missing-plan orders are preserved and marked for manual review after the
	// schema is available. They must not force an otherwise safe migration to
	// mutate nothing merely because the historical row is unrecoverable.
	return r != nil && len(r.DuplicateSubscriptionKeyCounts) > 0
}

// PreflightCilaiV1Migration performs only SELECTs. It intentionally runs
// before every legacy schema helper and before AutoMigrate in migrateDB.
func PreflightCilaiV1Migration() (*CilaiV1PreflightReport, error) {
	report := &CilaiV1PreflightReport{
		DuplicateSubscriptionKeyCounts: map[int]int{},
	}
	if DB == nil {
		return report, fmt.Errorf("%w: database is nil", ErrCilaiV1MigrationPreflight)
	}

	// A brand-new installation has no legacy tables yet and is safe to pass.
	if !cilaiTableExists(DB, "user_subscriptions") && !cilaiTableExists(DB, "subscription_orders") {
		return report, nil
	}

	if cilaiTableExists(DB, "tokens") && cilaiColumnExists(DB, "tokens", "user_subscription_id") {
		where := "user_subscription_id > 0 AND status = 1"
		if cilaiColumnExists(DB, "tokens", "deleted_at") {
			where = "deleted_at IS NULL AND " + where
		}
		var duplicateRows []struct {
			SubscriptionID int
			Count          int
		}
		if err := DB.Table("tokens").Select("user_subscription_id AS subscription_id, COUNT(*) AS count").
			Where(where).Group("user_subscription_id").Having("COUNT(*) > 1").Scan(&duplicateRows).Error; err != nil {
			return report, fmt.Errorf("%w: inspect token bindings: %v", ErrCilaiV1MigrationPreflight, err)
		}
		for _, row := range duplicateRows {
			report.DuplicateSubscriptionKeyCounts[row.SubscriptionID] = row.Count
		}
	}

	if cilaiTableExists(DB, "user_subscriptions") {
		if !cilaiColumnExists(DB, "user_subscriptions", "id") || !cilaiColumnExists(DB, "user_subscriptions", "status") || !cilaiColumnExists(DB, "user_subscriptions", "end_time") {
			return report, fmt.Errorf("%w: user_subscriptions lacks the columns required for deterministic key preflight", ErrCilaiV1MigrationPreflight)
		}
		var activeSubscriptions []struct {
			ID int
		}
		if err := DB.Table("user_subscriptions").Select("id").Where("status = ? AND end_time > ?", "active", common.GetTimestamp()).Order("id asc").Scan(&activeSubscriptions).Error; err != nil {
			return report, fmt.Errorf("%w: inspect active subscriptions: %v", ErrCilaiV1MigrationPreflight, err)
		}
		if len(activeSubscriptions) > 0 && (!cilaiTableExists(DB, "tokens") || !cilaiColumnExists(DB, "tokens", "user_subscription_id")) {
			return report, fmt.Errorf("%w: active subscriptions cannot be matched to token bindings", ErrCilaiV1MigrationPreflight)
		}
		for _, subscription := range activeSubscriptions {
			var count int64
			query := DB.Table("tokens").Where("user_subscription_id = ? AND status = 1", subscription.ID)
			if cilaiColumnExists(DB, "tokens", "deleted_at") {
				query = query.Where("deleted_at IS NULL")
			}
			if err := query.Count(&count).Error; err != nil {
				return report, fmt.Errorf("%w: count keys for subscription %d: %v", ErrCilaiV1MigrationPreflight, subscription.ID, err)
			}
			switch count {
			case 0:
				report.ZeroKeySubscriptionIDs = append(report.ZeroKeySubscriptionIDs, subscription.ID)
			case 1:
				report.OneKeySubscriptionIDs = append(report.OneKeySubscriptionIDs, subscription.ID)
			default:
				// Keep the explicit per-subscription count from the grouped query.
				if _, exists := report.DuplicateSubscriptionKeyCounts[subscription.ID]; !exists {
					report.DuplicateSubscriptionKeyCounts[subscription.ID] = int(count)
				}
			}
		}
	}

	if cilaiTableExists(DB, "subscription_orders") {
		if !cilaiColumnExists(DB, "subscription_orders", "plan_id") || !cilaiColumnExists(DB, "subscription_orders", "status") {
			return report, fmt.Errorf("%w: subscription_orders lacks plan_id/status", ErrCilaiV1MigrationPreflight)
		}
		var orders []struct {
			ID     int
			PlanID int
			Status string
		}
		if err := DB.Table("subscription_orders").Select("id, plan_id, status").Order("id asc").Scan(&orders).Error; err != nil {
			return report, fmt.Errorf("%w: inspect subscription orders: %v", ErrCilaiV1MigrationPreflight, err)
		}
		planTableExists := cilaiTableExists(DB, "subscription_plans")
		for _, order := range orders {
			switch strings.ToLower(strings.TrimSpace(order.Status)) {
			case common.TopUpStatusPending:
				report.LegacyPendingOrderIDs = append(report.LegacyPendingOrderIDs, order.ID)
			case common.TopUpStatusSuccess:
				report.LegacySuccessOrderIDs = append(report.LegacySuccessOrderIDs, order.ID)
			case common.TopUpStatusFailed, common.TopUpStatusExpired, "cancelled", "canceled":
				report.LegacyFailedOrderIDs = append(report.LegacyFailedOrderIDs, order.ID)
			default:
				report.LegacyUnknownOrderIDs = append(report.LegacyUnknownOrderIDs, order.ID)
			}
			if order.PlanID > 0 && planTableExists {
				var count int64
				if err := DB.Table("subscription_plans").Where("id = ?", order.PlanID).Count(&count).Error; err != nil {
					return report, fmt.Errorf("%w: inspect plan for order %d: %v", ErrCilaiV1MigrationPreflight, order.ID, err)
				}
				if count == 0 {
					report.OrdersMissingPlanIDs = append(report.OrdersMissingPlanIDs, order.ID)
				}
			}
		}
		if !planTableExists && len(orders) > 0 {
			return report, fmt.Errorf("%w: subscription_orders exist but subscription_plans does not", ErrCilaiV1MigrationPreflight)
		}
	}

	if report.hasBlockingIssue() {
		return report, fmt.Errorf("%w: duplicate entitlement keys=%v", ErrCilaiV1MigrationPreflight, report.DuplicateSubscriptionKeyCounts)
	}
	return report, nil
}

func cilaiTableExists(db *gorm.DB, table string) bool {
	if db == nil || strings.TrimSpace(table) == "" {
		return false
	}
	var count int64
	var err error
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		err = db.Raw("SELECT COUNT(1) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?", table).Scan(&count).Error
	case common.UsingMainDatabase(common.DatabaseTypeSQLite):
		err = db.Raw("SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count).Error
	default:
		err = db.Raw("SELECT COUNT(1) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&count).Error
	}
	return err == nil && count > 0
}

func cilaiColumnExists(db *gorm.DB, table string, column string) bool {
	if db == nil || strings.TrimSpace(table) == "" || strings.TrimSpace(column) == "" || !cilaiTableExists(db, table) {
		return false
	}
	var count int64
	var err error
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		err = db.Raw("SELECT COUNT(1) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?", table, column).Scan(&count).Error
	case common.UsingMainDatabase(common.DatabaseTypeSQLite):
		err = db.Raw("SELECT COUNT(1) FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&count).Error
	default:
		err = db.Raw("SELECT COUNT(1) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&count).Error
	}
	return err == nil && count > 0
}

func getCilaiV1MigrationState() (*CilaiV1MigrationState, error) {
	if !cilaiTableExists(DB, "cilai_v1_migration_states") {
		return nil, nil
	}
	var state CilaiV1MigrationState
	if err := DB.Where("migration_key = ?", CilaiV1MigrationKey).First(&state).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

func setCilaiV1MigrationState(stateName string, migrationErr error) error {
	if !cilaiTableExists(DB, "cilai_v1_migration_states") {
		return nil
	}
	now := common.GetTimestamp()
	state, err := getCilaiV1MigrationState()
	if err != nil {
		return err
	}
	if state == nil {
		state = &CilaiV1MigrationState{MigrationKey: CilaiV1MigrationKey, Version: 1}
	}
	if state.Version == 0 {
		state.Version = 1
	}
	state.State = stateName
	state.UpdatedAt = now
	state.Error = ""
	if migrationErr != nil {
		state.Error = migrationErr.Error()
	}
	if stateName == CilaiV1MigrationPreflightPassed {
		state.PreflightAt = now
	}
	if stateName == CilaiV1MigrationApplied {
		state.AppliedAt = now
	}
	return DB.Save(state).Error
}

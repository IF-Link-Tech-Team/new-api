// Package testutil contains opt-in integration-test infrastructure.
package testutil

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// OpenPostgres rejects non-local or non-disposable databases before connecting.
// Callers may migrate/delete synthetic data only after this check succeeds.
func OpenPostgres(dsn string) (*gorm.DB, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if (config.Host != "127.0.0.1" && config.Host != "localhost" && config.Host != "::1") || !strings.HasPrefix(config.Database, "cilai_pr3_") || len(config.Fallbacks) != 0 {
		return nil, fmt.Errorf("PostgreSQL tests require a loopback cilai_pr3_* database with no fallback hosts")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	var version string
	err = db.Raw("SHOW server_version").Scan(&version).Error
	if err != nil || !strings.HasPrefix(version, "16.") {
		sqlDB, closeErr := db.DB()
		if closeErr == nil {
			_ = sqlDB.Close()
		}
		return nil, fmt.Errorf("PostgreSQL 16 required (version=%q, error=%v)", version, err)
	}
	return db, nil
}

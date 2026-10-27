package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSQLiteSubscriptionPlanMigrationPreservesAllFields(t *testing.T) {
	previousDB, previousType := DB, common.MainDatabaseType()
	t.Cleanup(func() { DB = previousDB; common.SetMainDatabaseType(previousType) })
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	for _, upgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "released_schema"}[upgrade], func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			DB = db
			if upgrade {
				require.NoError(t, db.Exec("CREATE TABLE subscription_plans (id integer PRIMARY KEY, title varchar(128) NOT NULL, price_amount decimal(10,6) NOT NULL)").Error)
				require.NoError(t, db.Exec("INSERT INTO subscription_plans (id, title, price_amount) VALUES (1, 'existing', 9.9)").Error)
			}
			for range 2 {
				require.NoError(t, ensureSubscriptionPlanTableSQLite())
			}
			statement := &gorm.Statement{DB: db}
			require.NoError(t, statement.Parse(&SubscriptionPlan{}))
			for _, field := range statement.Schema.Fields {
				if field.DBName != "" {
					require.True(t, db.Migrator().HasColumn(&SubscriptionPlan{}, field.DBName), "missing plan field %s", field.DBName)
				}
			}
			plan := SubscriptionPlan{Title: "new", PriceAmount: 19.9, TemplateId: 7, DisplayMetadata: `{"recommended":true}`,
				PriceBook: "product:555", ConfigRevision: 7}
			require.NoError(t, db.Create(&plan).Error)
			require.NoError(t, ensureSubscriptionPlanTableSQLite())
			var saved SubscriptionPlan
			require.NoError(t, db.First(&saved, plan.Id).Error)
			require.Equal(t, plan.TemplateId, saved.TemplateId)
			require.Equal(t, plan.DisplayMetadata, saved.DisplayMetadata)
			require.Equal(t, plan.PriceBook, saved.PriceBook)
			require.Equal(t, plan.ConfigRevision, saved.ConfigRevision)
			if upgrade {
				saved = SubscriptionPlan{}
				require.NoError(t, db.First(&saved, 1).Error)
				require.Equal(t, "existing", saved.Title)
				require.Equal(t, 9.9, saved.PriceAmount)
			}
		})
	}
}

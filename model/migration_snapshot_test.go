package model

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProductionSnapshotMigrationInvariant(t *testing.T) {
	if os.Getenv("CILAI_TEST_FULL_MIGRATION") != "1" {
		t.Skip("production snapshot migration test is opt-in")
	}

	require.True(t, DB.Migrator().HasTable(&ProductPrice{}))
	require.True(t, DB.Migrator().HasTable(&ProductRedemptionClaim{}))
	require.True(t, DB.Migrator().HasColumn(&SubscriptionPlan{}, "Listed"))
	require.True(t, DB.Migrator().HasColumn(&SubscriptionPlan{}, "RedemptionEnabled"))
	require.True(t, DB.Migrator().HasColumn(&Channel{}, "UpstreamCosts"))
	require.True(t, DB.Migrator().HasColumn(&Redemption{}, "BatchCode"))
	require.True(t, DB.Migrator().HasColumn(&Redemption{}, "ClaimedSubscriptionId"))
	require.True(t, DB.Migrator().HasColumn(&Redemption{}, "ClaimedTokenId"))

	var duplicateEntitlementKeys int64
	require.NoError(t, DB.Raw(`
		SELECT COUNT(*)
		FROM (
			SELECT token_id
			FROM subscription_key_bindings
			GROUP BY token_id
			HAVING COUNT(*) > 1
		) AS duplicate_token_bindings
	`).Scan(&duplicateEntitlementKeys).Error)
	assert.Zero(t, duplicateEntitlementKeys)

	var invalidClaims int64
	require.NoError(t, DB.Model(&ProductRedemptionClaim{}).
		Where("user_subscription_id = 0 OR token_id = 0").
		Count(&invalidClaims).Error)
	assert.Zero(t, invalidClaims)
}

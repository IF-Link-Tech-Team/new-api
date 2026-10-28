package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionBillingMultipliersUseProductChannelRates(t *testing.T) {
	setupAccessTokenAudit(t)
	require.NoError(t, model.DB.AutoMigrate(&model.PriceBookPrice{}))
	for _, price := range []model.PriceBookPrice{
		{PriceBook: "product:10", Model: "single-channel", PriceVersion: "v1", BillingMultiplier: 1, ChannelRates: `[{"channel_id":2,"multiplier":0.75,"position":null}]`},
		{PriceBook: "product:10", Model: "same-rate", PriceVersion: "v1", BillingMultiplier: 1, ChannelRates: `[{"channel_id":2,"multiplier":0.75,"position":null},{"channel_id":3,"multiplier":0.75,"position":null}]`},
		{PriceBook: "product:10", Model: "mixed-rate", PriceVersion: "v1", BillingMultiplier: 1, ChannelRates: `[{"channel_id":2,"multiplier":0.75,"position":null},{"channel_id":3,"multiplier":1.1,"position":null}]`},
		{PriceBook: "product:10", Model: "legacy", PriceVersion: "v1", BillingMultiplier: 0.8, BillingExpression: `tier("base", p + c * 4)`,
			OfficialReferencePrice:  `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
			UserSellingPrice:        `{"input":1,"cached_input":0.2,"cache_creation":1,"output":4}`,
			UpstreamSettlementPrice: `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`,
			MaxUpstreamCost:         `{"input":0.5,"cached_input":0.1,"cache_creation":0.5,"output":2}`, AllowedChannels: `[2]`},
	} {
		require.NoError(t, model.DB.Session(&gorm.Session{SkipHooks: true}).Create(&price).Error)
	}

	got := subscriptionBillingMultipliers("", "product:10", "single-channel,same-rate,mixed-rate,legacy", "v1")
	require.Equal(t, map[string]float64{
		"single-channel": 0.75,
		"same-rate":      0.75,
		"legacy":         0.8,
	}, got, "mixed channel rates must not be misrepresented by a single multiplier")
}

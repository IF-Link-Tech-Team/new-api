package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setMarginFloorForTest(t *testing.T, productType string, margin float64) {
	t.Helper()
	template, err := model.GetProductTemplateByType(productType)
	require.NoError(t, err)
	original := *template
	updated := *template
	updated.MinMarginPercent = margin
	require.NoError(t, model.MigrateLockedProductTemplate(&updated))
	t.Cleanup(func() {
		require.NoError(t, model.MigrateLockedProductTemplate(&original))
	})
}

func seedMarginReportFixture(t *testing.T) {
	t.Helper()
	channel := &model.Channel{
		Id: 901, Name: "margin-report", Type: 1, Key: "sk-margin", Status: common.ChannelStatusEnabled,
		Models: "m1", Group: "default",
		UpstreamCosts: `{"m1":{"input":1,"output":2}}`,
	}
	require.NoError(t, model.DB.Create(channel).Error)
}

func marginReportGroupOf(result MarginReportResult, productType string) *MarginReportGroup {
	for index := range result.Groups {
		if result.Groups[index].ProductType == productType {
			return &result.Groups[index]
		}
	}
	return nil
}

func TestMarginReportAggregatesAndAlertsOnBreach(t *testing.T) {
	truncate(t)
	oldUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	seedMarginReportFixture(t)
	setMarginFloorForTest(t, model.ProductTypePackK3, 10)

	now := common.GetTimestamp()
	// Revenue 1,000,000 quota = 2 currency units; cost is 1M input tokens x 1
	// plus 1M output tokens x 2 = 3 units per 1M-token list price, so the
	// estimated margin is (2-3)/2 = -50%, far below the 10% floor.
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: 1, Type: model.LogTypeConsume, CreatedAt: now, ModelName: "m1", ChannelId: 901,
		Quota: 1_000_000, PromptTokens: 1_000_000, CompletionTokens: 1_000_000,
		Other: `{"product_type":"pack_k3"}`,
	}).Error)
	// Untagged consumption falls into the wallet bucket, which has no floor.
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: 1, Type: model.LogTypeConsume, CreatedAt: now, ModelName: "m1", ChannelId: 901,
		Quota: 10, PromptTokens: 1_000_000, CompletionTokens: 1_000_000,
	}).Error)
	// A topup log must not pollute the consume window.
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: 1, Type: model.LogTypeTopup, CreatedAt: now, Quota: 9_000_000,
	}).Error)

	result, err := RunMarginReport(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "pack_k3")
	assert.Equal(t, 1, result.Breaches)

	pack := marginReportGroupOf(result, model.ProductTypePackK3)
	require.NotNil(t, pack)
	assert.True(t, pack.Breached)
	assert.EqualValues(t, 1, pack.Requests)
	assert.EqualValues(t, 1_000_000, pack.RevenueQuota)
	assert.InDelta(t, 2.0, pack.Revenue, 0.000001)
	assert.InDelta(t, 3.0, pack.EstimatedCost, 0.000001)
	assert.InDelta(t, -50.0, pack.MarginPercent, 0.000001)
	assert.Equal(t, 10.0, pack.MinMarginPercent)

	wallet := marginReportGroupOf(result, model.ProductTypeWallet)
	require.NotNil(t, wallet)
	assert.False(t, wallet.Breached, "wallet has no margin floor and must never breach")
}

func TestMarginReportHealthyMarginPasses(t *testing.T) {
	truncate(t)
	oldUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	seedMarginReportFixture(t)
	setMarginFloorForTest(t, model.ProductTypePackK3, 10)

	now := common.GetTimestamp()
	// Revenue 100 units (50,000,000 quota) against 3 units of estimated cost.
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: 1, Type: model.LogTypeConsume, CreatedAt: now, ModelName: "m1", ChannelId: 901,
		Quota: 50_000_000, PromptTokens: 1_000_000, CompletionTokens: 1_000_000,
		Other: `{"product_type":"pack_k3"}`,
	}).Error)
	// A request whose channel has no cost for the model is excluded from the
	// cost side rather than failing the report.
	require.NoError(t, model.LOG_DB.Create(&model.Log{
		UserId: 1, Type: model.LogTypeConsume, CreatedAt: now, ModelName: "unknown-model", ChannelId: 901,
		Quota: 500_000, PromptTokens: 1000, CompletionTokens: 1000,
		Other: `{"product_type":"pack_k3"}`,
	}).Error)

	result, err := RunMarginReport(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, result.Breaches)
	pack := marginReportGroupOf(result, model.ProductTypePackK3)
	require.NotNil(t, pack)
	assert.False(t, pack.Breached)
	assert.EqualValues(t, 2, pack.Requests)
	assert.EqualValues(t, 1, pack.CostCovered)
	// Revenue is 101 units across both logs; only one carried cost data (3
	// units), so the estimate is (101-3)/101.
	assert.InDelta(t, (101.0-3.0)/101.0*100, pack.MarginPercent, 0.000001)
}

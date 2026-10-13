package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func productMultiplierQuota(t *testing.T, usage *dto.Usage, multiplier float64) int {
	t.Helper()
	ctx, _ := gin.CreateTestContext(nil)
	priceData := hosttypes.PriceData{
		ModelRatio:      2,
		CompletionRatio: 3,
		CacheRatio:      0.1,
		GroupRatioInfo:  hosttypes.GroupRatioInfo{GroupRatio: 1},
	}
	priceData.AddOtherRatio("product_billing_multiplier", multiplier)
	info := &relaycommon.RelayInfo{OriginModelName: "v1-product-model", PriceData: priceData}
	return calculateTextQuotaSummary(ctx, info, usage).Quota
}

func TestProductMultiplierAppliesOnceToInputCachedInputAndOutput(t *testing.T) {
	tests := []struct {
		name      string
		usage     *dto.Usage
		baseQuota int
		wantQuota int
	}{
		{
			name:      "input",
			usage:     &dto.Usage{PromptTokens: 100, TotalTokens: 100},
			baseQuota: 200,
			wantQuota: 170,
		},
		{
			name: "cached input",
			usage: &dto.Usage{
				PromptTokens: 100, TotalTokens: 100,
				PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 100},
			},
			baseQuota: 20,
			wantQuota: 17,
		},
		{
			name:      "output",
			usage:     &dto.Usage{CompletionTokens: 100, TotalTokens: 100},
			baseQuota: 600,
			wantQuota: 510,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.baseQuota, productMultiplierQuota(t, tt.usage, 1))
			require.Equal(t, tt.wantQuota, productMultiplierQuota(t, tt.usage, 0.85))
		})
	}
}

func TestTieredProductMultiplierIsFrozenAndAppliedOnce(t *testing.T) {
	expr := `tier("default", p * 2 + c * 10 + cr * 0.2)`
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode:       "tiered_expr",
		ModelName:         "qwen-test",
		ExprString:        expr,
		ExprHash:          billingexpr.ExprHashString(expr),
		GroupRatio:        1,
		QuotaPerUnit:      500_000,
		ProductType:       "monthly_subscription",
		ProductMultiplier: 0.85,
		PriceVersion:      "v1-fixture",
	}
	result, err := billingexpr.ComputeTieredQuota(snapshot, billingexpr.TokenParams{P: 1000, C: 500, CR: 100})
	require.NoError(t, err)
	// (1000*2 + 500*10 + 100*0.2) / 1M * 500K = 3510; ×0.85 = 2983.5 -> 2984.
	require.Equal(t, 2984, result.ActualQuotaAfterGroup)
	require.Equal(t, "monthly_subscription", snapshot.ProductType)
	require.Equal(t, "v1-fixture", snapshot.PriceVersion)
}

func TestConfigurableQwenContextTiersAtFrozenBoundaries(t *testing.T) {
	expr := `len <= 32000 ? tier("le_32k", p * 0.2 + c * 0.8) : len <= 256000 ? tier("le_256k", p * 0.6 + c * 2.4) : tier("le_1m", p * 1.2 + c * 4.8)`
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr),
		GroupRatio: 1, QuotaPerUnit: 500_000, ProductType: "pack_qwen", ProductMultiplier: 0.75, PriceVersion: "qwen-boundary-fixture",
	}
	for _, test := range []struct {
		name string
		len  float64
		tier string
	}{
		{name: "exactly 32K", len: 32_000, tier: "le_32k"},
		{name: "above 32K", len: 32_001, tier: "le_256k"},
		{name: "exactly 256K", len: 256_000, tier: "le_256k"},
		{name: "above 256K", len: 256_001, tier: "le_1m"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := billingexpr.ComputeTieredQuota(snapshot, billingexpr.TokenParams{P: test.len, Len: test.len})
			require.NoError(t, err)
			require.Equal(t, test.tier, result.MatchedTier)
		})
	}
}

func TestConfigurableDeepSeekPeakPricingUsesFrozenRequestTime(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	expr := `(weekday("Asia/Shanghai") >= 1 && weekday("Asia/Shanghai") <= 5 && ((hour("Asia/Shanghai") >= 9 && hour("Asia/Shanghai") < 12) || (hour("Asia/Shanghai") >= 14 && hour("Asia/Shanghai") < 18))) ? tier("peak", p * 2 + c * 8) : tier("offpeak", p * 1 + c * 4)`
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr),
		GroupRatio: 1, QuotaPerUnit: 500_000, ProductType: "pack_ds_glm", ProductMultiplier: 0.70, PriceVersion: "ds-time-fixture",
	}
	params := billingexpr.TokenParams{P: 1_000, C: 100, Len: 1_000}

	peakRequest := billingexpr.RequestInput{EvaluationUnix: time.Date(2026, 9, 14, 9, 0, 0, 0, location).Unix()}
	peak, err := billingexpr.ComputeTieredQuotaWithRequest(snapshot, params, peakRequest)
	require.NoError(t, err)
	require.Equal(t, "peak", peak.MatchedTier)

	offpeakRequest := billingexpr.RequestInput{EvaluationUnix: time.Date(2026, 9, 14, 12, 0, 0, 0, location).Unix()}
	offpeak, err := billingexpr.ComputeTieredQuotaWithRequest(snapshot, params, offpeakRequest)
	require.NoError(t, err)
	require.Equal(t, "offpeak", offpeak.MatchedTier)
	require.Greater(t, peak.ActualQuotaAfterGroup, offpeak.ActualQuotaAfterGroup)

	// Re-evaluating later with the same frozen request input must stay in the
	// original tier rather than reading settlement wall-clock time.
	peakAgain, err := billingexpr.ComputeTieredQuotaWithRequest(snapshot, params, peakRequest)
	require.NoError(t, err)
	require.Equal(t, peak, peakAgain)
}

package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelPriceHelperUsesWalletVersionedSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ProductPrice{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })

	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	oldGroupRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios))
	})

	price := &model.ProductPrice{
		ProductType: model.ProductTypeWallet, Model: "wallet-price-model", PriceVersion: "wallet-price-v1",
		OfficialReferencePrice:  `{"input":2,"cached_input":0.4,"output":8}`,
		UserSellingPrice:        `{"input":2,"cached_input":0.4,"output":8}`,
		UpstreamSettlementPrice: `{"input":1,"cached_input":0.2,"output":4}`,
		BillingExpression:       `tier("wallet", p * 2)`,
		BillingMultiplier:       1,
		AllowedChannels:         `[31]`,
		MaxUpstreamCost:         `{"input":1.5,"cached_input":0.3,"output":6}`,
		FallbackAllowed:         true,
		Enabled:                 true,
	}
	require.NoError(t, model.DB.Create(price).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName:          price.Model,
		UserGroup:                "default",
		UsingGroup:               "default",
		ProductType:              model.ProductTypeWallet,
		ProductBillingMultiplier: 1,
		ProductPriceVersion:      price.PriceVersion,
		BillingRequestInput:      &billingexpr.RequestInput{Body: []byte(`{}`)},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, 1000, priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, model.ProductTypeWallet, info.TieredBillingSnapshot.ProductType)
	require.Equal(t, price.PriceVersion, info.TieredBillingSnapshot.PriceVersion)
	require.Equal(t, 1.0, info.TieredBillingSnapshot.ProductMultiplier)
}

func TestModelPriceHelperUsesImmutableModelMultiplierForMixedClassroomPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ProductPrice{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })

	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	oldGroupRatios := ratio_setting.GroupRatio2JSONString()
	oldModelRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"qwen3.8-flash":99,"deepseek-v4-flash-202605":77}`))
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroupRatios))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldModelRatios))
	})

	price := &model.ProductPrice{
		ProductType: model.ProductTypePackDeepSeekClassroom, Model: model.ClassroomModelQwen,
		PriceVersion:            model.ClassroomPriceVersion,
		OfficialReferencePrice:  `{"input":0.8,"cached_input":0.1,"cache_creation":1.25,"output":2.7}`,
		UserSellingPrice:        `{"input":0.8,"cached_input":0.1,"cache_creation":1.25,"output":2.7}`,
		UpstreamSettlementPrice: `{"input":0.64,"cached_input":0.08,"cache_creation":1,"output":2.16}`,
		BillingExpression:       `tier("base", p * 0.8 + cr * 0.1 + cc * 1.25 + c * 2.7)`,
		BillingMultiplier:       1, AllowedChannels: `[14]`,
		MaxUpstreamCost: `{"input":0.64,"cached_input":0.08,"cache_creation":1,"output":2.16}`,
		Enabled:         true,
	}
	require.NoError(t, model.DB.Create(price).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: model.ClassroomModelQwen, UserGroup: "default", UsingGroup: "default",
		ProductType:              model.ProductTypePackDeepSeekClassroom,
		ProductBillingMultiplier: 0.60, ProductPriceVersion: model.ClassroomPriceVersion,
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`)},
	}
	priceData, err := ModelPriceHelper(ctx, info, 1_000, &types.TokenCountMeta{MaxTokens: 100})
	require.NoError(t, err)
	// (1,000*0.8 + 100*2.7) / 1M * 500,000 * 1.00 = 535.
	require.Equal(t, 535, priceData.QuotaToPreConsume)
	require.Equal(t, 1.0, info.ProductBillingMultiplier)
	require.Equal(t, 1.0, info.TieredBillingSnapshot.ProductMultiplier)

	tokenHubPrice := &model.ProductPrice{
		ProductType: model.ProductTypePackDeepSeekClassroom, Model: model.ClassroomModelTokenHub,
		PriceVersion:            model.ClassroomPriceVersion,
		OfficialReferencePrice:  `{"input":3,"cached_input":0.1,"output":9}`,
		UserSellingPrice:        `{"input":3,"cached_input":0.1,"output":9}`,
		UpstreamSettlementPrice: `{"input":1.078,"cached_input":0.0343,"output":3.234}`,
		BillingExpression:       `tier("base", p * 3 + cr * 0.1 + c * 9)`,
		BillingMultiplier:       1, AllowedChannels: `[1]`,
		MaxUpstreamCost: `{"input":1.078,"cached_input":0.0343,"output":3.234}`,
		Enabled:         true,
	}
	require.NoError(t, model.DB.Create(tokenHubPrice).Error)
	tokenHubInfo := &relaycommon.RelayInfo{
		OriginModelName: model.ClassroomModelTokenHub, UserGroup: "default", UsingGroup: "default",
		ProductType:              model.ProductTypePackDeepSeekClassroom,
		ProductBillingMultiplier: 0.60, ProductPriceVersion: model.ClassroomPriceVersion,
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`)},
	}
	tokenHubData, err := ModelPriceHelper(ctx, tokenHubInfo, 1_000, &types.TokenCountMeta{MaxTokens: 100})
	require.NoError(t, err)
	// (1,000*3 + 100*9) / 1M * 500,000 * 1.00 = 1,950; global ModelRatio 77 is ignored.
	require.Equal(t, 1_950, tokenHubData.QuotaToPreConsume)
	require.Equal(t, 1.0, tokenHubInfo.ProductBillingMultiplier)
	require.Equal(t, 1.0, tokenHubInfo.TieredBillingSnapshot.ProductMultiplier)
}

func TestModelPriceHelperTieredUsesPreloadedRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-test-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
	req.Body = nil
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"stream":true}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{
		BillingRatios: map[string]float64{"n": 3},
	})
	require.NoError(t, err)
	require.Equal(t, 1500, priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)
}

func TestModelPriceHelperTieredPreConsumeMaxTokensFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"tiered-fallback-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-fallback-model":"tier(\"base\", p * 3 + c * 15)"}`,
		"group_ratio_setting.group_ratio": `{"default":1,"free":0}`,
	}))

	const promptTokens = 1000

	cases := []struct {
		name      string
		group     string
		maxTokens int
		expected  int
	}{
		{
			// max_tokens omitted in a paid group -> fall back to 8192 completion tokens.
			// p*3 + c*15 = 1000*3 + 8192*15 = 125880 -> /1e6 * 500000 = 62940
			name:      "non-free group falls back to 8192 completion tokens",
			group:     "default",
			maxTokens: 0,
			expected:  62940,
		},
		{
			// explicit max_tokens is used verbatim, no fallback.
			// 1000*3 + 100*15 = 4500 -> /1e6 * 500000 = 2250
			name:      "explicit max_tokens is used verbatim",
			group:     "default",
			maxTokens: 100,
			expected:  2250,
		},
		{
			// free group (ratio 0) stays zero; fallback is gated on non-zero group ratio.
			name:      "free group stays zero without fallback",
			group:     "free",
			maxTokens: 0,
			expected:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			req.Header.Set("Content-Type", "application/json")
			ctx.Request = req
			ctx.Set("group", tc.group)

			info := &relaycommon.RelayInfo{
				OriginModelName: "tiered-fallback-model",
				UserGroup:       tc.group,
				UsingGroup:      tc.group,
				RequestHeaders:  map[string]string{"Content-Type": "application/json"},
				BillingRequestInput: &billingexpr.RequestInput{
					Headers: map[string]string{"Content-Type": "application/json"},
					Body:    []byte(`{}`),
				},
			}

			priceData, err := ModelPriceHelper(ctx, info, promptTokens, &types.TokenCountMeta{MaxTokens: tc.maxTokens})
			require.NoError(t, err)
			require.Equal(t, tc.expected, priceData.QuotaToPreConsume)
		})
	}
}

func TestModelPriceHelperTieredRejectsPreConsumeOverflow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"tiered-overflow-model":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-overflow-model":"tier(\"overflow\", p * 1000000000000000)"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-overflow-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		BillingRequestInput: &billingexpr.RequestInput{
			Body: []byte(`{}`),
		},
	}

	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})

	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaRound", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

func TestModelPriceHelperRequestBillingRatiosOnlyApplyToFixedPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedModelPrices := ratio_setting.ModelPrice2JSONString()
	savedModelRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedModelPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedModelRatios))
	})

	modelPrices, err := common.Marshal(map[string]float64{
		"fixed-image-price":      0.04,
		"fractional-image-price": 0.0000012,
		"overflow-image-price":   float64(common.MaxQuota) / common.QuotaPerUnit / 2,
	})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(modelPrices)))
	modelRatios, err := common.Marshal(map[string]float64{"ratio-image-price": 15})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(modelRatios)))

	tests := []struct {
		name           string
		model          string
		wantQuota      int
		wantUsePrice   bool
		wantImageCount bool
	}{
		{
			name:           "fixed price applies image count",
			model:          "fixed-image-price",
			wantQuota:      180000,
			wantUsePrice:   true,
			wantImageCount: true,
		},
		{
			name:         "ratio price ignores request billing ratios",
			model:        "ratio-image-price",
			wantQuota:    15000,
			wantUsePrice: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("group", "default")
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				UserGroup:       "default",
				UsingGroup:      "default",
			}
			meta := &types.TokenCountMeta{
				ImagePriceRatio: 3,
				BillingRatios:   map[string]float64{"n": 3},
			}

			priceData, err := ModelPriceHelper(ctx, info, 1000, meta)

			require.NoError(t, err)
			require.Equal(t, tt.wantQuota, priceData.QuotaToPreConsume)
			require.Equal(t, tt.wantUsePrice, priceData.UsePrice)
			require.Equal(t, tt.wantImageCount, priceData.HasOtherRatio("n"))
			require.Equal(t, priceData.OtherRatios(), info.PriceData.OtherRatios())
		})
	}

	newInfo := func(model string) (*gin.Context, *relaycommon.RelayInfo) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		return ctx, &relaycommon.RelayInfo{
			OriginModelName: model,
			UserGroup:       "default",
			UsingGroup:      "default",
		}
	}
	meta := &types.TokenCountMeta{BillingRatios: map[string]float64{"n": 3}}

	ctx, info := newInfo("fractional-image-price")
	priceData, err := ModelPriceHelper(ctx, info, 0, meta)
	require.NoError(t, err)
	// 0.0000012 * 500000 * 3 = 1.8, then truncate once to 1.
	require.Equal(t, 1, priceData.QuotaToPreConsume)

	ctx, info = newInfo("overflow-image-price")
	_, err = ModelPriceHelper(ctx, info, 0, meta)
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	require.Equal(t, "QuotaFromFloat", clamp.Op)
	require.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	require.Nil(t, info.Billing)
}

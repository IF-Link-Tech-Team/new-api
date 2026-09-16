package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestUnboundWalletKeyNeverConsumesAnAvailableSubscription(t *testing.T) {
	truncate(t)
	seedUser(t, 401, 10_000)
	require.NoError(t, model.DB.Create(&model.SubscriptionPlan{Id: 1, Title: "monthly", PriceAmount: 10, DurationUnit: model.SubscriptionDurationDay, DurationValue: 30, Enabled: true}).Error)
	sub := &model.UserSubscription{
		UserId: 401, PlanId: 1, AmountTotal: 50_000, AmountUsed: 0,
		StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: time.Now().Add(time.Hour).Unix(), Status: "active",
		ModelScope: "kimi-k3", ProductType: model.ProductTypeMonthlySubscription,
	}
	require.NoError(t, model.DB.Create(sub).Error)
	ctx, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		UserId: 401, OriginModelName: "kimi-k3", RequestId: "wallet-isolation",
		TokenUserSubscriptionId: 0, IsPlayground: true, ForcePreConsume: true,
	}

	session, apiErr := NewBillingSession(ctx, info, 1_000)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.Equal(t, BillingSourceWallet, info.BillingSource)

	var afterUser model.User
	require.NoError(t, model.DB.First(&afterUser, 401).Error)
	require.Equal(t, 9_000, afterUser.Quota)
	var afterSub model.UserSubscription
	require.NoError(t, model.DB.First(&afterSub, sub.Id).Error)
	require.Zero(t, afterSub.AmountUsed)
}

func TestBoundKeyInsufficientQuotaFailsWithoutWalletOrOtherPoolFallback(t *testing.T) {
	truncate(t)
	seedUser(t, 402, 100_000)
	require.NoError(t, model.DB.Create(&model.SubscriptionPlan{Id: 1, Title: "pack", PriceAmount: 10, DurationUnit: model.SubscriptionDurationDay, DurationValue: 30, Enabled: true}).Error)
	require.NoError(t, model.DB.Create(&model.SubscriptionPlan{Id: 2, Title: "monthly", PriceAmount: 10, DurationUnit: model.SubscriptionDurationDay, DurationValue: 30, Enabled: true}).Error)
	bound := &model.UserSubscription{
		UserId: 402, PlanId: 1, AmountTotal: 100, AmountUsed: 0,
		StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: time.Now().Add(time.Hour).Unix(), Status: "active",
		ModelScope: "kimi-k3", ProductType: model.ProductTypePackK3,
	}
	other := &model.UserSubscription{
		UserId: 402, PlanId: 2, AmountTotal: 100_000, AmountUsed: 0,
		StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: time.Now().Add(time.Hour).Unix(), Status: "active",
		ModelScope: "kimi-k3", ProductType: model.ProductTypeMonthlySubscription,
	}
	require.NoError(t, model.DB.Create(bound).Error)
	require.NoError(t, model.DB.Create(other).Error)
	ctx, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		UserId: 402, OriginModelName: "kimi-k3", RequestId: "bound-isolation",
		TokenUserSubscriptionId: bound.Id, IsPlayground: true, ForcePreConsume: true,
	}

	session, apiErr := NewBillingSession(ctx, info, 1_000)
	require.Nil(t, session)
	require.NotNil(t, apiErr)
	require.Equal(t, relaytypes.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())

	var afterUser model.User
	require.NoError(t, model.DB.First(&afterUser, 402).Error)
	require.Equal(t, 100_000, afterUser.Quota)
	var afterBound, afterOther model.UserSubscription
	require.NoError(t, model.DB.First(&afterBound, bound.Id).Error)
	require.NoError(t, model.DB.First(&afterOther, other.Id).Error)
	require.Zero(t, afterBound.AmountUsed)
	require.Zero(t, afterOther.AmountUsed)
}

func TestBoundKeyExpiredOrWrongModelFailsWithoutWalletFallback(t *testing.T) {
	for _, test := range []struct {
		name       string
		endTime    int64
		modelScope string
		modelName  string
	}{
		{name: "expired entitlement", endTime: time.Now().Add(-time.Minute).Unix(), modelScope: "kimi-k3", modelName: "kimi-k3"},
		{name: "wrong exact model", endTime: time.Now().Add(time.Hour).Unix(), modelScope: "kimi-k3", modelName: "kimi-k3-preview"},
	} {
		t.Run(test.name, func(t *testing.T) {
			truncate(t)
			seedUser(t, 403, 100_000)
			require.NoError(t, model.DB.Create(&model.SubscriptionPlan{Id: 1, Title: "pack", PriceAmount: 10, DurationUnit: model.SubscriptionDurationDay, DurationValue: 30, Enabled: true}).Error)
			bound := &model.UserSubscription{
				UserId: 403, PlanId: 1, AmountTotal: 100_000, AmountUsed: 0,
				StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: test.endTime, Status: "active",
				ModelScope: test.modelScope, ProductType: model.ProductTypePackK3,
			}
			require.NoError(t, model.DB.Create(bound).Error)
			ctx, _ := gin.CreateTestContext(nil)
			info := &relaycommon.RelayInfo{
				UserId: 403, OriginModelName: test.modelName, RequestId: "bound-rejection-" + test.name,
				TokenUserSubscriptionId: bound.Id, IsPlayground: true, ForcePreConsume: true,
			}

			session, apiErr := NewBillingSession(ctx, info, 1_000)
			require.Nil(t, session)
			require.NotNil(t, apiErr)
			require.Equal(t, relaytypes.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())

			var afterUser model.User
			require.NoError(t, model.DB.First(&afterUser, 403).Error)
			require.Equal(t, 100_000, afterUser.Quota)
			var afterSub model.UserSubscription
			require.NoError(t, model.DB.First(&afterSub, bound.Id).Error)
			require.Zero(t, afterSub.AmountUsed)
		})
	}
}

func TestV1PurchaseProvisionKeyAndConsumeSinglePoolE2E(t *testing.T) {
	truncate(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	seedUser(t, 404, 100_000)

	price := &model.ProductPrice{
		ProductType: model.ProductTypePackK3, Model: "kimi-k3", PriceVersion: "v1-e2e",
		OfficialReferencePrice:  `{"input":20,"cached_input":2,"output":100}`,
		UserSellingPrice:        `{"input":19,"cached_input":1.9,"output":95}`,
		UpstreamSettlementPrice: `{"input":14,"cached_input":1,"output":70}`,
		BillingExpression:       `tier("default", p * 20 + cr * 2 + c * 100)`,
		BillingMultiplier:       0.95, AllowedChannels: `[14]`,
		MaxUpstreamCost: `{"input":14,"cached_input":1,"output":70}`,
		Enabled:         true,
	}
	require.NoError(t, model.DB.Create(price).Error)
	plan := &model.SubscriptionPlan{
		Title: "K3 49.9 E2E", PriceAmount: 49.9, Currency: "CNY",
		DurationUnit: model.SubscriptionDurationDay, DurationValue: 30, Enabled: true,
		AllowBalancePay: common.GetPointer(true), AllowWalletOverflow: common.GetPointer(false),
		TotalAmount: 24_950_000, QuotaResetPeriod: model.SubscriptionResetNever,
		ModelScope: "kimi-k3", Category: model.SubscriptionCategoryPackage,
		ProductType: model.ProductTypePackK3, ProductCode: "k3-49-e2e", ProductFamily: "pack-k3-v1",
		BillingMultiplier: 0.95, PriceVersion: "v1-e2e",
		AllowedChannels: `{"kimi-k3":[14]}`,
		MaxUpstreamCost: `{"kimi-k3":{"input":14,"cached_input":1,"output":70}}`,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)

	order, _, err := model.CreateSubscriptionOrder(404, plan.Id, "v1-local-e2e", model.PaymentMethodAlipay, model.PaymentProviderAlipay, true)
	require.NoError(t, err)
	require.NoError(t, model.CompleteSubscriptionOrderWithReceipt(order.TradeNo, model.PaymentProviderAlipay, model.PaymentMethodAlipay, model.PaymentReceipt{
		Amount: decimal.RequireFromString("49.90"), Currency: "CNY", ProviderTradeNo: "alipay-local-e2e",
	}))
	order = model.GetSubscriptionOrderByTradeNo(order.TradeNo)
	require.NotNil(t, order.UserSubscriptionId)
	require.Equal(t, model.ProvisioningStatusProvisioned, order.ProvisioningStatus)

	var binding model.SubscriptionKeyBinding
	require.NoError(t, model.DB.Where("user_subscription_id = ?", *order.UserSubscriptionId).First(&binding).Error)
	var token model.Token
	require.NoError(t, model.DB.First(&token, binding.TokenId).Error)
	require.Equal(t, *order.UserSubscriptionId, token.UserSubscriptionId)
	require.Equal(t, "kimi-k3", token.ModelLimits)

	ctx, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		UserId: 404, OriginModelName: "kimi-k3", RequestId: "v1-local-e2e-consume",
		TokenUserSubscriptionId: token.UserSubscriptionId, TokenId: token.Id, TokenKey: token.Key,
		TokenUnlimited: true, IsPlayground: true, ForcePreConsume: true,
	}
	session, apiErr := NewBillingSession(ctx, info, 1_000)
	require.Nil(t, apiErr)
	require.NotNil(t, session)
	require.Equal(t, BillingSourceSubscription, info.BillingSource)
	require.Equal(t, token.UserSubscriptionId, info.SubscriptionId)

	var entitlement model.UserSubscription
	require.NoError(t, model.DB.First(&entitlement, token.UserSubscriptionId).Error)
	require.EqualValues(t, 1_000, entitlement.AmountUsed)
	var user model.User
	require.NoError(t, model.DB.First(&user, 404).Error)
	require.Equal(t, 100_000, user.Quota)
}

package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionPlanProductIDKeepsLegacyModelPacksOutOfPlanAliases(t *testing.T) {
	tests := []struct {
		name string
		plan model.SubscriptionPlan
		want string
	}{
		{
			name: "explicit product code wins",
			plan: model.SubscriptionPlan{ProductCode: "pack_deepseek_classroom", Title: "Pro"},
			want: "pack_deepseek_classroom",
		},
		{
			name: "conversion preserves the checkout code",
			plan: model.SubscriptionPlan{Id: 17, PriceBook: "product:17", ProductCode: "PACK_CONFIGURED", Title: "Updated title"},
			want: "pack_configured",
		},
		{
			name: "new generic product without code uses id",
			plan: model.SubscriptionPlan{Id: 18, PriceBook: "product:18", Title: "Pro"},
			want: "18",
		},
		{
			name: "deepseek pro is not subscription pro",
			plan: model.SubscriptionPlan{Title: "DeepSeek V4 Pro 流量包"},
			want: "deepseek-v4-pro",
		},
		{
			name: "plain pro remains the subscription alias",
			plan: model.SubscriptionPlan{Title: "Pro 月度套餐"},
			want: "pro",
		},
		{
			name: "legacy glm pack uses its catalog alias",
			plan: model.SubscriptionPlan{Title: "GLM 体验流量包"},
			want: "glm-general",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, subscriptionPlanProductID(test.plan))
		})
	}
}

func TestSubscriptionPlanMatchesPublicIDRejectsCrossProductAlias(t *testing.T) {
	deepseek := model.SubscriptionPlan{Title: "DeepSeek V4 Pro 流量包"}
	require.True(t, subscriptionPlanMatchesPublicID(deepseek, "deepseek-v4-pro"))
	require.False(t, subscriptionPlanMatchesPublicID(deepseek, "pro"))
}

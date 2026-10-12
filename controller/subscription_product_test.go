package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionPlanProductID(t *testing.T) {
	tests := []struct {
		name string
		plan model.SubscriptionPlan
		want string
	}{
		{name: "lite", plan: model.SubscriptionPlan{Id: 1, Title: "Lite"}, want: "lite"},
		{name: "pro", plan: model.SubscriptionPlan{Id: 2, Title: "Pro"}, want: "pro"},
		{name: "max", plan: model.SubscriptionPlan{Id: 3, Title: "Max"}, want: "max"},
		{name: "deepseek pro", plan: model.SubscriptionPlan{Id: 4, ModelScope: "deepseek-v4-pro"}, want: "deepseek-v4-pro"},
		{name: "deepseek flash", plan: model.SubscriptionPlan{Id: 5, Title: "流量包", ModelScope: "deepseek-v4-flash"}, want: "deepseek-v4-flash"},
		{name: "numeric fallback", plan: model.SubscriptionPlan{Id: 99, Title: "自定义套餐"}, want: "99"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, subscriptionPlanProductID(tt.plan))
		})
	}
}

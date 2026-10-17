package model

import (
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	BigfishProductFamily = "deepseek-bigfish-promo-20261017"
	BigfishPriceVersion  = "deepseek-bigfish-20261017-v1"
)

// Bigfish is a separate, one-time promotion. It cannot inherit the classroom
// SKU's duration or the monthly tier/upgrade contract, despite similar names.
func validateBigfishPlan(plan *SubscriptionPlan, policy *ProductRoutePolicy) error {
	prices := map[string]float64{
		"deepseek-bigfish-lite-20261017": 9.9,
		"deepseek-bigfish-pro-20261017":  19.9,
		"deepseek-bigfish-max-20261017":  39.9,
	}
	price, ok := prices[plan.ProductCode]
	if !ok || math.Abs(plan.PriceAmount-price) > 0.000001 {
		return errors.New("bigfish requires a distinct Lite/Pro/Max product code and price")
	}
	if plan.ProductFamily != BigfishProductFamily || plan.PriceVersion != BigfishPriceVersion || plan.ProductTier != 0 {
		return errors.New("bigfish requires an independent promotion family and price version; no monthly tier")
	}
	if plan.MaxPurchasePerUser != 1 || plan.RedemptionEnabled || plan.AllowBalancePay == nil || *plan.AllowBalancePay || plan.UpgradeGroup != "" || plan.DowngradeGroup != "" {
		return errors.New("bigfish is purchase-once, without redemption, balance payment or group changes")
	}
	if !sameStringSet(explicitModelIds(plan.ModelScope), []string{ClassroomModelV4GA, ClassroomModelV41}) || len(policy.AllowedChannels) != 2 || len(policy.MaxUpstreamCost) != 2 {
		return errors.New("all bigfish tiers must share exactly the two audited Flash models")
	}
	for _, modelName := range []string{ClassroomModelV4GA, ClassroomModelV41} {
		channels := policy.AllowedChannels[modelName]
		if len(channels) != 1 || channels[0] != 15 {
			return errors.New("bigfish Flash models must be restricted to channel 15")
		}
	}
	return nil
}

// Callers hold the user row lock, serializing issuance across all three SKUs.
// Expired/cancelled/spent entitlements still count; buying again never renews
// the promotion. Successful order history also survives entitlement deletion.
func checkBigfishPurchaseHistoryTx(tx *gorm.DB, userId int) error {
	var count int64
	if err := tx.Model(&UserSubscription{}).Where("user_id = ? AND product_type = ? AND product_family = ?", userId, ProductTypePackDeepSeekBigfish, BigfishProductFamily).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("大肥鱼活动三档合计每账号限购一份，不叠加、不续期")
	}
	if err := tx.Model(&SubscriptionOrder{}).Where("user_id = ? AND product_type = ? AND product_family = ? AND status = ?", userId, ProductTypePackDeepSeekBigfish, BigfishProductFamily, common.TopUpStatusSuccess).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("该账号已完成大肥鱼活动购买，不可重复购买其他档位")
	}
	return nil
}

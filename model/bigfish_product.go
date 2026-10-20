package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	BigfishProductFamily = "deepseek-bigfish-promo-20261017"
	BigfishPriceVersion  = "deepseek-bigfish-20261017-v1"
)

// Bigfish is a separate, one-time promotion. It cannot inherit the classroom
// SKU's duration or the monthly tier/upgrade contract, despite similar names.
// The contract numbers come from the product template; validateBigfishPlan
// adds the promotion-specific switch, identity and exact-route checks.
func validateBigfishPlan(plan *SubscriptionPlan, policy *ProductRoutePolicy, template *ProductTemplate, templateModels []string, templateChannels map[string][]int) error {
	if plan.ProductFamily != template.ProductFamily || plan.PriceVersion != template.PriceVersion || plan.ProductTier != 0 {
		return errors.New("bigfish requires an independent promotion family and price version; no monthly tier")
	}
	if plan.AllowBalancePay == nil || *plan.AllowBalancePay || plan.UpgradeGroup != "" || plan.DowngradeGroup != "" {
		return errors.New("bigfish is purchase-once, without redemption, balance payment or group changes")
	}
	if !sameStringSet(explicitModelIds(plan.ModelScope), templateModels) ||
		len(policy.AllowedChannels) != len(templateModels) || len(policy.MaxUpstreamCost) != len(templateModels) {
		return errors.New("all bigfish tiers must share exactly the two audited Flash models")
	}
	for _, modelName := range templateModels {
		if !sameChannelSet(policy.AllowedChannels[modelName], templateChannels[modelName]) {
			return fmt.Errorf("bigfish model %s channel binding differs from its product template", modelName)
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

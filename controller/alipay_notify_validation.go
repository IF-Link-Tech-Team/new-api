package controller

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/setting"
	"github.com/shopspring/decimal"
)

// validateAlipayNotifyIdentity binds a signed notification to the configured
// application and receiving merchant. Signature verification alone is not
// enough: a valid Alipay notification for another app or merchant must not
// complete an IF.Link order.
func validateAlipayNotifyIdentity(params map[string]string) error {
	appID := strings.TrimSpace(setting.AlipayAppID)
	sellerID := strings.TrimSpace(setting.AlipaySellerID)
	if appID == "" || sellerID == "" {
		return fmt.Errorf("支付宝应用或商户 PID 未配置")
	}
	if strings.TrimSpace(params["app_id"]) != appID {
		return fmt.Errorf("支付宝 app_id 不匹配")
	}
	if authAppID := strings.TrimSpace(params["auth_app_id"]); authAppID != "" && authAppID != appID {
		return fmt.Errorf("支付宝 auth_app_id 不匹配")
	}
	if strings.TrimSpace(params["seller_id"]) != sellerID {
		return fmt.Errorf("支付宝 seller_id 不匹配")
	}
	if currency := strings.TrimSpace(params["currency"]); currency != "" && !strings.EqualFold(currency, "CNY") {
		return fmt.Errorf("支付宝通知币种不是人民币")
	}
	return nil
}

// alipayNotifyAmountMatches compares amounts at currency precision. The
// expected amount is always loaded from the persisted order snapshot.
func alipayNotifyAmountMatches(params map[string]string, expected float64) bool {
	paid, err := decimal.NewFromString(strings.TrimSpace(params["total_amount"]))
	if err != nil {
		return false
	}
	expectedAmount := decimal.NewFromFloat(expected)
	return paid.Round(2).Cmp(expectedAmount.Round(2)) == 0
}

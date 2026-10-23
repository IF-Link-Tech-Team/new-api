package model

import (
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

// WalletQuota is the single conversion contract for order validation, callbacks
// and manual completion. Amount has historical provider-specific units; it must not be
// treated as a universal monetary amount or changed in existing order rows.
func (topUp *TopUp) WalletQuota() (int, error) {
	quota := decimal.NewFromInt(topUp.Amount)
	switch topUp.PaymentProvider {
	case PaymentProviderCreem:
		// Creem stores raw quota in Amount, already converted at order creation.
	case PaymentProviderStripe, PaymentProviderIFLinkPay:
		// Stripe stores purchased monetary units in Money. IFLinkPay is CNY 1:1
		// and accepts cents; its legacy integer Amount is only display metadata.
		if math.IsNaN(topUp.Money) || math.IsInf(topUp.Money, 0) {
			return 0, errors.New("充值金额无效")
		}
		quota = decimal.NewFromFloat(topUp.Money)
		fallthrough
	default:
		if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
			return 0, errors.New("充值额度换算配置无效")
		}
		quota = quota.Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	quota = quota.Truncate(0)
	// Check decimal bounds before IntPart: IntPart itself wraps above int64.
	if quota.LessThan(decimal.NewFromInt(1)) || quota.GreaterThan(decimal.NewFromInt(int64(common.MaxWalletQuota))) {
		return 0, errors.New("充值额度无效")
	}
	return common.WalletQuotaFromDecimalStrict(quota)
}

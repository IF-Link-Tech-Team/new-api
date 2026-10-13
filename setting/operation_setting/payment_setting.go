package operation_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

type PaymentSetting struct {
	AmountOptions  []int           `json:"amount_options"`
	AmountDiscount map[int]float64 `json:"amount_discount"` // 充值金额对应的折扣，例如 100 元 0.9 表示 100 元充值享受 9 折优惠

	ComplianceConfirmed    bool   `json:"compliance_confirmed"`
	ComplianceTermsVersion string `json:"compliance_terms_version"`
	ComplianceConfirmedAt  int64  `json:"compliance_confirmed_at"`
	ComplianceConfirmedBy  int    `json:"compliance_confirmed_by"`
	ComplianceConfirmedIP  string `json:"compliance_confirmed_ip"`

	// IFLinkPay 临时跳转第三方支付页:secret 由对方线下提供,为空则通道不启用
	IFLinkPaySecret  string `json:"iflink_pay_secret"`
	IFLinkPayBaseURL string `json:"iflink_pay_base_url"`
}

const CurrentComplianceTermsVersion = "v1"

// DefaultIFLinkPayBaseURL 是 IFLinkPay 支付页地址的出厂默认值,
// 管理员可在后台通过 payment_setting.iflink_pay_base_url 覆盖。
const DefaultIFLinkPayBaseURL = "https://www.aha-idea.com/pay/iflink"

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:    []int{10, 20, 50, 100, 200, 500},
	AmountDiscount:   map[int]float64{},
	IFLinkPayBaseURL: DefaultIFLinkPayBaseURL,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return &paymentSetting
}

func IsPaymentComplianceConfirmed() bool {
	return paymentSetting.ComplianceConfirmed &&
		paymentSetting.ComplianceTermsVersion == CurrentComplianceTermsVersion
}

// GetIFLinkPaySecret 返回 IFLinkPay 签名密钥(空表示通道未启用)。
func GetIFLinkPaySecret() string {
	return paymentSetting.IFLinkPaySecret
}

// GetIFLinkPayBaseURL 返回 IFLinkPay 支付页地址;配置为空时回退到默认值。
func GetIFLinkPayBaseURL() string {
	base := strings.TrimSpace(paymentSetting.IFLinkPayBaseURL)
	if base == "" {
		return DefaultIFLinkPayBaseURL
	}
	return strings.TrimRight(base, "/")
}

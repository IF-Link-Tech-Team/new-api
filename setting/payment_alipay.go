package setting

// Alipay (支付宝) payment provider configuration.
// 凭据由 operation_setting 通过 SaveAlipayConfig / paymentComplianceConfig 注入;
// 旧版兼容从 options 表读取并迁移到此处的逻辑见 model/payment_config.go。
//
// MVP 集成:仅支持"手机网站支付"(alipay.trade.wap.pay)和"电脑网站支付"
// (alipay.trade.page.pay)两条主链路;扫码支付/Car/小程序支付不在 MVP 范围。
//
// 签名方式:默认 SHA256withRSA(RSA2)。
// Notify 验签:使用支付宝公钥校验 sign 参数。

var (
	// AlipayAppID 支付宝开放平台应用 AppID (16 位数字)
	AlipayAppID = ""

	// AlipayPrivateKey 应用私钥 (RSA2, PKCS8, PEM 格式), 用于 iflink 签名支付请求
	AlipayPrivateKey = ""

	// AlipayPublicKey 支付宝公钥 (从开放平台下载), 用于 iflink 验签支付宝 notify
	AlipayPublicKey = ""

	// AlipayNotifyURL 异步通知 URL(由 code 拼接,不直接配置)
	// e.g. https://token.iflink.tech/api/topup/alipay/notify (通用 topup)
	//      https://token.iflink.tech/api/subscription/alipay/notify (订阅)

	// AlipayReturnURL 同步跳转 URL (用户在支付宝付款成功后浏览器跳转)
	AlipayReturnURL = ""

	// AlipayGateway 支付宝网关,默认是开放平台网关
	AlipayGateway = "https://openapi.alipay.com/gateway.do"

	// AlipaySandboxGateway 沙盒网关(MVP 默认走沙盒,零金额风险)
	AlipaySandboxGateway = "https://openapi.alipaydev.com/gateway.do"

	// AlipaySandbox 是否使用沙盒网关
	// MVP 默认 true,真实跑通后再切到 false
	AlipaySandbox = true

	// AlipayMinTopUp 最小充值金额(单位:元),低于此金额禁止下单
	AlipayMinTopUp = 1.0

	// AlipayUnitPrice 充值单位(元/quota 兑换率反向使用),保留兼容老支付
	AlipayUnitPrice = 8.0
)
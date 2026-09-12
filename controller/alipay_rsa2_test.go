package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
)

// 真实沙箱异步通知报文(trade_status=TRADE_SUCCESS, 2026-09-11 19:02:59)
// out_trade_no = TU-ALI-JVKvLkXp-1789124390
const realSandboxNotify = "gmt_create=2026-09-11+19%3A02%3A45&charset=utf-8&gmt_payment=2026-09-11+19%3A02%3A57" +
	"&notify_time=2026-09-11+19%3A02%3A59&subject=IF.Link+%E9%92%B1%E5%8C%85%E5%85%85%E5%80%BC+-+root" +
	"&sign=RYH3Hm%2FvymbQnx3G5F5pABraiLSNdSQeKuWZAfrDKBeyVB2GY2TkeuInwV1yo%2FoqlXd2WFCjDLggOG%2BBCabkK17tHmj0RbqLKqzQgr10RAAY3jkIJ6jZdquE7H%2FzfXiycnRqR2rTLPT5pQMTNhvKotMKhhTMY%2FzWem1qoVzvKSO0diVxCuPQebwkH0WRHe7cBrBpD0s8amV%2B%2Bu6OV%2BQqXFYomH8BF11Sw2loiHTDI9eB9R94VPg%2BJuzjf2qvrrCG88%2FSKPwB6zbb46VihQRq3nOTqi%2F64F%2BkXhU4pnfZA23wctdRhtMTTSKrO%2Fl5lrm7fbY5aM2mlWlHF8IfAcpsYw%3D%3D" +
	"&buyer_id=2088722112099654&invoice_amount=0.01&version=1.0" +
	"&notify_id=2026091101222190258199650508977386" +
	"&fund_bill_list=%5B%7B%22amount%22%3A%220.01%22%2C%22fundChannel%22%3A%22ALIPAYACCOUNT%22%7D%5D" +
	"&notify_type=trade_status_sync&out_trade_no=TU-ALI-JVKvLkXp-1789124390&total_amount=0.01" +
	"&trade_status=TRADE_SUCCESS&trade_no=2026091122001499650509008557&auth_app_id=9021000168607391" +
	"&receipt_amount=0.01&point_amount=0.00&buyer_pay_amount=0.01&app_id=9021000168607391" +
	"&sign_type=RSA2&seller_id=2088721112099646"

// 沙箱支付宝公钥(modulus A784507B...)。与 setting.AlipayPublicKey 无关,
// 测试内固定值,便于在无生产配置时回归。
const sandboxAlipayPublicKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAp4RQe75cq7xaFhg4u4Z7
Tc6e9WS9TAEXIduXD9NKIlpZrzhXzSjv/7sMeeN6ZY6QkXx/BXlZlJMXtOOnu/3f
PtXVSthAXa7Cnufnzj8NyhOpId8zOBtSuC11G6GtZwHyFNT4BmcdRwn7Vhp+gmLL
ILE+OtAH7EbpolXfjJ9Dm4B7TBbKEvoIHqAjYX2i5ZNw0u792WlGsTceKXBaLS0v
mlgn0ihb+zwX93fcLzHfUrZtzYMAveJtgtvZrfcoSd3bWQpW8XmdiPFcM5JIK+53
rewRb9pqVHPtjxOmGS7PHviTpQ9Y68IkLrxOC84lkvUX4lC8+gLvF0u5wqXYBEyJ
FwIDAQAB
-----END PUBLIC KEY-----`

// 请求签名口径必须保留 sign_type,否则沙箱网关报 invalid-signature。
func TestAlipaySignParamsKeepsSignType(t *testing.T) {
	got := alipaySignParams(map[string]string{
		"app_id":    "9021000168607391",
		"method":    "alipay.trade.page.pay",
		"sign_type": "RSA2",
		"sign":      "SHOULD_BE_DROPPED",
		"empty":     "",
	})
	want := "app_id=9021000168607391&method=alipay.trade.page.pay&sign_type=RSA2"
	if got != want {
		t.Fatalf("alipaySignParams\n got=%q\nwant=%q", got, want)
	}
}

// 异步通知验签口径必须排除 sign_type,否则真实沙箱通知验签失败。
func TestAlipayNotifySignParamsDropsSignType(t *testing.T) {
	got := alipayNotifySignParams(map[string]string{
		"app_id":    "9021000168607391",
		"method":    "alipay.trade.page.pay",
		"sign_type": "RSA2",
		"sign":      "SHOULD_BE_DROPPED",
	})
	want := "app_id=9021000168607391&method=alipay.trade.page.pay"
	if got != want {
		t.Fatalf("alipayNotifySignParams\n got=%q\nwant=%q", got, want)
	}
}

// 用真实沙箱通知报文回归:验签必须通过,且 trade_status 可解析。
func TestAlipayVerifyNotifyParamsRealSandboxNotify(t *testing.T) {
	orig := setting.AlipayPublicKey
	setting.AlipayPublicKey = sandboxAlipayPublicKey
	defer func() { setting.AlipayPublicKey = orig }()

	params, ok := alipayVerifyNotifyParams(realSandboxNotify)
	if !ok {
		t.Fatal("验签失败:真实沙箱通知未被接受")
	}
	if params["trade_status"] != "TRADE_SUCCESS" {
		t.Fatalf("trade_status = %q, want TRADE_SUCCESS", params["trade_status"])
	}
	if params["out_trade_no"] != "TU-ALI-JVKvLkXp-1789124390" {
		t.Fatalf("out_trade_no = %q", params["out_trade_no"])
	}
	if params["total_amount"] != "0.01" {
		t.Fatalf("total_amount = %q", params["total_amount"])
	}
}

// 篡改任一字段后必须验签失败,确保验签没有被放水。
func TestAlipayVerifyNotifyParamsRejectsTampered(t *testing.T) {
	orig := setting.AlipayPublicKey
	setting.AlipayPublicKey = sandboxAlipayPublicKey
	defer func() { setting.AlipayPublicKey = orig }()

	// 把金额从 0.01 改成 9.99
	tampered := ""
	{
		s := realSandboxNotify
		idx := 0
		for i := 0; i+9 <= len(s); i++ {
			if s[i:i+9] == "total_amo" {
				idx = i
				break
			}
		}
		tampered = s[:idx] + "total_amount=9.99" + s[idx+len("total_amount=0.01"):]
	}
	if _, ok := alipayVerifyNotifyParams(tampered); ok {
		t.Fatal("篡改金额后仍然验签通过 —— 验签存在漏洞")
	}
}

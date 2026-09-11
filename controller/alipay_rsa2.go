package controller

// 支付宝 v2 接口 RSA2 (SHA256withRSA) 签名 + 验签工具
//
// 支付宝开放平台签名规范:
//   1. 把待签名字符串所有参数(除 sign 外)按 key 字典序排序
//   2. 用 & = 拼接成 "k1=v1&k2=v2&..." 格式
//   3. 用应用私钥 SHA256withRSA 签名,结果 base64 编码
//   4. notify 验签:同步通知参数同样排序拼成待签字符串,用支付宝公钥 SHA256withRSA 验签

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting"
)

// alipaySignParams 排序后拼接待签字符串,过滤 sign / sign_type / 空值
func alipaySignParams(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, params[k]))
	}
	return strings.Join(parts, "&")
}

// alipaySignRSA2 用应用私钥对字符串做 SHA256withRSA 签名,返回 base64
func alipaySignRSA2(payload string) (string, error) {
	block, _ := pem.Decode([]byte(setting.AlipayPrivateKey))
	if block == nil {
		return "", errors.New("alipay private key PEM decode failed")
	}
	var rsaKey *rsa.PrivateKey
	switch block.Type {
	case "PRIVATE KEY":
		parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("parse PKCS8 private key: %w", err)
		}
		var ok bool
		rsaKey, ok = parsedKey.(*rsa.PrivateKey)
		if !ok {
			return "", errors.New("PKCS8 key is not RSA")
		}
	case "RSA PRIVATE KEY":
		var err error
		rsaKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("parse PKCS1 private key: %w", err)
		}
	default:
		return "", fmt.Errorf("unsupported perm struct block type: %q", block.Type)
	}
	hashed := sha256.Sum256([]byte(payload))
	signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("RSA sign: %w", err)
	}
	return base64.StdEncoding.EncodeToString(signature), nil
}

// alipayVerifyRSA2 用支付宝公钥验签异步通知
func alipayVerifyRSA2(payload string, sign string) bool {
	if sign == "" {
		return false
	}
	block, _ := pem.Decode([]byte(setting.AlipayPublicKey))
	if block == nil {
		return false
	}
	pubInterface, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return false
	}
	pubKey, ok := pubInterface.(*rsa.PublicKey)
	if !ok {
		return false
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sign)
	if err != nil {
		return false
	}
	hashed := sha256.Sum256([]byte(payload))
	return rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hashed[:], sigBytes) == nil
}

// alipayBuildSignedForm 构造一个可直接 POST 到支付宝网关的 application/x-www-form-urlencoded body
// bizContent JSON 字符串需要外部传进来(订单信息)
func alipayBuildSignedForm(method string, bizContent string, notifyURL string, returnURL string, extra map[string]string) (string, error) {
	params := map[string]string{
		"app_id":      setting.AlipayAppID,
		"method":      method,
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"notify_url":  notifyURL,
		"return_url":  returnURL,
		"biz_content": bizContent,
	}
	for k, v := range extra {
		params[k] = v
	}
	payload := alipaySignParams(params)
	sign, err := alipaySignRSA2(payload)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	params["sign"] = sign
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	values := url.Values{}
	for _, k := range keys {
		values.Set(k, params[k])
	}
	return values.Encode(), nil
}

// alipayVerifyNotifyParams 校验异步通知合法性:从原始 form-encoded body 提取所有参数,
// 移除 sign / sign_type 后按字典序拼成待签字符串,用支付宝公钥验证 sign。
func alipayVerifyNotifyParams(formRaw string) (map[string]string, bool) {
	parsed, err := url.ParseQuery(formRaw)
	if err != nil {
		return nil, false
	}
	params := make(map[string]string, len(parsed))
	for k, v := range parsed {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	sign := params["sign"]
	if sign == "" {
		return nil, false
	}
	payload := alipaySignParams(params)
	if !alipayVerifyRSA2(payload, sign) {
		return nil, false
	}
	return params, true
}

// alipayGateway 决定实际网关(sandbox / production)
func alipayGateway() string {
	if setting.AlipaySandbox {
		return setting.AlipaySandboxGateway
	}
	return setting.AlipayGateway
}

// alipayCall 通用 HTTP POST 调用支付宝网关(alipay.trade.* 系列接口)
//
//   - method: 接口名(alipay.trade.refund, alipay.trade.query 等)
//   - bizContent: 业务参数 JSON 字符串
//
// 返回:支付宝返回的整个 HTTP body(通常为 URL-encoded form 含支付宝响应字段)
func alipayCall(method string, bizContent string) (string, error) {
	params := map[string]string{
		"app_id":      setting.AlipayAppID,
		"method":      method,
		"charset":     "utf-8",
		"sign_type":   "RSA2",
		"timestamp":   time.Now().Format("2006-01-02 15:04:05"),
		"version":     "1.0",
		"biz_content": bizContent,
	}
	payload := alipaySignParams(params)
	sign, err := alipaySignRSA2(payload)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	params["sign"] = sign

	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	resp, err := http.PostForm(alipayGateway(), values)
	if err != nil {
		return "", fmt.Errorf("HTTP POST: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	return string(body), nil
}

// alipayParseResponse 解析支付宝公共返回的 URL-encoded form response
// 例: app_cert_sn=...&charset=utf-8&code=10000&msg=Success&...
func alipayParseResponse(body string) map[string]string {
	parsed, err := url.ParseQuery(body)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(parsed))
	for k, v := range parsed {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}
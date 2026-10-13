package controller

// 支付宝 v2 高级 API 调用封装
// - 退款: alipay.trade.refund
// - 退款查询: alipay.trade.fastpay.refund.query
// - 交易查询: alipay.trade.query
// - 关闭未支付订单: alipay.trade.close
//
// 所有调用走 alipayCall + alipayParseResponse(共享 RSA2 加签 / 解析)
//
// 退款流程参考:
//   1. 调 alipay.trade.refund 发退款请求
//   2. 等异步通知 alipay.trade.refund.depositback.completed (银行卡) 或 立即返回
//   3. 调 alipay.trade.fastpay.refund.query 查询退款状态

import (
	"encoding/json"
	"fmt"
)

// AlipayRefundResult 退款结果
type AlipayRefundResult struct {
	Code          string `json:"code"`           // 10000=成功
	Msg           string `json:"msg"`
	SubCode       string `json:"sub_code,omitempty"`
	SubMsg        string `json:"sub_msg,omitempty"`
	TradeNo       string `json:"trade_no"`         // 支付宝交易号
	OutTradeNo    string `json:"out_trade_no"`     // 商户订单号
	BuyerLogonId  string `json:"buyer_logon_id,omitempty"`
	FundChange    string `json:"fund_change,omitempty"` // 退款金额(元,两位小数)
	RefundFee     string `json:"refund_fee,omitempty"`
	GmtRefundPay  string `json:"gmt_refund_pay,omitempty"`
	RefundOrderId string `json:"refund_order_id,omitempty"`
}

// AlipayTradeQueryResult 交易查询结果
type AlipayTradeQueryResult struct {
	Code        string `json:"code"`
	Msg         string `json:"msg"`
	SubCode     string `json:"sub_code,omitempty"`
	SubMsg      string `json:"sub_msg,omitempty"`
	TradeNo     string `json:"trade_no"`
	OutTradeNo  string `json:"out_trade_no"`
	TradeStatus string `json:"trade_status"` // TRADE_CLOSED / TRADE_SUCCESS / TRADE_FINISHED / WAIT_BUYER_PAY
	TotalAmount string `json:"total_amount"`
	BuyerLogonId string `json:"buyer_logon_id,omitempty"`
}

// AlipayRefund 主动退款
//   - outTradeNo: 商户订单号(iflink.top_ups.trade_no / subscription_orders.trade_no)
//   - refundAmount: 退款金额(元,两位小数), 为 "" 时全额退款
//   - refundReason: 退款原因(展示给买家)
//   - outRequestNo: 商户退款请求号, 用于幂等; 为空时填 out_trade_no
func AlipayRefund(outTradeNo string, refundAmount string, refundReason string, outRequestNo string) (*AlipayRefundResult, error) {
	if outRequestNo == "" {
		outRequestNo = outTradeNo
	}
	biz := map[string]string{
		"out_trade_no":   outTradeNo,
		"refund_amount":  refundAmount, // 空 = 全额退款
		"refund_reason":  refundReason,
		"out_request_no": outRequestNo,
	}
	bizJSON, _ := json.Marshal(biz)
	body, err := alipayCall("alipay.trade.refund", string(bizJSON))
	if err != nil {
		return nil, err
	}
	resp := alipayParseResponse(body)
	out := &AlipayRefundResult{
		Code:         resp["code"],
		Msg:          resp["msg"],
		SubCode:      resp["sub_code"],
		SubMsg:       resp["sub_msg"],
		TradeNo:      resp["trade_no"],
		OutTradeNo:   resp["out_trade_no"],
		BuyerLogonId: resp["buyer_logon_id"],
		FundChange:   resp["fund_change"],
		RefundFee:    resp["refund_fee"],
	}
	if out.Code != "10000" {
		return out, fmt.Errorf("alipay refund failed code=%s msg=%s sub_code=%s sub_msg=%s", out.Code, out.Msg, out.SubCode, out.SubMsg)
	}
	return out, nil
}

// AlipayRefundQuery 查询退款状态(幂等性确认)
func AlipayRefundQuery(outTradeNo string, outRequestNo string) (*AlipayRefundResult, error) {
	if outRequestNo == "" {
		outRequestNo = outTradeNo
	}
	biz := map[string]string{
		"out_trade_no":   outTradeNo,
		"out_request_no": outRequestNo,
	}
	bizJSON, _ := json.Marshal(biz)
	body, err := alipayCall("alipay.trade.fastpay.refund.query", string(bizJSON))
	if err != nil {
		return nil, err
	}
	resp := alipayParseResponse(body)
	return &AlipayRefundResult{
		Code:        resp["code"],
		Msg:         resp["msg"],
		SubCode:     resp["sub_code"],
		SubMsg:      resp["sub_msg"],
		TradeNo:     resp["trade_no"],
		OutTradeNo:  resp["out_trade_no"],
		GmtRefundPay: resp["gmt_refund_pay"],
		RefundOrderId: resp["refund_order_id"],
	}, nil
}

// AlipayTradeQuery 查订单
func AlipayTradeQuery(outTradeNo string) (*AlipayTradeQueryResult, error) {
	biz := map[string]string{
		"out_trade_no": outTradeNo,
	}
	bizJSON, _ := json.Marshal(biz)
	body, err := alipayCall("alipay.trade.query", string(bizJSON))
	if err != nil {
		return nil, err
	}
	resp := alipayParseResponse(body)
	return &AlipayTradeQueryResult{
		Code:         resp["code"],
		Msg:          resp["msg"],
		SubCode:      resp["sub_code"],
		SubMsg:       resp["sub_msg"],
		TradeNo:      resp["trade_no"],
		OutTradeNo:   resp["out_trade_no"],
		TradeStatus:  resp["trade_status"],
		TotalAmount:  resp["total_amount"],
		BuyerLogonId: resp["buyer_logon_id"],
	}, nil
}

// AlipayCloseOrder 关闭未支付订单(用户超时未付)
func AlipayCloseOrder(outTradeNo string) error {
	biz := map[string]string{
		"out_trade_no": outTradeNo,
	}
	bizJSON, _ := json.Marshal(biz)
	body, err := alipayCall("alipay.trade.close", string(bizJSON))
	if err != nil {
		return err
	}
	resp := alipayParseResponse(body)
	if resp["code"] != "10000" {
		return fmt.Errorf("alipay close failed code=%s msg=%s", resp["code"], resp["msg"])
	}
	return nil
}
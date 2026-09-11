package controller

// 支付宝对账 API + 对账流程
//
// 流程:
//   1. 调 alipay.data.dataservice.bill.downloadurl.query 拿到对账单 ZIP 下载 URL(URL 24 小时有效)
//   2. HTTP GET 下载 ZIP, 解压得 trade/merchant 两种账单
//   3. 解析 CSV (GBK 编码, 需 iconv) → 跟 iflink top_ups / subscription_orders 对账
//
// MVP 阶段仅提供 API + ZIP 解压入口,完整对账 cron job 留给 ops 写。

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AlipayBillDownloadURLResult alipay.data.dataservice.bill.downloadurl.query 响应
type AlipayBillDownloadURLResult struct {
	Code           string `json:"code"`            // 10000=成功
	Msg            string `json:"msg"`
	BillDownloadURL string `json:"bill_download_url"` // ZIP 下载 URL, 24 小时有效
	BillFileName   string `json:"bill_file_name"`   // e.g. 208810112474xxxx-20260830.zip
}

// AlipayQueryBillDownloadURL 查对账单下载链接
//   - billType: trade(交易) / signcustomer(客户账单) 详见 alipay docs
//   - billDate: yyyy-MM-dd 形式
func AlipayQueryBillDownloadURL(billType string, billDate string) (*AlipayBillDownloadURLResult, error) {
	biz := map[string]string{
		"bill_type": billType,
		"bill_date": billDate,
	}
	body, err := alipayCallJSON("alipay.data.dataservice.bill.downloadurl.query", biz)
	if err != nil {
		return nil, err
	}
	resp := alipayParseResponse(body)
	return &AlipayBillDownloadURLResult{
		Code:            resp["code"],
		Msg:             resp["msg"],
		BillDownloadURL: resp["bill_download_url"],
		BillFileName:    resp["bill_file_name"],
	}, nil
}

// AlipayDownloadAndExtractZIP 下载对账单 ZIP 并解压到内存
// 返回 key=文件名(不含路径), value=ZIP 内文件内容
func AlipayDownloadAndExtractZIP(downloadURL string) (map[string][]byte, error) {
	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Get(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("download bill: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("bill http status %d", resp.StatusCode)
	}
	zipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read zip body: %w", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	out := make(map[string][]byte, len(reader.File))
	for _, f := range reader.File {
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read zip entry %s: %w", f.Name, err)
		}
		out[f.Name] = data
	}
	return out, nil
}

// alipayCallJSON 共享 helper(避免重复 JSON marshal)
func alipayCallJSON(method string, biz map[string]string) (string, error) {
	bizJSON, err := json.Marshal(biz)
	if err != nil {
		return "", err
	}
	return alipayCall(method, string(bizJSON))
}
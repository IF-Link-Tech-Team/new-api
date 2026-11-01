package service

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// IFLinkPay payment verification.
//
// The legacy integration accepted a signature that travelled through the
// browser: the same md5 appeared on the payment link and in the callback, so a
// payer could replay it to acknowledge an unpaid order. This file implements the
// replacement path: the backend itself asks the provider what happened.
//
// Trust model:
//   * The payment-link signature (md5) is used ONLY to create a payment request.
//     It is never treated as evidence that money moved.
//   * The provider returns an opaque server-side session; it is held in backend
//     memory and never sent to the browser.
//   * The provider's own status endpoint reports the payment state. Only its
//     explicit PAID result, matching the local order's trade number and amount,
//     can produce a settlement receipt.

// IFLinkPay status values reported by the provider's cashier contract.
const (
	IFLinkPayStatusPrepay    = "PREPAY"
	IFLinkPayStatusPaid      = "PAID"
	IFLinkPayStatusVerifying = "VERIFYING"
	IFLinkPayStatusExpired   = "EXPIRED"
	IFLinkPayStatusClosed    = "CLOSED"
	IFLinkPayStatusFailed    = "FAILED"
)

var (
	ErrIFLinkPayQueryUnavailable = errors.New("IFLinkPay payment verification is unavailable")
	ErrIFLinkPayNotPaid          = errors.New("IFLinkPay payment is not confirmed as paid")
	ErrIFLinkPayOrderMismatch    = errors.New("IFLinkPay verification does not match this order")
	ErrIFLinkPayAmountMismatch   = errors.New("IFLinkPay verified amount does not match this order")
)

// IFLinkPayPayment is the provider's view of one payment. Field names follow the
// provider's own cashier payload.
type IFLinkPayPayment struct {
	Status             string `json:"status"`
	Amount             int64  `json:"amount"` // fen
	Order              string `json:"order"`
	ReturnURL          string `json:"return_url"`
	ExpiresAt          string `json:"expires_at"`
	NotificationStatus string `json:"notification_status"`
	// ChannelTradeNo is the provider-side transaction identifier when the
	// provider exposes one. It is empty when the provider only tracks our order
	// number, in which case the order number is the single-use key.
	ChannelTradeNo string `json:"trade_no"`
}

type ifLinkPayCheckoutResponse struct {
	Session string           `json:"session"`
	Payment IFLinkPayPayment `json:"payment"`
}

// IFLinkPayVerifier performs server-side payment verification. Both fields are
// exported through the constructor below so tests can point it at a local fake
// provider; production always uses the configured provider origin.
type IFLinkPayVerifier struct {
	APIBaseURL string
	HTTPClient *http.Client
}

// NewIFLinkPayVerifier builds a verifier bound to the configured provider origin.
func NewIFLinkPayVerifier(apiBaseURL string) *IFLinkPayVerifier {
	return &IFLinkPayVerifier{
		APIBaseURL: strings.TrimRight(strings.TrimSpace(apiBaseURL), "/"),
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// IFLinkPayLinkSign reproduces the provider's payment-request signature:
// md5(uid + amountFen + order + ts + secret), lowercase hex.
//
// This authenticates ONLY the creation of a payment request. A value produced
// here must never be accepted as proof that the payment succeeded.
func IFLinkPayLinkSign(uid string, amountFen int64, order string, ts int64, secret string) string {
	raw := uid + strconv.FormatInt(amountFen, 10) + order + strconv.FormatInt(ts, 10) + secret
	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// OpenSession creates a provider-side payment session for an order. The returned
// token authorises reading that order's status and is kept server-side.
func (v *IFLinkPayVerifier) OpenSession(order string, amountFen int64, returnURL string, linkSecret string) (string, IFLinkPayPayment, error) {
	if v == nil || v.APIBaseURL == "" {
		return "", IFLinkPayPayment{}, ErrIFLinkPayQueryUnavailable
	}
	if strings.TrimSpace(order) == "" || amountFen <= 0 || strings.TrimSpace(linkSecret) == "" {
		return "", IFLinkPayPayment{}, ErrIFLinkPayQueryUnavailable
	}

	ts := time.Now().Unix()
	// The provider's contract calls this field uid, but it is an opaque external
	// reference; the order number is used so no local user id is exposed.
	body, err := common.Marshal(map[string]any{
		"uid":        order,
		"amount":     strconv.FormatInt(amountFen, 10),
		"order":      order,
		"ts":         strconv.FormatInt(ts, 10),
		"sign":       IFLinkPayLinkSign(order, amountFen, order, ts, linkSecret),
		"return_url": returnURL,
	})
	if err != nil {
		return "", IFLinkPayPayment{}, err
	}

	var out ifLinkPayCheckoutResponse
	status, err := v.doJSON(http.MethodPost, "/api/pay/iflink/checkout", body, "", &out)
	if err != nil {
		return "", IFLinkPayPayment{}, err
	}
	if status != http.StatusOK || strings.TrimSpace(out.Session) == "" {
		return "", IFLinkPayPayment{}, fmt.Errorf("%w: checkout did not return a session", ErrIFLinkPayQueryUnavailable)
	}
	return out.Session, out.Payment, nil
}

// FetchPayment reads the provider-verified state of the session's order.
func (v *IFLinkPayVerifier) FetchPayment(session string) (IFLinkPayPayment, error) {
	if v == nil || v.APIBaseURL == "" || strings.TrimSpace(session) == "" {
		return IFLinkPayPayment{}, ErrIFLinkPayQueryUnavailable
	}
	var payment IFLinkPayPayment
	status, err := v.doJSON(http.MethodGet, "/api/pay/iflink/status", nil, session, &payment)
	if err != nil {
		return IFLinkPayPayment{}, err
	}
	if status != http.StatusOK {
		return IFLinkPayPayment{}, fmt.Errorf("%w: status returned HTTP %d", ErrIFLinkPayQueryUnavailable, status)
	}
	return payment, nil
}

// VerifyPaid is the only function permitted to authorise settlement. It returns
// the verified payment only when the provider reports PAID and both the order
// number and the amount match the immutable local order exactly.
//
// Every other outcome (network failure, unknown status, mismatch, malformed
// body) is an error, so callers must leave the order unconfirmed.
func (v *IFLinkPayVerifier) VerifyPaid(session string, expectedOrder string, expectedAmountFen int64) (IFLinkPayPayment, error) {
	payment, err := v.FetchPayment(session)
	if err != nil {
		return IFLinkPayPayment{}, err
	}

	status := strings.ToUpper(strings.TrimSpace(payment.Status))
	if status != IFLinkPayStatusPaid {
		// PREPAY/VERIFYING are not failures, but neither is payment proof.
		return IFLinkPayPayment{}, fmt.Errorf("%w: provider status is %q", ErrIFLinkPayNotPaid, payment.Status)
	}
	if strings.TrimSpace(payment.Order) != strings.TrimSpace(expectedOrder) {
		return IFLinkPayPayment{}, ErrIFLinkPayOrderMismatch
	}
	if payment.Amount != expectedAmountFen {
		return IFLinkPayPayment{}, ErrIFLinkPayAmountMismatch
	}
	return payment, nil
}

// doJSON performs one provider call and decodes a JSON body into out.
func (v *IFLinkPayVerifier) doJSON(method, path string, body []byte, session string, out any) (int, error) {
	endpoint := v.APIBaseURL + path
	if _, err := url.Parse(endpoint); err != nil {
		return 0, fmt.Errorf("%w: invalid provider URL", ErrIFLinkPayQueryUnavailable)
	}

	var reader *strings.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	var req *http.Request
	var err error
	if reader != nil {
		req, err = http.NewRequest(method, endpoint, reader)
	} else {
		req, err = http.NewRequest(method, endpoint, nil)
	}
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		req.Header.Set("Authorization", "Bearer "+session)
	}

	client := v.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrIFLinkPayQueryUnavailable, err)
	}
	defer resp.Body.Close()

	// A non-200 body is diagnostic only; never decode it as a payment result.
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	if err := common.DecodeJson(resp.Body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("%w: malformed provider response", ErrIFLinkPayQueryUnavailable)
	}
	return resp.StatusCode, nil
}

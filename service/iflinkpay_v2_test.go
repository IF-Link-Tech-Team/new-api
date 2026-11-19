package service

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeTestJSON decodes a request body into v for the fake provider.
func decodeTestJSON(r *http.Request, v any) error {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// writeTestJSON writes v as a JSON response for the fake provider.
func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// fakeIFLinkPay emulates the provider's cashier contract:
//
//	POST /api/pay/iflink/checkout -> {session, payment}
//	GET  /api/pay/iflink/status   -> payment  (requires Bearer session)
//
// It lets the verification path be exercised without touching the real provider.
type fakeIFLinkPay struct {
	mu         sync.Mutex
	payments   map[string]IFLinkPayPayment // session -> payment
	linkSecret string
	// rejectCheckoutSign makes the fake refuse payment-request signatures, to
	// prove that a wrong link secret cannot open a session.
	rejectCheckoutSign bool
}

func newFakeIFLinkPay(secret string) *fakeIFLinkPay {
	return &fakeIFLinkPay{payments: map[string]IFLinkPayPayment{}, linkSecret: secret}
}

func (f *fakeIFLinkPay) setPayment(session string, p IFLinkPayPayment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payments[session] = p
}

func (f *fakeIFLinkPay) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/pay/iflink/checkout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			UID    string `json:"uid"`
			Amount string `json:"amount"`
			Order  string `json:"order"`
			TS     string `json:"ts"`
			Sign   string `json:"sign"`
		}
		if err := decodeTestJSON(r, &body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Reproduce the provider's server-side check of the payment-request sign.
		amountFen, err := strconv.ParseInt(body.Amount, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ts, err := strconv.ParseInt(body.TS, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		expected := md5.Sum([]byte(body.UID + strconv.FormatInt(amountFen, 10) +
			body.Order + strconv.FormatInt(ts, 10) + f.linkSecret))
		if hex.EncodeToString(expected[:]) != body.Sign || f.rejectCheckoutSign {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"IFLINK_PAY_SESSION_INVALID"}`))
			return
		}

		session := "sess-" + body.Order
		f.setPayment(session, IFLinkPayPayment{
			Status: IFLinkPayStatusPrepay,
			Amount: amountFen,
			Order:  body.Order,
		})
		writeTestJSON(w, map[string]any{
			"session": session,
			"payment": f.payments[session],
		})
	})

	mux.HandleFunc("/api/pay/iflink/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		token := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(token) <= len(prefix) || token[:len(prefix)] != prefix {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"IFLINK_PAY_SESSION_INVALID"}`))
			return
		}
		f.mu.Lock()
		payment, ok := f.payments[token[len(prefix):]]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"IFLINK_PAY_SESSION_INVALID"}`))
			return
		}
		writeTestJSON(w, payment)
	})

	return mux
}

func TestIFLinkPayVerifyPaidRequiresProviderConfirmation(t *testing.T) {
	const secret = "link-secret-for-tests"
	const order = "TU-IFP-order-1"
	const amountFen int64 = 149

	cases := []struct {
		name     string
		status   string
		amount   int64
		order    string
		wantErr  error
		wantPaid bool
	}{
		{name: "provider confirms paid", status: IFLinkPayStatusPaid, amount: amountFen, order: order, wantPaid: true},
		{name: "unpaid prepay is not payment proof", status: IFLinkPayStatusPrepay, amount: amountFen, order: order, wantErr: ErrIFLinkPayNotPaid},
		{name: "verifying is not payment proof", status: IFLinkPayStatusVerifying, amount: amountFen, order: order, wantErr: ErrIFLinkPayNotPaid},
		{name: "expired is not payment proof", status: IFLinkPayStatusExpired, amount: amountFen, order: order, wantErr: ErrIFLinkPayNotPaid},
		{name: "closed is not payment proof", status: IFLinkPayStatusClosed, amount: amountFen, order: order, wantErr: ErrIFLinkPayNotPaid},
		{name: "failed stays unconfirmed", status: IFLinkPayStatusFailed, amount: amountFen, order: order, wantErr: ErrIFLinkPayNotPaid},
		{name: "unknown status stays unconfirmed", status: "SOMETHING_NEW", amount: amountFen, order: order, wantErr: ErrIFLinkPayNotPaid},
		{name: "amount mismatch rejected", status: IFLinkPayStatusPaid, amount: amountFen + 1, order: order, wantErr: ErrIFLinkPayAmountMismatch},
		{name: "underpayment rejected", status: IFLinkPayStatusPaid, amount: 1, order: order, wantErr: ErrIFLinkPayAmountMismatch},
		{name: "order mismatch rejected", status: IFLinkPayStatusPaid, amount: amountFen, order: "TU-IFP-someone-else", wantErr: ErrIFLinkPayOrderMismatch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeIFLinkPay(secret)
			srv := httptest.NewServer(fake.handler())
			defer srv.Close()

			v := NewIFLinkPayVerifier(srv.URL)
			session, _, err := v.OpenSession(order, amountFen, "https://token.iflink.tech/wallet", secret)
			require.NoError(t, err)
			require.NotEmpty(t, session)

			// The provider's own view is what the attacker must not be able to fake.
			fake.setPayment(session, IFLinkPayPayment{
				Status: tc.status, Amount: tc.amount, Order: tc.order,
			})

			payment, err := v.VerifyPaid(session, order, amountFen)
			if tc.wantPaid {
				require.NoError(t, err)
				assert.Equal(t, IFLinkPayStatusPaid, payment.Status)
				assert.Equal(t, order, payment.Order)
				assert.Equal(t, amountFen, payment.Amount)
				return
			}
			require.Error(t, err)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			}
			assert.Empty(t, payment, "no receipt may be produced without provider confirmation")
		})
	}
}

func TestIFLinkPayVerifyRejectsBogusSession(t *testing.T) {
	srv := httptest.NewServer(newFakeIFLinkPay("s").handler())
	defer srv.Close()

	v := NewIFLinkPayVerifier(srv.URL)
	for _, token := range []string{"", "bogus", "sess-TU-IFP-unknown"} {
		_, err := v.VerifyPaid(token, "TU-IFP-order-1", 149)
		require.Error(t, err, "session %q must not verify", token)
	}
}

func TestIFLinkPayOpenSessionRejectsWrongLinkSecret(t *testing.T) {
	const secret = "correct-link-secret"
	fake := newFakeIFLinkPay(secret)
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	v := NewIFLinkPayVerifier(srv.URL)
	_, _, err := v.OpenSession("TU-IFP-order-2", 1000, "https://token.iflink.tech/wallet", "wrong-secret")
	require.Error(t, err, "a wrong payment-link secret must not open a session")
	assert.ErrorIs(t, err, ErrIFLinkPayQueryUnavailable)
}

func TestIFLinkPayVerifyFailsClosedWhenProviderUnavailable(t *testing.T) {
	// Provider down entirely.
	srv := httptest.NewServer(newFakeIFLinkPay("s").handler())
	baseURL := srv.URL
	srv.Close()

	v := NewIFLinkPayVerifier(baseURL)
	_, err := v.VerifyPaid("sess-anything", "TU-IFP-order-3", 100)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIFLinkPayQueryUnavailable,
		"an unreachable provider must never be read as a successful payment")

	// No base URL configured at all.
	empty := NewIFLinkPayVerifier("")
	_, err = empty.VerifyPaid("sess", "TU-IFP-order-3", 100)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIFLinkPayQueryUnavailable)
}

func TestIFLinkPayVerifyRejectsReplayedSessionForAnotherOrder(t *testing.T) {
	// A payer legitimately paid order A and tries to reuse that session to
	// settle order B. The provider reports order A, so B must stay unconfirmed.
	const secret = "link-secret-replay"
	const orderA = "TU-IFP-paid-order"
	const orderB = "TU-IFP-attacker-order"
	const amountFen int64 = 30000

	fake := newFakeIFLinkPay(secret)
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	v := NewIFLinkPayVerifier(srv.URL)
	sessionA, _, err := v.OpenSession(orderA, amountFen, "", secret)
	require.NoError(t, err)
	fake.setPayment(sessionA, IFLinkPayPayment{
		Status: IFLinkPayStatusPaid, Amount: amountFen, Order: orderA,
	})

	// Replaying order A's session against order B must be rejected.
	_, err = v.VerifyPaid(sessionA, orderB, amountFen)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIFLinkPayOrderMismatch)

	// The same session against its own order is legitimate (idempotent read).
	payment, err := v.VerifyPaid(sessionA, orderA, amountFen)
	require.NoError(t, err)
	assert.Equal(t, orderA, payment.Order)
}

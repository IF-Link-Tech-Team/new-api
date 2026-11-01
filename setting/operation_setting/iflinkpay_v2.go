package operation_setting

import (
	"os"
	"strings"
)

// IFLinkPay V2 verification credentials.
//
// These authenticate the server-to-server payment verification channel and must
// only ever be supplied through the backend process environment. They are never
// stored in the database, returned by any configuration API, or sent to a
// browser: leaking them would let an attacker forge settlement.
//
// The switch defaults to false so an untouched deployment can never resume
// automatic crediting by accident.
const (
	ifLinkPayV2EnabledEnv        = "IFLINK_PAY_V2_ENABLED"
	ifLinkPayQueryKeyIDEnv       = "IFLINK_PAY_QUERY_KEY_ID"
	ifLinkPayQuerySecretEnv      = "IFLINK_PAY_QUERY_SECRET"
	ifLinkPayCallbackKeyIDEnv    = "IFLINK_PAY_CALLBACK_KEY_ID"
	ifLinkPayCallbackSecretEnv   = "IFLINK_PAY_CALLBACK_SECRET"
	ifLinkPayClientIDEnv         = "IFLINK_PAY_CLIENT_ID"
	ifLinkPayV2APIBaseURLEnv     = "IFLINK_PAY_API_BASE_URL"
	ifLinkPayLinkSecretEnv       = "IFLINK_PAY_SECRET"
	defaultIFLinkPayV2APIBaseURL = "https://www.aha-idea.com"
)

// ifLinkPayV2Env returns the trimmed value of a backend-only credential env var.
func ifLinkPayV2Env(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

// IsIFLinkPayV2Enabled reports whether the verified-payment path was explicitly
// switched on. Anything other than a clear affirmative keeps it off.
func IsIFLinkPayV2Enabled() bool {
	switch strings.ToLower(ifLinkPayV2Env(ifLinkPayV2EnabledEnv)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// GetIFLinkPayV2APIBaseURL returns the provider origin used for server-side
// payment verification. The public cashier URL is a different setting because
// it carries a path.
func GetIFLinkPayV2APIBaseURL() string {
	base := ifLinkPayV2Env(ifLinkPayV2APIBaseURLEnv)
	if base == "" {
		base = defaultIFLinkPayV2APIBaseURL
	}
	return strings.TrimRight(base, "/")
}

// IFLinkPayV2Credentials is the backend-only credential set for the verification
// channel. It is populated from the process environment only.
type IFLinkPayV2Credentials struct {
	ClientID       string
	QueryKeyID     string
	QuerySecret    string
	CallbackKeyID  string
	CallbackSecret string
}

// GetIFLinkPayV2Credentials reads the verification credentials from the backend
// environment. Callers must treat an incomplete set as "verification
// unavailable" and fail closed rather than guessing.
func GetIFLinkPayV2Credentials() IFLinkPayV2Credentials {
	return IFLinkPayV2Credentials{
		ClientID:       ifLinkPayV2Env(ifLinkPayClientIDEnv),
		QueryKeyID:     ifLinkPayV2Env(ifLinkPayQueryKeyIDEnv),
		QuerySecret:    ifLinkPayV2Env(ifLinkPayQuerySecretEnv),
		CallbackKeyID:  ifLinkPayV2Env(ifLinkPayCallbackKeyIDEnv),
		CallbackSecret: ifLinkPayV2Env(ifLinkPayCallbackSecretEnv),
	}
}

// IFLinkPayV2QueryReady reports whether the server-side order query can be
// attempted at all. A seat is ready only when the switch is on and the merchant
// identity plus the query credential pair are all present.
func IFLinkPayV2QueryReady() bool {
	if !IsIFLinkPayV2Enabled() {
		return false
	}
	creds := GetIFLinkPayV2Credentials()
	return creds.ClientID != "" && creds.QueryKeyID != "" && creds.QuerySecret != ""
}

// IFLinkPayV2CallbackReady reports whether an independent callback credential
// pair is configured. The legacy shared payment-link secret is deliberately not
// part of this check: it can never authenticate a payment result.
func IFLinkPayV2CallbackReady() bool {
	if !IsIFLinkPayV2Enabled() {
		return false
	}
	creds := GetIFLinkPayV2Credentials()
	return creds.CallbackKeyID != "" && creds.CallbackSecret != ""
}

// GetIFLinkPayLinkSecret returns the secret used to SIGN payment requests.
//
// A backend-only environment value wins over the database setting. The database
// setting is exposed through the admin configuration API, so keeping a rotated
// production secret in the process environment avoids ever serving it from an
// API. When the environment variable is absent the legacy database value is used
// for backward compatibility.
//
// This secret authenticates the CREATION of a payment request only. It is not,
// and must never become, evidence that a payment succeeded.
func GetIFLinkPayLinkSecret() string {
	if fromEnv := ifLinkPayV2Env(ifLinkPayLinkSecretEnv); fromEnv != "" {
		return fromEnv
	}
	return strings.TrimSpace(GetIFLinkPaySecret())
}

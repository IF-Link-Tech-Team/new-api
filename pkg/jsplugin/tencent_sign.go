package jsplugin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Calcium-Ion/moejs"
)

// tencentSigningHeaders signs the exact JSON bytes returned by the driver.
// Credentials never leave this call except as the TC3 authorization signature.
func tencentSigningHeaders(r *moejs.Realm, argument moejs.Value, now time.Time) (moejs.Value, error) {
	if !argument.IsObject() {
		return moejs.Undefined(), r.TypeError("tencentSignTC3 requires an object")
	}
	fields := make(map[string]string, 8)
	for _, name := range []string{"url", "body", "secretId", "secretKey", "action", "version", "region", "token"} {
		value, err := argument.AsObject().GetProp(r, r.KeyFromGoString(name))
		if err != nil {
			return moejs.Undefined(), err
		}
		text, err := stringValue(r, value)
		if err != nil {
			return moejs.Undefined(), err
		}
		fields[name] = text
	}
	u, err := url.Parse(fields["url"])
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return moejs.Undefined(), fmt.Errorf("Tencent signing requires an HTTPS API root")
	}
	for _, name := range []string{"secretId", "secretKey", "action", "version"} {
		if fields[name] == "" || strings.ContainsAny(fields[name], "\r\n") {
			return moejs.Undefined(), fmt.Errorf("Tencent signing requires a valid %s", name)
		}
	}
	for _, name := range []string{"region", "token"} {
		if strings.ContainsAny(fields[name], "\r\n") {
			return moejs.Undefined(), fmt.Errorf("Tencent signing requires a valid %s", name)
		}
	}
	host := strings.ToLower(u.Host)
	service := strings.SplitN(u.Hostname(), ".", 2)[0]
	date := now.UTC().Format("2006-01-02")
	timestamp := strconv.FormatInt(now.Unix(), 10)
	payloadHash := sha256.Sum256([]byte(fields["body"]))
	canonicalHeaders := "content-type:application/json; charset=utf-8\nhost:" + host + "\nx-tc-action:" + strings.ToLower(fields["action"]) + "\n"
	const signedHeaders = "content-type;host;x-tc-action"
	canonical := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + hex.EncodeToString(payloadHash[:])
	canonicalHash := sha256.Sum256([]byte(canonical))
	scope := date + "/" + service + "/tc3_request"
	toSign := "TC3-HMAC-SHA256\n" + timestamp + "\n" + scope + "\n" + hex.EncodeToString(canonicalHash[:])
	sign := func(key []byte, message string) []byte {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(message))
		return mac.Sum(nil)
	}
	key := sign(sign(sign([]byte("TC3"+fields["secretKey"]), date), service), "tc3_request")
	headers := map[string]string{
		"Content-Type":   "application/json; charset=utf-8",
		"X-TC-Action":    fields["action"],
		"X-TC-Version":   fields["version"],
		"X-TC-Timestamp": timestamp,
		"Authorization":  "TC3-HMAC-SHA256 Credential=" + fields["secretId"] + "/" + scope + ", SignedHeaders=" + signedHeaders + ", Signature=" + hex.EncodeToString(sign(key, toSign)),
	}
	if fields["region"] != "" {
		headers["X-TC-Region"] = fields["region"]
	}
	if fields["token"] != "" {
		headers["X-TC-Token"] = fields["token"]
	}
	return r.FromGo(headers)
}

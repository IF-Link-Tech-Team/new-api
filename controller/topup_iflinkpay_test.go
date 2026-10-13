package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// IFLinkPay 签名是与第三方定稿的跨方协议: md5(uid + amount(分) + order + ts + secret),
// 直接拼接无分隔符、UTF-8、32 位小写 hex。这里的期望值由独立工具(md5 命令行)算出,
// 任何拼接顺序/编码改动都会破坏对接。
func TestIFLinkPaySignMatchesProtocolVectors(t *testing.T) {
	cases := []struct {
		name      string
		uid       string
		amountFen int64
		order     string
		ts        int64
		secret    string
		expect    string
	}{
		{
			name:      "basic order",
			uid:       "1",
			amountFen: 500,
			order:     "TU-IFP-ABCDEFGH",
			ts:        1700000000,
			secret:    "topsecret",
			expect:    "5e00acf42c5bd40c7242c246dc0e9793",
		},
		{
			name:      "larger amount different secret",
			uid:       "42",
			amountFen: 9900,
			order:     "ORDER-XYZ-99",
			ts:        1234567890,
			secret:    "another-secret",
			expect:    "fcda4ac208b72b628cdbaa54b3e09a62",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, ifLinkPaySign(tc.uid, tc.amountFen, tc.order, tc.ts, tc.secret))
		})
	}
}

func TestIFLinkPaySignChangesWithEveryField(t *testing.T) {
	base := ifLinkPaySign("1", 500, "TU-IFP-ABCDEFGH", 1700000000, "topsecret")
	assert.NotEqual(t, base, ifLinkPaySign("2", 500, "TU-IFP-ABCDEFGH", 1700000000, "topsecret"))
	assert.NotEqual(t, base, ifLinkPaySign("1", 501, "TU-IFP-ABCDEFGH", 1700000000, "topsecret"))
	assert.NotEqual(t, base, ifLinkPaySign("1", 500, "TU-IFP-ABCDEFGI", 1700000000, "topsecret"))
	assert.NotEqual(t, base, ifLinkPaySign("1", 500, "TU-IFP-ABCDEFGH", 1700000001, "topsecret"))
	assert.NotEqual(t, base, ifLinkPaySign("1", 500, "TU-IFP-ABCDEFGH", 1700000000, "wrong"))
}

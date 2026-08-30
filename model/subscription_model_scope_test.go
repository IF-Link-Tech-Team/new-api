package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModelScopeMatches(t *testing.T) {
	cases := []struct {
		name     string
		scope    string
		model    string
		expected bool
	}{
		{"empty scope matches everything", "", "any-model", true},
		{"blank scope matches everything", "  ", "any-model", true},
		{"exact entry matches", "deepseek-v4-flash,deepseek-v4-pro", "deepseek-v4-pro", true},
		{"prefix entry matches dated variant", "deepseek-v4-flash,deepseek-v4-pro", "deepseek-v4-pro-202606", true},
		{"unrelated model rejected", "deepseek-v4-flash,deepseek-v4-pro", "glm-5.2", false},
		{"prefix without separator still matches", "deepseek-v4", "deepseek-v4-flash", true},
		{"whitespace entries tolerated", " glm-5 , kimi-k2.7-code ", "kimi-k2.7-code", true},
		{"no entry matches empty model", "deepseek-v4", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, modelScopeMatches(tc.scope, tc.model))
		})
	}
}

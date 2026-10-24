package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeLinearTokenPriceMatchesExecution(t *testing.T) {
	for _, expression := range []string{
		`tier("base", p * 2 + cr * 0.2 + cc * 2.5 + c * 8)`,
		`v1:tier("base", 2 * p + p * 3 + cr * 0 + cc * 4 + c * 8) * 0.7`,
		`0.5 * tier("base", (p + cr + cc + c) * 4 / 2)`,
		`tier("base", p * 2 + c * 8 + 0)`,
	} {
		t.Run(expression, func(t *testing.T) {
			price, err := AnalyzeLinearTokenPrice(expression)
			require.NoError(t, err)
			params := TokenParams{P: 123, CR: 57, CC: 39, C: 231}
			actual, _, err := RunExpr(expression, params)
			require.NoError(t, err)
			expected := params.P*price.Coefficients["p"] + params.CR*price.Coefficients["cr"] +
				params.CC*price.Coefficients["cc"] + params.C*price.Coefficients["c"]
			require.InDelta(t, expected, actual, 1e-9)
			require.Equal(t, UsedVars(expression)["cr"], price.UsedVariables["cr"])
		})
	}
}

func TestAnalyzeLinearTokenPriceRejectsUnprovableOrHiddenBranches(t *testing.T) {
	for _, expression := range []string{
		`true ? tier("safe", p * 2) : tier("hidden", p * 0)`,
		`tier("base", p * 2) * (hour("Asia/Shanghai") < 12 ? 0.5 : 1)`,
		`tier("base", p * p)`,
		`tier("base", p * -2)`,
		`tier("base", p * 2 - cr)`,
		`tier("base", max(p, 1))`,
		`tier("base", p / 0)`,
		`tier("base", p * 1e308 * 1e308)`,
		`tier("base", p * 1e-308 * 1e-308)`,
		`tier("base", p + cc1h)`,
		`tier("base", p + img)`,
		`tier("base", p + ai)`,
		`tier("base", fixed(0.01))`,
		`tier("base", p) + tier("extra", c)`,
		`tier("base", tier("nested", p))`,
		`tier("base", p + 1)`,
		`p * 2 + c * 8`,
		`v2:tier("base", p)`,
	} {
		t.Run(expression, func(t *testing.T) {
			_, err := AnalyzeLinearTokenPrice(expression)
			require.Error(t, err)
		})
	}
}

func TestAnalyzeLinearTokenPriceRetainsExplicitFreeCacheLane(t *testing.T) {
	price, err := AnalyzeLinearTokenPrice(`tier("base", p * 2 + cr * 0 + cc * 0 + c * 8)`)
	require.NoError(t, err)
	require.True(t, price.UsedVariables["cr"])
	require.True(t, price.UsedVariables["cc"])
	require.Zero(t, price.Coefficients["cr"])
	require.Zero(t, price.Coefficients["cc"])
}

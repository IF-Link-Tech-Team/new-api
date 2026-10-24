package billingexpr

import (
	"errors"
	"math"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// LinearTokenPrice describes only the deliberately small automatic-verification
// whitelist. It does not change expression execution or infer a price from
// sample requests. Presence matters: an explicit cr * 0 still changes input
// normalization, whereas an omitted cr is charged through p.
type LinearTokenPrice struct {
	Coefficients  map[string]float64
	UsedVariables map[string]bool
}

// AnalyzeLinearTokenPrice accepts a single tier containing non-negative linear
// p/cr/cc/c terms, optionally scaled by constant factors. Conditional branches,
// fixed/request prices and other modalities require separate cost evidence.
// Inspect the original AST so even an optimized-away unsupported branch cannot
// masquerade as a verifiable price.
func AnalyzeLinearTokenPrice(expression string) (*LinearTokenPrice, error) {
	_, body := ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, err
	}
	terms, tiers, err := linearTokenTerms(tree.Node, true)
	if err != nil || tiers != 1 {
		return nil, errors.New("automatic verification requires one non-negative linear four-component token tier")
	}
	if _, err := CompileFromCache(expression); err != nil {
		return nil, err
	}
	used := UsedVars(expression)
	variables := make(map[string]bool, 4)
	for _, name := range []string{"p", "cr", "cc", "c"} {
		variables[name] = used[name]
	}
	return &LinearTokenPrice{Coefficients: terms, UsedVariables: variables}, nil
}

// linearTokenTerms returns coefficients, and the number of price markers.
// Scalar operands are literal-only; no request/time state enters this proof.
func linearTokenTerms(node ast.Node, allowTier bool) (map[string]float64, int, error) {
	switch part := node.(type) {
	case *ast.IdentifierNode:
		if !allowTier && (part.Value == "p" || part.Value == "cr" || part.Value == "cc" || part.Value == "c") {
			return map[string]float64{part.Value: 1}, 0, nil
		}
	case *ast.CallNode:
		callee, ok := part.Callee.(*ast.IdentifierNode)
		if allowTier && ok && callee.Value == "tier" && len(part.Arguments) == 2 {
			if _, literal := part.Arguments[0].(*ast.StringNode); !literal {
				break
			}
			terms, tiers, err := linearTokenTerms(part.Arguments[1], false)
			return terms, tiers + 1, err
		}
	case *ast.BinaryNode:
		if part.Operator == "+" && !allowTier {
			left, _, err := linearTokenTerms(part.Left, false)
			if err != nil {
				return nil, 0, err
			}
			right, _, err := linearTokenTerms(part.Right, false)
			if err != nil {
				return nil, 0, err
			}
			for name, value := range right {
				left[name] += value
				if math.IsInf(left[name], 0) {
					return nil, 0, errors.New("linear price coefficient overflow")
				}
			}
			return left, 0, nil
		}
		if part.Operator == "*" || part.Operator == "/" {
			factor, literal := requestRuleNumber(part.Right)
			base := part.Left
			if !literal && part.Operator == "*" {
				factor, literal = requestRuleNumber(part.Left)
				base = part.Right
			}
			if !literal || factor < 0 || math.IsInf(factor, 0) || math.IsNaN(factor) || part.Operator == "/" && factor == 0 {
				break
			}
			terms, tiers, err := linearTokenTerms(base, allowTier)
			if err != nil {
				return nil, 0, err
			}
			for name, value := range terms {
				if part.Operator == "/" {
					terms[name] = value / factor
				} else {
					terms[name] = value * factor
				}
				if math.IsNaN(terms[name]) || math.IsInf(terms[name], 0) || value > 0 && factor > 0 && terms[name] == 0 {
					return nil, 0, errors.New("linear price coefficient overflow or underflow")
				}
			}
			return terms, tiers, nil
		}
	case *ast.IntegerNode:
		if !allowTier && part.Value == 0 {
			return map[string]float64{}, 0, nil
		}
	case *ast.FloatNode:
		if !allowTier && part.Value == 0 {
			return map[string]float64{}, 0, nil
		}
	}
	return nil, 0, errors.New("expression is outside the automatic cost-verification whitelist")
}

// GATES: the conditions a correlation pass can decide. A gate is a
// deterministic, effect-free test over a stable place — a bare
// binding, a property place, or an equality between such a place
// and a literal — wrapped in any number of negations. ToBoolean of
// such a test is FIXED for a whole run, which is what lets two
// branches keyed by it correlate: assuming it truthy and falsy in
// turn partitions every run, so the two branches' join is exact.
//
// The gate's identity is its CANONICAL key (base symbol + detail
// spelling); the negation parity rides separately, so `if (a)` and
// `if (!a)` test the same gate with opposite verdicts, and
// `kind !== "a"` is the negation of `kind === "a"`.
//
// BLOCKED: gateKeyOf, assumedVerdict, gatesTestedBy, and
// correlationGateOf all need narrowing/condition_tree.ts
// (conditionTreeOf) — not yet ported (dataflow_facts precedes
// narrowing in the port order, and this file's own logic is
// entangled with the shared connective walk). Only literalSpelling,
// which has no such dependency, is ported here.
package dataflowfacts

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
)

// LiteralSpelling is a literal's canonical spelling — shared by equality
// gates and switch case labels, so `if (kind === "up")` and `case "up":`
// name the SAME gate. Nil for anything else.
func LiteralSpelling(e *ast.Node) *string {
	bare := e
	if ast.IsParenthesizedExpression(e) {
		bare = e.AsParenthesizedExpression().Expression
	}
	if ast.IsNumericLiteral(bare) {
		n, err := strconv.ParseFloat(bare.Text(), 64)
		if err == nil {
			spelling := "n:" + strconv.FormatFloat(n, 'f', -1, 64)
			return &spelling
		}
	}
	if ast.IsPrefixUnaryExpression(bare) {
		unary := bare.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			n, err := strconv.ParseFloat(unary.Operand.Text(), 64)
			if err == nil {
				spelling := "n:" + strconv.FormatFloat(-n, 'f', -1, 64)
				return &spelling
			}
		}
	}
	if ast.IsStringLiteral(bare) || ast.IsNoSubstitutionTemplateLiteral(bare) {
		spelling := "s:" + bare.Text()
		return &spelling
	}
	if bare.Kind == ast.KindTrueKeyword {
		spelling := "b:true"
		return &spelling
	}
	if bare.Kind == ast.KindFalseKeyword {
		spelling := "b:false"
		return &spelling
	}
	return nil
}

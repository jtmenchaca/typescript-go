// from dataflow_facts/path_conditions.ts
//
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
// GateAssumption itself is defined in flow_context.go (dataflowfacts'
// own port left it there — see that file's comment: dataflow_facts/
// path_conditions.go's gateKeyOf/assumedVerdict/gatesTestedBy/
// correlationGateOf are BLOCKED there on narrowing/condition_tree.ts,
// which was not yet ported when dataflowfacts landed). narrowing IS
// ported now, so assumedVerdict/gateKeyOf are portable — but
// GateAssumption is walk-owned (FlowContext needs it), so porting them
// against dataflowfacts.PlaceKey/dataflowfacts.LiteralSpelling here
// (in walk, which already imports dataflowfacts) keeps the single
// ownership PORT.md's banner established, rather than introducing a
// second copy of GateAssumption in dataflowfacts. gatesTestedBy and
// correlationGateOf are NOT needed by evaluation/ (only
// control_flow's statement-list splitting calls them) and are left
// for that stage.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// gateKey is GateKey in the TS source.
type gateKey struct {
	Base    *ast.Symbol
	Detail  string
	Negated bool
	Place   dataflowfacts.PlaceKey
}

// gateKeyOf is gateKeyOf in the TS source: the condition as a gate —
// nil when it is anything the pass cannot replay deterministically.
func gateKeyOf(ctx *FlowContext, condition *ast.Node) *gateKey {
	// the shared tree peels parens and negation once; a connective
	// (an and/or node) is not one replayable gate
	tree := conditiontree.ConditionTreeOf(condition, false)
	if tree.Kind != conditiontree.ConditionTreeLeaf {
		return nil
	}
	e := tree.Test
	negated := tree.Negated
	if ast.IsBinaryExpression(e) {
		bin := e.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		equal := kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindEqualsEqualsToken
		unequal := kind == ast.KindExclamationEqualsEqualsToken || kind == ast.KindExclamationEqualsToken
		if !equal && !unequal {
			return nil
		}
		leftLiteral := dataflowfacts.LiteralSpelling(bin.Left)
		rightLiteral := dataflowfacts.LiteralSpelling(bin.Right)
		var placeSide *ast.Node
		if leftLiteral == nil && rightLiteral != nil {
			placeSide = bin.Left
		} else if rightLiteral == nil && leftLiteral != nil {
			placeSide = bin.Right
		}
		var literal *string
		if leftLiteral != nil {
			literal = leftLiteral
		} else {
			literal = rightLiteral
		}
		if placeSide == nil || literal == nil {
			return nil
		}
		place := dataflowfacts.PlaceKeyOf(ctx.P.Checker, placeSide)
		if place == nil {
			return nil
		}
		gateNegated := negated
		if unequal {
			gateNegated = !negated
		}
		return &gateKey{Base: place.Base, Detail: place.Path + "===" + *literal, Negated: gateNegated, Place: *place}
	}
	place := dataflowfacts.PlaceKeyOf(ctx.P.Checker, e)
	if place == nil {
		return nil
	}
	return &gateKey{Base: place.Base, Detail: place.Path, Negated: negated, Place: *place}
}

// AssumedVerdict is assumedVerdict in the TS source: what the
// correlation passes say about this condition: the assumed verdict
// when the condition tests one of the assumed gates (through its
// negation parity), (false, false) otherwise.
func AssumedVerdict(ctx *FlowContext, assumptions []GateAssumption, condition *ast.Node) (verdict bool, decided bool) {
	if len(assumptions) == 0 {
		return false, false
	}
	key := gateKeyOf(ctx, condition)
	if key == nil {
		return false, false
	}
	for _, assumption := range assumptions {
		if key.Base == assumption.Base && key.Detail == assumption.Detail {
			if key.Negated {
				return !assumption.Truthy, true
			}
			return assumption.Truthy, true
		}
	}
	return false, false
}

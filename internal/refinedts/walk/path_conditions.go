// from dataflow_facts/path_conditions.ts
//
// GATES: the conditions a correlation pass can decide. A gate is a
// deterministic, effect-free test over stable places — a bare
// binding, a property place, an equality or relational comparison
// between such a place and a literal, or an equality or relational
// comparison between two such places — wrapped in any number of
// negations. ToBoolean of such a test is FIXED for a whole run,
// which is what lets two branches keyed by it correlate: assuming it
// truthy and falsy in turn partitions every run, so the two
// branches' join is exact.
//
// The gate's identity is its CANONICAL key (base symbol + detail
// spelling); the negation parity rides separately, so `if (a)` and
// `if (!a)` test the same gate with opposite verdicts, and
// `kind !== "a"` is the negation of `kind === "a"`.
//
// Four detail spellings, each with its own operator token so no
// spelling can be read as another:
//
//	place.Path + "===" + literal    place against a literal
//	place.Path + "<" + literal      place against a literal, and the
//	                                other three relational operators
//	pathA + "==p==" + pathB         two places against each other
//	pathA + "<p<" + pathB           two places against each other
//	                                relationally, and the other three
//	                                relational operators, each doubled
//	                                around the same "p"
//
// A path is "" or a run of `.name` and `[digits]` segments, and a
// literal spelling always opens with its own `n:`/`s:`/`b:` tag, so
// no path or literal can spell an operator token: `=`, `<`, and `>`
// appear in a detail only where this file writes one. That is what
// keeps the four apart — and in particular keeps the two-place forms
// away from the "===" spelling, the one switch_statement.go reads
// back by prefix to pick a clause, which stays byte-for-byte what it
// was. The two-place relational token doubles its operator around
// the "p" so that a first path ending in a property named `p` cannot
// make a one-place `<` spelling read as a two-place one.
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
	// Other is the second place of a two-place test — an equality or a
	// relational comparison — nil for every one-place form. A caller
	// checking stability has to check BOTH places: the test only stays
	// fixed for a run if neither side is rewritten.
	Other *dataflowfacts.PlaceKey
}

// relationalToken spells a relational operator for the detail key,
// and gives the MIRRORED operator to use whenever the two operands
// end up stored in the other order — because the place sits on the
// right of a literal (`10 > x` is the same test as `x < 10`), or
// because the canonical place order swapped a two-place test's
// operands (`b < a` stored a-first is `a > b`). Either way the two
// spellings of one test land on one key. Empty when the operator is
// not relational.
func relationalToken(kind ast.Kind) (token string, mirrored string, ok bool) {
	switch kind {
	case ast.KindLessThanToken:
		return "<", ">", true
	case ast.KindLessThanEqualsToken:
		return "<=", ">=", true
	case ast.KindGreaterThanToken:
		return ">", "<", true
	case ast.KindGreaterThanEqualsToken:
		return ">=", "<=", true
	}
	return "", "", false
}

// placeOrder puts two place keys in one canonical order, so `a === b`
// and `b === a` build the same detail spelling and count as one gate.
// Symbol pointers have no order of their own, so the base names and
// the property paths decide it. A relational caller reads the same
// two fields back to learn whether the operands came out swapped, and
// mirrors its operator when they did.
func placeOrder(left, right dataflowfacts.PlaceKey) (first, second dataflowfacts.PlaceKey) {
	if left.BaseName != right.BaseName {
		if left.BaseName < right.BaseName {
			return left, right
		}
		return right, left
	}
	if left.Path <= right.Path {
		return left, right
	}
	return right, left
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
		token, mirrored, relational := relationalToken(kind)
		if !equal && !unequal && !relational {
			return nil
		}
		leftLiteral := dataflowfacts.LiteralSpelling(bin.Left)
		rightLiteral := dataflowfacts.LiteralSpelling(bin.Right)
		var placeSide *ast.Node
		placeOnLeft := false
		if leftLiteral == nil && rightLiteral != nil {
			placeSide = bin.Left
			placeOnLeft = true
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
			// neither side is a literal: an equality between two tracked
			// places is still one fixed-for-a-run test, so it gates the
			// same way. Both places go into the key in a canonical order,
			// and the caller checks both for stability.
			if (equal || unequal) && leftLiteral == nil && rightLiteral == nil {
				leftPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Left)
				rightPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Right)
				if leftPlace == nil || rightPlace == nil {
					return nil
				}
				first, second := placeOrder(*leftPlace, *rightPlace)
				gateNegated := negated
				if unequal {
					gateNegated = !negated
				}
				return &gateKey{
					Base:    first.Base,
					Detail:  first.Path + "==p==" + second.BaseName + second.Path,
					Negated: gateNegated,
					Place:   first,
					Other:   &second,
				}
			}
			// a relational test between two tracked places is fixed for a
			// run the same way, so it gates too. The canonical order is
			// the same one equality uses, but `a < b` and `b < a` are
			// DIFFERENT tests, so the operand swap cannot be silent: when
			// the canonicalization moves the written left operand to the
			// second slot, the operator is mirrored with it. The stored
			// test is therefore always "canonical-first OP
			// canonical-second", which makes `a < b` and `b > a` — one
			// test written two ways — name one key, while `a < b` and
			// `a > b` name two. The parity stays on Negated for the NaN
			// reason the one-place relational form documents below.
			if relational && leftLiteral == nil && rightLiteral == nil {
				leftPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Left)
				rightPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Right)
				if leftPlace == nil || rightPlace == nil {
					return nil
				}
				first, second := placeOrder(*leftPlace, *rightPlace)
				spelled := token
				// placeOrder returned the operands swapped when its first
				// pick is not the one written on the left; comparing the
				// base names and paths says which happened, since those are
				// the fields it ordered on.
				if first.BaseName != leftPlace.BaseName || first.Path != leftPlace.Path {
					spelled = mirrored
				}
				return &gateKey{
					Base:    first.Base,
					Detail:  first.Path + spelled + "p" + spelled + second.BaseName + second.Path,
					Negated: negated,
					Place:   first,
					Other:   &second,
				}
			}
			// both sides literal, or a side that names no place at all
			return nil
		}
		place := dataflowfacts.PlaceKeyOf(ctx.P.Checker, placeSide)
		if place == nil {
			return nil
		}
		if relational {
			// a repeated `x < 10` is as fixed for a run as `x === 10`,
			// so it gates too. The operator rides in the key, mirrored
			// when the place sits on the right, so `x < 10` and `10 > x`
			// name one gate. The parity stays on the key rather than
			// folding into the operator: `!(x < 10)` carries the same
			// key with Negated flipped, which is the complement of the
			// test's ToBoolean whatever the comparands are. Rewriting it
			// as `x >= 10` instead would be wrong when x is NaN, since
			// both `<` and `>=` are false there.
			spelled := token
			if !placeOnLeft {
				spelled = mirrored
			}
			return &gateKey{Base: place.Base, Detail: place.Path + spelled + *literal, Negated: negated, Place: *place}
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

// What sort a value wears, read off the host's own type. A sort is
// number · boolean · string · sequence · reference (VOCABULARY.md),
// and it decides which knowledge a position admits: a word read under
// one sort may not be reread under another, which is what stops
// `as unknown as` from smuggling 65 in as "A" (TERMS-v2, the
// admitted-language rule).

package primitives

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// Sort is the semantic sorts the word carrier distinguishes — the map from
// an expression's underlying host type into the denotation's index.
// A sort-changing assertion (`as unknown as`) would let one word be
// reread under another sort — 65 satisfying "A" — so only a
// sort-preserving assertion passes the refinement through
// (TERMS-v2, the admitted-language rule).
type Sort string

const (
	SortString Sort = "string"
	SortNumber Sort = "number"
	SortBool   Sort = "boolean"
	SortArray  Sort = "array"
	SortOther  Sort = "other"
	SortOpaque Sort = "opaque"
)

// PrimitiveKindOf is primitiveKindOf in the TS source.
// c is *checker.Checker, standing in for the FlowContext/CheckerProgram
// wrapper (FlowContext.p.host) not yet ported at this layer — ctx.p.host in
// the TS source is the checker itself.
func PrimitiveKindOf(c *checker.Checker, t *checker.Type) Sort {
	if (t.Flags() & (checker.TypeFlagsAny | checker.TypeFlagsUnknown)) != 0 {
		return SortOpaque
	}
	if (t.Flags() & checker.TypeFlagsStringLike) != 0 {
		return SortString
	}
	if (t.Flags() & checker.TypeFlagsNumberLike) != 0 {
		return SortNumber
	}
	if (t.Flags() & checker.TypeFlagsBooleanLike) != 0 {
		return SortBool
	}
	if c.IsArrayLikeType(t) {
		return SortArray
	}
	return SortOther
}

// SortOfPresent is sortOfPresent in the TS source. The sort of a type's
// PRESENT part. Undefined and null members are absence — carried by the
// maybe wrapper, never as words — so an assertion that only adds or removes
// absence (`x!` on `number | undefined`) is sort-preserving: no word is
// reread. A union whose present members all wear one sort wears it too.
func SortOfPresent(c *checker.Checker, t *checker.Type) Sort {
	if !t.IsUnion() {
		return PrimitiveKindOf(c, t)
	}
	present := make([]*checker.Type, 0, len(t.Types()))
	for _, member := range t.Types() {
		if (member.Flags() & (checker.TypeFlagsUndefined | checker.TypeFlagsNull | checker.TypeFlagsVoid)) == 0 {
			present = append(present, member)
		}
	}
	if len(present) == 0 {
		return PrimitiveKindOf(c, t)
	}
	sorts := make(map[Sort]struct{})
	for _, member := range present {
		sorts[PrimitiveKindOf(c, member)] = struct{}{}
	}
	if len(sorts) == 1 {
		for sort := range sorts {
			return sort
		}
	}
	return PrimitiveKindOf(c, t)
}

// IsStringKind is isStringKind in the TS source.
func IsStringKind(c *checker.Checker, e *ast.Node) bool {
	return (c.GetTypeAtLocation(e).Flags() & checker.TypeFlagsStringLike) != 0
}

// stringLikeMemo mirrors the TS source's WeakMap<ts.Expression, boolean>.
// Go has no WeakMap; a plain map keyed by node pointer is the direct
// substitute (nodes are per-program here too, so staleness is not a
// concern within a program's lifetime — the one place true weak-reference
// GC behavior could not be preserved 1:1).
var stringLikeMemo = make(map[*ast.Node]bool)

// StringLikeSide is stringLikeSide in the TS source. Whether a `+` operand
// is string-like. A nested `+` reads it off its own operands — a string
// operand makes the sum a string, a number-only sum stays a number — so
// tsc is asked only at the leaves: getTypeAtLocation on a nested
// subexpression re-checks that whole subtree, which measured worse than
// quadratic on a 1,000-addition chain. Memoized per node (nodes are
// per-program, so a WeakMap never goes stale).
func StringLikeSide(c *checker.Checker, e *ast.Node) bool {
	if held, ok := stringLikeMemo[e]; ok {
		return held
	}
	var result bool
	switch {
	case ast.IsParenthesizedExpression(e):
		result = StringLikeSide(c, e.AsParenthesizedExpression().Expression)
	case ast.IsBinaryExpression(e) && e.AsBinaryExpression().OperatorToken.Kind == ast.KindPlusToken:
		bin := e.AsBinaryExpression()
		result = StringLikeSide(c, bin.Left) || StringLikeSide(c, bin.Right)
	default:
		result = (c.GetTypeAtLocation(e).Flags() & checker.TypeFlagsStringLike) != 0
	}
	stringLikeMemo[e] = result
	return result
}

// TypeofWordOf is typeofWordOf in the TS source. The `typeof` word a
// static type pins, or "" where the type admits more than one.
// Deterministic from tsc's own verdict — `typeof null` is "object", a
// callable is "function". (The TS source returns `string | null`; the
// empty string stands in for null since Sort/typeof words are never "".)
func TypeofWordOf(c *checker.Checker, t *checker.Type) string {
	if (t.Flags() & (checker.TypeFlagsAny | checker.TypeFlagsUnknown)) != 0 {
		return ""
	}
	if t.IsUnion() {
		words := make(map[string]struct{})
		for _, u := range t.Types() {
			words[TypeofWordOf(c, u)] = struct{}{}
		}
		if len(words) == 1 {
			for word := range words {
				return word
			}
		}
		return ""
	}
	if (t.Flags() & checker.TypeFlagsNumberLike) != 0 {
		return "number"
	}
	if (t.Flags() & checker.TypeFlagsStringLike) != 0 {
		return "string"
	}
	if (t.Flags() & checker.TypeFlagsBooleanLike) != 0 {
		return "boolean"
	}
	if (t.Flags() & checker.TypeFlagsBigIntLike) != 0 {
		return "bigint"
	}
	if (t.Flags() & checker.TypeFlagsESSymbolLike) != 0 {
		return "symbol"
	}
	if (t.Flags() & (checker.TypeFlagsUndefined | checker.TypeFlagsVoid)) != 0 {
		return "undefined"
	}
	if (t.Flags() & checker.TypeFlagsNull) != 0 {
		return "object"
	}
	if (t.Flags() & checker.TypeFlagsObject) != 0 {
		// a CONSTRUCT signature is a function too — `Type<any>` (nest's
		// constructor interface) types classes, and typeof a class is
		// "function"; asking only for call signatures called it "object"
		// and folded a live union guard false
		if len(c.GetCallSignatures(t)) > 0 || len(c.GetConstructSignatures(t)) > 0 {
			return "function"
		}
		// no "object" word is EVER pinned from a structural type: a
		// function carrying the right members satisfies any object type
		// (Object.assign(() => 1, { concat }) is a legal EnhancerArray,
		// and typeof it is "function"), and a primitive satisfies an
		// interface its apparent type covers ("abc" satisfies
		// { charAt(i: number): string }). recharts' RTK enhancers shim is
		// a live typeof-function test on an interface-typed value — the
		// "object" claim folded that branch provably-false, wrongly.
		return ""
	}
	return ""
}

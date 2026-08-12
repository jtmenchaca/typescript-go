// from assignability/admitted_sort.ts
//
// The admitted-language check (TERMS-v2): a tracked word carries the
// sort it was read under; a position admits the sorts its DECLARED
// side states. A word whose sort the position does not admit is not
// admitted knowledge — never a numeric reread of a string, array, or
// boolean carried through `any`.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
)

// admittedSortsOf is the TS source's admittedSortsOf: the sorts one
// type states, or (nil, false) where it states no usable sort (an
// unresolved alias, `any`, a bare object).
func admittedSortsOf(ctx *FlowContext, t *checker.Type) (map[primitives.Sort]bool, bool) {
	var parts []*checker.Type
	if t.IsUnion() {
		parts = t.Types()
	} else {
		parts = []*checker.Type{t}
	}
	admitted := map[primitives.Sort]bool{}
	for _, part := range parts {
		s := primitives.PrimitiveKindOf(ctx.P.Checker, part)
		if s == primitives.SortOpaque || s == primitives.SortOther {
			return nil, false
		}
		admitted[s] = true
	}
	return admitted, true
}

// CheckAdmittedSort is checkAdmittedSort in the TS source. True when
// a diagnostic was reported and the caller should return.
func CheckAdmittedSort(
	ctx *FlowContext,
	known abstractdomain.AbstractValue,
	node *ast.Node,
	what string,
	positionType *checker.Type,
) bool {
	if known.Kind != abstractdomain.KindValues {
		return false
	}
	// the position's sorts, first from the side that KNOWS the
	// position (the caller's declared type, the contextual type), and
	// where neither states a usable sort — an unresolved z.infer
	// alias answers "other" — from the expression's OWN static type:
	// tsc already checked that type against the position, so its sort
	// is admitted there, and a word provably wearing a different sort
	// reached it through an unchecked channel (a cast, an any)
	contextual := positionType
	if contextual == nil && ast.IsExpressionNode(node) {
		contextual = ctx.P.Checker.GetContextualType(node, checker.ContextFlagsNone)
	}
	var admitted map[primitives.Sort]bool
	var ok bool
	if contextual != nil {
		admitted, ok = admittedSortsOf(ctx, contextual)
	}
	if !ok {
		admitted, ok = admittedSortsOf(ctx, ctx.P.Checker.GetTypeAtLocation(node))
	}
	// PrimitiveKind (abstract_domain) and Sort (primitives) are
	// distinct named string types with the same literal values
	// ("number" | "string" | "boolean" | "array") for exactly this
	// comparison — the TS source's admittedSortsOf reads
	// primitiveKindOf's Sort and admitted.has(known.kindTag) compares
	// it against AbstractValue's PrimitiveKind directly (TS structural
	// typing over the shared string values); Go's nominal typing needs
	// the explicit conversion the TS source's own string union already
	// guarantees is safe.
	if !ok || admitted[primitives.Sort(known.KindTag)] {
		return false
	}
	// the position's declared side names its sorts and the value
	// provably wears another: no run satisfies the statement, so
	// the position refutes in plain words — a number where a
	// string is stated fails at runtime whatever its digits spell
	say := func(s string) string {
		switch s {
		case "number":
			return "a number"
		case "string":
			return "a string"
		case "boolean":
			return "a boolean"
		case "array":
			return "an array"
		default:
			return "a " + s
		}
	}
	var stated string
	for i, s := range sortedSortKeys(admitted) {
		if i > 0 {
			stated += " or "
		}
		stated += say(string(s))
	}
	ctx.Report(assignability.At(
		node,
		7001,
		what+" is "+say(string(known.KindTag))+", and the position states "+
			stated+" — "+say(string(known.KindTag))+" is not allowed here",
	))
	return true
}

// sortedSortKeys yields a map's Sort keys in a stable (sorted) order
// — the TS source's `[...admitted]` iterates a Set in INSERTION
// order; a Go map has no stable order, so this sorts to keep the
// message deterministic across runs (the TS insertion order itself
// comes from admittedSortsOf's own loop over `parts`, not from any
// meaningful ranking, so a stable substitute is faithful to the
// OBSERVABLE behavior — one fixed order per admitted set — without
// reproducing insertion order bit for bit).
func sortedSortKeys(m map[primitives.Sort]bool) []primitives.Sort {
	order := []primitives.Sort{primitives.SortNumber, primitives.SortString, primitives.SortBool, primitives.SortArray, primitives.SortOther, primitives.SortOpaque}
	var out []primitives.Sort
	for _, s := range order {
		if m[s] {
			out = append(out, s)
		}
	}
	return out
}

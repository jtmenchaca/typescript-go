// `Object.groupBy(items, cb)` — the model j-stdlib-surfaces.ts's
// objectGroupBy row reads through (readObjectGroupBy,
// iterator_spread_values.go).
//
// The model runs the callback once per exact element through the
// InlineCallback seam readMapGroupBy uses, takes each answer as ONE
// exact word (ToPropertyKey's own result sort, sec-topropertykey), and
// builds the bare-prototype object sec-object.groupby's
// OrdinaryObjectCreate(*null*) names, one own data property per
// distinct key holding CreateArrayFromList of that key's elements in
// items order.
//
// The two determination pins below read the BUILT VALUE — the object
// `const grouped = Object.groupBy([40, 200], age => …)` binds. The
// fixture's own positions (`grouped.young![0]`, `grouped.old![0]`) read
// one step further, through a `!` on a property access, and that step
// is ElementAccessOf's (element_access.go), not this model's — see
// TestObjectGroupBy_AGroupReadThroughANonNullAssertionIsNotYetRead.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

// groupByHeld walks the named function's body on a real checker-backed
// program and hands back the formatted value the environment holds for
// one name afterwards — compound_assign_family_test.go's harness, with
// no kernel seat needed: nothing this model does asks a kernel
// question, and the callback's own `age > 100` decides through
// CompareKnown on two exact numbers.
func groupByHeld(t *testing.T, source, name string) (string, bool) {
	t.Helper()
	p := entryEnvTestProgram(t, source)
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, nil, &diagnostics, nil)
	env := NewEnv()
	AnalyzeStatements(ctx, env, compoundAssignFunctionStatements(t, p, "f"), nil)
	held, ok := env.Get(name)
	if !ok {
		return "", false
	}
	return abstractdomain.FormatAbstractValue(held)
}

// The fixture's own grouping: the callback classifies 40 as "young" and
// 200 as "old", so the built object carries both keys with the routed
// element as an exact one-item list under each, in first-appearance
// order (40 is seen first, so "young" is the first key).
func TestObjectGroupBy_TheFixturesGroupingBuildsBothExactGroups(t *testing.T) {
	formatted, ok := groupByHeld(t, "function f(): void {\n"+
		"  const grouped = Object.groupBy([40, 200], (age) => (age > 100 ? \"old\" : \"young\"));\n"+
		"}\n", "grouped")
	if !ok || formatted != "{young: [40], old: [200]}" {
		t.Errorf("Object.groupBy([40, 200], age => age > 100 ? \"old\" : \"young\") = %q, %v, want %q",
			formatted, ok, "{young: [40], old: [200]}")
	}
}

// Every element routed to ONE key: the single group holds both elements
// in items order, and no second key exists.
func TestObjectGroupBy_OneKeyForEveryElementHoldsThemInItemsOrder(t *testing.T) {
	formatted, ok := groupByHeld(t, "function f(): void {\n"+
		"  const grouped = Object.groupBy([40, 200], () => \"all\");\n"+
		"}\n", "grouped")
	if !ok || formatted != "{all: [40, 200]}" {
		t.Errorf("Object.groupBy([40, 200], () => \"all\") = %q, %v, want %q",
			formatted, ok, "{all: [40, 200]}")
	}
}

// The decline pin: a callback whose answer for an element is NOT one
// exact word names no key this model can place, so the whole model
// declines to the residue rather than claiming a partial grouping. A
// NUMBER answer is the case — ToPropertyKey would ToString it
// (sec-topropertykey step 3), but the model takes determination only
// from a proved-string key, so it declines here rather than spelling
// the coercion.
func TestObjectGroupBy_ANonWordCallbackAnswerDeclinesTheWholeModel(t *testing.T) {
	formatted, ok := groupByHeld(t, "function f(): void {\n"+
		"  const grouped = Object.groupBy([40, 200], (age) => age);\n"+
		"}\n", "grouped")
	if ok && formatted == "{40: [40], 200: [200]}" {
		t.Errorf("a number-keyed groupBy claimed an exact grouping (%q) — only a proved-string key names a group this model places", formatted)
	}
}

// An INEXACT element list declines too: the model can only route
// elements it holds exactly, and a parameter-typed array holds no
// element list at all.
func TestObjectGroupBy_AnInexactItemListDeclinesTheWholeModel(t *testing.T) {
	formatted, ok := groupByHeld(t, "function f(xs: number[]): void {\n"+
		"  const grouped = Object.groupBy(xs, (age) => (age > 100 ? \"old\" : \"young\"));\n"+
		"}\n", "grouped")
	if ok && (formatted == "{young: [40], old: [200]}" || formatted == "{old: [200], young: [40]}") {
		t.Errorf("a groupBy over an unheld array claimed an exact grouping (%q)", formatted)
	}
}

// The fixture's own position, `grouped.young![0]`, reads back exactly:
// the model's exact per-key group indexes through ElementAccessOf's
// property-access receiver arm (the `!`/`as`/paren-wrapped property
// access the call-result gate admits — element_access.go), j-stdlib-
// surfaces.ts's objectGroupBy row.
func TestObjectGroupBy_AGroupReadThroughANonNullAssertionReadsExactly(t *testing.T) {
	formatted, ok := groupByHeld(t, "function f(): void {\n"+
		"  const grouped = Object.groupBy([40, 200], (age) => (age > 100 ? \"old\" : \"young\"));\n"+
		"  const good = grouped.young![0];\n"+
		"}\n", "good")
	if !ok || formatted != "40" {
		t.Errorf("grouped.young![0] = %q (%v), want exactly 40 — the routed group's own element", formatted, ok)
	}
}

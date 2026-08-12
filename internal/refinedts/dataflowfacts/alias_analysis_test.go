package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TestReferenceTyped ports the reference-vs-value distinction
// alias_analysis.test.ts exercises end to end (through the full check
// pipeline, which this directory does not have — service/ and
// kernel_bridge's kernel loader are out of scope here). This tests the
// same distinction directly against ReferenceTyped: array and object
// bindings are reference-typed, a number binding is not.
func TestReferenceTyped(t *testing.T) {
	c, file := checkerFor(t, `
const xs = [1, 2];
const n = 5;
xs;
n;
`)
	var exprs []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprs = append(exprs, statement.AsExpressionStatement().Expression)
		}
	}
	if !ReferenceTyped(c, exprs[0]) {
		t.Errorf("expected an array binding to be reference-typed")
	}
	if ReferenceTyped(c, exprs[1]) {
		t.Errorf("expected a number binding not to be reference-typed")
	}
}

func TestReferenceTypedSyntacticShortcuts(t *testing.T) {
	c, file := checkerFor(t, `
"a";
1;
true;
null;
[1, 2];
({ a: 1 });
(() => {});
`)
	var exprs []*ast.Node
	for _, statement := range file.Statements.Nodes {
		if ast.IsExpressionStatement(statement) {
			exprs = append(exprs, statement.AsExpressionStatement().Expression)
		}
	}
	cases := []struct {
		name string
		i    int
		want bool
	}{
		{"a string literal is not a reference", 0, false},
		{"a numeric literal is not a reference", 1, false},
		{"a boolean literal is not a reference", 2, false},
		{"null is not a reference", 3, false},
		{"an array literal is a reference", 4, true},
		{"an object literal is a reference", 5, true},
		{"an arrow function is a reference", 6, true},
	}
	for _, c2 := range cases {
		t.Run(c2.name, func(t *testing.T) {
			if got := ReferenceTyped(c, exprs[c2.i]); got != c2.want {
				t.Errorf("got %v, want %v", got, c2.want)
			}
		})
	}
}

func TestReadOnlyArrayMethodsAndStringReadMethods(t *testing.T) {
	if _, ok := ReadOnlyArrayMethods["push"]; ok {
		t.Errorf("push mutates — must not be read-only")
	}
	if _, ok := ReadOnlyArrayMethods["map"]; !ok {
		t.Errorf("map is read-only")
	}
	if _, ok := StringReadMethods["split"]; !ok {
		t.Errorf("split is a string read")
	}
	if _, ok := StringReadMethods["push"]; ok {
		t.Errorf("push is not a string method")
	}
}

// TestAliasClassesLinkJoinsIntoOneClass ports the shape of
// alias_analysis.test.ts's "mutation through an alias reaches every name
// sharing it" down to the class machinery it depends on: linking xs and ys
// puts both in the same class, in both directions, and a third unlinked
// name stays alone.
func TestAliasClassesLinkJoinsIntoOneClass(t *testing.T) {
	aliases := NewAliasClasses()
	aliases.Link("xs", "ys")
	class := aliases.ClassOf("xs")
	if _, ok := class["ys"]; !ok {
		t.Errorf("expected ys in xs's class")
	}
	if _, ok := aliases.ClassOf("ys")["xs"]; !ok {
		t.Errorf("expected xs in ys's class (both directions)")
	}
	if _, ok := aliases.ClassOf("zs")["xs"]; ok {
		t.Errorf("expected an unlinked name to stand alone")
	}
}

// TestAliasClassesLinkMergesExistingClasses covers link joining two
// ALREADY-linked groups into one — a third link brings every earlier
// member along.
func TestAliasClassesLinkMergesExistingClasses(t *testing.T) {
	aliases := NewAliasClasses()
	aliases.Link("a", "b")
	aliases.Link("c", "d")
	aliases.Link("b", "c")
	class := aliases.ClassOf("a")
	for _, want := range []string{"a", "b", "c", "d"} {
		if _, ok := class[want]; !ok {
			t.Errorf("expected %s in the merged class, got %v", want, class)
		}
	}
}

// TestAliasClassesHavocForgetsTheWholeClass ports "mutation through an
// alias reaches every name sharing it": havoc on one alias member erases
// EVERY member's tracked knowledge, since a write through any of them
// could have touched the shared reference.
func TestAliasClassesHavocForgetsTheWholeClass(t *testing.T) {
	aliases := NewAliasClasses()
	aliases.Link("xs", "ys")
	env := map[string]abstractdomain.AbstractValue{
		"xs":       abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		"ys":       abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		"zs":       abstractdomain.KnownValues([]float64{9}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		"xs.field": abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
	}
	aliases.Havoc(env, "ys")
	if xs := env["xs"]; xs.Kind != abstractdomain.KindUnknown {
		t.Errorf("expected xs to forget through its alias ys, got %+v", xs)
	}
	if ys := env["ys"]; ys.Kind != abstractdomain.KindUnknown {
		t.Errorf("expected ys to forget, got %+v", ys)
	}
	if zs := env["zs"]; zs.Kind == abstractdomain.KindUnknown {
		t.Errorf("expected zs (not in the class) to keep its facts")
	}
	if _, ok := env["xs.field"]; ok {
		t.Errorf("expected xs.field to be swept by ForgetPlaceEntries")
	}
}

// TestForgetPlaceEntries covers the dotted-key sweep directly: only keys
// rooted at the given name (with a dot boundary) are dropped.
func TestForgetPlaceEntries(t *testing.T) {
	env := map[string]abstractdomain.AbstractValue{
		"o.a":  abstractdomain.Unknown,
		"o.b":  abstractdomain.Unknown,
		"ob.c": abstractdomain.Unknown, // NOT rooted at "o" — no dot boundary
		"o":    abstractdomain.Unknown,
	}
	ForgetPlaceEntries(env, "o")
	if _, ok := env["o.a"]; ok {
		t.Errorf("expected o.a to be dropped")
	}
	if _, ok := env["o.b"]; ok {
		t.Errorf("expected o.b to be dropped")
	}
	if _, ok := env["ob.c"]; !ok {
		t.Errorf("expected ob.c to survive — it is not rooted at o")
	}
	if _, ok := env["o"]; !ok {
		t.Errorf("expected the bare binding o to survive — only DOTTED entries are swept")
	}
}

// TestAliasClassesInvalidateRetiresRowsRootedAtAWrittenName covers
// Register/Invalidate: a write to a place retires every live
// InvalidatableFact rooted at it or an alias of it, and leaves unrelated
// rows live.
func TestAliasClassesInvalidateRetiresRowsRootedAtAWrittenName(t *testing.T) {
	aliases := NewAliasClasses()
	aliases.Link("i", "j") // j aliases i
	rowOnI := &DifferenceConstraint{
		Minuend:    PlaceKey{BaseName: "n"},
		Subtrahend: PlaceKey{BaseName: "i"},
		Bound:      0,
	}
	rowOnUnrelated := &DifferenceConstraint{
		Minuend:    PlaceKey{BaseName: "p"},
		Subtrahend: PlaceKey{BaseName: "q"},
		Bound:      0,
	}
	aliases.Register([]InvalidatableFact{rowOnI, rowOnUnrelated})
	aliases.Invalidate("j") // j is an alias of i — should retire rowOnI too
	if !rowOnI.Dead {
		t.Errorf("expected the row rooted at i to retire through its alias j")
	}
	if rowOnUnrelated.Dead {
		t.Errorf("expected the unrelated row to stay live")
	}
}

// TestAliasClassesRegisterRootedCoversAnyArity covers registerRooted: a
// SumConstraint (3 places: two terms plus an anchor) invalidates through
// any of its roots, the same as a 2-place difference row.
func TestAliasClassesRegisterRootedCoversAnyArity(t *testing.T) {
	aliases := NewAliasClasses()
	sumRow := &SumConstraint{
		Terms:  [2]PlaceKey{{BaseName: "offset"}, {BaseName: "k"}},
		Anchor: PlaceKey{BaseName: "length"},
	}
	aliases.RegisterRooted(sumRow, []string{"offset", "k", "length"})
	aliases.Invalidate("k")
	if !sumRow.Dead {
		t.Errorf("expected the sum row to retire when one of its 3 roots is written")
	}
}

// TestUpdateTrackedSameShapedAliasTakesTheNewValue ports updateTracked's
// "same-shaped alias... takes the new one" branch: writing through xs
// updates every class member holding the SAME value ys held.
func TestUpdateTrackedSameShapedAliasTakesTheNewValue(t *testing.T) {
	aliases := NewAliasClasses()
	aliases.Link("xs", "ys")
	shared := abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	env := map[string]abstractdomain.AbstractValue{
		"xs": shared,
		"ys": shared,
	}
	next := abstractdomain.KnownValues([]float64{1, 2, -5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	UpdateTracked(aliases, env, "xs", next)
	if got := env["xs"]; got.Kind != abstractdomain.KindValues || len(got.Values) != 3 {
		t.Errorf("expected xs to take the new value, got %+v", got)
	}
	if got := env["ys"]; got.Kind != abstractdomain.KindValues || len(got.Values) != 3 {
		t.Errorf("expected ys (the same-shaped alias) to take the new value too, got %+v", got)
	}
}

// TestUpdateTrackedUnrelatedMemberForgets covers updateTracked's fallback:
// a class member that is neither the write's own name, a same-shaped
// alias, nor an embedder forgets entirely (residue).
func TestUpdateTrackedUnrelatedMemberForgets(t *testing.T) {
	aliases := NewAliasClasses()
	aliases.Link("xs", "ys")
	env := map[string]abstractdomain.AbstractValue{
		"xs": abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		"ys": abstractdomain.KnownValues([]float64{9, 9, 9}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), // NOT the same value as xs
	}
	next := abstractdomain.KnownValues([]float64{1, 2, -5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	UpdateTracked(aliases, env, "xs", next)
	if got := env["ys"]; got.Kind != abstractdomain.KindUnknown {
		t.Errorf("expected ys to forget (it held a different value, not an embedder), got %+v", got)
	}
}

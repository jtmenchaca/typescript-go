// The residue-reason sweep's callback_outcome.go family (the
// array-callback outcome unit): a shape reaching one of
// CallbackOutcome's/reduceOutcome's/findOutcome's/forEachOutcome's
// converted silence.Residue() sites must carry THAT site's sentence,
// not a bare unknown. The bound-map and undecided-find rows call
// CallbackOutcome directly (the same direct-call precedent
// residue_reason_inline_contract_test.go's rows set): CallbackOutcome's
// own outcome functions each re-derive their returned unknown from
// ItemsOf/ElementOf rather than propagating a converted receiver/
// binding verbatim, so these rows are built to read a site's sentence
// at a point the dispatch DOES carry it through untouched. See
// residue_reason_test.go's header for the sibling map.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// callbackOutcomeBoundMapCallSite finds the CallExpression node spelled
// `[<n>].map(bound)` under root — the call this test hands to
// CallbackOutcome directly, alongside the separately-resolved `f`
// declaration as the arrow (CallbackOf's own answer for a stored-bind
// argument, re-derived here rather than routed through CallbackOf so the
// test controls exactly which node plays which role).
func callbackOutcomeBoundMapCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call [..].map(bound)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "map"
	})
}

// TestCallbackOutcome_APreboundNonLiteralArgumentNamesItsOwnReader pins
// CallbackOutcome's own prebound-binding decline: `f.bind(null,
// sideEffect())` binds `f`'s first parameter at bind time to a CALL
// (not SyntacticLiteral), so the value this walk has no environment to
// re-evaluate binds through silence.ResidueOf rather than silently
// reading zero. `f`'s body returns that very parameter (`x`), so
// map-over-one-exact-item's own output list carries the sentence
// straight through as one of its Items — the one consumer in this
// dispatch that propagates a bound value untouched instead of
// re-deriving its own residue from ItemsOf/ElementOf.
//
// x: unknown — an EXPLICIT unknown parameter annotation, not `number`:
// BindParameter (callback_pins.go) seeds every bound name through
// silence.SeededBinding -> AfterReaders (after_readers.go), which reads
// a PRESENT (non-Opaque) KindUnknown as "silence to fill from the host
// type" and replaces it with ReadHostType's answer at the parameter's
// OWN declared type — a `number` parameter reseeds this site's own
// ResidueOf residue into a bare possiblyNaN-wrapped number ground before
// it ever reaches preboundBindings, discarding the very ResidueReason
// this test means to observe (confirmed empirically: the gate's own run
// showed x arriving possiblyNaN-wrapped, Inner set, not KindUnknown).
// ReadHostType on `unknown` answers (_, false), so AfterReaders' `if !ok
// { return held }` returns this site's residue untouched — the same
// `: unknown` dodge already used for a callee's OWN return type, applied
// here to a callee's PARAMETER type instead.
func TestCallbackOutcome_APreboundNonLiteralArgumentNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: unknown, y: number): unknown {\n"+
		"  return x;\n"+
		"}\n"+
		"function sideEffect(): number { return 1; }\n"+
		"function g(): void {\n"+
		"  const bound = f.bind(null, sideEffect());\n"+
		"  [1].map(bound);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	fArrow := entryEnvFunctionNamed(t, p, "f")
	call := callbackOutcomeBoundMapCallSite(t, p.Entry.AsNode())
	receiver := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	got := CallbackOutcome(ctx, NewEnv(), receiver, "map", call, fArrow, "", false, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	if got.Kind != abstractdomain.KindList || len(got.Items) != 1 {
		t.Fatalf("[1].map(bound) with bound = f.bind(null, sideEffect()) = %+v, want a one-item KindList (x is not a flat number since it carries residue)", got)
	}
	item := got.Items[0]
	if item.Kind != abstractdomain.KindUnknown {
		t.Fatalf("the mapped item (f's own x, the prebound value) = %+v, want KindUnknown", item)
	}
	if item.ResidueReason == "" {
		t.Fatalf("the prebound non-literal argument's unknown carries no ResidueReason")
	}
	if !strings.Contains(item.ResidueReason, "only a syntactic literal carries") {
		t.Errorf("ResidueReason = %q, want it to name the syntactic-literal-only rule", item.ResidueReason)
	}
}

// callbackOutcomeReduceCallSite finds the CallExpression node spelled
// `<receiver>.reduce(<callback>)` under root.
func callbackOutcomeReduceCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call xs.reduce(cb)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "reduce"
	})
}

// TestReduceOutcome_AnEmptyReceiverWithNoInitialValueNamesItsOwnReaderRatherThanPanicking
// pins reduceOutcome's empty-receiver-no-initial-value shape: sec-array.
// prototype.reduce step 4 ("If length = 0 and initialValue is not
// present, throw a TypeError exception") means `[].reduce(cb)` never
// completes at runtime. The receiver here is a KindValues array with a
// zero-length Values slice — ItemsOf turns that into a non-nil,
// zero-length items slice, the exact shape that used to reach an
// unguarded `items[1:]` and panic (`slice bounds out of range [1:0]`)
// before any caller observed a result. The guarded read must instead
// return the decline this branch already names ("nothing to vouch for
// there") rather than crashing.
func TestReduceOutcome_AnEmptyReceiverWithNoInitialValueNamesItsOwnReaderRatherThanPanicking(t *testing.T) {
	p := entryEnvTestProgram(t, "function g(): void {\n"+
		"  const empty: number[] = [];\n"+
		"  empty.reduce((acc: number, v: number) => acc + v);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	arrow := superArrayFirstNode(t, p.Entry.AsNode(), "the reduce callback arrow", ast.IsArrowFunction)
	call := callbackOutcomeReduceCallSite(t, p.Entry.AsNode())
	receiver := abstractdomain.KnownValues(nil, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	got := CallbackOutcome(ctx, NewEnv(), receiver, "reduce", call, arrow, "", false, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("[].reduce(cb) with no initial value = %+v, want KindUnknown (no panic)", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("the empty-receiver-no-initial-value unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "nothing to vouch for there") {
		t.Errorf("ResidueReason = %q, want it to name the empty-receiver-throws rule", got.ResidueReason)
	}
}

// callbackOutcomeFindCallSite finds the CallExpression node spelled
// `<receiver>.find(<callback>)` under root.
func callbackOutcomeFindCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call xs.find(cb)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "find"
	})
}

// TestFindOutcome_AnUndecidedSearchNamesItsOwnReader pins findOutcome's
// own fallback: a receiver that is not an exact sequence (ItemsOf nil)
// never enters the decided-predicate loop, so the search stays
// undecided — the result may be undefined or the found element, which
// leaves the model, and the residue must name that rather than leaving
// it bare.
func TestFindOutcome_AnUndecidedSearchNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function g(): void {\n"+
		"  const empty: number[] = [];\n"+
		"  empty.find((v: number) => v > 0);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	arrow := superArrayFirstNode(t, p.Entry.AsNode(), "the find callback arrow", ast.IsArrowFunction)
	call := callbackOutcomeFindCallSite(t, p.Entry.AsNode())
	// KindSet (not KindList/KindValues) reads as NOT an exact sequence —
	// ItemsOf answers nil for it — so findOutcome's decided-predicate
	// loop never runs and falls straight to this test's own target line.
	receiver := abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	got := CallbackOutcome(ctx, NewEnv(), receiver, "find", call, arrow, "", false, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("empty.find(cb) over a non-exact receiver = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("find's undecided-search unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "leaves the model") {
		t.Errorf("ResidueReason = %q, want it to name the undecided-search rule", got.ResidueReason)
	}
}

// callbackOutcomeForEachCallSite finds the CallExpression node spelled
// `<receiver>.forEach(<callback>)` under root.
func callbackOutcomeForEachCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call xs.forEach(cb)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "forEach"
	})
}

// TestForEachOutcome_TheCallResultIsUndefinedNotResidue pins forEach's
// own call result: sec-array.prototype.foreach's last step is
// unconditionally "Return undefined" — never a decline. An exact
// one-element array with no owner parameter runs the exact fold
// (forEachExactFold), whose own handled-return used to answer
// silence.Residue() (an unknown carrying no ResidueReason) where the
// spec pins undefined exactly.
func TestForEachOutcome_TheCallResultIsUndefinedNotResidue(t *testing.T) {
	p := entryEnvTestProgram(t, "function g(): void {\n"+
		"  const xs: number[] = [1];\n"+
		"  xs.forEach((v: number) => { v; });\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	arrow := superArrayFirstNode(t, p.Entry.AsNode(), "the forEach callback arrow", ast.IsArrowFunction)
	call := callbackOutcomeForEachCallSite(t, p.Entry.AsNode())
	receiver := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	got := CallbackOutcome(ctx, NewEnv(), receiver, "forEach", call, arrow, "", false, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	if got.Kind != abstractdomain.KindUndef {
		t.Fatalf("xs.forEach(cb) call result = %+v, want abstractdomain.Undef (sec-array.prototype.foreach: \"Return undefined\")", got)
	}
}

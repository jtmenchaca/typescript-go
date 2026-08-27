// Pins the AbsentFlavor fix at element_access.go's and
// element_in_bounds.go's own absence-producing sites: an element read
// past the end (a declared object-array's count floor, an
// object-star's unclaimed length, a repetition's proved-in-bounds
// hole, and an unvouched repetition read) all answer exactly
// undefined on a miss — never null (sec-ordinaryget: a missing own
// property, once the prototype chain reaches null, returns undefined)
// — so every wrapper these sites build must carry AbsentFlavorUndefOnly,
// not the pre-flavor conflated claim.
//
// The proved-in-bounds site wraps only where the SHAPE CHANNEL does:
// element_in_bounds.go's UncheckedIndexedAccessHonored reads the
// project's noUncheckedIndexedAccess, so that arm is pinned on both
// sides of the flag — wrapped with it on, the bare element with it off.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// elementAccessAbsentFlavorContext builds a bare FlowContext — no
// kernel needed, since none of these sites ask one.
func elementAccessAbsentFlavorContext(p *program.CheckerProgram, declared map[string]*annotations.DeclaredRefinement) *FlowContext {
	if declared == nil {
		declared = map[string]*annotations.DeclaredRefinement{}
	}
	return &FlowContext{
		P:         p,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  declared,
		Report:    func(assignability.RefinementDiagnostic) {},
	}
}

// elementAccessNodeOf returns the checked position's own
// ElementAccessExpression — the sole `xs[i]` read inside fn's body,
// found by AST walk rather than assumed statement position (fn's body
// carries the `const xs = 0;` declaration BEFORE the `xs[i];` read, so
// the read is never the body's first statement) — mirrors
// keyed_slot_reads_test.go's keyedSlotElementAccesses.
func elementAccessNodeOf(t *testing.T, p *program.CheckerProgram, fnName string) *ast.Node {
	t.Helper()
	fn := entryEnvFunctionNamed(t, p, fnName)
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("%s has no block body", fnName)
	}
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsElementAccessExpression(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	body.ForEachChild(visit)
	if found == nil {
		t.Fatalf("%s's body has no element access expression", fnName)
	}
	return found
}

// TestElementAccessOf_DeclaredObjectArrayPastFloorIsUndefOnly pins
// element_access.go's DeclaredObjectArray arm: an index that may sit
// past the guaranteed count reads the stated element wrapped
// possibly-absent.
func TestElementAccessOf_DeclaredObjectArrayPastFloorIsUndefOnly(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(i: number) { const xs = 0; xs[i]; }\n")
	// the checked node is `xs[i]` textually, but ElementAccessOf reads
	// through env + ctx.Declared rather than the type, so `xs`'s
	// declared type does not need to be an array for this arm
	node := elementAccessNodeOf(t, p, "f")
	ctx := elementAccessAbsentFlavorContext(p, map[string]*annotations.DeclaredRefinement{
		"xs": {
			Kind:        annotations.DeclaredObjectArray,
			Object:      &annotations.ObjectAnnotation{Keys: nil},
			Lo:          0,
			HiUnbounded: true,
		},
	})
	env := NewEnv()
	env.Set("xs", abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false))
	env.Set("i", abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	got := ElementAccessOf(ctx, env, node)
	if got == nil {
		t.Fatalf("ElementAccessOf(declared object-array, past floor) = nil, want a wrapped result")
	}
	if got.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("ElementAccessOf(declared object-array, past floor).Kind = %v, want KindPossiblyUndefined", got.Kind)
	}
	if got.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("ElementAccessOf(declared object-array, past floor).AbsentSide = %v, want AbsentFlavorUndefOnly", got.AbsentSide)
	}
}

// TestElementAccessOf_ObjectStarElementReadIsUndefOnly pins
// element_access.go's KindArrayHoles/object-star arm: a receiver whose
// element is stated but whose length is unclaimed reads that element
// wrapped possibly-absent.
func TestElementAccessOf_ObjectStarElementReadIsUndefOnly(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(i: number) { const xs = 0; xs[i]; }\n")
	node := elementAccessNodeOf(t, p, "f")
	ctx := elementAccessAbsentFlavorContext(p, nil)
	env := NewEnv()
	// KnownObjectStar requires a graph-shaped element (an object, or a
	// maybe over one) — a plain scalar would fail its own objectShaped
	// gate and build nothing.
	element := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "age", Value: abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)}},
		nil, true, abstractdomain.TrustProved, false,
	)
	star, ok := abstractdomain.KnownObjectStar(element, abstractdomain.TrustProved)
	if !ok {
		t.Fatalf("KnownObjectStar(an object element) refused, want a value")
	}
	env.Set("xs", star)
	env.Set("i", abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	got := ElementAccessOf(ctx, env, node)
	if got == nil {
		t.Fatalf("ElementAccessOf(object-star) = nil, want a wrapped result")
	}
	if got.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("ElementAccessOf(object-star).Kind = %v, want KindPossiblyUndefined", got.Kind)
	}
	if got.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("ElementAccessOf(object-star).AbsentSide = %v, want AbsentFlavorUndefOnly", got.AbsentSide)
	}
}

// provedInBoundsHoleRead runs element_in_bounds.go's proved-in-bounds
// arm over the given program: a repetition receiver with a raised
// counting floor of 1, read at index 0, which sits strictly under it
// (window.Hi=0 < rep.Lo=1) — the underFloor arm, the simplest way to
// reach "proved in bounds" without a length ledger row.
func provedInBoundsHoleRead(t *testing.T, p *program.CheckerProgram) *abstractdomain.AbstractValue {
	t.Helper()
	node := elementAccessNodeOf(t, p, "f")
	ctx := elementAccessAbsentFlavorContext(p, nil)
	env := NewEnv()
	two := 2
	rep := refinementsets.Repetition(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5})), 1, &two)
	receiver := abstractdomain.KnownSet(rep, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	index := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	return InBoundsElementOf(InBoundsElementOfParams{Ctx: ctx, Env: env, Expression: node, Receiver: receiver, Index: index})
}

// TestInBoundsElementOf_ProvedInBoundsHoleReadIsUndefOnly pins
// element_in_bounds.go's proved-in-bounds arm WITH
// noUncheckedIndexedAccess ON: an array (not string) receiver whose
// index is proved under the length still wears the absence, because
// the length can count holes — and with the flag on the shape channel
// answers `T | undefined` at the same read, so the two layers agree.
func TestInBoundsElementOf_ProvedInBoundsHoleReadIsUndefOnly(t *testing.T) {
	p := entryEnvTestProgramWithOptions(t,
		"function f(i: number) { const xs = 0; xs[i]; }\n",
		`"noUncheckedIndexedAccess": true`)
	got := provedInBoundsHoleRead(t, p)
	if got == nil {
		t.Fatalf("InBoundsElementOf(proved in bounds) = nil, want a wrapped result")
	}
	if got.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("InBoundsElementOf(proved in bounds).Kind = %v, want KindPossiblyUndefined", got.Kind)
	}
	if got.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("InBoundsElementOf(proved in bounds).AbsentSide = %v, want AbsentFlavorUndefOnly", got.AbsentSide)
	}
}

// TestInBoundsElementOf_ProvedInBoundsHoleReadKeepsElementWithoutTheFlag
// pins the other side of UncheckedIndexedAccessHonored's gate. tsc's
// `strict` does not imply noUncheckedIndexedAccess, so the common case
// — every project that does not name it — leaves the shape channel
// answering `T` at an indexed read. Wrapping anyway would make the
// refinement layer refuse values the host has already accepted
// (`arr.length > 0` then `arr[0]` into an Age position), so the
// absence rides only where the host puts one and the read answers the
// element outright.
func TestInBoundsElementOf_ProvedInBoundsHoleReadKeepsElementWithoutTheFlag(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(i: number) { const xs = 0; xs[i]; }\n")
	got := provedInBoundsHoleRead(t, p)
	if got == nil {
		t.Fatalf("InBoundsElementOf(proved in bounds, flag off) = nil, want the element")
	}
	if got.Kind != abstractdomain.KindSet {
		t.Fatalf("InBoundsElementOf(proved in bounds, flag off).Kind = %v, want KindSet — no maybe wrapper", got.Kind)
	}
}

// TestInBoundsElementOf_UnvouchedNumberIndexReadIsUndefOnly pins
// element_in_bounds.go's unvouched-index tail arm.
func TestInBoundsElementOf_UnvouchedNumberIndexReadIsUndefOnly(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(i: number) { const xs = 0; xs[i]; }\n")
	node := elementAccessNodeOf(t, p, "f")
	ctx := elementAccessAbsentFlavorContext(p, nil)
	env := NewEnv()
	rep := refinementsets.Repetition(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{5})), 0, nil)
	receiver := abstractdomain.KnownSet(rep, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	// an UNPROVED index (a plain variable, no window/ledger row) — the
	// tail arm at the bottom of InBoundsElementOf
	index := abstractdomain.AbstractValue{Kind: abstractdomain.KindUnknown}
	got := InBoundsElementOf(InBoundsElementOfParams{Ctx: ctx, Env: env, Expression: node, Receiver: receiver, Index: index})
	if got == nil {
		t.Fatalf("InBoundsElementOf(unvouched index) = nil, want a wrapped result")
	}
	if got.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("InBoundsElementOf(unvouched index).Kind = %v, want KindPossiblyUndefined", got.Kind)
	}
	if got.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("InBoundsElementOf(unvouched index).AbsentSide = %v, want AbsentFlavorUndefOnly", got.AbsentSide)
	}
}

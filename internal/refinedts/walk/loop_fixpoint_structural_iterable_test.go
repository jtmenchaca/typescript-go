// Pins loop_fixpoint.go's structural-iterable extension:
// structuralIterableElementType reads the element T off a host type
// whose own symbol spells Iterable or AsyncIterable — the two
// structural shapes GetElementTypeOfArrayType's array-only check
// (loop_fixpoint.go:452-455) does not cover, which is why
// a-statements.ts:447's `for await (… of stream())` over
// `AsyncIterable<number>` used to reach ElementOf's bare fallthrough.
//
// The array row (a-statements.ts:435/437) is untouched by this
// extension: GetElementTypeOfArrayType already resolves it, so
// elementKnown is no longer KindUnknown by the time this arm runs.
package walk

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// structuralIterableExpressionOf finds the sole for-of/for-await-of
// statement's own iterable Expression inside the named function's
// body — mirrors element_access_absent_flavor_test.go's
// elementAccessNodeOf (AST walk to the one checked position, not an
// assumed statement index).
func structuralIterableExpressionOf(t *testing.T, fn *ast.Node) *ast.Node {
	t.Helper()
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("function has no block body")
	}
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsForOfStatement(node) {
			found = node.AsForInOrOfStatement().Expression
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	visit(body)
	if found == nil {
		t.Fatalf("no for-of/for-await-of statement in the function body")
	}
	return found
}

// TestStructuralIterableElementType_AsyncIterableNumberReadsTheNumberGround
// pins a-statements.ts:447's own shape: `stream(): AsyncIterable<number>`
// drained by `for await`. The host type's symbol is AsyncIterable
// (default-library, one type argument), so structuralIterableElementType
// answers the checker's own number type, and reading THAT through
// typereading.ReadHostType gives the same NaN-wrapped ground the
// array-of-number arm hand-builds — "number, or NaN" — matching the
// sync case's own convention exactly.
func TestStructuralIterableElementType_AsyncIterableNumberReadsTheNumberGround(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function stream(): AsyncIterable<number>;
async function drain() {
	for await (const chunk of stream()) {
		void chunk;
	}
}
`)
	fn := entryEnvFunctionNamed(t, p, "drain")
	iterable := structuralIterableExpressionOf(t, fn)
	hostType := typereading.TypeAtLocation(p.Checker, iterable)
	if hostType == nil {
		t.Fatalf("TypeAtLocation(stream()) = nil, want AsyncIterable<number>")
	}
	element := structuralIterableElementType(p.Checker, hostType)
	if element == nil {
		t.Fatalf("structuralIterableElementType(AsyncIterable<number>) = nil, want the number type argument")
	}
	if element != p.Checker.GetNumberType() {
		t.Fatalf("structuralIterableElementType(AsyncIterable<number>) = %v, want the checker's own number type", element)
	}
	read, ok := typereading.ReadHostType(p.Checker, element, iterable, 0)
	if !ok {
		t.Fatalf("ReadHostType(number) ok = false, want a determined ground")
	}
	// the same shape typereading.NumberWithNaN() builds — PossiblyNaN
	// wrapping the whole number ray — which is what the array-of-number
	// arm hand-builds too (loop_fixpoint.go:454), so a for-await-of
	// element and a for-of element over a plain number[] now read the
	// same claim through two different host-type shapes.
	if read.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("ReadHostType(AsyncIterable<number>'s element).Kind = %v, want KindPossiblyNaN", read.Kind)
	}
	if read.Inner == nil || read.Inner.Kind != abstractdomain.KindSet {
		t.Fatalf("ReadHostType(AsyncIterable<number>'s element).Inner = %+v, want a KindSet", read.Inner)
	}
	if !reflect.DeepEqual(read.Inner.Set, refinementsets.Numbers) {
		t.Errorf("ReadHostType(AsyncIterable<number>'s element).Inner.Set = %+v, want refinementsets.Numbers (the whole number ray)", read.Inner.Set)
	}
}

// TestStructuralIterableElementType_PlainArrayIsNotAStructuralShape
// pins the negative: `number[]`'s own host type symbol is not named
// Iterable or AsyncIterable (it is the global Array reference), so
// structuralIterableElementType answers nil — the array row keeps
// reaching GetElementTypeOfArrayType exclusively, unaffected by this
// arm's addition.
func TestStructuralIterableElementType_PlainArrayIsNotAStructuralShape(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function unreadNumber(): number;
function drain() {
	for (const item of unreadNumber() as unknown as number[]) {
		void item;
	}
}
`)
	fn := entryEnvFunctionNamed(t, p, "drain")
	iterable := structuralIterableExpressionOf(t, fn)
	hostType := typereading.TypeAtLocation(p.Checker, iterable)
	if hostType == nil {
		t.Fatalf("TypeAtLocation(unreadNumber() as unknown as number[]) = nil, want number[]")
	}
	if element := structuralIterableElementType(p.Checker, hostType); element != nil {
		t.Errorf("structuralIterableElementType(number[]) = %v, want nil (arrays are not the structural shape)", element)
	}
	if element := p.Checker.GetElementTypeOfArrayType(hostType); element == nil || element != p.Checker.GetNumberType() {
		t.Errorf("GetElementTypeOfArrayType(number[]) = %v, want the checker's own number type (the row's existing coverage)", element)
	}
}

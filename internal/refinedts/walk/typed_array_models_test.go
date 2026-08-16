// Tests for typed_array_models.go: the ToXxx conversion table
// against hand-verified spec values, construction (zero-filled
// length, array-literal seeding through the conversion), element
// read/write (reusing the plain-array KindValues paths), .length,
// and the out-of-bounds no-op/undefined rows the spec fixes.
//
// Mirrors exact_length_array_test.go's shape: the model-level rows
// (no kernel) call ReadTypedArrayConstruction/ReadIndexedWrite
// directly through the entryEnvTestProgram/superArrayContracts
// recipe (AGENT-BRIEF.md's canonical program-from-source pattern),
// reusing super_and_array_ctor_test.go's superArrayNewIn/
// superArrayFirstNode node locators rather than re-deriving them.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

/* ── the conversion table, hand-verified against tmp/ecma262/spec.html
   (#sec-toint8, #sec-touint8, #sec-touint8clamp, #sec-tofixedsizeinteger)
   — the same values the fixture's own comments claim ──────────────── */

func TestToUint8_WrapsModulo256Unsigned(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{121, 121}, // no wrap under 256
		{200, 200}, // no wrap under 256
		{255, 255}, // the top of the range, unchanged
		{256, 0},   // wraps exactly to 0
		{257, 1},   // wraps to 1
		{512, 0},   // two full wraps
		{-1, 255},  // spec's mathematical modulo is non-negative
		{-50, 206}, // -50 mod 256 = 206
	}
	for _, c := range cases {
		if got := toUint8(c.in); got != c.want {
			t.Errorf("toUint8(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToInt8_WrapsModulo256Signed(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{100, 100},    // stays under the signed ceiling (127)
		{127, 127},    // the signed max, unchanged
		{128, -128},   // crosses into the negative half
		{200, -56},    // the fixture's own pinned row
		{255, -1},     // one below the wrap
		{256, 0},      // a full wrap
		{-56, -56},    // already in range
		{-128, -128},  // the signed min, unchanged
		{-129, 127},   // wraps from below
	}
	for _, c := range cases {
		if got := toInt8(c.in); got != c.want {
			t.Errorf("toInt8(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToUint8Clamp_ClampsToZeroTwoFiftyFiveNeverWraps(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{200, 200},  // inside the range, unchanged
		{255, 255},  // the ceiling, unchanged
		{300, 255},  // the fixture's own pinned row: clamps, does not wrap
		{-50, 0},    // the fixture's own pinned row: clamps, does not wrap
		{-1, 0},
		{256, 255},
	}
	for _, c := range cases {
		if got := toUint8Clamp(c.in); got != c.want {
			t.Errorf("toUint8Clamp(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTypedArrayConversion_NamesTheThreeModeledConstructorsOnly(t *testing.T) {
	for _, name := range []string{"Uint8Array", "Int8Array", "Uint8ClampedArray"} {
		if _, ok := typedArrayConversion(name); !ok {
			t.Errorf("typedArrayConversion(%q) answered false, want a row", name)
		}
	}
	for _, name := range []string{"Int16Array", "Uint32Array", "Float64Array", "Array", "NotAConstructor"} {
		if _, ok := typedArrayConversion(name); ok {
			t.Errorf("typedArrayConversion(%q) answered a row, want false — not modeled", name)
		}
	}
}

/* ── construction: length-only (zero-filled) ─────────────────────── */

func TestReadTypedArrayConstruction_LengthOnlyIsZeroFilled(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Uint8Array(4)[0]; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadTypedArrayConstruction answered nil for `new Uint8Array(4)`")
	}
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveArray {
		t.Fatalf("`new Uint8Array(4)` = %+v, want KindValues{PrimitiveArray}", *built)
	}
	if len(built.Values) != 4 {
		t.Fatalf("`new Uint8Array(4)` has %d elements, want 4", len(built.Values))
	}
	for i, v := range built.Values {
		if v != 0 {
			t.Errorf("slot %d = %v, want 0 (zero-filled)", i, v)
		}
	}
}

func TestReadTypedArrayConstruction_ZeroLengthIsTheEmptyArray(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Uint8Array(0).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadTypedArrayConstruction answered nil for `new Uint8Array(0)`")
	}
	if len(built.Values) != 0 {
		t.Errorf("`new Uint8Array(0)` has %d elements, want 0", len(built.Values))
	}
}

// A non-integer or negative length throws a RangeError at construction
// (ToIndex): no array exists for the model to answer, the same reading
// array_construction.go's own length arm gives.
func TestReadTypedArrayConstruction_AThrowingLengthAnswersNothing(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Uint8Array(-1).length + new Uint8Array(4.5).length; }\n")
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "f")
	var news []*ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if node == nil {
			return
		}
		if ast.IsNewExpression(node) {
			news = append(news, node)
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(fn.Body())
	if len(news) != 2 {
		t.Fatalf("found %d `new Uint8Array(...)` expressions, want 2", len(news))
	}
	for _, n := range news {
		if got := ReadTypedArrayConstruction(ctx, NewEnv(), n); got != nil {
			t.Errorf("a throwing length answered an array: %+v", *got)
		}
	}
}

/* ── construction: array-literal seeding through the conversion ───── */

func TestReadTypedArrayConstruction_ArrayLiteralRunsUint8OnEveryElement(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Uint8Array([1, 121, 200, 256])[0]; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadTypedArrayConstruction answered nil for `new Uint8Array([1, 121, 200, 256])`")
	}
	want := []float64{1, 121, 200, 0} // 256 wraps to 0 under ToUint8
	if len(built.Values) != len(want) {
		t.Fatalf("`new Uint8Array([1, 121, 200, 256])` has %d elements, want %d", len(built.Values), len(want))
	}
	for i, v := range want {
		if built.Values[i] != v {
			t.Errorf("slot %d = %v, want %v", i, built.Values[i], v)
		}
	}
}

func TestReadTypedArrayConstruction_ArrayLiteralRunsInt8OnEveryElement(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Int8Array([100, 200])[0]; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadTypedArrayConstruction answered nil for `new Int8Array([100, 200])`")
	}
	want := []float64{100, -56} // the fixture's own pinned ToInt8(200) = -56
	if len(built.Values) != len(want) {
		t.Fatalf("`new Int8Array([100, 200])` has %d elements, want %d", len(built.Values), len(want))
	}
	for i, v := range want {
		if built.Values[i] != v {
			t.Errorf("slot %d = %v, want %v", i, built.Values[i], v)
		}
	}
}

func TestReadTypedArrayConstruction_ArrayLiteralRunsUint8ClampOnEveryElement(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Uint8ClampedArray([200, 300, -50])[0]; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadTypedArrayConstruction answered nil for `new Uint8ClampedArray([200, 300, -50])`")
	}
	want := []float64{200, 255, 0} // the fixture's own pinned clamp rows
	if len(built.Values) != len(want) {
		t.Fatalf("`new Uint8ClampedArray([200, 300, -50])` has %d elements, want %d", len(built.Values), len(want))
	}
	for i, v := range want {
		if built.Values[i] != v {
			t.Errorf("slot %d = %v, want %v", i, built.Values[i], v)
		}
	}
}

// A spread or elision inside the literal breaks the one-to-one
// position mapping this row relies on — the construction stays
// unmodeled rather than overclaiming.
func TestReadTypedArrayConstruction_ASpreadInsideTheLiteralDeclines(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(xs: number[]): number { return new Uint8Array([1, ...xs])[0]; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built != nil {
		t.Errorf("`new Uint8Array([1, ...xs])` answered %+v, want nil (a spread breaks the position mapping)", *built)
	}
}

// A non-Uint8Array/Int8Array/Uint8ClampedArray constructor (not yet a
// row in the table) is not modeled — nil, not a wrong answer.
func TestReadTypedArrayConstruction_AnUnmodeledWidthDeclines(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Int16Array([1, 2])[0]; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built != nil {
		t.Errorf("`new Int16Array([1, 2])` answered %+v, want nil (Int16Array has no row in this table)", *built)
	}
}

/* ── element read: reuses the existing KindValues{PrimitiveArray} arm
   in element_access.go — no new code, checked here at the walk level
   to confirm the reuse actually reaches it ──────────────────────── */

func TestEvaluate_TypedArrayElementReadIsTheConvertedValue(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const bytes = new Uint8Array([255, 200]); return bytes[1]; }\n")
	ctx := superArrayContracts(t, p)
	// this test's own env, unlike superArrayContracts' bare FlowContext,
	// still needs `bytes` bound to what its declaration builds — nothing
	// here runs AnalyzeStatements, so the identifier-receiver arm in
	// element_access.go would otherwise see an unbound name and never
	// reach the KindValues element read this test means to exercise
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	env.Set("bytes", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [1] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 200 {
		t.Errorf("bytes[1] = %+v, want the exact scalar 200 (ToUint8(200) = 200, no wrap)", value)
	}
}

// An out-of-bounds read answers undefined, the same absence a plain
// array's element read wears past its own length — TypedArrayGetElement
// answers undefined for an invalid integer index (sec-typedarray-get,
// via IsValidIntegerIndex).
func TestEvaluate_TypedArrayOutOfBoundsElementReadIsUndefined(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): unknown { const bytes = new Uint8Array([1, 2]); return bytes[5]; }\n")
	ctx := superArrayContracts(t, p)
	// see TestEvaluate_TypedArrayElementReadIsTheConvertedValue: `bytes`
	// needs its own binding seeded by hand, since nothing here runs
	// AnalyzeStatements to bind the declaration itself
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	env.Set("bytes", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [5] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	if value.Kind != abstractdomain.KindUndef {
		t.Errorf("bytes[5] = %+v, want the absent value exactly", value)
	}
}

/* ── .length: reuses evaluate_property_access.go's KindValues arm ─── */

func TestEvaluate_TypedArrayLengthReadsExactly(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Uint8Array(200).length; }\n")
	ctx := superArrayContracts(t, p)
	lengthRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .length read", ast.IsPropertyAccessExpression)
	value := evaluateExpression(ctx, NewEnv(), lengthRead)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 200 {
		t.Errorf("new Uint8Array(200).length = %+v, want the exact scalar 200", value)
	}
}

/* ── element write: TypedArrayWriteConversion + ReadIndexedWrite ──── */

// TypedArrayWriteConversion reads the constructor kind off the
// CHECKER's own static type at the receiver — not off the
// AbstractValue, which carries no such tag.
func TestTypedArrayWriteConversion_ReadsTheConstructorOffTheStaticType(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const bytes = new Uint8Array(2); bytes[0] = 10; return bytes[0]; }\n")
	ctx := superArrayContracts(t, p)
	assignment := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the bytes[0] = 10 write", func(node *ast.Node) bool {
		return ast.IsBinaryExpression(node) && node.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken
	})
	elem := assignment.AsBinaryExpression().Left.AsElementAccessExpression()
	convert, ok := TypedArrayWriteConversion(ctx, elem.Expression)
	if !ok {
		t.Fatalf("TypedArrayWriteConversion answered false for a Uint8Array receiver")
	}
	if got := convert(300); got != toUint8(300) {
		t.Errorf("the resolved conversion(300) = %v, want toUint8(300) = %v", got, toUint8(300))
	}
}

func TestTypedArrayWriteConversion_APlainArrayReceiverAnswersFalse(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs: number[] = [1, 2]; xs[0] = 10; return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	assignment := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the xs[0] = 10 write", func(node *ast.Node) bool {
		return ast.IsBinaryExpression(node) && node.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken
	})
	elem := assignment.AsBinaryExpression().Left.AsElementAccessExpression()
	if _, ok := TypedArrayWriteConversion(ctx, elem.Expression); ok {
		t.Errorf("TypedArrayWriteConversion answered true for a plain number[] receiver, want false")
	}
}

// `ta[i] = v` on an in-bounds index stores the CONVERTED value — a
// later read sees it wrapped/clamped, even though the assignment
// EXPRESSION's own value stays the raw right-hand side (simple
// assignment's runtime semantics return rightValue unconditionally,
// #sec-assignment-operators-runtime-semantics-evaluation).
func TestEvaluate_TypedArrayIndexedWriteStoresTheConvertedValue(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const bytes = new Uint8Array(2); bytes[0] = 256; return bytes[0]; }\n")
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "f")
	assignment := superArrayFirstNode(t, fn.Body(), "the bytes[0] = 256 write", func(node *ast.Node) bool {
		return ast.IsBinaryExpression(node) && node.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken
	})
	env := NewEnv()
	// seed the environment the way the block statement walk would: the
	// `const bytes = new Uint8Array(2)` declaration first
	varStatement := fn.Body().AsBlock().Statements.Nodes[0]
	decl := varStatement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0]
	initializer := decl.AsVariableDeclaration().Initializer
	built := evaluateExpression(ctx, env, initializer)
	env.Set("bytes", built)

	assignmentValue, matched := ReadIndexedWrite(ctx, env, assignment)
	if !matched {
		t.Fatalf("ReadIndexedWrite did not match `bytes[0] = 256`")
	}
	// the assignment expression's own value is the RAW right-hand side,
	// unconverted
	if assignmentValue.Kind != abstractdomain.KindValues || len(assignmentValue.Values) != 1 || assignmentValue.Values[0] != 256 {
		t.Errorf("the assignment expression's value = %+v, want the raw 256", assignmentValue)
	}
	// a later read sees the CONVERTED value: ToUint8(256) = 0
	held, ok := env.Get("bytes")
	if !ok {
		t.Fatalf("bytes is no longer tracked after the write")
	}
	if held.Kind != abstractdomain.KindValues || len(held.Values) != 2 || held.Values[0] != 0 {
		t.Errorf("bytes after the write = %+v, want slot 0 = 0 (ToUint8(256) wraps to 0)", held)
	}
}

// An out-of-range indexed write is a spec no-op (TypedArraySetElement's
// own IsValidIntegerIndex guard skips the store) — the tracked array is
// unchanged.
func TestEvaluate_TypedArrayOutOfBoundsIndexedWriteIsANoOp(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const bytes = new Uint8Array(2); bytes[5] = 10; return bytes[0]; }\n")
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "f")
	assignment := superArrayFirstNode(t, fn.Body(), "the bytes[5] = 10 write", func(node *ast.Node) bool {
		return ast.IsBinaryExpression(node) && node.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken
	})
	env := NewEnv()
	varStatement := fn.Body().AsBlock().Statements.Nodes[0]
	decl := varStatement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0]
	initializer := decl.AsVariableDeclaration().Initializer
	built := evaluateExpression(ctx, env, initializer)
	env.Set("bytes", built)

	if _, matched := ReadIndexedWrite(ctx, env, assignment); !matched {
		t.Fatalf("ReadIndexedWrite did not match `bytes[5] = 10`")
	}
	held, ok := env.Get("bytes")
	if !ok {
		t.Fatalf("bytes is no longer tracked after the out-of-range write")
	}
	want := []float64{0, 0}
	if held.Kind != abstractdomain.KindValues || len(held.Values) != len(want) {
		t.Fatalf("bytes after the out-of-range write = %+v, want the unchanged zero-filled array", held)
	}
	for i, v := range want {
		if held.Values[i] != v {
			t.Errorf("slot %d = %v, want %v (unchanged — the write was out of bounds)", i, held.Values[i], v)
		}
	}
}

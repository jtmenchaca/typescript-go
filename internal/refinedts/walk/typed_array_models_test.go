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
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

/* ── the conversion table, hand-verified against specifications/javascript/spec.html
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
		{100, 100},   // stays under the signed ceiling (127)
		{127, 127},   // the signed max, unchanged
		{128, -128},  // crosses into the negative half
		{200, -56},   // the fixture's own pinned row
		{255, -1},    // one below the wrap
		{256, 0},     // a full wrap
		{-56, -56},   // already in range
		{-128, -128}, // the signed min, unchanged
		{-129, 127},  // wraps from below
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
		{200, 200}, // inside the range, unchanged
		{255, 255}, // the ceiling, unchanged
		{300, 255}, // the fixture's own pinned row: clamps, does not wrap
		{-50, 0},   // the fixture's own pinned row: clamps, does not wrap
		{-1, 0},
		{256, 255},
	}
	for _, c := range cases {
		if got := toUint8Clamp(c.in); got != c.want {
			t.Errorf("toUint8Clamp(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTypedArrayConversion_NamesTheNineModeledConstructorsOnly(t *testing.T) {
	for _, name := range []string{
		"Uint8Array", "Int8Array", "Uint8ClampedArray",
		"Int16Array", "Uint16Array", "Int32Array", "Uint32Array",
		"Float64Array", "Float32Array",
	} {
		if _, ok := typedArrayConversion(name); !ok {
			t.Errorf("typedArrayConversion(%q) answered false, want a row", name)
		}
	}
	for _, name := range []string{"BigInt64Array", "BigUint64Array", "Array", "NotAConstructor"} {
		if _, ok := typedArrayConversion(name); ok {
			t.Errorf("typedArrayConversion(%q) answered a row, want false — not modeled", name)
		}
	}
}

/* ── the wider-width conversions, hand-verified against
   specifications/javascript/spec.html (#sec-toint16, #sec-touint16,
   #sec-toint32, #sec-touint32, #sec-tofixedsizeinteger) ────────────── */

func TestToUint16_WrapsModulo65536Unsigned(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{65535, 65535}, // the top of the range, unchanged
		{65536, 0},     // wraps exactly to 0
		{70000, 4464},  // the fixture's own pinned wrap: 70000 mod 65536 = 4464
		{-1, 65535},    // spec's mathematical modulo is non-negative
	}
	for _, c := range cases {
		if got := toUint16(c.in); got != c.want {
			t.Errorf("toUint16(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToInt16_WrapsModulo65536Signed(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{32767, 32767},   // the signed max, unchanged
		{32768, -32768},  // crosses into the negative half
		{70000, 4464},    // 70000 mod 65536 = 4464; 4464 < 32768, no shift needed
		{40000, -25536},  // 40000 mod 65536 = 40000; 40000 >= 32768, so 40000 - 65536 = -25536
		{-32768, -32768}, // the signed min, unchanged
	}
	for _, c := range cases {
		if got := toInt16(c.in); got != c.want {
			t.Errorf("toInt16(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToUint32_WrapsModulo4294967296Unsigned(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{4294967295, 4294967295}, // the top of the range, unchanged
		{4294967296, 0},          // wraps exactly to 0
		{4294967297, 1},          // wraps to 1
		{-1, 4294967295},         // spec's mathematical modulo is non-negative
	}
	for _, c := range cases {
		if got := toUint32TypedArrayElement(c.in); got != c.want {
			t.Errorf("toUint32TypedArrayElement(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToInt32_WrapsModulo4294967296Signed(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{2147483647, 2147483647},   // the signed max, unchanged
		{2147483648, -2147483648},  // crosses into the negative half
		{-2147483648, -2147483648}, // the signed min, unchanged
	}
	for _, c := range cases {
		if got := toInt32(c.in); got != c.want {
			t.Errorf("toInt32(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIdentityConversion_Float64ArrayElementCarriesTheValueUnchanged(t *testing.T) {
	cases := []float64{0, 121, -50, 3.14159, 1e300, -1e-300}
	for _, v := range cases {
		if got := identityConversion(v); got != v {
			t.Errorf("identityConversion(%v) = %v, want %v unchanged", v, got, v)
		}
	}
}

// toFloat32RoundTrip is Go's own float64(float32(v)): round v to
// binary32 (Go's non-constant conversion rule), then widen back to
// binary64. Every "want" here is computed by that same expression, not
// transcribed, so the test checks the function IS this composition
// rather than checking it against a copied number.
func TestToFloat32RoundTrip_RoundsThroughBinary32ThenWidensBack(t *testing.T) {
	cases := []float64{0, 121, -50, 0.1, 1.5, 3.14159, 1e30, -1e-30}
	for _, v := range cases {
		want := float64(float32(v))
		if got := toFloat32RoundTrip(v); got != want {
			t.Errorf("toFloat32RoundTrip(%v) = %v, want %v (float64(float32(v)))", v, got, want)
		}
	}
	// 0.1 is NOT exactly representable in binary32: the round-trip
	// visibly changes it — confirms this test actually exercises a
	// real rounding step, not a no-op on every case.
	if got := toFloat32RoundTrip(0.1); got == 0.1 {
		t.Errorf("toFloat32RoundTrip(0.1) = %v, want a visibly rounded value (0.1 has no exact binary32 representation)", got)
	}
	// 1.5 IS exactly representable in binary32: the round-trip is a
	// true no-op, unlike 0.1's.
	if got := toFloat32RoundTrip(1.5); got != 1.5 {
		t.Errorf("toFloat32RoundTrip(1.5) = %v, want 1.5 unchanged (exactly representable in binary32)", got)
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

// A non-modeled constructor (not yet a row in the table) is not
// modeled — nil, not a wrong answer.
func TestReadTypedArrayConstruction_AnUnmodeledWidthDeclines(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): bigint { return new BigInt64Array([1n, 2n])[0]; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built != nil {
		t.Errorf("`new BigInt64Array([1n, 2n])` answered %+v, want nil (BigInt64Array has no row in this table)", *built)
	}
}

// A construction on BigInt64Array/BigUint64Array names the family in
// the recorded decline instead of falling to the generic
// constructor-name-blind "new builds a value the walk does not model"
// sentence.
func TestReadTypedArrayConstruction_UnmodeledFamiliesNameThemselvesInTheDecline(t *testing.T) {
	for _, tc := range []struct {
		source string
		family string
	}{
		{"function f(): bigint { return new BigInt64Array([1n, 2n])[0]; }\n", "BigInt64Array"},
		{"function f(): bigint { return new BigUint64Array([1n, 2n])[0]; }\n", "BigUint64Array"},
	} {
		p := entryEnvTestProgram(t, tc.source)
		ctx := superArrayContracts(t, p)
		assignability.BeginReasonNotes()
		built := ReadTypedArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
		notes := assignability.EndReasonNotes()
		if built != nil {
			t.Errorf("%s: answered %+v, want nil", tc.family, *built)
		}
		found := false
		for _, note := range notes {
			if note.Unsupported && strings.Contains(note.Said, tc.family) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no recorded decline named the family; notes = %+v", tc.family, notes)
		}
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

/* ── the new families: one construction+element-read pin each ─────── */

// Float64Array's own conversion is the identity — the element holds
// the constructed literal's value unchanged, the same shape the
// corpus's float64ArrayElementUndetermined row pins.
func TestEvaluate_Float64ArrayElementReadIsTheValueUnchanged(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = new Float64Array([3.14159, 2.5]); return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveArray {
		t.Fatalf("`new Float64Array([3.14159, 2.5])` = %+v, want KindValues{PrimitiveArray}", built)
	}
	env.Set("xs", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [0] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 3.14159 {
		t.Errorf("xs[0] = %+v, want the exact scalar 3.14159 (Float64Array carries it unchanged)", value)
	}
}

// A plain (non-typed) array literal with a NEGATIVE element —
// `[-2.5]` — parses that element as a PrefixUnaryExpression wrapping
// the literal `2.5`. EvaluateArrayLiteral evaluates each element
// through the general evaluateExpression, which now folds an exact
// singleton's unary minus without a kernel (arithmetic_transfer.go's
// negateImage) — so the whole literal collapses to the flat
// KnownValues tuple {-2.5}, the same shape a positive-only literal
// gets, rather than answering unknown.
func TestEvaluate_ArrayLiteralWithANegativeElementReadsTheNegatedValue(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = [-2.5]; return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	literal := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [-2.5] array literal", ast.IsArrayLiteralExpression)
	built := evaluateExpression(ctx, env, literal)
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveArray {
		t.Fatalf("`[-2.5]` = %+v, want KindValues{PrimitiveArray}", built)
	}
	if len(built.Values) != 1 || built.Values[0] != -2.5 {
		t.Errorf("`[-2.5]` = %+v, want the exact scalar -2.5", built.Values)
	}
}

// A positive-literal array alongside the negative one stays unchanged
// by the negateImage fix — the flat KnownValues collapse never touched
// the positive-literal path.
func TestEvaluate_ArrayLiteralWithPositiveElementsIsUnchanged(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = [1, 2.5, 3]; return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	literal := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [1, 2.5, 3] array literal", ast.IsArrayLiteralExpression)
	built := evaluateExpression(ctx, env, literal)
	want := []float64{1, 2.5, 3}
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveArray {
		t.Fatalf("`[1, 2.5, 3]` = %+v, want KindValues{PrimitiveArray}", built)
	}
	if len(built.Values) != len(want) {
		t.Fatalf("`[1, 2.5, 3]` has %d elements, want %d", len(built.Values), len(want))
	}
	for i, v := range want {
		if built.Values[i] != v {
			t.Errorf("slot %d = %v, want %v", i, built.Values[i], v)
		}
	}
}

// Int16Array's ToInt16 wraps modulo 2^16 into the signed range.
func TestEvaluate_Int16ArrayElementReadIsTheWrappedValue(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = new Int16Array([40000]); return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	env.Set("xs", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [0] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != -25536 {
		t.Errorf("xs[0] = %+v, want -25536 (ToInt16(40000) wraps into the signed range)", value)
	}
}

// Uint16Array's ToUint16 wraps modulo 2^16 — the pinned wrap case the
// brief names: 70000 -> 4464.
func TestEvaluate_Uint16ArrayElementReadIsTheWrappedValue(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = new Uint16Array([70000]); return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	env.Set("xs", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [0] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 4464 {
		t.Errorf("xs[0] = %+v, want 4464 (ToUint16(70000) = 70000 mod 65536)", value)
	}
}

// Int32Array's ToInt32 wraps modulo 2^32 into the signed range.
func TestEvaluate_Int32ArrayElementReadIsTheWrappedValue(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = new Int32Array([2147483648]); return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	env.Set("xs", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [0] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != -2147483648 {
		t.Errorf("xs[0] = %+v, want -2147483648 (ToInt32(2147483648) wraps into the signed range)", value)
	}
}

// Uint32Array's ToUint32 wraps modulo 2^32.
func TestEvaluate_Uint32ArrayElementReadIsTheWrappedValue(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = new Uint32Array([4294967297]); return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	env.Set("xs", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [0] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 1 {
		t.Errorf("xs[0] = %+v, want 1 (ToUint32(4294967297) = 4294967297 mod 4294967296)", value)
	}
}

// Float32Array's own conversion rounds through IEEE 754 binary32
// (roundTiesToEven) then widens back to binary64 — a value that is
// NOT exactly representable in binary32 (0.1) comes back visibly
// changed. The pinned want is computed by the same
// float64(float32(...)) round-trip toFloat32RoundTrip runs, not
// hand-transcribed, so the test measures the function under test
// against Go's own conversion rather than against a copied literal.
func TestEvaluate_Float32ArrayElementReadRoundsThroughBinary32(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = new Float32Array([0.1]); return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveArray {
		t.Fatalf("`new Float32Array([0.1])` = %+v, want KindValues{PrimitiveArray}", built)
	}
	env.Set("xs", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [0] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	want := float64(float32(0.1)) // the same round-trip toFloat32RoundTrip runs
	if want == 0.1 {
		t.Fatalf("test setup: float64(float32(0.1)) == 0.1 unrounded — 0.1 no longer exercises a visible round, pick a different input")
	}
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != want {
		t.Errorf("xs[0] = %+v, want %v (0.1 rounds through binary32, roundTiesToEven)", value, want)
	}
}

// A value exactly representable in binary32 (1.5 — a terminating
// binary fraction well within the mantissa's precision) passes through
// the round-trip unchanged: no rounding step has anything to round.
func TestEvaluate_Float32ArrayElementReadPassesThroughAnExactBinary32Value(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { const xs = new Float32Array([1.5]); return xs[0]; }\n")
	ctx := superArrayContracts(t, p)
	env := NewEnv()
	construction := superArrayNewIn(t, p, "f")
	built := evaluateExpression(ctx, env, construction)
	env.Set("xs", built)
	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [0] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, env, elementRead)
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 1.5 {
		t.Errorf("xs[0] = %+v, want the exact scalar 1.5 unchanged (1.5 is exactly representable in binary32)", value)
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

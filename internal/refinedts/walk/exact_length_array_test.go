// Exact-length arrays past ReadArrayConstruction's materialization
// ceiling (array_construction.go's arrayConstructionHoleLimit): the
// honest claim — length pinned exactly, every slot a hole — carried
// by abstractdomain.KindArrayHoles instead of a materialized KindList.
// KindArrayHoles wraps TWO existing-vocabulary claims rather than a
// bare length: the length is the ordinary scalar {n} (KnownValues,
// held in Inner — the same shape .length reads for every other array
// kind), and the PRESENT-element set is the kernel's own ∅
// (OneOf(nil)) — a hole array has no present element to admit. The
// pinned row below the ceiling (`new Array(40)`, KindList) is the
// regression this file must not move; the rows above it
// (`new Array(50000)`) are the new determination.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// sameEmptyElementSet checks a RefinedSet is exactly the empty set
// KnownArrayHoles wraps: one OneOf refinement admitting no values —
// the same shape kernel_delegation.go's own emptySet builds.
func sameEmptyElementSet(set refinementsets.RefinedSet) bool {
	if len(set.Forms) != 1 {
		return false
	}
	form := set.Forms[0]
	return form.Form == refinementsets.FormOneOf && len(form.W) == 0
}

/* ── the model, no kernel: below vs. at/past the materialization
   ceiling ────────────────────────────────────────────────────────── */

func TestReadArrayConstruction_BelowTheCeilingStillBuildsTheMaterializedList(t *testing.T) {
	// the pinned row (super_and_array_ctor_test.go) restated here so a
	// change to the ceiling's OWN threshold is caught beside the new
	// large-length row, not only in the older file
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(40).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for `new Array(40)`")
	}
	if built.Kind != abstractdomain.KindList || len(built.Items) != 40 {
		t.Fatalf("`new Array(40)` = %+v, want the 40-slot KindList", *built)
	}
	for i, item := range built.Items {
		if item.Kind != abstractdomain.KindUndef {
			t.Fatalf("slot %d = %+v, want the hole's undefined", i, item)
		}
	}
}

func TestReadArrayConstruction_PastTheCeilingBuildsArrayHolesInstead(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(50000).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for `new Array(50000)` — the length is past the materialization ceiling but still exact")
	}
	if built.Kind != abstractdomain.KindArrayHoles {
		t.Fatalf("`new Array(50000)` = %+v, want KindArrayHoles", *built)
	}
	length, ok := abstractdomain.LengthOfArrayHoles(*built)
	if !ok || length != 50000 {
		t.Errorf("`new Array(50000)`'s length = %d (ok=%v), want 50000", length, ok)
	}
	if !sameEmptyElementSet(built.ElementSet) {
		t.Errorf("`new Array(50000)`.ElementSet = %+v, want the empty set", built.ElementSet)
	}
}

func TestReadArrayConstruction_ExactlyAtTheCeilingStillMaterializes(t *testing.T) {
	// the boundary: arrayConstructionHoleLimit itself (10,000) is the
	// last length the materialized list still answers — the ceiling
	// check is `length > arrayConstructionHoleLimit`, so the equal case
	// stays a KindList
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(10000).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for `new Array(10000)`")
	}
	if built.Kind != abstractdomain.KindList || len(built.Items) != 10000 {
		t.Fatalf("`new Array(10000)` = %+v (kind %v), want the 10000-slot KindList", *built, built.Kind)
	}
}

func TestReadArrayConstruction_OneOverTheCeilingIsTheFirstArrayHolesLength(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(10001).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayNewIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for `new Array(10001)`")
	}
	length, ok := abstractdomain.LengthOfArrayHoles(*built)
	if built.Kind != abstractdomain.KindArrayHoles || !ok || length != 10001 {
		t.Fatalf("`new Array(10001)` = %+v, want KindArrayHoles wrapping length 10001", *built)
	}
}

// The `Array(n)` CALL spelling (no `new`) matches the constructor
// spelling past the ceiling too — ReadArrayConstruction answers both
// the same way (sec-array runs the same algorithm for both).
func TestReadArrayConstruction_TheCallSpellingPastTheCeilingBuildsArrayHolesToo(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return Array(50000).length; }\n")
	ctx := superArrayContracts(t, p)
	built := ReadArrayConstruction(ctx, NewEnv(), superArrayCallIn(t, p, "f"))
	if built == nil {
		t.Fatalf("ReadArrayConstruction answered nil for the call spelling `Array(50000)`")
	}
	length, ok := abstractdomain.LengthOfArrayHoles(*built)
	if built.Kind != abstractdomain.KindArrayHoles || !ok || length != 50000 {
		t.Fatalf("`Array(50000)` = %+v, want KindArrayHoles wrapping length 50000", *built)
	}
}

// The throwing shapes model no array whether or not the length would
// have crossed the materialization ceiling — the RangeError contract
// runs before this model is asked, so a length ToUint32 cannot fix
// exactly answers nil regardless of magnitude.
func TestReadArrayConstruction_AThrowingLengthPastTheCeilingStillAnswersNothing(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(-50000).length + new Array(4294967296).length + new Array(50000.5).length; }\n")
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
	if len(news) != 3 {
		t.Fatalf("found %d `new Array(...)` expressions in the body, want 3", len(news))
	}
	for _, n := range news {
		if got := ReadArrayConstruction(ctx, NewEnv(), n); got != nil {
			t.Errorf("a throwing length answered an array: %+v — the construction throws a RangeError and has no value", *got)
		}
	}
}

/* ── the abstract domain: KindArrayHoles' own claims ─────────────── */

// The reworked representation's own two claims, checked directly:
// Inner wraps the exact length as an ordinary KnownValues scalar
// (LengthOfArrayHoles reads it back), and ElementSet is the kernel's
// own ∅ — never a RepeatOf/star claim about the array's OWN
// membership (RepeatOf(∅, n, n) for n > 0 denotes ∅ itself, which
// would wrongly make the array a subset of every annotation).
func TestKnownArrayHoles_WrapsTheLengthScalarAndTheEmptyElementSet(t *testing.T) {
	built := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	if built.Inner == nil {
		t.Fatalf("KnownArrayHoles(50000).Inner is nil, want the wrapped {50000} scalar")
	}
	if built.Inner.Kind != abstractdomain.KindValues || len(built.Inner.Values) != 1 || built.Inner.Values[0] != 50000 {
		t.Errorf("KnownArrayHoles(50000).Inner = %+v, want KindValues{50000}", *built.Inner)
	}
	if !sameEmptyElementSet(built.ElementSet) {
		t.Errorf("KnownArrayHoles(50000).ElementSet = %+v, want the empty set (OneOf(nil))", built.ElementSet)
	}
	length, ok := abstractdomain.LengthOfArrayHoles(built)
	if !ok || length != 50000 {
		t.Errorf("LengthOfArrayHoles(built) = (%d, %v), want (50000, true)", length, ok)
	}
}

func TestKnownArrayHoles_SameKnownComparesByLengthAlone(t *testing.T) {
	a := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	b := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	c := abstractdomain.KnownArrayHoles(40, abstractdomain.TrustProved, false)
	if !abstractdomain.SameKnown(a, b) {
		t.Errorf("two KindArrayHoles of the same length should compare equal")
	}
	if abstractdomain.SameKnown(a, c) {
		t.Errorf("two KindArrayHoles of different lengths should NOT compare equal")
	}
}

// Two array-holes of the SAME length that disagree on density are NOT
// the same claim: Object.keys(arr) answers [] for the sparse one and
// n index strings for the dense one (object_static_models.go) — an
// observable difference SameKnown must not paper over.
func TestKnownArrayHoles_SameKnownDistinguishesDenseFromSparse(t *testing.T) {
	sparse := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	dense := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, true)
	if abstractdomain.SameKnown(sparse, dense) {
		t.Errorf("a sparse and a dense KindArrayHoles of the same length compared equal, want NOT equal — Object.keys reads them apart")
	}
}

func TestKnownArrayHoles_IsAlwaysTruthy(t *testing.T) {
	zero := abstractdomain.KnownArrayHoles(0, abstractdomain.TrustProved, false)
	v, known := abstractdomain.Truthiness(zero)
	if !known || !v {
		t.Errorf("Truthiness(KindArrayHoles{0}) = (%v, %v), want (true, true) — every array, even length 0, is an Object", v, known)
	}
}

func TestKnownArrayHoles_JoinOfEqualLengthsStaysExact(t *testing.T) {
	a := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	b := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	joined := abstractdomain.JoinKnown(a, b)
	length, ok := abstractdomain.LengthOfArrayHoles(joined)
	if joined.Kind != abstractdomain.KindArrayHoles || !ok || length != 50000 {
		t.Errorf("JoinKnown of two equal-length KindArrayHoles = %+v, want KindArrayHoles wrapping length 50000", joined)
	}
	if !joined.DenseKnown || joined.Dense {
		t.Errorf("JoinKnown of two SPARSE (dense=false) array-holes = %+v, want DenseKnown=true, Dense=false", joined)
	}
}

func TestKnownArrayHoles_JoinOfDifferingLengthsGoesToUnknown(t *testing.T) {
	a := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	b := abstractdomain.KnownArrayHoles(60000, abstractdomain.TrustProved, false)
	joined := abstractdomain.JoinKnown(a, b)
	if joined.Kind != abstractdomain.KindUnknown {
		t.Errorf("JoinKnown of two DIFFERENT-length KindArrayHoles = %+v, want Unknown", joined)
	}
}

// A density DISAGREEMENT at the SAME length keeps the Length/
// ElementSet determination (every pre-existing read still answers)
// but drops DenseKnown — Object.keys cannot honestly answer either
// [] or the index list for a value that came through both a sparse
// and a dense arm.
func TestKnownArrayHoles_JoinOfDisagreeingDensityKeepsLengthButDropsDenseKnown(t *testing.T) {
	sparse := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	dense := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, true)
	joined := abstractdomain.JoinKnown(sparse, dense)
	length, ok := abstractdomain.LengthOfArrayHoles(joined)
	if joined.Kind != abstractdomain.KindArrayHoles || !ok || length != 50000 {
		t.Errorf("JoinKnown of a sparse and a dense array-holes of the same length = %+v, want KindArrayHoles wrapping length 50000", joined)
	}
	if joined.DenseKnown {
		t.Errorf("JoinKnown of a sparse and a dense array-holes = %+v, want DenseKnown=false", joined)
	}
}

/* ── the walk: `.length` and element reads, through the kernel ───── */

func TestEvaluate_TheLargeArrayConstructorLengthReadsExactlyAndRefutesAge(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(50000).length; }\n")
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	lengthRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .length read", ast.IsPropertyAccessExpression)
	value := evaluateExpression(ctx, NewEnv(), lengthRead)
	superArrayExactScalar(t, kernel, value, 50000, "new Array(50000).length")

	// the Age refutation the fixture pins: [0, 120] excludes 50000, so a
	// checked Age position must refuse this value — StateOfKnown's own
	// set must exclude every point of Age, which superArrayExactScalar
	// already proved by excluding the neighbors; here the direct claim
	// is that 120 (Age's own ceiling) is excluded too
	state, ok := StateOfKnown(value)
	if !ok || state.Top {
		t.Fatalf("new Array(50000).length did not spell as a scalar state: %+v", value)
	}
	if kernel.Member(state.Set, []float64{120}) {
		t.Errorf("new Array(50000).length's state admits 120 — Age's own ceiling — so it would not refute an Age sink")
	}
}

func TestEvaluate_TheLargeArrayElementReadIsAbsent(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t,
		"function f(): unknown { return new Array(50000)[0]; }\n")
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel

	elementRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the [0] element read", ast.IsElementAccessExpression)
	value := evaluateExpression(ctx, NewEnv(), elementRead)
	if value.Kind != abstractdomain.KindUndef {
		t.Errorf("new Array(50000)[0] = %+v, want the absent value exactly", value)
	}
}

/* ── Array.from({length: n}) past the ceiling: the same dense claim
   — sec-array.from's array-like branch (specifications/javascript/spec.html,
   sec-array.from) writes an OWN property at every index via
   CreateDataPropertyOrThrow, so the result is DENSE undefined values,
   not sparse holes like `new Array(n)` — but KindArrayHoles' two
   claims (length {n}, present-element-set ∅) cover both shapes
   soundly: every read path answers identically either way. ───────── */

func TestReadArrayFrom_ArrayLikeOfLengthPastTheCeilingWithNoMapperBuildsArrayHoles(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return Array.from({length: 50000}).length; }\n")
	ctx := superArrayContracts(t, p)
	// `Array.from({length: 50000}).length` parses as a PropertyAccess
	// whose expression is the CallExpression — the first CallExpression
	// in the body IS the Array.from(...) call itself
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindArrayHoles {
		t.Fatalf("Array.from({length: 50000}) = %+v, want KindArrayHoles", built)
	}
	length, ok := abstractdomain.LengthOfArrayHoles(built)
	if !ok || length != 50000 {
		t.Errorf("Array.from({length: 50000})'s length = %d (ok=%v), want 50000", length, ok)
	}
}

func TestReadArrayFrom_ArrayLikeOfLengthAtTheCeilingStillMaterializes(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return Array.from({length: 10000}).length; }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindList || len(built.Items) != 10000 {
		t.Fatalf("Array.from({length: 10000}) = %+v (kind %v), want the 10000-slot KindList", built, built.Kind)
	}
}

// A MAPPER argument still declines past the ceiling — the model can
// only answer a mapped output by inlining the callback per index
// (sec-array.from step 10.d.i, Call(mapper, thisArg, « kValue, 𝔽(k)
// »)), which needs materialized items the same way map/filter do.
func TestReadArrayFrom_ArrayLikeOfLengthPastTheCeilingWithAMapperStillDeclines(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): unknown { return Array.from({length: 50000}, (_, i) => i); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind == abstractdomain.KindArrayHoles || built.Kind == abstractdomain.KindList {
		t.Errorf("Array.from({length: 50000}, mapper) = %+v, want a decline (the model cannot inline 50000 callback calls)", built)
	}
}

/* ── spread: `[...new Array(n)]` past the ceiling ────────────────── */

func TestEvaluateArrayLiteral_SpreadOfArrayHolesAloneCopiesTheSameClaim(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return [...new Array(50000)].length; }\n")
	ctx := superArrayContracts(t, p)
	lengthRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .length read", ast.IsPropertyAccessExpression)
	pa := lengthRead.AsPropertyAccessExpression()
	built := evaluateExpression(ctx, NewEnv(), pa.Expression)
	if built.Kind != abstractdomain.KindArrayHoles {
		t.Fatalf("[...new Array(50000)] = %+v, want KindArrayHoles (the copy shortcut)", built)
	}
	length, ok := abstractdomain.LengthOfArrayHoles(built)
	if !ok || length != 50000 {
		t.Errorf("[...new Array(50000)]'s length = %d (ok=%v), want 50000", length, ok)
	}
}

/* ── for-of: the literal-bounded unroll must DECLINE on a hole array
   past the budget — ItemsOf(KindArrayHoles) already answers nil for
   every length (loop_unroll.go:213's `items == nil` gate), verified
   directly rather than by running a whole loop analysis. ──────────── */

func TestItemsOf_ArrayHolesDeclinesAtEveryLength(t *testing.T) {
	small := abstractdomain.KnownArrayHoles(4, abstractdomain.TrustProved, false)
	if items := ItemsOf(small); items != nil {
		t.Errorf("ItemsOf(KindArrayHoles{4}) = %+v, want nil — array-holes never materializes items, even below LoopUnrollBudget", items)
	}
	large := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	if items := ItemsOf(large); items != nil {
		t.Errorf("ItemsOf(KindArrayHoles{50000}) = %+v, want nil", items)
	}
}

// ElementOf still answers exactly (undefined at every position), which
// is what a for-of loop's per-pass BINDING reads even where the
// literal unroll itself declines and the fixpoint path takes the loop
// instead (loop_fixpoint.go reads ElementOf, not ItemsOf).
func TestElementOf_ArrayHolesIsUndefinedAtEveryPosition(t *testing.T) {
	holes := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	element := ElementOf(holes)
	if element.Kind != abstractdomain.KindUndef {
		t.Errorf("ElementOf(KindArrayHoles{50000}) = %+v, want the absent value exactly", element)
	}
}

/* ── .join(): exactly (length−1) copies of the separator ─────────── */

func TestEvaluate_LargeArrayJoinPastTheCeilingIsTheWindowedCommaString(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): string { return new Array(50000).join(\",\"); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .join(...) call", ast.IsCallExpression)
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindSet {
		t.Fatalf("new Array(50000).join(\",\") = %+v, want KindSet (the windowed exact-length claim past the materialization ceiling)", built)
	}
	rep, ok := refinementsets.AsRepetition(built.Set)
	if !ok {
		t.Fatalf("new Array(50000).join(\",\")'s set does not read back as a repetition: %+v", built.Set)
	}
	if rep.Lo != 49999 || rep.Hi == nil || *rep.Hi != 49999 {
		t.Errorf("new Array(50000).join(\",\")'s window = [%d, %v], want the exact window [49999, 49999]", rep.Lo, rep.Hi)
	}
	if !sameOneCharacterSet(rep.Element, ',') {
		t.Errorf("new Array(50000).join(\",\")'s element set = %+v, want the single character ','", rep.Element)
	}
}

// The boundary just past the ceiling on BOTH counts: 11001 already
// makes `new Array(11001)` itself a KindArrayHoles receiver
// (11001 > arrayConstructionHoleLimit), and its join's own copy count
// (11000) is also past the ceiling — confirming the windowed path
// fires.
func TestEvaluate_ArrayHolesJoinPastTheCeilingOnBothCountsIsWindowed(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): string { return new Array(11001).join(\",\"); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .join(...) call", ast.IsCallExpression)
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindSet {
		t.Fatalf("new Array(11001).join(\",\") = %+v, want KindSet — 11000 copies is past arrayConstructionHoleLimit", built)
	}
}

// The narrower boundary: `new Array(10001)` is ALREADY a KindArrayHoles
// receiver (10001 > arrayConstructionHoleLimit), but its join's own
// copy count (10000) sits EXACTLY AT the ceiling — the materialized
// branch (copies <= arrayConstructionHoleLimit) still fires from a
// KindArrayHoles receiver, proving the two ceilings (the array's own
// construction ceiling, and join's copy-count ceiling) are checked
// independently rather than one gating the other.
func TestEvaluate_ArrayHolesJoinWithCopyCountAtTheCeilingStillMaterializes(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): string { return new Array(10001).join(\",\"); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .join(...) call", ast.IsCallExpression)
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveString {
		t.Fatalf("new Array(10001).join(\",\") = %+v, want the exact materialized string (10000 copies, at the ceiling)", built)
	}
	if len(built.Values) != 10000 {
		t.Errorf("new Array(10001).join(\",\") has %d codepoints, want 10000", len(built.Values))
	}
}

func TestEvaluate_ArrayJoinWithFewHolesMaterializesExactly(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): string { return new Array(4).join(\",\"); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .join(...) call", ast.IsCallExpression)
	built := evaluateExpression(ctx, NewEnv(), call)
	// new Array(4) materializes as KindList (4 <= arrayConstructionHoleLimit),
	// so this exercises the EXISTING KindList join path, not the new
	// KindArrayHoles one — kept as the boundary control
	if built.Kind != abstractdomain.KindValues || built.KindTag != abstractdomain.PrimitiveString {
		t.Fatalf("new Array(4).join(\",\") = %+v, want the exact materialized string", built)
	}
	if got := stringOfCodepoints(built.Values); got != ",,," {
		t.Errorf("new Array(4).join(\",\") = %q, want \",,,\"", got)
	}
}

// sameOneCharacterSet checks a RefinedSet is exactly OneOf([codepoint
// of r]) — the single-character alphabet the windowed join claim uses.
func sameOneCharacterSet(set refinementsets.RefinedSet, r rune) bool {
	if len(set.Forms) != 1 {
		return false
	}
	form := set.Forms[0]
	return form.Form == refinementsets.FormOneOf && len(form.W) == 1 && form.W[0] == float64(r)
}

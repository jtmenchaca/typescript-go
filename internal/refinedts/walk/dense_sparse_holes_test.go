// The dense/sparse distinction on KindArrayHoles: `new Array(n)` past
// the materialization ceiling is SPARSE (sec-array never calls
// CreateDataPropertyOrThrow, so no index is an own property —
// EnumerableOwnProperties, sec-enumerableownproperties, yields
// nothing); `Array.from({length: n})` past the ceiling is DENSE
// (sec-array.from's array-like branch calls CreateDataPropertyOrThrow
// at every index 0..n-1, so all n are own properties). Every read
// path landed before this file (.length, an element read,
// Array.isArray, instanceof, truthiness, spread, for-of, .join())
// answers identically either way — Object.keys/values/entries
// (object_static_models.go) are the one place the two shapes read
// apart, because they walk OWN property keys only
// (sec-enumerableownproperties). A KindArrayHoles receiver only ever
// exists PAST arrayConstructionHoleLimit (below it,
// ReadArrayConstruction/readArrayFrom both build a plain KindList
// instead — see exact_length_array_test.go), so every case here is
// necessarily a past-the-ceiling length. specifications/javascript/spec.html
// clauses cited throughout.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

/* ── the abstract domain: Dense/DenseKnown on KindArrayHoles ──────── */

func TestKnownArrayHoles_SparseCallSiteSetsDenseFalseAndDenseKnownTrue(t *testing.T) {
	sparse := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	if !sparse.DenseKnown || sparse.Dense {
		t.Errorf("KnownArrayHoles(50000, proved, false) = %+v, want DenseKnown=true, Dense=false", sparse)
	}
}

func TestKnownArrayHoles_DenseCallSiteSetsDenseTrueAndDenseKnownTrue(t *testing.T) {
	dense := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, true)
	if !dense.DenseKnown || !dense.Dense {
		t.Errorf("KnownArrayHoles(50000, proved, true) = %+v, want DenseKnown=true, Dense=true", dense)
	}
}

func TestKnownArrayHoles_SameKnownDistinguishesDenseFromSparseAtEqualLength(t *testing.T) {
	sparse := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	dense := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, true)
	if abstractdomain.SameKnown(sparse, dense) {
		t.Errorf("SameKnown(sparse, dense) at equal length = true, want false — Object.keys reads them apart")
	}
	sparse2 := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	if !abstractdomain.SameKnown(sparse, sparse2) {
		t.Errorf("SameKnown(sparse, sparse) at equal length = false, want true")
	}
}

func TestKnownArrayHoles_JoinOfTwoSparseStaysExactAndSparse(t *testing.T) {
	a := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	b := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	joined := abstractdomain.JoinKnown(a, b)
	length, ok := abstractdomain.LengthOfArrayHoles(joined)
	if joined.Kind != abstractdomain.KindArrayHoles || !ok || length != 50000 {
		t.Fatalf("JoinKnown(sparse, sparse) = %+v, want KindArrayHoles wrapping length 50000", joined)
	}
	if !joined.DenseKnown || joined.Dense {
		t.Errorf("JoinKnown(sparse, sparse) = %+v, want DenseKnown=true, Dense=false", joined)
	}
}

func TestKnownArrayHoles_JoinOfTwoDenseStaysExactAndDense(t *testing.T) {
	a := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, true)
	b := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, true)
	joined := abstractdomain.JoinKnown(a, b)
	length, ok := abstractdomain.LengthOfArrayHoles(joined)
	if joined.Kind != abstractdomain.KindArrayHoles || !ok || length != 50000 {
		t.Fatalf("JoinKnown(dense, dense) = %+v, want KindArrayHoles wrapping length 50000", joined)
	}
	if !joined.DenseKnown || !joined.Dense {
		t.Errorf("JoinKnown(dense, dense) = %+v, want DenseKnown=true, Dense=true", joined)
	}
}

// A density DISAGREEMENT at the same length keeps every pre-existing
// determination (Length/ElementSet — the array is still exactly a
// KindArrayHoles of length 50000, so .length/isArray/truthiness/etc.
// all still answer) but drops DenseKnown, since neither Dense=true
// nor Dense=false is honest for a value that came through both a
// sparse and a dense construction arm.
func TestKnownArrayHoles_JoinOfDisagreeingDensityKeepsLengthDropsDenseKnown(t *testing.T) {
	sparse := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, false)
	dense := abstractdomain.KnownArrayHoles(50000, abstractdomain.TrustProved, true)
	joined := abstractdomain.JoinKnown(sparse, dense)
	length, ok := abstractdomain.LengthOfArrayHoles(joined)
	if joined.Kind != abstractdomain.KindArrayHoles || !ok || length != 50000 {
		t.Fatalf("JoinKnown(sparse, dense) = %+v, want KindArrayHoles STILL wrapping length 50000 (the pre-existing determination must not regress)", joined)
	}
	if joined.DenseKnown {
		t.Errorf("JoinKnown(sparse, dense) = %+v, want DenseKnown=false", joined)
	}
	// truthiness is unaffected by the dropped DenseKnown — proving the
	// pre-existing determination really did survive, not just the kind tag
	v, known := abstractdomain.Truthiness(joined)
	if !known || !v {
		t.Errorf("Truthiness(joined) = (%v, %v), want (true, true) — every array is an Object regardless of density", v, known)
	}
}

/* ── the walk: Object.keys/values/entries over a SPARSE receiver ──── */

func TestObjectKeys_OverSparseArrayHolesAnswersTheExactEmptyList(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): string[] { return Object.keys(new Array(50000)); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindList || len(built.Items) != 0 {
		t.Fatalf("Object.keys(new Array(50000)) = %+v, want the exact empty KindList — sec-array never makes an index an own property", built)
	}
}

func TestObjectValues_OverSparseArrayHolesAnswersTheExactEmptyList(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): unknown[] { return Object.values(new Array(50000)); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindList || len(built.Items) != 0 {
		t.Fatalf("Object.values(new Array(50000)) = %+v, want the exact empty KindList", built)
	}
}

func TestObjectEntries_OverSparseArrayHolesAnswersTheExactEmptyList(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): [string, unknown][] { return Object.entries(new Array(50000)); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindList || len(built.Items) != 0 {
		t.Fatalf("Object.entries(new Array(50000)) = %+v, want the exact empty KindList", built)
	}
}

/* ── the walk: Object.values over a DENSE receiver — the one arm
   that does NOT decline, because its claim (undefined at every
   position) collapses to the SAME already-existing KindArrayHoles
   vocabulary rather than needing a new "n distinct strings" form ─── */

func TestObjectValues_OverDenseArrayHolesAnswersUndefinedAtExactlyLength(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): unknown { return Object.values(Array.from({length: 50000})); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindArrayHoles {
		t.Fatalf("Object.values(Array.from({length: 50000})) = %+v, want KindArrayHoles (50000 undefined values, past the materialization ceiling)", built)
	}
	length, ok := abstractdomain.LengthOfArrayHoles(built)
	if !ok || length != 50000 {
		t.Errorf("Object.values(...)'s length = %d (ok=%v), want 50000", length, ok)
	}
	// the RESULT array is itself dense (sec-object.values ->
	// CreateArrayFromList, sec-createarrayfromlist, which calls
	// CreateDataPropertyOrThrow at every index)
	if !built.DenseKnown || !built.Dense {
		t.Errorf("Object.values(...) = %+v, want DenseKnown=true, Dense=true (CreateArrayFromList makes every index an own property)", built)
	}
}

/* ── the walk: Object.keys/entries over a DENSE receiver decline —
   no vocabulary compresses "n distinct increasing decimal-string
   keys," and a KindArrayHoles receiver's length is always past
   arrayConstructionHoleLimit, so there is no in-range case where
   materializing the key list is even an option ─────────────────── */

func TestObjectKeys_OverDenseArrayHolesDeclines(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): unknown { return Object.keys(Array.from({length: 50000})); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind == abstractdomain.KindList || built.Kind == abstractdomain.KindArrayHoles {
		t.Errorf("Object.keys(Array.from({length: 50000})) = %+v, want a decline — no vocabulary compresses the n distinct index-string keys", built)
	}
}

func TestObjectEntries_OverDenseArrayHolesDeclines(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): unknown { return Object.entries(Array.from({length: 50000})); }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind == abstractdomain.KindList || built.Kind == abstractdomain.KindArrayHoles {
		t.Errorf("Object.entries(Array.from({length: 50000})) = %+v, want a decline — same key-string gap as Object.keys", built)
	}
}

// Below the ceiling, Array.from({length: n}) builds a plain KindList
// (readArrayFrom's counted branch), never a KindArrayHoles — the
// boundary control proving the declines above are read off a
// receiver PAST the ceiling specifically, not off Array.from results
// in general. The KindList case is the PRE-EXISTING behavior this
// file must not disturb; object_static_models.go's KindObject arms
// do not apply to a KindList receiver either, so this also declines,
// but for the unrelated, pre-existing reason that a plain KindList's
// items carry no key-name fact at all.
func TestReadArrayFrom_BelowCeilingIsAPlainListNotArrayHoles(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): number { return Array.from({length: 3}).length; }\n")
	ctx := superArrayContracts(t, p)
	call := superArrayCallIn(t, p, "f")
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind != abstractdomain.KindList || len(built.Items) != 3 {
		t.Fatalf("Array.from({length: 3}) = %+v (kind %v), want a 3-slot KindList", built, built.Kind)
	}
}

/* ── the walk: a joined receiver whose density was never established
   (DenseKnown=false) declines Object.keys/values/entries, rather
   than guessing which shape the array actually has ──────────────── */

func TestObjectKeys_OverArrayHolesWithUnknownDensityDeclines(t *testing.T) {
	p := entryEnvTestProgram(t,
		`function f(cond: boolean): unknown {
			return Object.keys(cond ? new Array(50000) : Array.from({length: 50000}));
		}`)
	ctx := superArrayContracts(t, p)
	// the OUTER call is Object.keys(...) — superArrayFirstNode walks in
	// source order and Object.keys(...) is visited before its own
	// argument's nested Array.from(...) call, so ast.IsCallExpression
	// finds Object.keys(...) first
	call := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the Object.keys(...) call", ast.IsCallExpression)
	built := evaluateExpression(ctx, NewEnv(), call)
	if built.Kind == abstractdomain.KindList {
		t.Errorf("Object.keys(...) over a joined sparse-or-dense receiver = %+v, want a decline — DenseKnown is false on the join, so neither [] nor the index list is honest", built)
	}
}

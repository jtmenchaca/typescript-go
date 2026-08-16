// Two precision gaps the hole-array unit (exact_length_array_test.go)
// named but did not close:
//
//  1. Mixed-literal spread folding: `[...new Array(n), 1]` fell to
//     sequenceOfElements' scalar-only fold, which failed the whole
//     literal to unknown on the hole positions' Undef contribution.
//     array_literal.go's sequenceOfElements now folds Undef as "this
//     position may be absent" (abstractdomain.PossiblyUndefined wrapping
//     the union), mirroring JoinKnown's own KindUndef arm
//     (lattice_operations.go) rather than failing the fold outright.
//  2. Exact string-window length: `.length` on a string-shaped KindSet
//     answered only the window's floor even where lo == hi, because the
//     window counts scalar codepoints while .length counts UTF-16 code
//     units — a real divergence on astral content, not an arbitrary
//     omission (array_method_models.go's join comment already named it).
//     evaluate_property_access.go's stringy branch now answers the exact
//     scalar when lo == hi AND the element alphabet is proven astral-free
//     (astralFreeSet, number_range.go) — a BMP separator's window reads
//     back exactly; an astral one keeps today's floor.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── gap 1: mixed-literal spread folding, model-level (no kernel) ──── */

// sequenceOfElements itself: [Undef, exactly 1] must fold to "absent or
// exactly 1" rather than declining to Unknown.
func TestSequenceOfElements_UndefAndExactValueFoldsToPossiblyUndefined(t *testing.T) {
	one := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got := sequenceOfElements([]abstractdomain.AbstractValue{abstractdomain.Undef, one})
	if got.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("sequenceOfElements([Undef, {1}]) = %+v, want KindPossiblyUndefined", got)
	}
	if got.Inner == nil || got.Inner.Kind != abstractdomain.KindSet {
		t.Fatalf("sequenceOfElements([Undef, {1}]).Inner = %+v, want a KindSet star claim", got.Inner)
	}
	rangeOfStar := RangeOfSet(*got.Inner.Set.Forms[0].A_)
	if rangeOfStar == nil || rangeOfStar.Lo != 1 || rangeOfStar.Hi != 1 {
		t.Errorf("sequenceOfElements([Undef, {1}]).Inner's star set = %+v, want the singleton {1}", got.Inner.Set)
	}
}

// An all-Undef sequence has nothing for PossiblyUndefined to wrap — no
// element ever built a set — so the fold stays quiet rather than
// overclaiming a star around nothing.
func TestSequenceOfElements_AllUndefStaysQuiet(t *testing.T) {
	got := sequenceOfElements([]abstractdomain.AbstractValue{abstractdomain.Undef, abstractdomain.Undef})
	if got.Kind == abstractdomain.KindPossiblyUndefined || got.Kind == abstractdomain.KindSet {
		t.Errorf("sequenceOfElements([Undef, Undef]) = %+v, want silence (no set was ever built to wrap)", got)
	}
}

// A genuinely unreadable element (unknown, non-opaque) beside an Undef
// still declines the whole fold — Undef only earns a pass on the
// scalar gate, not a blanket exemption for every other element.
func TestSequenceOfElements_UndefBesideUnknownStillDeclines(t *testing.T) {
	got := sequenceOfElements([]abstractdomain.AbstractValue{abstractdomain.Undef, abstractdomain.Unknown})
	if got.Kind != abstractdomain.KindUnknown {
		t.Errorf("sequenceOfElements([Undef, Unknown]) = %+v, want Unknown", got)
	}
}

/* ── gap 1: the walk, through EvaluateArrayLiteral's spread path ───── */

// `[...new Array(50000), 1]` — the mixed literal named in the task: the
// hole-array spread is not the sole element, so EvaluateArrayLiteral's
// unpinned-length path runs, ElementOf(KindArrayHoles) contributes
// Undef exactly, and the trailing `1` contributes its own singleton.
func TestEvaluateArrayLiteral_SpreadOfArrayHolesAndAnExactValueFoldsMixed(t *testing.T) {
	p := entryEnvTestProgram(t,
		"function f(): unknown { return [...new Array(50000), 1]; }\n")
	ctx := superArrayContracts(t, p)
	lit := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the array literal", ast.IsArrayLiteralExpression)
	built := evaluateExpression(ctx, NewEnv(), lit)
	if built.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("[...new Array(50000), 1] = %+v, want KindPossiblyUndefined (every position absent or exactly 1)", built)
	}
	if built.Inner == nil || built.Inner.Kind != abstractdomain.KindSet {
		t.Fatalf("[...new Array(50000), 1].Inner = %+v, want a KindSet star claim", built.Inner)
	}
}

/* ── gap 2: exact string-window length, the astral-freedom gate ────── */

// astralFreeSet's own two readings: a BMP-only alphabet reads free
// (a comma, and a bounded ASCII range); an alphabet that can reach the
// astral floor, or an unbounded one, reads not-free.
func TestAstralFreeSet_ExactBmpAlphabetReadsFree(t *testing.T) {
	comma := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{44}))
	if !astralFreeSet(comma) {
		t.Errorf("astralFreeSet({44}) = false, want true — a comma is BMP")
	}
	ascii := refinementsets.MakeRefinedSet(refinementsets.AtLeast(32), refinementsets.AtMost(126), refinementsets.Integer)
	if !astralFreeSet(ascii) {
		t.Errorf("astralFreeSet([32,126]) = false, want true — the printable ASCII range is BMP")
	}
}

func TestAstralFreeSet_AstralOrUnboundedAlphabetReadsNotFree(t *testing.T) {
	astral := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0x1F600})) // an emoji scalar
	if astralFreeSet(astral) {
		t.Errorf("astralFreeSet({0x1F600}) = true, want false — an emoji is astral")
	}
	unbounded := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)
	if astralFreeSet(unbounded) {
		t.Errorf("astralFreeSet([0, +inf)) = true, want false — an unbounded alphabet is not proven astral-free")
	}
	strictAtFloor := refinementsets.MakeRefinedSet(refinementsets.Below(0x10000 + 1))
	if astralFreeSet(strictAtFloor) {
		t.Errorf("astralFreeSet(< 0x10001) = true, want false — the enclosed range still reaches the astral floor")
	}
}

// `new Array(50000).join(",")`'s .length: the past-ceiling join answers
// a windowed KindSet with lo == hi == 49999 over the one-character
// alphabet {','} (a BMP separator) — .length on THAT receiver now
// answers the exact scalar, not merely "at least 49999".
//
// The source nests two PropertyAccessExpressions (`.join` and
// `.length`) plus the CallExpression between them, so the plain
// "first PropertyAccessExpression in source order" finder used
// elsewhere in this package would land on `.join`, not `.length` — the
// predicate here also requires the accessed name to be "length" and
// its receiver to be a CallExpression (`new Array(50000).join(",")`,
// not `new Array(50000)` itself).
func TestEvaluate_WindowedJoinLengthIsExactWhenTheSeparatorIsAstralFree(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := entryEnvTestProgram(t,
		"function f(): number { return new Array(50000).join(\",\").length; }\n")
	ctx := superArrayContracts(t, p)
	ctx.Kernel = kernel
	lengthRead := superArrayFirstNode(t, entryEnvFunctionNamed(t, p, "f").Body(), "the .length read after .join(...)", func(node *ast.Node) bool {
		if !ast.IsPropertyAccessExpression(node) {
			return false
		}
		pa := node.AsPropertyAccessExpression()
		name := pa.Name()
		return name != nil && ast.IsIdentifier(name) && name.Text() == "length" && ast.IsCallExpression(pa.Expression)
	})
	value := evaluateExpression(ctx, NewEnv(), lengthRead)
	superArrayExactScalar(t, kernel, value, 49999, "new Array(50000).join(\",\").length")
}

// A general string-shaped KindSet whose window's lo != hi keeps today's
// floor-only answer — bounds that genuinely differ are not this gap's
// fix to make exact, EVEN over a proven astral-free alphabet. This
// checks the exact GATE evaluate_property_access.go's stringy branch
// applies (`rep.Hi != nil && *rep.Hi == rep.Lo && astralFreeSet(...)`)
// against a window built the same way the join model builds its own
// (a one-character BMP alphabet, refinementsets.Repetition) — only the
// bound differs.
func TestPropertyAccess_ExactGateRequiresEqualBoundsEvenWhenAstralFree(t *testing.T) {
	comma := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{44}))
	if !astralFreeSet(comma) {
		t.Fatalf("test setup: {44} (a comma) must read astral-free")
	}
	windowed := refinementsets.Repetition(comma, 3, intPtr(5))
	rep, ok := refinementsets.AsRepetition(windowed)
	if !ok || rep.Hi == nil || rep.Lo == *rep.Hi {
		t.Fatalf("test setup: want a repetition whose lo != hi, got %+v (ok=%v)", rep, ok)
	}
	// this is the exact predicate evaluate_property_access.go's stringy
	// branch gates the singleton answer on — it must read false here
	exact := rep.Hi != nil && *rep.Hi == rep.Lo && astralFreeSet(rep.Element)
	if exact {
		t.Errorf("the exact-length gate fired on a window with lo=%d hi=%d — differing bounds must stay floor-only", rep.Lo, *rep.Hi)
	}
}

func intPtr(i int) *int { return &i }

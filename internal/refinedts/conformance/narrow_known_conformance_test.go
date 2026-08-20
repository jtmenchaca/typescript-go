// OPERATION 2 of the differential harness (see
// differential_harness_test.go for the three-verdict frame).
//
// abstractdomain.NarrowKnown — the adapter's own tightening — held to
// the kernel's proved narrowing, over cmp and eq trees on scalar sets.
//
// THE TWO ROUTES, precisely. The production path is narrowing/
// condition_analysis.go: it builds a kernelbridge.NarrowTree from the
// guard's syntax, asks kernel.Narrow(tree), takes the answer's
// `.Set.Forms`, and hands THOSE forms to NarrowKnown (via
// apply_narrowing.go:583). So the adapter's narrowed answer is
//
//     NarrowKnown(known, kernel.Narrow(tree).WhenTrue.Set.Forms)
//
// — an INTERSECTION the adapter performs locally, over a claim the
// kernel produced. The kernel's own twin of that whole composition is
// NarrowState, which takes the state AND the test and answers the
// filtered state directly, with no adapter-side intersection at all.
// This file runs both and compares by mutual ScalarSubset, exactly as
// lattice_conformance_test.go compares joins.
//
// The trees are built the way condition_analysis.go builds them —
// kernelbridge.NarrowTree with NarrowKindCmp / NarrowKindEq and the
// NarrowOp* comparison ops — so the comparison is over the real
// question shape, not a test-only one.
//
// THE DETERMINATION-GAP LEDGER. NarrowKnown declines (returns its
// input unchanged) for whole families of kinds, each one a body the
// kernel could have narrowed:
//
//   gap-1  KindValues              intersect_refinements.go:42-48
//          returns k unchanged — an exact word is never intersected
//          with a guard's forms, so `if (x > 5)` on a known-exact x
//          leaves the walk holding the pre-guard value. The kernel's
//          NarrowState on the same singleton set answers the filtered
//          set (empty on a refuted guard, the singleton on a held one),
//          which is strictly more. Asserted as a decline below.
//   gap-2  KindKindUnion           :75-79 — the guard leaves the union
//          standing rather than narrowing per arm. This is audit item 6
//          / item 10 (kind-union quantification, C-v): it waits on the
//          kernel's value vocabulary, so it is a NAMED blocked gap, not
//          a flip.
//   gap-3  KindObject / KindList / KindObjectStar / KindArrayHoles /
//          KindCollection / KindPromise / KindDate / KindSymbol /
//          KindHostFunction / KindBigints / KindRegex  — :42-48, all
//          returning k unchanged. These are the C-v vocabulary rows:
//          the kernel has no scalar question that takes them, so the
//          decline is CORRECT today and the gap is the vocabulary
//          decision, not this function.
//   gap-4  KindVariable with StarDepth > 0  — :53-57 returns k. A
//          starred bound is a sequence claim; the guard's scalar forms
//          name no position of it.
//
// THE SCRUTINY CLASS — flagged loudly, incomparable.
//
//   scrutiny-1  KindUndef / KindNull / KindNaN  (:60-67). NarrowKnown
//          returns the value UNCHANGED and the comment argues the
//          guarded branch is unreachable, so keeping the fact is sound.
//          That is the adapter ANSWERING (it hands back a determinate
//          claim) where the kernel would answer a filtered STATE with
//          the absent/NaN flags moved. The two are not comparable
//          through this function's signature at all: NarrowKnown takes
//          FORMS and returns an AbstractValue, so it has no channel for
//          a flag change, while NarrowState's whole job on these inputs
//          is the flag change. The composition NarrowKnown-after-Narrow
//          therefore cannot express what NarrowState answers for these
//          three kinds, and no assertion here can compare them. The
//          production path does not lose the fact — apply_narrowing.go
//          handles definedness and NaN structurally, on a different
//          channel (Narrowed.Definedness / .Truthiness) — but that
//          channel is OUTSIDE this operation, and this file does not
//          silently pretend the comparison was made.
//   scrutiny-2  KindPossiblyUndefined / KindPossiblyNaN (:68-74) strip
//          their wrapper and recurse. Stripping is a CLAIM — "a held
//          set-comparison proves the value was PRESENT" — and it is the
//          same claim NarrowState makes when it clears the flags on the
//          whenTrue side, so these rows ARE comparable and are asserted
//          as AGREEMENT rows below (the wrapper strips on both routes).

package conformance

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// narrowRow is one differential row: a scalar set the walk might hold,
// and a guard tree the reader might have lowered.
type narrowRow struct {
	name  string
	set   refinementsets.RefinedSet
	tree  kernelbridge.NarrowTree
	state func(set refinementsets.RefinedSet) kernelbridge.KnownStateWire
}

func plainState(set refinementsets.RefinedSet) kernelbridge.KnownStateWire {
	return kernelbridge.KnownStateWire{Set: set}
}

// narrowStateOpOf maps a leaf tree to the (op, w, hasW) triple
// NarrowState takes, for the leaves NarrowState speaks. NarrowState's
// vocabulary is narrower than Narrow's — it takes definedness,
// the two truthiness sorts, and "eq" — so only the eq rows compare
// through it, and the cmp rows compare through the Narrow-then-intersect
// composition against a hand-spelled expected set instead.
func narrowStateOpOf(tree kernelbridge.NarrowTree) (op string, w float64, hasW bool, ok bool) {
	if tree.Kind == kernelbridge.NarrowKindEq {
		return "eq", tree.K, true, true
	}
	return "", 0, false, false
}

// TestNarrowKnownAgreesWithTheKernelOverCmpAndEqTrees is the AGREEMENT
// half: for every scalar set and guard tree below, the adapter's
// composition (kernel.Narrow for the claim, NarrowKnown for the
// intersection) and the kernel's own NarrowState must admit the same
// values. Drift is a failure.
func TestNarrowKnownAgreesWithTheKernelOverCmpAndEqTrees(t *testing.T) {
	kernel := differentialKernel(t)

	window := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(10))
	integers := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(10), refinementsets.Integer)
	unbounded := refinementsets.MakeRefinedSet(refinementsets.AtLeast(-5))

	rows := []narrowRow{
		{"[0,10] eq 5", window,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 5}, plainState},
		{"[0,10] eq 0", window,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 0}, plainState},
		{"[0,10] eq 10", window,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 10}, plainState},
		// a value the set does not hold: truth admits nothing, and the
		// two routes must agree that it admits nothing
		{"[0,10] eq 99", window,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 99}, plainState},
		{"[0,10] eq -3", window,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: -3}, plainState},
		{"[0,10]∩ℤ eq 7", integers,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 7}, plainState},
		// an integer-marked set against a NON-integer word: truth admits
		// nothing, the sharpest place the two intersections could differ
		{"[0,10]∩ℤ eq 7.5", integers,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 7.5}, plainState},
		{"[-5,∞) eq 0", unbounded,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 0}, plainState},
	}

	for _, row := range rows {
		op, w, hasW, speaks := narrowStateOpOf(row.tree)
		if !speaks {
			t.Fatalf("%s: narrowStateOpOf declined a row this test poses", row.name)
		}
		// the ADAPTER route, exactly as production composes it
		answer := kernel.Narrow(row.tree)
		if answer.WhenTrue == nil {
			t.Errorf("%s: kernel.Narrow answered no whenTrue claim — the composition has no forms to intersect", row.name)
			continue
		}
		adapter := abstractdomain.NarrowKnown(
			abstractdomain.KnownSet(row.set, nil, abstractdomain.TrustProved,
				abstractdomain.SetKindTagNone),
			answer.WhenTrue.Set.Forms,
		)
		adapterSet, adapterOK := abstractdomain.SetOfKnown(adapter)
		if !adapterOK {
			t.Errorf("%s: SetOfKnown declined the narrowed value — SCRUTINY: the adapter holds a claim the tuple layer cannot state", row.name)
			continue
		}
		// the KERNEL route
		kernelTrue, _ := kernel.NarrowState(row.state(row.set), op, w, hasW)
		if kernelTrue.Top {
			t.Errorf("%s: kernel NarrowState answered top on a concrete state", row.name)
			continue
		}
		if !sameSet(kernel, adapterSet, kernelTrue.Set) {
			t.Errorf("%s: the adapter's narrowed set and the kernel's NarrowState set admit different values — the two routes disagree", row.name)
		}
	}
}

// TestNarrowKnownAgreesOnTheRefutedSide holds the SAME rows on the
// whenFalse side. The false side is where the two routes most easily
// part: the adapter intersects a DIFFERENCE form the kernel handed it,
// while the kernel filters directly, and a difference spelled two ways
// is exactly what mutual containment exists to compare.
func TestNarrowKnownAgreesOnTheRefutedSide(t *testing.T) {
	kernel := differentialKernel(t)

	window := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(10))
	integers := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(10), refinementsets.Integer)

	rows := []narrowRow{
		{"[0,10] not-eq 5", window,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 5}, plainState},
		{"[0,10] not-eq 0", window,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 0}, plainState},
		{"[0,10] not-eq 99", window,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 99}, plainState},
		{"[0,10]∩ℤ not-eq 7", integers,
			kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 7}, plainState},
	}

	for _, row := range rows {
		op, w, hasW, speaks := narrowStateOpOf(row.tree)
		if !speaks {
			t.Fatalf("%s: narrowStateOpOf declined a row this test poses", row.name)
		}
		answer := kernel.Narrow(row.tree)
		if answer.WhenFalse == nil {
			t.Errorf("%s: kernel.Narrow answered no whenFalse claim", row.name)
			continue
		}
		adapter := abstractdomain.NarrowKnown(
			abstractdomain.KnownSet(row.set, nil, abstractdomain.TrustProved,
				abstractdomain.SetKindTagNone),
			answer.WhenFalse.Set.Forms,
		)
		adapterSet, adapterOK := abstractdomain.SetOfKnown(adapter)
		if !adapterOK {
			t.Errorf("%s: SetOfKnown declined the narrowed value", row.name)
			continue
		}
		_, kernelFalse := kernel.NarrowState(row.state(row.set), op, w, hasW)
		if kernelFalse.Top {
			t.Errorf("%s: kernel NarrowState answered top on a concrete state", row.name)
			continue
		}
		if !sameSet(kernel, adapterSet, kernelFalse.Set) {
			t.Errorf("%s: the adapter's refuted set and the kernel's NarrowState refuted set admit different values — the two routes disagree", row.name)
		}
	}
}

// TestNarrowKnownFoldsComparisonTreesTheKernelWay covers the cmp
// leaves, which NarrowState does NOT speak (its vocabulary is
// definedness / the two truthiness sorts / eq). The comparison here is
// against the kernel's OWN intersection, replayed through ValidateChain
// the way replay_test.go replays a guard: the adapter's local fold and
// the kernel's derived chain must admit the same values on every probe.
func TestNarrowKnownFoldsComparisonTreesTheKernelWay(t *testing.T) {
	kernel := differentialKernel(t)

	window := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(10))
	probes := []float64{-1, 0, 1, 3, 5, 7, 10, 11}

	trees := []struct {
		name string
		tree kernelbridge.NarrowTree
	}{
		{"x >= 5", kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp,
			Op: kernelbridge.NarrowOpGe, K: 5}},
		{"x > 5", kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp,
			Op: kernelbridge.NarrowOpGt, K: 5}},
		{"x <= 5", kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp,
			Op: kernelbridge.NarrowOpLe, K: 5}},
		{"x < 5", kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp,
			Op: kernelbridge.NarrowOpLt, K: 5}},
		{"x >= 0", kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp,
			Op: kernelbridge.NarrowOpGe, K: 0}},
		{"x < 0", kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindCmp,
			Op: kernelbridge.NarrowOpLt, K: 0}},
		{"x >= 99 — refutes the whole window", kernelbridge.NarrowTree{
			Kind: kernelbridge.NarrowKindCmp, Op: kernelbridge.NarrowOpGe, K: 99}},
	}

	for _, row := range trees {
		answer := kernel.Narrow(row.tree)
		if answer.WhenTrue == nil {
			t.Errorf("%s: kernel.Narrow answered no whenTrue claim", row.name)
			continue
		}
		claim := answer.WhenTrue.Set
		adapter := abstractdomain.NarrowKnown(
			abstractdomain.KnownSet(window, nil, abstractdomain.TrustProved,
				abstractdomain.SetKindTagNone),
			claim.Forms,
		)
		adapterSet, adapterOK := abstractdomain.SetOfKnown(adapter)
		if !adapterOK {
			t.Errorf("%s: SetOfKnown declined the narrowed value", row.name)
			continue
		}
		for _, probe := range probes {
			local := kernel.Member(adapterSet, []float64{probe})
			replayed := kernel.ValidateChain(kernelbridge.Chain{
				Root: window,
				Ops: []kernelbridge.ChainOp{
					{Op: kernelbridge.ChainOpIntersectWith, Set: claim},
					{Op: kernelbridge.ChainOpMemberQ, Tuple: []float64{probe}},
				},
			})
			if replayed.Kind != kernelbridge.ValidateChainAnswer {
				t.Errorf("%s: the kernel chain declined at probe %v", row.name, probe)
				continue
			}
			if replayed.Answer != local {
				t.Errorf("%s: adapter admits %v = %v, kernel chain = %v — the two routes disagree",
					row.name, probe, local, replayed.Answer)
			}
		}
	}
}

// TestNarrowKnownDeclinesWhereTheKernelWouldNarrow is the
// DETERMINATION-GAP half: gap-1 and gap-2 above, asserted as declines.
// A row that starts narrowing fails here and forces the ledger current.
func TestNarrowKnownDeclinesWhereTheKernelWouldNarrow(t *testing.T) {
	kernel := differentialKernel(t)

	// gap-1: an EXACT word, guarded by `=== 5`. NarrowKnown hands the
	// value straight back; the kernel's NarrowState filters it.
	exactSeven := exactValue(7)
	claim := kernel.Narrow(kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 5})
	if claim.WhenTrue == nil {
		t.Fatalf("kernel.Narrow(eq 5) answered no whenTrue claim")
	}
	narrowed := abstractdomain.NarrowKnown(exactSeven, claim.WhenTrue.Set.Forms)
	if !abstractdomain.SameKnown(narrowed, exactSeven) {
		t.Errorf("gap-1: NarrowKnown now narrows a KindValues — the determination gap closed and this file's ledger is stale")
	}
	// the size of the gap: the kernel refutes the whole value
	kernelTrue, _ := kernel.NarrowState(plainState(exactSet(7)), "eq", 5, true)
	if kernelTrue.Top {
		t.Fatalf("gap-1: kernel NarrowState answered top on a concrete state")
	}
	if !kernel.ScalarEmpty(kernelTrue.Set) {
		t.Errorf("gap-1: kernel NarrowState(={7}, eq 5) admits something — the ledger records the wrong gap size")
	}

	// gap-2: a KIND UNION. The guard leaves it standing (audit item 6 /
	// 10, C-v — blocked on the kernel's value vocabulary, so this is a
	// NAMED blocked gap rather than a flip).
	union := abstractdomain.KindUnionOf([]abstractdomain.AbstractValue{
		exactValue(1),
		exactValue(2),
	})
	if union.Kind != abstractdomain.KindKindUnion {
		t.Skipf("gap-2: KindUnionOf collapsed the two arms to %v — the row needs a shape that stays a union", union.Kind)
	}
	unionNarrowed := abstractdomain.NarrowKnown(union, claim.WhenTrue.Set.Forms)
	if !abstractdomain.SameKnown(unionNarrowed, union) {
		t.Errorf("gap-2: NarrowKnown now narrows a KindKindUnion — the determination gap closed and this file's ledger is stale")
	}
}

// TestNarrowKnownStripsWrappersTheWayTheKernelClearsFlags is
// scrutiny-2, asserted: the adapter's wrapper strip and the kernel's
// flag clear are the same claim, so the two routes must land on the
// same admitted set AND the kernel must really clear the flag.
func TestNarrowKnownStripsWrappersTheWayTheKernelClearsFlags(t *testing.T) {
	kernel := differentialKernel(t)

	window := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(10))
	inner := abstractdomain.KnownSet(window, nil, abstractdomain.TrustProved,
		abstractdomain.SetKindTagNone)

	claim := kernel.Narrow(kernelbridge.NarrowTree{Kind: kernelbridge.NarrowKindEq, K: 5})
	if claim.WhenTrue == nil {
		t.Fatalf("kernel.Narrow(eq 5) answered no whenTrue claim")
	}

	rows := []struct {
		name    string
		value   abstractdomain.AbstractValue
		state   kernelbridge.KnownStateWire
		flagged func(kernelbridge.KnownStateWire) bool
	}{
		{
			name:  "possibly-undefined strips",
			value: abstractdomain.PossiblyUndefined(inner, "", false, false),
			state: kernelbridge.KnownStateWire{Set: window, Undef: true, Null: true},
			flagged: func(s kernelbridge.KnownStateWire) bool {
				return s.Undef || s.Null
			},
		},
		{
			name:  "possibly-NaN strips",
			value: abstractdomain.PossiblyNaN(inner),
			state: kernelbridge.KnownStateWire{Set: window, Nan: true},
			flagged: func(s kernelbridge.KnownStateWire) bool {
				return s.Nan
			},
		},
	}

	for _, row := range rows {
		narrowed := abstractdomain.NarrowKnown(row.value, claim.WhenTrue.Set.Forms)
		if narrowed.Kind == abstractdomain.KindPossiblyUndefined ||
			narrowed.Kind == abstractdomain.KindPossiblyNaN {
			t.Errorf("%s: NarrowKnown kept the wrapper — a held set comparison proves the value present and real", row.name)
			continue
		}
		adapterSet, adapterOK := abstractdomain.SetOfKnown(narrowed)
		if !adapterOK {
			t.Errorf("%s: SetOfKnown declined the narrowed value", row.name)
			continue
		}
		kernelTrue, _ := kernel.NarrowState(row.state, "eq", 5, true)
		if kernelTrue.Top {
			t.Errorf("%s: kernel NarrowState answered top on a concrete state", row.name)
			continue
		}
		if row.flagged(kernelTrue) {
			t.Errorf("%s: the kernel's whenTrue side still carries the flag — the adapter's strip claims more than the proof", row.name)
		}
		if !sameSet(kernel, adapterSet, kernelTrue.Set) {
			t.Errorf("%s: the stripped set and the kernel's filtered set admit different values", row.name)
		}
	}
}

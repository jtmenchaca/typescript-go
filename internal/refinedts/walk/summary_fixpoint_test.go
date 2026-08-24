// The certified constant fixpoint, driven through its stubbed seams so
// the iteration and the certification can be exercised with no kernel
// loaded. The seams are the three package variables summary_fixpoint.go
// declares — the const-summary builder, the applier, and the
// containment asker — swapped the way summary_registry_test.go swaps
// the registry's builder.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// fixpointSeams holds the stubs one test installs, restoring whatever
// was there when the test ends.
type fixpointSeams struct {
	Build   func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool)
	Apply   func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool)
	Contain func(a, b refinementsets.RefinedSet) (subset bool, decided bool)
	Compile func(ctx *FlowContext, declaration *ast.Node, selfBlob kernelbridge.SummaryBlob) (kernelbridge.SummaryBlob, LoweredSummary, bool)
}

func withFixpointSeams(t *testing.T, seams fixpointSeams) {
	t.Helper()
	heldBuild, heldApply := constSummaryBuilder, applySummaryAsker
	heldContain, heldCompile := setContainmentAsker, selfConstCompiler
	constSummaryBuilder = seams.Build
	applySummaryAsker = seams.Apply
	setContainmentAsker = seams.Contain
	selfConstCompiler = seams.Compile
	t.Cleanup(func() {
		constSummaryBuilder = heldBuild
		applySummaryAsker = heldApply
		setContainmentAsker = heldContain
		selfConstCompiler = heldCompile
	})
}

// selfConstCompilerFor is the stub compile every certification test
// installs: it answers a blob naming the round and the given shape, so
// retStateOf's apply stub is what decides the ret.
func selfConstCompilerFor(shape LoweredSummary) func(ctx *FlowContext, declaration *ast.Node, selfBlob kernelbridge.SummaryBlob) (kernelbridge.SummaryBlob, LoweredSummary, bool) {
	return func(ctx *FlowContext, declaration *ast.Node, selfBlob kernelbridge.SummaryBlob) (kernelbridge.SummaryBlob, LoweredSummary, bool) {
		return "compiled-with-" + selfBlob, shape, true
	}
}

// scalarState is a plain scalar knowledge state over the listed values.
func scalarState(values ...float64) kernelbridge.KnownStateWire {
	return kernelbridge.KnownStateWire{
		Set: refinementsets.MakeRefinedSet(refinementsets.OneOf(values)),
	}
}

// exitsWithRet builds the out-vector an apply answers: every slot
// absent, the ret slot carrying the given state.
func exitsWithRet(shape LoweredSummary, ret kernelbridge.KnownStateWire) []kernelbridge.KnownStateWire {
	exits := make([]kernelbridge.KnownStateWire, shape.SlotCount)
	for i := range exits {
		exits[i] = absentState
	}
	if shape.RetIndex < len(exits) {
		exits[shape.RetIndex] = ret
	}
	return exits
}

func TestTopEntriesFor_EveryEntryIsTopExceptTheDoneFlag(t *testing.T) {
	shape := LoweredSummary{ParamCount: 1, SlotCount: 4, DoneIndex: 2, RetIndex: 3}
	entries := topEntriesFor(shape)
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want one per slot (4)", len(entries))
	}
	for i, entry := range entries {
		if i == shape.DoneIndex {
			if entry.Top {
				t.Errorf("the done flag entered top; it must enter {0} like a real call's entry")
			}
			continue
		}
		if !entry.Top {
			t.Errorf("slot %d entered %+v, want top — top admits every concrete entry", i, entry)
		}
	}
}

func TestRetStateOf_AToprRetIsNoProposal(t *testing.T) {
	shape := LoweredSummary{ParamCount: 0, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	withFixpointSeams(t, fixpointSeams{
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			return exitsWithRet(shape, kernelbridge.KnownStateWire{Top: true}), true
		},
	})
	if _, ok := retStateOf("floor", shape); ok {
		t.Errorf("a top ret proposed a constant; top is exactly what the havoc floor already says")
	}
}

func TestRetStateOf_ARefusedApplyProposesNothing(t *testing.T) {
	shape := LoweredSummary{ParamCount: 0, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	withFixpointSeams(t, fixpointSeams{
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			return nil, false
		},
	})
	if _, ok := retStateOf("floor", shape); ok {
		t.Errorf("a refused apply still proposed a constant")
	}
}

func TestJoinStates_UnionsTheSetsAndRaisesEitherSidesFlags(t *testing.T) {
	a := scalarState(1)
	b := scalarState(2)
	b.Undef = true
	b.Null = true
	joined := joinStates(a, b)
	if joined.Top {
		t.Fatalf("the join went to top")
	}
	if !joined.Undef || !joined.Null {
		t.Errorf("absent did not ride along; either side raising it raises the join")
	}
	if joined.Nan {
		t.Errorf("NaN rode along although neither side raised it")
	}
	if len(joined.Set.Forms) != 1 || joined.Set.Forms[0].Form != refinementsets.FormUnion {
		t.Errorf("the joined set is not a union: %+v", joined.Set)
	}
}

func TestJoinStates_ATopSideMakesTheJoinTop(t *testing.T) {
	if joined := joinStates(scalarState(1), kernelbridge.KnownStateWire{Top: true}); !joined.Top {
		t.Errorf("joining with top answered %+v, want top", joined)
	}
}

func TestSameProposedState_ComparesTheSpelledWireAndTheFlags(t *testing.T) {
	a := scalarState(1, 2)
	b := scalarState(1, 2)
	if !sameProposedState(a, b) {
		t.Errorf("two identically spelled states read as different proposals")
	}
	b.Nan = true
	if sameProposedState(a, b) {
		t.Errorf("a raised NaN flag did not change the proposal")
	}
	if sameProposedState(scalarState(1), scalarState(2)) {
		t.Errorf("two different sets read as the same proposal")
	}
}

func TestCertifyConstant_ASubsetRetWithCoveredFlagsCertifies(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	shape := LoweredSummary{ParamCount: 1, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	seedSummaryShape(t, declaration, shape)
	asked := 0
	withFixpointSeams(t, fixpointSeams{
		Build: func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
			if arity != shape.SlotCount {
				t.Errorf("const summary arity = %d, want the declaration's SlotCount %d", arity, shape.SlotCount)
			}
			if retIndex != shape.RetIndex {
				t.Errorf("const summary ret index = %d, want the declaration's RetIndex %d", retIndex, shape.RetIndex)
			}
			return "const-blob", true
		},
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			return exitsWithRet(shape, scalarState(1)), true
		},
		Contain: func(a, b refinementsets.RefinedSet) (bool, bool) {
			asked++
			return true, true
		},
		Compile: selfConstCompilerFor(shape),
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if !certifyConstant(ctx, declaration, scalarState(1, 2)) {
		t.Fatalf("a ret the decider confirms inside R did not certify")
	}
	if asked != 1 {
		t.Errorf("containment asks = %d, want exactly 1", asked)
	}
}

func TestCertifyConstant_AnUndecidedContainmentIsAFailureNotAPass(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	shape := LoweredSummary{ParamCount: 1, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	seedSummaryShape(t, declaration, shape)
	withFixpointSeams(t, fixpointSeams{
		Build: func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
			return "const-blob", true
		},
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			return exitsWithRet(shape, scalarState(1)), true
		},
		Contain: func(a, b refinementsets.RefinedSet) (bool, bool) {
			// the shape no reachable decider judges
			return false, false
		},
		Compile: selfConstCompilerFor(shape),
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if certifyConstant(ctx, declaration, scalarState(1, 2)) {
		t.Errorf("an undecided containment certified — only a proved subset may")
	}
}

func TestCertifyConstant_AnUncoveredAbsentFlagFailsBeforeTheSubsetAsk(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	shape := LoweredSummary{ParamCount: 1, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	seedSummaryShape(t, declaration, shape)
	asked := 0
	answered := scalarState(1)
	answered.Undef = true
	answered.Null = true
	withFixpointSeams(t, fixpointSeams{
		Build: func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
			return "const-blob", true
		},
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			return exitsWithRet(shape, answered), true
		},
		Contain: func(a, b refinementsets.RefinedSet) (bool, bool) {
			asked++
			return true, true
		},
		Compile: selfConstCompilerFor(shape),
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	// R does NOT admit absence, and the answer may be absent
	if certifyConstant(ctx, declaration, scalarState(1, 2)) {
		t.Errorf("a ret that may be absent certified against an R that does not admit absence")
	}
	if asked != 0 {
		t.Errorf("the subset was asked %d times although the flags already failed", asked)
	}
}

func TestCertifyConstant_AnUncoveredNanFlagFails(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	shape := LoweredSummary{ParamCount: 1, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	seedSummaryShape(t, declaration, shape)
	answered := scalarState(1)
	answered.Nan = true
	withFixpointSeams(t, fixpointSeams{
		Build: func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
			return "const-blob", true
		},
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			return exitsWithRet(shape, answered), true
		},
		Contain: func(a, b refinementsets.RefinedSet) (bool, bool) { return true, true },
		Compile: selfConstCompilerFor(shape),
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if certifyConstant(ctx, declaration, scalarState(1, 2)) {
		t.Errorf("a ret that may be NaN certified against an R that does not admit NaN")
	}
}

func TestCertifyConstant_ATopCandidateIsNeverCertified(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	withFixpointSeams(t, fixpointSeams{
		Build: func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
			t.Errorf("a top candidate reached the const-summary build")
			return "", false
		},
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if certifyConstant(ctx, declaration, kernelbridge.KnownStateWire{Top: true}) {
		t.Errorf("a top candidate certified — top is the floor, not a constant")
	}
}

func TestProposeConstant_AStableRoundStopsTheIteration(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return f(n - 1); }")
	shape := LoweredSummary{ParamCount: 1, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	seedSummaryShape(t, declaration, shape)
	rounds := 0
	// every round answers the SAME ret the floor did, so the join adds
	// nothing new and the candidate stops moving on the first round
	withFixpointSeams(t, fixpointSeams{
		Build: func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
			return "const-blob", true
		},
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			if blob != "floor" {
				rounds++
			}
			return exitsWithRet(shape, scalarState(1)), true
		},
		Compile: selfConstCompilerFor(shape),
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	candidate, ok := proposeConstant(ctx, declaration, "floor", shape)
	if !ok {
		t.Fatalf("the iteration proposed nothing")
	}
	if candidate.Top {
		t.Fatalf("the proposal is top")
	}
	if rounds != 1 {
		t.Errorf("rounds = %d, want 1 — a candidate that stopped moving ends the iteration", rounds)
	}
}

func TestProposeConstant_ARoundGoingToTopProposesNothing(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return f(n - 1); }")
	shape := LoweredSummary{ParamCount: 1, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	seedSummaryShape(t, declaration, shape)
	withFixpointSeams(t, fixpointSeams{
		Build: func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
			return "const-blob", true
		},
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			if blob == "floor" {
				return exitsWithRet(shape, scalarState(1)), true
			}
			return exitsWithRet(shape, kernelbridge.KnownStateWire{Top: true}), true
		},
		Compile: selfConstCompilerFor(shape),
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	// a round whose compile answers top makes compileWithSelfConst
	// decline, which breaks the loop and leaves R0 standing — still a
	// proposal, and certification is what judges it
	candidate, ok := proposeConstant(ctx, declaration, "floor", shape)
	if !ok || candidate.Top {
		t.Errorf("proposal = (%+v, %v), want the floor's own R0 standing", candidate, ok)
	}
}

func TestProposeConstant_ARefusedFloorApplyProposesNothing(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return f(n - 1); }")
	shape := LoweredSummary{ParamCount: 1, SlotCount: 3, DoneIndex: 1, RetIndex: 2}
	seedSummaryShape(t, declaration, shape)
	withFixpointSeams(t, fixpointSeams{
		Apply: func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool) {
			return nil, false
		},
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := proposeConstant(ctx, declaration, "floor", shape); ok {
		t.Errorf("R0 could not be read, yet a constant was proposed")
	}
}

func TestConstSummaryBlobFor_UsesTheDeclarationsOwnSlotCountAndRetIndex(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return n; }")
	shape := LoweredSummary{ParamCount: 1, SlotCount: 6, DoneIndex: 4, RetIndex: 5}
	seedSummaryShape(t, declaration, shape)
	var sawArity, sawRet int
	withFixpointSeams(t, fixpointSeams{
		Build: func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
			sawArity, sawRet = arity, retIndex
			return "const-blob", true
		},
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	blob, ok := constSummaryBlobFor(ctx, declaration, scalarState(7))
	if !ok || blob != "const-blob" {
		t.Fatalf("constSummaryBlobFor = (%q, %v), want (%q, true)", blob, ok, "const-blob")
	}
	if sawArity != 6 {
		t.Errorf("arity = %d, want the SlotCount 6 — the splice's entry numbering must match", sawArity)
	}
	if sawRet != 5 {
		t.Errorf("ret index = %d, want the declaration's RetIndex 5", sawRet)
	}
}

func TestAskConstSummary_ATopStateBuildsNothing(t *testing.T) {
	if _, ok := askConstSummary(3, 2, kernelbridge.KnownStateWire{Top: true}); ok {
		t.Errorf("a top state built a constant summary — there is no constant claim in top")
	}
}

func TestHullStatesForm_MatchesTheSpelledShapeAndDivisor(t *testing.T) {
	hull := kernelbridge.BoundsResult{
		Hull: refinementsets.MakeRefinedSet(
			refinementsets.AtLeast(0), refinementsets.AtMost(9),
			refinementsets.Integer, refinementsets.MultipleOf(3),
		),
	}
	if !hullStatesForm(hull, refinementsets.Integer) {
		t.Errorf("the hull's own integrality mark did not match itself")
	}
	if !hullStatesForm(hull, refinementsets.MultipleOf(3)) {
		t.Errorf("the hull's own divisor did not match itself")
	}
	if hullStatesForm(hull, refinementsets.MultipleOf(5)) {
		t.Errorf("a different divisor matched — a moved divisor must drop")
	}
}

func TestEnclosureOf_ReadsBothEdgesAndRefusesAnUnboundedHull(t *testing.T) {
	bounded := kernelbridge.BoundsResult{
		Hull: refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(7)),
	}
	low, high, ok := enclosureOf(bounded)
	if !ok || low != -2 || high != 7 {
		t.Errorf("enclosure = (%v, %v, %v), want (-2, 7, true)", low, high, ok)
	}
	halfOpen := kernelbridge.BoundsResult{
		Hull: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer),
	}
	if _, _, ok := enclosureOf(halfOpen); ok {
		t.Errorf("a hull with only one edge read as an enclosure")
	}
}

func TestSummaryCycleInFlight_TrueOnlyWhileTheDeclarationsOwnBuildRuns(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return f(n - 1); }")
	var duringOwnBuild bool
	var duringAnothersBuild bool
	other := summaryDeclarationOf(t, "function g(n: number) { return n; }")
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		if d == declaration {
			duringOwnBuild = SummaryCycleInFlight(checkerOf(ctx), declaration)
			duringAnothersBuild = SummaryCycleInFlight(checkerOf(ctx), other)
		}
		return "blob", 2, true
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	if SummaryCycleInFlight(checkerOf(ctx), declaration) {
		t.Errorf("in flight before any build started")
	}
	if _, ok := SummaryBlobFor(ctx, declaration); !ok {
		t.Fatalf("the build declined")
	}
	if !duringOwnBuild {
		t.Errorf("the declaration did not read as in flight during its own build")
	}
	if duringAnothersBuild {
		t.Errorf("an unrelated declaration read as in flight")
	}
	if SummaryCycleInFlight(checkerOf(ctx), declaration) {
		t.Errorf("still in flight after the build finished")
	}
	if SummaryCycleInFlight(checkerOf(ctx), nil) {
		t.Errorf("a nil declaration read as in flight")
	}
}

func TestSummaryBlobFor_AHeldSelfBlobAnswersOnlyInsideTheFixpointsGate(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return f(n - 1); }")
	builds := 0
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		builds++
		return "built", 2, true
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}

	// OUTSIDE the gate the override is invisible: the ordinary route runs
	releaseSelf := holdSelfBlob(checkerOf(ctx), declaration, "self-const")
	outside, outsideOk := SummaryBlobFor(ctx, declaration)
	if !outsideOk || outside != "built" {
		t.Errorf("outside the gate = (%q, %v), want the ordinary answer %q", outside, outsideOk, "built")
	}
	releaseSelf()

	// INSIDE the gate it outranks the store
	clearSummaryStore()
	releaseGate := holdRegistryForFixpoint()
	releaseSelf = holdSelfBlob(checkerOf(ctx), declaration, "self-const")
	inside, insideOk := summaryBlobForUngated(ctx, declaration)
	releaseSelf()
	releaseGate()
	if !insideOk || inside != "self-const" {
		t.Fatalf("inside the gate = (%q, %v), want (%q, true)", inside, insideOk, "self-const")
	}

	// the override stored nothing of its own
	key := summaryKey{checker: checkerOf(ctx), declaration: declaration}
	summaryBlobsMu.Lock()
	_, stored := summaryBlobs[key]
	_, heldStill := summarySelfBlobs[key]
	summaryBlobsMu.Unlock()
	if stored {
		t.Errorf("the held self blob was stored as the declaration's answer")
	}
	if heldStill {
		t.Errorf("the self blob outlived its release")
	}
}

func TestReplaceStoredBlob_SwapsTheAnswerAndKeepsTheOutShape(t *testing.T) {
	declaration := summaryDeclarationOf(t, "function f(n: number) { return f(n - 1); }")
	withSummaryBuilder(t, func(ctx *FlowContext, d *ast.Node) (kernelbridge.SummaryBlob, int, bool) {
		return "havoc-floor", 5, true
	})
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := SummaryBlobFor(ctx, declaration); !ok {
		t.Fatalf("the floor build declined")
	}

	replaceStoredBlob(checkerOf(ctx), declaration, "certified-const")

	blob, ok := SummaryBlobFor(ctx, declaration)
	if !ok || blob != "certified-const" {
		t.Errorf("blob after the upgrade = (%q, %v), want (%q, true)", blob, ok, "certified-const")
	}
	index, shapeOk := SummaryOutShapeFor(ctx, declaration)
	if !shapeOk || index != 5 {
		t.Errorf("out shape after the upgrade = (%d, %v), want (5, true) — callers read the same index", index, shapeOk)
	}
}

// seedSummaryShape puts a lowered shape into the body memo so the
// fixpoint's LowerSummaryBody ask answers it without a kernel. Keyed
// on the nil checker — every fixpoint test here builds its FlowContext
// with no P, so checkerOf(ctx) reads nil at every call site this seeds
// for. Cleared when the test ends.
func seedSummaryShape(t *testing.T, declaration *ast.Node, shape LoweredSummary) {
	t.Helper()
	key := summaryKey{checker: nil, declaration: declaration}
	kernelSummariesMu.Lock()
	held, had := kernelSummaries[key]
	kernelSummaries[key] = summaryEntry{Summary: shape, Ok: true}
	kernelSummariesMu.Unlock()
	t.Cleanup(func() {
		kernelSummariesMu.Lock()
		if had {
			kernelSummaries[key] = held
		} else {
			delete(kernelSummaries, key)
		}
		kernelSummariesMu.Unlock()
	})
}

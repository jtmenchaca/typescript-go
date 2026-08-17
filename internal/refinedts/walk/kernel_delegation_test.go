// Ports control_flow/kernel_delegation.test.ts. The engine route,
// exercised end to end through the production module: a parsed
// branch harvests, lowers, walks kernel-side, and its exit claims
// tighten the environment — plus the declines that keep it honest.
// Skipped (never a faked pass) when the native kernel dylib is
// absent, the same gate kernelbridge's own round-trip tests use.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/parser"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// kernelDelegationStatementOf mirrors the TS test's statementOf: the
// first statement of a throwaway parsed source — no checker needed,
// EngineEntryOf reads only syntax plus the caller's sortAt callback.
func kernelDelegationStatementOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	opts := ast.SourceFileParseOptions{FileName: "/s.ts", Path: "/s.ts"}
	file := parser.ParseSourceFile(opts, source, core.ScriptKindTS)
	if len(file.Statements.Nodes) == 0 {
		t.Fatalf("no statements parsed from %q", source)
	}
	return file.Statements.Nodes[0]
}

func kernelDelegationLoadKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	kernel, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	return kernel
}

func TestKernelDelegation_ABranchOverAnUnknownBindingWalksKernelSideAndTightensTheEnvironmentToTheArmsUnion(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	env := NewEnv()
	env.Set("x", silence.Residue())
	statement := kernelDelegationStatementOf(t, "if (x === 0) { x = 1; } else { x = 2; }")
	entry, ok := EngineEntryOf(env, statement, func(*ast.Node) BindingKind { return BindingKindNumber })
	if !ok {
		t.Fatalf("EngineEntryOf ok = false, want true")
	}
	EngineMeetInto(env, entry)
	after, ok := env.Get("x")
	if !ok {
		t.Fatalf("env[x] missing")
	}
	state, ok := StateOfKnown(after)
	if !ok || state.Top {
		t.Fatalf("StateOfKnown(after) = %+v, %v, want a non-top state", state, ok)
	}
	if !kernel.Member(state.Set, []float64{1}) {
		t.Errorf("member(state.Set, [1]) = false, want true")
	}
	if !kernel.Member(state.Set, []float64{2}) {
		t.Errorf("member(state.Set, [2]) = false, want true")
	}
	if kernel.Member(state.Set, []float64{0}) {
		t.Errorf("member(state.Set, [0]) = true, want false")
	}
	if state.Undef || state.Null {
		t.Errorf("state admits absence (undef=%v null=%v), want neither", state.Undef, state.Null)
	}
}

func TestKernelDelegation_ADefinednessGuardWalksTheElseArmsWriteAndTheThenArmsSurvivalBothLandInTheExit(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	narrowing.SetNarrowKernel(kernel) // the production handover route
	env := NewEnv()
	env.Set("x", abstractdomain.PossiblyUndefined(abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), "", false, false))
	statement := kernelDelegationStatementOf(t, "if (x === undefined) { x = 0; }")
	entry, ok := EngineEntryOf(env, statement, func(*ast.Node) BindingKind { return BindingKindNumber })
	if !ok {
		t.Fatalf("EngineEntryOf ok = false, want true")
	}
	// production meets AFTER the checker's own walk updated the env;
	// simulate a post-state the checker could not pin
	env.Set("x", silence.Residue())
	EngineMeetInto(env, entry)
	after, ok := env.Get("x")
	if !ok {
		t.Fatalf("binding lost")
	}
	state, ok := StateOfKnown(after)
	if !ok || state.Top {
		t.Fatalf("StateOfKnown(after) = %+v, %v, want a non-top state", state, ok)
	}
	// `x === undefined` lowers to eqUndef, whose then-arm overwrites and
	// whose else-arm removes exactly the undefined admission — the
	// kernel's own exit is {0,5} with ONLY the null admission (the
	// entry wrapper conflates, and `!== undefined` is true of null, so
	// removing null would be unsound; the old IrTestDefined lowering
	// did exactly that). The READBACK now keeps that flavor: KnownOfState
	// reads a Null-only, non-empty-set state as the NullOnly-flavored
	// wrapper (abstractdomain.PossiblyAbsent's AbsentFlavorNullOnly), so
	// only the null admission survives to this assertion — Undef is
	// false, Null is true.
	if state.Undef {
		t.Errorf("state.Undef = true, want false — the else-arm proved only the null admission survives")
	}
	if !state.Null {
		t.Errorf("state.Null = false, want true — the else-arm's null admission survives the guard")
	}
	if !kernel.Member(state.Set, []float64{0}) {
		t.Errorf("member(state.Set, [0]) = false, want true")
	}
	if !kernel.Member(state.Set, []float64{5}) {
		t.Errorf("member(state.Set, [5]) = false, want true")
	}
	if kernel.Member(state.Set, []float64{1}) {
		t.Errorf("member(state.Set, [1]) = true, want false")
	}
}

func TestKernelDelegation_ANonScalarParticipantDeclinesTheRoute(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	env := NewEnv()
	env.Set("o", abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false))
	statement := kernelDelegationStatementOf(t, "if (o) { o = null; }")
	_, ok := EngineEntryOf(env, statement, func(*ast.Node) BindingKind { return BindingKindUnknown })
	if ok {
		t.Errorf("EngineEntryOf(non-scalar) ok = true, want false")
	}
}

func TestKernelDelegation_AnObjectsScalarFieldWalksAsABindingAndTheMeetLandsBackInsideTheObject(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	env := NewEnv()
	env.Set("o", abstractdomain.KnownObject([]abstractdomain.ObjectKey{{Name: "count", Value: silence.Residue()}}, nil, true, abstractdomain.TrustProved, false))
	statement := kernelDelegationStatementOf(t, "if (o.count === 0) { o.count = 1; } else { o.count = 2; }")
	entry, ok := EngineEntryOf(env, statement, func(*ast.Node) BindingKind { return BindingKindNumber })
	if !ok {
		t.Fatalf("EngineEntryOf ok = false, want true")
	}
	EngineMeetInto(env, entry)
	o, ok := env.Get("o")
	if !ok || o.Kind != abstractdomain.KindObject {
		t.Fatalf("env[o].Kind = %v, %v, want KindObject", o.Kind, ok)
	}
	var count abstractdomain.AbstractValue
	found := false
	for _, key := range o.Keys {
		if key.Name == "count" {
			count, found = key.Value, true
		}
	}
	if !found {
		t.Fatalf("o.Keys has no count")
	}
	state, ok := StateOfKnown(count)
	if !ok || state.Top {
		t.Fatalf("StateOfKnown(o.count) = %+v, %v, want a non-top state", state, ok)
	}
	if !kernel.Member(state.Set, []float64{1}) {
		t.Errorf("member(state.Set, [1]) = false, want true")
	}
	if !kernel.Member(state.Set, []float64{2}) {
		t.Errorf("member(state.Set, [2]) = false, want true")
	}
	if kernel.Member(state.Set, []float64{0}) {
		t.Errorf("member(state.Set, [0]) = true, want false")
	}
}

// TestStateOfKnownAndKnownOfState_KindNullRoundTripsExactlyNull pins the
// KindNull split: every producer of KindNull already knows the flavor
// — so StateOfKnown sends Null alone, and KnownOfState reads a
// Null-only, otherwise-empty state straight back to abstractdomain.Null
// rather than the conflated PossiblyUndefined wrapper. No kernel
// needed — both functions are plain Go over the wire struct.
func TestStateOfKnownAndKnownOfState_KindNullRoundTripsExactlyNull(t *testing.T) {
	state, ok := StateOfKnown(abstractdomain.Null)
	if !ok {
		t.Fatalf("StateOfKnown(Null) ok = false, want true")
	}
	if state.Top {
		t.Fatalf("StateOfKnown(Null) = %+v, want a non-top state", state)
	}
	if !state.Null {
		t.Errorf("StateOfKnown(Null).Null = false, want true")
	}
	if state.Undef {
		t.Errorf("StateOfKnown(Null).Undef = true, want false — Null is a deliberate exact-null site, not the conflated marker")
	}
	if state.Nan {
		t.Errorf("StateOfKnown(Null).Nan = true, want false")
	}
	back := KnownOfState(state)
	if back.Kind != abstractdomain.KindNull {
		t.Errorf("KnownOfState(StateOfKnown(Null)).Kind = %v, want KindNull", back.Kind)
	}
}

// TestStateOfKnownAndKnownOfState_KindUndefRoundTripsExactlyUndef pins
// the KindUndef producer audit's conclusion: every EXACT KindUndef site
// means exactly the undefined value (missing keys, out-of-bounds reads,
// void, uninitialized, the undefined literal) — none of them mean null
// — so StateOfKnown now sends Undef alone (the mirror of KindNull's own
// arm above), and KnownOfState reads an Undef-only, otherwise-empty
// state straight back to abstractdomain.Undef through the new fast
// path, not the conflated PossiblyUndefined wrapper.
func TestStateOfKnownAndKnownOfState_KindUndefRoundTripsExactlyUndef(t *testing.T) {
	state, ok := StateOfKnown(abstractdomain.Undef)
	if !ok {
		t.Fatalf("StateOfKnown(Undef) ok = false, want true")
	}
	if state.Top {
		t.Fatalf("StateOfKnown(Undef) = %+v, want a non-top state", state)
	}
	if !state.Undef {
		t.Errorf("StateOfKnown(Undef).Undef = false, want true")
	}
	if state.Null {
		t.Errorf("StateOfKnown(Undef).Null = true, want false — the producer audit closed: exact KindUndef never means null")
	}
	if state.Nan {
		t.Errorf("StateOfKnown(Undef).Nan = true, want false")
	}
	back := KnownOfState(state)
	if back.Kind != abstractdomain.KindUndef {
		t.Errorf("KnownOfState(StateOfKnown(Undef)).Kind = %v, want KindUndef", back.Kind)
	}
}

// TestKnownOfState_UndefAndNullTogetherStillReadsAsTheConflatedWrapper
// pins the OTHER side of the same gate: a state admitting BOTH flags —
// what only a CONFLATED-flavor maybe wrapper's own send still produces,
// exact KindUndef no longer being one of its sources — must keep
// reading back through the existing PossiblyUndefined wrapper, not
// either fast path; both fast paths check one flag WITHOUT the other.
func TestKnownOfState_UndefAndNullTogetherStillReadsAsTheConflatedWrapper(t *testing.T) {
	conflated := abstractdomain.PossiblyUndefined(
		abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		"", false, false,
	)
	state, ok := StateOfKnown(conflated)
	if !ok {
		t.Fatalf("StateOfKnown(conflated) ok = false, want true")
	}
	if !state.Undef || !state.Null {
		t.Fatalf("StateOfKnown(conflated) = %+v, want both Undef and Null set", state)
	}
	back := KnownOfState(state)
	if back.Kind != abstractdomain.KindPossiblyUndefined {
		t.Errorf("KnownOfState(StateOfKnown(conflated)).Kind = %v, want KindPossiblyUndefined (the conflated wrapper)", back.Kind)
	}
	if back.AbsentSide != abstractdomain.AbsentFlavorConflated {
		t.Errorf("KnownOfState(StateOfKnown(conflated)).AbsentSide = %v, want AbsentFlavorConflated", back.AbsentSide)
	}
}

// flavoredWrapperOf builds a {5}-or-absent wrapper carrying the given
// AbsentFlavor directly — the round-trip tests below need a wrapper
// with a NON-conflated flavor already stamped, which no existing
// production call site builds yet (every caller still goes through
// the flavor-blind PossiblyUndefined).
func flavoredWrapperOf(flavor abstractdomain.AbsentFlavor) abstractdomain.AbstractValue {
	return abstractdomain.PossiblyAbsent(
		abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		flavor, "", false, false,
	)
}

// TestStateOfKnownAndKnownOfState_UndefOnlyWrapperRoundTrips pins the
// send/read halves of the UndefOnly flavor together: StateOfKnown sends
// Undef alone (Null stays false), and KnownOfState reads that back as
// an UndefOnly-flavored wrapper — not the conflated one a bare Undef
// flag pair would produce.
func TestStateOfKnownAndKnownOfState_UndefOnlyWrapperRoundTrips(t *testing.T) {
	wrapper := flavoredWrapperOf(abstractdomain.AbsentFlavorUndefOnly)
	state, ok := StateOfKnown(wrapper)
	if !ok {
		t.Fatalf("StateOfKnown(UndefOnly wrapper) ok = false, want true")
	}
	if !state.Undef {
		t.Errorf("state.Undef = false, want true")
	}
	if state.Null {
		t.Errorf("state.Null = true, want false — UndefOnly sends Undef alone")
	}
	back := KnownOfState(state)
	if back.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("KnownOfState(state).Kind = %v, want KindPossiblyUndefined", back.Kind)
	}
	if back.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("KnownOfState(state).AbsentSide = %v, want AbsentFlavorUndefOnly", back.AbsentSide)
	}
}

// TestStateOfKnownAndKnownOfState_NullOnlyWrapperOverANonEmptySetRoundTrips
// pins the NullOnly twin: sends Null alone, and — because the inner set
// is NOT empty (unlike the exact abstractdomain.Null fast path) —
// KnownOfState must read it back as the NullOnly-flavored wrapper, not
// the bare Null value.
func TestStateOfKnownAndKnownOfState_NullOnlyWrapperOverANonEmptySetRoundTrips(t *testing.T) {
	wrapper := flavoredWrapperOf(abstractdomain.AbsentFlavorNullOnly)
	state, ok := StateOfKnown(wrapper)
	if !ok {
		t.Fatalf("StateOfKnown(NullOnly wrapper) ok = false, want true")
	}
	if state.Undef {
		t.Errorf("state.Undef = true, want false — NullOnly sends Null alone")
	}
	if !state.Null {
		t.Errorf("state.Null = false, want true")
	}
	back := KnownOfState(state)
	if back.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("KnownOfState(state).Kind = %v, want KindPossiblyUndefined (the set is non-empty, not the bare Null fast path)", back.Kind)
	}
	if back.AbsentSide != abstractdomain.AbsentFlavorNullOnly {
		t.Errorf("KnownOfState(state).AbsentSide = %v, want AbsentFlavorNullOnly", back.AbsentSide)
	}
}

// TestJoinKnown_UndefOnlyAndNullOnlyWrappersReadConflated pins the join
// lattice's cross-flavor rule end to end: an UndefOnly wrapper joined
// with a NullOnly wrapper over the same inner value admits EITHER
// admission now, so the joined wrapper's AbsentSide must read back
// conflated (the zero value) — never one flavor overwriting the other.
func TestJoinKnown_UndefOnlyAndNullOnlyWrappersReadConflated(t *testing.T) {
	undefOnly := flavoredWrapperOf(abstractdomain.AbsentFlavorUndefOnly)
	nullOnly := flavoredWrapperOf(abstractdomain.AbsentFlavorNullOnly)
	joined := abstractdomain.JoinKnown(undefOnly, nullOnly)
	if joined.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("JoinKnown(UndefOnly, NullOnly).Kind = %v, want KindPossiblyUndefined", joined.Kind)
	}
	if joined.AbsentSide != abstractdomain.AbsentFlavorConflated {
		t.Errorf("JoinKnown(UndefOnly, NullOnly).AbsentSide = %v, want AbsentFlavorConflated", joined.AbsentSide)
	}
}

// TestJoinKnown_SameFlavorStaysThatFlavor pins the other half of the
// join lattice: two UndefOnly wrappers over the same inner value join
// to an UndefOnly wrapper, not a conflated one — SameKnown's own
// AbsentSide check is what makes this arm reachable at all (a same-
// flavor pair short-circuits through JoinKnown's SameKnown(a, b) fast
// path before the flavor-joining arm below it ever runs).
func TestJoinKnown_SameFlavorStaysThatFlavor(t *testing.T) {
	a := flavoredWrapperOf(abstractdomain.AbsentFlavorUndefOnly)
	b := flavoredWrapperOf(abstractdomain.AbsentFlavorUndefOnly)
	joined := abstractdomain.JoinKnown(a, b)
	if joined.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("JoinKnown(UndefOnly, UndefOnly).Kind = %v, want KindPossiblyUndefined", joined.Kind)
	}
	if joined.AbsentSide != abstractdomain.AbsentFlavorUndefOnly {
		t.Errorf("JoinKnown(UndefOnly, UndefOnly).AbsentSide = %v, want AbsentFlavorUndefOnly", joined.AbsentSide)
	}
}

// TestSameKnown_DistinguishesFlavors pins SameKnown's own AbsentSide
// gate directly: two wrappers alike but for flavor are NOT the same
// knowledge — a NullOnly wrapper admits a different runtime set of
// values (5 or null) than an UndefOnly one (5 or undefined) does.
func TestSameKnown_DistinguishesFlavors(t *testing.T) {
	undefOnly := flavoredWrapperOf(abstractdomain.AbsentFlavorUndefOnly)
	nullOnly := flavoredWrapperOf(abstractdomain.AbsentFlavorNullOnly)
	if abstractdomain.SameKnown(undefOnly, nullOnly) {
		t.Errorf("SameKnown(UndefOnly, NullOnly) = true, want false")
	}
	conflated := abstractdomain.PossiblyUndefined(
		abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
		"", false, false,
	)
	if abstractdomain.SameKnown(undefOnly, conflated) {
		t.Errorf("SameKnown(UndefOnly, conflated) = true, want false")
	}
	if !abstractdomain.SameKnown(undefOnly, flavoredWrapperOf(abstractdomain.AbsentFlavorUndefOnly)) {
		t.Errorf("SameKnown(UndefOnly, UndefOnly) = false, want true")
	}
}

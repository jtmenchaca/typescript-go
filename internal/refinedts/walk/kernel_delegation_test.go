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
	env := Env{"x": silence.Residue()}
	statement := kernelDelegationStatementOf(t, "if (x === 0) { x = 1; } else { x = 2; }")
	entry, ok := EngineEntryOf(env, statement, func(*ast.Node) BindingKind { return BindingKindNumber })
	if !ok {
		t.Fatalf("EngineEntryOf ok = false, want true")
	}
	EngineMeetInto(env, entry)
	after, ok := env["x"]
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
	if state.Absent {
		t.Errorf("state.Absent = true, want false")
	}
}

func TestKernelDelegation_ADefinednessGuardWalksTheElseArmsWriteAndTheThenArmsSurvivalBothLandInTheExit(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	narrowing.SetNarrowKernel(kernel) // the production handover route
	env := Env{
		"x": abstractdomain.PossiblyUndefined(abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), "", false, false),
	}
	statement := kernelDelegationStatementOf(t, "if (x === undefined) { x = 0; }")
	entry, ok := EngineEntryOf(env, statement, func(*ast.Node) BindingKind { return BindingKindNumber })
	if !ok {
		t.Fatalf("EngineEntryOf ok = false, want true")
	}
	// production meets AFTER the checker's own walk updated the env;
	// simulate a post-state the checker could not pin
	env["x"] = silence.Residue()
	EngineMeetInto(env, entry)
	after, ok := env["x"]
	if !ok {
		t.Fatalf("binding lost")
	}
	state, ok := StateOfKnown(after)
	if !ok || state.Top {
		t.Fatalf("StateOfKnown(after) = %+v, %v, want a non-top state", state, ok)
	}
	// either the guard filled it or it was already present: 0 or 5,
	// never absent
	if state.Absent {
		t.Errorf("state.Absent = true, want false")
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
	env := Env{
		"o": abstractdomain.KnownObject(nil, nil, true, abstractdomain.TrustProved, false),
	}
	statement := kernelDelegationStatementOf(t, "if (o) { o = null; }")
	_, ok := EngineEntryOf(env, statement, func(*ast.Node) BindingKind { return BindingKindUnknown })
	if ok {
		t.Errorf("EngineEntryOf(non-scalar) ok = true, want false")
	}
}

func TestKernelDelegation_AnObjectsScalarFieldWalksAsABindingAndTheMeetLandsBackInsideTheObject(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	env := Env{
		"o": abstractdomain.KnownObject([]abstractdomain.ObjectKey{{Name: "count", Value: silence.Residue()}}, nil, true, abstractdomain.TrustProved, false),
	}
	statement := kernelDelegationStatementOf(t, "if (o.count === 0) { o.count = 1; } else { o.count = 2; }")
	entry, ok := EngineEntryOf(env, statement, func(*ast.Node) BindingKind { return BindingKindNumber })
	if !ok {
		t.Fatalf("EngineEntryOf ok = false, want true")
	}
	EngineMeetInto(env, entry)
	o, ok := env["o"]
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

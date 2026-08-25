// The A2.guard.ne accept-arm shape: a membership pin conjoined with
// an inequality subtraction — `[0.5, 1.5].includes(x) && x !== 1.5`
// pins x to exactly {0.5}, and a [0, 1]-declared return must admit
// it. Reproduces the corpus row (A2.guard.ne.ts, neSubtractionInside)
// in-process so the narrowing mechanism iterates under `go test`
// without a binary build or a fixture run.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// scatterPinUnitWindow is Unit's own window — z.number().min(0).max(1)
// read as a plain refinement set, the same stand-in convention
// compoundAssignAgeWindow keeps for Age.
func scatterPinUnitWindow() *annotations.DeclaredRefinement {
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	return &annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}

func TestScatterPinAndInequalityIntersectToTheSurvivor(t *testing.T) {
	kernel := compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(x: number): number {\n"+
		"  if ([0.5, 1.5].includes(x) && x !== 1.5) {\n"+
		"    return x;\n"+
		"  }\n"+
		"  return 0;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, kernel, &diagnostics, nil)
	env := NewEnv()
	// x is a tracked plain-number parameter — seeded the way the entry
	// env wears a bare `x: number` (the unknown residue is enough for
	// the narrowing harvest's isTracked gate).
	env.Set("x", silence.Residue())
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, scatterPinUnitWindow())
	for _, d := range diagnostics {
		t.Errorf("the pinned survivor {0.5} raised a diagnostic, want silence: %s", d.MessageText)
	}
}

// The same condition's HARVEST: the conjunction must produce BOTH
// narrowings for x — the structural includes pin AND the kernel's
// conjunction answer (which carries the `!== 1.5` subtraction). The
// harvest once dropped the kernel's answer behind the structural pin
// (condition_analysis.go's old `pinned` gate), which kept the refuted
// member alive.
func TestScatterPinHarvestShapes(t *testing.T) {
	compoundAssignLoadKernel(t)
	p := entryEnvTestProgram(t, "function f(x: number): number {\n"+
		"  if ([0.5, 1.5].includes(x) && x !== 1.5) {\n"+
		"    return x;\n"+
		"  }\n"+
		"  return 0;\n"+
		"}\n")
	statements := compoundAssignFunctionStatements(t, p, "f")
	ifStmt := statements[0].AsIfStatement()
	branches := narrowing.Narrowings(p.Checker, ifStmt.Expression, func(name string) bool {
		return name == "x"
	}, nil, narrowing.GuardReadNowhere)
	if len(branches.WhenTrue) < 2 {
		for i, n := range branches.WhenTrue {
			formatted := refinementsets.FormatForDiagnostics(refinementsets.MakeRefinedSet(n.Forms...))
			t.Logf("whenTrue[%d]: binding=%s refuting=%v forms=%s", i, n.Binding, n.Refuting, formatted)
		}
		t.Fatalf("the conjunction harvested %d narrowings for x, want both the structural pin and the kernel's conjunction answer", len(branches.WhenTrue))
	}
}

// CheckWithProgram is the editor seam: wrap a Program someone else
// already holds, run the refinement walk, and apply EditorView — a
// stale @refinedts-expect-error marker becomes its own 7005, and a
// covered fire is suppressed. The test wraps ProgramFromSource's own
// program as the "existing" one, exactly the LS shape (the caller
// holds the Program; CheckWithProgram takes its own checker lease).

package service

import (
	"context"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

const testSurfaceDir = "../../../../refined-ts-typescript/surface"

// TestCheckWithProgramStaleMarker7005: a marker covering a line
// nothing fires on surfaces as 7005 through the editor seam. No
// kernel is needed — the stale-marker path is pure EditorView.
func TestCheckWithProgramStaleMarker7005(t *testing.T) {
	source := "const clean: number = 1;\n" +
		"// @refinedts-expect-error\n" +
		"const alsoClean: number = 2;\n"
	built, err := ProgramFromSource(source, testSurfaceDir)
	if err != nil {
		t.Fatalf("ProgramFromSource: %v", err)
	}
	defer built.Done()

	result, err := CheckWithProgram(context.Background(), built.Program, "/main.ts", []string{SurfacePath})
	if err != nil {
		t.Fatalf("CheckWithProgram: %v", err)
	}
	found := false
	for _, d := range result.Refinements {
		if d.Code == 7005 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 7005 stale-marker diagnostic, got %+v", result.Refinements)
	}
	// shape is never collected on this seam (locked shape policy)
	if len(result.Shape) != 0 {
		t.Fatalf("expected no shape diagnostics from the editor seam, got %d", len(result.Shape))
	}
}

// TestCheckWithProgramSuppressesCoveredFire: the refuted.analysis.ts
// shape — fee(150) against z.number().min(0).max(100) — fires 7001,
// and a marker covering that line suppresses it. Needs the native
// kernel; skipped (never faked) when the dylib is absent.
func TestCheckWithProgramSuppressesCoveredFire(t *testing.T) {
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib not built")
	}
	fired := "import * as z from \"/surface/z.ts\";\n" +
		"const zPct = z.number().min(0).max(100);\n" +
		"type Pct = z.infer<typeof zPct>;\n" +
		"function fee(p: Pct): number {\n" +
		"  return 0;\n" +
		"}\n" +
		"fee(150);\n"
	built, err := ProgramFromSource(fired, testSurfaceDir)
	if err != nil {
		t.Fatalf("ProgramFromSource: %v", err)
	}
	defer built.Done()
	result, err := CheckWithProgram(context.Background(), built.Program, "/main.ts", []string{SurfacePath})
	if err != nil {
		t.Fatalf("CheckWithProgram: %v", err)
	}
	sawFire := false
	for _, d := range result.Refinements {
		if d.Code == 7001 {
			sawFire = true
		}
	}
	if !sawFire {
		t.Fatalf("expected a 7001 on fee(150), got %+v", result.Refinements)
	}

	covered := "import * as z from \"/surface/z.ts\";\n" +
		"const zPct = z.number().min(0).max(100);\n" +
		"type Pct = z.infer<typeof zPct>;\n" +
		"function fee(p: Pct): number {\n" +
		"  return 0;\n" +
		"}\n" +
		"// @refinedts-expect-error 7001\n" +
		"fee(150);\n"
	builtCovered, err := ProgramFromSource(covered, testSurfaceDir)
	if err != nil {
		t.Fatalf("ProgramFromSource (covered): %v", err)
	}
	defer builtCovered.Done()
	resultCovered, err := CheckWithProgram(context.Background(), builtCovered.Program, "/main.ts", []string{SurfacePath})
	if err != nil {
		t.Fatalf("CheckWithProgram (covered): %v", err)
	}
	for _, d := range resultCovered.Refinements {
		if d.Code == 7001 {
			t.Fatalf("the covered 7001 should be suppressed by EditorView, got %+v", resultCovered.Refinements)
		}
		if d.Code == 7005 {
			t.Fatalf("the marker is used — no stale 7005 should ride, got %+v", resultCovered.Refinements)
		}
	}
}

// TestSurfacePathsOfDiscovery: discovery finds the virtual surface at
// its exact FileName spelling. The entry must IMPORT the surface — a
// file the program never reaches is not a program source file, and
// discovery reads the program, not the filesystem (the plugin's own
// behavior: a workspace that never imports the surface discovers
// nothing).
func TestSurfacePathsOfDiscovery(t *testing.T) {
	built, err := ProgramFromSource(
		"import * as z from \"./surface/z.ts\";\nvoid z;\nconst x = 1;\n",
		testSurfaceDir)
	if err != nil {
		t.Fatalf("ProgramFromSource: %v", err)
	}
	defer built.Done()
	found := SurfacePathsOf(built.Program)
	saw := false
	for _, path := range found {
		if path == SurfacePath {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("expected discovery to find %q, got %v", SurfacePath, found)
	}
}

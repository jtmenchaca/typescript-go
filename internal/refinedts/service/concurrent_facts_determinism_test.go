// The determinism claim CHECKER-SEAMS.md states — a file's facts are
// a pure function of the file, its imports, and the kernel — held
// under CONCURRENCY: several goroutines, each with a fresh checker
// over one shared program, compile the e2e support file's annotation
// facts at once, and every compiled registry entry must spell the
// same set in every worker of every round. The A1 sweep corruption
// (2026-08-24) showed compiled sets intermittently losing forms
// (Wide's integer, then its bounds) only when entries walked in
// parallel; this test pins the compile layer's answer to that.
package service

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

func TestConcurrentFactsCompileIsDeterministic(t *testing.T) {
	root, err := filepath.Abs("../../../../../tests/e2e/membership")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"A1.guard.arm", "A1.guard.band", "A1.guard.eq", "A1.guard.exit",
		"A1.guard.lt", "A1.guard.member", "A1.guard.ne", "A1.guard.sort",
		"A1.guard.truthy", "A1.seed.boundary",
	}
	paths := make([]string, len(ids))
	for i, id := range ids {
		paths[i] = filepath.Join(root, id, id+".ts")
	}
	prog := ProgramFromDiskMany(paths)

	var support *ast.SourceFile
	surfacePaths := map[string]bool{}
	for _, sourceFile := range prog.SourceFiles() {
		if strings.HasSuffix(sourceFile.FileName(), "/support/ts/refined.ts") {
			support = sourceFile
		}
		if strings.HasSuffix(sourceFile.FileName(), "/surface/z.ts") {
			surfacePaths[sourceFile.FileName()] = true
		}
	}
	if support == nil {
		t.Fatal("the support file is not in the program")
	}
	if len(surfacePaths) == 0 {
		t.Fatal("the surface file is not in the program")
	}

	// one worker's compile: fresh checker, fresh merged maps, no
	// kernel (the kernel only serves the emptiness lint, which
	// reporting=false skips) — the spellings of every registry entry
	spellings := func() map[string]string {
		c, _ := checker.NewChecker(prog, nil)
		view := &program.CheckerProgram{
			Program:      prog,
			Checker:      c,
			Entry:        support,
			SurfacePaths: surfacePaths,
		}
		merged := walk.FileFactsMerged{
			Registry:  annotations.AnnotationRegistry{},
			Objects:   annotations.ObjectRegistry{},
			Contracts: map[*ast.Symbol]*walk.FunctionContract{},
		}
		facts := walk.CompileFileFacts(view, support, merged, nil, false, map[string]string{})
		out := map[string]string{}
		for symbol, annotation := range facts.Annotations {
			if annotation == nil || annotation.Set == nil {
				out[symbol.Name] = "<nil>"
				continue
			}
			out[symbol.Name] = kernelbridge.EncodeSet(*annotation.Set)
		}
		return out
	}

	const workers = 8
	const rounds = 6
	baseline := spellings()
	if len(baseline) == 0 {
		t.Fatal("the support file compiled no registry entries at all")
	}
	names := make([]string, 0, len(baseline))
	for name := range baseline {
		names = append(names, name)
	}
	sort.Strings(names)

	for round := range rounds {
		results := make([]map[string]string, workers)
		var wg sync.WaitGroup
		for w := range workers {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				results[w] = spellings()
			}(w)
		}
		wg.Wait()
		for w, result := range results {
			for _, name := range names {
				if result[name] != baseline[name] {
					t.Errorf("round %d worker %d: %s compiled as\n  %s\nwant\n  %s",
						round, w, name, result[name], baseline[name])
				}
			}
			if len(result) != len(baseline) {
				t.Errorf("round %d worker %d: %d registry entries, want %d", round, w, len(result), len(baseline))
			}
		}
		if t.Failed() {
			break
		}
	}
}

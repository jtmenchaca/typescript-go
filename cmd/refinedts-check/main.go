// from service/check_cli.ts
//
// The command line: refinedts-check <file.ts> [...] — tsc's own shape
// diagnostics first, then the refinement judgments, each at
// file:line:col. Exit 1 when anything fired; 0 on silence.
//
// @refinedts-expect-error markers are honored through
// service.ExpectationsOf (service/expect_error.go, landed by a
// concurrent porter per the task's instruction) — a matched fire is
// SILENT and does not fail the run; an expectation nothing fired on
// is itself an error, so stale declarations stay visible.

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/microsoft/typescript-go/internal/locale"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/service"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/scanner"
)

func main() {
	// the sweep's live set is the program + facts; collecting at every
	// 2× heap growth (Go's default) spent most of the sweep's CPU in
	// the collector (pprof 2026-08-12: gcDrain 28% + madvise 27% +
	// scanObject 21% of samples). Collect at 5× instead, under a hard
	// memory ceiling so a huge project degrades to more collection,
	// never to an OOM. Measured on the 260-file recharts list: 3103 ms
	// at the default, 2642 ms here, 3108 ms with the collector off
	// (a grow-only heap pays fresh page zeroing with no warm reuse).
	// Set here, not via GOGC — no environment variables (the standing
	// rule).
	debug.SetGCPercent(400)
	debug.SetMemoryLimit(8 << 30)

	surfaceFlag := flag.String("surface", "",
		"path to refined-ts-typescript/surface/z.ts (default: derived from this binary's location)")
	kernelFlag := flag.String("kernel", "",
		"path to librefinedts_kernel.dylib (default: derived from this binary's location)")
	listFlag := flag.String("list", "",
		"file holding newline-separated .ts paths to check (joins any positional args)")
	wallFlag := flag.Bool("wall", false,
		"print total wall time and file count to stderr when done")
	traceFlag := flag.Bool("trace", false,
		"record where refinement time goes and print the attribution report to stderr")
	traceOutFlag := flag.String("trace-out", "",
		"write the -trace report to this file instead of stderr")
	cpuProfileFlag := flag.String("cpuprofile", "",
		"write a pprof CPU profile to this file (exact attribution, no tracing overhead)")
	memProfileFlag := flag.String("memprofile", "",
		"write a pprof allocation profile to this file when the sweep ends")
	flag.Parse()
	files := flag.Args()
	if *listFlag != "" {
		listed, err := os.ReadFile(*listFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		for _, line := range strings.Split(string(listed), "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				files = append(files, trimmed)
			}
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: refinedts-check [-surface z.ts] [-kernel dylib] [-list files.txt] [-wall] [-trace] [-trace-out path] <file.ts> [...]")
		os.Exit(2)
	}
	startedAt := time.Now()

	surfacePath, err := surfaceZPath(*surfaceFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *kernelFlag != "" {
		kernelbridge.SetDylibPath(*kernelFlag)
	} else if derived, ok := repoRelative(
		"refined-ts-lean/native/build/librefinedts_kernel.dylib"); ok {
		kernelbridge.SetDylibPath(derived)
	}

	if *traceFlag {
		if *traceOutFlag != "" {
			tracing.SetWriteTo(*traceOutFlag)
		}
		tracing.TraceStart(tracing.GrainStep)
	}
	// os.Exit below never runs defers — the profile stops explicitly
	// before both exit paths
	stopProfile := func() {}
	if *cpuProfileFlag != "" {
		profileFile, err := os.Create(*cpuProfileFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := pprof.StartCPUProfile(profileFile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		stopProfile = pprof.StopCPUProfile
	}

	fired := false
	// every run is batch mode — service.CheckFiles builds ONE program
	// per covering tsconfig and shares every read-once store across
	// the entries; one file is a batch of one
	results := service.CheckFiles(files, surfacePath)
	for _, file := range files {
		result, held := results[file]
		if !held {
			fmt.Fprintf(os.Stderr, "%s: the entry file did not parse\n", file)
			fired = true
			continue
		}
		for _, d := range result.Shape {
			fired = true
			position := file
			if d.File() != nil {
				line, character := scanner.GetECMALineAndUTF16CharacterOfPosition(d.File(), d.Pos())
				position = fmt.Sprintf("%s:%d:%d", file, line+1, int(character)+1)
			}
			fmt.Fprintf(os.Stderr, "%s shape TS%d: %s\n", position, d.Code(), d.Localize(locale.Default))
		}

		text, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", file, err)
			fired = true
			continue
		}
		lineStarts := lineStartsOf(string(text))
		expectations := service.ExpectationsOf(string(text))
		for _, d := range result.Refinements {
			line := lineOf(lineStarts, d.Start)
			character := d.Start - lineStarts[line-1]
			spelled := fmt.Sprintf("%s:%d:%d refinement RTS%d: %s", file, line, character+1, d.Code, d.MessageText)
			var expected *service.Expectation
			for _, e := range expectations {
				if e.Line == line && (!e.HasCode || e.Code == d.Code) {
					expected = e
					break
				}
			}
			if expected != nil {
				expected.Used = true
				continue
			}
			fired = true
			fmt.Fprintln(os.Stderr, spelled)
		}
		for _, e := range expectations {
			if e.Used {
				continue
			}
			fired = true
			codeSuffix := ""
			if e.HasCode {
				codeSuffix = fmt.Sprintf(" RTS%d", e.Code)
			}
			fmt.Fprintf(os.Stderr, "%s:%d @refinedts-expect-error: line %d was expected to fire%s, and nothing did\n",
				file, e.MarkerLine, e.Line, codeSuffix)
		}
	}
	if *wallFlag {
		fmt.Fprintf(os.Stderr, "WALL %d ms  %d files\n",
			time.Since(startedAt).Milliseconds(), len(files))
	}
	if *traceFlag {
		tracing.TraceStop()
		tracing.EmitTraceReport()
		fmt.Fprintln(os.Stderr, kernelbridge.QuestionCostReport())
	}
	stopProfile()
	if *memProfileFlag != "" {
		profileFile, err := os.Create(*memProfileFlag)
		if err == nil {
			_ = pprof.Lookup("allocs").WriteTo(profileFile, 0)
			_ = profileFile.Close()
		}
	}
	if fired {
		os.Exit(1)
	}
	os.Exit(0)
}

// lineStartsOf is the byte offset of the start of each line, 1-indexed
// access via lineOf below — the same shape ts.SourceFile's own line
// map gives check_cli.ts's ts.createSourceFile call.
func lineStartsOf(text string) []int {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineOf is the TS source's `source.getLineAndCharacterOfPosition(d.start).line + 1`
// — the 1-based line containing byte offset pos.
func lineOf(lineStarts []int, pos int) int {
	lo, hi := 0, len(lineStarts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lineStarts[mid] <= pos {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

// repoRelative resolves a path relative to packages/refinedts/, first
// against this binary's own location (os.Executable — the Go stand-in
// for import.meta.url; the built binary sits inside refined-ts-go),
// then against the working directory's ancestry. ok=false when
// neither holds the file. No environment variables — behavior is
// configured by arguments and the binary's own position, never by
// ambient process state (the standing rule).
func repoRelative(underRefinedts string) (string, bool) {
	var roots []string
	if exe, err := os.Executable(); err == nil {
		// <repo>/packages/refinedts/refined-ts-go/<binary>
		roots = append(roots, filepath.Join(filepath.Dir(exe), ".."))
		// <repo>/packages/refinedts/refined-ts-go/cmd/refinedts-check/<binary>
		roots = append(roots, filepath.Join(filepath.Dir(exe), "..", "..", ".."))
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			roots = append(roots, filepath.Join(dir, "packages", "refinedts"))
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	for _, root := range roots {
		candidate := filepath.Join(root, underRefinedts)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

// surfaceZPath is the real on-disk path to refined-ts-typescript's
// surface/z.ts — ProgramFromDisk's recognition anchor, mirroring the
// TS source's `new URL("../surface/z.ts", import.meta.url).pathname`.
// Stated by -surface, else derived from the binary's own location.
func surfaceZPath(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if derived, ok := repoRelative("refined-ts-typescript/surface/z.ts"); ok {
		return derived, nil
	}
	return "", fmt.Errorf("cannot locate refined-ts-typescript/surface/z.ts — pass -surface")
}

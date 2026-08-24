// from service/check_cli.ts
//
// The command line: refinedts-check <file.ts> [...] — tsc's own shape
// diagnostics first, then the refinement judgments, each at
// file:line:col. Exit 1 when anything was reported; 0 when nothing was.
//
// @refinedts-expect-error markers are honored through
// service.ExpectationsOf and matched with Expectation.Covers
// (service/expect_error.go) — a matched expected error is not printed
// and does not fail the run; an expectation no error landed on is
// itself an error, so stale declarations stay visible. Covers never
// matches RTS7002 (the undetermined channel), so a marker over a
// 7002-only line reports stale rather than swallowing it.

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	"github.com/microsoft/typescript-go/internal/locale"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/service"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
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
	// (A 24 GB limit was tried against the madvise share pprof measured
	// on 2026-08-17 — 38.9% of CPU samples — on the theory that the 8 GB
	// ceiling under the 400-percent growth target forced limit-driven
	// collection and scavenge thrash. The wall did not improve — 4790 ms
	// before, 5285 ms with the raised limit — so the measured-best
	// pairing below stands and the allocation itself, not the pacer, is
	// the open speed item: the checker's type instantiation over the
	// corpus's redux/reselect generics dominates alloc_space.)
	debug.SetGCPercent(400)
	debug.SetMemoryLimit(8 << 30)

	surfaceFlag := flag.String("surface", "",
		"path to refined-ts-typescript/surface/z.ts (default: derived from this binary's location)")
	kernelFlag := flag.String("kernel", "",
		"path to librefined_kernel.dylib (default: derived from this binary's location)")
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
	detailFlag := flag.Bool("detail", false,
		"record per-entry mechanism timers only (honest wall, no trace inflation) and print the decomposition")
	kernelTraceFlag := flag.Bool("kernel-trace", false,
		"stream every kernel question and answer wire to stderr LIVE — the diagnosis line for a hang is the last Q with no A")
	noFactsCacheFlag := flag.Bool("no-facts-cache", false,
		"compile every entry's reachable files itself instead of sharing a facts store across workers (a determinism diagnostic)")
	exportFactFlag := flag.String("export-fact", "",
		"write this file's fact artifact instead of checking it (the cross-language edge's producer mode)")
	outFlag := flag.String("o", "",
		"artifact output path for -export-fact (default: the project-cache entry -export-fact's target reads by)")
	producerPyFlag := flag.String("producer-py", "",
		"path to the refinedpy-check binary, for the Python foreign-edge auto-export (default: a project-root build, then PATH)")
	projectRootFlag := flag.String("project-root", "",
		"the project root outright, bypassing the .git-walk (default: walk up from each target's directory)")
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
	// -export-fact alongside positional files would silently ignore one
	// side of the command line, which is worse than refusing the line —
	// the same refusal refinedpy_check.rs's read_invocation states for
	// its own --export-fact.
	if *exportFactFlag != "" && len(files) > 0 {
		fmt.Fprintln(os.Stderr, "usage: refinedts-check -export-fact <file.ts> [-o path] [-producer-py path]")
		os.Exit(2)
	}
	if *exportFactFlag == "" && len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: refinedts-check [-surface z.ts] [-kernel dylib] [-list files.txt] [-wall] [-trace] [-trace-out path] <file.ts> [...]")
		fmt.Fprintln(os.Stderr, "       refinedts-check -export-fact <file.ts> [-o path] [-producer-py path]")
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
		"refined-lean/native/build/librefined_kernel.dylib"); ok {
		kernelbridge.SetDylibPath(derived)
	}
	if *producerPyFlag != "" {
		walk.SetPythonProducerPath(*producerPyFlag)
	}
	service.SetFactsCacheDisabled(*noFactsCacheFlag)
	if *projectRootFlag != "" {
		walk.SetProjectRootOverride(*projectRootFlag)
	}

	if *exportFactFlag != "" {
		// omissions are never fatal — a work-queue item printed to
		// stderr, never a failure of this run — so exit 0 follows both
		// branches; only a read/parse/write failure exits 2.
		_, omissions, exportErr := service.ExportFact(*exportFactFlag, surfacePath, *outFlag)
		for _, omission := range omissions {
			fmt.Fprintln(os.Stderr, omission)
		}
		if exportErr != nil {
			fmt.Fprintln(os.Stderr, exportErr)
			os.Exit(2)
		}
		os.Exit(0)
	}

	if *kernelTraceFlag {
		kernelbridge.SetKernelTraceWriter(func(line string) {
			fmt.Fprintln(os.Stderr, line)
		})
	}
	if *traceFlag {
		if *traceOutFlag != "" {
			tracing.SetWriteTo(*traceOutFlag)
		}
		tracing.TraceStart(tracing.GrainStep)
	}
	if *detailFlag {
		tracing.SetDetailOnly(true)
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

	reported := false
	// every run is batch mode — service.CheckFiles builds ONE program
	// per covering tsconfig and shares every read-once store across
	// the entries; one file is a batch of one
	results := service.CheckFiles(files, surfacePath)
	for _, file := range files {
		result, held := results[file]
		if !held {
			fmt.Fprintf(os.Stderr, "%s: the entry file did not parse\n", file)
			reported = true
			continue
		}
		for _, d := range result.Shape {
			reported = true
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
			reported = true
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
				if e.Line == line && e.Covers(d.Code) {
					expected = e
					break
				}
			}
			if expected != nil {
				expected.Used = true
				continue
			}
			reported = true
			fmt.Fprintln(os.Stderr, spelled)
			// a finding's related steps print under it, one per line,
			// each at its own file and position — the terminal's form of
			// what the editor shows as relatedInformation
			for _, step := range d.Steps {
				fmt.Fprintln(os.Stderr, "  "+spellStep(step))
			}
		}
		for _, e := range expectations {
			if e.Used {
				continue
			}
			reported = true
			codeSuffix := ""
			if e.HasCode {
				codeSuffix = fmt.Sprintf(" RTS%d", e.Code)
			}
			fmt.Fprintf(os.Stderr, "%s:%d @refinedts-expect-error: an error%s was expected on line %d, and none was reported\n",
				file, e.MarkerLine, codeSuffix, e.Line)
		}
	}
	if *wallFlag {
		fmt.Fprintf(os.Stderr, "WALL %d ms  %d files\n",
			time.Since(startedAt).Milliseconds(), len(files))
		// the critical-path decomposition: wall ≈ program + shape +
		// busiest-checker file chain. A fix's honest ceiling is its
		// share of THESE rows, never its share of process CPU.
		fmt.Fprintf(os.Stderr, "  program %.0f ms   shape %.0f ms\n",
			service.SweepPhases.ProgramMs, service.SweepPhases.ShapeMs)
		type entryWall struct {
			path string
			ms   float64
		}
		var walls []entryWall
		total := 0.0
		for path, result := range results {
			walls = append(walls, entryWall{path: path, ms: result.WallMs})
			total += result.WallMs
		}
		sort.Slice(walls, func(i, j int) bool { return walls[i].ms > walls[j].ms })
		fmt.Fprintf(os.Stderr, "  refine %.0f ms summed across %d entries; slowest:\n", total, len(walls))
		for i, w := range walls {
			if i >= 12 {
				break
			}
			fmt.Fprintf(os.Stderr, "  %8.0f ms  %s\n", w.ms, filepath.Base(w.path))
		}
	}
	if *traceFlag {
		tracing.TraceStop()
		tracing.EmitTraceReport()
		fmt.Fprintln(os.Stderr, kernelbridge.QuestionCostReport())
	}
	if *detailFlag && !*traceFlag {
		fmt.Fprintln(os.Stderr, tracing.DetailReportText())
	}
	stopProfile()
	if *memProfileFlag != "" {
		profileFile, err := os.Create(*memProfileFlag)
		if err == nil {
			_ = pprof.Lookup("allocs").WriteTo(profileFile, 0)
			_ = profileFile.Close()
		}
	}
	if reported {
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

// spellStep prints one related step at its own file and position. A
// step in the compiled program reads its line from the SourceFile the
// checker already holds; a foreign step (a place in a file the
// TypeScript program never compiled) reads it from the text the
// adapter carried alongside the path. Where neither supplies a text,
// the step still names its file and its sentence, without a position
// it cannot compute.
func spellStep(step assignability.RelatedStep) string {
	name := step.ForeignFile
	text := step.ForeignText
	if step.File != nil {
		name = step.File.FileName()
		text = step.File.Text()
	}
	if text == "" || step.Start < 0 || step.Start > len(text) {
		if name == "" {
			return step.Sentence
		}
		return fmt.Sprintf("%s: %s", name, step.Sentence)
	}
	starts := lineStartsOf(text)
	line := lineOf(starts, step.Start)
	character := step.Start - starts[line-1]
	return fmt.Sprintf("%s:%d:%d %s", name, line, character+1, step.Sentence)
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

// repoRelative resolves a path relative to packages/, first against
// this binary's own location (os.Executable — the Go stand-in for
// import.meta.url; the built binary sits inside
// packages/refinedts/refined-ts-go), then against the working
// directory's ancestry. ok=false when neither holds the file. No
// environment variables — behavior is configured by arguments and the
// binary's own position, never by ambient process state (the standing
// rule). refined-lean sits beside refinedts under packages/ (moved
// out from under refinedts/refined-ts-lean), so callers now spell
// their target from packages/ down — "refinedts/refined-ts-typescript/…"
// or "refined-lean/…" — rather than from packages/refinedts/ down.
func repoRelative(underPackages string) (string, bool) {
	var roots []string
	if exe, err := os.Executable(); err == nil {
		// <repo>/packages/refinedts/refined-ts-go/<binary>
		roots = append(roots, filepath.Join(filepath.Dir(exe), "..", ".."))
		// <repo>/packages/refinedts/refined-ts-go/cmd/refinedts-check/<binary>
		roots = append(roots, filepath.Join(filepath.Dir(exe), "..", "..", "..", ".."))
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			roots = append(roots, filepath.Join(dir, "packages"))
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	for _, root := range roots {
		candidate := filepath.Join(root, underPackages)
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
	if derived, ok := repoRelative("refinedts/refined-ts-typescript/surface/z.ts"); ok {
		return derived, nil
	}
	return "", fmt.Errorf("cannot locate refined-ts-typescript/surface/z.ts — pass -surface")
}

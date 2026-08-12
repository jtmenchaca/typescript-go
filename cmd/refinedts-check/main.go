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
	"fmt"
	"os"

	"github.com/microsoft/typescript-go/internal/locale"
	"github.com/microsoft/typescript-go/internal/refinedts/service"
	"github.com/microsoft/typescript-go/internal/scanner"
)

func main() {
	var files []string
	for _, arg := range os.Args[1:] {
		if len(arg) > 0 && arg[0] == '-' {
			continue
		}
		files = append(files, arg)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: refinedts-check <file.ts> [...]")
		os.Exit(2)
	}

	surfacePath, err := surfaceZPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	fired := false
	for _, file := range files {
		result, err := service.CheckFile(file, surfacePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", file, err)
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

// surfaceZPath is the real on-disk path to refined-ts-typescript's
// surface/z.ts — ProgramFromDisk's recognition anchor, mirroring the
// TS source's `new URL("../surface/z.ts", import.meta.url).pathname`.
// Go has no import.meta.url; the path is derived from this binary's
// known position in the repo tree (cmd/refinedts-check, two levels
// under refined-ts-go) via an environment override for portability,
// falling back to the repo-relative path used throughout this port.
func surfaceZPath() (string, error) {
	if override := os.Getenv("REFINEDTS_SURFACE_PATH"); override != "" {
		return override, nil
	}
	const relative = "../../../refined-ts-typescript/surface/z.ts"
	if _, err := os.Stat(relative); err == nil {
		return relative, nil
	}
	return "", fmt.Errorf("cannot locate refined-ts-typescript/surface/z.ts — set REFINEDTS_SURFACE_PATH")
}

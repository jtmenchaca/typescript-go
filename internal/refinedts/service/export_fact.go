// ExportFact is refinedts-check's `-export-fact` mode: instead of
// judging a file, it writes the ONE function the file's stdio harness
// calls as a fact artifact another checker's cross-language edge reads
// (docs/one-checker/reverse-pair.md, Half A — the mirror of
// refinedpy_check.rs's export_file/export_module).
//
// The envelope is schema v2 (docs/one-checker/schema-v2.md,
// walk/foreign_edge_artifact.go's doc comment): kind "fact-artifact",
// version 2, language "typescript", and "es2023+" as the runtime
// band (RULING 2026-08-21: one JS-family band claiming ECMA-level
// behavior, replacing the provisional node-specific string — every
// premise the edge discharges, JSON round-trip and Number semantics,
// is an ECMA-262 claim, not a node-specific one, so any recognized JS
// runner — node, deno, bun, npx tsx — satisfies it).
//
// Every field is computed. A file with no recognized harness, a
// harness calling an unexported or unexportable function, or a
// function whose return derives no faithful set are OMISSIONS —
// printed by the caller (cmd/refinedts-check/main.go), never a stub
// written into the artifact.
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// ExportFactArtifactKind and ExportFactArtifactVersion are the
// envelope this producer writes: schema v2's one kind shared by every
// language (walk.FactArtifactKindV2/FactArtifactVersionV2), rather
// than a per-language kind string.
const (
	ExportFactArtifactKind    = walk.FactArtifactKindV2
	ExportFactArtifactVersion = walk.FactArtifactVersionV2
)

// ExportFactLanguage is the v2 `language` field this producer states —
// it selects which pins the runtime band is checked against.
const ExportFactLanguage = "typescript"

// ExportFactRuntimeBand is the one JS-family band claiming ECMA-level
// behavior — see the file banner.
const ExportFactRuntimeBand = "es2023+"

// ExportFact writes entryFilePath's fact artifact to outPath (the
// caller's `-o`, or walk.ForeignCacheArtifactPath's default when
// outPath is ""). The harness's one called function is the whole
// export: every OTHER function the file declares states no fact here,
// mirroring the Python producer's own one-function harness contract.
//
// written is "" ONLY where no surface is recognized at all (no
// harness call — nothing to name in an envelope). Once a harness IS
// recognized, the artifact is always written — target/language/
// runtime/surface stated in full, and "functions" either carrying the
// one exported fact or left an empty object — matching the Python
// exporter's own module-level shape (an artifact naming what it saw,
// omissions listed beside it, never withheld as a file). omissions
// names the one reason the called function itself carried no fact:
// the harness calling a function this file states no checked contract
// for, or ExportFunctionFact/WritesNothingToStdout declining it.
//
// err is non-nil only for a read/parse/write failure — a program that
// would not build, a directory that cannot be created, a file that
// cannot be renamed into place. An omission is never an error.
func ExportFact(entryFilePath string, surfacePath string, outPath string) (written string, omissions []string, err error) {
	p, buildErr := ProgramFromDisk(entryFilePath, surfacePath)
	if buildErr != nil {
		return "", nil, buildErr
	}
	if p.Done != nil {
		defer p.Done()
	}
	kernel := setupKernel()

	facts := programFactsCached(p, kernel, nil)
	entryContracts := map[*ast.Symbol]*walk.FunctionContract{}
	for symbol, contract := range facts.contracts {
		if ast.GetSourceFileOfNode(contract.Declaration) != p.Entry {
			continue
		}
		entryContracts[symbol] = contract
	}

	calledName, harnessShape, argIndex, harnessOk := walk.HarnessCallOf(p.Entry)
	if !harnessOk {
		// no recognized surface at all: there is nothing to name in an
		// envelope (no "calls", no harness kind), so this is the one
		// omission that writes no artifact — matching the Python
		// exporter's own "surface is exported only when a harness shape
		// matches" rule, extended here to the whole file since the Go
		// producer states one function's fact per file rather than a
		// module's many.
		return "", []string{entryFilePath + ": no recognized stdio harness — " +
			`a bare top-level console.log(JSON.stringify(<fn>(JSON.parse(readFileSync(0, "utf8"))))), ` +
			`console.log(JSON.stringify(<fn>(JSON.parse(process.argv[<literal int>])))), ` +
			`or console.log(JSON.stringify(<fn>(JSON.parse(readFileSync(process.argv[<literal int>], "utf8"))))) is the only shape read`}, nil
	}

	// From here the surface IS recognized (a named harness call exists),
	// so the artifact is written regardless of whether the called
	// function itself exports a fact — matching the Python exporter's
	// own module-level shape (empty "functions" rather than no file at
	// all) and the consumer's own remedy: a decline sentence naming the
	// omission never again points at a command that cannot produce a
	// file.
	var calledContract *walk.FunctionContract
	for symbol, contract := range entryContracts {
		if symbol.Name == calledName {
			calledContract = contract
			break
		}
	}

	ctx := &walk.FlowContext{
		P:         p,
		Kernel:    kernel,
		Contracts: entryContracts,
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}

	var entryRows []walk.ForeignEntryRow
	var returnSet refinementsets.RefinedSet
	var provenanceLine int
	var provenanceSaid string
	stdoutPure := false
	exported := false
	var functionOmission string

	switch {
	case calledContract == nil:
		functionOmission = "the harness calls it, and this file states no checked contract for it"
	default:
		var exportOmission string
		entryRows, returnSet, exportOmission = walk.ExportFunctionFact(ctx, calledContract)
		switch {
		case exportOmission != "":
			functionOmission = exportOmission
		case !walk.WritesNothingToStdout(p.Entry, calledContract.Declaration):
			functionOmission = "its body may write to stdout, which the JSON wire channel must carry alone"
		default:
			stdoutPure = true
			provenanceLine = walk.ProvenanceLineOf(p.Entry, calledContract.Declaration)
			provenanceSaid = walk.ProvenanceSaidOf(entryRows, returnSet)
			exported = true
		}
	}

	if functionOmission != "" {
		omissions = []string{entryFilePath + ": '" + calledName + "' is not exported: " + functionOmission}
	}

	sourceBytes, readErr := os.ReadFile(entryFilePath)
	if readErr != nil {
		return "", nil, fmt.Errorf("reading %s: %w", entryFilePath, readErr)
	}
	sum := sha256.Sum256(sourceBytes)
	contentHash := "sha256:" + hex.EncodeToString(sum[:])

	rendered, marshalErr := json.MarshalIndent(
		exportFactEnvelope(filepath.Base(entryFilePath), contentHash, calledName, harnessShape, argIndex,
			exported, entryRows, returnSet, stdoutPure, provenanceLine, provenanceSaid),
		"", "  ")
	if marshalErr != nil {
		return "", nil, fmt.Errorf("rendering the artifact for %s: %w", entryFilePath, marshalErr)
	}

	target := outPath
	if target == "" {
		target = walk.ForeignCacheArtifactPath(entryFilePath)
	}
	if writeErr := atomicWriteArtifact(target, append(rendered, '\n')); writeErr != nil {
		return "", nil, writeErr
	}
	return target, omissions, nil
}

// exportFactEnvelope builds the artifact as raw JSON-serializable maps
// — schema v2's shape (walk/foreign_edge_artifact.go's doc comment),
// with `language` "typescript" and `surface.kind` one of "stdin-json"
// (unchanged), "argv-json" (harnessShape == walk.HarnessShapeArgvJSON:
// {"kind": "argv-json", "argIndex": <int>, "stdout": "json", "calls":
// <fn>} — the same JSON.parse transport as stdin-json, carried through
// process.argv[argIndex] instead of stdin, so there is no "stdin"
// field on this surface), or "file-json" (harnessShape ==
// walk.HarnessShapeFileJSON: {"kind": "file-json", "argIndex": <int>,
// "stdout": "json", "calls": <fn>} — the target reads its JSON payload
// from the FILE named at process.argv[argIndex], also with no "stdin"
// field). Every <set> is kernelbridge.EncodeSet's own wire text,
// embedded as json.RawMessage so it is never re-encoded through a
// second string builder.
//
// exported is whether the harness-called function itself carried
// enough to state a fact: false leaves "functions" an EMPTY object —
// the surface (target/language/runtime/surface) is still stated in
// full, matching the Python exporter's own module-level shape (an
// artifact naming every def it saw, with only the exportable ones
// filled in) — never a missing file the caller's decline sentence
// then has no way to produce.
func exportFactEnvelope(
	targetFile string, contentHash string, harnessCalls string,
	harnessShape walk.HarnessShape, argIndex float64,
	exported bool,
	entryRows []walk.ForeignEntryRow, returnSet refinementsets.RefinedSet, stdoutPure bool,
	provenanceLine int, provenanceSaid string,
) map[string]any {
	functions := map[string]any{}
	if exported {
		entries := make([]map[string]any, 0, len(entryRows))
		for _, row := range entryRows {
			if row.IsSequence {
				entries = append(entries, map[string]any{
					"name": row.Name,
					"sequence": map[string]any{
						"element":       json.RawMessage(kernelbridge.EncodeSet(row.Element)),
						"lengthAtLeast": row.LengthAtLeast,
					},
				})
				continue
			}
			entries = append(entries, map[string]any{
				"name": row.Name,
				"set":  json.RawMessage(kernelbridge.EncodeSet(row.Set)),
			})
		}
		functions[harnessCalls] = map[string]any{
			"entry": entries,
			"return": map[string]any{
				"set":        json.RawMessage(kernelbridge.EncodeSet(returnSet)),
				"stdoutPure": stdoutPure,
			},
			"provenance": map[string]any{
				"line": provenanceLine,
				"said": provenanceSaid,
			},
		}
	}
	return map[string]any{
		"refined": map[string]any{
			"kind":    ExportFactArtifactKind,
			"version": ExportFactArtifactVersion,
		},
		"target": map[string]any{
			"file":        targetFile,
			"contentHash": contentHash,
		},
		"language": ExportFactLanguage,
		"runtime": map[string]any{
			"band": ExportFactRuntimeBand,
		},
		"surface":   exportFactSurface(harnessShape, argIndex, harnessCalls),
		"functions": functions,
	}
}

// exportFactSurface builds the `surface` field per the recognized
// harness shape: stdin-json (unchanged v2 shape), argv-json ({"kind":
// "argv-json", "argIndex": <int>, "stdout": "json", "calls": <fn>} —
// no "stdin" field; the payload rides process.argv[argIndex] instead),
// or file-json ({"kind": "file-json", "argIndex": <int>, "stdout":
// "json", "calls": <fn>} — no "stdin" field; the target reads JSON
// from the FILE named at process.argv[argIndex]).
func exportFactSurface(harnessShape walk.HarnessShape, argIndex float64, harnessCalls string) map[string]any {
	switch harnessShape {
	case walk.HarnessShapeArgvJSON:
		return map[string]any{
			"kind":     "argv-json",
			"argIndex": int(argIndex),
			"stdout":   "json",
			"calls":    harnessCalls,
		}
	case walk.HarnessShapeFileJSON:
		return map[string]any{
			"kind":     "file-json",
			"argIndex": int(argIndex),
			"stdout":   "json",
			"calls":    harnessCalls,
		}
	default:
		return map[string]any{
			"kind":   "stdin-json",
			"stdin":  "json",
			"stdout": "json",
			"calls":  harnessCalls,
		}
	}
}

// atomicWriteArtifact writes data to path by writing a temp file in
// the SAME directory and renaming it into place — rename is atomic on
// the same volume, so a concurrent reader (the Go consumer's own
// ReadForeignArtifact) never observes a torn file
// (docs/one-checker/fact-freshness.md's write discipline).
func atomicWriteArtifact(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temp file in %s: %w", dir, err)
	}
	tempPath := temp.Name()
	if _, writeErr := temp.Write(data); writeErr != nil {
		temp.Close()
		os.Remove(tempPath)
		return fmt.Errorf("writing %s: %w", tempPath, writeErr)
	}
	if closeErr := temp.Close(); closeErr != nil {
		os.Remove(tempPath)
		return fmt.Errorf("closing %s: %w", tempPath, closeErr)
	}
	if renameErr := os.Rename(tempPath, path); renameErr != nil {
		os.Remove(tempPath)
		return fmt.Errorf("renaming %s into place at %s: %w", tempPath, path, renameErr)
	}
	return nil
}

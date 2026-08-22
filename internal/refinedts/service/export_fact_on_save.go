// ExportFactOnSave is export_fact.go's producer, entered from a LIVE
// program instead of a fresh disk parse — the save-time freshness half
// of docs/one-checker/fact-freshness.md (TypeScript side, mirroring
// the Python did_save hook at refinedpy.rs:69-127).
//
// The LSP session already holds a *compiler.Program with the saved
// file bound; ProgramFromExisting wraps it exactly as
// CheckWithProgram does, so this never spawns a process and never
// re-parses from disk — the same in-process principle the Python
// producer's own export_module call rides.
//
// Two cheap gates run BEFORE the export pays for a checker lease and a
// contract walk:
//
//  1. HarnessCallOf — a pure AST scan requiring no type information: a
//     file with no recognized stdio harness can never export, so this
//     is checked against the entry's already-parsed syntax tree first.
//  2. The content-hash short-circuit — sha256 of the bytes actually on
//     disk, compared against the cached artifact's own target.contentHash.
//     A save that did not change the file's bytes (whitespace-only
//     editor churn, a re-save) writes nothing.
//
// Disk bytes, not the editor overlay: the Go consumer's own
// ReadForeignArtifact hashes the file AS IT SITS ON DISK
// (foreign_edge_artifact.go's checkTargetIntegrity), so the producer
// must hash the same bytes or its own contentHash could never match
// what a save actually persisted.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// ExportFactOnSave exports entryPath's fact artifact from the live
// program prog already holds, or answers ("", false) where the cheap
// gate declines or the content hash already matches the cache — never
// an error for either of those, since both are ordinary "nothing to
// do" outcomes, not failures.
//
// written is the path actually written; ok is true exactly when a
// fresh artifact was written. err is non-nil only for a genuine
// read/build/write failure — never for the harness gate or the
// hash-match short-circuit.
func ExportFactOnSave(ctx context.Context, prog *compiler.Program, entryPath string, surfacePaths []string) (written string, ok bool, err error) {
	entry := prog.GetSourceFile(entryPath)
	if entry == nil {
		return "", false, nil
	}
	sourceFile := entry.AsSourceFile()

	// gate 1: the cheap AST-only scan. HarnessCallOf reads no type,
	// asks the kernel nothing, and pays for no contract walk — a file
	// with no recognized harness declines here before anything heavier
	// runs.
	calledName, harnessShape, argIndex, harnessOk := walk.HarnessCallOf(sourceFile)
	if !harnessOk {
		return "", false, nil
	}

	sourceBytes, readErr := os.ReadFile(entryPath)
	if readErr != nil {
		return "", false, fmt.Errorf("reading %s: %w", entryPath, readErr)
	}
	sum := sha256.Sum256(sourceBytes)
	contentHash := "sha256:" + hex.EncodeToString(sum[:])

	target := walk.ForeignCacheArtifactPath(entryPath)

	// gate 2: the content-hash short-circuit — a save that left the
	// bytes unchanged already has a matching cached artifact, so
	// nothing is exported (and nothing is re-hashed downstream).
	if cachedArtifactContentHashMatches(target, contentHash) {
		return "", false, nil
	}

	p, buildErr := ProgramFromExisting(ctx, prog, entryPath, surfacePaths)
	if buildErr != nil {
		return "", false, buildErr
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

	var calledContract *walk.FunctionContract
	for symbol, contract := range entryContracts {
		if symbol.Name == calledName {
			calledContract = contract
			break
		}
	}
	if calledContract == nil {
		return "", false, nil
	}

	walkCtx := &walk.FlowContext{
		P:         p,
		Kernel:    kernel,
		Contracts: entryContracts,
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
	}
	entryRows, returnCases, omission := walk.ExportFunctionFact(walkCtx, calledContract)
	if omission != "" {
		return "", false, nil
	}
	if !walk.WritesNothingToStdout(p.Entry, calledContract.Declaration) {
		return "", false, nil
	}

	provenanceLine := walk.ProvenanceLineOf(p.Entry, calledContract.Declaration)
	provenanceSaid := walk.ProvenanceSaidOf(entryRows, returnCases)

	rendered, marshalErr := json.MarshalIndent(
		exportFactEnvelope(filepath.Base(entryPath), contentHash, calledName, harnessShape, argIndex,
			true, entryRows, returnCases, true, provenanceLine, provenanceSaid),
		"", "  ")
	if marshalErr != nil {
		return "", false, fmt.Errorf("rendering the artifact for %s: %w", entryPath, marshalErr)
	}
	if writeErr := atomicWriteArtifact(target, append(rendered, '\n')); writeErr != nil {
		return "", false, writeErr
	}
	return target, true, nil
}

// cachedArtifactContentHashMatches reads the cache entry's own
// target.contentHash and compares it against the bytes just hashed —
// a missing or unreadable cache entry never matches, which is the
// correct "export it" answer for a first save. This reads raw JSON
// rather than going through walk.ReadForeignArtifact: that reader
// checks TARGET INTEGRITY, runtime band, and harness shape too, all of
// which are the CONSUMER's premises, not the producer's — the producer
// only needs to know whether its own last write is already current.
func cachedArtifactContentHashMatches(artifactPath string, contentHash string) bool {
	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		return false
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false
	}
	target, ok := parsed["target"].(map[string]any)
	if !ok {
		return false
	}
	stated, ok := target["contentHash"].(string)
	return ok && stated == contentHash
}

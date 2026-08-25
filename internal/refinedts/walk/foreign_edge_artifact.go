// The fact a foreign target exports about itself, read off disk.
//
// A cross-language call edge (foreign_edge.go) claims something about
// the code that runs on the other side. That claim is only as good as
// the two things this file establishes: the artifact SAYS what the
// target's entry admits and what its return holds, and the artifact is
// about the FILE THE CHECK READS — the content hash, which is
// CROSS-LANGUAGE-EDGE.md §5's target-integrity premise and not a
// convenience.
//
// The only accepted envelope is the RULED cases schema — NO version
// field, ever ("refined":{"kind":"fact-artifact"} is an identity
// marker only): one kind shared by every language, distinguished by
// the `language` field rather than by a per-language kind string:
//
//	{"refined": {"kind": "fact-artifact"},
//	 "target": {"file", "contentHash": "sha256:<hex>"},
//	 "language": "python" | "typescript",
//	 "runtime": {"band": "cpython-3.11+" | "es2023+"},
//	 "surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "<fn>"}
//	          | {"kind": "argv-scalar", "argIndex": n, "parse": "float", "stdout": "json", "calls": "<fn>"}
//	          | {"kind": "stdin-json-argv-scalar", "stdin": "json", "argIndex": n, "parse": "float",
//	             "stdout": "json", "calls": "<fn>"}
//	          | {"kind": "file-json", "argIndex": n, "stdout": "json", "calls": "<fn>"},
//	 "functions": {"<name>": {
//	   "entry": [{"name", "sequence": {"element": {"cases": [Case]}, "lengthAtLeast": n}}
//	            |{"name", "cases": [Case]}],
//	   "return": {"cases": [Case], "stdoutPure": bool},
//	   "provenance": {"line": n, "said": "..."}}}}
//
// Case := {"sort": "number", "set": <WireSet>} | {"sort": "string", "set": <WireSet>}
//       | {"sort": "boolean"} | {"sort": "null"} — the full kernel wire
// set grammar inside a number/string case, reused verbatim from the
// existing kernelbridge codec; boolean and null carry no set at all
// (the whole-sort floor, and the absent value — both match what
// actually crosses the wire, since JSON.stringify's own bare tokens
// are what actually crosses). A single case still spells as a
// one-element cases list — there is no bare, non-list shorthand.
//
// The four surface kinds are different INBOUND channels: stdin-json's
// one crossing value arrives as JSON on stdin; argv-scalar's arrives as
// one string at sys.argv[argIndex], parsed with Python's float() — it
// carries no stdin field, since nothing crosses on stdin for that
// surface. stdin-json-argv-scalar is the two together: the target's
// entry has exactly TWO rows, entry[0] receiving the stdin JSON value
// and entry[1] the argv float. file-json's one crossing value arrives
// as JSON read from the FILE NAMED at sys.argv[argIndex] — the file is
// the carrier, and the transport model is the same JSON reading
// stdin-json applies, only relocated: entry has one row, for the
// file's own JSON content, not for the argv string that names it. A
// call crossing on the wrong channel for the target's own surface is a
// channel mismatch (foreign_edge.go), not a fit question.
//
// `language` selects which runtime pins the band is checked against
// ("adding a language does not add an artifact kind").
// `dispatchArtifactEnvelope` routes the (kind, language) pair to the
// reader whose field meanings it pins; any other pair, OR any other
// envelope shape at all — a "version" field, a bare "set"/old
// "sequence" spelling — declines by name: the reader parses the
// CURRENT shape strictly, and a superseded shape is NO-FACT with the
// named sentence, same as an unrecognized kind.
//
// Every Case's own <set> is the kernel's own forms JSON, decoded by
// kernelbridge.DecodeWireSet — the SAME decoder every kernel answer
// goes through, so a set that crossed the edge and a set the kernel
// answered are the same object. DecodeWireSet PANICS on an unknown
// form (its own contract: a mistyped wire is a violation, not a value
// to degrade on), so every decode here runs under a recover that turns
// the panic into a named decline — an artifact is a FILE, written by
// another program, and a checker must not die on a malformed one.
//
// Nothing here reaches the kernel or the walk. It reads bytes, hashes
// bytes, and answers a fact or a sentence saying why not.
//
// This file holds the artifact's shared types and the memoized entry
// point (ReadForeignArtifact); the reader that parses and verifies an
// envelope lives in foreign_edge_artifact_read.go, the surface reader
// in foreign_edge_artifact_surface.go, the per-function fact reader in
// foreign_edge_artifact_fact.go, producer resolution and auto-export
// in foreign_edge_artifact_export.go, and the compiled-binary sibling
// path in foreign_edge_artifact_compiled_binary.go.

package walk

import (
	"os"
	"path/filepath"
	"sync"
)

// ForeignArtifactSuffix is what the producer appends to the target's
// path under the project cache: `audio_level.py` caches as
// `.refined/cache/<relpath>/audio_level.py.refined.json`.
const ForeignArtifactSuffix = ".refined.json"

// ForeignCacheDir is the project-rooted cache both checkers share —
// gitignored, one entry per target, overwritten in place. The content
// hash INSIDE the artifact is the identity; the path only says where
// to look. A sidecar written with `--export-fact -o` is internal
// tooling, never consulted here.
var ForeignCacheDir = filepath.Join(".refined", "cache")

// FactArtifactKindV2 is the one envelope kind this consumer admits —
// one kind shared by every language, distinguished by the `language`
// field rather than by a per-language kind string. NO version field
// rides beside it, ever (the RULED schema's own rule: "refined" is an
// identity marker, not a ceremony) — a different kind, OR any envelope
// carrying a "version" field at all, is a decline, never a best-effort
// read: the reader parses the CURRENT shape strictly. The name keeps
// its "V2" suffix for the CONSTANT only (every existing caller —
// service/export_fact.go's own re-export — already spells it), not
// because a second version exists to distinguish it from.
const FactArtifactKindV2 = "fact-artifact"

// ForeignRuntimeBand is the interpreter band the Python pins commit to
// (CROSS-LANGUAGE-EDGE.md §5, runtime identity). An artifact naming a
// different band is about semantics this tree has not transcribed.
const ForeignRuntimeBand = "cpython-3.11+"

// ForeignExportCommand is the command that writes a missing artifact —
// carried INTO the diagnostic, so a missing fact reads as a work queue
// item rather than as a silent nothing.
const ForeignExportCommand = "refinedpy-check --export-fact"

// ForeignEntry is one parameter position the target states: either a
// SEQUENCE (an element cases list plus the length floor the body
// relies on) or a plain scalar cases list — the RULED schema's own
// "cases" vocabulary, reusing the Case type fact_export.go's exporter
// already defines (walk has no per-direction split: the same Case
// shape crosses the wire from a writer and back into a reader).
type ForeignEntry struct {
	Name string
	// ElementCases and LengthAtLeast describe a sequence position;
	// IsSequence says which of the two shapes this row is.
	IsSequence    bool
	ElementCases  []Case
	LengthAtLeast int
	// Cases is the scalar position's own cases list.
	Cases []Case
}

// ForeignReturn is what the target's result holds, plus the channel
// fact the edge consumes: stdoutPure is §5's channel-purity premise —
// the target writes NOTHING to stdout but the serialized result.
type ForeignReturn struct {
	Cases      []Case
	StdoutPure bool
}

// ForeignProvenance is where the target's claim came from: the line in
// the Python file and the sentence its checker said. Rendered as a
// related-information step in the foreign file (foreign_edge.go's
// four call sites), pointing at the line's own span.
//
// Text/Start/Length are filled together at read time from the SAME
// bytes checkTargetIntegrity already read for the hash (never re-read
// here): Text is the target's whole source, and Start/Length are the
// provenance line's own byte span within it — column 1, whole line,
// no trailing newline — computed once so foreign_edge.go can call
// StepInForeignFile(File, Text, Start, Length, Said) directly. Zero
// Length (no provenance line stated, or the line is out of range)
// means StepInForeignFile degrades to the file's head, same as an
// empty Text does.
type ForeignProvenance struct {
	File   string
	Line   int
	Said   string
	Text   string
	Start  int
	Length int
}

// ForeignFunctionFact is one target function's whole exported fact.
type ForeignFunctionFact struct {
	Name       string
	Entry      []ForeignEntry
	Return     ForeignReturn
	Provenance ForeignProvenance
}

// ForeignSurfaceChannel is which inbound channel(s) the target's
// __main__ block reads its crossing value(s) from — "stdin-json" (the
// value arrives as JSON on stdin), "argv-scalar" (the value arrives as
// one argv string, parsed with float()), "stdin-json-argv-scalar" (TWO
// values cross at once: a JSON value on stdin AND a float at one argv
// position), or "file-json" (the value arrives as JSON read from a file
// NAMED at one argv position — the file is the carrier, never the argv
// string itself). A target states exactly one channel shape, and a
// caller crossing on a different shape is a channel mismatch, not a fit
// question.
type ForeignSurfaceChannel string

const (
	ForeignSurfaceStdinJSON      ForeignSurfaceChannel = "stdin-json"
	ForeignSurfaceArgvScalar     ForeignSurfaceChannel = "argv-scalar"
	ForeignSurfaceMixedStdinArgv ForeignSurfaceChannel = "stdin-json-argv-scalar"
	ForeignSurfaceFileJSON       ForeignSurfaceChannel = "file-json"
)

// ForeignArtifact is the artifact as consumed: the runtime band it
// commits to, the surface the __main__ block runs, and the ONE function
// the surface calls, already selected.
type ForeignArtifact struct {
	// Path: the artifact file itself, for the diagnostics.
	Path string
	// TargetFile: the .py path the artifact is about, as resolved here
	// (not as the artifact spells it — the hash is what ties them).
	TargetFile  string
	RuntimeBand string
	// Surface: which inbound channel(s) the target reads (stdin-json,
	// argv-scalar, the mixed stdin+argv shape, or file-json) — the
	// consumer checks the caller's own crossing channel(s) against this
	// before judging fit at all.
	Surface ForeignSurfaceChannel
	// ArgvIndex: the argv position the target reads its scalar from —
	// meaningful for ForeignSurfaceArgvScalar (schema v2's "argIndex"),
	// ForeignSurfaceMixedStdinArgv (the argv leg's own position, entry[1]),
	// and ForeignSurfaceFileJSON (the position naming the file path).
	ArgvIndex int
	// Called: the fact of surface.calls — the function the stdin/stdout
	// surface actually invokes, which is the only one this edge consumes.
	Called ForeignFunctionFact
}

// foreignArtifactRow is one read's whole outcome, memoized: the fact
// or the sentence that stopped it, plus the artifact file's mtime AT
// FILL TIME — the freshness stopgap below reads this to notice a
// producer (or a live LSP) rewriting the cache mid-process.
type foreignArtifactRow struct {
	artifact *ForeignArtifact
	sentence string
	modTime  int64
}

// foreignArtifacts memoizes ReadForeignArtifact by target path. The
// walk reaches one statement many times — the speculative recovery
// passes, the correlation splits, every inlining of the enclosing
// function — and each reach would otherwise re-read and re-HASH the
// target. The row is held for the process, which is the same lifetime
// the question cache and the contract index already assume; a check
// reads a target it did not write, so a mid-check change to that file
// is a rebuild's concern, not this walk's.
var (
	foreignArtifactsMu sync.Mutex
	foreignArtifacts   = map[string]foreignArtifactRow{}
)

// ReadForeignArtifact resolves the target's project-cache entry,
// filling it through the resolved producer when it is missing or
// stale, checks every premise this file owns, and answers the
// surface-called function's fact — or ("", one sentence) saying which
// premise broke.
//
// The premises discharged HERE, each a real check and none assumed:
//
//   - the artifact EXISTS in the cache (a miss auto-exports when a
//     producer resolves; otherwise the sentence names the file and
//     the command that writes it);
//   - the envelope is this kind and this version;
//   - TARGET INTEGRITY (§5): sha256 of the .py file's ACTUAL BYTES
//     equals the artifact's stated contentHash. A mismatch means the
//     claim is about code that is not the code being checked;
//   - RUNTIME IDENTITY (§5): the stated band is the one the pins commit
//     to;
//   - the surface is a recognized channel (stdin-json or argv-scalar),
//     writes json on stdout, and names a function the artifact
//     actually carries a fact for.
//
// CHANNEL PURITY (§5) is NOT checked here: it is a property of the
// consumed function's return, so the edge checks it where it consumes
// it (foreign_edge.go), which is where the sentence can name the call.
//
// FRESHNESS (the stopgap docs/one-checker/fact-freshness.md names,
// pending the coordinator's push-based invalidation): the memo is held
// for the process, which was correct while a producer only ran ahead
// of the check — wrong once a live LSP writes the cache mid-session.
// Every read stats the artifact path; a changed mtime drops the row
// and re-reads rather than serving what a since-overwritten file said.
func ReadForeignArtifact(targetPath string) (*ForeignArtifact, string) {
	artifactPath := ForeignCacheArtifactPath(targetPath)
	currentModTime := artifactModTime(artifactPath)

	foreignArtifactsMu.Lock()
	held, memoized := foreignArtifacts[targetPath]
	foreignArtifactsMu.Unlock()
	if memoized && held.modTime == currentModTime {
		return held.artifact, held.sentence
	}

	artifact, sentence := readForeignArtifactUncached(targetPath)
	// the read above may itself have exported a fresh artifact (the
	// miss-triggers-export path), so the mtime recorded against the memo
	// is read AFTER that read, not the one taken before it
	foreignArtifactsMu.Lock()
	foreignArtifacts[targetPath] = foreignArtifactRow{
		artifact: artifact,
		sentence: sentence,
		modTime:  artifactModTime(artifactPath),
	}
	foreignArtifactsMu.Unlock()
	return artifact, sentence
}

// InvalidateForeignArtifact drops the memoized row for path (the
// target file — the same string ReadForeignArtifact is keyed by, not
// the .refined.json artifact path) so the next ReadForeignArtifact
// call re-reads and re-verifies rather than serving what an earlier
// call held. For an LSP that can OBSERVE a save (unlike this package's
// own mtime stopgap, which only notices a rewrite on the NEXT read),
// calling this on didSave is the push-based invalidation the
// mtime-polling comment above stands in for until it lands.
func InvalidateForeignArtifact(path string) {
	foreignArtifactsMu.Lock()
	delete(foreignArtifacts, path)
	foreignArtifactsMu.Unlock()
}

// artifactModTime answers the artifact file's modification time as a
// unix-nanosecond stamp, or 0 when the file does not exist — a missing
// file and "never read" both memo as 0, and the first successful write
// (a nonzero mtime) is itself a freshness change worth reacting to.
func artifactModTime(artifactPath string) int64 {
	info, err := os.Stat(artifactPath)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

// readForeignArtifactUncached fills the cache when it can and reads
// it — every premise checked, no memo consulted. A missing or failed
// artifact triggers ONE export attempt through the resolved producer
// (explicitProducerPyPath, then the project-root build, then PATH);
// when no producer resolves, the sentence names the file and the
// command, exactly as before.
//
// STALENESS (beside the content hash): a cached artifact that reads
// cleanly is still stale when the RESOLVED PRODUCER BINARY's own mtime
// is newer than the cache entry's — a rebuilt producer may derive a
// different fact for the SAME target source (a sharper kernel, a
// fixed bug in the exporter itself), and the content hash alone cannot
// notice that, since the target's bytes never changed. No stamps, no
// counters: the producer binary this same read already resolves for
// auto-export is stat'd once more here, and a newer mtime triggers the
// identical one re-export attempt the missing/failed path already
// takes, before the read that follows.
func readForeignArtifactUncached(targetPath string) (*ForeignArtifact, string) {
	artifactPath := ForeignCacheArtifactPath(targetPath)
	// REFINED_EXPORT_CHAIN is read ONCE here, at the point the spawn
	// decision is made — never inside exportForeignArtifact itself,
	// which takes the chain as a plain parameter so it is testable
	// without mutating process environment.
	exportChain := os.Getenv(ExportChainEnvVar)
	if producerBinaryNewerThanArtifact(targetPath, artifactPath) {
		// best-effort: a re-export failure here falls through to the
		// ordinary read below, which still answers whatever the existing
		// cache entry states — a producer that cannot be re-run is not
		// grounds to lose an already-valid fact
		exportForeignArtifact(targetPath, artifactPath, exportChain)
	}
	artifact, sentence := readAndVerifyForeignArtifact(targetPath, artifactPath)
	if sentence == "" {
		return artifact, ""
	}
	if exportSentence := exportForeignArtifact(targetPath, artifactPath, exportChain); exportSentence != "" {
		return nil, sentence + " (auto-export declined: " + exportSentence + ")"
	}
	return readAndVerifyForeignArtifact(targetPath, artifactPath)
}

// producerBinaryNewerThanArtifact answers whether the resolved
// producer binary's mtime is strictly newer than the cached artifact's
// own mtime — false whenever either file cannot be stat'd (no producer
// resolves, or no cache entry exists yet; the ordinary missing-artifact
// path already handles the latter) or the artifact is at least as new,
// which is the ordinary case once a rebuilt producer has re-exported.
func producerBinaryNewerThanArtifact(targetPath string, artifactPath string) bool {
	producer := resolveProducerPyPath(targetPath)
	if producer == "" {
		return false
	}
	producerInfo, producerErr := os.Stat(producer)
	if producerErr != nil {
		return false
	}
	artifactInfo, artifactErr := os.Stat(artifactPath)
	if artifactErr != nil {
		return false
	}
	return producerInfo.ModTime().After(artifactInfo.ModTime())
}

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
// The only accepted envelope is schema v2
// (docs/one-checker/schema-v2.md) — one kind shared by every language,
// distinguished by the `language` field rather than by a per-language
// kind string:
//
//	{"refined": {"kind": "fact-artifact", "version": 2},
//	 "target": {"file", "contentHash": "sha256:<hex>"},
//	 "language": "python" | "typescript",
//	 "runtime": {"band": "cpython-3.11+" | "es2023+"},
//	 "surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "<fn>"}
//	          | {"kind": "argv-scalar", "argIndex": n, "parse": "float", "stdout": "json", "calls": "<fn>"}
//	          | {"kind": "stdin-json-argv-scalar", "stdin": "json", "argIndex": n, "parse": "float",
//	             "stdout": "json", "calls": "<fn>"}
//	          | {"kind": "file-json", "argIndex": n, "stdout": "json", "calls": "<fn>"},
//	 "functions": {"<name>": {
//	   "entry": [{"name", "sequence": {"element": <set>, "lengthAtLeast": n}}
//	            |{"name", "set": <set>}],
//	   "return": {"set": <set>, "stdoutPure": bool},
//	   "provenance": {"line": n, "said": "..."}}}}
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
// `dispatchArtifactEnvelope` routes the (kind, version, language)
// triple to the reader whose field meanings it pins; any other triple
// declines by name.
//
// Every <set> is the kernel's own forms JSON, decoded by
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

package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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

// FactArtifactKindV2 and FactArtifactVersionV2 are the one envelope
// this consumer admits (docs/one-checker/schema-v2.md) — one kind
// shared by every language, distinguished by the `language` field
// rather than by a per-language kind string. A different kind or
// version is a decline, never a best-effort read: the fields'
// meanings are what the version pins.
const (
	FactArtifactKindV2    = "fact-artifact"
	FactArtifactVersionV2 = 2
)

// ForeignRuntimeBand is the interpreter band the Python pins commit to
// (CROSS-LANGUAGE-EDGE.md §5, runtime identity). An artifact naming a
// different band is about semantics this tree has not transcribed.
const ForeignRuntimeBand = "cpython-3.11+"

// ForeignExportCommand is the command that writes a missing artifact —
// carried INTO the diagnostic, so a missing fact reads as a work queue
// item rather than as a silent nothing.
const ForeignExportCommand = "refinedpy-check --export-fact"

// ForeignEntry is one parameter position the target states: either a
// SEQUENCE (an element set plus the length floor the body relies on)
// or a plain scalar set.
type ForeignEntry struct {
	Name string
	// Element and LengthAtLeast describe a sequence position; IsSequence
	// says which of the two shapes this row is.
	IsSequence    bool
	Element       refinementsets.RefinedSet
	LengthAtLeast int
	// Set is the scalar position's own set.
	Set refinementsets.RefinedSet
}

// ForeignReturn is what the target's result holds, plus the channel
// fact the edge consumes: stdoutPure is §5's channel-purity premise —
// the target writes NOTHING to stdout but the serialized result.
type ForeignReturn struct {
	Set        refinementsets.RefinedSet
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
func readForeignArtifactUncached(targetPath string) (*ForeignArtifact, string) {
	artifactPath := ForeignCacheArtifactPath(targetPath)
	artifact, sentence := readAndVerifyForeignArtifact(targetPath, artifactPath)
	if sentence == "" {
		return artifact, ""
	}
	if exportSentence := exportForeignArtifact(targetPath, artifactPath); exportSentence != "" {
		return nil, sentence + " (auto-export declined: " + exportSentence + ")"
	}
	return readAndVerifyForeignArtifact(targetPath, artifactPath)
}

// ForeignCacheArtifactPath resolves the target's cache entry: the
// nearest ancestor holding `.git` is the project root (the target's
// own directory when none is found), and the entry mirrors the
// target's path relative to that root. Exported: service/export_fact.go
// derives its own default `-o` from this same rule, so the two
// checkers meet at one file without either being told where.
func ForeignCacheArtifactPath(targetPath string) string {
	abs, err := filepath.Abs(targetPath)
	if err != nil {
		return targetPath + ForeignArtifactSuffix
	}
	root := projectRootOf(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(abs)
	}
	return filepath.Join(root, ForeignCacheDir, rel+ForeignArtifactSuffix)
}

// projectRootOverrideMu guards projectRootOverride, mirroring
// explicitProducerPyPath's discipline: a plain setter, never an
// environment variable.
var (
	projectRootOverrideMu sync.Mutex
	projectRootOverride   string
)

// SetProjectRootOverride states the project root outright (typically
// cmd/refinedts-check's `-project-root` flag, set by a caller — the
// `refined` front door — that already resolved it), bypassing the
// `.git`-walk below for both the cache path and producer resolution.
// "" (the default) restores the walk.
func SetProjectRootOverride(root string) {
	projectRootOverrideMu.Lock()
	defer projectRootOverrideMu.Unlock()
	projectRootOverride = root
}

// projectRootOf is the nearest ancestor of an absolute path holding
// `.git` — the target's own directory when none is found — unless
// SetProjectRootOverride named the root outright. Shared by
// ForeignCacheArtifactPath (where the cache entry lives) and
// exportForeignArtifact (where a project-local producer build lives),
// so the two never derive the root two different ways.
func projectRootOf(abs string) string {
	projectRootOverrideMu.Lock()
	override := projectRootOverride
	projectRootOverrideMu.Unlock()
	if override != "" {
		return override
	}
	root := filepath.Dir(abs)
	for dir := filepath.Dir(abs); ; {
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
			root = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return root
}

// explicitProducerPyPathMu guards explicitProducerPyPath, mirroring
// kernelbridge.SetDylibPath's own discipline: a plain setter, never an
// environment variable — behavior is configured by arguments (a
// binary's `-producer-py` flag) or the binary's own layout, never by
// ambient process state.
var (
	explicitProducerPyPathMu sync.Mutex
	explicitProducerPyPath   string
)

// SetPythonProducerPath states where the refinedpy-check binary lives,
// for a caller that already knows (typically cmd/refinedts-check's
// `-producer-py` flag). Resolution otherwise falls through to a
// project-root build, then PATH — see exportForeignArtifact.
func SetPythonProducerPath(path string) {
	explicitProducerPyPathMu.Lock()
	defer explicitProducerPyPathMu.Unlock()
	explicitProducerPyPath = path
}

// resolveProducerPyPath answers the refinedpy-check binary to run, in
// order: the caller-stated path (SetPythonProducerPath), a release
// build under the project root, a debug build under the project root,
// then whatever `refinedpy-check` PATH resolves to. "" means none of
// the four held — no environment variable is read at any step (the
// standing rule: ambient process state never configures behavior).
func resolveProducerPyPath(targetPath string) string {
	explicitProducerPyPathMu.Lock()
	explicit := explicitProducerPyPath
	explicitProducerPyPathMu.Unlock()
	if explicit != "" {
		return explicit
	}
	if abs, err := filepath.Abs(targetPath); err == nil {
		root := projectRootOf(abs)
		for _, profile := range []string{"release", "debug"} {
			candidate := filepath.Join(root, "packages", "refinedpy", "pyrefly", "target", profile, "refinedpy-check")
			if _, statErr := os.Stat(candidate); statErr == nil {
				return candidate
			}
		}
	}
	if found, err := exec.LookPath("refinedpy-check"); err == nil {
		return found
	}
	return ""
}

// exportForeignArtifact runs the resolved producer into the cache
// entry, answering "" on success and one sentence naming what stopped
// it. Resolution: resolveProducerPyPath's three-step order, above.
func exportForeignArtifact(targetPath string, artifactPath string) string {
	producer := resolveProducerPyPath(targetPath)
	if producer == "" {
		return "no -producer-py flag, no built refinedpy-check under the project root, and none on PATH"
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		return "the cache directory could not be created: " + err.Error()
	}
	command := exec.Command(producer, "--export-fact", targetPath, "-o", artifactPath)
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "the export run failed: " + message
	}
	return ""
}

// readAndVerifyForeignArtifact is the read itself: parse, then dispatch
// on the envelope triple to the reader whose field meanings it pins.
func readAndVerifyForeignArtifact(targetPath string, artifactPath string) (*ForeignArtifact, string) {
	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		return nil, "the Python target " + targetPath + " states no fact for this edge — " +
			"there is no " + artifactPath + "; write it with `" +
			ForeignExportCommand + " " + targetPath + "`"
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, artifactPath + " is not readable JSON, so the target states nothing this edge can use"
	}
	return dispatchArtifactEnvelope(parsed, targetPath, artifactPath)
}

// dispatchArtifactEnvelope reads the `refined` envelope and the v2
// `language` field, then routes to the reader whose field meanings the
// (kind, version, language) triple pins: only ("fact-artifact", 2,
// "python") is read. Any other triple declines by name, naming the one
// accepted form.
func dispatchArtifactEnvelope(parsed map[string]any, targetPath string, artifactPath string) (*ForeignArtifact, string) {
	envelope, ok := parsed["refined"].(map[string]any)
	if !ok {
		return nil, artifactPath + ` carries no "refined" envelope, so nothing identifies it as a fact artifact`
	}
	kind, _ := envelope["kind"].(string)
	version, versionOk := envelope["version"].(float64)
	language, _ := parsed["language"].(string)

	switch {
	case kind == FactArtifactKindV2 && versionOk && int(version) == FactArtifactVersionV2 && language == "python":
		return readPythonArtifact(parsed, targetPath, artifactPath)
	default:
		return nil, artifactPath + ` states (kind "` + kind + `", version ` + jsonNumberString(version) +
			`, language "` + language + `"), and this edge reads only ("` +
			FactArtifactKindV2 + `", ` + strconv.Itoa(FactArtifactVersionV2) + `, "python")`
	}
}

// readPythonArtifact is the body reader for language "python": target
// integrity, runtime band, then one called function named through
// `surface`, whose `kind` is either "stdin-json" or "argv-scalar"
// (schema-v2.md's two modeled transports).
func readPythonArtifact(parsed map[string]any, targetPath string, artifactPath string) (*ForeignArtifact, string) {
	sentence, targetBytes := checkTargetIntegrity(parsed, targetPath, artifactPath)
	if sentence != "" {
		return nil, sentence
	}
	band, bandOk := nestedString(parsed, "runtime", "band")
	if !bandOk {
		return nil, artifactPath + " names no runtime band, and the edge's claim inherits " +
			"whichever band the target's pins commit to"
	}
	if band != ForeignRuntimeBand {
		return nil, artifactPath + " commits to the runtime band " + band +
			", and this checker's Python pins commit to " + ForeignRuntimeBand +
			" — the edge cannot inherit semantics it has not transcribed"
	}
	surface, surfaceSentence := surfaceOf(parsed, artifactPath)
	if surfaceSentence != "" {
		return nil, surfaceSentence
	}
	fact, factSentence := functionFactOf(parsed, surface.calls, artifactPath, targetPath, targetBytes)
	if factSentence != "" {
		return nil, factSentence
	}
	return &ForeignArtifact{
		Path:        artifactPath,
		TargetFile:  targetPath,
		RuntimeBand: band,
		Surface:     surface.channel,
		ArgvIndex:   surface.argIndex,
		Called:      *fact,
	}, ""
}

// foreignSurface is surfaceOf's whole reading: which channel(s) the
// target serves, the argv position an argv-scalar/mixed/file-json
// surface names, and the one function the __main__ block calls.
type foreignSurface struct {
	channel  ForeignSurfaceChannel
	argIndex int
	calls    string
}

// surfaceOf reads the target's inbound/outbound channel: the wire is
// JSON in both directions for "stdin-json", one argv string parsed as a
// float for "argv-scalar", both of those together for
// "stdin-json-argv-scalar", or a file's JSON content named at one argv
// position for "file-json" — the outbound leg (stdout) is JSON in every
// case, since every transport still prints `json.dumps(...)`. The
// edge's whole claim is about the ONE named function — a target whose
// surface names a kind other than these four, or calls nothing this
// artifact names, transports something the JSON model does not
// describe.
func surfaceOf(parsed map[string]any, artifactPath string) (foreignSurface, string) {
	surface, ok := parsed["surface"].(map[string]any)
	if !ok {
		// the producer emits no surface key at all for a harness shape it
		// does not recognize
		return foreignSurface{}, artifactPath + " states no callable surface for its __main__ block " +
			"— a harness shape this producer does not export a surface for — so nothing says what " +
			"the target does with its input and output"
	}
	kind, _ := surface["kind"].(string)
	switch kind {
	case string(ForeignSurfaceStdinJSON):
		return surfaceOfStdinJSON(surface, artifactPath)
	case string(ForeignSurfaceArgvScalar):
		return surfaceOfArgvScalar(surface, artifactPath)
	case string(ForeignSurfaceMixedStdinArgv):
		return surfaceOfMixedStdinArgv(surface, artifactPath)
	case string(ForeignSurfaceFileJSON):
		return surfaceOfFileJSON(surface, artifactPath)
	default:
		return foreignSurface{}, artifactPath + ` states a surface of kind ` + quotedOrNone(kind) +
			`, and this edge applies the JSON transport model only to "stdin-json", "argv-scalar", ` +
			`"stdin-json-argv-scalar", or "file-json"`
	}
}

// surfaceOfStdinJSON reads the stdio surface: JSON in both directions.
func surfaceOfStdinJSON(surface map[string]any, artifactPath string) (foreignSurface, string) {
	stdin, _ := surface["stdin"].(string)
	stdout, _ := surface["stdout"].(string)
	if stdin != "json" || stdout != "json" {
		return foreignSurface{}, artifactPath + " states a surface reading " + quotedOrNone(stdin) +
			" on stdin and writing " + quotedOrNone(stdout) +
			" on stdout, and this edge applies the JSON transport model to both legs"
	}
	called, calledOk := surface["calls"].(string)
	if !calledOk || called == "" {
		return foreignSurface{}, artifactPath + " states no surface.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return foreignSurface{channel: ForeignSurfaceStdinJSON, calls: called}, ""
}

// surfaceOfArgvScalar reads the argv-scalar surface: the crossing value
// arrives as one argv string at `argIndex`, parsed with Python's
// float(); stdout is still JSON (schema-v2.md's exact spec: {"kind":
// "argv-scalar", "argIndex": 1, "parse": "float", "stdout": "json",
// "calls": "<fn>"} — no stdin field, since nothing crosses on stdin for
// this surface).
func surfaceOfArgvScalar(surface map[string]any, artifactPath string) (foreignSurface, string) {
	stdout, _ := surface["stdout"].(string)
	if stdout != "json" {
		return foreignSurface{}, artifactPath + " states an argv-scalar surface writing " +
			quotedOrNone(stdout) + " on stdout, and this edge applies the JSON transport model " +
			"to the return leg"
	}
	parse, _ := surface["parse"].(string)
	if parse != "float" {
		return foreignSurface{}, artifactPath + " states an argv-scalar surface parsing " +
			quotedOrNone(parse) + ", and this edge reads only the \"float\" parse — Python's " +
			"float(sys.argv[n])"
	}
	argIndexFloat, hasIndex := surface["argIndex"].(float64)
	if !hasIndex {
		return foreignSurface{}, artifactPath + " states an argv-scalar surface with no argIndex, " +
			"so nothing says which argv position the target reads its value from"
	}
	called, calledOk := surface["calls"].(string)
	if !calledOk || called == "" {
		return foreignSurface{}, artifactPath + " states no surface.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return foreignSurface{
		channel:  ForeignSurfaceArgvScalar,
		argIndex: int(argIndexFloat),
		calls:    called,
	}, ""
}

// surfaceOfMixedStdinArgv reads the mixed surface: one value crosses on
// stdin as JSON, and a SECOND value crosses at argv[argIndex], parsed
// with Python's float() — schema-v2.md's exact spec: {"kind":
// "stdin-json-argv-scalar", "stdin": "json", "argIndex": 1, "parse":
// "float", "stdout": "json", "calls": "<fn>"}. The target's own entry
// therefore has exactly TWO rows in this one function's fact: entry[0]
// is the stdin leg's own set, entry[1] the argv leg's — the caller
// (checkOutboundLeg's mixed branch) fits each leg through its own
// existing crossing function rather than through one combined question.
func surfaceOfMixedStdinArgv(surface map[string]any, artifactPath string) (foreignSurface, string) {
	stdin, _ := surface["stdin"].(string)
	stdout, _ := surface["stdout"].(string)
	if stdin != "json" || stdout != "json" {
		return foreignSurface{}, artifactPath + " states a mixed surface reading " + quotedOrNone(stdin) +
			" on stdin and writing " + quotedOrNone(stdout) +
			" on stdout, and this edge applies the JSON transport model to both legs"
	}
	parse, _ := surface["parse"].(string)
	if parse != "float" {
		return foreignSurface{}, artifactPath + " states a mixed surface parsing " +
			quotedOrNone(parse) + " on its argv leg, and this edge reads only the \"float\" parse — " +
			"Python's float(sys.argv[n])"
	}
	argIndexFloat, hasIndex := surface["argIndex"].(float64)
	if !hasIndex {
		return foreignSurface{}, artifactPath + " states a mixed surface with no argIndex, " +
			"so nothing says which argv position the target reads its second value from"
	}
	called, calledOk := surface["calls"].(string)
	if !calledOk || called == "" {
		return foreignSurface{}, artifactPath + " states no surface.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return foreignSurface{
		channel:  ForeignSurfaceMixedStdinArgv,
		argIndex: int(argIndexFloat),
		calls:    called,
	}, ""
}

// surfaceOfFileJSON reads the file-carried surface: the crossing value
// arrives as JSON, but read from a FILE whose path is named at
// argv[argIndex] — the argv string itself carries no data, only the
// path — schema-v2.md's exact spec: {"kind": "file-json", "argIndex": 1,
// "stdout": "json", "calls": "<fn>"}. The target's entry has one row —
// the file's own JSON content — exactly as stdin-json's does; only the
// carrier differs (a file instead of the stdin stream), so the JSON
// transport model itself is shared, never re-derived.
func surfaceOfFileJSON(surface map[string]any, artifactPath string) (foreignSurface, string) {
	stdout, _ := surface["stdout"].(string)
	if stdout != "json" {
		return foreignSurface{}, artifactPath + " states a file-json surface writing " +
			quotedOrNone(stdout) + " on stdout, and this edge applies the JSON transport model " +
			"to the return leg"
	}
	argIndexFloat, hasIndex := surface["argIndex"].(float64)
	if !hasIndex {
		return foreignSurface{}, artifactPath + " states a file-json surface with no argIndex, " +
			"so nothing says which argv position names the file the target reads"
	}
	called, calledOk := surface["calls"].(string)
	if !calledOk || called == "" {
		return foreignSurface{}, artifactPath + " states no surface.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return foreignSurface{
		channel:  ForeignSurfaceFileJSON,
		argIndex: int(argIndexFloat),
		calls:    called,
	}, ""
}

// checkTargetIntegrity is CROSS-LANGUAGE-EDGE.md §5's target-integrity
// premise, discharged by hashing the file the check will run against
// and comparing it to the hash the producer recorded. The artifact's
// own `target.file` string is NOT trusted as the identity — a path can
// be stale or relative to another root; the hash is the identity.
//
// Answers the bytes it read alongside the sentence, so a caller past
// this premise (functionFactOf, building the provenance step) can
// place a line in the target's text without a second read of the same
// file — nil whenever the sentence is non-empty.
func checkTargetIntegrity(parsed map[string]any, targetPath string, artifactPath string) (string, []byte) {
	stated, statedOk := nestedString(parsed, "target", "contentHash")
	if !statedOk {
		return artifactPath + " records no target contentHash, so nothing ties its claim to " +
			targetPath + " — the target-integrity premise cannot be discharged", nil
	}
	bytes, err := os.ReadFile(targetPath)
	if err != nil {
		return "the Python target " + targetPath + " cannot be read, so its stated fact cannot be tied to it", nil
	}
	sum := sha256.Sum256(bytes)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != stated {
		return artifactPath + " states the fact of a target whose contents hash to " + stated +
			", and " + targetPath + " hashes to " + actual +
			" — the exported fact is about different code than the code being checked; " +
			"re-export it with `" + ForeignExportCommand + " " + targetPath + "`", nil
	}
	return "", bytes
}

// functionFactOf reads one named function's row: its entry positions,
// its return, and the provenance a cross-language message renders.
// targetBytes is the target's own bytes, already read (and hash-
// verified) by checkTargetIntegrity — passed through so the provenance
// step's line span is computed from that one read, never a second one.
func functionFactOf(
	parsed map[string]any, name string, artifactPath string, targetPath string, targetBytes []byte,
) (fact *ForeignFunctionFact, sentence string) {
	// DecodeWireSet panics on a form it does not know — its own stated
	// contract for kernel answers. An artifact is a file another program
	// wrote, so a malformed form is a decline here, never a crash.
	defer func() {
		if recovered := recover(); recovered != nil {
			fact, sentence = nil, artifactPath+" states a set this checker's kernel grammar does not read, "+
				"so the fact for "+name+" cannot be decoded"
		}
	}()
	functions, ok := parsed["functions"].(map[string]any)
	if !ok {
		return nil, artifactPath + " carries no functions, so it states no fact about " + name
	}
	row, ok := functions[name].(map[string]any)
	if !ok {
		return nil, artifactPath + " names " + name + " as the surface's called function and " +
			"then states no fact for it"
	}
	entries, entriesSentence := artifactEntriesOf(row, name, artifactPath)
	if entriesSentence != "" {
		return nil, entriesSentence
	}
	returned, ok := row["return"].(map[string]any)
	if !ok {
		return nil, artifactPath + " states no return fact for " + name +
			", so nothing crosses back from this call"
	}
	rawSet, hasSet := returned["set"]
	if !hasSet {
		return nil, artifactPath + " states a return for " + name + " with no set, " +
			"so the value crossing back is unbounded"
	}
	stdoutPure, _ := returned["stdoutPure"].(bool)
	return &ForeignFunctionFact{
		Name:  name,
		Entry: entries,
		Return: ForeignReturn{
			Set:        kernelbridge.DecodeWireSet(rawSet),
			StdoutPure: stdoutPure,
		},
		Provenance: artifactProvenanceOf(row, targetPath, targetBytes),
	}, ""
}

// artifactEntriesOf reads the entry rows in the order the artifact
// spells them — that order IS the positional order of the target's
// parameters, which is how an argument finds the row it must fit.
func artifactEntriesOf(
	row map[string]any, name string, artifactPath string,
) ([]ForeignEntry, string) {
	rawEntries, ok := row["entry"].([]any)
	if !ok {
		return nil, artifactPath + " states no entry positions for " + name +
			", so nothing says what the target admits"
	}
	entries := make([]ForeignEntry, 0, len(rawEntries))
	for index, rawEntry := range rawEntries {
		entryRow, ok := rawEntry.(map[string]any)
		if !ok {
			return nil, artifactPath + " states an unreadable entry position " +
				strconv.Itoa(index) + " for " + name
		}
		entryName, _ := entryRow["name"].(string)
		if sequence, isSequence := entryRow["sequence"].(map[string]any); isSequence {
			rawElement, hasElement := sequence["element"]
			if !hasElement {
				return nil, artifactPath + " states a sequence entry " + entryName +
					" for " + name + " with no element set"
			}
			lengthAtLeast, _ := sequence["lengthAtLeast"].(float64)
			entries = append(entries, ForeignEntry{
				Name:          entryName,
				IsSequence:    true,
				Element:       kernelbridge.DecodeWireSet(rawElement),
				LengthAtLeast: int(lengthAtLeast),
			})
			continue
		}
		rawSet, hasSet := entryRow["set"]
		if !hasSet {
			return nil, artifactPath + " states an entry position " + entryName +
				" for " + name + " that is neither a sequence nor a set"
		}
		entries = append(entries, ForeignEntry{
			Name: entryName,
			Set:  kernelbridge.DecodeWireSet(rawSet),
		})
	}
	return entries, ""
}

// artifactProvenanceOf reads where the target's claim was made. Absent
// fields leave the provenance empty rather than declining — provenance
// makes a message readable; it is not a premise of the crossing.
//
// targetBytes is the SAME bytes checkTargetIntegrity already read (nil
// when that premise failed, in which case reading gets no further than
// here anyway) — the line's byte span is computed from them, never
// from a fresh read.
func artifactProvenanceOf(row map[string]any, targetPath string, targetBytes []byte) ForeignProvenance {
	provenance, ok := row["provenance"].(map[string]any)
	if !ok {
		return ForeignProvenance{File: targetPath}
	}
	line, _ := provenance["line"].(float64)
	said, _ := provenance["said"].(string)
	result := ForeignProvenance{File: targetPath, Line: int(line), Said: said}
	if result.Line > 0 && targetBytes != nil {
		text := string(targetBytes)
		if start, length, ok := lineSpan(text, result.Line); ok {
			result.Text = text
			result.Start = start
			result.Length = length
		}
	}
	return result
}

// lineSpan answers the byte offset and length of ONE-BASED line
// number `line` in text, spanning column 1 to the line's last byte
// before its terminating '\n' (or before EOF, on the file's last
// line) — never including the newline itself. Answers ok=false for a
// line number the text does not have (the artifact and the target
// have drifted, or line is 0/negative), and the caller leaves the
// provenance step to degrade to the file's head, exactly as
// StepInForeignFile already does for an empty Text.
//
// Mirrors fact_export.rs's own line_starts_of/line_of: line starts are
// offset 0 and every offset right after a '\n', so line N's start is
// starts[N-1] and its own 1-based number is what the producer writes
// as provenance.line.
func lineSpan(text string, line int) (start int, length int, ok bool) {
	if line <= 0 {
		return 0, 0, false
	}
	lineStart := 0
	lineIndex := 1
	for lineIndex < line {
		next := strings.IndexByte(text[lineStart:], '\n')
		if next < 0 {
			return 0, 0, false
		}
		lineStart += next + 1
		lineIndex++
	}
	end := strings.IndexByte(text[lineStart:], '\n')
	if end < 0 {
		end = len(text) - lineStart
	}
	return lineStart, end, true
}

// ProvenanceSentence renders the target's own step of the explanation
// as flat text: where the fact was said, and what was said there.
// foreign_edge.go's four diagnostic sites carry the same information
// as a real related-information step instead (StepInForeignFile,
// built from this same File/Text/Start/Length); this renderer stays
// for a caller that only has message text to work with.
func (p ForeignProvenance) ProvenanceSentence() string {
	if p.File == "" {
		return ""
	}
	where := p.File
	if p.Line > 0 {
		where += ":" + strconv.Itoa(p.Line)
	}
	if p.Said == "" {
		return "the target states this at " + where
	}
	return where + " said: " + p.Said
}

// nestedString reads parsed[outer][inner] as a string.
func nestedString(parsed map[string]any, outer string, inner string) (string, bool) {
	object, ok := parsed[outer].(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := object[inner].(string)
	return value, ok
}

// quotedOrNone spells a surface channel for a message: the word it
// states, or "nothing" where the field is absent.
func quotedOrNone(word string) string {
	if word == "" {
		return "nothing"
	}
	return `"` + word + `"`
}

// foreignSetWords is how a set reaches a diagnostic here — the same
// formatter every refinement diagnostic uses, so a crossing's message
// reads like any other refutation.
func foreignSetWords(set refinementsets.RefinedSet) string {
	words := refinementsets.FormatForDiagnostics(set)
	return strings.TrimSpace(words)
}

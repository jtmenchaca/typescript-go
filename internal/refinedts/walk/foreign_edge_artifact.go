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
// The schema is frozen (§17 E2); the Python side's exporter writes it
// and this side consumes it verbatim:
//
//	{"refined": {"kind": "python-fact-artifact", "version": 1},
//	 "target": {"file", "contentHash": "sha256:<hex>"},
//	 "runtime": {"band": "cpython-3.11+"},
//	 "harness": {"stdin": "json", "stdout": "json", "calls": "<fn>"},
//	 "functions": {"<name>": {
//	   "entry": [{"name", "sequence": {"element": <set>, "lengthAtLeast": n}}
//	            |{"name", "set": <set>}],
//	   "return": {"set": <set>, "stdoutPure": bool},
//	   "provenance": {"line": n, "said": "..."}}}}
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

// ForeignArtifactKind and ForeignArtifactVersion are the envelope this
// consumer admits. A different kind or version is a decline, never a
// best-effort read: the fields' meanings are what the version pins.
const (
	ForeignArtifactKind    = "python-fact-artifact"
	ForeignArtifactVersion = 1
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
// the Python file and the sentence its checker said. Rendered as the
// second step of a cross-language message (§9's chain, in its
// message-text form until relatedInformation carries it).
type ForeignProvenance struct {
	File string
	Line int
	Said string
}

// ForeignFunctionFact is one target function's whole exported fact.
type ForeignFunctionFact struct {
	Name       string
	Entry      []ForeignEntry
	Return     ForeignReturn
	Provenance ForeignProvenance
}

// ForeignArtifact is the artifact as consumed: the runtime band it
// commits to, the harness the __main__ block runs, and the ONE function
// the harness calls, already selected.
type ForeignArtifact struct {
	// Path: the artifact file itself, for the diagnostics.
	Path string
	// TargetFile: the .py path the artifact is about, as resolved here
	// (not as the artifact spells it — the hash is what ties them).
	TargetFile  string
	RuntimeBand string
	// Called: the fact of harness.calls — the function the stdin/stdout
	// harness actually invokes, which is the only one this edge consumes.
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
// harness-called function's fact — or ("", one sentence) saying which
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
//   - the harness reads json on stdin and writes json on stdout, and
//     names a function the artifact actually carries a fact for.
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

// projectRootOf is the nearest ancestor of an absolute path holding
// `.git` — the target's own directory when none is found. Shared by
// ForeignCacheArtifactPath (where the cache entry lives) and
// exportForeignArtifact (where a project-local producer build lives),
// so the two never derive the root two different ways.
func projectRootOf(abs string) string {
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

// readAndVerifyForeignArtifact is the read itself — every premise
// checked against the given cache entry.
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
	if sentence := checkArtifactEnvelope(parsed, artifactPath); sentence != "" {
		return nil, sentence
	}
	// TARGET INTEGRITY (§5): the claim holds of a run only if the code
	// that runs is the code that was checked
	if sentence := checkTargetIntegrity(parsed, targetPath, artifactPath); sentence != "" {
		return nil, sentence
	}
	// RUNTIME IDENTITY (§5): the pins commit to a band, not to "Python"
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
	calledName, sentence := harnessCalledName(parsed, artifactPath)
	if sentence != "" {
		return nil, sentence
	}
	fact, factSentence := functionFactOf(parsed, calledName, artifactPath, targetPath)
	if factSentence != "" {
		return nil, factSentence
	}
	return &ForeignArtifact{
		Path:        artifactPath,
		TargetFile:  targetPath,
		RuntimeBand: band,
		Called:      *fact,
	}, ""
}

// checkArtifactEnvelope reads the `refined` envelope: the kind this
// consumer knows and the version whose field meanings it was written
// against.
func checkArtifactEnvelope(parsed map[string]any, artifactPath string) string {
	envelope, ok := parsed["refined"].(map[string]any)
	if !ok {
		return artifactPath + ` carries no "refined" envelope, so nothing identifies it as a fact artifact`
	}
	kind, _ := envelope["kind"].(string)
	if kind != ForeignArtifactKind {
		return artifactPath + ` states the kind "` + kind + `", and this edge consumes "` +
			ForeignArtifactKind + `" — nothing else`
	}
	version, versionOk := envelope["version"].(float64)
	if !versionOk || int(version) != ForeignArtifactVersion {
		return artifactPath + " states artifact version " + jsonNumberString(version) +
			", and this edge reads version " + strconv.Itoa(ForeignArtifactVersion) +
			" — the field meanings are what the version pins"
	}
	return ""
}

// checkTargetIntegrity is CROSS-LANGUAGE-EDGE.md §5's target-integrity
// premise, discharged by hashing the file the check will run against
// and comparing it to the hash the producer recorded. The artifact's
// own `target.file` string is NOT trusted as the identity — a path can
// be stale or relative to another root; the hash is the identity.
func checkTargetIntegrity(parsed map[string]any, targetPath string, artifactPath string) string {
	stated, statedOk := nestedString(parsed, "target", "contentHash")
	if !statedOk {
		return artifactPath + " records no target contentHash, so nothing ties its claim to " +
			targetPath + " — the target-integrity premise cannot be discharged"
	}
	bytes, err := os.ReadFile(targetPath)
	if err != nil {
		return "the Python target " + targetPath + " cannot be read, so its stated fact cannot be tied to it"
	}
	sum := sha256.Sum256(bytes)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != stated {
		return artifactPath + " states the fact of a target whose contents hash to " + stated +
			", and " + targetPath + " hashes to " + actual +
			" — the exported fact is about different code than the code being checked; " +
			"re-export it with `" + ForeignExportCommand + " " + targetPath + "`"
	}
	return ""
}

// harnessCalledName reads the stdio harness: the wire is JSON in both
// directions, and one named function is what the __main__ block calls.
// The edge's whole claim is about THAT function — a target whose
// harness reads a different encoding, or calls nothing this artifact
// names, transports something the JSON model does not describe.
func harnessCalledName(parsed map[string]any, artifactPath string) (string, string) {
	harness, ok := parsed["harness"].(map[string]any)
	if !ok {
		return "", artifactPath + " describes no harness, so nothing says what the target does " +
			"with stdin and stdout — the JSON transport model has nothing to apply to"
	}
	stdin, _ := harness["stdin"].(string)
	stdout, _ := harness["stdout"].(string)
	if stdin != "json" || stdout != "json" {
		return "", artifactPath + " states a harness reading " + quotedOrNone(stdin) +
			" on stdin and writing " + quotedOrNone(stdout) +
			" on stdout, and this edge applies the JSON transport model to both legs"
	}
	called, calledOk := harness["calls"].(string)
	if !calledOk || called == "" {
		return "", artifactPath + " states no harness.calls function, so nothing names the code " +
			"that runs when this call executes"
	}
	return called, ""
}

// functionFactOf reads one named function's row: its entry positions,
// its return, and the provenance a cross-language message renders.
func functionFactOf(
	parsed map[string]any, name string, artifactPath string, targetPath string,
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
		return nil, artifactPath + " names " + name + " as the harness's called function and " +
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
		Provenance: artifactProvenanceOf(row, targetPath),
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
func artifactProvenanceOf(row map[string]any, targetPath string) ForeignProvenance {
	provenance, ok := row["provenance"].(map[string]any)
	if !ok {
		return ForeignProvenance{File: targetPath}
	}
	line, _ := provenance["line"].(float64)
	said, _ := provenance["said"].(string)
	return ForeignProvenance{File: targetPath, Line: int(line), Said: said}
}

// ProvenanceSentence renders the target's own step of the explanation:
// where the fact was said, and what was said there.
//
// TODO(§9 / R5): this is the MESSAGE-TEXT form of a two-step blame
// chain. tsc's diagnostic type already carries relatedInformation
// (internal/ast/diagnostic.go:165-166) with a working LSP renderer, and
// each link is a full diagnostic with its own file and location — but
// RefinedTS's own payload (assignability.RefinementDiagnostic) has no
// field for it and assignability.At cannot construct one, so a second
// step cannot be attached without widening that type and every reporter
// that relays it. Named work item: carry the Python step as a real
// related-information link once RefinementDiagnostic grows the field.
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

// quotedOrNone spells a harness channel for a message: the word it
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

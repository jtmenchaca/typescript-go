// The cross-language edge, at the two grains this package can pin
// without a Node type environment.
//
// WHAT IS PINNED HERE, and why it stops where it does. The artifact
// reader is pure I/O plus the kernel's own set decoder, so every one of
// its premises — envelope, target integrity, runtime band, harness,
// channel purity's carried flag — is exercised against REAL temp files
// with real sha256 sums. The recognizer's syntactic halves (the argv
// reading, the options reading, the sole-parse scan) are exercised
// against real parsed sources.
//
// What is NOT pinned here is the whole-route ForeignEdgeAt on the
// fixture's own spelling: resolvesToChildProcessMember asks the
// checker for execFileSync's declaring file, and entryEnvTestProgram
// builds a program with the default lib alone — no @types/node, so the
// name resolves to nothing and the route declines before it reaches
// disk. Standing this up needs a program host carrying a
// child_process.d.ts, which is a service-level fixture rather than a
// walk-level one; the end-to-end verification is E4's own row
// (CROSS-LANGUAGE-EDGE.md §17), run against the real tutorial fixture.
// Naming that here rather than faking the resolution is the point.

package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// foreignTargetSource is the Python body the artifacts below describe —
// its exact bytes are what the contentHash premise is checked against.
const foreignTargetSource = "import math\n\n\ndef audio_level(samples):\n" +
	"    return math.sqrt(sum(s * s for s in samples) / len(samples))\n"

// writeForeignTarget writes the .py file into a fresh temp directory and
// answers its path and the sha256 the artifact must state to match it.
func writeForeignTarget(t *testing.T) (string, string) {
	t.Helper()
	targetPath := filepath.Join(t.TempDir(), "audio_level.py")
	if err := os.WriteFile(targetPath, []byte(foreignTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignTargetSource))
	return targetPath, "sha256:" + hex.EncodeToString(sum[:])
}

// foreignArtifactJSON builds the frozen-schema artifact text: a sequence
// entry of -2 … 2 with at least one element, a 0 … 1 return, and the
// two flags the premises read.
func foreignArtifactJSON(contentHash string, targetFile string, stdoutPure bool) string {
	pure := "false"
	if stdoutPure {
		pure = "true"
	}
	return `{
  "refined": {"kind": "fact-artifact", "version": 2},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "audio_level"},
  "functions": {
    "audio_level": {
      "entry": [{"name": "samples", "sequence": {
        "element": {"forms": [{"form": "atLeast", "a": {"num": -2, "exp": 0}},
                              {"form": "atMost", "a": {"num": 2, "exp": 0}}]},
        "lengthAtLeast": 1}}],
      "return": {"set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                   {"form": "atMost", "a": {"num": 1, "exp": 0}}]},
                 "stdoutPure": ` + pure + `},
      "provenance": {"line": 5, "said": "math.sqrt of a mean of squares of -1 … 1 is 0 … 1"}
    }
  }
}`
}

// foreignScalarArgvTargetSource is the Python body the argv-scalar
// artifacts below describe — level_scalar_argv.py's own anatomy, kept
// tiny: one float parameter crossing on argv[1] alone, no stdin at all.
const foreignScalarArgvTargetSource = "def level_from_gain(gain):\n" +
	"    return min(1.0, gain / 4.0)\n"

// writeForeignScalarArgvTarget writes the argv-scalar target into a
// fresh temp directory and answers its path and the sha256 the
// artifact must state to match it.
func writeForeignScalarArgvTarget(t *testing.T) (string, string) {
	t.Helper()
	targetPath := filepath.Join(t.TempDir(), "level_scalar_argv.py")
	if err := os.WriteFile(targetPath, []byte(foreignScalarArgvTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignScalarArgvTargetSource))
	return targetPath, "sha256:" + hex.EncodeToString(sum[:])
}

// foreignArgvScalarArtifactJSON builds an artifact stating the
// argv-scalar surface: one scalar entry 0 … 4 (level_scalar_argv.py's
// own Gain window), argIndex 1, calling level_from_gain.
func foreignArgvScalarArtifactJSON(contentHash string, targetFile string) string {
	return `{
  "refined": {"kind": "fact-artifact", "version": 2},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "argv-scalar", "argIndex": 1, "parse": "float", "stdout": "json", "calls": "level_from_gain"},
  "functions": {
    "level_from_gain": {
      "entry": [{"name": "gain", "set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                                    {"form": "atMost", "a": {"num": 4, "exp": 0}}]}}],
      "return": {"set": {"forms": [{"form": "atLeast", "a": {"num": 0, "exp": 0}},
                                   {"form": "atMost", "a": {"num": 1, "exp": 0}}]},
                 "stdoutPure": true},
      "provenance": {"line": 2, "said": "min(1.0, gain / 4.0) is 0 … 1 for gain 0 … 4"}
    }
  }
}`
}

// writeForeignArtifact drops an artifact text at the target's cache
// entry and clears the read memo, so each case reads its own file
// rather than a previous case's answer. Every test also pins the
// producer resolution to a dead path, so a developer's PATH (or a
// project-root build) cannot turn a missing-artifact case into a live
// export.
func writeForeignArtifact(t *testing.T, targetPath string, text string) {
	t.Helper()
	SetPythonProducerPath("/nonexistent/refinedpy-check")
	t.Cleanup(func() { SetPythonProducerPath("") })
	artifactPath := ForeignCacheArtifactPath(targetPath)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("creating the cache directory: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte(text), 0o644); err != nil {
		t.Fatalf("writing the artifact: %v", err)
	}
	forgetForeignArtifact(targetPath)
}

// forgetForeignArtifact drops the memoized row for one target — the
// memo is held for the process, and a test that rewrites a file must
// not read the previous file's verdict.
func forgetForeignArtifact(targetPath string) {
	foreignArtifactsMu.Lock()
	delete(foreignArtifacts, targetPath)
	foreignArtifactsMu.Unlock()
}

func TestReadForeignArtifact_AWellFormedArtifactAnswersTheCalledFunctionsFact(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))

	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("a well-formed artifact declined: %s", sentence)
	}
	if artifact.Called.Name != "audio_level" {
		t.Errorf("Called.Name = %q, want audio_level — the harness names it", artifact.Called.Name)
	}
	if artifact.RuntimeBand != ForeignRuntimeBand {
		t.Errorf("RuntimeBand = %q, want %q", artifact.RuntimeBand, ForeignRuntimeBand)
	}
	if len(artifact.Called.Entry) != 1 {
		t.Fatalf("len(Entry) = %d, want 1: %+v", len(artifact.Called.Entry), artifact.Called.Entry)
	}
	entry := artifact.Called.Entry[0]
	if !entry.IsSequence || entry.LengthAtLeast != 1 {
		t.Errorf("Entry[0] = %+v, want a sequence with lengthAtLeast 1", entry)
	}
	// the element set came through the kernel's own decoder, so it is the
	// same object a kernel answer would have been
	if words := foreignSetWords(entry.Element); !strings.Contains(words, "-2") || !strings.Contains(words, "2") {
		t.Errorf("the entry element reads %q, want the -2 … 2 window the artifact states", words)
	}
	if !artifact.Called.Return.StdoutPure {
		t.Errorf("Return.StdoutPure = false, and the artifact states true")
	}
	if got := artifact.Called.Provenance.ProvenanceSentence(); !strings.Contains(got, ":5") {
		t.Errorf("ProvenanceSentence() = %q, want the target's line 5 in it", got)
	}
}

// TestReadForeignArtifact_ProvenanceSpanPointsAtTheStatedLinesOwnStart pins
// the offset computation itself, against foreignTargetSource's real 5
// lines and the fixture artifact's "line": 5 provenance: the span must
// start exactly where line 5 starts (column 1) and run to line 5's own
// length, with no trailing newline folded in, so foreign_edge.go's
// StepInForeignFile(File, Text, Start, Length, Said) call lands on the
// right characters without re-reading the file itself.
func TestReadForeignArtifact_ProvenanceSpanPointsAtTheStatedLinesOwnStart(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))

	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("a well-formed artifact declined: %s", sentence)
	}

	provenance := artifact.Called.Provenance
	if provenance.Text != foreignTargetSource {
		t.Fatalf("Provenance.Text = %q, want the target's own bytes (no re-read, no rewrite)", provenance.Text)
	}

	const line5 = "    return math.sqrt(sum(s * s for s in samples) / len(samples))"
	wantStart := strings.Index(foreignTargetSource, line5)
	if wantStart < 0 {
		t.Fatalf("test fixture drift: foreignTargetSource no longer contains %q", line5)
	}
	if provenance.Start != wantStart {
		t.Errorf("Provenance.Start = %d, want %d — line 5's own byte offset", provenance.Start, wantStart)
	}
	if provenance.Length != len(line5) {
		t.Errorf("Provenance.Length = %d, want %d — line 5's length with no trailing newline", provenance.Length, len(line5))
	}
	// the span must land EXACTLY on line 5's text, neither short nor
	// spilling into the newline or the next line (there is none here,
	// but a spill would still be a wrong length)
	if got := foreignTargetSource[provenance.Start : provenance.Start+provenance.Length]; got != line5 {
		t.Errorf("the computed span reads %q, want line 5 verbatim: %q", got, line5)
	}
}

func TestReadForeignArtifact_AMissingArtifactNamesTheFileAndTheCommandThatWritesIt(t *testing.T) {
	targetPath, _ := writeForeignTarget(t)
	SetPythonProducerPath("/nonexistent/refinedpy-check")
	t.Cleanup(func() { SetPythonProducerPath("") })
	forgetForeignArtifact(targetPath)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a missing artifact answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, ForeignCacheArtifactPath(targetPath)) {
		t.Errorf("the sentence %q does not name the cache entry that is missing", sentence)
	}
	if !strings.Contains(sentence, ForeignExportCommand) {
		t.Errorf("the sentence %q does not name the command that writes it — a missing fact is a work-queue item, not a silence", sentence)
	}
}

func TestReadForeignArtifact_AStaleContentHashDeclinesNamingTargetIntegrity(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	// the target changes AFTER the fact was exported: the claim is now
	// about code that is not the code being checked (§5)
	if err := os.WriteFile(targetPath, []byte(foreignTargetSource+"\n# edited\n"), 0o644); err != nil {
		t.Fatalf("rewriting the target: %v", err)
	}
	forgetForeignArtifact(targetPath)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a stale hash answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "different code than the code being checked") {
		t.Errorf("the sentence %q does not name the target-integrity premise", sentence)
	}
}

func TestReadForeignArtifact_AnotherRuntimeBandDeclinesNamingIt(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"band": "cpython-3.11+"`, `"band": "pypy-3.10"`, 1)
	writeForeignArtifact(t, targetPath, text)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a foreign runtime band answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "pypy-3.10") || !strings.Contains(sentence, ForeignRuntimeBand) {
		t.Errorf("the sentence %q does not name both bands — the premise is which semantics the pins transcribe", sentence)
	}
}

func TestReadForeignArtifact_AHarnessCallingAnUnstatedFunctionDeclinesNamingIt(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"calls": "audio_level"`, `"calls": "audio_level_unclamped"`, 1)
	writeForeignArtifact(t, targetPath, text)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a harness naming an unstated function answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "audio_level_unclamped") {
		t.Errorf("the sentence %q does not name the function whose fact is missing", sentence)
	}
}

func TestReadForeignArtifact_ANonJsonHarnessDeclinesNamingTheChannel(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"stdout": "json"`, `"stdout": "lines"`, 1)
	writeForeignArtifact(t, targetPath, text)

	if _, sentence := ReadForeignArtifact(targetPath); !strings.Contains(sentence, "JSON transport model") {
		t.Errorf("the sentence %q does not say the transport model has nothing to apply to", sentence)
	}
}

func TestReadForeignArtifact_AnUnknownVersionDeclinesRatherThanReadingItAnyway(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`"version": 2`, `"version": 3`, 1)
	writeForeignArtifact(t, targetPath, text)

	if _, sentence := ReadForeignArtifact(targetPath); !strings.Contains(sentence, "version") {
		t.Errorf("the sentence %q does not name the version — the field meanings are what the version pins", sentence)
	}
}

func TestReadForeignArtifact_AnUnreadableSetDeclinesRatherThanCrashingTheChecker(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	// DecodeWireSet panics on a form it does not know; an artifact is a
	// file another program wrote, so the panic must become a decline
	text := strings.Replace(
		foreignArtifactJSON(contentHash, targetPath, true),
		`{"form": "atLeast", "a": {"num": -2, "exp": 0}}`,
		`{"form": "someFormFromTheFuture"}`, 1)
	writeForeignArtifact(t, targetPath, text)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("an undecodable set answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "kernel grammar") {
		t.Errorf("the sentence %q does not say the set could not be decoded", sentence)
	}
}

/* ── the outbound leg, against a real kernel ─────────────────────── */

// foreignCrossingEnv seeds the value that crosses out under one name,
// as a repetition of a bounded element — the shape `boosted` wears in
// the tutorial fixture after the map derivation.
func foreignCrossingEnv(name string, lo float64, hi float64, atLeast int) Env {
	env := NewEnv()
	element := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
	env.Set(name, abstractdomain.KnownSet(
		refinementsets.Repetition(element, atLeast, nil), nil,
		abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	return env
}

// foreignEdgeFixture is the whole outbound-leg setup: an artifact read
// off disk, a context carrying a real kernel and a collecting sink, an
// env holding the crossing value, and the node it is judged at.
type foreignEdgeFixture struct {
	artifact *ForeignArtifact
	ctx      *FlowContext
	env      Env
	edge     *ForeignEdge
	reported *[]assignability.RefinementDiagnostic
}

func foreignOutboundFixture(t *testing.T, lo float64, hi float64, atLeast int) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(boosted: number[]) { boosted; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	payload := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].
		AsExpressionStatement().Expression
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      foreignCrossingEnv("boosted", lo, hi, atLeast),
		edge:     &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"},
		reported: &reported,
	}
}

func TestCheckOutboundLeg_AValueInsideTheStatedEntryPassesEveryPremise(t *testing.T) {
	// -2 … 2, at least one element: exactly what the artifact admits
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckOutboundLeg_ElementsOutsideTheStatedEntryFireAtTheCall(t *testing.T) {
	// -4 … 4 is not inside the target's -2 … 2: the value can escape what
	// the target states it accepts, which is a defect in THIS program
	fixture := foreignOutboundFixture(t, -4, 4, 1)
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("a crossing outside the stated entry passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("a crossing outside the stated entry DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(*fixture.reported), *fixture.reported)
	}
	fired := (*fixture.reported)[0]
	if fired.Code != 7001 {
		t.Errorf("Code = %d, want 7001 — the target admits -2 … 2 and the value reaches ±4", fired.Code)
	}
	if !strings.Contains(fired.MessageText, "audio_level") {
		t.Errorf("the message %q does not name the target function", fired.MessageText)
	}
	// the provenance no longer rides the message text — it is the second
	// step of the two-language explanation, a related step in the Python
	// file itself
	if strings.Contains(fired.MessageText, "said:") {
		t.Errorf("the message %q still concatenates the flat provenance sentence", fired.MessageText)
	}
	if len(fired.Steps) != 1 {
		t.Fatalf("want exactly 1 related step (the target's provenance line), got %d: %+v", len(fired.Steps), fired.Steps)
	}
	step := fired.Steps[0]
	if step.ForeignFile != fixture.artifact.TargetFile {
		t.Errorf("the step's ForeignFile = %q, want the target path %q", step.ForeignFile, fixture.artifact.TargetFile)
	}
	if step.Sentence != "math.sqrt of a mean of squares of -1 … 1 is 0 … 1" {
		t.Errorf("the step's Sentence = %q, want the artifact's own provenance.said", step.Sentence)
	}
	// line 5 of foreignTargetSource is "    return math.sqrt(...)" — the
	// step's span must start there, not merely name the file
	wantStart := strings.Index(foreignTargetSource, "    return math.sqrt")
	if step.Start != wantStart {
		t.Errorf("the step's Start = %d, want %d (the start of line 5)", step.Start, wantStart)
	}
	wantLength := len("    return math.sqrt(sum(s * s for s in samples) / len(samples))")
	if step.Length != wantLength {
		t.Errorf("the step's Length = %d, want %d (line 5's own length, no trailing newline)", step.Length, wantLength)
	}
}

func TestCheckOutboundLeg_AShorterSequenceThanTheTargetReliesOnFires(t *testing.T) {
	// elements fit, but the target's body divides by len and the artifact
	// states lengthAtLeast 1 — a possibly-empty sequence is a different
	// program on the other side
	fixture := foreignOutboundFixture(t, -2, 2, 0)
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a too-short crossing did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for the length floor: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "at least 1") {
		t.Errorf("the message %q does not carry the target's stated length floor", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckOutboundLeg_APossiblyNaNCrossingFiresNamingTheStringifyBehaviour(t *testing.T) {
	fixture := foreignOutboundFixture(t, -2, 2, 1)
	// NaN stringifies to null (§4), so the target would receive a value
	// this program never computed
	fixture.env.Set("boosted", abstractdomain.PossiblyNaN(abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(-2), refinementsets.AtMost(2)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)))

	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a possibly-NaN crossing did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for NaN-freedom: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "null") {
		t.Errorf("the message %q does not say what stringify does to NaN", (*fixture.reported)[0].MessageText)
	}
}

/* ── the argv-value leg, against a real kernel ───────────────────── */

// foreignArgvOutboundFixture is the argv-crossing counterpart of
// foreignOutboundFixture: an argv-scalar artifact read off disk, a
// context carrying a real kernel and a collecting sink, and an edge
// whose ArgvValue is the parsed source's own `const gain = <literalText>;`
// initializer node — the same shape d-data-legs.ts's row spells.
func foreignArgvOutboundFixture(t *testing.T, literalText string) foreignEdgeFixture {
	t.Helper()
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f() { const gain = `+strconv.Quote(literalText)+`; gain; }`+"\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	gainDeclaration := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	return foreignEdgeFixture{
		artifact: artifact,
		ctx:      ctx,
		env:      NewEnv(),
		edge:     &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"},
		reported: &reported,
	}
}

func TestCheckArgvCrossing_ALiteralInsideTheStatedEntryIsSilent(t *testing.T) {
	// 0.5 is inside the target's stated 0 … 4 gain window
	fixture := foreignArgvOutboundFixture(t, "0.5")
	if outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact); outcome != nil {
		t.Fatalf("a fitting argv crossing did not pass: %+v", outcome)
	}
	if len(*fixture.reported) != 0 {
		t.Errorf("a fitting argv crossing reported %d diagnostics: %+v", len(*fixture.reported), *fixture.reported)
	}
}

func TestCheckArgvCrossing_ALiteralOutsideTheStatedEntryFiresWithThePinnedSentence(t *testing.T) {
	// 9 is outside the target's stated 0 … 4 gain window
	fixture := foreignArgvOutboundFixture(t, "9")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil {
		t.Fatalf("an out-of-set argv literal passed")
	}
	if outcome.Decline != "" {
		t.Fatalf("an out-of-set argv literal DECLINED (%q); a fit failure is a refutation", outcome.Decline)
	}
	if len(*fixture.reported) != 1 {
		t.Fatalf("reported %d diagnostics, want exactly 1: %+v", len(*fixture.reported), *fixture.reported)
	}
	fired := (*fixture.reported)[0]
	if fired.Code != 7001 {
		t.Errorf("Code = %d, want 7001", fired.Code)
	}
	wantSentence := "the argv value crossing to level_from_gain is 9, and the target admits " +
		"0 … 4 — the value can escape what the target states it accepts"
	if !strings.Contains(fired.MessageText, "the value can escape what the target states it accepts") {
		t.Errorf("the message %q does not carry the fit-failure sentence, want it to contain %q", fired.MessageText, wantSentence)
	}
	if !strings.Contains(fired.MessageText, "level_from_gain") {
		t.Errorf("the message %q does not name the target function", fired.MessageText)
	}
}

func TestCheckArgvCrossing_ANonLiteralArgvValueIsRecognizedUndetermined(t *testing.T) {
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(gain: string) { gain; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	argvValue := statements[0].AsExpressionStatement().Expression
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("a non-literal argv value passed with no outcome at all")
	}
	if outcome.Decline != "the argv value is not a written string literal; its parsed value cannot be pinned" {
		t.Errorf("Decline = %q, want the recognized-undetermined sentence naming the construct", outcome.Decline)
	}
}

func TestCheckArgvCrossing_ANaNLiteralIsRefused(t *testing.T) {
	fixture := foreignArgvOutboundFixture(t, "nan")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("a NaN argv literal did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for NaN-freedom: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "NaN") {
		t.Errorf("the message %q does not name NaN", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckArgvCrossing_AParseFailureFiresNamingTheParseFailure(t *testing.T) {
	fixture := foreignArgvOutboundFixture(t, "--fast")
	outcome := checkOutboundLeg(fixture.ctx, fixture.env, fixture.edge, fixture.artifact)
	if outcome == nil || outcome.Decline != "" {
		t.Fatalf("an unparseable argv literal did not fire: %+v", outcome)
	}
	if len(*fixture.reported) != 1 || (*fixture.reported)[0].Code != 7001 {
		t.Fatalf("want exactly one 7001 for the parse failure: %+v", *fixture.reported)
	}
	if !strings.Contains((*fixture.reported)[0].MessageText, "does not parse as a Python float()") {
		t.Errorf("the message %q does not name the parse failure", (*fixture.reported)[0].MessageText)
	}
}

func TestCheckArgvCrossing_AnArgvCallAtAStdinJsonTargetDeclinesWithTheChannelMismatchSentence(t *testing.T) {
	targetPath, contentHash := writeForeignTarget(t)
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, `function f() { const gain = "0.5"; gain; }`+"\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	gainDeclaration := statements[0].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration()
	argvValue := gainDeclaration.Initializer
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	edge := &ForeignEdge{Call: argvValue, TargetPath: targetPath, ArgvValue: argvValue, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, NewEnv(), edge, artifact)
	if outcome == nil {
		t.Fatalf("an argv call at a stdin-json target passed with no outcome at all")
	}
	want := "the call passes the value as argv[1], but the target's fact serves JSON on stdin — " +
		"the channels do not meet"
	if outcome.Decline != want {
		t.Errorf("Decline = %q, want %q", outcome.Decline, want)
	}
}

func TestCheckOutboundLeg_AStdinCallAtAnArgvScalarTargetDeclinesWithTheMirroredChannelMismatchSentence(t *testing.T) {
	targetPath, contentHash := writeForeignScalarArgvTarget(t)
	writeForeignArtifact(t, targetPath, foreignArgvScalarArtifactJSON(contentHash, targetPath))
	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the fixture artifact declined: %s", sentence)
	}
	p := entryEnvTestProgram(t, "function f(gain: number) { gain; }\n")
	fn := entryEnvFunctionNamed(t, p, "f")
	payload := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].
		AsExpressionStatement().Expression
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
	env := NewEnv()
	env.Set("gain", abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	// Payload set, ArgvValue nil: the ordinary stdin shape, at a target
	// whose surface is argv-scalar
	edge := &ForeignEdge{Call: payload, TargetPath: targetPath, Payload: payload, StdoutName: "stdout"}
	outcome := checkOutboundLeg(ctx, env, edge, artifact)
	if outcome == nil {
		t.Fatalf("a stdin call at an argv-scalar target passed with no outcome at all")
	}
	want := "the call passes the value as JSON on stdin, but the target's fact serves an argv[1] " +
		"scalar — the channels do not meet"
	if outcome.Decline != want {
		t.Errorf("Decline = %q, want %q", outcome.Decline, want)
	}
}

/* ── recognition, syntax halves ──────────────────────────────────── */

func TestForeignEdgeRecognition_ReadsTheArgvAndOptionsTheFixtureSpells(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound execFileSync call was not read")
	}
	if name != "stdout" {
		t.Errorf("bound name = %q, want stdout", name)
	}
	args, _ := callArguments(call)
	if len(args) != 3 {
		t.Fatalf("len(args) = %d, want 3", len(args))
	}
	if interpreter, ok := stringLiteralText(args[0]); !ok || interpreter != "python3" {
		t.Errorf("argv[0] = %q (ok=%v), want python3", interpreter, ok)
	}
	runnerWord, script, _, scriptOk, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("the runner/script read declined: %s", sentence)
	}
	if !scriptOk || script != "./audio_level.py" {
		t.Errorf("argv[1] = %q (ok=%v), want ./audio_level.py", script, scriptOk)
	}
	if runnerWord != "python3" {
		t.Errorf("runnerWord = %q, want python3", runnerWord)
	}
	payload, encodingOk, sentence := execFileSyncOptionsOf(args[2])
	if sentence != "" {
		t.Fatalf("the options declined: %s", sentence)
	}
	if !encodingOk {
		t.Errorf(`encoding "utf8" was not read as making stdout a string`)
	}
	if payload == nil || !ast.IsIdentifier(payload) || payload.Text() != "boosted" {
		t.Errorf("the stringified payload is %v, want the identifier boosted", payload)
	}
	// the return leg's node: the sole JSON.parse of the bound name
	parse, at, parseSentence := soleParseConsumerOf(statements, 0, "stdout")
	if parseSentence != "" {
		t.Fatalf("the sole-parse scan declined: %s", parseSentence)
	}
	if at != 1 {
		t.Errorf("the parse sits in statement %d, want 1 — the override scopes to that one statement", at)
	}
	if !isForeignParseOf(parse, "stdout") {
		t.Errorf("the found node is not JSON.parse(stdout): %v", parse)
	}
}

// TestForeignEdgeRecognition_ATwoElementArgvNamesTheScriptAndTheDataElement
// pins the argv-value leg's own recognition: a plain interpreter's argv
// carrying [<script>, <data>] now reads as the script PLUS a data
// element, not "more arguments than this edge models" — d-data-legs.ts's
// numericValueAsArgvUndetermined row moving off the old refusal.
func TestForeignEdgeRecognition_ATwoElementArgvNamesTheScriptAndTheDataElement(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const gain = "0.5";
	const stdout = execFileSync("python3", ["./targets/level_scalar_argv.py", gain], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	runnerWord, script, dataElement, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("a two-element python argv declined: %s", sentence)
	}
	if !ok || runnerWord != "python3" || script != "./targets/level_scalar_argv.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want python3 / ./targets/level_scalar_argv.py / true", runnerWord, script, ok)
	}
	if dataElement == nil || !ast.IsIdentifier(dataElement) || dataElement.Text() != "gain" {
		t.Fatalf("dataElement = %v, want the gain identifier node", dataElement)
	}
}

// TestForeignEdgeRecognition_AnArgvWithArgumentsBeyondTheScriptAndDataElementDeclines
// pins the remaining boundary: THREE or more argv elements for a plain
// interpreter still name a shape this edge does not model (neither a
// bare script nor a script-plus-one-data-element).
func TestForeignEdgeRecognition_AnArgvWithArgumentsBeyondTheScriptAndDataElementDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("python3", ["./audio_level.py", "--fast", "--verbose"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	// the target takes arguments this edge models nothing about
	if _, _, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1]); ok || sentence != "" {
		t.Errorf("a three-element argv was read as a modeled shape (ok=%v, sentence=%q)", ok, sentence)
	}
}

func TestForeignEdgeRecognition_AMissingEncodingIsReadAsABufferResult(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], { input: JSON.stringify(boosted) });
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	_, encodingOk, sentence := execFileSyncOptionsOf(args[2])
	if sentence != "" {
		t.Fatalf("the options declined for the wrong reason: %s", sentence)
	}
	if encodingOk {
		t.Errorf("a missing encoding was read as making stdout a string; without one the sync exec answers a Buffer")
	}
}

/* ── runner + script argv: the b-runners.ts / c-reference-shapes.ts legs ── */

func TestRunnerAndScriptArgvOf_BarePythonIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("python", ["./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("bare python declined: %s", sentence)
	}
	if !ok || runnerWord != "python" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want python / ./targets/level_ok.py / true", runnerWord, script, ok)
	}
}

// TestRunnerAndScriptArgvOf_UvRunWithInterpreterIsRecognized pins
// b-runners.ts's uv-run row: `execFileSync("uv", ["run", "python3",
// "./targets/level_ok.py"], ...)` — a 3-element argv[1], not a
// runner-plus-one-script shape at all, read as "uv run" with the last
// element as the script.
func TestRunnerAndScriptArgvOf_UvRunWithInterpreterIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("uv", ["run", "python3", "./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("uv run python3 <script> declined: %s", sentence)
	}
	if !ok || runnerWord != "uv run" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want %q / ./targets/level_ok.py / true", runnerWord, script, ok, "uv run")
	}
}

// TestRunnerAndScriptArgvOf_UvRunWithoutAnInterpreterIsRecognized pins
// the two-element uv shape the brief also names: `uv run <script>` with
// no interpreter word in between.
func TestRunnerAndScriptArgvOf_UvRunWithoutAnInterpreterIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("uv", ["run", "./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if sentence != "" {
		t.Fatalf("uv run <script> declined: %s", sentence)
	}
	if !ok || runnerWord != "uv run" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want %q / ./targets/level_ok.py / true", runnerWord, script, ok, "uv run")
	}
}

// TestRunnerAndScriptArgvOf_UvWithAnUnrecognizedMiddleInterpreterIsNotThisEdge
// pins that a THIRD uv element that is not a recognized python spelling
// is a different program, not a decline (uv can run non-python targets
// too, and this reader only ever claims python's own runner words).
func TestRunnerAndScriptArgvOf_UvWithAnUnrecognizedMiddleInterpreterIsNotThisEdge(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("uv", ["run", "node", "./targets/level_ok.js"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	_, _, _, ok, sentence, _ := runnerAndScriptArgvOf(nil, args[0], args[1])
	if ok || sentence != "" {
		t.Errorf("uv run node <script> was read as a python edge (ok=%v, sentence=%q)", ok, sentence)
	}
}

// TestScriptElementOf_AConstBoundIdentifierResolvesToItsLiteralInitializer
// pins c-reference-shapes.ts's pathInConstUndetermined row moving to
// RECOGNIZED: a `const scriptPath = "./targets/level_ok.py"` one
// statement above the call, spread by name into argv, now resolves
// through the same const-follow relational_accumulation.go's
// accumulationLengthNodeOf already performs for a `.length` read.
func TestScriptElementOf_AConstBoundIdentifierResolvesToItsLiteralInitializer(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const scriptPath = "./targets/level_ok.py";
	const stdout = execFileSync("python3", [scriptPath], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	runnerWord, script, _, ok, sentence, _ := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if sentence != "" {
		t.Fatalf("a const-resolved script path declined: %s", sentence)
	}
	if !ok || runnerWord != "python3" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q ok=%v, want python3 / ./targets/level_ok.py / true", runnerWord, script, ok)
	}
}

// TestScriptElementOf_AConcatenatedPathOwesTheLawTwoSentence pins
// c-reference-shapes.ts's pathByConcatenationUndetermined row moving
// from silence to a recognized-undetermined law-2 sentence: the argv
// element is a BinaryExpression, neither a literal nor an identifier,
// so scriptElementOf cannot resolve it and must not stay silent.
func TestScriptElementOf_AConcatenatedPathOwesTheLawTwoSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const directory = "./targets/";
	const stdout = execFileSync("python3", [directory + "level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	_, _, _, ok, sentence, sentenceNode := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if ok {
		t.Fatalf("a concatenated path was read as a resolved script")
	}
	if sentence != scriptPathLawTwoSentence {
		t.Errorf("sentence = %q, want %q", sentence, scriptPathLawTwoSentence)
	}
	if sentenceNode == nil {
		t.Errorf("the law-2 sentence carries no node to point at")
	}
}

// TestScriptElementOf_AParameterHeldPathOwesTheLawTwoSentence pins
// c-reference-shapes.ts's pathFromParameterUndetermined row: the argv
// element is an identifier, but its ValueDeclaration is a
// ParameterDeclaration, not a const VariableDeclaration — resolution
// fails and the same law-2 sentence is owed, not silence.
func TestScriptElementOf_AParameterHeldPathOwesTheLawTwoSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(scriptPath: string) {
	const stdout = execFileSync("python3", [scriptPath], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[0])
	args, _ := callArguments(call)
	_, _, _, ok, sentence, sentenceNode := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if ok {
		t.Fatalf("a parameter-held path was read as a resolved script")
	}
	if sentence != scriptPathLawTwoSentence {
		t.Errorf("sentence = %q, want %q", sentence, scriptPathLawTwoSentence)
	}
	if sentenceNode == nil {
		t.Errorf("the law-2 sentence carries no node to point at")
	}
}

// TestScriptElementOf_ALetBoundIdentifierDoesNotResolve pins the const-
// only gate itself: a `let` the following statements could rewrite
// carries no fixed value, so the follow must not treat it as resolved.
func TestScriptElementOf_ALetBoundIdentifierDoesNotResolve(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	let scriptPath = "./targets/level_ok.py";
	const stdout = execFileSync("python3", [scriptPath], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	_, _, _, ok, sentence, _ := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if ok {
		t.Fatalf("a let-bound path was read as a resolved script")
	}
	if sentence != scriptPathLawTwoSentence {
		t.Errorf("sentence = %q, want %q", sentence, scriptPathLawTwoSentence)
	}
}

/* ── spawnSync: same argv/options shape, a result OBJECT ─────────────── */

func TestSpawnSyncEdgeOf_ReadsTheSameArgvAndOptionsShapeAsExecFileSync(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function spawnSync(file: string, args: string[], options: unknown): { stdout: string };
function f(boosted: number[]) {
	const result = spawnSync("python3", ["./targets/level_ok.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return JSON.parse(result.stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound spawnSync call was not read")
	}
	edge, recognized, sentence, _, path := spawnSyncEdgeOf(nil, call, name)
	if sentence != "" {
		t.Fatalf("spawnSync declined: %s", sentence)
	}
	if !recognized || edge == nil {
		t.Fatalf("spawnSync was not recognized")
	}
	if edge.StdoutName != "result" {
		t.Errorf("StdoutName = %q, want result", edge.StdoutName)
	}
	if !strings.HasSuffix(path, filepath.Join("targets", "level_ok.py")) {
		t.Errorf("TargetPath = %q, want it to end in targets/level_ok.py", path)
	}
	// the return leg reads result.stdout, not a bare identifier
	parse, at, parseSentence := soleParseConsumerOf(statements, 0, edge.StdoutName)
	if parseSentence != "" {
		t.Fatalf("the sole-parse scan declined: %s", parseSentence)
	}
	if at != 1 {
		t.Errorf("the parse sits in statement %d, want 1", at)
	}
	if !isForeignParseOf(parse, "result") {
		t.Errorf("the found node is not JSON.parse(result.stdout): %v", parse)
	}
}

func TestIsForeignParseOf_ReadsBothTheBareNameAndTheDotStdoutShape(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(result: { stdout: string }, stdout: string) {
	JSON.parse(result.stdout);
	JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	var resultDotStdout, bareStdout *ast.Node
	countResult, countBare := 0, 0
	foreignParseCallsIn(statements[0], "result", &resultDotStdout, &countResult)
	foreignParseCallsIn(statements[1], "stdout", &bareStdout, &countBare)
	if countResult != 1 || resultDotStdout == nil {
		t.Fatalf("JSON.parse(result.stdout) was not read as result's parse: count=%d", countResult)
	}
	if countBare != 1 || bareStdout == nil {
		t.Fatalf("JSON.parse(stdout) was not read as stdout's parse: count=%d", countBare)
	}
}

/* ── execSync: a shell command STRING, not an argv array ─────────────── */

// TestExecSyncEdgeOf_ALiteralSimpleCommandIsFollowedAndJudged pins the
// one execSync shape this reader follows: a written string literal
// tokenizing on single spaces into exactly a runner word and a `.py`
// path, neither token carrying any shell syntax.
func TestExecSyncEdgeOf_ALiteralSimpleCommandIsFollowedAndJudged(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f() {
	const stdout = execSync("python3 ./targets/level_ok.py", { encoding: "utf8" });
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound execSync call was not read")
	}
	edge, recognized, sentence, _, path := execSyncEdgeOf(call, name)
	if sentence != "" {
		t.Fatalf("a literal simple command declined: %s", sentence)
	}
	if !recognized || edge == nil {
		t.Fatalf("a literal simple execSync command was not recognized")
	}
	if edge.StdoutName != "stdout" {
		t.Errorf("StdoutName = %q, want stdout — execSync's bound name IS the stdout string", edge.StdoutName)
	}
	if !strings.HasSuffix(path, filepath.Join("targets", "level_ok.py")) {
		t.Errorf("TargetPath = %q, want it to end in targets/level_ok.py", path)
	}
	// the return leg reads the bound name directly, exactly like execFileSync's
	parse, at, parseSentence := soleParseConsumerOf(statements, 0, edge.StdoutName)
	if parseSentence != "" {
		t.Fatalf("the sole-parse scan declined: %s", parseSentence)
	}
	if at != 1 {
		t.Errorf("the parse sits in statement %d, want 1", at)
	}
	if !isForeignParseOf(parse, "stdout") {
		t.Errorf("the found node is not JSON.parse(stdout): %v", parse)
	}
}

func TestExecSyncEdgeOf_ATemplateWithASubstitutionOwesTheShellStringSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f(samples: number[]) {
	const stdout = execSync(
		` + "`python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`" + `,
		{ encoding: "utf8" },
	);
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, _ := constBoundCallOf(statements[0])
	edge, recognized, sentence, sentenceNode, _ := execSyncEdgeOf(call, name)
	if recognized || edge != nil {
		t.Fatalf("a template with a substitution was read as a followed command")
	}
	if sentence != execSyncShellStringSentence {
		t.Errorf("sentence = %q, want %q", sentence, execSyncShellStringSentence)
	}
	if sentenceNode == nil {
		t.Errorf("the law-2 sentence carries no node to point at")
	}
}

func TestExecSyncEdgeOf_APipeInTheCommandOwesTheShellStringSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f() {
	const stdout = execSync("python3 ./targets/level_ok.py | cat", { encoding: "utf8" });
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, _ := constBoundCallOf(statements[0])
	_, recognized, sentence, _, _ := execSyncEdgeOf(call, name)
	if recognized {
		t.Fatalf("a piped command was read as a followed command")
	}
	if sentence != execSyncShellStringSentence {
		t.Errorf("sentence = %q, want %q", sentence, execSyncShellStringSentence)
	}
}

func TestExecSyncSimpleCommandTokens_SplitsOnSingleSpacesAndRejectsShellSyntax(t *testing.T) {
	if _, _, ok := execSyncSimpleCommandTokens("python3 ./targets/level_ok.py"); !ok {
		t.Errorf("a plain two-word command was not read")
	}
	if _, _, ok := execSyncSimpleCommandTokens("python3 ./a.py ./b.py"); ok {
		t.Errorf("a three-token command was read as a two-word one")
	}
	for _, unsupported := range []string{
		"python3 './targets/level_ok.py'",
		"python3 $HOME/level_ok.py",
		"python3 ./a.py > out.txt",
		"python3 ./a.py < in.json",
		"python3 ./a.py & echo done",
		"python3 ./a.py; echo done",
		"python3 `./a.py`",
	} {
		if _, _, ok := execSyncSimpleCommandTokens(unsupported); ok {
			t.Errorf("%q was read as a plain simple command", unsupported)
		}
	}
}

/* ── spawn (async): recognized, and its own "does not determine (yet)" ── */

// TestSpawnAsyncEdgeOf_TheArgvIsRecognizedAndTheAsyncResultOwesItsOwnSentence
// pins a-invocation-functions.ts's spawnUndetermined row: the call
// itself is recognized (the argv names the script exactly as
// execFileSync's does), and the sentence names the missing reader —
// "does not determine (yet)" — never "cannot".
func TestSpawnAsyncEdgeOf_TheArgvIsRecognizedAndTheAsyncResultOwesItsOwnSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function spawn(file: string, args: string[]): unknown;
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	return child;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, ok := constBoundCallOf(statements[0])
	if !ok {
		t.Fatalf("the const-bound spawn call was not read")
	}
	edge, recognized, sentence, sentenceNode, path := spawnAsyncEdgeOf(nil, call, name, statements, 0)
	if edge != nil {
		t.Fatalf("spawn (async) answered a served edge: %+v", edge)
	}
	if recognized {
		t.Fatalf("spawn (async) answered recognized=true with no edge")
	}
	if !strings.Contains(sentence, "does not determine (yet)") {
		t.Errorf("sentence %q does not use the does-not-determine-yet wording", sentence)
	}
	if strings.Contains(sentence, "cannot") {
		t.Errorf("sentence %q says \"cannot\" rather than naming what would resolve it", sentence)
	}
	if sentenceNode == nil {
		t.Errorf("the sentence carries no node to point at")
	}
	if !strings.HasSuffix(path, filepath.Join("targets", "level_ok.py")) {
		t.Errorf("TargetPath = %q, want it to end in targets/level_ok.py — the reference is still read", path)
	}
}

/* ── spawnReturnLegOf: the accumulate-then-parse `.on()` pair reader ── */

// spawnPairSource is the declared-shape scaffold every spawnReturnLegOf
// case parses against — a minimal EventEmitter-shaped spawn result, so
// the fixture's own accumulate-then-parse body type-checks without a
// real @types/node.
const spawnPairSource = `
declare function spawn(file: string, args: string[]): {
	stdin: { write(chunk: string): void; end(): void };
	stdout: { on(event: string, cb: (chunk: string) => void): void };
	on(event: string, cb: () => void): void;
};
`

// TestSpawnReturnLegOf_TheAccumulateThenParsePairIsRecognized pins
// a-invocation-functions.ts's spawnUndetermined row: the accumulator
// (out) and the JSON.parse node inside the close handler are both
// named.
func TestSpawnReturnLegOf_TheAccumulateThenParsePairIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", (d) => {
		out += d;
	});
	child.on("close", () => {
		const level = JSON.parse(out);
		return level;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	leg, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence != "" {
		t.Fatalf("the accumulate-then-parse pair declined: %s", sentence)
	}
	if leg.AccumulatorName != "out" {
		t.Errorf("AccumulatorName = %q, want out", leg.AccumulatorName)
	}
	if leg.DataHandler == nil || leg.CloseHandler == nil {
		t.Fatalf("the handlers were not both recorded: %+v", leg)
	}
	if leg.ParseNode == nil || !isForeignParseOf(leg.ParseNode, "out") {
		t.Errorf("ParseNode = %v, want JSON.parse(out)", leg.ParseNode)
	}
}

// TestSpawnReturnLegOf_ACallbackReadingADifferentNameThanItsOwnParameterIsNotRecognized
// pins the specific-parameter discipline: the 'data' handler's `+=`
// right side is an outer identifier spelled "d" that is NOT the
// handler's own parameter — name equality is not enough.
func TestSpawnReturnLegOf_ACallbackReadingADifferentNameThanItsOwnParameterIsNotRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f(d: string) {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", () => {
		out += d;
	});
	child.on("close", () => {
		const level = JSON.parse(out);
		return level;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence == "" {
		t.Fatalf("a handler reading an outer name rather than its own parameter was recognized")
	}
	if !strings.Contains(sentence, "no accumulator is named") {
		t.Errorf("sentence %q does not name the accumulator gap", sentence)
	}
}

// TestSpawnReturnLegOf_AMissingCloseHandlerIsNotRecognized pins
// a-invocation-functions.ts's ORIGINAL spawnUndetermined body: a 'data'
// handler alone, with no 'close' handler at all.
func TestSpawnReturnLegOf_AMissingCloseHandlerIsNotRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", (d) => {
		out += d;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence == "" {
		t.Fatalf("a missing 'close' handler was recognized")
	}
	if !strings.Contains(sentence, `on("close"`) {
		t.Errorf("sentence %q does not name the missing close handler", sentence)
	}
}

// TestSpawnReturnLegOf_AnInterveningWriteToTheAccumulatorIsNotRecognized
// pins the hazard the brief calls out: a THIRD statement (neither
// handler) writing the accumulator between the call and the close
// handler must decline — the value the close handler reads is then not
// the value the two handlers alone accumulated.
func TestSpawnReturnLegOf_AnInterveningWriteToTheAccumulatorIsNotRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, spawnPairSource+`
function f() {
	const child = spawn("python3", ["./targets/level_ok.py"]);
	let out = "";
	child.stdout.on("data", (d) => {
		out += d;
	});
	out = "reset";
	child.on("close", () => {
		const level = JSON.parse(out);
		return level;
	});
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, sentence := spawnReturnLegOf(statements, 0, "child")
	if sentence == "" {
		t.Fatalf("an intervening write to the accumulator was recognized")
	}
	if !strings.Contains(sentence, "a statement other than the 'data' handler writes out") {
		t.Errorf("sentence %q does not name the intervening write", sentence)
	}
}

func TestForeignEdgeRecognition_TwoParsesOfTheStdoutBindingDecline(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	const a = JSON.parse(stdout);
	const b = JSON.parse(stdout);
	return [a, b];
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// one published fact cannot stand for two expressions
	if _, _, sentence := soleParseConsumerOf(statements, 0, "stdout"); sentence == "" {
		t.Errorf("two parses of the stdout binding were served one fact")
	} else if !strings.Contains(sentence, "parsed 2 times") {
		t.Errorf("the sentence %q does not say how many consumers there are", sentence)
	}
}

func TestForeignEdgeRecognition_NoParseOfTheStdoutBindingDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return stdout.length;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	if _, _, sentence := soleParseConsumerOf(statements, 0, "stdout"); sentence == "" {
		t.Errorf("a stdout binding nothing parses was served a JSON fact")
	}
}

func TestForeignEdgeRecognition_AParseInsideANestedFunctionIsNotTheConsumer(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(boosted: number[]) {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return () => JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// the arrow runs an unstated number of times, so no fact can be
	// pinned to one evaluation of that node
	if _, _, sentence := soleParseConsumerOf(statements, 0, "stdout"); sentence == "" {
		t.Errorf("a parse inside a nested function was pinned as the sole consumer")
	}
}

func TestForeignEdgeAt_ACallToSomeOtherProgramIsNotAnEdgeAndOwesNoSentence(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const out = execFileSync("ls", ["-l"], { encoding: "utf8" });
	return out;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	if outcome, isEdge := ForeignEdgeAt(ctx, NewEnv(), statements, 0); isEdge {
		t.Errorf("a call to a non-python program was read as a cross-language edge: %+v", outcome)
	}
}

func TestForeignEdgeAt_AnOrdinaryStatementIsNotAnEdge(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number) { const y = x + 1; return y; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	if outcome, isEdge := ForeignEdgeAt(ctx, NewEnv(), statements, 0); isEdge {
		t.Errorf("an ordinary declaration was read as a cross-language edge: %+v", outcome)
	}
}

/* ── the consumer-index push (analyze_statement.go's ForeignEdgeAt call) ── */
//
// A whole-route ForeignEdgeAt call that RECOGNIZES an edge (isEdge true)
// needs resolvesToChildProcessMember to resolve execFileSync to a
// real child_process.d.ts declaration, which this file's own banner
// names as deliberately not stood up here (a service-level fixture, not
// a walk-level one — E4's row is the real end-to-end check). So the
// "a fixture edge records its path" half of this unit cannot be pinned
// at this grain without building exactly the fixture the banner declines
// to fake; only the reachable half — an ordinary statement pushes
// nothing — is asserted here.

// TestAnalyzeStatements_AnOrdinaryStatementPushesNothingToTheForeignSink
// pins the other side of the push: listWalk's new
// `if running.ConsumedForeignSink != nil && outcome.TargetPath != ""`
// line never fires for a statement ForeignEdgeAt does not even
// recognize (isEdge false, no outcome at all) — the sink stays exactly
// as empty as a walk that never heard of the cross-language edge.
func TestAnalyzeStatements_AnOrdinaryStatementPushesNothingToTheForeignSink(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number) { const y = x + 1; return y; }\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	var consumed []string
	ctx.ConsumedForeignSink = &consumed
	env := NewEnv()
	env.Set("x", abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	AnalyzeStatements(ctx, env, statements, nil)
	if len(consumed) != 0 {
		t.Errorf("ConsumedForeignSink = %v, want empty — no edge was ever recognized here", consumed)
	}
}

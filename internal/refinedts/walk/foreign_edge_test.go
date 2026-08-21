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
// fixture's own spelling: resolvesToChildProcessExecFileSync asks the
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
  "refined": {"kind": "python-fact-artifact", "version": 1},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "runtime": {"band": "cpython-3.11+"},
  "harness": {"stdin": "json", "stdout": "json", "calls": "audio_level"},
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

// writeForeignArtifact drops an artifact text at the target's cache
// entry and clears the read memo, so each case reads its own file
// rather than a previous case's answer. Every test also pins the
// producer resolution to a dead path, so a developer's PATH cannot
// turn a missing-artifact case into a live export.
func writeForeignArtifact(t *testing.T, targetPath string, text string) {
	t.Helper()
	t.Setenv("REFINEDPY_CHECK", "/nonexistent/refinedpy-check")
	artifactPath := foreignCacheArtifactPath(targetPath)
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

func TestReadForeignArtifact_AMissingArtifactNamesTheFileAndTheCommandThatWritesIt(t *testing.T) {
	targetPath, _ := writeForeignTarget(t)
	t.Setenv("REFINEDPY_CHECK", "/nonexistent/refinedpy-check")
	forgetForeignArtifact(targetPath)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("a missing artifact answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, foreignCacheArtifactPath(targetPath)) {
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
		`"version": 1`, `"version": 2`, 1)
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
	// the provenance is the second step of the two-language explanation,
	// carried in the message text until relatedInformation can hold it
	if !strings.Contains(fired.MessageText, "said:") {
		t.Errorf("the message %q carries no provenance step from the target", fired.MessageText)
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
	name, call, ok := execFileSyncBindingOf(statements[0])
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
	script, scriptOk := singleScriptArgvOf(args[1])
	if !scriptOk || script != "./audio_level.py" {
		t.Errorf("argv[1] = %q (ok=%v), want ./audio_level.py", script, scriptOk)
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

func TestForeignEdgeRecognition_AnArgvWithArgumentsBeyondTheScriptDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("python3", ["./audio_level.py", "--fast"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := execFileSyncBindingOf(statements[0])
	args, _ := callArguments(call)
	// the target takes arguments this edge models nothing about
	if _, ok := singleScriptArgvOf(args[1]); ok {
		t.Errorf("a two-element argv was read as naming one script")
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
	_, call, _ := execFileSyncBindingOf(statements[0])
	args, _ := callArguments(call)
	_, encodingOk, sentence := execFileSyncOptionsOf(args[2])
	if sentence != "" {
		t.Fatalf("the options declined for the wrong reason: %s", sentence)
	}
	if encodingOk {
		t.Errorf("a missing encoding was read as making stdout a string; without one the sync exec answers a Buffer")
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

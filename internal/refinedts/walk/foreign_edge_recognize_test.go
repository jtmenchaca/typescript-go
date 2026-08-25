// Recognition of one foreign-edge statement, at its syntactic grain:
// the writeFileSync-then-execFileSync file-carried leg, the argv/
// options reading execFileSync/spawnSync share, and the execSync
// shell-string reader (its plain-command and heredoc shapes).

package walk

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── fileCrossingOf: the writeFileSync-then-execFileSync syntax reader ── */

// TestFileCrossingOf_AMatchingWriteImmediatelyBeforeTheCallIsRecognized
// pins d-data-legs.ts's tempFileNamedInArgvUndetermined shape: the
// statement immediately before the call writes the SAME path the call's
// own argv names, so fileCrossingOf answers the written payload and the
// argv element, with no sentence at all.
func TestFileCrossingOf_AMatchingWriteImmediatelyBeforeTheCallIsRecognized(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
declare function writeFileSync(path: string, data: string): void;
function f(samples: number[]) {
	writeFileSync("./targets/level_payload.json", JSON.stringify(samples));
	const stdout = execFileSync("python3", ["./targets/level_from_file.py", "./targets/level_payload.json"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	payload, filePathElement, sentence, ok := fileCrossingOf(ctx, statements, 1, args)
	if sentence != "" {
		t.Fatalf("a matching immediately-preceding write declined: %s", sentence)
	}
	if !ok {
		t.Fatalf("a matching immediately-preceding write was not recognized")
	}
	if payload == nil || !ast.IsIdentifier(payload) || payload.Text() != "samples" {
		t.Errorf("payload = %v, want the identifier samples (JSON.stringify's own argument)", payload)
	}
	if filePathElement == nil {
		t.Errorf("filePathElement is nil, want the argv element naming the file")
	}
}

// TestFileCrossingOf_AnInterveningStatementBetweenTheWriteAndTheCallDeclines
// pins the carrier premise's own boundary: the SAME write and the SAME
// matching argv path, but with one statement between them — the write is
// recognized (the path IS named by this call's argv), and the carrier
// premise (no statement between the write and the call) is what refuses
// it, named rather than silently falling through to an unrelated reading.
func TestFileCrossingOf_AnInterveningStatementBetweenTheWriteAndTheCallDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
declare function writeFileSync(path: string, data: string): void;
function f(samples: number[]) {
	writeFileSync("./targets/level_payload.json", JSON.stringify(samples));
	const unrelated = 1;
	const stdout = execFileSync("python3", ["./targets/level_from_file.py", "./targets/level_payload.json"], { encoding: "utf8" });
	return stdout + unrelated;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[2])
	args, _ := callArguments(call)
	_, _, sentence, ok := fileCrossingOf(ctx, statements, 2, args)
	if ok {
		t.Fatalf("a write separated from the call by an intervening statement was recognized")
	}
	if sentence == "" {
		t.Fatalf("an intervening statement between the write and the call was silently not-this-shape")
	}
	if !strings.Contains(sentence, "intervening statement") {
		t.Errorf("sentence %q does not name the intervening statement", sentence)
	}
}

// TestFileCrossingOf_AMismatchedWritePathStaysUndeterminedNamingIt pins
// the path-mismatch case: the immediately preceding statement DOES write
// a file, but a DIFFERENT path than either argv element names — recognized
// (the write and the call both exist) and undetermined (the carrier
// premise does not hold), named rather than silently read as no write at
// all or as an ordinary argv-scalar call.
func TestFileCrossingOf_AMismatchedWritePathStaysUndeterminedNamingIt(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
declare function writeFileSync(path: string, data: string): void;
function f(samples: number[]) {
	writeFileSync("./targets/wrong_path.json", JSON.stringify(samples));
	const stdout = execFileSync("python3", ["./targets/level_from_file.py", "./targets/level_payload.json"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	ctx := relationalAccumulationContext(p)
	_, call, _ := constBoundCallOf(statements[1])
	args, _ := callArguments(call)
	_, _, sentence, ok := fileCrossingOf(ctx, statements, 1, args)
	if ok {
		t.Fatalf("a mismatched write path was recognized as a fit")
	}
	if sentence == "" {
		t.Fatalf("a mismatched write path was silently not-this-shape")
	}
	if !strings.Contains(sentence, "wrong_path.json") {
		t.Errorf("sentence %q does not name the mismatched written path", sentence)
	}
	if !strings.Contains(sentence, "carrier premise") {
		t.Errorf("sentence %q does not name the carrier premise", sentence)
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
	edge, recognized, sentence, _, path := spawnSyncEdgeOf(nil, call, name, statements, 0)
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

// TestExecSyncEdgeOf_ATemplateWithASubstitutionRecognizesTheHeredocShape
// pins the ONE substitution shape execSyncHeredocCommandOf recognizes:
// a template whose constant prefix is `<runner> <script> <<<` and whose
// single substitution is JSON.stringify(<payload>) — the stdin-json
// convention spelled through a shell here-string
// (a-invocation-functions.ts's own execSyncUndetermined row, now
// recognized rather than declined). A template substitution that is NOT
// this narrow shape still owes the ordinary shell-string sentence — see
// TestExecSyncEdgeOf_ATemplateSubstitutionThatIsNotTheHeredocShapeStillDeclines.
func TestExecSyncEdgeOf_ATemplateWithASubstitutionRecognizesTheHeredocShape(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f(samples: number[]) {
	const stdout = execSync(
		`+"`python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`"+`,
		{ encoding: "utf8" },
	);
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, _ := constBoundCallOf(statements[0])
	edge, recognized, sentence, _, _ := execSyncEdgeOf(call, name)
	if sentence != "" {
		t.Fatalf("the heredoc shape declined: %s", sentence)
	}
	if !recognized || edge == nil {
		t.Fatalf("the heredoc shape was not recognized")
	}
	if edge.Payload == nil || !ast.IsIdentifier(edge.Payload) || edge.Payload.Text() != "samples" {
		t.Errorf("edge.Payload = %v, want the identifier samples", edge.Payload)
	}
}

// TestExecSyncEdgeOf_ATemplateSubstitutionThatIsNotTheHeredocShapeStillDeclines
// pins the boundary: a template substitution that does NOT match the
// narrow heredoc shape (here, a substitution that is not
// JSON.stringify(...)) still owes the ordinary shell-string sentence —
// the recognizer is exactly as narrow as its own doc states.
func TestExecSyncEdgeOf_ATemplateSubstitutionThatIsNotTheHeredocShapeStillDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f(samples: string) {
	const stdout = execSync(
		`+"`python3 ./targets/level_ok.py <<< '${samples}'`"+`,
		{ encoding: "utf8" },
	);
	return JSON.parse(stdout);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	name, call, _ := constBoundCallOf(statements[0])
	edge, recognized, sentence, sentenceNode, _ := execSyncEdgeOf(call, name)
	if recognized || edge != nil {
		t.Fatalf("a non-stringify substitution was read as a followed command")
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

/* ── construct: the no-stdin call against a stdin-reading target DETERMINES ── */
//
// d-data-legs.ts's computedInputKeySilentlySkippedUndetermined row (and any
// other call that reaches execFileSyncEdgeOf with no stdin `input` and no
// second argv element) recognizes as an edge with Payload/ArgvValue/FilePath
// all nil — mirroring checkArgvCrossing's ForeignSurfaceMixedStdinArgv case,
// checkOutboundLeg's own no-channel branch determines rather than declines
// when the target's surface is stdin-json: the target's harness reads its
// one value from stdin, this call sends none, so every concrete run throws
// at the target's own read before any value crosses — nothing here
// contradicts the target's stated entry.

func TestExecFileSyncEdgeOf_NoInputAndNoSecondArgvElementRecognizesWithNoPayload(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f() {
	const stdout = execFileSync("python3", ["./targets/level_ok.py"], { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[0])
	edge, ok, sentence, _, _ := execFileSyncEdgeOf(nil, call, "stdout", statements, 0)
	if sentence != "" {
		t.Fatalf("a call with no input and no second argv element declined: %s", sentence)
	}
	if !ok || edge == nil {
		t.Fatalf("a call with no input and no second argv element was not recognized (ok=%v)", ok)
	}
	if edge.Payload != nil || edge.ArgvValue != nil || edge.FilePath != nil {
		t.Errorf("edge = %+v, want Payload/ArgvValue/FilePath all nil", edge)
	}
}

/* ── construct: the execSync shell heredoc recognizer ── */
//
// a-invocation-functions.ts's execSyncUndetermined row spells the
// stdin-json convention through a shell here-string:
// `python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`.
// execSyncHeredocCommandOf recognizes EXACTLY this shape — a template
// whose constant prefix parses as `<runner> <script> <<<` (with an
// optional matched quote) and whose single substitution is
// JSON.stringify(<payload>) — and lowers it to the same recognized edge
// an execFileSync call with an `input` key gets.

func TestExecSyncHeredocCommandOf_TheSingleQuotedHereStringRecognizes(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = [0.5, -0.3, 0.2];\n"+
		"	const stdout = `python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	runnerWord, script, payload, ok := execSyncHeredocCommandOf(template)
	if !ok {
		t.Fatalf("the single-quoted here-string command was not recognized")
	}
	if runnerWord != "python3" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q, want python3 / ./targets/level_ok.py", runnerWord, script)
	}
	if payload == nil || !ast.IsIdentifier(payload) || payload.Text() != "samples" {
		t.Errorf("payload = %v, want the identifier samples", payload)
	}
}

func TestExecSyncHeredocCommandOf_AnUnquotedHereStringRecognizes(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = [0.5, -0.3, 0.2];\n"+
		"	const stdout = `python3 ./targets/level_ok.py <<< ${JSON.stringify(samples)}`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	runnerWord, script, payload, ok := execSyncHeredocCommandOf(template)
	if !ok {
		t.Fatalf("the unquoted here-string command was not recognized")
	}
	if runnerWord != "python3" || script != "./targets/level_ok.py" {
		t.Errorf("runnerWord=%q script=%q, want python3 / ./targets/level_ok.py", runnerWord, script)
	}
	if payload == nil || !ast.IsIdentifier(payload) || payload.Text() != "samples" {
		t.Errorf("payload = %v, want the identifier samples", payload)
	}
}

func TestExecSyncHeredocCommandOf_APipeInThePrefixIsRefused(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = [0.5, -0.3, 0.2];\n"+
		"	const stdout = `python3 ./targets/level_ok.py | tee out <<< '${JSON.stringify(samples)}'`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if _, _, _, ok := execSyncHeredocCommandOf(template); ok {
		t.Errorf("a prefix carrying a pipe was recognized as the narrow heredoc shape")
	}
}

func TestExecSyncHeredocCommandOf_TwoSubstitutionsAreRefused(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = [0.5, -0.3, 0.2];\n"+
		"	const extra = \"x\";\n"+
		"	const stdout = `python3 ./targets/level_ok.py ${extra} <<< '${JSON.stringify(samples)}'`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[2].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if _, _, _, ok := execSyncHeredocCommandOf(template); ok {
		t.Errorf("a template with two substitutions was recognized as the narrow heredoc shape")
	}
}

func TestExecSyncHeredocCommandOf_ANonStringifySubstitutionIsRefused(t *testing.T) {
	p := entryEnvTestProgram(t, "function f() {\n"+
		"	const samples = \"[0.5, -0.3, 0.2]\";\n"+
		"	const stdout = `python3 ./targets/level_ok.py <<< '${samples}'`;\n"+
		"	return stdout;\n"+
		"}\n")
	statements := relationalAccumulationBodyOf(t, p, "f")
	template := statements[1].AsVariableStatement().DeclarationList.
		AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer
	if _, _, _, ok := execSyncHeredocCommandOf(template); ok {
		t.Errorf("a substitution that is not JSON.stringify(...) was recognized as the narrow heredoc shape")
	}
}

func TestExecSyncEdgeOf_TheHeredocShapeRecognizesWithThePayload(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execSync(command: string, options: unknown): string;
function f() {
	const samples = [0.5, -0.3, 0.2];
	const stdout = execSync(`+"`python3 ./targets/level_ok.py <<< '${JSON.stringify(samples)}'`"+`, { encoding: "utf8" });
	return stdout;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	_, call, _ := constBoundCallOf(statements[1])
	edge, ok, sentence, _, _ := execSyncEdgeOf(call, "stdout")
	if sentence != "" {
		t.Fatalf("the heredoc shape declined: %s", sentence)
	}
	if !ok || edge == nil {
		t.Fatalf("the heredoc shape was not recognized (ok=%v)", ok)
	}
	if edge.Payload == nil || !ast.IsIdentifier(edge.Payload) || edge.Payload.Text() != "samples" {
		t.Errorf("edge.Payload = %v, want the identifier samples", edge.Payload)
	}
	if !strings.HasSuffix(edge.TargetPath, filepath.Join("targets", "level_ok.py")) {
		t.Errorf("edge.TargetPath = %q, want it to end in targets/level_ok.py", edge.TargetPath)
	}
}

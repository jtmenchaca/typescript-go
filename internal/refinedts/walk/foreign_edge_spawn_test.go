// The spawn (async) call's own recognition, and the accumulate-then-
// parse `.on()` pair reader spawnReturnLegOf still needs to find.

package walk

import (
	"path/filepath"
	"strings"
	"testing"
)

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

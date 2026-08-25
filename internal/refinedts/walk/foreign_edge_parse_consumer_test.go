// The return leg's sole-consumer scan: finding the JSON.parse node a
// bound stdout name reads through, and the three shapes that name it
// declines (two parses, an intervening write) versus the one it stays
// vacuously fine on (no consumer at all, or one buried in a nested
// function).

package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

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

// TestForeignEdgeRecognition_NoParseOfTheStdoutBindingIsVacuouslyFine
// pins construct 2's own determination: a recognized crossing whose
// result NO expression consumes needs no fact at all — soleParseConsumerOf
// answers (nil, -1, "") — a NIL node, an empty sentence — rather than a
// decline. There is no expression for a fact to attach to, and that is
// not a defect: the outbound leg's own judgment (a separate premise
// this function does not touch) still stands unchanged.
func TestForeignEdgeRecognition_NoParseOfTheStdoutBindingIsVacuouslyFine(t *testing.T) {
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
	found, at, sentence := soleParseConsumerOf(statements, 0, "stdout")
	if sentence != "" {
		t.Errorf("a stdout binding nothing parses reported a decline %q, want none", sentence)
	}
	if found != nil {
		t.Errorf("soleParseConsumerOf found a parse node %+v where none exists", found)
	}
	if at != -1 {
		t.Errorf("soleParseConsumerOf answered statement index %d for an absent consumer, want -1", at)
	}
}

// TestForeignEdgeRecognition_AParseInsideANestedFunctionIsVacuouslyFine
// is the nested-function twin of the "no consumer" row above: the arrow
// runs an unstated number of times, so foreignParseCallsIn's own
// function-boundary skip never counts it — from soleParseConsumerOf's
// point of view this body has NO top-level consumer either, and answers
// the same (nil, -1, "") as a body with no JSON.parse anywhere.
func TestForeignEdgeRecognition_AParseInsideANestedFunctionIsVacuouslyFine(t *testing.T) {
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
	// pinned to one evaluation of that node — and, as with the "no
	// consumer at all" row, that is not a defect: there is simply no
	// top-level expression for a fact to land on.
	found, _, sentence := soleParseConsumerOf(statements, 0, "stdout")
	if sentence != "" {
		t.Errorf("a parse inside a nested function reported a decline %q, want none", sentence)
	}
	if found != nil {
		t.Errorf("soleParseConsumerOf found a parse node %+v inside a nested function", found)
	}
}

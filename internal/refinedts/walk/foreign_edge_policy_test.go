// The orchestration layer above the artifact reader: the
// content-states-nothing vs cannot-read policy split, and ForeignEdgeAt's
// own recognition/no-recognition boundary. Split out of
// foreign_edge_test.go, which still holds the shared fixture builders
// (write*Target, *ArtifactJSON) every foreign_edge_*_test.go file in this
// package reuses.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

/* ── content-states-nothing vs cannot-read (ForeignEdgeAt's own policy split) ── */
//
// D5.count/D5.grade/D5.guard/D5.label/D5.propagate/D5.raise/D5.set are
// all designed so a consumer-side guard (or its deliberate absence,
// refused normally) carries the row's own determination WITHOUT a
// matching fact artifact — their own docstrings say so directly
// ("the consumer-side bounding guard is the path to a determination —
// the edge itself would otherwise be the named blocker"). Before this
// fix, ForeignEdgeAt reported RTS7002 unconditionally at the call site
// for EVERY artifact-read decline, drowning out both the correctly-
// guarded determination and a correctly-designed @refinedts-expect-error
// on the unguarded twin. foreignArtifactStatesNothing is the policy
// split: a sentence saying the artifact was READ but its CONTENT names
// nothing usable falls through to ordinary evaluation (no 7002); a
// sentence saying the artifact could not be TRUSTED at all keeps
// declining exactly as before.

// TestForeignArtifactStatesNothing_TheNoSurfaceSentenceFallsThrough
// pins the FIRST content-states-nothing shape verbatim against
// surfaceOf's own no-"surface"-key sentence (foreign_edge_artifact.go) —
// the exact shape D5.count/D5.grade/D5.guard/D5.label/D5.propagate/
// D5.raise/D5.set's artifacts carry (an ordinary stdin-json __main__
// harness the exporter still emitted no "surface" key for).
func TestForeignArtifactStatesNothing_TheNoSurfaceSentenceFallsThrough(t *testing.T) {
	artifactPath := "/repo/.refined/cache/D5.count.helper.py.refined.json"
	sentence := artifactPath + " states no callable surface for its __main__ block " +
		"— a harness shape this producer does not export a surface for — so nothing says what " +
		"the target does with its input and output"
	if !foreignArtifactStatesNothing(sentence) {
		t.Errorf("foreignArtifactStatesNothing(%q) = false, want true — the artifact was read; its content just names no surface", sentence)
	}
}

// TestForeignArtifactStatesNothing_TheNoFunctionRowSentenceFallsThrough
// pins the SECOND content-states-nothing shape verbatim against
// functionFactOf's own row-missing sentence — D5.edge's own artifact
// shape (a real "surface" naming "level_ok", but an empty "functions"
// map carrying no row for it).
func TestForeignArtifactStatesNothing_TheNoFunctionRowSentenceFallsThrough(t *testing.T) {
	artifactPath := "/repo/.refined/cache/D5.edge.helper.py.refined.json"
	sentence := artifactPath + " names " + "level_ok" + " as the surface's called function and " +
		"then states no fact for it"
	if !foreignArtifactStatesNothing(sentence) {
		t.Errorf("foreignArtifactStatesNothing(%q) = false, want true — the artifact was read; its content just names no fact for the called function", sentence)
	}
}

// TestForeignArtifactStatesNothing_AGenuinelyUnreadableArtifactKeepsDeclining
// pins the OTHER half of the split: every "cannot be trusted at all"
// shape — a missing file, unparseable JSON, a superseded/unrecognized
// envelope, a target-integrity hash mismatch, a runtime-band mismatch, a
// malformed surface field (wrong stdin/stdout channel, missing
// argIndex), a malformed cases/entries shape, an undecodable kernel
// set — must NOT be classified as content-states-nothing: the call
// keeps refusing with its real Decline exactly as before this fix.
func TestForeignArtifactStatesNothing_AGenuinelyUnreadableArtifactKeepsDeclining(t *testing.T) {
	cannotReadSentences := []string{
		"the Python target /repo/foo.py states no fact for this edge — there is no " +
			"/repo/.refined/cache/foo.py.refined.json; write it with `refinedpy-check --export-fact /repo/foo.py`",
		"/repo/.refined/cache/foo.py.refined.json is not readable JSON, so the target states nothing this edge can use",
		`/repo/.refined/cache/foo.py.refined.json carries no "refined" envelope, so nothing identifies it as a fact artifact`,
		`/repo/.refined/cache/foo.py.refined.json states a "version" field on its "refined" envelope, and the current ` +
			`schema states no version, ever — the envelope is a superseded shape, and states no fact this edge reads`,
		`/repo/.refined/cache/foo.py.refined.json states (kind "fact-artifact", language "typescript"), and this edge reads only ("fact-artifact", "python")`,
		"/repo/.refined/cache/foo.py.refined.json states the fact of a target whose contents hash to sha256:aaa, and " +
			"/repo/foo.py hashes to sha256:bbb — the exported fact is about different code than the code being checked; " +
			"re-export it with `refinedpy-check --export-fact /repo/foo.py`",
		"/repo/.refined/cache/foo.py.refined.json commits to the runtime band cpython-3.9, and this checker's Python pins commit to cpython-3.11+ " +
			"— the edge cannot inherit semantics it has not transcribed",
		`/repo/.refined/cache/foo.py.refined.json states a surface of kind "argv-vector", and this edge applies the JSON transport model only to ` +
			`"stdin-json", "argv-scalar", "stdin-json-argv-scalar", or "file-json"`,
		"/repo/.refined/cache/foo.py.refined.json carries no functions, so it states no fact about audio_level",
		"/repo/.refined/cache/foo.py.refined.json states a set this checker's kernel grammar does not read, so the fact for audio_level cannot be decoded",
	}
	for _, sentence := range cannotReadSentences {
		if foreignArtifactStatesNothing(sentence) {
			t.Errorf("foreignArtifactStatesNothing(%q) = true, want false — this sentence means the artifact could not be trusted, not that its content states nothing", sentence)
		}
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

// The return leg's own corners: the ±Infinity JSON.parse hazard, the
// Result-shaped object return (Item 1's own object-crosses-the-wire
// case), and the bare-sort declared return at a recognized crossing.
// Split out of foreign_edge_test.go, which still holds the shared
// fixture builders (write*Target, *ArtifactJSON) every
// foreign_edge_*_test.go file in this package reuses.

package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

/* ── the return leg's ±Infinity corner (json.dumps spells a token JSON.parse rejects) ── */
//
// json.dumps(float("inf")) writes the bare token `Infinity` rather than
// a JSON number literal (JSON itself is not at fault: `1e999` is a
// legal JSON number and parses to Infinity in both runtimes — the bare
// token is Python's default serializer's own choice), and JSON.parse
// throws on that token at runtime. A return set admitting either
// infinite corner must not bind as the parse's fact: it degrades to a
// named undetermined instead. foreignReturnCornerObstacle is the gate;
// these pin it directly against a real kernel's Member ask (x ∈ A), the
// same idiom effect_math_test.go's own Math.min/max corner checks and
// kernel_bridge_test.go's ℝ̄∖{0} row already use.

// foreignReturnCornerKernel loads the same kernel every other
// kernel-backed test in this file loads — skips (never a faked pass)
// when the native dylib is absent.
func foreignReturnCornerKernel(t *testing.T) *kernelbridge.RefinedTSKernel {
	t.Helper()
	return nanWrapperLoadKernel(t)
}

func TestForeignReturnCornerObstacle_APlusInfinityAdmittingSetDegrades(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	// ℝ̄ ∖ {0}: the same "admits +∞" set kernel_bridge_test.go's own
	// TestMembershipTheRuntimeCheckOverTheWireRoundTrip pins Member true
	// for at +∞ — a derived Python return this wide (e.g. no upper
	// bound at all) reaches this shape.
	admitsPosInf := refinementsets.MakeRefinedSet(refinementsets.Difference(
		refinementsets.Numbers, refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
	))
	sentence := foreignReturnCornerObstacle(ctx, admitsPosInf)
	if sentence == "" {
		t.Fatalf("a +Infinity-admitting return set answered no obstacle, want the named corner")
	}
	if !strings.Contains(sentence, "Infinity") {
		t.Errorf("sentence %q does not name the Infinity corner", sentence)
	}
	if !strings.Contains(sentence, "JSON") {
		t.Errorf("sentence %q does not name json.dumps's bare Infinity token that JSON.parse rejects", sentence)
	}
}

func TestForeignReturnCornerObstacle_AMinusInfinityAdmittingSetDegradesIdentically(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	// AtMost(0): a one-sided ray to -∞ admits -Infinity as a member
	// (the same ray shape refinement_forms_test.go's own
	// TestInfinitiesAreElementsNaNIsRefused pins AtLeast/AtMost's
	// corners as elements, never excluded members).
	admitsNegInf := refinementsets.MakeRefinedSet(refinementsets.AtMost(0))
	sentence := foreignReturnCornerObstacle(ctx, admitsNegInf)
	if sentence == "" {
		t.Fatalf("a -Infinity-admitting return set answered no obstacle, want the named corner")
	}
	if !strings.Contains(sentence, "-Infinity") {
		t.Errorf("sentence %q does not name the -Infinity corner", sentence)
	}
	if !strings.Contains(sentence, "JSON") {
		t.Errorf("sentence %q does not name json.dumps's bare Infinity token that JSON.parse rejects", sentence)
	}
}

// TestForeignReturnCornerObstacle_AFiniteWindowBindsExactlyAsBefore is
// the regression pin: audio_level.py's own real return window (0 … 1,
// the exact shape foreignArtifactJSON states and TestCheckOutboundLeg's
// own fixtures already exercise) admits neither corner, so the gate
// answers no obstacle and foreignReturnValue serves the same
// TrustSpec-graded fact it always has.
func TestForeignReturnCornerObstacle_AFiniteWindowBindsExactlyAsBefore(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	finiteWindow := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(0), refinementsets.AtMost(1),
	)
	if sentence := foreignReturnCornerObstacle(ctx, finiteWindow); sentence != "" {
		t.Fatalf("a finite 0 … 1 return window reported an obstacle, want none: %q", sentence)
	}
	artifact := &ForeignArtifact{Called: ForeignFunctionFact{
		Name:   "audio_level",
		Return: ForeignReturn{Cases: []Case{{Sort: CaseSortNumber, Set: finiteWindow}}, StdoutPure: true},
	}}
	got := foreignReturnValue(artifact)
	want := abstractdomain.KnownSet(finiteWindow, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("foreignReturnValue(finite window) = %+v, want %+v — a finite return must bind unchanged", got, want)
	}
}

// TestForeignReturnCornerObstacle_ARefusedQuestionAnswersNoObstacle pins
// the "no proof, no obstacle" reading a nil kernel gives — the same
// fallthrough foreignScalarSubset and subsetProved already answer for a
// question the kernel cannot decide, so an untested corner never
// falsely degrades a set the checker simply could not ask about.
func TestForeignReturnCornerObstacle_ARefusedQuestionAnswersNoObstacle(t *testing.T) {
	ctx := &FlowContext{}
	admitsPosInf := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	if sentence := foreignReturnCornerObstacle(ctx, admitsPosInf); sentence != "" {
		t.Errorf("a nil kernel answered an obstacle sentence %q, want none (refused, not refuted)", sentence)
	}
}

/* ── construct 1: the ±Infinity corner DETERMINES a finite return ── */
//
// h-numeric-edges.ts's maybeInfiniteStaticallySilentRuntimeThrows row: a
// return set admitting +Infinity no longer declines — the corner is
// DIFFERENCED OUT (foreignFiniteReturnSet) and the finite remainder
// binds, because a completed JSON.parse call never actually carries the
// corner value (every concrete run that would have is a thrown
// SyntaxError, per json.dumps's own bare-Infinity-token behavior).

// TestForeignFiniteReturnSet_APlusInfinityAdmittingSetNarrowsToFinite
// pins the ray-narrowing case h-numeric-edges.ts's own H3 row derives:
// atLeast(0) (an unbounded ray admitting +Infinity) narrows to a set
// that STILL admits every finite value at or above 0, but no longer
// admits +Infinity itself.
func TestForeignFiniteReturnSet_APlusInfinityAdmittingSetNarrowsToFinite(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	admitsPosInf := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	narrowed := foreignFiniteReturnSet(ctx, admitsPosInf)
	if ctx.Kernel.Member(narrowed, []float64{math.Inf(1)}) {
		t.Errorf("foreignFiniteReturnSet(atLeast(0)) still admits +Infinity, want it differenced out")
	}
	if !ctx.Kernel.Member(narrowed, []float64{1000}) {
		t.Errorf("foreignFiniteReturnSet(atLeast(0)) no longer admits an ordinary finite member (1000)")
	}
	if !ctx.Kernel.Member(narrowed, []float64{0}) {
		t.Errorf("foreignFiniteReturnSet(atLeast(0)) no longer admits its own finite floor (0)")
	}
}

// TestForeignFiniteReturnSet_AMinusInfinityAdmittingSetNarrowsToFinite
// is the mirror for a ray to -∞ (AtMost(0)).
func TestForeignFiniteReturnSet_AMinusInfinityAdmittingSetNarrowsToFinite(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	admitsNegInf := refinementsets.MakeRefinedSet(refinementsets.AtMost(0))
	narrowed := foreignFiniteReturnSet(ctx, admitsNegInf)
	if ctx.Kernel.Member(narrowed, []float64{math.Inf(-1)}) {
		t.Errorf("foreignFiniteReturnSet(atMost(0)) still admits -Infinity, want it differenced out")
	}
	if !ctx.Kernel.Member(narrowed, []float64{-1000}) {
		t.Errorf("foreignFiniteReturnSet(atMost(0)) no longer admits an ordinary finite member (-1000)")
	}
}

// TestForeignFiniteReturnSet_AFiniteWindowIsUnchanged pins the no-op
// case: a set admitting neither corner (audio_level.py's own 0…1 shape)
// answers unchanged — narrowing a set with nothing to narrow is a no-op,
// not a spurious difference-with-nothing wrapper.
func TestForeignFiniteReturnSet_AFiniteWindowIsUnchanged(t *testing.T) {
	ctx := &FlowContext{Kernel: foreignReturnCornerKernel(t)}
	finiteWindow := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1))
	narrowed := foreignFiniteReturnSet(ctx, finiteWindow)
	if !reflect.DeepEqual(narrowed, finiteWindow) {
		t.Errorf("foreignFiniteReturnSet(0…1) = %+v, want unchanged %+v", narrowed, finiteWindow)
	}
}

// TestForeignFiniteReturnSet_ARefusedQuestionAnswersUnchanged pins the
// "no proof, no narrowing" reading a nil kernel gives — the same
// fallthrough foreignReturnCornerObstacle itself already answers for a
// refused question, so an untested corner never gets narrowed away on
// a set the checker simply could not ask about.
func TestForeignFiniteReturnSet_ARefusedQuestionAnswersUnchanged(t *testing.T) {
	ctx := &FlowContext{}
	admitsPosInf := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0))
	narrowed := foreignFiniteReturnSet(ctx, admitsPosInf)
	if !reflect.DeepEqual(narrowed, admitsPosInf) {
		t.Errorf("foreignFiniteReturnSet with a nil kernel = %+v, want unchanged %+v", narrowed, admitsPosInf)
	}
}

/* ── item 1: object-shaped values cross the wire (a Result-style return) ── */

// foreignResultTargetSource is the Python body a Result-shaped
// artifact describes: two object cases in one return cases list —
// {"ok": true, "value": <number>} on success, {"ok": false, "error":
// <string>} on failure — the RULED schema's own reading of a
// Result-style union.
const foreignResultTargetSource = "def parse_level(text):\n" +
	"    try:\n" +
	"        return {\"ok\": True, \"value\": float(text)}\n" +
	"    except ValueError:\n" +
	"        return {\"ok\": False, \"error\": \"not a number\"}\n"

// writeForeignResultTarget writes the Result-shaped target into a
// fresh temp directory and answers its path and the sha256 the
// artifact must state to match it.
func writeForeignResultTarget(t *testing.T) (string, string) {
	t.Helper()
	targetPath := filepath.Join(t.TempDir(), "parse_level.py")
	if err := os.WriteFile(targetPath, []byte(foreignResultTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignResultTargetSource))
	return targetPath, "sha256:" + hex.EncodeToString(sum[:])
}

// stringsWireSet is refinementsets.Strings' own wire text
// (kernelbridge.EncodeSet(refinementsets.Strings)), spelled once here
// through the real encoder rather than hand-transcribed — a hand-
// written "star of codepoints" guess drifted from the decoder's actual
// form names (wire_decode.go's "star" case reads a capitalized "A"
// wrapping a full nested set, never a bare {"form":"codepoints"}).
var stringsWireSet = kernelbridge.EncodeSet(refinementsets.Strings)

// foreignResultArtifactJSON builds an artifact whose return "cases" is
// the RULED schema's object vocabulary: two object cases, each CLOSED
// (the producer states the exact key set each branch holds) — the
// success branch {"ok": boolean-true-only, "value": a finite number}
// and the failure branch {"ok": boolean-false-only, "error": a
// string}.
func foreignResultArtifactJSON(contentHash string, targetFile string) string {
	return `{
  "refined": {"kind": "fact-artifact"},
  "target": {"file": "` + targetFile + `", "contentHash": "` + contentHash + `"},
  "language": "python",
  "runtime": {"band": "cpython-3.11+"},
  "surface": {"kind": "stdin-json", "stdin": "json", "stdout": "json", "calls": "parse_level"},
  "functions": {
    "parse_level": {
      "entry": [{"name": "text", "cases": [{"sort": "string", "set": ` + stringsWireSet + `}]}],
      "return": {"cases": [
        {"sort": "object", "closed": true, "members": {
          "ok": [{"sort": "boolean"}],
          "value": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -1000000, "exp": 0}},
                                                            {"form": "atMost", "a": {"num": 1000000, "exp": 0}}]}}]
        }},
        {"sort": "object", "closed": true, "members": {
          "ok": [{"sort": "boolean"}],
          "error": [{"sort": "string", "set": ` + stringsWireSet + `}]
        }}
      ], "stdoutPure": true},
      "provenance": {"line": 3, "said": "parse_level answers {ok, value} or {ok, error}"}
    }
  }
}`
}

// TestForeignResultReturn_ATwoCaseObjectReturnBindsAndOkStyleAccessJudges
// pins Item 1 end to end at the grain this package can reach without a
// @types/node program host (this file's own header names that limit):
// a fixture-registered Result-shaped artifact — two object cases in
// one return cases list — reads through ReadForeignArtifact, lowers
// through foreignReturnValue into abstractdomain.KnownObject-shaped
// knowledge (a KindKindUnion of two KindObject arms, the union channel
// scalar multi-cases already use), and a `.ok`-style member judged
// through the EXISTING object-assignability law (CheckObjectTarget,
// via CheckAssignabilityOfArm's own union-arm dispatch) proves the
// member exists and fits on BOTH arms — never a re-derived reading
// specific to this artifact.
func TestForeignResultReturn_ATwoCaseObjectReturnBindsAndOkStyleAccessJudges(t *testing.T) {
	kernel := foreignReturnCornerKernel(t)
	targetPath, contentHash := writeForeignResultTarget(t)
	writeForeignArtifact(t, targetPath, foreignResultArtifactJSON(contentHash, targetPath))

	artifact, sentence := ReadForeignArtifact(targetPath)
	if sentence != "" {
		t.Fatalf("the Result-shaped fixture artifact declined: %s", sentence)
	}
	returnCases := artifact.Called.Return.Cases
	if len(returnCases) != 2 {
		t.Fatalf("len(returnCases) = %d, want 2 (the two object cases)", len(returnCases))
	}
	for i, c := range returnCases {
		if c.Sort != CaseSortObject {
			t.Fatalf("returnCases[%d].Sort = %q, want %q", i, c.Sort, CaseSortObject)
		}
		if !c.Closed {
			t.Errorf("returnCases[%d].Closed = false, want true — the artifact states \"closed\": true", i)
		}
	}

	bound := foreignReturnValue(artifact)
	if bound.Kind != abstractdomain.KindKindUnion {
		t.Fatalf("foreignReturnValue(Result artifact).Kind = %v, want KindKindUnion (two object arms joined)", bound.Kind)
	}
	if len(bound.Arms) != 2 {
		t.Fatalf("len(bound.Arms) = %d, want 2", len(bound.Arms))
	}
	for i, arm := range bound.Arms {
		if arm.Kind != abstractdomain.KindObject {
			t.Fatalf("bound.Arms[%d].Kind = %v, want KindObject", i, arm.Kind)
		}
		if !arm.Complete {
			t.Errorf("bound.Arms[%d].Complete = false, want true — Closed carried through from the artifact", i)
		}
		var hasOk bool
		for _, key := range arm.Keys {
			if key.Name == "ok" {
				hasOk = true
				if key.Value.Kind != abstractdomain.KindValues || key.Value.KindTag != abstractdomain.PrimitiveBoolean {
					t.Errorf("bound.Arms[%d]'s 'ok' key = %+v, want a KindValues{PrimitiveBoolean}", i, key.Value)
				}
			}
		}
		if !hasOk {
			t.Errorf("bound.Arms[%d] carries no 'ok' key: %+v", i, arm.Keys)
		}
	}

	// the ".ok"-style member access judges: a target object annotation
	// stating only {ok: boolean} (every arm the union may take carries
	// that key at that sort) must PROVE for this union, through the
	// EXISTING object-assignability laws — CheckAssignabilityOfArm's own
	// union dispatch (CheckKindUnion) walks each arm through
	// CheckObjectTarget, and a captured 7001/7002 on any arm is what
	// would fail this pin.
	p := entryEnvTestProgram(t, "const anchor = 1;\n")
	anchorStatement := p.Entry.Statements.Nodes[0]
	anchorNode := anchorStatement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes[0].AsVariableDeclaration().Initializer

	okTarget := annotations.DeclaredRefinement{
		Kind: annotations.DeclaredObject,
		Object: &annotations.ObjectAnnotation{
			Keys: []annotations.ObjectKeySpec{
				{
					Name:  "ok",
					Count: setPtr(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))),
					At:    anchorNode,
					Value: annotations.ObjectKeyValue{
						Kind: annotations.KeyValueSet,
						Set:  setPtr(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1}))),
					},
				},
			},
		},
	}

	var captured []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		Kernel: kernel,
		Report: func(d assignability.RefinementDiagnostic) { captured = append(captured, d) },
	}
	CheckAssignability(ctx, bound, okTarget, anchorNode, "the returned value", nil)
	for _, d := range captured {
		t.Errorf("checking '.ok' against the Result union reported %d: %s — want no refutation, every arm carries 'ok: boolean'", d.Code, d.MessageText)
	}
}

// TestReadForeignArtifact_AnObjectCaseWithNoMembersDeclinesNamingIt pins
// casesOf's strict object arm: an object case stating no "members" at
// all is a malformed claim (not a shape this edge can guess a key set
// for), and the decline names it — the same "recognized and named,
// never silently guessed past" discipline every other malformed-case
// row in this file already gets.
func TestReadForeignArtifact_AnObjectCaseWithNoMembersDeclinesNamingIt(t *testing.T) {
	targetPath, contentHash := writeForeignResultTarget(t)
	text := strings.Replace(
		foreignResultArtifactJSON(contentHash, targetPath),
		`{"sort": "object", "closed": true, "members": {
          "ok": [{"sort": "boolean"}],
          "value": [{"sort": "number", "set": {"forms": [{"form": "atLeast", "a": {"num": -1000000, "exp": 0}},
                                                            {"form": "atMost", "a": {"num": 1000000, "exp": 0}}]}}]
        }}`,
		`{"sort": "object", "closed": true}`, 1)
	writeForeignArtifact(t, targetPath, text)

	artifact, sentence := ReadForeignArtifact(targetPath)
	if artifact != nil {
		t.Fatalf("an object case with no members answered a fact: %+v", artifact)
	}
	if !strings.Contains(sentence, "members") {
		t.Errorf("the sentence %q does not name the missing \"members\" object", sentence)
	}
}

/* ── the bare-sort declared return, at a recognized crossing ──────── */
//
// ISSUES.md's own Python-side finding: "loop blockers unnamed when
// return annotation unreadable — bare `-> float` never judged". This
// measures whether the TS/Go side carries the SAME gap: a plain `number`
// (no zod refinement) declared FUNCTION RETURN TYPE, at a position a
// recognized crossing feeds.
//
// THE MEASURED ANSWER IS NO — the TS side does not carry this gap, and
// the evidence is the grounding fix type_node_sets.go's own comment
// documents: a bare `number`/`string`/`boolean` keyword type used to
// compile to nil-nil ("plain TypeScript", judged nowhere), and now
// compiles to a Stated DeclaredSet (R-bar for `number`) instead — the
// same DeclaredSet kind a real zod refinement compiles to. Two real
// production paths consume that: contract_file_facts.go's
// positionGrounds treats ANY DeclaredSet (bare or refined) as grounding
// the contract, so analyze_function.go's `if contract.Grounded { result
// = contract.Result }` passes the bare sort through as the SAME `result`
// a refined return would be; and return_statement.go's
// AnalyzeReturnStatement calls CheckAssignability(ctx, known, *result,
// ...) unconditionally whenever result != nil, with no separate branch
// for "the stated set happens to be a bare ground". This test proves it
// by RUNNING that exact pipeline (CompileContractFileFacts +
// AnalyzeFunction, the same two calls a real check performs) rather
// than reading the source: a value shaped exactly as a recognized
// crossing's return leg would produce it (foreignAbstractValueOfCases,
// a number case beside a null case — a target whose return states
// `Optional[float]`) is pinned on the JSON.parse node a crossing's
// return leg would attach through (ForeignEdgeAt's own NodeOverrides
// seam, foreign_edge.go's file banner), directly under a bare `: number`
// return type with NO zod refinement anywhere. If the bare sort judged
// nothing, this would report no diagnostic (the value simply passes
// through unwitnessed, the same silent gap ISSUES.md names on the
// Python side); it reports 7001 instead — the bare `number` return
// position excludes absence exactly as a refined one would, so the
// crossing's own possibly-null return leg is judged, not skipped.
func TestAnalyzeFunction_ABareNumberReturnTypeJudgesARecognizedCrossingsReturnLeg(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function execFileSync(file: string, args: string[], options: unknown): string;
function f(): number {
	const stdout = execFileSync("python3", ["./target.py"], { encoding: "utf8" });
	return JSON.parse(stdout);
}
`)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := entryEnvFunctionNamed(t, p, "f")
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	contract, ok := contracts[symbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts registered no contract for f")
	}
	if !contract.Grounded {
		t.Fatalf("a bare `: number` return type left the contract ungrounded — positionGrounds no longer treats a bare DeclaredSet as grounding")
	}
	if contract.Result == nil || contract.Result.Kind != annotations.DeclaredSet {
		t.Fatalf("contract.Result = %+v, want a DeclaredSet (the bare `number` keyword's own ground)", contract.Result)
	}

	// find the JSON.parse(stdout) node the return statement holds — the
	// exact node ForeignEdgeAt's own Override would pin (foreign_edge.go's
	// file banner: "the fact on JSON.parse(stdout) comes from ANOTHER
	// LANGUAGE'S checker")
	returnStatement := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[1]
	parseNode := returnStatement.AsReturnStatement().Expression

	// the value a recognized crossing's return leg would derive for a
	// target stating `Optional[float]` — a number case beside a null
	// case, folded through the SAME foreignAbstractValueOfCases the real
	// return leg calls (foreign_edge.go)
	crossingValue := foreignAbstractValueOfCases([]Case{
		{Sort: CaseSortNumber, Set: refinementsets.Numbers},
		{Sort: CaseSortNull},
	})
	if crossingValue.Kind != abstractdomain.KindPossiblyUndefined {
		t.Fatalf("foreignAbstractValueOfCases(number, null) = %+v, want KindPossiblyUndefined", crossingValue)
	}

	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:             p,
		Contracts:     contracts,
		Aliases:       dataflowfacts.NewAliasClasses(),
		Declared:      map[string]*annotations.DeclaredRefinement{},
		Report:        func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
		NodeOverrides: map[*ast.Node]abstractdomain.AbstractValue{parseNode: crossingValue},
	}
	AnalyzeFunction(ctx, contract, nil)

	if len(reported) == 0 {
		t.Fatalf("a bare `: number` return type reported NOTHING against a possibly-null crossed value — " +
			"the bare-sort declared return is not judged at this recognized crossing, the same gap ISSUES.md " +
			"names on the Python side")
	}
	fired7001 := false
	for _, d := range reported {
		if d.Code == 7001 {
			fired7001 = true
		}
	}
	if !fired7001 {
		t.Errorf("reported %+v, want a 7001 refutation naming the possibly-absent crossed value against the bare `number` return", reported)
	}
}

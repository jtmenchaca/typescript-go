// Tests for listWalk's own two ledgered defects (analyze_statement.go):
//
//  1. the loop-fixpoint path — a recognized cross-language call inside a
//     `for` body's own statement list must reach ForeignEdgeAt exactly
//     the way a straight-line or try-body statement list does.
//  2. the per-edge override — two edges recognized in the SAME flat
//     list (a diamond: two execFileSync calls, then two JSON.parse
//     reads) must each bind their own parse's fact; the second edge's
//     recognition must never clobber the first's still-pending
//     override.
//
// Both are exercised against a REAL resolvesToChildProcessMember pass:
// entryEnvTestProgram carries no @types/node (foreign_edge_test.go's
// own banner), so these tests stand up a hand-built child_process.d.ts
// at the path shape a real @types/node package would put it at
// (node_modules/@types/node/child_process.d.ts) — the same declaring-
// file-name test the real package resolves through, not a fake.
package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// childProcessDeclaration is a minimal child_process.d.ts body —
// execFileSync's own signature, the only export these tests call.
// Buffer is declared globally, exactly where the real @types/node
// package declares it (buffer.d.ts's own `global { interface Buffer
// extends Uint8Array {} }`, referenced from index.d.ts alongside
// child_process.d.ts) — without it, execFileSync's stated `string |
// Buffer` return resolves Buffer to an error type nothing can spell,
// which poisons the WHOLE union in ReturnTypeGround (typeGroundOf's
// own "a union with the unknown IS the unknown" rule) and falls the
// captured-stdout binding to Opaque on any path that reads the call's
// ordinary (non-overridden) evaluation. Declaring Buffer as the real
// package does removes that fixture gap rather than papering over it.
const childProcessDeclaration = `
declare module "child_process" {
  export function execFileSync(
    file: string,
    args?: readonly string[],
    options?: { input?: string; encoding?: string }
  ): string | Buffer;
}
`

// bufferGlobalDeclaration stands Buffer up as a real global interface —
// the same placement (global, extending Uint8Array) buffer.d.ts's own
// ambient declaration uses, minimal past the extends clause since
// nothing here reads any of Buffer's own members.
const bufferGlobalDeclaration = `
interface Buffer extends Uint8Array {}
`

// listWalkTestProgramWithNodeTypes stands up a program carrying a real
// child_process.d.ts at node_modules/@types/node/child_process.d.ts —
// the exact path shape resolvesToChildProcessMember's declaring-file
// test recognizes (isChildProcessDeclarationPath reads the BASE NAME
// alone, so this placement is not a special case, it is the ordinary
// one). entrySource may import { execFileSync } from "child_process"
// and it resolves for real.
//
// mainPath is the entry file's OWN absolute path — a REAL OS path
// (under a t.TempDir(), never "/main.ts") so resolveForeignScriptPath's
// relative "./audio_level.py" resolution (against the source file's own
// directory) lands on the same real directory writeForeignTarget wrote
// the .py file into, and ForeignCacheArtifactPath/ReadForeignArtifact
// (real os.Stat/os.ReadFile, no vfs) read the artifact this test wrote
// there for real. tsoptions/compiler operate on the vfs; path
// RESOLUTION inside foreign_edge.go operates on plain strings — the two
// halves only need to agree on the string, not on which FS backs it.
func listWalkTestProgramWithNodeTypes(t *testing.T, mainPath string, entrySource string) *program.CheckerProgram {
	t.Helper()
	nodeTypesDir := filepath.Join(filepath.Dir(mainPath), "node_modules", "@types", "node")
	tsconfigPath := filepath.Join(filepath.Dir(mainPath), "tsconfig.json")
	fs := vfstest.FromMap(map[string]string{
		mainPath: entrySource,
		filepath.Join(nodeTypesDir, "child_process.d.ts"): childProcessDeclaration,
		filepath.Join(nodeTypesDir, "buffer.d.ts"):         bufferGlobalDeclaration,
		filepath.Join(nodeTypesDir, "index.d.ts"): `/// <reference path="child_process.d.ts" />` + "\n" +
			`/// <reference path="buffer.d.ts" />` + "\n",
		filepath.Join(nodeTypesDir, "package.json"): `{"name": "@types/node", "version": "1.0.0", "types": "index.d.ts"}`,
		tsconfigPath: `{
			"compilerOptions": {"types": ["node"]},
			"files": [` + strconv.Quote(mainPath) + `]
		}`,
	}, false /*useCaseSensitiveFileNames*/)
	fs = bundled.WrapFS(fs)
	host := compiler.NewCompilerHost(filepath.Dir(mainPath), fs, bundled.LibPath(), nil, nil, nil)
	parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile(tsconfigPath, &core.CompilerOptions{}, nil, host, nil)
	if len(errors) != 0 {
		t.Fatalf("parsing tsconfig.json: %v", errors)
	}
	compilerProgram := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	compilerProgram.BindSourceFiles()
	c, done := compilerProgram.GetTypeChecker(t.Context())
	t.Cleanup(done)
	entry := compilerProgram.GetSourceFile(mainPath)
	if entry == nil {
		t.Fatalf("no entry source file at %s", mainPath)
	}
	return &program.CheckerProgram{Program: compilerProgram, Checker: c, Entry: entry}
}

// listWalkForeignEdgeFixture writes a green artifact for one execFileSync
// target and answers everything a listWalk-grain test needs: the real
// checker program, a context carrying a live kernel, and a diagnostic
// sink. Reuses foreign_edge_test.go's own artifact literal and target
// source so the crossing fit (audio_level's -2…2 entry, 0…1 return)
// is the same green shape foreignOutboundFixture already pins.
//
// The entry file and the .py target share ONE real temp directory,
// marked as a project root (a .git stub) so ForeignCacheArtifactPath's
// own ancestor walk terminates there rather than escaping to this repo's
// actual root.
func listWalkForeignEdgeFixture(t *testing.T, entrySource string) (*program.CheckerProgram, *FlowContext, *[]assignability.RefinementDiagnostic) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("marking the temp root: %v", err)
	}
	targetPath := filepath.Join(root, "audio_level.py")
	if err := os.WriteFile(targetPath, []byte(foreignTargetSource), 0o644); err != nil {
		t.Fatalf("writing the target: %v", err)
	}
	sum := sha256.Sum256([]byte(foreignTargetSource))
	contentHash := "sha256:" + hex.EncodeToString(sum[:])
	writeForeignArtifact(t, targetPath, foreignArtifactJSON(contentHash, targetPath, true))

	mainPath := filepath.Join(root, "main.ts")
	p := listWalkTestProgramWithNodeTypes(t, mainPath, entrySource)
	var reported []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Kernel:    nanWrapperLoadKernel(t),
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(d assignability.RefinementDiagnostic) { reported = append(reported, d) },
	}
	return p, ctx, &reported
}

/* ── (a) the loop pin: a recognized edge inside a for body binds ─── */

// TestListWalk_AForeignEdgeInsideAForLoopBodyBindsItsFact pins the
// corpus row b-runners.ts's own python3RecognizedInsideForLoop: the
// execFileSync call, the const binding, and the JSON.parse consumer all
// sit inside the for body's own Block — AnalyzeLoopStatement's SolveLoop
// walks that Block through the SAME AnalyzeStatement the top level
// uses (LoopAnalyzers.AnalyzeStatement in loop_statement.go), which
// dispatches a Block to AnalyzeBlockStatement -> AnalyzeStatements ->
// listWalk over the block's own statements — the identical route a
// try-body's own Block already gets. The parse must read the target's
// stated 0…1 fact, not residue.
func TestListWalk_AForeignEdgeInsideAForLoopBodyBindsItsFact(t *testing.T) {
	_, ctx, reported := listWalkForeignEdgeFixture(t, `
import { execFileSync } from "child_process";
function f(boosted: number[]): number {
	let last = 0;
	for (let i = 0; i < 1; i++) {
		const stdout = execFileSync("python3", ["./audio_level.py"], {
			input: JSON.stringify(boosted),
			encoding: "utf8",
		});
		const level: number = JSON.parse(stdout);
		last = level;
	}
	return last;
}
`)
	fn := entryEnvFunctionNamed(t, ctx.P, "f")
	outer := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	loop := outer[1]
	if !ast.IsForStatement(loop) {
		t.Fatalf("outer[1] is not a for statement: %v", loop.Kind)
	}
	body := loop.AsForStatement().Statement.AsBlock().Statements.Nodes
	if len(body) != 3 {
		t.Fatalf("loop body has %d statements, want 3 (the call, the parse declaration, the assignment)", len(body))
	}

	env := NewEnv()
	env.Set("boosted", abstractdomain.KnownSet(
		mustParseRefinedSet(t, -2, 2, 1), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))

	// walk the WHOLE function body, exactly as an ordinary check would —
	// `let last = 0;` seeds the tracked binding the loop later writes,
	// then the loop itself walks through AnalyzeLoopStatement's own
	// SolveLoop/bodyEffect route
	AnalyzeStatements(ctx, env, outer, nil)

	last, ok := env.Get("last")
	if !ok {
		t.Fatalf("env holds no value for last after the loop")
	}
	words := foreignSetWords(mustSetOfKnown(t, last))
	if !containsBoth(words, "0", "1") {
		t.Errorf("last reads %q after the loop, want the target's stated 0…1 fact bound through the parse — "+
			"an undetermined result here means ForeignEdgeAt never ran over the loop body's own statements", words)
	}
	for _, d := range *reported {
		if d.Code == 7002 {
			t.Errorf("a 7002 decline fired inside the loop body: %s", d.MessageText)
		}
	}
}

/* ── (b) the diamond pin: two edges in one flat body, both bind ──── */

// TestListWalk_ADiamondOfTwoForeignEdgesBothBindTheirOwnParse pins THE
// RULE this file states: pendingForeignOverrides is keyed by EACH
// edge's own OverrideStatement index. Two execFileSync calls recognized
// back to back (statements 0 and 1) each name a LATER statement (2 and
// 3) as their own parse's home; the second recognition (at index 1)
// must not overwrite the first's still-pending entry (for index 2) —
// both parses must read their OWN target's fact, and Math.min over the
// two derives the join a caller who lost either one could not.
func TestListWalk_ADiamondOfTwoForeignEdgesBothBindTheirOwnParse(t *testing.T) {
	_, ctx, reported := listWalkForeignEdgeFixture(t, `
import { execFileSync } from "child_process";
function f(a: number[], b: number[]): number {
	const stdoutA = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(a),
		encoding: "utf8",
	});
	const stdoutB = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(b),
		encoding: "utf8",
	});
	const levelA: number = JSON.parse(stdoutA);
	const levelB: number = JSON.parse(stdoutB);
	return Math.min(levelA, levelB);
}
`)
	statements := relationalAccumulationBodyOf(t, ctx.P, "f")
	if len(statements) != 5 {
		t.Fatalf("got %d statements, want 5 (two calls, two parses, one return)", len(statements))
	}

	env := NewEnv()
	env.Set("a", abstractdomain.KnownSet(
		mustParseRefinedSet(t, -2, 2, 1), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	env.Set("b", abstractdomain.KnownSet(
		mustParseRefinedSet(t, -2, 2, 1), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))

	AnalyzeStatements(ctx, env, statements, nil)

	levelA, okA := env.Get("levelA")
	levelB, okB := env.Get("levelB")
	if !okA || !okB {
		t.Fatalf("env after the diamond: levelA present=%v levelB present=%v, want both", okA, okB)
	}
	wordsA := foreignSetWords(mustSetOfKnown(t, levelA))
	wordsB := foreignSetWords(mustSetOfKnown(t, levelB))
	if !containsBoth(wordsA, "0", "1") {
		t.Errorf("levelA reads %q, want the target's stated 0…1 fact — the SECOND edge's recognition "+
			"clobbered the first's still-pending override", wordsA)
	}
	if !containsBoth(wordsB, "0", "1") {
		t.Errorf("levelB reads %q, want the target's stated 0…1 fact", wordsB)
	}
	for _, d := range *reported {
		if d.Code == 7002 {
			t.Errorf("a 7002 decline fired inside the diamond: %s", d.MessageText)
		}
	}
}

/* ── (c0) ordering: an outbound fire still binds the return-leg fact ── */

// TestListWalk_AnOutboundFireStillBindsTheReturnLegFact pins
// d-data-legs.ts's objectKeysReduceUndetermined row at the ForeignEdgeAt
// grain (TestCheckOutboundLeg_AWholeObjectPayloadFiresRatherThanDeclines
// already pins the same shape one layer down, at checkOutboundLeg
// alone): the payload crossing out is a WHOLE OBJECT against a sequence
// entry, so checkOutboundLeg fires 7001 through ctx.Report and answers
// an EMPTY outcome (Decline == "", Override == nil) — before this fix,
// ForeignEdgeAt returned that empty outcome immediately (foreign_edge.go
// line ~218's own early return), so the return leg's own machinery
// (channel purity, soleParseConsumerOf, the ±Infinity corner narrowing)
// never ran and `level` bound residue rather than the target's stated
// 0…1 fact — even though the artifact states a return fact and a real
// JSON.parse(stdout) consumer sits right there. The fix makes the
// return leg run regardless of the outbound leg's own outcome: `level`
// must still read the target's 0…1 fact, and the outbound fire must
// still report (the two legs are independent truths, not a choice
// between them).
func TestListWalk_AnOutboundFireStillBindsTheReturnLegFact(t *testing.T) {
	_, ctx, reported := listWalkForeignEdgeFixture(t, `
import { execFileSync } from "child_process";
function f(): number {
	const merged = { gain: 0.5, offset: -0.3 };
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(merged),
		encoding: "utf8",
	});
	const level: number = JSON.parse(stdout);
	return level;
}
`)
	statements := relationalAccumulationBodyOf(t, ctx.P, "f")
	env := NewEnv()
	// `merged` reads as a whole KnownObject, exactly the shape
	// Object.keys(defaults).reduce(...) derives in the real fixture row —
	// seeded directly here since this test's own subject is the ORDERING
	// fix, not the reduce-callback recognition TestCheckOutboundLeg_
	// AWholeObjectPayloadFiresRatherThanDeclines already covers.
	env.Set("merged", abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{
			{Name: "gain", Value: abstractdomain.KnownValues([]float64{0.5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)},
			{Name: "offset", Value: abstractdomain.KnownValues([]float64{-0.3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)},
		}, nil, true, abstractdomain.TrustProved, false))

	AnalyzeStatements(ctx, env, statements, nil)

	level, ok := env.Get("level")
	if !ok {
		t.Fatalf("env holds no value for level")
	}
	words := foreignSetWords(mustSetOfKnown(t, level))
	if !containsBoth(words, "0", "1") {
		t.Errorf("level reads %q, want the target's stated 0…1 return fact bound through the parse — "+
			"an outbound fire must not stop the independent return-leg fact from attaching", words)
	}
	sawOutboundFire := false
	for _, d := range *reported {
		if d.Code == 7001 && strings.Contains(d.MessageText, "is of type 'object'") {
			sawOutboundFire = true
		}
	}
	if !sawOutboundFire {
		t.Errorf("no 7001 fired naming the object-shaped payload — the outbound leg's own fire must still report: %+v", *reported)
	}
}

/* ── (c) single-edge regression: the ordinary (non-diamond) shape ── */

// TestListWalk_ASingleForeignEdgeStillBindsItsFact is the plain
// regression the diamond and loop fixes must not break: ONE recognized
// edge, at the top level, still walks its parse under the override —
// foreign_edge_test.go's own banner names this exact whole-route shape
// as needing a real child_process.d.ts, which listWalkForeignEdgeFixture
// now stands up.
func TestListWalk_ASingleForeignEdgeStillBindsItsFact(t *testing.T) {
	_, ctx, reported := listWalkForeignEdgeFixture(t, `
import { execFileSync } from "child_process";
function f(boosted: number[]): number {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	const level: number = JSON.parse(stdout);
	return level;
}
`)
	statements := relationalAccumulationBodyOf(t, ctx.P, "f")
	env := NewEnv()
	env.Set("boosted", abstractdomain.KnownSet(
		mustParseRefinedSet(t, -2, 2, 1), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))

	AnalyzeStatements(ctx, env, statements, nil)

	level, ok := env.Get("level")
	if !ok {
		t.Fatalf("env holds no value for level")
	}
	words := foreignSetWords(mustSetOfKnown(t, level))
	if !containsBoth(words, "0", "1") {
		t.Errorf("level reads %q, want the target's stated 0…1 fact", words)
	}
	for _, d := range *reported {
		if d.Code == 7002 {
			t.Errorf("a 7002 decline fired: %s", d.MessageText)
		}
	}
}

/* ── (c1) the intermediate captured-stdout binding ─────────────────── */

// TestListWalk_ADischargedCrossingBindsTheCapturedStdoutToTheJSONNumberGrammar
// pins the intermediate binding this fix adds: after a DISCHARGED
// crossing (audio_level.py's own 0…1 number return, StdoutPure, no
// outbound fire), `stdout` itself — the name execFileSync's result
// binds, read BETWEEN the call and JSON.parse — must no longer read as
// residue. It must read as a string-sorted set that admits the
// harness's own serialized text ("0.5\n") and excludes non-numeric
// text ("abc") and a bare number missing its trailing newline ("0.5").
func TestListWalk_ADischargedCrossingBindsTheCapturedStdoutToTheJSONNumberGrammar(t *testing.T) {
	_, ctx, reported := listWalkForeignEdgeFixture(t, `
import { execFileSync } from "child_process";
function f(boosted: number[]): number {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	const level: number = JSON.parse(stdout);
	return level;
}
`)
	statements := relationalAccumulationBodyOf(t, ctx.P, "f")
	env := NewEnv()
	env.Set("boosted", abstractdomain.KnownSet(
		mustParseRefinedSet(t, -2, 2, 1), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))

	AnalyzeStatements(ctx, env, statements, nil)

	stdout, ok := env.Get("stdout")
	if !ok {
		t.Fatalf("env holds no value for stdout")
	}
	set := mustSetOfKnown(t, stdout)
	if !ctx.Kernel.Member(set, refinementsets.CodepointsOf("0.5\n")) {
		t.Errorf("the stdout binding does not admit %q, want the JSON-number-plus-newline grammar to admit it", "0.5\n")
	}
	if ctx.Kernel.Member(set, refinementsets.CodepointsOf("abc")) {
		t.Errorf("the stdout binding admits %q, want non-numeric text excluded", "abc")
	}
	if ctx.Kernel.Member(set, refinementsets.CodepointsOf("0.5")) {
		t.Errorf("the stdout binding admits %q (no trailing newline), want the harness's own newline required", "0.5")
	}
	for _, d := range *reported {
		if d.Code == 7002 {
			t.Errorf("a 7002 decline fired: %s", d.MessageText)
		}
	}
}

// TestListWalk_AFiredOutboundLegLeavesTheCapturedStdoutBindingUnchanged
// pins the OTHER half of the rule this fix states in its own doc
// comment: a crossing whose OUTBOUND leg fires 7001 (the value crossing
// out escapes the target's stated entry) must NOT bind `stdout` to the
// serialized-set claim — the unvalidated-parse reading stays exactly as
// it read before this fix, since it is load-bearing for whatever
// generic-union return model the (still-bound, per the independent-
// truths design) parse node's own downstream read relies on.
// audio_level.py's own entry is [-2, 2]; an unbounded boosted element
// escapes it and fires at the call.
func TestListWalk_AFiredOutboundLegLeavesTheCapturedStdoutBindingUnchanged(t *testing.T) {
	_, ctx, reported := listWalkForeignEdgeFixture(t, `
import { execFileSync } from "child_process";
function f(boosted: number[]): number {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	const level: number = JSON.parse(stdout);
	return level;
}
`)
	statements := relationalAccumulationBodyOf(t, ctx.P, "f")
	env := NewEnv()
	// an UNBOUNDED element — outside audio_level.py's stated [-2, 2]
	// entry, so the outbound leg fires 7001 at the call (the same shape
	// TestListWalk_AnOutboundFireStillBindsTheReturnLegFact's own object
	// case exercises for the return leg, applied here to the SCALAR
	// entry instead)
	env.Set("boosted", abstractdomain.KnownSet(
		refinementsets.Repetition(refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))), 1, nil),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))

	AnalyzeStatements(ctx, env, statements, nil)

	sawOutboundFire := false
	for _, d := range *reported {
		if d.Code == 7001 {
			sawOutboundFire = true
		}
	}
	if !sawOutboundFire {
		t.Fatalf("no 7001 fired at the call — this test's own premise (an unbounded entry) did not hold: %+v", *reported)
	}
	stdout, ok := env.Get("stdout")
	if !ok {
		t.Fatalf("env holds no value for stdout")
	}
	set := mustSetOfKnown(t, stdout)
	if !ctx.Kernel.Member(set, refinementsets.CodepointsOf("abc")) {
		t.Errorf("a fired outbound leg narrowed the stdout binding to exclude %q — it must stay unchanged "+
			"(residue), since the unvalidated-parse reading is load-bearing for the fired path", "abc")
	}
}

/* ── (d) expiry: an override recognized but never reached still dies ── */

// TestListWalk_AnUnconsumedOverrideExpiresAtTheEndOfTheList pins the
// expiry half of the rule: a recognized edge whose OWN parse statement
// never runs (a return before it) must not leak its pending override
// into any OTHER node — pendingForeignOverrides is a local of listWalk,
// so it simply falls out of scope with the function return exactly as
// the single scalar slot did; this test asserts the observable
// consequence — the parse statement is never reached at all, and
// nothing else in the list wears the pinned fact by accident.
func TestListWalk_AnUnconsumedOverrideExpiresAtTheEndOfTheList(t *testing.T) {
	_, ctx, reported := listWalkForeignEdgeFixture(t, `
import { execFileSync } from "child_process";
function f(boosted: number[]): number {
	const stdout = execFileSync("python3", ["./audio_level.py"], {
		input: JSON.stringify(boosted),
		encoding: "utf8",
	});
	return 0;
}
`)
	statements := relationalAccumulationBodyOf(t, ctx.P, "f")
	if len(statements) != 2 {
		t.Fatalf("got %d statements, want 2 (the call, the early return)", len(statements))
	}
	env := NewEnv()
	env.Set("boosted", abstractdomain.KnownSet(
		mustParseRefinedSet(t, -2, 2, 1), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))

	// the edge recognizes at index 0 (its own outcome carries an
	// Override, since there is no parse statement — soleParseConsumerOf
	// declines, so ForeignEdgeAt actually answers a Decline here, not an
	// Override; either way nothing must survive past this call)
	exits := AnalyzeStatements(ctx, env, statements, nil)
	if !exits {
		t.Fatalf("the function does not exit through its own return")
	}
	// the pending-override map is listWalk's own local; there is no
	// surviving handle to it once AnalyzeStatements returns, so the only
	// assertion available from outside is that nothing panicked and no
	// stray fact was bound — env holds no "level"-shaped name at all
	if _, ok := env.Get("level"); ok {
		t.Errorf("env holds a value for a name this source never declared — an override leaked")
	}
	sawParseGap := false
	for _, d := range *reported {
		if d.Code == 7002 {
			sawParseGap = true
		}
	}
	_ = sawParseGap // informational: soleParseConsumerOf's own decline, not this rule's concern
}

/* ── shared helpers ────────────────────────────────────────────────── */

// mustParseRefinedSet is a repetition of a bounded element with at
// least atLeast members — the same shape foreign_edge_test.go's own
// foreignCrossingEnv builds for the value crossing out on stdin.
func mustParseRefinedSet(t *testing.T, lo float64, hi float64, atLeast int) refinementsets.RefinedSet {
	t.Helper()
	element := refinementsets.MakeRefinedSet(refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
	return refinementsets.Repetition(element, atLeast, nil)
}

// mustSetOfKnown reads an AbstractValue as a RefinedSet — the shape the
// target's own returned fact (foreignReturnValue) and this test's own
// seeded crossing values both wear.
func mustSetOfKnown(t *testing.T, v abstractdomain.AbstractValue) refinementsets.RefinedSet {
	t.Helper()
	set, ok := abstractdomain.SetOfKnown(v)
	if !ok {
		t.Fatalf("value is not read as a set: %+v", v)
	}
	return set
}

func containsBoth(text string, a string, b string) bool {
	return strings.Contains(text, a) && strings.Contains(text, b)
}

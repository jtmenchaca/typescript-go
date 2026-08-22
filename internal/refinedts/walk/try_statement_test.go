// AnalyzeTryStatement's own two obligations, pinned directly:
//
//  1. the try body's OWN statement list walks the edge-serving path
//     every other block gets (listWalk, through the same route a plain
//     Block or a for body reaches) — a recognized cross-language call
//     inside try{} must bind its fact, not fall to residue.
//  2. the exception-path snapshot semantics: an exception can be
//     observed after any prefix of the try text, and the catch clause's
//     env is the join of what every one of those prefixes left behind.
//     StatementObserver (flow_context.go) is how the try walk collects
//     those prefixes now that the body walks through listWalk instead
//     of a hand-rolled per-statement loop — this file pins that the
//     join still reads exactly what the old loop's append-then-break
//     order produced.
//
// The finally-body shape (AnalyzeStatements over FinallyBlock,
// untouched by this fix) is pinned alongside as the plain regression.
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

/* ── (a) a recognized edge inside a try body binds its fact ──────── */

// TestAnalyzeTryStatement_AForeignEdgeInsideATryBodyBindsItsFact is the
// bisected defect itself: python3RecognizedInsideForLoop's own sibling
// shape, but wrapped in try{}/finally{} instead of a for body. Before
// the fix, the try body's hand-rolled loop called AnalyzeStatement per
// statement directly — never listWalk — so ForeignEdgeAt's only call
// site (listWalk) never ran over these statements, and the JSON.parse
// consumer read residue instead of the target's stated 0…1 fact.
//
// No catch clause: a catch's own entry havoc (the try TEXT's direct
// writes forgotten pending the snapshot join) is a SEPARATE mechanism
// from the one under test here — this fixture isolates the edge-
// binding fix alone. The exception-path join itself is pinned by
// TestAnalyzeTryStatement_TheCatchEnvJoinsEveryTryPrefixSnapshot below.
func TestAnalyzeTryStatement_AForeignEdgeInsideATryBodyBindsItsFact(t *testing.T) {
	_, ctx, reported := listWalkForeignEdgeFixture(t, `
import { execFileSync } from "child_process";
function f(boosted: number[]): number {
	let last = 0;
	try {
		const stdout = execFileSync("python3", ["./audio_level.py"], {
			input: JSON.stringify(boosted),
			encoding: "utf8",
		});
		const level: number = JSON.parse(stdout);
		last = level;
	} finally {
		// no-op: exercises the finally path alongside, at no cost
	}
	return last;
}
`)
	fn := entryEnvFunctionNamed(t, ctx.P, "f")
	outer := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	tryStatement := outer[1]
	if !ast.IsTryStatement(tryStatement) {
		t.Fatalf("outer[1] is not a try statement: %v", tryStatement.Kind)
	}

	env := NewEnv()
	env.Set("boosted", abstractdomain.KnownSet(
		mustParseRefinedSet(t, -2, 2, 1), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))

	// walk the whole function body, exactly as an ordinary check would —
	// `let last = 0;` seeds the tracked binding, then the try statement
	// itself walks through AnalyzeTryStatement
	AnalyzeStatements(ctx, env, outer, nil)

	last, ok := env.Get("last")
	if !ok {
		t.Fatalf("env holds no value for last after the try statement")
	}
	words := foreignSetWords(mustSetOfKnown(t, last))
	if !containsBoth(words, "0", "1") {
		t.Errorf("last reads %q after the try statement, want the target's stated 0…1 fact — an undetermined "+
			"result here means ForeignEdgeAt never ran over the try body's own statements", words)
	}
	for _, d := range *reported {
		if d.Code == 7002 {
			t.Errorf("a 7002 decline fired inside the try body: %s", d.MessageText)
		}
	}
}

/* ── (b) the exception-path snapshot join ─────────────────────────── */

// analyzeTryStatementTestContext is a plain, kernel-free FlowContext,
// following relational_accumulation_test.go's own recipe: these two
// tests read the join over snapshots directly, no cross-language
// recognition and no kernel ask involved.
func analyzeTryStatementTestContext(p *program.CheckerProgram) *FlowContext {
	return &FlowContext{
		P:         p,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
}

// TestAnalyzeTryStatement_TheCatchEnvJoinsEveryTryPrefixSnapshot pins
// the snapshot semantics the old hand-rolled loop existed for, now
// produced by StatementObserver: the try body writes `x` three times in
// a row, and the catch clause must see x holding the JOIN of all three
// post-statement states (0, then 1, then 2) — not just the last one,
// and not just the pre-try state. A join that collapsed to a single
// snapshot (or to none) would read x as strictly 2 or as unknown in
// the catch; the real semantics is "the exception could have come out
// after any prefix," so x in the catch holds {0, 1, 2}.
func TestAnalyzeTryStatement_TheCatchEnvJoinsEveryTryPrefixSnapshot(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): number {
	let x = 0;
	let seenInCatch = 0;
	try {
		x = 0;
		x = 1;
		x = 2;
	} catch {
		seenInCatch = x;
	}
	return seenInCatch;
}
`)
	ctx := analyzeTryStatementTestContext(p)
	fn := entryEnvFunctionNamed(t, ctx.P, "f")
	statements := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	if len(statements) != 4 {
		t.Fatalf("got %d statements, want 4 (the two lets, the try, the return)", len(statements))
	}
	tryStatement := statements[2]
	if !ast.IsTryStatement(tryStatement) {
		t.Fatalf("statements[2] is not a try statement: %v", tryStatement.Kind)
	}

	env := NewEnv()
	env.Set("x", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	env.Set("seenInCatch", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))

	AnalyzeTryStatement(ctx, env, tryStatement, nil)

	seenInCatch, ok := env.Get("seenInCatch")
	if !ok {
		t.Fatalf("env holds no value for seenInCatch after the try statement")
	}
	// three distinct numeric writes join to a set (JoinKnown's own
	// numeric-union path), not a single KindValues literal — read it the
	// same way the foreign-edge listwalk tests read a joined numeric
	// fact: through SetOfKnown and the diagnostic formatter.
	set := mustSetOfKnown(t, seenInCatch)
	words := foreignSetWords(set)
	if !strings.Contains(words, "0") || !strings.Contains(words, "1") || !strings.Contains(words, "2") {
		t.Errorf("seenInCatch reads %q, want the join of every try-prefix snapshot {0, 1, 2} — a join that "+
			"collapsed to the last snapshot alone, or to none, means StatementObserver did not fire once per "+
			"try-body statement", words)
	}
}

// TestAnalyzeTryStatement_ACatchHavocedNameJoinsRatherThanReadsUnknown
// is the bisected defect itself: the pre-existing test's three writes
// (TestAnalyzeTryStatement_TheCatchEnvJoinsEveryTryPrefixSnapshot,
// above) are all plain-identifier literal assignments, so raisesNothing
// keeps every one of them out of the havoc set (statementsFromFirstThrowing
// finds nothing that can throw and drops the whole try body) — x is
// never havocked there, and the join loop's guard never fires for it.
//
// Here the bare call statement `g();` is the first statement
// raisesNothing marks as possibly-throwing (it is a CallExpression),
// so statementsFromFirstThrowing returns it AND every statement after
// it — including the two plain `x = 1;` / `x = 2;` literal assignments
// that follow. AssignedNamesDirect over that suffix puts x in
// `written`, so x IS havocked to Unknown at catch entry, even though
// x's own writes are themselves raise-free literals; the call three
// lines earlier is what puts them in the havoc window. (x is never
// assigned to a call's own result here, so its snapshots stay plain
// numbers — the call's possibly-NaN return value is not this test's
// concern, only whether the havoc'd name recovers via the join.)
//
// Before the fix, the join loop's `held.Kind == KindUnknown` guard then
// skipped x unconditionally (havoc always leaves that Kind), so the
// catch read x as bare Unknown. The fix makes the join run FOR
// havocked names: x in the catch must hold the join of the pre-try
// entry value (10) with every try-prefix snapshot (10, then 10 again
// after the call statement since x is untouched by it, then 1, then
// 2) — a proper joined set, not Unknown.
//
// The try body's own LAST statement is an unconditional `return`, so
// tryExits is true. That makes the continuing-state switch in
// AnalyzeTryStatement pick `case tryExits: ReplaceEnv(env, catchEnv)`
// — env after the call is exactly the catch clause's own join, not
// that join folded again with the try path's separate normal-
// completion state for the same name (a second, correct, and
// unrelated join this test is not about — AnalyzeTryStatement's
// `default` arm exists for exactly that other case).
func TestAnalyzeTryStatement_ACatchHavocedNameJoinsRatherThanReadsUnknown(t *testing.T) {
	p := entryEnvTestProgram(t, `
function g(): number {
	return 0;
}
function f(): number {
	let x = 10;
	let seenInCatch = 0;
	try {
		g();
		x = 1;
		x = 2;
		return seenInCatch;
	} catch {
		seenInCatch = x;
	}
	return seenInCatch;
}
`)
	ctx := analyzeTryStatementTestContext(p)
	fn := entryEnvFunctionNamed(t, ctx.P, "f")
	statements := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	if len(statements) != 4 {
		t.Fatalf("got %d statements, want 4 (the two lets, the try, the return)", len(statements))
	}
	tryStatement := statements[2]
	if !ast.IsTryStatement(tryStatement) {
		t.Fatalf("statements[2] is not a try statement: %v", tryStatement.Kind)
	}

	env := NewEnv()
	env.Set("x", abstractdomain.KnownValues([]float64{10}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	env.Set("seenInCatch", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))

	AnalyzeTryStatement(ctx, env, tryStatement, nil)

	// the try body's bare `g();` statement can raise per raisesNothing,
	// so x is havocked — read `env` directly is safe here because the
	// try body's own final `return` makes tryExits true, so the switch
	// selects catchEnv exactly (see the comment above the fixture).
	seenInCatch, ok := env.Get("seenInCatch")
	if !ok {
		t.Fatalf("env holds no value for seenInCatch after the try statement")
	}
	if seenInCatch.Kind == abstractdomain.KindUnknown {
		t.Fatalf("seenInCatch reads bare Unknown — the havoc-entry join never ran for the havocked name x; " +
			"want the join of the pre-try entry value (10) with every try-prefix snapshot, not the raw havoc residue")
	}
	// the pre-try entry value (10) must survive INTO the join: an
	// exception can fire before the try body's first statement runs at
	// all, so catch must see the entry value as one of the joined arms,
	// not just the post-statement snapshots.
	set := mustSetOfKnown(t, seenInCatch)
	words := foreignSetWords(set)
	if !strings.Contains(words, "10") {
		t.Errorf("seenInCatch reads %q, want the pre-try entry value 10 among the joined arms — an exception "+
			"before the try body's first statement leaves x at its entry value, which the join must carry in", words)
	}
	if !strings.Contains(words, "1") {
		t.Errorf("seenInCatch reads %q, want the post-`x = 1` snapshot's 1 among the joined arms", words)
	}
	if !strings.Contains(words, "2") {
		t.Errorf("seenInCatch reads %q, want the post-`x = 2` snapshot's 2 among the joined arms", words)
	}
}

/* ── (c) a name never written in the try keeps its entry value ────── */

// TestAnalyzeTryStatement_ANameNeverWrittenInTryKeepsItsEntryValue is
// the regression the fix must not disturb: a name the try body never
// assigns is never in `written`, never havocked, and the (now
// unconditional) join over its unchanged held value must still yield
// exactly that value — joining an untouched value against snapshots
// that all repeat it unchanged is a no-op, not a promotion to Unknown
// or a widened set.
//
// The try body's own LAST statement is an unconditional `return`, so
// tryExits is true and AnalyzeTryStatement's continuing-state switch
// picks `case tryExits: ReplaceEnv(env, catchEnv)` — env after the
// call reads exactly what the catch clause's own join produced, not
// that join folded again with the try path's unrelated normal-
// completion state for the same names (a second, correct join this
// test is not about — putting the `return` in the CATCH body instead
// would select tryEnv there, reading `seenInCatch` before the catch
// clause ever assigned it, which is a different bug in the test, not
// in AnalyzeTryStatement).
func TestAnalyzeTryStatement_ANameNeverWrittenInTryKeepsItsEntryValue(t *testing.T) {
	p := entryEnvTestProgram(t, `
function g(): number {
	return 0;
}
function f(): number {
	let untouched = 7;
	let x = 0;
	let seenInCatch = 0;
	try {
		x = g();
		return seenInCatch;
	} catch {
		seenInCatch = untouched;
	}
	return seenInCatch;
}
`)
	ctx := analyzeTryStatementTestContext(p)
	fn := entryEnvFunctionNamed(t, ctx.P, "f")
	statements := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	if len(statements) != 5 {
		t.Fatalf("got %d statements, want 5 (the three lets, the try, the return)", len(statements))
	}
	tryStatement := statements[3]
	if !ast.IsTryStatement(tryStatement) {
		t.Fatalf("statements[3] is not a try statement: %v", tryStatement.Kind)
	}

	env := NewEnv()
	env.Set("untouched", abstractdomain.KnownValues([]float64{7}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	env.Set("x", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	env.Set("seenInCatch", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))

	AnalyzeTryStatement(ctx, env, tryStatement, nil)

	seenInCatch, ok := env.Get("seenInCatch")
	if !ok {
		t.Fatalf("env holds no value for seenInCatch after the try statement")
	}
	if seenInCatch.Kind != abstractdomain.KindValues || len(seenInCatch.Values) != 1 || seenInCatch.Values[0] != 7 {
		t.Errorf("seenInCatch reads %+v, want exactly the literal 7 — `untouched` is never assigned in the try "+
			"body, so it must never be havocked, and the join over its unchanged value across every snapshot "+
			"must still read as the plain entry value, not Unknown or a widened set", seenInCatch)
	}
}

/* ── (d) the finally-body shape still binds (regression) ─────────── */

// TestAnalyzeTryStatement_AFinallyBodyStillWalksAndWrites is the plain
// regression this fix must not disturb: the finally block's own walk
// (AnalyzeStatements over FinallyBlock, untouched by this change) still
// runs and its write still lands on the continuing env.
func TestAnalyzeTryStatement_AFinallyBodyStillWalksAndWrites(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): number {
	let x = 0;
	try {
		x = 1;
	} finally {
		x = 2;
	}
	return x;
}
`)
	ctx := analyzeTryStatementTestContext(p)
	fn := entryEnvFunctionNamed(t, ctx.P, "f")
	statements := fn.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes
	if len(statements) != 3 {
		t.Fatalf("got %d statements, want 3 (the let, the try, the return)", len(statements))
	}
	tryStatement := statements[1]
	if !ast.IsTryStatement(tryStatement) {
		t.Fatalf("statements[1] is not a try statement: %v", tryStatement.Kind)
	}

	env := NewEnv()
	env.Set("x", abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))

	AnalyzeTryStatement(ctx, env, tryStatement, nil)

	x, ok := env.Get("x")
	if !ok {
		t.Fatalf("env holds no value for x after the try statement")
	}
	if x.Kind != abstractdomain.KindValues || len(x.Values) != 1 || x.Values[0] != 2 {
		t.Errorf("x reads %+v after the finally block, want exactly the literal 2 — the finally's own write "+
			"must land on the continuing env", x)
	}
}

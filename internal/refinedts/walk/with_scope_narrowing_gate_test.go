// The with-scope narrowing gate: a name read or tested inside a
// `with` body resolves against the SCOPE OBJECT, which a getter can
// answer differently at every read — so identifier evaluation and
// narrowing recording must not claim anything under a with scope.
//
// Companion to evaluate_expression.go's identifier arm (the
// NodeFlagsInWithStatement check ahead of the Infinity/NaN/undefined
// special cases and the tracked-env read) and assume_condition.go's
// assumeCondition (the NodeFlagsInWithStatement check ahead of the
// narrowing/difference-row/sum-row/length-guard machinery). Both
// gates test ast.NodeFlagsInWithStatement, the flag the binder stamps
// on every node whose ancestor is a WithStatement's own `statement`
// (internal/ast/nodeflags.go).
//
// No TS twin: with_statement.go's own banner is the port record for
// the with route as a whole (analyze_statement.ts routed a whole
// with statement to its unmodeled catch-all, which never walked the
// body at all).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// withGateIfConditionOf finds the with body's own `if` statement and
// hands back its condition node — the node under test, carrying
// NodeFlagsInWithStatement because its ancestor is the WithStatement's
// `statement`.
func withGateIfConditionOf(t *testing.T, statement *ast.Node) *ast.Node {
	t.Helper()
	with := statement.AsWithStatement()
	if with.Statement == nil {
		t.Fatalf("with statement has no body")
	}
	body := with.Statement
	if ast.IsBlock(body) {
		stmts := body.AsBlock().Statements.Nodes
		if len(stmts) == 0 {
			t.Fatalf("with body is empty")
		}
		body = stmts[0]
	}
	if !ast.IsIfStatement(body) {
		t.Fatalf("with body's first statement is not an if statement")
	}
	return body.AsIfStatement().Expression
}

// withGateStatedSingleton is a stated result set admitting exactly one
// number — the shape that makes an equality guard's exact value, if
// wrongly trusted, look like a determined pass.
func withGateStatedSingleton(v float64) *annotations.DeclaredRefinement {
	set := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{v}))
	return &annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}

// The identifier gate: a with-scoped read answers unknown residue
// even for a name the plain arm would otherwise special-case
// (Infinity), proving the with check runs BEFORE those special cases
// rather than being shadowed by them.
func TestWithScopeNarrowingGate_AWithScopedInfinityReadIsResidue(t *testing.T) {
	p := withReachTestProgram(t,
		"function f(obj: { Infinity: number }): number {\n"+
			"  with (obj) {\n"+
			"    return Infinity;\n"+
			"  }\n"+
			"  return 0;\n"+
			"}\n")
	statement := withReachStatementOf(t, p, "f")
	with := statement.AsWithStatement()
	body := with.Statement.AsBlock().Statements.Nodes[0]
	ret := body.AsReturnStatement().Expression
	if !ast.IsIdentifier(ret) || ret.Text() != "Infinity" {
		t.Fatalf("test setup: expected the return expression to be the identifier Infinity, got %v", ret.Kind)
	}
	if ret.Flags&ast.NodeFlagsInWithStatement == 0 {
		t.Fatalf("test setup: the Infinity identifier does not carry NodeFlagsInWithStatement")
	}
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	known := evaluateExpression(ctx, NewEnv(), ret)
	if known.Kind != abstractdomain.KindUnknown {
		t.Errorf("evaluateExpression(with-scoped Infinity) = %v, want KindUnknown — a with body's Infinity may be obj's own shadowing property", known.Kind)
	}
}

// The identifier gate also overrides a name the environment DOES
// track: a with body's read of a same-named binding may resolve to
// the scope object's property instead, so the tracked value must not
// be handed back.
func TestWithScopeNarrowingGate_AWithScopedReadIgnoresATrackedValue(t *testing.T) {
	p := withReachTestProgram(t,
		"function f(obj: { x: number }, x: number): number {\n"+
			"  with (obj) {\n"+
			"    return x;\n"+
			"  }\n"+
			"  return 0;\n"+
			"}\n")
	statement := withReachStatementOf(t, p, "f")
	with := statement.AsWithStatement()
	body := with.Statement.AsBlock().Statements.Nodes[0]
	ret := body.AsReturnStatement().Expression
	if ret.Flags&ast.NodeFlagsInWithStatement == 0 {
		t.Fatalf("test setup: the with-scoped x identifier does not carry NodeFlagsInWithStatement")
	}
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	env := NewEnv()
	// a value planted directly in the env, bypassing the door havoc, so
	// the gate under test is the identifier arm's own flag check, not
	// AnalyzeWithStatement's forgetting
	env.Set("x", abstractdomain.KnownValues([]float64{42}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	known := evaluateExpression(ctx, env, ret)
	if known.Kind != abstractdomain.KindUnknown {
		t.Errorf("evaluateExpression(with-scoped x) = %v (tracked value leaked through), want KindUnknown", known.Kind)
	}
}

// The narrowing gate, direct: assumeCondition on a with-scoped
// equality test must record NO narrowing — an equality guard
// ordinarily REPLACES an unknown binding with the exact tested value
// (narrowing.ApplyNarrowed's n.Exact arm), which is exactly the claim
// a with body's getter can falsify on the next read. Both branches
// must carry the entry env UNCHANGED, and no difference row records.
func TestWithScopeNarrowingGate_AWithScopedEqualityRecordsNoNarrowing(t *testing.T) {
	p := withReachTestProgram(t,
		"function f(obj: { x: number }, x: number): number {\n"+
			"  with (obj) {\n"+
			"    if (x === 40) {\n"+
			"      return x;\n"+
			"    }\n"+
			"  }\n"+
			"  return 0;\n"+
			"}\n")
	statement := withReachStatementOf(t, p, "f")
	condition := withGateIfConditionOf(t, statement)
	if condition.Flags&ast.NodeFlagsInWithStatement == 0 {
		t.Fatalf("test setup: the with-scoped condition does not carry NodeFlagsInWithStatement")
	}
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	env := NewEnv()
	env.Set("x", abstractdomain.Unknown)
	ifStmt := condition.Parent.AsIfStatement()
	assumed := assumeCondition(ctx, env, condition, AssumeConditionScope{
		WhenTrueScope:  ifStmt.ThenStatement,
		WhenFalseScope: statement,
		At:             condition,
	}, false, false)
	trueHeld, ok := assumed.WhenTrue.Env.Get("x")
	if !ok {
		t.Fatalf("assumeCondition's true-side env lost the binding x entirely")
	}
	if trueHeld.Kind == abstractdomain.KindValues {
		formatted, _ := abstractdomain.FormatAbstractValue(trueHeld)
		t.Errorf("assumeCondition(with-scoped x === 40).WhenTrue.Env[x] = %q, want unknown — the equality guard must not be trusted inside a with body", formatted)
	}
	if assumed.HeldConstraints != nil {
		t.Errorf("assumeCondition(with-scoped x === 40).HeldConstraints = %v, want nil — no row is recorded for a with-scoped condition", assumed.HeldConstraints)
	}
}

// The narrowing gate, control: the SAME equality guard OUTSIDE a with
// body DOES narrow — proving the gate above is with-specific, not a
// general regression in equality narrowing.
//
// Unlike every other test in this file, the control case reaches PAST
// the with gate into the ordinary narrowing.Narrowings call
// (assume_condition.go), which poses its equality question to the
// SEATED NARROW KERNEL (condition_analysis.go's narrowRefusable calls
// NarrowKernel().Narrow(tree)) — narrowKernelHolder's own comment:
// "Without one (a unit test that skipped setup), a condition narrows
// nothing — degraded loudly by the alerts that causes, never wrong."
// withReachContext's package comment ("no kernel... decided
// host-side") is right for the with-scoped tests around this one,
// which never reach the kernel call at all (the gate declines first),
// but the control case needs the kernel seated to observe narrowing
// actually firing — the same superArrayLoadKernel recipe
// super_and_array_ctor_test.go uses.
func TestWithScopeNarrowingGate_ThePlainEqualityControlDoesNarrow(t *testing.T) {
	kernel := superArrayLoadKernel(t)
	p := withReachTestProgram(t,
		"function f(x: number): number {\n"+
			"  if (x === 40) {\n"+
			"    return x;\n"+
			"  }\n"+
			"  return 0;\n"+
			"}\n")
	var conditionNode *ast.Node
	for _, s := range p.Entry.Statements.Nodes {
		if !ast.IsFunctionDeclaration(s) {
			continue
		}
		name := s.AsFunctionDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) || name.Text() != "f" {
			continue
		}
		body := s.Body().AsBlock().Statements.Nodes
		conditionNode = body[0].AsIfStatement().Expression
	}
	if conditionNode == nil {
		t.Fatalf("no function named f found")
	}
	if conditionNode.Flags&ast.NodeFlagsInWithStatement != 0 {
		t.Fatalf("test setup: the plain condition unexpectedly carries NodeFlagsInWithStatement")
	}
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	ctx.Kernel = kernel
	env := NewEnv()
	env.Set("x", abstractdomain.Unknown)
	ifStmt := conditionNode.Parent.AsIfStatement()
	assumed := assumeCondition(ctx, env, conditionNode, AssumeConditionScope{
		WhenTrueScope:  ifStmt.ThenStatement,
		WhenFalseScope: ifStmt.Parent,
		At:             conditionNode,
	}, false, false)
	trueHeld, ok := assumed.WhenTrue.Env.Get("x")
	if !ok {
		t.Fatalf("assumeCondition's true-side env lost the binding x entirely")
	}
	if trueHeld.Kind != abstractdomain.KindValues || len(trueHeld.Values) != 1 || trueHeld.Values[0] != 40 {
		formatted, _ := abstractdomain.FormatAbstractValue(trueHeld)
		t.Errorf("assumeCondition(plain x === 40).WhenTrue.Env[x] = %q, want the exact 40 — the control case must still narrow", formatted)
	}
}

// End to end: a return of a with-scoped name inside a with body's
// narrowed arm still fires against a stated singleton set — the
// narrowing gate must not let `x === 40` inside `with` quiet the
// alert the way it would if the guard were (wrongly) trusted. This
// is the a-statements.ts withStatement row's own shape (an
// expected-error return inside a with body), extended with a guard
// that would have made the value LOOK determined without the gate.
func TestWithScopeNarrowingGate_ANarrowedReturnInsideWithStillFires(t *testing.T) {
	p := withReachTestProgram(t,
		"function f(obj: { x: number }, x: number): number {\n"+
			"  with (obj) {\n"+
			"    if (x === 40) {\n"+
			"      return x;\n"+
			"    }\n"+
			"  }\n"+
			"  return 0;\n"+
			"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := withReachContext(p, &diagnostics)
	env := NewEnv()
	// AnalyzeWithStatement's own door havoc forgets x before the body
	// walks regardless of this plant (with_statement.go's
	// havocNamesMentioned) — x enters the if-condition unknown either
	// way, so what decides the outcome is purely whether assumeCondition
	// narrows it back to 40 from there
	statement := withReachStatementOf(t, p, "f")
	AnalyzeStatement(ctx, env, statement, withGateStatedSingleton(40))
	fired := false
	for _, d := range diagnostics {
		if d.Code == 7001 || d.Code == 7002 {
			fired = true
		}
	}
	if !fired {
		t.Errorf("no diagnostic fired for `return x` under a with-scoped `x === 40` guard against a stated {40} set; the walk must not claim x is provably 40 inside a with body")
	}
}

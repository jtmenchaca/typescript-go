// Pins InlineStoredClosure's own door — a closure STORED IN A CONST
// and called by name, resolved through the walk route rather than a
// registered contract. contract_file_facts.go's VariableDeclaration
// case only registers a const whose initializer IS an arrow/function
// expression directly; a const initialized by a FACTORY CALL (`const
// add = makeAdder()`) registers nothing under add's own symbol, so
// ContractOf(ctx, addCallee) answers nil at every call site — the
// resolving mechanism is InlineStoredClosure's own
// FactoryPinnedFunction branch (inliner.go), which reads the factory's
// body, finds its one returned closure, and treats add(...) as a call
// of that closure directly.
//
// This test asserts BOTH ends of the claim: ContractOf finds nothing
// for add's callee (so InlineStoredClosure, not the contract-inline
// route, is genuinely what resolves this call), and the call's own
// evaluated value is the exact scalar the closure's body computes —
// not merely "no diagnostic," which an Unknown value would also pass.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

const inlineStoredClosureFactoryConstSource = "function makeAdder(): (age: number) => number {\n" +
	"  return (age: number) => age + 1;\n" +
	"}\n" +
	"function callFactoryConst(): number {\n" +
	"  const add = makeAdder();\n" +
	"  return add(10);\n" +
	"}\n"

// TestInlineStoredClosure_FactoryConstResolvesThroughTheStoredClosureDoor
// is the door check: add's callee resolves to no contract at all
// (ContractOf answers nil), which is what forces
// EvaluateCallExpression's own routing to try InlineStoredClosure
// rather than the contract-inline path — the same fork
// AGENT-BRIEF.md's routing facts describe.
func TestInlineStoredClosure_FactoryConstResolvesThroughTheStoredClosureDoor(t *testing.T) {
	p := entryEnvTestProgram(t, inlineStoredClosureFactoryConstSource)
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "callFactoryConst")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	callExpr := returned.AsReturnStatement().Expression
	if !ast.IsCallExpression(callExpr) {
		t.Fatalf("callFactoryConst's return is not a call expression")
	}
	calleeExpression := callExpr.AsCallExpression().Expression
	if contract := ContractOf(ctx, calleeExpression); contract != nil {
		t.Fatalf("ContractOf found a registered contract for add's callee — this test no longer exercises InlineStoredClosure's own door, it exercises the contract-inline route instead")
	}
}

// TestInlineStoredClosure_FactoryConstCallEvaluatesExactly is the
// value check: add(10) must evaluate to the exact scalar 11, read off
// the closure the factory returned — not Unknown, which would also
// leave no diagnostic on a wide-window read and could hide a silent
// regression to the unmodeled-call fallback.
func TestInlineStoredClosure_FactoryConstCallEvaluatesExactly(t *testing.T) {
	p := entryEnvTestProgram(t, inlineStoredClosureFactoryConstSource)
	ctx := superArrayContracts(t, p)
	fn := entryEnvFunctionNamed(t, p, "callFactoryConst")
	returned := superArrayFirstNode(t, fn.Body(), "return statement", ast.IsReturnStatement)
	callExpr := returned.AsReturnStatement().Expression
	if !ast.IsCallExpression(callExpr) {
		t.Fatalf("callFactoryConst's return is not a call expression")
	}

	// add's own local binding must be seeded before the call reads it —
	// walk the whole function body through AnalyzeFunction so `const add
	// = makeAdder()` actually runs first, exactly as the fixture shape
	// requires (a bare evaluateExpression of the call alone would find
	// no "add" in env at all).
	var sink []abstractdomain.AbstractValue
	bodyCtx := *ctx
	bodyCtx.ReturnSink = &sink
	contract := &FunctionContract{
		Declaration: fn,
		Grounded:    false,
	}
	AnalyzeFunction(&bodyCtx, contract, nil)
	if len(sink) == 0 {
		t.Fatalf("AnalyzeFunction's ReturnSink caught nothing — the return statement never ran")
	}
	value := sink[0]
	for _, v := range sink[1:] {
		value = abstractdomain.JoinKnown(value, v)
	}
	if value.Kind != abstractdomain.KindValues || len(value.Values) != 1 || value.Values[0] != 11 {
		spelled, _ := abstractdomain.FormatAbstractValue(value)
		t.Errorf("add(10) = %q, want the exact scalar 11", spelled)
	}
}

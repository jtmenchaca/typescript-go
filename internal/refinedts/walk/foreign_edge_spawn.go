// The spawn (async) return leg: the accumulate-then-parse `.on()` pair
// a spawn call's own recognition still needs to find, even though
// nothing here yet serves the fact it names (no callback-body walk
// route exists — see spawnAsyncEdgeOf's own doc). No Python twin: the
// Python side's subprocess.Popen async equivalent recognizes through
// its own two-statement Popen/.communicate() pair rather than a
// Node-style event-emitter callback shape.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// spawnAsyncEdgeOf is `spawn(<runner>, <argv>)` — async, no captured
// return value at the call site itself. The reference (the argv naming
// the script) is exactly as followable as execFileSync's; the call
// itself is recognized on that basis alone.
//
// What the call ALONE cannot yet determine is the return leg, and this
// answers "does not determine (yet)", never "cannot": an accumulate-
// then-parse spawn body —
//
//	const child = spawn(<runner>, <argv>);
//	let out = "";
//	child.stdout.on("data", (d) => { out += d; });
//	child.on("close", () => { const result = JSON.parse(out); ... });
//
// — DOES deterministically name both the file that runs next (the
// argv, exactly as execFileSync's does) and the expression the target's
// fact would attach to (the JSON.parse inside the close handler): nothing
// about the shape is dynamic or unbounded. spawnReturnLegOf (below) now
// reads exactly that pair out of the statements following this call —
// the accumulator name and the parse node are RECOGNIZED. What still
// blocks serving is not the recognition: it is a way for listWalk's
// override (analyze_statement.go's `foreignOverrideAt`) to reach a node
// INSIDE a callback body rather than only a top-level statement, since
// the override today pins the whole statement CONTAINING the parse, and
// here that statement is the `.on("close", cb)` call, not the parse
// expression itself — walking that statement has to actually enter the
// callback body for the pinned node to ever be visited, and no
// callback-body-walking route exists for an arbitrary `.on()` handler
// today (only Promise .then/.catch handlers inline, via
// promiseRunHandler/InlineCallback in promise_instance_models.go). That
// route is analyze_statement.go's to build, outside this file's
// ownership; a body this reader cannot recognize at all (no pair, a
// mismatched parameter, an intervening write) still owes its own,
// narrower sentence naming exactly what breaks the recognition itself.
func spawnAsyncEdgeOf(
	ctx *FlowContext, call *ast.Node, name string, statements []*ast.Node, index int,
) (*ForeignEdge, bool, string, *ast.Node, string) {
	args, _ := callArguments(call)
	if len(args) < 2 {
		return nil, false, "", nil, ""
	}
	runnerWord, script, _, scriptOk, sentence, sentenceNode := runnerAndScriptArgvOf(ctx, args[0], args[1])
	if sentence != "" {
		return nil, false, sentence, sentenceNode, ""
	}
	if !scriptOk {
		return nil, false, "", nil, ""
	}
	resolvedPath, pathSentence := resolveForeignScriptPath(call, runnerWord, script)
	if pathSentence != "" {
		return nil, false, pathSentence, call, resolvedPath
	}
	if resolvedPath == "" {
		return nil, false, "", nil, ""
	}
	leg, legSentence := spawnReturnLegOf(statements, index, name)
	if legSentence == "" {
		// the pair IS recognized (the accumulator and the parse node are
		// both named) — what remains is the callback-body walk route, not
		// this reader's own gap
		return nil, false, "this call runs " + runnerWord + " on " + script + " with spawn — the argv names " +
			"the script exactly as execFileSync's does, and the accumulate-then-parse pair after it is " +
			"recognized (the accumulator " + leg.AccumulatorName + " and its JSON.parse are both named), " +
			"but this checker has no route that walks a callback body to attach a fact to a node inside " +
			"it — the edge is recognized and not yet served",
			call, resolvedPath
	}
	return nil, false, "this call runs " + runnerWord + " on " + script + " with spawn — the argv names " +
		"the script exactly as execFileSync's does, but its result does not determine (yet): " + legSentence,
		call, resolvedPath
}

/* ── the spawn (async) return leg: the accumulate-then-parse `.on()` pair ── */

// spawnReturnLegOf scans the statements AFTER a `spawn(...)` call bound
// to childName for the accumulate-then-parse pair —
//
//	<childName>.stdout.on("data", (d) => { out += d; });
//	<childName>.on("close", () => { ... JSON.parse(out) ... });
//
// — the same "scan the statements after this one" discipline
// soleParseConsumerOf already applies, one level deeper: past finding
// the two `.on()` calls, it steps INTO each handler's body to read the
// accumulator name (spawnAccumulatorNameOf) and the parse node
// (spawnParseNodeOf) the same way soleParseConsumerOf reads a top-level
// JSON.parse.
//
// Answers (leg, "") when the whole pair recognizes; ("", said)
// otherwise, naming the first construct that blocks it. A missing
// 'close' handler, a 'data' handler whose accumulator write does not
// read the handler's OWN parameter, or a write to the accumulator by a
// THIRD statement outside the two handlers all decline by name here —
// none of them are silent.
func spawnReturnLegOf(statements []*ast.Node, index int, childName string) (SpawnReturnLeg, string) {
	dataHandler, dataOk := spawnOnHandlerOf(statements, index, childName, true, "data")
	if !dataOk {
		return SpawnReturnLeg{}, "no " + childName + ".stdout.on(\"data\", ...) handler follows the call, " +
			"so there is no accumulator for a return fact to attach through"
	}
	accumulatorName, accumulatorOk := spawnAccumulatorNameOf(dataHandler)
	if !accumulatorOk {
		return SpawnReturnLeg{}, "the 'data' handler's body is not a single `<name> += <chunk>` statement " +
			"adding its own parameter into an outer name, so no accumulator is named"
	}
	closeHandler, closeOk := spawnOnHandlerOf(statements, index, childName, false, "close")
	if !closeOk {
		return SpawnReturnLeg{}, "no " + childName + ".on(\"close\", ...) handler follows the call, so " +
			"nothing reads " + accumulatorName + " back as the target's result"
	}
	parseNode, parseOk := spawnParseNodeOf(closeHandler, accumulatorName)
	if !parseOk {
		return SpawnReturnLeg{}, "the 'close' handler's body does not read " + accumulatorName +
			" through JSON.parse, so the target's stated result has no expression to land on"
	}
	// the intervening-write hazard: the 'data' handler's OWN `+=` is part
	// of the recognized shape, not a disqualifying write — every OTHER
	// statement between the call and the 'close' handler, and every
	// statement inside the 'close' handler's own body, must leave the
	// accumulator untouched
	if spawnAccumulatorWriteOutsideHandlers(statements, index, accumulatorName, dataHandler) {
		return SpawnReturnLeg{}, "a statement other than the 'data' handler writes " + accumulatorName +
			" after the call, so the value the 'close' handler parses is not the value the two " +
			"handlers alone accumulated"
	}
	return SpawnReturnLeg{
		AccumulatorName: accumulatorName,
		DataHandler:     dataHandler,
		CloseHandler:    closeHandler,
		ParseNode:       parseNode,
	}, ""
}

// spawnOnHandlerOf finds `<childName>.on(event, cb)` — or, when
// throughStdout is true, `<childName>.stdout.on(event, cb)` — among the
// expression-statement calls following index, and answers cb. These
// calls are bare expression statements (EventEmitter#on returns the
// emitter for chaining, but nothing here reads that return value), so
// they never enter constBoundCallOf's const-bound switch; this reads
// the shape directly.
func spawnOnHandlerOf(
	statements []*ast.Node, index int, childName string, throughStdout bool, event string,
) (*ast.Node, bool) {
	for _, statement := range statements[index+1:] {
		if !ast.IsExpressionStatement(statement) {
			continue
		}
		call := Unwrapped(statement.AsExpressionStatement().Expression)
		if call == nil || !ast.IsCallExpression(call) {
			continue
		}
		expr := call.AsCallExpression()
		callee := Unwrapped(expr.Expression)
		if callee == nil || !ast.IsPropertyAccessExpression(callee) {
			continue
		}
		access := callee.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) || access.Name().Text() != "on" {
			continue
		}
		receiver := Unwrapped(access.Expression)
		if receiver == nil {
			continue
		}
		if throughStdout {
			if !ast.IsPropertyAccessExpression(receiver) {
				continue
			}
			stdoutAccess := receiver.AsPropertyAccessExpression()
			if stdoutAccess.QuestionDotToken != nil || !ast.IsIdentifier(stdoutAccess.Name()) ||
				stdoutAccess.Name().Text() != "stdout" {
				continue
			}
			receiver = Unwrapped(stdoutAccess.Expression)
			if receiver == nil {
				continue
			}
		}
		if !ast.IsIdentifier(receiver) || receiver.Text() != childName {
			continue
		}
		if expr.Arguments == nil || len(expr.Arguments.Nodes) != 2 {
			continue
		}
		eventWord, eventOk := stringLiteralText(expr.Arguments.Nodes[0])
		if !eventOk || eventWord != event {
			continue
		}
		handler := Unwrapped(expr.Arguments.Nodes[1])
		if handler == nil || (!ast.IsArrowFunction(handler) && !ast.IsFunctionExpression(handler)) {
			continue
		}
		return handler, true
	}
	return nil, false
}

// spawnAccumulatorNameOf reads the 'data' handler's body as exactly one
// statement, `<name> += <param>;`, where the right side is a bare read
// of the handler's OWN FIRST PARAMETER — the specific parameter, never
// a same-spelled identifier from an outer scope: a handler whose body
// happens to add some other in-reach `d` is not this shape, so the
// check is the parameter DECLARATION node's own identity (by position),
// not a name-equality test against the parameter's spelled text.
func spawnAccumulatorNameOf(handler *ast.Node) (string, bool) {
	parameters := handler.Parameters()
	if len(parameters) != 1 {
		return "", false
	}
	parameterName := parameters[0].AsParameterDeclaration().Name()
	if parameterName == nil || !ast.IsIdentifier(parameterName) {
		return "", false
	}
	body := StatementsOf(handler.Body())
	if len(body) != 1 || !ast.IsExpressionStatement(body[0]) {
		return "", false
	}
	add := Unwrapped(body[0].AsExpressionStatement().Expression)
	if add == nil || !ast.IsBinaryExpression(add) {
		return "", false
	}
	bin := add.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindPlusEqualsToken {
		return "", false
	}
	target := Unwrapped(bin.Left)
	if target == nil || !ast.IsIdentifier(target) {
		return "", false
	}
	// the accumulator must be a DIFFERENT binding than the parameter —
	// summing the chunk into itself writes back nothing an outer scope
	// can read
	if target.Text() == parameterName.Text() {
		return "", false
	}
	right := Unwrapped(bin.Right)
	if right == nil || !ast.IsIdentifier(right) || right.Text() != parameterName.Text() {
		return "", false
	}
	return target.Text(), true
}

// spawnParseNodeOf finds the sole `JSON.parse(<accumulatorName>)` node
// inside the 'close' handler's body — the same read isForeignParseOf
// already performs for a bare stdout binding, applied to the accumulator
// name instead. Unlike soleParseConsumerOf's top-level scan, this reads
// exactly ONE handler body rather than a run of statements, so there is
// no "two or more consumers" question here: the handler either contains
// one parse of the name or it does not.
func spawnParseNodeOf(closeHandler *ast.Node, accumulatorName string) (*ast.Node, bool) {
	body := closeHandler.Body()
	if body == nil {
		return nil, false
	}
	var found *ast.Node
	count := 0
	foreignParseCallsIn(body, accumulatorName, &found, &count)
	if count != 1 {
		return nil, false
	}
	return found, true
}

// spawnAccumulatorWriteOutsideHandlers is the intervening-write hazard
// check: whether any statement OTHER than the 'data' handler's own
// recognized `+=` writes the accumulator between the spawn call and the
// end of the scan — a statement in between the two handlers, a
// statement after the 'close' handler in the same list, or a write
// inside the 'close' handler's own body (AssignedNamesDirect walks INTO
// a callback literal same as any other subtree, so a write inside
// CloseHandler's body is already covered by the same scan). The 'data'
// handler's own statement is the one excluded: its `+=` is the
// recognized shape, not a disqualifying write.
func spawnAccumulatorWriteOutsideHandlers(
	statements []*ast.Node, index int, accumulatorName string, dataHandler *ast.Node,
) bool {
	written := map[string]struct{}{}
	for _, statement := range statements[index+1:] {
		if spawnStatementIsHandlerFor(statement, dataHandler) {
			continue
		}
		AssignedNamesDirect(statement, written)
	}
	_, moves := written[accumulatorName]
	return moves
}

// spawnStatementIsHandlerFor is whether statement is the bare
// expression-statement call that hands handler as one of its arguments
// — the recognized `.on(...)` statement itself, excluded from the
// write scan because its own `+=` is the shape being recognized, not a
// write to disqualify it.
func spawnStatementIsHandlerFor(statement *ast.Node, handler *ast.Node) bool {
	if !ast.IsExpressionStatement(statement) {
		return false
	}
	call := Unwrapped(statement.AsExpressionStatement().Expression)
	if call == nil || !ast.IsCallExpression(call) {
		return false
	}
	expr := call.AsCallExpression()
	if expr.Arguments == nil {
		return false
	}
	for _, argument := range expr.Arguments.Nodes {
		if Unwrapped(argument) == handler {
			return true
		}
	}
	return false
}

// The return leg's sole-consumer scan: finds the `JSON.parse(<name>)`
// (or `JSON.parse(<name>.stdout)`) node the target's return fact
// attaches to, scanning the statements after the call in the same
// function.

package walk

import (
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the return leg ──────────────────────────────────────────────── */

// soleParseConsumerOf finds the `JSON.parse(<stdoutName>)` node the
// target's return fact attaches to, scanning the statements AFTER the
// call in the same function — the same same-function, count-the-
// occurrences discipline the relational accumulation's return shape
// uses to find its division.
//
// The declines, each because the fact would land on the wrong value:
//
//   - no parse of the name at all: nothing reads the target's output as
//     JSON here, so there is nothing to attach to;
//   - TWO OR MORE parses: one published fact cannot stand for two
//     nodes, and both would read it;
//   - an intervening WRITE to the name: the value the parse reads is
//     then not the value the call produced. (The recognizer already
//     requires a `const` binding, so this catches the shadowing and
//     reassignment shapes a const cannot prevent by itself.)
//
// A parse inside a NESTED FUNCTION BODY is not counted: that scope runs
// an unstated number of times, so the fact cannot be pinned to one
// evaluation — CollectLocals' own boundary, spelled the same way
// accumulationDivisionsIn spells it.
func soleParseConsumerOf(
	statements []*ast.Node, index int, stdoutName string,
) (*ast.Node, int, string) {
	var found *ast.Node
	foundAt := -1
	count := 0
	written := map[string]struct{}{}
	for offset, statement := range statements[index+1:] {
		// AssignedNamesDirect, not AssignedNames: the question here is
		// whether the TEXT rewrites the binding, and the call-mediated
		// reading would count the very `JSON.parse(stdout)` this route is
		// looking for as a possible write to it
		AssignedNamesDirect(statement, written)
		before := count
		foreignParseCallsIn(statement, stdoutName, &found, &count)
		if foundAt < 0 && count > before {
			foundAt = index + 1 + offset
		}
	}
	if _, moves := written[stdoutName]; moves {
		return nil, -1, "the stdout binding " + stdoutName + " is written after the call, so the " +
			"value parsed is not the value the Python target produced — no fact is attached"
	}
	if count == 0 {
		// nothing reads the target's stdout through JSON.parse at all — a
		// recognized crossing whose result NO expression consumes needs NO
		// fact: there is no node for one to land on, so this is not a
		// defect to name, only an absent attach. soleAskUnused answers
		// (nil, -1, "") — an EMPTY sentence — so the caller (ForeignEdgeAt)
		// reads this as "nothing to attach", not as a decline: the
		// outbound leg's own judgment (already discharged before this call
		// runs) still stands unchanged.
		return nil, -1, ""
	}
	if count > 1 {
		return nil, -1, stdoutName + " is parsed " + strconv.Itoa(count) + " times after the call, " +
			"and one stated result cannot stand for more than one expression — no fact is attached"
	}
	return found, foundAt, ""
}

// foreignParseCallsIn counts every `JSON.parse(<name>)` in a statement
// and remembers the first, never descending into a nested function.
func foreignParseCallsIn(node *ast.Node, name string, found **ast.Node, count *int) {
	var visit func(n *ast.Node) bool
	visit = func(n *ast.Node) bool {
		if ast.IsFunctionDeclaration(n) || ast.IsFunctionExpression(n) ||
			ast.IsArrowFunction(n) || ast.IsClassDeclaration(n) || ast.IsClassExpression(n) {
			return false
		}
		if isForeignParseOf(n, name) {
			if *found == nil {
				*found = n
			}
			*count++
			// the one argument is the bare name — no second occurrence can
			// hide inside it
			return false
		}
		n.ForEachChild(visit)
		return false
	}
	visit(node)
}

// isForeignParseOf is whether a node is exactly `JSON.parse(<name>)` or
// `JSON.parse(<name>.stdout)` — execFileSync's bound name IS the stdout
// string, so the bare identifier is the read; spawnSync's bound name is
// the whole result object, so its stdout string sits at `.stdout`. Both
// read the SAME binding for the write-check in soleParseConsumerOf
// (AssignedNamesDirect keys on the plain name either way).
func isForeignParseOf(node *ast.Node, name string) bool {
	if node == nil || !ast.IsCallExpression(node) {
		return false
	}
	call := node.AsCallExpression()
	callee := call.Expression
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return false
	}
	access := callee.AsPropertyAccessExpression()
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != "JSON" ||
		access.Name().Text() != "parse" {
		return false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return false
	}
	return isForeignParseArgumentOf(call.Arguments.Nodes[0], name)
}

// isForeignParseArgumentOf is whether a parse's one argument reads the
// bound name's stdout: the bare identifier (execFileSync/execSync), or
// `<name>.stdout` (spawnSync's result object).
func isForeignParseArgumentOf(argument *ast.Node, name string) bool {
	node := Unwrapped(argument)
	if node == nil {
		return false
	}
	if ast.IsIdentifier(node) {
		return node.Text() == name
	}
	if ast.IsPropertyAccessExpression(node) {
		access := node.AsPropertyAccessExpression()
		receiver := Unwrapped(access.Expression)
		return access.QuestionDotToken == nil && receiver != nil && ast.IsIdentifier(receiver) &&
			receiver.Text() == name && ast.IsIdentifier(access.Name()) && access.Name().Text() == "stdout"
	}
	return false
}

// split from ir_opaque_havoc.go — naming what the floor refused

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
)

// switchRefusals holds, per switch STATEMENT node, the reason
// LowerSwitch declined it. The refusals that still stand are:
//
//   - "switch on an untracked discriminant" — the discriminant names no
//     slot AND evaluating it moves state, so there is no arm-testing
//     chain and no tested-nothing chain either;
//   - "switch with no clauses";
//   - "switch whose clause list is ill-formed: two default clauses,
//     which the grammar does not admit" — NOT an open gap. CaseBlock's
//     productions (ECMA-262, sec-switch-statement) hold at most one
//     DefaultClause, so a second `default:` fails to parse and tsc
//     reports it; the guard cannot fire on a program that compiles and
//     stands only so a malformed tree answers nothing;
//   - "switch on a case label that is not a literal" — a label that is
//     no literal, no const chain to one, and no enum member;
//   - "switch whose default arm did not lower" and "switch whose case
//     arm did not lower" — the arm's own first blocker is the queue
//     entry, which its walk already named.
//
// The store exists because the two readings happen at different times.
// LowerSwitch knows exactly which gate it refused on, and then answers a
// plain false; the statement then falls to the havoc floor, which
// refuses any `return` inside it and reports "return inside switch" — a
// name that points at the return rather than at the thing the switch
// route could not read. A row naming the return is not a work-queue
// entry, because the return was never the problem. So the route leaves
// its reason keyed by the node, and the floor's own naming reads it back
// below.
//
// Keyed by node, so a body holding two switches keeps a reason for each,
// and a re-lowering of the same node overwrites its own earlier reason
// rather than accumulating. The lock is the same discipline the declined
// -construct store beside it keeps, for the same reason: several bodies
// may lower at once.
var (
	switchRefusalsLock sync.Mutex
	switchRefusals     = map[*ast.Node]string{}
)

// NoteSwitchRefusal records why LowerSwitch declined one switch
// statement. Last-wins: the route returns immediately after naming, so
// exactly one name is written per attempt, and a second attempt at the
// same node is the same walk reaching the same gate.
func NoteSwitchRefusal(statement *ast.Node, reason string) {
	if statement == nil || reason == "" {
		return
	}
	switchRefusalsLock.Lock()
	defer switchRefusalsLock.Unlock()
	switchRefusals[statement] = reason
}

// switchRefusalOf reads back the reason LowerSwitch left on a node, and
// CLEARS it: the floor names the statement once, and a later lowering of
// the same node writes its own reason first.
func switchRefusalOf(statement *ast.Node) (string, bool) {
	switchRefusalsLock.Lock()
	defer switchRefusalsLock.Unlock()
	held, refused := switchRefusals[statement]
	if refused {
		delete(switchRefusals, statement)
	}
	return held, refused
}

// DeclinedHavocConstruct names WHY the floor refused a statement, in the
// statement's own syntax rather than as a category. The coverage
// histogram is the work queue, so a row has to name something a reader
// can go and act on: "throw inside try" and "labelled break crossing
// out" are worth having, "a statement the lowering does not read" is
// not.
//
// The scan is havocEnumerable's, run again to find WHICH node refused —
// the predicate answers a bool because that is what the route needs, and
// this answers the name because that is what the report needs. Empty
// where nothing refuses (the caller then names its own reason).
func DeclinedHavocConstruct(statement *ast.Node) string {
	if statement == nil {
		return ""
	}
	// a SWITCH the chain route already refused names that refusal rather
	// than whatever the scan below finds first: the route knows which gate
	// it hit, and the scan only ever finds the `return` that the gate's
	// failure left stranded at the floor
	if ast.IsSwitchStatement(statement) {
		if named, refused := switchRefusalOf(statement); refused {
			return named
		}
	}
	reason := ""
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if reason != "" {
			return true
		}
		switch {
		case node.Kind == ast.KindWithStatement:
			reason = "with statement"
			return true
		case ast.IsBreakStatement(node), ast.IsContinueStatement(node):
			if !containedTransfer(node, statement) {
				reason = transferDeclineName(node)
			}
			return true
		case ast.IsReturnStatement(node):
			reason = "return inside " + havocConstructName(statement)
			return true
		case ast.IsThrowStatement(node):
			reason = "throw inside " + havocConstructName(statement)
			if throwInsideTry(node, statement) {
				reason = "throw inside try"
			}
			return true
		case isBareEvalCall(node):
			reason = "eval call"
			return true
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(statement)
	return reason
}

// transferDeclineName spells a break or continue that leaves the
// statement: labelled or bare, break or continue, each said as the
// construct it is.
func transferDeclineName(transfer *ast.Node) string {
	word := "break"
	if ast.IsContinueStatement(transfer) {
		word = "continue"
	}
	if transferLabelOf(transfer) != "" {
		return "labeled " + word + " crossing out"
	}
	return word + " crossing out"
}

// throwInsideTry is whether a throw sits lexically inside a `try` within
// the subtree — the case whose decline the reasoning in
// throwCarryingStatement holds, and the one the report names by that
// name so it reads as the construct it is.
func throwInsideTry(throw *ast.Node, root *ast.Node) bool {
	for node := throw.Parent; node != nil; node = node.Parent {
		if ast.IsTryStatement(node) {
			return true
		}
		if node == root {
			return ast.IsTryStatement(root)
		}
		if ast.IsFunctionLike(node) || ast.IsClassLike(node) {
			return false
		}
	}
	return false
}

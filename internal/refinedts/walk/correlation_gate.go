// from dataflow_facts/path_conditions.ts
//
// The two functions path_conditions.go's own header explicitly
// leaves for this stage: gatesTestedBy and correlationGateOf are NOT
// needed by evaluation/ (only control_flow's statement-list
// splitting calls them), so they land here rather than in that
// file — same ownership boundary that file's comment established.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// statementGatesMu/statementGatesCache substitute the TS source's
// `WeakMap<ts.Statement, readonly GateKey[]>` — a regular map guarded
// by a mutex, per PORT.md's weak-map convention.
var (
	statementGatesMu    sync.Mutex
	statementGatesCache = map[*ast.Node][]gateKey{}
)

// gatesTestedBy is gatesTestedBy in the TS source: the gates a
// statement tests — `if` conditions, ternary conditions, and
// short-circuit left sides, anywhere in its subtree. Cached per
// statement: loop widening re-walks lists.
func gatesTestedBy(ctx *FlowContext, statement *ast.Node) []gateKey {
	statementGatesMu.Lock()
	if held, ok := statementGatesCache[statement]; ok {
		statementGatesMu.Unlock()
		return held
	}
	statementGatesMu.Unlock()

	var found []gateKey
	note := func(condition *ast.Node) {
		if gate := gateKeyOf(ctx, condition); gate != nil {
			found = append(found, *gate)
		}
	}
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if ast.IsIfStatement(node) {
			note(node.AsIfStatement().Expression)
		}
		if ast.IsConditionalExpression(node) {
			note(node.AsConditionalExpression().Condition)
		}
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindAmpersandAmpersandToken || bin.OperatorToken.Kind == ast.KindBarBarToken {
				note(bin.Left)
			}
		}
		// a switch tests `scrutinee === <label>` once per literal case
		if ast.IsSwitchStatement(node) {
			switchStmt := node.AsSwitchStatement()
			place := dataflowfacts.PlaceKeyOf(ctx.P.Checker, switchStmt.Expression)
			if place != nil {
				for _, clause := range switchStmt.CaseBlock.AsCaseBlock().Clauses.Nodes {
					if !ast.IsCaseClause(clause) {
						continue
					}
					literal := dataflowfacts.LiteralSpelling(clause.AsCaseOrDefaultClause().Expression)
					if literal == nil {
						continue
					}
					found = append(found, gateKey{Base: place.Base, Detail: place.Path + "===" + *literal, Negated: false, Place: *place})
				}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(statement)

	statementGatesMu.Lock()
	statementGatesCache[statement] = found
	statementGatesMu.Unlock()
	return found
}

// CorrelationGateOf is correlationGateOf in the TS source: the gate
// a correlation pass would split a statement list on — one canonical
// key tested at least twice, whose place stays stable across the
// whole list (and every closure of the enclosing function).
func CorrelationGateOf(ctx *FlowContext, statements []*ast.Node, held []GateAssumption) (GateAssumption, bool) {
	if len(statements) == 0 {
		return GateAssumption{}, false
	}
	type countEntry struct {
		n   int
		key gateKey
	}
	counts := map[*ast.Symbol]map[string]*countEntry{}
	for _, statement := range statements {
		for _, gate := range gatesTestedBy(ctx, statement) {
			// a gate the surrounding passes already assume is decided —
			// the next split takes the next gate
			alreadyHeld := false
			for _, a := range held {
				if a.Base == gate.Base && a.Detail == gate.Detail {
					alreadyHeld = true
					break
				}
			}
			if alreadyHeld {
				continue
			}
			byDetail, ok := counts[gate.Base]
			if !ok {
				byDetail = map[string]*countEntry{}
				counts[gate.Base] = byDetail
			}
			entry, ok := byDetail[gate.Detail]
			if !ok {
				entry = &countEntry{key: gate}
				byDetail[gate.Detail] = entry
			}
			entry.n++
			if entry.n < 2 {
				continue
			}
			fn := dataflowfacts.EnclosingFunctionOf(statements[0])
			if !dataflowfacts.StableIn(gate.Place, statements, fn) {
				continue
			}
			return GateAssumption{Base: gate.Base, Detail: gate.Detail, Truthy: true}, true
		}
	}
	return GateAssumption{}, false
}

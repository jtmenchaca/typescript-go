// `with (o) …`: which binding a name inside the body resolves to is
// decided by the runtime object, not by syntax — the property set of o
// shadows or does not shadow each name, read by read. The body still
// RUNS, so it is still walked and its checked positions still judge.
// What the walk must never do is trust its own bookkeeping across the
// block: a read inside may see o's property instead of the binding,
// and a write inside may land on o instead of the binding. So every
// tracked name the body mentions is forgotten at the door and
// forgotten again at the exit. The body is judged, never trusted.
//
// No TS twin: the TS source's analyze_statement.ts routes a with
// statement to its unmodeled catch-all, which forgets assigned names
// but never walks the body — a `return` inside was silently swallowed
// and its checked position never fired (the a-statements.ts
// syntax-coverage fixture's withStatement pins the fixed behavior).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
)

// AnalyzeWithStatement walks `with (expression) statement`. The scope
// expression evaluates in the outer scope; the body walks under the
// honest entry state (every mentioned tracked name forgotten), so a
// checked position inside fires on what is actually provable — which
// for a with-scoped name is nothing (the host checker answers no
// semantic question inside a with block, and the walk holds no fact
// that survives the door). A body that returns or throws on every
// path ends the enclosing list, exactly as it does at runtime.
func AnalyzeWithStatement(ctx *FlowContext, env Env, statement *ast.Node, result *annotations.DeclaredRefinement) bool {
	with := statement.AsWithStatement()
	// the scope object itself is an ordinary expression of the OUTER
	// scope — it evaluates before any name resolves through it
	evaluateExpression(ctx, env, with.Expression)
	if with.Statement == nil {
		return false
	}
	// the door: a read inside the body may see the scope object's
	// property instead of the binding the name spells out here, so no
	// fact about a mentioned name enters
	havocNamesMentioned(ctx, env, with.Statement)
	exits := AnalyzeStatement(ctx, env, with.Statement, result)
	// the exit: a write inside may have landed on the scope object
	// rather than the binding (or the reverse), and any fact the body
	// walk recorded rests on reads whose referent the runtime chose —
	// none of it carries past the block
	if !exits {
		havocNamesMentioned(ctx, env, with.Statement)
	}
	return exits
}

// havocNamesMentioned forgets every tracked name that appears as an
// identifier anywhere in the subtree. The collection is deliberately
// coarse — property names and label names come along with binding
// reads — because forgetting an extra name only ever costs knowledge,
// never soundness. The scan does NOT stop at nested function
// boundaries: a function declared inside the body resolves its free
// names through the with scope when it runs.
func havocNamesMentioned(ctx *FlowContext, env Env, root *ast.Node) {
	mentioned := map[string]struct{}{}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsIdentifier(node) {
			mentioned[node.Text()] = struct{}{}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(root)
	for name := range mentioned {
		if _, ok := env.Get(name); ok {
			HavocEnv(ctx.Aliases, env, name)
		}
	}
}

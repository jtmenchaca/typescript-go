// EvaluateAwait's key-presence sweep (cast_and_await.go): a `has`
// guard's presence fact must not survive an await. An await ends the
// current job (sec-await), so anything another job can reach may be
// mutated while this one is suspended — the E5.branch row
// (packages/tests/e2e/effect/E5.branch/E5.branch.ts) states the TOCTOU
// this pins at the walk level.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// awaitExpressionIn walks the entry source for its first
// AwaitExpression node.
func awaitExpressionIn(t *testing.T, source *ast.SourceFile) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsAwaitExpression(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return found != nil
	}
	source.AsNode().ForEachChild(visit)
	if found == nil {
		t.Fatalf("no await-expression found")
	}
	return found
}

// TestEvaluateAwait_DropsAKeyPresenceFactAcrossTheSuspension pins the
// fix: a presence place written under a `map.has(key)` guard is gone
// from the environment once EvaluateAwait has walked an await —
// mirroring the real shape (`if (map.has(key)) { await x; ... }`)
// without needing the full checker-through-Map path to observe it.
func TestEvaluateAwait_DropsAKeyPresenceFactAcrossTheSuspension(t *testing.T) {
	p := entryEnvTestProgram(t, "declare function x(): Promise<void>;\n"+
		"async function f(): Promise<void> { await x(); }\n")
	awaitExpr := awaitExpressionIn(t, p.Entry)
	env := NewEnv()
	// the place string is written directly rather than through
	// KeyPresencePlace (which needs real AST identifier nodes for its
	// receiver/key that this probe has no reason to construct) — the
	// SEGMENT is what DropAllKeyPresenceFacts matches on, and that is
	// exactly what this pin exercises.
	env.Set("map"+dataflowfacts.KeyPresencePlaceSegment+"\"k\")", keyPresenceEstablished)
	if _, held := env.Get("map" + dataflowfacts.KeyPresencePlaceSegment + "\"k\")"); !held {
		t.Fatalf("setup: presence place not written")
	}
	ctx := &FlowContext{P: p}
	EvaluateAwait(ctx, env, awaitExpr)
	if _, held := env.Get("map" + dataflowfacts.KeyPresencePlaceSegment + "\"k\")"); held {
		t.Errorf("EvaluateAwait left a key-presence place standing across the await; want it swept")
	}
}

// TestNoAwaitLeavesAKeyPresenceFactStanding is the contrast pin: with
// no suspension in play, nothing in the walk's ordinary expression
// evaluation touches a presence place — the E5.branch row's second
// function (no await, same synchronous job) depends on this holding.
func TestNoAwaitLeavesAKeyPresenceFactStanding(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: number): number { return x; }\n")
	env := NewEnv()
	env.Set("map"+dataflowfacts.KeyPresencePlaceSegment+"\"k\")", keyPresenceEstablished)
	var call *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if call != nil {
			return true
		}
		if ast.IsIdentifier(node) && node.Text() == "x" {
			call = node
			return true
		}
		node.ForEachChild(visit)
		return call != nil
	}
	p.Entry.AsNode().ForEachChild(visit)
	if call == nil {
		t.Fatalf("no identifier `x` found")
	}
	ctx := &FlowContext{P: p}
	evaluateExpression(ctx, env, call)
	if _, held := env.Get("map" + dataflowfacts.KeyPresencePlaceSegment + "\"k\")"); !held {
		t.Errorf("ordinary evaluation (no await) swept a key-presence place; want it standing")
	}
}

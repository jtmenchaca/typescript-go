// Read-once for GetTypeAtLocation asks — no TS twin (Go-only
// mechanism, same precedent as host_type_memo.go). The checker's
// getTypeOfNode recomputes on every call — a props-union ask re-runs
// the conditional/mapped-type instantiation each time (pprof
// 2026-08-17: getConditionalTypeInstantiation 349MB cum over the
// recharts sweep) — while the walk asks the same node repeatedly
// (loop passes, branch joins, per-model receiver reads). The answer
// is a function of (checker, node) alone, so the first ask serves the
// rest. Answers are remembered per checker — "types obtained from
// different checkers must never mix", and a node's answer is only
// meaningful to the checker that produced it.

package typereading

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

var (
	typeAtLocationMu    sync.Mutex
	typeAtLocationMemos = map[*checker.Checker]map[*ast.Node]*checker.Type{}
)

// TypeAtLocation is the memoized face of c.GetTypeAtLocation: the
// checker's own answer for the node, remembered per (checker, node).
func TypeAtLocation(c *checker.Checker, node *ast.Node) *checker.Type {
	typeAtLocationMu.Lock()
	memo := typeAtLocationMemos[c]
	if memo == nil {
		memo = map[*ast.Node]*checker.Type{}
		typeAtLocationMemos[c] = memo
	}
	t, hit := memo[node]
	typeAtLocationMu.Unlock()
	if hit {
		tracing.CountBy("host.typeAtLocation.hit", 1)
		return t
	}
	tracing.CountBy("host.typeAtLocation.ask", 1)
	t = c.GetTypeAtLocation(node)
	typeAtLocationMu.Lock()
	memo[node] = t
	typeAtLocationMu.Unlock()
	return t
}

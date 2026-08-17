// Pins for the per-entry identifier-use index: the bucket a join
// reads must hold every identifier the old whole-file traversal
// visited, in the same source order, and bucketing by TEXT must not
// let two different symbols spelling one name merge into one join.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// identifierUsesByTraversal is the scan identifierUsesOf replaced —
// the whole-entry ForEachChild recursion declaredJoinUncached ran per
// declaration. Kept here as the oracle the index is checked against.
func identifierUsesByTraversal(p *program.CheckerProgram, text string) []*ast.Node {
	var found []*ast.Node
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if ast.IsIdentifier(node) && node.Text() == text {
			found = append(found, node)
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(p.Entry.AsNode())
	return found
}

func TestIdentifierUseIndex_MatchesTheWholeFileTraversal(t *testing.T) {
	p := callSiteTestProgram(t,
		"function f(n) { return n; }\n"+
			"f(10);\n"+
			"f(25);\n"+
			"const g = { f: 1 };\n"+
			"function h(f) { return f; }\n")

	for _, text := range []string{"f", "n", "g", "h"} {
		want := identifierUsesByTraversal(p, text)
		got := identifierUsesOf(p, text)
		if len(got) != len(want) {
			t.Fatalf("identifierUsesOf(%q) found %d nodes, traversal found %d", text, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("identifierUsesOf(%q)[%d] = %p, traversal has %p (order must match)", text, i, got[i], want[i])
			}
		}
	}
}

func TestIdentifierUseIndex_AnAbsentNameHasAnEmptyBucket(t *testing.T) {
	p := callSiteTestProgram(t, "function f(n) { return n; }\nf(1);\n")
	if uses := identifierUsesOf(p, "nowhere"); len(uses) != 0 {
		t.Errorf("identifierUsesOf(absent name) = %d nodes, want 0", len(uses))
	}
}

// Two declarations spelling one name: the index buckets both uses
// together by text, so the join's own symbolAt test is what keeps them
// apart. `outer`'s join must wear only outer's own call sites.
func TestCallSiteBindings_ASharedNameDoesNotMergeTwoSymbolsJoins(t *testing.T) {
	p := callSiteTestProgram(t,
		"function outer(n) { return n; }\n"+
			"outer(10);\n"+
			"function wrapper() {\n"+
			"  function outer(n) { return n; }\n"+
			"  outer(999);\n"+
			"  return outer(1000);\n"+
			"}\n")
	ctx := callSiteCtxOf(t, p)

	// both declarations spell "outer", so one bucket holds every use
	if uses := identifierUsesOf(p, "outer"); len(uses) < 5 {
		t.Fatalf("identifierUsesOf(outer) = %d nodes, want at least 5 (both declarations and their calls)", len(uses))
	}

	bindings, ok := CallSiteBindings(ctx, callSiteFunctionNamed(t, p, "outer"))
	if !ok {
		t.Fatalf("CallSiteBindings(top-level outer) ok = false, want true")
	}
	held, _ := bindings.Get("n")
	formatted, hasFormatted := abstractdomain.FormatAbstractValue(held)
	// an exact scalar formats BARE ("10"), never braced — the claim is
	// that the join holds EXACTLY the top-level site's 10, with the
	// inner declaration's 999/1000 sites excluded (a merge would show a
	// union)
	if !hasFormatted || formatted != "10" {
		t.Errorf("bindings[n] = %q, %v, want %q, true — the inner outer's 999/1000 sites must not join in",
			formatted, hasFormatted, "10")
	}
}

// The index is built once per program and reused: a second ask returns
// the same backing slice, so a join never pays a rebuild.
func TestIdentifierUseIndex_IsBuiltOncePerProgram(t *testing.T) {
	p := callSiteTestProgram(t, "function f(n) { return n; }\nf(1);\nf(2);\n")
	first := identifierUsesOf(p, "f")
	second := identifierUsesOf(p, "f")
	if len(first) == 0 {
		t.Fatalf("identifierUsesOf(f) found no uses")
	}
	if len(first) != len(second) {
		t.Fatalf("second ask found %d nodes, first found %d", len(second), len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("second ask returned different nodes — the index rebuilt instead of serving")
		}
	}
}

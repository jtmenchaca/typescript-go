// The per-entry identifier-use index — no TS twin (Go-only, same
// precedent as call_site_snapshot_fill.go's demand fill).
//
// declaredJoinUncached and callbackSitePins each need "every use of
// this name in the entry file, other than its own declaration." Both
// answered it by walking the WHOLE entry AST once per declaration
// (`visit(p.Entry.AsNode())`), so a file with d joined declarations
// paid d full traversals of the same tree — and each traversal tested
// every identifier it passed. The work is identical every time: the
// entry's AST does not change between joins.
//
// This file walks the entry ONCE per program and buckets every
// identifier node by its text. A join then reads its own bucket —
// typically a handful of nodes — instead of re-traversing the file.
// The symbol check stays at the CALLER, unchanged: bucketing is by
// TEXT (a cheap syntactic key), and the caller still asks
// symbolAt(...) == target on each candidate, so two different symbols
// spelling one name never merge. The index changes only how
// candidates are FOUND, never which ones qualify.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// identifierUseIndexes holds one index per program, guarded the same
// way call_site_snapshots.go's snapshotStores and call_site_bindings.go's
// bindingsMemo are: Go has no weak maps, so a plain map keyed on the
// program pointer substitutes and dies with the check that built it.
var (
	identifierUseMu      sync.Mutex
	identifierUseIndexes = map[*program.CheckerProgram]map[string][]*ast.Node{}
)

// identifierUsesOf answers every identifier node in the entry file
// whose text is `text`. Built once per program on the first ask and
// reused by every later one. The returned slice is the index's own —
// callers read it and never write to it.
func identifierUsesOf(p *program.CheckerProgram, text string) []*ast.Node {
	identifierUseMu.Lock()
	index, ok := identifierUseIndexes[p]
	if !ok {
		index = buildIdentifierUseIndex(p)
		identifierUseIndexes[p] = index
	}
	uses := index[text]
	identifierUseMu.Unlock()
	return uses
}

// buildIdentifierUseIndex walks the entry once and buckets every
// identifier by its text, in source order — the same order the old
// per-declaration ForEachChild recursion visited them, so a join that
// appends call sites while reading its bucket builds the same list it
// built before, in the same order.
func buildIdentifierUseIndex(p *program.CheckerProgram) map[string][]*ast.Node {
	index := map[string][]*ast.Node{}
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if ast.IsIdentifier(node) {
			text := node.Text()
			index[text] = append(index[text], node)
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(p.Entry.AsNode())
	return index
}

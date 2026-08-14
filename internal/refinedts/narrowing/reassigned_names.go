// Names a file ever reassigns, and every name a function can write.
// A function declaration whose name is written anywhere may hold a
// DIFFERENT function at runtime, so its body cannot stand for the
// call. Split from condition_analysis.ts per the v2 tree.
package narrowing

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// reassignedMu guards reassignedCache: names a file ever reassigns — a
// function declaration whose name is written anywhere may hold a
// DIFFERENT function at runtime, so its body cannot stand for the
// call.
//
// The TS source keys this memo with a WeakMap<ts.SourceFile, Set<string>>;
// Go has no weak maps, so this substitutes a regular map guarded by a
// mutex. Functionally identical per program: entries live exactly as
// long as the program that produced the source file is in use by this
// port.
var (
	reassignedMu    sync.Mutex
	reassignedCache = map[*ast.SourceFile]map[string]struct{}{}
)

// ReassignedNames is reassignedNames in the TS source.
func ReassignedNames(file *ast.SourceFile) map[string]struct{} {
	reassignedMu.Lock()
	held, ok := reassignedCache[file]
	reassignedMu.Unlock()
	if ok {
		return held
	}
	names := map[string]struct{}{}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment &&
				ast.IsIdentifier(bin.Left) {
				names[bin.Left.Text()] = struct{}{}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	file.AsNode().ForEachChild(visit)
	reassignedMu.Lock()
	reassignedCache[file] = names
	reassignedMu.Unlock()
	return names
}

// FunctionWrites is functionWrites in the TS source: every name the
// function can write — assignment targets, ++/--, non-read-only method
// receivers of reference values, reference arguments handed to calls (a
// value-sorted word travels by copy), with a default-library method's
// receiver spared. Read from the syntactic-facts seam — computed once
// per (checker, function), where it used to rescan the whole function
// behind every const-guard condition.
func FunctionWrites(c *checker.Checker, fn *ast.Node) map[string]struct{} {
	return dataflowfacts.WrittenNamesOf(c, fn)
}

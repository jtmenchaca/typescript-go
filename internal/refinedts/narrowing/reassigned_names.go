// Names a file ever reassigns, and every name a function can write.
// A function declaration whose name is written anywhere may hold a
// DIFFERENT function at runtime, so its body cannot stand for the
// call. Split from condition_analysis.ts per the v2 tree.
//
// BLOCKED: FunctionWrites (functionWrites in the TS source) reads
// dataflowfacts.WrittenNamesOf, which is NOT ported — it needs
// service/program_resolution.ts's resolvesToDefaultLib on a default-
// library method receiver, and dataflowfacts/syntactic_facts.go's own
// banner marks writtenNamesOf blocked on exactly that (PORT.md: the
// TS host/program adapter does not port, and this reader is outside
// this directory's allowed import set regardless). Every call site in
// this package that would read FunctionWrites is therefore also
// unported here — see bound_condition.go and pinned_function.go's own
// banners.
package narrowing

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
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

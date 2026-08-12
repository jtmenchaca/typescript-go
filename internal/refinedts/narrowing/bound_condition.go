// Conditions bound to names: resolve a tested expression to the
// condition a const binding holds, and the value-copy bindings a
// function's `const x = <place>` pairs make. Split from
// condition_analysis.ts per the v2 tree.
//
// BLOCKED: resolveBoundCondition, boundConditionInitializer,
// copyBindingsOf, and copySourcePlaceOf all read functionWrites
// (reassigned_names.go's FunctionWrites), which needs
// dataflowfacts.WrittenNamesOf — not ported (blocked on
// service/program_resolution.ts's resolvesToDefaultLib; see
// reassigned_names.go's banner and dataflowfacts/syntactic_facts.go's
// own). Every caller of these four functions in this package is
// therefore also unported: condition_analysis.go's narrowingsOf reads
// resolveBoundCondition only at its TOP (the const-name resolution
// pass) — that pass is dropped there, reported, and the rest of
// narrowingsOf (the connective and kernel-tree reading) ports whole.
// ConstCopiesOf itself has no such dependency and is ported below,
// ready for when functionWrites lands.
package narrowing

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// ConstCopy is one `const x = <tracked place>` pair a function
// declares.
type ConstCopy struct {
	Name  string
	Place dataflowfacts.TrackedPlace
}

// constCopiesMu guards constCopiesCache: every `const x = <tracked
// place>` pair a function declares — collected ONCE per function (the
// per-application visits of copyBindingsOf and copySourcePlaceOf each
// rescanned the whole function). Pure per node: the place reading is
// syntactic.
//
// The TS source keys this memo with a WeakMap<ts.Node, …>; Go has no
// weak maps, so this substitutes a regular map guarded by a mutex.
// Functionally identical per program: entries live exactly as long as
// the program that produced the function node is in use by this port.
var (
	constCopiesMu    sync.Mutex
	constCopiesCache = map[*ast.Node][]ConstCopy{}
)

// ConstCopiesOf is constCopiesOf in the TS source.
func ConstCopiesOf(fn *ast.Node) []ConstCopy {
	constCopiesMu.Lock()
	held, ok := constCopiesCache[fn]
	constCopiesMu.Unlock()
	if ok {
		return held
	}
	var copies []ConstCopy
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsVariableDeclaration(node) && ast.IsIdentifier(node.Name()) {
			varDecl := node.AsVariableDeclaration()
			if varDecl.Initializer != nil && ast.IsVariableDeclarationList(node.Parent) &&
				(node.Parent.Flags&ast.NodeFlagsConst) != 0 {
				place := dataflowfacts.TrackedPlaceOf(varDecl.Initializer, func(string) bool { return true })
				if place != nil {
					copies = append(copies, ConstCopy{Name: node.Name().Text(), Place: *place})
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	fn.ForEachChild(visit)
	constCopiesMu.Lock()
	constCopiesCache[fn] = copies
	constCopiesMu.Unlock()
	return copies
}

// BoundConditionInitializer is boundConditionInitializer in the TS
// source. BLOCKED — see the file banner: it needs FunctionWrites,
// which is not ported. Always answers (nil, false) here.
func BoundConditionInitializer(c *checker.Checker, e *ast.Node) *ast.Node {
	_ = c
	_ = e
	return nil
}

// ResolvedCondition is the { condition, flipped } pair
// resolveBoundCondition answers.
type ResolvedCondition struct {
	Condition *ast.Node
	Flipped   bool
}

// ResolveBoundCondition is resolveBoundCondition in the TS source.
// BLOCKED — see the file banner: it reads BoundConditionInitializer,
// which is not ported (needs FunctionWrites). Always answers
// (ResolvedCondition{}, false) here — narrowingsOf's caller then falls
// through to reading the condition as itself, exactly as the TS source
// does when resolution reads nothing.
func ResolveBoundCondition(c *checker.Checker, e *ast.Node) (ResolvedCondition, bool) {
	_ = c
	_ = e
	return ResolvedCondition{}, false
}

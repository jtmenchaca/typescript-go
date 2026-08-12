// from control_flow/ir_lowering_context.ts
//
// The slot vector and name map a lowering walk carries: which
// bindings are tracked, under which sort and typeof evidence, and
// how an inlined body resolves names without capturing the caller.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// LoweringResult is the (done, ret) slot pair for FUNCTION BODIES:
// `return e` writes the result slot and raises the done flag;
// statements after a returning branch run only under the flag's
// falsity. Both slots ride the ordinary proved grammar — no new
// kernel statement exists, so walk_sound covers returning bodies
// unchanged.
type LoweringResult struct {
	Done int
	Ret  int
}

// LoweringContext mirrors the TS LoweringContext interface.
type LoweringContext struct {
	// Bindings: tracked binding names, in walk order.
	Bindings []string
	// Sorts: per binding, the sort its occurrences wear.
	Sorts []BindingKind
	// Typeofs: per binding, what typeof answers for its defined
	// values. Nil means the TS `typeofs?` was absent.
	Typeofs []TypeofTag
	// Narrow: the kernel's narrowing question, for loop heads.
	Narrow func(tree kernelbridge.NarrowTree) kernelbridge.NarrowAnswer
	// Result: nil means the TS `result?` was absent (not a function
	// body lowering).
	Result *LoweringResult
	// Names: CLOSED name resolution for an inlined callee body: a
	// name not in the map is untracked, never the enclosing caller's
	// slot — an inlined body reading a free name must decline, not
	// capture. Nil means the TS `names?` was absent (top-level
	// lowering, resolve through Bindings instead).
	Names map[string]int
	// ResolveCallee: the declaration behind a called name, for
	// composition — a call to a resolvable pure callee lowers as its
	// body inlined into fresh slots. Nil: calls decline as before.
	ResolveCallee func(callee *ast.Node) *ast.Node
	// Allocate: grow the slot vector (composition needs fresh
	// slots); returns (0, false) past the owner's budget. Nil means
	// the TS `allocate?` was absent.
	Allocate func(name string, sort BindingKind, typeofTag TypeofTag) (int, bool)
	// Inlining: declarations currently being lowered — the
	// composition cycle guard. Nil means the TS `inlining?` was
	// absent.
	Inlining map[*ast.Node]struct{}
}

// IndexOf is indexOf in the TS source: the tracked index of an
// identifier or a property path (`o.k`), matched by spelled name.
// An inlined body resolves ONLY through its own name map.
func IndexOf(context *LoweringContext, name *ast.Node) (int, bool) {
	spelled, ok := SpelledNameOf(name)
	if !ok {
		return 0, false
	}
	if context.Names != nil {
		i, found := context.Names[spelled]
		return i, found
	}
	for i, binding := range context.Bindings {
		if binding == spelled {
			return i, true
		}
	}
	return 0, false
}

// NumberIndexOf is numberIndexOf in the TS source: a tracked
// NUMBER-sorted read, or (0, false) — arithmetic admits only the
// number sort.
func NumberIndexOf(context *LoweringContext, name *ast.Node) (int, bool) {
	i, ok := IndexOf(context, name)
	if !ok {
		return 0, false
	}
	if context.Sorts[i] == BindingKindNumber {
		return i, true
	}
	return 0, false
}

// StatementsOf is statementsOf in the TS source: a statement or
// block as a flat statement list.
func StatementsOf(s *ast.Node) []*ast.Node {
	if s == nil {
		return nil
	}
	if ast.IsBlock(s) {
		return append([]*ast.Node{}, s.AsBlock().Statements.Nodes...)
	}
	return []*ast.Node{s}
}

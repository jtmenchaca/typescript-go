// The module-const array-mutation scan: whether ANY statement in the
// module mutates a followed const array in place. A scalar const-follow
// stands on "the binding can never be rebound, and a scalar cannot be
// mutated through an alias" — an array can. Before UntrackedIdentifier
// hands out a module const's array literal, this file answers whether
// that theory still holds for THIS name in THIS module.
//
// A mutation site is one of three shapes, per the ruling (JT,
// 2026-08-21): a call whose callee is a property access on the name
// with a mutating array method (push/pop/shift/unshift/splice/sort/
// reverse/fill/copyWithin — the exact complement of
// dataflowfacts.ReadOnlyArrayMethods for these nine, spelled out here
// rather than read by exclusion, since the brief names them
// explicitly); an assignment or compound assignment whose target is an
// element access on the name (`xs[i] = …`, `xs[i] += …`); or the name
// passed as Object.assign's FIRST argument (the mutated receiver
// position — see readObjectStaticMethods's placeable-write rule).
//
// This is a full-element-mutation-dataflow-free scan on purpose: no
// alias tracking, no reachability pruning, no "was this call ever
// reached" reasoning. A mutation site anywhere in the module's text
// blocks the follow for that name — over-inclusive by design, the same
// direction AssignedNames documents ("a name that never actually
// changes stabilizes immediately"; here, "a name that's never actually
// mutated declines a serve it never needed").
package walk

import "github.com/microsoft/typescript-go/internal/ast"

// mutatingArrayMethods is the brief's exact nine — the destructive
// Array.prototype methods, spelled out rather than read as
// "everything ReadOnlyArrayMethods excludes" so this list can't drift
// silently if that allowlist ever grows a new read-only entry.
var mutatingArrayMethods = map[string]struct{}{
	"push":       {},
	"pop":        {},
	"shift":      {},
	"unshift":    {},
	"splice":     {},
	"sort":       {},
	"reverse":    {},
	"fill":       {},
	"copyWithin": {},
}

// ConstArrayMutated answers whether ANY statement in the module
// containing `moduleDeclaration` mutates the const array bound to
// `name`, by one of the three site shapes above. `moduleDeclaration`
// is the VariableDeclaration node the const-follow is about to serve —
// its source file is the module scanned.
//
// Scanned per follow, not memoized: the brief asks for a comment
// naming the choice. A memo keyed by (source file, name) would save
// re-walking the same module's statements across repeated follows of
// the same name in one check pass, but the const-follow itself is
// already the RARE path (module consts holding array literals, not
// every identifier read), and a cache adds a second map this file
// would need to invalidate correctly on program change — for a scan
// this cheap (one AST walk, no checker asks), re-scanning per follow
// is the simpler-to-trust choice. Revisit if a wall measurement ever
// shows this scan itself as a cost line.
func ConstArrayMutated(moduleDeclaration *ast.Node, name string) bool {
	file := ast.GetSourceFileOfNode(moduleDeclaration)
	if file == nil {
		return false
	}
	mutated := false
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if mutated || node == nil {
			return
		}
		if mutatingMethodCallOnName(node, name) || elementWriteOnName(node, name) || objectAssignFirstArgIsName(node, name) {
			mutated = true
			return
		}
		// walk into every child, function bodies included — a mutation
		// inside any function declared in the file counts, since the
		// const-follow's theory is about the WHOLE MODULE's text, not
		// merely top-level statements
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return mutated
		})
	}
	scan(file.AsNode())
	return mutated
}

// mutatingMethodCallOnName: `name.push(...)`, `name.sort()`, etc. —
// a call whose callee is a property access ON THE NAME with a
// mutating method.
func mutatingMethodCallOnName(node *ast.Node, name string) bool {
	if !ast.IsCallExpression(node) {
		return false
	}
	call := node.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return false
	}
	propAccess := call.Expression.AsPropertyAccessExpression()
	if !ast.IsIdentifier(propAccess.Expression) || propAccess.Expression.Text() != name {
		return false
	}
	_, mutates := mutatingArrayMethods[propAccess.Name().Text()]
	return mutates
}

// elementWriteOnName: `name[i] = …` or `name[i] += …` — an assignment
// (plain or compound) whose target is an element access rooted
// directly at the name.
func elementWriteOnName(node *ast.Node, name string) bool {
	if !ast.IsBinaryExpression(node) {
		return false
	}
	bin := node.AsBinaryExpression()
	if bin.OperatorToken.Kind < ast.KindFirstAssignment || bin.OperatorToken.Kind > ast.KindLastAssignment {
		return false
	}
	target := bin.Left
	if !ast.IsElementAccessExpression(target) {
		return false
	}
	receiver := target.AsElementAccessExpression().Expression
	return ast.IsIdentifier(receiver) && receiver.Text() == name
}

// objectAssignFirstArgIsName: `Object.assign(name, …)` — the name
// passed as Object.assign's first (mutated-receiver) argument.
func objectAssignFirstArgIsName(node *ast.Node, name string) bool {
	if !ast.IsCallExpression(node) {
		return false
	}
	call := node.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return false
	}
	propAccess := call.Expression.AsPropertyAccessExpression()
	if !ast.IsIdentifier(propAccess.Expression) || propAccess.Expression.Text() != "Object" {
		return false
	}
	if propAccess.Name().Text() != "assign" {
		return false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return false
	}
	first := call.Arguments.Nodes[0]
	return ast.IsIdentifier(first) && first.Text() == name
}

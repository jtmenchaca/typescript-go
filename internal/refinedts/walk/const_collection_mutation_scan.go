// The module-const COLLECTION mutation scan: whether any statement in
// the module mutates a followed const Map/Set in place. This is
// const_array_mutation_scan.go's exact question asked of the other
// mutable built-in the const-follow can hand out.
//
// A scalar const-follow stands on "the binding can never be rebound,
// and a scalar cannot be mutated through an alias". A Map or Set can:
// `known.set(k, v)`, `known.delete(k)`, `known.clear()` all change the
// entries the follow would have served, with no rebinding at all. So
// before UntrackedIdentifier hands out a module const's built
// collection, this file answers whether that theory still holds for
// THIS name in THIS module.
//
// A mutation site is one of two shapes. A call whose callee is a
// property access on the name with a mutating collection method —
// set/add/delete/clear, the complete list of Map.prototype and
// Set.prototype methods that write [[MapData]]/[[SetData]]
// (sec-map.prototype.set, sec-map.prototype.delete,
// sec-map.prototype.clear, sec-set.prototype.add,
// sec-set.prototype.delete, sec-set.prototype.clear); every other
// method on either prototype reads. Or the name PASSED as an argument
// anywhere: a callee holding the collection can call `set` on it, and
// unlike an array (whose in-place writers are all spelled at the call
// site the array-scan reads) a handed-off collection's mutation is
// invisible here. The array scan's Object.assign row has no collection
// twin — Object.assign copies enumerable own properties, and a Map's
// entries are internal slots, not properties, so it cannot reach them.
//
// Dataflow-free on purpose, the same as the array scan: no alias
// tracking, no reachability pruning. A mutation site anywhere in the
// module's text blocks the follow for that name — over-inclusive by
// design, in the direction that declines a serve rather than states a
// stale one.
package walk

import "github.com/microsoft/typescript-go/internal/ast"

// mutatingCollectionMethods is every writer on Map.prototype and
// Set.prototype, read off the spec's own clause list
// (specifications/javascript/spec.html, sec-map.prototype.* and
// sec-set.prototype.*) rather than as "everything else reads", so the
// list cannot drift silently.
//
// The insert pair is easy to miss and belongs here: getOrInsert
// (sec-map.prototype.getorinsert) and getOrInsertComputed
// (sec-map.prototype.getorinsertcomputed) both APPEND a new entry to
// [[MapData]] when the key is absent — they read on a hit and write on
// a miss, which makes them writers for this scan's purpose.
var mutatingCollectionMethods = map[string]struct{}{
	"set":                 {},
	"add":                 {},
	"delete":              {},
	"clear":               {},
	"getOrInsert":         {},
	"getOrInsertComputed": {},
}

// ConstCollectionMutated answers whether ANY statement in the module
// containing `moduleDeclaration` mutates the const collection bound to
// `name`, by either site shape above. `moduleDeclaration` is the
// VariableDeclaration node the const-follow is about to serve — its
// source file is the module scanned.
//
// The declaration's OWN initializer is skipped: `new Map([["a", 1]])`
// carries no call on the name, but a name passed to its own
// constructor argument (`new Map(known)`, which cannot occur before
// the binding exists) would otherwise be read as a hand-off. Scanning
// the file minus that one subtree keeps the argument row honest.
func ConstCollectionMutated(moduleDeclaration *ast.Node, name string) bool {
	file := ast.GetSourceFileOfNode(moduleDeclaration)
	if file == nil {
		return false
	}
	var initializer *ast.Node
	if ast.IsVariableDeclaration(moduleDeclaration) {
		initializer = moduleDeclaration.AsVariableDeclaration().Initializer
	}
	mutated := false
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if mutated || node == nil || node == initializer {
			return
		}
		if mutatingCollectionMethodCallOnName(node, name) || nameHandedToACall(node, name) {
			mutated = true
			return
		}
		// walk into every child, function bodies included — a mutation
		// inside any function declared in the file counts, since the
		// const-follow's theory is about the WHOLE MODULE's text
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return mutated
		})
	}
	scan(file.AsNode())
	return mutated
}

// mutatingCollectionMethodCallOnName: `name.set(k, v)`, `name.clear()`
// — a call whose callee is a property access ON THE NAME with one of
// the four writing methods.
func mutatingCollectionMethodCallOnName(node *ast.Node, name string) bool {
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
	_, mutates := mutatingCollectionMethods[propAccess.Name().Text()]
	return mutates
}

// nameHandedToACall: the name appears as an argument of a call or a
// `new`. The callee holds the collection and may write it, and this
// scan reads no callee bodies — so a hand-off is treated as a
// mutation. A property access on the name (`known.size`) is not an
// argument and does not reach here; only the bare identifier does.
func nameHandedToACall(node *ast.Node, name string) bool {
	var arguments []*ast.Node
	switch {
	case ast.IsCallExpression(node):
		if list := node.AsCallExpression().Arguments; list != nil {
			arguments = list.Nodes
		}
	case ast.IsNewExpression(node):
		if list := node.AsNewExpression().Arguments; list != nil {
			arguments = list.Nodes
		}
	default:
		return false
	}
	for _, argument := range arguments {
		bare := argument
		if ast.IsSpreadElement(bare) {
			bare = bare.AsSpreadElement().Expression
		}
		if ast.IsIdentifier(bare) && bare.Text() == name {
			return true
		}
	}
	return false
}

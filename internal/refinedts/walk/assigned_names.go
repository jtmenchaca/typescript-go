// from control_flow/assigned_names.ts
//
// Write-set scanners a loop (and its callers) accumulate into a Set:
// assignments, call-mediated closed-over writes, and break detection.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// AssignedNames adds every name the node may write: assignments,
// compound writes, ++/--, the receiver of a non-read-only method
// call, and any REFERENCE handed to a call (a callee may write a
// reference argument; a value-sorted word travels by copy). Pass
// the checker to filter handed arguments by reference sort; nil
// takes the conservative reading with every handed argument
// counted. Over-inclusion is safe — a name that never actually
// changes stabilizes immediately.
func AssignedNames(c *checker.Checker, node *ast.Node, into map[string]struct{}) {
	var held map[string]struct{}
	if c == nil {
		held = dataflowfacts.AssignedNameSetUnfiltered(node)
	} else {
		held = dataflowfacts.AssignedNameSet(c, node)
	}
	for name := range held {
		into[name] = struct{}{}
	}
}

// AssignedNamesDirect adds only the names the TEXT itself assigns —
// call-mediated writes are excluded, for a caller that models
// callee effects precisely (the try walk's per-statement snapshots
// carry them).
func AssignedNamesDirect(node *ast.Node, into map[string]struct{}) {
	for name := range dataflowfacts.AssignedDirectSet(node) {
		into[name] = struct{}{}
	}
}

// callMediatedWritesContract is the shape callMediatedWrites reads
// off a contract: only the declaration is used.
type callMediatedWritesContract struct {
	Declaration *ast.Node // FunctionDeclaration | MethodDeclaration | ArrowFunction | FunctionExpression
}

// CallMediatedWrites adds the CLOSED-OVER names a subtree's calls
// may write: each in-view callee's own assignments, minus its
// locals, followed through its callees in turn (cycle-safe). The
// caller's text never spells these writes — tailwindcss's
// `transform(root, 0)` pushed to a closed-over array, and the loop
// solver froze the array at [] — so every AssignedNames-based write
// model unions this in.
func CallMediatedWrites(
	c *checker.Checker,
	contracts map[*ast.Symbol]*FunctionContract,
	node *ast.Node,
	into map[string]struct{},
	visited map[*ast.Node]struct{},
) {
	if visited == nil {
		visited = map[*ast.Node]struct{}{}
	}
	var visit func(n *ast.Node) bool
	visit = func(n *ast.Node) bool {
		if ast.IsCallExpression(n) {
			call := n.AsCallExpression()
			var calleeName *ast.Node
			switch {
			case ast.IsIdentifier(call.Expression):
				calleeName = call.Expression
			case ast.IsPropertyAccessExpression(call.Expression):
				calleeName = call.Expression.AsPropertyAccessExpression().Name()
			}
			if calleeName != nil {
				symbol := symbolAt(c, calleeName)
				var contract *FunctionContract
				if symbol != nil {
					contract = contracts[symbol]
				}
				var declaration *ast.Node
				if contract != nil {
					declaration = contract.Declaration
				}
				if declaration != nil && declaration.Body() != nil {
					if _, seen := visited[declaration]; !seen {
						visited[declaration] = struct{}{}
						written := dataflowfacts.AssignedNameSetUnfiltered(declaration.Body())
						locals := dataflowfacts.CalleeLocalSet(declaration)
						for name := range written {
							if _, isLocal := locals[name]; !isLocal {
								into[name] = struct{}{}
							}
						}
						CallMediatedWrites(c, contracts, declaration.Body(), into, visited)
					}
				}
			}
		}
		n.ForEachChild(visit)
		return false
	}
	visit(node)
}

func ContainsBreak(node *ast.Node) bool {
	if ast.IsBreakStatement(node) {
		return true
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if !found {
			found = ContainsBreak(child)
		}
		return false
	})
	return found
}

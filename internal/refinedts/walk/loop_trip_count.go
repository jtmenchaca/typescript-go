// from control_flow/loop_trip_count.ts
//
// Literal trip counts and push-only argument collection for loops
// whose body only ever pushes onto a named array.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// literalOne is a numeric literal token spelling exactly 1.
func literalOne(e *ast.Node) bool {
	return ast.IsNumericLiteral(e) && float64(jsnum.FromString(e.Text())) == 1
}

// LiteralTripCount is literalTripCount in the TS source: the exact
// iteration count of `for (let i = A; i < B; i++)` with numeric
// literals, a unit increment, an index the body never writes, and no
// break, continue, or return — (0, false) anywhere the count is not
// pinned by the syntax alone. The start and the bound each read
// through const-to-const links, so `const N = 10; for (let i = 0; i <
// N; i++)` counts; a nil checker reads literal tokens only.
func LiteralTripCount(loop *ast.Node) (int, bool) {
	return LiteralTripCountWith(nil, loop)
}

// LiteralTripCountWith is LiteralTripCount with the checker that
// resolves a const-bound start or bound to its literal.
func LiteralTripCountWith(c *checker.Checker, loop *ast.Node) (int, bool) {
	if !ast.IsForStatement(loop) {
		return 0, false
	}
	forStmt := loop.AsForStatement()
	initializer := forStmt.Initializer
	if initializer == nil || !ast.IsVariableDeclarationList(initializer) {
		return 0, false
	}
	declarations := initializer.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return 0, false
	}
	declaration := declarations[0].AsVariableDeclaration()
	if !ast.IsIdentifier(declaration.Name()) || declaration.Initializer == nil {
		return 0, false
	}
	start, hasStart := dataflowfacts.ConstChainNumber(c, declaration.Initializer)
	if !hasStart {
		return 0, false
	}
	index := declaration.Name().Text()
	condition := forStmt.Condition
	if condition == nil || !ast.IsBinaryExpression(condition) {
		return 0, false
	}
	cond := condition.AsBinaryExpression()
	if !ast.IsIdentifier(cond.Left) || cond.Left.Text() != index {
		return 0, false
	}
	bound, hasBound := dataflowfacts.ConstChainNumber(c, cond.Right)
	if !hasBound {
		return 0, false
	}
	strict := cond.OperatorToken.Kind == ast.KindLessThanToken
	if !strict && cond.OperatorToken.Kind != ast.KindLessThanEqualsToken {
		return 0, false
	}
	incrementor := forStmt.Incrementor
	unitStep := false
	if incrementor != nil {
		if ast.IsPostfixUnaryExpression(incrementor) {
			unary := incrementor.AsPostfixUnaryExpression()
			unitStep = unary.Operator == ast.KindPlusPlusToken && ast.IsIdentifier(unary.Operand) && unary.Operand.Text() == index
		} else if ast.IsPrefixUnaryExpression(incrementor) {
			unary := incrementor.AsPrefixUnaryExpression()
			unitStep = unary.Operator == ast.KindPlusPlusToken && ast.IsIdentifier(unary.Operand) && unary.Operand.Text() == index
		} else if ast.IsBinaryExpression(incrementor) {
			// the same unit step spelled as an assignment: `i = i + 1`,
			// `i = 1 + i`, `i += 1`
			step := incrementor.AsBinaryExpression()
			if ast.IsIdentifier(step.Left) && step.Left.Text() == index {
				if step.OperatorToken.Kind == ast.KindPlusEqualsToken {
					unitStep = literalOne(step.Right)
				}
				if step.OperatorToken.Kind == ast.KindEqualsToken && ast.IsBinaryExpression(step.Right) {
					add := step.Right.AsBinaryExpression()
					if add.OperatorToken.Kind == ast.KindPlusToken {
						unitStep = (ast.IsIdentifier(add.Left) && add.Left.Text() == index && literalOne(add.Right)) ||
							(ast.IsIdentifier(add.Right) && add.Right.Text() == index && literalOne(add.Left))
					}
				}
			}
		}
	}
	if !unitStep {
		return 0, false
	}
	if start != float64(int(start)) || bound != float64(int(bound)) {
		return 0, false
	}
	escapes := false
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if escapes {
			return
		}
		if ast.IsBreakStatement(node) || ast.IsContinueStatement(node) || ast.IsReturnStatement(node) {
			escapes = true
		}
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment &&
				ast.IsIdentifier(bin.Left) && bin.Left.Text() == index {
				escapes = true
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if node != incrementor && ast.IsIdentifier(unary.Operand) && unary.Operand.Text() == index {
				escapes = true
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if node != incrementor && ast.IsIdentifier(unary.Operand) && unary.Operand.Text() == index {
				escapes = true
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	scan(forStmt.Statement)
	if escapes {
		return 0, false
	}
	count := bound - start
	if !strict {
		count++
	}
	if count < 0 {
		count = 0
	}
	return int(count), true
}

// TopLevelPushArguments is topLevelPushArguments in the TS source:
// how many elements one iteration pushes when EVERY push sits
// unconditionally at the body's top level — (0, false) when any push
// hides deeper (a conditional push breaks the count). The pushed array
// is named as a bare binding; TopLevelPushArgumentsAt reads the same
// count for a property path.
func TopLevelPushArguments(loop *ast.Node, name string) (int, bool) {
	return TopLevelPushArgumentsAt(loop, dataflowfacts.TrackedPlace{Binding: name})
}

// TopLevelPushArgumentsAt is TopLevelPushArguments over the package's
// place vocabulary (dataflowfacts.TrackedPlace): a bare name, or a
// stable property path off a tracked root — `this.items.push(x)` counts
// the same way `items.push(x)` does. A push whose receiver reads as a
// different place, or as no place at all (a computed index, a call),
// contributes nothing.
func TopLevelPushArgumentsAt(loop *ast.Node, place dataflowfacts.TrackedPlace) (int, bool) {
	var body *ast.Node
	if ast.IsForStatement(loop) {
		body = loop.AsForStatement().Statement
	}
	if body == nil || !ast.IsBlock(body) {
		return 0, false
	}
	count := 0
	for _, statement := range body.AsBlock().Statements.Nodes {
		if !ast.IsExpressionStatement(statement) {
			continue
		}
		e := statement.AsExpressionStatement().Expression
		if !ast.IsCallExpression(e) {
			continue
		}
		call := e.AsCallExpression()
		if !ast.IsPropertyAccessExpression(call.Expression) {
			continue
		}
		access := call.Expression.AsPropertyAccessExpression()
		if access.Name().Text() != "push" {
			continue
		}
		receiver := dataflowfacts.TrackedPlaceOf(access.Expression, func(name string) bool { return name == place.Binding })
		if receiver == nil || !dataflowfacts.SameTrackedPlace(*receiver, place) {
			continue
		}
		if call.Arguments != nil {
			count += len(call.Arguments.Nodes)
		}
	}
	if count == 0 {
		return 0, false
	}
	return count, true
}

// PushOnlyArguments is pushOnlyArguments in the TS source: the
// arguments of every `name.push(...)` in the loop, or (nil, false)
// when the name appears ANYWHERE else inside it — an assignment,
// another method, a read, an argument — since then pushing is not
// the whole story of the binding.
func PushOnlyArguments(loop *ast.Node, name string) ([]*ast.Node, bool) {
	return PushOnlyArgumentsAt(loop, dataflowfacts.TrackedPlace{Binding: name})
}

// PushOnlyArgumentsAt is PushOnlyArguments over the package's place
// vocabulary: the arguments of every `<place>.push(...)` in the loop,
// or (nil, false) when the place is touched any other way.
//
// The disqualification scan runs over the WHOLE path's mentions, not
// just the leaf: every root mention inside the loop is resolved to the
// place it names, and any mention that is not this place's own push
// disqualifies. A root mention naming a PREFIX of the place — plain
// `this` where the place is `this.items`, or `this.items` passed
// somewhere as a whole — reaches the array through a shorter path and
// can rebind or mutate it, so it disqualifies too; a mention naming a
// DISJOINT sibling place (`this.other`) leaves this one alone and is
// skipped. A mention the place reading cannot resolve at all (a
// computed index off the root) disqualifies, since it may be this
// array.
func PushOnlyArgumentsAt(loop *ast.Node, place dataflowfacts.TrackedPlace) ([]*ast.Node, bool) {
	var collected []*ast.Node
	disqualified := false
	isRoot := func(name string) bool { return name == place.Binding }
	// rootMention is the outermost access expression grown from this
	// mention of the root — `this` inside `this.items.push` answers the
	// `this.items` access, which is the place a push names.
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if disqualified {
			return
		}
		mentionsRoot := (ast.IsIdentifier(node) && node.Text() == place.Binding) ||
			(node.Kind == ast.KindThisKeyword && place.Binding == "this")
		if mentionsRoot {
			// climb the access chain this mention roots, then read the
			// widest place it spells
			widest := node
			for widest.Parent != nil &&
				((ast.IsPropertyAccessExpression(widest.Parent) && widest.Parent.AsPropertyAccessExpression().Expression == widest) ||
					(ast.IsElementAccessExpression(widest.Parent) && widest.Parent.AsElementAccessExpression().Expression == widest)) {
				widest = widest.Parent
			}
			named := dataflowfacts.TrackedPlaceOf(widest, isRoot)
			if named == nil {
				disqualified = true
				return
			}
			// the push shape: the widest place is `<place>.push` and its
			// parent is the call on it
			if len(named.Path) == len(place.Path)+1 &&
				named.Path[len(named.Path)-1] == "push" &&
				dataflowfacts.SameTrackedPlace(dataflowfacts.TrackedPlace{Binding: named.Binding, Path: named.Path[:len(named.Path)-1]}, place) {
				call := widest.Parent
				if call != nil && ast.IsCallExpression(call) && call.AsCallExpression().Expression == widest {
					if call.AsCallExpression().Arguments != nil {
						collected = append(collected, call.AsCallExpression().Arguments.Nodes...)
					}
					return
				}
				disqualified = true
				return
			}
			// a place at or above this one reaches the array; a disjoint
			// sibling does not
			if placeCovers(*named, place) {
				disqualified = true
			}
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(loop)
	if disqualified {
		return nil, false
	}
	return collected, true
}

// PushedPlaceCandidate is one property-path array the loop's body only
// ever pushes onto — the place the growth lands under, and the access
// expression that spells it, which the entry reading evaluates.
type PushedPlaceCandidate struct {
	Place  dataflowfacts.TrackedPlace
	Access *ast.Node
}

// PushedPlaceCandidates is every PROPERTY-PATH array the loop pushes
// onto — `this.items.push(x)`, `state.rows.push(y)` — one candidate per
// distinct place, each carrying the receiver expression the push named.
//
// A BARE name is never a candidate here: the name path already grows it
// from the fixpointed list, and feeding it twice would land two answers
// for one binding. Only paths of at least one segment come back.
//
// The scan reads receivers, not writes: a push whose receiver reads as
// no place at all (a computed index, a call result) contributes nothing,
// and the caller still has to run PushOnlyArgumentsAt over each
// candidate — this function says WHICH places the loop pushes onto, and
// that function says whether pushing is the whole story of each.
func PushedPlaceCandidates(loop *ast.Node, isRoot func(name string) bool) []PushedPlaceCandidate {
	var candidates []PushedPlaceCandidate
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			if ast.IsPropertyAccessExpression(call.Expression) {
				access := call.Expression.AsPropertyAccessExpression()
				if access.Name().Text() == "push" {
					if receiver := dataflowfacts.TrackedPlaceOf(access.Expression, isRoot); receiver != nil && len(receiver.Path) > 0 {
						seen := false
						for _, held := range candidates {
							if dataflowfacts.SameTrackedPlace(held.Place, *receiver) {
								seen = true
								break
							}
						}
						if !seen {
							candidates = append(candidates, PushedPlaceCandidate{Place: *receiver, Access: access.Expression})
						}
					}
				}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(loop)
	return candidates
}

// placeCovers is whether `outer` names the array `place` names or
// something containing it — the same binding, with a path that is a
// prefix of (or equal to, or extends) the place's own. A path that
// diverges at any segment names a different array.
func placeCovers(outer dataflowfacts.TrackedPlace, place dataflowfacts.TrackedPlace) bool {
	if outer.Binding != place.Binding {
		return false
	}
	shorter := len(outer.Path)
	if len(place.Path) < shorter {
		shorter = len(place.Path)
	}
	for i := 0; i < shorter; i++ {
		if outer.Path[i] != place.Path[i] {
			return false
		}
	}
	return true
}

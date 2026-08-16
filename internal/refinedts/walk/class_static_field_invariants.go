// Static field invariants: what a class's OWN static field provably
// holds at every read through the class's own name — the join of the
// field's initializer with every value the class's own text writes
// into it, spelled either `ClassName.field = v` or, inside a static
// block or static method, `this.field = v` (a static block's and a
// static method's `this` names the constructor object itself, sec-
// runtime-semantics-classdefinitionevaluation and sec-static-
// semantics-early-errors for class static blocks).
//
// A static field carries no privacy fence at all — `private static`
// is erased at runtime the same way an instance `private` field is —
// so this reads only the class's OWN declaration text: every static
// property initializer and every direct write inside the class body
// (a static block's top-level statements, a static method's top-level
// statements). A write reached through anything this scan cannot
// spell (a computed key, a destructuring target, a call that might
// write it) POISONS the field the same way class_field_invariants.go
// poisons an instance field it cannot fully collect — the invariant
// then answers nothing, never a stale guess.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

var (
	staticInvariantMemoMu sync.Mutex
	staticInvariantMemo   = map[*ast.Node]map[string]abstractdomain.AbstractValue{}
)

// StaticFieldInvariantsOf is the invariants of a class's static
// fields, memoized per declaration. computeStaticFieldInvariants does
// the collection; entries live as long as the program that produced
// the declaration (the same substitution class_field_invariants.go's
// invariantMemo takes for Go's lack of a WeakMap).
func StaticFieldInvariantsOf(ctx *FlowContext, declaration *ast.Node) map[string]abstractdomain.AbstractValue {
	staticInvariantMemoMu.Lock()
	if held, ok := staticInvariantMemo[declaration]; ok {
		staticInvariantMemoMu.Unlock()
		return held
	}
	staticInvariantMemoMu.Unlock()
	computed := computeStaticFieldInvariants(ctx, declaration)
	staticInvariantMemoMu.Lock()
	staticInvariantMemo[declaration] = computed
	staticInvariantMemoMu.Unlock()
	return computed
}

// staticWriteTargetName: the field name a PLAIN assignment writes —
// `ClassName.field = v` or `this.field = v`, receiver naming the
// given class. ("", false) for every other shape: a compound assign,
// an element access, a nested pattern, or a receiver that names
// neither the class nor `this` — the caller's own tests handle those.
func staticWriteTargetName(className string, node *ast.Node) (name string, isPlainWrite bool) {
	if !ast.IsBinaryExpression(node) {
		return "", false
	}
	be := node.AsBinaryExpression()
	if be.OperatorToken.Kind != ast.KindEqualsToken {
		return "", false
	}
	left := be.Left
	if !ast.IsPropertyAccessExpression(left) {
		return "", false
	}
	access := left.AsPropertyAccessExpression()
	nameNode := access.Name()
	if nameNode == nil || !(ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
		return "", false
	}
	receiver := access.Expression
	isClassName := ast.IsIdentifier(receiver) && receiver.Text() == className
	isThis := receiver.Kind == ast.KindThisKeyword
	if !isClassName && !isThis {
		return "", false
	}
	return nameNode.Text(), true
}

// scanStaticWrites walks a static block's or static method's body for
// every write that could touch a static field: a plain `ClassName.f =
// v` / `this.f = v` at any depth (not just the top statement — an `if`
// branch's write still lands), recorded into sink; anything else that
// writes THROUGH the class name or `this` (a compound assign, an
// element access, a destructuring target, `delete`) poisons instead,
// because the collection cannot place what value landed.
//
// unconditional additionally records, per name, whether EVERY write
// sunk for it sits directly in the block's own straight-line statement
// list — never crossing an `if`/`for`/`while`/`switch`/`try`/label or
// any other construct that could skip it. A write reached that way
// always runs, in the order it is written, so the caller can fold the
// sequence as LAST-WRITE-WINS instead of joining it with the
// initializer — `static total = 0; static { OverCounted.total = 200;
// }` runs the write unconditionally, so `total` is never observably 0
// after class evaluation and joining the two would wrongly keep 0
// alive. A write reached through any conditional construct clears the
// name from unconditional (once cleared, never re-set), falling back
// to the existing join-based reading, which is sound wherever the
// write might not run.
func scanStaticWrites(className string, node *ast.Node, sink map[string][]*ast.Node, poisoned map[string]struct{}, unconditional map[string]bool) {
	var visitStraightLine func(n *ast.Node)
	var visitConditional func(n *ast.Node)
	markWrite := func(n *ast.Node, straightLine bool) bool {
		if !ast.IsBinaryExpression(n) {
			return false
		}
		be := n.AsBinaryExpression()
		name, isPlain := staticWriteTargetName(className, n)
		if !isPlain {
			return false
		}
		sink[name] = append(sink[name], be.Right)
		if straightLine {
			if _, seen := unconditional[name]; !seen {
				unconditional[name] = true
			}
		} else {
			unconditional[name] = false
		}
		return true
	}
	markPoison := func(n *ast.Node) {
		if ast.IsBinaryExpression(n) {
			be := n.AsBinaryExpression()
			// a compound assign or any other binary write through the
			// class name or `this` poisons the name it can place
			if be.OperatorToken.Kind >= ast.KindFirstCompoundAssignment && be.OperatorToken.Kind <= ast.KindLastCompoundAssignment &&
				ast.IsPropertyAccessExpression(be.Left) {
				access := be.Left.AsPropertyAccessExpression()
				receiver := access.Expression
				if (ast.IsIdentifier(receiver) && receiver.Text() == className) || receiver.Kind == ast.KindThisKeyword {
					if nameNode := access.Name(); nameNode != nil && (ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
						poisoned[nameNode.Text()] = struct{}{}
						unconditional[nameNode.Text()] = false
					}
				}
			}
		}
		// `ClassName.f++` / `--` / `delete ClassName.f` — the same
		// receiver test, poisoning what it can place
		if ast.IsPostfixUnaryExpression(n) || ast.IsPrefixUnaryExpression(n) || ast.IsDeleteExpression(n) {
			var target *ast.Node
			switch {
			case ast.IsPostfixUnaryExpression(n):
				target = n.AsPostfixUnaryExpression().Operand
			case ast.IsPrefixUnaryExpression(n):
				target = n.AsPrefixUnaryExpression().Operand
			case ast.IsDeleteExpression(n):
				target = n.AsDeleteExpression().Expression
			}
			if target != nil && ast.IsPropertyAccessExpression(target) {
				access := target.AsPropertyAccessExpression()
				receiver := access.Expression
				if (ast.IsIdentifier(receiver) && receiver.Text() == className) || receiver.Kind == ast.KindThisKeyword {
					if nameNode := access.Name(); nameNode != nil && (ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
						poisoned[nameNode.Text()] = struct{}{}
						unconditional[nameNode.Text()] = false
					}
				}
			}
		}
	}
	// visitStraightLine walks a node every enclosing construct so far
	// has run unconditionally. Its own direct statement children stay
	// straight-line; anything that names a construct which might skip
	// its body hands off to visitConditional for that subtree.
	visitStraightLine = func(n *ast.Node) {
		if n != node && (ast.IsFunctionDeclaration(n) || ast.IsFunctionExpression(n) ||
			ast.IsArrowFunction(n) || ast.IsClassDeclaration(n) || ast.IsClassExpression(n)) {
			return
		}
		if markWrite(n, true) {
			visitConditional(n.AsBinaryExpression().Right)
			return
		}
		markPoison(n)
		if ast.IsIfStatement(n) || ast.IsForStatement(n) || ast.IsForInStatement(n) || ast.IsForOfStatement(n) ||
			ast.IsWhileStatement(n) || ast.IsDoStatement(n) || ast.IsSwitchStatement(n) ||
			ast.IsTryStatement(n) || ast.IsLabeledStatement(n) || ast.IsCatchClause(n) {
			visitConditional(n)
			return
		}
		n.ForEachChild(func(child *ast.Node) bool {
			visitStraightLine(child)
			return false
		})
	}
	// visitConditional walks a subtree some enclosing construct might
	// skip: writes still sink (the existing join reading covers them),
	// but never count as unconditional, regardless of their own local
	// shape.
	visitConditional = func(n *ast.Node) {
		if n != node && (ast.IsFunctionDeclaration(n) || ast.IsFunctionExpression(n) ||
			ast.IsArrowFunction(n) || ast.IsClassDeclaration(n) || ast.IsClassExpression(n)) {
			return
		}
		if markWrite(n, false) {
			visitConditional(n.AsBinaryExpression().Right)
			return
		}
		markPoison(n)
		n.ForEachChild(func(child *ast.Node) bool {
			visitConditional(child)
			return false
		})
	}
	visitStraightLine(node)
}

// computeStaticFieldInvariants collects a class's static field
// invariants: each field's own literal initializer, joined with every
// value a static block or static method body writes into it through
// the class's own name or `this`. A field no writer names keeps its
// initializer alone; a field some write poisons (an unspellable write
// landed on it) answers nothing — silence over a stale guess.
func computeStaticFieldInvariants(ctx *FlowContext, declaration *ast.Node) map[string]abstractdomain.AbstractValue {
	if declaration == nil || !ast.IsClassLike(declaration) {
		return map[string]abstractdomain.AbstractValue{}
	}
	className := ""
	if ast.IsClassDeclaration(declaration) {
		if name := declaration.Name(); name != nil && ast.IsIdentifier(name) {
			className = name.Text()
		}
	}
	members := declaration.ClassLikeData().Members.Nodes

	var candidateOrder []string
	candidates := map[string]abstractdomain.AbstractValue{}
	silent := *ctx
	silent.Report = func(assignability.RefinementDiagnostic) {}

	for _, member := range members {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		pd := member.AsPropertyDeclaration()
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic == 0 {
			continue
		}
		name := pd.Name()
		if name == nil || !(ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name)) {
			continue
		}
		nameText := name.Text()
		var value abstractdomain.AbstractValue
		if pd.Initializer == nil {
			value = abstractdomain.Undef
		} else {
			value = evaluateExpression(&silent, NewEnv(), pd.Initializer)
		}
		if _, seen := candidates[nameText]; !seen {
			candidateOrder = append(candidateOrder, nameText)
		}
		candidates[nameText] = value
	}
	if len(candidates) == 0 {
		return map[string]abstractdomain.AbstractValue{}
	}

	sink := map[string][]*ast.Node{}
	poisoned := map[string]struct{}{}
	unconditional := map[string]bool{}
	for _, member := range members {
		var body *ast.Node
		switch {
		case ast.IsClassStaticBlockDeclaration(member):
			body = member.AsClassStaticBlockDeclaration().Body
		case ast.IsMethodDeclaration(member) && ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0:
			body = member.Body()
		default:
			continue
		}
		if body == nil {
			continue
		}
		scanStaticWrites(className, body, sink, poisoned, unconditional)
	}

	invariants := map[string]abstractdomain.AbstractValue{}
	for _, name := range candidateOrder {
		if _, isPoisoned := poisoned[name]; isPoisoned {
			continue
		}
		var result abstractdomain.AbstractValue
		if unconditional[name] && len(sink[name]) > 0 {
			// every write sunk for this name sits in the block's own
			// straight-line statement list — it always runs, in the order
			// written, so the LAST one is what a read observes after class
			// evaluation. Class evaluation runs top-to-bottom
			// (sec-runtime-semantics-classdefinitionevaluation), and the
			// initializer is itself just the first write in that order —
			// an unconditional later write in the same order replaces it
			// outright rather than joining with it.
			last := sink[name][len(sink[name])-1]
			result = evaluateExpression(&silent, NewEnv(), last)
		} else {
			joined := candidates[name]
			for _, writeNode := range sink[name] {
				written := evaluateExpression(&silent, NewEnv(), writeNode)
				joined = abstractdomain.JoinKnown(joined, written)
			}
			result = joined
		}
		if result.Kind != abstractdomain.KindUnknown {
			invariants[name] = result
		}
	}
	return invariants
}

// ReadStaticFieldAccess reads `ClassName.field` through the class's
// own static field invariants — the constructor-object twin of
// ReadThisPropertyAccess/ReadObjectKeyAccess's instance reads. Nil
// wherever the receiver does not name a class declaration in reach,
// or the field carries no invariant (no candidate, or poisoned).
func ReadStaticFieldAccess(ctx *FlowContext, e *ast.Node) *abstractdomain.AbstractValue {
	if !ast.IsPropertyAccessExpression(e) {
		return nil
	}
	pa := e.AsPropertyAccessExpression()
	if !ast.IsIdentifier(pa.Expression) {
		return nil
	}
	c := checkerOf(ctx)
	if c == nil {
		return nil
	}
	symbol := symbolAt(c, pa.Expression)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsClassDeclaration(declaration) || ast.GetSourceFileOfNode(declaration).IsDeclarationFile {
		return nil
	}
	nameNode := pa.Name()
	if nameNode == nil || !(ast.IsIdentifier(nameNode) || ast.IsPrivateIdentifier(nameNode)) {
		return nil
	}
	invariants := StaticFieldInvariantsOf(ctx, declaration)
	if invariants == nil {
		return nil
	}
	if v, ok := invariants[nameNode.Text()]; ok {
		return &v
	}
	return nil
}

// split from ir_callback_summary.go — callback recognition: which
// argument spellings are a callback at all, and the free-name scan and
// capture resolution that decide whether one converts
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// arrowFunctionOf is the arrow or function expression an argument is,
// through parens and casts — or nil. A function EXPRESSION converts on
// the same terms an arrow does; what matters is that the body is
// lowerable and the free names are captures, not which spelling the
// source used. (A function expression's own `this` differs at runtime,
// which is why a `this` used as a call receiver still has to resolve
// through ResolveCallee rather than being assumed.)
func arrowFunctionOf(argument *ast.Node) *ast.Node {
	if argument == nil {
		return nil
	}
	head := Unwrapped(argument)
	if ast.IsArrowFunction(head) || ast.IsFunctionExpression(head) {
		return head
	}
	return nil
}

// mentionsThis is whether a subtree reads `this` ANYWHERE — as a value,
// as a call receiver, as a property root. Unlike scanFreeNames, which
// lets `this.m(…)` pass because the receiver is consumed by the callee
// resolution, this one admits no position at all: it answers the
// question a REFERENCE asks, which is whether the body would notice
// being called with a different receiver, and a `this.m(…)` call would
// notice as surely as a `this.field` read.
//
// A nested function is walked into as well, and that over-reports: an
// inner function expression's own `this` is its own. The over-report
// only ever declines more, which is the safe direction here, and the
// free-name scan declines nested functions outright anyway.
func mentionsThis(node *ast.Node) bool {
	if node == nil {
		return false
	}
	found := false
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if found {
			return true
		}
		if child.Kind == ast.KindThisKeyword {
			found = true
			return true
		}
		child.ForEachChild(visit)
		return false
	}
	visit(node)
	return found
}

// referencedFunctionOf is the declaration a callback argument NAMES,
// where the argument is a bare identifier (`xs.forEach(handler)`) or a
// `this.<name>` property read (`xs.forEach(this.onItem)`), resolved
// through the lowering context's own ResolveCallee — the same
// resolution the call routes use, so a name never stands for one
// declaration here and another there.
//
// The RECEIVER RULE, and why it is the one below. Handing a method over
// by name does not hand its receiver over with it: `xs.forEach(this.f)`
// calls f with `this` undefined (or the traversal's own thisArg), not
// with the object f was read off. A conversion that lowered f's body as
// though `this` were still bound would read the caller's this-bundle
// slots for values the run never sees there — a wrong answer, not a
// weak one.
//
// So the rule is a body census rather than a spelling census: a
// referenced body that mentions `this` in ANY position declines, naming
// a method reference losing its receiver. A body with no `this` at all
// cannot observe which receiver it was called on, so the bare call and
// the bound call agree on every value, and the conversion is sound
// whether the name was reached as a free function, an arrow held in a
// const, or a method read off `this`. Free functions and const-held
// arrows pass this census by construction, which is why they are the
// shapes that convert in practice.
//
// A body-less declaration, a generator, and an overridden method all
// answer nil through summaryLowerable and ResolveCallee's own gates.
func referencedFunctionOf(context *LoweringContext, argument *ast.Node) *ast.Node {
	if context == nil || context.ResolveCallee == nil || argument == nil {
		return nil
	}
	head := Unwrapped(argument)
	if head == nil {
		return nil
	}
	// the two reference spellings a slot-shaped lowering can name: a bare
	// identifier, and one `this.` step. Anything deeper (`a.b.f`) or
	// computed (`a[k]`) is left alone — ResolveCallee would answer for
	// the property name, and the extra steps are receiver structure this
	// route makes no claim about.
	switch {
	case ast.IsIdentifier(head):
	case ast.IsPropertyAccessExpression(head):
		access := head.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
			return nil
		}
		if Unwrapped(access.Expression).Kind != ast.KindThisKeyword {
			return nil
		}
	default:
		return nil
	}
	declaration := context.ResolveCallee(head)
	if declaration == nil || !summaryLowerable(declaration) {
		return nil
	}
	// a method reference losing its receiver: the body would read a
	// `this` the bare call does not supply
	if mentionsThis(declaration.Body()) {
		return nil
	}
	return declaration
}

// callbackFunctionOf is the function a callback argument stands for,
// whichever way it was spelled: the inline arrow or function expression
// arrowFunctionOf reads, or the declaration a name refers to. Every
// route in this file that used to ask arrowFunctionOf asks this
// instead, so an inline arrow and a named reference convert on one set
// of terms rather than two.
//
// The answer is a node with .Parameters() and .Body(), which is all the
// conversion below reads — lowerArrowSummary lowers a function
// declaration through the same door it lowers an arrow, and the blob
// cache keys on the node either way. A referenced declaration reached
// from two different traversals is ONE node, so the cache's
// layout-agreement check (sameParameterSorts / sameCaptures) is what
// keeps two sites from sharing a blob they disagree about — the same
// protection an arrow gets, now doing real work, since a named function
// genuinely can be passed to two collections of different element sorts.
func callbackFunctionOf(context *LoweringContext, argument *ast.Node) *ast.Node {
	if arrow := arrowFunctionOf(argument); arrow != nil {
		return arrow
	}
	return referencedFunctionOf(context, argument)
}

// arrowParameterNames is an arrow's declared parameter names, or
// declines: a default, a rest, or a binding pattern is not one entry
// the call site can fill.
func arrowParameterNames(arrow *ast.Node) ([]string, bool) {
	var names []string
	for _, parameter := range arrow.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) || pd.Initializer != nil || pd.DotDotDotToken != nil {
			return nil, false
		}
		names = append(names, pd.Name().Text())
	}
	return names, true
}

// arrowBoundNames is every name bound INSIDE the arrow's body: its
// parameters, and every local it declares (including the names an
// object binding pattern binds). A read of one of these is not a free
// name, so the capture scan skips it.
func arrowBoundNames(arrow *ast.Node) map[string]struct{} {
	bound := map[string]struct{}{}
	for _, parameter := range arrow.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if ast.IsIdentifier(pd.Name()) {
			bound[pd.Name().Text()] = struct{}{}
		}
	}
	body := arrow.Body()
	if body == nil {
		return bound
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsVariableDeclaration(node) || ast.IsBindingElement(node) {
			name := node.Name()
			if name != nil && ast.IsIdentifier(name) {
				bound[name.Text()] = struct{}{}
			}
		}
		// a for-of / for-in element binding and a catch clause's parameter
		// arrive through the same declaration nodes above; nothing else
		// binds a plain name in the lowered subset
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return bound
}

// writtenNamesIn is every name the subtree WRITES: an assignment's
// left side, a compound assignment's, and a step's operand — each read
// through its spelled head, so `total += 1` reports "total" and
// `o.k = 1` reports "o".
//
// It walks into nested functions too, which over-reports: a write to a
// NESTED function's own local would be read as a captured write here.
// The over-report changes no outcome — a nested function declines the
// arrow outright in the scan below — and erring toward decline is the
// safe direction for a rule whose whole job is to catch writes.
func writtenNamesIn(node *ast.Node) map[string]struct{} {
	written := map[string]struct{}{}
	note := func(target *ast.Node) {
		head := Unwrapped(target)
		for ast.IsPropertyAccessExpression(head) {
			head = Unwrapped(head.AsPropertyAccessExpression().Expression)
		}
		for ast.IsElementAccessExpression(head) {
			head = Unwrapped(head.AsElementAccessExpression().Expression)
		}
		if ast.IsIdentifier(head) {
			written[head.Text()] = struct{}{}
		}
	}
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if ast.IsBinaryExpression(child) {
			bin := child.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment {
				note(bin.Left)
			}
		}
		if ast.IsPrefixUnaryExpression(child) {
			unary := child.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				note(unary.Operand)
			}
		}
		if ast.IsPostfixUnaryExpression(child) {
			unary := child.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				note(unary.Operand)
			}
		}
		child.ForEachChild(visit)
		return false
	}
	visit(node)
	return written
}

// freeNameScan is what the scan reports: the free names read, in SOURCE
// ORDER OF FIRST READ (the order the capture entries are laid out in),
// and whether the arrow left the convertible subset at all.
type freeNameScan struct {
	Reads []string
	Ok    bool
}

// scanFreeNames walks an arrow's body and reports its free reads in
// first-read order, declining where the arrow leaves the convertible
// subset:
//
//   - a nested function inside the arrow — its own captures would need
//     converting too, and the lowering declines nested functions anyway;
//   - `this` in any position other than the RECEIVER of a call — a
//     `this` read as a value is a whole object no slot holds;
//   - a WRITE to any name the arrow did not bind — the caller's state
//     would move and no entry carries it back.
//
// A `this.m(…)` call contributes NO free name: the receiver is consumed
// by the resolution, and whether the callee resolves is decided by the
// ordinary call lowering (which asks ResolveCallee) rather than here.
func scanFreeNames(arrow *ast.Node) freeNameScan {
	body := arrow.Body()
	if body == nil {
		return freeNameScan{}
	}
	bound := arrowBoundNames(arrow)
	written := writtenNamesIn(body)
	// a write to a name the arrow did not bind is a captured write
	for name := range written {
		if _, isBound := bound[name]; !isBound {
			return freeNameScan{}
		}
	}
	var reads []string
	seen := map[string]struct{}{}
	declined := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if declined {
			return true
		}
		if ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) ||
			ast.IsArrowFunction(node) || ast.IsClassDeclaration(node) ||
			ast.IsClassExpression(node) {
			declined = true
			return true
		}
		// `this.m(…)` — the receiver is consumed whole; the arguments still
		// scan, since they may read captures
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			callee := Unwrapped(call.Expression)
			if ast.IsPropertyAccessExpression(callee) {
				receiver := Unwrapped(callee.AsPropertyAccessExpression().Expression)
				if receiver.Kind == ast.KindThisKeyword {
					if call.Arguments != nil {
						for _, argument := range call.Arguments.Nodes {
							visit(argument)
						}
					}
					return false
				}
			}
		}
		// `this` anywhere else — as a value, as a property read, as an
		// argument — is a whole object no slot holds
		if node.Kind == ast.KindThisKeyword {
			declined = true
			return true
		}
		// a property access's NAME half is not a read of a binding
		if ast.IsPropertyAccessExpression(node) {
			visit(node.AsPropertyAccessExpression().Expression)
			return false
		}
		if ast.IsIdentifier(node) {
			name := node.Text()
			if _, isBound := bound[name]; !isBound {
				if _, already := seen[name]; !already {
					seen[name] = struct{}{}
					reads = append(reads, name)
				}
			}
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	if declined {
		return freeNameScan{}
	}
	return freeNameScan{Reads: reads, Ok: true}
}

// capturesOf turns the scan's free reads into capture entries, or
// declines: every free name must resolve to a slot in the ENCLOSING
// lowering, since the call site binds each entry to a `var` of that
// slot. A free name with no slot — an import, a global, an outer
// function — has no `var` to bind, so the arrow declines.
//
// The capture list keeps the scan's order, which is the layout the
// entries take after the declared parameters.
func capturesOf(context *LoweringContext, scan freeNameScan) ([]capturedSlot, []int, bool) {
	if !scan.Ok {
		return nil, nil, false
	}
	var captures []capturedSlot
	var slots []int
	for _, name := range scan.Reads {
		index, found := slotIndexOfName(context, name)
		if !found {
			return nil, nil, false
		}
		if index >= len(context.Sorts) || index >= len(context.Typeofs) {
			return nil, nil, false
		}
		captures = append(captures, capturedSlot{
			Name:      name,
			Sort:      context.Sorts[index],
			TypeofTag: context.Typeofs[index],
		})
		slots = append(slots, index)
	}
	return captures, slots, true
}

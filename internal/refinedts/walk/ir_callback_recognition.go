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
// declines: a default, a rest is not one entry the call site can fill.
//
// An OBJECT-BINDING-PATTERN parameter (`{ word, width }`) is no longer an
// outright decline here — it names no single identifier, so its own slot
// contributes an empty string rather than refusing the whole arrow. The
// pattern's bound names are read separately, by
// arrowParameterElementBindings below, wherever a caller needs to fill
// its individual leaves from a record-shaped element (an array whose
// ElementMembers the site's receiver expands). A caller that has no such
// per-member source still cannot fill a pattern parameter — that refusal
// happens at capturesOf/convert time, not here.
func arrowParameterNames(arrow *ast.Node) ([]string, bool) {
	var names []string
	for _, parameter := range arrow.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if pd.Initializer != nil || pd.DotDotDotToken != nil {
			return nil, false
		}
		if ast.IsObjectBindingPattern(pd.Name()) {
			if _, ok := objectPatternElementBindings(pd.Name()); !ok {
				return nil, false
			}
			names = append(names, "")
			continue
		}
		if !ast.IsIdentifier(pd.Name()) {
			return nil, false
		}
		names = append(names, pd.Name().Text())
	}
	return names, true
}

// patternElementBinding is one leaf an object-binding-pattern parameter
// binds: the BOUND local name, and the MEMBER KEY it reads from the
// element's own shape — the pattern's PropertyName where a rename
// spells one (`{ width: w }` binds "w" from member "width"), the bound
// name itself otherwise (`{ width }` binds "width" from member
// "width").
//
// Resolvable is false for an element whose OWN shape a caller slot
// cannot fill even once the member it names is found: a REST element
// (`...rest`, binds an object of everything else — no single slot holds
// that), a DEFAULTED element (`a = 5` — applying the default needs a
// definedness branch this leaf-for-leaf reader does not build), or a
// NESTED pattern (`{ a: { b } }` — the outer name `a` binds no scalar).
// The bound name still rides out (arrowBoundNames' capture scan must
// not mistake a body read of it for a free name), but the caller
// resolving leaves treats it as UNKNOWN rather than refusing every
// OTHER element in the same pattern.
type patternElementBinding struct {
	Bound      string
	Key        string
	Resolvable bool
}

// objectPatternElementBindings reads a `{ a, b: renamed, ...rest }`-shaped
// binding-pattern NAME (a ParameterDeclaration's own Name node) as one
// binding per element. A plain identifier element (renamed or not)
// binds Resolvable; a rest element, a defaulted element, or a nested
// pattern still binds its own name (Resolvable = false) rather than
// refusing the whole pattern — the same "an unspellable member takes an
// unknown slot" stance ir_summary_body_lowering_slots.go's array-pattern
// collection already takes. Only a COMPUTED property name (`{ [k]: v
// }`) refuses outright: it names no fixed member key at all, so no
// caller could resolve it even as an unknown leaf, and no name is bound
// for the capture scan to skip either.
func objectPatternElementBindings(name *ast.Node) ([]patternElementBinding, bool) {
	if name == nil || !ast.IsObjectBindingPattern(name) {
		return nil, false
	}
	var bindings []patternElementBinding
	seen := map[string]struct{}{}
	for _, element := range name.AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		// a REST element binds no member key at all — its own bound name
		// stands for "everything else", read from no single leaf
		if binding.DotDotDotToken != nil {
			if binding.Name() == nil || !ast.IsIdentifier(binding.Name()) {
				return nil, false
			}
			bound := binding.Name().Text()
			if _, duplicate := seen[bound]; duplicate {
				return nil, false
			}
			seen[bound] = struct{}{}
			bindings = append(bindings, patternElementBinding{Bound: bound, Resolvable: false})
			continue
		}
		if binding.Name() == nil {
			return nil, false
		}
		// a NESTED pattern element (`{ a: { b } }`) binds no scalar under
		// its own outer name — PropertyName carries the member key where
		// one is spelled, the nested pattern itself contributes nothing
		// this reader can bind a single local from
		if !ast.IsIdentifier(binding.Name()) {
			if binding.PropertyName == nil || !ast.IsIdentifier(binding.PropertyName) {
				return nil, false
			}
			// nothing bound under a single local name — the nested pattern's
			// OWN leaves are invisible here, so nothing is added to `seen`
			// or `bindings`; a body read inside the nested pattern is a
			// different name entirely and arrowBoundNames' own recursive
			// scan over BindingElement nodes (not this function) covers it
			continue
		}
		key := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				return nil, false
			}
			key = binding.PropertyName.Text()
		}
		bound := binding.Name().Text()
		if _, duplicate := seen[bound]; duplicate {
			return nil, false
		}
		seen[bound] = struct{}{}
		// a DEFAULTED element (`{ a = 5 }`) still binds "a" from member
		// "a" — the key resolution is unaffected — but is not Resolvable:
		// applying the default needs a definedness branch this leaf-for-
		// leaf reader does not build, so the CALLER treats it as unknown
		// rather than refusing the sibling elements around it.
		bindings = append(bindings, patternElementBinding{
			Bound: bound, Key: key, Resolvable: binding.Initializer == nil,
		})
	}
	if len(bindings) == 0 {
		return nil, false
	}
	return bindings, true
}

// arrowBoundNames is every name bound INSIDE the arrow's body: its
// parameters, and every local it declares (including the names an
// object binding pattern binds). A read of one of these is not a free
// name, so the capture scan skips it.
//
// A destructured parameter (`{ word, width }`) binds each of ITS OWN
// leaf names too — a body read of `word` is a parameter read, not a
// free capture, whether or not the pattern goes on to convert (a
// pattern this reader admits but capturesOf/convert time cannot fill is
// still a bound name; it just leaves the whole arrow declining
// elsewhere, never here).
func arrowBoundNames(arrow *ast.Node) map[string]struct{} {
	bound := map[string]struct{}{}
	for _, parameter := range arrow.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if ast.IsIdentifier(pd.Name()) {
			bound[pd.Name().Text()] = struct{}{}
			continue
		}
		if ast.IsObjectBindingPattern(pd.Name()) {
			if elements, ok := objectPatternElementBindings(pd.Name()); ok {
				for _, element := range elements {
					bound[element.Bound] = struct{}{}
				}
			}
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
//
// CalleeOnly marks the free names whose EVERY occurrence stands in
// callee position of a plain call (`getValue(links[id])`) — a name the
// body never reads as a value. Such a name needs no capture entry at
// all when it resolves to a real declaration: the interior call lowers
// through ResolveCallee exactly as it would in any body, and demanding
// a value slot for it was what declined every arrow calling a
// module-level helper. CalleeNode holds one representative callee node
// per such name for the resolution.
type freeNameScan struct {
	Reads      []string
	CalleeOnly map[string]bool
	CalleeNode map[string]*ast.Node
	Ok         bool
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
	calleeSeen := map[string]bool{}
	calleeNode := map[string]*ast.Node{}
	valueSeen := map[string]bool{}
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
			// `getValue(…)` — a BARE-IDENTIFIER callee is still recorded
			// as a read (a captured function value called by name stays a
			// capture), but its position is remembered: a name whose every
			// occurrence is callee position needs no value slot when the
			// resolution answers a declaration (capturesOf's own gate)
			if ast.IsIdentifier(callee) {
				name := callee.Text()
				if _, isBound := bound[name]; !isBound {
					if _, already := seen[name]; !already {
						seen[name] = struct{}{}
						reads = append(reads, name)
					}
					calleeSeen[name] = true
					if _, held := calleeNode[name]; !held {
						calleeNode[name] = call.Expression
					}
				}
				if call.Arguments != nil {
					for _, argument := range call.Arguments.Nodes {
						visit(argument)
					}
				}
				return false
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
				valueSeen[name] = true
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
	calleeOnly := map[string]bool{}
	for name := range calleeSeen {
		if !valueSeen[name] {
			calleeOnly[name] = true
		}
	}
	return freeNameScan{Reads: reads, CalleeOnly: calleeOnly, CalleeNode: calleeNode, Ok: true}
}

// capturesOf turns the scan's free reads into capture entries, or
// declines. A SCALAR name takes one entry from its own slot. Beyond
// that, three widened arms serve what used to decline outright:
//
//   - a name whose EVERY occurrence is callee position and whose
//     resolution answers a bodied declaration contributes NO capture —
//     the interior call lowers through ResolveCallee exactly as any
//     body's call does, and no value slot ever existed to bind;
//   - a FLATTENED ARRAY (`links` living as "links.len"/"links.elem")
//     captures as a two-leaf bundle, so the arrow's own element reads
//     resolve against the same pair the caller holds;
//   - a FLATTENED RECORD captures as one leaf per slot — the object
//     capture appendSummaryCaptureEntries already lays out, mirrored
//     from closureCapturesOf's own expansion.
//
// A free name none of those hold — an import, a global read as a value
// — still declines. The capture list keeps the scan's order; the flat
// slots list carries ONE slot per laid-out entry (a bundle contributes
// one per leaf), which is the lockstep arrowCallStatement binds by.
func capturesOf(context *LoweringContext, scan freeNameScan) ([]capturedSlot, []int, bool) {
	if !scan.Ok {
		return nil, nil, false
	}
	var captures []capturedSlot
	var slots []int
	for _, name := range scan.Reads {
		index, found := slotIndexOfName(context, name)
		if found {
			if index >= len(context.Sorts) || index >= len(context.Typeofs) {
				return nil, nil, false
			}
			captures = append(captures, capturedSlot{
				Name:      name,
				Sort:      context.Sorts[index],
				TypeofTag: context.Typeofs[index],
			})
			slots = append(slots, index)
			continue
		}
		if scan.CalleeOnly[name] && context.ResolveCallee != nil {
			if callee := context.ResolveCallee(scan.CalleeNode[name]); callee != nil && callee.Body() != nil {
				continue
			}
		}
		if lenSlot, elemSlot, isArray := arraySlotsOf(context, name); isArray {
			if lenSlot >= len(context.Sorts) || elemSlot >= len(context.Sorts) {
				return nil, nil, false
			}
			captures = append(captures, capturedSlot{
				Name: name,
				Members: []capturedLeaf{
					{Member: "len", Sort: context.Sorts[lenSlot], TypeofTag: context.Typeofs[lenSlot]},
					{Member: "elem", Sort: context.Sorts[elemSlot], TypeofTag: context.Typeofs[elemSlot]},
				},
			})
			slots = append(slots, lenSlot, elemSlot)
			continue
		}
		if leaves, ok := leafSlotsUnder(context, name); ok {
			members := make([]capturedLeaf, len(leaves))
			for at, leaf := range leaves {
				if leaf.Index >= len(context.Sorts) {
					return nil, nil, false
				}
				members[at] = capturedLeaf{
					Member:    leaf.Path,
					Sort:      context.Sorts[leaf.Index],
					TypeofTag: context.Typeofs[leaf.Index],
				}
				slots = append(slots, leaf.Index)
			}
			captures = append(captures, capturedSlot{Name: name, Members: members})
			continue
		}
		return nil, nil, false
	}
	return captures, slots, true
}

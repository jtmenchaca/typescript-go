// An OBJECT LITERAL's shorthand METHOD writing its own record —
// `const person = { age: 40, bump(): void { this.age = this.age + 1 } }`
// — called through the record: `person.bump()`. The method's `this` IS
// the record, so its `this.age` writes are writes to the record's own
// leaf slots, and a call site that keeps believing the stale leaf across
// the call is unsound.
//
// The treatment is the class method's, re-rooted at the literal:
//
//   - the FLATTENING admits method rows (flatKeysOfLiteralWith skips
//     them; they spell no leaf) and the use scan admits the method name
//     in CALLEE position only (usesAreAllDeclaredKeySteps);
//   - the method's summary lays its receiver out as a `this` bundle
//     whose fields are the literal's own scalar rows
//     (literalThisBundleOf below, thisBundleOf's literal arm), so
//     `this.age` lowers onto a real entry and the written entry rides
//     out through BundleEntries;
//   - the CALL SITE threads the entries exactly as a class method's —
//     bundleRetsAndArgs fills "this.age" from the caller's
//     "person.age" slot and maps the written exit back — so the exact
//     written value (41, 200) lands on the leaf;
//   - what the bundle cannot spell takes the CLOSURE treatment
//     (LiteralMethodWriteStatements): the method's assigned-name census
//     havocs every caller slot it writes beyond `this`, and a body the
//     census cannot bound refuses the summary so the site falls to the
//     opaque tier, whose receiver-bundle havoc invalidates the leaves.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── recognition: the literal's method rows ──────────────────────── */

// isServableLiteralMethodRow answers whether ONE method row of an object
// literal is a method this treatment reads: a plain shorthand method
// with an identifier name and a body. A GENERATOR runs its body at
// resumption times no call site places, and an ASYNC body's writes past
// its first await land after the call statement returned — either would
// make the call-site write-back claim an order the runtime does not
// keep, so both decline (and with them the literal's flattening, which
// is where every method row stood before this treatment).
func isServableLiteralMethodRow(property *ast.Node) bool {
	if property == nil || !ast.IsMethodDeclaration(property) {
		return false
	}
	if property.AsMethodDeclaration().AsteriskToken != nil {
		return false
	}
	if property.ModifierFlags()&ast.ModifierFlagsAsync != 0 {
		return false
	}
	name := property.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return false
	}
	return property.Body() != nil
}

// literalMethodPaths is the method rows of a literal, keyed by their
// dotted path below the holder — "bump" for a top-level row, "a.m" for a
// method of a nested literal row — each holding its declaration node.
// The use scan admits exactly these spellings in callee position, and
// only there.
func literalMethodPaths(literal *ast.Node, prefix []string) map[string]*ast.Node {
	out := map[string]*ast.Node{}
	if literal == nil || !ast.IsObjectLiteralExpression(literal) {
		return out
	}
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		if ast.IsMethodDeclaration(property) {
			name := property.Name()
			if name == nil || !ast.IsIdentifier(name) {
				continue
			}
			path := append(append([]string{}, prefix...), name.Text())
			out[strings.Join(path, ".")] = property
			continue
		}
		if !ast.IsPropertyAssignment(property) {
			continue
		}
		assignment := property.AsPropertyAssignment()
		if assignment.Initializer == nil || assignment.Name() == nil || !ast.IsIdentifier(assignment.Name()) {
			continue
		}
		value := Unwrapped(assignment.Initializer)
		if !ast.IsObjectLiteralExpression(value) {
			continue
		}
		nested := literalMethodPaths(value, append(append([]string{}, prefix...), assignment.Name().Text()))
		for spelled, node := range nested {
			out[spelled] = node
		}
	}
	return out
}

/* ── the method's `this` bundle, read off the literal ────────────── */

// literalBundleFields is the literal's scalar rows as the method's
// receiver field set — the object-literal twin of ClassBundleFields.
// One field per plain one-step row (`age: 40`, a shorthand row), sorted
// the way the flattening sorts the same leaf (ObjectLocalKeySort /
// ObjectLocalKeyTypeof), so the bundle entry and the caller's slot for
// one leaf wear one sort.
//
// A method row spells no field (it is a call target, not a slot), and a
// nested-literal or array row is deeper than the one-step vocabulary —
// both are OMITTED rather than declined: a body that never touches the
// omitted name loses nothing, and a body that does touch it reads a
// member the field set never declared, which the census reports as an
// ESCAPE and the serving gate refuses.
func literalBundleFields(literal *ast.Node) []BundleField {
	if literal == nil || !ast.IsObjectLiteralExpression(literal) {
		return nil
	}
	var fields []BundleField
	seen := map[string]struct{}{}
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		var keyName *ast.Node
		var initializer *ast.Node
		switch {
		case ast.IsPropertyAssignment(property):
			assignment := property.AsPropertyAssignment()
			keyName = assignment.Name()
			initializer = assignment.Initializer
		case ast.IsShorthandPropertyAssignment(property):
			shorthand := property.AsShorthandPropertyAssignment()
			keyName = shorthand.Name()
			initializer = shorthand.Name()
		default:
			continue
		}
		if keyName == nil || !ast.IsIdentifier(keyName) || initializer == nil {
			continue
		}
		value := Unwrapped(initializer)
		if ast.IsObjectLiteralExpression(value) || ast.IsArrayLiteralExpression(value) {
			continue
		}
		text := keyName.Text()
		if _, already := seen[text]; already {
			continue
		}
		seen[text] = struct{}{}
		leaf := ObjectLocalKey{Path: []string{text}, Key: text, SlotName: text, Initializer: initializer}
		fields = append(fields, BundleField{
			Name:      text,
			SlotName:  text,
			Sort:      ObjectLocalKeySort(leaf),
			TypeofTag: ObjectLocalKeyTypeof(leaf),
		})
	}
	return fields
}

// literalThisBundleOf is thisBundleOf's OBJECT-LITERAL arm: the method's
// receiver fields are the literal's own scalar rows, and the census over
// its body is the same census a class method runs.
//
// One deliberate difference from the class arm: entries are laid out for
// the WRITTEN fields as well as the read ones. `spoil() { this.age =
// 200 }` writes age without reading it; with no entry the write would
// have no row and no ret to ride back on, and the caller's leaf would
// keep its stale value — the exact unsoundness this treatment closes. A
// write-only entry is honest here because the call site fills every
// this-entry from the receiver's own slot (or unknown where it has
// none, bundleRetsAndArgs), so the entry enters holding what the field
// held and the body's write overwrites it.
//
// A body the census cannot bound — an escaping receiver, a receiver
// method captured by a nested closure (the literal has no
// CaptureWriteSet reading yet) — answers Escaped, and the SERVING GATE
// (LiteralMethodWriteStatements) turns Escaped into a refusal of the
// summary at every call site, where the class arm's callers tolerate an
// unexpanded bundle. A computed store (`this[k] = v`) keeps the class
// arm's answer: every field joins the havoc set and every one is marked
// written.
func literalThisBundleOf(ctx *FlowContext, declaration *ast.Node, literal *ast.Node) thisBundleLayout {
	body := declaration.Body()
	if body == nil {
		return thisBundleLayout{}
	}
	fields := literalBundleFields(literal)
	if len(fields) == 0 {
		// no scalar row to spell a slot with; a body touching `this` at
		// all is then out of the vocabulary and must not serve
		if methodTouchesThis(body) {
			return thisBundleLayout{Escaped: true}
		}
		return thisBundleLayout{}
	}
	respelled := BundleFieldsAs("this", fields)
	census := FieldCensusWith(checkerOf(ctx), body, "this", respelled)
	var captureHavocNames []string
	switch {
	case census.Escapes:
		return thisBundleLayout{Escaped: true}
	case census.ComputedWrite:
		// `this[k] = v` moves a slot nothing names — the literal's rows
		// bound the set, so every field joins the havoc set
		for _, field := range fields {
			captureHavocNames = append(captureHavocNames, "this."+field.Name)
		}
	case len(census.CapturedMethodCalls) > 0:
		// a nested closure calling a receiver method: the class arm
		// computes the captured methods' transitive write set off the
		// class declaration; the literal carries no such reading, so the
		// bundle leaves the lowering's sight
		return thisBundleLayout{Escaped: true}
	}
	written := map[string]struct{}{}
	for _, field := range census.Writes {
		written[field.SlotName] = struct{}{}
	}
	for _, name := range captureHavocNames {
		written[name] = struct{}{}
	}
	inUse := map[string]struct{}{}
	for _, field := range census.Reads {
		inUse[field.SlotName] = struct{}{}
	}
	for name := range written {
		inUse[name] = struct{}{}
	}
	if len(inUse) == 0 {
		return thisBundleLayout{ReturnsSelf: census.ReturnsSelf}
	}
	entries := make([]bodySlot, 0, len(inUse))
	for _, field := range respelled {
		if _, used := inUse[field.SlotName]; !used {
			continue
		}
		entries = append(entries, bodySlot{
			Name:      field.SlotName,
			Sort:      field.Sort,
			TypeofTag: field.TypeofTag,
		})
	}
	return thisBundleLayout{
		Entries:           entries,
		Written:           written,
		Expanded:          len(entries) > 0,
		CaptureHavocNames: captureHavocNames,
		ReturnsSelf:       census.ReturnsSelf,
	}
}

// methodTouchesThis is whether a subtree holds a ThisKeyword anywhere,
// nested functions included. Conservative on purpose: a nested function
// expression's `this` is not the method's receiver, but a body carrying
// one is a body the one-step vocabulary should not vouch for.
func methodTouchesThis(node *ast.Node) bool {
	found := false
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if found || child == nil {
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

/* ── the call site: the serving gate and the capture-write havoc ── */

// objectLiteralMethodCalleeOf resolves a call to the OBJECT-LITERAL
// METHOD it runs, or nil for every other callee — the same resolution
// the summary route uses (summaryCalleeOf), so the gate and the served
// statement never disagree about which declaration a site runs.
func objectLiteralMethodCalleeOf(context *LoweringContext, call *ast.Node) *ast.Node {
	if context == nil || call == nil || !ast.IsCallExpression(call) {
		return nil
	}
	callee := summaryCalleeOf(context, call)
	if callee == nil || !ast.IsMethodDeclaration(callee) {
		return nil
	}
	if callee.Parent == nil || !ast.IsObjectLiteralExpression(callee.Parent) {
		return nil
	}
	return callee
}

// LiteralMethodWriteStatements is the call site's half of the
// treatment. For a call that does not run an object-literal method it
// answers (nil, true): serve exactly as before. For one that does:
//
//	(havoc, true)  — the summary MAY serve; `havoc` is the method's
//	                 capture-write set beyond `this` (the closure
//	                 census, methodCaptureWriteSlots), prepended so a
//	                 caller local the body assigns is not believed
//	                 across the call. The this-writes need no havoc:
//	                 the served statement's rets write the exact exits.
//	(havoc, false) — the summary must NOT serve: the receiver bundle
//	                 did not expand for a this-touching body, or the
//	                 body moves a flattened capture by a route the
//	                 write census does not spell. `havoc` then also
//	                 covers every flattened leaf under every name the
//	                 body mentions, and the caller prepends it to the
//	                 floor tier that answers the site (whose own
//	                 receiver-bundle havoc invalidates the record's
//	                 leaves).
func LiteralMethodWriteStatements(context *LoweringContext, call *ast.Node) ([]kernelbridge.IrStatement, bool) {
	method := objectLiteralMethodCalleeOf(context, call)
	if method == nil {
		return nil, true
	}
	body := method.Body()
	if body == nil {
		return nil, false
	}
	if methodMutatesFlattenedCaptureBeyondThis(context, body) {
		return methodRefusalHavoc(context, method), false
	}
	if methodTouchesThis(body) {
		bundle := thisBundleOf(context.Flow, method)
		if bundle.Escaped || !bundle.Expanded {
			return methodRefusalHavoc(context, method), false
		}
	}
	return havocAssignments(methodCaptureWriteSlots(context, method)), true
}

// methodCaptureWriteSlots is ClosureWriteSlots minus the receiver: the
// tracked caller slots a method's body assigns through CAPTURED names —
// `counter++` on an enclosing local — resolved against the caller's own
// slot vector. The `this.<field>` spellings are excluded: inside the
// method `this` is the record, never the caller's own receiver, so a
// caller that happens to hold "this.<field>" slots (a class method
// lowering this call) must not have them havocked by the record's
// writes; those land through the bundle rets instead.
//
// The census takes the METHOD NODE, never its .Body() — the pinned
// lesson from the closure census: handed a bare block it looks for
// closures INSIDE and the body's own top-level writes go unseen.
func methodCaptureWriteSlots(context *LoweringContext, method *ast.Node) map[int]struct{} {
	slots := map[int]struct{}{}
	if context == nil || method == nil {
		return slots
	}
	written := map[string]struct{}{}
	closureAssignedNames(method, written)
	for name := range written {
		if name == "this" || strings.HasPrefix(name, "this.") {
			continue
		}
		if slot, held := slotIndexOfName(context, name); held {
			slots[slot] = struct{}{}
		}
		for _, leaf := range flattenedSlotsUnder(context, name) {
			slots[leaf] = struct{}{}
		}
	}
	return slots
}

// methodRefusalHavoc is the havoc a REFUSED site prepends to its floor:
// the capture-write slots, plus every flattened leaf (and own slot)
// under every name the body mentions — a mutator call (`xs.push(v)`) or
// a hand-over (`f(xs)`) moves an object no write form spells, which is
// the same rule closureMutatesFlattenedCapture applies to a closure's
// declaration. The receiver's own leaves are the floor tier's business
// (withReceiverBundleHavoc), not repeated here.
func methodRefusalHavoc(context *LoweringContext, method *ast.Node) []kernelbridge.IrStatement {
	slots := methodCaptureWriteSlots(context, method)
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if node == nil {
			return true
		}
		if ast.IsIdentifier(node) {
			name := node.Text()
			for _, leaf := range flattenedSlotsUnder(context, name) {
				slots[leaf] = struct{}{}
			}
			if slot, held := slotIndexOfName(context, name); held {
				if len(flattenedSlotsUnder(context, name)) > 0 {
					slots[slot] = struct{}{}
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	if body := method.Body(); body != nil {
		visit(body)
	}
	return havocAssignments(slots)
}

// methodMutatesFlattenedCaptureBeyondThis is
// closureMutatesFlattenedCapture with the receiver carved out: a member
// or element write rooted at `this` is the bundle's to carry (or the
// census's to escape on), so only the OTHER routes refuse the serving —
// a member write whose target has no caller slot, and a mention of a
// flattened caller local, either of which moves state the write census
// cannot spell.
func methodMutatesFlattenedCaptureBeyondThis(context *LoweringContext, body *ast.Node) bool {
	if context == nil || body == nil {
		return false
	}
	mutates := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if mutates || node == nil {
			return true
		}
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if memberWriteWithoutSlotBeyondThis(context, bin.Left) {
					mutates = true
					return true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(node) || ast.IsPostfixUnaryExpression(node) {
			var operator ast.Kind
			var operand *ast.Node
			if ast.IsPrefixUnaryExpression(node) {
				operator, operand = node.AsPrefixUnaryExpression().Operator, node.AsPrefixUnaryExpression().Operand
			} else {
				operator, operand = node.AsPostfixUnaryExpression().Operator, node.AsPostfixUnaryExpression().Operand
			}
			if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
				if memberWriteWithoutSlotBeyondThis(context, operand) {
					mutates = true
					return true
				}
			}
		}
		if ast.IsIdentifier(node) && len(flattenedSlotsUnder(context, node.Text())) > 0 {
			mutates = true
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return mutates
}

// memberWriteWithoutSlotBeyondThis is memberWriteWithoutSlot minus the
// `this`-rooted spellings: a plain `this.<field>` chain is the census's
// to read and the bundle's to carry, and a computed `this[k]` store is
// the census's ComputedWrite. Everything else keeps the original rule —
// a member or element target that resolves to no slot moves a leaf the
// write census cannot name.
func memberWriteWithoutSlotBeyondThis(context *LoweringContext, target *ast.Node) bool {
	head := Unwrapped(target)
	if head == nil || ast.IsIdentifier(head) {
		return false
	}
	if !ast.IsPropertyAccessExpression(head) && !ast.IsElementAccessExpression(head) {
		return false
	}
	if root, _, isPath := propertyPathOf(head); isPath && root == "this" {
		return false
	}
	if ast.IsElementAccessExpression(head) {
		receiver := Unwrapped(head.AsElementAccessExpression().Expression)
		if receiver != nil && receiver.Kind == ast.KindThisKeyword {
			return false
		}
	}
	if _, held := IndexOf(context, head); held {
		return false
	}
	return true
}

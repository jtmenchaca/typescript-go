// Object locals flattened into scalar slots for the flow IR.
//
// A body that keeps a fixed-shape record in a local — `const p = { lo:
// 0, hi: n }` — writes and reads it as `p.lo` / `p.hi`. The kernel's
// walk is over a vector of scalar slots, so such a local can be
// carried as ONE SLOT PER LEAF PATH, spelled "p.lo", "p.hi", and for a
// nested literal "p.a.b": the leaf paths a fixed-shape literal names,
// each holding one scalar. The kernel is untouched — it never learns
// several slots came from one object.
//
// The flattening is only sound while the object has no identity the
// body can observe. This file's recognizer answers the flat leaf set
// for a declaration, or declines: every use of the name in the body
// must be a `p.a.b` path on a leaf the literal declared (or an
// INTERIOR path in one of the whole-record forms below), and the
// literal itself must be plain `key: expression` rows whose values are
// scalars or further fixed-shape literals. Anything else — an alias, a
// call argument, a return of the whole record, a delete, a key added
// later, a computed or spread key — declines, because after flattening
// those uses would read or write an object that no longer exists as
// one value.
//
// The whole-record forms that DO lower, recognized here and lowered in
// lowering_to_kernel_ir.go:
//
//   - `p = q` between two flattened records of the same leaf shape:
//     per-leaf slot assigns.
//   - `p = { … }` where the literal has exactly p's leaf shape: the
//     same, each leaf taking its own row.
//   - `const { x, y } = p`: per-leaf slot reads.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// ObjectLocal is one flattened record local: the declaration it came
// from, the name it was spelled under, and its LEAF paths in literal
// order paired with the initializer each leaf was given.
type ObjectLocal struct {
	Declaration *ast.Node // VariableDeclaration
	Name        string
	Keys        []ObjectLocalKey
}

// ObjectLocalKey is one leaf of a flattened record: the leaf's path
// below the holder (["a", "b"] for `p.a.b`), the key's own name (the
// LAST path step, which is what a destructuring row and a one-step read
// name), the slot name it is tracked under ("p.a.b"), and the
// expression the literal assigned it.
type ObjectLocalKey struct {
	Path        []string
	Key         string
	SlotName    string
	Initializer *ast.Node
}

// objectLiteralOfDeclaration is the object literal a declaration's
// initializer is, through parens and casts — or nil.
func objectLiteralOfDeclaration(declaration *ast.Node) *ast.Node {
	if !ast.IsVariableDeclaration(declaration) {
		return nil
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil
	}
	literal := Unwrapped(initializer)
	if !ast.IsObjectLiteralExpression(literal) {
		return nil
	}
	return literal
}

// flatKeysOfLiteral reads an object literal's rows as a flat LEAF list,
// recursing into nested fixed-shape literals: a row whose value is
// another object literal contributes that literal's own leaves under
// the row's key, so `{ a: { b: 1 } }` names the single leaf "p.a.b".
// Every row must be a plain `key: expression` assignment with an
// identifier key; spreads, computed keys, shorthand rows, methods,
// accessors and array-literal values all decline — none of them names
// one leaf holding one scalar. `prefix` is the path already walked
// below the holder, `holder` the spelled root ("p").
func flatKeysOfLiteral(literal *ast.Node, holder string, prefix []string) ([]ObjectLocalKey, bool) {
	var keys []ObjectLocalKey
	seen := map[string]struct{}{}
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		if !ast.IsPropertyAssignment(property) {
			return nil, false
		}
		assignment := property.AsPropertyAssignment()
		if !ast.IsIdentifier(assignment.Name()) {
			return nil, false
		}
		if assignment.Initializer == nil {
			return nil, false
		}
		key := assignment.Name().Text()
		if _, already := seen[key]; already {
			return nil, false
		}
		seen[key] = struct{}{}
		path := append(append([]string{}, prefix...), key)
		value := Unwrapped(assignment.Initializer)
		// a nested fixed-shape literal contributes its OWN leaves under
		// this key — one more level of the same rule
		if ast.IsObjectLiteralExpression(value) {
			nested, ok := flatKeysOfLiteral(value, holder, path)
			if !ok {
				return nil, false
			}
			keys = append(keys, nested...)
			continue
		}
		// an array value is the array flattening's business (a.len / a.elem),
		// not a scalar leaf — a record row holding one is not spelled here
		if ast.IsArrayLiteralExpression(value) {
			return nil, false
		}
		keys = append(keys, ObjectLocalKey{
			Path:        path,
			Key:         key,
			SlotName:    holder + "." + strings.Join(path, "."),
			Initializer: assignment.Initializer,
		})
	}
	if len(keys) == 0 {
		return nil, false
	}
	return keys, true
}

// propertyPathOf reads a chained property access down to a root
// identifier or `this`: `p.a.b` answers ("p", ["a","b"]) and
// `this.count` answers ("this", ["count"]) — the spelling the bundle
// layout gives a method's field slots. Optional steps (`p?.a`) and
// computed steps decline — neither is a plain path read.
func propertyPathOf(node *ast.Node) (root string, path []string, ok bool) {
	var steps []string
	current := node
	for ast.IsPropertyAccessExpression(current) {
		access := current.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil {
			return "", nil, false
		}
		if !ast.IsIdentifier(access.Name()) {
			return "", nil, false
		}
		steps = append(steps, access.Name().Text())
		current = access.Expression
	}
	if len(steps) == 0 {
		return "", nil, false
	}
	if !ast.IsIdentifier(current) && current.Kind != ast.KindThisKeyword {
		return "", nil, false
	}
	// steps were collected outermost-first; the path reads root-first
	for left, right := 0, len(steps)-1; left < right; left, right = left+1, right-1 {
		steps[left], steps[right] = steps[right], steps[left]
	}
	if current.Kind == ast.KindThisKeyword {
		return "this", steps, true
	}
	return current.Text(), steps, true
}

// declaredLeafPaths is the leaf-path set a flattened record declares,
// as a lookup on the joined spelling.
func declaredLeafPaths(keys []ObjectLocalKey) map[string]struct{} {
	out := map[string]struct{}{}
	for _, key := range keys {
		out[strings.Join(key.Path, ".")] = struct{}{}
	}
	return out
}

// recordShapeOf is a flattened record's leaf shape as a comparable
// spelling — the joined leaf paths in literal order. Two records assign
// leaf-for-leaf only where these agree.
func recordShapeOf(keys []ObjectLocalKey) string {
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = strings.Join(key.Path, ".")
	}
	return strings.Join(parts, "|")
}

// usesAreAllDeclaredKeySteps scans a body for every occurrence of the
// name and answers whether each one is a `name.a.b` path on a leaf the
// literal declared, or one of the recognized whole-record forms. The
// declaration's own name position and the literal's own rows are not
// uses. A `delete p.k` reads the record as a mutable object, so it
// declines even though its operand IS a path; every other whole-name
// occurrence — an alias, an argument, a return, an element access
// `p[e]` — declines, because after flattening there is no one value for
// it to denote.
func usesAreAllDeclaredKeySteps(
	body *ast.Node,
	declaration *ast.Node,
	name string,
	keys []ObjectLocalKey,
	sameShapeName func(other string) bool,
) bool {
	declared := declaredLeafPaths(keys)
	shape := recordShapeOf(keys)
	declarationName := declaration.AsVariableDeclaration().Name()
	ok := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		// `delete p.k` — the record's shape is observed, not just its
		// leaves' values. Checked BEFORE the path rule below, which would
		// otherwise admit the operand as an ordinary read.
		if ast.IsDeleteExpression(node) {
			operand := Unwrapped(node.AsDeleteExpression().Expression)
			if root, _, isPath := propertyPathOf(operand); isPath && root == name {
				ok = false
				return true
			}
		}
		// `p = q` / `p = { … }` — the whole record written leaf for leaf.
		// Both sides are consumed here so neither reaches the bare-name
		// test below. This scan runs once per record, so the SAME
		// assignment is seen from the target's side (name on the left) and
		// from the source's side (name on the right); both are admitted,
		// each against the other's shape.
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindEqualsToken {
				left := bin.Left
				right := Unwrapped(bin.Right)
				// this record is the TARGET
				if ast.IsIdentifier(left) && left.Text() == name {
					if ast.IsIdentifier(right) && sameShapeName(right.Text()) {
						return false
					}
					if ast.IsObjectLiteralExpression(right) {
						if rows, rowsOk := flatKeysOfLiteral(right, name, nil); rowsOk && recordShapeOf(rows) == shape {
							// the rows' own initializers still have to be scanned —
							// one of them could mention this record
							for _, row := range rows {
								visit(row.Initializer)
							}
							return false
						}
					}
					ok = false
					return true
				}
				// this record is the SOURCE: `q = p` where q is a flattened
				// record of the same leaf shape reads p leaf by leaf, never
				// as one value
				if ast.IsIdentifier(right) && right.Text() == name &&
					ast.IsIdentifier(left) && sameShapeName(left.Text()) {
					return false
				}
			}
		}
		// `const { x, y } = p` — the leaves read into fresh names. Every
		// bound name must be a one-step leaf of this record.
		if ast.IsVariableDeclaration(node) {
			decl := node.AsVariableDeclaration()
			if decl.Initializer != nil && ast.IsObjectBindingPattern(decl.Name()) {
				initializer := Unwrapped(decl.Initializer)
				if ast.IsIdentifier(initializer) && initializer.Text() == name {
					if _, patternOk := destructuredLeafNamesOf(decl.Name(), keys); patternOk {
						return false
					}
					ok = false
					return true
				}
			}
		}
		// `p.a.b` — a path; the root and every step name are consumed here,
		// so none reaches the bare-identifier test below
		if root, path, isPath := propertyPathOf(node); isPath && root == name {
			if _, isDeclared := declared[strings.Join(path, ".")]; !isDeclared {
				ok = false // a leaf the literal never gave a slot
				return true
			}
			return false
		}
		// Every other occurrence of the bare name — an alias `q = p`, an
		// argument `f(p)`, `return p`, an element access `p[e]`, an
		// optional step `p?.k`, a nested `q.p` never reaching here as a
		// root — is the WHOLE record in a position the flattening cannot
		// spell.
		if ast.IsIdentifier(node) && node.Text() == name && node != declarationName {
			ok = false
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return ok
}

// destructuredLeafNamesOf reads an object binding pattern against a
// flattened record's leaves: every element must be a plain
// `{ x }` or `{ x: y }` row naming a ONE-STEP leaf of the record, with
// no default, no rest, no nested pattern. Answers each bound name
// paired with the leaf it reads.
type destructuredLeaf struct {
	BoundName string
	Key       ObjectLocalKey
}

func destructuredLeafNamesOf(pattern *ast.Node, keys []ObjectLocalKey) ([]destructuredLeaf, bool) {
	if !ast.IsObjectBindingPattern(pattern) {
		return nil, false
	}
	var out []destructuredLeaf
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || binding.Initializer != nil {
			return nil, false
		}
		if !ast.IsIdentifier(binding.Name()) {
			return nil, false
		}
		read := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				return nil, false
			}
			read = binding.PropertyName.Text()
		}
		found := false
		for _, key := range keys {
			if len(key.Path) == 1 && key.Path[0] == read {
				out = append(out, destructuredLeaf{BoundName: binding.Name().Text(), Key: key})
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// ObjectLocalOf is the recognizer: a declaration `const p = { k: e, … }`
// — nested literals included — whose every use in the body is a path on
// a declared leaf or a recognized whole-record form becomes the slot
// family "p.a.b"; anything else declines. Total-or-decline — the caller
// reads the flat leaves or keeps its existing route.
//
// `sameShapeName` answers whether another spelled local is a flattened
// record of THIS record's leaf shape, which is what `p = q` needs; the
// batch entry ObjectLocalsOf supplies it, and a lone call may pass nil
// (no record-to-record assignment is then admitted).
func ObjectLocalOf(body *ast.Node, declaration *ast.Node, sameShapeName func(other string) bool) (ObjectLocal, bool) {
	literal := objectLiteralOfDeclaration(declaration)
	if literal == nil {
		return ObjectLocal{}, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ObjectLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	keys, ok := flatKeysOfLiteral(literal, name, nil)
	if !ok {
		return ObjectLocal{}, false
	}
	// a leaf's own initializer must not mention the record — `{ lo: 0, hi:
	// p.lo }` reads a slot that does not exist yet
	for _, key := range keys {
		if mentionsName(key.Initializer, name) {
			return ObjectLocal{}, false
		}
	}
	if sameShapeName == nil {
		sameShapeName = func(string) bool { return false }
	}
	if !usesAreAllDeclaredKeySteps(body, declaration, name, keys, sameShapeName) {
		return ObjectLocal{}, false
	}
	return ObjectLocal{Declaration: declaration, Name: name, Keys: keys}, true
}

// mentionsName is whether a subtree contains the identifier at all, in
// any position.
func mentionsName(node *ast.Node, name string) bool {
	found := false
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if found {
			return true
		}
		if ast.IsIdentifier(child) && child.Text() == name {
			found = true
			return true
		}
		child.ForEachChild(visit)
		return false
	}
	visit(node)
	return found
}

// ObjectLocalKeySort is a flattened leaf's sort, read the way LocalSort
// reads a scalar local's: a string literal is a string, anything else
// the lowering reads numerically.
func ObjectLocalKeySort(key ObjectLocalKey) BindingKind {
	if ast.IsStringLiteral(Unwrapped(key.Initializer)) {
		return BindingKindString
	}
	return BindingKindNumber
}

// ObjectLocalKeyTypeof is a flattened leaf's typeof evidence, from its
// initializer's syntax alone — the twin of LocalTypeof.
func ObjectLocalKeyTypeof(key ObjectLocalKey) TypeofTag {
	e := Unwrapped(key.Initializer)
	if ast.IsStringLiteral(e) {
		return TypeofTagString
	}
	if _, isNumber := NumberOf(e); ast.IsNumericLiteral(e) || isNumber {
		return TypeofTagNumber
	}
	if e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword {
		return TypeofTagBoolean
	}
	return TypeofTagNone
}

// slotIndexOfName resolves a spelled slot name the way IndexOf
// resolves a node's: through the closed name map where an inlined body
// supplied one, through the binding list otherwise.
func slotIndexOfName(context *LoweringContext, spelled string) (int, bool) {
	if context.Names != nil {
		index, found := context.Names[spelled]
		return index, found
	}
	for index, binding := range context.Bindings {
		if binding == spelled {
			return index, true
		}
	}
	return 0, false
}

// PathSlotIndexOf resolves a DEEP path read (`p.a.b`) to its leaf slot.
// SpelledNameOf spells only one step, so a nested leaf's slot has to be
// looked up from the path itself; a one-step path resolves to exactly
// the name SpelledNameOf would have produced, so this subsumes it.
func PathSlotIndexOf(context *LoweringContext, node *ast.Node) (int, bool) {
	root, path, ok := propertyPathOf(Unwrapped(node))
	if !ok {
		return 0, false
	}
	return slotIndexOfName(context, root+"."+strings.Join(path, "."))
}

// ObjectLocalDeclarationAssignments is the object-literal declaration's
// lowering: one ordinary assignment per LEAF, in literal order, each
// writing the leaf's own slot from its initializer through the shared
// RHS grammar. Declines unless EVERY leaf has a slot and every
// initializer lowers — a leaf left at its absent entry state would read
// later as undefined, which is not what the literal wrote.
func ObjectLocalDeclarationAssignments(context *LoweringContext, local ObjectLocal) ([]AssignmentTarget, bool) {
	out := make([]AssignmentTarget, 0, len(local.Keys))
	for _, key := range local.Keys {
		target, found := slotIndexOfName(context, key.SlotName)
		if !found {
			return nil, false
		}
		effect, ok := RhsEffect(context, context.Sorts[target], key.Initializer)
		if !ok {
			return nil, false
		}
		out = append(out, AssignmentTarget{Target: target, Effect: effect})
	}
	return out, true
}

// ObjectDeclarationAssignmentsOf is the lowering-side entry: a variable
// statement declaring ONE object-literal local, read as its per-leaf
// assignments. Syntax alone decides — the recognizer's admission is
// already recorded in the slot vector, so the gate here is simply that
// every derived slot name resolves. A record the summary declined has
// no such slots, so this declines too and the statement takes its
// former route.
func ObjectDeclarationAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0]
	literal := objectLiteralOfDeclaration(declaration)
	if literal == nil {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	keys, ok := flatKeysOfLiteral(literal, name, nil)
	if !ok {
		return nil, false
	}
	return ObjectLocalDeclarationAssignments(context, ObjectLocal{Declaration: declaration, Name: name, Keys: keys})
}

// RecordAssignmentOf is the record REASSIGNMENT lowering: `p = q`
// between two flattened records of the same leaf shape, and `p = { … }`
// where the literal spells exactly p's leaves. Both become one ordinary
// assignment per leaf, in the target's leaf order — a whole-record
// write the kernel never sees as a record.
//
// The shapes are read from the slot vector, not from a recognizer
// table: a leaf slot exists exactly where the recognizer admitted the
// record, so "every leaf of p has a slot and every matching leaf of the
// source has one too" IS the shape agreement. Declines on any leaf
// without a slot, exactly the total-or-decline law.
func RecordAssignmentOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken || !ast.IsIdentifier(bin.Left) {
		return nil, false
	}
	target := bin.Left.Text()
	leaves, ok := leafSlotsUnder(context, target)
	if !ok {
		return nil, false
	}
	right := Unwrapped(bin.Right)
	// `p = q`: each of p's leaves copies q's leaf of the same path
	if ast.IsIdentifier(right) {
		source := right.Text()
		sourceLeaves, sourceOk := leafSlotsUnder(context, source)
		if !sourceOk || len(sourceLeaves) != len(leaves) {
			return nil, false
		}
		sourceOfPath := map[string]int{}
		for _, leaf := range sourceLeaves {
			sourceOfPath[leaf.Path] = leaf.Index
		}
		out := make([]AssignmentTarget, 0, len(leaves))
		for _, leaf := range leaves {
			from, found := sourceOfPath[leaf.Path]
			if !found {
				return nil, false
			}
			out = append(out, AssignmentTarget{Target: leaf.Index, Effect: varEffect(from)})
		}
		return out, true
	}
	// `p = { … }`: the literal's leaves must be exactly p's, and each row
	// lowers through the shared RHS grammar into its leaf's slot. The
	// rows are matched to leaves BY PATH — a literal spelling its keys in
	// another order is the same record.
	if ast.IsObjectLiteralExpression(right) {
		rows, rowsOk := flatKeysOfLiteral(right, target, nil)
		if !rowsOk || len(rows) != len(leaves) {
			return nil, false
		}
		slotOfPath := map[string]int{}
		for _, leaf := range leaves {
			slotOfPath[leaf.Path] = leaf.Index
		}
		out := make([]AssignmentTarget, 0, len(rows))
		for _, row := range rows {
			slot, found := slotOfPath[strings.Join(row.Path, ".")]
			if !found {
				return nil, false
			}
			effect, effectOk := RhsEffect(context, context.Sorts[slot], row.Initializer)
			if !effectOk {
				return nil, false
			}
			out = append(out, AssignmentTarget{Target: slot, Effect: effect})
		}
		return out, true
	}
	return nil, false
}

// leafSlot is one flattened leaf found in the slot vector: its path
// below the holder and the slot it sits in.
type leafSlot struct {
	Path  string
	Index int
}

// leafSlotsUnder is every slot whose spelled name is a path below the
// holder — the flattened record's leaves, in slot order. Answers false
// where the holder has no leaves at all (an unflattened name, or one
// the recognizer declined).
func leafSlotsUnder(context *LoweringContext, holder string) ([]leafSlot, bool) {
	prefix := holder + "."
	// a name with a slot OF ITS OWN is a scalar, not a flattened record —
	// its assignments take the ordinary single-slot route
	if _, whole := slotIndexOfName(context, holder); whole {
		return nil, false
	}
	// an ARRAY's two slots ride the same "name.suffix" spelling but are
	// not record leaves — a whole-array assignment is not a leaf-for-leaf
	// write, so it takes no route here
	if _, _, isArray := arraySlotsOf(context, holder); isArray {
		return nil, false
	}
	var out []leafSlot
	collect := func(spelled string, index int) {
		if strings.HasPrefix(spelled, prefix) {
			out = append(out, leafSlot{Path: strings.TrimPrefix(spelled, prefix), Index: index})
		}
	}
	if context.Names != nil {
		// the closed map has no order of its own; the caller compares two
		// records leaf for leaf, so both sides are sorted by path
		for spelled, index := range context.Names {
			collect(spelled, index)
		}
	} else {
		for index, binding := range context.Bindings {
			collect(binding, index)
		}
	}
	sortLeafSlots(out)
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// sortLeafSlots orders leaves by path so two records compare leaf for
// leaf regardless of how their slots were laid out.
func sortLeafSlots(slots []leafSlot) {
	for i := 1; i < len(slots); i++ {
		for j := i; j > 0 && slots[j].Path < slots[j-1].Path; j-- {
			slots[j], slots[j-1] = slots[j-1], slots[j]
		}
	}
}

// DestructuringAssignmentsOf is the destructuring lowering: `const { x,
// y } = p` where p is a flattened record becomes one assignment per
// bound name, each reading its leaf's slot. Declines unless every bound
// name has a slot AND names a one-step leaf — a nested pattern or a
// default reads shapes the flattening does not spell.
func DestructuringAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	decl := declarations[0].AsVariableDeclaration()
	if decl.Initializer == nil || !ast.IsObjectBindingPattern(decl.Name()) {
		return nil, false
	}
	initializer := Unwrapped(decl.Initializer)
	var holder string
	switch {
	case ast.IsIdentifier(initializer):
		holder = initializer.Text()
	case initializer.Kind == ast.KindThisKeyword:
		// `const { count } = this` — the method's own bundle spells its
		// fields "this.<name>", so the same leaf read serves
		holder = "this"
	default:
		return nil, false
	}
	var out []AssignmentTarget
	for _, element := range decl.Name().AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || binding.Initializer != nil {
			return nil, false
		}
		if !ast.IsIdentifier(binding.Name()) {
			return nil, false
		}
		read := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				return nil, false
			}
			read = binding.PropertyName.Text()
		}
		source, sourceOk := slotIndexOfName(context, holder+"."+read)
		if !sourceOk {
			return nil, false
		}
		target, targetOk := slotIndexOfName(context, binding.Name().Text())
		if !targetOk {
			return nil, false
		}
		out = append(out, AssignmentTarget{Target: target, Effect: varEffect(source)})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// ObjectLocalsOf runs the recognizer over a body's collected locals and
// answers the ones that flatten, keyed by declaration. A local that
// declines is simply absent — the caller keeps treating it as a whole
// binding, which is today's behaviour.
//
// Two passes: the first reads each local's leaf shape from its literal
// alone, which is what `p = q` compares; the second runs the full
// use scan with that shape table in hand. A record whose shape table
// entry vanished in the second pass (because its own uses declined)
// leaves any partner that assigned from it declining too, on the next
// pass — so the loop repeats until the admitted set stops shrinking.
func ObjectLocalsOf(body *ast.Node, locals []*ast.Node) map[*ast.Node]ObjectLocal {
	// candidate shapes, from the literals alone
	shapeOfName := map[string]string{}
	candidates := map[*ast.Node]string{}
	for _, declaration := range locals {
		literal := objectLiteralOfDeclaration(declaration)
		if literal == nil || !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
			continue
		}
		name := declaration.AsVariableDeclaration().Name().Text()
		keys, ok := flatKeysOfLiteral(literal, name, nil)
		if !ok {
			continue
		}
		shape := recordShapeOf(keys)
		// a name declared twice with disagreeing shapes has no one shape
		if held, seen := shapeOfName[name]; seen && held != shape {
			shapeOfName[name] = ""
			continue
		}
		shapeOfName[name] = shape
		candidates[declaration] = shape
	}
	admitted := map[*ast.Node]ObjectLocal{}
	for {
		next := map[*ast.Node]ObjectLocal{}
		for declaration, shape := range candidates {
			sameShape := func(other string) bool {
				held, seen := shapeOfName[other]
				return seen && held != "" && held == shape
			}
			if local, ok := ObjectLocalOf(body, declaration, sameShape); ok {
				next[declaration] = local
			}
		}
		if len(next) == len(candidates) {
			admitted = next
			break
		}
		// a declined candidate withdraws its shape, which may decline a
		// partner that assigned from it — repeat until stable
		for declaration := range candidates {
			if _, kept := next[declaration]; !kept {
				name := declaration.AsVariableDeclaration().Name().Text()
				delete(shapeOfName, name)
				delete(candidates, declaration)
			}
		}
		admitted = next
		if len(candidates) == 0 {
			break
		}
	}
	return admitted
}

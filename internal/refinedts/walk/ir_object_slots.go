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
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
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
//
// Initializer is nil for the families whose leaves come from a
// DECLARATION rather than from a literal row — a constructor's exit
// rows, a declared record type. Those leaves carry their evidence in
// Sort/TypeofTag instead, which ObjectLocalKeySort and
// ObjectLocalKeyTypeof read in front of the initializer's syntax. A
// literal's leaf leaves both zero and keeps the syntax reading it has
// always had.
type ObjectLocalKey struct {
	Path        []string
	Key         string
	SlotName    string
	Initializer *ast.Node
	// Sort and TypeofTag are the DECLARED evidence for a leaf with no
	// initializer to read. Declared is what tells the two apart: a
	// literal leaf and a declared leaf whose sort happens to be the zero
	// BindingKind must not be confused.
	Declared  bool
	Sort      BindingKind
	TypeofTag TypeofTag
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
// identifier key OR a STABLE SYMBOL const key; spreads, other computed
// keys, shorthand rows, methods, accessors and array-literal values all
// decline — none of them names one leaf holding one scalar. `prefix` is
// the path already walked below the holder, `holder` the spelled root
// ("p").
//
// A SYMBOL-KEYED ROW (`{ [K_MODULE_ID]: id }`) contributes its leaf
// under the derived `#sym:` name, which is the same key identity the
// class census spells its symbol fields with (StableSymbolKeyName,
// ir_field_bundles.go). What makes it one leaf is what makes it one
// field there: the const cannot be rebound, its declaration runs once
// per module, and the symbol IS the property key at runtime, so two rows
// spelled with the same const are one key and rows spelled with
// different consts are different keys.
//
// The SPELLING composes with the leaf vocabulary unchanged. A leaf named
// `#sym:S` under holder `p` spells the slot "p.#sym:S", and every reader
// of that spelling reads it as one step below p: leafSlotsUnder trims
// the `p.` prefix and keeps the rest whole, and splitOneStep cuts at the
// FIRST dot, so a `#sym:` name — which holds no dot — stays one member.
// The `#` and `:` are what keep it apart from a plain property name and
// from a `#`-named private one, exactly as the field census's own note
// says.
func flatKeysOfLiteral(literal *ast.Node, holder string, prefix []string) ([]ObjectLocalKey, bool) {
	return flatKeysOfLiteralWith(nil, literal, holder, prefix)
}

// flatKeysOfLiteralWith is flatKeysOfLiteral carrying the checker the
// STABLE SYMBOL KEY needs. With a nil checker every computed key
// declines, which is the reading every caller had before the symbol key
// reached this vocabulary.
func flatKeysOfLiteralWith(
	c *checker.Checker,
	literal *ast.Node,
	holder string,
	prefix []string,
) ([]ObjectLocalKey, bool) {
	var keys []ObjectLocalKey
	seen := map[string]struct{}{}
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		if !ast.IsPropertyAssignment(property) {
			return nil, false
		}
		assignment := property.AsPropertyAssignment()
		if assignment.Initializer == nil {
			return nil, false
		}
		var key string
		switch {
		case ast.IsIdentifier(assignment.Name()):
			key = assignment.Name().Text()
		default:
			// a computed key names one leaf only through a stable symbol
			// const; every other computed key names nothing the vector holds
			symbolKey, isSymbolKey := symbolMemberFieldName(c, assignment.Name())
			if !isSymbolKey {
				return nil, false
			}
			key = symbolKey
		}
		if _, already := seen[key]; already {
			return nil, false
		}
		seen[key] = struct{}{}
		path := append(append([]string{}, prefix...), key)
		value := Unwrapped(assignment.Initializer)
		// a nested fixed-shape literal contributes its OWN leaves under
		// this key — one more level of the same rule
		if ast.IsObjectLiteralExpression(value) {
			nested, ok := flatKeysOfLiteralWith(c, value, holder, path)
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
//
// A SYMBOL-KEYED step `p[S]` is not one of those declines: under a
// stable symbol const it names ONE declared leaf, so it reads and writes
// that leaf exactly as `p.k` does. The refusal it used to take was the
// bare-name arm catching `p` inside an element access, which is the
// right answer for `p[e]` — an index nothing spells — and the wrong one
// for a key the vocabulary now holds. `p?.[S]` still declines: an
// absent receiver is what no leaf spells.
func usesAreAllDeclaredKeySteps(
	c *checker.Checker,
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
						if rows, rowsOk := flatKeysOfLiteralWith(c, right, name, nil); rowsOk && recordShapeOf(rows) == shape {
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
		// `p[S]` under a stable symbol const — one declared leaf, read or
		// written the way a dotted step is. The root is consumed here so it
		// does not reach the bare-name test below.
		if ast.IsElementAccessExpression(node) {
			element := node.AsElementAccessExpression()
			if root := Unwrapped(element.Expression); root != nil &&
				ast.IsIdentifier(root) && root.Text() == name {
				symbolKey, isSymbolKey := SymbolKeyedFieldName(c, node)
				if !isSymbolKey {
					ok = false // an index nothing spells
					return true
				}
				if _, isDeclared := declared[symbolKey]; !isDeclared {
					ok = false // a leaf the literal never gave a slot
					return true
				}
				// the KEY expression is the const's own name, not a use of
				// the record — nothing to walk under it
				return false
			}
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
	return ObjectLocalIn(nil, body, declaration, sameShapeName, nil)
}

// ObjectLocalIn is ObjectLocalOf with the check's own context, so the
// declarations whose leaves come from a DECLARATION rather than a
// literal are recognized too: a `new C()` whose constructor serves, a
// declared record type over an opaque initializer, and a `??` or ternary
// both of whose arms name one family.
//
// The four routes are tried in that order and are EXCLUSIVE — the first
// that answers a family is the local's, and one name has one slot
// family. The USE SCAN is then the same scan for every family: whatever
// produced the leaves, every occurrence of the name in the body must
// still be a path on a declared leaf or one of the recognized
// whole-record forms. Widening which declarations produce families never
// widens which uses are admitted.
//
// `familyOfName` answers the leaves another spelled local flattens to,
// which the join arms read; the batch entry ObjectLocalsIn supplies it,
// and nil admits no name-armed join.
func ObjectLocalIn(
	ctx *FlowContext,
	body *ast.Node,
	declaration *ast.Node,
	sameShapeName func(other string) bool,
	familyOfName func(name string) ([]ObjectLocalKey, bool),
) (ObjectLocal, bool) {
	if !ast.IsVariableDeclaration(declaration) ||
		!ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ObjectLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	keys, ok := declarationLeavesOf(ctx, declaration, name, familyOfName)
	if !ok {
		return ObjectLocal{}, false
	}
	// a leaf's own initializer must not mention the record — `{ lo: 0, hi:
	// p.lo }` reads a slot that does not exist yet. A DECLARED leaf has no
	// initializer and no such row to check.
	for _, key := range keys {
		if key.Initializer != nil && mentionsName(key.Initializer, name) {
			return ObjectLocal{}, false
		}
	}
	if sameShapeName == nil {
		sameShapeName = func(string) bool { return false }
	}
	if !usesAreAllDeclaredKeySteps(checkerOf(ctx), body, declaration, name, keys, sameShapeName) {
		return ObjectLocal{}, false
	}
	return ObjectLocal{Declaration: declaration, Name: name, Keys: keys}, true
}

// declarationLeavesOf is the one place the four family sources are
// ordered: the object literal first (the reading that needs no context
// and has always served), then the constructor's exit rows, then the
// declared type, then the two-armed join. The first answer wins.
func declarationLeavesOf(
	ctx *FlowContext,
	declaration *ast.Node,
	name string,
	familyOfName func(name string) ([]ObjectLocalKey, bool),
) ([]ObjectLocalKey, bool) {
	if literal := objectLiteralOfDeclaration(declaration); literal != nil {
		return flatKeysOfLiteralWith(checkerOf(ctx), literal, name, nil)
	}
	if ctx == nil {
		return nil, false
	}
	if keys, ok := constructedLeavesOf(ctx, declaration); ok {
		return keys, true
	}
	if keys, ok := declaredTypeLeavesOf(ctx, declaration); ok {
		return keys, true
	}
	return joinedArmLeavesOf(ctx, declaration, familyOfName)
}

/* ── the NON-LITERAL declarations that flatten ───────────────────── */

// declaredLeavesOf turns a member list read off a DECLARATION — a
// declared record type's members, a constructor's this-field exits —
// into the leaf list a flattened local carries. Every leaf is one step
// below the holder (a declaration names members, not paths), and each
// carries its own declared sort rather than an initializer to read.
func declaredLeavesOf(holder string, members []recordParamMember) []ObjectLocalKey {
	keys := make([]ObjectLocalKey, 0, len(members))
	for _, member := range members {
		keys = append(keys, ObjectLocalKey{
			Path:      []string{member.Key},
			Key:       member.Key,
			SlotName:  holder + "." + member.Key,
			Declared:  true,
			Sort:      member.Sort,
			TypeofTag: member.TypeofTag,
		})
	}
	return keys
}

// declaredTypeLeavesOf is recognizer (2): the leaves a declaration's own
// TYPE ANNOTATION names, whatever its initializer turns out to be.
//
// WHY AN OPAQUE INITIALIZER IS STILL SOUND — the parameter precedent,
// verbatim. A record-expanded PARAMETER already lays out one slot per
// declared member and fills none of them with a value: the members come
// from the annotation, the values come from whatever the caller passed,
// and the entry slots stand for values this body has never seen. That is
// exactly the position a `const x: Bounds = somethingOpaque()` local is
// in — the annotation promises the member NAMES to every value the
// initializer could produce, and the leaves claim nothing about the
// member VALUES. Declared leaves with unknown values claim nothing
// false; they only give the body's `x.lo` a slot to read, which then
// answers the same unknown the whole-name slot answered. The soundness
// does not come from the initializer at all, which is why an opaque one
// costs nothing.
//
// The members read through recordParamMembersIn's own reader, so a
// declared record local and a declared record parameter expand to
// byte-identical member lists — one reading, two seams. Everything that
// reader declines (a class, an unbounded generic, a merged interface, a
// computed member name, a union whose arms share no member) declines here
// too and the local keeps its whole-name slot. A member whose annotation
// does not sort contributes an UNKNOWN-SORTED leaf rather than declining,
// exactly as it does for a parameter — the leaf names the key and admits
// only the definedness test.
func declaredTypeLeavesOf(ctx *FlowContext, declaration *ast.Node) ([]ObjectLocalKey, bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	decl := declaration.AsVariableDeclaration()
	if decl.Type == nil || decl.Name() == nil || !ast.IsIdentifier(decl.Name()) {
		return nil, false
	}
	holder := decl.Name().Text()
	members, isRecord := declaredTypeNodeMembers(ctx, holder, decl.Type)
	if !isRecord {
		return nil, false
	}
	return declaredLeavesOf(holder, members), true
}

// declaredTypeNodeMembers reads ONE type annotation as a scalar member
// list: an inline type literal off its own syntax, a UNION through the
// members its arms share, a named type through the resolution
// recordParamMembersIn performs, and a TYPE PARAMETER through its own
// CONSTRAINT.
//
// The type-parameter arm is what `<T extends Bounds>(x: T)` needs: the
// constraint is the only thing the annotation promises about every T a
// caller could apply, so the constraint's members are the ones the
// leaves may name. An UNBOUNDED type parameter promises nothing and
// declines — there is no member list a caller could not contradict.
func declaredTypeNodeMembers(ctx *FlowContext, holder string, typeNode *ast.Node) ([]recordParamMember, bool) {
	if typeNode == nil {
		return nil, false
	}
	if ast.IsTypeLiteralNode(typeNode) {
		return scalarMemberListOf(holder, typeNode.AsTypeLiteralNode().Members.Nodes)
	}
	// a UNION annotation reads through the same reader a record PARAMETER
	// takes — the members every arm declares — so a declared local and a
	// declared parameter written with one union expand to one member list
	if ast.IsUnionTypeNode(typeNode) {
		return namedTypeMembersOf(ctx, holder, typeNode)
	}
	if !ast.IsTypeReferenceNode(typeNode) {
		return nil, false
	}
	if members, isRecord := namedTypeMembersOf(ctx, holder, typeNode); isRecord {
		return members, true
	}
	return constraintMembersOf(ctx, holder, typeNode)
}

// constraintMembersOf resolves a type reference that names a TYPE
// PARAMETER to the members its constraint spells.
//
// The reference's own decline rules apply first — type arguments and
// qualified names are refused by namedTypeMembersOf before this is
// reached — and the constraint is then read by the SAME member reader
// every other route uses, so a constraint written as an inline literal
// and one written behind an interface expand identically. A type
// parameter with no constraint, or a constraint the member reader
// declines, answers false and the declaration keeps its whole-name slot.
func constraintMembersOf(ctx *FlowContext, holder string, typeNode *ast.Node) ([]recordParamMember, bool) {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, false
	}
	reference := typeNode.AsTypeReferenceNode()
	if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
		return nil, false
	}
	typeName := reference.TypeName
	if typeName == nil || !ast.IsIdentifier(typeName) {
		return nil, false
	}
	symbol := symbolAt(ctx.P.Checker, typeName)
	if symbol == nil || len(symbol.Declarations) != 1 {
		return nil, false
	}
	declaration := symbol.Declarations[0]
	if declaration == nil || declaration.Kind != ast.KindTypeParameter {
		return nil, false
	}
	constraint := declaration.AsTypeParameterDeclaration().Constraint
	if constraint == nil {
		// an UNBOUNDED generic promises no member to any caller
		return nil, false
	}
	// the constraint is read by the ordinary member rules; a constraint
	// that is itself a type parameter is not followed — one link only, so
	// the reading cannot loop
	if ast.IsTypeLiteralNode(constraint) {
		return scalarMemberListOf(holder, constraint.AsTypeLiteralNode().Members.Nodes)
	}
	return namedTypeMembersOf(ctx, holder, constraint)
}

// constructedLeavesOf is recognizer (1): a `const x = new C()` local
// whose constructor SERVES flattens to the fields that constructor
// writes, each leaf wearing its DECLARED field sort.
//
// The field family comes from the same rows constructorFieldRets
// threads: a served constructor's summary carries one this-entry per
// field the body touches, and the WRITTEN ones are the fields the
// constructor actually left a value in. Those are the leaves worth
// slots — an entry the constructor only READS holds whatever the field
// entered with, which for a fresh instance is absent, so giving it a
// leaf would offer a slot no exit ever fills.
//
// The SORT comes from the class's own field declarations (ClassFieldsOf,
// the reading the receiver bundle already lays its slots out under), not
// from the exit row: a BundleEntry names where a slot sits and whether
// the body moved it, and nothing else. Reading the sort from the
// declaration is what makes this local's leaf and the same field's slot
// inside a method wear one sort. A written field the class does not
// declare — one assigned in the constructor with no property
// declaration — has no annotation to read and takes the unknown sort,
// which admits only the definedness test.
//
// A constructor that does not serve — no summary, no resolvable class,
// no written this-field — declines, and the local keeps its whole-name
// slot exactly as today.
func constructedLeavesOf(ctx *FlowContext, declaration *ast.Node) ([]ObjectLocalKey, bool) {
	if ctx == nil || !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	decl := declaration.AsVariableDeclaration()
	if decl.Initializer == nil || decl.Name() == nil || !ast.IsIdentifier(decl.Name()) {
		return nil, false
	}
	construction := Unwrapped(decl.Initializer)
	if construction == nil || !ast.IsNewExpression(construction) {
		return nil, false
	}
	constructor := constructedClassConstructor(ctx, construction)
	if constructor == nil {
		return nil, false
	}
	shape, served := LowerSummaryBody(ctx, constructor)
	if !served {
		return nil, false
	}
	// the declared sorts of the class the constructor belongs to; a class
	// whose fields do not read leaves every leaf unknown-sorted
	sortOfField := map[string]BundleField{}
	if fields, readable := ClassFieldsOf(ctx, constructor.Parent); readable {
		for _, field := range fields {
			sortOfField[field.Name] = field
		}
	}
	holder := decl.Name().Text()
	var keys []ObjectLocalKey
	seen := map[string]struct{}{}
	for _, entry := range shape.BundleEntries {
		if !entry.Written {
			continue
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			continue
		}
		if _, already := seen[field]; already {
			continue
		}
		seen[field] = struct{}{}
		sort, tag := BindingKindUnknown, TypeofTagNone
		if declared, found := sortOfField[field]; found {
			sort, tag = declared.Sort, declared.TypeofTag
		}
		keys = append(keys, ObjectLocalKey{
			Path:      []string{field},
			Key:       field,
			SlotName:  holder + "." + field,
			Declared:  true,
			Sort:      sort,
			TypeofTag: tag,
		})
	}
	if len(keys) == 0 {
		return nil, false
	}
	return keys, true
}

// constructedClassConstructor resolves a `new C()` to the CONSTRUCTOR
// declaration whose summary the leaves are read from — the SAME
// resolution the call lowering uses, so the leaves and the exit rows
// that fill them always name one declaration. A `new` whose callee does
// not resolve to a class with a constructor body answers nil.
func constructedClassConstructor(ctx *FlowContext, construction *ast.Node) *ast.Node {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil
	}
	callee := Unwrapped(construction.AsNewExpression().Expression)
	if callee == nil || !ast.IsIdentifier(callee) {
		return nil
	}
	symbol := symbolAt(ctx.P.Checker, callee)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	classLike := symbol.ValueDeclaration
	if !ast.IsClassLike(classLike) || ast.GetSourceFileOfNode(classLike).IsDeclarationFile {
		return nil
	}
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if ast.IsConstructorDeclaration(member) && member.Body() != nil {
			return member
		}
	}
	return nil
}

// joinedArmLeavesOf is recognizer (3): `const x = a ?? b` and
// `const x = c ? a : b` where BOTH arms flatten to the SAME member
// family, which the local then takes as its own.
//
// The join is per leaf: a member both arms spell keeps its sort where
// the two agree and takes the unknown sort where they disagree — the
// weaker reading, which claims nothing either arm contradicts. Arms
// naming DIFFERENT member sets have no one family, so the local keeps
// its whole-name slot: one name has one slot family, the exclusivity
// rule the whole flattening rests on.
//
// Each arm is read by armLeavesOf: a spelled object literal, a name
// whose own declaration flattens, or — for any arm at all — the members
// its own DECLARED TYPE spells. So a member read (`request.socket`) is
// an admissible arm whenever the member's annotation reads as a record;
// what refuses it is the annotation, never the arm's syntax. An arm
// whose type reads as a class, a union, or a generic still names no
// member family, and the whole shape declines.
func joinedArmLeavesOf(
	ctx *FlowContext,
	declaration *ast.Node,
	familyOfName func(name string) ([]ObjectLocalKey, bool),
) ([]ObjectLocalKey, bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return nil, false
	}
	decl := declaration.AsVariableDeclaration()
	if decl.Initializer == nil || decl.Name() == nil || !ast.IsIdentifier(decl.Name()) {
		return nil, false
	}
	holder := decl.Name().Text()
	left, right, isJoin := joinArmsOf(Unwrapped(decl.Initializer))
	if !isJoin {
		return nil, false
	}
	leftKeys, leftOk := armLeavesOf(ctx, holder, left, familyOfName)
	if !leftOk {
		return nil, false
	}
	rightKeys, rightOk := armLeavesOf(ctx, holder, right, familyOfName)
	if !rightOk {
		return nil, false
	}
	if recordShapeOf(leftKeys) != recordShapeOf(rightKeys) {
		// the arms disagree about the family — no one slot layout serves
		// both, so the name stays whole
		return nil, false
	}
	sortOfPath := map[string]BindingKind{}
	tagOfPath := map[string]TypeofTag{}
	for _, key := range rightKeys {
		sortOfPath[strings.Join(key.Path, ".")] = ObjectLocalKeySort(key)
		tagOfPath[strings.Join(key.Path, ".")] = ObjectLocalKeyTypeof(key)
	}
	out := make([]ObjectLocalKey, 0, len(leftKeys))
	for _, key := range leftKeys {
		path := strings.Join(key.Path, ".")
		sort := ObjectLocalKeySort(key)
		tag := ObjectLocalKeyTypeof(key)
		if other := sortOfPath[path]; other != sort {
			sort = BindingKindUnknown
		}
		if other := tagOfPath[path]; other != tag {
			tag = TypeofTagNone
		}
		out = append(out, ObjectLocalKey{
			Path:      key.Path,
			Key:       key.Key,
			SlotName:  key.SlotName,
			Declared:  true,
			Sort:      sort,
			TypeofTag: tag,
		})
	}
	return out, true
}

// joinArmsOf is the two arms a joining initializer holds: `a ?? b`'s
// sides, and a ternary's two branches. Its CONDITION is not an arm —
// nothing about the record's shape comes from it.
func joinArmsOf(initializer *ast.Node) (left *ast.Node, right *ast.Node, ok bool) {
	if initializer == nil {
		return nil, nil, false
	}
	if ast.IsBinaryExpression(initializer) {
		bin := initializer.AsBinaryExpression()
		if bin.OperatorToken.Kind == ast.KindQuestionQuestionToken {
			return Unwrapped(bin.Left), Unwrapped(bin.Right), true
		}
		return nil, nil, false
	}
	if initializer.Kind == ast.KindConditionalExpression {
		conditional := initializer.AsConditionalExpression()
		if conditional.WhenTrue == nil || conditional.WhenFalse == nil {
			return nil, nil, false
		}
		return Unwrapped(conditional.WhenTrue), Unwrapped(conditional.WhenFalse), true
	}
	return nil, nil, false
}

// armLeavesOf is the member family ONE arm of a join names, spelled
// under the joined local's holder: an object literal's own leaves, the
// family a spelled name's declaration already flattens to, or — for any
// arm at all — the members the arm's own RESOLVED TYPE spells.
//
// WHY THE TYPE READING IS THE RIGHT THIRD ARM. The first two arms read
// the arm's VALUE: a literal names its rows, a flattening name names the
// family its declaration already carries. Neither reaches
// `request.socket` or `pick()`, and the reason is not that those arms
// promise less — it is that the reading was looking in the wrong place.
// What a join arm has to supply is a MEMBER FAMILY, and a declared type
// is exactly a promise of member names over every value the expression
// could produce. That is the same argument declaredTypeLeavesOf already
// makes for an opaque initializer, applied one level out: the annotation
// promises the names, the leaves claim nothing about the values, and the
// slots then answer whatever the whole-name slot answered.
//
// So the fallback is by DECLARED TYPE and works for ANY arm expression
// whose resolution spells a record — a name, a member read, a call — and
// every existing rule stands on top of it unchanged: the two arms must
// still agree leaf for leaf (recordShapeOf), and the per-leaf sort join
// still weakens a disagreement to unknown.
//
// A leaf here is one step by construction (declaredLeavesOf spells a
// member list, never a path), which is what keeps a type-read arm and a
// literal arm comparable at all.
func armLeavesOf(
	ctx *FlowContext,
	holder string,
	arm *ast.Node,
	familyOfName func(name string) ([]ObjectLocalKey, bool),
) ([]ObjectLocalKey, bool) {
	if arm == nil {
		return nil, false
	}
	if ast.IsObjectLiteralExpression(arm) {
		return flatKeysOfLiteral(arm, holder, nil)
	}
	if ast.IsIdentifier(arm) && familyOfName != nil {
		if keys, found := familyOfName(arm.Text()); found {
			// respell the family under THIS local's holder — the arm's own
			// holder names the arm's slots, not the joined local's
			out := make([]ObjectLocalKey, 0, len(keys))
			for _, key := range keys {
				out = append(out, ObjectLocalKey{
					Path:        key.Path,
					Key:         key.Key,
					SlotName:    holder + "." + strings.Join(key.Path, "."),
					Initializer: key.Initializer,
					Declared:    key.Declared,
					Sort:        key.Sort,
					TypeofTag:   key.TypeofTag,
				})
			}
			return out, true
		}
	}
	return resolvedTypeArmLeaves(ctx, holder, arm)
}

// resolvedTypeArmLeaves is the member family ONE join arm's own
// DECLARED TYPE spells, whatever the arm's syntax is.
//
// The arm resolves through the checker to the declaration it names, and
// that declaration's SPELLED type node is read by declaredTypeNodeMembers
// — the same reader a declared record local and a record parameter take,
// so an arm annotated `Bounds` and a local declared `Bounds` expand to
// byte-identical member lists. The checker is asked only which
// declaration an expression names; the members come off that
// declaration's own syntax, which is what keeps the answer
// check-independent the way the one-level reading is.
//
// The reachable arms, all through one resolution: a NAME resolves to its
// variable or parameter declaration, a MEMBER READ (`h.window`) resolves
// to the property declaration or signature it steps to, and a CALL
// (`make()`) resolves through its CALLEE to that declaration's spelled
// RETURN annotation (calleeReturnTypeNodeOf) — a call places no symbol
// of its own, but what it promises is exactly what its callee declares
// it returns. One shape resolves to no declaration and declines here:
//
//   - a member read whose RECEIVER is `any`. The checker resolves the
//     whole access to `any` and places no symbol, which is the correct
//     answer: an `any` receiver promises no member to anybody, so there
//     is no annotation naming a family and none can be invented. This is
//     what nest's `request.socket` is — `TRequest extends
//     IncomingMessage = any` makes the receiver's resolved type `any`,
//     and the arm names no family for that reason rather than for the
//     class rule.
//
// Everything declaredTypeNodeMembers refuses refuses here too: a CLASS
// type (an instance carries methods, private state and identity no
// flattening holds), a union, a generic with type arguments, a merged
// interface, a non-record annotation, and a declaration with no spelled
// type at all (an inferred field). In each case the arm names no family
// and the joined local keeps its whole-name slot.
func resolvedTypeArmLeaves(ctx *FlowContext, holder string, arm *ast.Node) ([]ObjectLocalKey, bool) {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil || arm == nil {
		return nil, false
	}
	typeNode := declaredTypeNodeOfExpression(ctx, arm)
	if typeNode == nil {
		return nil, false
	}
	members, isRecord := declaredTypeNodeMembers(ctx, holder, typeNode)
	if !isRecord {
		return nil, false
	}
	return declaredLeavesOf(holder, members), true
}

// declaredTypeNodeOfExpression is the type node an EXPRESSION's own
// declaration spells — declaredTypeNodeOfReceiver's reading
// (return_type_ground.go) over a join arm rather than a `.get()`
// receiver, and for the same reason: what matters is that the resolved
// declaration spells its type, not how the expression is written.
//
// A name, a `this.field`, and a longer property chain all reach the same
// declaration question. An expression the checker does not place, or one
// whose declaration carries no spelled type, answers nil.
//
// A CALL is the one arm whose type is not its own declaration's: a call
// expression places no symbol, so the reading above answers nil for
// `flag ? make() : fallback` however well `make` is annotated. What a
// call promises is its CALLEE's spelled RETURN type, so the call arm
// resolves the callee to its declaration — the same resolution this
// function performs on a name — and reads the return annotation off it.
func declaredTypeNodeOfExpression(ctx *FlowContext, e *ast.Node) *ast.Node {
	if ast.IsCallExpression(e) {
		return calleeReturnTypeNodeOf(ctx, e)
	}
	symbol := ctx.P.Checker.GetSymbolAtLocation(e)
	if symbol == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if declaration == nil {
		// an interface member has no value declaration; its property
		// signature is the spelling
		for _, held := range symbol.Declarations {
			if ast.IsPropertySignatureDeclaration(held) {
				declaration = held
				break
			}
		}
	}
	if declaration == nil {
		return nil
	}
	switch {
	case ast.IsParameterDeclaration(declaration):
		return declaration.AsParameterDeclaration().Type
	case ast.IsVariableDeclaration(declaration):
		return declaration.AsVariableDeclaration().Type
	case ast.IsPropertyDeclaration(declaration):
		return declaration.AsPropertyDeclaration().Type
	case ast.IsPropertySignatureDeclaration(declaration):
		return declaration.AsPropertySignatureDeclaration().Type
	}
	return nil
}

// calleeReturnTypeNodeOf is the RETURN type node a call's callee
// declares — the reading that lets `flag ? make() : fallback` name a
// member family when the two-arm join reads a record.
//
// THE ROUTE, and why it is this one. The join arms are compared by
// declaredTypeNodeMembers, which reads a type NODE — the annotation's
// own syntax — so the answer has to be a node, not a checker type. A
// call resolves to no declaration of its own, but its CALLEE does: a
// bare name resolves to the function declaration, a `this.make()` or
// `helpers.make()` to the method declaration or the property holding
// the function. Reading the return annotation off that declaration is
// the same claim declaredTypeNodeOfReceiver makes about a receiver's
// declared type, one level in — the annotation promises the names over
// every value the call could produce, and the leaves claim nothing about
// the values.
//
// The alias-following symbol lookup is the one every registry uses, so
// an IMPORTED `make` resolves to its declaration in the exporting file
// and reads that file's annotation.
//
// WHAT ANSWERS NIL, each a plain refusal rather than a guess:
//
//   - an INFERRED return type. `function make() { return { … } }` spells
//     no return annotation, so there is no node to read. The resolved
//     TYPE holds the members, but the member reader consumes a node, and
//     a synthesized node would be a second reading of member names
//     beside the one this file guarantees both arms take. The arm names
//     no family and the joined local keeps its whole-name slot.
//   - a callee this resolution does not place — a call through a value
//     (`handlers[k]()`), an immediately-invoked literal, an overloaded
//     name whose declarations disagree.
//   - a callee whose declaration is not a function form that spells a
//     return type at all.
//
// Everything declaredTypeNodeMembers refuses still refuses on top of
// this — a class type, a union, a generic with type arguments.
func calleeReturnTypeNodeOf(ctx *FlowContext, call *ast.Node) *ast.Node {
	callee := Unwrapped(call.AsCallExpression().Expression)
	if callee == nil {
		return nil
	}
	// the NAME half is what carries the symbol: a bare identifier is its
	// own name, and a member call's name is the step
	var name *ast.Node
	switch {
	case ast.IsIdentifier(callee):
		name = callee
	case ast.IsPropertyAccessExpression(callee):
		name = callee.AsPropertyAccessExpression().Name()
	default:
		return nil
	}
	symbol := symbolAt(ctx.P.Checker, name)
	if symbol == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if declaration == nil {
		// an interface method or a declared function signature has no value
		// declaration; the signature is the spelling
		for _, held := range symbol.Declarations {
			if returnTypeNodeOfDeclaration(held) != nil {
				declaration = held
				break
			}
		}
	}
	if declaration == nil {
		return nil
	}
	return returnTypeNodeOfDeclaration(declaration)
}

// returnTypeNodeOfDeclaration is the return type node a DECLARATION
// spells, over the forms a callee resolves to: the function-like forms
// themselves, the interface/class method and function signatures, and a
// `const f = (…): R => …` or `handler: (…) => R` whose annotation sits
// on the initializer or on the property's own function type.
func returnTypeNodeOfDeclaration(declaration *ast.Node) *ast.Node {
	if declaration == nil {
		return nil
	}
	switch {
	case ast.IsArrowFunction(declaration):
		return declaration.AsArrowFunction().Type
	case ast.IsFunctionExpression(declaration):
		return declaration.AsFunctionExpression().Type
	case ast.IsFunctionDeclaration(declaration):
		return declaration.AsFunctionDeclaration().Type
	case ast.IsMethodDeclaration(declaration):
		return declaration.AsMethodDeclaration().Type
	case ast.IsMethodSignatureDeclaration(declaration):
		return declaration.AsMethodSignatureDeclaration().Type
	case ast.IsVariableDeclaration(declaration):
		// `const make: () => Bounds = …` states the return on the variable's
		// own function type; `const make = (): Bounds => …` states it on the
		// initializer. The annotation wins where both are spelled — it is
		// what every caller is checked against.
		if annotation := declaration.AsVariableDeclaration().Type; annotation != nil {
			return functionTypeReturnNodeOf(annotation)
		}
		return returnTypeNodeOfDeclaration(Unwrapped(declaration.AsVariableDeclaration().Initializer))
	case ast.IsPropertyDeclaration(declaration):
		if annotation := declaration.AsPropertyDeclaration().Type; annotation != nil {
			return functionTypeReturnNodeOf(annotation)
		}
		return returnTypeNodeOfDeclaration(Unwrapped(declaration.AsPropertyDeclaration().Initializer))
	case ast.IsPropertySignatureDeclaration(declaration):
		return functionTypeReturnNodeOf(declaration.AsPropertySignatureDeclaration().Type)
	}
	return nil
}

// functionTypeReturnNodeOf is the return node of a spelled FUNCTION TYPE
// — the `Bounds` of `(x: number) => Bounds`. Anything else spells no
// return.
func functionTypeReturnNodeOf(typeNode *ast.Node) *ast.Node {
	if typeNode == nil || !ast.IsFunctionTypeNode(typeNode) {
		return nil
	}
	return typeNode.AsFunctionTypeNode().Type
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

// ObjectLocalKeySort is a flattened leaf's sort. A leaf that came from a
// DECLARATION — a constructor's exit row, a declared record member —
// carries its own sort and answers it; a leaf that came from a literal
// row reads the way LocalSort reads a scalar local's: a string literal
// is a string, anything else the lowering reads numerically.
func ObjectLocalKeySort(key ObjectLocalKey) BindingKind {
	if key.Declared {
		return key.Sort
	}
	if ast.IsStringLiteral(Unwrapped(key.Initializer)) {
		return BindingKindString
	}
	return BindingKindNumber
}

// ObjectLocalKeyTypeof is a flattened leaf's typeof evidence: the
// declared tag where the leaf came from a declaration, and the
// initializer's syntax alone otherwise — the twin of LocalTypeof.
func ObjectLocalKeyTypeof(key ObjectLocalKey) TypeofTag {
	if key.Declared {
		return key.TypeofTag
	}
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
//
// A SYMBOL-KEYED step (`p[S]`) resolves here too, under the derived
// `#sym:` leaf name the flattening spelled it with. The path is one step
// by construction — the key is a const's own name, never a chain — so
// the spelling is `p.#sym:S` and the lookup is the same lookup.
func PathSlotIndexOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if root, path, ok := propertyPathOf(head); ok {
		return slotIndexOfName(context, root+"."+strings.Join(path, "."))
	}
	if root, leaf, ok := symbolKeyedLeafOf(context, head); ok {
		return slotIndexOfName(context, root+"."+leaf)
	}
	return 0, false
}

// symbolKeyedLeafOf reads `p[S]` as the holder and the derived `#sym:`
// leaf name it steps to — the element-access twin of propertyPathOf's
// one-step reading, over the same stable key identity the flattening
// spelled the leaf with.
//
// A holder that is not a plain name, an optional step, or a key that is
// not a stable symbol const answers false: those name no leaf, which is
// the same answer the recognizer's own use scan gives them.
func symbolKeyedLeafOf(context *LoweringContext, node *ast.Node) (root string, leaf string, ok bool) {
	if context == nil || node == nil || !ast.IsElementAccessExpression(node) {
		return "", "", false
	}
	holder := Unwrapped(node.AsElementAccessExpression().Expression)
	if holder == nil || !ast.IsIdentifier(holder) {
		return "", "", false
	}
	name, isSymbolKey := SymbolKeyedFieldName(checkerOf(context.Flow), node)
	if !isSymbolKey {
		return "", "", false
	}
	return holder.Text(), name, true
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
		if key.Declared || key.Initializer == nil {
			// a DECLARED leaf has no row to lower: the declaration named the
			// member, and the initializer said nothing about its value. The
			// statement route declines and the declaration's own route — the
			// opaque call's havoc, the constructor's exit rows — is what
			// fills these slots.
			return nil, false
		}
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

// DestructuringWithDefaultsOf is the leaf-exact destructuring with
// per-element DEFAULTS: `const { x = 1, y } = p` from a flattened
// holder. Each element assigns its leaf's slot, and a defaulted element
// follows with the definedness branch the defaulted parameters ride —
// only an undefined leaf takes the default. Rests, nested patterns,
// computed keys, and defaults the effect grammar cannot spell decline
// to the routes after.
func DestructuringWithDefaultsOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
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
		holder = "this"
	default:
		return nil, false
	}
	var out []kernelbridge.IrStatement
	sawDefault := false
	for _, element := range decl.Name().AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || !ast.IsIdentifier(binding.Name()) {
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
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: target,
			Effect: varEffect(source),
		})
		if binding.Initializer == nil {
			continue
		}
		sawDefault = true
		if !writeAndCallFree(binding.Initializer) {
			return nil, false
		}
		defaultEffect, lowered := RhsEffect(context, context.Sorts[target], binding.Initializer)
		if !lowered {
			return nil, false
		}
		out = append(out, kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementBranch,
			On:   target,
			Test: kernelbridge.IrTestDefined,
			Else: []kernelbridge.IrStatement{{
				Kind:   kernelbridge.IrStatementAssign,
				Target: target,
				Effect: defaultEffect,
			}},
		})
	}
	// with no default present the plain route already served — this one
	// only exists for the defaulted shape
	if !sawDefault || len(out) == 0 {
		return nil, false
	}
	return out, true
}

// PatternAssignmentsOf is the destructuring lowering for every SOURCE
// the leaf-exact route above does not read: `const { a } = call()`,
// `const { b } = holder.path`, `const [x, y] = xs`. The bound values
// have no spelling — so every bound name that has a slot takes UNKNOWN,
// which is exactly what is true of it — and the source's own effects
// are carried: a call source lowers through the call machinery (a
// compiled callee splices, an opaque one havocs and names itself), and
// any other source must move nothing. The statement is then READ: the
// names were never knowable, and knowing that is not a hole.
//
// The pattern's own defaults and computed keys must also move nothing —
// a default expression with a call inside would run code the unknown
// assignment does not spell.
func PatternAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	decl := declarations[0].AsVariableDeclaration()
	name := decl.Name()
	if decl.Initializer == nil || name == nil ||
		(!ast.IsObjectBindingPattern(name) && !ast.IsArrayBindingPattern(name)) {
		return nil, false
	}
	if !patternMovesNothing(name) {
		return nil, false
	}
	source := Unwrapped(decl.Initializer)
	var out []kernelbridge.IrStatement
	if ast.IsCallExpression(source) {
		called, ok := SummaryCallOrHavoc(context, source, -1)
		if !ok {
			return nil, false
		}
		out = append(out, called...)
	} else if !writeAndCallFree(source) {
		return nil, false
	}
	// an ARRAY pattern from a FLATTENED array local reads each element
	// as element-or-undefined — the elem slot's join wrapped orAbsent —
	// instead of unknown: nothing bounds WHICH element each name took,
	// but every element is inside the elem join, and a short array
	// leaves undefined, which orAbsent spells exactly.
	if ast.IsArrayBindingPattern(name) && ast.IsIdentifier(source) {
		if _, elemSlot, slotsOk := arraySlotsOf(context, source.Text()); slotsOk {
			handled := true
			var precise []kernelbridge.IrStatement
			for _, element := range name.AsBindingPattern().Elements.Nodes {
				if element == nil || !ast.IsBindingElement(element) {
					continue
				}
				binding := element.AsBindingElement()
				bound := binding.Name()
				if binding.DotDotDotToken != nil || binding.Initializer != nil ||
					bound == nil || !ast.IsIdentifier(bound) {
					handled = false
					break
				}
				slot, has := slotIndexOfName(context, bound.Text())
				if !has {
					continue
				}
				elemVar := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: elemSlot}
				precise = append(precise, kernelbridge.IrStatement{
					Kind:   kernelbridge.IrStatementAssign,
					Target: slot,
					Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &elemVar},
				})
			}
			if handled {
				return append(out, precise...), true
			}
		}
	}
	for _, bound := range boundPatternNames(name) {
		slot, has := slotIndexOfName(context, bound)
		if !has {
			// an untracked name holds no slot and no belief — nothing to
			// assign
			continue
		}
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: slot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
		})
	}
	return out, true
}

// patternMovesNothing answers whether binding the pattern can run any
// code: every default value and every computed key must be write- and
// call-free, at every depth.
func patternMovesNothing(pattern *ast.Node) bool {
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		if element == nil || !ast.IsBindingElement(element) {
			continue
		}
		binding := element.AsBindingElement()
		if binding.Initializer != nil && !writeAndCallFree(binding.Initializer) {
			return false
		}
		if binding.PropertyName != nil && ast.IsComputedPropertyName(binding.PropertyName) {
			if !writeAndCallFree(binding.PropertyName.AsComputedPropertyName().Expression) {
				return false
			}
		}
		if name := binding.Name(); name != nil &&
			(ast.IsObjectBindingPattern(name) || ast.IsArrayBindingPattern(name)) {
			if !patternMovesNothing(name) {
				return false
			}
		}
	}
	return true
}

// boundPatternNames collects every identifier a pattern binds, at every
// depth — the names whose slots the unknown assignments cover.
func boundPatternNames(pattern *ast.Node) []string {
	var names []string
	var collect func(p *ast.Node)
	collect = func(p *ast.Node) {
		for _, element := range p.AsBindingPattern().Elements.Nodes {
			if element == nil || !ast.IsBindingElement(element) {
				continue
			}
			name := element.AsBindingElement().Name()
			if name == nil {
				continue
			}
			if ast.IsIdentifier(name) {
				names = append(names, name.Text())
				continue
			}
			if ast.IsObjectBindingPattern(name) || ast.IsArrayBindingPattern(name) {
				collect(name)
			}
		}
	}
	collect(pattern)
	return names
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
	return ObjectLocalsIn(nil, body, locals)
}

// ObjectLocalsIn is ObjectLocalsOf with the check's own context, so the
// non-literal families are recognized. The context is threaded, not
// consulted for anything but resolution: a nil one answers exactly what
// ObjectLocalsOf always answered, which is what the seams holding no
// checker keep.
func ObjectLocalsIn(ctx *FlowContext, body *ast.Node, locals []*ast.Node) map[*ast.Node]ObjectLocal {
	// candidate shapes, from each declaration's own family source. The
	// LITERAL candidates are collected first and become the family table
	// the join arms read, so `const x = a ?? b` sees whatever `a` and `b`
	// already flatten to. A join whose arm is itself a join is not
	// resolved — one level only, so the collection cannot depend on its
	// own order.
	shapeOfName := map[string]string{}
	candidates := map[*ast.Node]string{}
	literalFamilies := map[string][]ObjectLocalKey{}
	for _, declaration := range locals {
		if !ast.IsVariableDeclaration(declaration) ||
			!ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
			continue
		}
		literal := objectLiteralOfDeclaration(declaration)
		if literal == nil {
			continue
		}
		name := declaration.AsVariableDeclaration().Name().Text()
		if keys, ok := flatKeysOfLiteral(literal, name, nil); ok {
			literalFamilies[name] = keys
		}
	}
	// a name's family for the join arms: its literal leaves where it has
	// them, its DECLARED-TYPE leaves otherwise. The constructor and join
	// routes are deliberately not offered here — a constructor's leaves
	// depend on a summary the layout may still be building, and a join of
	// joins would make the answer depend on collection order.
	declaredFamilies := map[string][]ObjectLocalKey{}
	if ctx != nil {
		for _, declaration := range locals {
			if !ast.IsVariableDeclaration(declaration) ||
				!ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
				continue
			}
			name := declaration.AsVariableDeclaration().Name().Text()
			if _, hasLiteral := literalFamilies[name]; hasLiteral {
				continue
			}
			if keys, ok := declaredTypeLeavesOf(ctx, declaration); ok {
				declaredFamilies[name] = keys
			}
		}
	}
	familyOfName := func(name string) ([]ObjectLocalKey, bool) {
		if keys, found := literalFamilies[name]; found {
			return keys, true
		}
		keys, found := declaredFamilies[name]
		return keys, found
	}
	for _, declaration := range locals {
		if !ast.IsVariableDeclaration(declaration) ||
			!ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
			continue
		}
		name := declaration.AsVariableDeclaration().Name().Text()
		keys, ok := declarationLeavesOf(ctx, declaration, name, familyOfName)
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
			if local, ok := ObjectLocalIn(ctx, body, declaration, sameShape, familyOfName); ok {
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

// Object locals flattened into scalar slots for the flow IR.
//
// A body that keeps a fixed-shape record in a local — `const p = { lo:
// 0, hi: n }` — writes and reads it as `p.lo` / `p.hi`. The kernel's
// walk is over a vector of scalar slots, so such a local can be
// carried as ONE SLOT PER KEY, spelled "p.lo" and "p.hi": the names
// SpelledNameOf already produces for a single property step, which
// IndexOf already resolves. The kernel is untouched — it never learns
// two slots came from one object.
//
// The flattening is only sound while the object has no identity the
// body can observe. This file's recognizer answers the flat key set
// for a declaration, or declines: every use of the name in the body
// must be a `p.key` step on a key the literal declared, and the
// literal itself must be plain `key: expression` rows with no nesting.
// Anything else — an alias, a call argument, a return of the whole
// record, a delete, a key added later, a computed or spread key —
// declines, because after flattening those uses would read or write
// an object that no longer exists as one value.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// ObjectLocal is one flattened record local: the declaration it came
// from, the name it was spelled under, and its keys in literal order
// paired with the initializer each key was given.
type ObjectLocal struct {
	Declaration *ast.Node // VariableDeclaration
	Name        string
	Keys        []ObjectLocalKey
}

// ObjectLocalKey is one key of a flattened record: the key's own name,
// the slot name it is tracked under ("p.lo"), and the expression the
// literal assigned it.
type ObjectLocalKey struct {
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

// flatKeysOfLiteral reads an object literal's rows as a flat key list,
// or declines. Every row must be a plain `key: expression` assignment
// with an identifier key and a scalar-shaped value — an object-valued
// row would need slots of its own, which this flattening does not
// spell. Spreads, computed keys, shorthand rows, methods and accessors
// all decline: none of them names one key holding one scalar.
func flatKeysOfLiteral(literal *ast.Node, holder string) ([]ObjectLocalKey, bool) {
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
		value := Unwrapped(assignment.Initializer)
		if ast.IsObjectLiteralExpression(value) || ast.IsArrayLiteralExpression(value) {
			return nil, false
		}
		key := assignment.Name().Text()
		if _, already := seen[key]; already {
			return nil, false
		}
		seen[key] = struct{}{}
		keys = append(keys, ObjectLocalKey{
			Key:         key,
			SlotName:    holder + "." + key,
			Initializer: assignment.Initializer,
		})
	}
	if len(keys) == 0 {
		return nil, false
	}
	return keys, true
}

// usesAreAllDeclaredKeySteps scans a body for every occurrence of the
// name and answers whether each one is a `name.key` step on a key the
// literal declared. The declaration's own name position and the
// literal's own rows are not uses. A `delete p.k` reads the record as
// a mutable object, so it declines even though its operand IS a key
// step; every other whole-name occurrence — an alias, an argument, a
// return, an element access `p[e]` — declines for the same reason:
// after flattening there is no one value for it to denote.
func usesAreAllDeclaredKeySteps(body *ast.Node, declaration *ast.Node, name string, keys []ObjectLocalKey) bool {
	declared := map[string]struct{}{}
	for _, key := range keys {
		declared[key.Key] = struct{}{}
	}
	declarationName := declaration.AsVariableDeclaration().Name()
	ok := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		// `delete p.k` — the record's shape is observed, not just its
		// keys' values. Checked BEFORE the step rule below, which would
		// otherwise admit the operand as an ordinary read.
		if ast.IsDeleteExpression(node) {
			operand := Unwrapped(node.AsDeleteExpression().Expression)
			if holder, _, isStep := propertyStepOf(operand); isStep && holder == name {
				ok = false
				return true
			}
		}
		// `p.k` — a step; the holder and the key name are both consumed
		// here, so neither reaches the bare-identifier test below
		if holder, key, isStep := propertyStepOf(node); isStep && holder == name {
			if _, isDeclared := declared[key]; !isDeclared {
				ok = false // a key the literal never gave a slot
				return true
			}
			return false
		}
		// Every other occurrence of the bare name — an alias `q = p`, an
		// argument `f(p)`, `return p`, an element access `p[e]`, an
		// optional step `p?.k`, a nested `q.p` never reaching here as a
		// holder — is the WHOLE record in a position the flattening
		// cannot spell.
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

// propertyStepOf is a `holder.key` property access with both sides
// plain identifiers — the one shape SpelledNameOf tracks.
func propertyStepOf(node *ast.Node) (holder string, key string, ok bool) {
	if !ast.IsPropertyAccessExpression(node) {
		return "", "", false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", "", false
	}
	if !ast.IsIdentifier(access.Expression) || !ast.IsIdentifier(access.Name()) {
		return "", "", false
	}
	return access.Expression.Text(), access.Name().Text(), true
}

// ObjectLocalOf is the recognizer: a declaration `const p = { k: e, … }`
// whose every use in the body is a step on a declared key becomes the
// slot family "p.k"; anything else declines. Total-or-decline — the
// caller reads the flat keys or keeps its existing route.
func ObjectLocalOf(body *ast.Node, declaration *ast.Node) (ObjectLocal, bool) {
	literal := objectLiteralOfDeclaration(declaration)
	if literal == nil {
		return ObjectLocal{}, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return ObjectLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	keys, ok := flatKeysOfLiteral(literal, name)
	if !ok {
		return ObjectLocal{}, false
	}
	// a key's own initializer must not mention the record — `{ lo: 0, hi:
	// p.lo }` reads a slot that does not exist yet
	for _, key := range keys {
		if mentionsName(key.Initializer, name) {
			return ObjectLocal{}, false
		}
	}
	if !usesAreAllDeclaredKeySteps(body, declaration, name, keys) {
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

// ObjectLocalKeySort is a flattened key's sort, read the way LocalSort
// reads a scalar local's: a string literal is a string, anything else
// the lowering reads numerically.
func ObjectLocalKeySort(key ObjectLocalKey) BindingKind {
	if ast.IsStringLiteral(Unwrapped(key.Initializer)) {
		return BindingKindString
	}
	return BindingKindNumber
}

// ObjectLocalKeyTypeof is a flattened key's typeof evidence, from its
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

// ObjectLocalDeclarationAssignments is the object-literal declaration's
// lowering: one ordinary assignment per key, in literal order, each
// writing the key's own slot from its initializer through the shared
// RHS grammar. Declines unless EVERY key has a slot and every
// initializer lowers — a key left at its absent entry state would read
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
// statement declaring ONE object-literal local, read as its per-key
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
	keys, ok := flatKeysOfLiteral(literal, name)
	if !ok {
		return nil, false
	}
	return ObjectLocalDeclarationAssignments(context, ObjectLocal{Declaration: declaration, Name: name, Keys: keys})
}

// ObjectLocalsOf runs the recognizer over a body's collected locals and
// answers the ones that flatten, keyed by declaration. A local that
// declines is simply absent — the caller keeps treating it as a whole
// binding, which is today's behaviour.
func ObjectLocalsOf(body *ast.Node, locals []*ast.Node) map[*ast.Node]ObjectLocal {
	out := map[*ast.Node]ObjectLocal{}
	for _, declaration := range locals {
		if local, ok := ObjectLocalOf(body, declaration); ok {
			out[declaration] = local
		}
	}
	return out
}

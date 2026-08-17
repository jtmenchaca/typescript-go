// split from ir_object_slots.go — slot indexing and the declaration's per-leaf assignments

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

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
//
// The path read here admits ONE optional step adjacent to the root
// (`p?.a`, propertyPathAdmittingRootOptionalStep) — sound because the
// slot lookup below is the real gate: "p.a" only resolves to an index
// where the recognizer already admitted p as a flattened local (whose
// root is never null/undefined), so a root that is not such a local
// still fails the lookup exactly as before.
func PathSlotIndexOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if root, path, ok := propertyPathAdmittingRootOptionalStep(head); ok {
		return slotIndexOfName(context, root+"."+strings.Join(path, "."))
	}
	if root, leaf, ok := symbolKeyedLeafOf(context, head); ok {
		return slotIndexOfName(context, root+"."+leaf)
	}
	if spelling, ok := arrayElementMemberLeafOf(head); ok {
		return slotIndexOfName(context, spelling)
	}
	return 0, false
}

// arrayElementMemberLeafOf reads `xs[i].a` — a PropertyAccessExpression
// whose receiver is an ElementAccessExpression over a plain (non-optional)
// name — as the ELEM-LEAF spelling "xs.elem.a", the slot step 2
// (SummaryParameterEntriesIn's array-parameter branch, this agent's
// territory) lays out for a flattened array whose element is a record.
//
// The elem-leaf denotes the JOIN over every element the array can hold,
// the same weak-summary story the plain scalar elem slot already carries
// one member wide — `xs[i].a` for ANY `i` answers the same join, exactly
// as `xs[i]` alone already answers the join through the scalar elem slot.
// A deeper receiver (`xs[i].a.b`, a nested member two steps below the
// index) or a receiver that is not one plain ElementAccessExpression
// directly on an identifier both decline here — this reads the single
// step adjacent to an index, mirroring symbolKeyedLeafOf's own one-step
// shape.
//
// Only the SPELLING is built here; whether "xs.elem.a" resolves to an
// actual slot is slotIndexOfName's own question — a name whose array
// never expanded per-member (a scalar-elemented array, or an array whose
// element record only PARTIALLY matches this step) answers false there,
// exactly as an unresolved plain path already does.
func arrayElementMemberLeafOf(node *ast.Node) (spelling string, ok bool) {
	if !ast.IsPropertyAccessExpression(node) {
		return "", false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", false
	}
	if !ast.IsIdentifier(access.Name()) {
		return "", false
	}
	receiver := Unwrapped(access.Expression)
	if !ast.IsElementAccessExpression(receiver) {
		return "", false
	}
	element := receiver.AsElementAccessExpression()
	if element.QuestionDotToken != nil {
		return "", false
	}
	holder := Unwrapped(element.Expression)
	if !ast.IsIdentifier(holder) {
		return "", false
	}
	return holder.Text() + arrayElemSuffix + "." + access.Name().Text(), true
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
		out = append(out, AssignmentTarget{Target: target, Effect: asVarStateEffect(effect)})
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

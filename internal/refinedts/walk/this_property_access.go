// from evaluation/this_property_access.ts
//
// A `this.key` read wears its class's FIELD INVARIANT: the join of
// the initializer with every value the class's own text writes —
// unless the walk already holds a narrowed `this` object.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// ReadThisPropertyAccess reads a `this.key` access through the
// class's field invariants.
func ReadThisPropertyAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if !ast.IsPropertyAccessExpression(e) ||
		e.AsPropertyAccessExpression().Expression.Kind != ast.KindThisKeyword {
		return nil
	}
	return readThisFieldInvariant(ctx, env, e, e.AsPropertyAccessExpression().Name().Text())
}

// ReadThisSymbolKeyedAccess reads a `this[S]` access — the SYMBOL-KEYED
// spelling of a field — through the same class field invariants the
// dotted read uses. The key identity (ir_field_bundles.go) turns the
// stable symbol const into the field's own `#sym:` name, which is the
// name the invariant collection and InitialThisStateOf both stored it
// under, so one field answers one way however it is spelled.
//
// Nil for every other bracketed key: `this[k]` with a variable key,
// `this[Symbol.iterator]`, a key const bound to something other than a
// Symbol construction. Those name no field and keep whatever the
// element readers make of them.
func ReadThisSymbolKeyedAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if !ast.IsElementAccessExpression(e) ||
		Unwrapped(e.AsElementAccessExpression().Expression).Kind != ast.KindThisKeyword {
		return nil
	}
	name, isSymbolKey := SymbolKeyedFieldName(checkerOf(ctx), e)
	if !isSymbolKey {
		return nil
	}
	return readThisFieldInvariant(ctx, env, e, name)
}

// readThisFieldInvariant is the shared body of the two `this` field
// reads: the class's standing invariant for the named field, or the
// opaque floor where the field has none.
func readThisFieldInvariant(ctx *FlowContext, env Env, e *ast.Node, name string) *abstractdomain.AbstractValue {
	// a `this` field read wears its class's FIELD INVARIANT: the join of
	// the initializer with every value the class's own text writes —
	// complete because the field is private and `this` never escapes
	// the plain-key discipline (fields.ts declines otherwise)
	// …unless the walk HOLDS a narrowed `this` object: then the
	// general property read below answers through it, so a guard's
	// narrowing on the key outranks the standing invariant
	held, hasThis := env.Get("this")
	thisIsObject := hasThis && held.Kind == abstractdomain.KindObject
	if ctx.ThisWriteSink != nil || thisIsObject {
		return nil
	}
	var cursor *ast.Node = e
	for cursor.Parent != nil && !ast.IsClassDeclaration(cursor) && !ast.IsClassExpression(cursor) {
		cursor = cursor.Parent
	}
	if ast.IsClassDeclaration(cursor) || ast.IsClassExpression(cursor) {
		invariants := FieldInvariantsOf(ctx, cursor)
		if invariants != nil {
			if v, ok := invariants[name]; ok {
				return &v
			}
		}
		// a field with NO standing invariant holds whatever the
		// constructor and the object's life put there — outside this
		// walk's determination, so the type is everything the file
		// determines (the provenance survives destructuring)
		out := abstractdomain.Opaque
		return &out
	}
	return nil
}

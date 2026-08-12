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
	// a `this.key` read wears its class's FIELD INVARIANT: the join of
	// the initializer with every value the class's own text writes —
	// complete because the field is private and `this` never escapes
	// the plain-key discipline (fields.ts declines otherwise)
	// …unless the walk HOLDS a narrowed `this` object: then the
	// general property read below answers through it, so a guard's
	// narrowing on the key outranks the standing invariant
	held, hasThis := env["this"]
	thisIsObject := hasThis && held.Kind == abstractdomain.KindObject
	if !(ast.IsPropertyAccessExpression(e) &&
		e.AsPropertyAccessExpression().Expression.Kind == ast.KindThisKeyword &&
		ctx.ThisWriteSink == nil &&
		!thisIsObject) {
		return nil
	}
	var cursor *ast.Node = e
	for cursor.Parent != nil && !ast.IsClassDeclaration(cursor) && !ast.IsClassExpression(cursor) {
		cursor = cursor.Parent
	}
	if ast.IsClassDeclaration(cursor) || ast.IsClassExpression(cursor) {
		invariants := FieldInvariantsOf(ctx, cursor)
		name := e.AsPropertyAccessExpression().Name().Text()
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

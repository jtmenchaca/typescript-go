// split from ir_object_slots.go — the type node an arm's declaration spells

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

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

// inferredCalleeReturnDeclaredLeaves is the member family a call arm's
// callee promises when calleeReturnTypeNodeOf found no spelled return
// annotation — `function make() { return { lo: 1, hi: 2 } }` has no
// return node for that reader, but the checker still RESOLVES a return
// type from the body's own return statements.
//
// WHY THIS DOES NOT SYNTHESIZE A SECOND MEMBER READING. The resolved
// return type is used for exactly one question — which DECLARATION does
// its symbol name — and the answer, once found, is fed through the SAME
// interface/alias member rules declaredTypeMembersOf applies to a
// SPELLED reference (interfaceOwnMembersOf, heritageMembersOf,
// scalarMemberListOfIn: the identical private readers, called directly
// since the declaration is already in hand rather than a type-name node
// to resolve one). Nothing here re-derives a member list by walking the
// resolved TYPE's own properties — that would be the second reading the
// briefing warns against. A resolved type with no symbol, several
// declarations, or a shape the spelled path itself refuses (a generic
// instantiation, a union, a primitive) refuses here too, on exactly the
// same tests namedTypeMembersOf already states.
//
// THE THREE REFUSALS, each mirroring an existing one:
//
//   - no symbol at all (a primitive return, an unresolved call) — the
//     same "nothing to read syntax off" namedTypeMembersOf already
//     states for an unresolvable name;
//   - more than one declaration (a merged interface) — the same merge
//     refusal declaredTypeMembersOf states for a spelled reference;
//   - a GENERIC INSTANTIATION, read off the resolved type's own
//     ObjectFlags (ObjectFlagsReference marks "instantiated generic
//     class, interface, or tuple type" — checker/types.go's own
//     comment) — the same refusal a type-argument-bearing reference
//     already gets in namedTypeMembersOf, reached here through the
//     type's shape rather than the reference's syntax, since there is
//     no reference node for an inferred return to carry arguments on.
//
// A UNION return answers no symbol at all (checker.Type.Symbol is a
// per-declaration field; a synthesized union type carries none), so it
// refuses through the first test without a dedicated one.
//
// NO MEMO. resolvedTypeArmLeaves, which calls this, already re-resolves
// through ctx on every call with no cache of its own — a call arm's
// family is read fresh each time. Adding a memo here would diverge from
// that existing, unmemoized shape; there is nothing existing to keep
// consistent with a second (ctx-less) reading, unlike the parameter
// expansion's resolvedRecordMembers, which exists because a ctx-less
// seam already reads its memo. This path always requires a checker (a
// resolved return type does not exist without one), so no ctx-less
// caller can observe a stale answer.
func inferredCalleeReturnDeclaredLeaves(ctx *FlowContext, holder string, call *ast.Node) ([]recordParamMember, bool) {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil || call == nil {
		return nil, false
	}
	signature := ctx.P.Checker.GetResolvedSignature(call)
	if signature == nil {
		return nil, false
	}
	returnType := ctx.P.Checker.GetReturnTypeOfSignature(signature)
	if returnType == nil {
		return nil, false
	}
	// a generic instantiation's members depend on the type arguments
	// applied, which a return position spells nowhere — the same refusal
	// namedTypeMembersOf gives a reference carrying type arguments
	if (returnType.ObjectFlags() & checker.ObjectFlagsReference) != 0 {
		return nil, false
	}
	symbol := returnType.Symbol()
	if symbol == nil || len(symbol.Declarations) != 1 {
		return nil, false
	}
	declaration := symbol.Declarations[0]
	if declaration == nil {
		return nil, false
	}
	if ast.IsInterfaceDeclaration(declaration) {
		asInterface := declaration.AsInterfaceDeclaration()
		parameterNames := typeParameterNamesOf(asInterface.TypeParameters)
		own, ownOk := interfaceOwnMembersOf(holder, asInterface, parameterNames)
		if !ownOk {
			return nil, false
		}
		inherited, inheritedOk := heritageMembersOf(ctx, holder, asInterface, []*ast.Node{declaration})
		if !inheritedOk {
			return nil, false
		}
		merged := mergeShadowedMembers(inherited, own)
		if len(merged) == 0 {
			return nil, false
		}
		return merged, true
	}
	if ast.IsTypeAliasDeclaration(declaration) {
		asAlias := declaration.AsTypeAliasDeclaration()
		if asAlias.Type == nil || !ast.IsTypeLiteralNode(asAlias.Type) {
			// an intersection, a union, or another reference on the right
			// side is the SPELLED path's job (declaredTypeMembersOf) — this
			// reader only resolves the DECLARATION for an inferred return,
			// and only a type literal's members are read off syntax with no
			// further named-type resolution needed
			return nil, false
		}
		parameterNames := typeParameterNamesOf(asAlias.TypeParameters)
		return scalarMemberListOfIn(holder, asAlias.Type.AsTypeLiteralNode().Members.Nodes, parameterNames, true)
	}
	// a class, an enum, a module, an anonymous object type behind no
	// named declaration — none expand
	return nil, false
}

// split from ir_object_slots.go — the DECLARED-TYPE family source

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the NON-LITERAL declarations that flatten ───────────────────── */

// declaredLeavesOf turns a member list read off a DECLARATION — a
// declared record type's members, a constructor's this-field exits —
// into the leaf list a flattened local carries. Each leaf carries the
// member's whole PATH below the holder (a nested member reading answers
// multi-step paths like ["inner","deep"]) and its own declared sort
// rather than an initializer to read.
func declaredLeavesOf(holder string, members []recordParamMember) []ObjectLocalKey {
	keys := make([]ObjectLocalKey, 0, len(members))
	for _, member := range members {
		path := append([]string{}, member.Path...)
		keys = append(keys, ObjectLocalKey{
			Path:      path,
			Key:       member.Key,
			SlotName:  holder + "." + strings.Join(path, "."),
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

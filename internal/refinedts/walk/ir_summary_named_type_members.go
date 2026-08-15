// split from ir_summary_body.go — record parameters: named-type resolution

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// namedTypeMembersOf resolves a parameter annotation that is a TYPE
// REFERENCE to a PLAIN IDENTIFIER — `p: Bounds` — to the members it
// stands for, or (false). A UNION annotation written straight on the
// parameter is admitted here too, expanding to the members every arm
// declares (unionMembersOf's own argument); everything below is about
// the reference case.
//
// WHY THIS IS CHECK-INDEPENDENT. The identity of the answer is the
// resolved DECLARATION NODE, and the members are then read off that
// node's own SYNTAX by scalarMemberListOf — the very reading the inline
// type literal takes. Nothing here asks the checker what a type MEANS;
// the checker is used only to say which declaration a name is. So the
// only way two checks could answer differently is by resolving the name
// to a different declaration, and every shape where that is possible
// declines below:
//
//   - a name carrying TYPE ARGUMENTS (`Box<number>`) — the members would
//     depend on the arguments, which no entry vector spells;
//   - a symbol with NO declaration, or with declarations that are none of
//     the two admitted kinds — nothing to read syntax off;
//   - a symbol with MORE THAN ONE declaration — an interface declared
//     twice merges its members across declarations, and reading only the
//     first would build a member list the other declaration contradicts;
//   - a TYPE ALIAS whose right side is not a TYPE LITERAL (a union,
//     another reference, a mapped or conditional type) — the census reads
//     syntax, and only a literal spells its members;
//   - a CLASS — an instance carries methods, accessors, private state and
//     aliases the flattening cannot hold, and its fields are not promised
//     by the annotation alone.
//
// AN INTERFACE WITH HERITAGE (`interface Bounds extends Base { … }`)
// expands: the heritage clause's parent references resolve through the
// same symbolAt this function already uses, each parent's own members
// read under the SAME per-member rules, recursively (a parent's own
// heritage walks too), and the child's members SHADOW a parent's on a
// name collision — TypeScript's own rule for an inherited property a
// derived interface redeclares. Every existing decline still holds at
// every link of the chain: type arguments on the reference, a merged
// symbol (more than one declaration), a parent that is not a plain
// interface or an alias of a type literal, and any member the per-member
// rules refuse (a computed name, an initializer, a duplicate key — a
// member whose annotation does not SORT contributes unknown-sorted
// rather than refusing). A CYCLE in the chain (illegal TS, but not assumed
// pre-checked) declines rather than looping — the walk carries the
// declaration nodes already on its own path and refuses to re-enter one.
//
// TWO THINGS A LINK NO LONGER REFUSES, each argued where it is decided.
// A parent declaring NO DATA MEMBER contributes nothing rather than
// declining the child (declaredTypeMembersOf's position split), and a
// declaration carrying TYPE PARAMETERS is read member-wise rather than
// refused whole (scalarMemberListOfIn's invariance rule). Together they
// are what lets `interface WritableStream extends EventEmitter` read:
// the parent is an all-methods generic interface, so it contributes no
// leaf and needs no generic reasoning at all, and the child's own
// `writable: boolean` mentions no parameter of anything.
//
// A QUALIFIED NAME (`NodeJS.WritableStream`) resolves too. The two-level
// form is still one entity: the checker is asked which declaration the
// whole name is, exactly as it is asked for a plain identifier, and the
// members come off that declaration's own syntax. Nothing about a
// namespace qualifier makes the answer depend on what a call applied —
// that was the type-ARGUMENT objection, which stands unchanged. Refusing
// the qualifier refused a name, not an ambiguity.
//
// THE DECLARING FILE IS NOT A REASON TO REFUSE. A name whose declaration
// lives in a lib or .d.ts file reads through the same reader as a user
// one: each member the reader can spell is as true of a lib-declared
// record as of a user-declared one, and the member rules are what decide
// readability either way. This mirrors typereading's landed lib-record
// argument verbatim (host_type.go: "the declaring file is not the thing
// that made them expensive") — what lib shapes really cost is their
// SIZE, and the member budget below is what bounds that.
//
// The answer stays check-independent for the same reason the one-level
// reading is: the checker is asked only which declaration a name is, and
// the members come off those declarations' own syntax.
//
// A nil context, or one with no program or no checker, resolves nothing
// and declines — the nil-tolerance the ctx-less callers rely on: a
// lowering that runs without a checker keeps exactly today's behaviour
// rather than crashing.
func namedTypeMembersOf(ctx *FlowContext, holder string, typeNode *ast.Node) ([]recordParamMember, bool) {
	// a UNION written straight on the annotation
	// (`p: Type | DynamicModule`) is not a name to resolve; it expands to
	// the members EVERY arm declares, read at the ENTRY position so arms
	// sharing nothing decline here (unionMembersOf)
	if ast.IsUnionTypeNode(typeNode) {
		if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
			return nil, false
		}
		return unionMembersOf(ctx, holder, typeNode, nil, true)
	}
	if !ast.IsTypeReferenceNode(typeNode) {
		return nil, false
	}
	reference := typeNode.AsTypeReferenceNode()
	// type ARGUMENTS make the members depend on what was applied
	if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
		return nil, false
	}
	typeName := reference.TypeName
	// a plain identifier or a QUALIFIED name — both name one entity the
	// checker resolves to one declaration
	if !isResolvableTypeName(typeName) {
		return nil, false
	}
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, false
	}
	// the ENTRY position: a name expanding to no member declines here, so
	// the holder keeps its single whole-name slot
	return declaredTypeMembersOf(ctx, holder, typeName, nil, true)
}

// isResolvableTypeName says whether a type NAME is one the member
// reading resolves: a plain identifier (`Bounds`) or a QUALIFIED name
// (`NodeJS.WritableStream`).
//
// Both name ONE entity, and the reading asks the checker exactly one
// question about either — which declaration is this. A qualified name's
// left side is a namespace, which changes where the name is looked up
// and nothing about what the answer means; the checker performs that
// lookup itself when asked about the whole name. So the qualifier costs
// this reader nothing it was relying on.
func isResolvableTypeName(typeName *ast.Node) bool {
	if typeName == nil {
		return false
	}
	return ast.IsIdentifier(typeName) || ast.IsQualifiedName(typeName)
}

// declaredTypeMembersOf is the reading namedTypeMembersOf performs at
// every link of a heritage chain: resolve ONE type NAME to its single
// declaration and answer the members that declaration stands for,
// including whatever it inherits.
//
// The name may be plain or QUALIFIED, and its declaration may live in a
// LIB or .d.ts file — neither changes what is read. symbolAt answers
// which declaration the name is, and the members come off that
// declaration's own syntax under the same per-member rules a user
// interface takes. A lib record's readable members are as true as a user
// record's (typereading/host_type.go's landed argument); what bounds the
// cost is the member reading itself, never the declaring file.
//
// `visiting` holds the declaration nodes already on this walk's path. A
// name resolving back onto one of them is a cycle, which answers false
// rather than recursing forever.
//
// EMPTY MEANS TWO DIFFERENT THINGS DEPENDING ON POSITION, which is what
// `atEntry` splits. At the ENTRY — the annotation a parameter or a local
// is actually written with — a name expanding to no member is a name
// that bought nothing: the holder keeps its single whole-name slot,
// because laying out zero leaves for a value the body reads would leave
// every read of it unserved with no slot to point at. That is the
// decline-on-empty rule, and it stays.
//
// At a LINK — a heritage parent, an intersection side — empty means the
// side declares no DATA member, and that is a complete, true answer
// rather than a failure. The child's promise is its own members plus
// whatever the parent declares, so a parent declaring nothing adds no
// leaf and takes none away. Refusing the child over it would confuse
// "this side contributes nothing" with "this side is unreadable", which
// are opposite facts: the first is knowledge, the second is its absence.
// The unreadable case still declines the whole reading at every link
// (intersectionMembersOf's own argument) — this only stops a parent that
// is READ, and read to zero data members, from poisoning a child that
// spells members of its own.
func declaredTypeMembersOf(
	ctx *FlowContext,
	holder string,
	typeName *ast.Node,
	visiting []*ast.Node,
	atEntry bool,
) ([]recordParamMember, bool) {
	symbol := symbolAt(ctx.P.Checker, typeName)
	if symbol == nil || len(symbol.Declarations) != 1 {
		return nil, false
	}
	declaration := symbol.Declarations[0]
	if declaration == nil {
		return nil, false
	}
	for _, seen := range visiting {
		if seen == declaration {
			return nil, false
		}
	}
	if ast.IsInterfaceDeclaration(declaration) {
		asInterface := declaration.AsInterfaceDeclaration()
		parameterNames := typeParameterNamesOf(asInterface.TypeParameters)
		// a fresh path slice per link: sibling parents each recurse with
		// their own copy, so one branch's appends never land in another's.
		// The interface's OWN members read through the SAME widened
		// reader a heritage parent's members do — a checker to resolve a
		// nested member's stable-symbol name or named-type reference, and
		// this declaration on the visiting path so a nested member whose
		// reference resolves back onto THIS interface terminates.
		ownPath := make([]*ast.Node, len(visiting), len(visiting)+1)
		copy(ownPath, visiting)
		own, ownOk := interfaceOwnMembersWithCheckerIn(
			checkerOf(ctx), holder, asInterface, parameterNames, append(ownPath, declaration))
		if !ownOk {
			return nil, false
		}
		path := make([]*ast.Node, len(visiting), len(visiting)+1)
		copy(path, visiting)
		inherited, inheritedOk := heritageMembersOf(ctx, holder, asInterface, append(path, declaration))
		if !inheritedOk {
			return nil, false
		}
		merged := mergeShadowedMembers(inherited, own)
		if atEntry && len(merged) == 0 {
			return nil, false
		}
		return merged, true
	}
	if ast.IsTypeAliasDeclaration(declaration) {
		asAlias := declaration.AsTypeAliasDeclaration()
		parameterNames := typeParameterNamesOf(asAlias.TypeParameters)
		if asAlias.Type == nil {
			return nil, false
		}
		if ast.IsTypeLiteralNode(asAlias.Type) {
			// a fresh path slice per link, the declaration appended — a
			// NESTED member of this literal that resolves back onto this
			// same alias (`type A = { b: A }` behind another name, or a
			// two-alias cycle) is caught the way a heritage cycle already
			// is: the revisit finds `declaration` already on the path
			path := make([]*ast.Node, len(visiting), len(visiting)+1)
			copy(path, visiting)
			members, readable := scalarMemberListWithCheckerIn(
				checkerOf(ctx), holder, asAlias.Type.AsTypeLiteralNode().Members.Nodes, parameterNames, atEntry,
				append(path, declaration))
			return members, readable
		}
		if ast.IsIntersectionTypeNode(asAlias.Type) {
			// a PARAMETERIZED alias of an intersection would have to
			// substitute into each side before reading it, and the sides are
			// read as written — so the invariance argument does not reach
			// here and the alias declines
			if len(parameterNames) > 0 {
				return nil, false
			}
			path := make([]*ast.Node, len(visiting), len(visiting)+1)
			copy(path, visiting)
			return intersectionMembersOf(ctx, holder, asAlias.Type, append(path, declaration), atEntry)
		}
		if ast.IsUnionTypeNode(asAlias.Type) {
			// a PARAMETERIZED alias of a union would have to substitute into
			// each arm before reading it, and the arms are read as written —
			// so the invariance argument does not reach here and the alias
			// declines, exactly as the intersection alias does
			if len(parameterNames) > 0 {
				return nil, false
			}
			path := make([]*ast.Node, len(visiting), len(visiting)+1)
			copy(path, visiting)
			return unionMembersOf(ctx, holder, asAlias.Type, append(path, declaration), atEntry)
		}
		return nil, false
	}
	// a class, an enum, a module, a type parameter — none expand
	return nil, false
}

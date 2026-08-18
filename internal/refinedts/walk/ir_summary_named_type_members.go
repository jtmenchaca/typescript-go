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
// THE ONE ARM THAT IS NOT CHECK-INDEPENDENT is a reference carrying
// TYPE ARGUMENTS (`Box<number>`): its members exist only under the
// checker's instantiation, so that arm reads them from the checker's
// own answer and says so on its own doc
// (instantiatedReferenceMembersOf, ir_summary_instantiated_members.go).
// The layout/call-site agreement never depended on check-independence —
// recordParamMembersIn's memo is what pins one answer for both seams,
// for that arm exactly as for these.
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
// `visiting` carries the declaration nodes already on the member-reading
// path (nil at a fresh entry); it rides into declaredTypeMembersOf so a
// name reached again through an array or nested-member hop terminates.
func namedTypeMembersOf(ctx *FlowContext, holder string, typeNode *ast.Node, visiting []*ast.Node) ([]recordParamMember, bool) {
	// a UNION written straight on the annotation
	// (`p: Type | DynamicModule`) is not a name to resolve; it expands to
	// the members EVERY arm declares, read at the ENTRY position so arms
	// sharing nothing decline here (unionMembersOf)
	if ast.IsUnionTypeNode(typeNode) {
		if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
			return nil, false
		}
		return unionMembersOf(ctx, holder, typeNode, visiting, true)
	}
	if !ast.IsTypeReferenceNode(typeNode) {
		return nil, false
	}
	reference := typeNode.AsTypeReferenceNode()
	// `Pick<T, K>` spells a closed member list of its own (T's members,
	// filtered to the K keys) and keeps its dedicated reader — checked
	// before the general instantiated arm below so its per-key absence
	// fallback stays exactly as pinned
	if pickMembers, pickOk := pickMembersOf(ctx, holder, reference); pickOk {
		return pickMembers, true
	}
	// type ARGUMENTS make the members depend on what was applied — and
	// the annotation itself spells what was applied, so the reference
	// resolves through the checker's own INSTANTIATION
	// (instantiatedReferenceMembersOf, which states where its answer's
	// authority comes from and what it declines)
	if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
		return instantiatedReferenceMembersOf(ctx, holder, typeNode)
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
	return declaredTypeMembersOf(ctx, holder, typeName, visiting, true)
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
		if ast.IsTypeReferenceNode(asAlias.Type) {
			// an alias of a TYPE REFERENCE (`type Picked = Pick<Base, 'lo'>`,
			// a plain `type A = B`, or `type NumberBox = Box<number>`)
			// recurses through the SAME readings namedTypeMembersOf itself
			// takes at the top level — the Pick shape first, then the
			// general named-type resolution, then (new) the checker's own
			// INSTANTIATION for a target carrying type arguments — so an
			// alias costs nothing beyond one more link on the cycle-guarded
			// path. A parameterized alias of a reference would have to
			// substitute into the reference's own arguments first, which
			// this reader has no instantiation to do, so it declines exactly
			// as the intersection/union alias arms do above.
			if len(parameterNames) > 0 {
				return nil, false
			}
			path := make([]*ast.Node, len(visiting), len(visiting)+1)
			copy(path, visiting)
			path = append(path, declaration)
			innerReference := asAlias.Type.AsTypeReferenceNode()
			if pickMembers, pickOk := pickMembersOf(ctx, holder, innerReference); pickOk {
				return pickMembers, true
			}
			// the alias's own target carries type arguments
			// (`type NumberBox = Box<number>`): this reader has no
			// syntax-level substitution to perform, but the checker's own
			// instantiation already answers exactly this question — the SAME
			// checker-authority reading a directly-annotated `p: Box<number>`
			// takes (instantiatedReferenceMembersOf), asked of asAlias.Type
			// itself. That reader keeps its own class/array/budget guards,
			// and its own-body-uses guard answers true unconditionally here
			// (the type node's parent is this TypeAliasDeclaration, never a
			// ParameterDeclaration) — sound, because the ENTRY-level
			// whole-name fallback still guards the actual parameter this
			// alias was reached from: this call only ever contributes
			// members to a LARGER answer atEntry's own empty-check (or an
			// enclosing intersection/union reader) still gates.
			if innerReference.TypeArguments != nil && len(innerReference.TypeArguments.Nodes) > 0 {
				return instantiatedReferenceMembersOf(ctx, holder, asAlias.Type)
			}
			if !isResolvableTypeName(innerReference.TypeName) {
				return nil, false
			}
			return declaredTypeMembersOf(ctx, holder, innerReference.TypeName, path, atEntry)
		}
		return nil, false
	}
	// a class, an enum, a module, a type parameter — none expand
	return nil, false
}

// pickMembersOf reads a `Pick<T, K>` type reference as its own member
// list: one leaf per literal key K names, filtered out of T's own
// members.
//
// WHY THIS IS SOUND OVER-APPROXIMATION RATHER THAN AN EXACT READING.
// `Pick` promises the picked NAME is a member of the result — TypeScript
// itself defines `Pick<T, K> = { [P in K]: T[P] }`, so the result's
// member at P has exactly T's own member type at P. Two things can keep
// this reader from stating that type exactly: T's own member may not be
// one this census can sort (a nested shape, a mentioned type parameter),
// and — for the intersection-T case below — the reader may only be able
// to read SOME of T's arms. Either way the leaf still contributes,
// wearing unknown sort and MayBeAbsent: the unknown sort claims nothing
// about the VALUE, and MayBeAbsent is the weaker promise, so the leaf
// never claims more than Pick's own contract states. Where a picked
// key's member IS found in a readable arm, taking that arm's own
// Sort/TypeofTag/MayBeAbsent is exact when T is a single record, and a
// SOUND OVER-APPROXIMATION when T is an intersection with an unreadable
// arm: the true member type at that key is the INTERSECTION of every
// arm's own declared type at that key (a value satisfying `A & B` has
// both A's and B's member there), and a single arm's declared type is
// always a superset of that intersection — so claiming one arm's own
// reading can only be WEAKER than the truth, never stronger, which is
// the direction soundness requires.
//
// K must be a string-literal type or a union of string-literal types —
// anything else (a keyof, a generic K, a template literal) leaves the
// picked set unspellable and this reader declines, falling through to
// the ordinary type-argument refusal above it.
func pickMembersOf(ctx *FlowContext, holder string, reference *ast.TypeReferenceNode) ([]recordParamMember, bool) {
	if reference == nil || reference.TypeName == nil || !ast.IsIdentifier(reference.TypeName) ||
		reference.TypeName.Text() != "Pick" {
		return nil, false
	}
	if reference.TypeArguments == nil || len(reference.TypeArguments.Nodes) != 2 {
		return nil, false
	}
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, false
	}
	sourceArg := reference.TypeArguments.Nodes[0]
	keysArg := reference.TypeArguments.Nodes[1]
	keys, keysOk := stringLiteralKeysOf(keysArg)
	if !keysOk || len(keys) == 0 {
		return nil, false
	}
	gathered, gatheredOk := pickSourceMembersOf(ctx, holder, sourceArg)
	if !gatheredOk {
		return nil, false
	}
	byKey := map[string]recordParamMember{}
	for _, member := range gathered {
		if len(member.Path) != 1 {
			continue
		}
		byKey[member.Key] = member
	}
	out := make([]recordParamMember, 0, len(keys))
	for _, key := range keys {
		if member, found := byKey[key]; found {
			out = append(out, member)
			continue
		}
		// a picked key no gathered arm names: Pick still promises the NAME
		// (K is checked against T at the call site by the host checker
		// itself, so a key reaching here is one T does carry) — unknown
		// sort claims nothing about the value, and MayBeAbsent is the
		// weaker promise, so this leaf states no more than "this key may be
		// there, and if it is, nothing is claimed about its value"
		out = append(out, recordParamMember{
			Key:         key,
			Path:        []string{key},
			SlotName:    holder + "." + key,
			Sort:        BindingKindUnknown,
			TypeofTag:   TypeofTagNone,
			MayBeAbsent: true,
		})
	}
	return out, true
}

// stringLiteralKeysOf reads K in `Pick<T, K>` — a string-literal type
// (`'lo'`) or a union of string-literal types (`'lo' | 'hi'`) — as the
// plain key strings it names. Anything else (keyof, a generic, a
// template literal, a number literal) answers false: the picked set is
// not spellable as a fixed list of names.
func stringLiteralKeysOf(node *ast.Node) ([]string, bool) {
	if literal, ok := stringLiteralTextOf(node); ok {
		return []string{literal}, true
	}
	if !ast.IsUnionTypeNode(node) {
		return nil, false
	}
	arms := node.AsUnionTypeNode().Types
	if arms == nil || len(arms.Nodes) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(arms.Nodes))
	for _, arm := range arms.Nodes {
		text, ok := stringLiteralTextOf(arm)
		if !ok {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}

// stringLiteralTextOf reads a single string-literal TYPE node's own
// text — `ast.IsLiteralTypeNode` wrapping an `ast.IsStringLiteral`, the
// same test type_node_sets.go and type_node_aliases.go already use for
// a literal type's key text.
func stringLiteralTextOf(node *ast.Node) (string, bool) {
	if node == nil || !ast.IsLiteralTypeNode(node) {
		return "", false
	}
	literal := node.AsLiteralTypeNode().Literal
	if literal == nil || !ast.IsStringLiteral(literal) {
		return "", false
	}
	return literal.AsStringLiteral().Text, true
}

// pickSourceMembersOf gathers T's own members for `Pick<T, K>` — the
// members every route Pick's arg-1 position admits: a plain or
// qualified named type (through declaredTypeMembersOf, at a LINK
// position so a source contributing no data member still gathers as
// empty rather than refusing Pick outright), or an INTERSECTION, read
// arm BY ARM rather than through intersectionMembersOf's own all-sides-
// readable rule.
//
// AN INTERSECTION SOURCE READS DIFFERENTLY FOR Pick THAN FOR A PLAIN
// annotation. intersectionMembersOf refuses its whole answer the moment
// one side is unreadable, because a plain intersection annotation
// promises every member of every side and an unread side might hide
// members nothing here can name. Pick already answers UNKNOWN-SORTED,
// MayBeAbsent for a key no gathered arm names (pickMembersOf's own
// fallback) — so an unexpandable arm here costs only the SORT of
// whichever picked keys that arm alone would have named, never the
// list's own completeness the way it would for the general reader. This
// reader therefore SKIPS an unexpandable arm instead of refusing the
// whole Pick — gathering everything the readable arms state and letting
// pickMembersOf's per-key fallback cover the rest.
func pickSourceMembersOf(ctx *FlowContext, holder string, sourceArg *ast.Node) ([]recordParamMember, bool) {
	switch {
	case ast.IsIntersectionTypeNode(sourceArg):
		sides := sourceArg.AsIntersectionTypeNode().Types
		if sides == nil || len(sides.Nodes) == 0 {
			return nil, false
		}
		var gathered []recordParamMember
		for _, side := range sides.Nodes {
			var members []recordParamMember
			var readable bool
			switch {
			case ast.IsTypeLiteralNode(side):
				members, readable = scalarMemberListWithCheckerIn(
					checkerOf(ctx), holder, side.AsTypeLiteralNode().Members.Nodes, nil, false, nil)
			case ast.IsTypeReferenceNode(side):
				sideReference := side.AsTypeReferenceNode()
				if sideReference.TypeArguments == nil || len(sideReference.TypeArguments.Nodes) == 0 {
					if isResolvableTypeName(sideReference.TypeName) {
						members, readable = declaredTypeMembersOf(ctx, holder, sideReference.TypeName, nil, false)
					}
				}
			}
			if !readable {
				// an unexpandable arm is SKIPPED for Pick, not a whole refusal
				// (this function's own doc argues why) — a class arm, a
				// generic-applied arm, anything declaredTypeMembersOf itself
				// refuses
				continue
			}
			gathered = mergeShadowedMembers(gathered, members)
		}
		if len(gathered) == 0 {
			return nil, true
		}
		return gathered, true
	case ast.IsTypeReferenceNode(sourceArg):
		reference := sourceArg.AsTypeReferenceNode()
		if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
			return nil, false
		}
		if !isResolvableTypeName(reference.TypeName) {
			return nil, false
		}
		// an alias whose OWN target is an intersection (`type Combined =
		// HasLo & Unexpandable`) recurses into the SAME lenient, arm-by-arm
		// reading this function takes for an intersection written straight
		// on Pick's first argument — declaredTypeMembersOf's own
		// intersection arm delegates to the STRICT intersectionMembersOf,
		// which refuses the whole answer over one unreadable arm; Pick's
		// looser contract (a key no gathered arm names still contributes
		// unknown-sorted, MayBeAbsent) tolerates that arm being skipped
		// instead, so the source-position reading must not go through the
		// strict path just because the intersection sits behind a name.
		if aliasTarget, isAlias := aliasedTypeNode(ctx, reference.TypeName); isAlias {
			if ast.IsIntersectionTypeNode(aliasTarget) {
				return pickSourceMembersOf(ctx, holder, aliasTarget)
			}
		}
		return declaredTypeMembersOf(ctx, holder, reference.TypeName, nil, false)
	case ast.IsTypeLiteralNode(sourceArg):
		return scalarMemberListWithCheckerIn(
			checkerOf(ctx), holder, sourceArg.AsTypeLiteralNode().Members.Nodes, nil, false, nil)
	}
	return nil, false
}

// aliasedTypeNode resolves a type NAME to its own right-hand-side syntax
// node where the name is a NON-GENERIC type alias — `type Combined = X`
// answers X's node for the name "Combined". Anything else (an
// interface, a class, a generic alias, an unresolvable name) answers
// (nil, false): this reader only exists so pickSourceMembersOf's
// type-reference case can look ONE layer through a plain alias to find
// an intersection hiding behind it, never to re-derive the general
// named-type resolution declaredTypeMembersOf already owns.
func aliasedTypeNode(ctx *FlowContext, typeName *ast.Node) (*ast.Node, bool) {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, false
	}
	symbol := symbolAt(ctx.P.Checker, typeName)
	if symbol == nil || len(symbol.Declarations) != 1 {
		return nil, false
	}
	declaration := symbol.Declarations[0]
	if declaration == nil || !ast.IsTypeAliasDeclaration(declaration) {
		return nil, false
	}
	asAlias := declaration.AsTypeAliasDeclaration()
	if asAlias.TypeParameters != nil && len(asAlias.TypeParameters.Nodes) > 0 {
		return nil, false
	}
	if asAlias.Type == nil {
		return nil, false
	}
	return asAlias.Type, true
}

// A whole function body lowered for the SUMMARY compiler.
//
// The whole-body route (kernel_summaries.go) lowers per call, keyed by
// the sorts the arguments supplied — a different sort vector is a
// different lowering. A SUMMARY quantifies over all entries instead, so
// it is compiled once per declaration and the sorts cannot come from
// any call: they are read from the declaration's own parameter type
// annotations, which every entry shares.
//
// What this adds beyond the per-call lowering is the callee TABLE: a
// body whose call sites lower to IrStatementCall carries one blob per
// callee, and the table rides out beside the statements so the compile
// can splice each callee's already-built summary.
//
// The lowering is also where a body's OUTCOME is recorded — complete,
// porous, or declined naming the construct it refused
// (summary_outcome.go holds the store). This is the one place the fate
// of a body is known, so it is the one place that reports it.

package walk

import (
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── record parameters ───────────────────────────────────────────── */

// recordParamMember is one member of a parameter's RECORD annotation —
// an inline type literal, or the interface or type alias a named
// annotation resolves to: the key it is spelled under, the slot name the
// body reads it by ("p.lo"), and the sort and typeof evidence its OWN
// annotation states.
type recordParamMember struct {
	Key       string
	SlotName  string
	Sort      BindingKind
	TypeofTag TypeofTag
	// MayBeAbsent: the member was declared OPTIONAL (`lo?: number`), so
	// its value is the declared sort OR the absent value. The sort
	// vocabulary has three words — number, string, unknown — and none of
	// them spells "number-or-absent", so the absence rides the ENTRY
	// STATE instead: a maybe-wrapped abstract value crosses the wire as
	// its inner set with Absent set (StateOfKnown's KindPossiblyUndefined
	// arm, kernel_delegation.go), which is exactly the state a call fills
	// this entry with. The slot keeps the inner sort; the state carries
	// the absence. That is the same division declared_value.go makes for
	// an optional object KEY — the key's reads wear PossiblyUndefined
	// around the sort-bearing value, never a third sort.
	MayBeAbsent bool
}

// scalarMemberListOf reads a list of TYPE ELEMENTS as the member list a
// record parameter expands to, spelled under `holder` ("p.lo"/"p.hi").
//
// Every element must be a PROPERTY SIGNATURE with a plain identifier
// name and no initializer. A computed name, an initializer, an index
// signature, a duplicate key, or an EMPTY list answers false and the
// parameter keeps its single whole-name slot.
//
// A MEMBER'S ANNOTATION NO LONGER HAS TO BE A SCALAR KEYWORD. A property
// whose annotation this reading cannot sort — a type reference
// (`module: Type<any>`), an array (`imports?: Array<…>`), a function
// type, a nested literal, a union — CONTRIBUTES, wearing the unknown
// sort. It is the contract split ClassFieldsOf already documents
// (ir_field_bundles.go): the census reports the shape it found, and the
// consumer decides what an unknown-sorted slot is worth.
//
// THE ACCOUNTING ARGUMENT, which is why this is not a weakening. Every
// reason the total-or-decline rule existed is preserved by the
// unknown-sorted leaf rather than by the refusal:
//
//   - the member is NAMED, so the uses discipline still accounts for it.
//     usesAreAllDeclaredKeySteps keys on declaredLeafPaths — the leaf
//     PATHS — and recordParameterUseOf on the member KEYS, neither on a
//     sort. A body reading `p.module` reads a declared one-step path
//     either way, so the "these are the members, all of them" promise
//     every consumer reads a `true` answer as is exactly as true as it
//     was: the list still names every declared property. What refusing
//     did was make the list UNAVAILABLE, never more complete.
//   - the entry TOP-fills. thisEntryState (kernel_summaries.go) fills a
//     member entry from the argument's own field by KEY, and TOPs where
//     the argument is not a known object, does not name the member, or
//     holds a value the wire cannot spell. An unknown-sorted member is
//     the third of those, which the fill already handles — it is the
//     established parameter discipline: declared members, unknown values.
//   - no test admits an unknown-sorted slot. Every read and test gate is
//     a POSITIVE sort test — TestOf admits only IrTestDefined outside
//     `== BindingKindNumber` / `== BindingKindString`, NumberIndexOf and
//     EffectOf's numberSlot admit only the number sort. So an
//     unknown-sorted leaf answers definedness and nothing else, which is
//     precisely what is known about it.
//
// What the refusal actually cost was every SCALAR SIBLING of the
// unreadable member: one `module: Type<any>` killed the expansion of
// `global?: boolean` beside it. That is the same trade the optional-member
// and method-signature rules above already rejected, for the same reason —
// a fact one member cannot state is not a reason to stop stating the
// others.
//
// A CALL or CONSTRUCT SIGNATURE is SKIPPED, exactly as a method signature
// is and for the same reason: it resolves as a call, names no slot, and
// the uses discipline refuses any body that reads a name no leaf holds.
// `interface Type<T> { new (...args: any[]): T }` is the shape this
// exists for — its one construct signature leaves the list contributing
// nothing rather than unreadable.
//
// AN OPTIONAL MEMBER (`lo?: number`) CONTRIBUTES ITS LEAF, wearing
// MayBeAbsent. The expansion contract is that every member is PROMISED
// to every entry, and an optional member is promised — as possibly
// absent. That is a WEAKER promise, not a missing one: the annotation
// still says this key is a number where it is there at all, and still
// says no other key is declared. What the earlier refusal actually
// objected to was the SORT — no word in the three-word sort vocabulary
// spells "number-or-absent" — and the absence does not have to live in
// the sort. It lives in the entry STATE, which is what entry states are
// for: a maybe-wrapped value crosses as its inner set with Absent set
// (StateOfKnown, kernel_delegation.go), so the slot stays number-sorted
// and the caller's own knowledge of the key — present, absent, or
// unknown — is what fills it. A caller that knows nothing fills TOP,
// which the entry quantifier already covers. Refusing instead cost the
// whole parameter its expansion over a fact the state already carries;
// recharts' props records are optional members almost throughout.
//
// A METHOD SIGNATURE (`writeHead?(...): void`) IS SKIPPED — it
// contributes no leaf and kills nothing. A method is not a slot; it
// resolves as a CALL, which is the established rule (ir_field_bundles
// .go's class-field census refuses methods for the same reason). The
// record contract tolerates members no leaf names exactly as a
// Complete:false object does, and the USES discipline is what keeps
// that honest: a body that actually calls `res.write(…)` reads a
// one-step path whose name is in no leaf, which
// usesAreAllDeclaredKeySteps (ir_object_slots.go) and
// recordParameterUseOf both refuse — so a skipped method's absence can
// never let a call read as an accounted-for leaf use. Skipping only
// stops a method from poisoning a list of readable scalar members.
//
// Each member's sort and typeof read through declaredParamSort's own
// reading, member-wise: number and boolean ride the number sort (their
// typeof differs), string rides the string sort.
//
// This is the ONE member reading. Both the type-literal case and the
// resolved named-type case go through it, so a `{ lo: number }` written
// inline and the same members written behind an `interface` expand to
// byte-identical member lists — they must, because the layout and the
// call sites both build their entry vectors from this answer.
func scalarMemberListOf(holder string, members []*ast.Node) ([]recordParamMember, bool) {
	return scalarMemberListOfIn(holder, members, nil, true)
}

// scalarMemberListOfIn is the member reading scalarMemberListOf performs,
// told two things about the position it is reading in.
//
// `parameterNames` are the TYPE PARAMETERS of the declaration these
// members were written on, and the rule they carry is an INVARIANCE
// argument: a member whose own annotation never mentions a parameter of
// its declaration reads the same at every instantiation. `writable:
// boolean` on `interface S<T>` is boolean for S<A> and S<B> alike —
// nothing a caller applies can change it, because T does not appear in
// it. So the parameterization is irrelevant to what that member's leaf
// is, and the member reads exactly as written. That is why the whole
// declaration no longer has to be refused for carrying parameters: the
// parameters are a property of the DECLARATION, and soundness is a
// question about each MEMBER.
//
// A member that DOES mention a parameter is the case the argument does
// not cover, and it CONTRIBUTES UNKNOWN-SORTED rather than being skipped.
// Its true annotation is whatever the instantiation substituted, which
// this reader has no instantiation to substitute from; a default or a
// constraint would only give one reading among many, and reading `T` as
// its default would claim of every caller what is true of the ones that
// applied nothing. But "I cannot say what sort this member is" is
// exactly what the unknown sort says, and the member is DECLARED — the
// name is promised to every instantiation whatever `T` turns out to be,
// since a type argument substitutes a member's TYPE and never removes
// the member.
//
// So the two readings are made consistent by the same accounting
// argument the scalar widening above makes: contribute-unknown beats
// skip for a DECLARED name. A skipped member leaves its name in no leaf,
// which costs the body every read of it (recordParameterUseOf refuses a
// path no leaf holds); a contributed one names the leaf, TOP-fills its
// entry, and admits only the definedness test. The skip claimed less
// and served less. Nothing about the parameterization makes the NAME
// less promised, and the sort is where the ignorance belongs.
//
// A METHOD stays skipped, and the difference is not an inconsistency: a
// method names no slot at all, being a call rather than a value, so
// there is no leaf whose sort could carry the ignorance.
//
// The scan for a mention is SYNTACTIC — every identifier in the member's
// annotation, at any depth, tested against the declaration's parameter
// names. A name that shadows a parameter inside the annotation would be
// read as a mention and the member skipped, which is the safe direction.
//
// `atEntry` is the position split declaredTypeMembersOf states: at an
// entry a list yielding no leaf declines, at a LINK it contributes an
// empty list. An all-methods interface is the shape this exists for.
func scalarMemberListOfIn(
	holder string,
	members []*ast.Node,
	parameterNames map[string]struct{},
	atEntry bool,
) ([]recordParamMember, bool) {
	if len(members) == 0 {
		if atEntry {
			return nil, false
		}
		return nil, true
	}
	seen := map[string]struct{}{}
	out := make([]recordParamMember, 0, len(members))
	for _, member := range members {
		// a METHOD, a CALL signature, and a CONSTRUCT signature all
		// resolve as calls, never as slots: they name no leaf and refuse
		// none, and the uses discipline refuses any body that actually
		// calls one
		if ast.IsMethodSignatureDeclaration(member) ||
			ast.IsCallSignatureDeclaration(member) ||
			ast.IsConstructSignatureDeclaration(member) {
			continue
		}
		if !ast.IsPropertySignatureDeclaration(member) {
			return nil, false
		}
		signature := member.AsPropertySignatureDeclaration()
		// an INITIALIZER on a type element is not a value any entry
		// carries — no route applies it, so the list declines
		if signature.Initializer != nil {
			return nil, false
		}
		// `lo?: number` is the member promised as possibly absent: the
		// leaf is contributed sorted by the inner annotation, and the
		// absence rides the entry state
		mayBeAbsent := signature.PostfixToken != nil
		if signature.Type == nil || signature.Name() == nil || !ast.IsIdentifier(signature.Name()) {
			return nil, false
		}
		// the member's sort, or UNKNOWN where this reading cannot state
		// one. Two cases land on unknown, argued above: an annotation that
		// is not a scalar keyword, and an annotation MENTIONING a type
		// parameter of its own declaration (whose sort varies by
		// instantiation, which is exactly what unknown says). Either way
		// the member is declared, so it contributes its named leaf.
		sort, tag := BindingKindUnknown, TypeofTagNone
		if !mentionsTypeParameter(signature.Type, parameterNames) {
			switch signature.Type.Kind {
			case ast.KindNumberKeyword:
				sort, tag = BindingKindNumber, TypeofTagNumber
			case ast.KindBooleanKeyword:
				// booleans ride the number sort — declaredParamSort's own rule
				sort, tag = BindingKindNumber, TypeofTagBoolean
			case ast.KindStringKeyword:
				sort, tag = BindingKindString, TypeofTagString
			}
		}
		key := signature.Name().Text()
		if _, already := seen[key]; already {
			return nil, false
		}
		seen[key] = struct{}{}
		out = append(out, recordParamMember{
			Key:         key,
			SlotName:    holder + "." + key,
			Sort:        sort,
			TypeofTag:   tag,
			MayBeAbsent: mayBeAbsent,
		})
	}
	// a list contributing no leaf — all methods, all call or construct
	// signatures, or empty. At an ENTRY the holder keeps its whole-name
	// slot; at a LINK the side contributes nothing and the child reads on.
	// (A parameter-mentioning member no longer lands here: it contributes
	// its named leaf unknown-sorted.)
	if len(out) == 0 {
		if atEntry {
			return nil, false
		}
		return nil, true
	}
	return out, true
}

// typeParameterNamesOf is the set of names a declaration's TYPE
// PARAMETERS bind — the names a member's annotation is scanned against.
// A declaration with no parameter list answers the empty set, under
// which every member reads as written.
func typeParameterNamesOf(list *ast.NodeList) map[string]struct{} {
	if list == nil || len(list.Nodes) == 0 {
		return nil
	}
	names := map[string]struct{}{}
	for _, parameter := range list.Nodes {
		if !ast.IsTypeParameterDeclaration(parameter) {
			continue
		}
		name := parameter.AsTypeParameterDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) {
			continue
		}
		names[name.Text()] = struct{}{}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

// mentionsTypeParameter says whether an annotation names any of the
// declaration's type parameters, anywhere inside it.
//
// The walk is over every identifier the annotation contains, at any
// depth, because a parameter can appear anywhere a type can: `T`,
// `T[]`, `Map<string, T>`, `{ x: T }`, `T extends U ? A : B`. Matching
// on the NAME is what makes this a sound over-approximation — a
// different entity that happens to share a parameter's spelling reads as
// a mention and costs that member its leaf, never the reverse.
//
// A nil annotation is not a mention; the member reading below refuses it
// on its own ground.
func mentionsTypeParameter(annotation *ast.Node, parameterNames map[string]struct{}) bool {
	if annotation == nil || len(parameterNames) == 0 {
		return false
	}
	mentions := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if mentions {
			return true
		}
		if ast.IsIdentifier(node) {
			if _, named := parameterNames[node.Text()]; named {
				mentions = true
				return true
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(annotation)
	return mentions
}

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
		own, ownOk := interfaceOwnMembersOf(holder, asInterface, parameterNames)
		if !ownOk {
			return nil, false
		}
		// a fresh path slice per link: sibling parents each recurse with
		// their own copy, so one branch's appends never land in another's
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
			members, readable := scalarMemberListOfIn(
				holder, asAlias.Type.AsTypeLiteralNode().Members.Nodes, parameterNames, atEntry)
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

// intersectionMembersOf reads an INTERSECTION annotation
// (`type W = NodeJS.WritableStream & WriteHeaders`) as one member list.
//
// WHAT AN INTERSECTION MEANS HERE. A value of `A & B` is a value of A and
// a value of B at once, so it carries EVERY member of A and every member
// of B. That is the whole rule, and it is why the sides union rather than
// meet: each side's members are individually promised to every value the
// annotation admits, which is exactly the promise a record expansion
// needs before a member may become a slot.
//
// Each intersectee is read by the SAME member reader every other route
// uses — a type literal off its own syntax, a named interface through the
// heritage-walking route, a nested alias recursively, cycle-guarded by
// the `visiting` path this walk already carries. So an intersection of
// two interfaces and the one interface spelling the same members expand
// identically.
//
// WHERE TWO SIDES NAME ONE MEMBER, mergeShadowedMembers keeps the later
// side's reading at the earlier side's position — the same discipline the
// heritage walk uses for a redeclared inherited property, and the same
// reason: the slot vector must not move because a second side restated a
// member. The two sides agreeing is the ordinary case; where they
// disagree about a member's SORT, an intersection member's true type is
// the two member types' own intersection, which this reader has no form
// for. It does not guess: a member the two sides sort differently is a
// member no single slot can stand for, and the WHOLE expansion declines
// rather than picking one side's sort. Picking would be the unsound
// move — a slot sorted number for a member some callers pass a string.
//
// WHERE THE SIDES DISAGREE ABOUT ABSENCE, the REQUIRED reading wins, and
// this one has an answer where the sort does not. A value of `A & B`
// satisfies both, so a member A declares required and B declares
// optional is required of every such value: the intersection of "number"
// and "number-or-absent" is "number". Taking the required reading is
// therefore the true one, not a guess — and it is also the conservative
// direction, since it never claims a value may be absent that must be
// there.
//
// AN UNREADABLE SIDE DECLINES THE WHOLE READING, and this is the part
// worth stating rather than assuming. The tempting argument runs: an
// intersection can only ADD members, so the readable side's members are
// still promised to every value, and expanding on them alone is sound.
// The promise half of that is true. What it misses is that every consumer
// reads a `true` answer as "these are the members, all of them" — which
// is why an unreadable SIDE is different in kind from an unreadable
// MEMBER: a member whose annotation does not sort is still NAMED, so the
// list stays complete and only the sort goes unknown, while a side this
// reader cannot read hides members it cannot even name. A record local's leaves
// (ir_object_slots.go) are laid out as the value's WHOLE flattening, and
// the uses that would observe the value as a value are then refused on
// the ground that every leaf is accounted for. Answering a partial list
// as if it were whole would let a use of an unnamed member read as
// accounted-for when nothing holds it. The honest reading of a side this
// reader cannot read is that it does not know what that side declares —
// not that it declares nothing — so the answer is false and the holder
// keeps its whole-name slot. A side becoming readable later widens the
// answer; nothing has to be taken back.
func intersectionMembersOf(
	ctx *FlowContext,
	holder string,
	intersection *ast.Node,
	visiting []*ast.Node,
	atEntry bool,
) ([]recordParamMember, bool) {
	sides := intersection.AsIntersectionTypeNode().Types
	if sides == nil || len(sides.Nodes) == 0 {
		return nil, false
	}
	var merged []recordParamMember
	sortOfKey := map[string]BindingKind{}
	tagOfKey := map[string]TypeofTag{}
	// whether any side so far declared this member REQUIRED
	requiredKey := map[string]bool{}
	for _, side := range sides.Nodes {
		var members []recordParamMember
		var readable bool
		switch {
		case ast.IsTypeLiteralNode(side):
			// a SIDE is a link: a literal side of nothing but methods
			// contributes no member and declines nothing
			members, readable = scalarMemberListOfIn(
				holder, side.AsTypeLiteralNode().Members.Nodes, nil, false)
		case ast.IsTypeReferenceNode(side):
			reference := side.AsTypeReferenceNode()
			// the entry reference's own refusal, at every side: type
			// arguments make the members depend on what was applied. A
			// QUALIFIED side name resolves like a plain one — it names one
			// entity, which is what `NodeJS.WritableStream & WriteHeaders`
			// needs from this reader.
			if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
				return nil, false
			}
			if !isResolvableTypeName(reference.TypeName) {
				return nil, false
			}
			members, readable = declaredTypeMembersOf(ctx, holder, reference.TypeName, visiting, false)
		}
		if !readable {
			return nil, false
		}
		// a member both sides name must be sorted the same on both, or no
		// one slot stands for it. Absence is noted per key and settled
		// once over the whole answer below.
		for _, member := range members {
			sort, named := sortOfKey[member.Key]
			if named && (sort != member.Sort || tagOfKey[member.Key] != member.TypeofTag) {
				return nil, false
			}
			sortOfKey[member.Key] = member.Sort
			tagOfKey[member.Key] = member.TypeofTag
			requiredKey[member.Key] = requiredKey[member.Key] || !member.MayBeAbsent
		}
		merged = mergeShadowedMembers(merged, members)
	}
	// an intersection every side of which contributed no data member: at
	// an entry the holder keeps its whole-name slot, at a link the
	// intersection itself contributes nothing
	if atEntry && len(merged) == 0 {
		return nil, false
	}
	// the required reading applied once over the whole answer, so a side
	// declaring a member required settles it whether it came before or
	// after the side that declared it optional. Written into a COPY: a
	// single-side merge hands back the side's own slice, which another
	// reading may hold.
	settled := make([]recordParamMember, len(merged))
	copy(settled, merged)
	for at := range settled {
		if requiredKey[settled[at].Key] {
			settled[at].MayBeAbsent = false
		}
	}
	return settled, true
}

// unionMembersOf reads a UNION annotation (`p: A | B | C`) as the members
// EVERY arm declares.
//
// WHAT A UNION MEANS HERE, and why it is the mirror of the intersection
// above. A value of `A | B` is a value of A OR a value of B, so the only
// members the annotation promises to every such value are the ones EVERY
// arm declares — the arms' member lists INTERSECT rather than union. A
// member only one arm spells is a member the value may simply not have,
// and giving it a slot would name a leaf for a value that never carries
// it. A member every arm spells is there whichever arm the value is, and
// that is exactly the promise a record expansion needs before a member
// may become a slot.
//
// Each ARM is read by the SAME member reader every other route uses — a
// type literal off its own syntax, a named interface through the
// heritage-walking route, a nested alias recursively, cycle-guarded by
// the `visiting` path this walk already carries. So a union of two
// interfaces and the one interface spelling the members they share expand
// identically.
//
// AN ARM'S SORT MUST AGREE WITH THE OTHERS', or the whole expansion
// declines — the same rule intersectionMembersOf argues at its own merge,
// for the same reason. A member the arms sort differently is a member no
// single slot can stand for, and picking one arm's sort would be the
// unsound move: a slot sorted number for a member the value carries as a
// string whenever it is the other arm. The reader does not guess.
//
// WHERE THE ARMS DISAGREE ABOUT ABSENCE, THE WEAKER PROMISE WINS — and
// this is the exact opposite of the intersection's rule, because the
// connective is. A value of `A | B` satisfies ONE of them, so a member A
// declares required and B declares optional is only optional of that
// value: the value may be the arm where the member is optional, and
// nothing in the annotation says which arm it is. So the union of
// "number" and "number-or-absent" is "number-or-absent", and the member
// is carried MayBeAbsent as soon as ANY arm marks it. Taking the required
// reading would claim a value must carry a member the B arm lets it
// omit — the unsound direction. Carrying the absence is the true reading
// and also the conservative one, since the absence rides the entry state
// and never claims a value is there.
//
// AN UNREADABLE ARM DECLINES THE WHOLE EXPANSION, verbatim the argument
// intersectionMembersOf makes for an unreadable side. The tempting move
// is to intersect over the readable arms alone and say the result is
// still promised — but every consumer reads a `true` answer as "these are
// the members, all of them", and an unreadable ARM hides members it
// cannot name (unlike an unsortable MEMBER, which is named and merely
// unknown-sorted). An arm this reader cannot read is an arm whose members it
// does not KNOW — not one that declares nothing — and intersecting
// against an unknown list would keep members that arm may well not have.
// The honest answer is false, and the holder keeps its whole-name slot. An
// arm becoming readable later widens the answer; nothing has to be taken
// back.
//
// AN EMPTY INTERSECTION DECLINES AT AN ENTRY. Arms sharing no member
// expand to nothing, and expanding to nothing is not expanding: the
// holder keeps its single whole-name slot, exactly as the entry rule
// states everywhere else. At a LINK the empty answer is the true one —
// the union side contributes no member and takes none away.
//
// A CLASS arm is read by whatever declaredTypeMembersOf makes of it, and
// that reader refuses a class outright. So a union with a class arm
// declines whole, which is the honest reading: this walk has no member
// list for that arm at all.
func unionMembersOf(
	ctx *FlowContext,
	holder string,
	union *ast.Node,
	visiting []*ast.Node,
	atEntry bool,
) ([]recordParamMember, bool) {
	arms := union.AsUnionTypeNode().Types
	if arms == nil || len(arms.Nodes) == 0 {
		return nil, false
	}
	// the running intersection: the first arm seeds it, every later arm
	// keeps only what it also declares
	var common []recordParamMember
	// whether any arm so far declared this member OPTIONAL — the weaker
	// promise, settled once over the whole answer below
	absentKey := map[string]bool{}
	for at, arm := range arms.Nodes {
		members, readable := unionArmMembersOf(ctx, holder, arm, visiting)
		if !readable {
			return nil, false
		}
		byKey := map[string]recordParamMember{}
		for _, member := range members {
			// an arm spelling one member twice is a list the scalar reader
			// already refuses, so a collision here cannot happen; the map is
			// the arm's own lookup for the intersection step
			byKey[member.Key] = member
			absentKey[member.Key] = absentKey[member.Key] || member.MayBeAbsent
		}
		if at == 0 {
			// the first arm's list, copied: the intersection is narrowed in
			// place below and the arm's own slice may be held by another
			// reading
			common = make([]recordParamMember, len(members))
			copy(common, members)
			continue
		}
		kept := make([]recordParamMember, 0, len(common))
		for _, member := range common {
			armMember, declared := byKey[member.Key]
			if !declared {
				// a member this arm does not declare is a member the value may
				// not have — it leaves the intersection
				continue
			}
			// a member the arms sort differently is a member no one slot
			// stands for
			if armMember.Sort != member.Sort || armMember.TypeofTag != member.TypeofTag {
				return nil, false
			}
			kept = append(kept, member)
		}
		common = kept
		if len(common) == 0 {
			break
		}
	}
	// arms sharing no member: at an entry the holder keeps its whole-name
	// slot, at a link the union contributes nothing
	if atEntry && len(common) == 0 {
		return nil, false
	}
	// the weaker promise applied once over the whole answer, so an arm
	// declaring a member optional settles it whether it came before or
	// after the arm that declared it required
	for index := range common {
		if absentKey[common[index].Key] {
			common[index].MayBeAbsent = true
		}
	}
	return common, true
}

// unionArmMembersOf reads ONE arm of a union as a member list.
//
// An arm is a LINK, never an entry: an arm that declares no data member
// is READ, and reading it to nothing is a true answer about that arm —
// it just leaves the running intersection empty, which the caller settles
// under its own entry rule. The arm forms admitted are the ones every
// other route admits: an inline type literal off its own syntax, and a
// type reference resolved through declaredTypeMembersOf under the entry
// reference's own refusals (type arguments make the members depend on
// what was applied; a name that is neither plain nor qualified names no
// one entity). Every other arm form — a literal type, a keyword, an
// array, a function type — is an arm this reader cannot read, and it
// declines the whole union.
func unionArmMembersOf(
	ctx *FlowContext,
	holder string,
	arm *ast.Node,
	visiting []*ast.Node,
) ([]recordParamMember, bool) {
	switch {
	case ast.IsTypeLiteralNode(arm):
		return scalarMemberListOfIn(holder, arm.AsTypeLiteralNode().Members.Nodes, nil, false)
	case ast.IsTypeReferenceNode(arm):
		reference := arm.AsTypeReferenceNode()
		if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
			return nil, false
		}
		if !isResolvableTypeName(reference.TypeName) {
			return nil, false
		}
		return declaredTypeMembersOf(ctx, holder, reference.TypeName, visiting, false)
	}
	return nil, false
}

// interfaceOwnMembersOf reads an interface's OWN member list under the
// scalar rules, allowing the EMPTY list an entry reading refuses:
// `interface Bounds extends Base {}` declares nothing itself and takes
// every member from its parent. The caller applies the entry rule to the
// MERGED list, so an interface with no members and no heritage still
// declines at an entry exactly as before.
//
// `parameterNames` are the interface's own type parameters, carried in
// so each member is tested for mentioning one — the invariance rule
// scalarMemberListOfIn states.
func interfaceOwnMembersOf(
	holder string,
	asInterface *ast.InterfaceDeclaration,
	parameterNames map[string]struct{},
) ([]recordParamMember, bool) {
	if asInterface.Members == nil || len(asInterface.Members.Nodes) == 0 {
		return nil, true
	}
	return scalarMemberListOfIn(holder, asInterface.Members.Nodes, parameterNames, false)
}

// heritageMembersOf reads everything an interface INHERITS: each
// heritage clause's parent references resolved to their own members by
// the same reading, in clause and reference order, later parents
// shadowing earlier ones the way TypeScript's own resolution does.
//
// A parent reference carrying TYPE ARGUMENTS (`extends Box<number>`)
// declines — the same shape the entry reference itself refuses, for the
// same reason.
//
// A QUALIFIED parent (`extends NodeJS.EventEmitter`) resolves like a
// plain one. In a heritage clause the qualifier is spelled as a PROPERTY
// ACCESS rather than a QualifiedName — the parser reads a heritage
// parent as an expression — so both spellings are admitted here, and
// symbolAt answers on either.
func heritageMembersOf(
	ctx *FlowContext,
	holder string,
	asInterface *ast.InterfaceDeclaration,
	visiting []*ast.Node,
) ([]recordParamMember, bool) {
	if asInterface.HeritageClauses == nil {
		return nil, true
	}
	var inherited []recordParamMember
	for _, clause := range asInterface.HeritageClauses.Nodes {
		heritage := clause.AsHeritageClause()
		if heritage.Types == nil {
			continue
		}
		for _, reference := range heritage.Types.Nodes {
			if !ast.IsExpressionWithTypeArguments(reference) {
				return nil, false
			}
			parent := reference.AsExpressionWithTypeArguments()
			if parent.TypeArguments != nil && len(parent.TypeArguments.Nodes) > 0 {
				return nil, false
			}
			// a plain parent name, or a QUALIFIED one — spelled as a
			// property access in heritage position
			if parent.Expression == nil ||
				!(ast.IsIdentifier(parent.Expression) || ast.IsPropertyAccessExpression(parent.Expression)) {
				return nil, false
			}
			// a parent is a LINK, not the entry: one that reads to no data
			// member contributes nothing rather than declining this
			// interface
			members, ok := declaredTypeMembersOf(ctx, holder, parent.Expression, visiting, false)
			if !ok {
				return nil, false
			}
			inherited = mergeShadowedMembers(inherited, members)
		}
	}
	return inherited, true
}

// mergeShadowedMembers lays the `shadowing` list over the `base` one:
// a member both spell is the SHADOWING one's, kept at the position the
// base already gave it, and a member only the shadowing list spells is
// appended after. Keeping the base's position is what makes the layout
// deterministic — the slot order a parent's members took does not move
// because a child redeclared one of them.
func mergeShadowedMembers(base, shadowing []recordParamMember) []recordParamMember {
	if len(base) == 0 {
		return shadowing
	}
	at := map[string]int{}
	merged := make([]recordParamMember, 0, len(base)+len(shadowing))
	for _, member := range base {
		at[member.Key] = len(merged)
		merged = append(merged, member)
	}
	for _, member := range shadowing {
		if index, already := at[member.Key]; already {
			merged[index] = member
			continue
		}
		at[member.Key] = len(merged)
		merged = append(merged, member)
	}
	return merged
}

// resolvedRecordMembers remembers what ONE parameter node expanded to
// the FIRST time a context resolved it, so every later reading answers
// the same member list.
//
// Why the memo, and not just "resolve again": the two seams that read
// this expansion do not both hold a context. The LAYOUT
// (lowerSummaryBodyWithCaptures) threads the check's FlowContext down;
// the CALL SITES that walk a callee's parameters reach the expansion
// through the ctx-less spelling. If the named-type case answered
// members under one and declined under the other, the layout would build
// N entry slots while the site filled 1 — entry k would take a value
// belonging to entry j, a silent unsoundness with no syntax to point at.
// Caching the first resolved answer under the PARAMETER NODE makes the
// two readings one answer by construction.
//
// The memo only ever ADDS the named-type case: a parameter whose
// annotation is an inline type literal is answered syntactically on
// every path and never consults it.
var (
	resolvedRecordMembersMu sync.Mutex
	resolvedRecordMembers   = map[*ast.Node][]recordParamMember{}
)

// ClearResolvedRecordMembers drops every remembered named-type
// expansion. The memo is keyed on parameter nodes from one program, so a
// caller that builds a new program clears it; the tests clear it between
// cases.
func ClearResolvedRecordMembers() {
	resolvedRecordMembersMu.Lock()
	resolvedRecordMembers = map[*ast.Node]([]recordParamMember){}
	resolvedRecordMembersMu.Unlock()
}

// recordParamMembersOf is the ctx-less spelling every seam that has no
// context reads: the SYNTACTIC type-literal case, plus whatever named
// type a context already resolved for this parameter (see
// resolvedRecordMembers). It never resolves a new name itself.
func recordParamMembersOf(parameter *ast.Node) ([]recordParamMember, bool) {
	return recordParamMembersIn(nil, parameter)
}

// recordParamMembersIn reads a parameter's annotation as a record of
// scalar members and answers one member per property, spelled
// "p.lo"/"p.hi".
//
// Two annotations expand, and nothing else:
//
//	(a) a SYNTACTIC TYPE LITERAL — `p: { lo: number, hi: string }` —
//	    read straight off the annotation, no context needed;
//	(b) a TYPE REFERENCE to a plain identifier naming an INTERFACE (no
//	    heritage, no type parameters) or a TYPE ALIAS of a type literal,
//	    resolved through the context's checker to its one declaration and
//	    then read off THAT declaration's syntax by the same member rules
//	    (namedTypeMembersOf states why the answer is check-independent).
//
// A class name, a generic with no readable constraint, an unresolvable
// name, an index signature, a computed member name, or a duplicate key
// answers false and the parameter keeps its single whole-name slot. A
// declaration's summary quantifies over every caller, and only what the
// annotation itself promises to every entry may become slots. A member
// whose own annotation is not number/boolean/string does not refuse: it
// contributes its named leaf unknown-sorted, which promises the NAME to
// every entry and claims nothing about the value
// (scalarMemberListOfIn's accounting argument).
//
// An OPTIONAL member is such a promise — a weaker one — so it
// contributes its leaf wearing MayBeAbsent; a METHOD signature names no
// slot and is skipped; a QUALIFIED name resolves like a plain one; an
// INTERSECTION reads as the union of its sides' members
// (intersectionMembersOf); and a UNION reads as the members every ARM
// declares, the intersection of the arms' lists, with the weaker absence
// promise winning across arms (unionMembersOf). Each is argued at its own
// reader above.
func recordParamMembersIn(ctx *FlowContext, parameter *ast.Node) ([]recordParamMember, bool) {
	pd := parameter.AsParameterDeclaration()
	if pd.Type == nil || pd.Name() == nil {
		return nil, false
	}
	// the HOLDER the members are spelled under. An identifier parameter
	// spells its own; a BINDING-PATTERN parameter (`({ lo }: Bounds)`)
	// has no name to spell, and its reader — the pattern branch of
	// SummaryParameterEntriesIn — takes only each member's Key, Sort and
	// TypeofTag, never the SlotName. The pattern's own bound names become
	// the entry slots, so the holder here names nothing the body reads.
	holder := ""
	switch {
	case ast.IsIdentifier(pd.Name()):
		holder = pd.Name().Text()
	case ast.IsObjectBindingPattern(pd.Name()):
		holder = "#pattern"
	default:
		return nil, false
	}
	// (a) the inline literal — answered the same on every path, with or
	// without a context, so it never touches the memo
	if ast.IsTypeLiteralNode(pd.Type) {
		return scalarMemberListOf(holder, pd.Type.AsTypeLiteralNode().Members.Nodes)
	}
	// (b) the named type — whatever a context resolved for this parameter
	// stands for every later reading
	resolvedRecordMembersMu.Lock()
	held, remembered := resolvedRecordMembers[parameter]
	resolvedRecordMembersMu.Unlock()
	if remembered {
		return held, len(held) > 0
	}
	members, expanded := namedTypeMembersOf(ctx, holder, pd.Type)
	if !expanded && ast.IsTypeReferenceNode(pd.Type) {
		// a GENERIC parameter reads through its CONSTRAINT: the constraint
		// is the only member set the annotation promises to every type
		// argument a caller could apply, so it is exactly what may become
		// slots. An unbounded generic promises nothing and stays declined.
		members, expanded = constraintMembersOf(ctx, holder, pd.Type)
	}
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		// nothing resolved and nothing to remember: a later reading WITH a
		// context must still be free to resolve this parameter
		return nil, false
	}
	if !expanded {
		members = nil
	}
	resolvedRecordMembersMu.Lock()
	resolvedRecordMembers[parameter] = members
	resolvedRecordMembersMu.Unlock()
	return members, expanded
}

// SummaryParameterEntries is the ONE expansion both the layout and the
// call sites read: the entry slots one declared parameter contributes.
//
// A scalar (or richer-typed, or unannotated) parameter contributes
// exactly ONE entry under its own spelled name, wearing declaredParamSort
// / declaredParamTypeof — today's layout, unchanged. A RECORD parameter
// contributes ONE ENTRY PER MEMBER, spelled "p.lo"/"p.hi", each sorted by
// its own member annotation: an inline type literal of scalar members, or
// — through SummaryParameterEntriesIn, which holds a context to resolve
// with — an interface or type alias whose members read the same way.
//
// (false) where the parameter itself declines outright: a binding
// pattern, a default, or a rest — the same three the lowering has always
// refused, kept here so the two seams cannot disagree about how many
// entries a parameter is worth.
//
// Both the body layout (lowerSummaryBodyWithCaptures) and the call-site
// argument vector (summaryCallStatement) build their entry lists by
// walking the declared parameters through THIS function, so the callee's
// arity, the caller's argument order, and the apply side's entry states
// are three readings of one answer.
func SummaryParameterEntries(parameter *ast.Node) ([]bodySlot, bool) {
	return SummaryParameterEntriesIn(nil, parameter)
}

// SummaryParameterEntriesIn is the same expansion with the check's own
// context, so a parameter annotated with a NAMED type (an interface, a
// type alias of a literal) resolves and expands rather than declining.
// The ctx-less spelling above stays for the seams that hold no context;
// once any context has resolved a parameter, both answer the same list
// (recordParamMembersIn's memo).
func SummaryParameterEntriesIn(ctx *FlowContext, parameter *ast.Node) ([]bodySlot, bool) {
	pd := parameter.AsParameterDeclaration()
	// a BINDING-PATTERN parameter (`({ transform, whitelist }: Options)`)
	// binds locals from the argument object's members: one entry per
	// element, slot name the BOUND name, Key the annotation member it
	// fills from, sort the member's own. Defaults, rests, computed keys,
	// nested patterns, a member the annotation does not spell, and a
	// duplicate bound name all refuse.
	if pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) && pd.DotDotDotToken == nil && pd.Initializer == nil {
		members, isRecord := recordParamMembersIn(ctx, parameter)
		if !isRecord {
			return nil, false
		}
		byKey := map[string]recordParamMember{}
		for _, member := range members {
			byKey[member.Key] = member
		}
		seen := map[string]struct{}{}
		var out []bodySlot
		for _, element := range pd.Name().AsBindingPattern().Elements.Nodes {
			binding := element.AsBindingElement()
			if binding.DotDotDotToken != nil || binding.Initializer != nil ||
				binding.Name() == nil || !ast.IsIdentifier(binding.Name()) {
				return nil, false
			}
			key := binding.Name().Text()
			if binding.PropertyName != nil {
				if !ast.IsIdentifier(binding.PropertyName) {
					return nil, false
				}
				key = binding.PropertyName.Text()
			}
			member, declared := byKey[key]
			if !declared {
				return nil, false
			}
			bound := binding.Name().Text()
			if _, duplicate := seen[bound]; duplicate {
				return nil, false
			}
			seen[bound] = struct{}{}
			out = append(out, bodySlot{
				Name:      bound,
				Key:       key,
				Sort:      member.Sort,
				TypeofTag: member.TypeofTag,
			})
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	}
	if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) {
		return nil, false
	}
	// a REST parameter binds an ARRAY — always defined, possibly empty,
	// its contents unspellable in a scalar slot. One entry under the
	// parameter's own name, unknown-sorted: reads of it answer nothing,
	// which is exactly what is known, and the body no longer refuses.
	if pd.DotDotDotToken != nil {
		return []bodySlot{{
			Name:      pd.Name().Text(),
			Sort:      BindingKindUnknown,
			TypeofTag: TypeofTagNone,
		}}, true
	}
	if members, isRecord := recordParamMembersIn(ctx, parameter); isRecord {
		// a defaulted RECORD parameter would need the default object
		// applied member-wise across the expansion, which no route
		// spells — the refusal stays for the record shape alone. A
		// defaulted SCALAR parameter takes its one entry below, and the
		// body lowering applies the default under a definedness branch.
		if pd.Initializer != nil {
			return nil, false
		}
		out := make([]bodySlot, 0, len(members))
		for _, member := range members {
			out = append(out, bodySlot{
				Name:      member.SlotName,
				Sort:      member.Sort,
				TypeofTag: member.TypeofTag,
			})
		}
		return out, true
	}
	// an ARRAY-TYPED parameter takes the local array's own two slots,
	// "ids.len" and "ids.elem" — AFTER the record arm, which states the
	// exclusivity the caller relies on: one name has one slot family, and
	// a record annotation and an array annotation are disjoint by syntax
	// so no parameter ever reaches both.
	if local, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
		return []bodySlot{
			{Name: local.LenSlotName, Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
			{Name: local.ElemSlotName, Sort: ArrayElementSort(local), TypeofTag: ArrayElementTypeof(local)},
		}, true
	}
	return []bodySlot{{
		Name:      pd.Name().Text(),
		Sort:      declaredParamSort(parameter),
		TypeofTag: declaredParamTypeof(parameter),
	}}, true
}

// resolvedArrayParameters remembers what ONE parameter node flattened to
// the FIRST time a reading resolved it, for the reason
// resolvedRecordMembers holds the record expansion: the LAYOUT reads this
// expansion holding the check's context, while the CALL SITES reach it
// through the ctx-less spelling, and an element sort answered one way
// under a checker and another way without one would give the two seams
// two different slot sorts for one entry.
//
// The COUNT never depended on the checker — a parameter the syntax calls
// an array flattens either way — so only the sort is being pinned. That
// is still worth pinning: entry k's sort is what the caller's argument
// effect is built under.
var (
	resolvedArrayParametersMu sync.Mutex
	resolvedArrayParameters   = map[*ast.Node]ArrayLocal{}
)

// ClearResolvedArrayParameters drops every remembered parameter
// flattening. Keyed on parameter nodes from one program, so a caller that
// builds a new program clears it, as it clears the record memo.
func ClearResolvedArrayParameters() {
	resolvedArrayParametersMu.Lock()
	resolvedArrayParameters = map[*ast.Node]ArrayLocal{}
	resolvedArrayParametersMu.Unlock()
}

// arrayParamSlotsIn recognizes an array-typed parameter, memoized so the
// two seams answer one list. The BODY the use scan needs is the
// parameter's own enclosing declaration — a parameter is never read
// outside the function it belongs to, so the body reached from the
// parameter node is the one body its uses live in.
//
// A parameter whose function has no body (an overload signature, a
// declaration-file signature) flattens nothing: there are no uses to
// scan, and two slots standing for an array nobody reads would only make
// the vector wider.
func arrayParamSlotsIn(ctx *FlowContext, parameter *ast.Node) (ArrayLocal, bool) {
	resolvedArrayParametersMu.Lock()
	held, remembered := resolvedArrayParameters[parameter]
	resolvedArrayParametersMu.Unlock()
	if remembered {
		return held, held.Name != ""
	}
	owner := parameter.Parent
	if owner == nil {
		return ArrayLocal{}, false
	}
	body := owner.Body()
	if body == nil {
		return ArrayLocal{}, false
	}
	var c *checker.Checker
	if ctx != nil && ctx.P != nil {
		c = ctx.P.Checker
	}
	local, flattened := ArrayParameterOf(c, body, parameter)
	if c == nil {
		// nothing was resolved against a checker, so nothing is remembered:
		// a later reading WITH a context must still be free to read the
		// element sort off the resolved type
		return local, flattened
	}
	if !flattened {
		local = ArrayLocal{}
	}
	resolvedArrayParametersMu.Lock()
	resolvedArrayParameters[parameter] = local
	resolvedArrayParametersMu.Unlock()
	return local, flattened
}

// recordParameterUse says what a body does with an EXPANDED parameter's
// own name, apart from reading its declared members.
type recordParameterUse int

const (
	// every occurrence is `p.lo` on a declared member — the expansion
	// spells the whole body and nothing else is needed
	recordParameterMembersOnly recordParameterUse = iota
	// the body mentions the WHOLE record somewhere that only READS it:
	// a spread (`{ ...p }`), a `return p`, an equality test. The leaves
	// are read out; the object itself is never handed to code that could
	// store into it, so every slot keeps its value.
	recordParameterReadWhole
	// the body hands the WHOLE record to code — a call argument, a `new`,
	// a store into another name. The callee may write the caller's object
	// through the reference, so no leaf can be believed past it. The body
	// still lowers, on the pair of moves the layout makes together: the
	// leaves havoc at every code-running statement, and the leaf rows go
	// out Written so the caller takes them back.
	recordParameterEscapesWhole
	// the body does something to the record no slot can stand for: a
	// write through a member, an undeclared member, a computed or
	// optional step, a deep path.
	recordParameterUnreadable
)

// recordParameterUseOf scans a body for every occurrence of an EXPANDED
// parameter's name and answers what the body does with it.
//
// `p.lo` in value position on a DECLARED member is the ordinary reading —
// it consumes the root and the step, and contributes nothing here.
//
// A WHOLE-NAME occurrence is classified by the position it stands in,
// because after the expansion there is no single slot denoting `p` and
// what the body may still be served depends on whether that position can
// MOVE the object:
//
//   - a SPREAD element (`{ ...p }`, `f(...p)` is not this — see below) and
//     a `return p` read the fields and hand out no writable reference the
//     body itself uses again. Their leaves stay believable, so the body
//     lowers whole and the whole-name expression takes the opaque floor
//     its own route already gives it.
//   - a CALL or NEW ARGUMENT (`f(p)`, `new C(p)`) and a STORE (`q = p`,
//     `xs.push(p)` — the push argument is a call argument) hand the
//     object to code that may store into it. Sound only with every leaf
//     havocked at the hand-over AND the leaf rows carrying the movement
//     back to whoever filled them, which the layout arranges as one pair
//     (lowerSummaryBodyWithCaptures' record-parameter branch).
//
// A WRITE TO A DECLARED MEMBER (`p.lo = 1`, `p.lo += 1`, `p.lo++`) is
// SERVED, and this is the one classification that changed: the leaf has a
// slot of its own — the expansion laid "p.lo" out as an entry, and
// SpelledNameOf spells exactly that one step — so the write lowers as an
// ordinary assignment to that slot, and the leaf row goes out Written so
// the caller takes the moved value back through the call statement's rets
// (recordParamRets, ir_summary_call.go). That is the same write-back
// threading a HANDED-OVER record's leaves already ride; the difference is
// only that here the lowering can see the write and name its value,
// instead of standing in for code it cannot read.
//
// The written members are reported beside the use, because the layout
// needs to know WHICH leaves moved: a member the body only reads keeps
// its row unwritten, so the caller goes on believing it. Marking every
// leaf written would be sound and needlessly coarse.
//
// These still make the body unreadable, each because no slot stands for
// what was written:
//
//   - a member the annotation never declared (`p.mid`) — no slot holds
//     it, and reading it would silently answer another slot's state.
//     THIS IS ALSO WHAT KEEPS THE METHOD SKIP HONEST: a method signature
//     contributes no leaf (scalarMemberListOf), so a body that calls
//     `p.write(…)` reads a one-step path on an undeclared name and lands
//     here. The call is refused, never admitted as an accounted-for leaf
//     use. Skipping widens which member LISTS read; it never widens
//     which USES are served;
//   - a `delete p.lo` — the leaf slot holds a value, and no slot state
//     spells "this key is no longer present";
//   - a deep path (`p.lo.x`) — the members are scalars, so no such leaf
//     exists;
//   - a COMPUTED or OPTIONAL step (`p[e]`, `p?.lo`) — the first names no
//     member, the second reads a record that may be absent.
//
// The answer is the WORST use found: one hand-over makes the whole body's
// leaves movable, and one unreadable use declines it whatever else it
// does.
func recordParameterUseOf(
	body *ast.Node,
	name string,
	members []recordParamMember,
) (recordParameterUse, map[string]struct{}) {
	declared := map[string]struct{}{}
	for _, member := range members {
		declared[member.Key] = struct{}{}
	}
	// the declared members this body ASSIGNS through the parameter — the
	// leaves whose rows go out Written
	writtenMembers := map[string]struct{}{}
	// writeThroughParameter classifies a node that writes through this
	// parameter's spelling: (served, unreadable). A one-step write on a
	// DECLARED member notes the member and is served; every other write
	// through the name is unreadable.
	writeThroughParameter := func(target *ast.Node, deletes bool) (served bool, unreadable bool) {
		root, path, ok := propertyPathOf(Unwrapped(target))
		if !ok || root != name {
			return false, false
		}
		if deletes {
			// no slot state spells an absent KEY
			return false, true
		}
		if len(path) != 1 {
			return false, true
		}
		if _, isDeclared := declared[path[0]]; !isDeclared {
			return false, true
		}
		writtenMembers[path[0]] = struct{}{}
		return true, false
	}
	// a node that WRITES through this parameter's spelling, answering
	// whether the write is unreadable — a served member write answers
	// false here and has already been noted
	writesThroughParameter := func(node *ast.Node) bool {
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if _, unreadable := writeThroughParameter(bin.Left, false); unreadable {
					return true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if _, unreadable := writeThroughParameter(unary.Operand, false); unreadable {
					return true
				}
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if _, unreadable := writeThroughParameter(unary.Operand, false); unreadable {
					return true
				}
			}
		}
		if ast.IsDeleteExpression(node) {
			if _, unreadable := writeThroughParameter(node.AsDeleteExpression().Expression, true); unreadable {
				return true
			}
		}
		return false
	}
	worst := recordParameterMembersOnly
	// the worst use wins, and an unreadable one ends the walk
	note := func(use recordParameterUse) {
		if use > worst {
			worst = use
		}
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if worst == recordParameterUnreadable {
			return true
		}
		if writesThroughParameter(node) {
			note(recordParameterUnreadable)
			return true
		}
		// a declared member READ consumes the root and the step name, so
		// neither reaches the bare-name test below
		if root, path, isPath := propertyPathOf(Unwrapped(node)); isPath && root == name {
			if len(path) != 1 {
				note(recordParameterUnreadable)
				return true
			}
			if _, isDeclared := declared[path[0]]; !isDeclared {
				note(recordParameterUnreadable)
				return true
			}
			return false
		}
		// every other occurrence of the bare name is the WHOLE record, and
		// the POSITION it stands in decides what the body may still be
		// served
		if ast.IsIdentifier(node) && node.Text() == name && !isPropertyStepName(node) {
			note(wholeRecordUseAt(node))
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	if worst == recordParameterUnreadable {
		// the body declines whole; the members noted before the refusal
		// name nothing the layout will lay out
		return worst, nil
	}
	return worst, writtenMembers
}

// wholeRecordUseAt classifies ONE whole-name occurrence by the position
// it stands in — the reading recordParameterUseOf's doc states.
//
// READ-WHOLE, the positions that hand out no reference the body could
// later store through:
//
//   - a SPREAD in an object literal (`{ ...p, y: 1 }`) — the fields are
//     copied out into a fresh object;
//   - a `return p` — the value leaves; nothing in this body reads it
//     again, and the caller already holds whatever it passed.
//
// ESCAPES-WHOLE, the positions that hand the object to code:
//
//   - a CALL or NEW argument, INCLUDING a spread one (`f(...p)` passes
//     the object's own entries, and `f(p)` the object) — the callee may
//     store into it;
//   - anything else a bare mention can be: an initializer, an assignment
//     right side, an array element, a property value. Each stores the
//     reference under a name this scan does not follow.
//
// The default is the ESCAPING one: a position this reading does not
// recognize is one whose writes it cannot rule out.
func wholeRecordUseAt(node *ast.Node) recordParameterUse {
	parent := node.Parent
	if parent == nil {
		return recordParameterEscapesWhole
	}
	if ast.IsReturnStatement(parent) {
		return recordParameterReadWhole
	}
	if ast.IsSpreadAssignment(parent) {
		// `{ ...p }` copies the fields out; `f(...p)` is a SpreadElement,
		// which is an argument and falls through to the escape below
		return recordParameterReadWhole
	}
	return recordParameterEscapesWhole
}

/* ── the `this` bundle ───────────────────────────────────────────── */

// thisBundleLayout is what a METHOD's own receiver is worth as entry
// slots: the fields the body READS become entries spelled
// "this.<field>", laid out after the declared parameters, and the fields
// it WRITES are named so the outs can carry them back.
//
// `expanded` false is the whole decline of the EXPANSION, never of the
// body: the method still lowers, its this-reads simply find no slot and
// hit the opaque floor exactly as they do today. `escaped` says WHY it
// declined when the reason was the receiver leaving the lowering's sight
// — the caller notes it as the body's first havoc, because a bundle that
// escaped may be written through a name nothing here saw.
type thisBundleLayout struct {
	Entries  []bodySlot
	Written  map[string]struct{}
	Expanded bool
	Escaped  bool
	// CaptureHavocNames: the slot spellings ("this.count") of the fields a
	// method-calling CAPTURE can move — the captured methods' transitive
	// write set. Non-empty puts the body's statement walk in havoc mode
	// (LoweringContext.CaptureHavocSlots); each named field is also in
	// Written, so the call sites read its exit state instead of keeping
	// the caller's own.
	CaptureHavocNames []string
	// ReturnsSelf: the body ends `return this` — the serving seams must
	// forget the caller's receiver knowledge (LoweredSummary
	// .ReturnsReceiver carries the requirement out).
	ReturnsSelf bool
}

// thisBundleOf reads a declaration's `this` bundle: (nothing) for
// anything that is not a method, and otherwise the field census of the
// enclosing class run over the method's body.
//
// The rules, per the wave-4 design:
//
//   - only a METHOD has a `this` bundle. An arrow keeps its enclosing
//     `this`, but the arrow route lays out CAPTURES after the declared
//     parameters, and a bundle would have to share that ground — so the
//     two are exclusive and the caller asserts it rather than laying out
//     both.
//   - the READ fields become entries, in the class's own declaration
//     order (FieldCensusOf answers in that order, which is what keeps
//     the layout and the apply side building one vector).
//   - COMPUTED access expands anyway: `this[k]` names no field, but the
//     havoc floor already stands in for the statement that performed it,
//     and the declaration still bounds which slots could be meant.
//   - an ESCAPING receiver does NOT expand. The bundle may move through a
//     name the census never saw, so an entry's state could be stale
//     mid-body while the slot still reads as known — the one shape where
//     expanding would claim more than it knows.
func thisBundleOf(ctx *FlowContext, declaration *ast.Node) thisBundleLayout {
	if declaration == nil {
		return thisBundleLayout{}
	}
	// an ACCESSOR is a class member with a body and a `this`, exactly
	// like a method — its bundle is what lets a getter over a backing
	// field compile at all (the accessor-call route reads it)
	if !ast.IsMethodDeclaration(declaration) &&
		!ast.IsGetAccessorDeclaration(declaration) &&
		!ast.IsSetAccessorDeclaration(declaration) &&
		!ast.IsConstructorDeclaration(declaration) {
		return thisBundleLayout{}
	}
	body := declaration.Body()
	if body == nil {
		return thisBundleLayout{}
	}
	classLike := declaration.Parent
	if classLike == nil || !ast.IsClassLike(classLike) {
		return thisBundleLayout{}
	}
	// declared members UNION constructor parameter properties — nest's
	// classes declare most fields as `constructor(private readonly …)`
	fields, isClass := ClassBundleFields(ctx, classLike)
	if !isClass || len(fields) == 0 {
		return thisBundleLayout{}
	}
	census := FieldCensusOf(body, "this", BundleFieldsAs("this", fields))
	// a METHOD-CALLING capture is admissible HERE, because this consumer
	// has the havoc machinery: the captured methods' transitive write set
	// becomes the havoc slots every code-running statement brackets, and
	// each of those fields is marked written so the call sites read its
	// exit state. An incomputable write set keeps the escape.
	var captureHavocNames []string
	switch {
	case census.Escapes:
		return thisBundleLayout{Escaped: true}
	case census.ComputedWrite:
		// `this[k] = v` moves a slot nothing names — the DECLARATION
		// bounds the set, so EVERY field joins the havoc set and every
		// code-running or element-storing statement brackets them
		for _, field := range fields {
			captureHavocNames = append(captureHavocNames, "this."+field.Name)
		}
	case len(census.CapturedMethodCalls) > 0:
		wipes, computable := CaptureWriteSet(classLike, fields, census.CapturedMethodCalls)
		if !computable {
			return thisBundleLayout{Escaped: true}
		}
		for _, field := range wipes {
			captureHavocNames = append(captureHavocNames, "this."+field.Name)
		}
	}
	// A CONSTRUCTOR RUNS MORE THAN ITS BODY. The class's field
	// INITIALIZERS and its PARAMETER PROPERTIES are statements the runtime
	// runs on the way in, and the constructor prelude
	// (lowerSummaryBodyWithCaptures) emits exactly those assignments ahead
	// of the body's own. The census, though, walks the BODY, so a field
	// written only by its initializer — `private _statusCode = 200;` in a
	// class whose constructor only calls super — appears in neither Reads
	// nor Writes, and the bundle it belongs to never expands. That is a
	// field the constructor demonstrably leaves a value in, reported as a
	// field the constructor never touched.
	//
	// So a constructor's prelude-written fields join the census's own
	// writes here, read off the class's declarations rather than off the
	// body. They are the same fields the prelude will assign, resolved by
	// the same two rules the prelude applies (an initialized property
	// declaration, a parameter property), so the layout and the prelude
	// name one set: every slot the prelude writes exists, and every slot
	// laid out for a prelude write is one the prelude fills.
	preludeWritten := constructorPreludeFields(declaration, fields)
	if len(census.Reads) == 0 && len(census.Writes) == 0 &&
		len(captureHavocNames) == 0 && len(preludeWritten) == 0 {
		return thisBundleLayout{}
	}
	written := map[string]struct{}{}
	for _, field := range census.Writes {
		written[field.SlotName] = struct{}{}
	}
	for _, name := range captureHavocNames {
		written[name] = struct{}{}
	}
	for _, field := range preludeWritten {
		written[field.SlotName] = struct{}{}
	}
	// the READ fields carry entries. A write-only field has no entry state
	// for the caller to fill — its slot is one the body creates, which the
	// locals' own layout would have to hold — so this wave lays out the
	// reads and names the writes among them.
	//
	// A CONSTRUCTOR's prelude-written fields carry entries too, and for the
	// reason the read fields do not have to argue: the prelude ASSIGNS every
	// one of them before any statement runs, so the entry's incoming value
	// is overwritten before anything can read it. The entry state a caller
	// would fill is dead on arrival, which is what makes laying out a
	// write-only slot honest here and not in a method. The entry exists so
	// the prelude has a slot to write and the exit row has a slot to report
	// — which is what a `new C()` local's leaves are read from.
	entries := make([]bodySlot, 0, len(census.Reads)+len(preludeWritten))
	for _, field := range census.Reads {
		entries = append(entries, bodySlot{
			Name:      field.SlotName,
			Sort:      field.Sort,
			TypeofTag: field.TypeofTag,
		})
	}
	// the prelude fields come after the read ones, each at most once — a
	// field the body ALSO reads already has its entry, and a second would
	// put the same spelling in the vector twice
	laidOut := map[string]struct{}{}
	for _, entry := range entries {
		laidOut[entry.Name] = struct{}{}
	}
	for _, field := range preludeWritten {
		if _, already := laidOut[field.SlotName]; already {
			continue
		}
		laidOut[field.SlotName] = struct{}{}
		entries = append(entries, bodySlot{
			Name:      field.SlotName,
			Sort:      field.Sort,
			TypeofTag: field.TypeofTag,
		})
	}
	return thisBundleLayout{
		Entries:           entries,
		Written:           written,
		Expanded:          len(entries) > 0,
		CaptureHavocNames: captureHavocNames,
		ReturnsSelf:       census.ReturnsSelf,
	}
}

// constructorPreludeFields is the field set a CONSTRUCTOR's prelude
// writes before its body's first statement: every class member that is
// an initialized property declaration, and every parameter property.
//
// It exists so the LAYOUT and the PRELUDE read one list. The prelude
// (lowerSummaryBodyWithCaptures) emits an assignment per initialized
// property and per parameter property, each onto the slot named
// "this.<field>"; this function names exactly those fields off the same
// declarations, so a slot the prelude looks up always exists, and a slot
// laid out for a prelude write is always one the prelude fills. Reading
// them apart is what let the two disagree: the layout asked the body
// census, which never sees an initializer.
//
// Answered in the given field set's order — the declaration order the
// slot vector is built in — and only for fields that set holds, so a
// property the field reading declined (a computed key spelling no slot)
// contributes nothing here either.
//
// Anything that is not a constructor answers nothing: a method has no
// prelude, and its fields are exactly what its body touches.
func constructorPreludeFields(declaration *ast.Node, fields []BundleField) []BundleField {
	if declaration == nil || !ast.IsConstructorDeclaration(declaration) {
		return nil
	}
	classLike := declaration.Parent
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil
	}
	assigned := map[string]struct{}{}
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		property := member.AsPropertyDeclaration()
		// an UNINITIALIZED declaration (`private _headers?: Headers;`)
		// writes nothing — the field enters the body absent, and claiming a
		// value was left in it would be the one thing this must not say
		if property.Initializer == nil || property.Name() == nil || !ast.IsIdentifier(property.Name()) {
			continue
		}
		assigned[property.Name().Text()] = struct{}{}
	}
	for _, parameter := range declaration.Parameters() {
		if !isParameterPropertyDeclaration(parameter) {
			continue
		}
		pd := parameter.AsParameterDeclaration()
		if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) {
			continue
		}
		assigned[pd.Name().Text()] = struct{}{}
	}
	if len(assigned) == 0 {
		return nil
	}
	written := make([]BundleField, 0, len(assigned))
	for _, field := range fields {
		if _, isAssigned := assigned[field.Name]; isAssigned {
			written = append(written, field)
		}
	}
	return written
}

// summarySlotBudget is the summary route's own slot ceiling — the same
// figure the whole-body route uses, kept here so the two routes admit
// the same bodies.
const summarySlotBudget = 32

/* ── the returned value's members ────────────────────────────────── */

// retMemberSlotName is the slot one member of a RETURNED object literal
// rides in: "#ret.type" beside the "#ret" the scalar return writes. The
// "#" prefix is the same one #done and #ret wear — no source name can
// collide with it, so a member slot never shadows a local.
func retMemberSlotName(member string) string {
	return "#ret." + member
}

// retLenSlotName / retElemSlotName are the pair a RETURNED array literal
// rides in, spelled the way a flattened local array's pair is spelled
// (ir_array_slots.go's ".len"/".elem" convention) so the two readings of
// "an array is a length and a joined element" stay one convention.
func retLenSlotName() string  { return "#ret.len" }
func retElemSlotName() string { return "#ret.elem" }

// retMemberNameOfSlot is retMemberSlotName read backwards: the member a
// "#ret.<member>" slot stands for. The pair's spellings answer "len" and
// "elem", which is what the array shape's two rows are named.
func retMemberNameOfSlot(slotName string) string {
	return strings.TrimPrefix(slotName, "#ret.")
}

// RetMemberEntry is one slot of a returned value's shape: the member it
// stands for and the slot index its exit is read from. The layout fills
// these rows and every consumer reads them — the apply route rebuilding
// an object, the call statement deciding what its rets carry — so the
// three seams walk one list rather than each re-deriving which slot is
// which.
//
// Name is the object member's own key ("type", "dynamicMetadata"), or
// the array pair's spelling ("len", "elem") where Kind says array.
type RetMemberEntry struct {
	Name  string
	Index int
}

// RetShapeKind says WHAT the returned value's member slots describe.
type RetShapeKind int

const (
	// the ret slot alone carries the value — every body that returns a
	// scalar, and every body whose returns this allocator could not read
	RetShapeNone RetShapeKind = iota
	// the members are an OBJECT's keys, one slot per key
	RetShapeObject
	// the members are an ARRAY's length and joined element, two slots
	RetShapeArray
)

// returnedLiteralShape reads a body's RETURN statements and answers the
// member slots the returned value needs, or (nil, RetShapeNone) where
// the ret slot alone is what the value can ride.
//
// The rule is one shape for the WHOLE body. A summary has one exit row,
// so every returning path must agree about what the returned value IS —
// a body returning `{ a, b }` on one path and `[x]` on another has no
// single member layout, and one returning `{ a, b }` beside a bare
// `return` or `return someName` has members on one path and nothing to
// say on the other. Both keep the scalar ret alone, which is exactly
// today's answer.
//
// What DOES allocate:
//
//   - every return in the body carries an OBJECT LITERAL, and the union
//     of their keys is the member list. A key one arm spells and another
//     does not still allocates: the arm that does not write it leaves the
//     slot at its absent entry state, which is what "this path returned
//     an object without that key" means.
//   - every return in the body carries an ARRAY LITERAL with no spread,
//     and the pair is allocated. The lengths need not agree — the exits
//     join, and a join of two exact lengths is what the caller is owed.
//
// A member whose VALUE the effect grammar cannot spell does NOT refuse:
// its slot takes unknown at the return (the lowering's own arm), and a
// partial object beats a whole unknown. What refuses here is only a shape
// question — a spread, a computed key the reader cannot name, an accessor
// or method member, a shorthand of a name, all of which change WHICH keys
// exist rather than what one key holds.
func returnedLiteralShape(body *ast.Node) ([]bodySlot, RetShapeKind) {
	if body == nil {
		return nil, RetShapeNone
	}
	returns := returnedExpressionsOf(body)
	if len(returns) == 0 {
		return nil, RetShapeNone
	}
	objects := 0
	arrays := 0
	for _, returned := range returns {
		head := Unwrapped(returned)
		if head == nil {
			continue
		}
		// A LITERAL THAT RUNS CODE allocates nothing, and this is the rule
		// the whole shape rests on rather than a precision choice. The
		// return lowering writes members as EFFECTS, which have no room for
		// a statement, so a literal whose evaluation calls or writes cannot
		// write its own members — it falls to the floor and leaves the
		// member slots holding whatever came before. Were the shape still
		// allocated, that arm's exits would read as "the returned object has
		// no such key" for a path that in fact returned every key: a WRONG
		// answer, not a weak one. Refusing the shape for the whole body
		// keeps every path's answer the unknown it is today.
		if !writeAndCallFree(head) {
			return nil, RetShapeNone
		}
		switch {
		case ast.IsObjectLiteralExpression(head):
			objects++
		case ast.IsArrayLiteralExpression(head):
			arrays++
		}
	}
	// one shape for the whole body, and every path must carry it
	if objects == len(returns) {
		return objectRetMembersOf(returns)
	}
	if arrays == len(returns) {
		return arrayRetMembersOf(returns)
	}
	return nil, RetShapeNone
}

// objectRetMembersOf reads every returned object literal's keys as the
// member slot list — the union, in first-seen order, so the layout is
// deterministic across the arms.
//
// Every property of every returned literal must NAME ONE KEY this reader
// can spell: a plain identifier or string-literal property assignment, or
// a shorthand. A spread, a computed key, an accessor and a method each
// answer none — a spread brings keys from a source this reader cannot
// enumerate, and the others carry no member value a slot could hold — and
// the whole body then keeps its scalar ret.
func objectRetMembersOf(returns []*ast.Node) ([]bodySlot, RetShapeKind) {
	var order []string
	seen := map[string]struct{}{}
	for _, returned := range returns {
		literal := Unwrapped(returned).AsObjectLiteralExpression()
		if len(literal.Properties.Nodes) == 0 {
			// `return {}` names no member, so it says nothing a slot could
			// carry and nothing that contradicts another arm's keys
			continue
		}
		for _, property := range literal.Properties.Nodes {
			name, named := retMemberNameOf(property)
			if !named {
				return nil, RetShapeNone
			}
			if _, already := seen[name]; already {
				continue
			}
			seen[name] = struct{}{}
			order = append(order, name)
		}
	}
	if len(order) == 0 {
		return nil, RetShapeNone
	}
	// a member's SORT is unknown: the layout runs before any statement
	// lowers, so the values the arms write are not yet read, and a sort
	// nothing promises may not be assumed. The return arm writes each
	// slot through the unknown sort's own reading, and the exit state is
	// what the value turns out to be.
	out := make([]bodySlot, 0, len(order))
	for _, name := range order {
		out = append(out, bodySlot{
			Name:      retMemberSlotName(name),
			Sort:      BindingKindUnknown,
			TypeofTag: TypeofTagNone,
		})
	}
	return out, RetShapeObject
}

// retMemberNameOf spells the ONE key a literal property writes, or
// (false) where the property names no single key this reader can state.
func retMemberNameOf(property *ast.Node) (string, bool) {
	if ast.IsShorthandPropertyAssignment(property) {
		name := property.AsShorthandPropertyAssignment().Name()
		if name != nil && ast.IsIdentifier(name) {
			return name.Text(), true
		}
		return "", false
	}
	if !ast.IsPropertyAssignment(property) {
		return "", false
	}
	name := property.AsPropertyAssignment().Name()
	if name == nil {
		return "", false
	}
	if ast.IsIdentifier(name) || ast.IsStringLiteral(name) {
		return name.Text(), true
	}
	return "", false
}

// arrayRetMembersOf answers the ".len"/".elem" pair for a body whose
// every return carries an array literal.
//
// A SPREAD element refuses the whole shape: the length is then whatever
// the spread source holds, which no literal count states, and a wrong
// length is a wrong answer rather than a weak one.
func arrayRetMembersOf(returns []*ast.Node) ([]bodySlot, RetShapeKind) {
	for _, returned := range returns {
		literal := Unwrapped(returned).AsArrayLiteralExpression()
		for _, element := range literal.Elements.Nodes {
			if ast.IsSpreadElement(element) {
				return nil, RetShapeNone
			}
		}
	}
	return []bodySlot{
		{Name: retLenSlotName(), Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
		{Name: retElemSlotName(), Sort: BindingKindUnknown, TypeofTag: TypeofTagNone},
	}, RetShapeArray
}

// returnedExpressionsOf collects the expression of every `return e` in a
// body, skipping NESTED function-likes — an inner function's returns are
// the inner function's value, not this body's.
//
// A bare `return` (no expression) is collected as nil, so the shape
// reader above sees a path whose value is undefined and keeps the scalar
// ret: a body that sometimes returns an object and sometimes nothing has
// no single member layout.
func returnedExpressionsOf(body *ast.Node) []*ast.Node {
	var out []*ast.Node
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if node == nil {
			return
		}
		if node != body && ast.IsFunctionLike(node) {
			return
		}
		if ast.IsReturnStatement(node) {
			out = append(out, node.AsReturnStatement().Expression)
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	// a CONCISE arrow body is its own single return
	if !ast.IsBlock(body) {
		return []*ast.Node{body}
	}
	scan(body)
	return out
}

// (Parameter sorts and typeof evidence read through kernel_summaries
// .go's declaredParamSort / declaredParamTypeof: an unannotated or
// richer-typed parameter is UNKNOWN — a summary quantifies over all
// entries, so a sort nothing promises may not be assumed.)

// bodySlot is one slot the body's locals contribute: its spelled name,
// the sort its occurrences wear, and what typeof answers for it.
type bodySlot struct {
	Name      string
	Sort      BindingKind
	TypeofTag TypeofTag
	// Key: the annotation MEMBER this entry fills from at call sites,
	// where it differs from Name — the binding-pattern parameter's
	// renaming (`{ transform: t }` binds t from member transform).
	// Empty for every other entry kind.
	Key string
}

// collectSummaryLocals is CollectLocals widened by exactly two shapes,
// and — where CollectLocals declines the whole body — narrowed to a SKIP.
//
// (1) A NESTED FUNCTION-LIKE NODE (a function or arrow expression, a
// function or class declaration, a class expression) is SKIPPED, not
// declined. Its declarations are the INNER function's, not this body's:
// collecting them would lay out slots for names this body cannot read,
// and declining costs this body its whole route over statements it
// could still lower. So the subtree is stepped over and the collection
// carries on with the enclosing body's own declarations.
//
// The statement HOLDING the nested function then lowers by whichever
// route reads it:
//
//   - a recognized callback form at a call site (`xs.map(x => x + 1)`,
//     ir_callback_summary.go) converts the arrow into its own summary
//     and lowers the site to one call statement — that route reads the
//     arrow node itself and never asks this collection about it;
//   - otherwise the HAVOC FLOOR. `const cb = x => …` is a variable
//     statement whose right side no effect grammar reads, so every route
//     declines and OpaqueHavocStatements serves it under rule (c): the
//     declared name's own slot takes `unknown`
//     (declaredNameSlots → the identifier's slot). An arrow VALUE has no
//     slot semantics of its own — nothing lowered can read "the function
//     cb is" — and every later use of cb is a CALL, which the opaque
//     call tier havocs in its own right. The floor also walks INTO the
//     arrow for rules (a) and (b), so an arrow writing an outer name
//     havocs that name's slot too (havocSlotsOfStatement's own comment).
//
// THE BOUNDARY RULE THIS SKIP DEPENDS ON. Stepping over a nested
// function is sound only because the two predicates answer the SAME
// question about it: this collection lays out no slot for the inner
// body's own names, and the floor havocs every name of THIS body the
// inner one writes. That agreement holds only while every route
// admitting a statement that hands over a closure either falls to the
// floor or asks ClosureEscapesTrackedWrite (effect_expression.go), which
// is the rule stated once for both sites. The routes that admit WITHOUT
// the floor's walk — the effect grammar's literal, ternary and
// short-circuit arms; the branch-shaped return's inert arm; this file's
// constructor and default preludes — each ask it, and each havoc the
// closure's write set where the answer is yes. A route added later that
// believes a slot an arrow writes would break the skip above, not merely
// lose precision.
//
// A FUNCTION DECLARATION statement (`function helper() {…}`) is served
// the same way, and its HOISTING cannot be observed by anything lowered.
// JS hoists a function declaration to the top of its scope, so a call
// written above the declaration statement still reaches it — the floor's
// havoc, which sits at the statement's own position, would be too late
// for that call. It does not matter: `helper` is not a variable
// declaration, so this collection gives it no slot at all, and a name
// with no slot cannot be read by any lowered statement. There is no
// knowledge about `helper` for an out-of-order write to falsify, and a
// call THROUGH it resolves (or does not) through ResolveCallee, which
// reads the declaration node rather than any slot.
//
// (2) An OBJECT BINDING PATTERN (`const { x, y } = p`) contributes one
// ordinary scalar local per bound name rather than declining the body.
// That is what the destructuring lowering needs — each bound name gets
// its own slot, written from the record leaf it reads.
//
// (3) An ARRAY BINDING PATTERN (`const [a, b] = xs`) contributes its
// bound names as ordinary UNKNOWN-sorted locals, one slot per element
// name, rather than declining. No route lowers the declaration itself —
// DestructuringAssignmentsOf reads object patterns alone, and the
// two-slot array flattening does not distinguish positions — so the
// declaring statement reaches the havoc floor, which havocs exactly
// those bound names' slots under rule (c) (declaredNameSlots recurses
// through both binding-pattern kinds). The names are then honestly
// unknown rather than unreadable, and every OTHER statement of the body
// keeps its knowledge.
//
// Inside an array pattern, an element with a plain identifier name is
// collected and everything else — a REST element (`...rest`), a NESTED
// pattern (`const [[a]] = xs`), an omitted hole — is skipped. A skipped
// element's name still gets havocked where it has one, and where it has
// none nothing lowered can read it; a DEFAULT (`const [a = 1] = xs`) is
// covered the same way, since the havoc is the whole value claim for
// that name and it admits the default as readily as the element.
//
// Everything else is CollectLocals unchanged: single-identifier
// declarations in source order, recursing into branch arms and blocks.
//
// (CollectLocals itself lives in tracked_bindings.go, which the whole-
// body route shares; widening it there would change that route's
// admitted set too. This variant belongs to the summary route alone.)
func collectSummaryLocals(body *ast.Node) (locals []*ast.Node, patterns []*ast.Node, ok bool) {
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		// a nested function-like node's declarations are ITS locals, not
		// this body's — the subtree is stepped over whole
		if ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) ||
			ast.IsArrowFunction(node) || ast.IsClassDeclaration(node) ||
			ast.IsClassExpression(node) {
			return false
		}
		if ast.IsVariableDeclaration(node) {
			name := node.Name()
			if ast.IsObjectBindingPattern(name) || ast.IsArrayBindingPattern(name) {
				patterns = append(patterns, node)
				// the INITIALIZER still walks: a nested declaration inside it
				// (`const { x } = (() => …)()`) belongs to this body
				node.ForEachChild(visit)
				return false
			}
			if !ast.IsIdentifier(name) {
				// a declaration whose name is neither an identifier nor either
				// binding pattern has no name to lay out; the floor's rule (c)
				// answers no slots for it and the statement havocs nothing
				return false
			}
			locals = append(locals, node)
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return locals, patterns, true
}

// destructuredSlotsOf lays out the slots a destructuring declaration
// needs: one per bound name.
//
// An OBJECT pattern's names wear the SORT of the record leaf each one
// reads. A pattern whose source is not a flattened record, or that names
// a leaf the record does not have, contributes unknown-sorted slots —
// the read then finds no leaf slot and the lowering declines, which is
// the total-or-decline law doing its work.
//
// An ARRAY pattern's names are UNKNOWN-sorted, every one of them. The
// two-slot flattening holds a length and the JOIN of the elements, and
// nothing that distinguishes position `0` from position `1` — so there
// is no leaf whose sort a bound name could wear. The declaring statement
// reaches the havoc floor, which writes `unknown` into exactly these
// slots (rule (c)), and the unknown sort is what that answer already
// says: the names are readable and nothing is claimed about them.
//
// Either kind SKIPS what it cannot name — a rest element, a nested
// pattern, an omitted hole. A skipped element's own bound names get no
// slot, and a name with no slot cannot be read by any lowered statement,
// so nothing is claimed about it either way. (The floor still havocs
// whatever slots such a name DOES have, since declaredNameSlots walks
// both pattern kinds whole.)
func destructuredSlotsOf(pattern *ast.Node, records map[*ast.Node]ObjectLocal) []bodySlot {
	decl := pattern.AsVariableDeclaration()
	isArray := ast.IsArrayBindingPattern(decl.Name())
	// the leaves of the record this pattern reads, by their one-step key
	leafOfKey := map[string]ObjectLocalKey{}
	if !isArray && decl.Initializer != nil {
		initializer := Unwrapped(decl.Initializer)
		if ast.IsIdentifier(initializer) {
			source := initializer.Text()
			for _, record := range records {
				if record.Name != source {
					continue
				}
				for _, key := range record.Keys {
					if len(key.Path) == 1 {
						leafOfKey[key.Path[0]] = key
					}
				}
			}
		}
	}
	var out []bodySlot
	for _, element := range decl.Name().AsBindingPattern().Elements.Nodes {
		if !ast.IsBindingElement(element) {
			// an omitted hole (`const [, b] = xs`) binds no name
			continue
		}
		binding := element.AsBindingElement()
		// a rest element holds the REMAINDER — an array or an object, not
		// a scalar the slot vector can carry — and a nested pattern binds
		// names one level down that no leaf spells
		if binding.DotDotDotToken != nil || binding.Name() == nil || !ast.IsIdentifier(binding.Name()) {
			continue
		}
		slot := bodySlot{Name: binding.Name().Text(), Sort: BindingKindUnknown, TypeofTag: TypeofTagNone}
		if isArray {
			// no leaf distinguishes a position: unknown-sorted, and the
			// declaring statement's havoc is what fills it
			out = append(out, slot)
			continue
		}
		read := binding.Name().Text()
		if binding.PropertyName != nil && ast.IsIdentifier(binding.PropertyName) {
			read = binding.PropertyName.Text()
		}
		if leaf, found := leafOfKey[read]; found {
			slot.Sort = ObjectLocalKeySort(leaf)
			slot.TypeofTag = ObjectLocalKeyTypeof(leaf)
		}
		out = append(out, slot)
	}
	return out
}

// localSlotsOf lays out a body's locals as slots: a scalar local takes
// one, a flattened record one PER LEAF ("p.a.b"), and a flattened array
// TWO ("a.len", "a.elem"). A local the recognizers declined keeps its
// single whole-name slot, whose key or index reads then find no slot
// and decline the lowering — the existing behaviour.
//
// `c` is the host checker a plain-name scalar's sort is resolved
// against (LocalSortResolved); nil reads the initializer's syntax
// alone. The flattened families read their own leaves and never ask it.
func localSlotsOf(
	c *checker.Checker,
	body *ast.Node,
	locals []*ast.Node,
	patterns []*ast.Node,
	parameterNames map[string]struct{},
) []bodySlot {
	return localSlotsIn(nil, c, body, locals, patterns, parameterNames)
}

// localSlotsIn is localSlotsOf holding the check's own context, so the
// record families whose leaves come from a DECLARATION — a served
// constructor's exit rows, a declared record type over an opaque
// initializer, a two-armed join — are recognized beside the literal one.
// The ctx-less spelling above stays for the seams that hold no context;
// they keep exactly today's layout.
func localSlotsIn(
	ctx *FlowContext,
	c *checker.Checker,
	body *ast.Node,
	locals []*ast.Node,
	patterns []*ast.Node,
	parameterNames map[string]struct{},
) []bodySlot {
	objectLocals := ObjectLocalsIn(ctx, body, locals)
	// the collections flatten first: the array recognizer needs them to
	// admit the bridge (`[...m.values()]` reads the map's slots)
	collectionLocals := MapLocalsOf(body, locals)
	arrayLocals := ArrayLocalsOf(body, locals, collectionLocals)
	var order []string
	declaredOf := map[string]*ast.Node{}
	for _, declaration := range locals {
		name := declaration.AsVariableDeclaration().Name().Text()
		if _, isParameter := parameterNames[name]; isParameter {
			continue
		}
		if previous, seen := declaredOf[name]; seen {
			// a name declared TWICE keeps the last declaration's reading; a
			// pair that disagrees about SHAPE has no one slot family, so
			// both lose their flattening and the name stays whole
			_, wasRecord := objectLocals[previous]
			_, isRecord := objectLocals[declaration]
			_, wasArray := arrayLocals[previous]
			_, isArray := arrayLocals[declaration]
			_, wasCollection := collectionLocals[previous]
			_, isCollection := collectionLocals[declaration]
			if wasRecord != isRecord || wasArray != isArray ||
				wasCollection != isCollection {
				delete(objectLocals, previous)
				delete(objectLocals, declaration)
				delete(arrayLocals, previous)
				delete(arrayLocals, declaration)
				delete(collectionLocals, previous)
				delete(collectionLocals, declaration)
			}
		} else {
			order = append(order, name)
		}
		declaredOf[name] = declaration
	}
	var out []bodySlot
	for _, name := range order {
		declared, has := declaredOf[name]
		if !has {
			out = append(out, bodySlot{Name: name, Sort: BindingKindUnknown, TypeofTag: TypeofTagNone})
			continue
		}
		if local, flattened := objectLocals[declared]; flattened {
			for _, key := range local.Keys {
				out = append(out, bodySlot{
					Name:      key.SlotName,
					Sort:      ObjectLocalKeySort(key),
					TypeofTag: ObjectLocalKeyTypeof(key),
				})
			}
			continue
		}
		if local, flattened := arrayLocals[declared]; flattened {
			out = append(out,
				bodySlot{Name: local.LenSlotName, Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
				bodySlot{Name: local.ElemSlotName, Sort: ArrayElementSort(local), TypeofTag: ArrayElementTypeof(local)},
			)
			continue
		}
		if local, flattened := collectionLocals[declared]; flattened {
			for _, slot := range MapLocalSlots(local) {
				out = append(out, bodySlot{
					Name:      slot.Name,
					Sort:      slot.Sort,
					TypeofTag: slot.TypeofTag,
				})
			}
			continue
		}
		out = append(out, bodySlot{
			Name:      name,
			Sort:      LocalSortResolved(c, declared),
			TypeofTag: LocalTypeof(declared),
		})
	}
	// the destructured names last: an object pattern's each wearing its
	// leaf's sort, an array pattern's each unknown-sorted (no leaf
	// distinguishes a position). A name some other local already claimed
	// keeps that local's slot — one name, one slot.
	held := map[string]struct{}{}
	for _, slot := range out {
		held[slot.Name] = struct{}{}
	}
	for _, pattern := range patterns {
		for _, slot := range destructuredSlotsOf(pattern, objectLocals) {
			if _, already := held[slot.Name]; already {
				continue
			}
			if _, isParameter := parameterNames[slot.Name]; isParameter {
				continue
			}
			held[slot.Name] = struct{}{}
			out = append(out, slot)
		}
	}
	return out
}

// A for-of element binding arrives with the other locals (CollectLocals
// walks into the initializer), and neither flattening recognizer admits
// it: it has no initializer, so it is neither an object literal nor an
// array literal. It therefore takes an ordinary whole-name slot, which
// is exactly what ArrayForOfLowering writes the per-pass element into.

// capturedSlot is one READ-ONLY capture an arrow argument closes over:
// the name it is spelled under in the enclosing body, and the sort and
// typeof evidence its caller slot wears. Each one becomes an EXTRA
// entry of the arrow's summary, laid out immediately after the declared
// parameters — so entry k for k < len(parameters) is the k-th
// parameter, and entry len(parameters)+j is the j-th capture, in the
// order the free-variable scan reported them (source order of first
// read). The call site binds each to a `var` of the caller slot the
// name resolves to, which is why the layout has to be the scan's own
// deterministic order and not a map's.
type capturedSlot struct {
	Name      string
	Sort      BindingKind
	TypeofTag TypeofTag
	// Written: the closure's body ASSIGNS this captured name. The entry
	// still enters from the caller's own slot — a captured `settled` IS
	// the caller's `settled`, read before it is written — and the row
	// additionally rides out in BundleEntries so the call site maps its
	// EXIT back onto that same caller slot.
	//
	// This is the one bit that turns a capture from a value passed in
	// into a place written through, and it is why a written capture is
	// laid out exactly as a record-parameter leaf is: both are entries
	// whose exits belong to a slot the caller already holds.
	Written bool
	// Members: an OBJECT capture's leaves, in the census's own order.
	//
	// A capture with no members is the SCALAR one above — one entry
	// under the name, carrying the name's own value. A capture WITH
	// members is a bundle: no entry stands for the name itself, and one
	// entry stands for each leaf, spelled
	// "#capture.<name>.<member>". That is the record-parameter shape
	// applied to a capture, and it is laid out the same way for the same
	// reason — the caller already holds a slot per leaf, so each leaf's
	// value comes in from that slot and each moved leaf's exit goes back
	// to it.
	//
	// Sort and TypeofTag above belong to the SCALAR case only. A
	// bundle's evidence is per leaf, so it rides in the member rows.
	Members []capturedLeaf
	// MethodCalls: the members this closure calls AS METHODS on the
	// capture. MethodWrites is the subset the callee resolution found
	// MAY move the receiver — nil means the resolution was not
	// performed at all, which the layout reads as "every call may
	// move", the doubt direction.
	MethodCalls  []string
	MethodWrites map[string]struct{}
}

// capturedLeaf is ONE member of an object capture: the member name, the
// evidence the caller's leaf slot wears, and whether the closure moves
// it.
//
// Written here means the same thing it means on a record-parameter leaf
// row: the closure assigned this member directly, or code the closure
// handed the object to may have moved it. Either way the caller takes
// the leaf's exit back rather than keeping its own value.
type capturedLeaf struct {
	Member    string
	Sort      BindingKind
	TypeofTag TypeofTag
	Written   bool
}

// capturedSlotName is the BundleEntries path a capture row rides under.
//
// The path vocabulary already spells two rooted families — "this.<field>"
// for a method's receiver bundle, "<holder>.<member>" for a record or
// class-typed parameter's leaves — and both are read back by splitting on
// their root. A capture is neither: it is the caller's OWN name, spelled
// exactly as the caller spells it, with no holder in front. So it takes
// its own prefix rather than borrowing a rooted one, which keeps the
// readers total — bundleRetsAndArgs asks for "this.", bundleParamRetsAndArgs
// asks for a holder, and neither can mistake a capture row for its own.
func capturedSlotName(name string) string { return "#capture." + name }

// capturedNameOfSlot is capturedSlotName read backwards: the caller name
// a capture row stands for, and whether the path is a capture row at all.
//
// An OBJECT capture's leaf rides under "#capture.<name>.<member>", so
// this answers "<name>.<member>" for one — the whole spelling below the
// prefix, which is exactly the key the call site's write-back map holds
// its leaf slots under. One reader serves both kinds of row.
func capturedNameOfSlot(path string) (string, bool) {
	if !strings.HasPrefix(path, "#capture.") {
		return "", false
	}
	return strings.TrimPrefix(path, "#capture."), true
}

// capturedLeafSlotName is the entry name ONE leaf of an OBJECT capture
// rides under: "#capture.disconnectSource.writableEnded".
//
// The record-parameter leaves are spelled "<holder>.<member>" with no
// prefix, because a parameter's holder is a name the callee declared and
// no caller slot competes for it. A capture's holder is the CALLER's own
// name, so an unprefixed "stream.writableEnded" would be exactly the
// spelling the caller's own flattened local already wears — one string
// standing for two different bodies' slots. The prefix keeps the two
// apart, and it is the same prefix the scalar capture rows wear, so one
// reader recognizes both kinds.
func capturedLeafSlotName(name string, member string) string {
	return "#capture." + name + "." + member
}

// lowerSummaryBody lowers a declaration's whole body for the summary
// compiler: the parameter slots first (the compiler's arity), then the
// locals' slots, then the done flag and the result slot, with the
// callee table the body's call statements built. LowerSummaryBody
// (kernel_summaries.go) is the memoized door in front of it.
//
// Total-or-decline, exactly as every other lowering here: a body that
// leaves the grammar answers false and the caller keeps its existing
// route.
func lowerSummaryBody(ctx *FlowContext, declaration *ast.Node) (LoweredSummary, bool) {
	return lowerSummaryBodyWithCaptures(ctx, declaration, nil, nil)
}

// lowerArrowSummary lowers an ARROW (or function expression) ARGUMENT
// closure-converted: its declared parameters first, then one entry per
// READ-ONLY capture in the scan's order, then the locals, the done flag
// and the result slot. Everything past the extra entries is the
// ordinary body lowering — a capture is just another entry as far as
// the compiled summary is concerned, which is exactly why closure
// conversion needs nothing new kernel-side.
//
// The declared parameters wear the SITE's sorts rather than the
// declaration's annotations. A top-level declaration's summary must read
// sorts from its own annotations, since it quantifies over callers a
// lowering cannot see; an arrow argument has exactly ONE call site, and
// that site fills entry 0 with a `var` of the receiver's element slot
// whose sort the caller's layout already carries. Reading the sort off
// the annotation instead would make every unannotated `x => x + 1`
// unknown-sorted and decline its own arithmetic — the parameter would be
// the one entry whose sort the site knows and the summary refuses. The
// captures already ride their caller slots' sorts for the same reason;
// this puts the parameters on the same footing.
//
// The async gate is NOT consulted here beyond what summaryLowerable
// says: a lowered async body's #ret holds the SETTLED inner value (the
// ret-as-inner convention), so an async arrow converts exactly like a
// sync one and the awaiting site adds nothing.
func lowerArrowSummary(
	ctx *FlowContext,
	arrow *ast.Node,
	parameters []parameterSlotSort,
	captures []capturedSlot,
) (LoweredSummary, bool) {
	return lowerSummaryBodyWithCaptures(ctx, arrow, parameters, captures)
}

// parameterSlotSort is the sort and typeof evidence ONE declared
// parameter entry wears when the call site knows them. A nil entry list
// (the declaration route) leaves every parameter reading its own
// annotation.
type parameterSlotSort struct {
	Sort      BindingKind
	TypeofTag TypeofTag
}

// lowerSummaryBodyWithCaptures is the one lowering both doors share:
// nil parameters and nil captures is the plain declaration route, a
// supplied pair is the closure-converted arrow route. The arrow route
// comes through lowerArrowSummary, which records under the ARROW node —
// the same declaration identity the outcome store keys by.
//
// THE OUTCOME IS RECORDED HERE, and only here: this is the one place a
// body's fate is known. Three answers, one per body:
//
//   - DECLINED, naming the first CONSTRUCT the lowering refused ("a
//     generator body", "a binding-pattern parameter") — the reason the
//     worker returned;
//   - POROUS, naming the first construct that HAVOCKED, where the
//     lowering succeeded but context.FirstHavoc is non-empty;
//   - COMPLETE otherwise — the lowering read every statement.
//
// The outcome store ranks a later record against the one it holds
// (RecordSummaryOutcome), so the fixpoint's re-lowerings never talk a
// settled body back down.
func lowerSummaryBodyWithCaptures(
	ctx *FlowContext,
	declaration *ast.Node,
	parameterSorts []parameterSlotSort,
	captures []capturedSlot,
) (LoweredSummary, bool) {
	summary, havoc, declined, ok := lowerSummaryBodyReporting(ctx, declaration, parameterSorts, captures)
	name := summaryBodyName(declaration)
	if !ok {
		// A declaration with NO BODY is not a body: an overload signature
		// stands in front of the implementation that follows it, and an
		// abstract member stands in front of the subclasses that supply
		// it. Neither has statements for the lowering to read, so neither
		// is a body the outcome store should hold a row for — recording
		// one puts scaffolding in the denominator and then declines it.
		// The implementation and the subclass bodies record their own.
		if isBodylessSignature(declaration) {
			return LoweredSummary{}, false
		}
		RecordSummaryOutcome(declaration, name, SummaryDeclined, declined)
		return LoweredSummary{}, false
	}
	if havoc != "" {
		RecordSummaryOutcome(declaration, name, SummaryPorous, havoc)
		return summary, true
	}
	RecordSummaryOutcome(declaration, name, SummaryComplete, "")
	return summary, true
}

// isBodylessSignature answers whether a declaration is a SIGNATURE
// rather than a body: a function, method, or constructor declaration
// with no body at all.
//
// TypeScript spells two of these. An OVERLOAD signature sits directly in
// front of the implementation that carries the statements — `create(a):
// T;` twice, then `create(a, b?, c?): T { … }` — and the implementation
// is the body every call really runs. An ABSTRACT member (or a member of
// an ambient class or an interface) has no implementation in this file
// at all; the bodies live in the subclasses, which are declarations of
// their own.
//
// Either way there are no statements here to read, so the enumeration
// counts the implementation once instead of counting each signature and
// then declining it for the thing it never had.
func isBodylessSignature(declaration *ast.Node) bool {
	if declaration == nil || declaration.Body() != nil {
		return false
	}
	switch declaration.Kind {
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration,
		ast.KindConstructor, ast.KindMethodSignature,
		ast.KindGetAccessor, ast.KindSetAccessor:
		return true
	}
	return false
}

// summaryBodyName spells a lowered body the way the report spells
// contracts: its declared name, or "" for an anonymous arrow or function
// expression — the outcome store keys on the NODE, so an unnamed body
// still has one record; the name is only what the tally prints.
func summaryBodyName(declaration *ast.Node) string {
	if declaration == nil {
		return ""
	}
	name := declaration.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return ""
	}
	return name.Text()
}

// lowerSummaryBodyReporting is the lowering itself. Beyond the summary
// and its ok flag it answers TWO strings, at most one of them non-empty:
// `havoc` names the first construct that havocked on a lowering that
// SUCCEEDED, and `declined` names the construct the lowering refused.
// Each decline return below names the CONSTRUCT it refused, never a
// category — the tally is read to find out what to build next, so
// "a generator body" is worth having and "unsupported" is not.
func lowerSummaryBodyReporting(
	ctx *FlowContext,
	declaration *ast.Node,
	parameterSorts []parameterSlotSort,
	captures []capturedSlot,
) (summary LoweredSummary, havoc string, declined string, ok bool) {
	if !summaryLowerable(declaration) {
		if declaration != nil && declaration.Body() == nil {
			return LoweredSummary{}, "", "a body-less declaration", false
		}
		return LoweredSummary{}, "", "a generator body", false
	}
	body := declaration.Body()
	kernel := EngineKernelHeld()
	if kernel == nil {
		return LoweredSummary{}, "", "no kernel held", false
	}
	// parameters: plain identifiers, no defaults, no rest. A RECORD
	// parameter — an inline type literal, or a named interface or type
	// alias the context resolves — EXPANDS to one entry per member
	// (SummaryParameterEntriesIn), so the entry vector is no longer
	// one-to-one with the declared parameters and the arrow route's
	// per-parameter sorts are indexed by DECLARATION position while the
	// entry vector runs ahead of it.
	//
	// The context threads down to the expansion HERE, and this is the only
	// seam that holds one: the resolution it performs is remembered under
	// the parameter node, so the ctx-less readings the call sites take
	// answer the same member list (recordParamMembersIn's memo).
	parameters := declaration.Parameters()
	paramNames := make([]string, 0, len(parameters))
	paramSorts := make([]BindingKind, 0, len(parameters))
	paramTypeofs := make([]TypeofTag, 0, len(parameters))
	// the bundle rows the summary rides out with: a record parameter's
	// leaves and, below, the method's this-fields. Filled in slot order,
	// which is the order the entries are appended in.
	var bundleEntries []BundleEntry
	// the leaf spellings ("parentRect.width") of every record parameter
	// this body HANDS OVER whole. They join the havoc vector below, so the
	// statements that run code cannot believe a leaf across the hand-over.
	var handOverHavocNames []string
	// the DEFAULTED parameters' slots, remembered on the single-entry
	// path and read by the prelude below, which applies each default
	// under a definedness branch — the runtime's own rule: undefined,
	// and only undefined, takes the default
	type defaultedParameterSlot struct {
		Slot        int
		Initializer *ast.Node
	}
	var defaultedSlots []defaultedParameterSlot
	for index, parameter := range parameters {
		entries, entriesOk := SummaryParameterEntriesIn(ctx, parameter)
		if !entriesOk {
			return LoweredSummary{}, "", declinedParameterConstruct(parameter), false
		}
		// a BINDING-PATTERN parameter: its entries are the BOUND names,
		// each an ordinary scalar slot the call sites fill from the
		// argument object's member (entry.Key). Before the record branch,
		// which reads the same annotation but keys by member order — the
		// pattern's order and subset are the entries' own.
		if pd := parameter.AsParameterDeclaration(); pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) {
			if index < len(parameterSorts) {
				return LoweredSummary{}, "", "a binding-pattern parameter of an arrow argument", false
			}
			for _, entry := range entries {
				paramNames = append(paramNames, entry.Name)
				paramSorts = append(paramSorts, entry.Sort)
				paramTypeofs = append(paramTypeofs, entry.TypeofTag)
			}
			continue
		}
		if members, expanded := recordParamMembersIn(ctx, parameter); expanded {
			// what an EXPANDED parameter's own name is used for, apart from
			// reading its declared members.
			//
			// A READ of the whole record — a spread (`{ ...p, y: 1 }`), a
			// `return p` — copies the fields out and hands no reference this
			// body stores through, so every leaf keeps its value and the
			// body lowers. The whole-name expression itself takes the opaque
			// floor its own route gives it.
			//
			// A HAND-OVER (`f(p)`, `q = p`) lowers too, on a PAIR of moves
			// that are only sound together. Inside, every leaf of this
			// parameter is havocked at each code-running statement
			// (handOverHavocNames below, which rides the same
			// CaptureHavocSlots vector a method-calling capture rides), so
			// nothing here believes a leaf across the hand-over. Outside, each
			// leaf row goes out Written, and a caller that filled those leaves
			// from its own flattened record local takes them back through the
			// call statement's rets (recordParamRets, ir_summary_call.go) — so
			// nobody, in either body, is left believing a stale slot.
			use, writtenMembers := recordParameterUseOf(
				body, parameter.AsParameterDeclaration().Name().Text(), members)
			if use == recordParameterUnreadable {
				return LoweredSummary{}, "", "a whole-record parameter use", false
			}
			handedOver := use == recordParameterEscapesWhole
			// the arrow route fills ONE entry per declared parameter with a
			// site sort, which an expanded parameter has no single entry for
			if index < len(parameterSorts) {
				return LoweredSummary{}, "", "a record parameter of an arrow argument", false
			}
			// the leaf SLOT NAMES this body writes directly ("p.lo"), read
			// off the member list so the entry loop below and the use scan
			// agree by spelling rather than by position
			writtenSlotNames := map[string]struct{}{}
			for _, member := range members {
				if _, moved := writtenMembers[member.Key]; moved {
					writtenSlotNames[member.SlotName] = struct{}{}
				}
			}
			for _, entry := range entries {
				// a record parameter's leaf is a bundle entry the call site may
				// have to map back, and Written is now the union of TWO ways a
				// leaf moves:
				//
				//   - the HAND-OVER bit — the body passed the whole record to
				//     code, so any leaf may have been moved by that code;
				//   - a DIRECT MEMBER WRITE (`p.lo = 1`) this body performs
				//     itself, which lowers as an ordinary assignment onto the
				//     leaf's own slot.
				//
				// Both carry the movement into the caller through the same
				// rets threading; a leaf that is neither keeps its value, and
				// the caller goes on believing it.
				_, writtenHere := writtenSlotNames[entry.Name]
				bundleEntries = append(bundleEntries, BundleEntry{
					Path:    entry.Name,
					Index:   len(paramNames),
					Written: handedOver || writtenHere,
				})
				if handedOver {
					handOverHavocNames = append(handOverHavocNames, entry.Name)
				}
				paramNames = append(paramNames, entry.Name)
				paramSorts = append(paramSorts, entry.Sort)
				paramTypeofs = append(paramTypeofs, entry.TypeofTag)
			}
			continue
		}
		// an ARRAY-TYPED parameter takes the two slots a flattened array
		// local takes, "ids.len" and "ids.elem", in the parameter's own slot
		// position. The pair comes from SummaryParameterEntriesIn — the same
		// entries list the call sites walk — so the caller's argument vector
		// and this layout stay one answer about how many entries the
		// parameter is worth.
		//
		// No bundle row rides out: the two slots hold a length and the JOIN
		// of the elements, not fields of the caller's object, and nothing a
		// caller spells maps onto them the way "q.lo" maps onto a record
		// leaf. The values enter from the entry state and go nowhere back.
		if _, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
			// the arrow route fills ONE entry per declared parameter with a
			// site sort, which the two-slot pair has no single entry for
			if index < len(parameterSorts) {
				return LoweredSummary{}, "", "an array parameter of an arrow argument", false
			}
			for _, entry := range entries {
				paramNames = append(paramNames, entry.Name)
				paramSorts = append(paramSorts, entry.Sort)
				paramTypeofs = append(paramTypeofs, entry.TypeofTag)
			}
			continue
		}
		// a CLASS-TYPED parameter is a slot bundle: the fields its body
		// READS become entries spelled "wrapper.<field>", after whatever
		// expansions came before, in the census's declaration order. The
		// census is the one report the call sites read back, so the rows
		// ride out in BundleEntries with the write flags it found.
		if _, census, _, isBundle := BundleParamCensus(ctx, body, parameter); isBundle && census.Believable() {
			// the arrow route fills ONE entry per declared parameter with a
			// site sort, which an expanded bundle has no single entry for
			if index < len(parameterSorts) {
				return LoweredSummary{}, "", "a class-typed parameter of an arrow argument", false
			}
			written := map[string]struct{}{}
			for _, field := range census.Writes {
				written[field.SlotName] = struct{}{}
			}
			for _, field := range census.Reads {
				_, isWritten := written[field.SlotName]
				bundleEntries = append(bundleEntries, BundleEntry{
					Path:    field.SlotName,
					Index:   len(paramNames),
					Written: isWritten,
				})
				paramNames = append(paramNames, field.SlotName)
				paramSorts = append(paramSorts, field.Sort)
				paramTypeofs = append(paramTypeofs, field.TypeofTag)
			}
			if len(census.Reads) > 0 {
				continue
			}
		}
		if pd := parameter.AsParameterDeclaration(); pd.Initializer != nil {
			defaultedSlots = append(defaultedSlots, defaultedParameterSlot{
				Slot:        len(paramNames),
				Initializer: pd.Initializer,
			})
		}
		paramNames = append(paramNames, entries[0].Name)
		// the site's sort where the arrow route supplied one, the
		// declaration's own annotation otherwise. A supplied sort is what
		// the entry the site fills already wears, so the summary quantifies
		// over exactly the values that entry can take.
		if index < len(parameterSorts) {
			paramSorts = append(paramSorts, parameterSorts[index].Sort)
			paramTypeofs = append(paramTypeofs, parameterSorts[index].TypeofTag)
			continue
		}
		paramSorts = append(paramSorts, entries[0].Sort)
		paramTypeofs = append(paramTypeofs, entries[0].TypeofTag)
	}
	// the captures ride as EXTRA entries immediately after the declared
	// parameters, in the scan's own order — the call site fills entry
	// len(parameters)+j with a var of the caller slot capture j resolved
	// to, so the two orders must agree exactly. A capture whose name a
	// parameter already claims is the parameter's, not the capture's:
	// the inner binding shadows, and the scan never reported it free.
	//
	// A WRITTEN capture takes one more thing: a bundle row, so the call
	// site maps its exit back onto the caller's own slot. THE ROW AND THE
	// ENTRY ARE ALLOCATED IN ONE STEP HERE, and that is the lockstep
	// discipline the whole write-back machinery rests on — the row's
	// Index is `len(paramNames)` read at the moment the entry is
	// appended, so the row can never name a position the entry did not
	// take. Every other bundle family in this layout (the record leaves
	// above, the this-fields below) allocates the same way for the same
	// reason: one allocator, and every seam that reads the rows reads
	// what this loop wrote rather than re-deriving an index of its own.
	//
	// An OBJECT capture takes the SAME step, once per leaf: the leaf's
	// row and the leaf's entry are appended together, so a leaf row can
	// never name a position its entry did not take either. The lockstep
	// argument is the whole argument, and extending it to leaves is
	// extending the argument rather than adding a second one — the loop
	// below still writes `len(paramNames)` at the moment of the append,
	// and there is still exactly one allocator.
	declaredCount := len(paramNames)
	// an object capture's leaf slots, by the spelling the body reads
	// them under — read below to put them in the havoc vector where a
	// method call on the capture may move them
	captureLeafSlots := map[string]int{}
	for _, capture := range captures {
		shadowed := false
		for _, name := range paramNames[:declaredCount] {
			if name == capture.Name {
				shadowed = true
				break
			}
		}
		if shadowed {
			return LoweredSummary{}, "", "a capture shadowed by a parameter", false
		}
		if len(capture.Members) > 0 {
			for _, leaf := range capture.Members {
				// the ENTRY is spelled the way the BODY reads the leaf —
				// "stream.writableEnded", which is what SpelledNameOf answers
				// for the member access, so the statement walk resolves it
				// through slotIndexOfName like any other slot. The ROW is
				// spelled "#capture.stream.writableEnded", because a row is
				// read at the CALL SITE, where "stream.writableEnded" is
				// already the caller's own leaf and the two must not collide.
				// The scalar capture rows keep the same two spellings for the
				// same reason.
				spelled := capture.Name + "." + leaf.Member
				if leaf.Written {
					bundleEntries = append(bundleEntries, BundleEntry{
						Path:    capturedLeafSlotName(capture.Name, leaf.Member),
						Index:   len(paramNames),
						Written: true,
					})
				}
				captureLeafSlots[spelled] = len(paramNames)
				paramNames = append(paramNames, spelled)
				paramSorts = append(paramSorts, leaf.Sort)
				paramTypeofs = append(paramTypeofs, leaf.TypeofTag)
			}
			continue
		}
		if capture.Written {
			bundleEntries = append(bundleEntries, BundleEntry{
				Path:    capturedSlotName(capture.Name),
				Index:   len(paramNames),
				Written: true,
			})
		}
		paramNames = append(paramNames, capture.Name)
		paramSorts = append(paramSorts, capture.Sort)
		paramTypeofs = append(paramTypeofs, capture.TypeofTag)
	}
	// the `this` bundle rides as EXTRA entries after the declared
	// parameters — the same ground the arrow route's captures take, which
	// is why the two are EXCLUSIVE: only a method has a this bundle, and
	// only an arrow or function expression carries captures, so no
	// declaration ever lays out both. The assertion states it rather than
	// leaving the two silently sharing indices.
	bundle := thisBundleOf(ctx, declaration)
	if bundle.Expanded && len(captures) > 0 {
		return LoweredSummary{}, "", "a this bundle beside arrow captures", false
	}
	for _, entry := range bundle.Entries {
		_, isWritten := bundle.Written[entry.Name]
		bundleEntries = append(bundleEntries, BundleEntry{
			Path:    entry.Name,
			Index:   len(paramNames),
			Written: isWritten,
		})
		paramNames = append(paramNames, entry.Name)
		paramSorts = append(paramSorts, entry.Sort)
		paramTypeofs = append(paramTypeofs, entry.TypeofTag)
	}
	// a concise arrow body IS a single return
	var statements []*ast.Node
	if ast.IsBlock(body) {
		statements = append(statements, body.AsBlock().Statements.Nodes...)
	} else {
		statements = append(statements, syntheticReturnStatement(body))
	}
	// the collection no longer declines a body for what it cannot lay out
	// — a nested function is SKIPPED (its declarations are the inner
	// function's) and an array pattern's names are collected unknown-
	// sorted. Either way the statement holding the construct lowers by
	// its own route or by the havoc floor, so the body keeps its route
	// and its other statements keep their knowledge. The ok flag stays in
	// the signature because ir_inline_call.go reads it, and a false
	// answer there would still be a decline.
	var locals []*ast.Node
	var patterns []*ast.Node
	if ast.IsBlock(body) {
		collected, collectedPatterns, collectedOk := collectSummaryLocals(body)
		if !collectedOk {
			return LoweredSummary{}, "", "a body the local collection does not read", false
		}
		locals, patterns = collected, collectedPatterns
	}
	parameterNames := map[string]struct{}{}
	for _, name := range paramNames {
		parameterNames[name] = struct{}{}
	}
	// an EXPANDED parameter's entries are spelled "p.lo", so the HOLDER
	// name is not among them; a local named `p` would then take its own
	// slot beside the leaves. (Such a body already declined above — the
	// declaration's own `p` is a whole-name occurrence the use scan
	// refuses — so this only keeps the two readings agreeing.)
	for _, parameter := range parameters {
		// a BINDING-PATTERN parameter has no holder name to reserve — its
		// entries ARE the bound names, already in parameterNames above
		name := parameter.AsParameterDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) {
			continue
		}
		if _, expanded := recordParamMembersIn(ctx, parameter); expanded {
			parameterNames[name.Text()] = struct{}{}
		}
		// an ARRAY parameter's entries are spelled "ids.len"/"ids.elem", so
		// the holder is not among them either — the same reservation, for
		// the same reason: a local named `ids` would otherwise lay a second
		// slot family under one name.
		if _, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
			parameterNames[name.Text()] = struct{}{}
		}
	}
	var slotChecker *checker.Checker
	if ctx != nil && ctx.P != nil {
		slotChecker = ctx.P.Checker
	}
	slots := localSlotsIn(ctx, slotChecker, body, locals, patterns, parameterNames)
	// MUTABLE vectors: composition allocates fresh slots past #ret
	bindings := append([]string{}, paramNames...)
	sorts := append([]BindingKind{}, paramSorts...)
	typeofs := append([]TypeofTag{}, paramTypeofs...)
	for _, slot := range slots {
		bindings = append(bindings, slot.Name)
		sorts = append(sorts, slot.Sort)
		typeofs = append(typeofs, slot.TypeofTag)
	}
	bindings = append(bindings, "#done", "#ret")
	sorts = append(sorts, BindingKindNumber, BindingKindUnknown)
	typeofs = append(typeofs, TypeofTagNumber, TypeofTagNone)
	doneIndex := len(bindings) - 2
	retIndex := len(bindings) - 1
	// THE RETURNED VALUE'S MEMBERS. A body returning an object or array
	// LITERAL takes one slot per member beside the scalar #ret, the way a
	// record parameter takes one per member — the return lowering writes
	// each member's own effect into its own slot, and the call sites
	// rebuild the value from the several exits. The rows ride out in
	// RetMembers so the layout's answer about WHICH slot is which member
	// is the one every consumer reads.
	//
	// The slots come after #ret, so #done and #ret keep the indices every
	// existing reader computes for them and nothing about the scalar route
	// moves.
	retMemberSlots, retShape := returnedLiteralShape(body)
	var retMembers []RetMemberEntry
	for _, slot := range retMemberSlots {
		retMembers = append(retMembers, RetMemberEntry{
			Name:  retMemberNameOfSlot(slot.Name),
			Index: len(bindings),
		})
		bindings = append(bindings, slot.Name)
		sorts = append(sorts, slot.Sort)
		typeofs = append(typeofs, slot.TypeofTag)
	}
	if len(bindings) > summarySlotBudget {
		return LoweredSummary{}, "", "a body past the slot budget", false
	}
	table := &SummaryTableBuilder{}
	context := &LoweringContext{
		Bindings: bindings,
		Sorts:    sorts,
		Typeofs:  typeofs,
		Narrow:   kernel.Narrow,
		Result:   &LoweringResult{Done: doneIndex, Ret: retIndex},
		ResolveCallee: func(callee *ast.Node) *ast.Node {
			called := ContractOf(ctx, callee)
			if called == nil || !summaryLowerable(called.Declaration) {
				return nil
			}
			return called.Declaration
		},
		Inlining:     map[*ast.Node]struct{}{declaration: {}},
		Flow:         ctx,
		SummaryTable: table,
		// the returned value's member slots, so the return arm writes each
		// member into its own slot rather than writing the whole literal
		// off as unknown
		RetShape:   retShape,
		RetMembers: retMembers,
	}
	// an ESCAPING receiver is the ONE whole decline of the expansion, and
	// it is POROUS rather than declined: the body still lowers, its
	// this-reads simply find no slot and hit the opaque floor. The note
	// goes in before the statements lower so it names the earliest reason
	// the body stopped being read whole — the receiver left the lowering's
	// sight before any statement could.
	if bundle.Escaped {
		NoteFirstHavoc(context, "this escapes")
	}
	// a method-calling capture's havoc set, resolved to slot indices —
	// non-empty puts the statement walk in havoc mode. A havocked FIELD with
	// no slot (a write-only field the layout gave no entry) needs none:
	// no slot means no belief to invalidate.
	for _, havocName := range bundle.CaptureHavocNames {
		if slot, held := slotIndexOfName(context, havocName); held {
			context.CaptureHavocSlots = append(context.CaptureHavocSlots, slot)
		}
	}
	// a HANDED-OVER record parameter's leaves ride the same vector: the
	// body passed the whole object to code, so any code-running statement
	// may have moved every leaf and none of them may be believed across
	// one. The rows also went out Written, so the caller takes the moved
	// values back through the call statement's rets rather than keeping
	// what it sent.
	for _, havocName := range handOverHavocNames {
		if slot, held := slotIndexOfName(context, havocName); held {
			context.CaptureHavocSlots = append(context.CaptureHavocSlots, slot)
		}
	}
	// A METHOD CALL ON AN OBJECT CAPTURE — `disconnectSource.removeListener(…)`,
	// `response.end()` — rides the same vector, and this is where the sound
	// reading is written down.
	//
	// WHY HAVOC AND NOT A REFUSAL. The caller's leaf slots are a CLOSED set:
	// the caller flattened exactly these members and believes nothing about
	// the object beyond them. So "the callee may have moved any member" is,
	// for the caller, exactly "every leaf of this capture moved" — a
	// statement the row vocabulary can make, because every leaf already has
	// an entry and a row. Havocking them inside the summary makes the
	// summary's own walk stop believing them from that statement on, and
	// each havocked leaf goes out Written (closureCapturesOf marks it), so
	// the caller reads the exit rather than keeping the value it sent.
	// Refusing instead would be sound too, and strictly weaker: the whole
	// closure would fall back to the write-set havoc, which forgets the same
	// leaves AND every other slot the closure touches.
	//
	// A member the callee writes that the caller never flattened moves
	// nothing anyone believes — no slot on either side spells it — which is
	// what makes the closed set enough.
	//
	// WHERE THE CALLEE RESOLVES AND SAYS UNTOUCHED, nothing is havocked:
	// receiverWritten answers from the callee's own summary
	// (SummaryReceiverEffects), and a body that lowered and wrote no
	// this-field and returned no receiver moved no member of the object it
	// was called on. An unresolved callee, or one whose body declined to
	// lower, answers written — the doubt direction — and takes the havoc.
	for _, capture := range captures {
		if len(capture.Members) == 0 || len(capture.MethodCalls) == 0 {
			continue
		}
		moves := false
		for _, method := range capture.MethodCalls {
			if capture.MethodWrites == nil {
				moves = true
				break
			}
			if _, written := capture.MethodWrites[method]; written {
				moves = true
				break
			}
		}
		if !moves {
			continue
		}
		for _, leaf := range capture.Members {
			if slot, held := captureLeafSlots[capture.Name+"."+leaf.Member]; held {
				context.CaptureHavocSlots = append(context.CaptureHavocSlots, slot)
			}
		}
	}
	// allocate grows the CONTEXT's own vectors, not copies of them: a slot
	// handed out past the initial layout must be readable through
	// context.Sorts at the index it was given, and a Go slice header
	// copied before the growth would not carry it.
	context.Allocate = func(name string, sort BindingKind, typeofTag TypeofTag) (int, bool) {
		if len(context.Bindings) >= summarySlotBudget {
			return 0, false
		}
		context.Bindings = append(context.Bindings, name)
		context.Sorts = append(context.Sorts, sort)
		context.Typeofs = append(context.Typeofs, typeofTag)
		return len(context.Bindings) - 1, true
	}
	// THE CONSTRUCTOR PRELUDE: a constructor's body begins life the
	// runtime already lived — the class's field INITIALIZERS have run,
	// and each PARAMETER PROPERTY holds its argument. Both are ordinary
	// assignments onto the bundle's own slots, emitted ahead of the
	// statements; an initializer the effect grammar cannot spell leaves
	// its slot unknown (never a stale absent), and every touched field
	// joins Written through the census's own store recognition upstream.
	var constructorPrelude []kernelbridge.IrStatement
	if ast.IsConstructorDeclaration(declaration) {
		if classLike := declaration.Parent; classLike != nil && ast.IsClassLike(classLike) {
			for _, member := range classLike.ClassLikeData().Members.Nodes {
				if !ast.IsPropertyDeclaration(member) {
					continue
				}
				property := member.AsPropertyDeclaration()
				if property.Initializer == nil || property.Name() == nil || !ast.IsIdentifier(property.Name()) {
					continue
				}
				slot, has := slotIndexOfName(context, "this."+property.Name().Text())
				if !has {
					continue
				}
				effect, lowered := RhsEffect(context, context.Sorts[slot], property.Initializer)
				if !lowered {
					effect = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}
				}
				constructorPrelude = append(constructorPrelude, kernelbridge.IrStatement{
					Kind: kernelbridge.IrStatementAssign, Target: slot, Effect: effect,
				})
				// A FIELD HOLDING A CLOSURE — `private handler = () => {
				// this.count++ }` — is the one initializer whose slot write is
				// not the end of the story. The prelude ADMITS every field
				// (an unreadable initializer takes unknown rather than
				// declining), so unlike an ordinary statement this one never
				// reaches the havoc floor, and the floor's walk INTO the arrow
				// is what would otherwise havoc the names it writes. Those
				// names are havocked here instead, right after the field's own
				// write: the closure may run at any later time, so nothing
				// after this may believe them. ClosureEscapesTrackedWrite
				// states the boundary rule both sites share.
				if ClosureEscapesTrackedWrite(context, property.Initializer) {
					if written, enumerable := havocSlotsOfStatement(context, property.Initializer); enumerable {
						constructorPrelude = append(constructorPrelude, havocAssignments(written)...)
					}
				}
			}
		}
		for _, parameter := range parameters {
			if !isParameterPropertyDeclaration(parameter) {
				continue
			}
			pd := parameter.AsParameterDeclaration()
			if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) {
				continue
			}
			fieldSlot, hasField := slotIndexOfName(context, "this."+pd.Name().Text())
			if !hasField {
				continue
			}
			effect := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}
			if paramSlot, hasParam := slotIndexOfName(context, pd.Name().Text()); hasParam {
				effect = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: paramSlot}
			}
			constructorPrelude = append(constructorPrelude, kernelbridge.IrStatement{
				Kind: kernelbridge.IrStatementAssign, Target: fieldSlot, Effect: effect,
			})
		}
	}
	// THE DEFAULT PRELUDE: each defaulted parameter applies its default
	// exactly where the runtime does — only when the call left the entry
	// undefined. The branch tests the slot's definedness (the kernel's
	// IrTest.defined, covered by walk_sound), and the else arm assigns
	// the default; a supplied argument walks the empty then arm untouched.
	//
	// The default's VALUE is read where the effect grammar can spell it
	// (`= 0`, `= null`, `= other`), and is UNKNOWN where it cannot —
	// `= new ApplicationConfig()`, `= createContextId()`,
	// `= this.container.getModules()`. Unknown is exactly the opaque
	// call's own admission: the slot's value is unconstrained and the
	// branch structure around it is still the runtime's own, so a
	// supplied argument keeps everything the entry state promised and
	// only the defaulted run loses the value.
	//
	// A default that RUNS code — a `new`, a call, an await — also runs
	// whatever a stored closure of this body can run, so it brackets the
	// capture-havoc set exactly as a code-running statement in the body
	// does (lowering_to_kernel_ir.go's bracketing). The bracket goes
	// OUTSIDE the branch: it must hold on both arms, because the caller
	// chooses which arm runs and neither may be believed across the
	// initializer's code. The whole prelude runs before any statement, so
	// the statement walk's own leading bracket is not enough — nothing
	// has yet forced those slots to forget.
	captureHavocPrelude := map[int]struct{}{}
	for _, slot := range context.CaptureHavocSlots {
		if slot >= 0 {
			captureHavocPrelude[slot] = struct{}{}
		}
	}
	var prelude []kernelbridge.IrStatement
	defaultEffects := map[int]kernelbridge.LoopEffect{}
	for _, defaulted := range defaultedSlots {
		effect, lowered := RhsEffect(context, context.Sorts[defaulted.Slot], defaulted.Initializer)
		// A DEFAULT THAT IS A CALL — `= createContextId()`,
		// `= this.container.getModules()` — is SERVED where the callee has
		// a summary, instead of taking unknown.
		//
		// The earlier refusal said the prelude has no statement stream, and
		// that predates the branch-shaped prelude: the else arm below IS a
		// statement list, which is exactly the position SummaryCallOrHavoc
		// needs, and it writes the call's value into the parameter's own
		// slot. The summary TABLE is live too — `table` and `context` are
		// built above this loop, and SummaryBlobFor builds a callee's blob
		// on demand — so a callee's blob is reachable here on the same
		// terms it is reachable from any body statement.
		//
		// The call goes in the ELSE ARM alone, which is where the runtime
		// runs it: a supplied argument never evaluates the default, so
		// putting the call on the then arm would run code the real run does
		// not. The bracketing around the branch is unchanged and still
		// required — the call runs code, so a stored closure of this body
		// may run inside it.
		var defaultCall []kernelbridge.IrStatement
		if !lowered && context.SummaryTable != nil {
			if head := Unwrapped(defaulted.Initializer); head != nil &&
				(ast.IsCallExpression(head) || ast.IsNewExpression(head)) {
				if served, servedOk := SummaryCallOrHavoc(context, head, defaulted.Slot); servedOk {
					defaultCall = served
				}
			}
		}
		if lowered {
			defaultEffects[defaulted.Slot] = effect
		} else {
			// the default is a construct the effect grammar cannot spell.
			// The slot takes unknown on the arm the default runs on; no
			// value is claimed, so nothing said here is wrong. A SERVED
			// call replaces that unknown outright — the statements it built
			// write the slot themselves.
			effect = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}
		}
		runsCode := StatementRunsCode(defaulted.Initializer)
		if runsCode && len(captureHavocPrelude) > 0 {
			prelude = append(prelude, havocAssignments(captureHavocPrelude)...)
		}
		// A DEFAULT HOLDING A CLOSURE — `cb = () => { this.count++ }` —
		// hands the arrow to whoever the parameter goes on to, and calling
		// it writes this body's names. StatementRunsCode does not see it
		// (building an arrow runs nothing), and this prelude admits every
		// default rather than declining, so the havoc floor's walk into the
		// arrow never happens for it. The names go unknown here instead —
		// inside the same else arm, since only the run that took the default
		// built the closure. ClosureEscapesTrackedWrite states the boundary
		// rule this shares with the census.
		defaultArm := []kernelbridge.IrStatement{{
			Kind:   kernelbridge.IrStatementAssign,
			Target: defaulted.Slot,
			Effect: effect,
		}}
		if len(defaultCall) > 0 {
			// the served call's own statements write the slot; the unknown
			// assign above would only overwrite what the call answered
			defaultArm = defaultCall
		}
		if ClosureEscapesTrackedWrite(context, defaulted.Initializer) {
			if written, enumerable := havocSlotsOfStatement(context, defaulted.Initializer); enumerable {
				defaultArm = append(defaultArm, havocAssignments(written)...)
			}
		}
		prelude = append(prelude, kernelbridge.IrStatement{
			Kind: kernelbridge.IrStatementBranch,
			On:   defaulted.Slot,
			Test: kernelbridge.IrTestDefined,
			Else: defaultArm,
		})
		if runsCode && len(captureHavocPrelude) > 0 {
			prelude = append(prelude, havocAssignments(captureHavocPrelude)...)
		}
	}
	stmts, statementsOk := LowerStatements(context, statements)
	if !statementsOk {
		// the statement walk names the construct it refused ON — the
		// havoc floor's own first-wins report ("throw inside try",
		// "with statement", "labeled break crossing out"). The
		// histogram is the work queue, so its rows name syntax someone
		// can act on; the generic spelling is the fallback for a
		// decline that reached here without naming itself.
		named := DeclinedConstructOf(context)
		if named == "" {
			named = "a statement the lowering does not read"
		}
		return LoweredSummary{}, "", named, false
	}
	// ParamCount counts the ENTRIES the caller fills, not the declared
	// parameters: an expanded record parameter contributes one entry per
	// member, a method's read this-fields one each, and the captures one
	// each. The apply route's "everything past ParamCount enters absent"
	// rule reads this number, so it has to be the entry count or a record
	// parameter's later leaves — and every this-field — would enter absent.
	//
	// FirstHavoc is the set-once field the havoc routes fill: empty means
	// every statement was READ, non-empty names the first construct that
	// was stood in for. The door above turns the two into complete/porous.
	// the preludes run FIRST, in the runtime's own order: defaults land
	// before anything reads a parameter, then a constructor's field
	// initializers and parameter properties, then the body
	if len(constructorPrelude) > 0 {
		stmts = append(constructorPrelude, stmts...)
	}
	if len(prelude) > 0 {
		stmts = append(prelude, stmts...)
	}
	return LoweredSummary{
		Stmts:           stmts,
		ParamCount:      len(paramNames),
		DoneIndex:       doneIndex,
		RetIndex:        retIndex,
		SlotCount:       len(context.Bindings),
		Table:           table.Blobs,
		BundleEntries:   bundleEntries,
		DefaultEffects:  defaultEffects,
		ReturnsReceiver: bundle.ReturnsSelf,
		RetShape:        retShape,
		RetMembers:      retMembers,
	}, context.FirstHavoc, "", true
}

// declinedParameterConstruct names WHICH parameter shape the expansion
// refused — the three SummaryParameterEntriesIn answers false for, each
// spelled as the construct it is rather than as a category.
func declinedParameterConstruct(parameter *ast.Node) string {
	pd := parameter.AsParameterDeclaration()
	if pd.DotDotDotToken != nil {
		return "a rest parameter"
	}
	if pd.Initializer != nil {
		return "a defaulted parameter"
	}
	if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) {
		return "a binding-pattern parameter"
	}
	return "a parameter the expansion does not read"
}

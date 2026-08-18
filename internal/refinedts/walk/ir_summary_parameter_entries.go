// split from ir_summary_body.go — the parameter expansion and its memos

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

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
	// without a context, so it never touches the memo. A checker widens
	// only ONE thing here — a COMPUTED member name resolving to a stable
	// module-level symbol const (scalarMemberListWithCheckerIn's doc) —
	// so a ctx-less reading still answers every non-computed member the
	// same as scalarMemberListOf always has.
	if ast.IsTypeLiteralNode(pd.Type) {
		return scalarMemberListWithCheckerIn(
			checkerOf(ctx), holder, pd.Type.AsTypeLiteralNode().Members.Nodes, nil, true, nil)
	}
	// (b) the named type — whatever a context resolved for this parameter
	// stands for every later reading
	resolvedRecordMembersMu.Lock()
	held, remembered := resolvedRecordMembers[parameter]
	resolvedRecordMembersMu.Unlock()
	if remembered {
		return held, len(held) > 0
	}
	members, expanded := namedTypeMembersOf(ctx, holder, pd.Type, nil)
	if !expanded && ast.IsTypeReferenceNode(pd.Type) {
		// a GENERIC parameter reads through its CONSTRAINT: the constraint
		// is the only member set the annotation promises to every type
		// argument a caller could apply, so it is exactly what may become
		// slots. An unbounded generic promises nothing and stays declined.
		members, expanded = constraintMembersOf(ctx, holder, pd.Type)
	}
	if !expanded && ast.IsTypeReferenceNode(pd.Type) {
		// an ALIAS OF AN INSTANTIATION (`type PolarViewBoxRequired =
		// Required<PolarViewBox>`): the syntax route's alias arm refuses an
		// applied reference behind a name, but GetTypeFromTypeNode resolves
		// through the alias to the same instantiated object type the
		// direct-arguments arm reads, so the checker-authority reader
		// (instantiatedReferenceMembersOf, with its own class/array/budget/
		// own-body guards) answers it. Gated to a reference carrying NO
		// arguments so the direct-arguments case keeps its one route
		// through namedTypeMembersOf.
		reference := pd.Type.AsTypeReferenceNode()
		if reference.TypeArguments == nil || len(reference.TypeArguments.Nodes) == 0 {
			members, expanded = instantiatedReferenceMembersOf(ctx, holder, pd.Type)
		}
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
// A BINDING-PATTERN parameter contributes one entry per bound element
// (precise where the annotation spells the member, TOP-filled where the
// element or the whole pattern is defaulted), and a REST parameter one
// unknown-sorted entry. (false) only where even a name cannot be bound —
// a nested or computed element, a duplicate bound name, an array
// pattern — kept here so the two seams cannot disagree about how many
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
	// fills from, sort the member's own. A member whose own sort is
	// unknown (a richer-typed or nested-literal member) still binds —
	// its entry rides in unknown-sorted (BindingKindUnknown,
	// TypeofTagNone): reads of the bound local answer nothing, which is
	// exactly what is known, the same argument the REST-parameter arm
	// below already makes for its own single unknown-sorted entry.
	// Defaults, rests, computed keys, nested patterns, a member the
	// annotation does not spell, and a duplicate bound name all refuse.
	if pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) && pd.DotDotDotToken == nil && pd.Initializer == nil {
		members, isRecord := recordParamMembersIn(ctx, parameter)
		if !isRecord {
			return nil, false
		}
		// a binding-pattern element binds from a DEPTH-1 member only — its
		// own PropertyName/Name is a single identifier, never a path — so
		// only len(Path) == 1 members are indexed here. A member that
		// expanded to NESTED leaves (`numericAxis: BaseAxisWithScale`
		// reading as "numericAxis.scale.…") has no depth-1 row; its ROOT
		// key is remembered so the element binding it can take a TOP
		// entry below instead of refusing the whole pattern.
		byKey := map[string]recordParamMember{}
		nestedRoots := map[string]struct{}{}
		for _, member := range members {
			if len(member.Path) != 1 {
				nestedRoots[member.Path[0]] = struct{}{}
				continue
			}
			byKey[member.Key] = member
		}
		seen := map[string]struct{}{}
		var out []bodySlot
		for _, element := range pd.Name().AsBindingPattern().Elements.Nodes {
			binding := element.AsBindingElement()
			if binding.Name() == nil || !ast.IsIdentifier(binding.Name()) {
				return nil, false
			}
			bound := binding.Name().Text()
			if _, duplicate := seen[bound]; duplicate {
				return nil, false
			}
			seen[bound] = struct{}{}
			// a REST element binds a fresh object of the remaining members,
			// and a DEFAULTED element binds member-or-default: neither value
			// is one member's own state, so each takes an unknown-sorted
			// TOP-filled entry (bodySlot.TopEntry's doc) instead of refusing
			// the whole pattern — reads of the bound name answer nothing,
			// which is exactly what is known.
			if binding.DotDotDotToken != nil || binding.Initializer != nil {
				out = append(out, bodySlot{
					Name:      bound,
					Sort:      BindingKindUnknown,
					TypeofTag: TypeofTagNone,
					TopEntry:  true,
				})
				continue
			}
			key := bound
			if binding.PropertyName != nil {
				if !ast.IsIdentifier(binding.PropertyName) {
					return nil, false
				}
				key = binding.PropertyName.Text()
			}
			member, declared := byKey[key]
			if !declared {
				// the key names a member that expanded to NESTED leaves: the
				// bound value is the member OBJECT, which no scalar entry
				// spells — a flat entry vector cannot carry its leaf paths
				// (the pattern seams fill by one-segment Key). The element
				// binds an unknown-sorted TOP entry: reads of the bound name
				// answer nothing, which is exactly what is known, and the
				// pattern's scalar siblings keep their precise bindings. A
				// key NO member spells at any depth still refuses.
				if _, isNestedRoot := nestedRoots[key]; isNestedRoot {
					out = append(out, bodySlot{
						Name:      bound,
						Sort:      BindingKindUnknown,
						TypeofTag: TypeofTagNone,
						TopEntry:  true,
					})
					continue
				}
				return nil, false
			}
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
	// a DEFAULTED WHOLE PATTERN (`({ a, b } = {})`): the parameter's value
	// is the argument OR the default object, so no element binds one
	// member's own state — a definite member state would claim what the
	// runtime bound from the default. Every bound name takes an
	// unknown-sorted TOP-filled entry instead (bodySlot.TopEntry), the
	// same claim the rest- and defaulted-ELEMENT arms above make: reads of
	// the bound names answer nothing, which is exactly what is known, and
	// the body lowers instead of refusing. A rest element inside binds its
	// fresh-object name the same way. A nested or non-identifier element
	// still refuses — even a TOP fill needs a name to fill — and so does a
	// duplicate bound name or an empty pattern.
	if pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) && pd.DotDotDotToken == nil && pd.Initializer != nil {
		seen := map[string]struct{}{}
		var out []bodySlot
		for _, element := range pd.Name().AsBindingPattern().Elements.Nodes {
			binding := element.AsBindingElement()
			if binding.Name() == nil || !ast.IsIdentifier(binding.Name()) {
				return nil, false
			}
			bound := binding.Name().Text()
			if _, duplicate := seen[bound]; duplicate {
				return nil, false
			}
			seen[bound] = struct{}{}
			out = append(out, bodySlot{
				Name:      bound,
				Sort:      BindingKindUnknown,
				TypeofTag: TypeofTagNone,
				TopEntry:  true,
			})
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	}
	// an ARRAY-DESTRUCTURED parameter (`([min, max]: [number, number])`,
	// `([min, max]: NumberDomain)` where NumberDomain is a tuple alias) —
	// binds each element POSITIONALLY rather than by member name, so it
	// reads a different annotation shape than the object-pattern arms
	// above: a TUPLE type node names each position's own sort directly —
	// bound precisely, since a fixed tuple GUARANTEES that position is
	// present. An ARRAY type node (`number[]`) names one sort every
	// position shares, but the array may hold fewer elements than the
	// pattern names, so a position past the end is not a promise the
	// annotation makes; no vocabulary at this entry layer carries
	// "present-but-maybe-absent" (bodySlot has no absence flag, unlike
	// recordParamMember's MayBeAbsent — every existing TopEntry row in
	// this function already pairs TopEntry with an unknown sort, and
	// ir_summary_call_statement.go / kernel_summaries.go's own TopEntry
	// arms never read Sort once TopEntry is set), so an array-annotated
	// element takes an unknown-sorted TOP-filled entry instead of a
	// precise one: TOP claims nothing, which is exactly what is known
	// about a position the annotation does not guarantee. No annotation,
	// or an annotation that is neither a tuple nor an array, leaves every
	// element TOP-filled the same way. A rest element or a defaulted
	// element binds TOP too — the same claim the object-pattern arms
	// above make for their own rest/defaulted elements. An ELISION
	// (`[, max]`) binds no name at all, so it contributes no entry — the
	// same skip bindingNames (block_statement.go) gives an elision at a
	// declaration pattern, though (see below) the AST shape that skip
	// checks is not the one an array BINDING PATTERN's elision actually
	// carries. A nested or non-identifier element, or a duplicate bound
	// name, still refuses the whole pattern.
	//
	// The elision's AST shape here is NOT an OmittedExpression, contrary
	// to the block_statement.go precedent this arm was written from:
	// parseArrayBindingElement (internal/parser/parser.go) never emits an
	// OmittedExpression for a missing element in an ARRAY BINDING PATTERN
	// — it always returns a BindingElement, with DotDotDotToken, Name, and
	// Initializer all nil for the missing slot. OmittedExpression is what
	// a missing slot in an array LITERAL parses to
	// (parseArrayLiteralExpression); a binding pattern's own missing slot
	// never takes that shape. So a nil-Named BindingElement here IS the
	// elision, read the same way the object-pattern arms above already
	// read a duplicate/rest/defaulted element — checked before the "no
	// identifier name" refusal so `[a, , c]` does not decline the whole
	// pattern. The IsOmittedExpression check below stays too (harmless,
	// and correct if any caller ever synthesizes one).
	if pd.Name() != nil && ast.IsArrayBindingPattern(pd.Name()) && pd.DotDotDotToken == nil && pd.Initializer == nil {
		tupleSorts, tupleTags, isTuple := tupleTypeNodeElementSorts(pd.Type)
		seen := map[string]struct{}{}
		var out []bodySlot
		topEntry := func(name string) bodySlot {
			return bodySlot{Name: name, Sort: BindingKindUnknown, TypeofTag: TypeofTagNone, TopEntry: true}
		}
		for index, element := range pd.Name().AsBindingPattern().Elements.Nodes {
			if ast.IsOmittedExpression(element) {
				continue
			}
			binding := element.AsBindingElement()
			if binding.Name() == nil {
				// a nameless BindingElement with no rest token and no
				// initializer either is the elision itself (the parser fact
				// above) — skip it exactly as the IsOmittedExpression branch
				// does, WITHOUT skipping `index`, so a later tuple position
				// still lands on its own sort. A nameless element carrying a
				// rest token or an initializer cannot happen syntactically
				// (both require a name to attach to) but is refused rather
				// than silently skipped, same as any other malformed shape
				// this function declines.
				if binding.DotDotDotToken == nil && binding.Initializer == nil {
					continue
				}
				return nil, false
			}
			if !ast.IsIdentifier(binding.Name()) {
				return nil, false
			}
			bound := binding.Name().Text()
			if _, duplicate := seen[bound]; duplicate {
				return nil, false
			}
			seen[bound] = struct{}{}
			if binding.DotDotDotToken != nil || binding.Initializer != nil {
				out = append(out, topEntry(bound))
				continue
			}
			if isTuple && index < len(tupleSorts) && tupleSorts[index] != BindingKindUnknown {
				out = append(out, bodySlot{Name: bound, Sort: tupleSorts[index], TypeofTag: tupleTags[index]})
				continue
			}
			out = append(out, topEntry(bound))
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
	//
	// An array whose ELEMENT is itself a record (local.ElementMembers
	// populated, step 1) widens the entry list from two to 1+N: the len
	// slot unchanged, and one "xs.elem.<member>" entry per member IN
	// PLACE of the single scalar elem entry — each member sorted by its
	// own annotation, exactly as the record-parameter arm above sorts a
	// plain record's members. The COUNT depends on this expansion, which
	// is why arrayParamSlotsIn's own memo (not a fresh reading here) is
	// what both this layout seam and the call-site seam must read: two
	// readings that disagreed on ElementMembers would build entry vectors
	// of different widths for one declaration.
	if local, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
		out := make([]bodySlot, 0, 1+max(1, len(local.ElementMembers)))
		out = append(out, bodySlot{Name: local.LenSlotName, Sort: BindingKindNumber, TypeofTag: TypeofTagNumber})
		if len(local.ElementMembers) > 0 {
			for _, member := range local.ElementMembers {
				out = append(out, bodySlot{
					Name:      member.SlotName,
					Sort:      member.Sort,
					TypeofTag: member.TypeofTag,
				})
			}
			return out, true
		}
		out = append(out, bodySlot{Name: local.ElemSlotName, Sort: ArrayElementSort(local), TypeofTag: ArrayElementTypeof(local)})
		return out, true
	}
	return []bodySlot{{
		Name:      pd.Name().Text(),
		Sort:      declaredParamSort(parameter),
		TypeofTag: declaredParamTypeof(parameter),
	}}, true
}

// unionArmSort reads ONE type node as the sort/typeof it wears — the
// plain keyword, or a literal narrowing of the same primitive: a string
// literal, a numeric literal (negatives through the prefix minus), or
// `true`/`false` (booleans ride the number sort with their own typeof
// evidence, the rule declaredParamSort's keyword case already applies).
// Anything else answers unknown. Used by the tuple reader below for each
// position's own type node. (A sibling reader wiring this into
// declaredParamSort itself was reverted 2026-08-17: a union-sorted
// parameter made switch-body summaries complete, and a complete summary
// serves every caller the JOIN of the arms, shadowing the walk's
// exact-argument replay — the serving-order design has to land first.)
func unionArmSort(branch *ast.Node) (BindingKind, TypeofTag) {
	switch branch.Kind {
	case ast.KindStringKeyword:
		return BindingKindString, TypeofTagString
	case ast.KindNumberKeyword:
		return BindingKindNumber, TypeofTagNumber
	case ast.KindBooleanKeyword:
		return BindingKindNumber, TypeofTagBoolean
	}
	if !ast.IsLiteralTypeNode(branch) {
		return BindingKindUnknown, TypeofTagNone
	}
	literal := branch.AsLiteralTypeNode().Literal
	if ast.IsStringLiteral(literal) {
		return BindingKindString, TypeofTagString
	}
	if ast.IsNumericLiteral(literal) {
		return BindingKindNumber, TypeofTagNumber
	}
	if ast.IsPrefixUnaryExpression(literal) {
		unary := literal.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			return BindingKindNumber, TypeofTagNumber
		}
	}
	if literal.Kind == ast.KindTrueKeyword || literal.Kind == ast.KindFalseKeyword {
		return BindingKindNumber, TypeofTagBoolean
	}
	return BindingKindUnknown, TypeofTagNone
}

// tupleTypeNodeElementSorts reads a FIXED TUPLE type node's own elements
// positionally — the annotation `[number, string]` binds position 0 the
// number sort and position 1 the string sort, mirroring host_type.go's
// "fixed tuple types read positionally" rule (typereading package) at
// the syntax layer this package reads without a checker.
//
// A NAMED tuple element (`[min: number, max: number]`) unwraps to its
// own Type node first — the name is documentation, not a member key a
// binding-pattern element could rename through (array patterns bind
// positionally, never by name). An OPTIONAL or REST element's own
// position is not a promise the annotation makes to every tuple value —
// a shorter tuple can still satisfy an optional-tailed type — so that
// position answers unknown, same as any element whose own type is
// neither a keyword nor a matching literal; the caller TOP-fills those.
//
// The second answer is whether the type node is a tuple at all: a plain
// `[number, number]`, `[min: number, max: number]`, or an alias resolved
// no further than this syntax (`NumberDomain` itself is NOT resolved
// here — this reader is syntax-only, matching every other reader in this
// file that runs without a context) all qualify only when the ANNOTATION
// ITSELF is a TupleTypeNode; a type-alias reference stays unrecognized,
// same gap arrayTypeNodeElementSort leaves for `Array<T>` aliases behind
// a second name.
func tupleTypeNodeElementSorts(typeNode *ast.Node) ([]BindingKind, []TypeofTag, bool) {
	if typeNode == nil {
		return nil, nil, false
	}
	node := typeNode
	if node.Kind == ast.KindParenthesizedType {
		node = node.AsParenthesizedTypeNode().Type
	}
	if node.Kind != ast.KindTupleType {
		return nil, nil, false
	}
	elements := node.AsTupleTypeNode().Elements.Nodes
	sorts := make([]BindingKind, len(elements))
	tags := make([]TypeofTag, len(elements))
	for at, element := range elements {
		held := element
		if ast.IsNamedTupleMember(element) {
			member := element.AsNamedTupleMember()
			if member.DotDotDotToken != nil || member.QuestionToken != nil {
				sorts[at], tags[at] = BindingKindUnknown, TypeofTagNone
				continue
			}
			held = member.Type
		}
		if ast.IsOptionalTypeNode(held) || ast.IsRestTypeNode(held) {
			sorts[at], tags[at] = BindingKindUnknown, TypeofTagNone
			continue
		}
		sorts[at], tags[at] = unionArmSort(held)
	}
	return sorts, tags, true
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
	local, flattened := ArrayParameterOf(ctx, c, body, parameter)
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

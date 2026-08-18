// split from ir_summary_body.go — record parameters: the member reading

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

/* ── record parameters ───────────────────────────────────────────── */

// recordParamMember is one member of a parameter's RECORD annotation —
// an inline type literal, or the interface or type alias a named
// annotation resolves to: the key it is spelled under, the slot name the
// body reads it by ("p.lo"), and the sort and typeof evidence its OWN
// annotation states.
type recordParamMember struct {
	// Key is this member's OWN name — the last segment of Path. "lo" for
	// a flat member, "deep" for a member reached by recursing into a
	// nested type literal (`inner: { deep: number }`'s child). It is the
	// member's identity at ITS OWN level, never the joined path — a
	// consumer that only handles depth-1 members states that explicitly
	// (len(Path) == 1) rather than relying on a dotted Key failing to
	// match a flat lookup by accident.
	Key string
	// Path is this member's full identity from the holder down, in
	// root-to-leaf order — ["inner", "deep"] for the member above,
	// ["lo"] for a flat one. Path's last element is always Key. Every
	// IDENTITY, DEDUP, or USES route (mergeShadowedMembers, the union/
	// intersection merges, recordParameterUseOf's declared set) keys on
	// strings.Join(Path, ".") — the full path — because two members at
	// different depths can share a bare Key ("deep" under two different
	// parents) and only the joined path tells them apart. A FLAT-ONLY
	// route (one that reads only depth-1 members, by the shape it
	// serves) checks len(Path) == 1 and then uses Key for its lookup.
	Path      []string
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
	//
	// A NESTED leaf's MayBeAbsent composes as the WEAKER promise along
	// its whole path: an optional `inner?:` makes every child of inner
	// possibly absent even where the child's own annotation is required,
	// because the child is only there at all when inner is. Absence
	// still rides the entry state, never the sort, at every depth.
	MayBeAbsent bool
	// ArrayPair: this member is one of the two an ARRAY-VALUED element
	// expands to (arrayElementPairMembers) — the inner array's own
	// "len"/"elem" pair — rather than a record member. The flag is what
	// keeps a record member that happens to be spelled "len" or "elem"
	// from being read as the inner pair: the `.length`→".len" mapping
	// and the inner-pair recognizers gate on it.
	ArrayPair bool
}

// scalarMemberListOf reads a list of TYPE ELEMENTS as the member list a
// record parameter expands to, spelled under `holder` ("p.lo"/"p.hi").
//
// Every element must be a PROPERTY SIGNATURE with a plain identifier
// name and no initializer. A computed name, an initializer, a duplicate
// key, or an EMPTY list answers false and the parameter keeps its single
// whole-name slot. An INDEX SIGNATURE is skipped like a method — see the
// index-signature note at its own check below.
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

// scalarMemberListOfIn is scalarMemberListWithCheckerIn with no checker
// to resolve a computed name's stable symbol against — the reading every
// caller that holds no *checker.Checker took before that widening
// existed, kept as its own name so those call sites need no signature
// change. A computed name still SKIPS rather than refusing the list; the
// only thing a nil checker costs is the stable-symbol case, which has
// nothing to resolve against without one.
func scalarMemberListOfIn(
	holder string,
	members []*ast.Node,
	parameterNames map[string]struct{},
	atEntry bool,
) ([]recordParamMember, bool) {
	return scalarMemberListWithCheckerIn(nil, holder, members, parameterNames, atEntry, nil)
}

// scalarMemberListWithCheckerIn is the member reading scalarMemberListOf
// performs, told two things about the position it is reading in.
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
//
// `c` is the checker a COMPUTED type-element name needs to read as a
// STABLE SYMBOL — the same key identity flatKeysOfLiteralWith reads for
// an object literal's computed row (symbolMemberFieldName,
// ir_field_bundles_symbol_keys.go): a plain identifier bound to a
// module-level `const` built by `Symbol()`/`Symbol.for(...)`. A type
// element named `[S]: number` under such a const contributes its leaf
// under the derived `#sym:S` name, exactly as the object-literal row
// does. Any OTHER computed name — a string/numeric literal computed
// name, an expression the checker cannot pin to a stable const —
// SKIPS: it names no leaf and kills nothing, the same skip a method
// signature already takes. It cannot refuse the list, because a
// computed member is not a name any read could ever spell as an
// accounted-for leaf — usesAreAllDeclaredKeySteps and
// recordParameterUseOf both refuse a use whose name is not a declared
// KEY, so nothing unsound can serve past the skip; the difference from
// the OLD refusal is only that the member's SCALAR SIBLINGS keep
// expanding instead of losing their leaves too. A nil checker (the
// ctx-less callers) skips every computed name, which is exactly the
// "any other computed name" case — no widening is lost, only the
// stable-symbol case is unavailable without a checker.
// `visiting` is the DECLARATION cycle guard a NESTED member's own type
// reference is resolved under (nestedMemberLeavesOf) — the declaration
// nodes already on this walk's path, so `interface A { b: B } interface
// B { a: A }` terminates at the revisit by falling back to the unknown
// leaf rather than recursing forever. A top-level call (scalarMemberListOfIn)
// starts the walk with nothing visited yet.
func scalarMemberListWithCheckerIn(
	c *checker.Checker,
	holder string,
	members []*ast.Node,
	parameterNames map[string]struct{},
	atEntry bool,
	visiting []*ast.Node,
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
		// an INDEX SIGNATURE (`[k: string]: number`) names no single leaf
		// either — `p.mid` is not "the index signature", it is an
		// undeclared member, and the uses discipline already refuses any
		// read of a name no leaf holds (recordParameterUseOf). So an index
		// signature is SKIPPED exactly as a method is: it contributes
		// nothing and kills nothing, rather than refusing the whole list
		// the way one `[k: string]: unknown` used to kill every scalar
		// sibling.
		if ast.IsIndexSignatureDeclaration(member) {
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
		if signature.Type == nil || signature.Name() == nil {
			return nil, false
		}
		var key string
		switch {
		case ast.IsIdentifier(signature.Name()):
			key = signature.Name().Text()
		default:
			// a COMPUTED name over a stable module-level symbol const
			// contributes its leaf under the derived `#sym:` name; every
			// other computed name — a literal computed name, an
			// unresolvable expression, or no checker to resolve one — SKIPS
			// this member rather than refusing the list (the doc above)
			symbolKey, isSymbolKey := symbolMemberFieldName(c, signature.Name())
			if !isSymbolKey {
				continue
			}
			key = symbolKey
		}
		if _, already := seen[key]; already {
			return nil, false
		}
		seen[key] = struct{}{}
		// a NESTED FAMILY: the member's own annotation is itself a type
		// literal, a type reference the named-type reader can resolve, an
		// INSTANTIATED type reference carrying its own type arguments
		// (`ReturnType<typeof reducer>`, nestedMemberLeavesOf's
		// TypeReferenceNode-with-arguments arm), or an array-typed member —
		// read at any depth by the SAME reading, one recursion per level
		// (nestedMemberLeavesOf argues the path, the absence composition,
		// and the cycle guard), and appended UNCONDITIONALLY like every
		// other nested kind: capability is never refused for cost, so a
		// holder whose members expand wide gets every leaf; the body's own
		// slot count is measured at the wall, not guarded against here. A
		// nested annotation this recursion cannot read — mentions a type
		// parameter, or resolves to nothing readable — keeps today's single
		// unknown-sorted leaf below, never a refusal.
		if nested, nestedOk := nestedMemberLeavesOf(
			c, holder, key, signature.Type, parameterNames, mayBeAbsent, visiting); nestedOk {
			out = append(out, nested...)
			continue
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
		out = append(out, recordParamMember{
			Key:         key,
			Path:        []string{key},
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

// flowContextOfChecker wraps a bare checker as the minimal *FlowContext
// arrayParameterElementMembers needs to resolve a NAMED-TYPE array
// element (`sourceLinks: SankeyNode[]`) — the same wrapping the
// TypeReferenceNode arm below already builds inline for
// declaredTypeMembersOf. A nil checker answers a nil context, which
// arrayParameterElementMembers's own named-type call
// (namedTypeMembersOf) already declines on.
func flowContextOfChecker(c *checker.Checker) *FlowContext {
	if c == nil {
		return nil
	}
	return &FlowContext{P: &program.CheckerProgram{Checker: c}}
}

// nestedMemberLeavesOf is ONE member's contribution where its own
// annotation is itself a FAMILY of members rather than a scalar — an
// inline type literal (`inner: { deep: number }`), or a type reference
// the named-type reader can resolve (`inner: Inner`). It answers the
// CHILD leaves, prefixed under `key`, rather than the single
// unknown-sorted leaf scalarMemberListWithCheckerIn's caller falls back
// to when this answers (nil, false).
//
// THE PATH. Each child leaf's own Path is `key` followed by the child's
// own path below it, and Key is that joined with dots — so `p: { inner:
// { deep: number } }` answers a leaf whose Path is ["inner","deep"] and
// whose SlotName is "p.inner.deep", read by recursing into the nested
// members under the HOLDER "p.inner": the recursive call's own SlotName
// convention (holder+"."+childKey) already spells the right slot, and
// only Key/Path need the parent's key stitched on front.
//
// THE ABSENCE COMPOSES AS THE WEAKER PROMISE. `parentMayBeAbsent` is
// whether `key` ITSELF was declared optional; every child leaf carries
// MayBeAbsent as parentMayBeAbsent OR its own — an optional `inner?:`
// makes every one of its children possibly absent (there is no value at
// `p.inner.deep` unless `p.inner` is there at all), even where the child's
// own annotation is required. This is a per-leaf OR down the whole path,
// applied one level at a time as each recursion returns.
//
// A PARAMETER-MENTIONING annotation is not read here at all: the caller
// only reaches this function after failing scalarMemberListWithCheckerIn's
// own unknown-sort fallback is decided AFTER this answers, so
// mentionsTypeParameter is checked FIRST, before recursing — the same
// invariance argument applies at any depth, and a nested shape that
// varies by instantiation is exactly as unreadable as a scalar one that
// does.
//
// A TYPE REFERENCE recurses through the SAME resolution
// declaredTypeMembersOf performs for an entry-position named type,
// called at a LINK position (atEntry=false) so a referenced family
// declaring no data member contributes an empty list rather than
// poisoning this member — the same split declaredTypeMembersOf's own
// doc states. `visiting` is threaded through unchanged, so a cycle
// through TWO OR MORE nested named types is caught the same way a
// heritage cycle is: `interface A { b: B } interface B { a: A }`
// resolves B's declaration, which is not yet visited, recurses into A
// again, finds A's declaration already on the path, and answers false —
// which this function reads as "unreadable", falling back to the single
// unknown-sorted leaf for the member that named it. A nil checker
// resolves no type reference (namedTypeMembersOf's own gate), so that
// case is unreadable too and falls back the same way — sound, and no
// different from any other unresolvable named type.
//
// AN ARRAY-VALUED MEMBER (`ticks: number[]`, Sankey.tsx's `sourceLinks:
// number[]` on SankeyNode) is a FAMILY too, one this function did not
// expand before: `p.ticks.length` (axisSelectors' getDomainDefinition
// callers) and `node.sourceLinks.length` (Sankey's relax loops) both
// need the array's own ".len"/".elem" pair spelled under the member's
// holder, exactly as an array-typed PARAMETER's own element expands
// (arrayElementPairMembers, ir_array_nested_elements.go — READ-ONLY,
// this agent's own territory calls it rather than re-deriving it). The
// element's OWN shape (scalar, record, or a further nested array)
// recurses through arrayParameterElementMembers inside that reader, so
// `sourceLinks: LinkDataItemDy[]` with a record element expands its own
// members under "p.sourceLinks.elem.<member>" for free — no separate
// case is needed here for a record- or array-elemented member array,
// only the ONE call into the shared reader. `elementTypeNodeOf`
// recognizes `T[]`, `readonly T[]`, `Array<T>`, and `ReadonlyArray<T>`
// — the same array-type vocabulary the parameter-level reader uses —
// so a member array wears exactly the annotations an array parameter
// does. `arrayElementPairMembers` never declines (a scalar or
// unreadable element still answers the plain "len"/"elem" pair), so
// this arm always contributes once the annotation IS an array type; an
// annotation that is NOT one falls through to the type-reference/type-
// literal cases below unaffected.
//
// Anything else the member's annotation could be — a scalar keyword, a
// union — is not this function's business; it answers (nil, false) and
// the caller's own scalar/unknown reading applies.
func nestedMemberLeavesOf(
	c *checker.Checker,
	holder string,
	key string,
	annotation *ast.Node,
	parameterNames map[string]struct{},
	parentMayBeAbsent bool,
	visiting []*ast.Node,
) ([]recordParamMember, bool) {
	if annotation == nil || mentionsTypeParameter(annotation, parameterNames) {
		return nil, false
	}
	childHolder := holder + "." + key
	var children []recordParamMember
	var readable bool
	switch {
	case ast.IsTypeLiteralNode(annotation):
		children, readable = scalarMemberListWithCheckerIn(
			c, childHolder, annotation.AsTypeLiteralNode().Members.Nodes, nil, false, visiting)
	case elementTypeNodeOf(annotation) != nil:
		children, readable = arrayElementPairMembers(
			flowContextOfChecker(c), c, annotation, elementTypeNodeOf(annotation), childHolder, visiting)
	case ast.IsTypeReferenceNode(annotation):
		reference := annotation.AsTypeReferenceNode()
		if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
			// a NESTED member carrying type arguments (`options:
			// ReturnType<typeof optionsReducer>` on RechartsRootState) — the
			// same INSTANTIATED reading namedTypeMembersOf takes at the entry
			// position (instantiatedReferenceMembersOf,
			// ir_summary_instantiated_members.go), tried here so a member
			// whose own annotation needs the checker's substitution, not just
			// its declaration's written syntax, still expands rather than
			// falling back to a single unknown-sorted leaf. `c == nil` leaves
			// this unreadable, same as the plain-reference arm below.
			if c == nil {
				return nil, false
			}
			children, readable = instantiatedReferenceMembersOf(
				flowContextOfChecker(c), childHolder, annotation)
			break
		}
		if !isResolvableTypeName(reference.TypeName) || c == nil {
			return nil, false
		}
		children, readable = declaredTypeMembersOf(
			&FlowContext{P: &program.CheckerProgram{Checker: c}}, childHolder, reference.TypeName, visiting, false)
	default:
		return nil, false
	}
	if !readable {
		return nil, false
	}
	out := make([]recordParamMember, 0, len(children))
	for _, child := range children {
		path := make([]string, 0, len(child.Path)+1)
		path = append(path, key)
		path = append(path, child.Path...)
		out = append(out, recordParamMember{
			// Key is this leaf's OWN name — the child's own Key is
			// unchanged by being nested one level deeper, since Path's
			// last segment (child.Key) is still path's last segment
			Key:       child.Key,
			Path:      path,
			SlotName:  child.SlotName,
			Sort:      child.Sort,
			TypeofTag: child.TypeofTag,
			// ArrayPair rides through unchanged: a "len"/"elem" leaf nested
			// under an array-typed member (this function's own array arm
			// above) is still that same pair one level deeper, and the
			// ".length"→".len" mapping and hasNestedElementPair
			// (ir_array_nested_elements.go) both gate on this flag — dropping
			// it here would silently un-mark the pair for every member array
			// this nesting reaches.
			ArrayPair:   child.ArrayPair,
			MayBeAbsent: parentMayBeAbsent || child.MayBeAbsent,
		})
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

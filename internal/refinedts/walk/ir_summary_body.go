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
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
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
}

// scalarMemberListOf reads a list of TYPE ELEMENTS as the member list a
// record parameter expands to, spelled under `holder` ("p.lo"/"p.hi").
//
// The rule is total-or-decline over the whole list: every element must
// be a PROPERTY SIGNATURE with a plain identifier name, no `?`, no
// initializer, and its own annotation exactly number, boolean, or
// string. A method signature, an index signature, a call or construct
// signature, a computed name, an optional member, a nested literal, a
// union, an array, a duplicate key, or an EMPTY list answers false and
// the parameter keeps its single whole-name slot.
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
	if len(members) == 0 {
		return nil, false
	}
	seen := map[string]struct{}{}
	out := make([]recordParamMember, 0, len(members))
	for _, member := range members {
		if !ast.IsPropertySignatureDeclaration(member) {
			return nil, false
		}
		signature := member.AsPropertySignatureDeclaration()
		// `lo?: number` admits absence, which a scalar entry slot cannot
		// carry apart from its value; the whole parameter declines
		if signature.PostfixToken != nil || signature.Initializer != nil {
			return nil, false
		}
		if signature.Type == nil || signature.Name() == nil || !ast.IsIdentifier(signature.Name()) {
			return nil, false
		}
		var sort BindingKind
		var tag TypeofTag
		switch signature.Type.Kind {
		case ast.KindNumberKeyword:
			sort, tag = BindingKindNumber, TypeofTagNumber
		case ast.KindBooleanKeyword:
			// booleans ride the number sort — declaredParamSort's own rule
			sort, tag = BindingKindNumber, TypeofTagBoolean
		case ast.KindStringKeyword:
			sort, tag = BindingKindString, TypeofTagString
		default:
			return nil, false
		}
		key := signature.Name().Text()
		if _, already := seen[key]; already {
			return nil, false
		}
		seen[key] = struct{}{}
		out = append(out, recordParamMember{
			Key:       key,
			SlotName:  holder + "." + key,
			Sort:      sort,
			TypeofTag: tag,
		})
	}
	return out, true
}

// namedTypeMembersOf resolves a parameter annotation that is a TYPE
// REFERENCE to a PLAIN IDENTIFIER — `p: Bounds` — to the members it
// stands for, or (false).
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
//   - a QUALIFIED name (`ns.Bounds`) or a name carrying TYPE ARGUMENTS
//     (`Box<number>`) — the members would depend on the arguments, which
//     no entry vector spells;
//   - a symbol with NO declaration, or with declarations that are none of
//     the two admitted kinds — nothing to read syntax off;
//   - a symbol with MORE THAN ONE declaration — an interface declared
//     twice merges its members across declarations, and reading only the
//     first would build a member list the other declaration contradicts;
//   - an INTERFACE with HERITAGE (`extends`) — an inherited member is
//     declared somewhere this reading never visits, so the list would be
//     incomplete and a body reading the inherited member would spell a
//     slot the layout never made;
//   - an interface or alias with TYPE PARAMETERS — the members'
//     annotations are not the ones any instance actually holds;
//   - a TYPE ALIAS whose right side is not a TYPE LITERAL (a union,
//     another reference, a mapped or conditional type) — the census reads
//     syntax, and only a literal spells its members;
//   - a CLASS — an instance carries methods, accessors, private state and
//     aliases the flattening cannot hold, and its fields are not promised
//     by the annotation alone.
//
// A nil context, or one with no program or no checker, resolves nothing
// and declines — the nil-tolerance the ctx-less callers rely on: a
// lowering that runs without a checker keeps exactly today's behaviour
// rather than crashing.
func namedTypeMembersOf(ctx *FlowContext, holder string, typeNode *ast.Node) ([]recordParamMember, bool) {
	if !ast.IsTypeReferenceNode(typeNode) {
		return nil, false
	}
	reference := typeNode.AsTypeReferenceNode()
	// type ARGUMENTS make the members depend on what was applied
	if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
		return nil, false
	}
	typeName := reference.TypeName
	// only a PLAIN identifier: a qualified name reaches into a namespace
	// whose resolution this reading does not claim
	if typeName == nil || !ast.IsIdentifier(typeName) {
		return nil, false
	}
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, false
	}
	symbol := symbolAt(ctx.P.Checker, typeName)
	if symbol == nil || len(symbol.Declarations) != 1 {
		return nil, false
	}
	declaration := symbol.Declarations[0]
	if declaration == nil {
		return nil, false
	}
	if ast.IsInterfaceDeclaration(declaration) {
		asInterface := declaration.AsInterfaceDeclaration()
		if asInterface.HeritageClauses != nil && len(asInterface.HeritageClauses.Nodes) > 0 {
			return nil, false
		}
		if asInterface.TypeParameters != nil && len(asInterface.TypeParameters.Nodes) > 0 {
			return nil, false
		}
		return scalarMemberListOf(holder, asInterface.Members.Nodes)
	}
	if ast.IsTypeAliasDeclaration(declaration) {
		asAlias := declaration.AsTypeAliasDeclaration()
		if asAlias.TypeParameters != nil && len(asAlias.TypeParameters.Nodes) > 0 {
			return nil, false
		}
		if asAlias.Type == nil || !ast.IsTypeLiteralNode(asAlias.Type) {
			return nil, false
		}
		return scalarMemberListOf(holder, asAlias.Type.AsTypeLiteralNode().Members.Nodes)
	}
	// a class, an enum, a module, a type parameter — none expand
	return nil, false
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
// A class name, a generic, a union, an intersection, a qualified name,
// an unresolvable name, an optional member, a method, an index
// signature, a nested literal, or a member whose own annotation is not
// number/boolean/string answers false and the parameter keeps its single
// whole-name slot — exactly today's behaviour. A declaration's summary
// quantifies over every caller, and only what the annotation itself
// promises to every entry may become slots.
func recordParamMembersIn(ctx *FlowContext, parameter *ast.Node) ([]recordParamMember, bool) {
	pd := parameter.AsParameterDeclaration()
	if pd.Type == nil || pd.Name() == nil || !ast.IsIdentifier(pd.Name()) {
		return nil, false
	}
	holder := pd.Name().Text()
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
	if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) || pd.DotDotDotToken != nil {
		return nil, false
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
	return []bodySlot{{
		Name:      pd.Name().Text(),
		Sort:      declaredParamSort(parameter),
		TypeofTag: declaredParamTypeof(parameter),
	}}, true
}

// recordParameterUsesAreDeclaredReads scans a body for every occurrence
// of an EXPANDED parameter's name and answers whether each one is a
// READ of a declared member — `p.lo` in value position.
//
// Everything else declines the body:
//
//   - a whole-name use (`f(p)`, `return p`, `q = p`, `p[e]`, `p?.lo`) —
//     after the expansion there is no one value for it to denote;
//   - a member the annotation never declared (`p.mid`) — no slot holds
//     it, and reading it would silently answer another slot's state;
//   - a WRITE to a member (`p.lo = 1`, `p.lo += 1`, `p.lo++`, `delete
//     p.lo`) — the caller's own object would move, and a summary carries
//     no effect back out through its entries;
//   - a deep path (`p.lo.x`) — the members are scalars, so no such leaf
//     exists.
func recordParameterUsesAreDeclaredReads(body *ast.Node, name string, members []recordParamMember) bool {
	declared := map[string]struct{}{}
	for _, member := range members {
		declared[member.Key] = struct{}{}
	}
	// a node that WRITES through this parameter's spelling
	writesThroughParameter := func(node *ast.Node) bool {
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if root, _, ok := propertyPathOf(Unwrapped(bin.Left)); ok && root == name {
					return true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if root, _, ok := propertyPathOf(Unwrapped(unary.Operand)); ok && root == name {
					return true
				}
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if root, _, ok := propertyPathOf(Unwrapped(unary.Operand)); ok && root == name {
					return true
				}
			}
		}
		if ast.IsDeleteExpression(node) {
			if root, _, ok := propertyPathOf(Unwrapped(node.AsDeleteExpression().Expression)); ok && root == name {
				return true
			}
		}
		return false
	}
	ok := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		if writesThroughParameter(node) {
			ok = false
			return true
		}
		// a declared member READ consumes the root and the step name, so
		// neither reaches the bare-name test below
		if root, path, isPath := propertyPathOf(Unwrapped(node)); isPath && root == name {
			if len(path) != 1 {
				ok = false
				return true
			}
			if _, isDeclared := declared[path[0]]; !isDeclared {
				ok = false
				return true
			}
			return false
		}
		// every other occurrence of the bare name is the WHOLE record in a
		// position the expansion cannot spell
		if ast.IsIdentifier(node) && node.Text() == name {
			ok = false
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return ok
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
		!ast.IsSetAccessorDeclaration(declaration) {
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
	if len(census.CapturedMethodCalls) > 0 && !census.Escapes && !census.ComputedWrite {
		wipes, computable := CaptureWriteSet(classLike, fields, census.CapturedMethodCalls)
		if !computable {
			return thisBundleLayout{Escaped: true}
		}
		for _, field := range wipes {
			captureHavocNames = append(captureHavocNames, "this."+field.Name)
		}
	} else if !census.Believable() {
		// an escape and a computed STORE both move the object through
		// something no slot names — no believable bundle either way
		return thisBundleLayout{Escaped: true}
	}
	if len(census.Reads) == 0 && len(census.Writes) == 0 && len(captureHavocNames) == 0 {
		return thisBundleLayout{}
	}
	written := map[string]struct{}{}
	for _, field := range census.Writes {
		written[field.SlotName] = struct{}{}
	}
	for _, name := range captureHavocNames {
		written[name] = struct{}{}
	}
	// the READ fields carry entries. A write-only field has no entry state
	// for the caller to fill — its slot is one the body creates, which the
	// locals' own layout would have to hold — so this wave lays out the
	// reads and names the writes among them.
	entries := make([]bodySlot, 0, len(census.Reads))
	for _, field := range census.Reads {
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
	}
}

// summarySlotBudget is the summary route's own slot ceiling — the same
// figure the whole-body route uses, kept here so the two routes admit
// the same bodies.
const summarySlotBudget = 32

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
func localSlotsOf(
	body *ast.Node,
	locals []*ast.Node,
	patterns []*ast.Node,
	parameterNames map[string]struct{},
) []bodySlot {
	objectLocals := ObjectLocalsOf(body, locals)
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
			Sort:      LocalSort(declared),
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
		if members, expanded := recordParamMembersIn(ctx, parameter); expanded {
			// an EXPANDED parameter's every use in the body must be a read of
			// a declared member; a whole-p use, an undeclared member, or a
			// write through it declines the body outright
			if !recordParameterUsesAreDeclaredReads(body, parameter.AsParameterDeclaration().Name().Text(), members) {
				return LoweredSummary{}, "", "a whole-record parameter use", false
			}
			// the arrow route fills ONE entry per declared parameter with a
			// site sort, which an expanded parameter has no single entry for
			if index < len(parameterSorts) {
				return LoweredSummary{}, "", "a record parameter of an arrow argument", false
			}
			for _, entry := range entries {
				// a record parameter's leaf is a bundle entry the call site may
				// have to map back. Written is FALSE for every one of them: a
				// write through an expanded parameter declined the body above,
				// so no leaf of a body that got this far is ever moved.
				bundleEntries = append(bundleEntries, BundleEntry{
					Path:    entry.Name,
					Index:   len(paramNames),
					Written: false,
				})
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
	declaredCount := len(paramNames)
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
		if _, expanded := recordParamMembersIn(ctx, parameter); expanded {
			parameterNames[parameter.AsParameterDeclaration().Name().Text()] = struct{}{}
		}
	}
	slots := localSlotsOf(body, locals, patterns, parameterNames)
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
	if len(bindings) > summarySlotBudget {
		return LoweredSummary{}, "", "a body past the slot budget", false
	}
	doneIndex := len(bindings) - 2
	retIndex := len(bindings) - 1
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
	// THE DEFAULT PRELUDE: each defaulted parameter applies its default
	// exactly where the runtime does — only when the call left the entry
	// undefined. The branch tests the slot's definedness (the kernel's
	// IrTest.defined, covered by walk_sound), and the else arm assigns
	// the lowered default; a supplied argument walks the empty then arm
	// untouched. A default the effect grammar cannot spell havocs its
	// OWN slot and names the construct — the body's other statements
	// keep their knowledge.
	var prelude []kernelbridge.IrStatement
	defaultEffects := map[int]kernelbridge.LoopEffect{}
	for _, defaulted := range defaultedSlots {
		if effect, lowered := RhsEffect(context, context.Sorts[defaulted.Slot], defaulted.Initializer); lowered {
			defaultEffects[defaulted.Slot] = effect
			prelude = append(prelude, kernelbridge.IrStatement{
				Kind: kernelbridge.IrStatementBranch,
				On:   defaulted.Slot,
				Test: kernelbridge.IrTestDefined,
				Else: []kernelbridge.IrStatement{{
					Kind:   kernelbridge.IrStatementAssign,
					Target: defaulted.Slot,
					Effect: effect,
				}},
			})
			continue
		}
		NoteFirstHavoc(context, "a defaulted parameter")
		prelude = append(prelude, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: defaulted.Slot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
		})
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
	// the prelude runs FIRST: a default is applied before any body
	// statement can read the parameter
	if len(prelude) > 0 {
		stmts = append(prelude, stmts...)
	}
	return LoweredSummary{
		Stmts:          stmts,
		ParamCount:     len(paramNames),
		DoneIndex:      doneIndex,
		RetIndex:       retIndex,
		SlotCount:      len(context.Bindings),
		Table:          table.Blobs,
		BundleEntries:  bundleEntries,
		DefaultEffects: defaultEffects,
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

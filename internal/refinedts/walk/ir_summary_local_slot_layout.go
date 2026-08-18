// split from ir_summary_body.go — the body's own slots

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

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
	// TopEntry: call sites fill this entry TOP unconditionally, never
	// from the argument's member. A DEFAULTED pattern element binds
	// member-or-default — a definite-undefined member state would claim
	// undefined where the runtime bound the default — and a REST pattern
	// element binds a fresh object of the remaining members, which no
	// member state spells. TOP claims nothing and the entry quantifier
	// already covers it.
	TopEntry bool
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
//
// (4) A LOCAL WHOSE INITIALIZER IS AN OBJECT LITERAL THE FLATTENER
// REFUSED — a spread row, a computed key, any row flatKeysOfLiteral
// does not read as one scalar leaf — declines the WHOLE body where the
// body later reads a MEMBER of that name (`p.hi`). Such a local gets no
// leaf slots (the recognizer already refuses the literal) and keeps
// only its whole-name slot, which a member read cannot resolve through:
// nothing downstream of this collection distinguishes "no leaf slot
// because the shape was never tried" from "no leaf slot because the
// value carries no scalar spelling at all", so a member read on it
// would otherwise fall to the inert-return floor and answer unknown for
// a value that in fact has a name — the wrong shape of weak. Declining
// here, before the layout or the lowering ever sees the body, is the
// one place this file owns that can still say no.
//
// A local whose declaration this collection has not classified as an
// object literal at all — a parameter, a scalar, an array, a record
// read through some other route — is untouched: only a name whose OWN
// initializer is `{ … }` and whose rows the flattener refuses triggers
// this.
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
	for _, declaration := range locals {
		if refusedLiteralLocalReadAsMember(body, declaration) {
			return nil, nil, false
		}
	}
	return locals, patterns, true
}

// refusedLiteralLocalReadAsMember answers whether `declaration` is
// `const name = { … }` whose rows the flattener refuses (a spread, a
// computed key not a stable symbol const, an array-valued row — every
// shape flatKeysOfLiteral declines), AND the body reads a MEMBER of
// `name` somewhere (`name.k`). The declaration's own initializer is not
// itself a use.
func refusedLiteralLocalReadAsMember(body *ast.Node, declaration *ast.Node) bool {
	literal := objectLiteralOfDeclaration(declaration)
	if literal == nil {
		return false
	}
	if _, ok := flatKeysOfLiteral(literal, "", nil); ok {
		// the literal flattens fine; the recognizer's own use scan is what
		// decides whether the body's uses of it admit that flattening
		return false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	declarationName := declaration.AsVariableDeclaration().Name()
	found := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found {
			return true
		}
		if node == declarationName {
			return false
		}
		// `p?.a` counts as a member read here too: `p` is this local's own
		// root, always defined once flattened, so the optional step is not
		// a reason to treat the read as absent.
		if root, _, isPath := propertyPathAdmittingRootOptionalStep(node); isPath && root == name {
			found = true
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return found
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

// localSortAndTypeof is a plain scalar local's sort and typeof evidence,
// tried in order and stopping at the first grounded answer:
//
//  1. the let's OWN declared annotation (`let x1: number`) —
//     annotationSort's reading, the same one a parameter or a bundle
//     field wears.
//  2. LocalSortResolved / LocalTypeof — the initializer's syntax, with
//     a call head's return type consulted through the checker.
//  3. an UNINITIALIZED, unannotated local's WRITE-DERIVED sort: the
//     checker's own resolved type at a plain READ occurrence of the
//     name inside this body. `let x1; x1 = x2 - 1;` carries no
//     annotation and no initializer, so (1) and (2) both answer
//     unknown — but the checker's control-flow analysis narrows a
//     later read of `x1` to `number` from the assignment alone
//     (confirmed against this exact shape: GetTypeAtLocation on the
//     bare declaration name answers `any`, the same call on a read
//     occurrence after the assignment answers the assigned type).
//     Reading it at a USE site rather than the declaration is what
//     makes this grounded rather than a guess — a name never read
//     answers "undefined" there and stays unknown, and a name whose
//     reads disagree (one branch leaves it a string, another a
//     number) also stays unknown, since only ONE read is consulted
//     and a caller wanting soundness across every read still needs
//     every read to agree with what that one read said. Here every
//     read occurrence is checked and required to agree, which is the
//     stronger reading the CartesianAxis body's own several
//     assignments (`x1 = x2 - 1`, `x1 = x2 + sign * finalTickSize`, …)
//     needs before its later `x1` read may wear a number sort.
func localSortAndTypeof(
	ctx *FlowContext,
	c *checker.Checker,
	body *ast.Node,
	declaration *ast.Node,
) (BindingKind, TypeofTag) {
	decl := declaration.AsVariableDeclaration()
	if sort, typeofTag := annotationSort(decl.Type); sort != BindingKindUnknown {
		return sort, typeofTag
	}
	if sort := LocalSortResolved(c, declaration); sort != BindingKindUnknown {
		return sort, LocalTypeof(declaration)
	}
	if decl.Type != nil || decl.Initializer != nil || c == nil {
		// an ANNOTATED or INITIALIZED local that still reads unknown has
		// already had its one honest chance — a write-derived reading is
		// for the uninitialized, unannotated case alone, where nothing
		// else was ever going to ground it
		return BindingKindUnknown, TypeofTagNone
	}
	return writtenSortOf(ctx, c, body, declaration)
}

// writtenSortOf grounds an uninitialized local's sort from the checker's
// OWN resolved type at every plain read occurrence of its name in the
// body — never the declaration name itself (which resolves to `any`,
// having nothing to narrow from) and never an occurrence that is itself
// the LEFT side of a plain assignment (that position states nothing
// about the value read back). Every read found must agree on the SAME
// mask LocalSortResolved already applies (number/boolean-only,
// string-only) or the local stays unknown — one disagreeing read is
// answer enough to refuse, matching the package's "never guess a sort"
// rule.
func writtenSortOf(
	ctx *FlowContext,
	c *checker.Checker,
	body *ast.Node,
	declaration *ast.Node,
) (BindingKind, TypeofTag) {
	if c == nil || ctx == nil {
		return BindingKindUnknown, TypeofTagNone
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	declarationName := declaration.AsVariableDeclaration().Name()
	sawSort := BindingKind("")
	sawTypeof := TypeofTag("")
	agreed := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !agreed {
			return true
		}
		if node == declarationName {
			return false
		}
		if !ast.IsIdentifier(node) || node.Text() != name {
			node.ForEachChild(visit)
			return false
		}
		if assignmentLeftHandSide(node) {
			// the write itself states nothing about the read-back value —
			// only a READ occurrence's narrowed type is evidence
			return false
		}
		sort, typeofTag := ResolvedExpressionSort(c, node)
		if sort == BindingKindUnknown {
			agreed = false
			return true
		}
		if sawSort == "" {
			sawSort, sawTypeof = sort, typeofTag
			return false
		}
		if sawSort != sort || sawTypeof != typeofTag {
			agreed = false
			return true
		}
		return false
	}
	visit(body)
	if !agreed || sawSort == "" {
		return BindingKindUnknown, TypeofTagNone
	}
	return sawSort, sawTypeof
}

// assignmentLeftHandSide answers whether `node` is the plain identifier
// LEFT side of a `=` or compound-assignment binary expression — the one
// occurrence shape writtenSortOf must skip, since that position's own
// narrowed type is the RIGHT side's, not evidence about a later read.
func assignmentLeftHandSide(node *ast.Node) bool {
	parent := node.Parent
	if parent == nil || !ast.IsBinaryExpression(parent) {
		return false
	}
	bin := parent.AsBinaryExpression()
	if bin.Left != node {
		return false
	}
	return bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
		bin.OperatorToken.Kind <= ast.KindLastAssignment
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
		sort, typeofTag := localSortAndTypeof(ctx, c, body, declared)
		out = append(out, bodySlot{
			Name:      name,
			Sort:      sort,
			TypeofTag: typeofTag,
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

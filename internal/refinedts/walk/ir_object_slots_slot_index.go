// split from ir_object_slots.go — slot indexing and the declaration's per-leaf assignments

package walk

import (
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// slotIndexOfName resolves a spelled slot name the way IndexOf
// resolves a node's: through the closed name map where an inlined body
// supplied one, through the binding list otherwise.
func slotIndexOfName(context *LoweringContext, spelled string) (int, bool) {
	if context.Names != nil {
		index, found := context.Names[spelled]
		return index, found
	}
	for index, binding := range context.Bindings {
		if binding == spelled {
			return index, true
		}
	}
	return 0, false
}

// PathSlotIndexOf resolves a DEEP path read (`p.a.b`) to its leaf slot.
// SpelledNameOf spells only one step, so a nested leaf's slot has to be
// looked up from the path itself; a one-step path resolves to exactly
// the name SpelledNameOf would have produced, so this subsumes it.
//
// A SYMBOL-KEYED step (`p[S]`) resolves here too, under the derived
// `#sym:` leaf name the flattening spelled it with. The path is one step
// by construction — the key is a const's own name, never a chain — so
// the spelling is `p.#sym:S` and the lookup is the same lookup.
//
// The path read here admits ONE optional step adjacent to the root
// (`p?.a`, propertyPathAdmittingRootOptionalStep) — sound because the
// slot lookup below is the real gate: "p.a" only resolves to an index
// where the recognizer already admitted p as a flattened local (whose
// root is never null/undefined), so a root that is not such a local
// still fails the lookup exactly as before.
func PathSlotIndexOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if root, path, ok := propertyPathAdmittingRootOptionalStep(head); ok {
		spelled := root + "." + strings.Join(path, ".")
		if index, found := slotIndexOfName(context, spelled); found {
			return index, true
		}
		// `p.a` where `p` itself resolved to no slot: p may be an ELEMENT
		// ALIAS (elementAliasSlotIndexOf's own doc) — a `const p = xs[i]`
		// binding over a record-elemented flattened array, admitted only
		// where every use of p in the body is a declared-member step. Tried
		// after the ordinary lookup, which is always the cheaper and more
		// common answer. One step only — the alias never spells a deeper
		// path, since the aliased value is the ARRAY ELEMENT itself, one
		// level below xs, and its own members are scalars.
		if len(path) == 1 {
			if rootNode := rootIdentifierOf(head); rootNode != nil {
				if index, found := elementAliasSlotIndexOf(context, rootNode, path[0]); found {
					return index, true
				}
			}
		}
		return 0, false
	}
	if root, leaf, ok := symbolKeyedLeafOf(context, head); ok {
		return slotIndexOfName(context, root+"."+leaf)
	}
	if holder, member, ok := arrayElementMemberLeafOf(head); ok {
		if index, found := slotIndexOfName(context, holder.Text()+arrayElemSuffix+"."+member); found {
			return index, true
		}
		// `xs[i].length` over an element that is itself an array reads the
		// inner pair's len slot — the same ".length"→".len" mapping the
		// flattened array's own length read wears. Tried only after the
		// literal spelling missed, so a record element declaring its own
		// "length" member keeps it; gated on the pair itself, so a record
		// member merely spelled "len" is never served for a length read.
		if member == "length" {
			if local, expanded := memberExpandedArrayOf(context, holder); expanded && hasNestedElementPair(local) {
				return slotIndexOfName(context, local.ElemSlotName+arrayLenSuffix)
			}
		}
		return 0, false
	}
	return 0, false
}

// memberExpandedArrayOf resolves an identifier NODE to the flattened
// array PARAMETER it names, where that array's element expanded to
// members (a record's leaves, or the inner array pair) — the shapes
// whose scalar "xs.elem" slot does not exist, so the two-slot pair
// resolution misses and the parameter's own layout is the authority.
func memberExpandedArrayOf(context *LoweringContext, holder *ast.Node) (ArrayLocal, bool) {
	if context == nil || context.Flow == nil || holder == nil || !ast.IsIdentifier(holder) {
		return ArrayLocal{}, false
	}
	c := checkerOf(context.Flow)
	if c == nil {
		return ArrayLocal{}, false
	}
	symbol := symbolAt(c, holder)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return ArrayLocal{}, false
	}
	if !ast.IsParameterDeclaration(symbol.ValueDeclaration) {
		return ArrayLocal{}, false
	}
	local, flattened := arrayParamSlotsIn(context.Flow, symbol.ValueDeclaration)
	if !flattened || len(local.ElementMembers) == 0 {
		return ArrayLocal{}, false
	}
	return local, true
}

// rootIdentifierOf answers the root IDENTIFIER node a plain or
// root-optional property path reads from — `p.a`'s `p`, `p?.a`'s `p` —
// or nil for a `this`-rooted path, which the alias route never admits
// (`this` cannot be `const p = xs[i]`'s own name).
func rootIdentifierOf(node *ast.Node) *ast.Node {
	current := node
	for ast.IsPropertyAccessExpression(current) {
		current = current.AsPropertyAccessExpression().Expression
	}
	if ast.IsIdentifier(current) {
		return current
	}
	return nil
}

/* ── element-local ALIASING: `const p = xs[i]` over a record-elemented
   flattened array parameter ────────────────────────────────────────── */

// elementAliasSlotIndexOf resolves "p.<member>" to the SAME slot index
// "xs.elem.<member>" already occupies, where `p`'s own declaration is
// `const p = xs[i]` and `xs` is a flattened array parameter whose element
// expands to a record (ArrayLocal.ElementMembers). `rootNode` is the read
// site's own root identifier (`p` at THIS occurrence of `p.a`) — resolved
// to p's declaration through the checker, the alias-following reach
// symbolAt already gives every other seam in this package.
//
// THE DESIGN CHOICE, decided here rather than by adding leaf slots
// through ObjectLocalKey/declarationLeavesOf (the route the object-local
// family otherwise always takes): `p` is an ALIAS of the indexed element,
// not a copy of its members. Aliasing means "p.a" and "xs.elem.a" must be
// the SAME slot, and the slot vector this package lays out (localSlotsIn,
// ir_summary_local_slot_layout.go) allocates one fresh index per spelled
// name it is handed — two names always get two indices there, however
// they are spelled. The only seam that can make two SPELLINGS answer one
// INDEX is resolution itself: this function redirects the lookup, after
// the ordinary name map has already answered "no slot for p.a", to the
// array's own already-laid-out "xs.elem.a" slot. No new slot is ever
// allocated for p; the declaration `const p = xs[i]` therefore lowers to
// nothing (ObjectDeclarationAssignmentsOf and ArrayDeclarationAssignmentsOf
// both decline it — neither recognizes the initializer shape — so no
// statement writes anything at its position, which is exactly right: the
// alias is bookkeeping, no state moves).
//
// A write through EITHER spelling must join into this same slot rather
// than replace it, mirroring the plain elem slot's own weak-update rule
// (ir_array_slots.go's doc) — that write route is unbuilt here (out of
// this file's territory; a write through "p.a = v" or "xs[i].a = v" must
// DECLINE, never replace, until it lands).
func elementAliasSlotIndexOf(context *LoweringContext, rootNode *ast.Node, member string) (int, bool) {
	target, ok := elementAliasTargetOf(context, rootNode)
	if !ok {
		return 0, false
	}
	for _, candidate := range target.ElementMembers {
		// a member matches at its OWN level only: a nested leaf (Path
		// deeper than one) is not a flat member of the element, and a bare
		// Key match would serve `p.deep` the "inner.deep" slot — a wrong
		// answer, not a weak one
		if len(candidate.Path) != 1 {
			continue
		}
		if candidate.Key == member {
			return slotIndexOfName(context, candidate.SlotName)
		}
	}
	// `p.length` over an element that is itself an array reads the inner
	// pair's len — the ".length"→".len" mapping the flattened array's own
	// length read wears, gated on the pair (ArrayPair) so a record member
	// merely spelled "len" is never served for a length read
	if member == "length" {
		for _, candidate := range target.ElementMembers {
			if candidate.ArrayPair && len(candidate.Path) == 1 && candidate.Path[0] == "len" {
				return slotIndexOfName(context, candidate.SlotName)
			}
		}
	}
	return 0, false
}

// elementAliasedLocals memoizes, per DECLARATION NODE, whether `const p =
// xs[i]` admits the aliasing — the array it aliases, or the zero value
// where it does not. Keyed exactly as resolvedArrayParameters
// (ir_summary_parameter_entries.go) is: a fresh program clears it, so a
// stale answer from an earlier program's node pointer can never leak in.
var (
	elementAliasedLocalsMu sync.Mutex
	elementAliasedLocals   = map[*ast.Node]ArrayLocal{}
)

// ClearElementAliasedLocals drops every remembered alias admission. Keyed
// on declaration nodes from one program; a caller that builds a new
// program clears it, as the array-parameter and record-member memos do.
func ClearElementAliasedLocals() {
	elementAliasedLocalsMu.Lock()
	elementAliasedLocals = map[*ast.Node]ArrayLocal{}
	elementAliasedLocalsMu.Unlock()
}

// elementAliasTargetOf resolves the READ SITE's own root identifier to
// the array it aliases, admitting the alias only once per DECLARATION
// NODE (memoized — every occurrence of `p` in the body shares one
// admission answer) and only where EVERY use of the declared name in the
// enclosing body is a declared-member path step — AGENT-BRIEF's own rule
// 3: a hand-over (`f(p)`), a bare return, or a reassignment declines the
// whole aliasing, and the name then resolves through no route at all,
// exactly as it does today.
func elementAliasTargetOf(context *LoweringContext, rootNode *ast.Node) (ArrayLocal, bool) {
	if context == nil || context.Flow == nil || rootNode == nil {
		return ArrayLocal{}, false
	}
	c := checkerOf(context.Flow)
	if c == nil {
		return ArrayLocal{}, false
	}
	declaration := aliasCandidateDeclarationOf(c, rootNode)
	if declaration == nil {
		return ArrayLocal{}, false
	}
	return elementAliasTargetOfDeclaration(context.Flow, c, declaration, nil)
}

// elementAliasTargetOfDeclaration is the admission keyed on the alias's
// own DECLARATION node — the memoized half elementAliasTargetOf resolves
// to, callable directly where the SOURCE of another alias is itself a
// declaration (`const node = row[j]` resolving `row`). `visiting`
// carries the declarations already on this admission's path: a source
// chain that circles back to itself refuses at the revisit instead of
// recursing forever.
func elementAliasTargetOfDeclaration(
	flow *FlowContext, c *checker.Checker, declaration *ast.Node, visiting map[*ast.Node]struct{},
) (ArrayLocal, bool) {
	elementAliasedLocalsMu.Lock()
	held, remembered := elementAliasedLocals[declaration]
	elementAliasedLocalsMu.Unlock()
	if remembered {
		return held, held.Name != ""
	}
	if _, inFlight := visiting[declaration]; inFlight {
		return ArrayLocal{}, false
	}
	if visiting == nil {
		visiting = map[*ast.Node]struct{}{}
	}
	visiting[declaration] = struct{}{}
	local, admitted := admitElementAlias(flow, c, declaration, visiting)
	if !admitted {
		local = ArrayLocal{}
	}
	elementAliasedLocalsMu.Lock()
	elementAliasedLocals[declaration] = local
	elementAliasedLocalsMu.Unlock()
	return local, admitted
}

// aliasCandidateDeclarationOf resolves the read site's root identifier to
// its own VariableDeclaration node — the alias-following symbolAt reach
// (cast_and_await.go), narrowed to a plain `const` declarator. Anything
// else (a parameter, a `let`, an import, an unresolved name) answers nil:
// only a `const` local can be an unreassignable alias (AGENT-BRIEF rule
// 4 — `let p = xs[i]; p = xs[j];` declines outright).
func aliasCandidateDeclarationOf(c *checker.Checker, rootNode *ast.Node) *ast.Node {
	symbol := symbolAt(c, rootNode)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return nil
	}
	if declaration.Parent == nil || !ast.IsVariableDeclarationList(declaration.Parent) ||
		(declaration.Parent.Flags&ast.NodeFlagsConst) == 0 {
		return nil
	}
	return declaration
}

// admitElementAlias runs the recognizer over ONE candidate declaration:
// `const p = xs[i]` where `xs` resolves to a flattened array parameter
// whose element expands to a record. Every step is a hard gate — the
// initializer shape, the source's own flattening, and the use scan — so a
// declaration that fails any one keeps the name resolving through no
// route, which is the honest porous answer AGENT-BRIEF's step 4 pins.
func admitElementAlias(
	flow *FlowContext, c *checker.Checker, declaration *ast.Node, visiting map[*ast.Node]struct{},
) (ArrayLocal, bool) {
	decl := declaration.AsVariableDeclaration()
	if decl.Initializer == nil || decl.Name() == nil || !ast.IsIdentifier(decl.Name()) {
		return ArrayLocal{}, false
	}
	initializer := Unwrapped(decl.Initializer)
	if !ast.IsElementAccessExpression(initializer) {
		return ArrayLocal{}, false
	}
	element := initializer.AsElementAccessExpression()
	if element.QuestionDotToken != nil {
		// an absent receiver reads as undefined at this position, which no
		// member leaf can stand for — the guarded-read case AGENT-BRIEF's
		// step 4 names: no identity slot is invented here
		return ArrayLocal{}, false
	}
	sourceName := Unwrapped(element.Expression)
	if !ast.IsIdentifier(sourceName) {
		return ArrayLocal{}, false
	}
	sourceSymbol := symbolAt(c, sourceName)
	if sourceSymbol == nil || sourceSymbol.ValueDeclaration == nil {
		return ArrayLocal{}, false
	}
	source := sourceSymbol.ValueDeclaration
	var local ArrayLocal
	var flattened bool
	switch {
	case ast.IsParameterDeclaration(source):
		local, flattened = arrayParamSlotsIn(flow, source)
	case ast.IsVariableDeclaration(source) &&
		source.Parent != nil && ast.IsVariableDeclarationList(source.Parent) &&
		(source.Parent.Flags&ast.NodeFlagsConst) != 0:
		// the source is itself an admitted element alias (`const row =
		// xs[i]` feeding `const node = row[j]`) binding an inner ARRAY:
		// the aliased element's own derived layout is what this alias's
		// reads live in — composed prefixes, no new slot anywhere
		if outer, isAlias := elementAliasTargetOfDeclaration(flow, c, source, visiting); isAlias {
			local, flattened = elementArrayLocalOf(outer)
		}
	}
	if !flattened || len(local.ElementMembers) == 0 {
		return ArrayLocal{}, false
	}
	// the BODY the use scan runs over is p's own enclosing block: a
	// variable declaration's function is several parents up
	// (VariableDeclaration -> VariableDeclarationList -> VariableStatement
	// -> the enclosing Block or function body).
	body := enclosingFunctionBodyOf(declaration)
	if body == nil {
		return ArrayLocal{}, false
	}
	aliasName := decl.Name().Text()
	keys := make([]ObjectLocalKey, 0, len(local.ElementMembers))
	for _, member := range local.ElementMembers {
		keys = append(keys, ObjectLocalKey{
			Path:      member.Path,
			Key:       member.Key,
			SlotName:  aliasName + "." + strings.Join(member.Path, "."),
			Declared:  true,
			Sort:      member.Sort,
			TypeofTag: member.TypeofTag,
		})
	}
	if !aliasUsesAdmissible(body, declaration, aliasName, keys, hasNestedElementPair(local)) {
		return ArrayLocal{}, false
	}
	return local, true
}

// aliasUsesAdmissible is the ALIAS's own use scan — wider than
// usesAreAllDeclaredKeySteps in exactly the positions the alias has
// sound semantics for, and narrower nowhere:
//
//   - a declared-member path step (`p.depth`, read OR written) — the
//     write side is sound because AssignmentOfExpression wraps every
//     alias-resolved write in a JOIN with the slot's own state (the
//     weak update one-element-of-many needs);
//   - a bare occurrence consumed as a TEST — an if/while/for/ternary
//     condition, under `!`, under a `&&`/`||` chain whose own result
//     is so consumed, or an equality against null/undefined. A test
//     reads presence and truthiness; the alias value never escapes;
//   - a bare occurrence as a direct CALL ARGUMENT — a hand-over. The
//     opaque havoc enumerator names the aliased element's member
//     slots for exactly this shape (elementAliasHavocSlots), so the
//     callee's possible writes through the reference are covered;
//   - where the aliased element is itself an ARRAY (`nestedPair`): an
//     index READ (`p[j]`, the inner elem join) and a `p.length` READ
//     (the inner len join). The WRITE side of both stays refused —
//     `p[j] = v`, `p.length = k`, and their stepped forms would need
//     the join-write route these shapes do not have yet, and `delete
//     p[j]` changes a length no slot here spells.
//
// Everything else — `return p`, `const q = p`, `delete p.k`, a
// reassignment of p itself — still refuses the aliasing whole.
func aliasUsesAdmissible(
	body *ast.Node, declaration *ast.Node, name string, keys []ObjectLocalKey, nestedPair bool,
) bool {
	declared := declaredLeafPaths(keys)
	declarationName := declaration.AsVariableDeclaration().Name()
	ok := true
	var visit func(node *ast.Node) bool
	visitIfPresent := func(node *ast.Node) {
		if node != nil {
			visit(node)
		}
	}
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		if ast.IsDeleteExpression(node) {
			operand := Unwrapped(node.AsDeleteExpression().Expression)
			if root, _, isPath := propertyPathOf(operand); isPath && root == name {
				ok = false
				return true
			}
			// `delete p[j]` shrinks the inner array and leaves a hole —
			// neither is a fact the joined pair can carry
			if _, isIndex := indexAccessOf(operand, name); isIndex {
				ok = false
				return true
			}
		}
		// a WRITE through the pair shapes — `p[j] = v`, `p.length = k` —
		// refuses: no join-write route exists for either spelling here,
		// and a length write truncates
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment {
				left := Unwrapped(bin.Left)
				if _, isIndex := indexAccessOf(left, name); isIndex {
					ok = false
					return true
				}
				if root, path, isPath := propertyPathAdmittingRootOptionalStep(left); isPath &&
					root == name && len(path) == 1 && path[0] == "length" {
					ok = false
					return true
				}
			}
		}
		// `p[j]++` / `--p.length` — the same writes, spelled as steps
		if ast.IsPrefixUnaryExpression(node) || ast.IsPostfixUnaryExpression(node) {
			var operator ast.Kind
			var operand *ast.Node
			if ast.IsPrefixUnaryExpression(node) {
				unary := node.AsPrefixUnaryExpression()
				operator, operand = unary.Operator, unary.Operand
			} else {
				unary := node.AsPostfixUnaryExpression()
				operator, operand = unary.Operator, unary.Operand
			}
			if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
				stepped := Unwrapped(operand)
				if _, isIndex := indexAccessOf(stepped, name); isIndex {
					ok = false
					return true
				}
				if root, path, isPath := propertyPathAdmittingRootOptionalStep(stepped); isPath &&
					root == name && len(path) == 1 && path[0] == "length" {
					ok = false
					return true
				}
			}
		}
		// `p[j]` read — the inner elem join, admitted only where the
		// element carries the pair; the index expression still scans
		if index, isIndex := indexAccessOf(node, name); isIndex {
			if !nestedPair {
				ok = false
				return true
			}
			visitIfPresent(index)
			return false
		}
		if root, path, isPath := propertyPathAdmittingRootOptionalStep(node); isPath && root == name {
			joined := strings.Join(path, ".")
			// `p.length` read — the inner len join, under the same
			// ".length"→".len" mapping the flattened array's own length
			// read wears
			if nestedPair && joined == "length" {
				return false
			}
			if _, isDeclared := declared[joined]; !isDeclared {
				ok = false
				return true
			}
			return false
		}
		if ast.IsIdentifier(node) && node.Text() == name && node != declarationName {
			if !aliasBareUseAdmissible(node) {
				ok = false
				return true
			}
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return ok
}

// aliasBareUseAdmissible classifies ONE bare occurrence of the alias
// name by the position that consumes it: a test consumer or a direct
// call argument admits; everything else refuses (the value would
// escape as an identity the slots cannot spell).
func aliasBareUseAdmissible(node *ast.Node) bool {
	current := node
	for {
		parent := current.Parent
		if parent == nil {
			return false
		}
		switch {
		case ast.IsParenthesizedExpression(parent):
			current = parent
			continue
		case ast.IsIfStatement(parent):
			return parent.AsIfStatement().Expression == current
		case ast.IsWhileStatement(parent):
			return parent.AsWhileStatement().Expression == current
		case ast.IsDoStatement(parent):
			return parent.AsDoStatement().Expression == current
		case ast.IsForStatement(parent):
			return parent.AsForStatement().Condition == current
		case ast.IsConditionalExpression(parent):
			return parent.AsConditionalExpression().Condition == current
		case ast.IsPrefixUnaryExpression(parent):
			// `!p` reads truthiness alone; the value never escapes
			if parent.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
				return true
			}
			return false
		case ast.IsTypeOfExpression(parent):
			return true
		case ast.IsBinaryExpression(parent):
			bin := parent.AsBinaryExpression()
			switch bin.OperatorToken.Kind {
			case ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken,
				ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken:
				// an equality against null/undefined reads presence and
				// answers a boolean — admissible wherever it sits
				other := bin.Left
				if other == current {
					other = bin.Right
				}
				other = Unwrapped(other)
				if other != nil && (other.Kind == ast.KindNullKeyword ||
					(ast.IsIdentifier(other) && other.Text() == "undefined")) {
					return true
				}
				return false
			case ast.KindAmpersandAmpersandToken, ast.KindBarBarToken:
				// `p && …` / `p || …` may hand p's own value onward —
				// admissible only where the chain's RESULT is itself a test
				current = parent
				continue
			}
			return false
		case ast.IsCallExpression(parent):
			call := parent.AsCallExpression()
			if call.Arguments != nil {
				for _, argument := range call.Arguments.Nodes {
					if argument == current {
						return true
					}
				}
			}
			return false
		default:
			return false
		}
	}
}

// ElementAliasResolvedSlot answers the slot a one-step path resolves to
// ONLY through the element-alias fallback — (slot, true) exactly where
// the ordinary spelled lookup fails and the alias route answers. The
// write side reads this: a write landing on such a slot is one element
// of many, so its effect must JOIN into the slot rather than replace it
// (AssignmentOfExpression's weak-update wrap).
func ElementAliasResolvedSlot(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if head == nil {
		return 0, false
	}
	root, path, ok := propertyPathAdmittingRootOptionalStep(head)
	if !ok || len(path) != 1 {
		return 0, false
	}
	if _, spelled := slotIndexOfName(context, root+"."+path[0]); spelled {
		return 0, false
	}
	rootNode := rootIdentifierOf(head)
	if rootNode == nil {
		return 0, false
	}
	return elementAliasSlotIndexOf(context, rootNode, path[0])
}

// ElementAliasHavocSlots answers the aliased element's member slots for
// a bare mention of the alias name — what a hand-over (`f(p)`) puts in
// unseen code's reach. The opaque havoc enumerator reads this beside
// flattenedSlotsUnder: the alias holds no slots under its own name, but
// the element it stands for does, and a callee may write any of them
// through the reference.
func ElementAliasHavocSlots(context *LoweringContext, node *ast.Node) []int {
	if node == nil || !ast.IsIdentifier(node) {
		return nil
	}
	target, ok := elementAliasTargetOf(context, node)
	if !ok {
		return nil
	}
	var out []int
	for _, member := range target.ElementMembers {
		if index, found := slotIndexOfName(context, member.SlotName); found {
			out = append(out, index)
		}
	}
	return out
}

// enclosingFunctionBodyOf walks a node up to the nearest enclosing
// function-like body — the block (or concise arrow expression) a local
// declared inside it is read within. Nil where no such ancestor exists
// (a declaration outside any function, which never reaches this file's
// callers in practice, since a top-level lowering always starts from a
// function declaration).
func enclosingFunctionBodyOf(node *ast.Node) *ast.Node {
	for current := node.Parent; current != nil; current = current.Parent {
		if ast.IsFunctionDeclaration(current) || ast.IsFunctionExpression(current) ||
			ast.IsArrowFunction(current) || ast.IsMethodDeclaration(current) ||
			ast.IsConstructorDeclaration(current) || ast.IsGetAccessorDeclaration(current) ||
			ast.IsSetAccessorDeclaration(current) {
			return current.Body()
		}
	}
	return nil
}

// arrayElementMemberLeafOf reads `xs[i].a` — a PropertyAccessExpression
// whose receiver is an ElementAccessExpression over a plain (non-optional)
// name — as the ELEM-LEAF spelling "xs.elem.a", the slot step 2
// (SummaryParameterEntriesIn's array-parameter branch, this agent's
// territory) lays out for a flattened array whose element is a record.
//
// The elem-leaf denotes the JOIN over every element the array can hold,
// the same weak-summary story the plain scalar elem slot already carries
// one member wide — `xs[i].a` for ANY `i` answers the same join, exactly
// as `xs[i]` alone already answers the join through the scalar elem slot.
// A deeper receiver (`xs[i].a.b`, a nested member two steps below the
// index) or a receiver that is not one plain ElementAccessExpression
// directly on an identifier both decline here — this reads the single
// step adjacent to an index, mirroring symbolKeyedLeafOf's own one-step
// shape.
//
// Only the SPELLING is derived from this answer; whether it resolves to
// an actual slot is slotIndexOfName's own question — a name whose array
// never expanded per-member (a scalar-elemented array, or an array whose
// element record only PARTIALLY matches this step) answers false there,
// exactly as an unresolved plain path already does. The HOLDER node
// rides out beside the member so the caller can gate the
// ".length"→".len" mapping on the holder's own layout.
func arrayElementMemberLeafOf(node *ast.Node) (holder *ast.Node, member string, ok bool) {
	if !ast.IsPropertyAccessExpression(node) {
		return nil, "", false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return nil, "", false
	}
	if !ast.IsIdentifier(access.Name()) {
		return nil, "", false
	}
	receiver := Unwrapped(access.Expression)
	if !ast.IsElementAccessExpression(receiver) {
		return nil, "", false
	}
	element := receiver.AsElementAccessExpression()
	if element.QuestionDotToken != nil {
		return nil, "", false
	}
	head := Unwrapped(element.Expression)
	if !ast.IsIdentifier(head) {
		return nil, "", false
	}
	return head, access.Name().Text(), true
}

// symbolKeyedLeafOf reads `p[S]` as the holder and the derived `#sym:`
// leaf name it steps to — the element-access twin of propertyPathOf's
// one-step reading, over the same stable key identity the flattening
// spelled the leaf with.
//
// A holder that is not a plain name, an optional step, or a key that is
// not a stable symbol const answers false: those name no leaf, which is
// the same answer the recognizer's own use scan gives them.
func symbolKeyedLeafOf(context *LoweringContext, node *ast.Node) (root string, leaf string, ok bool) {
	if context == nil || node == nil || !ast.IsElementAccessExpression(node) {
		return "", "", false
	}
	holder := Unwrapped(node.AsElementAccessExpression().Expression)
	if holder == nil || !ast.IsIdentifier(holder) {
		return "", "", false
	}
	name, isSymbolKey := SymbolKeyedFieldName(checkerOf(context.Flow), node)
	if !isSymbolKey {
		return "", "", false
	}
	return holder.Text(), name, true
}

// ObjectLocalDeclarationAssignments is the object-literal declaration's
// lowering: one ordinary assignment per LEAF, in literal order, each
// writing the leaf's own slot from its initializer through the shared
// RHS grammar. Declines unless EVERY leaf has a slot and every
// initializer lowers — a leaf left at its absent entry state would read
// later as undefined, which is not what the literal wrote.
func ObjectLocalDeclarationAssignments(context *LoweringContext, local ObjectLocal) ([]AssignmentTarget, bool) {
	out := make([]AssignmentTarget, 0, len(local.Keys))
	for _, key := range local.Keys {
		if key.Declared || key.Initializer == nil {
			// a DECLARED leaf has no row to lower: the declaration named the
			// member, and the initializer said nothing about its value. The
			// statement route declines and the declaration's own route — the
			// opaque call's havoc, the constructor's exit rows — is what
			// fills these slots.
			return nil, false
		}
		target, found := slotIndexOfName(context, key.SlotName)
		if !found {
			return nil, false
		}
		effect, ok := RhsEffect(context, context.Sorts[target], key.Initializer)
		if !ok {
			return nil, false
		}
		out = append(out, AssignmentTarget{Target: target, Effect: asVarStateEffect(effect)})
	}
	return out, true
}

// ObjectDeclarationAssignmentsOf is the lowering-side entry: a variable
// statement declaring ONE object-literal local, read as its per-leaf
// assignments. Syntax alone decides — the recognizer's admission is
// already recorded in the slot vector, so the gate here is simply that
// every derived slot name resolves. A record the summary declined has
// no such slots, so this declines too and the statement takes its
// former route.
func ObjectDeclarationAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0]
	// `const p = xs[i]` over a record-elemented flattened array: the
	// declaration is bookkeeping ONLY — p is an ALIAS of the slot xs.elem
	// already carries (elementAliasSlotIndexOf's own doc), so no state
	// moves here. Answering (nil, true) is what tells the caller this
	// statement is HANDLED with zero assignments, rather than falling
	// through to the havoc floor, which would otherwise havoc p's
	// (nonexistent) whole-name slot and mark the body porous at
	// "declaration" for a statement that in fact moved nothing and needs
	// nothing moved. Tried before the object-literal reading below, whose
	// own objectLiteralOfDeclaration check declines an ElementAccessExpression
	// initializer outright.
	if isAdmittedElementAlias(context, declaration) {
		return nil, true
	}
	literal := objectLiteralOfDeclaration(declaration)
	if literal == nil {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	keys, ok := flatKeysOfLiteral(literal, name, nil)
	if !ok {
		return nil, false
	}
	return ObjectLocalDeclarationAssignments(context, ObjectLocal{Declaration: declaration, Name: name, Keys: keys})
}

// isAdmittedElementAlias is whether `declaration` is a `const p = xs[i]`
// this file's alias recognizer (admitElementAlias) accepts — the same
// gate elementAliasSlotIndexOf consults at read time, run here from the
// declaration's OWN name node so the declaration-statement route and
// every later read of p agree about one admission.
func isAdmittedElementAlias(context *LoweringContext, declaration *ast.Node) bool {
	if !ast.IsVariableDeclaration(declaration) {
		return false
	}
	name := declaration.AsVariableDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		return false
	}
	_, ok := elementAliasTargetOf(context, name)
	return ok
}

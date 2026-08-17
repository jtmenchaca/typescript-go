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
	if spelling, ok := arrayElementMemberLeafOf(head); ok {
		return slotIndexOfName(context, spelling)
	}
	return 0, false
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
		if candidate.Key == member {
			return slotIndexOfName(context, candidate.SlotName)
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
	elementAliasedLocalsMu.Lock()
	held, remembered := elementAliasedLocals[declaration]
	elementAliasedLocalsMu.Unlock()
	if remembered {
		return held, held.Name != ""
	}
	local, admitted := admitElementAlias(context.Flow, c, declaration)
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
func admitElementAlias(flow *FlowContext, c *checker.Checker, declaration *ast.Node) (ArrayLocal, bool) {
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
	parameter := sourceSymbol.ValueDeclaration
	if !ast.IsParameterDeclaration(parameter) {
		return ArrayLocal{}, false
	}
	local, flattened := arrayParamSlotsIn(flow, parameter)
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
			Path:      []string{member.Key},
			Key:       member.Key,
			SlotName:  aliasName + "." + member.Key,
			Declared:  true,
			Sort:      member.Sort,
			TypeofTag: member.TypeofTag,
		})
	}
	if !usesAreAllDeclaredKeySteps(c, body, declaration, aliasName, keys, nil, func(string) bool { return false }) {
		return ArrayLocal{}, false
	}
	// usesAreAllDeclaredKeySteps admits a WRITE through a declared path
	// exactly as readily as a read — right for an OWNED flattened local
	// (its slot answers to no other name), wrong here: "p.a" is a SECOND
	// SPELLING of "xs.elem.a"'s own slot, and IndexOf resolves a write
	// target through the identical seam a read uses (AGENT-BRIEF's own
	// step 2 names the danger — every route in this package, not only the
	// object-local family, shares one IndexOf for both). A write through
	// "p.a = v" would therefore REPLACE the array's own weak-summary join
	// rather than joining into it, corrupting what every OTHER element's
	// read through "xs[j].a" is allowed to assume. The write-side JOIN
	// route AGENT-BRIEF step 2 describes is unbuilt in this file, so until
	// it lands, any write through the alias spelling must decline the
	// WHOLE aliasing rather than let IndexOf's ordinary write route treat
	// the shared slot as this local's own.
	if elementAliasHasWriteThrough(body, declaration, aliasName) {
		return ArrayLocal{}, false
	}
	return local, true
}

// elementAliasHasWriteThrough scans for any WRITE through the alias name
// — a plain, compound, or logical assignment whose target is `p` itself
// or a `p.<member>` path, or a `++`/`--` step on one. usesAreAllDeclaredKeySteps
// has already run and admits these as ordinary declared-path uses; this
// is the alias-specific refusal on top of it (see admitElementAlias's own
// comment on why a write here cannot be let through the shared IndexOf
// seam a plain replace would use).
func elementAliasHasWriteThrough(body *ast.Node, declarationNode *ast.Node, name string) bool {
	declarationName := declarationNode.AsVariableDeclaration().Name()
	found := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found {
			return true
		}
		if node == declarationName {
			return false
		}
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			kind := bin.OperatorToken.Kind
			isAssignmentToken := kind == ast.KindEqualsToken ||
				(kind >= ast.KindFirstAssignment && kind <= ast.KindLastAssignment)
			if isAssignmentToken {
				left := Unwrapped(bin.Left)
				if ast.IsIdentifier(left) && left.Text() == name {
					found = true
					return true
				}
				if root, _, isPath := propertyPathAdmittingRootOptionalStep(left); isPath && root == name {
					found = true
					return true
				}
			}
		}
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
				operand = Unwrapped(operand)
				if ast.IsIdentifier(operand) && operand.Text() == name {
					found = true
					return true
				}
				if root, _, isPath := propertyPathAdmittingRootOptionalStep(operand); isPath && root == name {
					found = true
					return true
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return found
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
// Only the SPELLING is built here; whether "xs.elem.a" resolves to an
// actual slot is slotIndexOfName's own question — a name whose array
// never expanded per-member (a scalar-elemented array, or an array whose
// element record only PARTIALLY matches this step) answers false there,
// exactly as an unresolved plain path already does.
func arrayElementMemberLeafOf(node *ast.Node) (spelling string, ok bool) {
	if !ast.IsPropertyAccessExpression(node) {
		return "", false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", false
	}
	if !ast.IsIdentifier(access.Name()) {
		return "", false
	}
	receiver := Unwrapped(access.Expression)
	if !ast.IsElementAccessExpression(receiver) {
		return "", false
	}
	element := receiver.AsElementAccessExpression()
	if element.QuestionDotToken != nil {
		return "", false
	}
	holder := Unwrapped(element.Expression)
	if !ast.IsIdentifier(holder) {
		return "", false
	}
	return holder.Text() + arrayElemSuffix + "." + access.Name().Text(), true
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

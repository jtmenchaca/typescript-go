// split from ir_summary_body.go — what a body does with an expanded parameter

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

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
	// the body HANDS the whole record to a call or construction
	// (`f(p)`, `new C(p)`) — or an INTERIOR of it, through a path the
	// leaves do not spell, to running code (`f(p.inner)`,
	// `p.ticks.map(cb)` — interiorPathReadAt's call arms): the interior
	// reference reaches the same unseen code a whole hand-over reaches,
	// so it takes the same treatment. The layout serves this by marking EVERY
	// leaf written and joining the leaves to HandOverHavocNames: no
	// code-running statement believes a leaf across the hand-over, the
	// leaves ride out Written so call sites read the exit states back
	// (recordParamRets), and the interior call statement itself either
	// EMBEDS a complete callee (whose own summary bounds what it did to
	// the leaves) or falls to the opaque floor and the body goes porous
	// — the honest cascade either way. The one channel this cannot
	// carry — the callee STORING the reference for later — cannot hide
	// inside a COMPLETE callee: a store to an outer name has no slot
	// and poisons the callee's own summary first.
	recordParameterHandedOver
	// the body does something to the record no slot can stand for: a
	// write through an UNDECLARED member or a deeper-than-declared path
	// (a declared leaf's write is served — writtenMembers), a STORE of
	// the whole record OR of an interior read under another name
	// (`const q = p`, `const q = p.inner` — the scan cannot follow the
	// alias's writes), a `delete` through any member (an absence claim
	// the declared shape contradicts), a computed step.
	recordParameterUnreadable
)

// recordParameterUseOf scans a body for every occurrence of an EXPANDED
// parameter's name and answers what the body does with it.
//
// `p.lo` in value position on a DECLARED member is the ordinary reading —
// it consumes the root and the step, and contributes nothing here.
//
// A WHOLE-NAME occurrence is classified by the position it stands in,
// because after the expansion there is no single slot denoting `p`:
//
//   - a SPREAD element (`{ ...p }`, `f(...p)` is not this — see below) and
//     a `return p` read the fields and hand out no writable reference the
//     body itself uses again. Their leaves stay believable, so the body
//     lowers whole and the whole-name expression takes the opaque floor
//     its own route already gives it.
//   - a CALL or NEW ARGUMENT (`f(p)`, `new C(p)`) and a STORE (`q = p`,
//     `xs.push(p)` — the push argument is a call argument) hand the
//     object to code that may store into it, and no route carries that
//     move back out to the caller's own leaves — the whole body refuses.
//
// A WRITE THROUGH ANY MEMBER (`p.lo = 1`, `p.lo += 1`, `p.lo++`,
// `delete p.lo`) refuses too, declared member or not: the write moves
// the caller's own object, and the entry vector a summary answers
// through carries no effect back out to it — the same reason a
// hand-over refuses.
//
// A PATH THE LEAVES DO NOT SPELL — a member the annotation never
// declared (`p.mid`), a path stopping short of a nested leaf
// (`p.inner` where only `p.inner.deep` is a leaf), a path reaching
// past one (`p.lo.x`, `p.ticks.map`) — is an INTERIOR read: it never
// resolves to a slot (reading it could not silently answer another
// slot's state — the lookup has nothing to answer with), but its VALUE
// may be a reference into the caller's own object, so the POSITION
// that consumes the value decides (interiorPathReadAt):
//
//   - a test, a `return`, or a spread reads the value and opens no
//     silent in-body write channel — read-whole;
//   - a call or new ARGUMENT, or the path standing as a CALLEE
//     (`p.ticks.map(…)`, `p.write(…)` — the skipped method row's own
//     call), hands the interior to running code — the hand-over havoc,
//     exactly as `f(p)` takes it;
//   - a STORE (`const q = p.inner`) refuses: a write through the
//     stored alias would move the caller's own leaf with no statement
//     mentioning `p`, which no havoc wiring covers.
//
// These make the body unreadable:
//
//   - a COMPUTED step on the bare name (`p[e]`) — an index nothing
//     spells;
//   - a DEEPER optional step over an interior path (`p.a?.b` where `a`
//     names no leaf) — the interior read's consumer is another member
//     step, which the position vocabulary does not admit.
//
// The answer is the WORST use found: one unreadable use declines the
// body whatever else it does.
func recordParameterUseOf(
	body *ast.Node,
	name string,
	members []recordParamMember,
) (recordParameterUse, map[string]struct{}) {
	use, writtenMembers, _ := recordParameterUseWithNestedRootsOf(body, name, members)
	return use, writtenMembers
}

// recordParameterUseWithNestedRootsOf is recordParameterUseOf's full
// answer, adding the NESTED-ROOT keys a whole-parameter destructuring
// declaration bound (destructuresOnlyDeclaredMembers' widened arm) —
// the root name of every member whose own leaf sits BELOW a declared
// key rather than at it (`parentViewBox` for a member expanded to
// `parentViewBox.x`). appendRecordParameterEntries reads this set to
// havoc every leaf under such a root: the destructured local is a live
// alias into the caller's object one level down, the same reference a
// hand-over argument is, so a write through it after this point must
// not be believed the way an ordinary declared-leaf write is.
func recordParameterUseWithNestedRootsOf(
	body *ast.Node,
	name string,
	members []recordParamMember,
) (recordParameterUse, map[string]struct{}, map[string]struct{}) {
	// declared is keyed on the JOINED path — "lo" for a flat member,
	// "inner.deep" for a nested one (strings.Join(member.Path, ".")),
	// never the bare Key: two members at different depths can share a
	// Key ("deep" under two different parents). A multi-step read is
	// admitted only where its OWN joined path names a declared leaf; a
	// path stopping short of one, or reaching past one, matches nothing
	// here and classifies as an interior read (interiorPathReadAt).
	declared := map[string]struct{}{}
	arrayPairLen := map[string]struct{}{}
	// nestedRoots names every member whose leaf lands BELOW its own
	// depth-1 key — the key itself is not "declared" (no leaf sits
	// there), but a destructuring element naming it is asking for the
	// nested FAMILY, not an undeclared member. destructuresOnlyDeclaredMembers
	// reads this to admit such an element as a TOP-bound alias instead
	// of refusing the whole declaration.
	nestedRoots := map[string]struct{}{}
	for _, member := range members {
		joined := strings.Join(member.Path, ".")
		declared[joined] = struct{}{}
		if len(member.Path) > 1 {
			nestedRoots[member.Path[0]] = struct{}{}
		}
		// a "len" leaf of a MEMBER'S own array pair (nestedMemberLeavesOf's
		// array arm, ir_summary_record_member_reading.go — `ticks: number[]`
		// answers "ticks.len") is also reachable by the SOURCE spelling
		// `.length` wears, the same ".length"→".len" mapping
		// PathSlotIndexOf/elementAliasSlotIndexOf already apply at the
		// lowering side; recorded here under the PARENT path (with "len"
		// dropped) so declaredPathOf can answer a `.length` read as
		// declared too. Gated on ArrayPair so an ordinary record member
		// that merely happens to be spelled "len" is never matched this way.
		if member.ArrayPair && len(member.Path) > 0 && member.Path[len(member.Path)-1] == "len" {
			parent := strings.Join(member.Path[:len(member.Path)-1], ".")
			arrayPairLen[parent] = struct{}{}
		}
	}
	// declaredPathOf answers whether a joined path names a declared leaf —
	// directly, or through the ".length"→".len" mapping over a member's
	// own array pair (`p.ticks.length` reads the same leaf `p.ticks.len`
	// does, Sankey.tsx's `node.sourceLinks.length` shape).
	declaredPathOf := func(joined string) bool {
		if _, ok := declared[joined]; ok {
			return true
		}
		if !strings.HasSuffix(joined, ".length") {
			return false
		}
		parent := strings.TrimSuffix(joined, ".length")
		_, ok := arrayPairLen[parent]
		return ok
	}
	worst := recordParameterMembersOnly
	// the worst use wins, and an unreadable one ends the walk
	note := func(use recordParameterUse) {
		if use > worst {
			worst = use
		}
	}
	// the DECLARED leaves the body writes — each is a caller-visible
	// effect the summary carries out through its Written row, exactly
	// the class-typed bundle's treatment (recordParamRets threads the
	// exit back onto the caller's own leaf slot)
	writtenMembers := map[string]struct{}{}
	// destructuredNestedRoots: the nested-root keys a whole-parameter
	// destructuring declaration actually bound, collected as
	// wholeRecordUseAt classifies each occurrence — appendRecordParameterEntries
	// havocs every leaf under each one
	destructuredNestedRoots := map[string]struct{}{}
	// writeTargetPath reads a write target through this parameter's
	// spelling: the joined member path, or ("", false) for another
	// name's target.
	writeTargetPath := func(target *ast.Node) (string, bool) {
		root, path, ok := propertyPathOf(Unwrapped(target))
		if !ok || root != name {
			return "", false
		}
		return strings.Join(path, "."), true
	}
	// writesThroughParameter classifies a node that WRITES through this
	// parameter's spelling: a write to a DECLARED leaf is SERVED (the
	// leaf's slot takes the assignment and its Written row carries the
	// exit out — noteWrite), any other write refuses. `delete` refuses
	// even on a declared leaf: it claims absence of a member the
	// declared shape states.
	noteWrite := func(path string, declaredLeaf bool) {
		if !declaredLeaf {
			note(recordParameterUnreadable)
			return
		}
		writtenMembers[path] = struct{}{}
	}
	// answers (rhs, handled): handled says the node was a write through
	// the parameter and was classified; rhs, when non-nil, is the
	// assignment's own right side, which the VISITOR still walks — a
	// hand-over or refusal hiding there (`p.lo = g(p)`) must classify
	// exactly as it would anywhere else
	writesThroughParameter := func(node *ast.Node) (*ast.Node, bool) {
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if path, isParam := writeTargetPath(bin.Left); isParam {
					_, declaredLeaf := declared[path]
					noteWrite(path, declaredLeaf)
					return bin.Right, true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if path, isParam := writeTargetPath(unary.Operand); isParam {
					_, declaredLeaf := declared[path]
					noteWrite(path, declaredLeaf)
					return nil, true
				}
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if path, isParam := writeTargetPath(unary.Operand); isParam {
					_, declaredLeaf := declared[path]
					noteWrite(path, declaredLeaf)
					return nil, true
				}
			}
		}
		if ast.IsDeleteExpression(node) {
			if _, isParam := writeTargetPath(node.AsDeleteExpression().Expression); isParam {
				note(recordParameterUnreadable)
				return nil, true
			}
		}
		return nil, false
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if worst == recordParameterUnreadable {
			return true
		}
		// the classifier notes for itself: a declared-leaf write lands in
		// writtenMembers, everything else notes unreadable inside. The
		// assignment's right side still walks — a use hiding there
		// (`p.lo = g(p)`) classifies as it would anywhere else.
		if rhs, handled := writesThroughParameter(node); handled {
			if rhs != nil {
				visit(rhs)
			}
			return true
		}
		// a declared member READ consumes the root and every step name, so
		// none of them reaches the bare-name test below. The path's OWN
		// joined spelling must name a declared leaf — a nested member's
		// leaf is declared under its own full path (member.Path, joined),
		// so `p.inner.deep` is admitted the same way `p.lo` always was. A
		// ROOT-ADJACENT optional step (`p?.lo`) reads the same leaf the
		// plain step does: the slot's entry state already carries what the
		// optional step can add — a holder whose annotation has an absent
		// arm marks every leaf MayBeAbsent (unionMembersOf's absent-arm
		// rule), so the read-out-as-undefined case sits inside the absent
		// admission the entry state states, and a holder with no absent
		// arm is the declared record, on which `p?.lo` IS `p.lo`. A deeper
		// optional step (`p.a?.b`) is not a path here and falls through. A
		// path that stops short of a declared leaf or reaches past one is
		// an INTERIOR read, classified by the position that consumes its
		// value (interiorPathReadAt).
		if root, path, isPath := propertyPathAdmittingRootOptionalStep(Unwrapped(node)); isPath && root == name {
			if !declaredPathOf(strings.Join(path, ".")) {
				note(interiorPathReadAt(node))
			}
			return false
		}
		// every other occurrence of the bare name is the WHOLE record, and
		// the POSITION it stands in decides what the body may still be
		// served
		if ast.IsIdentifier(node) && node.Text() == name && !isPropertyStepName(node) {
			note(wholeRecordUseAt(node, declared, nestedRoots, destructuredNestedRoots))
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	if worst == recordParameterUnreadable {
		// the body declines whole; the members noted before the refusal
		// name nothing the layout will lay out
		return worst, nil, nil
	}
	return worst, writtenMembers, destructuredNestedRoots
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
// HANDED-OVER, a bare CALL or NEW argument (`f(p)`, `new C(p)`): the
// callee may write the record's members, and the layout serves exactly
// that — every leaf marked Written and joined to HandOverHavocNames
// (recordParameterHandedOver's own doc). A SPREAD argument (`f(...p)`)
// stays refused: it passes the entries positionally, a shape no leaf
// row spells.
//
// READ-WHOLE also covers a TRUTHINESS TEST — the node stands as a `!`
// operand, an `&&`/`||` operand, or an `if`/`while`/ternary CONDITION
// (the condition itself, or a term of one built from `&&`/`||`/`!` over
// other such terms). Testing a value's truthiness reads it and hands out
// no reference the body could store through — spec-wise, ToBoolean
// (sec-toboolean, tmp/ecma262/spec.html) consumes the operand and
// produces a fresh boolean, never the operand itself, so the record can
// only escape through this position if it is ALSO used somewhere else
// that already gets its own classification (a `return`, a call argument,
// a store). The GUARD lowering (LowerGuard, ir_guard.go) is where this
// pays off: only a NON-OPTIONAL record parameter's whole name may fold
// as "always truthy" there — this classification is position-only and
// intentionally blind to optionality, exactly like every other arm here.
//
// READ-WHOLE also covers an EQUALITY OR IDENTITY TEST — the node stands
// as an operand of `===`, `!==`, `==`, or `!=` (`p === q`, `p == null`).
// IsStrictlyEqual and IsLooselyEqual (sec-isstrictlyequal,
// sec-abstract-equality-comparison, tmp/ecma262/spec.html) read both
// operand VALUES and answer a fresh boolean; neither algorithm stores
// either operand anywhere, so the position hands out no reference the
// body could write through — the same argument the truthiness test
// makes, one level narrower (two operands instead of one implicit
// ToBoolean coercion).
//
// READ-WHOLE also covers a `typeof` OPERAND — the node stands as the
// Expression of a TypeOfExpression (`typeof p`). The typeof operator
// (sec-typeof-operator) reads the operand's value and answers a fresh
// string tag; the operand itself never escapes.
//
// MEMBERS-ONLY, a DECLARATION DESTRUCTURING THE WHOLE PARAMETER —
// `const { lo } = p;` (Sankey.tsx's `const { targetNodes } = curNode;`
// is this shape). The parameter's own name never denotes a slot after
// expansion, but a destructuring declaration only ever READS members
// out of it — no route through this shape lets the RECEIVER value
// escape as a reference the body could store into, so it is no riskier
// than an ordinary `p.lo` read. It counts as members-only, not merely
// read-whole, when EVERY bound element is a plain identifier (no
// default, no rest, no computed key) naming EITHER a DECLARED depth-1
// member OR a NESTED-ROOT key (a member whose own leaf sits one or more
// steps below it — `parentViewBox` for a member expanded to
// `parentViewBox.x`). A depth-1 element binds its precise leaf exactly
// as before; a nested-root element binds an alias into the caller's
// object one level down, collected into `hoisted` so the caller havocs
// every leaf under it (appendRecordParameterEntries) — the same
// treatment a hand-over argument already takes, because a write through
// the alias later in the body would move that leaf with no statement
// spelling the parameter's own name. An element naming neither, or any
// non-plain element (a default, a rest, a computed key, a nested
// pattern), falls through to the refusal below: nothing here promises
// those shapes a slot to read from.
//
// The default refuses too: a bare mention this reading does not
// recognize — an assignment right side, an array element, a property
// value, or a destructuring declaration this reading cannot classify —
// stores the reference under a name the scan does not follow, so its
// writes cannot be ruled out either.
func wholeRecordUseAt(
	node *ast.Node,
	declared map[string]struct{},
	nestedRoots map[string]struct{},
	hoisted map[string]struct{},
) recordParameterUse {
	parent := node.Parent
	if parent == nil {
		return recordParameterUnreadable
	}
	if ast.IsReturnStatement(parent) {
		return recordParameterReadWhole
	}
	if ast.IsSpreadAssignment(parent) {
		// `{ ...p }` copies the fields out; `f(...p)` is a SpreadElement,
		// which is an argument and falls through to the refusal below
		return recordParameterReadWhole
	}
	if ast.IsCallExpression(parent) || ast.IsNewExpression(parent) {
		// the node must stand in ARGUMENT position, not callee position —
		// `p()` calls the record, which no leaf row spells
		if arguments := argumentsOf(parent); arguments != nil {
			for _, argument := range arguments {
				if argument == node {
					return recordParameterHandedOver
				}
			}
		}
	}
	if ast.IsVariableDeclaration(parent) {
		declaration := parent.AsVariableDeclaration()
		if declaration.Initializer == node {
			roots, ok := destructuresOnlyDeclaredMembers(declaration.Name(), declared, nestedRoots)
			if ok {
				for root := range roots {
					hoisted[root] = struct{}{}
				}
				return recordParameterMembersOnly
			}
		}
	}
	if isTruthinessTestPosition(node, parent) {
		return recordParameterReadWhole
	}
	if isEqualityTestOperand(node, parent) {
		return recordParameterReadWhole
	}
	if isTypeofOperand(node, parent) {
		return recordParameterReadWhole
	}
	if isInOperatorRightOperand(node, parent) {
		return recordParameterReadWhole
	}
	return recordParameterUnreadable
}

// isTruthinessTestPosition answers whether NODE stands in a position that
// only ever tests its truthiness: a `!` operand, an `&&`/`||` operand, or
// an `if`/`while`/ternary CONDITION. Each of these hands the value to
// ToBoolean (sec-toboolean, tmp/ecma262/spec.html) and nowhere else — no
// route through this position lets the value itself escape as a
// reference the body could store through.
func isTruthinessTestPosition(node *ast.Node, parent *ast.Node) bool {
	if ast.IsPrefixUnaryExpression(parent) {
		return parent.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken &&
			parent.AsPrefixUnaryExpression().Operand == node
	}
	if ast.IsBinaryExpression(parent) {
		bin := parent.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		return (kind == ast.KindAmpersandAmpersandToken || kind == ast.KindBarBarToken) &&
			(bin.Left == node || bin.Right == node)
	}
	if ast.IsIfStatement(parent) {
		return parent.AsIfStatement().Expression == node
	}
	if ast.IsWhileStatement(parent) {
		return parent.AsWhileStatement().Expression == node
	}
	if ast.IsDoStatement(parent) {
		return parent.AsDoStatement().Expression == node
	}
	if ast.IsConditionalExpression(parent) {
		return parent.AsConditionalExpression().Condition == node
	}
	return false
}

// isEqualityTestOperand answers whether NODE stands as an operand of
// `===`, `!==`, `==`, or `!=`. IsStrictlyEqual/the abstract equality
// comparison (sec-isstrictlyequal, sec-abstract-equality-comparison,
// tmp/ecma262/spec.html) read both operand values and answer a fresh
// boolean — neither algorithm stores an operand anywhere, so this
// position hands out no reference the body could write through, the
// same argument isTruthinessTestPosition makes for ToBoolean.
func isEqualityTestOperand(node *ast.Node, parent *ast.Node) bool {
	if !ast.IsBinaryExpression(parent) {
		return false
	}
	bin := parent.AsBinaryExpression()
	kind := bin.OperatorToken.Kind
	isEqualityKind := kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindExclamationEqualsEqualsToken ||
		kind == ast.KindEqualsEqualsToken || kind == ast.KindExclamationEqualsToken
	return isEqualityKind && (bin.Left == node || bin.Right == node)
}

// isTypeofOperand answers whether NODE is the operand of a
// TypeOfExpression (`typeof p`). The typeof operator
// (sec-typeof-operator, tmp/ecma262/spec.html) reads the operand's
// value and answers a fresh string tag; the operand itself never
// escapes.
func isTypeofOperand(node *ast.Node, parent *ast.Node) bool {
	return ast.IsTypeOfExpression(parent) && parent.AsTypeOfExpression().Expression == node
}

// isInOperatorRightOperand answers whether NODE stands as the RIGHT
// operand of `in` (`'key' in p`) — axisSelectors.ts's getDomainDefinition
// reads `!('domain' in axisSettings)` this way. The relational `in`
// evaluation (sec-relational-operators-runtime-semantics-evaluation,
// tmp/ecma262/spec.html) is `HasProperty(rightValue, ToPropertyKey(
// leftValue))`: it reads the right operand's value and answers a fresh
// boolean, never storing either operand anywhere — the same read-only
// shape isEqualityTestOperand already argues for `===`/`==`. The LEFT
// operand position (`p in q`, the record used as a property key) is a
// different question this arm does not answer and is left refused.
func isInOperatorRightOperand(node *ast.Node, parent *ast.Node) bool {
	if !ast.IsBinaryExpression(parent) {
		return false
	}
	bin := parent.AsBinaryExpression()
	return bin.OperatorToken.Kind == ast.KindInKeyword && bin.Right == node
}

// destructuresOnlyDeclaredMembers says whether a binding NAME is an
// object binding pattern whose every element is a plain identifier (no
// rest, no computed key) naming a DECLARED depth-1 member or a
// NESTED-ROOT key — the shape a whole-parameter destructuring
// declaration must wear to count as members-only rather than an
// unreadable whole-record use. Answers the NESTED-ROOT keys the pattern
// actually bound (possibly empty, non-nil on success) and whether the
// pattern qualifies at all.
//
// A RENAMED element (`const { lo: low } = p;`) reads member `lo` under
// local name `low` — the same PropertyName/Name split a binding-pattern
// PARAMETER element already reads (SummaryParameterEntriesIn's pattern
// arm).
//
// A DEFAULTED element naming a DECLARED depth-1 member
// (`const { offset = 0 } = options;` — getCartesianPosition.tsx's own
// shape) is admitted: the LOWERING side already has a route for exactly
// this shape (DestructuringWithDefaultsOf,
// ir_object_slots_destructuring.go — a definedness branch running the
// default only on the exactly-undefined leg, spec-cited there), it was
// only this classifier that refused the whole body before the lowering
// route was ever tried. Admitting here is what lets the statement REACH
// that route; the route's own gates (writeAndCallFree on the default
// expression, RhsEffect's own success) are independent and still apply
// at lowering time — an admission here is necessary, not sufficient, the
// same relationship a renamed element already has with its own
// slotIndexOfName resolution.
//
// A NESTED-ROOT element (`const { parentViewBox: alias } = options;`
// where `parentViewBox` expanded to `parentViewBox.x`, no depth-1 row of
// its own) is admitted ONLY plain and undefaulted: there is no source
// SLOT for `options.parentViewBox` to test definedness against or read
// a value from (slotIndexOfName finds none — only the nested leaf
// exists), so DestructuringWithDefaultsOf's eqUndef branch has nothing
// to test and a defaulted nested-root element stays refused. A plain
// nested-root element needs no slot at all: the lowering binds its local
// to an untracked value and moves on (bodySlot.TopEntry's own reasoning,
// SummaryParameterEntriesIn's binding-pattern-PARAMETER arm — reads of
// the bound name answer nothing, which is exactly what is known), and
// the caller havocs every leaf under the root because the alias can be
// written through later in the body.
//
// Any other shape — a non-pattern name, a rest, a computed key, a nested
// pattern, a defaulted nested-root element, or a name naming neither a
// declared leaf nor a nested root — answers (nil, false).
func destructuresOnlyDeclaredMembers(
	name *ast.Node,
	declared map[string]struct{},
	nestedRoots map[string]struct{},
) (map[string]struct{}, bool) {
	if name == nil || !ast.IsObjectBindingPattern(name) {
		return nil, false
	}
	elements := name.AsBindingPattern().Elements.Nodes
	if len(elements) == 0 {
		return nil, false
	}
	roots := map[string]struct{}{}
	for _, element := range elements {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil ||
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
		if _, isDeclared := declared[key]; isDeclared {
			continue
		}
		if _, isNestedRoot := nestedRoots[key]; isNestedRoot {
			if binding.Initializer != nil {
				// no source slot exists to test definedness against — a
				// defaulted nested-root element has nothing
				// DestructuringWithDefaultsOf can lower
				return nil, false
			}
			roots[key] = struct{}{}
			continue
		}
		return nil, false
	}
	return roots, true
}

// argumentsOf reads a call's or construction's argument nodes.
func argumentsOf(call *ast.Node) []*ast.Node {
	if ast.IsCallExpression(call) {
		if a := call.AsCallExpression().Arguments; a != nil {
			return a.Nodes
		}
		return nil
	}
	if a := call.AsNewExpression().Arguments; a != nil {
		return a.Nodes
	}
	return nil
}

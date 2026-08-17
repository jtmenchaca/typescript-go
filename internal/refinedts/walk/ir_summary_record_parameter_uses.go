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
	// (`f(p)`, `new C(p)`). The layout serves this by marking EVERY
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
	// the whole record under another name (`const q = p` — the scan
	// cannot follow the alias's writes), a `delete` through any member
	// (an absence claim the declared shape contradicts), an undeclared
	// member READ, a computed or optional step.
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
// These also make the body unreadable:
//
//   - a member the annotation never declared (`p.mid`) — no slot holds
//     it, and reading it would silently answer another slot's state.
//     THIS IS ALSO WHAT KEEPS THE METHOD SKIP HONEST: a method signature
//     contributes no leaf (scalarMemberListOf), so a body that calls
//     `p.write(…)` reads a one-step path on an undeclared name and lands
//     here. The call is refused, never admitted as an accounted-for leaf
//     use. Skipping widens which member LISTS read; it never widens
//     which USES are served;
//   - a path DEEPER than any declared leaf (`p.lo.x` where lo is a
//     scalar) — no such leaf exists. A path a NESTED member's own leaf
//     DOES declare (`p.inner.deep`) is the ordinary reading, admitted
//     the same way a one-step path always was — see below;
//   - a COMPUTED or OPTIONAL step (`p[e]`, `p?.lo`) — the first names no
//     member, the second reads a record that may be absent.
//
// The answer is the WORST use found: one unreadable use declines the
// body whatever else it does.
func recordParameterUseOf(
	body *ast.Node,
	name string,
	members []recordParamMember,
) (recordParameterUse, map[string]struct{}) {
	// declared is keyed on the JOINED path — "lo" for a flat member,
	// "inner.deep" for a nested one (strings.Join(member.Path, ".")),
	// never the bare Key: two members at different depths can share a
	// Key ("deep" under two different parents). A multi-step read is
	// admitted only where its OWN joined path names a declared leaf; a
	// path stopping short of one, or reaching past one, matches nothing
	// here and falls to the unreadable branch below.
	declared := map[string]struct{}{}
	for _, member := range members {
		declared[strings.Join(member.Path, ".")] = struct{}{}
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
		// so `p.inner.deep` is admitted the same way `p.lo` always was, and
		// a path that stops short of a declared leaf or reaches past one
		// matches nothing and falls to the refusal.
		if root, path, isPath := propertyPathOf(Unwrapped(node)); isPath && root == name {
			if _, isDeclared := declared[strings.Join(path, ".")]; !isDeclared {
				note(recordParameterUnreadable)
				return true
			}
			return false
		}
		// every other occurrence of the bare name is the WHOLE record, and
		// the POSITION it stands in decides what the body may still be
		// served
		if ast.IsIdentifier(node) && node.Text() == name && !isPropertyStepName(node) {
			note(wholeRecordUseAt(node, declared))
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
// MEMBERS-ONLY, a DECLARATION DESTRUCTURING THE WHOLE PARAMETER —
// `const { lo } = p;` (Sankey.tsx's `const { targetNodes } = curNode;`
// is this shape). The parameter's own name never denotes a slot after
// expansion, but a destructuring declaration only ever READS members
// out of it — no route through this shape lets the RECEIVER value
// escape as a reference the body could store into, so it is no riskier
// than an ordinary `p.lo` read. It counts as members-only, not merely
// read-whole, only when EVERY bound element is a plain identifier (no
// default, no rest, no computed key) naming a DECLARED depth-1 member —
// an element naming an undeclared member, or any non-plain element
// (a default, a rest, a computed key, a nested pattern), falls through
// to the refusal below: nothing here promises those shapes a slot to
// read from.
//
// The default refuses too: a bare mention this reading does not
// recognize — an assignment right side, an array element, a property
// value, or a destructuring declaration this reading cannot classify —
// stores the reference under a name the scan does not follow, so its
// writes cannot be ruled out either.
func wholeRecordUseAt(node *ast.Node, declared map[string]struct{}) recordParameterUse {
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
		if declaration.Initializer == node && destructuresOnlyDeclaredMembers(declaration.Name(), declared) {
			return recordParameterMembersOnly
		}
	}
	if isTruthinessTestPosition(node, parent) {
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

// destructuresOnlyDeclaredMembers says whether a binding NAME is an
// object binding pattern whose every element is a plain identifier (no
// default, no rest, no computed key) naming a DECLARED depth-1 member —
// the shape a whole-parameter destructuring declaration must wear to
// count as members-only rather than an unreadable whole-record use.
//
// A RENAMED element (`const { lo: low } = p;`) reads member `lo` under
// local name `low` — the same PropertyName/Name split a binding-pattern
// PARAMETER element already reads (SummaryParameterEntriesIn's pattern
// arm). Any other shape — a non-pattern name, a default, a rest, a
// computed key, a nested pattern, or a name this reading cannot resolve
// to a plain identifier — answers false.
func destructuresOnlyDeclaredMembers(name *ast.Node, declared map[string]struct{}) bool {
	if name == nil || !ast.IsObjectBindingPattern(name) {
		return false
	}
	elements := name.AsBindingPattern().Elements.Nodes
	if len(elements) == 0 {
		return false
	}
	for _, element := range elements {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || binding.Initializer != nil ||
			binding.Name() == nil || !ast.IsIdentifier(binding.Name()) {
			return false
		}
		key := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				return false
			}
			key = binding.PropertyName.Text()
		}
		if _, isDeclared := declared[key]; !isDeclared {
			return false
		}
	}
	return true
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

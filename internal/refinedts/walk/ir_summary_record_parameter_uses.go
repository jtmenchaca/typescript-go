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
	// UNUSED as an answer — kept as a distinct ordinal so the "worst use
	// wins" comparison below still reads as an ordered scale, and so a
	// caller switching on this type sees the retired name rather than a
	// silently reused one. Nothing constructs it: a hand-over to code
	// (a call/new argument, a store into another name) and a write
	// through a member both name no slot a summary can carry an effect
	// back through, so both refuse outright — see recordParameterUnreadable.
	recordParameterEscapesWhole
	// the body does something to the record no slot can stand for: a
	// write through ANY member (declared or not — a write moves the
	// caller's own object, which no summary entry carries back out), a
	// hand-over of the whole record to code (a call/new argument, a
	// store into another name), an undeclared member READ, a computed or
	// optional step, or a deep path.
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
	// no write through the parameter is ever served (see the doc above),
	// so this map is always empty — kept in the return shape the layout
	// still reads, rather than reworking every caller's signature for a
	// map that never holds a key
	writtenMembers := map[string]struct{}{}
	// writeThroughParameter answers whether a node writes through this
	// parameter's spelling at all — a WRITE position through an expanded
	// parameter's member is a caller-visible effect no summary carries
	// out, so every write refuses, declared member or not.
	writeThroughParameter := func(target *ast.Node) bool {
		root, _, ok := propertyPathOf(Unwrapped(target))
		return ok && root == name
	}
	// a node that WRITES through this parameter's spelling, whatever
	// member it names
	writesThroughParameter := func(node *ast.Node) bool {
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if writeThroughParameter(bin.Left) {
					return true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if writeThroughParameter(unary.Operand) {
					return true
				}
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if writeThroughParameter(unary.Operand) {
					return true
				}
			}
		}
		if ast.IsDeleteExpression(node) {
			if writeThroughParameter(node.AsDeleteExpression().Expression) {
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
// UNREADABLE, a bare CALL or NEW argument (`f(p)`, `new C(p)`, INCLUDING
// a spread one — `f(...p)` passes the object's own entries): the callee
// may store into it, and no write-back threading carries that move out
// to the caller — the same refusal a `const q = p` alias already takes.
// A summary that served this would let the callee's write silently
// stand in for the caller's own object with no route back.
//
// The default refuses too: a bare mention this reading does not
// recognize — an initializer, an assignment right side, an array
// element, a property value — stores the reference under a name the
// scan does not follow, so its writes cannot be ruled out either.
func wholeRecordUseAt(node *ast.Node) recordParameterUse {
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
	return recordParameterUnreadable
}

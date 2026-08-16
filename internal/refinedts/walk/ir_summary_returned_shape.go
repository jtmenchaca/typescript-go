// split from ir_summary_body.go — the returned value's members

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

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

// returnedWholeParameterMembers answers the member rows for a body whose
// EVERY return is a bare read of the SAME record-expanded parameter —
// `return person;`, where `person: { age: number }` already carries one
// entry slot per leaf ("person.age"). Unlike returnedLiteralShape's object
// case, no new slot is allocated and no new effect is written: the
// parameter's own entry slots already hold the value for every run, at
// entry AND at any later exit (a body that writes `person.age` before
// returning it leaves the write's own value sitting in that same slot,
// which is exactly what the return should read back). The member rows
// here ALIAS the existing bundle entries by index — pointing the returned
// object's "age" key at the same exit summaryMemberResult would read for
// `person.age` on its own.
//
// The rule mirrors returnedLiteralShape's own: one shape for the whole
// body. A body returning `person` on one path and something else (even
// `return person.age`, a narrower read) on another has no single member
// layout to alias, and keeps the scalar #ret alone.
func returnedWholeParameterMembers(body *ast.Node, bundleEntries []BundleEntry) ([]RetMemberEntry, RetShapeKind) {
	if body == nil || len(bundleEntries) == 0 {
		return nil, RetShapeNone
	}
	returns := returnedExpressionsOf(body)
	if len(returns) == 0 {
		return nil, RetShapeNone
	}
	var holder string
	for _, returned := range returns {
		head := Unwrapped(returned)
		if head == nil || !ast.IsIdentifier(head) {
			return nil, RetShapeNone
		}
		name := head.Text()
		if holder == "" {
			holder = name
		} else if holder != name {
			// two different names read on two paths: no single parameter's
			// leaves can stand for the whole body's returned value
			return nil, RetShapeNone
		}
	}
	if holder == "" {
		return nil, RetShapeNone
	}
	prefix := holder + "."
	var members []RetMemberEntry
	for _, entry := range bundleEntries {
		if !strings.HasPrefix(entry.Path, prefix) {
			continue
		}
		// only the parameter's own DEPTH-1 leaves name a key of the
		// returned object directly; a nested leaf ("person.address.city")
		// belongs to a member this reader does not reconstruct
		leaf := strings.TrimPrefix(entry.Path, prefix)
		if strings.Contains(leaf, ".") {
			continue
		}
		members = append(members, RetMemberEntry{Name: leaf, Index: entry.Index})
	}
	if len(members) == 0 {
		return nil, RetShapeNone
	}
	return members, RetShapeObject
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

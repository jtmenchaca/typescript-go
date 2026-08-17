// split from ir_summary_body.go — record parameters: intersections and unions

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// intersectionMembersOf reads an INTERSECTION annotation
// (`type W = NodeJS.WritableStream & WriteHeaders`) as one member list.
//
// WHAT AN INTERSECTION MEANS HERE. A value of `A & B` is a value of A and
// a value of B at once, so it carries EVERY member of A and every member
// of B. That is the whole rule, and it is why the sides union rather than
// meet: each side's members are individually promised to every value the
// annotation admits, which is exactly the promise a record expansion
// needs before a member may become a slot.
//
// Each intersectee is read by the SAME member reader every other route
// uses — a type literal off its own syntax, a named interface through the
// heritage-walking route, a nested alias recursively, cycle-guarded by
// the `visiting` path this walk already carries. So an intersection of
// two interfaces and the one interface spelling the same members expand
// identically.
//
// WHERE TWO SIDES NAME ONE MEMBER, mergeShadowedMembers keeps the later
// side's reading at the earlier side's position — the same discipline the
// heritage walk uses for a redeclared inherited property, and the same
// reason: the slot vector must not move because a second side restated a
// member. The two sides agreeing is the ordinary case; where they
// disagree about a member's SORT, an intersection member's true type is
// the two member types' own intersection, which this reader has no form
// for. It does not guess: a member the two sides sort differently is a
// member no single slot can stand for, and the WHOLE expansion declines
// rather than picking one side's sort. Picking would be the unsound
// move — a slot sorted number for a member some callers pass a string.
//
// WHERE THE SIDES DISAGREE ABOUT ABSENCE, the REQUIRED reading wins, and
// this one has an answer where the sort does not. A value of `A & B`
// satisfies both, so a member A declares required and B declares
// optional is required of every such value: the intersection of "number"
// and "number-or-absent" is "number". Taking the required reading is
// therefore the true one, not a guess — and it is also the conservative
// direction, since it never claims a value may be absent that must be
// there.
//
// AN UNREADABLE SIDE DECLINES THE WHOLE READING, and this is the part
// worth stating rather than assuming. The tempting argument runs: an
// intersection can only ADD members, so the readable side's members are
// still promised to every value, and expanding on them alone is sound.
// The promise half of that is true. What it misses is that every consumer
// reads a `true` answer as "these are the members, all of them" — which
// is why an unreadable SIDE is different in kind from an unreadable
// MEMBER: a member whose annotation does not sort is still NAMED, so the
// list stays complete and only the sort goes unknown, while a side this
// reader cannot read hides members it cannot even name. A record local's leaves
// (ir_object_slots.go) are laid out as the value's WHOLE flattening, and
// the uses that would observe the value as a value are then refused on
// the ground that every leaf is accounted for. Answering a partial list
// as if it were whole would let a use of an unnamed member read as
// accounted-for when nothing holds it. The honest reading of a side this
// reader cannot read is that it does not know what that side declares —
// not that it declares nothing — so the answer is false and the holder
// keeps its whole-name slot. A side becoming readable later widens the
// answer; nothing has to be taken back.
func intersectionMembersOf(
	ctx *FlowContext,
	holder string,
	intersection *ast.Node,
	visiting []*ast.Node,
	atEntry bool,
) ([]recordParamMember, bool) {
	sides := intersection.AsIntersectionTypeNode().Types
	if sides == nil || len(sides.Nodes) == 0 {
		return nil, false
	}
	var merged []recordParamMember
	sortOfKey := map[string]BindingKind{}
	tagOfKey := map[string]TypeofTag{}
	// whether any side so far declared this member REQUIRED
	requiredKey := map[string]bool{}
	for _, side := range sides.Nodes {
		var members []recordParamMember
		var readable bool
		switch {
		case ast.IsTypeLiteralNode(side):
			// a SIDE is a link: a literal side of nothing but methods
			// contributes no member and declines nothing
			members, readable = scalarMemberListWithCheckerIn(
				checkerOf(ctx), holder, side.AsTypeLiteralNode().Members.Nodes, nil, false, visiting)
		case ast.IsTypeReferenceNode(side):
			reference := side.AsTypeReferenceNode()
			// the entry reference's own refusal, at every side: type
			// arguments make the members depend on what was applied. A
			// QUALIFIED side name resolves like a plain one — it names one
			// entity, which is what `NodeJS.WritableStream & WriteHeaders`
			// needs from this reader.
			if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
				return nil, false
			}
			if !isResolvableTypeName(reference.TypeName) {
				return nil, false
			}
			members, readable = declaredTypeMembersOf(ctx, holder, reference.TypeName, visiting, false)
		}
		if !readable {
			return nil, false
		}
		// a member both sides name must be sorted the same on both, or no
		// one slot stands for it. Absence is noted per key and settled
		// once over the whole answer below. Identity is the FULL PATH,
		// not the bare Key — two members at different depths can share a
		// Key ("deep" under two different parents).
		for _, member := range members {
			path := strings.Join(member.Path, ".")
			sort, named := sortOfKey[path]
			if named && (sort != member.Sort || tagOfKey[path] != member.TypeofTag) {
				return nil, false
			}
			sortOfKey[path] = member.Sort
			tagOfKey[path] = member.TypeofTag
			requiredKey[path] = requiredKey[path] || !member.MayBeAbsent
		}
		merged = mergeShadowedMembers(merged, members)
	}
	// an intersection every side of which contributed no data member: at
	// an entry the holder keeps its whole-name slot, at a link the
	// intersection itself contributes nothing
	if atEntry && len(merged) == 0 {
		return nil, false
	}
	// the required reading applied once over the whole answer, so a side
	// declaring a member required settles it whether it came before or
	// after the side that declared it optional. Written into a COPY: a
	// single-side merge hands back the side's own slice, which another
	// reading may hold.
	settled := make([]recordParamMember, len(merged))
	copy(settled, merged)
	for at := range settled {
		if requiredKey[strings.Join(settled[at].Path, ".")] {
			settled[at].MayBeAbsent = false
		}
	}
	return settled, true
}

// unionMembersOf reads a UNION annotation (`p: A | B | C`) as the members
// EVERY arm declares.
//
// WHAT A UNION MEANS HERE, and why it is the mirror of the intersection
// above. A value of `A | B` is a value of A OR a value of B, so the only
// members the annotation promises to every such value are the ones EVERY
// arm declares — the arms' member lists INTERSECT rather than union. A
// member only one arm spells is a member the value may simply not have,
// and giving it a slot would name a leaf for a value that never carries
// it. A member every arm spells is there whichever arm the value is, and
// that is exactly the promise a record expansion needs before a member
// may become a slot.
//
// Each ARM is read by the SAME member reader every other route uses — a
// type literal off its own syntax, a named interface through the
// heritage-walking route, a nested alias recursively, cycle-guarded by
// the `visiting` path this walk already carries. So a union of two
// interfaces and the one interface spelling the members they share expand
// identically.
//
// A MEMBER THE ARMS SORT DIFFERENTLY degrades to an UNKNOWN-SORTED
// leaf rather than declining the whole expansion. Picking one arm's
// sort would be the unsound move — a slot sorted number for a member
// the value carries as a string whenever it is the other arm — but
// unknown claims nothing about the value and still promises the NAME,
// which every arm does declare. The leaf then admits only the
// definedness test, the same weaker-true-claim reading an unsortable
// member already takes inside one arm.
//
// AN `undefined` OR `null` ARM contributes no member list and is
// EXCLUDED from the intersection rather than declining it; every
// surviving member then wears MayBeAbsent. Soundness is the
// continuing-run argument: on a run where the value took the absent
// arm, ANY leaf read through the holder THROWS (sec-property-accessors'
// RequireObjectCoercible on an undefined or null base) and the run
// ends before the read produces a value — so a leaf's claims quantify
// over the runs where the holder was an object, exactly the runs the
// record arms describe. MayBeAbsent additionally covers the leaf's
// value being read out as undefined where the guard machinery reads
// the slot without a proof of presence: the absent admission is a
// superset of what any continuing run observes, weaker and never
// wrong. What this deliberately does NOT claim is the holder's own
// IDENTITY — a bare `entry` in value position still has no slot, and
// the truthy fold (ir_guard_truthy_record.go) demands a syntactically
// non-optional annotation, never mere expansion success.
//
// WHERE THE ARMS DISAGREE ABOUT ABSENCE, THE WEAKER PROMISE WINS — and
// this is the exact opposite of the intersection's rule, because the
// connective is. A value of `A | B` satisfies ONE of them, so a member A
// declares required and B declares optional is only optional of that
// value: the value may be the arm where the member is optional, and
// nothing in the annotation says which arm it is. So the union of
// "number" and "number-or-absent" is "number-or-absent", and the member
// is carried MayBeAbsent as soon as ANY arm marks it. Taking the required
// reading would claim a value must carry a member the B arm lets it
// omit — the unsound direction. Carrying the absence is the true reading
// and also the conservative one, since the absence rides the entry state
// and never claims a value is there.
//
// AN UNREADABLE ARM DECLINES THE WHOLE EXPANSION, verbatim the argument
// intersectionMembersOf makes for an unreadable side. The tempting move
// is to intersect over the readable arms alone and say the result is
// still promised — but every consumer reads a `true` answer as "these are
// the members, all of them", and an unreadable ARM hides members it
// cannot name (unlike an unsortable MEMBER, which is named and merely
// unknown-sorted). An arm this reader cannot read is an arm whose members it
// does not KNOW — not one that declares nothing — and intersecting
// against an unknown list would keep members that arm may well not have.
// The honest answer is false, and the holder keeps its whole-name slot. An
// arm becoming readable later widens the answer; nothing has to be taken
// back.
//
// AN EMPTY INTERSECTION DECLINES AT AN ENTRY. Arms sharing no member
// expand to nothing, and expanding to nothing is not expanding: the
// holder keeps its single whole-name slot, exactly as the entry rule
// states everywhere else. At a LINK the empty answer is the true one —
// the union side contributes no member and takes none away.
//
// A CLASS arm is read by whatever declaredTypeMembersOf makes of it, and
// that reader refuses a class outright. So a union with a class arm
// declines whole, which is the honest reading: this walk has no member
// list for that arm at all.
func unionMembersOf(
	ctx *FlowContext,
	holder string,
	union *ast.Node,
	visiting []*ast.Node,
	atEntry bool,
) ([]recordParamMember, bool) {
	arms := union.AsUnionTypeNode().Types
	if arms == nil || len(arms.Nodes) == 0 {
		return nil, false
	}
	// the running intersection: the first RECORD arm seeds it, every
	// later record arm keeps only what it also declares. An absent arm
	// (`undefined`, `null`) is skipped here and settled below.
	var common []recordParamMember
	seeded := false
	sawAbsentArm := false
	// whether any arm so far declared this member OPTIONAL — the weaker
	// promise, settled once over the whole answer below
	absentKey := map[string]bool{}
	for _, arm := range arms.Nodes {
		if isAbsentUnionArm(arm) {
			sawAbsentArm = true
			continue
		}
		members, readable := unionArmMembersOf(ctx, holder, arm, visiting)
		if !readable {
			return nil, false
		}
		byPath := map[string]recordParamMember{}
		for _, member := range members {
			// an arm spelling one member twice is a list the scalar reader
			// already refuses, so a collision here cannot happen; the map is
			// the arm's own lookup for the intersection step. Keyed on the
			// FULL PATH — two members at different depths can share a bare
			// Key ("deep" under two different parents).
			path := strings.Join(member.Path, ".")
			byPath[path] = member
			absentKey[path] = absentKey[path] || member.MayBeAbsent
		}
		if !seeded {
			// the first record arm's list, copied: the intersection is
			// narrowed in place below and the arm's own slice may be held by
			// another reading
			seeded = true
			common = make([]recordParamMember, len(members))
			copy(common, members)
			continue
		}
		kept := make([]recordParamMember, 0, len(common))
		for _, member := range common {
			armMember, declared := byPath[strings.Join(member.Path, ".")]
			if !declared {
				// a member this arm does not declare is a member the value may
				// not have — it leaves the intersection
				continue
			}
			// a member the arms sort differently keeps its NAME and loses
			// its sort: unknown promises the name and claims nothing about
			// the value (the doc's own argument above)
			if armMember.Sort != member.Sort || armMember.TypeofTag != member.TypeofTag {
				member.Sort = BindingKindUnknown
				member.TypeofTag = TypeofTagNone
			}
			kept = append(kept, member)
		}
		common = kept
		if len(common) == 0 {
			break
		}
	}
	// a union of absent arms alone (`undefined | null`) declares no
	// record member anywhere; arms sharing no member expand to nothing.
	// At an entry the holder keeps its whole-name slot, at a link the
	// union contributes nothing.
	if atEntry && len(common) == 0 {
		return nil, false
	}
	// the weaker promise applied once over the whole answer: an arm
	// declaring a member optional settles it whether it came before or
	// after the arm that declared it required, and an ABSENT ARM settles
	// every member at once — the holder itself may be the absent arm, and
	// a leaf read then throws rather than answers (the doc's
	// continuing-run argument), so absent admission is the superset claim
	for index := range common {
		if sawAbsentArm || absentKey[strings.Join(common[index].Path, ".")] {
			common[index].MayBeAbsent = true
		}
	}
	return common, true
}

// isAbsentUnionArm: the `undefined` keyword arm and the `null` literal
// arm — the two spellings a union writes an absent value with. Every
// other keyword arm (a `void`, a `string`) stays with unionArmMembersOf,
// which declines what it cannot read.
func isAbsentUnionArm(arm *ast.Node) bool {
	if arm.Kind == ast.KindUndefinedKeyword {
		return true
	}
	if ast.IsLiteralTypeNode(arm) {
		return arm.AsLiteralTypeNode().Literal.Kind == ast.KindNullKeyword
	}
	return false
}

// unionArmMembersOf reads ONE arm of a union as a member list.
//
// An arm is a LINK, never an entry: an arm that declares no data member
// is READ, and reading it to nothing is a true answer about that arm —
// it just leaves the running intersection empty, which the caller settles
// under its own entry rule. The arm forms admitted are the ones every
// other route admits: an inline type literal off its own syntax, and a
// type reference resolved through declaredTypeMembersOf under the entry
// reference's own refusals (type arguments make the members depend on
// what was applied; a name that is neither plain nor qualified names no
// one entity). Every other arm form — a literal type, a keyword, an
// array, a function type — is an arm this reader cannot read, and it
// declines the whole union.
func unionArmMembersOf(
	ctx *FlowContext,
	holder string,
	arm *ast.Node,
	visiting []*ast.Node,
) ([]recordParamMember, bool) {
	switch {
	case ast.IsTypeLiteralNode(arm):
		return scalarMemberListWithCheckerIn(checkerOf(ctx), holder, arm.AsTypeLiteralNode().Members.Nodes, nil, false, visiting)
	case ast.IsTypeReferenceNode(arm):
		reference := arm.AsTypeReferenceNode()
		if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) > 0 {
			return nil, false
		}
		if !isResolvableTypeName(reference.TypeName) {
			return nil, false
		}
		return declaredTypeMembersOf(ctx, holder, reference.TypeName, visiting, false)
	}
	return nil, false
}

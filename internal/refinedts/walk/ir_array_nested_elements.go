// split from ir_array_parameters.go — the ARRAY-VALUED element: the
// inner pair's expansion and the derived inner ArrayLocal the
// second-level alias resolves against

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// arrayElementPairMembers is the ARRAY-VALUED element's expansion: the
// element of `xs: number[][]` is itself an array, so it expands to the
// inner array's own pair spelled under the elem slot — "xs.elem.len"
// (the inner length, an ordinary number) and "xs.elem.elem" (the join
// of the inner elements) — the same ".len"/".elem" vocabulary the
// outer flattening already wears, one level down. An inner element
// that is itself a record or another array recurses through the SAME
// element reader, so its leaves land under "xs.elem.elem." with their
// paths prefixed by "elem" — `SankeyNode[][]` answers "xs.elem.len"
// plus one "xs.elem.elem.<member>" per SankeyNode member.
//
// Every slot here is a JOIN over the outer elements: "xs.elem.len" is
// the join of every inner array's length, "xs.elem.elem" the join of
// every element of every inner array — the weak-summary story the
// scalar elem slot already tells, told per member. The two pair
// members wear ArrayPair, which is what tells a `.length` read's
// mapping (and the inner-pair recognizers) that "len" here is the
// inner array's own length and not a record member that happens to be
// spelled "len".
// `visiting` carries the declaration nodes already on the member-reading
// path — the array hop passes it through unchanged, so a recursive type
// reached through an array member (`interface Node { children: Node[] }`)
// terminates at declaredTypeMembersOf's revisit check instead of
// expanding forever.
func arrayElementPairMembers(
	ctx *FlowContext, c *checker.Checker, arrayNode *ast.Node, innerElement *ast.Node, elemHolder string,
	visiting []*ast.Node,
) ([]recordParamMember, bool) {
	out := []recordParamMember{{
		Key:       "len",
		Path:      []string{"len"},
		SlotName:  elemHolder + arrayLenSuffix,
		Sort:      BindingKindNumber,
		TypeofTag: TypeofTagNumber,
		ArrayPair: true,
	}}
	childHolder := elemHolder + arrayElemSuffix
	if children, expanded := arrayParameterElementMembers(ctx, c, innerElement, childHolder, visiting); expanded {
		for _, child := range children {
			path := make([]string, 0, len(child.Path)+1)
			path = append(path, "elem")
			path = append(path, child.Path...)
			out = append(out, recordParamMember{
				Key:         child.Key,
				Path:        path,
				SlotName:    child.SlotName,
				Sort:        child.Sort,
				TypeofTag:   child.TypeofTag,
				MayBeAbsent: child.MayBeAbsent,
				ArrayPair:   child.ArrayPair,
			})
		}
		return out, true
	}
	// a scalar (or unreadable) inner element keeps the single elem leaf,
	// sorted by its own syntax first and the checker's element reading
	// where the syntax does not spell it — the same two readings the
	// outer elem slot's sort takes
	sort := typeNodeSort(innerElement)
	if sort == BindingKindUnknown && c != nil {
		sort = checkedElementSort(c, arrayNode)
	}
	tag := TypeofTagNone
	switch sort {
	case BindingKindNumber:
		tag = TypeofTagNumber
	case BindingKindString:
		tag = TypeofTagString
	}
	// booleans ride the number sort with their own typeof evidence —
	// scalarMemberListWithCheckerIn's rule, applied to the inner element
	elementNode := Unwrapped(innerElement)
	if elementNode.Kind == ast.KindParenthesizedType {
		elementNode = elementNode.AsParenthesizedTypeNode().Type
	}
	if elementNode.Kind == ast.KindBooleanKeyword {
		tag = TypeofTagBoolean
	}
	out = append(out, recordParamMember{
		Key:       "elem",
		Path:      []string{"elem"},
		SlotName:  childHolder,
		Sort:      sort,
		TypeofTag: tag,
		ArrayPair: true,
	})
	return out, true
}

// hasNestedElementPair says whether a flattened array's ELEMENT carries
// the inner ".len"/".elem" pair — a depth-1 "len" member wearing
// ArrayPair — which is what admits index reads one level down (`xs[i][j]`,
// an alias's `row[j]`) and the `.length`→".len" mapping on the element.
func hasNestedElementPair(local ArrayLocal) bool {
	for _, member := range local.ElementMembers {
		if member.ArrayPair && len(member.Path) == 1 && member.Path[0] == "len" {
			return true
		}
	}
	return false
}

// elementArrayLocalOf derives the INNER array's own ArrayLocal from an
// outer local whose element carries the nested pair — the layout a
// second-level element alias (`const node = row[j]` after `const row =
// xs[i]`) resolves against. The derived local's name is the outer ELEM
// SLOT itself ("xs.elem"): its pair is "xs.elem.len"/"xs.elem.elem",
// and its ElementMembers are the outer's "elem"-rooted members one
// level stripped, so the derivation composes at any depth. No slot is
// invented here — every SlotName answered is one the outer expansion
// already laid out.
func elementArrayLocalOf(outer ArrayLocal) (ArrayLocal, bool) {
	if !hasNestedElementPair(outer) {
		return ArrayLocal{}, false
	}
	inner := ArrayLocal{
		Declaration:  outer.Declaration,
		Name:         outer.ElemSlotName,
		LenSlotName:  outer.ElemSlotName + arrayLenSuffix,
		ElemSlotName: outer.ElemSlotName + arrayElemSuffix,
		Parameter:    outer.Parameter,
	}
	for _, member := range outer.ElementMembers {
		if len(member.Path) == 1 && member.Path[0] == "elem" && member.ArrayPair {
			inner.DeclaredElementSort = member.Sort
			continue
		}
		if len(member.Path) > 1 && member.Path[0] == "elem" {
			inner.ElementMembers = append(inner.ElementMembers, recordParamMember{
				Key:         member.Key,
				Path:        member.Path[1:],
				SlotName:    member.SlotName,
				Sort:        member.Sort,
				TypeofTag:   member.TypeofTag,
				MayBeAbsent: member.MayBeAbsent,
				ArrayPair:   member.ArrayPair,
			})
		}
	}
	return inner, true
}

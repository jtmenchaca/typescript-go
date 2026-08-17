// split from ir_array_slots.go — the array-typed PARAMETER: the sort
// read from its declared type, and the recognizers over a body's
// parameter list

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// arrayParameterElementSort is the element sort of a parameter declared
// as an array, read from its DECLARED TYPE.
//
// Two readings, the syntactic one first so a body lowering without a
// checker still flattens the common annotations:
//
//	number[] / readonly number[] / Array<number> → the number sort
//	string[] / readonly string[] / Array<string> → the string sort
//
// Anything else — a union element, an object element, a tuple, an
// unannotated parameter — goes to the checker, which reads the resolved
// element type under exactly the masking LocalSortResolved uses: a type
// wearing only number/boolean flags is the number sort, only string
// flags the string sort, and a mixture or anything else is unknown.
//
// An unknown element sort still FLATTENS: the two slots exist and the
// length half is exact, while tests on the element slot decline. That
// loses coverage on the elements and never soundness — the same bargain
// an empty literal's unknown-until-pushed element already makes.
func arrayParameterElementSort(c *checker.Checker, parameter *ast.Node) (BindingKind, bool) {
	typeNode := parameter.AsParameterDeclaration().Type
	if typeNode != nil {
		if sort, isArray := arrayTypeNodeElementSort(typeNode); isArray {
			if sort != BindingKindUnknown || c == nil {
				return sort, true
			}
			// an array by syntax whose ELEMENT the syntax does not spell:
			// the checker reads the element type
			return checkedElementSort(c, typeNode), true
		}
		return BindingKindUnknown, false
	}
	return BindingKindUnknown, false
}

// arrayTypeNodeElementSort reads an ARRAY type node and answers the sort
// its element wears by syntax alone. The second answer is whether the
// node is an array type at all — `number[]`, `readonly number[]`, and
// `Array<number>` all are; `number` and `{a: number}` are not.
func arrayTypeNodeElementSort(typeNode *ast.Node) (BindingKind, bool) {
	node := typeNode
	// `readonly T[]` wraps the array type; the readonly says nothing about
	// the element's sort, and the two slots are read-only in the lowering
	// either way
	if node.Kind == ast.KindTypeOperator {
		operator := node.AsTypeOperatorNode()
		if operator.Operator != ast.KindReadonlyKeyword {
			return BindingKindUnknown, false
		}
		node = operator.Type
	}
	if node.Kind == ast.KindParenthesizedType {
		node = node.AsParenthesizedTypeNode().Type
	}
	switch {
	case node.Kind == ast.KindArrayType:
		return typeNodeSort(node.AsArrayTypeNode().ElementType), true
	case node.Kind == ast.KindTypeReference:
		// `Array<number>` / `ReadonlyArray<number>` — one type argument,
		// and the name has to be the array constructor's own
		reference := node.AsTypeReferenceNode()
		if !ast.IsIdentifier(reference.TypeName) {
			return BindingKindUnknown, false
		}
		switch reference.TypeName.Text() {
		case "Array", "ReadonlyArray":
		default:
			return BindingKindUnknown, false
		}
		if reference.TypeArguments == nil || len(reference.TypeArguments.Nodes) != 1 {
			return BindingKindUnknown, false
		}
		return typeNodeSort(reference.TypeArguments.Nodes[0]), true
	}
	return BindingKindUnknown, false
}

// typeNodeSort is a type node's sort by its own syntax: the number and
// string keywords and their literal types, and nothing else. A union, a
// reference, an object type — all unknown, and the checker reading above
// is what resolves those where one is available.
func typeNodeSort(typeNode *ast.Node) BindingKind {
	node := typeNode
	if node.Kind == ast.KindParenthesizedType {
		node = node.AsParenthesizedTypeNode().Type
	}
	switch node.Kind {
	case ast.KindNumberKeyword, ast.KindBooleanKeyword:
		return BindingKindNumber
	case ast.KindStringKeyword:
		return BindingKindString
	case ast.KindLiteralType:
		literal := node.AsLiteralTypeNode().Literal
		switch {
		case ast.IsNumericLiteral(literal),
			literal.Kind == ast.KindTrueKeyword, literal.Kind == ast.KindFalseKeyword:
			return BindingKindNumber
		case ast.IsStringLiteral(literal):
			return BindingKindString
		}
	}
	return BindingKindUnknown
}

// arrayParameterElementMembers reads a parameter's declared array TYPE
// NODE and answers the RECORD member list its ELEMENT expands to — one
// member per property, spelled "xs.elem.a"/"xs.elem.b" — or declines
// where the element is not a record shape.
//
// Two element-annotation shapes expand, mirroring recordParamMembersIn's
// own two cases at the PARAMETER level, applied here to the array's
// element instead:
//
//	(a) a SYNTACTIC TYPE LITERAL element (`{ a: number, b: number }[]`) —
//	    read straight off the element type node, no context needed;
//	(b) a TYPE REFERENCE element naming an interface or a type alias of a
//	    type literal (`SankeyNode[]`) — resolved through namedTypeMembersOf
//	    exactly as a record parameter's own annotation resolves.
//
// `elemHolder` is the elem slot's own name ("xs.elem") — passing it as
// the holder to the member reader means every returned member's SlotName
// already reads "xs.elem.<member>", with no further joining needed by
// any caller.
//
// A scalar element (number, string), a union, an unresolvable reference,
// or any other non-record shape answers (nil, false): the array keeps
// its plain scalar elem slot, exactly as before this reader existed.
func arrayParameterElementMembers(
	ctx *FlowContext, c *checker.Checker, elementTypeNode *ast.Node, elemHolder string,
) ([]recordParamMember, bool) {
	if elementTypeNode == nil {
		return nil, false
	}
	node := Unwrapped(elementTypeNode)
	if node.Kind == ast.KindParenthesizedType {
		node = node.AsParenthesizedTypeNode().Type
	}
	if ast.IsTypeLiteralNode(node) {
		return scalarMemberListWithCheckerIn(c, elemHolder, node.AsTypeLiteralNode().Members.Nodes, nil, true, nil)
	}
	return namedTypeMembersOf(ctx, elemHolder, node)
}

// elementTypeNodeOf reads an ARRAY type node's own element type node —
// `number[]`'s `number`, `Array<{a: number}>`'s `{a: number}` — through
// the same readonly/paren/Array<T> unwrapping arrayTypeNodeElementSort
// uses to decide WHETHER a node is an array type at all. Returns nil
// where the node is not an array type by that same syntax.
func elementTypeNodeOf(typeNode *ast.Node) *ast.Node {
	node := typeNode
	if node.Kind == ast.KindTypeOperator {
		operator := node.AsTypeOperatorNode()
		if operator.Operator != ast.KindReadonlyKeyword {
			return nil
		}
		node = operator.Type
	}
	if node.Kind == ast.KindParenthesizedType {
		node = node.AsParenthesizedTypeNode().Type
	}
	switch {
	case node.Kind == ast.KindArrayType:
		return node.AsArrayTypeNode().ElementType
	case node.Kind == ast.KindTypeReference:
		reference := node.AsTypeReferenceNode()
		if !ast.IsIdentifier(reference.TypeName) {
			return nil
		}
		switch reference.TypeName.Text() {
		case "Array", "ReadonlyArray":
		default:
			return nil
		}
		if reference.TypeArguments == nil || len(reference.TypeArguments.Nodes) != 1 {
			return nil
		}
		return reference.TypeArguments.Nodes[0]
	}
	return nil
}

// checkedElementSort reads an array TYPE NODE's element sort through the
// host checker, under the same flag masking LocalSortResolved uses.
func checkedElementSort(c *checker.Checker, typeNode *ast.Node) BindingKind {
	t := c.GetTypeFromTypeNode(typeNode)
	if t == nil {
		return BindingKindUnknown
	}
	element := c.GetElementTypeOfArrayType(t)
	if element == nil {
		return BindingKindUnknown
	}
	flags := element.Flags()
	numOrBool := checker.TypeFlagsNumber | checker.TypeFlagsNumberLiteral |
		checker.TypeFlagsBoolean | checker.TypeFlagsBooleanLiteral
	if (flags&numOrBool) != 0 && (flags & ^numOrBool) == 0 {
		return BindingKindNumber
	}
	strOrLit := checker.TypeFlagsString | checker.TypeFlagsStringLiteral
	if (flags&strOrLit) != 0 && (flags & ^strOrLit) == 0 {
		return BindingKindString
	}
	return BindingKindUnknown
}

// ArrayParameterOf is the recognizer for an array-typed PARAMETER:
// `function f(ids: number[]) { … }` becomes the two slots "ids.len" and
// "ids.elem", exactly as a locally-declared array does.
//
// Why a parameter can flatten at all. The two slots are a length and the
// JOIN of the elements, and a parameter's values arrive from the caller
// rather than from an initializer — so the slots start at whatever the
// entry state says and every reading below them (`ids.length`, `ids[i]`,
// `ids.reduce(cb, seed)`) is the same reading a local array's slots get.
// Nothing about the recognized forms depends on where the values came
// from; only the WRITING of the two slots did, and a parameter is
// already bound when the body starts.
//
// The use scan is the local's, unchanged and total-or-decline: an alias,
// a return of the whole array, a `pop()`, a `length` write, or any other
// occurrence two scalar slots cannot spell declines the parameter, and
// the name then stays whole exactly as it does today.
//
// A parameter with a BINDING PATTERN name, a rest parameter, or a
// DEFAULT declines: a pattern binds names one level down that no slot
// spells, a rest holds the remainder rather than one declared array, and
// a default is an initializer the entry state does not run.
//
// `ctx` is optional (nil is the reading every caller with no context
// took before ElementMembers existed) — it is asked ONLY to resolve a
// NAMED-TYPE element (`SankeyNode[]`) to its member list; a syntactic
// type-literal element (`{a: number}[]`) resolves with no context, same
// as `c` (the checker) already did for the plain element SORT.
func ArrayParameterOf(ctx *FlowContext, c *checker.Checker, body *ast.Node, parameter *ast.Node) (ArrayLocal, bool) {
	if body == nil || parameter == nil || !ast.IsParameterDeclaration(parameter) {
		return ArrayLocal{}, false
	}
	declared := parameter.AsParameterDeclaration()
	if declared.DotDotDotToken != nil || declared.Initializer != nil {
		return ArrayLocal{}, false
	}
	name := declared.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return ArrayLocal{}, false
	}
	sort, isArray := arrayParameterElementSort(c, parameter)
	if !isArray {
		return ArrayLocal{}, false
	}
	spelled := name.Text()
	if !usesAreAllArrayFormsFrom(body, name, spelled) {
		return ArrayLocal{}, false
	}
	local := ArrayLocal{
		Declaration:         parameter,
		Name:                spelled,
		LenSlotName:         spelled + arrayLenSuffix,
		ElemSlotName:        spelled + arrayElemSuffix,
		Parameter:           true,
		DeclaredElementSort: sort,
	}
	if elementType := elementTypeNodeOf(declared.Type); elementType != nil {
		if members, isRecord := arrayParameterElementMembers(ctx, c, elementType, local.ElemSlotName); isRecord {
			local.ElementMembers = members
		}
	}
	return local, true
}

// ArrayParametersOf runs the parameter recognizer over a body's
// parameter list and answers the ones that flatten, keyed by the
// parameter declaration — the same shape ArrayLocalsOf answers in.
//
// A name declared as BOTH a parameter and a local keeps the local's
// reading: the local's own declaration is what the body's statements
// write, and two flattenings of one spelling would lay out two slot
// pairs under the same names. The caller holds the local table and
// passes the names it already flattened.
func ArrayParametersOf(
	c *checker.Checker, body *ast.Node, parameters []*ast.Node, takenNames map[string]struct{},
) map[*ast.Node]ArrayLocal {
	out := map[*ast.Node]ArrayLocal{}
	for _, parameter := range parameters {
		local, ok := ArrayParameterOf(nil, c, body, parameter)
		if !ok {
			continue
		}
		if _, taken := takenNames[local.Name]; taken {
			continue
		}
		out[parameter] = local
	}
	return out
}

// split from ir_array_slots.go — the array LITERAL's readers and the
// declaration lowering that dispatches over every initializer form

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// arrayLiteralOfDeclaration is the array literal a declaration's
// initializer is, through parens and casts — or nil.
func arrayLiteralOfDeclaration(declaration *ast.Node) *ast.Node {
	if !ast.IsVariableDeclaration(declaration) {
		return nil
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil
	}
	literal := Unwrapped(initializer)
	if !ast.IsArrayLiteralExpression(literal) {
		return nil
	}
	return literal
}

// arrayElementsOf reads an array literal's elements, or declines: a
// spread names no one element, and a nested object or array value is
// not a scalar the element slot can hold.
func arrayElementsOf(literal *ast.Node) ([]*ast.Node, bool) {
	var out []*ast.Node
	for _, element := range literal.AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsSpreadElement(element) || ast.IsOmittedExpression(element) {
			return nil, false
		}
		value := Unwrapped(element)
		if ast.IsObjectLiteralExpression(value) || ast.IsArrayLiteralExpression(value) {
			return nil, false
		}
		out = append(out, element)
	}
	return out, true
}

// ArrayDeclarationAssignmentsOf is the lowering-side entry for a
// flattened array's declaration: the len slot takes the literal's
// count as an exact constant, and the elem slot takes the JOIN of the
// literal's element effects. An EMPTY literal writes the absent-
// carrying constant into the elem slot — there is no element, so an
// index read must produce undefined, and the absent flag is where that
// lives.
func ArrayDeclarationAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0]
	// the array-to-array COPY, tried first: `const b = [...a]` writes b's
	// two slots from a's own. Ahead of the bridge, whose bare-name reader
	// would otherwise take the source for a Set.
	if copied, ok := copiedArrayDeclarationAssignmentsOf(context, declaration); ok {
		return copied, true
	}
	// the BRIDGE: `const a = [...m.values()]` writes the two array slots
	// from the collection's own — the count from its size and the element
	// join from its values (or its keys, for a `keys()` bridge). Both are
	// plain slot reads, so what the array's readers see afterwards is
	// indistinguishable from a literal's lowering.
	if bridged, ok := bridgedDeclarationAssignmentsOf(context, declaration); ok {
		return bridged, true
	}
	// the SPLIT: `const parts = s.split(sep)` writes the elem slot from
	// the kernel's drawn-from row over the receiver and the len slot from
	// nothing
	if split, ok := splitDeclarationAssignmentsOf(context, declaration); ok {
		return split, true
	}
	literal := arrayLiteralOfDeclaration(declaration)
	if literal == nil {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	lenSlot, elemSlot, ok := arraySlotsOf(context, name)
	if !ok {
		return nil, false
	}
	elements, elementsOk := arrayElementsOf(literal)
	if !elementsOk {
		return nil, false
	}
	out := []AssignmentTarget{{Target: lenSlot, Effect: constNumber(float64(len(elements)))}}
	if len(elements) == 0 {
		out = append(out, AssignmentTarget{Target: elemSlot, Effect: kernelbridge.AbsentConst()})
		return out, true
	}
	var joined kernelbridge.LoopEffect
	for index, element := range elements {
		effect, effectOk := RhsEffect(context, context.Sorts[elemSlot], element)
		if !effectOk {
			return nil, false
		}
		if index == 0 {
			joined = effect
			continue
		}
		joined = joinEffect(joined, effect)
	}
	out = append(out, AssignmentTarget{Target: elemSlot, Effect: joined})
	return out, true
}

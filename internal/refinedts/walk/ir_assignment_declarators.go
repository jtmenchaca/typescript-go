// split from ir_assignment.go — declarator assignments

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// DeclarationAssignment is declarationAssignment in the TS source: a
// single-name declaration's target and effect.
func DeclarationAssignment(context *LoweringContext, declarations []*ast.Node) (AssignmentTarget, bool) {
	if len(declarations) != 1 {
		return AssignmentTarget{}, false
	}
	return declaratorAssignment(context, declarations[0])
}

// declaratorAssignment lowers ONE declarator: an identifier name whose
// slot exists, holding its initializer's effect — or, with NO
// initializer, holding exactly the value the runtime gives it:
// undefined. `let x;` is not an unknown, it is an absent constant.
func declaratorAssignment(context *LoweringContext, declaration *ast.Node) (AssignmentTarget, bool) {
	d := declaration.AsVariableDeclaration()
	if !ast.IsIdentifier(d.Name()) {
		return AssignmentTarget{}, false
	}
	target, ok := IndexOf(context, d.Name())
	if !ok {
		return AssignmentTarget{}, false
	}
	if d.Initializer == nil {
		return AssignmentTarget{Target: target, Effect: kernelbridge.AbsentConst()}, true
	}
	effect, ok := RhsEffect(context, context.Sorts[target], d.Initializer)
	if !ok {
		return AssignmentTarget{}, false
	}
	return AssignmentTarget{Target: target, Effect: effect}, true
}

// declaratorAssignments lowers ONE declarator into however many slots it
// writes, which is what a declarator whose name is NOT one scalar slot
// needs.
//
// Three cases, and the difference is only where the name's slots are:
//
//   - the name has its OWN slot: the single rule above, unchanged.
//   - the name is FLATTENED (a record, an array, a collection, a
//     promise) and the declarator has NO INITIALIZER: `let box;` leaves
//     every path under box undefined, so every leaf takes the absent
//     constant. That is the runtime's own answer for the whole subtree,
//     not an approximation of it.
//   - the name has NO SLOT ANYWHERE: nothing is written, and nothing
//     lowered can read the name either, so there is nothing to be wrong
//     about. This is a SUCCESS with an empty write list, which is what
//     lets a statement declaring several names lower when only some of
//     them are tracked.
//
// A FLATTENED name WITH an initializer is not this route's: the
// declaration-shaped recognizers (the object, array and collection
// families) already read those, and each writes the leaves from the
// initializer's own parts. Reaching here with one means those declined,
// and writing absent over the leaves would claim the initializer wrote
// nothing — so it declines, exactly as it did before.
func declaratorAssignments(context *LoweringContext, declaration *ast.Node) ([]AssignmentTarget, bool) {
	d := declaration.AsVariableDeclaration()
	if !ast.IsIdentifier(d.Name()) {
		return nil, false
	}
	if _, has := IndexOf(context, d.Name()); has {
		assignment, ok := declaratorAssignment(context, declaration)
		if !ok {
			return nil, false
		}
		return []AssignmentTarget{assignment}, true
	}
	leaves := flattenedSlotsUnder(context, d.Name().Text())
	if len(leaves) == 0 {
		// a name nothing tracks: no write, and no reader either
		return nil, true
	}
	if d.Initializer != nil {
		return nil, false
	}
	out := make([]AssignmentTarget, 0, len(leaves))
	for _, leaf := range leaves {
		out = append(out, AssignmentTarget{Target: leaf, Effect: kernelbridge.AbsentConst()})
	}
	return out, true
}

// MultiDeclarationAssignmentsOf lowers `let a = 1, b = 2` — a variable
// statement with SEVERAL declarators, each an ordinary declarator the
// single route already reads, and each writing however many slots its
// own name holds. All-or-nothing: one declarator no route spells
// declines the statement to the next route (and ultimately the floor),
// exactly as the whole statement declined before this existed.
//
// A declarator naming something with NO slot writes nothing and does not
// decline the statement — the clause `let viewBox, label, positionAttrs;`
// lowers the names that are tracked and passes over the ones that are
// not, which is sound because nothing lowered can read an untracked
// name.
//
// Only statements with two or more declarators are this route's — a
// single declarator keeps its existing routes, object literals and
// calls included, which run earlier in the dispatch.
func MultiDeclarationAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) < 2 {
		return nil, false
	}
	out := make([]AssignmentTarget, 0, len(declarations))
	for _, declaration := range declarations {
		assignments, ok := declaratorAssignments(context, declaration)
		if !ok {
			return nil, false
		}
		out = append(out, assignments...)
	}
	return out, true
}

// The DESTRUCTURED-DECLARATION twin of importedHookFlattenedDeclarationStatement
// (ir_summary_call_flattened_target.go): `const { a, b } = f(...)` where
// `f` has a COMPILED SUMMARY (summaryCallStatement's own route, not the
// imported-hook tier) whose every return is the same object literal —
// returnedLiteralShape (ir_summary_returned_shape.go) laid out one
// RetMemberEntry per key. Sector.tsx:91-103's own shape:
// `const { circleTangency } = getTangentCircle({...})`, getTangentCircle
// declared a few lines above IN THE SAME FILE.
//
// callAssignmentShapeOf (ir_summary_call_surface.go) only ever reads
// ast.IsIdentifier(d.Name()) — a binding-pattern name declines it
// outright, so SummaryCallStatementOf never reaches summaryCallStatement
// for this shape at all. DestructuringAssignmentsOf
// (ir_object_slots_destructuring.go) is the only existing destructuring
// route, and it only ever reads a HOLDER that is already an
// identifier/this local — a bare call on the right is not a holder it
// resolves.
//
// threadRetMemberRets (ir_summary_call_ret_members.go) already carries
// the callee's own answer for "which slot is which member" — it drops
// every row to -1 today because the statement route's TARGET is a
// single scalar index with no member spellings under it. A destructured
// declaration is the opposite shape: no scalar target at all, but a
// NAME per bound member the layout already flattened as an ordinary
// local (collectSummaryLocals's object-binding-pattern case,
// ir_summary_local_slot_layout.go, comment (2): "each bound name gets
// its own slot"). This recognizer builds the call statement with
// target=-1 (nothing scalar to write) and threads each RetMemberEntry
// whose Name matches a bound key into that bound name's own slot,
// mirroring constructorFieldRets's by-name write-back one level down
// (by MEMBER NAME instead of by FIELD PATH under one target name).
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// destructuredCallDeclarationStatement recognizes `const { k: name, ... }
// = call(...)` where the callee resolves to a declaration with a
// COMPILED SUMMARY carrying an object RetShape, and threads each bound
// member's exit into the bound name's own slot. Declines wherever:
//
//   - the statement is not a single-declarator object-binding-pattern
//     declaration with a call initializer (DestructuringAssignmentsOf's
//     own gate, applied to a call RHS instead of a holder identifier);
//   - any binding element is a rest, a default, or a non-identifier name
//     (the same shapes SummaryParameterEntries/DestructuringAssignmentsOf
//     already refuse — no route here spells a nested or defaulted leaf);
//   - the call does not lower through summaryCallStatement at all (an
//     unresolved callee, a declining body, a non-COMPLETE outcome — every
//     existing gate inside summaryCallStatement/SummaryOutcomeOf applies
//     unchanged);
//   - the callee's RetShape is not RetShapeObject, or it carries no
//     RetMembers (a scalar-returning callee has nothing to thread here;
//     DestructuringAssignmentsOf's own "no leaf" reasoning covers it).
//
// A bound name with NO caller slot (nothing later reads it) is not an
// error — that binding element's row simply stays -1, exactly as
// threadRetMemberRets already drops an unspellable row. Every OTHER
// destructured member the caller has no name for also stays -1: dropping
// is sound for the same reason threadRetMemberRets's own doc gives —
// nothing lowered can read a member the caller never bound.
func destructuredCallDeclarationStatement(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil || !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	d := declarations[0].AsVariableDeclaration()
	if d.Initializer == nil || !ast.IsObjectBindingPattern(d.Name()) {
		return nil, false
	}
	call := Unwrapped(d.Initializer)
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	elements := d.Name().AsBindingPattern().Elements.Nodes
	if len(elements) == 0 {
		return nil, false
	}
	type boundLeaf struct {
		key  string
		slot int
		has  bool
	}
	leaves := make([]boundLeaf, 0, len(elements))
	for _, element := range elements {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || binding.Initializer != nil {
			return nil, false
		}
		if !ast.IsIdentifier(binding.Name()) {
			return nil, false
		}
		key := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				return nil, false
			}
			key = binding.PropertyName.Text()
		}
		slot, held := slotIndexOfName(context, binding.Name().Text())
		leaves = append(leaves, boundLeaf{key: key, slot: slot, has: held})
	}
	callStatement, built := summaryCallStatement(context, call, -1)
	if !built {
		return nil, false
	}
	calleeDeclaration := summaryCalleeOf(context, call)
	if calleeDeclaration == nil {
		return nil, false
	}
	calleeShape, shapeKnown := LowerSummaryBody(context.Flow, calleeDeclaration)
	if !shapeKnown || calleeShape.RetShape != RetShapeObject || len(calleeShape.RetMembers) == 0 {
		return nil, false
	}
	for _, leaf := range leaves {
		if !leaf.has {
			continue
		}
		for _, member := range calleeShape.RetMembers {
			if member.Name != leaf.key {
				continue
			}
			if member.Index < 0 || member.Index >= len(callStatement.Rets) {
				continue
			}
			callStatement.Rets[member.Index] = leaf.slot
		}
	}
	out := []kernelbridge.IrStatement{callStatement}
	out = append(out, arrayArgumentPostCallHavoc(context, call)...)
	return out, true
}

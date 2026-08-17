// The FLATTENED-TARGET twin of importedHookCallStatement
// (ir_summary_imported_hook_calls.go): `const center = f(...)` where
// `f` is a bare imported identifier (or any callee those earlier tiers
// already declined on) and `center` is not one scalar slot but a
// FLATTENED record local — `center.x`, `center.y` tracked as separate
// leaves because a later statement reads `center.x`.
//
// callAssignmentShapeOf (ir_summary_call_surface.go) reads a
// declarator's target through IndexOf(context, d.Name()), which asks
// for the BARE name's own slot. A flattened local never has one — only
// its leaf paths do (ir_object_slots_slot_index.go) — so that shape
// answered (0, nil, false) for every such declarator, and
// SummaryCallStatementOf declined the whole statement before
// SummaryCallOrHavocNamed's imported-hook tier ever got a target to
// serve. The corpus shape is tmp/recharts-src/src/util/PolarUtils.ts's
// polarToCartesian: `const center = polarToCartesian(cx, cy, r, a);`
// then `center.x`/`center.y` read back.
//
// THE SOUND ANSWER is the same one importedHookCallStatement already
// gives a scalar target — the call's own value is unknown — carried
// over every leaf of the flattened name instead of one slot. A
// foreign summary has no per-field return shape to thread (unlike a
// local composed call, whose LoweredSummary could in principle carry
// one out-row per field), so "unknown at every leaf" is the whole
// claim: nothing here pretends `center.x` came out any more exact than
// the call's own unread return type says.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// importedHookFlattenedDeclarationStatement recognizes `const name =
// call(...)` where `name` has no scalar slot of its own but DOES have
// flattened leaf slots, and `call` is a callee an earlier tier already
// declined on (this recognizer runs where SummaryCallStatementOf's own
// scalar-target route already failed — see its call site in
// SummaryCallStatementOf). Declines wherever the RHS is not a call, the
// name has no flattened slots at all (nothing here tracks it, and the
// caller's existing "no slots, no write" success already covers that
// shape through declaratorAssignments — this route only needs to run
// where a call sits on the right and a leaf set exists), or the
// arguments are not all write-and-call-free (importedHookArgumentsFree,
// the same proof importedHookCallStatement's scalar twin uses).
func importedHookFlattenedDeclarationStatement(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil || !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	d := declarations[0].AsVariableDeclaration()
	if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
		return nil, false
	}
	// a name with its OWN slot is the scalar route's (callAssignmentShapeOf
	// already served it, or declined it for a reason this route cannot
	// fix); only a name with NO scalar slot reaches here
	if _, hasScalar := IndexOf(context, d.Name()); hasScalar {
		return nil, false
	}
	leaves := flattenedSlotsUnder(context, d.Name().Text())
	if len(leaves) == 0 {
		return nil, false
	}
	call := Unwrapped(d.Initializer)
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	_, arguments, isHookCall := importedHookCallOf(context, call)
	if !isHookCall {
		return nil, false
	}
	if !importedHookArgumentsFree(context, arguments) {
		return nil, false
	}
	slots := make(map[int]struct{}, len(leaves))
	for _, leaf := range leaves {
		slots[leaf] = struct{}{}
	}
	return havocAssignments(slots), true
}

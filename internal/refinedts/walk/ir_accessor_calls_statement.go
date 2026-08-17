// split from ir_accessor_calls.go — the shared statement builder

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the shared statement builder ────────────────────────────────── */

// accessorCallStatement builds the IrStatementCall an accessor call
// lowers to, for both directions.
//
// It is summaryCallStatement's own construction with the argument walk
// replaced: an accessor has no argument LIST to lower (a getter has
// none at all; a setter's one value the caller already lowered), so the
// entry vector is built directly from the callee's shape.
//
// THE FILL RULES, taken from bundleRetsAndArgs and applied entry by
// entry:
//
//   - a "this.<field>" bundle entry takes a VAR of the caller's
//     "<receiverPath>.<field>" slot;
//   - a "this.<field>" entry the caller has NO slot for takes the
//     UNKNOWN effect — never the absent constant, which would claim the
//     field IS undefined, a claim about a value the caller never had;
//   - a WRITTEN "this.<field>" entry maps its own index back to the
//     caller's slot for the same spelling; where the caller has no slot
//     the ret stays -1 and the write lands nowhere;
//   - every entry the bundle rows do not name — the setter's parameter,
//     the callee's locals, its grown slots — takes the absent constant,
//     which is what the apply side's own padding sends, EXCEPT the done
//     flag, which enters {0} exactly as summaryCallStatement's padding
//     spells it.
//
// A NON-"this." bundle row (a record-parameter leaf) declines the whole
// statement: an accessor's parameter is one value, not a record, so such
// a row means the callee's layout is not the shape this site can fill.
//
// `value` non-nil fills entry 0 — the setter's declared parameter.
// `target` >= 0 points the return out-state at a caller slot; -1 drops
// it, which is the setter's case.
func accessorCallStatement(
	context *LoweringContext,
	shape LoweredSummary,
	blob kernelbridge.SummaryBlob,
	declaration *ast.Node,
	receiverPath string,
	value *kernelbridge.LoopEffect,
	target int,
) (kernelbridge.IrStatement, bool) {
	if shape.SlotCount <= 0 {
		return kernelbridge.IrStatement{}, false
	}
	// the entry vector: absent everywhere the rules below do not speak,
	// with the done flag's {0} exactly where the padding puts it
	args := make([]kernelbridge.LoopEffect, shape.SlotCount)
	for index := range args {
		if index == shape.DoneIndex {
			args[index] = doneDownEffect()
			continue
		}
		args[index] = kernelbridge.AbsentConst()
	}
	// rets: -1 says nothing reads that out-state
	rets := make([]int, shape.RetIndex+1)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 {
		if shape.RetIndex >= len(rets) {
			return kernelbridge.IrStatement{}, false
		}
		rets[shape.RetIndex] = target
	}
	// the setter's one declared parameter is entry 0 — the layout puts
	// declared parameters first and bundle entries after. `value` may be
	// a bare copy or a compound effect (setterValueEffect's own RhsEffect
	// read); either way this is the WHOLE entry-0 arg, never an operand,
	// so a bare copy upgrades to the whole-state read here.
	if value != nil {
		args[0] = asVarStateEffect(*value)
	}
	for _, entry := range shape.BundleEntries {
		if entry.Index < 0 || entry.Index >= len(args) {
			return kernelbridge.IrStatement{}, false
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			// a record-parameter leaf: an accessor's parameter is one value,
			// so a layout carrying such a row is not this site's shape
			return kernelbridge.IrStatement{}, false
		}
		slot, held := slotIndexOfName(context, receiverPath+"."+field)
		if !held {
			// the caller knows nothing about this field, and nothing is what
			// the entry must say
			args[entry.Index] = unknownEffect
			continue
		}
		args[entry.Index] = varStateEffect(slot)
		if entry.Written && entry.Index < len(rets) {
			rets[entry.Index] = slot
		}
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(declaration, blob),
		Args:   args,
		Rets:   rets,
	}, true
}

// doneDownEffect is the done flag's entry effect — exactly {0}, "not yet
// returned". summaryCallStatement's padding spells the same constant for
// the same slot; the two must agree or a spliced compile would start
// with the flag already up.
func doneDownEffect() kernelbridge.LoopEffect {
	return constNumber(0)
}

// split from ir_accessor_calls.go — the getter read and the setter write

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the getter read ─────────────────────────────────────────────── */

// GetterReadEffect lowers `o.x` where x is a GETTER with a compiled
// summary: the read is a zero-argument call, hoisted ahead of the
// statement it appears in, and the expression's value is the temp the
// call's return landed in.
//
// The shape, step by step:
//
//  1. resolve the accessor (AccessorDeclarationsOf) and demand its blob
//     from the registry (SummaryBlobFor) — building it on this first ask,
//     bottom-up, exactly as an ordinary callee's is built;
//  2. build the ENTRY VECTOR from the getter's own LoweredSummary: a
//     getter declares no parameters, so every entry it has is a bundle
//     entry, and each "this.<field>" one takes the caller's
//     "<receiverPath>.<field>" slot as a var — the fill rules
//     bundleRetsAndArgs states, applied here because there is no
//     CallExpression to hand that function;
//  3. allocate a temp for the return, point the statement's rets at it,
//     and append the statement to context.Hoisted;
//  4. answer `var temp`.
//
// Declines — each leaving the read exactly where it stood, at the floor:
// no accessor, no getter (a set-only property), no blob (the getter's
// own body declined), no hoist room (context.CanHoist false, or
// Allocate absent or past the budget), a receiver with no spelled path,
// and a lowering with no Flow or no SummaryTable.
//
// GATED ON CanHoist. A read inside a loop head, a branch test, or any
// other position whose statements the lowering has nowhere to put a
// hoisted statement is not a place a call may run: the call would then
// execute on a path the IR does not spell. The flag is the owner's
// answer to "is there a statement position here", and this route asks
// before it builds anything.
func GetterReadEffect(context *LoweringContext, access *ast.Node) (kernelbridge.LoopEffect, bool) {
	if context == nil || !context.CanHoist {
		return kernelbridge.LoopEffect{}, false
	}
	if context.Flow == nil || context.SummaryTable == nil || context.Allocate == nil {
		return kernelbridge.LoopEffect{}, false
	}
	getter, _, resolved := AccessorDeclarationsOf(context.Flow, access)
	if !resolved || getter == nil {
		return kernelbridge.LoopEffect{}, false
	}
	blob, hasBlob := SummaryBlobFor(context.Flow, getter)
	if !hasBlob {
		return kernelbridge.LoopEffect{}, false
	}
	shape, shapeKnown := LowerSummaryBody(context.Flow, getter)
	if !shapeKnown {
		return kernelbridge.LoopEffect{}, false
	}
	receiverPath, pathOk := accessorReceiverPathOf(access)
	if !pathOk {
		return kernelbridge.LoopEffect{}, false
	}
	// the return's landing slot: one fresh temp per read, named after the
	// path it read so a body's IR reads back to the syntax it came from
	temp, allocated := context.Allocate(
		accessorTempName(receiverPath, access),
		context.AccessorSortOf(getter),
		context.AccessorTypeofOf(getter),
	)
	if !allocated {
		return kernelbridge.LoopEffect{}, false
	}
	statement, built := accessorCallStatement(context, shape, blob, getter, receiverPath, nil, temp)
	if !built {
		return kernelbridge.LoopEffect{}, false
	}
	context.Hoisted = append(context.Hoisted, statement)
	return varEffect(temp), true
}

/* ── the setter write ────────────────────────────────────────────── */

// SetterWriteStatements lowers `o.x = e` where x is a SETTER with a
// compiled summary: the statements that call the setter's body with the
// written value as its one parameter entry.
//
// The differences from the getter read, and they are only these:
//
//   - the setter's ONE declared parameter takes the value effect the
//     caller already lowered, at entry 0 — the parameter is entry 0 by
//     the layout's own ordering (declared parameters first, bundle
//     entries after), so no search is needed;
//   - there is NO ret: a setter's value is discarded by the language, so
//     nothing lands anywhere and no temp is allocated;
//   - the WRITTEN bundle entries ride back into the caller's own slots,
//     which is the whole point — `this.x = v` through a setter that
//     assigns `this._x` must move the caller's "this._x" slot, or the
//     caller's stale knowledge of _x survives a write that changed it.
//
// The write-back rule is bundleRetsAndArgs' verbatim: a written entry's
// own index in the out vector maps to the caller's slot of the same
// spelling, and where the caller holds no slot the ret stays -1 and the
// write lands nowhere (nothing lowered can read that spelling, so no
// knowledge survives the call that the write would falsify).
//
// Declines: no accessor, no setter (a get-only property — the write is
// a runtime error or a silent no-op, neither of which this claims), no
// blob, a setter whose declared parameter count is not exactly one, a
// receiver with no spelled path, and a lowering with no Flow or table.
// CanHoist is asked as the read asks it: the statements have to go
// somewhere, and the caller splices them into a statement position.
func SetterWriteStatements(
	context *LoweringContext,
	access *ast.Node,
	value kernelbridge.LoopEffect,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || !context.CanHoist {
		return nil, false
	}
	if context.Flow == nil || context.SummaryTable == nil {
		return nil, false
	}
	_, setter, resolved := AccessorDeclarationsOf(context.Flow, access)
	if !resolved || setter == nil {
		return nil, false
	}
	// a setter takes exactly one value; anything else is not the form the
	// entry vector was laid out for
	if len(setter.Parameters()) != 1 {
		return nil, false
	}
	blob, hasBlob := SummaryBlobFor(context.Flow, setter)
	if !hasBlob {
		return nil, false
	}
	shape, shapeKnown := LowerSummaryBody(context.Flow, setter)
	if !shapeKnown {
		return nil, false
	}
	if shape.ParamCount < 1 {
		return nil, false
	}
	receiverPath, pathOk := accessorReceiverPathOf(access)
	if !pathOk {
		return nil, false
	}
	statement, built := accessorCallStatement(context, shape, blob, setter, receiverPath, &value, -1)
	if !built {
		return nil, false
	}
	return []kernelbridge.IrStatement{statement}, true
}

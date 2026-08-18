// split from ir_summary_body.go — the lowering itself

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// lowerSummaryBodyReporting is the lowering itself. Beyond the summary
// and its ok flag it answers TWO strings, at most one of them non-empty:
// `havoc` names the first construct that havocked on a lowering that
// SUCCEEDED, and `declined` names the construct the lowering refused.
// Each decline return below names the CONSTRUCT it refused, never a
// category — the tally is read to find out what to build next, so
// "a generator body" is worth having and "unsupported" is not.
func lowerSummaryBodyReporting(
	ctx *FlowContext,
	declaration *ast.Node,
	parameterSorts []parameterSlotSort,
	captures []capturedSlot,
) (summary LoweredSummary, havoc string, declined string, ok bool) {
	if !summaryLowerable(declaration) {
		if declaration != nil && declaration.Body() == nil {
			return LoweredSummary{}, "", "a body-less declaration", false
		}
		return LoweredSummary{}, "", "a generator body", false
	}
	body := declaration.Body()
	kernel := EngineKernelHeld()
	if kernel == nil {
		return LoweredSummary{}, "", "no kernel held", false
	}
	parameters := declaration.Parameters()
	layout, parameterDeclined, parameterOk := summaryParameterEntries(ctx, body, parameters, parameterSorts)
	if !parameterOk {
		return LoweredSummary{}, "", parameterDeclined, false
	}
	if captureDeclined, captureOk := appendSummaryCaptureEntries(captures, &layout); !captureOk {
		return LoweredSummary{}, "", captureDeclined, false
	}
	bundle := thisBundleOf(ctx, declaration)
	if bundleDeclined, bundleOk := appendSummaryThisBundleEntries(bundle, len(captures), &layout); !bundleOk {
		return LoweredSummary{}, "", bundleDeclined, false
	}
	slotLayout, slotDeclined, slotOk := summarySlotLayoutOf(ctx, body, parameters, layout)
	if !slotOk {
		return LoweredSummary{}, "", slotDeclined, false
	}
	table := &SummaryTableBuilder{}
	context := &LoweringContext{
		Bindings: slotLayout.Bindings,
		Sorts:    slotLayout.Sorts,
		Typeofs:  slotLayout.Typeofs,
		Narrow:   kernel.Narrow,
		Result:   &LoweringResult{Done: slotLayout.DoneIndex, Ret: slotLayout.RetIndex},
		ResolveCallee: func(callee *ast.Node) *ast.Node {
			called := ContractOf(ctx, callee)
			if called == nil {
				// `super.m(…)` names no symbol ContractOf can follow;
				// super_binding.go walks the enclosing class's heritage
				// to the base body the call RUNS — the dispatch is
				// static, so the resolved declaration's summary splices
				// exactly as a named callee's does
				called = SuperCallContract(ctx, callee)
			}
			if called == nil || !summaryLowerable(called.Declaration) {
				return nil
			}
			return called.Declaration
		},
		Inlining:     map[*ast.Node]struct{}{declaration: {}},
		Flow:         ctx,
		SummaryTable: table,
		// the returned value's member slots, so the return arm writes each
		// member into its own slot rather than writing the whole literal
		// off as unknown
		RetShape:   slotLayout.RetShape,
		RetMembers: slotLayout.RetMembers,
	}
	wireSummaryCaptureHavoc(context, bundle, layout, captures)
	// allocate grows the CONTEXT's own vectors, not copies of them: a slot
	// handed out past the initial layout must be readable through
	// context.Sorts at the index it was given, and a Go slice header
	// copied before the growth would not carry it. Capability is never
	// refused for cost — a body needing forty slots gets forty; cost
	// shows at the wall, never as a decline here.
	context.Allocate = func(name string, sort BindingKind, typeofTag TypeofTag) (int, bool) {
		context.Bindings = append(context.Bindings, name)
		context.Sorts = append(context.Sorts, sort)
		context.Typeofs = append(context.Typeofs, typeofTag)
		return len(context.Bindings) - 1, true
	}
	constructorPrelude := summaryConstructorPrelude(context, declaration, parameters)
	prelude, defaultEffects := summaryDefaultPrelude(context, layout.DefaultedSlots)
	stmts, statementsOk := LowerStatements(context, slotLayout.Statements)
	if !statementsOk {
		// the statement walk names the construct it refused ON — the
		// havoc floor's own first-wins report ("throw inside try",
		// "with statement", "labeled break crossing out"). The
		// histogram is the work queue, so its rows name syntax someone
		// can act on; the generic spelling is the fallback for a
		// decline that reached here without naming itself.
		named := DeclinedConstructOf(context)
		if named == "" {
			named = "a statement the lowering does not read"
		}
		return LoweredSummary{}, "", named, false
	}
	// ParamCount counts the ENTRIES the caller fills, not the declared
	// parameters: an expanded record parameter contributes one entry per
	// member, a method's read this-fields one each, and the captures one
	// each. The apply route's "everything past ParamCount enters absent"
	// rule reads this number, so it has to be the entry count or a record
	// parameter's later leaves — and every this-field — would enter absent.
	//
	// FirstHavoc is the set-once field the havoc routes fill: empty means
	// every statement was READ, non-empty names the first construct that
	// was stood in for. The door above turns the two into complete/porous.
	// the preludes run FIRST, in the runtime's own order: defaults land
	// before anything reads a parameter, then a constructor's field
	// initializers and parameter properties, then the body
	if len(constructorPrelude) > 0 {
		stmts = append(constructorPrelude, stmts...)
	}
	if len(prelude) > 0 {
		stmts = append(prelude, stmts...)
	}
	return LoweredSummary{
		Stmts:           stmts,
		ParamCount:      len(layout.Names),
		DoneIndex:       slotLayout.DoneIndex,
		RetIndex:        slotLayout.RetIndex,
		SlotCount:       len(context.Bindings),
		Table:           table.Blobs,
		BundleEntries:   layout.BundleEntries,
		DefaultEffects:  defaultEffects,
		ReturnsReceiver: bundle.ReturnsSelf,
		RetShape:        slotLayout.RetShape,
		RetMembers:      slotLayout.RetMembers,
	}, context.FirstHavoc, "", true
}

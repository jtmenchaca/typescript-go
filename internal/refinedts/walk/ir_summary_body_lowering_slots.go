// split from ir_summary_body_lowering.go — the statement and slot layout

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// summarySlotLayout is the body's statements beside the MUTABLE binding
// vectors the lowering starts from: the entries, then the locals, then
// #done and #ret, then the returned value's member slots.
type summarySlotLayout struct {
	Statements []*ast.Node
	Bindings   []string
	Sorts      []BindingKind
	Typeofs    []TypeofTag
	DoneIndex  int
	RetIndex   int
	RetShape   RetShapeKind
	RetMembers []RetMemberEntry
}

// summarySlotLayoutOf reads the body's statements and its locals and
// lays the slot vectors out on top of the entry layout.
func summarySlotLayoutOf(
	ctx *FlowContext,
	body *ast.Node,
	parameters []*ast.Node,
	layout summaryEntryLayout,
) (slotLayout summarySlotLayout, declined string, ok bool) {
	// a concise arrow body IS a single return
	var statements []*ast.Node
	if ast.IsBlock(body) {
		statements = append(statements, body.AsBlock().Statements.Nodes...)
	} else {
		statements = append(statements, syntheticReturnStatement(body))
	}
	// the collection no longer declines a body for what it cannot lay out
	// — a nested function is SKIPPED (its declarations are the inner
	// function's) and an array pattern's names are collected unknown-
	// sorted. Either way the statement holding the construct lowers by
	// its own route or by the havoc floor, so the body keeps its route
	// and its other statements keep their knowledge. The ok flag stays in
	// the signature because ir_inline_call.go reads it, and a false
	// answer there would still be a decline.
	var locals []*ast.Node
	var patterns []*ast.Node
	if ast.IsBlock(body) {
		collected, collectedPatterns, collectedOk := collectSummaryLocals(body)
		if !collectedOk {
			return slotLayout, "a body the local collection does not read", false
		}
		locals, patterns = collected, collectedPatterns
	}
	parameterNames := map[string]struct{}{}
	for _, name := range layout.Names {
		parameterNames[name] = struct{}{}
	}
	// an EXPANDED parameter's entries are spelled "p.lo", so the HOLDER
	// name is not among them; a local named `p` would then take its own
	// slot beside the leaves. (Such a body already declined above — the
	// declaration's own `p` is a whole-name occurrence the use scan
	// refuses — so this only keeps the two readings agreeing.)
	for _, parameter := range parameters {
		// a BINDING-PATTERN parameter has no holder name to reserve — its
		// entries ARE the bound names, already in parameterNames above
		name := parameter.AsParameterDeclaration().Name()
		if name == nil || !ast.IsIdentifier(name) {
			continue
		}
		if _, expanded := recordParamMembersIn(ctx, parameter); expanded {
			parameterNames[name.Text()] = struct{}{}
		}
		// an ARRAY parameter's entries are spelled "ids.len"/"ids.elem", so
		// the holder is not among them either — the same reservation, for
		// the same reason: a local named `ids` would otherwise lay a second
		// slot family under one name.
		if _, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
			parameterNames[name.Text()] = struct{}{}
		}
	}
	var slotChecker *checker.Checker
	if ctx != nil && ctx.P != nil {
		slotChecker = ctx.P.Checker
	}
	slots := localSlotsIn(ctx, slotChecker, body, locals, patterns, parameterNames)
	// MUTABLE vectors: composition allocates fresh slots past #ret
	bindings := append([]string{}, layout.Names...)
	sorts := append([]BindingKind{}, layout.Sorts...)
	typeofs := append([]TypeofTag{}, layout.Typeofs...)
	for _, slot := range slots {
		bindings = append(bindings, slot.Name)
		sorts = append(sorts, slot.Sort)
		typeofs = append(typeofs, slot.TypeofTag)
	}
	bindings = append(bindings, "#done", "#ret")
	sorts = append(sorts, BindingKindNumber, BindingKindUnknown)
	typeofs = append(typeofs, TypeofTagNumber, TypeofTagNone)
	doneIndex := len(bindings) - 2
	retIndex := len(bindings) - 1
	// THE RETURNED VALUE'S MEMBERS. A body returning an object or array
	// LITERAL takes one slot per member beside the scalar #ret, the way a
	// record parameter takes one per member — the return lowering writes
	// each member's own effect into its own slot, and the call sites
	// rebuild the value from the several exits. The rows ride out in
	// RetMembers so the layout's answer about WHICH slot is which member
	// is the one every consumer reads.
	//
	// The slots come after #ret, so #done and #ret keep the indices every
	// existing reader computes for them and nothing about the scalar route
	// moves.
	retMemberSlots, retShape := returnedLiteralShape(body)
	var retMembers []RetMemberEntry
	for _, slot := range retMemberSlots {
		retMembers = append(retMembers, RetMemberEntry{
			Name:  retMemberNameOfSlot(slot.Name),
			Index: len(bindings),
		})
		bindings = append(bindings, slot.Name)
		sorts = append(sorts, slot.Sort)
		typeofs = append(typeofs, slot.TypeofTag)
	}
	if len(bindings) > summarySlotBudget {
		return slotLayout, "a body past the slot budget", false
	}
	return summarySlotLayout{
		Statements: statements,
		Bindings:   bindings,
		Sorts:      sorts,
		Typeofs:    typeofs,
		DoneIndex:  doneIndex,
		RetIndex:   retIndex,
		RetShape:   retShape,
		RetMembers: retMembers,
	}, "", true
}

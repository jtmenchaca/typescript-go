// split from ir_summary_body_lowering.go — the statement and slot layout

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
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

// retDeclaredStringSort is the one sort #ret may wear beyond unknown:
// the STRING the body's own return annotation states. A `: string` body
// returns a string on every path — the annotation's own word, the same
// trust grade declaredParamSort carries for a parameter — and the string
// sort is what routes a returned template or `+`-concatenation through
// the sequence grammar: under the unconditional unknown, the return
// route's sort fallback read every summary-lowered return numerically,
// so a template holding a call had no reading at all and the body went
// porous at "return (template over call …)".
//
// Every other annotation keeps the unknown sort #ret always wore. A
// number or boolean annotation changes nothing the return route does
// not already do (its fallback sort IS number), and an absent or
// unspelled one claims nothing — so this deliberately moves only the
// sort that unlocks a reading nothing else reaches.
//
// The keyword spelling reads syntactically (annotationSort); an aliased
// annotation (`: D3ScaleType`, a union of string literals) resolves
// through the checker, constituent by constituent — a union type's own
// flags word says only "union", so the mask walks its members. Two
// member families are admitted:
//
//   - STRING-LIKE: string, a string literal, a template-literal type,
//     a string mapping — each is a subtype of string.
//   - ABSENT: undefined, null, void. `: string | undefined` still
//     returns a STRING wherever it returns a VALUE — the absent arms
//     ride the ret slot's own state (RhsEffect's absent-keyword arm
//     writes the absent constant under any sort), never its sort, so
//     admitting them here widens nothing the state does not already
//     carry. At least one string-like member must be present.
//
// An async body's `Promise<string>` wears object flags and stays
// unknown — the ret-as-inner convention is not read here.
func retDeclaredStringSort(ctx *FlowContext, body *ast.Node) BindingKind {
	if body == nil || body.Parent == nil {
		return BindingKindUnknown
	}
	typeNode := body.Parent.Type()
	if typeNode == nil {
		return BindingKindUnknown
	}
	if sort, _ := annotationSort(typeNode); sort == BindingKindString {
		return BindingKindString
	}
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return BindingKindUnknown
	}
	t := typereading.TypeAtLocation(ctx.P.Checker, typeNode)
	if t == nil {
		return BindingKindUnknown
	}
	constituents := []*checker.Type{t}
	if t.IsUnion() {
		constituents = t.Types()
	}
	strLike := checker.TypeFlagsString | checker.TypeFlagsStringLiteral |
		checker.TypeFlagsTemplateLiteral | checker.TypeFlagsStringMapping
	absent := checker.TypeFlagsUndefined | checker.TypeFlagsNull | checker.TypeFlagsVoid
	sawString := false
	for _, member := range constituents {
		flags := member.Flags()
		switch {
		case (flags&strLike) != 0 && (flags & ^strLike) == 0:
			sawString = true
		case (flags&absent) != 0 && (flags & ^absent) == 0:
			// rides the state, not the sort
		default:
			return BindingKindUnknown
		}
	}
	if sawString {
		return BindingKindString
	}
	return BindingKindUnknown
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
	sorts = append(sorts, BindingKindNumber, retDeclaredStringSort(ctx, body))
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
	// THE WHOLE-PARAMETER RETURN. `return person;`, where `person` is a
	// record-expanded parameter, carries its members already — one entry
	// slot per leaf, filled at call entry and left exact by any write the
	// body makes to them. No slot is allocated here and no statement runs:
	// the rows alias the parameter's OWN bundle-entry indices, so the exit
	// summaryMemberResult reads for each key is the same exit a bare
	// `person.age` read would answer. Tried only where the literal shape
	// above found nothing — the two are mutually exclusive by construction
	// (a body cannot return both an object literal and a bare identifier on
	// every path at once).
	if retShape == RetShapeNone {
		if aliasMembers, aliasShape := returnedWholeParameterMembers(body, layout.BundleEntries); aliasShape != RetShapeNone {
			retMembers = aliasMembers
			retShape = aliasShape
		}
	}
	// THE WHOLE-ARRAY RETURN — returnedWholeParameterMembers' array twin
	// (returnedWholeArrayMembers' own doc): `return result;` where every
	// return is the same array-flattened name aliases "#ret.len"/
	// "#ret.elem" onto that name's own pair, no new slot, no new effect.
	if retShape == RetShapeNone {
		if returned := returnedExpressionsOf(body); len(returned) > 0 && returned[0] != nil {
			if head := Unwrapped(returned[0]); head != nil && ast.IsIdentifier(head) {
				name := head.Text()
				lenIndex, elemIndex := -1, -1
				for at, binding := range bindings {
					switch binding {
					case name + ".len":
						lenIndex = at
					case name + ".elem":
						elemIndex = at
					}
				}
				if lenIndex >= 0 && elemIndex >= 0 {
					if aliasMembers, aliasShape := returnedWholeArrayMembers(body, name, lenIndex, elemIndex); aliasShape != RetShapeNone {
						retMembers = aliasMembers
						retShape = aliasShape
					}
				}
			}
		}
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

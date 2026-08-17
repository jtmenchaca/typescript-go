// split from ir_summary_body_lowering.go — the parameter entry loop

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// summaryParameterEntries lays out the DECLARED parameters: plain
// identifiers, no defaults, no rest. A RECORD parameter — an inline type
// literal, or a named interface or type alias the context resolves —
// EXPANDS to one entry per member (SummaryParameterEntriesIn), so the
// entry vector is no longer one-to-one with the declared parameters and
// the arrow route's per-parameter sorts are indexed by DECLARATION
// position while the entry vector runs ahead of it.
//
// The context threads down to the expansion HERE, and this is the only
// seam that holds one: the resolution it performs is remembered under
// the parameter node, so the ctx-less readings the call sites take
// answer the same member list (recordParamMembersIn's memo).
func summaryParameterEntries(
	ctx *FlowContext,
	body *ast.Node,
	parameters []*ast.Node,
	parameterSorts []parameterSlotSort,
) (layout summaryEntryLayout, declined string, ok bool) {
	layout.Names = make([]string, 0, len(parameters))
	layout.Sorts = make([]BindingKind, 0, len(parameters))
	layout.Typeofs = make([]TypeofTag, 0, len(parameters))
	for index, parameter := range parameters {
		entries, entriesOk := SummaryParameterEntriesIn(ctx, parameter)
		if !entriesOk {
			return layout, declinedParameterConstruct(parameter), false
		}
		// a BINDING-PATTERN parameter: its entries are the BOUND names,
		// each an ordinary scalar slot the call sites fill from the
		// argument object's member (entry.Key). Before the record branch,
		// which reads the same annotation but keys by member order — the
		// pattern's order and subset are the entries' own.
		if pd := parameter.AsParameterDeclaration(); pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) {
			if index < len(parameterSorts) {
				return layout, "a binding-pattern parameter of an arrow argument", false
			}
			for _, entry := range entries {
				layout.Names = append(layout.Names, entry.Name)
				layout.Sorts = append(layout.Sorts, entry.Sort)
				layout.Typeofs = append(layout.Typeofs, entry.TypeofTag)
			}
			continue
		}
		if members, expanded := recordParamMembersIn(ctx, parameter); expanded {
			recordDeclined, recordOk := appendRecordParameterEntries(
				body, parameter, index, parameterSorts, entries, members, &layout)
			if !recordOk {
				return layout, recordDeclined, false
			}
			continue
		}
		// an ARRAY-TYPED parameter takes the two slots a flattened array
		// local takes, "ids.len" and "ids.elem", in the parameter's own slot
		// position. The pair comes from SummaryParameterEntriesIn — the same
		// entries list the call sites walk — so the caller's argument vector
		// and this layout stay one answer about how many entries the
		// parameter is worth.
		//
		// No bundle row rides out: the two slots hold a length and the JOIN
		// of the elements, not fields of the caller's object, and nothing a
		// caller spells maps onto them the way "q.lo" maps onto a record
		// leaf. The values enter from the entry state and go nowhere back.
		if _, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
			// the arrow route ordinarily fills ONE entry per declared
			// parameter with a site sort, which the two-slot pair has no
			// single entry for — UNLESS the site itself marked this
			// position Array, meaning it is filling the pair (two effects,
			// arrowCallStatement's own lockstep with convertReduceArrow).
			// A site sort present but NOT marked Array is a genuine
			// mismatch (a scalar-filled position whose declared type reads
			// as an array) and still refuses.
			if index < len(parameterSorts) && !parameterSorts[index].Array {
				return layout, "an array parameter of an arrow argument", false
			}
			for _, entry := range entries {
				layout.Names = append(layout.Names, entry.Name)
				layout.Sorts = append(layout.Sorts, entry.Sort)
				layout.Typeofs = append(layout.Typeofs, entry.TypeofTag)
			}
			continue
		}
		// a CLASS-TYPED parameter is a slot bundle: the fields its body
		// READS become entries spelled "wrapper.<field>", after whatever
		// expansions came before, in the census's declaration order. The
		// census is the one report the call sites read back, so the rows
		// ride out in BundleEntries with the write flags it found.
		if _, census, _, isBundle := BundleParamCensus(ctx, body, parameter); isBundle && census.Believable() {
			// the arrow route fills ONE entry per declared parameter with a
			// site sort, which an expanded bundle has no single entry for
			if index < len(parameterSorts) {
				return layout, "a class-typed parameter of an arrow argument", false
			}
			written := map[string]struct{}{}
			for _, field := range census.Writes {
				written[field.SlotName] = struct{}{}
			}
			for _, field := range census.Reads {
				_, isWritten := written[field.SlotName]
				layout.BundleEntries = append(layout.BundleEntries, BundleEntry{
					Path:    field.SlotName,
					Index:   len(layout.Names),
					Written: isWritten,
				})
				layout.Names = append(layout.Names, field.SlotName)
				layout.Sorts = append(layout.Sorts, field.Sort)
				layout.Typeofs = append(layout.Typeofs, field.TypeofTag)
			}
			if len(census.Reads) > 0 {
				continue
			}
		}
		if pd := parameter.AsParameterDeclaration(); pd.Initializer != nil {
			layout.DefaultedSlots = append(layout.DefaultedSlots, defaultedParameterSlot{
				Slot:        len(layout.Names),
				Initializer: pd.Initializer,
			})
		}
		layout.Names = append(layout.Names, entries[0].Name)
		// the site's sort where the arrow route supplied one, the
		// declaration's own annotation otherwise. A supplied sort is what
		// the entry the site fills already wears, so the summary quantifies
		// over exactly the values that entry can take.
		if index < len(parameterSorts) {
			layout.Sorts = append(layout.Sorts, parameterSorts[index].Sort)
			layout.Typeofs = append(layout.Typeofs, parameterSorts[index].TypeofTag)
			continue
		}
		layout.Sorts = append(layout.Sorts, entries[0].Sort)
		layout.Typeofs = append(layout.Typeofs, entries[0].TypeofTag)
	}
	return layout, "", true
}

// split from ir_summary_body_lowering.go — the record parameter branch

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// appendRecordParameterEntries lays out an EXPANDED record parameter's
// leaves, and says what an expanded parameter's own name is used for,
// apart from reading its declared members.
//
// A READ of the whole record — a spread (`{ ...p, y: 1 }`), a
// `return p` — copies the fields out and hands no reference this
// body stores through, so every leaf keeps its value and the
// body lowers. The whole-name expression itself takes the opaque
// floor its own route gives it.
//
// Every other whole-name use REFUSES (recordParameterUseOf's doc): a
// hand-over (`f(p)`, `q = p`) and a write through any member (`p.lo =
// 1`) both move the caller's own object, and the entry vector a summary
// answers through carries no effect back out to it. None of these
// leaves ever goes out Written, so BundleEntries below always reports
// unwritten rows.
func appendRecordParameterEntries(
	body *ast.Node,
	parameter *ast.Node,
	index int,
	parameterSorts []parameterSlotSort,
	entries []bodySlot,
	members []recordParamMember,
	layout *summaryEntryLayout,
) (declined string, ok bool) {
	use, _ := recordParameterUseOf(
		body, parameter.AsParameterDeclaration().Name().Text(), members)
	if use == recordParameterUnreadable {
		return "a whole-record parameter use", false
	}
	// the arrow route fills ONE entry per declared parameter with a
	// site sort, which an expanded parameter has no single entry for
	if index < len(parameterSorts) {
		return "a record parameter of an arrow argument", false
	}
	for _, entry := range entries {
		layout.BundleEntries = append(layout.BundleEntries, BundleEntry{
			Path:    entry.Name,
			Index:   len(layout.Names),
			Written: false,
		})
		layout.Names = append(layout.Names, entry.Name)
		layout.Sorts = append(layout.Sorts, entry.Sort)
		layout.Typeofs = append(layout.Typeofs, entry.TypeofTag)
	}
	return "", true
}

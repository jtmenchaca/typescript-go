// split from ir_summary_body_lowering.go — the record parameter branch

package walk

import (
	"strings"

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
// A WRITE to a DECLARED leaf (`p.lo = 1`) is served: the leaf's row
// goes out Written, and the call sites thread the exit back onto the
// caller's own leaf slot (recordParamRets) — the class-typed bundle's
// own treatment. A HAND-OVER (`f(p)`) is served by the havoc-and-
// write-back reading recordParameterHandedOver documents: every leaf
// Written and joined to HandOverHavocNames. A STORE (`q = p`) and a
// write through anything undeclared still refuse
// (recordParameterUnreadable).
func appendRecordParameterEntries(
	body *ast.Node,
	parameter *ast.Node,
	index int,
	parameterSorts []parameterSlotSort,
	entries []bodySlot,
	members []recordParamMember,
	layout *summaryEntryLayout,
) (declined string, ok bool) {
	use, writtenMembers := recordParameterUseOf(
		body, parameter.AsParameterDeclaration().Name().Text(), members)
	if use == recordParameterUnreadable {
		return "a whole-record parameter use", false
	}
	// the arrow route fills ONE entry per declared parameter with a
	// site sort, which an expanded parameter has no single entry for
	if index < len(parameterSorts) {
		return "a record parameter of an arrow argument", false
	}
	// the entries and the member list are the SAME expansion in the same
	// order (SummaryParameterEntries' one-function rule); the written
	// lookup is keyed by the member's joined path, so the two must walk
	// together
	if len(entries) != len(members) {
		return "a record parameter whose entries drifted from its members", false
	}
	handed := use == recordParameterHandedOver
	for at, entry := range entries {
		written := handed
		if !written {
			_, written = writtenMembers[strings.Join(members[at].Path, ".")]
		}
		layout.BundleEntries = append(layout.BundleEntries, BundleEntry{
			Path:    entry.Name,
			Index:   len(layout.Names),
			Written: written,
		})
		if handed {
			// no code-running statement believes a handed-over leaf
			// across a call — the wiring puts these in the havoc vector
			// (wireSummaryCaptureHavoc)
			layout.HandOverHavocNames = append(layout.HandOverHavocNames, entry.Name)
		}
		layout.Names = append(layout.Names, entry.Name)
		layout.Sorts = append(layout.Sorts, entry.Sort)
		layout.Typeofs = append(layout.Typeofs, entry.TypeofTag)
	}
	return "", true
}

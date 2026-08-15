// split from ir_summary_body_lowering.go — the entry layout vocabulary

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// defaultedParameterSlot is a DEFAULTED parameter's slot, remembered on
// the single-entry path and read by the default prelude, which applies
// each default under a definedness branch — the runtime's own rule:
// undefined, and only undefined, takes the default
type defaultedParameterSlot struct {
	Slot        int
	Initializer *ast.Node
}

// summaryEntryLayout is the entry vector the three appending loops fill
// in slot order — the declared parameters, then the captures, then the
// method's this-fields. Every seam that reads a bundle row reads what
// those loops wrote rather than re-deriving an index of its own.
type summaryEntryLayout struct {
	Names   []string
	Sorts   []BindingKind
	Typeofs []TypeofTag
	// the bundle rows the summary rides out with: a record parameter's
	// leaves and the method's this-fields. Filled in slot order, which is
	// the order the entries are appended in.
	BundleEntries []BundleEntry
	// the leaf spellings ("parentRect.width") of every record parameter
	// this body HANDS OVER whole. They join the havoc vector, so the
	// statements that run code cannot believe a leaf across the hand-over.
	HandOverHavocNames []string
	// the DEFAULTED parameters' slots, in declaration order
	DefaultedSlots []defaultedParameterSlot
	// an object capture's leaf slots, by the spelling the body reads
	// them under — read by the havoc wiring to put them in the havoc
	// vector where a method call on the capture may move them
	CaptureLeafSlots map[string]int
}

// declinedParameterConstruct names WHICH parameter shape the expansion
// refused — the three SummaryParameterEntriesIn answers false for, each
// spelled as the construct it is rather than as a category.
func declinedParameterConstruct(parameter *ast.Node) string {
	pd := parameter.AsParameterDeclaration()
	if pd.DotDotDotToken != nil {
		return "a rest parameter"
	}
	if pd.Initializer != nil {
		return "a defaulted parameter"
	}
	if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) {
		return "a binding-pattern parameter"
	}
	return "a parameter the expansion does not read"
}

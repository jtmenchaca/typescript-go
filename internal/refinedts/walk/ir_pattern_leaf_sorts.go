// New file — the per-leaf sort SIDE CHANNEL a binding-pattern parameter
// position takes at an arrow-argument site.
//
// parameterSlotSort (ir_summary_body.go, not this agent's file) carries
// ONE Sort/TypeofTag pair per DECLARED parameter position — there is no
// field for "this position is a pattern with N leaf sorts, one per
// bound name." Widening that struct is out of this agent's territory,
// so the per-leaf evidence rides a memo instead, keyed on the PATTERN
// PARAMETER NODE itself — the same shape resolvedRecordMembers
// (ir_summary_parameter_entries.go) already uses to let a call-side
// reading and a layout-side reading agree on one answer without a
// struct field connecting them.
package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
)

// patternLeafSort is ONE bound name's sort evidence at the site that
// filled it — Bound is the local the pattern binds (`{ width: w }`
// binds "w"), matching patternElementBinding.Bound so the layout side
// can zip this list against objectPatternElementBindings' own order.
type patternLeafSort struct {
	Bound     string
	Sort      BindingKind
	TypeofTag TypeofTag
}

var (
	patternLeafSortsMu sync.Mutex
	patternLeafSorts   = map[*ast.Node][]patternLeafSort{}
)

// rememberPatternLeafSorts records the per-leaf sorts an arrow-argument
// SITE built for one binding-pattern parameter — reduceElementPatternEntries
// (ir_callback_convert.go) calls this once it has resolved every bound
// name against the receiver's element members. Keyed on the parameter's
// own Name node (the binding pattern), which is the identity
// summaryParameterEntries' pattern arm already has in hand.
func rememberPatternLeafSorts(pattern *ast.Node, leaves []patternLeafSort) {
	patternLeafSortsMu.Lock()
	patternLeafSorts[pattern] = leaves
	patternLeafSortsMu.Unlock()
}

// patternLeafSortsFor answers the per-leaf sorts a SITE remembered for
// this exact binding-pattern node, and whether any were ever recorded.
// A pattern no site has converted yet (or one still mid-conversion)
// answers false — summaryParameterEntries falls back to its existing
// refusal for that position.
func patternLeafSortsFor(pattern *ast.Node) ([]patternLeafSort, bool) {
	patternLeafSortsMu.Lock()
	defer patternLeafSortsMu.Unlock()
	leaves, found := patternLeafSorts[pattern]
	return leaves, found
}

// ClearPatternLeafSorts drops every remembered per-leaf sort list. Keyed
// on parameter nodes from one program, so a caller that builds a new
// program clears it; the tests clear it between cases.
func ClearPatternLeafSorts() {
	patternLeafSortsMu.Lock()
	patternLeafSorts = map[*ast.Node][]patternLeafSort{}
	patternLeafSortsMu.Unlock()
}

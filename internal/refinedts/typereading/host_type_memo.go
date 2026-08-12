// Read-once for resolved-type reads — no TS twin (Go-only mechanism,
// same precedent as walk's call_site_snapshot_fill.go). The TS source
// read types through the tsgo-oracle host, whose handle layer already
// deduplicated; this tree reads *checker.Type directly, and recharts'
// props unions made the same reads dominate the sweep's allocations
// (pprof 2026-08-12: getPropertiesOfUnionOrIntersectionType 22.8% of
// alloc_space). Answers are remembered per checker — "types obtained
// from different checkers must never mix", and a type pointer is
// unique to its checker, so the per-checker map is the mixing guard.
//
// The `at` node is NOT in the key: it only serves as the fallback
// declaration site for a member symbol with no declaration of its
// own, where the member's declared type does not vary by location.

package typereading

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

type hostTypeMemoKey struct {
	t     *checker.Type
	depth int
}

type hostTypeMemoRow struct {
	known abstractdomain.AbstractValue
	ok    bool
}

var (
	hostTypeMemoMu sync.Mutex
	hostTypeMemos  = map[*checker.Checker]map[hostTypeMemoKey]hostTypeMemoRow{}
)

// ReadHostType is the memoized face of readHostType: the same answer
// a fresh read gives, remembered per (checker, type, depth).
func ReadHostType(c *checker.Checker, t *checker.Type, at *ast.Node, depth int) (abstractdomain.AbstractValue, bool) {
	key := hostTypeMemoKey{t: t, depth: depth}
	hostTypeMemoMu.Lock()
	memo := hostTypeMemos[c]
	if memo == nil {
		memo = map[hostTypeMemoKey]hostTypeMemoRow{}
		hostTypeMemos[c] = memo
	}
	row, hit := memo[key]
	hostTypeMemoMu.Unlock()
	if hit {
		return row.known, row.ok
	}
	known, ok := readHostTypeUncached(c, t, at, depth)
	hostTypeMemoMu.Lock()
	memo[key] = hostTypeMemoRow{known: known, ok: ok}
	hostTypeMemoMu.Unlock()
	return known, ok
}

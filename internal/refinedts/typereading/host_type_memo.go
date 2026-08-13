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
// The `at` node is NOT in the key — and any answer that actually
// USED the `at` fallback (a member symbol with no declaration of its
// own) is never cached at all: such an answer is a function of the
// call site, and a site-dependent first write under a site-free key
// varies with the parallel sweep's scheduling order.

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
	usedAt := false
	return readHostTypeMemoized(c, t, at, depth, &usedAt)
}

// readHostTypeMemoized answers from the memo, computing and
// remembering on a miss — but a result that depended on the `at`
// fallback anywhere in its recursion is answered WITHOUT being
// remembered: it is a function of the call site, and caching the
// first site's answer under a site-free key made verdicts vary with
// the parallel sweep's scheduling order (the flickering-fire class).
func readHostTypeMemoized(c *checker.Checker, t *checker.Type, at *ast.Node, depth int, usedAt *bool) (abstractdomain.AbstractValue, bool) {
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
	inner := false
	known, ok := readHostTypeUncached(c, t, at, depth, &inner)
	if inner {
		*usedAt = true
		return known, ok
	}
	hostTypeMemoMu.Lock()
	memo[key] = hostTypeMemoRow{known: known, ok: ok}
	hostTypeMemoMu.Unlock()
	return known, ok
}

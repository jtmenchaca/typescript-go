// The Session-owned refinement result cache. LanguageService is
// rebuilt for every request, so anything that must outlive one request
// — here, the RefinementFix payloads that code actions relay after the
// diagnostics pull that computed them — lives on the Session and is
// reached through Host accessors, the AutoImportRegistry pattern
// (GO-LSP-EDITOR-PATH.md §14.1, locked §15.11).
//
// The key is fileName + overlay version: an entry answers only for
// the exact buffer text it was computed from; a stale version misses
// and the caller recomputes. The type stays THIN on purpose — one map
// and a mutex — because ls importing assignability is the one
// refinedts type this package graph takes on (§16.3 item 4).

package ls

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

type refinementEntry struct {
	version int32
	diags   []assignability.RefinementDiagnostic
}

// RefinementCache holds each file's last refinement diagnostics
// (including their Fixes) keyed by overlay version. One per Session;
// every Snapshot hands out the same pointer.
type RefinementCache struct {
	mu   sync.Mutex
	held map[string]refinementEntry
}

func NewRefinementCache() *RefinementCache {
	return &RefinementCache{held: map[string]refinementEntry{}}
}

// Put records a file's refinement diagnostics at an overlay version,
// replacing whatever version was held.
func (c *RefinementCache) Put(fileName string, version int32, diags []assignability.RefinementDiagnostic) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held[fileName] = refinementEntry{version: version, diags: diags}
}

// Get answers the held diagnostics exactly when the version matches.
func (c *RefinementCache) Get(fileName string, version int32) ([]assignability.RefinementDiagnostic, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, held := c.held[fileName]
	if !held || entry.version != version {
		return nil, false
	}
	return entry.diags, true
}

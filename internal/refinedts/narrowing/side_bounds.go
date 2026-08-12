// The finite window a known numeric value lies in — used by index
// windows, loop fixpoint, property access, and comparison leaves.

package narrowing

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// Window is the { lo, hi } pair a comparison's other side, or a known
// value, provably lies in.
type Window struct {
	Lo float64
	Hi float64
}

// SideBounds resolves a comparison's NON-PLACE side to the window its
// runtime value provably lies in — the caller closes over its
// environment and evaluates. A nil function, or an (ok=false) answer,
// claims nothing, and the leaf stays `other`.
type SideBounds func(e *ast.Node) (Window, bool)

// BoundsOfKnown is boundsOfKnown in the TS source: the finite window a
// KNOWN value lies in: exact numbers give their spread; a set asks the
// kernel's bounds entry, whose edges come from the proved enclosure and
// emptiness deciders. ok=false where no finite window is provable —
// sets prove realness (NaN is never a member), which is what lets the
// cmpSet leaf's model assume a real other side.
func BoundsOfKnown(known abstractdomain.AbstractValue) (window Window, ok bool) {
	if known.Kind == abstractdomain.KindValues &&
		known.KindTag == abstractdomain.PrimitiveNumber && len(known.Values) > 0 {
		lo, hi := known.Values[0], known.Values[0]
		for _, v := range known.Values[1:] {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		return Window{Lo: lo, Hi: hi}, true
	}
	if known.Kind == abstractdomain.KindSet && NarrowKernel != nil {
		defer func() {
			if recover() != nil {
				// a refused question — the TS source's try/catch
				window, ok = Window{}, false
			}
		}()
		bounds := NarrowKernel.Bounds(known.Set)
		if bounds.Empty {
			return Window{}, false
		}
		lo := math.Inf(-1)
		hi := math.Inf(1)
		for _, f := range bounds.Hull.Forms {
			if f.Form == refinementsets.FormAtLeast && f.A > lo {
				lo = f.A
			}
			if f.Form == refinementsets.FormAtMost && f.A < hi {
				hi = f.A
			}
		}
		// a one-sided window still narrows: `x > s` with s ≥ lo puts a
		// real x above lo even when no upper edge is provable — the
		// kernel's cmpSet claims are stated over extended endpoints
		if math.IsInf(lo, -1) && math.IsInf(hi, 1) {
			return Window{}, false
		}
		return Window{Lo: lo, Hi: hi}, true
	}
	return Window{}, false
}

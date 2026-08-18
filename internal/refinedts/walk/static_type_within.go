// The static-type-within escape: a checked position whose value the
// walk could not determine, but whose OWN static type already lies
// within the stated set. tsc's shape check proves membership for
// every value the position can hold, so the stated set narrows
// nothing beyond the host type — the position is determined by the
// host's own check, and no alert fires. The recharts twin:
// `useErrorBarDirection(outsideProps.direction)`, where the argument
// is typed `ErrorBarDirection | undefined` and the parameter states
// the same union.
//
// Only a BARE set row escapes this way: temporal, dependent,
// measured, refine-carrying and unread rows all state more than a
// set, and those still owe a value.

package walk

import (
	"reflect"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// StaticTypeWithinTarget answers whether the node's own static type
// grounds to a set provably inside the target's stated set. False is
// never a verdict — it only means this escape does not apply and the
// caller's own alert stands.
func StaticTypeWithinTarget(ctx *FlowContext, node *ast.Node, target annotations.DeclaredRefinement) bool {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil || node == nil {
		return false
	}
	// the maybe target admits absence; the ground's own maybe wrap must
	// then be admitted too, and a bare target admits none
	targetAdmitsAbsence := false
	targetInner := target
	if target.Kind == annotations.DeclaredPossiblyUndefined && target.Inner != nil {
		targetAdmitsAbsence = true
		targetInner = *target.Inner
	}
	if targetInner.Kind != annotations.DeclaredSet || targetInner.Set == nil {
		return false
	}
	if targetInner.Temporal != nil || targetInner.Unread || targetInner.Refine != nil ||
		len(targetInner.Depends) > 0 || len(targetInner.ElementDepends) > 0 || targetInner.Measures != nil {
		return false
	}
	// the type is read from the CAST-STRIPPED expression: `x as T`
	// wears T without tsc having checked membership — the `as`
	// suppressed exactly the check this escape's premise rests on, so
	// the proof, if any, lives on the uncast x. (Sankey's
	// `targetType as SankeyElementType` smuggled a split() piece past
	// a 'node' | 'link' position; reading the cast's own type here
	// silenced that true refutation.)
	head := Unwrapped(node)
	if head == nil {
		return false
	}
	ground := typeGroundOf(ctx, typereading.TypeAtLocation(ctx.P.Checker, head), head)
	if ground == nil {
		return false
	}
	g := *ground
	if g.Kind == abstractdomain.KindPossiblyUndefined {
		if !targetAdmitsAbsence {
			return false
		}
		g = *g.Inner
	}
	var groundSet refinementsets.RefinedSet
	switch {
	case g.Kind == abstractdomain.KindSet && g.SetKindTag == abstractdomain.SetKindTagNone:
		groundSet = g.Set
	case g.Kind == abstractdomain.KindValues:
		s, ok := abstractdomain.SetOfKnown(g)
		if !ok {
			return false
		}
		groundSet = s
	default:
		// a NaN-admitting number ground, a union of sorts, a constructed
		// shape: none of these is a set this comparison can carry
		return false
	}
	// the common spelling — the same alias on both sides — compares
	// structurally without a kernel question
	if reflect.DeepEqual(groundSet, *targetInner.Set) {
		return true
	}
	return subsetProved(ctx, groundSet, *targetInner.Set)
}

// subsetProved asks the kernel A ⊆ B, scalar layer first, sequence
// layer second, each recovered — a refused question is no proof, and
// no proof keeps the caller's alert.
func subsetProved(ctx *FlowContext, a refinementsets.RefinedSet, b refinementsets.RefinedSet) (proved bool) {
	if ctx == nil || ctx.Kernel == nil {
		return false
	}
	ask := func(question func(refinementsets.RefinedSet, refinementsets.RefinedSet) bool) bool {
		defer func() {
			if recover() != nil {
				proved = false
			}
		}()
		return question(a, b)
	}
	if ctx.Kernel.ScalarSubset != nil && ask(ctx.Kernel.ScalarSubset) {
		return true
	}
	if ctx.Kernel.SeqSubset != nil && ask(ctx.Kernel.SeqSubset) {
		return true
	}
	return false
}

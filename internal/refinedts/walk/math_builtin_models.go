// from evaluation/math_builtin_models.ts
//
// Math.* recognition: transferMathCall, sqrt sign-split over exact
// values and sets, and the implementation-approximated family that
// only notes which enclosure is still outstanding.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// approximatedMath is APPROXIMATED_MATH in the TS source: the Math
// functions ECMA-262 leaves implementation-approximated — exact reads
// are impossible, interval enclosures are the kernel work that
// answers them (the kernel map's enclosure family).
var approximatedMath = map[string]struct{}{}

// readMathBuiltin is readMathBuiltin in the TS source: Math.f(…) when
// Math resolves to the default library. Nil when the call is not a
// Math property access.
func readMathBuiltin(ctx *FlowContext, env Env, e *ast.Node, spreadArguments func(args []*ast.Node) []abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil
	}
	pa := call.Expression.AsPropertyAccessExpression()
	if !(ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Math" && resolvesToDefaultLib(ctx, pa.Expression)) {
		return nil
	}
	name := pa.Name().Text()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	spread := spreadArguments(arguments)
	transferred, transferredOk := TransferMathCall(name, spread)
	if transferredOk && transferred.Kind != abstractdomain.KindUnknown {
		return &transferred
	}
	// Math.min/max(...values) over an UNBOUNDED numeric array: the
	// spread could not expand (EffectiveArgumentsOf has no count to
	// place positions at, so `spread` above is one Residue and the
	// ordinary fold just declined) — even though the call's own answer
	// IS determined. min/max over any nonempty set of reals, or the
	// empty-array -Infinity (sec-math.max/min), both lie inside the
	// whole number ground: the answer is bounded above and below by
	// nothing tighter than R-bar itself. The claim rests on the
	// spread's SOURCE array's own declared element type — a checked
	// `number[]`/`Array<number>`/… — the same GetElementTypeOfArrayType
	// identity test loop_fixpoint.go's for-of element reading already
	// uses for the identical claim, so the ground carries the same
	// TrustLibrary provenance. An EXPANDABLE spread (a literal array,
	// e.g.) never reaches here: TransferMathCall's own fold above
	// already answered it exactly, tighter than this ground would.
	if (name == "min" || name == "max") && len(arguments) == 1 && ast.IsSpreadElement(arguments[0]) {
		source := arguments[0].AsSpreadElement().Expression
		if t := typereading.TypeAtLocation(ctx.P.Checker, source); t != nil {
			tracing.CountBy("host.elementTypeOfArrayType", 1)
			element := ctx.P.Checker.GetElementTypeOfArrayType(t)
			isNumberElement := false
			if element != nil {
				tracing.CountBy("host.numberType", 1)
				isNumberElement = element == ctx.P.Checker.GetNumberType()
			}
			if isNumberElement {
				out := abstractdomain.AtTrustLevel(
					abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)),
					abstractdomain.TrustLibrary,
				)
				return &out
			}
		}
	}
	// sqrt over exact values admitting both signs: the nonnegative
	// roots are spec-exact and the negatives are NaN — the answer is
	// the roots, or NaN (sec-math.sqrt: NaN for x < 0, exact otherwise)
	if name == "sqrt" && len(spread) > 0 && spread[0].Kind == abstractdomain.KindValues &&
		spread[0].KindTag == abstractdomain.PrimitiveNumber && len(spread[0].Values) > 0 {
		only := spread[0]
		anyNegative := false
		var roots []float64
		for _, v := range only.Values {
			if v < 0 {
				anyNegative = true
				continue
			}
			roots = append(roots, math.Sqrt(v))
		}
		if anyNegative {
			grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(only), abstractdomain.TrustSpec)
			if len(roots) > 0 {
				out := abstractdomain.AtTrustLevel(abstractdomain.PossiblyNaN(abstractdomain.KnownValues(roots, abstractdomain.PrimitiveNumber, grade)), grade)
				return &out
			}
			out := abstractdomain.AtTrustLevel(abstractdomain.NaNValue, grade)
			return &out
		}
	}
	// sqrt over a SET admitting negatives splits by sign: the
	// nonnegative half's image through the transfer, or NaN
	if name == "sqrt" && len(spread) > 0 && spread[0].Kind == abstractdomain.KindSet {
		only := spread[0]
		nonneg := refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, only.Set.Forms...), refinementsets.AtLeast(0))...)
		rooted, rootedOk := TransferMathCall("sqrt", []abstractdomain.AbstractValue{
			abstractdomain.KnownSet(nonneg, nil, abstractdomain.TrustLevelOf(only), abstractdomain.SetKindTagNone),
		})
		if rootedOk && rooted.Kind != abstractdomain.KindUnknown && rooted.Kind != abstractdomain.KindNaN {
			out := abstractdomain.AtTrustLevel(
				abstractdomain.PossiblyNaN(rooted),
				abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(only), abstractdomain.TrustSpec),
			)
			return &out
		}
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site: "expression",
				Node: e,
				Said: "Math.sqrt() over a set admitting a negative is NaN " +
					"alongside numbers — the nonnegative half's image was " +
					"declined",
				Unsupported: true,
			})
		}
	}
	// the implementation-approximated family answers only its pinned
	// corners today — the interval enclosures are kernel work, and the
	// row says which call waits on them. A name with no rows at all
	// says so; a MODELED name on unresolved operands stays with the
	// row's default.
	if _, approximated := approximatedMath[name]; approximated && assignability.CollectingReasons() {
		assignability.NoteReason(assignability.ReasonNote{
			Site:        "expression",
			Node:        e,
			Said:        "Math." + name + "() is implementation-approximated — no tight enclosure yet",
			Unsupported: true,
		})
	} else if !transferredOk {
		NoteUnmodeledCall(ctx, e)
	}
	out := silence.Residue()
	return &out
}

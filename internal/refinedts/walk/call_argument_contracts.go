// from evaluation/call_argument_contracts.ts
//
// What a contracted call's arguments owe: each position's stated
// refinement, including dependent bounds instantiated from sibling
// arguments, order-ledger rows, and windowed edges.

package walk

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func dependsOpWord(op string) string {
	switch op {
	case "ge":
		return ">="
	case "gt":
		return ">"
	case "le":
		return "<="
	default:
		return "<"
	}
}

// CheckContractArguments is checkContractArguments in the TS source.
//
// The positions come from the shared placement reader
// (EffectiveArgumentsOf), so a tagged template's obligations line up
// too — position 0 is the template object, positions 1.. the
// substitutions — and so do a spreading call's, whose exact source
// contributes one position per item. A position with no source
// expression carries nothing to hang a diagnostic on and no caller
// state to judge, so it is skipped: the template object is the walk's
// own construction, and an item expanded out of a spread was written
// inside the source array, not at this call.
func CheckContractArguments(ctx *FlowContext, contract *FunctionContract, effective EffectiveArguments) {
	argumentNodes := effective.Nodes
	argKnowns := effective.Knowns
	if argumentNodes == nil {
		return
	}
	parameters := contract.Declaration.Parameters()
	for i, argument := range argumentNodes {
		if argument == nil || i >= len(argKnowns) {
			continue
		}
		var stated *annotations.DeclaredRefinement
		if i < len(contract.Params) {
			stated = contract.Params[i]
		}
		var parameterType *ast.Node
		if i < len(parameters) {
			parameterType = parameters[i].AsParameterDeclaration().Type
		}
		if stated == nil {
			// a FUNCTION-typed position: the callback handed over owes
			// its returns to the position's stated return type
			CheckCallbackArgument(ctx, argument, parameterType)
			// a PLAIN-typed position still refutes on SORT — the value
			// provably wearing another sort (a symbol smuggled past a
			// cast into a `string` domain) is wrong on every run
			RefutePlainSort(ctx, argKnowns[i], parameterType, argument, "argument")
			continue
		}
		argKnown := argKnowns[i]
		// the argument's OWN static type already inside the stated set —
		// including the maybe wrap on both sides — is tsc's proof for
		// every value this position can pass (static_type_within.go).
		// Judged HERE, before the maybe peel below: after the peel the
		// escape would see an absence-admitting static type against the
		// bare inner statement and rightly refuse.
		if StaticTypeWithinTarget(ctx, argument, *stated) {
			continue
		}
		// a maybe position admits absence outright; present
		// knowledge judges against the inner statement
		if stated.Kind == annotations.DeclaredPossiblyUndefined {
			if argKnown.Kind == abstractdomain.KindUndef {
				continue
			}
			if argKnown.Kind == abstractdomain.KindPossiblyUndefined {
				argKnown = *argKnown.Inner
			}
			stated = stated.Inner
		}
		if stated.Kind == annotations.DeclaredVariable {
			// the position states T; the obligation the call carries
			// is T ⊆ bound, checked argument by argument — only a
			// GROUNDED bound judges (a plain-TS bound stays silent)
			if stated.BoundGrounded {
				set := *stated.Bound
				for d := 0; d < stated.StarDepth; d++ {
					set = refinementsets.MakeRefinedSet(refinementsets.Star(set))
				}
				CheckAssignability(ctx, argKnown, annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}, argument, "argument", nil)
			}
			// an OBJECT bound judges the argument against the object
			// itself: T ⊆ the annotation, so every argument owes its
			// obligations
			if stated.BoundObject != nil && stated.StarDepth == 0 {
				CheckAssignability(ctx, argKnown, annotations.DeclaredRefinement{Kind: annotations.DeclaredObject, Object: stated.BoundObject}, argument, "argument", nil)
			}
			continue
		}
		// a DEPENDENT bound instantiates here: the named sibling
		// argument's exact value makes the dependent set CONSTANT,
		// and the existing kernel judges it; a windowed sibling
		// judges by its edges (accept past the far edge, refute
		// short of the near one). An unreadable sibling leaves the
		// base statement to checkAssignability alone.
		if stated.Kind == annotations.DeclaredSet && len(stated.Depends) > 0 {
			type resolvedDepend struct {
				depends annotations.DependentBound
				path    []string
				j       int
				sibling *abstractdomain.AbstractValue
				v       float64
				hasV    bool
			}
			resolved := make([]resolvedDepend, len(stated.Depends))
			for di, depends := range stated.Depends {
				path := strings.Split(depends.Param, ".")
				j := -1
				for pi, parameter := range parameters {
					pd := parameter.AsParameterDeclaration()
					if ast.IsIdentifier(pd.Name()) && pd.Name().Text() == path[0] {
						j = pi
						break
					}
				}
				var sibling *abstractdomain.AbstractValue
				if j >= 0 && j < len(argKnowns) {
					s := argKnowns[j]
					sibling = &s
				}
				for _, key := range path[1:] {
					if sibling != nil && sibling.Kind == abstractdomain.KindObject {
						if idx, ok := objectKeyIndex(*sibling, key); ok {
							v := sibling.Keys[idx].Value
							sibling = &v
						} else {
							sibling = nil
						}
					} else {
						sibling = nil
					}
				}
				var v float64
				hasV := false
				if sibling != nil && sibling.Kind == abstractdomain.KindValues &&
					sibling.KindTag == abstractdomain.PrimitiveNumber && len(sibling.Values) == 1 && isFinite(sibling.Values[0]) {
					v, hasV = sibling.Values[0], true
				}
				resolved[di] = resolvedDepend{depends: depends, path: path, j: j, sibling: sibling, v: v, hasV: hasV}
			}
			var exactForms []refinementsets.Refinement
			origin := ""
			for _, r := range resolved {
				if !r.hasV {
					continue
				}
				switch r.depends.Op {
				case "ge":
					exactForms = append(exactForms, refinementsets.AtLeast(r.v))
				case "gt":
					exactForms = append(exactForms, refinementsets.Above(r.v))
				case "le":
					exactForms = append(exactForms, refinementsets.AtMost(r.v))
				default:
					exactForms = append(exactForms, refinementsets.Below(r.v))
				}
				origin += " (" + dependsOpWord(r.depends.Op) + " '" + r.depends.Param + "', which is " +
					strconv.FormatFloat(r.v, 'g', -1, 64) + " here)"
			}
			// the RELATION itself may be a held theorem: a guard or an
			// early return recorded `value op sibling` in the order
			// ledger, and the rows decide the edge with no window at
			// all. A held row proves both sides REAL and PRESENT (NaN
			// and undefined pass no comparison), so a proved edge
			// judges the base statement with those wrappers stripped.
			rowProved := false
			var undecided []resolvedDepend
			for _, r := range resolved {
				if r.hasV {
					continue
				}
				settledByRows := false
				if len(r.path) == 1 {
					argumentPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, argument)
					var siblingArgument *ast.Node
					if r.j >= 0 && r.j < len(argumentNodes) {
						siblingArgument = argumentNodes[r.j]
					}
					var siblingPlace *dataflowfacts.PlaceKey
					if siblingArgument != nil {
						siblingPlace = dataflowfacts.PlaceKeyOf(ctx.P.Checker, siblingArgument)
					}
					if argumentPlace != nil && siblingPlace != nil {
						decided := dataflowfacts.DecideComparison(
							ctx.DifferenceConstraints, *argumentPlace, *siblingPlace,
							dataflowfacts.ComparisonOp(r.depends.Op), ctx.Kernel,
						)
						if decided != nil && *decided {
							rowProved = true
							settledByRows = true
						} else if decided != nil && !*decided {
							ctx.Report(assignability.At(
								argument, 7001,
								"argument is not "+dependsOpWord(r.depends.Op)+" '"+r.depends.Param+
									"' — the held comparisons prove the opposite",
							))
							settledByRows = true
						}
					}
				}
				if !settledByRows {
					undecided = append(undecided, r)
				}
			}
			// ONE base judgment, the instantiated bounds folded in and
			// their origins appended; a row-proved edge strips the
			// wrappers the rows prove impossible
			baseKnown := argKnown
			if rowProved {
				for baseKnown.Kind == abstractdomain.KindPossiblyNaN || baseKnown.Kind == abstractdomain.KindPossiblyUndefined {
					baseKnown = *baseKnown.Inner
				}
			}
			originText := origin
			frame := ctx
			if originText != "" {
				originalReport := ctx.Report
				withOrigin := *ctx
				withOrigin.Report = func(d assignability.RefinementDiagnostic) {
					if d.Code == 7001 {
						d.MessageText = d.MessageText + originText
					}
					originalReport(d)
				}
				frame = &withOrigin
			}
			forms := append(append([]refinementsets.Refinement{}, stated.Set.Forms...), exactForms...)
			set := refinementsets.MakeRefinedSet(forms...)
			CheckAssignability(frame, baseKnown, annotations.DeclaredRefinement{
				Kind: annotations.DeclaredSet, Set: &set, LibraryAdapter: stated.LibraryAdapter,
			}, argument, "argument", nil)
			// whatever neither an exact value nor a row settled judges
			// by the sibling's window, or alerts
			for _, r := range undecided {
				if !CheckDependentEdge(ctx, argKnown, r.sibling, DependentRelation{Op: r.depends.Op, Param: r.depends.Param}, argument, "argument") {
					ctx.Report(assignability.At(argument, 7002, assignability.AlertText))
				}
			}
			continue
		}
		CheckAssignability(ctx, argKnown, *stated, argument, "argument", nil)
	}
}

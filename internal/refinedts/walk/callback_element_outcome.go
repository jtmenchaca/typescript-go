// from interprocedural/callback_element_outcome.ts
//
// map / flatMap / filter — the per-element outcomes that build a new
// sequence (or keep a subset) from the receiver's items.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// MapOutcome is map / flatMap over an exact sequence or a star
// element.
func MapOutcome(walk *CallbackWalk, method string) abstractdomain.AbstractValue {
	ctx := walk.Ctx
	receiver := walk.Receiver
	silent := walk.Silent
	ownerParameter, hasOwnerParameter := walk.OwnerParameter, walk.HasOwnerParameter
	parameterAt := walk.ParameterAt
	nameAt := walk.NameAt
	evalBody := walk.EvalBody
	reportPins := walk.ReportPins
	finish := walk.Finish

	parameter0 := parameterAt(0)
	if parameter0 == nil {
		return finish(silence.Residue())
	}
	// an exact sequence: run the map, item by item (silently) —
	// destructuring parameters read their keys from each item. All-
	// scalar outputs collapse to the flat tuple; anything else is
	// the exact LIST of outputs. flatMap flattens one level, which
	// for number/array outputs is appending their values.
	var exactResult *abstractdomain.AbstractValue
	items := ItemsOf(receiver)
	if items != nil {
		outputs := make([]abstractdomain.AbstractValue, len(items))
		for i, item := range items {
			bindings := map[string]abstractdomain.AbstractValue{}
			BindParameter(ctx.P.Checker, parameter0, item, bindings)
			if indexParameter, ok := nameAt(1); ok {
				bindings[indexParameter] = abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			}
			if hasOwnerParameter {
				bindings[ownerParameter] = receiver
			}
			outputs[i] = evalBody(silent, bindings)
		}
		if method == "map" {
			flat := true
			values := make([]float64, len(outputs))
			for i, out := range outputs {
				if out.Kind == abstractdomain.KindValues && len(out.Values) == 1 && out.KindTag == abstractdomain.PrimitiveNumber {
					values[i] = out.Values[0]
				} else {
					flat = false
					break
				}
			}
			var result abstractdomain.AbstractValue
			if flat {
				result = abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
			} else {
				result = abstractdomain.KnownList(outputs, abstractdomain.TrustProved)
			}
			exactResult = &result
		} else {
			var flattenedValues []float64
			exact := true
			for _, out := range outputs {
				if out.Kind == abstractdomain.KindValues && (out.KindTag == abstractdomain.PrimitiveNumber || out.KindTag == abstractdomain.PrimitiveArray) {
					flattenedValues = append(flattenedValues, out.Values...)
				} else {
					exact = false
				}
			}
			if exact {
				result := abstractdomain.KnownValues(flattenedValues, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
				exactResult = &result
			}
		}
	}
	// ONE reporting pass — the same pin law hover uses
	out := evalBody(ctx, reportPins(nil))
	if exactResult != nil {
		return finish(*exactResult)
	}
	flattened := out
	if method == "flatMap" {
		flattened = ElementOf(out)
	}
	outSet, hasOutSet := abstractdomain.SetOfKnown(flattened)
	// map answers ONE output per input (sec-array.prototype.map:
	// the result's length IS the receiver's), so the receiver's
	// repetition window survives; flatMap's flattening does not
	var window *refinementsets.Repeated
	if method == "map" && receiver.Kind == abstractdomain.KindSet {
		if rep, ok := refinementsets.AsRepetition(receiver.Set); ok {
			window = &rep
		}
	}
	if hasOutSet {
		var built refinementsets.RefinedSet
		if window == nil {
			built = refinementsets.MakeRefinedSet(refinementsets.Star(outSet))
		} else {
			built = refinementsets.Repetition(outSet, window.Lo, window.Hi)
		}
		return finish(abstractdomain.KnownSet(built, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	}
	// an output the tuple layer cannot hold — the callback answered a
	// record or a class instance — builds the OBJECT-STAR instead: map
	// answers one output per input, so every position of the result
	// holds what the body answered. The count the receiver's window
	// stated does not ride along (the object-star claims no length),
	// which says less than `map` proves and never more.
	if built, ok := abstractdomain.KnownObjectStar(flattened, abstractdomain.TrustProved); ok {
		return finish(built)
	}
	// an OPAQUE output element makes the built array opaque with
	// it; a plain unknown stays the walk's own gap
	if flattened.Kind == abstractdomain.KindUnknown && flattened.Opaque {
		return finish(abstractdomain.Opaque)
	}
	return finish(silence.Residue())
}

// FilterOutcome is filter — keep decided items, or narrow the star
// element by lift.
func FilterOutcome(walk *CallbackWalk) abstractdomain.AbstractValue {
	ctx := walk.Ctx
	receiver := walk.Receiver
	call := walk.Call
	body := walk.Body
	silent := walk.Silent
	element := walk.Element
	elementParameter, hasElementParameter := walk.ElementParameter, walk.HasElementParameter
	ownerParameter, hasOwnerParameter := walk.OwnerParameter, walk.HasOwnerParameter
	parameterAt := walk.ParameterAt
	nameAt := walk.NameAt
	evalBody := walk.EvalBody
	reportPins := walk.ReportPins
	finish := walk.Finish

	// an exact sequence with a DECIDED predicate keeps exactly the
	// passing items
	parameter0 := parameterAt(0)
	items := ItemsOf(receiver)
	if items != nil && parameter0 != nil {
		var kept []abstractdomain.AbstractValue
		decided := true
		for i, item := range items {
			bindings := map[string]abstractdomain.AbstractValue{}
			BindParameter(ctx.P.Checker, parameter0, item, bindings)
			if indexParameter, ok := nameAt(1); ok {
				bindings[indexParameter] = abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			}
			if hasOwnerParameter {
				bindings[ownerParameter] = receiver
			}
			verdict, known := abstractdomain.TruthinessDecided(evalBody(silent, bindings))
			if !known {
				decided = false
			} else if verdict {
				kept = append(kept, item)
			}
		}
		if decided {
			evalBody(ctx, reportPins(nil))
			flat := true
			values := make([]float64, len(kept))
			for i, item := range kept {
				if item.Kind == abstractdomain.KindValues && len(item.Values) == 1 && item.KindTag == abstractdomain.PrimitiveNumber {
					values[i] = item.Values[0]
				} else {
					flat = false
					break
				}
			}
			if flat {
				return finish(abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			}
			return finish(abstractdomain.KnownList(kept, abstractdomain.TrustProved))
		}
	}
	if !hasElementParameter {
		return finish(silence.Residue())
	}
	kept := element
	// an unknown element falls back to the receiver's DECLARED
	// element type, when one reads — a maybe element the predicate
	// then strips; an unreadable element stays unknown, and the
	// lift below can still pin FORMS (a kept element satisfied the
	// predicate at runtime, whatever its declared type was)
	if kept.Kind == abstractdomain.KindUnknown {
		var receiverExpression *ast.Node
		if ast.IsPropertyAccessExpression(call.AsCallExpression().Expression) {
			receiverExpression = call.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
		}
		if receiverExpression != nil && ast.IsIdentifier(receiverExpression) {
			symbol := ctx.P.Checker.GetSymbolAtLocation(receiverExpression)
			var declaration *ast.Node
			if symbol != nil {
				declaration = symbol.ValueDeclaration
			}
			var typeNode *ast.Node
			if declaration != nil && (ast.IsParameterDeclaration(declaration) || ast.IsVariableDeclaration(declaration)) {
				typeNode = declaration.Type()
			}
			if typeNode != nil && ast.IsArrayTypeNode(typeNode) {
				read := annotations.AnnotationOfType(ctx.P, typeNode.AsArrayTypeNode().ElementType, ctx.Registry, ctx.Objects)
				if read.Stated != nil {
					if read.Stated.Kind == annotations.DeclaredPossiblyUndefined && read.Stated.Inner != nil && read.Stated.Inner.Kind == annotations.DeclaredSet {
						kept = abstractdomain.PossiblyUndefined(abstractdomain.KnownSet(*read.Stated.Inner.Set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone), "", false, false)
					} else if read.Stated.Kind == annotations.DeclaredSet {
						kept = abstractdomain.KnownSet(*read.Stated.Set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
					}
				}
			}
		}
	}
	if !ast.IsBlock(body) {
		lifted := narrowing.Narrowings(ctx.P.Checker, body, func(name string) bool {
			return name == elementParameter
		}, nil, narrowing.GuardReadNowhere)
		for _, n := range lifted.WhenTrue {
			if n.Binding == elementParameter && len(n.Path) == 0 {
				kept = narrowing.ApplyNarrowed(kept, n)
			}
		}
	}
	// the body's own judgments, once — same pin law as hover
	evalBody(ctx, reportPins(nil))
	keptSet, hasKeptSet := abstractdomain.SetOfKnown(kept)
	// filter never adds members: the unnarrowed element is sound
	if !hasKeptSet {
		// a GRAPH-shaped kept element keeps the per-position claim even
		// though the tuple layer holds nothing: filter selects a subset of
		// the receiver's own elements (sec-array.prototype.filter), so
		// every position of the result holds what one position of the
		// receiver held. The count drops, which filter never proved
		// anyway.
		if built, ok := abstractdomain.KnownObjectStar(kept, abstractdomain.TrustProved); ok {
			return finish(built)
		}
		return finish(silence.Residue())
	}
	return finish(abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Star(keptSet)), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
}

// from interprocedural/callback_pins.ts
//
// What a callback's parameters wear given the values the call
// convention hands them. bindParameter is the primitive; array
// methods, Array.from({length}), and schema transform/codec are the
// slot laws. The inliner (report pass) and callSiteBindings both
// ask here — per-invocation exact indices stay with the runner.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ArrayCallbackMethods is ARRAY_CALLBACK_METHODS in the TS source.
var ArrayCallbackMethods = map[string]struct{}{
	"map":     {},
	"filter":  {},
	"reduce":  {},
	"forEach": {},
	"flatMap": {},
	"find":    {},
}

// BindParameter seeds a parameter's names from an argument's
// knowledge — a plain name takes the value; a destructuring pattern
// reads its keys (or elements) exactly the way the statement walk
// does. A name whose slot lands on nothing wears the host's type at
// that name. False when the parameter is absent.
func BindParameter(c *checker.Checker, parameter *ast.Node, value abstractdomain.AbstractValue, into map[string]abstractdomain.AbstractValue) bool {
	if parameter == nil {
		return false
	}
	ReadDestructuring(parameter.AsParameterDeclaration().Name(), value, func(name string, held abstractdomain.AbstractValue, at *ast.Node) {
		into[name] = silence.SeededBinding(c, held, at)
	})
	return true
}

// arrayCallbackPinsParams is the destructured-parameters struct for
// ArrayCallbackPins (3+ fields, per convention).
type arrayCallbackPinsParams struct {
	c           *checker.Checker
	fn          *ast.Node // PinTarget: any node with .Parameters()
	receiver    abstractdomain.AbstractValue
	method      string
	accumulator *abstractdomain.AbstractValue
	bindOffset  int
}

// ArrayCallbackPins joins/reports pins for an array-callback method:
// element, index window, owner; reduce's accumulator when supplied.
// `bindOffset` is Function.prototype.bind's prepended-argument count.
func ArrayCallbackPins(p arrayCallbackPinsParams) map[string]abstractdomain.AbstractValue {
	bindings := map[string]abstractdomain.AbstractValue{}
	base := p.bindOffset
	if p.method == "reduce" {
		base++
	}
	parameters := p.fn.Parameters()
	bindSlot := func(slot int, value abstractdomain.AbstractValue) {
		var parameter *ast.Node
		if slot >= 0 && slot < len(parameters) {
			parameter = parameters[slot]
		}
		BindParameter(p.c, parameter, value, bindings)
	}
	bindSlot(base+0, ElementOf(p.receiver))
	bindSlot(base+1, abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	bindSlot(base+2, p.receiver)
	if p.method == "reduce" && p.accumulator != nil {
		bindSlot(p.bindOffset+0, *p.accumulator)
	}
	return bindings
}

// arrayFromMapperPinsParams is the destructured-parameters struct
// for ArrayFromMapperPins.
type arrayFromMapperPinsParams struct {
	c  *checker.Checker
	fn *ast.Node
}

// ArrayFromMapperPins is Array.from({ length }, mapper): «
// undefined, 𝔽(k) » with k ≥ 0 (sec-array.from array-like path).
func ArrayFromMapperPins(p arrayFromMapperPinsParams) map[string]abstractdomain.AbstractValue {
	bindings := map[string]abstractdomain.AbstractValue{}
	parameters := p.fn.Parameters()
	var parameter0, parameter1 *ast.Node
	if len(parameters) > 0 {
		parameter0 = parameters[0]
	}
	if len(parameters) > 1 {
		parameter1 = parameters[1]
	}
	BindParameter(p.c, parameter0, abstractdomain.AtTrustLevel(abstractdomain.Undef, abstractdomain.TrustSpec), bindings)
	BindParameter(
		p.c,
		parameter1,
		abstractdomain.AtTrustLevel(
			abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
			abstractdomain.TrustSpec,
		),
		bindings,
	)
	return bindings
}

// schemaCallbackPinsParams is the destructured-parameters struct for
// SchemaCallbackPins.
type schemaCallbackPinsParams struct {
	c        *checker.Checker
	fn       *ast.Node
	compiled annotations.Annotation
}

// SchemaCallbackPins is a transform / codec callback: parameter 0
// wears the compiled annotation (`wornOfAnnotation`). Nil when the
// callback has no parameter.
func SchemaCallbackPins(p schemaCallbackPinsParams) map[string]abstractdomain.AbstractValue {
	parameters := p.fn.Parameters()
	if len(parameters) == 0 {
		return nil
	}
	bindings := map[string]abstractdomain.AbstractValue{}
	BindParameter(p.c, parameters[0], WornOfAnnotation(p.compiled), bindings)
	return bindings
}

// Numeric chain methods: value bounds, sequence length, measures,
// modular windows, and date min/max. Returns nil when the method is
// not numeric so the caller can keep reading.
//
// Ported 1:1 from annotations/chain_numeric_method.ts.

package annotations

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// NumericChainMethodParams is the destructured named-parameter
// struct for NumericChainMethod.
type NumericChainMethodParams struct {
	P      *program.CheckerProgram
	At     *ast.Node
	Inner  Annotation
	Method string
	Args   []*ast.Node
}

// buildForm panics for an invalid form argument (multipleOf(0), a
// negative repetition bound, ...) -- guardedForm recovers it as the
// TS source's try/catch does.
func guardedForm(build func() []refinementsets.Refinement) (forms []refinementsets.Refinement, errText string) {
	defer func() {
		if r := recover(); r != nil {
			errText = panicText(r)
		}
	}()
	return build(), ""
}

func panicText(r any) string {
	if err, ok := r.(error); ok {
		return err.Error()
	}
	if s, ok := r.(string); ok {
		return s
	}
	return "unsupported"
}

// NumericChainMethod is numericChainMethod in the TS source.
func NumericChainMethod(params NumericChainMethodParams) (result *Compiled) {
	p, at, inner, method, args := params.P, params.At, params.Inner, params.Method, params.Args
	base := derefSet(inner.Set)

	// the set spelling before and after this one method -- nil means
	// "not a numeric method", read by ChainMethod as keep-looking, not
	// as a dropped conjunct
	if diagnose.EventOn("annotations.chainMethod") {
		before := kernelbridge.EncodeSet(base)
		defer func() {
			if result == nil {
				diagnose.Log("annotations.chainMethod",
					"method", method, "before", before, "after", before, "numeric", false)
				return
			}
			after := before
			if result.Annotation != nil {
				after = kernelbridge.EncodeSet(derefSet(result.Annotation.Set))
			}
			diagnose.Log("annotations.chainMethod",
				"method", method, "before", before, "after", after, "numeric", true,
				"unsupported", IsUnsupported(*result))
		}()
	}

	withForm := func(build func(k float64) refinementsets.Refinement) *Compiled {
		k, ok := oneNumberArg(p, args)
		if !ok {
			return unsupportedf(at, ".%s takes one numeric literal", method)
		}
		forms, errText := guardedForm(func() []refinementsets.Refinement {
			return []refinementsets.Refinement{build(k)}
		})
		if errText != "" {
			return unsupportedf(at, "%s", errText)
		}
		// riders (measures, depends, libraryAdapter) survive later
		// chain methods -- a tightened set still wears them.
		// CanonicalScalarForms folds the new ray against any ray base
		// already carries (z.number()'s own atLeast(-Inf) beside a
		// tighter .gte(0)) and drops what stays vacuous beside it --
		// the same hygiene the string chain's WithoutStringGround call
		// gives its own ground conjunct, generalized: FoldRayForms
		// reads a same-direction ray dominance regardless of which
		// ground the ray sits over.
		result := inner
		result.Set = setPtr(refinementsets.CanonicalScalarForms(refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), forms...)...)))
		return &Compiled{Annotation: &result}
	}

	/* Sequence length bounds first; value bounds on scalar chains.
	   Every bound reads EXACTLY as the count the user wrote -- k
	   scalar values. A library whose runtime counts differently (zod
	   counts UTF-16 units) has a runtime bug the checker does not
	   inherit; its semantic_quirks.go records the divergent cases. */
	bound := func(kind string, scalar func(k float64) refinementsets.Refinement, hasScalar bool) *Compiled {
		k, ok := oneNumberArg(p, args)
		if !ok {
			return unsupportedf(at, ".%s takes one numeric literal", method)
		}
		tightened, tightenedOk, errText := guardedTighten(base, kind, int(k))
		if errText != "" {
			return unsupportedf(at, "%s", errText)
		}
		if tightenedOk {
			result := inner
			result.Word = nil
			result.Set = setPtr(tightened)
			return &Compiled{Annotation: &result}
		}
		// a PATTERN chain carries no repetition to tighten -- the
		// length bound CONJOINS its own bounded repetition instead:
		// `startsWith("u").min(2)` is the pattern AND the window.
		// Keyed on the base being SEQUENCE-shaped: a scalar chain
		// keeps its ray forms below.
		if isSequenceShaped(base) {
			lo := 0
			var hi *int
			if kind == "max" {
				lo = 0
			} else {
				lo = int(k)
			}
			if kind != "min" {
				hiVal := int(k)
				hi = &hiVal
			}
			repForms, errText := guardedForm(func() []refinementsets.Refinement {
				return refinementsets.Repetition(refinementsets.Codepoints, lo, hi).Forms
			})
			if errText != "" {
				return unsupportedf(at, "%s", errText)
			}
			result := inner
			result.Word = nil
			result.Set = setPtr(refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), repForms...)...))
			return &Compiled{Annotation: &result}
		}
		if !hasScalar {
			return unsupportedf(at, ".%s needs a sequence-shaped chain", method)
		}
		return withForm(scalar)
	}

	switch method {
	case "min":
		if inner.Date {
			t, ok := dateMillisArg(args)
			if !ok {
				return unsupportedf(at, ".min on a date chain takes new Date(<literal>)")
			}
			return &Compiled{Annotation: &Annotation{
				Set:  setPtr(refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), refinementsets.AtLeast(t))...)),
				Date: true,
			}}
		}
		return bound("min", refinementsets.AtLeast, true)
	case "gte":
		return withForm(refinementsets.AtLeast)
	case "max":
		if inner.Date {
			t, ok := dateMillisArg(args)
			if !ok {
				return unsupportedf(at, ".max on a date chain takes new Date(<literal>)")
			}
			return &Compiled{Annotation: &Annotation{
				Set:  setPtr(refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), refinementsets.AtMost(t))...)),
				Date: true,
			}}
		}
		return bound("max", refinementsets.AtMost, true)
	case "lte":
		return withForm(refinementsets.AtMost)
	case "gt":
		return withForm(refinementsets.Above)
	case "lt":
		return withForm(refinementsets.Below)
	case "length":
		return bound("length", nil, false)
	case "sumTo":
		// a sequence MEASURE: the exact reduce total, parse-checked
		k, ok := oneNumberArg(p, args)
		if !ok {
			return unsupportedf(at, ".sumTo takes one numeric literal")
		}
		if _, repOk := refinementsets.AsRepetition(base); !repOk {
			return unsupportedf(at, ".sumTo needs a sequence-shaped chain")
		}
		result := inner
		result.Set = setPtr(base)
		measures := Measures{}
		if inner.Measures != nil {
			measures = *inner.Measures
		}
		measures.HasSum = true
		measures.Sum = k
		result.Measures = &measures
		return &Compiled{Annotation: &result}
	case "sorted":
		// a sequence MEASURE: non-decreasing elements, parse-checked
		if len(args) != 0 {
			return unsupportedf(at, ".sorted takes no arguments")
		}
		if _, repOk := refinementsets.AsRepetition(base); !repOk {
			return unsupportedf(at, ".sorted needs a sequence-shaped chain")
		}
		result := inner
		result.Set = setPtr(base)
		measures := Measures{}
		if inner.Measures != nil {
			measures = *inner.Measures
		}
		measures.Sorted = true
		result.Measures = &measures
		return &Compiled{Annotation: &result}
	case "int":
		return &Compiled{Annotation: &Annotation{Set: setPtr(refinementsets.CanonicalScalarForms(refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), refinementsets.Integer)...)))}}
	case "multipleOf":
		return withForm(refinementsets.MultipleOf)
	case "finite":
		// the finite reals: R-bar without +-infinity (NaN is never an
		// element of any set -- the boundary rejects it) -- zod's own
		// `.finite()`
		if len(args) != 0 {
			return unsupportedf(at, ".finite takes no arguments")
		}
		return &Compiled{Annotation: &Annotation{
			Set: setPtr(refinementsets.CanonicalScalarForms(refinementsets.MakeRefinedSet(append(
				append([]refinementsets.Refinement{}, base.Forms...),
				refinementsets.Difference(
					refinementsets.Numbers,
					refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(-1), math.Inf(1)})),
				),
			)...))),
		}}
	case "mod":
		// the NORMALIZED modular window -- RefinedTS's own chain
		// method (ModWindowForms): a value in [0, p) whose cycle
		// position sits in [lo, hi], wrapping when lo > hi
		if len(args) != 3 {
			return unsupportedf(at, ".%s takes three numeric literals (period, low edge, high edge)", method)
		}
		period, ok1 := NumberArg(p, args[0])
		lo, ok2 := NumberArg(p, args[1])
		hi, ok3 := NumberArg(p, args[2])
		if !ok1 || !ok2 || !ok3 {
			return unsupportedf(at, ".%s takes three numeric literals (period, low edge, high edge)", method)
		}
		forms, errText := guardedForm(func() []refinementsets.Refinement {
			return refinementsets.ModWindowForms(period, lo, hi)
		})
		if errText != "" {
			return unsupportedf(at, "%s", errText)
		}
		result := inner
		result.Set = setPtr(refinementsets.CanonicalScalarForms(refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, base.Forms...), forms...)...)))
		return &Compiled{Annotation: &result}
	default:
		return nil
	}
}

// oneNumberArg reads the sole numeric-literal argument, mirroring the
// TS source's repeated `args.length === 1 ? numberArg(p, args[0]) :
// null` guard.
func oneNumberArg(p *program.CheckerProgram, args []*ast.Node) (float64, bool) {
	if len(args) != 1 {
		return 0, false
	}
	return NumberArg(p, args[0])
}

// guardedTighten wraps refinementsets.TightenRepetition's possible
// panic (an invalid rebuilt window) as a (value, ok, err) triple --
// the TS source's try/catch around `tightenRepetition(...)`.
func guardedTighten(base refinementsets.RefinedSet, method string, k int) (result refinementsets.RefinedSet, ok bool, errText string) {
	defer func() {
		if r := recover(); r != nil {
			errText = panicText(r)
		}
	}()
	tightened, tightenedOk := refinementsets.TightenRepetition(base, method, k, nil)
	return tightened, tightenedOk, ""
}

// isSequenceShaped is the base.forms.some(...) test in bound(): the
// base carries a repetition-family form.
func isSequenceShaped(base refinementsets.RefinedSet) bool {
	for _, f := range base.Forms {
		switch f.Form {
		case refinementsets.FormConcatenation, refinementsets.FormStar,
			refinementsets.FormRepeat, refinementsets.FormEmptyTuple, refinementsets.FormWord:
			return true
		}
	}
	return false
}

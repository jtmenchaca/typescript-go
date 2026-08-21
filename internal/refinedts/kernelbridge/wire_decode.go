// Kernel JSON answers back to checker values: a wire number to its
// float, a wire set to the grammar, and the envelope every answer
// wears (an error field, or the stated fields).
package kernelbridge

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// wireNumberRaw mirrors the `unknown` the TS decoder reads: either the
// bare string "-inf"/"+inf"/"-0", or {"num":…,"exp":…}.
func decodeRawNumber(raw any) (float64, error) {
	if s, ok := raw.(string); ok {
		switch s {
		case "-inf":
			return math.Inf(-1), nil
		case "+inf":
			return math.Inf(1), nil
		case "-0":
			// IEEE negative zero: the kernel's own ExtendedReal.negZero,
			// distinct from +0 by math.Signbit alone (x == 0 is true for
			// both) — math.Copysign(0, -1) is how a float64 carries that
			// sign.
			return math.Copysign(0, -1), nil
		default:
			return 0, fmt.Errorf("kernel: unexpected wire number string %q", s)
		}
	}
	o, ok := raw.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("kernel: unexpected wire number shape: %v", raw)
	}
	num, numOK := o["num"].(float64)
	exp, expOK := o["exp"].(float64)
	if !numOK || !expOK {
		return 0, fmt.Errorf("kernel: unexpected wire number shape: %v", raw)
	}
	return num * pow2(exp), nil
}

// DecodeWireNumber is decodeWireNumber in the TS source: a wire number
// back to the JS float it names — exact: the kernel renormalizes every
// mantissa under 2^53 before answering. Panics on a malformed answer,
// mirroring the TS cast-through-unknown (a mistyped kernel answer is a
// contract violation, not a value this checker degrades on).
func DecodeWireNumber(raw any) float64 {
	x, err := decodeRawNumber(raw)
	if err != nil {
		panic(err.Error())
	}
	return x
}

// pow2 mirrors `o.num * 2 ** o.exp` — exact for the integer exponents
// the wire carries.
func pow2(exp float64) float64 {
	return math.Pow(2, exp)
}

func formOf(o map[string]any) (string, error) {
	form, ok := o["form"].(string)
	if !ok {
		return "", fmt.Errorf("kernel answered a form with no \"form\" field: %v", o)
	}
	return form, nil
}

// DecodeWireSet is decodeWireSet in the TS source: a set the kernel
// answered, rebuilt as the checker's own value — the full grammar: the
// transfers answer bound/integrality/step forms (plus the "integer or
// ±∞" union), and the narrowing question answers arbitrary constructed
// sets. Panics on an unknown form, mirroring the TS `throw`.
func DecodeWireSet(raw any) refinementsets.RefinedSet {
	top, ok := raw.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("kernel answered an unexpected set shape: %v", raw))
	}
	rawForms, ok := top["forms"].([]any)
	if !ok {
		panic(fmt.Sprintf("kernel answered an unexpected set shape: %v", raw))
	}
	forms := make([]refinementsets.Refinement, len(rawForms))
	for i, f := range rawForms {
		o, ok := f.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("kernel answered an unknown form: %v", f))
		}
		form, err := formOf(o)
		if err != nil {
			panic(err.Error())
		}
		switch form {
		case "atLeast":
			forms[i] = refinementsets.AtLeast(DecodeWireNumber(o["a"]))
		case "above":
			forms[i] = refinementsets.Above(DecodeWireNumber(o["a"]))
		case "atMost":
			forms[i] = refinementsets.AtMost(DecodeWireNumber(o["a"]))
		case "below":
			forms[i] = refinementsets.Below(DecodeWireNumber(o["a"]))
		case "integer":
			forms[i] = refinementsets.Integer
		case "multipleOf":
			forms[i] = refinementsets.MultipleOf(DecodeWireNumber(o["d"]))
		case "oneOf":
			rawW, ok := o["w"].([]any)
			if !ok {
				panic(fmt.Sprintf("kernel answered an unknown form: %v", f))
			}
			w := make([]float64, len(rawW))
			for j, x := range rawW {
				w[j] = DecodeWireNumber(x)
			}
			forms[i] = refinementsets.OneOf(w)
		case "emptyTuple":
			forms[i] = refinementsets.EmptyTuple
		case "concatenation":
			forms[i] = refinementsets.Concatenation(DecodeWireSet(o["A"]), DecodeWireSet(o["B"]))
		case "star":
			forms[i] = refinementsets.Star(DecodeWireSet(o["A"]))
		case "repeat":
			lo, _ := o["lo"].(float64)
			var hi *int
			if rawHi, held := o["hi"]; held {
				if h, ok := rawHi.(float64); ok {
					hv := int(h)
					hi = &hv
				}
			}
			forms[i] = refinementsets.RepeatOf(DecodeWireSet(o["A"]), int(lo), hi)
		case "union":
			forms[i] = refinementsets.Union(DecodeWireSet(o["A"]), DecodeWireSet(o["B"]))
		case "difference":
			forms[i] = refinementsets.Difference(DecodeWireSet(o["A"]), DecodeWireSet(o["B"]))
		default:
			panic(fmt.Sprintf("kernel answered an unknown form: %v", f))
		}
	}
	return refinementsets.MakeRefinedSet(forms...)
}

// Answered is `answered` in the TS source: parse the kernel's raw JSON
// and throw its stated error, if any.
func Answered(raw string) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		panic(fmt.Sprintf("kernel: unparseable answer: %v", err))
	}
	if message, ok := parsed["error"].(string); ok {
		panic(fmt.Sprintf("kernel: %s", message))
	}
	return parsed
}

// BooleanField is `booleanField` in the TS source.
func BooleanField(raw string, field string) bool {
	value, ok := Answered(raw)[field].(bool)
	if !ok {
		panic(fmt.Sprintf("kernel answered without a boolean %q: %s", field, raw))
	}
	return value
}

// KernelFault mirrors kernel_interface.ts's KernelFault.
type KernelFault struct {
	Path        int64
	Code        int64
	MessageText string
	// Key is the dotted key path a VALUE-judgment fault (codes
	// 3008-3014, boundary/exports_graph.lean's valueFaultRow) wears
	// instead of a path index; "" on every specification-level fault.
	// 3014 (subset undecided on the shape pair) is the one code a
	// caller reads as a decline, never a program verdict.
	Key string
}

// JudgeAnswer is the anonymous return shape of decodeJudgeAnswer in the
// TS source.
type JudgeAnswer struct {
	Structural   bool
	Faults       []KernelFault
	Witnessed    bool
	WitnessBound float64
	// Value/HasValue: the value-instantiation verdict, present only
	// when the question carried a "values" field — true is a proved
	// instantiation (instantiateB_iff); false arrives with its
	// 3008-3012 faults.
	Value    bool
	HasValue bool
}

// DecodeJudgeAnswer is decodeJudgeAnswer in the TS source.
func DecodeJudgeAnswer(parsed map[string]any) JudgeAnswer {
	structural, structuralOK := parsed["structural"].(bool)
	rawFaults, faultsOK := parsed["faults"].([]any)
	witnessed, witnessedOK := parsed["witnessed"].(bool)
	witnessBound, boundOK := parsed["witnessBound"].(float64)
	if !structuralOK || !faultsOK || !witnessedOK || !boundOK {
		panic(fmt.Sprintf(
			"kernel checkAssignability answered an unexpected shape: %v", parsed,
		))
	}
	faults := make([]KernelFault, len(rawFaults))
	for i, rf := range rawFaults {
		f, ok := rf.(map[string]any)
		if !ok {
			panic(fmt.Sprintf(
				"kernel checkAssignability answered an unexpected shape: %v", parsed,
			))
		}
		key, _ := f["key"].(string)
		faults[i] = KernelFault{
			Path:        int64(asFloat(f["path"])),
			Code:        int64(asFloat(f["code"])),
			MessageText: fmt.Sprintf("%v", f["messageText"]),
			Key:         key,
		}
	}
	value, hasValue := parsed["value"].(bool)
	return JudgeAnswer{
		Structural:   structural,
		Faults:       faults,
		Witnessed:    witnessed,
		WitnessBound: witnessBound,
		Value:        value,
		HasValue:     hasValue,
	}
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		// mirrors TS `Number(f.path)` reading a numeral string
		var out float64
		fmt.Sscanf(n, "%g", &out)
		return out
	default:
		return 0
	}
}

// ValidateChainKind is the tag of the ValidateChain result union.
type ValidateChainKind string

const (
	ValidateChainSet      ValidateChainKind = "set"
	ValidateChainAnswer   ValidateChainKind = "answer"
	ValidateChainDeclined ValidateChainKind = "declined"
)

// ValidateChainResult is the TS union
// `{ kind: "set" } | { kind: "answer"; answer } | { kind: "declined"; why }`.
type ValidateChainResult struct {
	Kind   ValidateChainKind
	Answer bool
	Why    string
}

// DecodeValidateChain is decodeValidateChain in the TS source.
func DecodeValidateChain(parsed map[string]any) ValidateChainResult {
	kind, _ := parsed["kind"].(string)
	if kind == "set" {
		return ValidateChainResult{Kind: ValidateChainSet}
	}
	if kind == "answer" {
		if answer, ok := parsed["answer"].(bool); ok {
			return ValidateChainResult{Kind: ValidateChainAnswer, Answer: answer}
		}
	}
	if kind == "declined" {
		if why, ok := parsed["why"].(string); ok {
			return ValidateChainResult{Kind: ValidateChainDeclined, Why: why}
		}
	}
	panic(fmt.Sprintf("kernel validate answered an unexpected shape: %v", parsed))
}

// BoundsResult is the TS union
// `{ empty: true } | { empty: false; hull: RefinedSet }`.
type BoundsResult struct {
	Empty bool
	Hull  refinementsets.RefinedSet
}

// DecodeBounds is decodeBounds in the TS source.
func DecodeBounds(parsed map[string]any) BoundsResult {
	if empty, ok := parsed["empty"].(bool); ok && empty {
		return BoundsResult{Empty: true}
	}
	if set, held := parsed["set"]; held && set != nil {
		return BoundsResult{Empty: false, Hull: DecodeWireSet(set)}
	}
	panic(fmt.Sprintf("kernel bounds answered an unexpected shape: %v", parsed))
}

// DecodeMembers is decodeMembers in the TS source.
func DecodeMembers(parsed map[string]any) []float64 {
	rawMembers, ok := parsed["members"].([]any)
	if !ok {
		panic(fmt.Sprintf("kernel members answered an unexpected shape: %v", parsed))
	}
	members := make([]float64, len(rawMembers))
	for i, m := range rawMembers {
		members[i] = DecodeWireNumber(m)
	}
	return members
}

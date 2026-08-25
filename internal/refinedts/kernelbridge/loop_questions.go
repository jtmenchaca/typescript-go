// Loop and walk questions: entry premises, the loop question and its
// wire, the invariant widen/ask asks, and the loop-var answer decode.
package kernelbridge

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// InvariantPremiseKind is the tag of an InvariantPremise.
type InvariantPremiseKind string

const (
	InvariantPremiseValues InvariantPremiseKind = "values"
	InvariantPremiseSet    InvariantPremiseKind = "set"
)

// InvariantPremise is one premise of the loop-invariant certificate: a
// concrete value (membership) or a set it lies in (subset).
type InvariantPremise struct {
	Kind   InvariantPremiseKind
	Values []float64
	Set    refinementsets.RefinedSet
}

// PremiseWire is premiseWire in the TS source.
func PremiseWire(p InvariantPremise) string {
	if p.Kind == InvariantPremiseValues {
		return fmt.Sprintf(`{"tuple":%s}`, EncodeTuple(p.Values))
	}
	return fmt.Sprintf(`{"set":%s}`, EncodeSet(p.Set))
}

// PremiseKey is premiseKey in the TS source.
//
// The "values" branch used to hand-format the raw tuple with
// marshalWireValue (a literal json.Marshal), the same collapsed
// spelling CanonicalPairOfSetAndTuple's member-question key carried
// before its own fix: Go's encoding/json refuses to marshal a
// non-finite float64 at all, so a tuple holding +Inf, -Inf, or NaN
// panicked here. A loop-entry premise genuinely carries such a value —
// abstractdomain.KindValues tracks an exact NaN or Infinity read
// (`memo_spell_test.go` pins both spelling distinctly) and
// walk/loop_candidate.go and walk/certified_invariant.go pass that
// tracked value straight into InvariantPremiseValues — so the honest
// fix is the same one CanonicalPairOfSetAndTuple took: key every
// member with cacheNumberString, which keeps each finite numeral as
// before and gives +Inf/-Inf/NaN their own distinct sentinel instead
// of erroring, so two premises differing only in which non-finite
// value they hold key apart rather than crashing.
func PremiseKey(p InvariantPremise) (string, bool) {
	if p.Kind == InvariantPremiseValues {
		parts := make([]string, len(p.Values))
		for i, x := range p.Values {
			parts[i] = cacheNumberString(x)
		}
		return "[" + strings.Join(parts, ",") + "]", true
	}
	key := CanonicalKeyOf(wireSet(p.Set))
	if key == nil {
		return "", false
	}
	return *key, true
}

// LoopQuestion is the TS LoopQuestion interface.
type LoopQuestion struct {
	// Entry: per binding, the entry premise, or nil when nothing is
	// known.
	Entry []*InvariantPremise
	// Cond: per binding, the loop condition's narrowing set, if one
	// reads (nil otherwise).
	Cond []*refinementsets.RefinedSet
	// Body: per binding, the body's effect.
	Body []LoopEffect
	// CondCmp: the head when it compared two tracked slots. Nothing
	// constant bounds either side, so this rides instead of Cond and
	// the kernel cuts each pass entry by the bound the other slot's
	// own entry value states.
	CondCmp *IrLoopCondCmp
}

// LoopVarAnswerKind is the tag of a LoopVarAnswer.
type LoopVarAnswerKind string

const (
	LoopVarAnswerSet     LoopVarAnswerKind = "set"
	LoopVarAnswerUnknown LoopVarAnswerKind = "unknown"
)

// LoopVarAnswer is the TS LoopVarAnswer union.
type LoopVarAnswer struct {
	Kind LoopVarAnswerKind
	Set  refinementsets.RefinedSet
}

// LoopWire is loopWire in the TS source.
func LoopWire(q LoopQuestion) string {
	entry := make([]string, len(q.Entry))
	for i, p := range q.Entry {
		if p == nil {
			entry[i] = `{"unknown":true}`
		} else {
			entry[i] = PremiseWire(*p)
		}
	}
	cond := make([]string, len(q.Cond))
	for i, c := range q.Cond {
		cond[i] = optSetWire(c)
	}
	body := make([]string, len(q.Body))
	for i, b := range q.Body {
		body[i] = EffectWire(b)
	}
	condCmp := ""
	if q.CondCmp != nil {
		condCmp = fmt.Sprintf(
			`,"condCmp":{"on":%d,"test":"%s","onB":%d}`,
			q.CondCmp.On, q.CondCmp.Test, q.CondCmp.OnB,
		)
	}
	return fmt.Sprintf(
		`{"entry":[%s],"cond":[%s],"body":[%s]%s}`,
		strings.Join(entry, ","), strings.Join(cond, ","),
		strings.Join(body, ","), condCmp,
	)
}

// InvariantWidenWireResult is the non-nil return of InvariantWidenWire.
type InvariantWidenWireResult struct {
	Wire   string
	Key    string
	HasKey bool
}

// flattenUnionLeaves mirrors the TS flatten() closure in
// invariantWidenWire: walk a union tree's leaves in wire form.
func flattenUnionLeaves(set refinementsets.RefinedSet, leaves *[]string) {
	var f *refinementsets.Refinement
	if len(set.Forms) == 1 {
		f = &set.Forms[0]
	}
	if f != nil && f.Form == refinementsets.FormUnion {
		flattenUnionLeaves(*f.A_, leaves)
		flattenUnionLeaves(*f.B, leaves)
	} else {
		*leaves = append(*leaves, EncodeSet(set))
	}
}

// InvariantWidenWire is invariantWidenWire in the TS source: the
// widening invariant wire when the candidate is a union whose leaves
// include the step's set — the kernel builds base ∪ step itself
// (`invariantWidenB_certifies`). Returns ok=false when the shape does
// not apply (the TS `null`).
func InvariantWidenWire(
	candidate refinementsets.RefinedSet, entry InvariantPremise, step InvariantPremise,
) (result InvariantWidenWireResult, ok bool) {
	if step.Kind != InvariantPremiseSet || len(candidate.Forms) != 1 {
		return InvariantWidenWireResult{}, false
	}
	form := candidate.Forms[0]
	if form.Form != refinementsets.FormUnion {
		return InvariantWidenWireResult{}, false
	}
	stepWire := EncodeSet(step.Set)
	var leaves []string
	flattenUnionLeaves(candidate, &leaves)
	at := -1
	for i, leaf := range leaves {
		if leaf == stepWire {
			at = i
			break
		}
	}
	var rest []string
	if at != -1 {
		for i, leaf := range leaves {
			if i != at {
				rest = append(rest, leaf)
			}
		}
	}
	if len(rest) == 0 {
		return InvariantWidenWireResult{}, false
	}
	base := rest[0]
	for _, v := range rest[1:] {
		base = fmt.Sprintf(`{"forms":[{"form":"union","A":%s,"B":%s}]}`, base, v)
	}
	wire := fmt.Sprintf(`{"entry":%s,"base":%s,"step":%s}`, PremiseWire(entry), base, stepWire)
	canonC := CanonicalKeyOf(wireSet(candidate))
	canonE, canonEOK := PremiseKey(entry)
	if canonC == nil || !canonEOK {
		return InvariantWidenWireResult{Wire: wire}, true
	}
	return InvariantWidenWireResult{
		Wire: wire, Key: fmt.Sprintf("widen:%s%s", *canonC, canonE), HasKey: true,
	}, true
}

// InvariantAskWireResult is the return of InvariantAskWire.
type InvariantAskWireResult struct {
	Wire   string
	Key    string
	HasKey bool
}

// InvariantAskWire is invariantAskWire in the TS source.
func InvariantAskWire(
	candidate refinementsets.RefinedSet, entry InvariantPremise, step InvariantPremise,
) InvariantAskWireResult {
	wire := fmt.Sprintf(
		`{"candidate":%s,"entry":%s,"step":%s}`,
		EncodeSet(candidate), PremiseWire(entry), PremiseWire(step),
	)
	canonC := CanonicalKeyOf(wireSet(candidate))
	canonE, canonEOK := PremiseKey(entry)
	canonS, canonSOK := PremiseKey(step)
	if canonC == nil || !canonEOK || !canonSOK {
		return InvariantAskWireResult{Wire: wire}
	}
	return InvariantAskWireResult{
		Wire: wire, Key: fmt.Sprintf("%s\x01%s\x01%s", *canonC, canonE, canonS), HasKey: true,
	}
}

// DecodeLoopVars is decodeLoopVars in the TS source.
func DecodeLoopVars(parsed map[string]any) []LoopVarAnswer {
	rawVars, ok := parsed["vars"].([]any)
	if !ok {
		panic(fmt.Sprintf("kernel solveLoop answered an unexpected shape: %v", parsed))
	}
	vars := make([]LoopVarAnswer, len(rawVars))
	for i, rv := range rawVars {
		v, ok := rv.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("kernel solveLoop answered an unexpected shape: %v", parsed))
		}
		if v["kind"] == "set" {
			vars[i] = LoopVarAnswer{Kind: LoopVarAnswerSet, Set: DecodeWireSet(v["set"])}
		} else {
			vars[i] = LoopVarAnswer{Kind: LoopVarAnswerUnknown}
		}
	}
	return vars
}

// The JSON the kernel decodes (boundary/exports.lean's wire forms),
// produced from the checker's working values. Numbers cross as exact
// integer pairs via ground/dyadics — the −∞/+∞ elements as the strings
// "-inf"/"+inf", and IEEE negative zero as the string "-0" (the
// kernel's own ExtendedReal.negZero, distinct from the ordinary
// {num:0, exp:0} dyadic pair +0 shares). This file is encoding only;
// the kernel's answers are plain JSON and parse with JSON.parse at the
// loader.
//
// encodeSpecification and its CardinalityPath/Specification inputs are
// NOT ported here: object_graphs/graph_specification.ts (Specification,
// CardinalityPath, ObjectKey, ObjectNode, CountGroup, countGroup,
// specification) has no Go twin yet — object_graphs is later in the
// port order. Reported as blocked in the port report.
package kernelbridge

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// WireNumber is the TS union
// `{ num: number; exp: number } | "-inf" | "+inf" | "-0"` — a dyadic pair
// for a finite value, one of the two infinity strings, or the negative-zero
// string. Marshals to exactly the TS wire shape.
type WireNumber struct {
	Dyadic    primitives.Dyadic
	IsInf     bool
	InfWord   string // "-inf" or "+inf", meaningful only when IsInf
	IsNegZero bool   // −0: the kernel's separate ExtendedReal constructor
}

// MarshalJSON writes the dyadic pair {"num":…,"exp":…}, the bare infinity
// string, or the bare "-0" string.
func (w WireNumber) MarshalJSON() ([]byte, error) {
	if w.IsInf {
		return json.Marshal(w.InfWord)
	}
	if w.IsNegZero {
		return json.Marshal("-0")
	}
	return json.Marshal(struct {
		Num int64 `json:"num"`
		Exp int64 `json:"exp"`
	}{Num: w.Dyadic.Num, Exp: w.Dyadic.Exp})
}

// WireNumberOf is wireNumber in the TS source.
func WireNumberOf(x float64) WireNumber {
	if math.IsInf(x, 1) {
		return WireNumber{IsInf: true, InfWord: "+inf"}
	}
	if math.IsInf(x, -1) {
		return WireNumber{IsInf: true, InfWord: "-inf"}
	}
	// −0 is IEEE negative zero: math.Signbit is what distinguishes it from
	// +0 (x == 0 is true for both). The kernel now holds a separate
	// ExtendedReal constructor for it, so the sign must survive the wire
	// rather than collapse into the ordinary {num:0, exp:0} dyadic pair.
	if x == 0 && math.Signbit(x) {
		return WireNumber{IsNegZero: true}
	}
	d, err := primitives.DyadicOfNumber(x)
	if err != nil {
		// x is finite here (the ±∞ cases are handled above) and NaN is
		// refused by the boundary before a float ever reaches this
		// function — mirrors the TS: dyadicOfNumber throws only on NaN
		// or ±∞, neither reachable at this point.
		panic(fmt.Sprintf("wireNumber: %v", err))
	}
	return WireNumber{Dyadic: d}
}

// WireBigInt is the wire shape of a bigint: exact at any width — plain
// below 2^53, decimal digits as a string beyond (the kernel's decodeInt
// reads both).
type WireBigInt struct {
	NumInt    int64
	NumString string
	UseString bool
	Exp       int64
}

func (w WireBigInt) MarshalJSON() ([]byte, error) {
	if w.UseString {
		return json.Marshal(struct {
			Num string `json:"num"`
			Exp int64  `json:"exp"`
		}{Num: w.NumString, Exp: w.Exp})
	}
	return json.Marshal(struct {
		Num int64 `json:"num"`
		Exp int64 `json:"exp"`
	}{Num: w.NumInt, Exp: w.Exp})
}

const maxSafeInteger int64 = 9007199254740991

// WireBigIntOf is wireBigInt in the TS source. TS bigint has no width
// bound; the Go twin takes int64 per the port's number convention
// (bigint → int64 unless the TS code exceeds it). Every magnitude an
// int64 can hold fits the plain-number branch (2^63-1 vastly exceeds
// 2^53-1's own boundary check only when the TS bigint is wider than
// int64, which does not arise here), so the string branch is dead code
// kept for shape parity with the TS wire and the decoder that reads it.
func WireBigIntOf(v int64) WireBigInt {
	abs := v
	if abs < 0 {
		abs = -abs
	}
	if abs <= maxSafeInteger {
		return WireBigInt{NumInt: v, Exp: 0}
	}
	return WireBigInt{NumString: strconv.FormatInt(v, 10), UseString: true, Exp: 0}
}

// wireFormJSON is wireForm in the TS source, spelled as a hand-built
// JSON string rather than a marshaled struct: the TS object literal's
// KEY ORDER survives into JSON.stringify's output, and the kernel_bridge
// test fixtures (wire_format_test.go, ported 1:1 from wire_format.test.ts)
// assert those exact strings — `encoding/json`'s map-based Marshal sorts
// keys alphabetically instead, which does not match. wireFormValue below
// (a map[string]any) exists ONLY for canonicalKeyOf's order-INsensitive
// walk (question_cache.go sorts keys itself either way).
func wireFormJSON(r refinementsets.Refinement) string {
	switch r.Form {
	case refinementsets.FormAtLeast, refinementsets.FormAbove,
		refinementsets.FormAtMost, refinementsets.FormBelow:
		return fmt.Sprintf(`{"form":"%s","a":%s}`, r.Form, marshalWireValue(WireNumberOf(r.A)))
	case refinementsets.FormInteger, refinementsets.FormEmptyTuple:
		return fmt.Sprintf(`{"form":"%s"}`, r.Form)
	case refinementsets.FormMultipleOf:
		d, err := primitives.DyadicOfNumber(r.A)
		if err != nil {
			panic(fmt.Sprintf("wireFormJSON: multipleOf: %v", err))
		}
		return fmt.Sprintf(`{"form":"%s","d":{"num":%d,"exp":%d}}`, r.Form, d.Num, d.Exp)
	case refinementsets.FormOneOf, refinementsets.FormWord:
		w := make([]string, len(r.W))
		for i, x := range r.W {
			w[i] = marshalWireValue(WireNumberOf(x))
		}
		return fmt.Sprintf(`{"form":"%s","w":[%s]}`, r.Form, joinComma(w))
	case refinementsets.FormStar:
		return fmt.Sprintf(`{"form":"%s","A":%s}`, r.Form, encodeSetJSON(*r.A_))
	case refinementsets.FormRepeat, refinementsets.FormRepeatWord:
		// an absent hi decodes as the unbounded window
		if r.Hi == nil {
			return fmt.Sprintf(`{"form":"%s","A":%s,"lo":%d}`, r.Form, encodeSetJSON(*r.A_), r.Lo)
		}
		return fmt.Sprintf(
			`{"form":"%s","A":%s,"lo":%d,"hi":%d}`, r.Form, encodeSetJSON(*r.A_), r.Lo, *r.Hi,
		)
	case refinementsets.FormConcatenation, refinementsets.FormUnion,
		refinementsets.FormDifference:
		return fmt.Sprintf(
			`{"form":"%s","A":%s,"B":%s}`, r.Form, encodeSetJSON(*r.A_), encodeSetJSON(*r.B),
		)
	}
	refinementsets.UnreachedForm(r)
	return ""
}

func encodeSetJSON(set refinementsets.RefinedSet) string {
	forms := make([]string, len(set.Forms))
	for i, f := range set.Forms {
		forms[i] = wireFormJSON(f)
	}
	return fmt.Sprintf(`{"forms":[%s]}`, joinComma(forms))
}

// EncodeSet is encodeSet in the TS source. Every question re-encodes
// its sets — the wire is built from the working value each time it is
// asked, including when the answer is about to come from the question
// cache. `wire.bytes` is how much JSON that comes to over a run.
func EncodeSet(set refinementsets.RefinedSet) string {
	if !tracing.Recording(tracing.GrainStep) {
		return encodeSetJSON(set)
	}
	return tracing.Span("wire.encodeSet", func() string {
		text := encodeSetJSON(set)
		tracing.CountBy("wire.bytes", int64(len(text)))
		return text
	}, tracing.GrainStep)
}

// wireFormValue and wireSet are the order-INsensitive twins of
// wireFormJSON/encodeSetJSON, used only as CanonicalKeyOf's input (see
// question_cache.go: canonicalKeyWithBudget sorts every object's keys
// itself, so field order here is immaterial — only the value shape
// needs to survive the walk).
func wireFormValue(r refinementsets.Refinement) map[string]any {
	switch r.Form {
	case refinementsets.FormAtLeast, refinementsets.FormAbove,
		refinementsets.FormAtMost, refinementsets.FormBelow:
		return map[string]any{"form": string(r.Form), "a": WireNumberOf(r.A)}
	case refinementsets.FormInteger, refinementsets.FormEmptyTuple:
		return map[string]any{"form": string(r.Form)}
	case refinementsets.FormMultipleOf:
		d, err := primitives.DyadicOfNumber(r.A)
		if err != nil {
			panic(fmt.Sprintf("wireFormValue: multipleOf: %v", err))
		}
		return map[string]any{"form": string(r.Form), "d": map[string]any{"num": d.Num, "exp": d.Exp}}
	case refinementsets.FormOneOf, refinementsets.FormWord:
		w := make([]any, len(r.W))
		for i, x := range r.W {
			w[i] = WireNumberOf(x)
		}
		return map[string]any{"form": string(r.Form), "w": w}
	case refinementsets.FormStar:
		return map[string]any{"form": string(r.Form), "A": wireSet(*r.A_)}
	case refinementsets.FormRepeat, refinementsets.FormRepeatWord:
		if r.Hi == nil {
			return map[string]any{
				"form": string(r.Form), "A": wireSet(*r.A_), "lo": r.Lo,
			}
		}
		return map[string]any{
			"form": string(r.Form), "A": wireSet(*r.A_), "lo": r.Lo, "hi": *r.Hi,
		}
	case refinementsets.FormConcatenation, refinementsets.FormUnion,
		refinementsets.FormDifference:
		return map[string]any{
			"form": string(r.Form), "A": wireSet(*r.A_), "B": wireSet(*r.B),
		}
	}
	refinementsets.UnreachedForm(r)
	return nil
}

func wireSet(set refinementsets.RefinedSet) map[string]any {
	forms := make([]any, len(set.Forms))
	for i, f := range set.Forms {
		forms[i] = wireFormValue(f)
	}
	return map[string]any{"forms": forms}
}

// scalarValue is the TS scalarValue helper: a tuple member the compact
// form can carry — printable ASCII, excluding the quote and the
// backslash — one byte per character, no escapes, exactly what the
// kernel's minimal JSON reader takes. Everything else (controls,
// non-ASCII, the two escaped characters) keeps the list form.
//
// TS: `typeof x === "number" && Number.isInteger(x) && …` — the Go
// twin takes float64 (the TS union member types), so the isInteger
// check is explicit; a Go int64 (the bigint member) is never a scalar
// value in the TS source either (its typeof is "bigint", not "number").
func scalarValue(x float64) bool {
	return x == math.Trunc(x) && !math.IsInf(x, 0) &&
		x >= 0x20 && x <= 0x7E && x != 0x22 && x != 0x5C
}

// EncodeTuple is encodeTuple in the TS source, over the float64 member
// (number | bigint) — see EncodeTupleBigInt for the bigint member.
func EncodeTuple(xs []float64) string {
	if !tracing.Recording(tracing.GrainStep) {
		return encodeTupleBody(xs)
	}
	return tracing.Span("wire.encodeTuple", func() string {
		return encodeTupleBody(xs)
	}, tracing.GrainStep)
}

func encodeTupleBody(xs []float64) string {
	if len(xs) > 0 {
		allScalar := true
		for _, x := range xs {
			if !scalarValue(x) {
				allScalar = false
				break
			}
		}
		if allScalar {
			// all scalar values: the whole tuple crosses as ONE string —
			// each character is its code point, the same integers the list
			// form would carry, at a fraction of the wire and encode cost
			var text strings.Builder
			for _, x := range xs {
				text.WriteRune(rune(int32(x)))
			}
			encoded, err := json.Marshal(text.String())
			if err != nil {
				panic(fmt.Sprintf("EncodeTuple: %v", err))
			}
			return fmt.Sprintf(`{"w":%s}`, encoded)
		}
	}
	w := make([]WireNumber, len(xs))
	for i, x := range xs {
		w[i] = WireNumberOf(x)
	}
	out, err := json.Marshal(w)
	if err != nil {
		panic(fmt.Sprintf("EncodeTuple: %v", err))
	}
	return string(out)
}

// ChainOpKind is the tag of a ChainOp — the discriminated union's Op
// field values, kept as their own type per the port's convention for a
// union with a small interface only when payloads genuinely differ; here
// the three shapes ("set" op, starOf/emptyQ, memberQ) are pure data, so
// one struct with the fields every kind might use, like Refinement.
type ChainOpKind string

const (
	ChainOpIntersectWith  ChainOpKind = "intersectWith"
	ChainOpUnionWith      ChainOpKind = "unionWith"
	ChainOpDifferenceWith ChainOpKind = "differenceWith"
	ChainOpConcatWith     ChainOpKind = "concatWith"
	ChainOpConcatUnder    ChainOpKind = "concatUnder"
	ChainOpSubsetQ        ChainOpKind = "subsetQ"
	ChainOpDisjointQ      ChainOpKind = "disjointQ"
	ChainOpStarOf         ChainOpKind = "starOf"
	ChainOpEmptyQ         ChainOpKind = "emptyQ"
	ChainOpMemberQ        ChainOpKind = "memberQ"
)

// ChainOp is one derivation-chain step: a set-returning application, or
// a question terminator (SPEC Task 2 — the certifying seam).
type ChainOp struct {
	Op    ChainOpKind
	Set   refinementsets.RefinedSet // meaningful when Op names a "set" variant
	Tuple []float64                 // meaningful when Op is ChainOpMemberQ
}

// Chain is the TS Chain interface.
type Chain struct {
	Root refinementsets.RefinedSet
	Ops  []ChainOp
}

func chainOpCarriesSet(op ChainOpKind) bool {
	switch op {
	case ChainOpIntersectWith, ChainOpUnionWith, ChainOpDifferenceWith,
		ChainOpConcatWith, ChainOpConcatUnder, ChainOpSubsetQ, ChainOpDisjointQ:
		return true
	default:
		return false
	}
}

// EncodeChain is encodeChain in the TS source.
func EncodeChain(c Chain) string {
	ops := make([]map[string]any, len(c.Ops))
	for i, op := range c.Ops {
		if chainOpCarriesSet(op.Op) {
			ops[i] = map[string]any{"op": string(op.Op), "set": wireSet(op.Set)}
		} else if op.Op == ChainOpMemberQ {
			w := make([]WireNumber, len(op.Tuple))
			for j, x := range op.Tuple {
				w[j] = WireNumberOf(x)
			}
			ops[i] = map[string]any{"op": string(op.Op), "tuple": w}
		} else {
			ops[i] = map[string]any{"op": string(op.Op)}
		}
	}
	out, err := json.Marshal(map[string]any{
		"root": wireSet(c.Root),
		"ops":  ops,
	})
	if err != nil {
		panic(fmt.Sprintf("EncodeChain: %v", err))
	}
	return string(out)
}

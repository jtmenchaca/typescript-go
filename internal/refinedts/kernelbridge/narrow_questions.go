// Narrowing questions: a condition tree on one place, the two
// claims it proves, and the flat knowledge-state the walk joins
// and filters. Linear ledger rows ride here with the state wire.
package kernelbridge

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// LinearFact is one linear ledger row for the kernel's decider:
// integer coefficients by variable position, a bound, the strict flag.
type LinearFact struct {
	Coefs  []float64
	Bound  float64
	Strict bool
}

// NarrowTreeKind is the tag of a NarrowTree.
type NarrowTreeKind string

const (
	NarrowKindCmp      NarrowTreeKind = "cmp"
	NarrowKindCmpSet   NarrowTreeKind = "cmpSet"
	NarrowKindEq       NarrowTreeKind = "eq"
	NarrowKindModZero  NarrowTreeKind = "modZero"
	NarrowKindIsInt    NarrowTreeKind = "isInt"
	NarrowKindIsFinite NarrowTreeKind = "isFinite"
	NarrowKindIsNaN    NarrowTreeKind = "isNaN"
	NarrowKindEqSeq    NarrowTreeKind = "eqSeq"
	NarrowKindInSet    NarrowTreeKind = "inSet"
	NarrowKindOther    NarrowTreeKind = "other"
	NarrowKindNot      NarrowTreeKind = "not"
	NarrowKindAnd      NarrowTreeKind = "and"
	NarrowKindOr       NarrowTreeKind = "or"
)

// NarrowCmpOp is the op field of a "cmp" / "cmpSet" NarrowTree.
type NarrowCmpOp string

const (
	NarrowOpGe NarrowCmpOp = "ge"
	NarrowOpGt NarrowCmpOp = "gt"
	NarrowOpLe NarrowCmpOp = "le"
	NarrowOpLt NarrowCmpOp = "lt"
)

// NarrowTree is a condition on ONE place, lowered for the kernel's
// narrowing question: recognized tests at the leaves, `other` for
// every test the reading could not vouch for, and the guard's own !,
// &&, || structure. The SORT obligation stays with the reader: numeric
// leaves are lowered only where tsc types the place a number, the
// string equality only where it types a string. Collapsed to one
// struct with a Kind tag per the port's convention.
type NarrowTree struct {
	Kind NarrowTreeKind

	Op NarrowCmpOp // "cmp" / "cmpSet"
	K  float64     // "cmp" / "eq"
	Lo float64     // "cmpSet"
	Hi float64     // "cmpSet"
	D  float64     // "modZero"

	Points []float64 // "eqSeq"

	// Set: membership in a stated set — a string place tested against a
	// compiled pattern (`/re/.test(x)`, `x.includes("s")`). "inSet".
	Set refinementsets.RefinedSet

	// A/B: "not" (A only), "and" / "or" (A and B).
	A *NarrowTree
	B *NarrowTree
}

// NarrowClaim is one side's claim: the set the place is admitted into,
// and whether the claim is STRONG (the side's holding itself proves
// the value real — inside ℝ̄, no NaN) or WEAK (it holds only for values
// already known real).
type NarrowClaim struct {
	Set    refinementsets.RefinedSet
	Strong bool
}

// NarrowAnswer is the TS NarrowAnswer interface. A nil claim is the TS
// `null` — no claim on that side.
type NarrowAnswer struct {
	WhenTrue  *NarrowClaim
	WhenFalse *NarrowClaim
}

// KnownStateWire is a knowledge state on the wire — the kernel's flat
// normal form of the checker's scalar knowledge: no knowledge at all
// (Top), or a refined set with an or-absent flag and an or-NaN flag.
type KnownStateWire struct {
	Top bool

	Set    refinementsets.RefinedSet
	Absent bool
	Nan    bool
}

// StateWire is stateWire in the TS source.
func StateWire(s KnownStateWire) string {
	if s.Top {
		return `{"top":true}`
	}
	return fmt.Sprintf(`{"set":%s,"absent":%v,"nan":%v}`, EncodeSet(s.Set), s.Absent, s.Nan)
}

// DecodeWireState is decodeWireState in the TS source. Panics when raw
// is absent or malformed, mirroring the TS `throw`.
func DecodeWireState(raw any) KnownStateWire {
	if raw == nil {
		panic("kernel answered no knowledge state at all")
	}
	o, ok := raw.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("kernel answered an unexpected knowledge state: %v", raw))
	}
	if top, ok := o["top"].(bool); ok && top {
		return KnownStateWire{Top: true}
	}
	set, setHeld := o["set"]
	absent, absentOK := o["absent"].(bool)
	nan, nanOK := o["nan"].(bool)
	if setHeld && set != nil && absentOK && nanOK {
		return KnownStateWire{Set: DecodeWireSet(set), Absent: absent, Nan: nan}
	}
	panic(fmt.Sprintf("kernel answered an unexpected knowledge state: %v", raw))
}

// NarrowWire is narrowWire in the TS source.
func NarrowWire(t NarrowTree) string {
	switch t.Kind {
	case NarrowKindCmp:
		return fmt.Sprintf(`{"test":"%s","k":%s}`, t.Op, marshalWireValue(WireNumberOf(t.K)))
	case NarrowKindCmpSet:
		return fmt.Sprintf(
			`{"test":"%s","lo":%s,"hi":%s}`,
			t.Op, marshalWireValue(WireNumberOf(t.Lo)), marshalWireValue(WireNumberOf(t.Hi)),
		)
	case NarrowKindEq:
		return fmt.Sprintf(`{"test":"eq","k":%s}`, marshalWireValue(WireNumberOf(t.K)))
	case NarrowKindModZero:
		return fmt.Sprintf(`{"test":"modZero","d":%s}`, marshalWireValue(WireNumberOf(t.D)))
	case NarrowKindIsInt, NarrowKindIsFinite, NarrowKindIsNaN, NarrowKindOther:
		return fmt.Sprintf(`{"test":"%s"}`, t.Kind)
	case NarrowKindEqSeq:
		return fmt.Sprintf(`{"test":"eqSeq","t":%s}`, EncodeTuple(t.Points))
	case NarrowKindInSet:
		return fmt.Sprintf(`{"test":"inSet","set":%s}`, EncodeSet(t.Set))
	case NarrowKindNot:
		return fmt.Sprintf(`{"not":%s}`, NarrowWire(*t.A))
	case NarrowKindAnd:
		return fmt.Sprintf(`{"and":[%s,%s]}`, NarrowWire(*t.A), NarrowWire(*t.B))
	case NarrowKindOr:
		return fmt.Sprintf(`{"or":[%s,%s]}`, NarrowWire(*t.A), NarrowWire(*t.B))
	}
	panic(fmt.Sprintf("NarrowWire: unreached kind %q", t.Kind))
}

// GateNarrow is gateNarrow in the TS source: fail fast on a NaN
// endpoint with a readable message — hygiene only, the dyadic encoder
// refuses NaN itself. Nothing here is load-bearing: the kernel's
// answer is proved sound for every decodable tree
// (transfers/narrow_correct.lean covers every divisor of the remainder
// test, zero and negative included).
func GateNarrow(t NarrowTree) {
	switch t.Kind {
	case NarrowKindCmp, NarrowKindEq:
		if isNaN(t.K) {
			panic("a narrowing endpoint is NaN")
		}
	case NarrowKindCmpSet:
		if isNaN(t.Lo) || isNaN(t.Hi) {
			panic("a narrowing endpoint is NaN")
		}
	case NarrowKindNot:
		GateNarrow(*t.A)
	case NarrowKindAnd, NarrowKindOr:
		GateNarrow(*t.A)
		GateNarrow(*t.B)
	}
}

func isNaN(x float64) bool { return x != x }

// DecodeNarrowAnswer is decodeNarrowAnswer in the TS source.
func DecodeNarrowAnswer(parsed map[string]any) NarrowAnswer {
	side := func(raw any) *NarrowClaim {
		o, ok := raw.(map[string]any)
		if !ok {
			panic(fmt.Sprintf("kernel narrow answered an unexpected claim: %v", raw))
		}
		if none, ok := o["none"].(bool); ok && none {
			return nil
		}
		strong, strongOK := o["strong"].(bool)
		set, setHeld := o["set"]
		if strongOK && setHeld && set != nil {
			return &NarrowClaim{Set: DecodeWireSet(set), Strong: strong}
		}
		panic(fmt.Sprintf("kernel narrow answered an unexpected claim: %v", raw))
	}
	whenTrue, trueHeld := parsed["whenTrue"]
	whenFalse, falseHeld := parsed["whenFalse"]
	if !trueHeld || !falseHeld {
		panic(fmt.Sprintf("kernel narrow answered an unexpected shape: %v", parsed))
	}
	return NarrowAnswer{WhenTrue: side(whenTrue), WhenFalse: side(whenFalse)}
}

// LinearWireInput mirrors the destructured `{ facts, target }` parameter
// of linearWire in the TS source.
type LinearWireInput struct {
	Facts  []LinearFact
	Target LinearFact
}

func linearFactWire(f LinearFact) string {
	d, err := primitives.DyadicOfNumber(f.Bound)
	if err != nil {
		panic(fmt.Sprintf("linearFactWire: %v", err))
	}
	coefs := make([]string, len(f.Coefs))
	for i, c := range f.Coefs {
		coefs[i] = trimFloat(c)
	}
	strictSuffix := "}"
	if f.Strict {
		strictSuffix = `,"strict":true}`
	}
	return fmt.Sprintf(
		`{"coefs":[%s],"bound":{"num":%d,"exp":%d}%s`,
		joinComma(coefs), d.Num, d.Exp, strictSuffix,
	)
}

// trimFloat mirrors `f.coefs.join(",")` — coefficients are integers by
// contract (documented on LinearFact); formatted without a trailing
// ".0" the way a JS number would join.
func trimFloat(x float64) string {
	if x == float64(int64(x)) {
		return fmt.Sprintf("%d", int64(x))
	}
	return fmt.Sprintf("%g", x)
}

// LinearWire is linearWire in the TS source.
func LinearWire(input LinearWireInput) string {
	facts := make([]string, len(input.Facts))
	for i, f := range input.Facts {
		facts[i] = linearFactWire(f)
	}
	return fmt.Sprintf(
		`{"facts":[%s],"target":%s}`, joinComma(facts), linearFactWire(input.Target),
	)
}

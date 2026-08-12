// Transfer questions: the float image of a JavaScript operation,
// posed as operand sets (or pow's pinned NaN / unknown / set) and
// read back as nan, unknown, exact values, or a certified set.
package kernelbridge

import (
	"encoding/json"
	"fmt"

	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// marshalWireValue is a shared helper used by every question file that
// hand-builds a JSON literal for one map value — no TS twin (it stands
// in for `JSON.stringify` at those call sites).
func marshalWireValue(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshalWireValue: %v", err))
	}
	return string(out)
}

// PowOperandKind is the tag of a PowOperandWire.
type PowOperandKind string

const (
	PowOperandNaN     PowOperandKind = "nan"
	PowOperandUnknown PowOperandKind = "unknown"
	PowOperandSet     PowOperandKind = "set"
)

// PowOperandWire is a pow operand as the wire states it: the pinned
// NaN, an unreadable value (anything — NaN included), or a set.
type PowOperandWire struct {
	Kind PowOperandKind
	Set  refinementsets.RefinedSet // meaningful when Kind is PowOperandSet
}

// TransferQuestionOp is the tag of a TransferQuestion.
type TransferQuestionOp string

const (
	TransferOpSubOrdGap    TransferQuestionOp = "subOrdGap"
	TransferOpAdd          TransferQuestionOp = "add"
	TransferOpSub          TransferQuestionOp = "sub"
	TransferOpSubOrd       TransferQuestionOp = "subOrd"
	TransferOpMul          TransferQuestionOp = "mul"
	TransferOpDiv          TransferQuestionOp = "div"
	TransferOpRem          TransferQuestionOp = "rem"
	TransferOpMin          TransferQuestionOp = "min"
	TransferOpMax          TransferQuestionOp = "max"
	TransferOpBitOr        TransferQuestionOp = "bitOr"
	TransferOpBitAnd       TransferQuestionOp = "bitAnd"
	TransferOpBitXor       TransferQuestionOp = "bitXor"
	TransferOpShl          TransferQuestionOp = "shl"
	TransferOpSar          TransferQuestionOp = "sar"
	TransferOpShr          TransferQuestionOp = "shr"
	TransferOpCountProduct TransferQuestionOp = "countProduct"
	TransferOpHypot        TransferQuestionOp = "hypot"
	TransferOpAtan2        TransferQuestionOp = "atan2"
	TransferOpNeg          TransferQuestionOp = "neg"
	TransferOpFloor        TransferQuestionOp = "floor"
	TransferOpCeil         TransferQuestionOp = "ceil"
	TransferOpRound        TransferQuestionOp = "round"
	TransferOpTrunc        TransferQuestionOp = "trunc"
	TransferOpAbs          TransferQuestionOp = "abs"
	TransferOpExp          TransferQuestionOp = "exp"
	TransferOpSqrt         TransferQuestionOp = "sqrt"
	TransferOpLog          TransferQuestionOp = "log"
	TransferOpLog2         TransferQuestionOp = "log2"
	TransferOpLog10        TransferQuestionOp = "log10"
	TransferOpExpm1        TransferQuestionOp = "expm1"
	TransferOpLog1p        TransferQuestionOp = "log1p"
	TransferOpCbrt         TransferQuestionOp = "cbrt"
	TransferOpSin          TransferQuestionOp = "sin"
	TransferOpCos          TransferQuestionOp = "cos"
	TransferOpTan          TransferQuestionOp = "tan"
	TransferOpSinh         TransferQuestionOp = "sinh"
	TransferOpCosh         TransferQuestionOp = "cosh"
	TransferOpTanh         TransferQuestionOp = "tanh"
	TransferOpAtan         TransferQuestionOp = "atan"
	TransferOpAsin         TransferQuestionOp = "asin"
	TransferOpAtanh        TransferQuestionOp = "atanh"
	TransferOpAsinh        TransferQuestionOp = "asinh"
	TransferOpAcosh        TransferQuestionOp = "acosh"
	TransferOpToInt32      TransferQuestionOp = "toInt32"
	TransferOpPow          TransferQuestionOp = "pow"
)

func transferOpIsUnary(op TransferQuestionOp) bool {
	switch op {
	case TransferOpNeg, TransferOpFloor, TransferOpCeil, TransferOpRound,
		TransferOpTrunc, TransferOpAbs, TransferOpExp, TransferOpSqrt,
		TransferOpLog, TransferOpLog2, TransferOpLog10, TransferOpExpm1,
		TransferOpLog1p, TransferOpCbrt, TransferOpSin, TransferOpCos,
		TransferOpTan, TransferOpSinh, TransferOpCosh, TransferOpTanh,
		TransferOpAtan, TransferOpAsin, TransferOpAtanh, TransferOpAsinh,
		TransferOpAcosh, TransferOpToInt32:
		return true
	default:
		return false
	}
}

// TransferQuestion is the TS TransferQuestion discriminated union,
// collapsed to one struct with an Op tag: A/B for the binary and
// unary ops (B unused for unary), C for subOrdGap, Base/Exp for pow.
type TransferQuestion struct {
	Op TransferQuestionOp

	A refinementsets.RefinedSet
	B refinementsets.RefinedSet
	C float64 // subOrdGap only; must be finite

	Base PowOperandWire
	Exp  PowOperandWire
}

// TransferAnswerKind is the tag of a TransferAnswer.
type TransferAnswerKind string

const (
	TransferAnswerNaN     TransferAnswerKind = "nan"
	TransferAnswerUnknown TransferAnswerKind = "unknown"
	TransferAnswerValues  TransferAnswerKind = "values"
	TransferAnswerSet     TransferAnswerKind = "set"
)

// TransferAnswer is the TS TransferAnswer discriminated union.
type TransferAnswer struct {
	Kind   TransferAnswerKind
	Values []float64
	Set    refinementsets.RefinedSet
}

func powOperandWire(o PowOperandWire) map[string]any {
	if o.Kind == PowOperandSet {
		return map[string]any{"kind": "set", "set": wireSet(o.Set)}
	}
	return map[string]any{"kind": string(o.Kind)}
}

// TransferWire is transferWire in the TS source.
func TransferWire(question TransferQuestion) string {
	if question.Op == TransferOpSubOrdGap {
		c, err := primitives.DyadicOfNumber(question.C)
		if err != nil {
			panic(fmt.Sprintf("TransferWire: subOrdGap: %v", err))
		}
		return fmt.Sprintf(
			`{"op":"subOrdGap","A":%s,"B":%s,"c":{"num":%d,"exp":%d}}`,
			EncodeSet(question.A), EncodeSet(question.B), c.Num, c.Exp,
		)
	}
	if question.Op == TransferOpPow {
		base := marshalWireValue(powOperandWire(question.Base))
		exp := marshalWireValue(powOperandWire(question.Exp))
		return fmt.Sprintf(`{"op":"pow","base":%s,"exp":%s}`, base, exp)
	}
	if !transferOpIsUnary(question.Op) {
		return fmt.Sprintf(
			`{"op":"%s","A":%s,"B":%s}`,
			question.Op, EncodeSet(question.A), EncodeSet(question.B),
		)
	}
	return fmt.Sprintf(`{"op":"%s","A":%s}`, question.Op, EncodeSet(question.A))
}

// DecodeTransferAnswer is decodeTransferAnswer in the TS source.
func DecodeTransferAnswer(parsed map[string]any) TransferAnswer {
	kind, _ := parsed["kind"].(string)
	switch kind {
	case "nan":
		return TransferAnswer{Kind: TransferAnswerNaN}
	case "unknown":
		return TransferAnswer{Kind: TransferAnswerUnknown}
	case "values":
		rawValues, ok := parsed["values"].([]any)
		if !ok {
			panic(fmt.Sprintf("kernel transfer answered an unexpected kind: %v", parsed))
		}
		values := make([]float64, len(rawValues))
		for i, v := range rawValues {
			values[i] = DecodeWireNumber(v)
		}
		return TransferAnswer{Kind: TransferAnswerValues, Values: values}
	case "set":
		return TransferAnswer{Kind: TransferAnswerSet, Set: DecodeWireSet(parsed["set"])}
	default:
		panic(fmt.Sprintf("kernel transfer answered an unexpected kind: %v", parsed))
	}
}

// `%TypedArray%.prototype.subarray(start, end)` — the VIEW a typed
// array hands out over its own storage.
//
// sec-%typedarray%.prototype.subarray (specifications/javascript/spec.html)
// returns "a new TypedArray whose element type is the element type of
// this TypedArray and whose ArrayBuffer is the ArrayBuffer of this
// TypedArray, referencing the elements in the interval from start
// (inclusive) to end (exclusive)". The algorithm clamps each bound
// through ToClampedIndex against the source length (steps for
// _startIndex_ and _endIndex_), takes newLength as
// max(endIndex − startIndex, 0), and creates the view over the SAME
// buffer at the computed byte offset.
//
// WHAT THE WALK CAN ANSWER. A typed array this walk built holds its
// elements as an exact value tuple (KnownValues over PrimitiveArray,
// typed_array_models.go's own representation — a typed array of exact
// elements is the same claim shape a plain number array wears). At the
// moment of the subarray call, the buffer's contents ARE that tuple, so
// the view's own elements are exactly the window the clamped bounds
// name. That is what this reader answers.
//
// The shared buffer is what makes this a VIEW rather than a copy, and
// it is also the reason the answer is a snapshot rather than a standing
// alias: a later write through EITHER the owner or the view is visible
// through the other. The walk's own write path already handles that
// correctly for the shape this reader returns — an element write to a
// tracked name retires the whole receiver (dataflowfacts's
// markWriteTarget, the rule element_access.go's KindList note states),
// so a view read AFTER such a write does not answer this stale window.
// A write through the OTHER name is the case a standing alias would be
// needed for; this reader does not claim it, and a program that writes
// through the owner after taking the view reads the residue rather than
// this snapshot.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// readTypedArraySubarray answers `owner.subarray(start[, end])` where
// the receiver is a typed array the walk holds exactly. nil where the
// method is not subarray, the receiver's static type is not one of the
// modeled typed-array families, the receiver's own value is not an
// exact tuple, or a bound is not one exact integer — every one of those
// leaves the call to the readers after it.
func readTypedArraySubarray(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, e, receiverExpression, receiver, method := site.Ctx, site.E, site.ReceiverExpression, site.Receiver, site.Method
	if method != "subarray" {
		return nil
	}
	// the receiver must be a TYPED array, not a plain one: Array.prototype
	// has no subarray, so a plain-array receiver spelling it is not this
	// call at all
	if _, ok := TypedArrayWriteConversion(ctx, receiverExpression); !ok {
		return nil
	}
	if receiver.Kind != abstractdomain.KindValues || receiver.KindTag != abstractdomain.PrimitiveArray {
		return nil
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	if len(arguments) > 2 {
		return nil
	}
	sourceLength := len(receiver.Values)
	bounds := make([]float64, 0, len(arguments))
	for _, argument := range arguments {
		known := evaluateExpression(ctx, site.Env, argument)
		if known.Kind != abstractdomain.KindValues || known.KindTag != abstractdomain.PrimitiveNumber ||
			len(known.Values) != 1 || !isInteger(known.Values[0]) {
			return nil
		}
		bounds = append(bounds, known.Values[0])
	}
	// ToClampedIndex against the source length: a negative bound counts
	// from the end and clamps at 0, a bound past the end clamps at the
	// length (the same reading sliceFloat64 already runs for
	// Array.prototype.slice's own ToClampedIndex steps)
	startIndex := 0
	if len(bounds) >= 1 {
		startIndex = clampedTypedArrayIndex(bounds[0], sourceLength)
	}
	endIndex := sourceLength
	if len(bounds) >= 2 {
		endIndex = clampedTypedArrayIndex(bounds[1], sourceLength)
	}
	// newLength is max(endIndex − startIndex, 0)
	if endIndex < startIndex {
		endIndex = startIndex
	}
	window := make([]float64, endIndex-startIndex)
	copy(window, receiver.Values[startIndex:endIndex])
	// the view's element type is the SOURCE's element type (the clause's
	// own first sentence), and every value in the window already went
	// through that type's conversion when it was stored — so no
	// conversion runs again here.
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(receiver))
	out := abstractdomain.KnownValues(window, abstractdomain.PrimitiveArray, grade)
	return &out
}

// clampedTypedArrayIndex is ToClampedIndex against a source length: a
// negative index counts from the end and floors at 0, a positive one
// ceilings at the length.
func clampedTypedArrayIndex(index float64, length int) int {
	if index < 0 {
		at := length + int(index)
		if at < 0 {
			return 0
		}
		return at
	}
	if int(index) > length {
		return length
	}
	return int(index)
}

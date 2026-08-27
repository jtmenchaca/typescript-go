// `new ArrayBuffer(n)`, `new DataView(buffer)`, and the DataView
// get/set methods that read and write fixed-width integers at an
// explicit byte offset and endianness.
//
// THE ONE SHAPE. A buffer IS its bytes, and a DataView over the whole
// of one is those same bytes read a different way. Both are held as
// KnownValues(bytes, PrimitiveArray, grade) — the exact tuple shape a
// typed array already wears (typed_array_models.go's own banner: "a
// typed array of exact-value elements is the SAME claim shape a plain
// number array already wears"), one element per BYTE, each in
// [0, 255]. Nothing new enters the domain: the byte store is a tuple,
// and the get/set methods are transfers over it.
//
// Construction, per the clauses in specifications/javascript/spec.html:
//
//   - `new ArrayBuffer(n)` — ArrayBuffer(length[, options])
//     (#sec-arraybuffer-length) takes byteLength through ToIndex and
//     calls AllocateArrayBuffer (#sec-allocatearraybuffer), whose
//     CreateByteDataBlock step allocates a Data Block of byteLength
//     bytes with every byte initialized to 0. So the buffer's own
//     value is a tuple of n zeroes. An `options` argument (a resizable
//     buffer) is not modeled: a resize changes the tuple's length
//     later, and this reader states a fixed one.
//
//   - `new DataView(buffer)` — DataView(buffer[, byteOffset[,
//     byteLength]]) (#sec-dataview-buffer-byteoffset-bytelength) sets
//     [[ByteOffset]] to the offset argument and [[ByteLength]] to the
//     length argument, both defaulting to the whole buffer. Only the
//     WHOLE-buffer spelling is read here — one argument, offset 0 —
//     because the view's own value is then exactly the buffer's own
//     bytes and needs no offset to carry alongside it.
//
// Reading and writing, both routed through the two shared AOs:
//
//   - `view.setUint8(i, v)` — SetViewValue(view, i, true, ~uint8~, v)
//     (#sec-dataview.prototype.setuint8; the AO at #sec-setviewvalue).
//     SetViewValue ToNumbers the value, checks getIndex + elementSize
//     against the view size (a RangeError past the end), and calls
//     SetValueInBuffer, which runs NumericToRawBytes — the same AO
//     whose "Conversion Operation" step typed_array_models.go's
//     toUint8 already mirrors for ~uint8~. So the stored byte is
//     toUint8(v), landing at index i of the tuple.
//
//   - `view.getUint32(i, littleEndian)` — GetViewValue(view, i,
//     littleEndian, ~uint32~) (#sec-dataview.prototype.getuint32; the
//     AO at #sec-getviewvalue), which reads elementSize bytes from the
//     buffer and composes them through RawBytesToNumeric
//     (#sec-rawbytestonumeric): "If isLittleEndian is false, reverse
//     the order of the elements of rawBytes", then for an unsigned
//     element type "the byte elements of rawBytes concatenated and
//     interpreted as a bit string encoding of an unsigned
//     little-endian binary number". That composition is what
//     composeUnsignedBytes below runs.
//
// THE VIEW IS A SNAPSHOT, NOT A STANDING ALIAS — exactly the reading
// typed_array_view_models.go states for `subarray`. `new
// DataView(buffer)` copies the buffer's bytes as they stand at the
// construction, and a later write through the BUFFER's own name is not
// visible through the view here. What makes that sound rather than a
// gap is the same rule that file cites: a write to a tracked name
// retires the whole receiver, so the program that writes through the
// other name reads the residue rather than a stale tuple.
//
// WHAT IS NOT MODELED. Every method whose element type is wider than
// one byte on the WRITE side (setUint16/setUint32/setFloat64 and the
// signed and float families), and every float or signed READ, needs
// the reverse direction of NumericToRawBytes — the encode step, which
// splits one Number into elementSize bytes (and for the float types
// runs the IEEE binary32/binary64 encoding). This file states only
// the byte-at-a-time write and the unsigned integer read, so the
// other spellings answer nothing here and fall to the unmodeled tail
// rather than being guessed at.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// unsignedViewReads is the DataView READ method table: the method name
// to the byte width its element type reads. One row per width — the
// table other widths extend by adding a row, never by branching, the
// same shape TypedArrayConversions wears.
//
// Only the UNSIGNED integer types are here: RawBytesToNumeric composes
// an unsigned type's bytes as a plain unsigned little-endian binary
// number, which composeUnsignedBytes states exactly. A signed type
// takes the two's-complement branch of that same clause, and a float
// type takes the IEEE-decode branch; neither is stated in this file, so
// neither has a row.
var unsignedViewReads = map[string]int{
	"getUint8":  1,
	"getUint16": 2,
	"getUint32": 4,
}

// arrayBufferByteLengthLimit caps the tuple a `new ArrayBuffer(n)`
// materializes. A buffer is an EXACT per-byte tuple, so an n in the
// megabytes would build a tuple with that many entries for no answer a
// program reads back — the same materialization ceiling
// array_construction.go states for `new Array(n)`. Past it this reader
// says nothing and the unmodeled tail answers.
const arrayBufferByteLengthLimit = 4096

// ReadArrayBufferConstruction answers what `new ArrayBuffer(n)` builds
// — a tuple of n zero bytes — or nil where the callee is not the
// default lib's ArrayBuffer, the length is not one exact non-negative
// integer, an options argument is present, or the length is past the
// materialization ceiling.
func ReadArrayBufferConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if !isDefaultLibConstructor(ctx, e, "ArrayBuffer") {
		return nil
	}
	args, hasArgs := callArguments(e)
	if !hasArgs || len(args) != 1 || ast.IsSpreadElement(args[0]) {
		return nil
	}
	known := evaluateExpression(ctx, env, args[0])
	if known.Kind != abstractdomain.KindValues || known.KindTag != abstractdomain.PrimitiveNumber ||
		len(known.Values) != 1 {
		return nil
	}
	byteLength := known.Values[0]
	if !isNonNegativeInteger(byteLength) || byteLength > arrayBufferByteLengthLimit {
		return nil
	}
	// CreateByteDataBlock initializes every byte to 0 — Go's own zero
	// value for the slice is that same tuple
	bytes := make([]float64, int(byteLength))
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(known))
	out := abstractdomain.KnownValues(bytes, abstractdomain.PrimitiveArray, grade)
	return &out
}

// ReadDataViewConstruction answers what `new DataView(buffer)` builds
// over a buffer the walk holds exactly — the same byte tuple — or nil
// for any other spelling (a byteOffset/byteLength triple, a buffer the
// walk does not hold as an exact tuple).
func ReadDataViewConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if !isDefaultLibConstructor(ctx, e, "DataView") {
		return nil
	}
	args, hasArgs := callArguments(e)
	if !hasArgs || len(args) != 1 || ast.IsSpreadElement(args[0]) {
		return nil
	}
	buffer := evaluateExpression(ctx, env, args[0])
	if buffer.Kind != abstractdomain.KindValues || buffer.KindTag != abstractdomain.PrimitiveArray {
		return nil
	}
	bytes := make([]float64, len(buffer.Values))
	copy(bytes, buffer.Values)
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(buffer))
	out := abstractdomain.KnownValues(bytes, abstractdomain.PrimitiveArray, grade)
	return &out
}

// readDataViewMethods answers `view.setUint8(i, v)` (writing the byte
// back through the tracked name, the call itself answering undefined
// per SetViewValue's own "Return undefined") and the unsigned integer
// reads. nil for every other method, receiver shape, or argument the
// rows below do not state.
func readDataViewMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, method := site.Ctx, site.Env, site.E, site.Method
	if method != "setUint8" {
		if _, isRead := unsignedViewReads[method]; !isRead {
			return nil
		}
	}
	// the receiver must be a DATA VIEW: a plain array holds the same
	// tuple shape, and DataView.prototype's methods are not on it, so
	// the static type is what tells the two apart — the same question
	// TypedArrayWriteConversion asks for a typed array's write
	if !isDataViewReceiver(ctx, site.ReceiverExpression) {
		return nil
	}
	receiver := site.Receiver
	if receiver.Kind != abstractdomain.KindValues || receiver.KindTag != abstractdomain.PrimitiveArray {
		return nil
	}
	args, hasArgs := callArguments(e)
	if !hasArgs {
		return nil
	}
	// the byte offset, exact and non-negative: an offset the walk has
	// not pinned names no byte to read or write
	if len(args) == 0 || ast.IsSpreadElement(args[0]) {
		return nil
	}
	offsetKnown := evaluateExpression(ctx, env, args[0])
	if offsetKnown.Kind != abstractdomain.KindValues || offsetKnown.KindTag != abstractdomain.PrimitiveNumber ||
		len(offsetKnown.Values) != 1 || !isNonNegativeInteger(offsetKnown.Values[0]) {
		return nil
	}
	offset := int(offsetKnown.Values[0])
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(receiver))
	grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(offsetKnown))

	if method == "setUint8" {
		if len(args) != 2 || ast.IsSpreadElement(args[1]) {
			return nil
		}
		// getIndex + elementSize > viewSize throws a RangeError
		// (SetViewValue step 12): no byte is stored, so this row does
		// not claim a resulting buffer
		if offset+1 > len(receiver.Values) {
			return nil
		}
		if !site.HasTrackedName {
			// the store lands in the view's own buffer, and with no
			// tracked name there is nowhere to put it — a later read
			// off this same expression would answer the pre-write
			// bytes, which is exactly the overclaim this declines
			return nil
		}
		valueKnown := evaluateExpression(ctx, env, args[1])
		if valueKnown.Kind != abstractdomain.KindValues || valueKnown.KindTag != abstractdomain.PrimitiveNumber ||
			len(valueKnown.Values) != 1 || !isInteger(valueKnown.Values[0]) {
			return nil
		}
		bytes := make([]float64, len(receiver.Values))
		copy(bytes, receiver.Values)
		// NumericToRawBytes runs ~uint8~'s own Conversion Operation,
		// ToUint8 — the row typed_array_models.go already states
		bytes[offset] = toUint8(valueKnown.Values[0])
		grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(valueKnown))
		UpdateTrackedEnv(ctx.Aliases, env, site.TrackedName,
			abstractdomain.KnownValues(bytes, abstractdomain.PrimitiveArray, grade))
		// SetViewValue's last step: "Return undefined"
		out := abstractdomain.Undef
		return &out
	}

	width := unsignedViewReads[method]
	// GetViewValue step 11: getIndex + elementSize > viewSize throws a
	// RangeError, so no value is read
	if offset+width > len(receiver.Values) {
		return nil
	}
	// littleEndian defaults to *false* when the argument is absent
	// (each get method's own step 2); an argument present must be one
	// exact boolean for the byte order to be pinned
	littleEndian := false
	if len(args) >= 2 {
		if len(args) > 2 || ast.IsSpreadElement(args[1]) {
			return nil
		}
		endianKnown := evaluateExpression(ctx, env, args[1])
		truth, known := abstractdomain.Truthiness(endianKnown)
		if !known {
			return nil
		}
		littleEndian = truth
		grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(endianKnown))
	}
	window := receiver.Values[offset : offset+width]
	// every byte read must itself be exact — a buffer this walk built
	// holds exact bytes by construction, and a byte that is not one is
	// a value this composition cannot state
	for _, b := range window {
		if !isInteger(b) || b < 0 || b > 255 {
			return nil
		}
	}
	value := composeUnsignedBytes(window, littleEndian)
	out := abstractdomain.KnownValues([]float64{value}, abstractdomain.PrimitiveNumber, grade)
	return &out
}

// composeUnsignedBytes is RawBytesToNumeric's unsigned-integer path
// (#sec-rawbytestonumeric): with isLittleEndian false the byte list is
// reversed first, and the result is the bytes "concatenated and
// interpreted as a bit string encoding of an unsigned little-endian
// binary number" — byte k contributing byte[k] × 256^k.
//
// Exact in float64 for every width this file's table names: the widest
// is 4 bytes, whose largest value 2^32 − 1 is far inside the 2^53
// integers a float64 represents without loss.
func composeUnsignedBytes(bytes []float64, littleEndian bool) float64 {
	ordered := make([]float64, len(bytes))
	if littleEndian {
		copy(ordered, bytes)
	} else {
		for i, b := range bytes {
			ordered[len(bytes)-1-i] = b
		}
	}
	value := 0.0
	place := 1.0
	for _, b := range ordered {
		value += b * place
		place *= 256
	}
	return value
}

// isDataViewReceiver reads, off the CHECKER's own static type, whether
// a receiver expression is a DataView — the same question
// TypedArrayWriteConversion asks by symbol name for a typed array,
// asked here because a DataView's tracked value is a bare byte tuple
// carrying no tag of its own that says which view built it.
func isDataViewReceiver(ctx *FlowContext, receiverExpression *ast.Node) bool {
	receiverType := typereading.TypeAtLocation(ctx.P.Checker, receiverExpression)
	if receiverType == nil || receiverType.Symbol() == nil {
		return false
	}
	return receiverType.Symbol().Name == "DataView"
}

// isDefaultLibConstructor reports whether `new NAME(...)` spells the
// given default-lib constructor — the same two questions
// typedArrayConstructorName asks (an identifier callee, resolving to
// the default lib), for one named constructor rather than a table.
func isDefaultLibConstructor(ctx *FlowContext, e *ast.Node, name string) bool {
	callee := calleeOf(e)
	if callee == nil || !ast.IsIdentifier(callee) {
		return false
	}
	if callee.Text() != name {
		return false
	}
	return resolvesToDefaultLib(ctx, callee)
}

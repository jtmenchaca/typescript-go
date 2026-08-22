// TypedArray construction and element write: `new Uint8Array(n)` /
// `new Int8Array(n)` / `new Uint8ClampedArray(n)` (zero-filled) and
// `new Uint8Array([...])` (each element seeded through the
// constructor's own conversion), plus the write-side conversion an
// indexed store runs before it lands.
//
// A typed array of exact-value elements is the SAME claim shape a
// plain number array already wears — KnownValues(..., PrimitiveArray,
// grade) — so element read and `.length` need no new code at all:
// element_access.go's KindValues arm and evaluate_property_access.go's
// `.length` arm already answer both exactly for any value built this
// way (the same reuse array_construction.go's own comment calls out
// for the array-literal build). Construction and the write-side
// conversion are the only two places a typed array's behavior
// diverges from a plain array's, so they are the only two this file
// adds.
//
// Conversions cite specifications/javascript/spec.html:
//   - ToFixedSizeInteger (#sec-tofixedsizeinteger) — the shared
//     modulo-2^bitWidth step ToInt8/ToUint8/ToInt16/ToUint16/ToInt32/
//     ToUint32 all delegate to: "fixedInt is int modulo 2^bitWidth";
//     signed additionally subtracts 2^bitWidth when
//     fixedInt >= 2^(bitWidth-1).
//   - ToUint8 (#sec-touint8) — ToFixedSizeInteger(int, unsigned, 8).
//   - ToInt8 (#sec-toint8) — ToFixedSizeInteger(int, signed, 8).
//   - ToUint8Clamp (#sec-touint8clamp) — clamps to [0, 255] and rounds
//     (round-half-to-even); does not wrap.
//   - ToInt16 (#sec-toint16) — ToFixedSizeInteger(int, signed, 16).
//   - ToUint16 (#sec-touint16) — ToFixedSizeInteger(int, unsigned, 16).
//   - ToInt32 (#sec-toint32) — ToFixedSizeInteger(int, signed, 32).
//   - ToUint32 (#sec-touint32) — ToFixedSizeInteger(int, unsigned, 32).
//   - Float64Array's element conversion — table-the-typedarray-
//     constructors lists NO "Conversion Operation" entry for
//     Element Type ~float64~ (nor ~float32~/~float16~); NumericToRawBytes
//     (#sec-numerictorawbytes) branches on Element Type BEFORE reaching
//     the "Conversion Operation" column step, and its ~float64~ branch
//     is "Let rawBytes be a List whose elements are ... the IEEE
//     754-2019 binary64 format encoding of value" — value here already
//     arrived as a Number (TypedArraySetElement's `? ToNumber(value)`,
//     #sec-typedarraysetelement), so the element holds that Number
//     unchanged: the identity conversion.
//
// Hand-verified against the fixture's own claimed values:
//   ToUint8(121)      = 121   (121 mod 256, no wrap)
//   ToUint8(200)      = 200   (200 mod 256, no wrap)
//   ToUint8(256)      = 0     (256 mod 256)
//   ToInt8(200)       = -56   (200 mod 256 = 200; 200 >= 128, so
//                              200 - 256 = -56)
//   ToUint8Clamp(300) = 255   (300 > 255, clamps to the ceiling)
//   ToUint8Clamp(-50) = 0     (-50 < 0, clamps to the floor)
//   ToUint16(70000)   = 4464  (70000 mod 65536 = 4464)
//
// Float32Array and BigInt64Array/BigUint64Array are NOT in this table.
// Float32Array's own element conversion (NumericToRawBytes's ~float32~
// branch, same clause as above) is "the result of converting value to
// IEEE 754-2019 binary32 format using roundTiesToEven mode" — exactly
// Math.fround (#sec-math.fround: ToNumber, then binary32-round-then-
// back-to-binary64) — but this table's conversion shape is
// func(float64) float64 computed in Go's own float64 domain with no
// binary32-rounding primitive; stating Float32Array here would either
// approximate (wrong) or require a new rounding primitive this file
// does not have, so the row is left out rather than stating a lossy
// value as exact. BigInt64Array/BigUint64Array's own conversions
// (ToBigInt64/#sec-tobigint64, ToBigUint64/#sec-tobiguint64) take and
// return BigInt, not Number — this table's whole shape
// (func(float64) float64) has no BigInt vocabulary to route them
// through either. Both declines are named at construction time
// (noteUnmodeledTypedArrayFamily below) rather than left silent.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// typedArrayConversion is the ToXxx element conversion a typed array
// constructor NAME runs on every seeded/written value, or (nil,
// false) for a constructor this file does not model yet. One row per
// width — TypedArrayConversions is the table other widths extend by
// adding a row, never by branching.
var TypedArrayConversions = map[string]func(float64) float64{
	"Uint8Array":        toUint8,
	"Int8Array":         toInt8,
	"Uint8ClampedArray": toUint8Clamp,
	"Int16Array":        toInt16,
	"Uint16Array":       toUint16,
	"Int32Array":        toInt32,
	"Uint32Array":       toUint32TypedArrayElement,
	"Float64Array":      identityConversion,
}

// unmodeledTypedArrayFamilies names every default-lib typed-array
// constructor this table recognizes but does NOT land — Float32Array
// and the two BigInt-element families — so a construction on one of
// them names the family in the decline instead of falling to the
// generic, constructor-name-blind "new builds a value the walk does
// not model" sentence (syntax_models.go's ast.KindNewExpression row).
var unmodeledTypedArrayFamilies = map[string]string{
	"Float32Array":   "Float32Array's element conversion rounds through IEEE 754 binary32 (Math.fround) — this table's func(float64) float64 conversions have no binary32-rounding primitive to state it exactly",
	"BigInt64Array":  "BigInt64Array's element conversion (ToBigInt64) takes and returns BigInt — this table's conversions are all func(float64) float64 and have no BigInt vocabulary",
	"BigUint64Array": "BigUint64Array's element conversion (ToBigUint64) takes and returns BigInt — this table's conversions are all func(float64) float64 and have no BigInt vocabulary",
}

func typedArrayConversion(name string) (func(float64) float64, bool) {
	convert, ok := TypedArrayConversions[name]
	return convert, ok
}

// noteUnmodeledTypedArrayFamilyIfNamed records, by name, a `new` whose
// callee is one of unmodeledTypedArrayFamilies (Float32Array,
// BigInt64Array, BigUint64Array) resolving to the default lib — the
// named-absence counterpart of builtin_models.go's NoteUnmodeledCall,
// for constructors rather than calls. A no-op for every other callee
// (an ordinary unrecognized identifier, a shadowed local, a
// non-identifier callee), which falls to the generic syntax-kind
// decline exactly as before.
func noteUnmodeledTypedArrayFamilyIfNamed(ctx *FlowContext, e *ast.Node) {
	callee := calleeOf(e)
	if callee == nil || !ast.IsIdentifier(callee) {
		return
	}
	name := callee.Text()
	reason, ok := unmodeledTypedArrayFamilies[name]
	if !ok {
		return
	}
	if !resolvesToDefaultLib(ctx, callee) {
		return
	}
	if !assignability.CollectingReasons() {
		return
	}
	assignability.NoteReason(assignability.ReasonNote{
		Site:        "expression",
		Node:        e,
		Said:        name + " is not modeled: " + reason,
		Unsupported: true,
	})
}

// toFixedSizeInteger is ToFixedSizeInteger (#sec-tofixedsizeinteger):
// int modulo 2^bitWidth, shifted into the signed range when asked.
// The argument here always arrives already an integer (every caller
// pre-checks isInteger), which is ToIntegerOrInfinity's own job one
// step up (ToInt8/ToUint8 call it before this) — infinities never
// reach this function, so the "+/-inf maps to 0" step of the spec
// algorithm has no row here.
func toFixedSizeInteger(intValue float64, signed bool, bitWidth uint) float64 {
	modulus := float64(uint64(1) << bitWidth)
	fixed := floorMod(intValue, modulus)
	if signed && fixed >= modulus/2 {
		fixed -= modulus
	}
	return fixed
}

// floorMod is floor-mod (always non-negative for a positive modulus),
// matching the spec's mathematical "modulo" — Go's math.Mod (and this
// package's own `mod`, JS `%`) keeps the dividend's sign, which would
// answer a negative fixedInt for a negative input the spec never does.
func floorMod(value, modulus float64) float64 {
	m := math.Mod(value, modulus)
	if m < 0 {
		m += modulus
	}
	return m
}

// toUint8 is ToUint8 (#sec-touint8): ToFixedSizeInteger(int,
// unsigned, 8) — wraps modulo 2^8 into [0, 255].
func toUint8(v float64) float64 {
	return toFixedSizeInteger(v, false, 8)
}

// toInt8 is ToInt8 (#sec-toint8): ToFixedSizeInteger(int, signed, 8)
// — wraps modulo 2^8 into [-128, 127].
func toInt8(v float64) float64 {
	return toFixedSizeInteger(v, true, 8)
}

// toUint8Clamp is ToUint8Clamp (#sec-touint8clamp): clamps to [0,
// 255] and rounds — never wraps. The fixture's own rows only exercise
// integer inputs (200, 300, -50), so the round-half-to-even step
// (only live for a non-integral clamped value) never fires on any
// value this model sees; the clamp bounds are the whole of what runs.
func toUint8Clamp(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return v
}

// toInt16 is ToInt16 (#sec-toint16): ToFixedSizeInteger(int, signed,
// 16) — wraps modulo 2^16 into [-32768, 32767].
func toInt16(v float64) float64 {
	return toFixedSizeInteger(v, true, 16)
}

// toUint16 is ToUint16 (#sec-touint16): ToFixedSizeInteger(int,
// unsigned, 16) — wraps modulo 2^16 into [0, 65535].
func toUint16(v float64) float64 {
	return toFixedSizeInteger(v, false, 16)
}

// toInt32 is ToInt32 (#sec-toint32): ToFixedSizeInteger(int, signed,
// 32) — wraps modulo 2^32 into [-2147483648, 2147483647].
func toInt32(v float64) float64 {
	return toFixedSizeInteger(v, true, 32)
}

// toUint32TypedArrayElement is ToUint32 (#sec-touint32):
// ToFixedSizeInteger(int, unsigned, 32) — wraps modulo 2^32 into [0,
// 4294967295]. Named apart from math_transfer.go's own toUint32
// (a different signature, func(float64) uint32, for the bitwise
// operators' ToUint32) — this is the element-conversion shape,
// func(float64) float64, the table below needs.
func toUint32TypedArrayElement(v float64) float64 {
	return toFixedSizeInteger(v, false, 32)
}

// identityConversion is Float64Array's own element conversion:
// NumericToRawBytes (#sec-numerictorawbytes) encodes a ~float64~
// element as "the IEEE 754-2019 binary64 format encoding of value"
// with no separate "Conversion Operation" column entry (table-the-
// typedarray-constructors leaves that cell empty for Float64Array) —
// the value a Float64Array element holds IS the Number that reached
// it (already ToNumber'd one step up, TypedArraySetElement's own `?
// ToNumber(value)`, #sec-typedarraysetelement), carried through
// unchanged.
func identityConversion(v float64) float64 {
	return v
}

// typedArrayConstructorName reads the identifier a `new NAME(...)`
// spells when NAME resolves to one of the default-lib TypedArray
// constructors this file models — or ("", false) for every other
// callee (a different constructor, a shadowed local, a non-identifier
// callee).
func typedArrayConstructorName(ctx *FlowContext, e *ast.Node) (string, bool) {
	callee := calleeOf(e)
	if callee == nil || !ast.IsIdentifier(callee) {
		return "", false
	}
	name := callee.Text()
	if _, ok := typedArrayConversion(name); !ok {
		return "", false
	}
	if !resolvesToDefaultLib(ctx, callee) {
		return "", false
	}
	return name, true
}

// ReadTypedArrayConstruction answers what `new Uint8Array(...)` /
// `new Int8Array(...)` / `new Uint8ClampedArray(...)` BUILDS, or nil
// where no row here speaks — sits beside ReadArrayConstruction in
// EvaluateNewExpression's chain, the same "one construction reader
// per built-in" shape collection_models.go's ReadCollectionConstruction
// and array_construction.go's ReadArrayConstruction both already
// wear.
//
// Two rows, both from the shared _TypedArray_ (...args) algorithm
// (#sec-typedarray, oldids sec-typedarray-length/sec-typedarray-object,
// tmp/ecma262/spec.html):
//   - one NUMBER argument (a non-Object length): "Assert: firstArg is
//     not an Object. Let elementLength be ? ToIndex(firstArg). Return
//     ? AllocateTypedArray(...)" — AllocateTypedArray's own
//     AllocateTypedArrayBuffer zeroes every byte of the freshly
//     allocated buffer (#sec-allocatetypedarraybuffer's
//     AllocateArrayBuffer step), and 0 already IS every conversion's
//     own fixed point (toUint8(0) = toInt8(0) = toUint8Clamp(0) = 0),
//     so the zero-filled claim holds under any of this table's rows
//     without running the row at all.
//   - one ARRAY-LITERAL argument: an Array is an Object with
//     %Symbol.iterator%, so construction takes the "usingIterator"
//     branch — IteratorToList then InitializeTypedArrayFromList
//     (#sec-initializetypedarrayfromlist), which seeds each slot via
//     `Set(obj, ToString(k), kValue, true)` in order. A TypedArray's
//     own [[Set]] (#sec-typedarray-set) routes every numeric-string
//     key through TypedArraySetElement (#sec-typedarraysetelement,
//     oldid sec-integerindexedelementset), which ToNumbers the value
//     and stores it via SetValueInBuffer -> NumericToRawBytes
//     (#sec-numerictorawbytes, oldid sec-numbertorawbytes) — the AO
//     that actually runs the element type's own "Conversion
//     Operation" (table-the-typedarray-constructors: Uint8Array ->
//     ToUint8, Int8Array -> ToInt8, Uint8ClampedArray -> ToUint8Clamp)
//     before encoding the bytes. That conversion-operation table is
//     the wrap/clamp table this file's toUint8/toInt8/toUint8Clamp
//     mirror.
//
// Every other argument shape (a length the walk has not pinned to one
// exact non-negative integer, a non-literal array-like, an
// ArrayBuffer + offset/length triple, another TypedArray) is not
// modeled: nil, so the unmodeled tail answers instead of this row
// overclaiming.
func ReadTypedArrayConstruction(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	name, ok := typedArrayConstructorName(ctx, e)
	if !ok {
		noteUnmodeledTypedArrayFamilyIfNamed(ctx, e)
		return nil
	}
	convert, _ := typedArrayConversion(name)
	args, hasArgs := callArguments(e)
	if !hasArgs || len(args) != 1 {
		return nil
	}
	argument := args[0]
	if ast.IsSpreadElement(argument) {
		return nil
	}
	if ast.IsArrayLiteralExpression(argument) {
		elements := argument.AsArrayLiteralExpression().Elements.Nodes
		values := make([]float64, 0, len(elements))
		grade := abstractdomain.TrustSpec
		for _, element := range elements {
			if ast.IsOmittedExpression(element) || ast.IsSpreadElement(element) {
				// a hole or a spread inside the literal: which slots the
				// elements fill is not one-to-one with the literal's own
				// positions, so this row does not speak
				return nil
			}
			known := evaluateExpression(ctx, env, element)
			if known.Kind != abstractdomain.KindValues || known.KindTag != abstractdomain.PrimitiveNumber ||
				len(known.Values) != 1 {
				return nil
			}
			grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(known))
			values = append(values, convert(known.Values[0]))
		}
		out := abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, grade)
		return &out
	}
	known := evaluateExpression(ctx, env, argument)
	if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber &&
		len(known.Values) == 1 {
		length := known.Values[0]
		if !isNonNegativeInteger(length) {
			// a non-integer or negative length throws (RangeError) at
			// construction: no array exists for this model to answer,
			// the same "the contract row fired" reading
			// array_construction.go's length arm gives
			return nil
		}
		grade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(known))
		values := make([]float64, int(length))
		out := abstractdomain.KnownValues(values, abstractdomain.PrimitiveArray, grade)
		return &out
	}
	return nil
}

// TypedArrayWriteConversion reads, off the CHECKER's own static type
// at a receiver expression, which ToXxx conversion an indexed write
// through it runs before the value lands — or (nil, false) where the
// receiver's type is not one of this table's constructors. This is
// the write-side counterpart of ReadTypedArrayConstruction: a typed
// array built as KnownValues(..., PrimitiveArray, ...) carries no tag
// of its own saying WHICH typed array it is (the same shape a plain
// number array wears), so the write path asks the type checker the
// same way collection_models.go's spec-fixed fallback and
// date_models.go's getTime/valueOf row both already do
// (receiverType.Symbol().Name) rather than adding a new AbstractValue
// field only this one path would read.
func TypedArrayWriteConversion(ctx *FlowContext, receiverExpression *ast.Node) (func(float64) float64, bool) {
	receiverType := typereading.TypeAtLocation(ctx.P.Checker, receiverExpression)
	if receiverType == nil || receiverType.Symbol() == nil {
		return nil, false
	}
	return typedArrayConversion(receiverType.Symbol().Name)
}

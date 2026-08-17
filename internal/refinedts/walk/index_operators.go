// from evaluation/index_operators.ts
//
// Indexed writes on a tracked name, and the index-order test used by
// sorted-sequence subtraction. An exact tuple with a pinned in-range
// index takes the write in place; a SINGLE exactly-known key (a
// literal, or an index expression whose static type has one
// string-literal member) adds or replaces that key exactly like a
// property write; a key-typed index carrying several CANDIDATE keys
// writes only the ones already present, joined with the new value.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ReadIndexedWrite is readIndexedWrite in the TS source: a write
// through an INDEX on a tracked name — or (AbstractValue{}, false)
// where the left side is not that form.
func ReadIndexedWrite(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken || !ast.IsElementAccessExpression(bin.Left) {
		return abstractdomain.AbstractValue{}, false
	}
	elem := bin.Left.AsElementAccessExpression()
	if !ast.IsIdentifier(elem.Expression) {
		return abstractdomain.AbstractValue{}, false
	}
	name := elem.Expression.Text()
	if _, ok := env.Get(name); !ok {
		return abstractdomain.AbstractValue{}, false
	}
	receiver, ok := env.Get(name)
	if !ok {
		receiver = silence.Residue()
	}
	index := evaluateExpression(ctx, env, elem.ArgumentExpression)
	value := evaluateExpression(ctx, env, bin.Right)
	// a declared sequence's element set is an invariant: judge the
	// written value against it before the tracked value moves
	WriteElement(ctx, bin.Left, value, bin.Right)
	// `ta[i] = v` on a TypedArray runs the constructor's own ToXxx
	// conversion on v before the STORED element changes
	// (TypedArraySetElement, #sec-typedarraysetelement, oldid
	// sec-integerindexedelementset — called from TypedArray's own
	// [[Set]], #sec-typedarray-set): ToUint8 wraps modulo 2^8, ToInt8
	// wraps signed, ToUint8Clamp clamps to [0, 255]. The ASSIGNMENT
	// EXPRESSION's own value is unaffected — simple-assignment runtime
	// semantics (#sec-assignment-operators-runtime-semantics-evaluation)
	// read `PutValue(leftRef, rightValue); Return rightValue` — the
	// UNCONVERTED right-hand value, always; the conversion is visible
	// only to a LATER READ of the element, never to the assignment
	// expression itself (`x = (ta[i] = 200)` is 200, exactly like a
	// plain array). The receiver's AbstractValue carries no tag saying
	// which conversion applies — the checker's own static type at the
	// receiver expression does (typed_array_models.go's
	// TypedArrayWriteConversion, the same receiverType.Symbol().Name
	// reading collection_models.go and date_models.go already use for
	// their own spec-fixed rows).
	if convert, isTypedArray := TypedArrayWriteConversion(ctx, elem.Expression); isTypedArray &&
		receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray &&
		index.Kind == abstractdomain.KindValues && len(index.Values) == 1 &&
		value.Kind == abstractdomain.KindValues && len(value.Values) == 1 &&
		value.KindTag == abstractdomain.PrimitiveNumber &&
		isInteger(index.Values[0]) {
		// an out-of-range index is a spec no-op (TypedArraySetElement's own
		// IsValidIntegerIndex guard skips the store, per its note: "no
		// effect when attempting to write past the end") — the tracked
		// array is unchanged either way; only an in-bounds index moves it
		if index.Values[0] >= 0 && int(index.Values[0]) < len(receiver.Values) {
			next := append([]float64{}, receiver.Values...)
			next[int(index.Values[0])] = convert(value.Values[0])
			UpdateTrackedEnv(ctx.Aliases, env, name, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
		}
		return value, true
	}
	if receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray &&
		index.Kind == abstractdomain.KindValues && len(index.Values) == 1 &&
		value.Kind == abstractdomain.KindValues && len(value.Values) == 1 &&
		value.KindTag == abstractdomain.PrimitiveNumber &&
		isInteger(index.Values[0]) &&
		index.Values[0] >= 0 && int(index.Values[0]) < len(receiver.Values) {
		next := append([]float64{}, receiver.Values...)
		next[int(index.Values[0])] = value.Values[0]
		UpdateTrackedEnv(ctx.Aliases, env, name, abstractdomain.KnownValues(next, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
		return value, true
	}
	if receiver.Kind == abstractdomain.KindObject {
		argument := elem.ArgumentExpression
		var literal string
		hasLiteral := false
		if ast.IsStringLiteral(argument) || ast.IsNoSubstitutionTemplateLiteral(argument) {
			literal, hasLiteral = argument.Text(), true
		}
		// a SINGLE exactly-known key — spelled literally in source, or the
		// only member of the index expression's own static type (`key:
		// "age"`, the element type Object.keys(defaults).reduce binds a
		// key parameter to when `defaults` has one key) — writes exactly
		// like a property access `acc.age = v` does (WriteProperty,
		// assignments.go): add the key when it is absent, replace it when
		// present. There is no ambiguity about WHICH key moved, so this
		// needs no presence gate and no join with a value the key may
		// never have held.
		var indexType *checker.Type
		if !hasLiteral {
			indexType = ctx.P.Checker.GetTypeAtLocation(argument)
			if !indexType.IsUnion() && indexType.IsStringLiteral() {
				if lit, ok := indexType.AsLiteralType().Value().(string); ok {
					literal, hasLiteral = lit, true
				}
			}
		}
		if hasLiteral {
			keys := setObjectKey(receiver.Keys, literal, value)
			UpdateTrackedEnv(ctx.Aliases, env, name, abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false))
			return value, true
		}
		// a UNION of candidate keys (`key: "a" | "b"`): which one the
		// runtime actually wrote is unknown, so a key the receiver does
		// not already carry cannot be safely invented — only a candidate
		// ALREADY present can be joined with the new value, same as before.
		var candidates []*checker.Type
		if indexType.IsUnion() {
			candidates = indexType.Types()
		} else {
			candidates = []*checker.Type{indexType}
		}
		var written []string
		readable := len(candidates) > 1
		for _, candidate := range candidates {
			if candidate.IsStringLiteral() {
				lit, _ := candidate.AsLiteralType().Value().(string)
				written = append(written, lit)
			} else {
				readable = false
			}
		}
		if readable {
			allPresent := true
			for _, key := range written {
				if _, hasKey := objectKeyIndex(receiver, key); !hasKey {
					allPresent = false
					break
				}
			}
			if allPresent {
				keys := append([]abstractdomain.ObjectKey{}, receiver.Keys...)
				for _, key := range written {
					idx, _ := objectKeyIndex(receiver, key)
					keys[idx] = abstractdomain.ObjectKey{Name: key, Value: abstractdomain.JoinKnown(keys[idx].Value, value)}
				}
				UpdateTrackedEnv(ctx.Aliases, env, name, abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false))
				return value, true
			}
		}
	}
	HavocEnv(ctx.Aliases, env, name)
	return value, true
}

// ReadIndexedCompoundWrite is ReadIndexedWrite's compound twin:
// `name[i] OP= e` on a tracked identifier — `+= -= *= /= %=`, the six
// bitwise/shift compounds, and `**=`, over an EXACT-tuple number array
// with a pinned in-range index. (AbstractValue{}, false) where the left
// side is not that form, the receiver is not a plain in-bounds number
// array, or the index/value are not exact.
//
// Before this function existed, a compound through an element access
// (`ages[0] += 190`) matched no arm in ReadAssignment or ReadIndexedWrite
// (both gate on ast.KindEqualsToken, or an identifier/property LEFT —
// never an ElementAccessExpression with a compound token) and fell to
// ReadForgottenAssignment, which evaluates the right side, forgets the
// whole receiver, and returns — no judge ever ran, so an out-of-set
// compound write (`ages[0] += 190` past Age's ceiling) passed silently.
// This function runs the SAME judge WriteElement already runs for a
// direct `ages[0] = 200` write (assignments.go), against the COMPUTED
// value rather than a written literal, and folds the write back through
// UpdateTrackedEnv exactly as ReadIndexedWrite's own exact-array arm
// does.
func ReadIndexedCompoundWrite(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind < ast.KindFirstCompoundAssignment ||
		bin.OperatorToken.Kind > ast.KindLastCompoundAssignment ||
		!ast.IsElementAccessExpression(bin.Left) {
		return abstractdomain.AbstractValue{}, false
	}
	elem := bin.Left.AsElementAccessExpression()
	if !ast.IsIdentifier(elem.Expression) {
		return abstractdomain.AbstractValue{}, false
	}
	name := elem.Expression.Text()
	receiver, hasReceiver := env.Get(name)
	if !hasReceiver {
		return abstractdomain.AbstractValue{}, false
	}
	if receiver.Kind != abstractdomain.KindValues || receiver.KindTag != abstractdomain.PrimitiveArray {
		return abstractdomain.AbstractValue{}, false
	}
	index := evaluateExpression(ctx, env, elem.ArgumentExpression)
	if index.Kind != abstractdomain.KindValues || len(index.Values) != 1 || !isInteger(index.Values[0]) {
		return abstractdomain.AbstractValue{}, false
	}
	i := index.Values[0]
	if i < 0 || int(i) >= len(receiver.Values) {
		return abstractdomain.AbstractValue{}, false
	}
	right := evaluateExpression(ctx, env, bin.Right)
	before := abstractdomain.KnownValues([]float64{receiver.Values[int(i)]}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(receiver))
	next := compoundResult(ctx, bin.Left, bin.Right, bin.OperatorToken.Kind, before, right)
	// a declared sequence's element set is an invariant: judge the
	// written value against it before the tracked value moves — the
	// exact rule WriteElement already applies to a direct `a[i] = v`
	// write, applied here to the COMPUTED compound result
	WriteElement(ctx, bin.Left, next, e)
	if next.Kind == abstractdomain.KindValues && len(next.Values) == 1 && next.KindTag == abstractdomain.PrimitiveNumber {
		if convert, isTypedArray := TypedArrayWriteConversion(ctx, elem.Expression); isTypedArray {
			stored := append([]float64{}, receiver.Values...)
			stored[int(i)] = convert(next.Values[0])
			UpdateTrackedEnv(ctx.Aliases, env, name, abstractdomain.KnownValues(stored, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
			return next, true
		}
		stored := append([]float64{}, receiver.Values...)
		stored[int(i)] = next.Values[0]
		UpdateTrackedEnv(ctx.Aliases, env, name, abstractdomain.KnownValues(stored, abstractdomain.PrimitiveArray, abstractdomain.TrustProved))
		return next, true
	}
	HavocEnv(ctx.Aliases, env, name)
	return next, true
}

func objectKeyIndex(receiver abstractdomain.AbstractValue, name string) (int, bool) {
	for i, key := range receiver.Keys {
		if key.Name == name {
			return i, true
		}
	}
	return 0, false
}

func setObjectKey(keys []abstractdomain.ObjectKey, name string, value abstractdomain.AbstractValue) []abstractdomain.ObjectKey {
	out := append([]abstractdomain.ObjectKey{}, keys...)
	for i, key := range out {
		if key.Name == name {
			out[i] = abstractdomain.ObjectKey{Name: name, Value: value}
			return out
		}
	}
	return append(out, abstractdomain.ObjectKey{Name: name, Value: value})
}

// IndexAtLeast is indexAtLeast in the TS source: does the LEFT index
// provably sit at or above the RIGHT one? By literals, by a ledger
// row over their places, or by their windows (left's floor at or
// above right's ceiling). Effect-free sides only — a decision must
// not re-run an effect.
func indexAtLeast(ctx *FlowContext, env Env, left, right *ast.Node) bool {
	if ast.IsNumericLiteral(left) && ast.IsNumericLiteral(right) {
		return float64(jsnum.FromString(left.Text())) >= float64(jsnum.FromString(right.Text()))
	}
	// as place + offset: i ≥ i − 1 outright; across places, a ledger
	// row L − R ≥ b decides when b covers the offsets' difference
	// (array indexes sit under 2^32, so the offsets are exact)
	leftSide := dataflowfacts.OffsetPlaceOf(ctx.P.Checker, left)
	var rightSide *dataflowfacts.OffsetPlace
	if leftSide != nil {
		rightSide = dataflowfacts.OffsetPlaceOf(ctx.P.Checker, right)
	}
	if leftSide != nil && rightSide != nil {
		if dataflowfacts.SamePlace(leftSide.Place, rightSide.Place) {
			return leftSide.Offset >= rightSide.Offset
		}
		row := dataflowfacts.DifferenceConstraintFor(ctx.DifferenceConstraints, leftSide.Place, rightSide.Place)
		if row != nil && row.Bound >= float64(rightSide.Offset-leftSide.Offset) {
			return true
		}
	}
	effectFree := func(side *ast.Node) bool {
		return ast.IsNumericLiteral(side) || ast.IsIdentifier(side) ||
			(ast.IsPropertyAccessExpression(side) && ast.IsIdentifier(side.AsPropertyAccessExpression().Expression))
	}
	if !effectFree(left) || !effectFree(right) {
		return false
	}
	leftRange := RangeOfKnown(evaluateExpression(ctx, env, left))
	var rightRange *NumberRange
	if leftRange != nil {
		rightRange = RangeOfKnown(evaluateExpression(ctx, env, right))
	}
	return leftRange != nil && rightRange != nil && leftRange.Lo >= rightRange.Hi
}

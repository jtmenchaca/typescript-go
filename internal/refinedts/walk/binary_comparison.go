// from evaluation/binary_comparison.ts
//
// Relational and equality operators, `instanceof` against default-
// library constructors, and `in` against a complete key set. Ledger
// rows decide comparisons without rereading either value when both
// sides are number-sorted and their places are stable.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// instanceTable is INSTANCE_TABLE in the TS source: the
// default-library constructors whose %Symbol.hasInstance% is the
// inherited default, making `instanceof` OrdinaryHasInstance — a
// prototype-chain walk the checker can decide for values it built.
// Resolved to the default library before use, never matched by name
// alone.
var instanceTable = map[string]bool{
	"Object": true, "Array": true, "Function": true, "Date": true,
	"RegExp": true, "Error": true, "Map": true, "Set": true,
	"WeakMap": true, "WeakSet": true, "Promise": true, "Number": true,
	"String": true, "Boolean": true,
}

// ReadInKeyword is readInKeyword in the TS source: `k in o`. A key
// STATED on the object proves presence with no completeness needed;
// absence is a theorem only on a COMPLETE key set — a literal-built
// object knows every key it has. HasProperty walks the prototype
// chain (sec-relational-operators-runtime-semantics-evaluation →
// HasProperty), so a key every ordinary object inherits ("toString"
// in {}) answers true even off the own keys.
func ReadInKeyword(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindInKeyword {
		return abstractdomain.AbstractValue{}, false
	}
	keyKnown := evaluateExpression(ctx, env, bin.Left)
	target := evaluateExpression(ctx, env, bin.Right)
	var key string
	hasKey := false
	if ast.IsStringLiteral(bin.Left) || ast.IsNoSubstitutionTemplateLiteral(bin.Left) {
		key, hasKey = bin.Left.Text(), true
	} else if keyKnown.Kind == abstractdomain.KindValues && keyKnown.KindTag == abstractdomain.PrimitiveString {
		key, hasKey = stringOf(keyKnown.Values), true
	}
	if hasKey && target.Kind == abstractdomain.KindObject {
		// presence of a STATED key needs no completeness — the key is
		// there whatever else the object holds. But a key holding
		// undefined is ambiguous in this domain: an explicit
		// `{k: undefined}` answers true while a deleted key (spelled as
		// the same Undef entry) answers false, so those stay unknown.
		if index, hasOwn := objectKeyIndex(target, key); hasOwn {
			entry := target.Keys[index].Value
			if entry.Kind != abstractdomain.KindUndef && entry.Kind != abstractdomain.KindPossiblyUndefined {
				return abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec), true
			}
			return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{keyKnown, target}), true
		}
		if abstractdomain.ObjectPrototypeFunctionKeys[key] {
			return abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec), true
		}
		// ABSENCE still needs the complete key set: only a
		// literal-built object knows every key it has — and never a
		// target whose declared type is an OPEN MAP (element_access.go's
		// OpenMapAt), where any key may be present at some other call.
		// The presence half above is untouched: a stated key is there
		// whatever the type says, so it needs no completeness at all.
		// The parameter binding now strips the completeness an open-map
		// parameter never earned; this gate is defense in depth for the
		// evaluation-path routes that cross no parameter binding.
		if target.Complete && !openMapReceiver(ctx, bin.Right) {
			return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec), true
		}
	}
	return abstractdomain.UnknownOver([]abstractdomain.AbstractValue{keyKnown, target}), true
}

// ReadInstanceOf is readInstanceOf in the TS source: `x instanceof C`
// against a default-library constructor.
func ReadInstanceOf(ctx *FlowContext, env Env, e *ast.Node) (abstractdomain.AbstractValue, bool) {
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindInstanceOfKeyword {
		return abstractdomain.AbstractValue{}, false
	}
	left := evaluateExpression(ctx, env, bin.Left)
	evaluateExpression(ctx, env, bin.Right)
	var constructor string
	hasConstructor := false
	if ast.IsIdentifier(bin.Right) && instanceTable[bin.Right.Text()] &&
		ctx.P.Checker.SymbolInDefaultLib(ctx.P.Checker.GetSymbolAtLocation(bin.Right)) {
		constructor, hasConstructor = bin.Right.Text(), true
	}
	boolPair := func() abstractdomain.AbstractValue {
		return abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	}
	if !hasConstructor {
		// an unrecognized right side still answers a boolean
		return boolPair(), true
	}
	primitive := left.Kind == abstractdomain.KindNaN || left.Kind == abstractdomain.KindUndef ||
		(left.Kind == abstractdomain.KindValues && left.KindTag != abstractdomain.PrimitiveArray)
	if primitive {
		return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec), true
	}
	if left.Kind == abstractdomain.KindValues || left.Kind == abstractdomain.KindList || left.Kind == abstractdomain.KindArrayHoles {
		// an array the walk built: Array.prototype → Object.prototype
		v := float64(0)
		if constructor == "Array" || constructor == "Object" {
			v = 1
		}
		return abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec), true
	}
	if left.Kind == abstractdomain.KindObject && left.Complete {
		// an object BUILT IN VIEW (complete = a literal): its chain is
		// Object.prototype and nothing else. A stated object stays
		// unknown — the promise is structural, and the runtime value
		// could be a class instance wearing any prototype.
		v := float64(0)
		if constructor == "Object" {
			v = 1
		}
		return abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec), true
	}
	// a verdict the walk cannot decide is still a BOOLEAN —
	// InstanceofOperator answers ToBoolean of its handler either way
	// (sec-instanceofoperator)
	return boolPair(), true
}

// ReadComparison is readComparison in the TS source: equality and
// relational operators — or (AbstractValue{}, false) where the token
// is none of them. Left and right are already evaluated.
func ReadComparison(ctx *FlowContext, e *ast.Node, left, right abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	bin := e.AsBinaryExpression()
	var comparison ComparisonOp
	hasComparison := true
	switch bin.OperatorToken.Kind {
	case ast.KindLessThanToken:
		comparison = CompareLt
	case ast.KindGreaterThanToken:
		comparison = CompareGt
	case ast.KindLessThanEqualsToken:
		comparison = CompareLe
	case ast.KindGreaterThanEqualsToken:
		comparison = CompareGe
	case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
		comparison = CompareEq
	case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
		comparison = CompareNe
	default:
		hasComparison = false
	}
	if !hasComparison {
		return abstractdomain.AbstractValue{}, false
	}
	strict := bin.OperatorToken.Kind == ast.KindEqualsEqualsEqualsToken ||
		bin.OperatorToken.Kind == ast.KindExclamationEqualsEqualsToken
	// a pair the ledger orders is DECIDED without reading either
	// value: the dominating guard already compared these very
	// places, and neither has moved since. Number-sorted
	// knowledge on both sides pins the runtime values as numbers —
	// an object's stateful valueOf could not replay the guard.
	if abstractdomain.IsNumericKind(left) && abstractdomain.IsNumericKind(right) {
		leftPlace := dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Left)
		var rightPlace *dataflowfacts.PlaceKey
		if leftPlace != nil {
			rightPlace = dataflowfacts.PlaceKeyOf(ctx.P.Checker, bin.Right)
		}
		if leftPlace != nil && rightPlace != nil {
			decided := dataflowfacts.DecideComparison(
				ctx.DifferenceConstraints, *leftPlace, *rightPlace,
				dataflowfacts.ComparisonOp(comparison), ctx.Kernel,
			)
			if decided != nil {
				v := float64(0)
				if *decided {
					v = 1
				}
				return abstractdomain.KnownValues(
					[]float64{v},
					abstractdomain.PrimitiveBoolean,
					abstractdomain.MinTrustLevel(
						abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(left), abstractdomain.TrustLevelOf(right)),
						abstractdomain.TrustSpec,
					),
				), true
			}
		}
	}
	return CompareKnown(ctx, comparison, strict, left, right), true
}

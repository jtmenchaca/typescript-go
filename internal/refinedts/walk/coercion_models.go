// from evaluation/coercion_models.ts
//
// The coercion models: ToNumber and the parse grammars at exact
// values, String()/Number(), parseInt/parseFloat, the Number
// predicates, and JSON.parse/JSON.stringify. Split from
// builtin_models.ts per the v2 tree.
//
// The String-target primitives live in coercion_models_string.go, the
// Number-target primitives (StringToNumber, parseInt/parseFloat) in
// coercion_models_number.go, the URI functions in
// coercion_models_uri.go, and JSON.parse/JSON.stringify in
// coercion_models_json.go.

package walk

import (
	"math"
	"math/big"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readCoercionGlobals is readCoercionGlobals in the TS source: the
// global coercion calls — ONE recognition per callee (finding 22):
// String(), Number(), and parseInt/parseFloat in both spellings each
// read in exactly one place, so two readings of the same call can
// never diverge.
func readCoercionGlobals(ctx *FlowContext, env Env, e *ast.Node, spreadArguments func(args []*ast.Node) []abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	// String(value): the empty string with no argument, ToString of the
	// argument otherwise (sec-string-constructor-string-value). Exact
	// where the argument is one exact word — the host computes
	// ToString; the undef marker conflates undefined ("undefined") with
	// null ("null"), so it stays at the sort. Every call returns a
	// STRING, so the widest answer never drops below C*.
	if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "String" &&
		resolvesToDefaultLib(ctx, call.Expression) && len(arguments) <= 1 {
		if len(arguments) == 0 {
			out := abstractdomain.KnownValues(nil, abstractdomain.PrimitiveString, abstractdomain.TrustSpec)
			return &out
		}
		spread := spreadArguments(arguments)
		if len(spread) > 0 {
			first := spread[0]
			grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(first), abstractdomain.TrustSpec)
			if first.Kind == abstractdomain.KindValues && first.KindTag == abstractdomain.PrimitiveString {
				out := abstractdomain.AtTrustLevel(first, grade)
				return &out
			}
			// every other value converts through the ONE text model —
			// numbers and booleans their words, undefined/null theirs, a
			// plain object "[object Object]", an array its comma join
			text, ok := TextOfKnown(ctx.Kernel.Decimal, first)
			if ok {
				held := abstractdomain.MinTrustLevel(grade, text.Grade)
				var out abstractdomain.AbstractValue
				if text.HasExact {
					out = abstractdomain.KnownValues(text.Exact, abstractdomain.PrimitiveString, held)
				} else {
					out = abstractdomain.KnownSet(text.Set, nil, held, abstractdomain.SetKindTagNone)
				}
				return &out
			}
			out := abstractdomain.KnownSet(refinementsets.Strings, nil, grade, abstractdomain.SetKindTagNone)
			return &out
		}
		out := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		return &out
	}
	// String.fromCharCode — each argument maps to the code unit whose
	// numeric value is ℝ(? ToUint16(_next_)), and the result is the
	// string-concatenation of those units (sec-string.fromcharcode).
	// Exact where every argument is one exact number and the units are
	// well-formed UTF-16; every call returns a STRING regardless (the
	// algorithm only builds code units), so the widest answer keeps the
	// sort.
	if ast.IsPropertyAccessExpression(call.Expression) {
		pa := call.Expression.AsPropertyAccessExpression()
		if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "String" &&
			resolvesToDefaultLib(ctx, pa.Expression) && pa.Name().Text() == "fromCharCode" {
			exact := true
			values := make([]float64, 0, len(arguments))
			grade := abstractdomain.TrustSpec
			for _, argument := range arguments {
				if ast.IsSpreadElement(argument) {
					// a spread's element count is not pinned here; its
					// inner expression still evaluates (writes ride), and
					// the sort answer below still holds
					evaluateExpression(ctx, env, argument.AsSpreadElement().Expression)
					exact = false
					continue
				}
				argKnown := evaluateExpression(ctx, env, argument)
				grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(argKnown))
				if argKnown.Kind == abstractdomain.KindNaN {
					// ToUint16(NaN) is +0 — ToIntegerOrInfinity reads NaN
					// as 0 (sec-touint16)
					values = append(values, math.NaN())
					continue
				}
				if argKnown.Kind == abstractdomain.KindValues && argKnown.KindTag == abstractdomain.PrimitiveNumber && len(argKnown.Values) == 1 {
					values = append(values, argKnown.Values[0])
					continue
				}
				exact = false
			}
			if exact {
				if text, ok := jsFromCharCode(values); ok {
					out := abstractdomain.KnownValues(refinementsets.CodepointsOf(text), abstractdomain.PrimitiveString, grade)
					return &out
				}
			}
			out := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
			return &out
		}
	}
	// Number(value): +0 with no argument, ToNumber of the argument
	// otherwise (sec-number-constructor-number-value). The non-string
	// ToNumber rows are the transcribed table
	// (transfers/tonumber_corners.lean); StringToNumber is exactly
	// specified (sec-tonumber-applied-to-the-string-type), so the host
	// computes the exact value. The undef marker conflates undefined
	// (NaN) with null (+0), so it declines to exactness — and a NUMBER
	// OR NaN always comes out, so the widest answer holds the sort's
	// whole ground instead of dropping to nothing.
	if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Number" &&
		resolvesToDefaultLib(ctx, call.Expression) && len(arguments) <= 1 {
		if len(arguments) == 0 {
			out := abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			return &out
		}
		spread := spreadArguments(arguments)
		if len(spread) > 0 {
			first := spread[0]
			grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(first), abstractdomain.TrustSpec)
			if first.Kind == abstractdomain.KindNaN {
				out := abstractdomain.AtTrustLevel(abstractdomain.NaNValue, grade)
				return &out
			}
			if first.Kind == abstractdomain.KindValues && first.KindTag == abstractdomain.PrimitiveNumber {
				out := abstractdomain.AtTrustLevel(first, grade)
				return &out
			}
			if first.Kind == abstractdomain.KindValues && first.KindTag == abstractdomain.PrimitiveBoolean {
				out := abstractdomain.KnownValues(first.Values, abstractdomain.PrimitiveNumber, grade)
				return &out
			}
			if first.Kind == abstractdomain.KindValues && first.KindTag == abstractdomain.PrimitiveString {
				coerced, ok := jsStringToNumber(stringOf(first.Values))
				if !ok {
					out := abstractdomain.AtTrustLevel(abstractdomain.NaNValue, grade)
					return &out
				}
				out := abstractdomain.KnownValues([]float64{coerced}, abstractdomain.PrimitiveNumber, grade)
				return &out
			}
			// Number(bigint) is BigInt::toNumber — the number value
			// nearest the mathematical integer (sec-number-constructor →
			// NumberValue of the BigInt), round-to-nearest exactly as
			// big.Float's Float64 rounds. Never NaN.
			if first.Kind == abstractdomain.KindBigints && len(first.BigintValues) > 0 {
				numbers := make([]float64, len(first.BigintValues))
				for i, v := range first.BigintValues {
					f, _ := new(big.Float).SetInt(v).Float64()
					numbers[i] = f
				}
				out := abstractdomain.KnownValues(numbers, abstractdomain.PrimitiveNumber, grade)
				return &out
			}
			// the widest answer's real half is the number GROUND spelled
			// with its one vacuous conjunct (CanonicalScalarForms' own
			// rule: an empty form list is a set the kernel's questions
			// do not accept), so the NaN-wrapper judge's subset ask can
			// actually run against a refined sink.
			inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))), nil, grade, abstractdomain.SetKindTagNone)
			out := abstractdomain.PossiblyNaN(inner)
			return &out
		}
		inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(math.Inf(-1))), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		out := abstractdomain.PossiblyNaN(inner)
		return &out
	}
	// parseInt / parseFloat — exactly specified string grammars
	// (sec-parseint-string-radix, sec-parsefloat-string): on an exact
	// string (and an exact radix), the host computes the exact row —
	// the same string-read rule clz32 uses. ONE reading for both
	// spellings, the globals and Number's own copies.
	{
		var parseName string
		hasParseName := false
		if ast.IsPropertyAccessExpression(call.Expression) {
			pa := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Number" && resolvesToDefaultLib(ctx, pa.Expression) &&
				(pa.Name().Text() == "parseInt" || pa.Name().Text() == "parseFloat") {
				parseName, hasParseName = pa.Name().Text(), true
			}
		} else if ast.IsIdentifier(call.Expression) &&
			(call.Expression.Text() == "parseInt" || call.Expression.Text() == "parseFloat") &&
			resolvesToDefaultLib(ctx, call.Expression) {
			parseName, hasParseName = call.Expression.Text(), true
		}
		if hasParseName && len(arguments) >= 1 {
			spread := spreadArguments(arguments)
			var argument abstractdomain.AbstractValue
			hasArgument := len(spread) > 0
			if hasArgument {
				argument = spread[0]
			}
			var radix *abstractdomain.AbstractValue
			if len(arguments) >= 2 && len(spread) >= 2 {
				radix = &spread[1]
			}
			if hasArgument && argument.Kind == abstractdomain.KindValues && argument.KindTag == abstractdomain.PrimitiveString {
				text := stringOf(argument.Values)
				grade := abstractdomain.TrustLevelOf(argument)
				if radix == nil {
					grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustSpec)
				} else {
					grade = abstractdomain.MinTrustLevel(grade, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(*radix), abstractdomain.TrustSpec))
				}
				if parseName == "parseFloat" {
					x, ok := jsParseFloat(text)
					if !ok {
						out := abstractdomain.AtTrustLevel(abstractdomain.NaNValue, grade)
						return &out
					}
					out := abstractdomain.KnownValues([]float64{x}, abstractdomain.PrimitiveNumber, grade)
					return &out
				}
				radixOk := radix == nil
				var radixValue float64
				if radix != nil && radix.Kind == abstractdomain.KindValues && len(radix.Values) == 1 &&
					radix.KindTag != abstractdomain.PrimitiveString && radix.KindTag != abstractdomain.PrimitiveArray {
					radixOk = true
					radixValue = radix.Values[0]
				}
				if radixOk {
					var x float64
					var ok bool
					if radix == nil {
						x, ok = jsParseInt(text, 0)
					} else {
						x, ok = jsParseInt(text, int(radixValue))
					}
					if !ok {
						out := abstractdomain.AtTrustLevel(abstractdomain.NaNValue, grade)
						return &out
					}
					out := abstractdomain.KnownValues([]float64{x}, abstractdomain.PrimitiveNumber, grade)
					return &out
				}
			}
			// an argument the walk cannot pin still has the SPEC's
			// result shape. parseInt returns 𝔽(sign × mathInt) of an
			// unbounded integer — an integral double, or ±∞ where the
			// digits overflow 𝔽 (parseInt("9".repeat(400)) is
			// Infinity), or NaN (sec-parseint-string-radix). parseFloat
			// is any double or NaN (sec-parsefloat-string). The NaN arm
			// rides the wrapper, so a NaN guard downstream strips it and
			// the shape survives.
			if parseName == "parseInt" {
				inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Union(
					refinementsets.MakeRefinedSet(refinementsets.Integer),
					refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{math.Inf(1), math.Inf(-1)})),
				)), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
				out := abstractdomain.PossiblyNaN(inner)
				return &out
			}
			// parseFloat's own unpinned shape is any double — the number
			// GROUND (sec-parsefloat-string step 6, StringNumericValue of
			// any StrDecimalLiteral, ±∞ included) spelled with its one
			// vacuous ray conjunct, the SAME R-bar spelling refinementsets.Numbers
			// and Number(value)'s own widest-answer branch above (line 167)
			// use — never refinementsets.MakeRefinedSet() with ZERO forms,
			// which is a set the kernel's scalar questions cannot even pose
			// (Numbers' own doc comment: "the kernel's scalar questions ask
			// for at least one refinement"). The zero-form spelling here was
			// the bug: an unposeable scalar set silently declined every
			// downstream question instead of answering the real ground.
			inner := abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
			out := abstractdomain.PossiblyNaN(inner)
			return &out
		}
	}
	// the URI handling functions (sec-uri-handling-functions):
	// decodeURI/decodeURIComponent run Decode, which THROWS a URIError
	// on a percent escape that is not two hex digits or spells bad
	// UTF-8 (sec-decodeuri-encodeduri,
	// sec-decodeuricomponent-encodeduricomponent); encodeURI/
	// encodeURIComponent run Encode, which throws on a lone surrogate.
	// On an exact string the host runs the spec's own algorithm — an
	// exact result, or the refutation in the text's own words. Anything
	// less exact still answers a STRING on every completing run.
	{
		var uriName string
		hasURIName := false
		if ast.IsIdentifier(call.Expression) {
			switch call.Expression.Text() {
			case "decodeURI", "decodeURIComponent", "encodeURI", "encodeURIComponent":
				if resolvesToDefaultLib(ctx, call.Expression) {
					uriName, hasURIName = call.Expression.Text(), true
				}
			}
		}
		if hasURIName && len(arguments) == 1 {
			spread := spreadArguments(arguments)
			if len(spread) > 0 {
				argument := spread[0]
				if argument.Kind == abstractdomain.KindValues && argument.KindTag == abstractdomain.PrimitiveString {
					text := stringOf(argument.Values)
					grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(argument), abstractdomain.TrustSpec)
					out, err := jsURIFunction(uriName, text)
					if err != nil {
						var message string
						if strings.HasPrefix(uriName, "decode") {
							message = "the text has a percent escape that isn't a well-formed " +
								"encoding — " + uriName + "() throws on it"
						} else {
							message = "the text has a lone surrogate — " + uriName + "() throws on it"
						}
						ctx.Report(assignability.At(arguments[0], 7001, message))
						residue := silence.Residue()
						return &residue
					}
					result := abstractdomain.KnownValues(refinementsets.CodepointsOf(out), abstractdomain.PrimitiveString, grade)
					return &result
				}
			}
			result := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
			return &result
		}
	}
	// the Number predicates — no coercion, so a non-number word answers
	// false outright; the exact rows are the spec's own
	if ast.IsPropertyAccessExpression(call.Expression) {
		pa := call.Expression.AsPropertyAccessExpression()
		if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Number" && resolvesToDefaultLib(ctx, pa.Expression) && len(arguments) == 1 {
			predicate := pa.Name().Text()
			argument := evaluateExpression(ctx, env, arguments[0])
			boolOf := func(v bool) *abstractdomain.AbstractValue {
				n := float64(0)
				if v {
					n = 1
				}
				out := abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveBoolean, abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(argument), abstractdomain.TrustSpec)) // transcribed spec rows
				return &out
			}
			if predicate == "isNaN" || predicate == "isFinite" || predicate == "isInteger" || predicate == "isSafeInteger" {
				if argument.Kind == abstractdomain.KindNaN {
					return boolOf(predicate == "isNaN")
				}
				if argument.Kind == abstractdomain.KindUndef || argument.Kind == abstractdomain.KindObject {
					return boolOf(false)
				}
				if argument.Kind == abstractdomain.KindValues {
					if argument.KindTag == abstractdomain.PrimitiveString || argument.KindTag == abstractdomain.PrimitiveArray {
						return boolOf(false)
					}
					if argument.KindTag == abstractdomain.PrimitiveBoolean {
						return boolOf(false)
					}
					if len(argument.Values) == 1 {
						x := argument.Values[0]
						switch predicate {
						case "isNaN":
							return boolOf(false) // an exact word is never NaN
						case "isFinite":
							return boolOf(!math.IsInf(x, 0))
						case "isInteger":
							return boolOf(x == math.Trunc(x) && !math.IsInf(x, 0))
						case "isSafeInteger":
							return boolOf(x == math.Trunc(x) && math.Abs(x) <= 9007199254740991)
						}
					}
				}
				out := silence.Residue()
				return &out
			}
			// a Number method outside the predicate rows — parseInt,
			// parseFloat and kin, each an exactly specified string
			// grammar nobody has modeled yet
			NoteUnmodeledCall(ctx, e)
			out := silence.Residue()
			return &out
		}
	}
	return nil
}

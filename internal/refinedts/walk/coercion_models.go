// from evaluation/coercion_models.ts
//
// The coercion models: ToNumber and the parse grammars at exact
// values, String()/Number(), parseInt/parseFloat, the Number
// predicates, and JSON.parse/JSON.stringify. Split from
// builtin_models.ts per the v2 tree.

package walk

import (
	"encoding/json"
	"math"
	"net/url"
	"strconv"
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

// jsFromCharCode is String.fromCharCode (sec-string.fromcharcode):
// each argument becomes the code unit whose numeric value is
// ℝ(? ToUint16(_next_)) and the result is their concatenation. False
// where the units spell a LONE surrogate — the code-point encoding
// cannot carry half a pair, so that shape keeps the sort-level answer
// instead of a silently wrong tuple.
func jsFromCharCode(values []float64) (string, bool) {
	units := make([]uint16, len(values))
	for i, v := range values {
		units[i] = jsToUint16(v)
	}
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF {
			if i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
				i++
				continue
			}
			return "", false
		}
		if u >= 0xDC00 && u <= 0xDFFF {
			return "", false
		}
	}
	return utf16ToString(units), true
}

// jsToUint16 is ToUint16 (sec-touint16): ToIntegerOrInfinity — NaN
// reads 0, the rest truncate — then ToFixedSizeInteger(int, ~unsigned~,
// 16): ±∞ read 0, the rest take modulo 2^16 (sec-tofixedsizeinteger).
func jsToUint16(v float64) uint16 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	truncated := math.Trunc(v)
	m := math.Mod(truncated, 65536)
	if m < 0 {
		m += 65536
	}
	return uint16(m)
}

// jsStringToNumber mirrors StringToNumber
// (sec-tonumber-applied-to-the-string-type) closely enough for this
// file's exact reads: JS's grammar (whitespace-trimmed, hex/octal/
// binary prefixes, Infinity, empty is 0) — Go's strconv.ParseFloat
// alone diverges on the 0x/0o/0b prefixes and the bare "Infinity"
// spelling, so those are special-cased first.
func jsStringToNumber(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, true
	}
	neg := false
	unsigned := trimmed
	if strings.HasPrefix(unsigned, "+") {
		unsigned = unsigned[1:]
	} else if strings.HasPrefix(unsigned, "-") {
		neg = true
		unsigned = unsigned[1:]
	}
	if unsigned == "Infinity" {
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "0x") || strings.HasPrefix(lower, "-0x") || strings.HasPrefix(lower, "+0x") {
		v, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(trimmed, "+"), "-"), "0x"), 16, 64)
		if err != nil {
			return 0, false
		}
		f := float64(v)
		if strings.HasPrefix(trimmed, "-") {
			f = -f
		}
		return f, true
	}
	if strings.HasPrefix(lower, "0o") {
		v, err := strconv.ParseUint(trimmed[2:], 8, 64)
		if err != nil {
			return 0, false
		}
		return float64(v), true
	}
	if strings.HasPrefix(lower, "0b") {
		v, err := strconv.ParseUint(trimmed[2:], 2, 64)
		if err != nil {
			return 0, false
		}
		return float64(v), true
	}
	v, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// jsParseFloat mirrors parseFloat (sec-parsefloat-string): the
// longest numeric prefix of the trimmed text, or NaN when none.
func jsParseFloat(text string) (float64, bool) {
	trimmed := strings.TrimLeft(text, " \t\n\r\v\f\u00A0\uFEFF")
	end := 0
	sawDigit := false
	sawDot := false
	sawExp := false
	i := 0
	if i < len(trimmed) && (trimmed[i] == '+' || trimmed[i] == '-') {
		i++
	}
	if strings.HasPrefix(trimmed[i:], "Infinity") {
		v := math.Inf(1)
		if strings.HasPrefix(trimmed, "-") {
			v = math.Inf(-1)
		}
		return v, true
	}
	for ; i < len(trimmed); i++ {
		c := trimmed[i]
		if c >= '0' && c <= '9' {
			sawDigit = true
			end = i + 1
			continue
		}
		if c == '.' && !sawDot && !sawExp {
			sawDot = true
			end = i + 1
			continue
		}
		if (c == 'e' || c == 'E') && sawDigit && !sawExp {
			// only consume the exponent marker if digits follow (with an
			// optional sign)
			j := i + 1
			if j < len(trimmed) && (trimmed[j] == '+' || trimmed[j] == '-') {
				j++
			}
			if j < len(trimmed) && trimmed[j] >= '0' && trimmed[j] <= '9' {
				sawExp = true
				i = j
				for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
					i++
				}
				end = i
				i--
				continue
			}
			break
		}
		break
	}
	if !sawDigit {
		return 0, false
	}
	v, err := strconv.ParseFloat(trimmed[:end], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// jsParseInt mirrors parseInt (sec-parseint-string-radix): a leading
// sign, an optional 0x/0X prefix selecting radix 16, the longest
// prefix of digits valid in the radix. radix 0 means "unspecified" —
// the TS caller's `null` case.
func jsParseInt(text string, radix int) (float64, bool) {
	trimmed := strings.TrimLeft(text, " \t\n\r\v\f\u00A0\uFEFF")
	neg := false
	i := 0
	if i < len(trimmed) && (trimmed[i] == '+' || trimmed[i] == '-') {
		neg = trimmed[i] == '-'
		i++
	}
	stripPrefix := radix == 0 || radix == 16
	if stripPrefix && i+1 < len(trimmed) && trimmed[i] == '0' && (trimmed[i+1] == 'x' || trimmed[i+1] == 'X') {
		i += 2
		radix = 16
	} else if radix == 0 {
		radix = 10
	}
	if radix < 2 || radix > 36 {
		return 0, false
	}
	digitValue := func(c byte) int {
		switch {
		case c >= '0' && c <= '9':
			return int(c - '0')
		case c >= 'a' && c <= 'z':
			return int(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			return int(c-'A') + 10
		default:
			return -1
		}
	}
	start := i
	for i < len(trimmed) {
		d := digitValue(trimmed[i])
		if d < 0 || d >= radix {
			break
		}
		i++
	}
	if i == start {
		return 0, false
	}
	digits := trimmed[start:i]
	result := 0.0
	for _, c := range []byte(digits) {
		result = result*float64(radix) + float64(digitValue(c))
	}
	if neg {
		result = -result
	}
	return result, true
}

// jsURIFunction runs decodeURI/decodeURIComponent/encodeURI/
// encodeURIComponent, mirroring the spec's percent-encoding grammar
// closely enough for this file's exact reads. Go's net/url quotes
// differently (space becomes "+" in QueryEscape, not "%20"), so
// PathEscape/PathUnescape stand in, with the reserved-character sets
// adjusted to each function's own list
// (sec-uri-handling-functions).
func jsURIFunction(name string, text string) (string, error) {
	switch name {
	case "decodeURIComponent", "decodeURI":
		return url.QueryUnescape(strings.ReplaceAll(text, "+", "%2B"))
	case "encodeURIComponent":
		return url.QueryEscape(text), nil
	case "encodeURI":
		// encodeURI leaves reserved/unescaped characters alone
		// (;/?:@&=+$,#), unlike encodeURIComponent
		var b strings.Builder
		for _, r := range text {
			if strings.ContainsRune(";/?:@&=+$,#-_.!~*'()", r) ||
				(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
				continue
			}
			b.WriteString(url.QueryEscape(string(r)))
		}
		return b.String(), nil
	}
	return "", nil
}

// readJsonMethods is readJsonMethods in the TS source: JSON.parse and
// JSON.stringify as method calls — parse runs BEFORE the schema-parse
// model in the dispatcher's chain, which would otherwise swallow the
// `parse` name.
func readJsonMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	isJSONReceiver := ast.IsIdentifier(receiverExpression) && receiverExpression.Text() == "JSON" && resolvesToDefaultLib(ctx, receiverExpression)
	if isJSONReceiver && method == "parse" && len(arguments) == 1 {
		// JSON.parse(JSON.stringify(x)): the round trip through the SAME
		// process never leaves the JS value domain — parse re-seeds
		// whatever set x itself carried (jsonRoundTripOf's own doc),
		// rather than routing through an intermediate exact string that
		// only an ALREADY-exact x could produce. Checked on the argument
		// EXPRESSION (the nested call's own shape), not on text's
		// evaluated kind, so a windowed (non-exact) x is recognized here
		// before falling to the exact-text or unknown-text arms below.
		if stringifyArgument, isStringifyCall := jsonStringifyCallArgument(arguments[0]); isStringifyCall {
			inner := evaluateExpression(ctx, env, stringifyArgument)
			if roundTripped, ok := jsonRoundTripOf(inner); ok {
				return &roundTripped
			}
		}
		text := evaluateExpression(ctx, env, arguments[0])
		if text.Kind == abstractdomain.KindValues && text.KindTag == abstractdomain.PrimitiveString {
			var parsed any
			err := json.Unmarshal([]byte(stringOf(text.Values)), &parsed)
			if err != nil {
				out := silence.Residue()
				return &out
			}
			grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(text), abstractdomain.TrustSpec)
			out := jsonToKnown(parsed, grade)
			return &out
		}
		// unknown text: the result is whatever JSON value the text
		// spells — sec-json.parse's own grammar is the answer (a
		// number, a string, a boolean, null, or an object — JSONNumber /
		// JSONString / JSONBooleanLiteral / JSONNullLiteral /
		// JSONObject in sec-json.parse's production, with a JSONArray
		// folding under the same object-shaped claim CheckObjectKnown
		// already reads for "a string or an array"). This is DETERMINED
		// knowledge — every arm names an admitted JSON shape — so a
		// narrower declared position (Unit's [0, 1], Code's
		// /^[A-Z]{2}$/, a literal-true type) refutes the mismatched
		// arms outright (CheckKindUnion), rather than sitting undetermined:
		// an undetermined can never be designated (TESTING-TENETS.md), and
		// "any JSON value narrower than the target" is exactly the sound
		// refusal sec-json.parse's own contract already earns.
		const jsonParseOfUnknownTextSaid = "JSON.parse of unknown text yields whatever JSON " +
			"value the text spells — the type is everything this " +
			"file determines"
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site:        "expression",
				Node:        e,
				Said:        jsonParseOfUnknownTextSaid,
				Unsupported: false,
			})
		}
		out := anyJSONValue(abstractdomain.TrustSpec)
		// the union is DETERMINED (every arm is a real claim, never
		// KindUnknown), so the ordinary ResidueReason carrier — read only
		// off KindUnknown elsewhere — does not apply to it directly; but
		// CheckKindUnion (maybe_and_union.go) now reads a KindKindUnion's
		// own ResidueReason too (abstract_value.go's doc: its second
		// carrier), on BOTH the refutation path (an arm demonstrably
		// fails, e.g. the object arm against a scalar target) and the
		// generic-alert fallback (every arm judged, none refuted) — so
		// this sentence surfaces at the diagnostic either way, naming
		// JSON.parse rather than the arm's own internal shape or the
		// bare AlertText.
		out.ResidueReason = jsonParseOfUnknownTextSaid
		return &out
	}
	// JSON.stringify on exactly known structure: the serialization is
	// exactly specified (sec-json.stringify — default replacer and
	// space, own keys in insertion order), so the host computes the
	// exact text. The undef marker conflates undefined with null — the
	// two serialize differently, so it declines.
	if isJSONReceiver && method == "stringify" && len(arguments) == 1 {
		argKnown := evaluateExpression(ctx, env, arguments[0])
		floor := abstractdomain.TrustSpec
		text, ok := jsonStringifyOf(argKnown, func(grade abstractdomain.TrustLevel) { floor = abstractdomain.MinTrustLevel(floor, grade) })
		if ok {
			out := abstractdomain.KnownValues(refinementsets.CodepointsOf(text), abstractdomain.PrimitiveString, floor)
			return &out
		}
		// sec-json.stringify: the result is a String OR UNDEFINED — an
		// undefined, function, or symbol root serializes to nothing.
		// tsc's lib types it plain `string`, and prisma's defensive
		// `typeof wire !== 'string'` guard folded dead off that lie
		// until this arm told the spec's truth.
		inner := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
		out := abstractdomain.PossiblyUndefined(inner, "", false, false)
		return &out
	}
	return nil
}

// jsonStringifyOf builds JSON.stringify's exact text BY HAND, in the
// object's own key order — encoding/json.Marshal on a map SORTS keys
// alphabetically, which cannot reproduce a TS object literal's field
// order (PORT.md). This walks the SAME shapes JsValueExact admits
// (float64, string, bool, absence, list, complete object), reading
// AbstractValue.Keys directly instead of routing through the
// order-losing map form. noteGrade threads the trust floor the same
// way JsValueOf's reading parameter does. Returns ("", false) where
// any part is looser than a single value, mirroring JsValueOf's
// NOT_EXACT.
func jsonStringifyOf(known abstractdomain.AbstractValue, noteGrade func(grade abstractdomain.TrustLevel)) (string, bool) {
	if noteGrade != nil {
		noteGrade(abstractdomain.TrustLevelOf(known))
	}
	switch known.Kind {
	case abstractdomain.KindValues:
		if known.KindTag == abstractdomain.PrimitiveString {
			encoded, err := json.Marshal(stringOf(known.Values))
			if err != nil {
				return "", false
			}
			return string(encoded), true
		}
		if known.KindTag == abstractdomain.PrimitiveArray {
			var b strings.Builder
			b.WriteByte('[')
			for i, v := range known.Values {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(jsonNumberString(v))
			}
			b.WriteByte(']')
			return b.String(), true
		}
		if known.KindTag == abstractdomain.PrimitiveBoolean {
			if len(known.Values) == 1 {
				if known.Values[0] != 0 {
					return "true", true
				}
				return "false", true
			}
			return "", false
		}
		if len(known.Values) == 1 {
			return jsonNumberString(known.Values[0]), true
		}
		return "", false
	case abstractdomain.KindUndef:
		return "", false // the marker conflates undefined (omitted) with null ("null") — not one exact text
	case abstractdomain.KindNaN:
		return "null", true // sec-json.stringify: NaN and ±Infinity serialize as "null"
	case abstractdomain.KindObject:
		if !known.Complete {
			return "", false
		}
		var b strings.Builder
		b.WriteByte('{')
		wroteAny := false
		for _, key := range known.Keys {
			// a SYMBOL slot (#sym:…, keyed_slot_reads.go) is not a
			// String-valued key — SerializeJSONObject never writes it
			if symbolSlotKey(key.Name) {
				continue
			}
			value, ok := jsonStringifyOf(key.Value, noteGrade)
			if !ok {
				return "", false
			}
			// undefined VALUES are OMITTED from an object's serialization
			// (sec-serializejsonproperty step 8), not written as "null"
			if key.Value.Kind == abstractdomain.KindUndef {
				continue
			}
			if wroteAny {
				b.WriteByte(',')
			}
			nameEncoded, err := json.Marshal(key.Name)
			if err != nil {
				return "", false
			}
			b.Write(nameEncoded)
			b.WriteByte(':')
			b.WriteString(value)
			wroteAny = true
		}
		b.WriteByte('}')
		return b.String(), true
	case abstractdomain.KindList:
		var b strings.Builder
		b.WriteByte('[')
		for i, item := range known.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			// undefined ITEMS in an array serialize as "null"
			// (sec-serializejsonarray step 8), never omitted
			if item.Kind == abstractdomain.KindUndef {
				b.WriteString("null")
				continue
			}
			value, ok := jsonStringifyOf(item, noteGrade)
			if !ok {
				return "", false
			}
			b.WriteString(value)
		}
		b.WriteByte(']')
		return b.String(), true
	default:
		// set, variable, possiblyUndefined, possiblyNaN, kindUnion,
		// objectStar, collection, promise, date, symbol, hostFunction,
		// bigints, regex, unknown: none of them pin one exact value. The
		// object-star in particular states no LENGTH, so there is not
		// even a position count to write brackets around.
		return "", false
	}
}

// jsonNumberString mirrors JS's JSON.stringify of a number: the
// spec-exact decimal form (Number::toString radix 10), with ±Infinity
// and NaN written "null" (sec-serializejsonproperty step 4 reads
// through Quote/ToString, and Number::toString has no representation
// for either — sec-json.stringify's own note says so explicitly).
func jsonNumberString(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "null"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// jsonToKnown is the TS source's inline toKnown: a decoded
// encoding/json value (float64 | string | bool | nil | []any |
// map[string]any) as AbstractValue knowledge, at the given grade.
func jsonToKnown(v any, grade abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	switch value := v.(type) {
	case float64:
		return abstractdomain.KnownValues([]float64{value}, abstractdomain.PrimitiveNumber, grade)
	case string:
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(value), abstractdomain.PrimitiveString, grade)
	case bool:
		n := float64(0)
		if value {
			n = 1
		}
		return abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveBoolean, grade)
	case nil:
		// JSON's null decodes as Go's untyped nil (encoding/json's own
		// mapping) — the parsed value is JS null (sec-json.parse's
		// JSONNullLiteral), never undefined; a record member holding
		// undefined is DROPPED before serialization (sec-
		// serializejsonproperty step 8) and so never reaches this
		// decode at all. Undef and Null read differently at a
		// possiblyUndefined target (RefutePossiblyAbsent's own
		// AbsentFlavorNullOnly branch), so the two are not
		// interchangeable here.
		return abstractdomain.AtTrustLevel(abstractdomain.Null, grade)
	case []any:
		items := make([]abstractdomain.AbstractValue, len(value))
		for i, item := range value {
			items[i] = jsonToKnown(item, grade)
		}
		return abstractdomain.KnownList(items, grade)
	case map[string]any:
		var keys []abstractdomain.ObjectKey
		for name, held := range value {
			keys = append(keys, abstractdomain.ObjectKey{Name: name, Value: jsonToKnown(held, grade)})
		}
		return abstractdomain.AtTrustLevel(abstractdomain.KnownObject(keys, nil, true, abstractdomain.TrustProved, false), grade)
	default:
		out := silence.Residue()
		return out
	}
}

// anyJSONValue is the determined claim sec-json.parse's own grammar
// makes about ANY value a successful parse can answer: a number, a
// string, a boolean, null, or an object (an array reads as an object
// too — sec-typeof-operator, and CheckObjectKnown's "a string or an
// array" wording already reads a bare KindObject arm that way against
// a sequence-shaped target). Every arm is DETERMINED (never Unknown),
// so KindUnionOf never collapses this to the residue it replaces —
// each arm instead judges on its own against the checked position
// (CheckKindUnion), and a target the JSON grammar cannot fit (Unit's
// [0, 1], a literal-true type, Code's pattern) is refuted through the
// arm that demonstrably fails, never left undetermined.
func anyJSONValue(grade abstractdomain.TrustLevel) abstractdomain.AbstractValue {
	numberGround := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(), nil, grade, abstractdomain.SetKindTagNone)
	stringGround := abstractdomain.KnownSet(refinementsets.Strings, nil, grade, abstractdomain.SetKindTagNone)
	booleanGround := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, grade)
	objectGround := abstractdomain.AtTrustLevel(abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustProved, false), grade)
	return abstractdomain.KindUnionOf([]abstractdomain.AbstractValue{
		numberGround,
		stringGround,
		booleanGround,
		abstractdomain.AtTrustLevel(abstractdomain.Null, grade),
		objectGround,
	})
}

// jsonStringifyCallArgument reports whether e is a call to
// JSON.stringify with exactly one argument, answering that argument's
// own expression node. Used to recognize JSON.parse(JSON.stringify(x))
// syntactically, at the argument-expression level — jsonRoundTripOf
// then reads x's OWN abstract value rather than the intermediate
// string jsonStringifyOf would have to reconstruct exactly.
//
// DELIBERATELY SYNTACTIC-NESTED-CALL-ONLY: a bare identifier bound
// through a CONST to `JSON.stringify(x)` earlier
// (`const encoded = JSON.stringify(v); … JSON.parse(encoded)`,
// B7.keep.join's own shape) is NOT resolved here, even though a const
// can never be REASSIGNED — because the object x itself can still be
// MUTATED IN PLACE between the stringify call and the later
// JSON.parse read (`v.a = v.a + 1`, B7.keep.write's own shape), and
// evaluating x's identifier at the LATER read site would silently
// read its post-mutation value while claiming it as the round trip of
// what was actually serialized earlier — a wrong answer, not merely
// an imprecision. A sound version of this widening needs a "no write
// to x's root name in any statement between the two points, across
// nested blocks" check (B7.keep.join's own const is declared OUTSIDE
// the if/else that later reads it, so a same-block-only or
// immediately-preceding-statement check is not enough either); no
// such statement-RANGE write-set utility exists yet in dataflowfacts
// or walk (AssignedNames/AssignedNameSet scan a whole subtree, not a
// range between two arbitrary points across block boundaries) — see
// AGENT-BRIEF.md.
func jsonStringifyCallArgument(e *ast.Node) (*ast.Node, bool) {
	if !ast.IsCallExpression(e) {
		return nil, false
	}
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil, false
	}
	pa := call.Expression.AsPropertyAccessExpression()
	if !ast.IsIdentifier(pa.Expression) || pa.Expression.Text() != "JSON" || pa.Name().Text() != "stringify" {
		return nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return nil, false
	}
	return call.Arguments.Nodes[0], true
}

// jsonRoundTripOf answers JSON.parse(JSON.stringify(value)) read AT
// value's own set, without ever materializing the intermediate text —
// sec-json.stringify then sec-json.parse compose to the identity on
// every value already exactly known (jsonStringifyOf/jsonToKnown
// already round-trip those losslessly; ok=false there defers to that
// existing exact-text path). What this adds is the WINDOWED case
// jsonStringifyOf cannot spell as one exact string: a scalar number
// set survives the trip unchanged (Number::toString then ToNumber is
// the identity on every finite double — sec-numeric-types-number-
// tostring, sec-json.parse's JSONNumber production), and an object's
// keys recurse the same way, with an undefined-valued key DROPPED
// (sec-serializejsonproperty step 8) rather than carried through.
// grade floors at TrustSpec, the same boundary the exact-text round
// trip already stamps (json.Unmarshal's own text crossing).
func jsonRoundTripOf(value abstractdomain.AbstractValue) (abstractdomain.AbstractValue, bool) {
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(value), abstractdomain.TrustSpec)
	switch value.Kind {
	case abstractdomain.KindSet:
		if value.SetKindTag != abstractdomain.SetKindTagNone {
			return abstractdomain.AbstractValue{}, false
		}
		// a string ground or pattern set round-trips through JSON the
		// same identity way a number window does (Quote then the
		// matching string literal production reads back the same
		// codepoints, sec-quotejsonstring / sec-json.parse's
		// JSONString) — KindOfClaim reads the SORT the set is grounded
		// on the identical way typeof discrimination does (a KindSet
		// never claims boolean — setSortOfForms answers only string,
		// number, or none), so only a numeric or string ground takes
		// this identity arm; anything else (a symbol-tagged set, or a
		// set this reader cannot sort) keeps the caller's existing
		// exact-text path.
		switch abstractdomain.KindOfClaim(value) {
		case abstractdomain.ClaimSortNumber, abstractdomain.ClaimSortString:
			return abstractdomain.KnownSet(value.Set, value.Temporal, grade, value.SetKindTag), true
		}
		return abstractdomain.AbstractValue{}, false
	case abstractdomain.KindObject:
		if !value.Complete {
			return abstractdomain.AbstractValue{}, false
		}
		var keys []abstractdomain.ObjectKey
		for _, key := range value.Keys {
			if symbolSlotKey(key.Name) {
				continue // never a String-valued key SerializeJSONObject writes
			}
			if key.Value.Kind == abstractdomain.KindUndef {
				continue // dropped, not written (sec-serializejsonproperty step 8)
			}
			held, ok := jsonRoundTripOf(key.Value)
			if !ok {
				if text, exact := jsonStringifyOf(key.Value, nil); exact {
					var parsed any
					if err := json.Unmarshal([]byte(text), &parsed); err == nil {
						held = jsonToKnown(parsed, grade)
						ok = true
					}
				}
			}
			if !ok {
				return abstractdomain.AbstractValue{}, false
			}
			keys = append(keys, abstractdomain.ObjectKey{Name: key.Name, Value: held})
		}
		return abstractdomain.AtTrustLevel(abstractdomain.KnownObject(keys, nil, true, abstractdomain.TrustProved, false), grade), true
	default:
		return abstractdomain.AbstractValue{}, false
	}
}

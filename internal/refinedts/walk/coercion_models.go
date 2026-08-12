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
			inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(), nil, grade, abstractdomain.SetKindTagNone)
			out := abstractdomain.PossiblyNaN(inner)
			return &out
		}
		inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
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
			inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(), nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
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
		// spells — sec-json.parse admits no more, and the set language
		// says no less than that, so this is everything the file
		// determines here
		if assignability.CollectingReasons() {
			assignability.NoteReason(assignability.ReasonNote{
				Site: "expression",
				Node: e,
				Said: "JSON.parse of unknown text yields whatever JSON " +
					"value the text spells — the type is everything this " +
					"file determines",
				Unsupported: false,
			})
		}
		out := silence.Residue()
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
		// collection, promise, date, symbol, hostFunction, bigints,
		// regex, unknown: none of them pin one exact value
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
		return abstractdomain.AtTrustLevel(abstractdomain.Undef, grade)
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

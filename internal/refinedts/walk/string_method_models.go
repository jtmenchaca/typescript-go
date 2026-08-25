// from evaluation/string_method_models.ts
//
// The string-method models: host-evaluated reads on exact tuples (the
// host running the check is the host that will run the program), the
// kind-preserving fallbacks the spec pins even where the value is
// unknown, and .match through a const-bound regex literal. Split
// from builtin_models.ts per the v2 tree.
//
// JS string reads are UTF-16 facts; model strings are scalar-value
// tuples (TERMS-v2, Semantic Rulings). `.length` counts code units,
// so an astral scalar counts twice — exact for a known tuple. Unit-
// indexed reads (`s[i]`, `.at`, `.slice`) coincide with scalar
// positions only on astral-free tuples; elsewhere they can shift or
// split a surrogate pair, and the answer is unknown, never a guess.
//
// The case-mapping/trim rows live in string_method_models_case.go,
// the regex-facing rows in string_method_models_regex.go, the
// replace-family assembly in string_method_models_replace.go, and the
// UTF-16 code-unit primitives in string_method_models_utf16.go.

package walk

import (
	"math"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// stringOutMethods is STRING_OUT_METHODS in the TS source: the string
// reads whose RESULT is a string again — the sort the spec pins even
// when the value is unknown.
var stringOutMethods = map[string]struct{}{
	"toUpperCase": {}, "toLowerCase": {}, "trim": {}, "trimStart": {}, "trimEnd": {},
	// trimLeft/trimRight are the Annex B names for the SAME function
	// objects as trimStart/trimEnd (String.prototype.trimleft,
	// String.prototype.trimright)
	"trimLeft": {}, "trimRight": {},
	"replace": {}, "replaceAll": {}, "charAt": {}, "padStart": {}, "padEnd": {},
	"repeat": {}, "slice": {}, "substring": {},
	// normalize returns a string in the named Unicode Normalization
	// Form (sec-string.prototype.normalize)
	"normalize": {},
}

// exactStringOf is the TS source's inline exactString: an exact
// string word's text, or ("", false).
func exactStringOf(k abstractdomain.AbstractValue) (string, bool) {
	if k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveString {
		return stringOf(k.Values), true
	}
	return "", false
}

// exactIntOf is the TS source's inline exactInt: an exact integer
// word's value, or (0, false).
func exactIntOf(k abstractdomain.AbstractValue) (int, bool) {
	if k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveNumber &&
		len(k.Values) == 1 && k.Values[0] == float64(int64(k.Values[0])) {
		return int(k.Values[0]), true
	}
	return 0, false
}

// repeatCountLimit is how many counts a `.repeat` image is spelled one
// at a time. Each count contributes one word to a union, so this also
// bounds the width of the set built below.
const repeatCountLimit = 16

// repeatCountMembers is the finite list of NON-NEGATIVE INTEGER counts
// a `.repeat` argument holds, read through the kernel's own proved
// membership enumeration — the same door SimplifyScalar's members
// reading uses. (nil, false) where the value is not a plain scalar set,
// where no kernel is seated, where the set holds more counts than the
// limit, or where any member is negative or fractional (a count
// sec-string.prototype.repeat step 3 raises a RangeError for, which
// carries no value for the image to hold).
func repeatCountMembers(ctx *FlowContext, count abstractdomain.AbstractValue) (members []int, ok bool) {
	if count.Kind != abstractdomain.KindSet || count.SetKindTag != abstractdomain.SetKindTagNone {
		return nil, false
	}
	if ctx == nil || ctx.Kernel == nil || ctx.Kernel.Members == nil {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			members, ok = nil, false
		}
	}()
	// repeat TRUNCATES its count: sec-string.prototype.repeat step 2
	// runs ToIntegerOrInfinity, which drops the fractional part. So the
	// image over a count window is the image over the INTEGERS in that
	// window, and a real-valued window like [0, 3] names the same four
	// results {0, 1, 2, 3} do. Conjoining Integer here is what makes the
	// window finite enough to enumerate at all.
	integral := count.Set
	integral.Forms = append(append([]refinementsets.Refinement{}, integral.Forms...), refinementsets.Integer)
	raw := ctx.Kernel.Members(integral, repeatCountLimit)
	if len(raw) == 0 || len(raw) > repeatCountLimit {
		return nil, false
	}
	out := make([]int, 0, len(raw))
	for _, value := range raw {
		if value < 0 || value != math.Trunc(value) {
			return nil, false
		}
		out = append(out, int(value))
	}
	return out, true
}

// unionOfExactStrings folds a list of exact string words into ONE set
// value spelling exactly those words — the union tree WordTuplesOf
// reads back. Every input must be an exact string word; anything else
// answers (zero, false) and the caller keeps the answer it had.
func unionOfExactStrings(words []abstractdomain.AbstractValue, grade abstractdomain.TrustLevel) (abstractdomain.AbstractValue, bool) {
	if len(words) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	if len(words) == 1 {
		return words[0], true
	}
	var folded refinementsets.RefinedSet
	for i, word := range words {
		text, textOk := exactStringOf(word)
		if !textOk {
			return abstractdomain.AbstractValue{}, false
		}
		tuple := refinementsets.StringTuple(text)
		if i == 0 {
			folded = tuple
			continue
		}
		folded = refinementsets.MakeRefinedSet(refinementsets.Union(folded, tuple))
	}
	return abstractdomain.KnownSet(folded, nil, grade, abstractdomain.SetKindTagNone), true
}

// readStringMethods is readStringMethods in the TS source: the string
// reads under the read-only gate: exact answers on an exact tuple,
// kind-preserving answers on a string-kinded receiver the analysis
// cannot pin. Nil where no string row speaks.
func readStringMethods(site MethodCallSite, argKnowns []abstractdomain.AbstractValue, receiverStringy bool, oracleGrade abstractdomain.TrustLevel) *abstractdomain.AbstractValue {
	ctx, e, receiver, method := site.Ctx, site.E, site.Receiver, site.Method
	// a slice/substring index whose window still admits the search
	// sentinel: indexOf answers an integer at least −1, and a slice
	// built on the not-found −1 silently reads from the wrong place —
	// the guard `!== −1` is what discharges it (the findIndex sentinel
	// class, at the read side). A FINDING, not an undetermined verdict:
	// the read's value still determines (a slice of a string is a
	// string, whatever the window), so the sort rows below keep
	// serving — this row only names the unguarded sentinel.
	if method == "slice" || method == "substring" {
		for _, argument := range argKnowns {
			if argument.Kind != abstractdomain.KindSet || argument.SetKindTag != abstractdomain.SetKindTagNone {
				continue
			}
			window := RangeOfSet(argument.Set)
			if window != nil && window.Int && window.Lo == -1 {
				ctx.Report(assignability.At(
					e, 7001,
					"an index may be a search's -1 (not found) — guard it with "+
						"!== -1 before "+method,
				))
				break
			}
		}
	}
	outString := func(s string) abstractdomain.AbstractValue {
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(s), abstractdomain.PrimitiveString, oracleGrade)
	}
	boolOf := func(v bool) abstractdomain.AbstractValue {
		n := float64(0)
		if v {
			n = 1
		}
		return abstractdomain.KnownValues([]float64{n}, abstractdomain.PrimitiveBoolean, oracleGrade)
	}
	outNumber := func(v float64) abstractdomain.AbstractValue {
		return abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, oracleGrade)
	}
	// Number.prototype.toString (sec-numeric-types-number-tostring): a
	// NUMERIC receiver spells its radix-r digits, lowercase letters past
	// '9'. An exact integer receiver computes the exact spelling through
	// the host's own formatter; a non-negative integer WINDOW answers
	// the digit-counted repetition over the radix alphabet — the same
	// transfer numericSetText already gives String(n) at radix 10. The
	// radix must be an exact integer in [2, 36] (absent is 10);
	// anything else falls through to the sort answer.
	if method == "toString" && !receiverStringy && len(argKnowns) <= 1 {
		radix, radixOk := 10, len(argKnowns) == 0
		if len(argKnowns) == 1 {
			if r, ok := exactIntOf(argKnowns[0]); ok && r >= 2 && r <= 36 {
				radix, radixOk = r, true
			}
		}
		if radixOk {
			grade := abstractdomain.MinTrustLevel(oracleGrade, abstractdomain.TrustSpec)
			if receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveNumber {
				allInt := true
				texts := make([]string, 0, len(receiver.Values))
				for _, v := range receiver.Values {
					if v != math.Trunc(v) || math.Abs(v) > float64(int64(1)<<53) {
						allInt = false
						break
					}
					texts = append(texts, strconv.FormatInt(int64(v), radix))
				}
				if allInt && len(texts) == 1 {
					out := outString(texts[0])
					return &out
				}
				if allInt && len(texts) > 1 {
					set := refinementsets.StringTuple(texts[0])
					for i := 1; i < len(texts); i++ {
						set = unionOf(set, refinementsets.StringTuple(texts[i]))
					}
					out := abstractdomain.KnownSet(set, nil, grade, abstractdomain.SetKindTagNone)
					return &out
				}
			}
			if receiver.Kind == abstractdomain.KindSet && receiver.SetKindTag == abstractdomain.SetKindTagNone &&
				refinementsets.OnOneTupleLayer(receiver.Set) {
				out := abstractdomain.KnownSet(numericSetRadixText(receiver.Set, radix), nil, grade, abstractdomain.SetKindTagNone)
				return &out
			}
		}
	}
	// the spec-exact string reads, computed ON THE EXACT TUPLE by
	// round-tripping through the host's own string functions (the
	// template-span rule: the host running the check is the host that
	// will run the program, so its Unicode tables and unit indexing ARE
	// the runtime's — astral words included)
	if receiverStringy && receiver.Kind == abstractdomain.KindValues {
		text := stringOf(receiver.Values)
		call := e.AsCallExpression()
		var arguments []*ast.Node
		if call.Arguments != nil {
			arguments = call.Arguments.Nodes
		}
		if len(arguments) == 0 {
			// String.prototype.toString on a string is the string itself
			// (sec-string.prototype.tostring: Return ? ThisStringValue(*this*
			// value)) — the receiver rides unchanged, with no text
			// round-trip, so even a lone-surrogate tuple stays exact
			if method == "toString" {
				out := abstractdomain.AtTrustLevel(receiver, oracleGrade)
				return &out
			}
			if result, ok := exactZeroArgStringRow(method, text); ok {
				out := outString(result)
				return &out
			}
		}
		// split on an exact separator: the pieces are exactly
		// specified (sec-string.prototype.split — SplitMatch by code
		// units), so the host computes them. A NON-EMPTY separator
		// never cuts a surrogate pair the receiver's spelling didn't
		// already expose; the empty separator unit-splits, so it stays
		// exact only on astral-free receivers.
		if method == "split" && len(argKnowns) >= 1 {
			separator, sepOk := exactStringOf(argKnowns[0])
			limit, hasLimit := -1, false
			if len(argKnowns) >= 2 {
				limit, hasLimit = exactIntOf(argKnowns[1])
			}
			if sepOk && (len(separator) > 0 || refinementsets.AstralFree(receiver.Values)) &&
				(len(argKnowns) < 2 || hasLimit) {
				var pieces []string
				if separator == "" {
					pieces = splitEmpty(text)
				} else {
					pieces = strings.Split(text, separator)
				}
				if hasLimit && limit < len(pieces) {
					if limit < 0 {
						pieces = nil
					} else {
						pieces = pieces[:limit]
					}
				}
				items := make([]abstractdomain.AbstractValue, len(pieces))
				for i, piece := range pieces {
					items[i] = outString(piece)
				}
				out := abstractdomain.KnownList(items, oracleGrade)
				return &out
			}
		}
		// an exact string matched against an exact regex: the match
		// semantics are transcribed (sec-string.prototype.match via
		// RegExp.prototype[@@match]), so the host computes the exact
		// result — a miss answers exactly the NULL value (RegExp.prototype
		// [%Symbol.match%] step 5.a delegates the non-global case to
		// RegExpExec, whose own clause — sec-regexp.prototype.exec —
		// states "returns an Array... or *null* if string did not match";
		// the global case's own repeat loop, step 6.d.i, returns the
		// literal "*null*" directly on the first no-match)
		if method == "match" && len(argKnowns) == 1 {
			pattern := argKnowns[0]
			// a STICKY pattern reads the regex object's mutable
			// lastIndex, which the walk does not track — only global
			// and plain patterns start from a pinned position
			// (sec-string.prototype.match sets lastIndex to 0 for
			// global; a fresh plain regex starts at 0)
			if pattern.Kind == abstractdomain.KindRegex && !strings.Contains(pattern.Flags, "y") {
				compiled, ok := regexToGo(pattern.Source, pattern.Flags)
				if !ok {
					out := silence.Residue()
					return &out
				}
				result := compiled.FindStringSubmatch(text)
				if result == nil {
					out := abstractdomain.AtTrustLevel(abstractdomain.Null, oracleGrade)
					return &out
				}
				words := make([]abstractdomain.AbstractValue, len(result))
				for i, word := range result {
					words[i] = abstractdomain.KnownValues(refinementsets.CodepointsOf(word), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
				}
				list := abstractdomain.KnownList(words, oracleGrade)
				// the pattern's NAMED groups name the same exact values a
				// second time, under the result's `groups` object
				// (sec-regexpbuiltinexec step 34)
				if spans, spansOk := captureGroupSourceSpans(pattern.Source); spansOk && len(spans)+1 == len(words) {
					list = withNamedGroups(list, pattern.Source, spans, words[1:], oracleGrade)
				}
				out := list
				return &out
			}
		}
		// an exact replace: pattern a string or a pinned regex,
		// replacement a plain string — GetSubstitution is spec-exact
		// (sec-string.prototype.replace), so the host computes it.
		// replaceAll walks the same GetSubstitution over every
		// occurrence (sec-string.prototype.replaceall), so it folds by
		// the same argument.
		if (method == "replace" || method == "replaceAll") && len(argKnowns) == 2 {
			replacement, replOk := exactStringOf(argKnowns[1])
			if replOk {
				pattern := argKnowns[0]
				if literal, literalOk := exactStringOf(pattern); literalOk {
					var out abstractdomain.AbstractValue
					if method == "replace" {
						out = outString(strings.Replace(text, literal, replacement, 1))
					} else {
						out = outString(strings.ReplaceAll(text, literal, replacement))
					}
					return &out
				}
				// replaceAll THROWS on a global-less regex, so only the
				// replace arm folds pinned regexes
				if method == "replace" && pattern.Kind == abstractdomain.KindRegex && !strings.Contains(pattern.Flags, "y") {
					compiled, ok := regexToGo(pattern.Source, pattern.Flags)
					if !ok {
						out := silence.Residue()
						return &out
					}
					out := outString(compiled.ReplaceAllString(text, goReplacementOf(replacement)))
					return &out
				}
			}
			// a FUNCTION replacer at an exact string pattern: the spec
			// computes each replacement as ? ToString(? Call(_replaceValue_,
			// *undefined*, « _searchString_, 𝔽(_position_), _string_ »)) —
			// once at the first match (sec-string.prototype.replace) or
			// once per match position, advancing by max(1, _searchLength_)
			// (sec-string.prototype.replaceall) — and the functional path
			// runs NO GetSubstitution: the returned text lands literally.
			// With the replacer's body inline, each call runs with those
			// exact arguments; an exact string answer at every position
			// assembles the exact result. Anything less exact keeps the
			// readings below (the sort-level string-out row still speaks).
			if literal, literalOk := exactStringOf(argKnowns[0]); literalOk {
				replacer := Unwrapped(call.Arguments.Nodes[1])
				if replacer != nil && !ast.IsArrowFunction(replacer) && !ast.IsFunctionExpression(replacer) {
					// a NAMED replacer — the same fallback promiseHandlerOf
					// takes for a settlement handler passed by name:
					// follow it to its declaration when the resolver can
					// reach one. FunctionInReach/PinnedFunctionOf answers a
					// FunctionDeclaration node here (a top-level `function
					// swap(...) {...}`), never an arrow or function
					// expression, so the gate right below must admit that
					// shape too or this resolution can never actually be
					// used — inlineReplacerCall itself is already
					// declaration-shape-agnostic (Parameters()/Body() read
					// the same off all three function node kinds).
					if named := FunctionInReach(ctx, call.Arguments.Nodes[1]); named != nil {
						replacer = named
					}
				}
				if replacer != nil && (ast.IsArrowFunction(replacer) || ast.IsFunctionExpression(replacer) || ast.IsFunctionDeclaration(replacer)) && replacer.Body() != nil {
					floor := oracleGrade
					result, ok := replaceWithFunctionResult(text, literal, method == "replaceAll", func(matched string, position int) (string, bool) {
						answered := inlineReplacerCall(ctx, site.Env, replacer, matched, position, text)
						if answered.Kind == abstractdomain.KindValues && answered.KindTag == abstractdomain.PrimitiveString {
							floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(answered))
							return stringOf(answered.Values), true
						}
						return "", false
					})
					if ok {
						out := abstractdomain.KnownValues(refinementsets.CodepointsOf(result), abstractdomain.PrimitiveString, floor)
						return &out
					}
				}
			}
		}
		if (method == "includes" || method == "startsWith" || method == "endsWith" ||
			method == "indexOf" || method == "lastIndexOf") && len(argKnowns) == 1 {
			needle, ok := exactStringOf(argKnowns[0])
			if ok {
				switch method {
				case "includes":
					out := boolOf(strings.Contains(text, needle))
					return &out
				case "startsWith":
					out := boolOf(strings.HasPrefix(text, needle))
					return &out
				case "endsWith":
					out := boolOf(strings.HasSuffix(text, needle))
					return &out
				case "indexOf":
					out := outNumber(float64(strings.Index(text, needle)))
					return &out
				case "lastIndexOf":
					out := outNumber(float64(strings.LastIndex(text, needle)))
					return &out
				}
			}
		}
		// `.normalize(form)` on an exact string
		// (sec-string.prototype.normalize): the Unicode Normalization
		// Form the argument names, applied to the receiver — a pure
		// function of the two, so the host computes it. The default with
		// no argument is NFC (step 3). An unrecognized form THROWS a
		// RangeError (step 5), so it carries no value; that shape falls
		// through to the sort-level answer rather than being claimed.
		if method == "normalize" && len(argKnowns) <= 1 {
			form := "NFC"
			formOk := true
			if len(argKnowns) == 1 {
				form, formOk = exactStringOf(argKnowns[0])
			}
			if formOk {
				if normalized, ok := normalizedText(form, text); ok {
					out := outString(normalized)
					return &out
				}
			}
		}
		if method == "repeat" && len(argKnowns) == 1 {
			n, ok := exactIntOf(argKnowns[0])
			// a negative count THROWS rather than returning — the
			// reader carries values, not exceptions
			if ok && n >= 0 {
				out := outString(strings.Repeat(text, n))
				return &out
			}
			// a count the flow bounded to a SMALL INTEGER SET (`0 <= n &&
			// n <= 3`) names finitely many results, one per member —
			// sec-string.prototype.repeat is a function of the count, so
			// the image of a finite count set is the finite set of those
			// repetitions. The members come from the kernel's own proved
			// membership enumeration, so this claims exactly the counts
			// the set holds and no others; a set it will not enumerate,
			// or one holding a negative count (which throws), falls
			// through to the sort-level answer below.
			if counts, countsOk := repeatCountMembers(ctx, argKnowns[0]); countsOk {
				words := make([]abstractdomain.AbstractValue, 0, len(counts))
				for _, count := range counts {
					words = append(words, outString(strings.Repeat(text, count)))
				}
				if joined, joinedOk := unionOfExactStrings(words, oracleGrade); joinedOk {
					return &joined
				}
			}
		}
		if (method == "charAt" || method == "charCodeAt" || method == "codePointAt") && len(argKnowns) <= 1 {
			i, iOk := 0, true
			if len(argKnowns) == 1 {
				i, iOk = exactIntOf(argKnowns[0])
			}
			if iOk {
				units := utf16UnitsOf(text)
				if method == "charAt" {
					var out abstractdomain.AbstractValue
					if i >= 0 && i < len(units) {
						out = outString(utf16ToString([]uint16{units[i]}))
					} else {
						out = outString("")
					}
					return &out
				}
				if method == "charCodeAt" {
					if i < 0 || i >= len(units) {
						out := abstractdomain.AtTrustLevel(abstractdomain.NaNValue, oracleGrade)
						return &out
					}
					out := outNumber(float64(units[i]))
					return &out
				}
				// codePointAt
				point, ok := codePointAtUTF16(units, i)
				if !ok {
					out := abstractdomain.AtTrustLevel(abstractdomain.Undef, oracleGrade)
					return &out
				}
				out := outNumber(float64(point))
				return &out
			}
		}
		if (method == "padStart" || method == "padEnd") && len(argKnowns) >= 1 && len(argKnowns) <= 2 {
			n, nOk := exactIntOf(argKnowns[0])
			pad, padOk := " ", true
			if len(argKnowns) == 2 {
				pad, padOk = exactStringOf(argKnowns[1])
			}
			if nOk && padOk {
				var out abstractdomain.AbstractValue
				if method == "padStart" {
					out = outString(jsPadStart(text, n, pad))
				} else {
					out = outString(jsPadEnd(text, n, pad))
				}
				return &out
			}
		}
		if (method == "slice" || method == "substring") && len(argKnowns) <= 2 {
			window := make([]int, len(argKnowns))
			every := true
			for i, k := range argKnowns {
				n, ok := exactIntOf(k)
				if !ok {
					every = false
					break
				}
				window[i] = n
			}
			if every {
				units := utf16UnitsOf(text)
				var a, b int
				if len(window) >= 1 {
					a = window[0]
				}
				b = len(units)
				if len(window) >= 2 {
					b = window[1]
				}
				var out abstractdomain.AbstractValue
				if method == "slice" {
					out = outString(utf16ToString(jsSlice(units, a, b)))
				} else {
					// substring clamps and SWAPS a reversed window
					// (sec-string.prototype.substring); the host computes
					// both
					out = outString(utf16ToString(jsSubstring(units, a, b)))
				}
				return &out
			}
		}
		// split on an exact separator is the exact LIST of parts; with
		// no separator, the one-part list
		if method == "split" && len(argKnowns) <= 1 {
			if len(argKnowns) == 0 {
				item := abstractdomain.KnownValues(receiver.Values, abstractdomain.PrimitiveString, oracleGrade)
				out := abstractdomain.KnownList([]abstractdomain.AbstractValue{item}, abstractdomain.TrustProved)
				return &out
			}
			separator, ok := exactStringOf(argKnowns[0])
			if ok {
				var pieces []string
				if separator == "" {
					pieces = splitEmpty(text)
				} else {
					pieces = strings.Split(text, separator)
				}
				items := make([]abstractdomain.AbstractValue, len(pieces))
				for i, part := range pieces {
					items[i] = abstractdomain.KnownValues(refinementsets.CodepointsOf(part), abstractdomain.PrimitiveString, oracleGrade)
				}
				out := abstractdomain.KnownList(items, abstractdomain.TrustProved)
				return &out
			}
		}
	}
	// a STRING-SORTED receiver the walk cannot pin exactly: the read's
	// RESULT SORT is still the spec's — a string comes out (split hands
	// an array of strings, an index sits at -1 or above) even where the
	// value stays unknown, so the chain downstream keeps computing
	// instead of going dark
	if receiverStringy && receiver.Kind != abstractdomain.KindValues {
		call := e.AsCallExpression()
		var arguments []*ast.Node
		if call.Arguments != nil {
			arguments = call.Arguments.Nodes
		}
		// .match with a regex literal: null with no match, else the
		// match array (sec-string.prototype.match,
		// sec-regexp.prototype-%symbol.match%). The global flag collects
		// the match STRINGS; without it the array holds the matched
		// substring then one slot per capture group — a string where the
		// group must participate in any match, a string or undefined
		// where a quantifier, alternation, or lookaround can leave it
		// unset.
		if method == "match" && len(arguments) == 1 && ast.IsRegularExpressionLiteral(arguments[0]) {
			source := arguments[0].Text()
			lastSlash := strings.LastIndex(source, "/")
			flags := source[lastSlash+1:]
			if strings.Contains(flags, "g") {
				inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Star(refinementsets.Strings)), nil, oracleGrade, abstractdomain.SetKindTagNone)
				out := abstractdomain.PossiblyUndefined(inner, "", false, false)
				return &out
			}
			// each capturing group's own slot carries the LANGUAGE its
			// sub-pattern denotes (captureGroupSetOf, the same door
			// `.regex` and `.exec` compile through), not the bare string
			// sort — /(?<digits>\d+)/ names a digit grammar, and the
			// result's `.groups` names the same value under the group's
			// own name (sec-regexpbuiltinexec step 34).
			pattern := source[1:lastSlash]
			spans, ok := captureGroupSourceSpans(pattern)
			if ok {
				stringOut := abstractdomain.KnownSet(refinementsets.Strings, nil, oracleGrade, abstractdomain.SetKindTagNone)
				elements := captureGroupElements(pattern, flags, spans, oracleGrade)
				items := append([]abstractdomain.AbstractValue{stringOut}, elements...)
				inner := withNamedGroups(abstractdomain.KnownList(items, abstractdomain.TrustProved), pattern, spans, elements, oracleGrade)
				out := abstractdomain.PossiblyUndefined(inner, "", false, false)
				return &out
			}
			out := abstractdomain.PossiblyUndefined(silence.Residue(), "", false, false)
			return &out
		}
		// String.prototype.toString on a string-sorted receiver is the
		// receiver itself (sec-string.prototype.tostring) — identity
		// keeps the whole SET, not just the sort
		if method == "toString" && len(arguments) == 0 {
			out := receiver
			return &out
		}
		// `.slice(0, n)` over a sequence-SHAPED set (a concatenation or
		// repetition form, not an exact value): the kernel's prefix read
		// answers take-n of every member — a repetition window over the
		// folded alphabet, length [min lo n, n]. JS `.slice(0, n)` on a
		// shorter string returns the whole string, the same clamping the
		// kernel's window already states, so this is faithful for THIS
		// shape only — a non-zero start or an unrecognized n keeps
		// falling through to the sort-level answer below.
		if method == "slice" && len(arguments) == 2 && receiver.Kind == abstractdomain.KindSet &&
			receiver.SetKindTag == abstractdomain.SetKindTagNone {
			start, startOk := exactIntOf(argKnowns[0])
			n, nOk := exactIntOf(argKnowns[1])
			if startOk && start == 0 && nOk && n >= 0 {
				if prefixSet, ok := ctx.Kernel.SeqPrefix(receiver.Set, n); ok {
					out := abstractdomain.KnownSet(prefixSet, nil, oracleGrade, abstractdomain.SetKindTagNone)
					return &out
				}
			}
		}
		// `.padStart(n, p)` over a repetition-SHAPED set (repeat(S, lo,
		// hi), not an exact value) with an exact integer targetLength and
		// a literal padString: StringPad only pads when maxLength exceeds
		// the receiver's length (sec-stringpad step 2 — "If maxLength <=
		// stringLength, return string"), so where the window's own lower
		// bound already meets n, EVERY length the window admits already
		// clears the threshold and the receiver rides back unchanged. Where
		// the lower bound falls short, the pad prefix's own length is
		// exactly maxLength - stringLength (step 4) — a window of [0, n -
		// lo] over the shorter members and 0 over the ones already at or
		// past n — so repeat(codepoints-of-p, 0, n-lo) concatenated ahead
		// of the receiver's own shape is the over-approximation: sound at
		// every length the receiver's window admits, exact only where lo
		// alone already decides the outcome (the repeat(Digits,4,4)
		// through padStart(4, "0") case: lo == hi == n, so the receiver
		// rides back exactly unchanged).
		if method == "padStart" && len(argKnowns) >= 1 && len(argKnowns) <= 2 &&
			receiver.Kind == abstractdomain.KindSet && receiver.SetKindTag == abstractdomain.SetKindTagNone {
			n, nOk := exactIntOf(argKnowns[0])
			pad, padOk := " ", true
			if len(argKnowns) == 2 {
				pad, padOk = exactStringOf(argKnowns[1])
			}
			if nOk && padOk && n >= 0 {
				if repeated, ok := refinementsets.AsRepetition(receiver.Set); ok {
					// an empty fillString never pads, whatever the length
					// (sec-stringpad step 3: "If fillString is the empty
					// String, return string") — the receiver rides back
					// unchanged rather than concatenating an unsatisfiable
					// empty alphabet
					if repeated.Lo >= n || pad == "" {
						out := abstractdomain.KnownSet(receiver.Set, nil, oracleGrade, abstractdomain.SetKindTagNone)
						return &out
					}
					padWindowHi := n - repeated.Lo
					padAlphabet := refinementsets.MakeRefinedSet(refinementsets.OneOf(refinementsets.CodepointsOf(pad)))
					padPrefix := refinementsets.Repetition(padAlphabet, 0, &padWindowHi)
					concatenated := refinementsets.MakeRefinedSet(refinementsets.Concatenation(padPrefix, receiver.Set))
					out := abstractdomain.KnownSet(concatenated, nil, oracleGrade, abstractdomain.SetKindTagNone)
					return &out
				}
			}
		}
		if _, ok := stringOutMethods[method]; ok {
			out := abstractdomain.KnownSet(refinementsets.Strings, nil, oracleGrade, abstractdomain.SetKindTagNone)
			return &out
		}
		if method == "split" {
			out := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Star(refinementsets.Strings)), nil, oracleGrade, abstractdomain.SetKindTagNone)
			return &out
		}
		if method == "indexOf" || method == "lastIndexOf" {
			// only SET-known receivers reach here (exact strings ride the
			// host-evaluated path above), and a set's repetition window
			// counts scalars — an UNDERcount of code units past an
			// astral — so no length cap is sound on this branch; the
			// floor is the whole honest claim
			out := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-1)), nil, oracleGrade, abstractdomain.SetKindTagNone)
			return &out
		}
		// a code unit: an integer in [0, 65535], or NaN out of range
		// (sec-string.prototype.charcodeat)
		if method == "charCodeAt" {
			inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(0xFFFF)), nil, oracleGrade, abstractdomain.SetKindTagNone)
			out := abstractdomain.PossiblyNaN(inner)
			return &out
		}
		// a code point: an integer in [0, 0x10FFFF], or undefined out of
		// range (sec-string.prototype.codepointat)
		if method == "codePointAt" {
			inner := abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(0x10FFFF)), nil, oracleGrade, abstractdomain.SetKindTagNone)
			out := abstractdomain.PossiblyUndefined(inner, "", false, false)
			return &out
		}
	}
	return nil
}

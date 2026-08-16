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
// RE2 (Go's regexp) has no lookahead/lookbehind — a JS regex literal
// using those constructs fails regexToGo's compile and this file's
// readers answer nil/residue for it rather than a silently wrong
// match, per PORT.md's regex-divergence rule.

package walk

import (
	"regexp"
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
}

// regexToGo translates a JS regex source/flags pair to a Go RE2
// regexp, or (nil, false) where RE2 cannot express it (lookaround, a
// few JS-only escapes) — the caller answers residue/nil rather than a
// silently wrong match.
func regexToGo(source string, flags string) (*regexp.Regexp, bool) {
	prefix := ""
	if strings.Contains(flags, "i") {
		prefix += "i"
	}
	if strings.Contains(flags, "s") {
		prefix += "s"
	}
	if strings.Contains(flags, "m") {
		prefix += "m"
	}
	pattern := source
	if prefix != "" {
		pattern = "(?" + prefix + ")" + pattern
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return nil, false
	}
	return compiled, true
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
	// class, at the read side)
	if method == "slice" || method == "substring" {
		for _, argument := range argKnowns {
			if argument.Kind != abstractdomain.KindSet || argument.SetKindTag != abstractdomain.SetKindTagNone {
				continue
			}
			window := RangeOfSet(argument.Set)
			if window != nil && window.Int && window.Lo == -1 {
				ctx.Report(assignability.At(
					e, 7002,
					"an index may be a search's -1 (not found) — guard it with "+
						"!== -1 before "+method+". "+assignability.AlertText,
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
		// result — null becomes the absent marker
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
					out := abstractdomain.AtTrustLevel(abstractdomain.Undef, oracleGrade)
					return &out
				}
				words := make([]abstractdomain.AbstractValue, len(result))
				for i, word := range result {
					words[i] = abstractdomain.KnownValues(refinementsets.CodepointsOf(word), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
				}
				out := abstractdomain.KnownList(words, oracleGrade)
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
				if replacer != nil && (ast.IsArrowFunction(replacer) || ast.IsFunctionExpression(replacer)) && replacer.Body() != nil {
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
		if method == "repeat" && len(argKnowns) == 1 {
			n, ok := exactIntOf(argKnowns[0])
			// a negative count THROWS rather than returning — the
			// reader carries values, not exceptions
			if ok && n >= 0 {
				out := outString(strings.Repeat(text, n))
				return &out
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
			captures, ok := CaptureGroupsOf(source[1:lastSlash])
			if ok {
				stringOut := abstractdomain.KnownSet(refinementsets.Strings, nil, oracleGrade, abstractdomain.SetKindTagNone)
				items := make([]abstractdomain.AbstractValue, 0, len(captures)+1)
				items = append(items, stringOut)
				for _, group := range captures {
					if group.Optional {
						items = append(items, abstractdomain.PossiblyUndefined(stringOut, "", false, false))
					} else {
						items = append(items, stringOut)
					}
				}
				inner := abstractdomain.KnownList(items, abstractdomain.TrustProved)
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

// readStringMatchWithConstRegex is readStringMatchWithConstRegex in
// the TS source: `.match` with a LITERAL pattern on an exact string —
// the result is exactly specified (String.prototype.match through
// RegExp semantics), so the host computes the exact row — the string-
// read rule again. The regex OBJECT stays unmodeled; only the spec-
// pinned result of this call is claimed. No match is null, which
// leaves the model as the absent marker. The pattern may ride through
// a const name bound to a regex literal — but a sticky non-global
// regex reads its own lastIndex, which is process state, so that
// shape stays out.
func readStringMatchWithConstRegex(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver := site.Ctx, site.Env, site.E, site.Receiver
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	regexLiteralOf := func(argument *ast.Node) *ast.Node {
		if ast.IsRegularExpressionLiteral(argument) {
			return argument
		}
		if !ast.IsIdentifier(argument) {
			return nil
		}
		symbol := symbolAt(ctx.P.Checker, argument)
		if symbol == nil || len(symbol.Declarations) != 1 {
			return nil
		}
		declaration := symbol.Declarations[0]
		if !ast.IsVariableDeclaration(declaration) {
			return nil
		}
		varDecl := declaration.AsVariableDeclaration()
		if varDecl.Initializer == nil || !ast.IsRegularExpressionLiteral(varDecl.Initializer) {
			return nil
		}
		declList := declaration.Parent
		if declList == nil || !ast.IsVariableDeclarationList(declList) ||
			declList.Flags&ast.NodeFlagsConst == 0 {
			return nil
		}
		text := varDecl.Initializer.Text()
		flags := text[strings.LastIndex(text, "/")+1:]
		if strings.Contains(flags, "y") && !strings.Contains(flags, "g") {
			return nil
		}
		return varDecl.Initializer
	}
	var pattern *ast.Node
	if site.Method == "match" && len(arguments) == 1 {
		pattern = regexLiteralOf(arguments[0])
	}
	if pattern == nil {
		return nil
	}
	receiverKnown := receiver
	if site.HasTrackedName {
		if held, ok := env.Get(site.TrackedName); ok {
			receiverKnown = held
		} else {
			receiverKnown = silence.Residue()
		}
	}
	if !(receiverKnown.Kind == abstractdomain.KindValues && receiverKnown.KindTag == abstractdomain.PrimitiveString) {
		return nil
	}
	text := stringOf(receiverKnown.Values)
	literal := pattern.Text()
	lastSlash := strings.LastIndex(literal, "/")
	source := literal[1:lastSlash]
	flags := literal[lastSlash+1:]
	grade := abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiverKnown), abstractdomain.TrustSpec)
	compiled, ok := regexToGo(source, flags)
	if !ok {
		out := silence.Residue()
		return &out
	}
	matched := compiled.FindStringSubmatch(text)
	if matched == nil {
		out := abstractdomain.AtTrustLevel(abstractdomain.Undef, grade)
		return &out
	}
	// an unmatched capture group is undefined — the absent slot. Go's
	// regexp reports an unmatched group as "" indistinguishably from an
	// empty match — FindStringSubmatchIndex tells them apart by index.
	indices := compiled.FindStringSubmatchIndex(text)
	items := make([]abstractdomain.AbstractValue, len(matched))
	for i, part := range matched {
		if i > 0 && indices[2*i] == -1 {
			items[i] = abstractdomain.Undef
			continue
		}
		items[i] = abstractdomain.KnownValues(refinementsets.CodepointsOf(part), abstractdomain.PrimitiveString, grade)
	}
	out := abstractdomain.KnownList(items, grade)
	return &out
}

// exactZeroArgStringRow computes the argument-free string reads on an
// exact receiver text — the case-mapping and trimming rows of
// readStringMethods, split out so their transcription is testable on
// its own. ("", false) where no row speaks.
func exactZeroArgStringRow(method string, text string) (string, bool) {
	switch method {
	// Go's ToUpper/ToLower is the SIMPLE case mapping; the spec's
	// Default Case Conversion carries SpecialCasing's multi-point rows
	// ("ß" uppercases to "SS" — sec-string.prototype.touppercase), so
	// only the ASCII range, where the two agree, computes — a wider
	// receiver keeps the sort-level answer
	case "toUpperCase":
		if isASCII(text) {
			return strings.ToUpper(text), true
		}
	case "toLowerCase":
		if isASCII(text) {
			return strings.ToLower(text), true
		}
	// the trims remove the spec's white-space set (sec-trimstring:
	// WhiteSpace ∪ LineTerminator), which is not Go's — unicode.IsSpace
	// holds NEL and omits ZWNBSP — and not the ASCII cut list either
	// (NBSP, LS, PS). trimLeft/trimRight are the Annex B names for the
	// SAME function objects — "The initial value of the *trimLeft*
	// property is %String.prototype.trimStart%"
	// (String.prototype.trimleft, String.prototype.trimright) — so each
	// alias computes its target's row.
	case "trim":
		return strings.TrimFunc(text, isJSWhiteSpace), true
	case "trimStart", "trimLeft":
		return strings.TrimLeftFunc(text, isJSWhiteSpace), true
	case "trimEnd", "trimRight":
		return strings.TrimRightFunc(text, isJSWhiteSpace), true
	}
	return "", false
}

// replaceWithFunctionResult assembles String.prototype.replace /
// replaceAll over an exact receiver and an exact string pattern with a
// per-position replacement oracle. Positions are UTF-16 CODE-UNIT
// indices (StringIndexOf counts code units); replaceAll's match scan
// advances by max(1, _searchLength_) (sec-string.prototype.replaceall),
// and the pieces concatenate preserved-then-replacement with the tail
// appended (sec-string.prototype.replace steps 9-15). ("", false) when
// the oracle cannot pin a replacement.
func replaceWithFunctionResult(text string, pattern string, everyMatch bool, replacementAt func(matched string, position int) (string, bool)) (string, bool) {
	units := utf16UnitsOf(text)
	patternUnits := utf16UnitsOf(pattern)
	searchLength := len(patternUnits)
	advanceBy := searchLength
	if advanceBy < 1 {
		advanceBy = 1
	}
	// StringIndexOf over code units: the first start at or after `from`
	// where every pattern unit matches; an EMPTY pattern matches at
	// every index up to and including the length (sec-stringindexof)
	indexOfUnits := func(from int) int {
		for i := from; i+searchLength <= len(units); i++ {
			match := true
			for j := 0; j < searchLength; j++ {
				if units[i+j] != patternUnits[j] {
					match = false
					break
				}
			}
			if match {
				return i
			}
		}
		return -1
	}
	var positions []int
	position := indexOfUnits(0)
	if everyMatch {
		for position != -1 {
			positions = append(positions, position)
			position = indexOfUnits(position + advanceBy)
		}
	} else if position != -1 {
		positions = append(positions, position)
	}
	// no match: the receiver rides unchanged (sec-string.prototype.replace
	// returns _string_ when _position_ is ~not-found~)
	if len(positions) == 0 {
		return text, true
	}
	var built []uint16
	endOfLastMatch := 0
	for _, matchPosition := range positions {
		replacement, ok := replacementAt(pattern, matchPosition)
		if !ok {
			return "", false
		}
		built = append(built, units[endOfLastMatch:matchPosition]...)
		built = append(built, utf16UnitsOf(replacement)...)
		endOfLastMatch = matchPosition + searchLength
	}
	if endOfLastMatch < len(units) {
		built = append(built, units[endOfLastMatch:]...)
	}
	return utf16ToString(built), true
}

// inlineReplacerCall runs a function replacer's body once with the
// spec's three arguments bound — « _searchString_, 𝔽(_position_),
// _string_ » (sec-string.prototype.replace) — the same mechanics as
// InlineCallback (callback_models.go), widened to the three-argument
// shape. Whatever the body writes to outer names is forgotten: a
// replacer is still a write site.
func inlineReplacerCall(ctx *FlowContext, env Env, replacer *ast.Node, matched string, position int, whole string) abstractdomain.AbstractValue {
	callArguments := []abstractdomain.AbstractValue{
		abstractdomain.KnownValues(refinementsets.CodepointsOf(matched), abstractdomain.PrimitiveString, abstractdomain.TrustSpec),
		abstractdomain.KnownValues([]float64{float64(position)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec),
		abstractdomain.KnownValues(refinementsets.CodepointsOf(whole), abstractdomain.PrimitiveString, abstractdomain.TrustSpec),
	}
	parameters := replacer.Parameters()
	bindings := map[string]abstractdomain.AbstractValue{}
	for i, argument := range callArguments {
		var parameter *ast.Node
		if len(parameters) > i {
			parameter = parameters[i]
		}
		BindParameter(ctx.P.Checker, parameter, argument, bindings)
	}
	callEnv := env.Clone()
	for name, known := range bindings {
		callEnv.Set(name, known)
	}
	body := replacer.Body()
	result := silence.Residue()
	if body != nil {
		if ast.IsBlock(body) {
			var sink []abstractdomain.AbstractValue
			sinkCtx := *ctx
			sinkCtx.ReturnSink = &sink
			AnalyzeStatement(&sinkCtx, callEnv, body, nil)
			if len(sink) > 0 {
				joined := sink[0]
				for _, v := range sink[1:] {
					joined = abstractdomain.JoinKnown(joined, v)
				}
				result = joined
			}
		} else {
			result = evaluateExpression(ctx, callEnv, body)
		}
	}
	written := map[string]struct{}{}
	if body != nil {
		AssignedNames(ctx.P.Checker, body, written)
	}
	for name := range written {
		if _, ok := env.Get(name); ok {
			HavocEnv(ctx.Aliases, env, name)
		}
	}
	return result
}

// goReplacementOf mirrors a JS plain-string replacement against Go's
// regexp ReplaceAll, which reads `$name`/`$1` as its own group syntax
// — a literal `$` in a JS replacement string must escape to `$$`.
func goReplacementOf(replacement string) string {
	return strings.ReplaceAll(replacement, "$", "$$")
}

// splitEmpty mirrors `"".split("")` semantics: one entry per UTF-16
// code unit (JS String.prototype.split on the empty separator is
// unit-indexed, same as charAt).
func splitEmpty(text string) []string {
	units := utf16UnitsOf(text)
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = utf16ToString([]uint16{u})
	}
	return out
}

// utf16UnitsOf/utf16ToString/jsSlice/jsSubstring/codePointAtUTF16/
// jsPadStart/jsPadEnd: UTF-16 code-unit primitives — JS string reads
// are unit-indexed (sec-ecmascript-language-types-string-type), which
// this file's exact reads must mirror exactly rather than reading Go's
// (rune-indexed) strings directly.
func utf16UnitsOf(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

func utf16ToString(units []uint16) string {
	var b strings.Builder
	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			r := (rune(u)-0xD800)<<10 + (rune(units[i+1]) - 0xDC00) + 0x10000
			b.WriteRune(r)
			i++
			continue
		}
		b.WriteRune(rune(u))
	}
	return b.String()
}

func jsSlice(units []uint16, a, b int) []uint16 {
	n := len(units)
	clamp := func(i int) int {
		if i < 0 {
			i = n + i
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	start := clamp(a)
	end := clamp(b)
	if start >= end {
		return nil
	}
	return units[start:end]
}

func jsSubstring(units []uint16, a, b int) []uint16 {
	n := len(units)
	clamp := func(i int) int {
		if i < 0 {
			return 0
		}
		if i > n {
			return n
		}
		return i
	}
	start := clamp(a)
	end := clamp(b)
	if start > end {
		start, end = end, start
	}
	return units[start:end]
}

func codePointAtUTF16(units []uint16, i int) (int, bool) {
	if i < 0 || i >= len(units) {
		return 0, false
	}
	u := units[i]
	if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
		return int((rune(u)-0xD800)<<10+(rune(units[i+1])-0xDC00)) + 0x10000, true
	}
	return int(u), true
}

func jsPadStart(text string, targetLength int, pad string) string {
	units := utf16UnitsOf(text)
	if len(units) >= targetLength || pad == "" {
		return text
	}
	padUnits := utf16UnitsOf(pad)
	need := targetLength - len(units)
	var built []uint16
	for len(built) < need {
		built = append(built, padUnits...)
	}
	built = built[:need]
	return utf16ToString(built) + text
}

func jsPadEnd(text string, targetLength int, pad string) string {
	units := utf16UnitsOf(text)
	if len(units) >= targetLength || pad == "" {
		return text
	}
	padUnits := utf16UnitsOf(pad)
	need := targetLength - len(units)
	var built []uint16
	for len(built) < need {
		built = append(built, padUnits...)
	}
	built = built[:need]
	return text + utf16ToString(built)
}

// The COMPOSITIONAL JSON-serialization grammar over a crossed cases
// schema: json_number_grammar.go's scalar-window arm generalized to
// every case kind the RULED artifact schema carries (walk/
// foreign_edge_artifact.go's casesOf: number, string, boolean, null,
// object -- read off that reader directly, not assumed). A captured-
// stdout binding's string set is the JSON serialization grammar of
// the crossed value's FULL semantic type: the checker already knows
// what sorts the value can be (the cases list), so it already knows
// what JSON.stringify can spell for it.
//
// This package cannot import walk (walk already imports
// refinementsets; the reverse would cycle), so the case shape here
// (JSONCase/JSONCaseKind) is a package-local mirror of walk.Case/
// CaseSort -- the same five tags, converted at the one call site
// (walk/foreign_edge.go's foreignStdoutSerializedValue).
//
// EVERY ARM IS SOUND: an over-approximation of what SerializeJSONProperty
// (specifications/javascript/spec.html#sec-serializejsonproperty) can
// write for that case's own sort, cited per arm below. Where a case's
// exact grammar is not derivable with the pattern vocabulary this tree
// already has, the arm answers its kind's WEAKEST sound grammar rather
// than failing outright -- and a union of several cases composes
// per-arm, so one hard case never sinks a composition whose other arms
// are derivable (JSONValueGrammar's own union loop, below).
package refinementsets

import "sort"

// JSONCaseKind mirrors walk.CaseSort's five tags -- this package's own
// copy, since refinementsets cannot import walk (see the file banner).
type JSONCaseKind string

const (
	JSONCaseNumber  JSONCaseKind = "number"
	JSONCaseString  JSONCaseKind = "string"
	JSONCaseBoolean JSONCaseKind = "boolean"
	JSONCaseNull    JSONCaseKind = "null"
	JSONCaseObject  JSONCaseKind = "object"
)

// JSONCase mirrors walk.Case: a number/string case carries its own Set
// (the scalar window or string shape the target states); a boolean or
// null case carries neither; an object case carries Members (a key ->
// union-of-cases map, the SAME shape a member's own "cases" list reads
// as in the artifact -- a member may itself be a union of several
// sorts, exactly as a top-level return can) and Closed (the producer's
// own completeness claim: true means the value carries EXACTLY these
// keys and no others; false or unstated means it may carry additional
// keys the exact "key":value composition below cannot enumerate).
type JSONCase struct {
	Kind    JSONCaseKind
	Set     RefinedSet
	Members map[string][]JSONCase
	Closed  bool
}

// JSONValueGrammar is the top-level composition: cases is a union of
// sorts (the RULED schema's own "cases" list, for a TOP-LEVEL return --
// the one line the harness's captured stdout actually prints). Each
// PRESENT case lowers through its own kind's arm below, composed BARE
// (jsonCasesGrammarBare -- no line terminator anywhere in the
// composition, including inside an object's own members, since a
// member's value never ends a line), and the harness's own trailing
// newline (jsonNewline) is appended EXACTLY ONCE here, after the whole
// union is built -- never per-arm, and never again inside
// jsonObjectCaseGrammar's own recursive call for a member's cases list
// (that call reaches jsonCasesGrammarBare directly, bypassing this
// wrapper, so a member's value composes with no embedded newline before
// the object's own closing brace or the next comma). ok=false only
// where EVERY case in the list failed to lower -- a single derivable
// arm among several is served rather than discarding the whole
// composition, per this file's own per-arm-degrades-alone discipline.
func JSONValueGrammar(cases []JSONCase) (RefinedSet, bool) {
	bare, ok := jsonCasesGrammarBare(cases)
	if !ok {
		return RefinedSet{}, false
	}
	return withTrailingNewline(bare), true
}

// jsonCasesGrammarBare unions each PRESENT case's own bare arm (no
// trailing newline anywhere in the result) -- mirrors
// foreignAbstractValueOfCases' identical sort-distinguished-arms
// reading of the same list shape (walk/foreign_edge.go). Called by
// JSONValueGrammar (which appends the one top-level newline after) and
// directly by jsonObjectCaseGrammar for each member's own cases list
// (which never appends one -- a member's value sits before "," or "}",
// never before a line's end).
func jsonCasesGrammarBare(cases []JSONCase) (RefinedSet, bool) {
	var grammar RefinedSet
	held := false
	for _, c := range cases {
		arm, ok := jsonCaseGrammarBare(c)
		if !ok {
			continue
		}
		if !held {
			grammar = arm
			held = true
			continue
		}
		grammar = MakeRefinedSet(Union(grammar, arm))
	}
	return grammar, held
}

// jsonCaseGrammarBare is ONE case's own arm, WITHOUT a trailing
// newline, each cited to the SerializeJSONProperty step it corresponds
// to (specifications/javascript/spec.html#sec-serializejsonproperty's
// own dispatch: null / true / false / finite Number via ToString /
// String via QuoteJSONString / Object via SerializeJSONObject --
// SerializeJSONProperty steps 6-11). None of those steps writes a line
// terminator -- QuoteJSONString, Number::toString, and
// SerializeJSONObject all produce plain value text; the newline is the
// harness's own stdout convention, added once by JSONValueGrammar,
// never by SerializeJSONProperty's own clauses.
func jsonCaseGrammarBare(c JSONCase) (RefinedSet, bool) {
	switch c.Kind {
	case JSONCaseNumber:
		return jsonNumberCaseGrammarBare(c.Set)
	case JSONCaseString:
		return jsonStringCaseGrammar(c.Set)
	case JSONCaseBoolean:
		return jsonBooleanCaseGrammar()
	case JSONCaseNull:
		return jsonNullCaseGrammar()
	case JSONCaseObject:
		return jsonObjectCaseGrammar(c.Members, c.Closed)
	default:
		return RefinedSet{}, false
	}
}

// JSONValueHover is the SEMANTIC spelling of a cases list — "the JSON
// of <what the crossed value itself reads as>", reusing this package's
// OWN existing per-case vocabulary (SortWordForHover, FormatForHover,
// the quoted-literal spelling FromPoints already gives a string case's
// exact words) rather than inventing new phrasing. This is a SEPARATE
// walk from JSONValueGrammar, computed over the SAME cases list rather
// than re-parsed off the compiled grammar text afterward — reading a
// composed regex-shaped pattern back apart into "which keys, which
// sorts" would be the exact twin-decider duplication
// json_number_grammar_test.go's own banner already refuses (a
// hand-rolled matcher standing in for what the composer itself already
// knows). ok=false exactly where JSONValueGrammar itself would answer
// ok=false — no case in the list could be spelled at all.
func JSONValueHover(cases []JSONCase) (string, bool) {
	spelled, ok := jsonCasesHoverWords(cases)
	if !ok {
		return "", false
	}
	return "the JSON of " + spelled, true
}

// jsonCasesHoverWords spells a union-of-sorts list as this checker's
// own union vocabulary -- one case's own word alone, or several joined
// by " | ", the SAME bar FormatForHover's own union arm already reads
// as "or" (format_for_hover.go's FormUnion case). Skips a case whose
// own kind cannot be spelled (mirrors JSONValueGrammar's own
// per-arm-degrades-alone reading) rather than failing the whole list;
// ok=false only where NOTHING in the list could be spelled.
func jsonCasesHoverWords(cases []JSONCase) (string, bool) {
	var words []string
	for _, c := range cases {
		word, ok := jsonCaseHoverWord(c)
		if !ok {
			continue
		}
		words = append(words, word)
	}
	if len(words) == 0 {
		return "", false
	}
	return joinStrings(words, " | "), true
}

// jsonCaseHoverWord is ONE case's own hover word: SortWordForHover's
// existing sort words for a number/string case (with a number
// case's own window facts and a string case's own exact-literal
// reading appended through FormatForHover exactly as any other set
// would spell), the surface's own "boolean" word, "null" for the
// absent literal (the hover vocabulary's existing word for JSON's own
// null token, distinct from "absent" -- formatCases's word for the
// RULED schema's possibly-absent wrapper, which this is not: a
// present null CASE is a value, not an absence), and the object arm's
// own brace spelling (jsonObjectHoverWord, below).
func jsonCaseHoverWord(c JSONCase) (string, bool) {
	switch c.Kind {
	case JSONCaseNumber, JSONCaseString:
		word := SortWordForHover(c.Set)
		if facts, ok := FormatForHover(c.Set); ok {
			return word + " " + facts, true
		}
		return word, true
	case JSONCaseBoolean:
		return "boolean", true
	case JSONCaseNull:
		return "null", true
	case JSONCaseObject:
		return jsonObjectHoverWord(c.Members, c.Closed)
	default:
		return "", false
	}
}

// jsonObjectHoverWord spells an object case as "{key: <word>, ...}" --
// the same brace-and-colon shape formatObjectCase (walk/fact_export.go)
// already reads for the identical cases schema, rebuilt here from this
// package's OWN vocabulary since refinementsets cannot call into walk.
// Keys are sorted for a deterministic spelling (Members is a Go map);
// the PROSE reads in one canonical order regardless of how many
// orderings the underlying GRAMMAR's own union actually alternates --
// member order is not a semantic fact about the value, only about one
// serialized text of it, so the hover states it once.
//
// closed FALSE (the producer states no completeness claim, or states
// the value may carry additional keys) declines outright, EVEN with
// zero stated members: an exact "{key: word, ...}" (or bare "{}")
// spelling would claim MORE than an open object admits (a real
// serialized value can carry members beyond the stated ones), and
// this file states no vocabulary for "these keys, plus possibly
// others" rather than guess at one -- mirrors jsonObjectCaseGrammar's
// own Closed gate exactly, so the hover and the grammar agree on what
// an open object can claim.
//
// closed TRUE with zero members spells "{}" (a CLOSED empty object
// genuinely holds no members, ever). A member whose own cases list
// cannot be spelled at all falls the WHOLE object word back to
// ok=false, mirroring jsonObjectCaseGrammar's identical fall-through.
func jsonObjectHoverWord(members map[string][]JSONCase, closed bool) (string, bool) {
	if !closed {
		return "", false
	}
	if len(members) == 0 {
		return "{}", true
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		word, ok := jsonCasesHoverWords(members[name])
		if !ok {
			return "", false
		}
		parts = append(parts, name+": "+word)
	}
	return "{" + joinStrings(parts, ", ") + "}", true
}

/* ── the scalar/literal arms ─────────────────────────────────────── */

// jsonNumberCaseGrammarBare is json_number_grammar.go's own landed
// arms, WITHOUT their baked-in trailing newline: TightenedJSONNumberGrammar
// and WindowlessJSONNumberGrammar both compile a pattern ending in a
// literal `\n` (that file's own top-level convention, correct for their
// direct callers -- jsonNumberGrammarSet and the all-number branch of
// foreignStdoutSerializedValue, both already at the true top level, so
// their baked-in newline is exactly right there and those functions
// stay untouched). This file's own composition instead needs a BARE
// number production it can nest inside an object member (before "," or
// "}", never before a line's end) or union with another bare arm, with
// the harness's one trailing newline added back exactly once by
// JSONValueGrammar -- so this reuses the identical pattern PIECES
// (jsonZeroToOnePattern etc., same package) with the trailing `\n`
// trimmed, one production asked from a third door rather than a second
// hand-copied string.
func jsonNumberCaseGrammarBare(window RefinedSet) (RefinedSet, bool) {
	if tightened, ok := bareTightenedJSONNumberGrammar(window); ok {
		return tightened, true
	}
	return bareWindowlessJSONNumberGrammar(), true
}

// bareWindowlessJSONNumberGrammar is WindowlessJSONNumberGrammar's own
// pattern pieces, compiled without the trailing `\n`.
func bareWindowlessJSONNumberGrammar() RefinedSet {
	pattern := `-?` + jsonIntegerPartPattern + jsonPlainOrScientificTailPattern
	set, _ := compileAnchoredGrammar(pattern)
	return set
}

// bareTightenedJSONNumberGrammar mirrors TightenedJSONNumberGrammar's
// own case dispatch exactly (see that function's doc for the citations
// behind each branch), reusing the SAME jsonZeroToOnePattern /
// jsonNonNegativePattern / jsonNonPositivePattern / digit-count
// constants with the trailing `\n` trimmed off each. Kept as a
// parallel function rather than a shared helper with a "strip the
// newline" flag on the exported original, since the exported
// TightenedJSONNumberGrammar's own contract (used directly by
// jsonNumberGrammarSet and by json_number_grammar_test.go) is that it
// ALWAYS carries the newline -- this stays a separate, bare-only door.
func bareTightenedJSONNumberGrammar(windowSet RefinedSet) (RefinedSet, bool) {
	lo, hi, loStrict, hiStrict, isInteger, ok := PlainScalarWindow(windowSet)
	if !ok {
		return RefinedSet{}, false
	}
	nonNegative := lo >= 0 && !loStrict
	nonPositive := hi <= 0 && !hiStrict
	switch {
	case nonNegative && hi <= 1 && !hiStrict:
		return compileAnchoredGrammar(trimTrailingNewlinePattern(jsonZeroToOnePattern))
	case isInteger && nonNegative && !hiStrict:
		return bareIntegerDigitCountGrammar(lo, hi), true
	case nonNegative:
		return compileAnchoredGrammar(trimTrailingNewlinePattern(jsonNonNegativePattern))
	case nonPositive:
		return compileAnchoredGrammar(trimTrailingNewlinePattern(jsonNonPositivePattern))
	}
	return RefinedSet{}, false
}

// trimTrailingNewlinePattern drops the literal `\n` every
// json_number_grammar.go pattern constant ends its own production
// with -- a plain string trim, not a regex operation, since the
// trailing four characters of each constant are always that exact
// literal escape sequence.
func trimTrailingNewlinePattern(pattern string) string {
	const suffix = `\n`
	if len(pattern) >= len(suffix) && pattern[len(pattern)-len(suffix):] == suffix {
		return pattern[:len(pattern)-len(suffix)]
	}
	return pattern
}

// bareIntegerDigitCountGrammar mirrors integerDigitCountGrammar's own
// digit-count window (json_number_grammar.go), without the trailing
// newline that function appends for its own top-level contract.
func bareIntegerDigitCountGrammar(lo, hi float64) RefinedSet {
	loCount := decimalDigitCount(lo)
	hiCount := decimalDigitCount(hi)
	return Repetition(Digits, loCount, &hiCount)
}

// WindowlessJSONNumberGrammar is jsonNumberGrammarPattern's own set
// (walk/foreign_edge.go), rebuilt from the SAME pattern pieces
// TightenedJSONNumberGrammar already imports (jsonIntegerPartPattern,
// jsonPlainOrScientificTailPattern) rather than a second hand-copied
// string -- one production, asked from two doors. Exported so a
// caller with no window classification at all (an unbounded number
// case, or a caller outside this file) can still reach the sound
// windowless claim without duplicating the pattern.
func WindowlessJSONNumberGrammar() RefinedSet {
	// compileAnchoredGrammar itself panics on a compile failure (its own
	// doc: "an impossible state") rather than answering ok=false, so
	// there is no second failure branch to handle here
	pattern := `-?` + jsonIntegerPartPattern + jsonPlainOrScientificTailPattern + `\n`
	set, _ := compileAnchoredGrammar(pattern)
	return set
}

// jsonNewline is the one trailing LINE FEED (0x000A) the harness's
// captured stdout always carries after a serialized value -- the same
// literal json_number_grammar.go's own patterns already bake into
// their own top-level productions (see that file's
// integerDigitCountGrammar comment: "the one trailing newline the
// harness's captured stdout always carries appended after it"). Every
// arm in THIS file composes BARE (no embedded newline, so a value
// nests cleanly inside an object member before "," or "}"); jsonNewline
// is appended exactly once, by JSONValueGrammar alone, after the whole
// top-level union is built.
var jsonNewline = MakeRefinedSet(OneOf([]float64{'\n'}))

// withTrailingNewline concatenates set with jsonNewline -- called only
// by JSONValueGrammar, the one true top-level door.
func withTrailingNewline(set RefinedSet) RefinedSet {
	return MakeRefinedSet(Concatenation(set, jsonNewline))
}

// jsonStringCaseGrammar is SerializeJSONProperty step 8 ("If _value_
// is a String, return QuoteJSONString(_value_)"): a double-quoted,
// escaped literal (#sec-quotejsonstring's own product -- wrap in
// 0x0022 QUOTATION MARK code units, escape the table's seven named
// controls plus every code point below 0x0020 and every lone
// surrogate, UTF16EncodeCodePoint everything else unchanged). Bare --
// no trailing newline; JSONValueGrammar appends the one top-level
// newline after composing every case.
//
// A FINITE word set (the target states an exact string enum, e.g. a
// Python Literal["a","b"]) spells EXACTLY: each word's own QuoteJSONString
// spelling, alternated -- WordTuplesOfConjunction reads the finite
// word list off the set syntactically (no kernel round trip, same
// discipline PlainScalarWindow already keeps for the number arm), and
// FromPoints already spells one word's exact JSON-quoted form
// (format_string_shapes.go, itself QuoteJSONString's escaping via
// jsonQuoteString/encoding/json -- Go's encoding/json escapes the
// identical control set plus HTML-sensitive code points, a STRICT
// SUPERSET of the spec's own escape table, so every FromPoints
// spelling is still exactly the text QuoteJSONString would write for
// that literal). An infinite/patterned string set (the ordinary
// z.string() case, any startsWith/endsWith/length window) has no
// finite word list to enumerate -- the sound WIDE arm is the general
// JSON-string grammar: a quote, any run of Codepoints, a quote, since
// QuoteJSONString admits every scalar value the escaping steps cover
// and excludes nothing this checker can state a tighter claim about.
func jsonStringCaseGrammar(set RefinedSet) (RefinedSet, bool) {
	if words, ok := WordTuplesOfConjunction(set); ok && len(words) > 0 {
		return jsonExactStringAlternation(words)
	}
	return jsonWideStringGrammar(), true
}

// jsonExactStringAlternation alternates each word's own JSON-quoted
// spelling (FromPoints), bare, sorted so the built set is deterministic
// across runs (WordTuplesOfConjunction's own order traces back to a Go
// map walk upstream of the string set's construction in some callers).
// A word outside the scalar range (FromPoints ok=false -- a lone
// surrogate slipped into a wire set) falls the WHOLE arm back to the
// wide grammar rather than guess at that one word's spelling.
func jsonExactStringAlternation(words [][]float64) (RefinedSet, bool) {
	spellings := make([]string, 0, len(words))
	for _, word := range words {
		spelled, ok := FromPoints(word)
		if !ok {
			return jsonWideStringGrammar(), true
		}
		spellings = append(spellings, spelled)
	}
	sort.Strings(spellings)
	var grammar RefinedSet
	for i, spelled := range spellings {
		arm := StringTuple(spelled)
		if i == 0 {
			grammar = arm
			continue
		}
		grammar = MakeRefinedSet(Union(grammar, arm))
	}
	return grammar, true
}

// jsonWideStringGrammar is the sound wide arm for a string case with
// no finite word list: a quote, C*, a quote -- QuoteJSONString always
// opens and closes with 0x0022, and every scalar value in between
// (escaped or not) is a member of Codepoints (#sec-quotejsonstring
// steps 1-4). Bare -- no trailing newline.
func jsonWideStringGrammar() RefinedSet {
	quote := MakeRefinedSet(OneOf([]float64{'"'}))
	return MakeRefinedSet(Concatenation(quote, MakeRefinedSet(Concatenation(Strings, quote))))
}

// jsonBooleanCaseGrammar is SerializeJSONProperty steps 6-7 ("If
// _value_ is *true*, return *"true"*" / "If _value_ is *false*, return
// *"false"*"): the two-word alternation, exact, bare -- a boolean case
// states the whole sort, and both tokens are always reachable.
func jsonBooleanCaseGrammar() (RefinedSet, bool) {
	return MakeRefinedSet(Union(StringTuple("true"), StringTuple("false"))), true
}

// jsonNullCaseGrammar is SerializeJSONProperty step 5 ("If _value_ is
// *null*, return *"null"*"): the one literal, exact, bare.
func jsonNullCaseGrammar() (RefinedSet, bool) {
	return StringTuple("null"), true
}

/* ── the object arm ──────────────────────────────────────────────── */

// jsonObjectMemberLimit is the member count above which this file
// states no exact-order claim at all (see jsonObjectCaseGrammar):
// the pattern vocabulary can state a bounded alternation-of-
// permutations for a small, fixed key set, but the permutation count
// grows factorially and stops being a sound thing to hand the regex
// compiler past a few members.
const jsonObjectMemberLimit = 3

// jsonObjectCaseGrammar is SerializeJSONObject's own shape
// (#sec-serializejsonobject): "{" + comma-joined "key":value members +
// "}", "{}" for zero members (steps 12-13's own two branches). The
// artifact schema (walk/foreign_edge_artifact.go's casesOf) states
// Members as a Go map -- the wire's JSON object already lost its own
// producer-side insertion order at decode time, and the RULED schema
// states no separate order field -- so THIS reader has no fixed member
// order to serialize by. EnumerableOwnProperties (the property list
// SerializeJSONObject actually walks, #sec-serializejsonobject step 6)
// is itself insertion order for a plain object, which is exactly the
// order this schema does not carry: the sound claim is therefore over
// EVERY ORDERING the members could have serialized in, not one assumed
// order.
//
// closed is the producer's own completeness claim (walk.Case.Closed,
// carried through unchanged): a value CAN carry members beyond the
// stated ones whenever closed is false, and the exact "{key:value,...}"
// composition below is SOUND ONLY when the member set is exactly the
// stated one -- an open object's real serialized text may hold an
// extra "somekey":... this file states no vocabulary for, so an open
// object (any member count, INCLUDING zero stated members) falls to
// the wide arm unconditionally rather than overclaim.
//
//   - open (closed == false): the wide arm, regardless of member count
//     -- "{}" would exclude a real extra-member text, and the exact
//     permutation arm would too.
//   - closed, zero members: "{}" exactly (step 12) -- a CLOSED empty
//     object genuinely holds no members, ever.
//   - closed, 1..jsonObjectMemberLimit members: the alternation of
//     every permutation of "key":<member grammar> pairs, comma-joined,
//     brace-wrapped -- a small, explicitly bounded union the pattern
//     vocabulary can state outright (jsonObjectPermutations, below).
//   - closed, more members: the general JSON-object wide arm -- "{",
//     ANY run of Codepoints, "}" -- sound because every
//     SerializeJSONObject output opens with "{" and closes with "}"
//     (steps 12-13) and nothing about the interior text is claimed
//     beyond that.
//
// A member whose own cases list fails to lower entirely (jsonCaseGrammarBare
// ok=false for every one of its cases) makes the WHOLE object case fall
// to the wide arm -- an unspellable member means the exact "key":value
// composition cannot be built, but "{...}" is still sound regardless.
func jsonObjectCaseGrammar(members map[string][]JSONCase, closed bool) (RefinedSet, bool) {
	if !closed {
		return jsonWideObjectGrammar(), true
	}
	if len(members) == 0 {
		return StringTuple("{}"), true
	}
	if len(members) > jsonObjectMemberLimit {
		return jsonWideObjectGrammar(), true
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	quotedKeys := make(map[string]string, len(names))
	memberGrammars := make(map[string]RefinedSet, len(names))
	for _, name := range names {
		quoted, quoteOk := FromPoints(CodepointsOf(name))
		if !quoteOk {
			return jsonWideObjectGrammar(), true
		}
		// jsonCasesGrammarBare, NOT JSONValueGrammar: a member's own value
		// text sits before "," or "}", never before a line's end, so it
		// composes with no embedded newline -- the exact defect this
		// fixes (JSONValueGrammar's own trailing jsonNewline landing mid-
		// object, between the digits and the closing brace, excluding
		// the true serialized text's own trailing newline AFTER "}").
		grammar, ok := jsonCasesGrammarBare(members[name])
		if !ok {
			return jsonWideObjectGrammar(), true
		}
		quotedKeys[name] = quoted
		memberGrammars[name] = grammar
	}
	return jsonObjectPermutations(names, quotedKeys, memberGrammars), true
}

// jsonWideObjectGrammar is the sound fallback for an object case this
// file cannot spell exactly: "{", any text, "}" -- every
// SerializeJSONObject output (#sec-serializejsonobject steps 12-13)
// opens with LEFT CURLY BRACKET and closes with RIGHT CURLY BRACKET
// with nothing else claimed about the interior.
func jsonWideObjectGrammar() RefinedSet {
	open := MakeRefinedSet(OneOf([]float64{'{'}))
	close_ := MakeRefinedSet(OneOf([]float64{'}'}))
	return MakeRefinedSet(Concatenation(open, MakeRefinedSet(Concatenation(Strings, close_))))
}

// jsonMemberPair is "key":<grammar> -- the key's own PRE-QUOTED
// spelling (jsonObjectCaseGrammar's own FromPoints call, run once per
// key rather than once per permutation) concatenated with ":" and the
// member's own grammar (#sec-serializejsonobject step 7's own member
// assembly: QuoteJSONString(key) + ":" + stringP, [[Gap]] empty since
// this checker states no indentation premise for a captured-stdout
// binding).
func jsonMemberPair(quotedKey string, grammar RefinedSet) RefinedSet {
	keyGrammar := StringTuple(quotedKey + ":")
	return MakeRefinedSet(Concatenation(keyGrammar, grammar))
}

// jsonObjectPermutations alternates EVERY ordering of names' own
// "key":value pairs, comma-joined and brace-wrapped -- the small-N
// bounded union jsonObjectCaseGrammar's own doc states. len(names) <=
// jsonObjectMemberLimit is the caller's own contract (checked there),
// so the permutation count here is bounded (<= 3! = 6). Every key in
// quotedKeys already passed FromPoints in the caller, so no ok=false
// path is needed here.
func jsonObjectPermutations(names []string, quotedKeys map[string]string, memberGrammars map[string]RefinedSet) RefinedSet {
	var grammar RefinedSet
	held := false
	permuteNames(names, func(ordering []string) {
		pairs := make([]RefinedSet, len(ordering))
		for i, name := range ordering {
			pairs[i] = jsonMemberPair(quotedKeys[name], memberGrammars[name])
		}
		body := jsonCommaJoin(pairs)
		open := MakeRefinedSet(OneOf([]float64{'{'}))
		close_ := MakeRefinedSet(OneOf([]float64{'}'}))
		wrapped := MakeRefinedSet(Concatenation(open, MakeRefinedSet(Concatenation(body, close_))))
		if !held {
			grammar = wrapped
			held = true
			return
		}
		grammar = MakeRefinedSet(Union(grammar, wrapped))
	})
	return grammar
}

// jsonCommaJoin is the comma-separated concatenation of parts, in
// order -- #sec-serializejsonobject step 10's own COMMA join, no
// separator before the first or after the last part.
func jsonCommaJoin(parts []RefinedSet) RefinedSet {
	if len(parts) == 0 {
		return MakeRefinedSet(EmptyTuple)
	}
	joined := parts[len(parts)-1]
	comma := MakeRefinedSet(OneOf([]float64{','}))
	for i := len(parts) - 2; i >= 0; i-- {
		joined = MakeRefinedSet(Concatenation(parts[i], MakeRefinedSet(Concatenation(comma, joined))))
	}
	return joined
}

// permuteNames calls visit once per permutation of names, in
// lexicographic order of the permutation's own index sequence
// (Heap's algorithm would reorder in place; this file instead recurses
// over remaining/chosen slices, simplest to read for the small N this
// is ever called with).
func permuteNames(names []string, visit func([]string)) {
	if len(names) == 0 {
		visit(nil)
		return
	}
	var recurse func(chosen []string, remaining []string)
	recurse = func(chosen []string, remaining []string) {
		if len(remaining) == 0 {
			visit(append([]string{}, chosen...))
			return
		}
		for i := range remaining {
			next := append([]string{}, remaining[:i]...)
			next = append(next, remaining[i+1:]...)
			recurse(append(chosen, remaining[i]), next)
		}
	}
	recurse(nil, names)
}

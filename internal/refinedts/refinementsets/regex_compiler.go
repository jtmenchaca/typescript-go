// The format grammar (TERMS.md §6, .regex): the supported subset of
// regular-expression syntax compiled to closed-list forms --
//
//	character classes -> codepoint ranges and "one of W"
//	concatenation      -> the concatenation form
//	alternation        -> the union form
//	quantifiers        -> star and bounded repetition
//
// Backreferences and lookaround do not denote regular languages and are
// unsupported, the same policy as the z.refine ban. An unanchored
// pattern matches a substring, so it denotes C* . L . C*; anchors ^ $
// pin their side.

package refinementsets

import (
	"strconv"
	"strings"
)

// UnsupportedPattern mirrors the TS source's UnsupportedPattern error
// class: a refusal, not an impossible-state panic.
type UnsupportedPattern struct {
	Message string
}

func (e *UnsupportedPattern) Error() string {
	return e.Message
}

func unsupported(message string) error {
	return &UnsupportedPattern{Message: message}
}

func one(points []float64) RefinedSet {
	return MakeRefinedSet(OneOf(points))
}

func codeRange(lo, hi float64) RefinedSet {
	return MakeRefinedSet(Integer, AtLeast(lo), AtMost(hi))
}

var regexDigits = codeRange(0x30, 0x39)
var regexWord = MakeRefinedSet(
	Integer,
	Union(
		MakeRefinedSet(Union(codeRange(0x30, 0x39), codeRange(0x41, 0x5A))),
		MakeRefinedSet(Union(codeRange(0x61, 0x7A), one([]float64{0x5F}))),
	),
)
var regexSpace = one([]float64{0x20, 0x09, 0x0A, 0x0D, 0x0B, 0x0C})

func unionOf(members []RefinedSet) RefinedSet {
	set := members[0]
	for _, member := range members[1:] {
		set = MakeRefinedSet(Union(set, member))
	}
	return set
}

// regexTerminators is the line terminators an unflagged . never matches
// (LF, CR, LS, PS).
var regexTerminators = one([]float64{0x0A, 0x0D, 0x2028, 0x2029})

// flagReading is FlagReading in the TS source.
type flagReading struct {
	foldCase     bool
	dotAll       bool
	perCodePoint bool
}

// dotSet is what the flags make of `.`: one UTF-16 UNIT without u/v --
// an astral code point is two units, so it is outside an unflagged dot
// -- one code point with them; line terminators excluded unless s
// admits them.
func dotSet(flags flagReading) RefinedSet {
	var units RefinedSet
	if flags.perCodePoint {
		units = Codepoints
	} else {
		units = MakeRefinedSet(Integer, AtLeast(0), AtMost(0xFFFF))
	}
	if flags.dotAll {
		return units
	}
	return MakeRefinedSet(Difference(units, regexTerminators))
}

// readFlags reads the flags. g/y/d change matching mechanics, never
// the language; i, s, u, v are honored; anything else changes what the
// pattern denotes in a way this grammar does not model, so the pattern
// is unsupported rather than misread.
func readFlags(flags string) (flagReading, error) {
	for _, flag := range flags {
		if !strings.ContainsRune("gydisuv", flag) {
			return flagReading{}, unsupported("the '" + string(flag) + "' flag is not modeled")
		}
	}
	return flagReading{
		foldCase:     strings.Contains(flags, "i"),
		dotAll:       strings.Contains(flags, "s"),
		perCodePoint: strings.Contains(flags, "u") || strings.Contains(flags, "v"),
	}, nil
}

// regexReader is Reader in the TS source: a recursive-descent reader
// over the pattern source. Runs over the pattern's runes (Unicode
// scalar values), matching the TS source's per-character reads on a
// pattern that -- per the vendored grammar policy -- never crosses the
// surrogate range on its own.
type regexReader struct {
	source []rune
	i      int
	flags  flagReading
}

func newRegexReader(source string, flags flagReading) *regexReader {
	return &regexReader{source: []rune(source), flags: flags}
}

func (r *regexReader) done() bool {
	return r.i >= len(r.source)
}

func (r *regexReader) peek() rune {
	if r.i >= len(r.source) {
		return 0
	}
	return r.source[r.i]
}

func (r *regexReader) peekAt(offset int) rune {
	if r.i+offset >= len(r.source) {
		return 0
	}
	return r.source[r.i+offset]
}

func (r *regexReader) next() rune {
	if r.i >= len(r.source) {
		return 0
	}
	c := r.source[r.i]
	r.i++
	return c
}

func (r *regexReader) take(c rune) bool {
	if r.peek() == c {
		r.i++
		return true
	}
	return false
}

// alternation: sequence ('|' sequence)*
func (r *regexReader) alternation() (RefinedSet, error) {
	branches := []RefinedSet{}
	first, err := r.sequence()
	if err != nil {
		return RefinedSet{}, err
	}
	branches = append(branches, first)
	for r.take('|') {
		next, err := r.sequence()
		if err != nil {
			return RefinedSet{}, err
		}
		branches = append(branches, next)
	}
	return unionOf(branches), nil
}

func (r *regexReader) sequence() (RefinedSet, error) {
	var parts []RefinedSet
	for !r.done() && r.peek() != '|' && r.peek() != ')' {
		part, err := r.quantified()
		if err != nil {
			return RefinedSet{}, err
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return MakeRefinedSet(EmptyTuple), nil
	}
	set := parts[len(parts)-1]
	for k := len(parts) - 2; k >= 0; k-- {
		set = MakeRefinedSet(Concatenation(parts[k], set))
	}
	return set, nil
}

func (r *regexReader) quantified() (RefinedSet, error) {
	atom, err := r.atom()
	if err != nil {
		return RefinedSet{}, err
	}
	if r.take('*') {
		return Repetition(atom, 0, nil), nil
	}
	if r.take('+') {
		return Repetition(atom, 1, nil), nil
	}
	if r.take('?') {
		return Repetition(atom, 0, intPtr(1)), nil
	}
	if r.peek() == '{' {
		return r.counted(atom)
	}
	return atom, nil
}

func (r *regexReader) counted(atom RefinedSet) (RefinedSet, error) {
	r.next() // {
	digits := ""
	for isDigitRune(r.peek()) {
		digits += string(r.next())
	}
	if digits == "" {
		return RefinedSet{}, unsupported("a counted quantifier needs a count")
	}
	lo, _ := strconv.Atoi(digits)
	if r.take('}') {
		return Repetition(atom, lo, intPtr(lo)), nil
	}
	if !r.take(',') {
		return RefinedSet{}, unsupported("malformed counted quantifier")
	}
	if r.take('}') {
		return Repetition(atom, lo, nil), nil
	}
	upper := ""
	for isDigitRune(r.peek()) {
		upper += string(r.next())
	}
	if upper == "" || !r.take('}') {
		return RefinedSet{}, unsupported("malformed counted quantifier")
	}
	hi, _ := strconv.Atoi(upper)
	return Repetition(atom, lo, intPtr(hi)), nil
}

func isDigitRune(c rune) bool {
	return c >= '0' && c <= '9'
}

func (r *regexReader) atom() (RefinedSet, error) {
	c := r.next()
	if c == '(' {
		if r.peek() == '?' {
			// (?: grouping is fine; lookaround and named groups are not
			r.next()
			if r.take(':') {
				inner, err := r.alternation()
				if err != nil {
					return RefinedSet{}, err
				}
				if !r.take(')') {
					return RefinedSet{}, unsupported("unclosed group")
				}
				return inner, nil
			}
			return RefinedSet{}, unsupported("lookaround and special groups do not denote regular languages")
		}
		inner, err := r.alternation()
		if err != nil {
			return RefinedSet{}, err
		}
		if !r.take(')') {
			return RefinedSet{}, unsupported("unclosed group")
		}
		return inner, nil
	}
	if c == '[' {
		return r.characterClass()
	}
	if c == '.' {
		return dotSet(r.flags), nil
	}
	if c == '\\' {
		return r.escape(r.next())
	}
	if strings.ContainsRune("*+?{}()|]", c) {
		return RefinedSet{}, unsupported("unexpected '" + string(c) + "' in the pattern")
	}
	if c == '^' || c == '$' {
		// anchors are read at the pattern's ends only; mid-pattern
		// anchors change which side is padded and are unsupported rather
		// than misread as literals
		return RefinedSet{}, unsupported("a mid-pattern '" + string(c) + "' anchor is not supported")
	}
	return r.literal(float64(c))
}

// literal is a literal code point, folded under the i flag: ASCII
// letters admit both cases; a cased character beyond ASCII is
// unsupported rather than matched one-sidedly.
func (r *regexReader) literal(cp float64) (RefinedSet, error) {
	if !r.flags.foldCase {
		return one([]float64{cp}), nil
	}
	if cp >= 0x41 && cp <= 0x5A {
		return one([]float64{cp, cp + 0x20}), nil
	}
	if cp >= 0x61 && cp <= 0x7A {
		return one([]float64{cp - 0x20, cp}), nil
	}
	ch := string(rune(int32(cp)))
	if strings.ToLower(ch) != strings.ToUpper(ch) {
		return RefinedSet{}, unsupported("case-insensitive matching beyond ASCII is not modeled")
	}
	return one([]float64{cp}), nil
}

func (r *regexReader) escape(c rune) (RefinedSet, error) {
	switch c {
	case 'd':
		return regexDigits, nil
	case 'D':
		return MakeRefinedSet(Difference(Codepoints, regexDigits)), nil
	case 'w':
		return regexWord, nil
	case 'W':
		return MakeRefinedSet(Difference(Codepoints, regexWord)), nil
	case 's':
		return regexSpace, nil
	case 'S':
		return MakeRefinedSet(Difference(Codepoints, regexSpace)), nil
	case 'n':
		return one([]float64{0x0A}), nil
	case 't':
		return one([]float64{0x09}), nil
	case 'r':
		return one([]float64{0x0D}), nil
	default:
		if isDigitRune(c) {
			return RefinedSet{}, unsupported("backreferences do not denote regular languages")
		}
		// an escaped literal: \. \[ \\ ...
		return r.literal(float64(c))
	}
}

func (r *regexReader) characterClass() (RefinedSet, error) {
	negated := r.take('^')
	var members []RefinedSet
	for !r.done() && r.peek() != ']' {
		c := r.next()
		if c == '\\' {
			escaped := r.next()
			byName := map[rune]RefinedSet{
				'd': regexDigits,
				'w': regexWord,
				's': regexSpace,
				'n': one([]float64{0x0A}),
				't': one([]float64{0x09}),
				'r': one([]float64{0x0D}),
			}
			if named, ok := byName[escaped]; ok {
				members = append(members, named)
				continue
			}
			c = escaped
		}
		start := float64(c)
		if r.peek() == '-' && r.peekAt(1) != ']' {
			r.next()
			endChar := r.next()
			if endChar == '\\' {
				endChar = r.next()
			}
			rangeSet, err := r.classRange(start, float64(endChar))
			if err != nil {
				return RefinedSet{}, err
			}
			members = append(members, rangeSet)
			continue
		}
		lit, err := r.literal(start)
		if err != nil {
			return RefinedSet{}, err
		}
		members = append(members, lit)
	}
	if !r.take(']') {
		return RefinedSet{}, unsupported("unclosed character class")
	}
	if len(members) == 0 {
		return RefinedSet{}, unsupported("an empty character class")
	}
	inside := unionOf(members)
	if negated {
		return MakeRefinedSet(Difference(Codepoints, inside)), nil
	}
	return inside, nil
}

// classRange is a class range, folded under the i flag: a range inside
// one ASCII letter case admits its mirror; a range touching any other
// cased character is unsupported rather than matched one-sidedly.
func (r *regexReader) classRange(start, end float64) (RefinedSet, error) {
	if !r.flags.foldCase {
		return codeRange(start, end), nil
	}
	if start >= 0x41 && end <= 0x5A {
		return MakeRefinedSet(Union(codeRange(start, end), codeRange(start+0x20, end+0x20))), nil
	}
	if start >= 0x61 && end <= 0x7A {
		return MakeRefinedSet(Union(codeRange(start-0x20, end-0x20), codeRange(start, end))), nil
	}
	caseless := end <= 0x7F &&
		(end < 0x41 || start > 0x7A ||
			(start > 0x5A && end < 0x61 && start <= end))
	if caseless {
		return codeRange(start, end), nil
	}
	return RefinedSet{}, unsupported("case-insensitive matching over this range is not modeled")
}

// GrammarResult is the result of FormatGrammar: either a compiled set,
// or an unsupported-pattern message. A struct with an ok discriminant
// rather than TS's `{ set } | { unsupported }` object union, per the
// port's discriminated-union convention.
type GrammarResult struct {
	Set         RefinedSet
	Unsupported string
	Ok          bool
}

// FormatGrammar compiles a pattern's supported subset to the set it
// denotes, or a refusal message. Anchors pin their side; an unanchored
// side is padded with C* (a regex matches a substring).
func FormatGrammar(pattern string, flags string) GrammarResult {
	reading, err := readFlags(flags)
	if err != nil {
		return unsupportedResult(err)
	}
	body := []rune(pattern)
	anchoredStart := len(body) > 0 && body[0] == '^'
	if anchoredStart {
		body = body[1:]
	}
	anchoredEnd := len(body) > 0 && body[len(body)-1] == '$' &&
		!(len(body) >= 2 && body[len(body)-2] == '\\')
	if anchoredEnd {
		body = body[:len(body)-1]
	}
	// LEADING negative lookaheads at an anchored start are set
	// differences: ^(?!p)rest denotes rest minus the strings some
	// prefix of which p matches -- p's own compilation padded with C*.
	// Lookaheads anywhere else stay unsupported.
	var forbidden []RefinedSet
	for anchoredStart && hasPrefixRunes(body, "(?!") {
		depth := 0
		end := -1
		for k := 0; k < len(body); k++ {
			ch := body[k]
			if ch == '\\' {
				k++
				continue
			}
			if ch == '(' {
				depth++
			}
			if ch == ')' {
				depth--
				if depth == 0 {
					end = k
					break
				}
			}
		}
		if end == -1 {
			return GrammarResult{Unsupported: "unclosed group"}
		}
		innerReader := newRegexReader(string(body[3:end]), reading)
		inner, err := innerReader.alternation()
		if err != nil {
			return unsupportedResult(err)
		}
		if !innerReader.done() {
			return GrammarResult{Unsupported: "trailing pattern content"}
		}
		forbidden = append(forbidden, MakeRefinedSet(Concatenation(inner, Strings)))
		body = body[end+1:]
	}
	reader := newRegexReader(string(body), reading)
	set, err := reader.alternation()
	if err != nil {
		return unsupportedResult(err)
	}
	if !reader.done() {
		return GrammarResult{Unsupported: "trailing pattern content"}
	}
	if !anchoredEnd {
		set = MakeRefinedSet(Concatenation(set, Strings))
	}
	if !anchoredStart {
		set = MakeRefinedSet(Concatenation(Strings, set))
	}
	for _, f := range forbidden {
		set = MakeRefinedSet(Difference(set, f))
	}
	return GrammarResult{Set: set, Ok: true}
}

func unsupportedResult(err error) GrammarResult {
	if up, ok := err.(*UnsupportedPattern); ok {
		return GrammarResult{Unsupported: up.Message}
	}
	panic(err)
}

func hasPrefixRunes(s []rune, prefix string) bool {
	p := []rune(prefix)
	if len(s) < len(p) {
		return false
	}
	for i, c := range p {
		if s[i] != c {
			return false
		}
	}
	return true
}

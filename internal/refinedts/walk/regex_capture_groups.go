// from evaluation/regex_capture_groups.ts
//
// Capture-group optionality read off a regex pattern's own text —
// which groups a successful match can leave unset. Split from
// builtin_models.ts per the v2 tree.

package walk

// CaptureGroup is the TS source's `{ readonly optional: boolean }`
// element of CaptureGroupsOf's result.
type CaptureGroup struct {
	Optional bool
}

type captureSpan struct {
	start      int
	end        int
	capturing  bool
	lookaround bool
}

// CaptureGroupsOf is captureGroupsOf in the TS source: the capture
// groups of a regex pattern, each marked optional when the group can
// be left unset in a successful match — a zero-admitting quantifier
// on the group or any construct enclosing it, an alternation
// anywhere, or a lookaround around it. Conservative on purpose: unsure
// reads as optional (a string-or-undefined claim is sound either
// way), and an unreadable structure (unbalanced parentheses) answers
// (nil, false).
func CaptureGroupsOf(pattern string) ([]CaptureGroup, bool) {
	var groups []captureSpan
	var stack []int
	inClass := false
	sawAlternation := false
	runes := []rune(pattern)
	at := func(i int) rune {
		if i < 0 || i >= len(runes) {
			return 0
		}
		return runes[i]
	}
	hasPrefixAt := func(i int, prefix string) bool {
		pr := []rune(prefix)
		if i+len(pr) > len(runes) {
			return false
		}
		for j, r := range pr {
			if runes[i+j] != r {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '\\' {
			i++
			continue
		}
		if inClass {
			if c == ']' {
				inClass = false
			}
			continue
		}
		if c == '[' {
			inClass = true
			continue
		}
		if c == '|' {
			sawAlternation = true
			continue
		}
		if c == '(' {
			special := at(i+1) == '?'
			named := hasPrefixAt(i, "(?<") && at(i+3) != '=' && at(i+3) != '!'
			groups = append(groups, captureSpan{
				start:      i,
				end:        -1,
				capturing:  !special || named,
				lookaround: special && !named && at(i+2) != ':',
			})
			stack = append(stack, len(groups)-1)
			continue
		}
		if c == ')' {
			if len(stack) == 0 {
				return nil, false
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			groups[open].end = i
		}
	}
	if len(stack) > 0 {
		return nil, false
	}
	zeroQuantified := func(endIndex int) bool {
		next := at(endIndex + 1)
		return next == '?' || next == '*' || (next == '{' && at(endIndex+2) == '0')
	}
	var optionalSpans []captureSpan
	for _, g := range groups {
		if zeroQuantified(g.end) || g.lookaround {
			optionalSpans = append(optionalSpans, g)
		}
	}
	var out []CaptureGroup
	for _, g := range groups {
		if !g.capturing {
			continue
		}
		optional := sawAlternation || zeroQuantified(g.end) || g.lookaround
		if !optional {
			for _, s := range optionalSpans {
				if s.start < g.start && g.end < s.end {
					optional = true
					break
				}
			}
		}
		out = append(out, CaptureGroup{Optional: optional})
	}
	return out, true
}

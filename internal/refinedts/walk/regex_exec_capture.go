// RegExp.prototype.exec's capture groups (sec-regexp.prototype.exec):
// a literal-regex receiver's `.exec(s)` answers null or a match array
// — index 0 the whole match, index N the N-th capturing group's own
// text. A capturing group's VALUE LANGUAGE is its sub-pattern
// compiled through the SAME door `.regex` uses (refinementsets.
// FormatGrammar), anchored ^...$ so the compile denotes exactly what
// the group matched rather than a substring-anywhere reading. A group
// under a quantifier that always participates is never undefined on a
// match; CaptureGroupsOf (regex_capture_groups.go) already answers
// which groups a successful match can leave unset — that same
// optionality reading is reused here rather than re-derived.
//
// This file adds ONE more thing regex_capture_groups.go's CaptureGroup
// does not carry: the group's own SOURCE TEXT, so its language can be
// compiled. The span walk below mirrors CaptureGroupsOf's bracket/
// escape/class handling exactly (same special-group detection), kept
// as a second, purpose-built walk rather than widening the exported
// CaptureGroup type — regex_capture_groups.go's own doc note says its
// struct is read by an existing caller (.match) whose shape stays
// fixed.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// execCaptureSpan is one capturing group's source span, alongside the
// same optionality CaptureGroupsOf computes.
type execCaptureSpan struct {
	// innerStart/innerEnd bound the group's OWN sub-pattern, excluding
	// the enclosing "(" and ")" — the text FormatGrammar compiles.
	innerStart int
	innerEnd   int
	optional   bool
}

// captureGroupSourceSpans walks a pattern's capturing groups in order,
// pairing each with its inner source text's rune bounds and the
// optionality CaptureGroupsOf's own rules assign (a zero-admitting
// quantifier on the group or an enclosing construct, a lookaround
// around it, or an alternation in a scope enclosing it). (nil, false)
// on an unbalanced pattern, the same refusal shape CaptureGroupsOf
// answers.
func captureGroupSourceSpans(pattern string) ([]execCaptureSpan, bool) {
	type span struct {
		// open is the position of the group's OWN "(" — the coordinate
		// CaptureGroupsOf's containment check compares (a nested group's
		// "(" always sits strictly after its enclosing group's "("). start
		// is the sub-pattern's own bound — past "(?:"/"(?<name>" for the
		// two special forms this walk still descends into — the text
		// FormatGrammar compiles.
		open       int
		start      int
		end        int
		capturing  bool
		lookaround bool
		alternated bool
	}
	var groups []span
	var stack []int
	inClass := false
	topAlternated := false
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
			if len(stack) == 0 {
				topAlternated = true
			} else {
				groups[stack[len(stack)-1]].alternated = true
			}
			continue
		}
		if c == '(' {
			special := at(i+1) == '?'
			named := hasPrefixAt(i, "(?<") && at(i+3) != '=' && at(i+3) != '!'
			// innerStart sits right after the opening delimiter: "(" for
			// a capturing group, past "(?:" / "(?<name>" for the two
			// forms whose own contents this walk still descends into (a
			// non-capturing group is not itself a numbered group, but a
			// capturing group nested inside one still needs its outer
			// bound read past the right delimiter)
			innerStart := i + 1
			if special {
				if named {
					// skip to the closing ">" of (?<name>
					j := i + 3
					for j < len(runes) && runes[j] != '>' {
						j++
					}
					innerStart = j + 1
				} else if at(i+2) == ':' {
					innerStart = i + 3
				}
			}
			groups = append(groups, span{
				open:       i,
				start:      innerStart,
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
	var optionalSpans []span
	for _, g := range groups {
		if zeroQuantified(g.end) || g.lookaround || g.alternated {
			optionalSpans = append(optionalSpans, g)
		}
	}
	var out []execCaptureSpan
	for _, g := range groups {
		if !g.capturing {
			continue
		}
		optional := topAlternated || zeroQuantified(g.end) || g.lookaround
		if !optional {
			for _, s := range optionalSpans {
				if s.open < g.open && g.end < s.end {
					optional = true
					break
				}
			}
		}
		out = append(out, execCaptureSpan{innerStart: g.start, innerEnd: g.end, optional: optional})
	}
	return out, true
}

// captureGroupSetOf compiles capturing group `index`'s (1-based, as
// RegExp numbers them) own sub-pattern to the language it denotes: the
// same FormatGrammar door `.regex` uses, with the sub-pattern anchored
// ^...$ so the compile reads as "exactly what this group matched"
// rather than FormatGrammar's own unanchored substring-anywhere
// padding. (AbstractValue{}, false, false) when the index is out of
// range or the sub-pattern falls outside the supported grammar —
// silence, never a guessed set.
func captureGroupSetOf(pattern string, flags string, index int) (refinementsets.RefinedSet, bool, bool) {
	spans, ok := captureGroupSourceSpans(pattern)
	if !ok || index < 1 || index > len(spans) {
		return refinementsets.RefinedSet{}, false, false
	}
	group := spans[index-1]
	runes := []rune(pattern)
	if group.innerStart < 0 || group.innerEnd < group.innerStart || group.innerEnd > len(runes) {
		return refinementsets.RefinedSet{}, false, false
	}
	inner := string(runes[group.innerStart:group.innerEnd])
	compiled := refinementsets.FormatGrammar("^"+inner+"$", flags)
	if !compiled.Ok {
		return refinementsets.RefinedSet{}, false, group.optional
	}
	return compiled.Set, true, group.optional
}

// regexLiteralReceiverOf reads a `.exec` call's receiver as a regex
// LITERAL, or a const bound to one — the same resolution
// readStringMatchWithConstRegex's regexLiteralOf performs for `.match`,
// reused here under the receiver's own name (`re.exec(s)` rather than
// `s.match(re)`).
func regexLiteralReceiverOf(ctx *FlowContext, receiverExpression *ast.Node) *ast.Node {
	if ast.IsRegularExpressionLiteral(receiverExpression) {
		return receiverExpression
	}
	if !ast.IsIdentifier(receiverExpression) {
		return nil
	}
	symbol := symbolAt(ctx.P.Checker, receiverExpression)
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
	return varDecl.Initializer
}

// readRegExpExecCall models `re.exec(s)` for a LITERAL regex receiver
// (or a const bound to one): null, or a match array whose index 0 is
// the STRING ground (the whole match's text is not exactly known
// without running the match) and index N is capturing group N's own
// VALUE LANGUAGE, compiled by captureGroupSetOf. A group a successful
// match can leave unset (CaptureGroupsOf's optionality) wraps in the
// maybe marker; one that always participates under its match reads as
// a plain KindSet with no wrapper — sec-regexp.prototype.exec: "the
// result can be either... String, or undefined" per capturing group.
//
// A STICKY pattern reads the regex object's own mutable lastIndex,
// which this walk does not track, so only a non-sticky pattern is
// read (the same restriction the .match model applies).
func readRegExpExecCall(site MethodCallSite) *abstractdomain.AbstractValue {
	if site.Method != "exec" {
		return nil
	}
	call := site.E.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	if len(arguments) != 1 {
		return nil
	}
	pattern := regexLiteralReceiverOf(site.Ctx, site.ReceiverExpression)
	if pattern == nil {
		return nil
	}
	literal := pattern.Text()
	lastSlash := strings.LastIndex(literal, "/")
	if lastSlash <= 0 {
		return nil
	}
	source := literal[1:lastSlash]
	flags := literal[lastSlash+1:]
	if strings.Contains(flags, "y") {
		return nil
	}
	spans, ok := captureGroupSourceSpans(source)
	if !ok {
		return nil
	}
	oracleGrade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(site.Receiver))
	// index 0: the whole match's text. Its exact value depends on
	// running the match against the argument, which this reader does
	// not do (unlike .match's exact-receiver path in
	// string_method_models.go, which round-trips through Go's regexp
	// engine on an EXACT string — this reader answers the sort-level
	// claim for ANY string argument, exact or not, per group). The spec
	// pins it as a string.
	stringOut := abstractdomain.KnownSet(refinementsets.Strings, nil, oracleGrade, abstractdomain.SetKindTagNone)
	items := make([]abstractdomain.AbstractValue, 0, len(spans)+1)
	items = append(items, stringOut)
	for i := range spans {
		set, compiled, optional := captureGroupSetOf(source, flags, i+1)
		var element abstractdomain.AbstractValue
		if compiled {
			element = abstractdomain.KnownSet(set, nil, oracleGrade, abstractdomain.SetKindTagNone)
		} else {
			// the group's own sub-pattern falls outside the supported
			// grammar (backreferences, lookaround, an unmodeled flag): the
			// slot still exists at match time, but its language stays the
			// walk's own gap rather than a guessed set
			element = silence.Residue()
		}
		if optional {
			items = append(items, abstractdomain.PossiblyUndefined(element, "", false, false))
		} else {
			items = append(items, element)
		}
	}
	innerList := abstractdomain.KnownList(items, abstractdomain.TrustProved)
	out := abstractdomain.PossiblyUndefined(innerList, "", false, false)
	return &out
}

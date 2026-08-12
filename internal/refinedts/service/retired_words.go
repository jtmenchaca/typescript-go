// Retired identifiers from VOCABULARY.md. Live checker .ts must not
// revive them — the gate's interface is this list, parsed from the
// retired column. English words (`judge`, `Stated`) stay out; only
// camelCase / CONSTANT_CASE identifiers count.
//
// Ported 1:1 from service/retired_words.ts.
package service

import (
	"regexp"
	"sort"
	"strings"
)

var retiredCellBacktick = regexp.MustCompile("`")
var retiredCellParenthetical = regexp.MustCompile(`\([^)]*\)`)
var retiredCellSplitter = regexp.MustCompile(`[^A-Za-z0-9_]+`)
var retiredTableHeaderCells = regexp.MustCompile(`^\|\s*-+`)
var codeIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var constantCasePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
var camelCasePattern = regexp.MustCompile(`[a-z][A-Z]`)

// RetiredIdentifiersFrom reports the code identifiers in a VOCABULARY
// retired cell. Only tables whose header is `retired` count.
// Parentheticals and prose are dropped; a token counts when it is
// camelCase or CONSTANT_CASE (not a single English word).
func RetiredIdentifiersFrom(markdown string) []string {
	ids := map[string]struct{}{}
	inRetiredTable := false
	for _, line := range strings.Split(markdown, "\n") {
		if !strings.HasPrefix(line, "|") {
			inRetiredTable = false
			continue
		}
		if retiredTableHeaderCells.MatchString(line) {
			continue
		}
		rawCells := strings.Split(line, "|")
		if len(rawCells) < 2 {
			continue
		}
		cells := []string{}
		for _, c := range rawCells[1 : len(rawCells)-1] {
			cells = append(cells, strings.TrimSpace(c))
		}
		if len(cells) < 2 {
			continue
		}
		if cells[0] == "retired" {
			inRetiredTable = true
			continue
		}
		if !inRetiredTable {
			continue
		}
		for _, token := range tokenize(cells[0]) {
			if isCodeIdentifier(token) {
				ids[token] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// RetiredHitsIn reports which retired identifiers appear as whole
// words in source.
func RetiredHitsIn(source string, retired []string) []string {
	hits := []string{}
	for _, id := range retired {
		pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\b`)
		if pattern.MatchString(source) {
			hits = append(hits, id)
		}
	}
	return hits
}

func tokenize(cell string) []string {
	cell = retiredCellBacktick.ReplaceAllString(cell, "")
	cell = retiredCellParenthetical.ReplaceAllString(cell, " ")
	parts := retiredCellSplitter.Split(cell, -1)
	result := []string{}
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func isCodeIdentifier(token string) bool {
	if !codeIdentifierPattern.MatchString(token) {
		return false
	}
	if len(token) < 4 {
		return false
	}
	if constantCasePattern.MatchString(token) {
		return len(token) >= 8
	}
	return camelCasePattern.MatchString(token)
}

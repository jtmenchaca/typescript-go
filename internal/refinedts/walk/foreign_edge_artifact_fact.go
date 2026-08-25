// Reading one named function's fact: its entry positions, its return
// cases, its provenance, and the small message-rendering helpers those
// readings share.

package walk

import (
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// functionFactOf reads one named function's row: its entry positions,
// its return, and the provenance a cross-language message renders.
// targetBytes is the target's own bytes, already read (and hash-
// verified) by checkTargetIntegrity — passed through so the provenance
// step's line span is computed from that one read, never a second one.
func functionFactOf(
	parsed map[string]any, name string, artifactPath string, targetPath string, targetBytes []byte,
) (fact *ForeignFunctionFact, sentence string) {
	// DecodeWireSet panics on a form it does not know — its own stated
	// contract for kernel answers. An artifact is a file another program
	// wrote, so a malformed form is a decline here, never a crash.
	defer func() {
		if recovered := recover(); recovered != nil {
			fact, sentence = nil, artifactPath+" states a set this checker's kernel grammar does not read, "+
				"so the fact for "+name+" cannot be decoded"
		}
	}()
	functions, ok := parsed["functions"].(map[string]any)
	if !ok {
		return nil, artifactPath + " carries no functions, so it states no fact about " + name
	}
	row, ok := functions[name].(map[string]any)
	if !ok {
		return nil, artifactPath + " names " + name + " as the surface's called function and " +
			"then states no fact for it"
	}
	entries, entriesSentence := artifactEntriesOf(row, name, artifactPath)
	if entriesSentence != "" {
		return nil, entriesSentence
	}
	returned, ok := row["return"].(map[string]any)
	if !ok {
		return nil, artifactPath + " states no return fact for " + name +
			", so nothing crosses back from this call"
	}
	rawCases, hasCases := returned["cases"]
	if !hasCases {
		return nil, artifactPath + " states a return for " + name + " with no cases, " +
			"so the value crossing back is unbounded"
	}
	cases, casesSentence := casesOf(rawCases, name, artifactPath)
	if casesSentence != "" {
		return nil, casesSentence
	}
	stdoutPure, _ := returned["stdoutPure"].(bool)
	return &ForeignFunctionFact{
		Name:  name,
		Entry: entries,
		Return: ForeignReturn{
			Cases:      cases,
			StdoutPure: stdoutPure,
		},
		Provenance: artifactProvenanceOf(row, targetPath, targetBytes),
	}, ""
}

// artifactEntriesOf reads the entry rows in the order the artifact
// spells them — that order IS the positional order of the target's
// parameters, which is how an argument finds the row it must fit.
func artifactEntriesOf(
	row map[string]any, name string, artifactPath string,
) ([]ForeignEntry, string) {
	rawEntries, ok := row["entry"].([]any)
	if !ok {
		return nil, artifactPath + " states no entry positions for " + name +
			", so nothing says what the target admits"
	}
	entries := make([]ForeignEntry, 0, len(rawEntries))
	for index, rawEntry := range rawEntries {
		entryRow, ok := rawEntry.(map[string]any)
		if !ok {
			return nil, artifactPath + " states an unreadable entry position " +
				strconv.Itoa(index) + " for " + name
		}
		entryName, _ := entryRow["name"].(string)
		if sequence, isSequence := entryRow["sequence"].(map[string]any); isSequence {
			element, hasElement := sequence["element"].(map[string]any)
			if !hasElement {
				return nil, artifactPath + " states a sequence entry " + entryName +
					" for " + name + " with no element"
			}
			rawElementCases, hasCases := element["cases"]
			if !hasCases {
				return nil, artifactPath + " states a sequence entry " + entryName +
					" for " + name + " whose element states no cases"
			}
			elementCases, casesSentence := casesOf(rawElementCases, entryName, artifactPath)
			if casesSentence != "" {
				return nil, casesSentence
			}
			lengthAtLeast, _ := sequence["lengthAtLeast"].(float64)
			entries = append(entries, ForeignEntry{
				Name:          entryName,
				IsSequence:    true,
				ElementCases:  elementCases,
				LengthAtLeast: int(lengthAtLeast),
			})
			continue
		}
		rawCases, hasCases := entryRow["cases"]
		if !hasCases {
			return nil, artifactPath + " states an entry position " + entryName +
				" for " + name + " that is neither a sequence nor a cases list"
		}
		cases, casesSentence := casesOf(rawCases, entryName, artifactPath)
		if casesSentence != "" {
			return nil, casesSentence
		}
		entries = append(entries, ForeignEntry{
			Name:  entryName,
			Cases: cases,
		})
	}
	return entries, ""
}

// casesOf reads a "cases" JSON array into []Case — the RULED schema's
// own union arm list. A number/string case requires its own "set",
// decoded through the SAME kernelbridge.DecodeWireSet every other set
// on this edge goes through; a boolean/null case carries no set at
// all; an object case requires its own "members" object (a key ->
// cases-list map, read recursively through this same function — a
// member's cases may themselves carry object cases) and "closed"
// (defaulting to false when absent, the honest reading for a producer
// that states no completeness claim at all — "closed" unstated is
// never assumed true). Every element is read STRICTLY: an unreadable
// element, a missing/unrecognized "sort", a number/string case missing
// its "set", or an object case missing its "members" all decline by
// name — a cases list is a claim another program's checker made, and
// a malformed member is a defect in that claim, never a value to
// guess past.
func casesOf(raw any, forName string, artifactPath string) ([]Case, string) {
	rawList, ok := raw.([]any)
	if !ok {
		return nil, artifactPath + " states a \"cases\" field for " + forName +
			" that is not a JSON array, so nothing says which sorts it admits"
	}
	if len(rawList) == 0 {
		return nil, artifactPath + " states an empty \"cases\" list for " + forName +
			", so nothing crosses at that position"
	}
	cases := make([]Case, 0, len(rawList))
	for index, rawCase := range rawList {
		caseRow, ok := rawCase.(map[string]any)
		if !ok {
			return nil, artifactPath + " states an unreadable case " + strconv.Itoa(index) +
				" for " + forName
		}
		sort, _ := caseRow["sort"].(string)
		switch CaseSort(sort) {
		case CaseSortNumber, CaseSortString:
			rawSet, hasSet := caseRow["set"]
			if !hasSet {
				return nil, artifactPath + " states a " + sort + " case for " + forName +
					" with no set, so nothing bounds that case's members"
			}
			cases = append(cases, Case{Sort: CaseSort(sort), Set: kernelbridge.DecodeWireSet(rawSet)})
		case CaseSortBoolean, CaseSortNull:
			cases = append(cases, Case{Sort: CaseSort(sort)})
		case CaseSortObject:
			rawMembers, hasMembers := caseRow["members"].(map[string]any)
			if !hasMembers {
				return nil, artifactPath + " states an object case for " + forName +
					" with no \"members\" object, so nothing says which keys it holds"
			}
			members := make(map[string][]Case, len(rawMembers))
			for key, rawMemberCases := range rawMembers {
				memberCases, memberSentence := casesOf(rawMemberCases, forName+"'s key '"+key+"'", artifactPath)
				if memberSentence != "" {
					return nil, memberSentence
				}
				members[key] = memberCases
			}
			closed, _ := caseRow["closed"].(bool)
			cases = append(cases, Case{Sort: CaseSortObject, Members: members, Closed: closed})
		default:
			return nil, artifactPath + " states a case for " + forName + ` of sort ` + quotedOrNone(sort) +
				`, and this edge reads only "number", "string", "boolean", "null", or "object"`
		}
	}
	return cases, ""
}

// artifactProvenanceOf reads where the target's claim was made. Absent
// fields leave the provenance empty rather than declining — provenance
// makes a message readable; it is not a premise of the crossing.
//
// targetBytes is the SAME bytes checkTargetIntegrity already read (nil
// when that premise failed, in which case reading gets no further than
// here anyway) — the line's byte span is computed from them, never
// from a fresh read.
func artifactProvenanceOf(row map[string]any, targetPath string, targetBytes []byte) ForeignProvenance {
	provenance, ok := row["provenance"].(map[string]any)
	if !ok {
		return ForeignProvenance{File: targetPath}
	}
	line, _ := provenance["line"].(float64)
	said, _ := provenance["said"].(string)
	result := ForeignProvenance{File: targetPath, Line: int(line), Said: said}
	if result.Line > 0 && targetBytes != nil {
		text := string(targetBytes)
		if start, length, ok := lineSpan(text, result.Line); ok {
			result.Text = text
			result.Start = start
			result.Length = length
		}
	}
	return result
}

// lineSpan answers the byte offset and length of ONE-BASED line
// number `line` in text, spanning column 1 to the line's last byte
// before its terminating '\n' (or before EOF, on the file's last
// line) — never including the newline itself. Answers ok=false for a
// line number the text does not have (the artifact and the target
// have drifted, or line is 0/negative), and the caller leaves the
// provenance step to degrade to the file's head, exactly as
// StepInForeignFile already does for an empty Text.
//
// Mirrors fact_export.rs's own line_starts_of/line_of: line starts are
// offset 0 and every offset right after a '\n', so line N's start is
// starts[N-1] and its own 1-based number is what the producer writes
// as provenance.line.
func lineSpan(text string, line int) (start int, length int, ok bool) {
	if line <= 0 {
		return 0, 0, false
	}
	lineStart := 0
	lineIndex := 1
	for lineIndex < line {
		next := strings.IndexByte(text[lineStart:], '\n')
		if next < 0 {
			return 0, 0, false
		}
		lineStart += next + 1
		lineIndex++
	}
	end := strings.IndexByte(text[lineStart:], '\n')
	if end < 0 {
		end = len(text) - lineStart
	}
	return lineStart, end, true
}

// ProvenanceSentence renders the target's own step of the explanation
// as flat text: where the fact was said, and what was said there.
// foreign_edge.go's four diagnostic sites carry the same information
// as a real related-information step instead (StepInForeignFile,
// built from this same File/Text/Start/Length); this renderer stays
// for a caller that only has message text to work with.
func (p ForeignProvenance) ProvenanceSentence() string {
	if p.File == "" {
		return ""
	}
	where := p.File
	if p.Line > 0 {
		where += ":" + strconv.Itoa(p.Line)
	}
	if p.Said == "" {
		return "the target states this at " + where
	}
	return where + " said: " + p.Said
}

// nestedString reads parsed[outer][inner] as a string.
func nestedString(parsed map[string]any, outer string, inner string) (string, bool) {
	object, ok := parsed[outer].(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := object[inner].(string)
	return value, ok
}

// quotedOrNone spells a surface channel for a message: the word it
// states, or "nothing" where the field is absent.
func quotedOrNone(word string) string {
	if word == "" {
		return "nothing"
	}
	return `"` + word + `"`
}

// foreignSetWords is how a set reaches a diagnostic here — the same
// formatter every refinement diagnostic uses, so a crossing's message
// reads like any other refutation.
func foreignSetWords(set refinementsets.RefinedSet) string {
	words := refinementsets.FormatForDiagnostics(set)
	return strings.TrimSpace(words)
}

// from evaluation/string_method_models.ts
//
// The case-mapping and trimming rows: the UNCONDITIONAL SpecialCasing.txt
// table, the conditional-casing gate that declines rather than folding
// the wrong default mapping, and the argument-free string reads
// (toUpperCase/toLowerCase/trim family) that ride on top of them. Split
// from string_method_models.go per file-length discipline.

package walk

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// normalizedText is one of the four Unicode Normalization Forms applied
// to a text (sec-string.prototype.normalize, whose forms are exactly
// UAX #15's NFC, NFD, NFKC and NFKD). x/text's norm package carries the
// Unicode Character Database's own decomposition and composition data,
// so the answer is the standard's, not an approximation. A form name
// outside the four answers ("", false) — that call raises a RangeError
// and carries no value.
func normalizedText(form string, text string) (string, bool) {
	switch form {
	case "NFC":
		return norm.NFC.String(text), true
	case "NFD":
		return norm.NFD.String(text), true
	case "NFKC":
		return norm.NFKC.String(text), true
	case "NFKD":
		return norm.NFKD.String(text), true
	}
	return "", false
}

// specialCasingUpper/specialCasingLower are the UNCONDITIONAL rows of
// SpecialCasing.txt (specifications/unicode/SpecialCasing.txt, the
// "Unconditional mappings" section) — the multi-code-point case
// mappings that hold regardless of language or surrounding text
// (sec-string.prototype.touppercase, sec-string.prototype.tolowercase
// point at the Unicode Character Database's full case mappings, which
// is UnicodeData.txt's simple 1:1 rows plus this table). The
// CONDITIONAL section (Greek final sigma, Turkic/Lithuanian/Azeri
// dotted-I) is a separate, context-or-language-sensitive table and is
// NOT folded here — hasConditionalCasing gates those code points to a
// decline instead.
var specialCasingUpper = map[rune]string{
	0x00DF: "SS",     // LATIN SMALL LETTER SHARP S
	0xFB00: "FF",     // LATIN SMALL LIGATURE FF
	0xFB01: "FI",     // LATIN SMALL LIGATURE FI
	0xFB02: "FL",     // LATIN SMALL LIGATURE FL
	0xFB03: "FFI",    // LATIN SMALL LIGATURE FFI
	0xFB04: "FFL",    // LATIN SMALL LIGATURE FFL
	0xFB05: "ST",     // LATIN SMALL LIGATURE LONG S T
	0xFB06: "ST",     // LATIN SMALL LIGATURE ST
	0x0587: "ԵՒ", // ARMENIAN SMALL LIGATURE ECH YIWN
	0xFB13: "ՄՆ", // ARMENIAN SMALL LIGATURE MEN NOW
	0xFB14: "ՄԵ", // ARMENIAN SMALL LIGATURE MEN ECH
	0xFB15: "ՄԻ", // ARMENIAN SMALL LIGATURE MEN INI
	0xFB16: "ՎՆ", // ARMENIAN SMALL LIGATURE VEW NOW
	0xFB17: "ՄԽ", // ARMENIAN SMALL LIGATURE MEN XEH
}

// specialCasingLower holds the unconditional rows whose LOWERCASE
// column differs from the code point itself — only 0130 (LATIN
// CAPITAL LETTER I WITH DOT ABOVE), which lowercases to "i" plus a
// combining dot above (U+0307), never a bare "i".
var specialCasingLower = map[rune]string{
	0x0130: "i̇",
}

// hasConditionalCasing reports whether text holds a code point whose
// full case mapping depends on context or language (SpecialCasing.txt
// §Conditional Mappings — Greek Σ's final-sigma rule, and the
// Turkic/Lithuanian/Azeri dotted-I rows) — the receiver declines
// rather than folding the wrong (default) mapping.
func hasConditionalCasing(text string) bool {
	for _, r := range text {
		switch r {
		case 0x03A3, 0x0069, 0x0130, 0x0049, 0x0131:
			return true
		}
	}
	return false
}

// mapCase applies special first, then Go's unicode.ToUpper/ToLower
// (UnicodeData.txt's simple 1:1 mapping) to every code point not in
// the table.
func mapCase(text string, special map[rune]string, simple func(rune) rune) string {
	var b strings.Builder
	for _, r := range text {
		if mapped, ok := special[r]; ok {
			b.WriteString(mapped)
			continue
		}
		b.WriteRune(simple(r))
	}
	return b.String()
}

// exactZeroArgStringRow computes the argument-free string reads on an
// exact receiver text — the case-mapping and trimming rows of
// readStringMethods, split out so their transcription is testable on
// its own. ("", false) where no row speaks.
func exactZeroArgStringRow(method string, text string) (string, bool) {
	switch method {
	// Default Case Conversion (sec-string.prototype.touppercase,
	// sec-string.prototype.tolowercase) is UnicodeData.txt's simple
	// mapping (unicode.ToUpper/ToLower) PLUS SpecialCasing.txt's
	// unconditional multi-code-point table (specialCasingUpper —
	// e.g. "ß" -> "SS"). What remains untranscribed is the
	// CONTEXT/LANGUAGE-sensitive section of SpecialCasing.txt (Greek
	// final sigma, Turkic/Lithuanian/Azeri dotted-I) — a receiver
	// holding one of those code points declines.
	case "toUpperCase":
		if hasConditionalCasing(text) {
			return "", false
		}
		return mapCase(text, specialCasingUpper, unicode.ToUpper), true
	case "toLowerCase":
		if hasConditionalCasing(text) {
			return "", false
		}
		return mapCase(text, specialCasingLower, unicode.ToLower), true
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

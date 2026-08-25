// from evaluation/coercion_models.ts
//
// The Number-target coercion primitives: StringToNumber (Number(str)'s
// own grammar) and the parseInt/parseFloat string grammars. Split from
// coercion_models.go per file-length discipline.

package walk

import (
	"math"
	"strconv"
	"strings"
)

// jsParseWhiteSpaceCutset is parseInt/parseFloat's own leading
// whitespace cutset (sec-parseint-string-radix, sec-parsefloat-string
// both trim WhiteSpace/LineTerminator via TrimString): the ASCII
// controls plus NO-BREAK SPACE (U+00A0) and ZERO WIDTH NO-BREAK SPACE
// / BOM (U+FEFF), spelled through rune literals rather than a string
// literal so the BOM code point never lands as a raw byte inside the
// source file.
var jsParseWhiteSpaceCutset = string([]rune{' ', '\t', '\n', '\r', '\v', '\f', 0x00A0, 0xFEFF})

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
	trimmed := strings.TrimLeft(text, jsParseWhiteSpaceCutset)
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
	trimmed := strings.TrimLeft(text, jsParseWhiteSpaceCutset)
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

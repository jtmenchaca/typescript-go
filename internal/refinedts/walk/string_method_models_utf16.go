// from evaluation/string_method_models.ts
//
// UTF-16 code-unit primitives — JS string reads are unit-indexed
// (sec-ecmascript-language-types-string-type), which the exact reads in
// string_method_models.go must mirror exactly rather than reading Go's
// (rune-indexed) strings directly. Split from string_method_models.go
// per file-length discipline.

package walk

import "strings"

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

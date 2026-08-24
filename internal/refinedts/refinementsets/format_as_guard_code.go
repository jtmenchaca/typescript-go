// Runnable-guard and z-chain formatting: code the reader can paste.
// Number.isInteger(r) && r >= 0, z.number().int().min(1). Only forms
// the narrowing layer verifiably lifts appear, so a guard shown is a
// guard that works -- nil beats an unprovable example. Split from
// display.ts per the v2 tree.

package refinementsets

import (
	"math"
	"sort"
)

func codeNumber(x float64) string {
	if x == math.Inf(1) {
		return "Infinity"
	}
	if x == math.Inf(-1) {
		return "-Infinity"
	}
	return formatJSNumber(x)
}

// FormatAsSchemaChain is the set spelled as the surface's own chain --
// the reverse of the reader: z.number().int().min(1).max(9) for the
// folded scalar forms, and finitely many removed points as a readable
// refine (.refine((v) => v !== 13)) the reader reads back to exactly
// this set. ok=false where a form has no chain spelling (the sequence
// forms, unions) -- no contract is offered rather than a wrong one. A
// single exact value spells as its literal.
func FormatAsSchemaChain(r RefinedSet) (string, bool) {
	if len(r.Forms) == 0 {
		return "", false
	}
	pool := append([]Refinement{}, FoldedForms(r)...)
	if len(pool) == 0 {
		return "", false
	}
	// peel differences: each contributes its base's constraints back
	// into the pool and its finitely many removed points to the refine
	var removed []float64
	for peeled := 0; peeled < 8; peeled++ {
		at := -1
		for i, f := range pool {
			if f.Form == FormDifference {
				at = i
				break
			}
		}
		if at == -1 {
			break
		}
		f := pool[at]
		b := FoldedForms(*f.B)
		if len(b) != 1 || b[0].Form != FormOneOf || len(b[0].W) == 0 {
			return "", false
		}
		removed = append(removed, b[0].W...)
		aFolded := FoldedForms(*f.A_)
		newPool := append([]Refinement{}, pool[:at]...)
		newPool = append(newPool, aFolded...)
		newPool = append(newPool, pool[at+1:]...)
		pool = newPool
	}
	for _, f := range pool {
		if f.Form == FormDifference {
			return "", false
		}
	}
	if len(removed) == 0 && len(pool) == 1 && pool[0].Form == FormOneOf && len(pool[0].W) == 1 {
		return "z.literal(" + codeNumber(pool[0].W[0]) + ")", true
	}
	var calls []string
	integral := false
	for _, f := range pool {
		switch f.Form {
		case FormInteger:
			integral = true
		case FormAtLeast:
			calls = append(calls, ".min("+codeNumber(f.A)+")")
		case FormAbove:
			calls = append(calls, ".gt("+codeNumber(f.A)+")")
		case FormAtMost:
			calls = append(calls, ".max("+codeNumber(f.A)+")")
		case FormBelow:
			calls = append(calls, ".lt("+codeNumber(f.A)+")")
		case FormMultipleOf:
			calls = append(calls, ".multipleOf("+codeNumber(f.A)+")")
		case FormOneOf, FormEmptyTuple, FormConcatenation, FormStar, FormRepeat, FormRepeatWord, FormUnion, FormDifference, FormWord:
			// no chain spelling -- a wrong contract is worse than none
			return "", false
		default:
			UnreachedForm(f)
		}
	}
	distinct := distinctSortedFloats(removed)
	refine := ""
	if len(distinct) > 0 {
		parts := make([]string, len(distinct))
		for i, v := range distinct {
			parts[i] = "v !== " + codeNumber(v)
		}
		refine = ".refine((v) => " + joinStrings(parts, " && ") + ")"
	}
	result := "z.number()"
	if integral {
		result += ".int()"
	}
	result += joinStrings(calls, "")
	result += refine
	return result, true
}

func distinctSortedFloats(xs []float64) []float64 {
	seen := make(map[float64]bool)
	var out []float64
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Float64s(out)
	return out
}

// FormatAsGuardCode is the comparison that PROVES the set, spelled
// over name -- code a diagnostic can show as the repair. The narrowing
// layer lifts comparisons, ===  (with || folding to a union), and
// Number.isInteger, so every guard spelled here is one the checker
// verifiably accepts. ok=false where a folded form has no liftable
// guard (multiple-of, the sequence forms) -- no example is shown
// rather than an unprovable one.
func FormatAsGuardCode(name string, r RefinedSet) (string, bool) {
	if len(r.Forms) == 0 {
		return "", false
	}
	folded := FoldedForms(r)
	if len(folded) == 0 {
		return "", false
	}
	var parts []string
	for _, f := range folded {
		switch f.Form {
		case FormAtLeast:
			parts = append(parts, name+" >= "+codeNumber(f.A))
		case FormAbove:
			parts = append(parts, name+" > "+codeNumber(f.A))
		case FormAtMost:
			parts = append(parts, name+" <= "+codeNumber(f.A))
		case FormBelow:
			parts = append(parts, name+" < "+codeNumber(f.A))
		case FormInteger:
			parts = append(parts, "Number.isInteger("+name+")")
		case FormOneOf:
			if len(f.W) == 0 {
				return "", false
			}
			if len(f.W) == 1 {
				parts = append(parts, name+" === "+codeNumber(f.W[0]))
			} else {
				eqs := make([]string, len(f.W))
				for i, v := range f.W {
					eqs[i] = name + " === " + codeNumber(v)
				}
				parts = append(parts, "("+joinStrings(eqs, " || ")+")")
			}
		case FormMultipleOf, FormEmptyTuple, FormConcatenation, FormStar, FormRepeat, FormRepeatWord, FormUnion, FormDifference, FormWord:
			// no liftable guard -- no example beats an unprovable one
			return "", false
		default:
			UnreachedForm(f)
		}
	}
	return joinStrings(parts, " && "), true
}

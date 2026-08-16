// formatObjectAnnotation / formatKeyValue, ported from the TS
// source's abstract_domain/format_abstract_values.ts. They live HERE
// rather than in abstractdomain because they read
// annotations.ObjectAnnotation, which abstractdomain must not import
// (the dependency order its own header states); service imports both
// sides, so the formatter joins the hover stack it serves —
// GO-LSP-EDITOR-PATH.md §11.9 step 1's "lift the formatter beside
// annotations" resolution.

package service

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// FormatObjectAnnotation is formatObjectAnnotation in the TS source:
// an object STATEMENT rendered for hover — each key with what it
// states, a `?` where the count admits absence, the same shape the
// developer wrote in the z.object. ok=false is the TS null (nothing
// to show).
func FormatObjectAnnotation(object *annotations.ObjectAnnotation) (string, bool) {
	if object == nil || len(object.Keys) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(object.Keys))
	for _, key := range object.Keys {
		name := key.Name
		if key.MayBeAbsent {
			name += "?"
		}
		keys = append(keys, name+": "+formatKeyValue(key.Value))
	}
	return "{" + strings.Join(keys, ", ") + "}", true
}

func formatKeyValue(value annotations.ObjectKeyValue) string {
	switch value.Kind {
	case annotations.KeyValueSet:
		// a symbol key's whole claim is its sort; a bigint key says
		// its sort and then its windows — integrality is the sort's
		// own fact, so the word "integer" would only repeat it
		if value.KindTag == "symbol" {
			return "symbol"
		}
		if value.KindTag == "bigint" {
			var bare []refinementsets.Refinement
			for _, form := range value.Set.Forms {
				if form.Form != refinementsets.FormInteger {
					bare = append(bare, form)
				}
			}
			if len(bare) == 0 {
				return "bigint"
			}
			shown, ok := refinementsets.FormatForHover(refinementsets.MakeRefinedSet(bare...))
			if !ok {
				return "bigint"
			}
			return "bigint, " + bareBraces(shown)
		}
		// the key speaks its WORD where one rides — `email`, never the
		// grammar's algebra — and unread says so in the same breath
		wordText, wordCovers, hasWord := "", 0, false
		if value.Word != nil {
			wordText, wordCovers, hasWord = value.Word.Text, value.Word.Covers, true
		}
		var shown string
		shownOk := false
		if value.Set != nil {
			shown, shownOk = abstractdomain.FormatWordedSet(*value.Set, wordText, wordCovers, hasWord, value.Unread)
		}
		// a DEPENDENT bound rides beside the base facts, speaking
		// about the same 𝑥 the other facts do — `𝑥 ≥ lo`
		depends := dependentBoundWords(value.Depends)
		// the key states no more than its type — say that, never "any"
		if !shownOk {
			if depends == "" {
				return "unconstrained"
			}
			return depends
		}
		if !strings.HasPrefix(shown, "{") {
			if depends == "" {
				return shown
			}
			return shown + ", " + depends
		}
		inner := shown[1 : len(shown)-1]
		joined := inner
		if depends != "" {
			joined = inner + ", " + depends
		}
		if strings.Contains(joined, ", ") {
			return "(" + joined + ")"
		}
		return joined
	case annotations.KeyValueObject:
		shown, ok := FormatObjectAnnotation(value.Object)
		if !ok {
			return "{}"
		}
		return shown
	case annotations.KeyValueCollection:
		inner := func(set *refinementsets.RefinedSet) string {
			if set == nil {
				return "unconstrained"
			}
			shown, ok := refinementsets.FormatForHover(*set)
			if !ok {
				return "unconstrained"
			}
			return bareBraces(shown)
		}
		// the size window speaks only beyond "any size" — integrality
		// and nonnegativity are what a size IS
		var stated []refinementsets.Refinement
		if value.Size != nil {
			for _, form := range value.Size.Forms {
				if form.Form != refinementsets.FormInteger &&
					!(form.Form == refinementsets.FormAtLeast && form.A == 0) {
					stated = append(stated, form)
				}
			}
		}
		sizeWords := ""
		if len(stated) > 0 {
			statedSet := refinementsets.MakeRefinedSet(stated...)
			sizeWords = ", size " + strings.ReplaceAll(inner(&statedSet), "𝑥 ", "")
		}
		if value.Flavor == "map" {
			return "Map {" + inner(value.Key) + " → " + inner(value.Value) + sizeWords + "}"
		}
		return "Set {" + inner(value.Value) + sizeWords + "}"
	case annotations.KeyValueReference:
		if value.Target == nil {
			return "→ ?"
		}
		return "→ " + value.Target.Name
	}
	return "unconstrained"
}

// dependentBoundWords spells dependent bounds — `𝑥 ≥ lo, 𝑥 < hi` —
// shared by the key formatter and the stated-answer path.
func dependentBoundWords(depends []annotations.DependentBound) string {
	if len(depends) == 0 {
		return ""
	}
	parts := make([]string, 0, len(depends))
	for _, dep := range depends {
		op := "<"
		switch dep.Op {
		case "ge":
			op = "≥"
		case "gt":
			op = ">"
		case "le":
			op = "≤"
		}
		parts = append(parts, "𝑥 "+op+" "+dep.Param)
	}
	return strings.Join(parts, ", ")
}

// bareBraces strips one outer brace pair — the TS
// `shown.startsWith("{") ? shown.slice(1, -1) : shown` idiom.
func bareBraces(shown string) string {
	if strings.HasPrefix(shown, "{") && strings.HasSuffix(shown, "}") {
		return shown[1 : len(shown)-1]
	}
	return shown
}

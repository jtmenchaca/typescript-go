// The narrowing dispatch seam's answer spelling for the derivation
// trace (DERIVATION-TRACE.md). Sets are spelled by the kernel's own
// diagnostic formatter — refinementsets.FormatForm, the same spelling
// every other set in this tree carries — never a second one invented
// here.
//
// Only ever called from inside a derivation.Active() guard, so a run
// with no trace requested never builds any of these strings.

package narrowing

import (
	"sort"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// spellNarrowings states what a guard proved, per branch: which place
// the narrowing lands on, and the set each branch admits there.
func spellNarrowings(branch BranchNarrowings) string {
	var parts []string
	if whenTrue := spellBranch(branch.WhenTrue); whenTrue != "" {
		parts = append(parts, "true → "+whenTrue)
	}
	if whenFalse := spellBranch(branch.WhenFalse); whenFalse != "" {
		parts = append(parts, "false → "+whenFalse)
	}
	return strings.Join(parts, "; ")
}

// spellBranch is one branch's narrowings, one place per entry, in a
// stable order — a trace that reorders between runs is not a trace a
// conformance diff can read.
func spellBranch(narrowings []Narrowed) string {
	var spelled []string
	for _, n := range narrowings {
		place := n.Binding
		for _, key := range n.Path {
			place += "." + key
		}
		spelled = append(spelled, place+" "+spellNarrowed(n))
	}
	sort.Strings(spelled)
	return strings.Join(spelled, ", ")
}

// spellNarrowed is one place's claim: its refinement forms in the
// kernel's spelling, whether those forms hold only weakly, plus every
// structural channel the forms do not carry (definedness, truthiness,
// an exact pinned tuple, a proven shape, an excluded kind, a word set
// held or excluded, an excluded boolean word, the Array brand, a
// refuted instanceof brand). Every field Narrowed can carry gets a
// clause here — a trace that drops one silently sends diagnosis at
// whatever DID get printed.
func spellNarrowed(n Narrowed) string {
	var parts []string
	for _, form := range n.Forms {
		parts = append(parts, refinementsets.FormatForm(form))
	}
	// Refuting marks the whole claim WEAK — it holds only for a value
	// already known real, so applying it to an unknown binding would
	// smuggle NaN in. Two narrowings that otherwise print identically
	// but differ in Refuting apply to different sets of values; the
	// trace names that difference rather than printing identical text
	// for both.
	if n.Refuting {
		parts = append(parts, "weakly (known-real only)")
	}
	if n.Definedness != "" {
		parts = append(parts, n.Definedness)
	}
	if n.KeepAbsent {
		parts = append(parts, "keeps absent")
	}
	if n.Truthiness != "" {
		parts = append(parts, n.Truthiness)
	}
	// a held equality pins the EXACT tuple; a string equality pins the
	// codepoint WORD, a numeric one the value set — the two spellings the
	// kernel's own formatter already has for those shapes
	if len(n.Exact) > 0 {
		exact := refinementsets.OneOf(n.Exact)
		if n.ExactSort == abstractdomain.PrimitiveString {
			exact = refinementsets.Word(n.Exact)
		}
		parts = append(parts, refinementsets.FormatForm(exact))
	}
	// a proven shape — the ground a typeof word names, the object an
	// instanceof holds for, the key an `in` finds — spelled with the
	// same inline formatter a hover uses for a nested value, so the
	// trace never claims "no set" where a real shape rides
	if n.HasShape {
		parts = append(parts, "shape "+spellShape(n.Shape))
	}
	if n.ExcludesKind != "" {
		parts = append(parts, "excludes "+n.ExcludesKind)
	}
	if n.HasWordSet {
		parts = append(parts, "one of "+spellWordSet(n.WordSet))
	}
	if n.HasExcludesBooleanWord {
		parts = append(parts, "excludes "+strconv.FormatBool(n.ExcludesBooleanWord != 0))
	}
	if n.HasWordSetExcluded {
		parts = append(parts, "excludes "+spellWordSet(n.WordSetExcluded))
	}
	if n.SequenceBrand {
		parts = append(parts, "Array")
	}
	if n.RefutedBrand != "" {
		parts = append(parts, "excludes "+n.RefutedBrand)
	}
	if len(parts) == 0 {
		return "no set"
	}
	return strings.Join(parts, " ∧ ")
}

// spellShape is a proven Shape's own claim. FormatAbstractValueInline
// is the hover's inline formatter, but a hover assumes a host type
// line stands beside it and elides a claim that repeats that type
// (e.g. the bare number ground reads as nothing extra, since the
// hover's `number` line already says it) — that elision has nothing to
// lean on in a trace, so a shape the hover formatter declines falls
// back to the kernel's own form-by-form spelling of the set inside,
// unwrapping the possibly-NaN / possibly-absent wrappers this walk's
// grounds carry and naming what they add.
func spellShape(shape abstractdomain.AbstractValue) string {
	if shown, ok := abstractdomain.FormatAbstractValueInline(shape); ok {
		return shown
	}
	switch shape.Kind {
	case abstractdomain.KindPossiblyNaN:
		return spellShape(*shape.Inner) + ", or NaN"
	case abstractdomain.KindPossiblyUndefined:
		return spellShape(*shape.Inner) + ", or absent"
	case abstractdomain.KindSet:
		var forms []string
		for _, f := range shape.Set.Forms {
			forms = append(forms, refinementsets.FormatForm(f))
		}
		if len(forms) == 0 {
			return "any value"
		}
		return strings.Join(forms, " ∧ ")
	default:
		return "unformattable"
	}
}

// spellWordSet is a disjunction of literal-equality WORDS, each spelled
// the same way a single string equality already is (refinementsets.Word
// through the kernel's own formatter), joined the way spellNarrowings
// joins branches.
func spellWordSet(words [][]float64) string {
	spelled := make([]string, len(words))
	for i, w := range words {
		spelled[i] = refinementsets.FormatForm(refinementsets.Word(w))
	}
	return strings.Join(spelled, " | ")
}

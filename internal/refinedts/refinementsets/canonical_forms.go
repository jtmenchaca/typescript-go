// Structural canonicalization — the SPELLING hygiene that keeps a
// grown set readable by the collapse machinery and small on the wire.
// Every rewrite here is a syntactic EQUALITY (the same members under
// the same semantics), never an approximation:
//
//   - a UNION arm structurally equal to another arm is the same set
//     said twice — one copy stays;
//   - a union tree flattens to its arm list (union is associative),
//     and a one-arm union IS that arm, whose conjuncts merge into the
//     surrounding forms list;
//   - `atMost +inf` and `atLeast -inf` hold of every member of ℝ̄, so
//     beside any other conjunct they add nothing;
//   - a conjunct structurally equal to another conjunct is the same
//     claim said twice — one copy stays;
//   - two same-direction bound conjuncts collapse to the tightest ray
//     (FoldRayForms): atLeast(3) beside atLeast(5) is atLeast(5) alone,
//     a strict bound wins its tie against a non-strict one at the same
//     value (above(3) dominates atLeast(3)), and the dual holds for the
//     upper class (atMost/below).
//
// Why this exists: joins wrap a fresh Union node per round and meets
// CONCATENATE forms lists, so a loop-fixpoint candidate reached the
// kernel as intersections-of-unions of near-duplicates — modest bytes,
// but the kernel's DNF distribution multiplies union widths per level
// and its negation multiplies again, so one `invariant` ask on such a
// spelling ran effectively forever (createCategoricalInverse.ts's
// bisect hung the recharts wall). The integer-run collapse in
// JoinKnown already answers the SEMANTIC half of this problem; this
// file restores its sight by keeping the spelling plain.

package refinementsets

import "math"

// CanonicalScalarForms rewrites a set's spelling by the equalities
// above, recursively through union arms. The members are untouched.
func CanonicalScalarForms(set RefinedSet) RefinedSet {
	forms := canonicalFormList(set.Forms)
	if len(forms) == 0 {
		// every conjunct was vacuous — the set is ℝ̄* said with no
		// forms, which the kernel's questions do not accept; keep one
		// vacuous conjunct as the spelling
		return MakeRefinedSet(AtLeast(math.Inf(-1)))
	}
	return RefinedSet{Forms: forms}
}

func canonicalFormList(forms []Refinement) []Refinement {
	out := make([]Refinement, 0, len(forms))
	for _, form := range forms {
		if form.Form == FormUnion {
			arms := flattenUnionArms(RefinedSet{Forms: []Refinement{form}})
			deduped := make([]RefinedSet, 0, len(arms))
			for _, arm := range arms {
				canon := CanonicalScalarForms(arm)
				duplicate := false
				for _, held := range deduped {
					if sameSetJSON(held, canon) {
						duplicate = true
						break
					}
				}
				if !duplicate {
					deduped = append(deduped, canon)
				}
			}
			if len(deduped) == 1 {
				// a one-arm union IS the arm — its conjuncts join the
				// surrounding list
				out = append(out, deduped[0].Forms...)
				continue
			}
			rebuilt := deduped[len(deduped)-1]
			for i := len(deduped) - 2; i >= 0; i-- {
				rebuilt = MakeRefinedSet(Union(deduped[i], rebuilt))
			}
			out = append(out, rebuilt.Forms...)
			continue
		}
		out = append(out, form)
	}
	// same-direction bound conjuncts collapse to the tightest ray per
	// class (FoldRayForms, refinement_forms.go): atLeast(3) beside
	// atLeast(5) is atLeast(5) alone, and a strict/non-strict tie at the
	// same bound favors the strict form (above(3) dominates atLeast(3),
	// since x > 3 is the stronger claim). This is the same fold the
	// narrowing seam already asks the kernel through
	// (abstractdomain/intersect_refinements.go) — applied here so the
	// SPELLING already carries the dominance a later ask would answer,
	// rather than leaving two live conjuncts that say the same bound
	// twice at different tightness.
	out = FoldRayForms(out)
	// vacuous conjuncts drop beside any other conjunct
	kept := make([]Refinement, 0, len(out))
	for _, form := range out {
		if isVacuousBound(form) {
			continue
		}
		kept = append(kept, form)
	}
	if len(kept) == 0 {
		kept = out[:min(1, len(out))]
	}
	// a conjunct said twice stays once
	deduped := make([]Refinement, 0, len(kept))
	for _, form := range kept {
		duplicate := false
		for _, held := range deduped {
			if sameSetJSON(MakeRefinedSet(form), MakeRefinedSet(held)) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			deduped = append(deduped, form)
		}
	}
	return deduped
}

// flattenUnionArms reads a set as the flat list of its union arms: a
// single-form union recurses into both sides, anything else is one arm.
func flattenUnionArms(set RefinedSet) []RefinedSet {
	if len(set.Forms) == 1 && set.Forms[0].Form == FormUnion {
		left := flattenUnionArms(*set.Forms[0].A_)
		right := flattenUnionArms(*set.Forms[0].B)
		return append(left, right...)
	}
	return []RefinedSet{set}
}

func isVacuousBound(form Refinement) bool {
	if form.Form == FormAtMost {
		return math.IsInf(form.A, 1)
	}
	if form.Form == FormAtLeast {
		return math.IsInf(form.A, -1)
	}
	return false
}

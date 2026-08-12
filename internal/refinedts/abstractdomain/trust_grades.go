// The TRUST GRADE a piece of knowledge carries — the weakest
// boundary in its derivation (TRUST.md). A backend LEDGER, never
// developer-facing: hovers and diagnostics do not show it. Absent
// means "proved" (kernel-decided); every reading that crosses a
// weaker boundary stamps the downgrade, and composition takes the
// minimum. Ledger fidelity grows with stamps — a missing stamp
// overstates the ledger, never a verdict.

package abstractdomain

// TrustLevel is TrustLevel in the TS source.
type TrustLevel string

const (
	TrustProved   TrustLevel = "proved"
	TrustSpec     TrustLevel = "spec"
	TrustEngine   TrustLevel = "engine"
	TrustLibrary  TrustLevel = "library"
	TrustAsserted TrustLevel = "asserted"
)

var trustLevelOrder = map[TrustLevel]int{
	TrustProved:  4,
	TrustSpec:    3,
	TrustEngine:  2,
	TrustLibrary: 1,
	// knowledge that crossed a type assertion (a cast): the value's
	// content is kept — the cast changes no runtime value — but the
	// crossing is the weakest boundary there is, admitted only at the
	// dial's "full"
	TrustAsserted: 0,
}

// TrustLevelOf is trustLevelOf in the TS source.
func TrustLevelOf(k AbstractValue) TrustLevel {
	if k.Grade != "" {
		return k.Grade
	}
	return TrustProved
}

// MinTrustLevel is minTrustLevel in the TS source.
func MinTrustLevel(a, b TrustLevel) TrustLevel {
	if trustLevelOrder[a] <= trustLevelOrder[b] {
		return a
	}
	return b
}

// DerivedTrustLevel is derivedTrustLevel in the TS source: the grade a
// DERIVED value wears — the deriving row's own grade met with every
// operand's — the weakest boundary in the derivation, computed by the
// constructor so a site cannot overstate by forgetting a floor.
func DerivedTrustLevel(row TrustLevel, operands ...AbstractValue) TrustLevel {
	held := row
	for _, operand := range operands {
		held = MinTrustLevel(held, TrustLevelOf(operand))
	}
	return held
}

// TrustLevelAdmitted is trustLevelAdmitted in the TS source: does the
// strictness dial admit this grade? "full" admits every boundary; a
// named mode admits that boundary and every stronger one.
//
// The TS source reads STRICTNESS from service/analysis_limits.ts, whose
// only defined value today is "full" (admits every grade unconditionally).
// service/ is not ported yet (go-port-tracker.md: "pending (last)"), so
// that read is inlined as the literal "full" behavior below rather than
// naming an unported constant; when service ports, this reads the real
// dial instead.
func TrustLevelAdmitted(g TrustLevel) bool {
	return true
}

// AtTrustLevel is atTrustLevel in the TS source: the same knowledge, at
// (no better than) the given grade. The unclaimed kinds carry no grade —
// there is nothing to downgrade.
func AtTrustLevel(k AbstractValue, grade TrustLevel) AbstractValue {
	if k.Kind == KindUnknown || k.Kind == KindVariable {
		return k
	}
	held := TrustLevelOf(k)
	lowered := MinTrustLevel(held, grade)
	if lowered == held {
		return k
	}
	out := k
	out.Grade = lowered
	return out
}

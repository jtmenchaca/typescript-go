// from evaluation/membership_ground_models.ts
//
// Membership and sentinel-search grounds under the read-only gate:
// includes / startsWith / endsWith as boolean, indexOf / lastIndexOf
// as an integer at least −1 (capped by a known length when held).

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// readMembershipGroundMethods is readMembershipGroundMethods in the
// TS source: boolean membership / affix tests and sentinel index
// searches. Nil when the method is none of those.
func readMembershipGroundMethods(site MethodCallSite, oracleGrade abstractdomain.TrustLevel) *abstractdomain.AbstractValue {
	receiver, method := site.Receiver, site.Method
	// the BOOLEAN-answering reads: membership and affix tests
	// return a Boolean on every run (sec-array.prototype.includes,
	// sec-string.prototype.includes, sec-string.prototype.startswith,
	// sec-string.prototype.endswith), so even an undecided test
	// determines the boolean ground — a value, never nothing
	if method == "includes" || method == "startsWith" || method == "endsWith" {
		out := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, oracleGrade)
		return &out
	}
	// the SENTINEL searches answer −1 or an index on every run
	// (sec-array.prototype.indexof / lastindexof): an integer at
	// least −1, and a found index is STRICTLY below the length
	// (the spec's k ranges over [0, len)), so a bounded receiver
	// caps the answer at its length − 1 — the callback-taking
	// finders stay with the callback models
	if method == "indexOf" || method == "lastIndexOf" {
		var lengthHi *int
		switch {
		case receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray:
			n := len(receiver.Values)
			lengthHi = &n
		case receiver.Kind == abstractdomain.KindList:
			n := len(receiver.Items)
			lengthHi = &n
		case receiver.Kind == abstractdomain.KindSet && receiver.SetKindTag == abstractdomain.SetKindTagNone:
			if rep, ok := refinementsets.AsRepetition(receiver.Set); ok && rep.Hi != nil {
				lengthHi = rep.Hi
			}
		}
		var set refinementsets.RefinedSet
		if lengthHi == nil {
			set = refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-1))
		} else {
			set = refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(-1), refinementsets.AtMost(float64(*lengthHi-1)))
		}
		out := abstractdomain.KnownSet(set, nil, oracleGrade, abstractdomain.SetKindTagNone)
		return &out
	}
	return nil
}

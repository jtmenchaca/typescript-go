// from control_flow/answer_types.ts
//
// The answer a position returns: printed words, or why there are none.
// Every exit of answerFlowAt constructs one of these. Coverage and
// hover read the same type.

package walk

import "github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"

// Unknown is why there is no answer at a position. A hover shows
// nothing for every one of these, but they do not mean the same
// thing: some are the checker working correctly, one is the checker
// failing. Keeping them apart is what makes a coverage count mean
// anything.
type Unknown struct {
	Why string // "not-a-name" | "annotation-not-read" | "kernel-declined" | "kernel-not-loaded" | "adds-nothing" | "not-tracked" | "out-of-reach" | "noted" | "broke"
	// Said, Unsupported: only set when Why == "noted"
	Said        string
	Unsupported bool
	// Error: only set when Why == "broke"
	Error string
}

// Answer is what a value is known to be, or why that is not
// answerable. The grade is the ledger's read of the claim — the
// weakest boundary in its derivation — absent means proved.
// ShownByHost marks a claim whose words TypeScript's own type line
// already displays.
type Answer struct {
	// Known branch (set when Unknown.Why == "")
	HasKnown    bool
	Known       string
	Grade       abstractdomain.TrustLevel
	HasGrade    bool
	ShownByHost bool
	// Unknown branch
	UnknownValue Unknown
}

// No constructs the "unknown" branch of Answer.
func No(unknown Unknown) Answer {
	return Answer{UnknownValue: unknown}
}

// Claim constructs a stated claim: the grade rides only below
// proved (absent means proved), and the host tag rides only when
// the type line already shows the words.
func Claim(known string, grade abstractdomain.TrustLevel, shownByHost bool) Answer {
	answer := Answer{HasKnown: true, Known: known}
	if grade != abstractdomain.TrustProved {
		answer.Grade = grade
		answer.HasGrade = true
	}
	if shownByHost {
		answer.ShownByHost = true
	}
	return answer
}

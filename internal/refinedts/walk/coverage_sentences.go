// from assignability/coverage_sentences.ts
//
// THE wording module: every coverage sentence lives here, and
// VerdictOf is the one mapping from a silence reason to a sentence
// plus a determined/unsupported verdict. answerFlowAt, coverage, and
// the pins all read this file — nowhere else invents a sentence.

package walk

// Sentence holds every coverage sentence — SENTENCE in the TS
// source.
var Sentence = struct {
	KernelNotLoaded    string
	KernelDeclined     string
	AnnotationNotRead  string
	NotTracked         string
	OutOfReach         string
	AddsNothing        string
	NotAName           string
	WalkStatesNothing  string
	PresentAndAbsent   string
	NothingPins        string
	Ambient            string
	HostClass          string
	FromOutside        string
	SettlementFunction string
}{
	KernelNotLoaded:    "the kernel was not loaded",
	KernelDeclined:     "the kernel declined the question",
	AnnotationNotRead:  "the type annotation is not read as a refinement",
	NotTracked:         "the walk does not follow this name",
	OutOfReach:         "the walk cannot vouch for the state at this position",
	AddsNothing:        "determined — the stated set says exactly what the type says",
	NotAName:           "not a name",
	WalkStatesNothing:  "determined — the walk states nothing beyond the type",
	PresentAndAbsent:   "determined — the present value states nothing beyond the type, and absence rides beside it",
	NothingPins:        "the walk holds nothing that pins this value",
	Ambient:            "an ambient declaration — the type is everything this file determines",
	HostClass:          "the type is a host class — everything the file determines here",
	FromOutside:        "the value arrives from outside this file — the type is everything this file determines",
	SettlementFunction: "the promise's settlement function — a function, read at its calls",
}

// SentenceVerdict is the (said, unsupported) pair VerdictOf answers.
type SentenceVerdict struct {
	Said        string
	Unsupported bool
}

// VerdictOf is verdictOf in the TS source: the sentence for a row
// without a printed set, and the verdict — unsupported means
// something in the file determines more than the checker states; the
// rest are DETERMINED — the checker finished its job and has nothing
// to print beyond the type line.
func VerdictOf(unknown Unknown) SentenceVerdict {
	switch unknown.Why {
	case "broke":
		return SentenceVerdict{Said: "the walk threw: " + unknown.Error, Unsupported: true}
	case "noted":
		return SentenceVerdict{Said: unknown.Said, Unsupported: unknown.Unsupported}
	case "kernel-declined":
		return SentenceVerdict{Said: Sentence.KernelDeclined, Unsupported: true}
	case "kernel-not-loaded":
		return SentenceVerdict{Said: Sentence.KernelNotLoaded, Unsupported: true}
	case "annotation-not-read":
		return SentenceVerdict{Said: Sentence.AnnotationNotRead, Unsupported: true}
	case "not-tracked":
		return SentenceVerdict{Said: Sentence.NotTracked, Unsupported: true}
	case "out-of-reach":
		return SentenceVerdict{Said: Sentence.OutOfReach, Unsupported: true}
	case "adds-nothing":
		return SentenceVerdict{Said: Sentence.AddsNothing, Unsupported: false}
	case "not-a-name":
		return SentenceVerdict{Said: Sentence.NotAName, Unsupported: false}
	}
	// the TS source's switch is exhaustive over Unknown["why"]'s
	// literal union — every case above is one of its members, so this
	// is the "impossible state, loudly" fallback PORT.md's
	// UnreachedForm precedent establishes for an unmatched tag.
	panic("VerdictOf: unrecognized Unknown.Why " + unknown.Why)
}

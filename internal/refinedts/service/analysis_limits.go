// The bounds that change what the checker CONCLUDES.
//
// Every option here decides how hard the checker tries before it
// answers "I don't know". Loosen one and it works out more; tighten
// one and it works out less and says nothing about the difference.
// Nothing here reads an environment variable — you edit this file.
//
// Read cache_tuning.ts (tuning.ts in the TS tree) for the options
// that cannot change a conclusion.
//
// Ported 1:1 from service/analysis_limits.ts.
//
// NOTE for the reader landing later: abstractdomain/trust_grades.go's
// TrustLevelAdmitted currently inlines the "full" behavior below
// rather than reading Strictness from this file (abstractdomain
// ports earlier than service, per PORT.md's port order, and cannot
// import forward). A later pass should point TrustLevelAdmitted at
// Strictness here. Do not edit abstractdomain to do that from this
// unit — this file only lands the real values.
package service

/* ── how far the checker will follow your code ───────────────────── */

// CallDepth: when your code calls a function, the checker reads that
// function's body to work out the exact answer rather than just its
// declared type. Those functions call others, so it goes deeper.
//
// How many levels down it will go. HasCallDepth false (TS `null`)
// follows the code as far as it goes. A number stops there and uses
// the declared type instead — still correct, just vaguer, and
// nothing at the call site says so.
//
// No limit is what the checker did before a bound was ever added. The
// runaway recursion that made a bound look necessary was a missing
// re-entry guard in the callback walk, since fixed; the deepest real
// file measured after that fix was 18 levels.
var CallDepth int
var HasCallDepth bool = false

// ArithmeticFailure: what to ignore when the arithmetic reader fails.
//
// "declined" — ignore only the prover declining a question it checked
// too expensive. That is a real outcome and the value is genuinely
// unknown.
//
// "anything" — also ignore crashes in the checker's own arithmetic
// code, which makes a defect look exactly like an expensive question.
type ArithmeticFailureMode string

const (
	ArithmeticFailureDeclined ArithmeticFailureMode = "declined"
	ArithmeticFailureAnything ArithmeticFailureMode = "anything"
)

var ArithmeticFailure ArithmeticFailureMode = ArithmeticFailureDeclined

/* ── how much work one question may be ───────────────────────────── */

// Nothing on this side. There used to be a pre-gate here that
// estimated a question's cost from its syntax and refused to ask when
// the estimate was too high. It was invalidated, for two reasons that
// are worth keeping written down: it was silent, so a refused
// question and a value nothing was known about read identically; and
// it did not bound what it claimed to — a question could pass it,
// pass the prover's own copy of it, and still exhaust the prover's
// memory.
//
// The prover's own gate is the only one now, and it is final. This
// side asks and MEASURES instead (boundary/observed_cost.ts), so an
// expensive question can be read rather than guessed at.

/* ── which claims the checker acts on ────────────────────────────── */

// Strictness is the strictness dial (TRUST.md's ruled step 4): which
// trust boundaries the checker ACTS on. A claim's grade is the
// weakest boundary in its derivation; this names the weakest boundary
// admitted.
//
// "full" admits every grade — today's behavior, and the default.
// Tightened, a judgment built on inadmissible knowledge does not fire
// (it alerts instead — never a refutation from a boundary the
// workspace distrusts), and an inadmissible claim is not shown — its
// position says the dial held it back.
type StrictnessLevel string

const (
	StrictnessFull    StrictnessLevel = "full"
	StrictnessLibrary StrictnessLevel = "library"
	StrictnessEngine  StrictnessLevel = "engine"
	StrictnessSpec    StrictnessLevel = "spec"
	StrictnessProved  StrictnessLevel = "proved"
)

var Strictness StrictnessLevel = StrictnessFull

// AllowCasts: whether the workspace trusts type assertions outright.
// When true, a sort-changing cast is believed — the checker adopts
// the asserted shape, keeps quiet, and no diagnostic is ever caused
// by a cast. When false (the default), the checker keeps following
// the real value through every cast: the cast quiets the shape
// checker, not this one. A single site can opt out without this
// option by writing `@refinedts-allow-cast` in a comment on the
// cast's line (or the line above it).
var AllowCasts bool = false

/* ── saying so ───────────────────────────────────────────────────── */

// AnnounceGivingUp: print a line whenever the checker stops short — a
// bound reached, a question declined, a repeat visit cut off.
//
// Off, a run tells you nothing about how much it skipped, because
// every one of those looks identical to "there was nothing to say
// about this value".
var AnnounceGivingUp bool = false

/* ── the ones you cannot turn off ────────────────────────────────── */

// Listed so this file is the whole story. Each answers "unknown" and
// reports nothing; each is required for the checker to terminate or
// to stay sound, so none of them is a setting:
//
//   - a function that calls itself, at the three places a call is
//     read by walking its body
//   - a callback that reaches itself again
//   - a guard whose narrowing question the prover declines — the
//     guard then narrows nothing
//   - an emptiness question the prover declines
//   - a question whose filing name would exceed `KeyChars`
//     (cache_tuning.go) — answered, but re-asked every time
//   - a prover that ran out of memory: that question is declined and
//     the prover is rebuilt

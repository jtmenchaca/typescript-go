// The typed question surface. Type-only in the TS source (the loader
// still owns the implementation) — the Go twin is the RefinedTSKernel
// struct kernel_bridge.go builds, with the same method set as methods.
//
// `Structural` and `CheckAssignability` take the specification as its
// ENCODED WIRE STRING (objectgraphs.EncodeSpecification): the
// Specification type lives in objectgraphs, which imports this
// package — Go forbids the cycle a typed parameter would close.
package kernelbridge

import (
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// KernelFault mirrors the TS KernelFault interface — see wire_decode.go
// (the same struct backs DecodeJudgeAnswer's Faults field).

// RefinedTSKernel is the TS RefinedTSKernel interface, as the struct of
// live closures kernel_bridge.go's LoadKernel/KernelFromCalls builds
// (the TS interface is realized the same way, by kernelAsks returning
// an object literal of closures).
type RefinedTSKernel struct {
	// Member: x ∈ A — the runtime check (proved: memberB_iff).
	Member func(set refinementsets.RefinedSet, tuple []float64) bool
	// ScalarEmpty: A = ∅ on the 1-tuple layer — a theorem in both
	// directions (scalarEmptyB_false names a member; scalarEmptyB_true
	// via sat?_complete).
	ScalarEmpty func(set refinementsets.RefinedSet) bool
	// ScalarSubset: A ⊆ B on the 1-tuple layer — the type check, a
	// theorem in both directions.
	ScalarSubset func(a, b refinementsets.RefinedSet) bool
	// ScalarDisjoint: A ∩ B = ∅ on the 1-tuple layer — a theorem in both
	// directions.
	ScalarDisjoint func(a, b refinementsets.RefinedSet) bool
	// SeqEmpty: emptiness for recognized sequence shapes (tuples/arrays
	// of 1-tuple-layer sets) — a theorem in both directions.
	SeqEmpty func(set refinementsets.RefinedSet) bool
	// SeqSubset: subset for recognized sequence shapes. `true` is a
	// theorem (seqSubsetB_true); `false` means no positional proof —
	// read it conservatively (the counterexample construction is owed).
	SeqSubset func(a, b refinementsets.RefinedSet) bool
	// Structural: does the graph specification hold structurally —
	// the wire-string form of the TS structural(spec); the parameter
	// is objectgraphs.EncodeSpecification's output (the typed wrapper
	// lives in objectgraphs — the Specification type is defined there
	// and that package imports this one, so a typed parameter here
	// would close an import cycle).
	Structural func(specWire string) bool
	// CheckAssignability: judge the graph specification — the
	// wire-string form of the TS checkAssignability(spec), same cycle
	// note as Structural.
	CheckAssignability func(specWire string) JudgeAnswer
	// ValidateChain: validate a derivation chain — the certifying seam:
	// every step replays through the proved set functions; a
	// member-terminated chain answering true PROVES membership in the
	// derived set (eval_member_sound).
	ValidateChain func(chain Chain) ValidateChainResult
	// Calendar: the ISO calendar (vendored spec §13.1–13.3, §7.5):
	// epoch-day conversion both ways — the loop-free inverse
	// SELF-CERTIFIES per answer through the normative forward map —
	// date validity, day of week, and duration validity. Refusals panic
	// (the TS "Refusals throw").
	Calendar func(question CalendarQuestion) map[string]any
	// Transfer: the float image of a JavaScript operation, computed on
	// the kernel from the operands' sets: exact singletons pin the very
	// float a conformant runtime returns (incl. the spec's NaN cells),
	// ranges answer certified endpoint bounds with integrality and
	// power-of-two steps. Executable kernel rules; the operator
	// soundness file is in flight (transfers/transfer_correct.lean).
	Transfer func(question TransferQuestion) TransferAnswer
	// Envelope: a proved outward bound on |fl(a op b) − (a op b)| over
	// every admitted integer pair of two integer-marked windows: 0
	// while the result window stays inside ±2^53, half the covering
	// binade's quantum beyond it (transfers/envelope_correct.lean). A
	// nil bool result is a refusal — an unmarked or unbounded window, or
	// one leaving ±2^62 — and the caller keeps its row unwidened there.
	Envelope func(op string, a, b refinementsets.RefinedSet) (value float64, ok bool)
	// LinearImplies: does the held system of linear rows imply the
	// target row — Σ coefs·x ≥ bound per fact, strict when marked,
	// decided by Fourier–Motzkin refutation with positive integer
	// multipliers? A true answer is a theorem over every valuation
	// (transfers/linear_correct.lean, linImpliesB_sound); false is a
	// refusal to claim, never a verdict.
	LinearImplies func(facts []LinearFact, target LinearFact) bool
	// Bounds: the integral hull of a scalar set, computed on the kernel
	// in exact integer arithmetic: for a nonempty integral set with
	// finite edges, the least and greatest members — each bisection
	// step justified by the proved emptiness decider — and for other
	// scalar shapes the proved enclosure unchanged. The empty set says
	// so. A refusal panics.
	Bounds func(set refinementsets.RefinedSet) BoundsResult
	// Members: the members of a small integral scalar set, each
	// confirmed by the proved membership decider — one question where
	// asking member-by-member costs one per candidate. A hull wider
	// than `cap` is declined (panics), never sampled.
	Members func(set refinementsets.RefinedSet, cap int) []float64
	// Decimal: ToString of an integral finite number, computed by the
	// kernel instead of the check-time host — the spec's plain-decimal
	// form below 10^21. A nil-ok result is where the kernel declines
	// (non-integral, or past the plain form's edge): the host's answer
	// stands there, at spec grade.
	Decimal func(value float64) (text string, ok bool)
	// Invariant: the loop-invariant certificate: the entry premise
	// lands inside the candidate AND the step's image lands back
	// inside — one kernel conjunction, with the induction principle
	// proved behind it (invariant_certifies): every iterate of every
	// run stays inside a certified candidate. A premise no route reads
	// answers false — a refusal, never a claim.
	Invariant func(candidate refinementsets.RefinedSet, entry, step InvariantPremise) bool
	// SolveLoop: solve one loop: the checker reads the program (the
	// lowered effects, entry premises, and condition narrowings), the
	// kernel iterates its own proved transfers, widens, and certifies
	// the candidate invariant per binding — withdrawals cascade, so no
	// claim leans on a claim that was withdrawn. The answer is the
	// certified set per binding, or unknown — never a guess.
	SolveLoop func(question LoopQuestion) []LoopVarAnswer
	// Narrow: what a condition proves about the place it tests: the
	// checker reads the guard into a tree of recognized tests, the
	// KERNEL constructs both branches' sets — what truth admits and
	// what falsity admits, each strong (its holding proves the value
	// real) or weak (it holds only for values already known real, the
	// NaN discipline). Proved: transfers/narrow_correct.lean.
	Narrow func(tree NarrowTree) NarrowAnswer
	// JoinState: the join of two knowledge states — what survives a
	// merge of two paths: the union form on the sets, or on each flag.
	// Exact by `join_exact` (set_functions/known_state.lean). The
	// checker's own join mirrors this algebra for speed; the
	// conformance suite holds the mirror to this entry.
	JoinState func(a, b KnownStateWire) KnownStateWire
	// NarrowState: a structural narrowing on a knowledge state —
	// definedness, truthiness under the number or string sort, or
	// strict equality against a real word. Both sides come back as
	// states; each is a proved filter (the narrow*_sound theorems,
	// set_functions/known_state.lean). The `w` operand rides only with
	// op "eq" — NarrowStateOp captures that as (op, w, hasW).
	NarrowState func(state KnownStateWire, op string, w float64, hasW bool) (whenTrue, whenFalse KnownStateWire)
	// Walk: walk one lowered body — assignments through the proved
	// transfers, branches split by the proved narrowings and joined by
	// the proved exact join — and answer every binding's exit state
	// (set_functions/walk.lean). The beginning of the shared engine:
	// the first question that carries a program shape whole.
	Walk func(states []KnownStateWire, stmts []IrStatement) []KnownStateWire
	// InitMs: wall-clock ms for glue factory + Lean runtime init.
	InitMs float64
}

// The certified constant fixpoint for a RECURSIVE declaration.
//
// A recursive f cannot splice its own summary, so its self-calls lower
// as HAVOC and its blob answers top wherever the recursion carried the
// value. That havoc tier is the FLOOR — sound, and often useless. This
// file goes on top of it: when f's return is set-shaped (booleans,
// enums, literal unions, a bounded numeric window), a CONSTANT summary
// `constSummary R` — a summary that answers R for every entry — can be
// proposed by iteration and then CERTIFIED by the kernel.
//
// The shape of the argument, which is the design's depth induction:
// compile f's body with its self table entry bound to `constSummary R`,
// apply that compile at ALL-TOP entries (top admits every concrete
// entry), and read the ret. If that ret is inside R, then by strong
// induction on recursion depth every concrete run of f returns
// something R admits — a depth-k run's self-calls are depth-<k runs,
// whose returns R admits by the induction hypothesis, so the
// constSummary claim holds exactly where the run reads it, and
// walk_sound carries the body around it.
//
// Go's half of that is PROPOSE and ASK. The proposal is heuristic and
// proves nothing; the certification is a kernel question and proves
// everything. Nothing here reasons about set structure on the Go side
// to decide containment — the containment questions all go to the
// proved deciders (ScalarSubset, SeqSubset) or, where those decline the
// shape, to the enclosure the wire states themselves expose (Bounds).
//
// Wire formats are untouched: `constSummary R` is built by asking the
// kernel to summarize a one-statement body, `assign retIndex (const R)`,
// at the declaration's own arity. summarize's identity-out property —
// its out vector IS the binding row — makes that compile exactly the
// constant summary: every entry runs the one assignment, and the ret
// out-state is R no matter what came in.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// fixpointRounds is how many proposal rounds run before the iteration
// gives up and certifies whatever it holds. The loop solver's own
// candidate takes three join-iterations before widening; this takes the
// same three, for the same reason: a set that has not settled in three
// rounds is not going to settle by counting further, and widening is
// what closes it instead.
const fixpointRounds = 3

// constSummaryBuilder is the seam the fixpoint compiles its constant
// summaries through — replaced in tests so the iteration can be driven
// without a kernel. nil means the production route (askConstSummary).
var constSummaryBuilder func(arity int, retIndex int, set kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool)

// applySummaryAsker is the seam the fixpoint applies through, same
// reason. nil means kernelbridge.AskApplySummary.
var applySummaryAsker func(blob kernelbridge.SummaryBlob, entries []kernelbridge.KnownStateWire) ([]kernelbridge.KnownStateWire, bool)

// setContainmentAsker is the seam the CERTIFICATION asks through: does
// the proved decider confirm a ⊆ b. nil means the kernel's own
// ScalarSubset / SeqSubset.
var setContainmentAsker func(a, b refinementsets.RefinedSet) (subset bool, decided bool)

// selfConstCompiler is the seam the ROUND compiles through: re-lower the
// declaration with its self entry bound to the given blob, compile, and
// answer the compiled blob beside the shape to read the ret out of. nil
// means the production route (relowerWithSelfConst).
var selfConstCompiler func(ctx *FlowContext, declaration *ast.Node, selfBlob kernelbridge.SummaryBlob) (kernelbridge.SummaryBlob, LoweredSummary, bool)

// askConstSummary builds `constSummary R` WITHOUT touching any wire
// format: the kernel is asked to summarize a body of one statement —
// `assign retIndex (const R)` — at the declaration's own arity. Every
// other slot's out-state is its entry, untouched, which is exactly what
// a constant summary should say about slots it does not write; the ret
// out-state is R for every entry, which is the constant claim itself.
//
// The state rides as a constState effect so R's UNDEF, NULL and NaN
// ride-alongs travel with it, each its own flag: a plain const effect
// carries only the set, and an R that admits undefined, null, or NaN
// would silently lose them.
func askConstSummary(arity int, retIndex int, state kernelbridge.KnownStateWire) (kernelbridge.SummaryBlob, bool) {
	if state.Top {
		// no constant claim to make — top is what the havoc floor already says
		return "", false
	}
	// every NON-ret slot is assigned unknownE, which evaluates to TOP:
	// the certificate theorem's constancy hypothesis (ConstSelf,
	// transfers/recursion_correct.lean) demands the constant summary
	// answer C at the ret and TOP everywhere else — an identity out
	// (the entry riding through) would claim the caller's entry state
	// admits the callee's arbitrary exit, which nothing proves. Built
	// through summarize, so summarize_ok covers its well-formedness.
	statements := make([]kernelbridge.IrStatement, 0, arity)
	for slot := 0; slot < arity; slot++ {
		if slot == retIndex {
			statements = append(statements, kernelbridge.IrStatement{
				Kind:   kernelbridge.IrStatementAssign,
				Target: slot,
				Effect: kernelbridge.LoopEffect{
					Kind:  kernelbridge.LoopEffectConstState,
					Set:   state.Set,
					Undef: state.Undef,
					Null:  state.Null,
					Nan:   state.Nan,
				},
			})
			continue
		}
		statements = append(statements, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: slot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
		})
	}
	// no table: a constant body indexes no callee
	return kernelbridge.AskSummarize(arity, statements, nil)
}

// constSummaryBlobFor builds the declaration's constant self-summary
// for the candidate state R — arity equal to the declaration's own
// lowered SlotCount, so the entry numbering the caller's splice sends
// and the numbering this compile expects are the same number, and the
// ret written at the declaration's own retIndex so callers reading the
// stored out-shape find the answer where they already look.
func constSummaryBlobFor(
	ctx *FlowContext,
	declaration *ast.Node,
	state kernelbridge.KnownStateWire,
) (kernelbridge.SummaryBlob, bool) {
	shape, lowered := LowerSummaryBody(ctx, declaration)
	if !lowered {
		return "", false
	}
	build := constSummaryBuilder
	if build == nil {
		build = askConstSummary
	}
	return build(shape.SlotCount, shape.RetIndex, state)
}

// topEntriesFor is the entry vector the fixpoint applies at: every slot
// TOP, except the done flag, which enters {0} exactly as a real call's
// entry does. Top admits every concrete entry, which is what makes a
// claim read off this application a claim about every run.
func topEntriesFor(shape LoweredSummary) []kernelbridge.KnownStateWire {
	entries := make([]kernelbridge.KnownStateWire, shape.SlotCount)
	for i := range entries {
		entries[i] = kernelbridge.KnownStateWire{Top: true}
	}
	if shape.DoneIndex >= 0 && shape.DoneIndex < len(entries) {
		entries[shape.DoneIndex] = doneDownState
	}
	return entries
}

// retStateOf applies a blob at all-top entries and reads the ret
// out-state. A refused ask, a short answer, or a top ret all answer
// (zero, false) — nothing to propose and nothing to certify.
func retStateOf(
	blob kernelbridge.SummaryBlob,
	shape LoweredSummary,
) (kernelbridge.KnownStateWire, bool) {
	apply := applySummaryAsker
	if apply == nil {
		apply = kernelbridge.AskApplySummary
	}
	exits, ok := apply(blob, topEntriesFor(shape))
	if !ok || shape.RetIndex >= len(exits) {
		return kernelbridge.KnownStateWire{}, false
	}
	ret := exits[shape.RetIndex]
	if ret.Top {
		return kernelbridge.KnownStateWire{}, false
	}
	return ret, true
}

// compileWithSelfConst re-lowers the declaration with its OWN table
// entry bound to `constSummary R`, compiles that, and answers the ret
// the compile produces at all-top entries.
//
// The injection runs through the registry's self-blob override: while
// the override is held, SummaryBlobFor answers the const blob for this
// declaration, so the body's self-call lowers as a REAL call statement
// against a table entry this function controls instead of re-entering
// the in-flight build and havocking. The re-lowering skips the memo
// (RelowerSummaryBody) because each round lowers the same declaration
// under a different self entry.
func compileWithSelfConst(
	ctx *FlowContext,
	declaration *ast.Node,
	candidate kernelbridge.KnownStateWire,
) (kernelbridge.KnownStateWire, bool) {
	selfBlob, built := constSummaryBlobFor(ctx, declaration, candidate)
	if !built {
		return kernelbridge.KnownStateWire{}, false
	}
	compile := selfConstCompiler
	if compile == nil {
		compile = relowerWithSelfConst
	}
	blob, lowered, ok := compile(ctx, declaration, selfBlob)
	if !ok {
		return kernelbridge.KnownStateWire{}, false
	}
	return retStateOf(blob, lowered)
}

// relowerWithSelfConst is the production half of that: hold the self
// override, re-lower off the memo, release, and compile the statements
// with the table the re-lowering built. The override is released before
// the compile — the compile reads only the statements and the table,
// both of which the re-lowering already resolved.
func relowerWithSelfConst(
	ctx *FlowContext, declaration *ast.Node, selfBlob kernelbridge.SummaryBlob,
) (kernelbridge.SummaryBlob, LoweredSummary, bool) {
	release := holdSelfBlob(declaration, selfBlob)
	lowered, ok := RelowerSummaryBody(ctx, declaration)
	release()
	if !ok {
		return "", LoweredSummary{}, false
	}
	blob, asked := kernelbridge.AskSummarize(lowered.SlotCount, lowered.Stmts, lowered.Table)
	if !asked {
		return "", LoweredSummary{}, false
	}
	return blob, lowered, true
}

/* ── proposing ───────────────────────────────────────────────────── */

// joinStates is the proposal's join: the union of the two sets, and
// each ride-along flag raised if either side raises it. The union form
// is what the kernel's own join builds, so the proposed candidate stays
// inside the grammar the certification asks about.
func joinStates(a, b kernelbridge.KnownStateWire) kernelbridge.KnownStateWire {
	if a.Top || b.Top {
		return kernelbridge.KnownStateWire{Top: true}
	}
	flags := kernelbridge.KnownStateWire{
		Undef: a.Undef || b.Undef, Null: a.Null || b.Null, Nan: a.Nan || b.Nan,
	}
	// two sides that SPELL the same need no union: `A ∪ A` is A, and
	// writing it as a union would grow the candidate's syntax every round
	// and keep the iteration from ever reading as stable
	if kernelbridge.EncodeSet(a.Set) == kernelbridge.EncodeSet(b.Set) {
		flags.Set = a.Set
		return flags
	}
	flags.Set = refinementsets.MakeRefinedSet(refinementsets.Union(a.Set, b.Set))
	return flags
}

// widenState is the loop solver's keep-stable-drop-moving reading,
// applied to a SET's enclosure rather than to one abstract value's
// bounds: ask the kernel for each round's integral hull, keep an edge
// that stopped moving between the two rounds, and drop a moving edge to
// its infinity — which, spelled as a set, means the widened candidate
// keeps only the bounds both hulls agree on.
//
// Every bound here is read from the KERNEL's Bounds answer, never
// derived Go-side: this function chooses which of the kernel's own
// stated edges to keep, and the certification then has to confirm the
// choice anyway.
//
// A shape Bounds declines, or an empty hull, answers the joined
// candidate unwidened — the certification is the boundary either way.
func widenState(
	kernel *kernelbridge.RefinedTSKernel,
	previous, current kernelbridge.KnownStateWire,
) kernelbridge.KnownStateWire {
	if kernel == nil || kernel.Bounds == nil || previous.Top || current.Top {
		return current
	}
	previousHull, previousOk := boundsOf(kernel, previous.Set)
	currentHull, currentOk := boundsOf(kernel, current.Set)
	if !previousOk || !currentOk {
		return current
	}
	previousLow, previousHigh, previousRead := enclosureOf(previousHull)
	currentLow, currentHigh, currentRead := enclosureOf(currentHull)
	if !previousRead || !currentRead {
		return current
	}
	var forms []refinementsets.Refinement
	// a bound that STOPPED MOVING is kept; a moving one is dropped, which
	// is the same as not writing it at all
	if previousLow == currentLow {
		forms = append(forms, refinementsets.AtLeast(currentLow))
	}
	if previousHigh == currentHigh {
		forms = append(forms, refinementsets.AtMost(currentHigh))
	}
	// integrality and the step ride along with the kept bounds, exactly as
	// widenVal carries `int` and `step` through from the current iterate.
	// Both hulls have to state it, or it moved too and drops with the rest.
	for _, form := range currentHull.Hull.Forms {
		if form.Form != refinementsets.FormInteger && form.Form != refinementsets.FormMultipleOf {
			continue
		}
		if hullStatesForm(previousHull, form) {
			forms = append(forms, form)
		}
	}
	if len(forms) == 0 {
		// both edges moved: nothing bounded survives, so there is no
		// constant worth proposing
		return kernelbridge.KnownStateWire{Top: true}
	}
	return kernelbridge.KnownStateWire{
		Set:   refinementsets.MakeRefinedSet(forms...),
		Undef: previous.Undef || current.Undef,
		Null:  previous.Null || current.Null,
		Nan:   previous.Nan || current.Nan,
	}
}

// hullStatesForm answers whether a hull carries the same shape form —
// the same integrality mark, or the same divisor. Spelled comparison,
// so a divisor that changed reads as a different form and drops.
func hullStatesForm(hull kernelbridge.BoundsResult, form refinementsets.Refinement) bool {
	for _, held := range hull.Hull.Forms {
		if held.Form == form.Form && held.A == form.A {
			return true
		}
	}
	return false
}

// boundsOf asks the kernel's Bounds question, folding its refusal (a
// panic, the discipline every question in that package keeps) into the
// false half of a pair.
func boundsOf(
	kernel *kernelbridge.RefinedTSKernel, set refinementsets.RefinedSet,
) (result kernelbridge.BoundsResult, ok bool) {
	defer func() {
		if recover() != nil {
			result, ok = kernelbridge.BoundsResult{}, false
		}
	}()
	answer := kernel.Bounds(set)
	if answer.Empty {
		return kernelbridge.BoundsResult{}, false
	}
	return answer, true
}

// enclosureOf reads the least and greatest edge out of a hull the
// kernel stated. The hull comes back as a conjunction of forms, so the
// two edges are whichever AtLeast/Above and AtMost/Below it carries;
// a hull with neither is unbounded and answers false.
func enclosureOf(hull kernelbridge.BoundsResult) (low, high float64, ok bool) {
	hasLow, hasHigh := false, false
	for _, form := range hull.Hull.Forms {
		switch form.Form {
		case refinementsets.FormAtLeast, refinementsets.FormAbove:
			low, hasLow = form.A, true
		case refinementsets.FormAtMost, refinementsets.FormBelow:
			high, hasHigh = form.A, true
		}
	}
	return low, high, hasLow && hasHigh
}

// proposeConstant runs the iteration: start from the havoc compile's
// own ret at top entries, then repeatedly rebuild the declaration with
// its self entry bound to the current candidate, join the resulting ret
// back in, and stop when the candidate stops moving or the rounds run
// out. Widening closes the last two rounds.
//
// Heuristic throughout — this proves nothing at all. What comes out is
// a CANDIDATE, and certifyConstant is the only thing that can make it a
// claim.
func proposeConstant(
	ctx *FlowContext,
	declaration *ast.Node,
	floorBlob kernelbridge.SummaryBlob,
	shape LoweredSummary,
) (kernelbridge.KnownStateWire, bool) {
	// R0: what the havoc compile already says at top entries
	candidate, ok := retStateOf(floorBlob, shape)
	if !ok {
		return kernelbridge.KnownStateWire{}, false
	}
	kernel := EngineKernelHeld()
	for round := 0; round < fixpointRounds; round++ {
		next, built := compileWithSelfConst(ctx, declaration, candidate)
		if !built {
			// the self-const route did not compile; the candidate in hand is
			// still worth certifying, and certification is where it is judged
			break
		}
		joined := joinStates(candidate, next)
		if joined.Top {
			return kernelbridge.KnownStateWire{}, false
		}
		if round == fixpointRounds-1 {
			joined = widenState(kernel, candidate, joined)
			if joined.Top {
				return kernelbridge.KnownStateWire{}, false
			}
		}
		if sameProposedState(candidate, joined) {
			return candidate, true
		}
		candidate = joined
	}
	return candidate, true
}

// sameProposedState is the iteration's stop test: identical wire text
// and identical ride-along flags. Comparing the ENCODED sets rather
// than the structs is what makes this a decidable stop — two sets that
// spell the same way are the same proposal, and two that do not are
// treated as different even if some decider would call them equal,
// which only costs another round.
func sameProposedState(a, b kernelbridge.KnownStateWire) bool {
	if a.Top != b.Top {
		return false
	}
	if a.Top {
		return true
	}
	return a.Undef == b.Undef && a.Null == b.Null && a.Nan == b.Nan &&
		kernelbridge.StateWire(a) == kernelbridge.StateWire(b)
}

/* ── certifying ──────────────────────────────────────────────────── */

// containedInAsked asks whether a ⊆ b through a PROVED decider. The
// scalar route answers for the 1-tuple layer; the sequence route
// answers for recognized sequence shapes and its `true` is the theorem
// (its `false` is a refusal to claim, so a false there decides
// nothing and this answers undecided rather than "not contained").
//
// A refusal (a panic — the discipline of every question in that
// package) is undecided too.
func containedInAsked(a, b refinementsets.RefinedSet) (subset bool, decided bool) {
	if asker := setContainmentAsker; asker != nil {
		return asker(a, b)
	}
	kernel := EngineKernelHeld()
	if kernel == nil {
		return false, false
	}
	if scalar, ok := scalarSubsetAsked(kernel, a, b); ok {
		if scalar {
			return true, true
		}
		// the scalar decider is a theorem in BOTH directions, so its false
		// is a verdict, not a refusal
		return false, true
	}
	if kernel.SeqSubset == nil {
		return false, false
	}
	if sequence, ok := seqSubsetAsked(kernel, a, b); ok && sequence {
		return true, true
	}
	return false, false
}

func scalarSubsetAsked(
	kernel *kernelbridge.RefinedTSKernel, a, b refinementsets.RefinedSet,
) (subset bool, asked bool) {
	if kernel.ScalarSubset == nil {
		return false, false
	}
	defer func() {
		if recover() != nil {
			subset, asked = false, false
		}
	}()
	return kernel.ScalarSubset(a, b), true
}

func seqSubsetAsked(
	kernel *kernelbridge.RefinedTSKernel, a, b refinementsets.RefinedSet,
) (subset bool, asked bool) {
	defer func() {
		if recover() != nil {
			subset, asked = false, false
		}
	}()
	return kernel.SeqSubset(a, b), true
}

// certifyConstant is the boundary. With candidate R: compile f with its
// self entry bound to `constSummary R`, apply that compile at ALL-TOP
// entries — which admit every concrete entry — and require
//
//   - the answered ret's SET to be inside R's set by a PROVED subset
//     decider, and
//   - R to COVER the answered ret's ride-alongs: an answer that may be
//     absent needs an R that admits absence, an answer that may be NaN
//     needs an R that admits NaN.
//
// Both halves must hold. A decider that does not decide the shape is a
// FAILURE to certify, never a pass: the enclosure the wire states
// expose is what is available from Go, and where it says nothing, this
// says nothing. No Go-side reasoning about set structure decides
// containment here — the only Go-side judgements are the two boolean
// flag comparisons, which are the states' own stated bits.
func certifyConstant(
	ctx *FlowContext,
	declaration *ast.Node,
	candidate kernelbridge.KnownStateWire,
) bool {
	if candidate.Top {
		return false
	}
	answered, built := compileWithSelfConst(ctx, declaration, candidate)
	if !built {
		return false
	}
	if answered.Top {
		return false
	}
	// the ride-alongs: R has to admit whatever the answer may be
	if answered.Undef && !candidate.Undef {
		return false
	}
	if answered.Null && !candidate.Null {
		return false
	}
	if answered.Nan && !candidate.Nan {
		return false
	}
	subset, decided := containedInAsked(answered.Set, candidate.Set)
	return decided && subset
}

/* ── the upgrade ─────────────────────────────────────────────────── */

// upgradeRecursiveSummary is the whole arc, run ONCE per recursive
// declaration right after its havoc-floor build finishes: propose a
// constant by iteration, certify it against the kernel, and answer the
// certified constant's blob. A failure at any step answers false and
// the floor stands — the recursive body still has its havoc summary,
// and its callers still compose it.
//
// What lands in the store is `constSummary R` itself: a caller splices
// it like any other blob and never learns that f recurses.
func upgradeRecursiveSummary(
	ctx *FlowContext, declaration *ast.Node,
) (kernelbridge.SummaryBlob, bool) {
	// ONE upgrade at a time across the process, and every other route
	// held out of the registry while it runs. The self-const override is
	// a registry-wide key that OUTRANKS the store, so a concurrent
	// checker asking for the same declaration mid-round would read the
	// fixpoint's temporary constant as a real answer — a claim nothing
	// has certified yet. The exclusive hold (holdRegistryForFixpoint)
	// closes that: while it is held, every SummaryBlobFor from another
	// goroutine waits, and the only asks that reach the override are the
	// re-lowerings this function drives.
	releaseRegistry := holdRegistryForFixpoint()
	defer releaseRegistry()
	if EngineKernelHeld() == nil {
		return "", false
	}
	shape, lowered := LowerSummaryBody(ctx, declaration)
	if !lowered {
		return "", false
	}
	floorBlob, hasFloor := storedBlobOf(declaration)
	if !hasFloor {
		return "", false
	}
	candidate, proposed := proposeConstant(ctx, declaration, floorBlob, shape)
	if !proposed {
		return "", false
	}
	if !certifyConstant(ctx, declaration, candidate) {
		return "", false
	}
	return constSummaryBlobFor(ctx, declaration, candidate)
}

// storedBlobOf reads the declaration's stored answer — the havoc floor
// the outer build just wrote — without going through SummaryBlobFor,
// which would re-enter the upgrade.
func storedBlobOf(declaration *ast.Node) (kernelbridge.SummaryBlob, bool) {
	summaryBlobsMu.Lock()
	defer summaryBlobsMu.Unlock()
	held, has := summaryBlobs[declaration]
	if !has || !held.Ok {
		return "", false
	}
	return held.Blob, true
}

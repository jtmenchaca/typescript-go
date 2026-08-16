// from control_flow/loop_candidate.ts
//
// Settle an unstable loop candidate: exact-join iterates, kernel
// solve when the body lowers, walker widen-and-certify otherwise,
// then one tightening round and scalar simplification.
//
// CROSS-DIRECTORY: abstractValueOfDeclared is
// assignability/declared_value.ts's function — a concurrent agent's
// FlowContext-reading file joining this package per PORT.md.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

const loopSettleIterations = 3

// SettleLoopCandidateInput mirrors settleLoopCandidate's destructured
// parameter.
type SettleLoopCandidateInput struct {
	Ctx              *FlowContext
	Env              Env
	Loop             *ast.Node
	Silent           *FlowContext
	StepImage        func(fromEnv Env, reporting *FlowContext) Env
	Fixpointed       []string
	Touched          []string
	ConditionWritten map[string]struct{}
	PremiseEnv       Env
	Condition        *ast.Node
	ElementName      string
	HasElementName   bool
	ElementKnown     abstractdomain.AbstractValue
}

// SettleLoopCandidate is settleLoopCandidate in the TS source:
// iterate the body effect over the exact join; what refuses to
// stabilize is handed to the kernel (when the body lowers) or
// widened from the walked iterates and certified. One narrowing
// round recovers precision; scalar simplification only renames.
func SettleLoopCandidate(input SettleLoopCandidateInput) Env {
	ctx := input.Ctx
	env := input.Env

	// ── iterate the effect over the exact join ───────────────────────
	candidate := env.Clone()
	for _, name := range input.Touched {
		// a declared binding written by the CONDITION is not re-armed to
		// its stated set — those writes are unseen, so the invariant is
		// not re-established here
		if _, written := input.ConditionWritten[name]; written {
			continue
		}
		if stated, ok := ctx.Declared[name]; ok {
			candidate.Set(name, AbstractValueOfDeclared(*stated))
		}
	}
	iterates := []Env{candidate.Clone()}
	stable := len(input.Fixpointed) == 0
	for i := 0; i < loopSettleIterations && !stable; i++ {
		stepped := input.StepImage(candidate, input.Silent)
		next := candidate.Clone()
		changed := false
		for _, name := range input.Fixpointed {
			joined := abstractdomain.JoinKnown(envOrResidue(candidate, name), envOrResidue(stepped, name))
			if !sameKnown(joined, envOrResidue(candidate, name)) {
				changed = true
			}
			next.Set(name, joined)
		}
		candidate = next
		iterates = append(iterates, candidate.Clone())
		stable = !changed
	}

	// ── the kernel solves what refused to stabilize ──────────────────
	if !stable {
		// First choice: hand the whole loop to the kernel. The lowering
		// reads the body into the solver's effect grammar; the kernel
		// iterates its own proved transfers, widens, and certifies the
		// candidate per binding (withdrawals cascading, so no claim
		// leans on a withdrawn one). Bindings it answers are settled;
		// the rest fall to the walker-based widen-and-certify below.
		answered := map[string]struct{}{}
		var incrementor *ast.Node
		hasIncrementor := false
		if ast.IsForStatement(input.Loop) {
			incrementor = input.Loop.AsForStatement().Incrementor
			hasIncrementor = incrementor != nil
		}
		lowered, loweredOk := LowerLoopEffect(
			ctx, input.Loop, incrementor, hasIncrementor,
			input.Fixpointed, candidate, input.Condition,
			input.ElementName, input.HasElementName, input.ElementKnown,
		)
		if loweredOk && len(input.Fixpointed) > 0 {
			answers, ok := func() (answers []kernelbridge.LoopVarAnswer, ok bool) {
				defer func() {
					if recover() != nil {
						answers, ok = nil, false
					}
				}()
				// the solved answer's ceiling is the weakest entry premise —
				// composition by minimum
				premiseFloor := abstractdomain.TrustProved
				entry := make([]*kernelbridge.InvariantPremise, len(input.Fixpointed))
				for i, name := range input.Fixpointed {
					known := envOrResidue(input.PremiseEnv, name)
					premiseFloor = abstractdomain.MinTrustLevel(premiseFloor, abstractdomain.TrustLevelOf(known))
					if known.Kind == abstractdomain.KindValues {
						entry[i] = &kernelbridge.InvariantPremise{Kind: kernelbridge.InvariantPremiseValues, Values: known.Values}
					} else if set, ok := abstractdomain.SetOfKnown(known); ok {
						entry[i] = &kernelbridge.InvariantPremise{Kind: kernelbridge.InvariantPremiseSet, Set: set}
					}
				}
				out := ctx.Kernel.SolveLoop(kernelbridge.LoopQuestion{Entry: entry, Cond: lowered.Cond, Body: lowered.Body})
				return out, true
			}()
			if ok {
				for i, answer := range answers {
					if answer.Kind == kernelbridge.LoopVarAnswerSet {
						candidate.Set(input.Fixpointed[i], abstractdomain.KnownSet(answer.Set, nil, premiseFloorOf(input.PremiseEnv, input.Fixpointed), abstractdomain.SetKindTagNone))
						answered[input.Fixpointed[i]] = struct{}{}
					}
				}
			}
		}

		// ── widen the rest from the walked iterates ────────────────────
		// per side: a bound stable across the last two iterates is kept;
		// a moving one widens to ±∞ (certification chooses soundness, so
		// this only chooses precision)
		for _, name := range input.Fixpointed {
			if _, done := answered[name]; done {
				continue
			}
			ranges := make([]*NumberRange, len(iterates))
			anyNil := false
			for i, iterate := range iterates {
				ranges[i] = RangeOfKnown(envOrResidue(iterate, name))
				if ranges[i] == nil {
					anyNil = true
				}
			}
			if anyNil {
				candidate.Set(name, silence.Residue())
				continue
			}
			last := len(ranges) - 1
			lo := ranges[last].Lo
			if ranges[last].Lo != ranges[last-1].Lo {
				lo = math.Inf(-1)
			}
			hi := ranges[last].Hi
			if ranges[last].Hi != ranges[last-1].Hi {
				hi = math.Inf(1)
			}
			int := true
			for _, r := range ranges {
				if !r.Int {
					int = false
					break
				}
			}
			// a step every iterate carries survives the widening — the
			// kernel certifies the candidate either way
			step := math.Inf(1)
			everyStepFinite := true
			for _, r := range ranges {
				if r.Step == nil || math.IsInf(*r.Step, 0) || math.IsNaN(*r.Step) || *r.Step == 1 {
					everyStepFinite = false
					break
				}
				if *r.Step < step {
					step = *r.Step
				}
			}
			if !everyStepFinite {
				step = math.NaN() // marks "no step" below
			}
			hasStep := everyStepFinite && !math.IsNaN(step) && !math.IsInf(step, 0)
			if math.IsInf(lo, -1) && math.IsInf(hi, 1) && !int && !hasStep {
				candidate.Set(name, silence.Residue())
			} else {
				forms := []refinementsets.Refinement{refinementsets.AtLeast(lo), refinementsets.AtMost(hi)}
				if int {
					forms = append(forms, refinementsets.Integer)
				}
				if hasStep {
					forms = append(forms, refinementsets.MultipleOf(step))
				}
				candidate.Set(name, abstractdomain.KnownSet(refinementsets.MakeRefinedSet(forms...), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
			}
		}
		// certify the walker-widened bindings — withdrawals CASCADE: a
		// surviving certificate's step image was computed from every
		// candidate, so when one is withdrawn the survivors re-check
		// with it gone; a claim must never lean on a withdrawn claim.
		// (Kernel-answered bindings hold on their own certificates and
		// never lean on the walker's.)
		recheck := false
		for _, name := range input.Fixpointed {
			if _, done := answered[name]; !done {
				recheck = true
				break
			}
		}
		for recheck {
			recheck = false
			stepped := input.StepImage(candidate, input.Silent)
			for _, name := range input.Fixpointed {
				if _, done := answered[name]; done {
					continue
				}
				invariant := envOrResidue(candidate, name)
				if invariant.Kind != abstractdomain.KindSet {
					continue
				}
				certified := CertifiedInvariant(ctx, envOrResidue(env, name), envOrResidue(stepped, name), invariant.Set)
				if !certified {
					candidate.Set(name, silence.Residue())
					recheck = true
				}
			}
		}

		// ── one narrowing round recovers what the condition cut ────────
		// entry ⊔ step(certified) is tighter (the step ran under the
		// condition's narrowing); the kernel re-certifies the WHOLE
		// tightened environment or none of it — mixed tightenings could
		// otherwise lean on each other
		stepOnce := input.StepImage(candidate, input.Silent)
		tightened := candidate.Clone()
		tightenedAny := false
		for _, name := range input.Fixpointed {
			if envOrResidue(candidate, name).Kind != abstractdomain.KindSet {
				continue
			}
			next := abstractdomain.JoinKnown(envOrResidue(env, name), envOrResidue(stepOnce, name))
			if next.Kind == abstractdomain.KindUnknown {
				continue
			}
			if !sameKnown(next, envOrResidue(candidate, name)) {
				tightenedAny = true
			}
			tightened.Set(name, next)
		}
		if tightenedAny {
			stepAgain := input.StepImage(tightened, input.Silent)
			certifies := true
			for _, name := range input.Fixpointed {
				invariant := envOrResidue(tightened, name)
				if invariant.Kind != abstractdomain.KindSet {
					continue
				}
				if !CertifiedInvariant(ctx, envOrResidue(env, name), envOrResidue(stepAgain, name), invariant.Set) {
					certifies = false
					break
				}
			}
			if certifies {
				candidate = tightened
			}
		}
	}

	// ── the certified facts, said plainly ────────────────────────────
	// The invariant is exact and unruly: a union the fixpoint built,
	// which the condition then narrows. Ask the kernel whether some
	// plainer set holds the very same values, and carry THAT forward —
	// the body's walk, everything downstream of the loop, and any hover
	// over either all read it. Replaced only where the kernel proved
	// the two equal, so this changes how the facts read and never
	// which facts they are.
	simplifier := kernelSimplificationAdapter{ctx.Kernel}
	candidate.Range(func(name string, known abstractdomain.AbstractValue) bool {
		if known.Kind != abstractdomain.KindSet {
			return true
		}
		plainer := refinementsets.SimplifyScalar(simplifier, known.Set)
		if kernelbridge.EncodeSet(plainer) != kernelbridge.EncodeSet(known.Set) {
			out := known
			out.Set = plainer
			candidate.Set(name, out)
		}
		return true
	})
	return candidate
}

// kernelSimplificationAdapter satisfies refinementsets.SimplificationKernel
// (a method-shaped interface) over kernelbridge.RefinedTSKernel (a struct
// of function-typed fields — the Go twin of the TS RefinedTSKernel object
// literal). refinementsets.SimplifyScalar's own comment invites exactly
// this: "Whatever package does port kernel_bridge can satisfy this
// interface directly" — kernelbridge itself cannot (it cannot import
// refinementsets' sibling without a cycle risk being introduced at the
// wrong layer), so the adapter lives here at the first call site instead.
type kernelSimplificationAdapter struct {
	kernel *kernelbridge.RefinedTSKernel
}

// SimplificationKernelOf hands the same adapter to callers outside
// this package (the hover rendering in service simplifies the sets it
// spells the same way the loop candidate does).
func SimplificationKernelOf(kernel *kernelbridge.RefinedTSKernel) refinementsets.SimplificationKernel {
	return kernelSimplificationAdapter{kernel: kernel}
}

func (k kernelSimplificationAdapter) ScalarSubset(a, b refinementsets.RefinedSet) bool {
	return k.kernel.ScalarSubset(a, b)
}

func (k kernelSimplificationAdapter) Bounds(set refinementsets.RefinedSet) refinementsets.BoundsResult {
	b := k.kernel.Bounds(set)
	return refinementsets.BoundsResult{Empty: b.Empty, Hull: b.Hull}
}

func (k kernelSimplificationAdapter) Members(set refinementsets.RefinedSet, cap int) []float64 {
	return k.kernel.Members(set, cap)
}

func premiseFloorOf(premiseEnv Env, fixpointed []string) abstractdomain.TrustLevel {
	floor := abstractdomain.TrustProved
	for _, name := range fixpointed {
		floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(envOrResidue(premiseEnv, name)))
	}
	return floor
}

// sameKnown is sameKnown in the TS source.
func sameKnown(a, b abstractdomain.AbstractValue) bool {
	return abstractdomain.SameKnown(a, b)
}

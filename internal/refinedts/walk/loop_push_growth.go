// from control_flow/loop_push_growth.ts
//
// Grow an after-loop array that the body only ever pushed onto: the
// kernel certifies the sequence, and a literal trip count may pin
// the exact length.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// GrowPushedArraysInput mirrors growPushedArrays' destructured
// parameter.
type GrowPushedArraysInput struct {
	Ctx                *FlowContext
	Env                Env
	Loop               *ast.Node
	Candidate          Env
	After              Env
	Fixpointed         []string
	EvaluateExpression func(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue
}

// GrowPushedArrays is growPushedArrays in the TS source: an array
// the body only ever PUSHES onto grows a sequence over the pushed
// elements' set — the kernel certifies per instance that appending
// an element of S keeps the tuple in S-star, and that the entry
// tuple already sits inside — so every exit, at any iteration count,
// wears the sequence.
func GrowPushedArrays(input GrowPushedArraysInput) {
	ctx := input.Ctx
	for _, name := range input.Fixpointed {
		if envOrResidue(input.After, name).Kind != abstractdomain.KindUnknown {
			continue
		}
		if len(ctx.Aliases.ClassOf(name)) > 1 {
			continue
		}
		entry := envOrResidue(input.Env, name)
		if entry.Kind != abstractdomain.KindValues || entry.KindTag != abstractdomain.PrimitiveArray {
			continue
		}
		pushed, ok := PushOnlyArguments(input.Loop, name)
		if !ok || len(pushed) == 0 {
			continue
		}
		var element *refinementsets.RefinedSet
		if len(entry.Values) > 0 {
			made := refinementsets.MakeRefinedSet(refinementsets.OneOf(append([]float64{}, entry.Values...)))
			element = &made
		}
		sound := true
		for _, argument := range pushed {
			known := input.EvaluateExpression(ctx, cloneEnv(input.Candidate), argument)
			var set refinementsets.RefinedSet
			setOk := false
			if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber {
				set, setOk = abstractdomain.SetOfKnown(known)
			} else if known.Kind == abstractdomain.KindSet && refinementsets.OnOneTupleLayer(known.Set) {
				set, setOk = known.Set, true
			}
			if !setOk {
				sound = false
				break
			}
			if element == nil {
				element = &set
			} else {
				union := refinementsets.MakeRefinedSet(refinementsets.Union(*element, set))
				element = &union
			}
		}
		if !sound || element == nil {
			continue
		}
		sequence := refinementsets.MakeRefinedSet(refinementsets.Star(*element))
		func() {
			defer func() {
				recover() // a refused question leaves the binding unknown
			}()
			grows := ctx.Kernel.SeqSubset(refinementsets.MakeRefinedSet(refinementsets.Concatenation(sequence, *element)), sequence)
			entryInside := ctx.Kernel.Member(sequence, entry.Values)
			if grows && entryInside {
				// a literal-bounded counted loop whose pushes all sit
				// unconditionally at the body's top level pins the LENGTH
				// too: entry plus one round of pushes per iteration — the
				// repeat form carries the exact count
				count, hasCount := LiteralTripCount(input.Loop)
				perIteration, hasPerIteration := TopLevelPushArguments(input.Loop, name)
				if hasCount && hasPerIteration && perIteration == len(pushed) {
					lo := len(entry.Values) + count*perIteration
					hi := lo
					input.After[name] = abstractdomain.KnownSet(
						refinementsets.MakeRefinedSet(refinementsets.RepeatOf(*element, lo, &hi)),
						nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
					)
				} else {
					input.After[name] = abstractdomain.KnownSet(sequence, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
				}
			}
		}()
	}
}

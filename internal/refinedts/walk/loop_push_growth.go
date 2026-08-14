// from control_flow/loop_push_growth.ts
//
// Grow an after-loop array that the body only ever pushed onto: the
// kernel certifies the sequence, and a literal trip count may pin
// the exact length. A push whose value reads as no set widens the
// element to the one-tuple layer rather than unsaying the growth.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// GrowPushedArraysInput mirrors growPushedArrays' destructured
// parameter.
//
// Fixpointed names the BARE bindings the loop settled; PushPlaces names
// the PROPERTY-PATH arrays its body pushed onto (`this.items`), which no
// name-keyed list can hold. The two are disjoint by construction:
// PushedPlaceCandidates only answers paths of at least one segment.
type GrowPushedArraysInput struct {
	Ctx                *FlowContext
	Env                Env
	Loop               *ast.Node
	Candidate          Env
	After              Env
	Fixpointed         []string
	PushPlaces         []PushedPlaceCandidate
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
	// the loop's write set, read once and only when an alias asks for it
	var loopWrites map[string]struct{}
	for _, name := range input.Fixpointed {
		if envOrResidue(input.After, name).Kind != abstractdomain.KindUnknown {
			continue
		}
		// a SECOND alias only blocks the growth when the loop can write
		// through it: two names for one array make the push-only story a
		// story about one name, and a write through the other name moves
		// the same array. An alias the loop never writes cannot — the
		// aliasing is then just two names reading one unmoved value, and
		// the push-only reading through `name` is the whole story after
		// all. The write set is the loop's own (assignments, ++/--,
		// receivers of non-read-only calls, references handed to callees,
		// and the closed-over writes calls mediate) — the same vocabulary
		// every other write model in this walk unions in.
		if aliases := ctx.Aliases.ClassOf(name); len(aliases) > 1 {
			if loopWrites == nil {
				loopWrites = map[string]struct{}{}
				AssignedNames(ctx.P.Checker, input.Loop, loopWrites)
				CallMediatedWrites(ctx.P.Checker, ctx.Contracts, input.Loop, loopWrites, nil)
			}
			writtenAlias := false
			for member := range aliases {
				if member == name {
					continue
				}
				if _, written := loopWrites[member]; written {
					writtenAlias = true
					break
				}
			}
			if writtenAlias {
				continue
			}
		}
		entry := envOrResidue(input.Env, name)
		growOnePushedArray(input, dataflowfacts.TrackedPlace{Binding: name}, entry, name)
	}
	growPushedPlaces(input)
}

// growPushedPlaces runs the same growth over the PROPERTY-PATH arrays
// the body pushed onto — `this.items.push(x)`, `state.rows.push(y)`.
//
// THE ALIASING ARGUMENT for a property path. A bare name's push-only
// story is a story about one binding, so the name path asks the alias
// classes whether a SECOND name could move the same array. A path has no
// alias class to ask: `this.items` is reached by walking `this`, and
// anything that can reach `this` can reach the array through it. So the
// gate here is the path's own mentions, and it is deliberately the
// conservative one — PushOnlyArgumentsAt already resolves EVERY mention
// of the root inside the loop to the place it names, and disqualifies on
// any mention that is not this place's own push:
//
//   - a mention naming a PREFIX (`this` alone, or `this.items` handed to
//     a call, or assigned from) reaches the array through a shorter path
//     and can rebind it or hand it out, so it disqualifies;
//   - a mention naming an EXTENSION (`this.items.length`, `this.items[0]
//     = y`) touches the array itself, so it disqualifies;
//   - a mention the place reading cannot resolve (a computed index off
//     the root) may be this array, so it disqualifies;
//   - a DISJOINT sibling (`this.other`) leaves this array alone and is
//     skipped.
//
// That is placeCovers' rule, and it is strictly stronger than "no other
// name writes it": a path that survives it is never mentioned inside the
// loop except as the receiver of its own pushes, so nothing in the loop
// holds a second handle to hand out. The one hole a name's alias class
// would also leave — a handle taken OUTSIDE the loop and written by a
// call the loop makes — is closed by the same disqualification, since
// making that call requires mentioning the root.
func growPushedPlaces(input GrowPushedArraysInput) {
	ctx := input.Ctx
	for _, candidate := range input.PushPlaces {
		// the entry value is read by EVALUATING the access the push named,
		// at the loop's entry state — the property path's twin of the name
		// path's env read
		entry := input.EvaluateExpression(ctx, input.Env.Clone(), candidate.Access)
		pathKey := candidate.Place.Binding
		for _, segment := range candidate.Place.Path {
			pathKey += "." + segment
		}
		if envOrResidue(input.After, pathKey).Kind != abstractdomain.KindUnknown {
			continue
		}
		growOnePushedArray(input, candidate.Place, entry, pathKey)
	}
}

// growOnePushedArray is the growth itself, which is place-agnostic once
// the entry value is in hand: the pushed arguments come from the place,
// the sequence claim is certified by the kernel, and the answer lands in
// the after-loop environment under `key` — a bare name for a binding,
// the dotted place-value spelling for a property path.
func growOnePushedArray(input GrowPushedArraysInput, place dataflowfacts.TrackedPlace, entry abstractdomain.AbstractValue, key string) {
	ctx := input.Ctx
	if entry.Kind != abstractdomain.KindValues || entry.KindTag != abstractdomain.PrimitiveArray {
		return
	}
	pushed, ok := PushOnlyArgumentsAt(input.Loop, place)
	if !ok || len(pushed) == 0 {
		return
	}
	var element *refinementsets.RefinedSet
	if len(entry.Values) > 0 {
		made := refinementsets.MakeRefinedSet(refinementsets.OneOf(append([]float64{}, entry.Values...)))
		element = &made
	}
	for _, argument := range pushed {
		known := input.EvaluateExpression(ctx, input.Candidate.Clone(), argument)
		var set refinementsets.RefinedSet
		setOk := false
		if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber {
			set, setOk = abstractdomain.SetOfKnown(known)
		} else if known.Kind == abstractdomain.KindSet && refinementsets.OnOneTupleLayer(known.Set) {
			set, setOk = known.Set, true
		}
		// One push whose value reads as no set widens the element to
		// ANY ONE-TUPLE VALUE — an unknown scalar — rather than
		// unsaying the whole growth. The sequence claim IS a claim
		// about the element set (S-star says every member is an S), so
		// the widening has to name a set that holds every scalar and
		// nothing off the 1-tuple layer: that is `Numbers`, the ray
		// from −∞, which the layer test admits and the bare root does
		// not (the root holds every TUPLE, so Star of it would claim a
		// sequence of arbitrary tuples). The LENGTH half rides the same
		// element, since RepeatOf takes it too.
		//
		// So a mixed push says what it knows: the length grows by the
		// push count, and the elements are unknown scalars.
		if !setOk {
			set = refinementsets.Numbers
		}
		if element == nil {
			element = &set
		} else {
			union := refinementsets.MakeRefinedSet(refinementsets.Union(*element, set))
			element = &union
		}
	}
	if element == nil {
		return
	}
	sequence := refinementsets.MakeRefinedSet(refinementsets.Star(*element))
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
		perIteration, hasPerIteration := TopLevelPushArgumentsAt(input.Loop, place)
		if hasCount && hasPerIteration && perIteration == len(pushed) {
			lo := len(entry.Values) + count*perIteration
			hi := lo
			input.After.Set(key, abstractdomain.KnownSet(
				refinementsets.MakeRefinedSet(refinementsets.RepeatOf(*element, lo, &hi)),
				nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
			))
		} else {
			input.After.Set(key, abstractdomain.KnownSet(sequence, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
		}
	}
}

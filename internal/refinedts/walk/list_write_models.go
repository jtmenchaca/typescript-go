// The writing array methods over an exact LIST — a receiver whose
// positions the walk holds one by one, but whose items are not all
// exact scalars: `const arr = [a, b, c]` where a, b, c are parameters
// a guard narrowed to a window builds a KindList of three set-shaped
// items, never the flat KnownValues tuple readArrayWriteMethods's own
// path handles.
//
// Every method here moves ITEMS, so it needs no arithmetic on their
// values at all — push appends, pop removes the last and answers it,
// shift removes the first and answers it, unshift prepends, splice
// cuts a run out and answers it. The flat-tuple reader can only do
// this when every element is one exact number; over a list the same
// moves are exact whatever each item holds, which is why the list gets
// a reader of its own rather than a widening of that one.
//
// A KindList is HOLE-FREE by construction (element_access.go's own
// note states the rule and why), so removing from the front or back is
// exactly the item at that end, and an empty list answers undefined
// with nothing removed.
//
// Clauses, from specifications/javascript/spec.html:
//   - sec-array.prototype.push — appends each argument in order and
//     returns the new length.
//   - sec-array.prototype.pop — "If len = 0, return *undefined*",
//     otherwise reads index len−1, deletes it, sets length to len−1,
//     and returns the value read.
//   - sec-array.prototype.shift — "If len = 0, return *undefined*",
//     otherwise reads index 0, moves every later element down one,
//     deletes the last, and returns the value read.
//   - sec-array.prototype.unshift — moves every element up by the
//     argument count, writes the arguments at the front, returns the
//     new length.
//   - sec-array.prototype.splice — removes deleteCount items from
//     start, inserts the given ones there, and returns the removed run.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readListWriteMethods is readArrayWriteMethods's list arm: the same
// five writers, item-wise. The receiver is known KindList and the name
// known tracked — its caller checked both.
func readListWriteMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver, method := site.Ctx, site.Env, site.E, site.Receiver, site.Method
	trackedName := site.TrackedName
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	argKnowns := make([]abstractdomain.AbstractValue, len(arguments))
	for i, argument := range arguments {
		if ast.IsSpreadElement(argument) {
			// which items a spread contributes is a separate reading;
			// the class forgets rather than guess the count
			HavocEnv(ctx.Aliases, env, trackedName)
			out := silence.Residue()
			return &out
		}
		argKnowns[i] = evaluateExpression(ctx, env, argument)
	}
	held := append([]abstractdomain.AbstractValue{}, receiver.Items...)
	grade := abstractdomain.TrustLevelOf(receiver)
	for _, k := range argKnowns {
		grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(k))
	}
	// the new length, as the writers that answer one spell it
	lengthOf := func(n int) abstractdomain.AbstractValue {
		return abstractdomain.KnownValues([]float64{float64(n)}, abstractdomain.PrimitiveNumber, grade)
	}

	switch method {
	case "push":
		next := append(append([]abstractdomain.AbstractValue{}, held...), argKnowns...)
		UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownList(next, grade))
		out := lengthOf(len(next))
		return &out

	case "unshift":
		next := append(append([]abstractdomain.AbstractValue{}, argKnowns...), held...)
		UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownList(next, grade))
		out := lengthOf(len(next))
		return &out

	case "pop":
		if len(argKnowns) != 0 {
			return nil
		}
		if len(held) == 0 {
			// sec-array.prototype.pop step "If len = 0": the length is
			// set to 0 (already is) and the answer is exactly undefined
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownList(nil, grade))
			out := abstractdomain.Undef
			return &out
		}
		removed := held[len(held)-1]
		UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownList(held[:len(held)-1], grade))
		return &removed

	case "shift":
		if len(argKnowns) != 0 {
			return nil
		}
		if len(held) == 0 {
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownList(nil, grade))
			out := abstractdomain.Undef
			return &out
		}
		removed := held[0]
		UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownList(held[1:], grade))
		return &removed

	case "fill":
		// one value, no window — the same form readFreshArrayFill reads
		// for an untracked receiver; a partial window needs its bounds
		// read exactly before a per-slot result composes
		if len(argKnowns) != 1 {
			return nil
		}
		next := make([]abstractdomain.AbstractValue, len(held))
		for i := range next {
			next[i] = argKnowns[0]
		}
		filled := abstractdomain.KnownList(next, grade)
		UpdateTrackedEnv(ctx.Aliases, env, trackedName, filled)
		return &filled

	case "splice":
		if len(argKnowns) < 1 {
			return nil
		}
		// only the bounds need to be exact integers; the INSERTED items
		// are moved whole, whatever they hold
		start, startOk := exactIntegerHeld(argKnowns[0])
		if !startOk {
			HavocEnv(ctx.Aliases, env, trackedName)
			out := silence.Residue()
			return &out
		}
		at := int(start)
		if at < 0 {
			at = len(held) + at
			if at < 0 {
				at = 0
			}
		}
		if at > len(held) {
			at = len(held)
		}
		deleteCount := len(held) - at
		if len(argKnowns) >= 2 {
			count, countOk := exactIntegerHeld(argKnowns[1])
			if !countOk {
				HavocEnv(ctx.Aliases, env, trackedName)
				out := silence.Residue()
				return &out
			}
			deleteCount = int(count)
			if deleteCount < 0 {
				deleteCount = 0
			}
			if at+deleteCount > len(held) {
				deleteCount = len(held) - at
			}
		}
		removed := append([]abstractdomain.AbstractValue{}, held[at:at+deleteCount]...)
		var inserted []abstractdomain.AbstractValue
		if len(argKnowns) > 2 {
			inserted = argKnowns[2:]
		}
		next := make([]abstractdomain.AbstractValue, 0, len(held)-deleteCount+len(inserted))
		next = append(next, held[:at]...)
		next = append(next, inserted...)
		next = append(next, held[at+deleteCount:]...)
		UpdateTrackedEnv(ctx.Aliases, env, trackedName, abstractdomain.KnownList(next, grade))
		out := abstractdomain.KnownList(removed, grade)
		return &out
	}
	return nil
}

// exactIntegerHeld reads one exact integer off a VALUE, or (0, false).
// Named apart from effect_exact_string_methods.go's own exactIntegerOf,
// which reads an integer off an AST NODE — a different question with a
// different signature.
func exactIntegerHeld(k abstractdomain.AbstractValue) (float64, bool) {
	if k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveNumber &&
		len(k.Values) == 1 && isInteger(k.Values[0]) {
		return k.Values[0], true
	}
	return 0, false
}

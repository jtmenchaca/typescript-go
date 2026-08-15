// Arrow callbacks, closure-converted: a collection method whose
// callback is an arrow argument lowers as ONE call statement applying
// the arrow's own compiled summary.
//
// The census that motivated this: collection callbacks capture `this`
// and outer locals READ-ONLY. Both convert without any new kernel form,
// because a capture is nothing but an extra ENTRY of the arrow's
// summary. An arrow with parameters p₀…pₙ and captures c₀…cₘ compiles
// to a summary of arity n+m+1+locals, whose entries are the parameters
// first and the captures immediately after; the call site fills the
// parameter entries from the receiver's element slot and the capture
// entries with a `var` of the caller slot each capture name resolves
// to. Nothing kernel-side learns that some entries came from a closure.
//
// What CONVERTS — an arrow (or function expression) argument whose free
// names are only:
//
//   - its own parameters;
//   - `this`-rooted method calls (`this.m(…)`) that resolve through the
//     lowering context's ResolveCallee exactly as any other call does —
//     the receiver is not a value the arrow reads, only a name the
//     resolution consumes;
//   - READ-ONLY captures of caller locals that HAVE a slot.
//
// What DECLINES:
//
//   - a captured WRITE (`total += x`, `seen[k] = 1`, `i++` on an outer
//     name) — the write would move the caller's state and an entry
//     carries nothing back;
//   - a capture with no slot in the enclosing lowering — there is no
//     `var` to bind the entry to;
//   - a free name that is neither parameter, capture-with-slot, nor
//     `this` — a module import, a global, an outer function;
//   - a nested function inside the arrow, and a `this` used as a VALUE
//     rather than as a call receiver;
//   - a body the ordinary summary lowering declines.
//
// ASYNC arrows convert exactly like sync ones. A lowered async body's
// #ret holds the SETTLED inner value (the ret-as-inner convention), so
// `xs.map(async cb)` and `xs.map(cb)` lower identically and the await
// over the result adds nothing.
//
// A callback does NOT have to be spelled inline. A callback argument
// that NAMES a function — `xs.forEach(handler)`, `xs.map(this.render)`
// — converts on exactly the arrow's terms, with the named declaration's
// own parameters and body standing where the arrow's would (see
// callbackFunctionOf). What the reference costs is the receiver: a
// method handed over BARE loses its binding, so at the traversal's call
// the runtime `this` is undefined rather than the object the method was
// read off. The rule that keeps that honest is the narrowest sound one
// — a referenced body that mentions `this` anywhere DECLINES, whatever
// the spelling it was reached through, and a body free of `this` cannot
// tell the difference between being called bare and being called on its
// owner, so it converts.
//
// The recognized statement forms — each total-or-decline. The array
// forms stand over a FLATTENED array receiver whose "xs.len"/"xs.elem"
// slots resolve:
//
//	ys = xs.map(cb)     → ys.len := var xs.len; call cb at xs.elem → ys.elem
//	xs.forEach(cb)      → call cb at xs.elem, no ret
//	ys = xs.filter(cb)  → ys.elem := var xs.elem; ys.len := integer ≥ 0
//	ys = xs.find(cb)    → call cb at xs.elem with no ret (the predicate's
//	                      own answer is not what find returns), then
//	                      ys := xs.elem OR-ABSENT — find hands back an
//	                      element the array held, or undefined where no
//	                      element passed, and the or-absent effect is
//	                      exactly that pair
//	ys = xs.reduce(cb, seed)  → call cb with the ACCUMULATOR entry first
//	                      and the element second (reduce shifts every
//	                      slot one over, which is the same shift
//	                      ArrayCallbackPins spells), ret → ys. The seed
//	                      fills the accumulator entry; the FOLD is not
//	                      unrolled — the entry is the JOIN of the seed
//	                      and cb's own ret, which covers the accumulator
//	                      at every pass, the same join-of-elements
//	                      argument the element slot already rides
//	ys = xs.flatMap(cb) → call cb at xs.elem for its EFFECTS, ys := unknown
//	                      — flatMap's result is the CONCATENATION of cb's
//	                      per-element arrays, and the two-slot flattening
//	                      has no spelling for an array of arrays flattened
//	                      one level. The callback still converts, which is
//	                      the point: an effectful or unconvertible cb
//	                      declines the statement instead of passing
//	                      unread, and the result honestly answers nothing
//	ys = await Promise.all(xs.map(cb))  → the map lowering above
//	xs.find(cb); / xs.map(cb); / xs.reduce(cb, seed);  → the RESULT
//	                      DISCARDED: one call statement at the element
//	                      entry with no ret, exactly what forEach emits.
//	                      The traversal runs, so the callback runs, and a
//	                      callback that does not convert declines the
//	                      statement rather than passing unread. reduce
//	                      keeps its shifted layout with the accumulator
//	                      entry absent — no target slot means nothing for
//	                      that entry to join against
//
// over a FLATTENED Map or Set whose "m.size"/"m.vals"(/"m.keys") family
// resolves:
//
//	m.forEach(cb)       → call cb at m.vals (and m.keys second, for a
//	                      Map; the value again for a Set), no ret
//
// and over a PROMISE-HELD local whose "p.inner" slot the await lowering
// allocated:
//
//	p.then(cb)          → call cb at p.inner, no ret
//	q = p.then(cb)      → the same, ret → a fresh "q.inner" slot, which
//	                      makes q a promise-held local a later `await q`
//	                      reads back
//
// The join-of-elements argument is what makes one call statement cover
// a whole traversal: the element (or values) slot holds the JOIN of
// everything the collection can hold, cb's summary quantifies over ALL
// entries, so applying it at the join covers cb's image of every
// individual element. One application, every iteration.
//
// An array callback's SECOND parameter is the INDEX, and it enters the
// integer-≥0 constant set rather than absent: every concrete index is a
// non-negative integer, so the entry is honest, and it is what lets
// `(x, i) => x + i` convert instead of declining on the `i` read. The
// THIRD parameter — the collection itself — stays absent, since no slot
// holds a whole array or a whole Map.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// SummaryCallbackStatementOf is the lowering-side entry: a statement
// whose right side is a recognized call over a flattened array, a
// flattened Map or Set, or a promise-held local, with a closure-
// converting callback — lowered to the call statement and whatever slot
// writes the shape carries beside it.
//
// Total-or-decline. Anything unrecognized — a receiver that is not
// flattened, a callback that captures a write, a method not listed, a
// result with nowhere to land — answers false, and the statement then
// takes whatever route it had.
func SummaryCallbackStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || statement == nil {
		return nil, false
	}
	// the statement forms whose value goes NOWHERE
	if ast.IsExpressionStatement(statement) {
		expression := Unwrapped(statement.AsExpressionStatement().Expression)
		if source, ok := collectionCallOf(expression); ok {
			switch source.Method {
			// `xs.forEach(cb);` / `m.forEach(cb);` — the traversal runs. The
			// receiver decides which flattening answers: an array's two slots
			// or a collection's size/vals/keys family. A receiver that is
			// neither declines both.
			case "forEach":
				if lowered, arrayOk := forEachStatement(context, source); arrayOk {
					return lowered, true
				}
				return collectionForEachStatement(context, source)
			// `p.then(cb);` — the callback runs on the settled value and its
			// own answer goes nowhere
			case "then":
				return thenStatements(context, source, "")
			// `xs.find(cb);` / `xs.map(cb);` / `.filter` / `.flatMap` with the
			// RESULT DISCARDED — the traversal still runs, so the callback
			// runs, and what it answers goes nowhere
			case "map", "filter", "find", "flatMap":
				return discardedResultStatement(context, source)
			}
		}
		// `xs.reduce(cb, seed);` — the two-argument shape, its result
		// discarded. The fold still runs the callback once per element.
		if reduceSource, _, ok := reduceCallOf(expression); ok {
			return discardedResultStatement(context, reduceSource)
		}
	}
	// `ys = e` / `const ys = e` — the shape both call routes read. The
	// target here is an ARRAY, whose two slots the name resolves to, so
	// the scalar target callAssignmentShapeOf answers with is not what
	// this uses; the NAME is.
	target, rhs, shapeOk := callbackAssignmentNameOf(context, statement)
	if !shapeOk {
		return nil, false
	}
	// `ys = await Promise.all(xs.map(cb))` / the unawaited spelling —
	// the map lowering, since the await is the identity on the result
	if inner, ok := promiseAllMapOf(rhs); ok {
		return mapStatements(context, inner, target)
	}
	// `ys = xs.reduce(cb, seed)` — the two-argument shape collectionCallOf
	// refuses, read by its own reader ahead of the one-argument switch
	if reduceSource, seed, ok := reduceCallOf(rhs); ok {
		return reduceStatements(context, reduceSource, seed, target)
	}
	source, sourceOk := collectionCallOf(rhs)
	if !sourceOk {
		return nil, false
	}
	// every method callback_pins.go's ArrayCallbackMethods names has a
	// case here, so the two routes agree about which array methods carry
	// a modeled callback — `reduce` above, and the rest below
	switch source.Method {
	case "map":
		return mapStatements(context, source, target)
	case "filter":
		return filterStatements(context, source, target)
	case "find":
		return findStatements(context, source, target)
	case "flatMap":
		return flatMapStatements(context, source, target)
	case "then":
		return thenStatements(context, source, target)
	}
	return nil, false
}

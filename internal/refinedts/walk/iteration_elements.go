// from evaluation/iteration_elements.ts
//
// What one element of an iterable is, where the iterable expression
// itself says so. Split from builtin_models.ts per the v2 tree.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// resolvesToDefaultLib mirrors service/program_resolution.ts's
// resolvesToDefaultLib once p.host is the checker itself (PORT.md):
// whether the node's symbol declares in the checker's default
// library.
func resolvesToDefaultLib(ctx *FlowContext, node *ast.Node) bool {
	return ctx.P.Checker.SymbolInDefaultLib(ctx.P.Checker.GetSymbolAtLocation(node))
}

// heldArgumentValue reads what the walk already HOLDS for an
// expression, without evaluating it: a bound name, or a property
// chain off one (`this.config`, `state.rows.byKey`). Nothing else
// answers — a call, an index, an operator each run code, and this
// reading exists precisely so that code does not run a second time.
// Nil where the walk holds nothing for the shape.
func heldArgumentValue(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if ast.IsIdentifier(e) {
		if held, ok := env.Get(e.Text()); ok {
			return &held
		}
		return nil
	}
	if ast.IsPropertyAccessExpression(e) {
		access := e.AsPropertyAccessExpression()
		receiver := heldArgumentValue(ctx, env, access.Expression)
		if receiver == nil {
			return nil
		}
		// an opaque receiver's properties arrive opaque with it
		if receiver.Kind == abstractdomain.KindUnknown && receiver.Opaque {
			out := abstractdomain.Opaque
			return &out
		}
		if receiver.Kind != abstractdomain.KindObject {
			return nil
		}
		if held, ok := objectKeyValue(*receiver, access.Name().Text()); ok {
			return &held
		}
		return nil
	}
	// `this` reads the receiver the walk bound for this body
	if e.Kind == ast.KindThisKeyword {
		if held, ok := env.Get("this"); ok {
			return &held
		}
	}
	return nil
}

// collectionIterationElement: what one element of iterating a TRACKED
// Map or Set is. `for (const v of s)` and `of m.values()` hand a
// value; `of m.keys()` a key; a bare Map and `of m.entries()` hand a
// [key, value] pair. Read off the collection the walk already holds,
// so nothing is evaluated twice — the same discipline the entries
// reading below wears. Nil where the receiver is not a held
// collection, or the view is one this reading does not name.
func collectionIterationElement(ctx *FlowContext, env Env, iterable *ast.Node) *abstractdomain.AbstractValue {
	receiver := iterable
	view := ""
	if ast.IsCallExpression(iterable) {
		call := iterable.AsCallExpression()
		if len(call.Arguments.Nodes) != 0 || !ast.IsPropertyAccessExpression(call.Expression) {
			return nil
		}
		access := call.Expression.AsPropertyAccessExpression()
		view = access.Name().Text()
		if view != "values" && view != "keys" && view != "entries" {
			return nil
		}
		receiver = access.Expression
	}
	held := heldArgumentValue(ctx, env, receiver)
	if held == nil || held.Kind != abstractdomain.KindCollection {
		return nil
	}
	// an INCOMPLETE record names some entries but not all of them, so
	// no join over it covers every element the loop yields
	if !held.Complete {
		return nil
	}
	// a Set has no keys of its own — every entry's key IS its member,
	// and `keys()` on a Set yields those same members
	isMap := held.CollectionFlavor == abstractdomain.FlavorMap
	if view == "" && isMap {
		view = "entries"
	}
	if view == "keys" && !isMap {
		view = "values"
	}
	grade := abstractdomain.TrustLevelOf(*held)
	// an empty collection yields nothing at all, so no element reading
	// describes a trip that never happens
	if len(held.Entries) == 0 {
		return nil
	}
	joinOver := func(pick func(abstractdomain.CollectionEntry) abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		joined := pick(held.Entries[0])
		for _, entry := range held.Entries[1:] {
			joined = abstractdomain.JoinKnown(joined, pick(entry))
		}
		return abstractdomain.AtTrustLevel(joined, grade)
	}
	switch view {
	case "values":
		if !isMap {
			// a Set's members are its entry KEYS
			out := joinOver(func(e abstractdomain.CollectionEntry) abstractdomain.AbstractValue { return e.Key })
			return &out
		}
		out := joinOver(func(e abstractdomain.CollectionEntry) abstractdomain.AbstractValue { return e.Value })
		return &out
	case "keys":
		out := joinOver(func(e abstractdomain.CollectionEntry) abstractdomain.AbstractValue { return e.Key })
		return &out
	case "entries":
		if !isMap {
			return nil
		}
		keys := joinOver(func(e abstractdomain.CollectionEntry) abstractdomain.AbstractValue { return e.Key })
		values := joinOver(func(e abstractdomain.CollectionEntry) abstractdomain.AbstractValue { return e.Value })
		out := abstractdomain.KnownList([]abstractdomain.AbstractValue{keys, values}, grade)
		return &out
	}
	// a bare SET iterates its members
	out := joinOver(func(e abstractdomain.CollectionEntry) abstractdomain.AbstractValue { return e.Key })
	return &out
}

// IterationElementOf: what one element of a for-of is, where the
// ITERABLE expression says so even though the walk holds no
// sequence for it: a web collection's iterator (web.ts), a tracked
// Map or Set and its views, or an Object.entries call — whose pair
// KEYS are Strings (sec-object.entries) whatever the argument. Nil
// where none speaks; the loop's own reading stands. A generator
// call and a composed iterator chain say nothing here — the walk
// holds no sequence for either.
func IterationElementOf(ctx *FlowContext, env Env, iterable *ast.Node) *abstractdomain.AbstractValue {
	web := WebIterationElement(ctx.P, iterable)
	if web != nil {
		return web
	}
	if collection := collectionIterationElement(ctx, env, iterable); collection != nil {
		return collection
	}
	if ast.IsCallExpression(iterable) {
		call := iterable.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) {
			access := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(access.Expression) &&
				access.Expression.Text() == "Object" &&
				access.Name().Text() == "entries" &&
				resolvesToDefaultLib(ctx, access.Expression) &&
				len(call.Arguments.Nodes) == 1 {
				// the argument's value is read only where reading it
				// cannot replay an effect — a name, or a property chain
				// off a tracked name. Anything richer was already
				// evaluated once by the loop's iterable walk, and
				// evaluating it again would run its code twice.
				argument := call.Arguments.Nodes[0]
				held := heldArgumentValue(ctx, env, argument)
				value := silence.Residue()
				if held != nil && held.Kind == abstractdomain.KindUnknown && held.Opaque {
					value = abstractdomain.Opaque
				}
				out := abstractdomain.KnownList([]abstractdomain.AbstractValue{
					abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone),
					value,
				}, abstractdomain.TrustSpec)
				return &out
			}
		}
	}
	return nil
}

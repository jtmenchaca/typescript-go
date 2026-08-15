// The iterator protocol read as an expression: `it.next()` and what
// its result record holds.
//
// This is the half of collection iteration that does NOT go through a
// for-of head. iteration_elements.go answers what one element of a
// TRACKED collection is, off the entries the walk already holds; the
// rows here answer a `.next()` call on an iterator the walk holds no
// entries for — `container.getModules().values()` bound to a name, and
// then `modules.next().value`. Recognition is by the receiver's STATIC
// type name (MapIterator / SetIterator / ArrayIterator /
// StringIterator, lib.es2015.iterable.d.ts:135, 190, 70, 264), the
// same standing the web-platform and Date fallback rows rest on: those
// are library-declared classes with no constructor a user can call, so
// a value of that type came from a collection's own view method.
//
// The result record's SHAPE is the specification's
// (sec-createiterresultobject: an ordinary object with exactly a value
// property and a done property, done a Boolean), so the record and its
// done field wear the spec grade. The value field's own knowledge is
// read off the iterator's element type, which is the declaration's
// claim, and it wears whatever that reading gives it.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// builtinIteratorClasses: the iterator classes the default library
// declares for its own collections. Each carries its element type as
// its one type argument.
// A generator's own lib types belong here for the same reason the
// collection views do: `Generator<T, …>` and `IterableIterator<T>` are
// library-declared with no constructor a user can call, so a value of
// that type came from a generator call, and T is what the body yields —
// tsc checked every `yield e` against it. The names and the
// first-argument rule live with the generator's other readings
// (generator_element.go).
var builtinIteratorClasses = map[string]bool{
	"MapIterator": true, "SetIterator": true, "ArrayIterator": true,
	"StringIterator": true, "RegExpStringIterator": true,
}

// builtinIteratorClassNamed answers whether a symbol name is one of the
// iterator classes this file reads — the collection views above, or one
// of the generator/iterable spellings.
func builtinIteratorClassNamed(name string) bool {
	return builtinIteratorClasses[name] || generatorReturnTypeNames[name]
}

// builtinIteratorElementOf: the element type of a receiver whose
// static type is one of the library's iterator classes — the T of
// MapIterator<T> and its kin — or (nil, false) when the receiver is
// not such an iterator. A user's own class named MapIterator answers
// nothing: the symbol has to be the default library's.
func builtinIteratorElementOf(ctx *FlowContext, receiver *ast.Node) (*checker.Type, bool) {
	t := ctx.P.Checker.GetTypeAtLocation(receiver)
	if t == nil {
		return nil, false
	}
	symbol := t.Symbol()
	if symbol == nil || !builtinIteratorClassNamed(symbol.Name) {
		return nil, false
	}
	if !ctx.P.Checker.SymbolInDefaultLib(symbol) {
		return nil, false
	}
	// the element rides as the reference's first type argument; a
	// reference with none states no element, and the row below answers
	// the record with an unread value rather than claiming one
	if (t.ObjectFlags() & checker.ObjectFlagsReference) == 0 {
		return nil, false
	}
	arguments := ctx.P.Checker.GetTypeArguments(t)
	if len(arguments) == 0 {
		return nil, false
	}
	return arguments[0], true
}

// builtinIteratorSequenceOf: the array a builtin iterator's elements
// build when something drains it whole — `Array.from(it)`, `[...it]`.
// The count is not stated (the collection behind the view holds
// however many entries it holds), so the answer is the star of what
// one element admits: these elements, length unknown. The element
// comes from the iterator's own type argument read the way
// `.next().value` reads it, so both routes answer the same thing about
// the same element.
//
// An element the reader cannot spell as a SET — a class instance, a
// record, anything living in the object graph rather than the tuple
// layer — stars into the object-star: the same per-position claim,
// carried where the graph answers it rather than the kernel. That is
// what makes `Array.from(this._providers.values())` hold InstanceWrapper
// elements. Only an element NEITHER layer holds answers (zero, false),
// and the caller keeps the answer it already had.
func builtinIteratorSequenceOf(ctx *FlowContext, receiver *ast.Node) (abstractdomain.AbstractValue, bool) {
	// a GENERATOR call is an iterator too, and draining it is the same
	// question: every value the body yields lands in the array, at a
	// count the body's own control flow decides. Its element is read
	// from the yields (or the declared yield type), which the
	// generator's own file answers, and the star is built there over the
	// very same recipe — so `[...g()]` and `Array.from(g())` reach it
	// through this one door, exactly as `[...m.values()]` does.
	if sequence, ok := GeneratorSequenceOf(ctx, receiver); ok {
		return sequence, true
	}
	element, ok := builtinIteratorElementOf(ctx, receiver)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	read, hasRead := typereading.ReadHostType(ctx.P.Checker, element, receiver, 0)
	if !hasRead {
		return abstractdomain.AbstractValue{}, false
	}
	return typereading.StarOfElement(read)
}

// iteratorResultRecord: the record `next()` hands back — exactly a
// value property and a done property (sec-createiterresultobject).
// done is the boolean pair: a call that cannot be ordered against the
// iterator's exhaustion may return either. The record is COMPLETE:
// the specification builds it with those two properties and no other,
// so a read of any third name is exactly undefined.
func iteratorResultRecord(value abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	done := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
		nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone,
	)
	return abstractdomain.KnownObject([]abstractdomain.ObjectKey{
		{Name: "value", Value: value},
		{Name: "done", Value: done},
	}, nil, true, abstractdomain.TrustSpec, false)
}

// readIteratorNext: `it.next()` on one of the library's own collection
// iterators. The answer is the result record: done a boolean, and
// value whatever the iterator's element type states — for a
// `MapIterator<Module>` that is a Module, for a `MapIterator<[K, V]>`
// the pair. An iterator whose LAST element the call may have passed
// hands back the return value instead of an element (done true), so
// the value field carries the element's reading beside absence: the
// record's own `done` is what tells the two apart, and a caller that
// checks it narrows to the element. Nil when the receiver is not a
// library iterator or the method is not next.
func readIteratorNext(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, e, receiverExpression, method := site.Ctx, site.E, site.ReceiverExpression, site.Method
	if method != "next" {
		return nil
	}
	call := e.AsCallExpression()
	if call.Arguments != nil && len(call.Arguments.Nodes) != 0 {
		return nil
	}
	// what the element type states. An unread type leaves the value
	// unclaimed rather than opaque — the iterator's own contents came
	// from a collection this file may yet determine.
	value := silence.Residue()
	// `g().next()` reads the generator's own yields first: the body is
	// in reach, so what it hands the caller is read from the yield
	// expressions themselves rather than from the type argument alone.
	// The declared route below still answers for a generator bound to a
	// name, and for every collection view.
	if yielded, ok := GeneratorElementOf(ctx, receiverExpression); ok {
		value = yielded
	} else {
		element, ok := builtinIteratorElementOf(ctx, receiverExpression)
		if !ok {
			return nil
		}
		if read, hasRead := typereading.ReadHostType(ctx.P.Checker, element, receiverExpression, 0); hasRead {
			value = read
		}
	}
	// A next() the walk cannot order against the iterator's exhaustion
	// may be the one PAST the last element, and that call's record holds
	// the iterator's RETURN value with done true — undefined for every
	// collection view, and for a generator whatever its `return e` gave
	// (undefined where it has none). The element reading does not cover
	// that value, so the field carries the element BESIDE absence: the
	// maybe wrapper is what makes the record true of both the element
	// calls and the finishing one, and the record's own `done` is what
	// tells a caller which it holds — a caller that checks it narrows
	// back to the element.
	out := iteratorResultRecord(abstractdomain.PossiblyUndefined(value, abstractdomain.TrustSpec, true, false))
	return &out
}

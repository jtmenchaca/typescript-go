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
var builtinIteratorClasses = map[string]bool{
	"MapIterator": true, "SetIterator": true, "ArrayIterator": true,
	"StringIterator": true, "RegExpStringIterator": true,
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
	if symbol == nil || !builtinIteratorClasses[symbol.Name] {
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
// layer — leaves nothing to star: a sequence claim is a claim about
// every position, and there is no set to put at one. Those answer
// (zero, false) and the caller keeps the answer it already had.
func builtinIteratorSequenceOf(ctx *FlowContext, receiver *ast.Node) (abstractdomain.AbstractValue, bool) {
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
	element, ok := builtinIteratorElementOf(ctx, receiverExpression)
	if !ok {
		return nil
	}
	// what the element type states. An unread type leaves the value
	// unclaimed rather than opaque — the iterator's own contents came
	// from a collection this file may yet determine.
	value := silence.Residue()
	if read, hasRead := typereading.ReadHostType(ctx.P.Checker, element, receiverExpression, 0); hasRead {
		value = read
	}
	// a next() past the last element yields the iterator's RETURN value
	// (undefined for every one of these classes) with done true, so the
	// value the caller reads is the element or nothing at all
	out := iteratorResultRecord(abstractdomain.PossiblyUndefined(value, abstractdomain.TrustSpec, true, false))
	return &out
}

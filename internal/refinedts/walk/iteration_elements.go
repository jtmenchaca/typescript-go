// from evaluation/iteration_elements.ts
//
// What one element of an iterable is, where the iterable expression
// itself says so. Split from builtin_models.ts per the v2 tree.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// resolvesToDefaultLib mirrors service/program_resolution.ts's
// resolvesToDefaultLib once p.host is the checker itself (PORT.md):
// whether the node's symbol declares in the checker's default
// library.
func resolvesToDefaultLib(ctx *FlowContext, node *ast.Node) bool {
	tracing.CountBy("host.symbolAtLocation", 1)
	tracing.CountBy("host.symbolInDefaultLib", 1)
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
			// s.matchAll(/pattern/g): each yielded match's group 0
			// (index 0 of the RegExpExecArray) is IN the pattern's own
			// language — sec-string.prototype.matchall step 5 builds
			// the iterator over sec-regexpexec, whose group-0 slice is
			// always what the pattern matched, whatever the receiver.
			// This reads the pattern's own compiled grammar (the same
			// door captureGroupSetOf/.match's const-regex reading use),
			// never the receiver's content — so it holds for an
			// UNTRACKED receiver too, unlike an exact-tuple execution.
			if access.QuestionDotToken == nil && access.Name().Text() == "matchAll" &&
				len(call.Arguments.Nodes) == 1 {
				if pattern := regexLiteralReceiverOf(ctx, call.Arguments.Nodes[0]); pattern != nil {
					literal := pattern.Text()
					lastSlash := strings.LastIndex(literal, "/")
					source := literal[1:lastSlash]
					flags := literal[lastSlash+1:]
					// group 0 is exactly what the pattern matched, not a
					// substring-anywhere reading — anchor both sides the
					// same way captureGroupSetOf anchors a capturing
					// group's own sub-pattern, so an unanchored side is
					// not padded with C* (FormatGrammar's own default for
					// a bare, unanchored pattern).
					compiled := refinementsets.FormatGrammar("^(?:"+source+")$", flags)
					if compiled.Ok {
						group0 := abstractdomain.KnownSet(compiled.Set, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
						out := abstractdomain.KnownList([]abstractdomain.AbstractValue{group0}, abstractdomain.TrustSpec)
						return &out
					}
				}
			}
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
	if declared := declaredCollectionElement(ctx, iterable); declared != nil {
		return declared
	}
	return nil
}

// declaredCollectionElement is what one element of iterating a Map or
// Set is when the walk built NO entries for it — a parameter, a field,
// any receiver whose contents this walk never watched, but whose
// DECLARED type states what its members are.
//
// A `Set<T>` iterates its members and a `Map<K, V>` its [key, value]
// pairs (sec-set.prototype-%symbol.iterator% yields each element of
// [[SetData]]; sec-map.prototype-%symbol.iterator% is entries, which
// yields a two-element Array per entry). What the members ARE is the
// type's own statement, held by the same generic discipline that makes
// MapValueAnnotation's V hold of every stored value: nothing enters a
// `Set<T>` without wearing T at its write site. So the element wears
// T's stated set at LIBRARY grade, exactly as the map-value read does.
//
// This runs after every reading that watches actual contents. A
// collection the walk DID build answers through collectionIterationElement
// above, with its real entries — strictly better than the declared type
// — and reaches this only when it stated nothing.
func declaredCollectionElement(ctx *FlowContext, iterable *ast.Node) *abstractdomain.AbstractValue {
	receiver := iterable
	view := ""
	if ast.IsCallExpression(iterable) {
		call := iterable.AsCallExpression()
		if call.Arguments == nil || len(call.Arguments.Nodes) != 0 ||
			!ast.IsPropertyAccessExpression(call.Expression) {
			return nil
		}
		access := call.Expression.AsPropertyAccessExpression()
		view = access.Name().Text()
		if view != "values" && view != "keys" && view != "entries" {
			return nil
		}
		receiver = access.Expression
	}
	typeNode := declaredTypeNodeOfReceiver(ctx, receiver)
	if typeNode == nil {
		// A CONSTRUCTION states the members a declaration would have.
		// `const s = new Set(arr)` spells no annotation of its own, but
		// AddEntriesFromIterable's Set counterpart calls add(item) on
		// every item the source yields (sec-set-iterable), so every
		// member of the built set is one of the source's own elements
		// and the source's declared element type states which. That is
		// the same generic discipline read one step earlier — at the
		// construction rather than at the annotation the construction
		// would have been assigned to — which is exactly how
		// mapValueTypeNodeOfReceiver already reads a Map built the same
		// way. Without it, a set built from a declared `Wide[]` stated
		// nothing about its members and every iteration of it went
		// unpinned.
		return builtSetMemberOfConstruction(ctx, receiver, view)
	}
	if !ast.IsTypeReferenceNode(typeNode) {
		return nil
	}
	typeRef := typeNode.AsTypeReferenceNode()
	if !ast.IsIdentifier(typeRef.TypeName) || typeRef.TypeArguments == nil {
		return nil
	}
	arguments := typeRef.TypeArguments.Nodes
	// the stated set behind one type argument, or nil where the
	// annotation reader cannot spell it
	stated := func(node *ast.Node) *abstractdomain.AbstractValue {
		read := annotations.AnnotationOfType(ctx.P, node, ctx.Registry, ctx.Objects)
		if read.Stated == nil || read.Unsupported != "" || read.Stated.Kind != annotations.DeclaredSet {
			return nil
		}
		out := abstractdomain.AtTrustLevel(
			abstractdomain.KnownSet(*read.Stated.Set, read.Stated.Temporal, abstractdomain.TrustProved, setKindTagOf(read.Stated.KindTag)),
			abstractdomain.TrustLibrary,
		)
		return &out
	}
	switch typeRef.TypeName.Text() {
	case "Set", "ReadonlySet":
		if len(arguments) != 1 {
			return nil
		}
		// a Set has no keys of its own — values(), keys() and the bare
		// iteration all yield its members
		if view == "entries" {
			member := stated(arguments[0])
			if member == nil {
				return nil
			}
			out := abstractdomain.KnownList([]abstractdomain.AbstractValue{*member, *member}, abstractdomain.TrustLibrary)
			return &out
		}
		return stated(arguments[0])
	case "Map", "ReadonlyMap":
		if len(arguments) != 2 {
			return nil
		}
		switch view {
		case "keys":
			return stated(arguments[0])
		case "values":
			return stated(arguments[1])
		}
		// a bare Map and entries() both yield [key, value]
		key, value := stated(arguments[0]), stated(arguments[1])
		if key == nil || value == nil {
			return nil
		}
		out := abstractdomain.KnownList([]abstractdomain.AbstractValue{*key, *value}, abstractdomain.TrustLibrary)
		return &out
	}
	return nil
}

// builtSetMemberOfConstruction is one member of iterating a Set the
// walk holds no entries for and whose binding spells no type — the
// `const s = new Set(arr)` shape, where the SOURCE states what the
// members are.
//
// sec-set-iterable calls `add` on every item the argument yields, so
// each member of the built set is one of the source's own elements.
// A source declared `Wide[]` therefore states that every member is a
// Wide, at LIBRARY grade — the same claim, on the same generic
// discipline, that a written-out `Set<Wide>` annotation would make.
// The array's own element type node is what states it, read through
// the same declaredTypeNodeOfReceiver the map-value side reads its
// source through.
//
// Only a `new Set(...)` initializer with a source whose declaration
// spells an array type is read. Anything else — an unspelled source, a
// Map construction (mapValueTypeNodeOfReceiver already owns that side),
// a set built by other means — answers nil and the iteration keeps
// whatever it had.
func builtSetMemberOfConstruction(ctx *FlowContext, receiver *ast.Node, view string) *abstractdomain.AbstractValue {
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := ctx.P.Checker.GetSymbolAtLocation(receiver)
	if symbol == nil || symbol.ValueDeclaration == nil ||
		!ast.IsVariableDeclaration(symbol.ValueDeclaration) {
		return nil
	}
	initializer := symbol.ValueDeclaration.AsVariableDeclaration().Initializer
	if initializer == nil || !ast.IsNewExpression(initializer) {
		return nil
	}
	newExpr := initializer.AsNewExpression()
	if !ast.IsIdentifier(newExpr.Expression) || newExpr.Expression.Text() != "Set" ||
		!resolvesToDefaultLib(ctx, newExpr.Expression) {
		return nil
	}
	// `new Set<T>(…)` spells T at the construction itself
	var memberNode *ast.Node
	if newExpr.TypeArguments != nil && len(newExpr.TypeArguments.Nodes) == 1 {
		memberNode = newExpr.TypeArguments.Nodes[0]
	} else {
		if newExpr.Arguments == nil || len(newExpr.Arguments.Nodes) != 1 {
			return nil
		}
		sourceNode := declaredTypeNodeOfReceiver(ctx, newExpr.Arguments.Nodes[0])
		if sourceNode == nil || !ast.IsArrayTypeNode(sourceNode) {
			return nil
		}
		memberNode = sourceNode.AsArrayTypeNode().ElementType
	}
	if memberNode == nil {
		return nil
	}
	read := annotations.AnnotationOfType(ctx.P, memberNode, ctx.Registry, ctx.Objects)
	if read.Stated == nil || read.Unsupported != "" || read.Stated.Kind != annotations.DeclaredSet {
		return nil
	}
	member := abstractdomain.AtTrustLevel(
		abstractdomain.KnownSet(*read.Stated.Set, read.Stated.Temporal, abstractdomain.TrustProved, setKindTagOf(read.Stated.KindTag)),
		abstractdomain.TrustLibrary,
	)
	// a Set has no keys of its own: values(), keys() and the bare
	// iteration all yield its members, and entries() pairs each member
	// with itself (sec-set.prototype.entries)
	if view == "entries" {
		out := abstractdomain.KnownList([]abstractdomain.AbstractValue{member, member}, abstractdomain.TrustLibrary)
		return &out
	}
	return &member
}

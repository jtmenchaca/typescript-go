// What a generator call hands back: the iterator, and what one element
// of it is.
//
// A call to `function* g()` does not run the body — it builds a
// Generator object, and the values the caller sees are the ones the
// body YIELDS, one per resumption. So the call's value is an iterator
// whose element is the join of what the body's yield expressions read.
// That is a different question from what the body RETURNS, which is
// the value a finished generator's last `next()` carries and nothing
// the loop or the spread ever sees.
//
// Two readings answer the element, in order:
//
//   - the body's own yields, walked here (generatorYieldElement). Each
//     `yield e` reads e in the DECLARATION's own scope, with no caller
//     knowledge — a generator's body resumes at times no call site
//     places, so nothing walk-ordered may ride into it.
//   - the declared return type's element, where the yields do not read:
//     `Generator<T>` / `Iterable<T>` / `IterableIterator<T>` state T,
//     and tsc checked the body's yields against it. That is the same
//     standing the builtin iterator classes rest on, so it is read
//     through the same door.
//
// The element then feeds the three routes a caller can take, all of
// which already exist for the library's own iterators: `.next()`
// through the result record, `[...g()]` through the array literal's
// spread, and `Array.from(g())` through its unary row.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// generatorReturnTypeNames: the lib types a generator declaration may
// state, each carrying the YIELD type as its FIRST type argument
// (lib.es2015.generator.d.ts: `Generator<T = unknown, TReturn = any,
// TNext = any>`; lib.es2015.iterable.d.ts: `IterableIterator<T,
// TReturn, TNext>`, `Iterator<T, TReturn, TNext>`;
// lib.es2018.asyncgenerator.d.ts: `AsyncGenerator<T, TReturn, TNext>`).
// The order is what makes one read serve them all: whatever else the
// type states, argument zero is what the body yields.
//
// `Iterable<T>` and `AsyncIterable<T>` are deliberately NOT here. They
// are the structural supertypes an ARRAY satisfies, so a function
// declared to return one may hand back a plain array, and reading that
// call as an iterator object would discard what the sequence routes
// already say about it. The names below all denote a value with the
// iterator's own `next`, which an array is not.
var generatorReturnTypeNames = map[string]bool{
	"Generator": true, "Iterator": true,
	"IterableIterator": true, "IteratorObject": true,
	"AsyncGenerator": true, "AsyncIterator": true,
	"AsyncIterableIterator": true, "AsyncIteratorObject": true,
}

// GeneratorDeclarationOf: the declaration behind a call, where that
// declaration is a generator — a `function*`, a generator function
// expression, or a `*method()`. Nil for every other callee. The
// contract lookup is the same door every other call-site reader uses,
// so a generator imported from another file resolves here too.
func GeneratorDeclarationOf(ctx *FlowContext, call *ast.Node) *ast.Node {
	if !ast.IsCallExpression(call) {
		return nil
	}
	contract := ContractOf(ctx, call.AsCallExpression().Expression)
	if contract == nil {
		return nil
	}
	if !IsGeneratorDeclaration(contract.Declaration) {
		return nil
	}
	return contract.Declaration
}

// IsGeneratorDeclaration: a declaration written with the star. The
// three forms that carry an asterisk token are the three the summary
// gate refuses (kernel_summaries.go), and they are read the same way
// here so one question has one answer.
func IsGeneratorDeclaration(declaration *ast.Node) bool {
	if declaration == nil {
		return false
	}
	switch declaration.Kind {
	case ast.KindFunctionDeclaration:
		return declaration.AsFunctionDeclaration().AsteriskToken != nil
	case ast.KindFunctionExpression:
		return declaration.AsFunctionExpression().AsteriskToken != nil
	case ast.KindMethodDeclaration:
		return declaration.AsMethodDeclaration().AsteriskToken != nil
	}
	return false
}

// yieldExpressionsOf collects every `yield e` in a body, skipping
// NESTED function-likes — an inner function's yields belong to that
// inner generator, not this one. It is returnedExpressionsOf's twin
// for the resumption positions, and it keeps the same shape of answer:
// one entry per yield, the expression or nil.
//
// A DELEGATING `yield* xs` is collected separately: what it hands the
// caller is not xs but every element OF xs, so its expression cannot
// join with the plain yields' values. The two lists come back apart
// and the reader below decides what each one can say.
func yieldExpressionsOf(body *ast.Node) (plain []*ast.Node, delegated []*ast.Node) {
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if node == nil {
			return
		}
		if node != body && ast.IsFunctionLike(node) {
			return
		}
		if ast.IsYieldExpression(node) {
			y := node.AsYieldExpression()
			if y.AsteriskToken != nil {
				delegated = append(delegated, y.Expression)
			} else {
				plain = append(plain, y.Expression)
			}
			// a yield's own operand may itself hold a yield
			// (`yield (yield a)`), so the walk continues through it
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	scan(body)
	return plain, delegated
}

// generatorYieldElement: the join of what the body's yield expressions
// read — what ONE element the caller receives admits.
//
// The walk runs on a FRESH environment. A generator body resumes at
// times no call site places, and each resumption's values come from
// the caller through `next(v)`, so no caller's knowledge may ride into
// the reading; what survives is what the yields say on their own
// (a literal, a computed value off the body's own locals, a parameter's
// declared set through the entry binding).
//
// Nothing determined answers (zero, false), and every route above
// falls back to the declared type. A `yield` with NO expression hands
// the caller undefined, which is a value like any other and joins.
func generatorYieldElement(ctx *FlowContext, declaration *ast.Node) (abstractdomain.AbstractValue, bool) {
	body := declaration.Body()
	if body == nil || !ast.IsBlock(body) {
		return abstractdomain.AbstractValue{}, false
	}
	// a generator that DELEGATES to itself — `function* g() { yield* g() }`,
	// or a cycle through a second generator — would ask this same reading
	// for its own element while that reading is still being built. The
	// re-entry set is the one the inline route already keeps
	// (ctx.Inlining, keyed by the callee's symbol): a body already in
	// flight answers nothing here, so the delegation declines and the
	// declared yield type is what speaks for the recursion.
	var walkingKey *ast.Symbol
	if name := declaration.Name(); name != nil {
		walkingKey = ctx.P.Checker.GetSymbolAtLocation(name)
	}
	walking := ctx.Inlining
	if walkingKey != nil {
		if _, inFlight := walking[walkingKey]; inFlight {
			return abstractdomain.AbstractValue{}, false
		}
		if walking == nil {
			walking = map[*ast.Symbol]struct{}{}
		}
		walking[walkingKey] = struct{}{}
		defer delete(walking, walkingKey)
	}
	plain, delegated := yieldExpressionsOf(body)
	if len(plain) == 0 && len(delegated) == 0 {
		return abstractdomain.AbstractValue{}, false
	}
	// the body's own scope, with nothing of the caller in it
	inner := *ctx
	inner.Report = func(d assignability.RefinementDiagnostic) {}
	inner.ReturnSink = nil
	inner.ThrowSink = nil
	inner.Declared = nil
	inner.DifferenceConstraints = nil
	inner.GateAssumptions = nil
	inner.CallableParams = nil
	inner.Inlining = walking
	bodyEnv := NewEnv()
	BindEntryEnv(BindEntryEnvInput{
		P:          inner.P,
		Env:        bodyEnv,
		Parameters: declaration.Parameters(),
	})

	var joined *abstractdomain.AbstractValue
	join := func(piece abstractdomain.AbstractValue) bool {
		if piece.Kind == abstractdomain.KindUnknown {
			return false
		}
		if joined == nil {
			joined = &piece
			return true
		}
		merged := abstractdomain.JoinKnown(*joined, piece)
		joined = &merged
		return true
	}
	for _, expression := range plain {
		// a bare `yield` hands the caller undefined
		if expression == nil {
			if !join(abstractdomain.Undef) {
				return abstractdomain.AbstractValue{}, false
			}
			continue
		}
		if !join(evaluateExpression(&inner, bodyEnv, expression)) {
			return abstractdomain.AbstractValue{}, false
		}
	}
	// `yield* xs` hands the caller every element OF xs, so the element
	// this generator poses is the inner iterable's element. Where that
	// element does not read, the whole join has a position it cannot
	// speak for, and the reading declines — a delegation is a real
	// source of values, and passing over it would claim the plain
	// yields cover every element when they do not.
	for _, expression := range delegated {
		if expression == nil {
			return abstractdomain.AbstractValue{}, false
		}
		element := delegatedElementOf(&inner, bodyEnv, expression)
		if element == nil || !join(*element) {
			if assignability.CollectingReasons() {
				assignability.NoteReason(assignability.ReasonNote{
					Site:        "expression",
					Node:        expression,
					Said:        "yield* hands on another iterable's elements — this one is not read",
					Unsupported: true,
				})
			}
			return abstractdomain.AbstractValue{}, false
		}
	}
	if joined == nil {
		return abstractdomain.AbstractValue{}, false
	}
	return *joined, true
}

// delegatedElementOf: one element of the iterable a `yield*` hands on.
// The same readers the for-of head uses answer it — a tracked
// collection, a web collection's iterator, a library iterator view, a
// nested generator call — and a sequence the walk holds answers
// through its own element. Nil where none speaks.
func delegatedElementOf(ctx *FlowContext, env Env, iterable *ast.Node) *abstractdomain.AbstractValue {
	if element := IterationElementOf(ctx, env, iterable); element != nil {
		return element
	}
	if sequence, ok := GeneratorSequenceOf(ctx, iterable); ok {
		element := ElementOf(sequence)
		return &element
	}
	if sequence, ok := builtinIteratorSequenceOf(ctx, iterable); ok {
		element := ElementOf(sequence)
		return &element
	}
	walked := evaluateExpression(ctx, env, iterable)
	element := ElementOf(walked)
	if element.Kind == abstractdomain.KindUnknown {
		return nil
	}
	return &element
}

// generatorDeclaredElement: the element a generator's DECLARED return
// type states — the T of `Generator<T, …>`, `IterableIterator<T>` and
// their kin. tsc checks every `yield e` in the body against that T, so
// the claim is the declaration's and it wears LIBRARY grade the way
// every other stated-type reading does.
//
// The type is read off the CALL's own resolved type rather than the
// declaration's written node, so an inferred return type answers here
// too: tsc infers `Generator<number, void, unknown>` for a body
// yielding numbers, and argument zero is the same T either way.
func generatorDeclaredElement(ctx *FlowContext, call *ast.Node) (*checker.Type, bool) {
	t := typereading.TypeAtLocation(ctx.P.Checker, call)
	if t == nil {
		return nil, false
	}
	symbol := t.Symbol()
	if symbol == nil || !generatorReturnTypeNames[symbol.Name] {
		return nil, false
	}
	if !ctx.P.Checker.SymbolInDefaultLib(symbol) {
		return nil, false
	}
	if (t.ObjectFlags() & checker.ObjectFlagsReference) == 0 {
		return nil, false
	}
	arguments := ctx.P.Checker.GetTypeArguments(t)
	if len(arguments) == 0 {
		return nil, false
	}
	return arguments[0], true
}

// GeneratorElementOf: what ONE value a generator call hands its caller
// admits — the body's yields where they read, the declared yield type
// otherwise. The two are the same claim about the same position, so a
// caller reads whichever speaks and never has to know which did.
func GeneratorElementOf(ctx *FlowContext, call *ast.Node) (abstractdomain.AbstractValue, bool) {
	declaration := GeneratorDeclarationOf(ctx, call)
	if declaration == nil {
		return abstractdomain.AbstractValue{}, false
	}
	if element, ok := generatorYieldElement(ctx, declaration); ok {
		return element, true
	}
	stated, ok := generatorDeclaredElement(ctx, call)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	read, hasRead := typereading.ReadHostType(ctx.P.Checker, stated, call, 0)
	if !hasRead || read.Kind == abstractdomain.KindUnknown {
		return abstractdomain.AbstractValue{}, false
	}
	return abstractdomain.AtTrustLevel(read, abstractdomain.TrustLibrary), true
}

// GeneratorSequenceOf: the array a generator's elements build when
// something DRAINS it whole — `[...g()]`, `Array.from(g())`. Every
// value the body yields lands in the array, in order
// (sec-runtime-semantics-arrayaccumulation, sec-array.from step 6), and
// the COUNT is whatever the body's own control flow decides, which the
// element reading does not state beyond a proven FLOOR (below). So the
// answer is the star of what one element admits: these elements, length
// unknown past that floor — exactly what the library iterators' own
// drain answers.
//
// A set-valued element stars into the tuple layer; an element the
// graph holds — a record, a class instance — stars into the
// object-star instead. StarOfElementAtLeast is the one recipe for both,
// so the two layers do not have to be told apart here.
func GeneratorSequenceOf(ctx *FlowContext, call *ast.Node) (abstractdomain.AbstractValue, bool) {
	element, ok := GeneratorElementOf(ctx, call)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	declaration := GeneratorDeclarationOf(ctx, call)
	lo := generatorMinimumYieldCount(declaration)
	return typereading.StarOfElementAtLeast(element, lo)
}

// generatorMinimumYieldCount: how many yields a generator body runs
// through UNCONDITIONALLY before anything could stop it — a sound lower
// bound on the drained sequence's length, read straight-line from the
// top of the body.
//
// A generator resumes deterministically, one statement at a time
// (sec-generatorstart's suspended-start context runs the body forward
// exactly as written). So a leading run of plain `yield e;` expression
// statements at the body's OWN top level — no block, no branch, no loop
// ahead of them — each runs before the next, and the count of that run
// is a floor on the total: the body cannot finish, throw, or loop back
// without having passed through all of them first. The scan stops at
// the first statement that is not a bare plain-yield expression
// statement (a block, an if, a loop, a yield* delegation, a yield
// nested inside a larger expression) — past that point nothing is
// proven unconditional anymore, so the count freezes rather than
// guessing.
func generatorMinimumYieldCount(declaration *ast.Node) int {
	if declaration == nil {
		return 0
	}
	body := declaration.Body()
	if body == nil || !ast.IsBlock(body) {
		return 0
	}
	count := 0
	for _, statement := range body.AsBlock().Statements.Nodes {
		if !ast.IsExpressionStatement(statement) {
			break
		}
		expression := statement.AsExpressionStatement().Expression
		if !ast.IsYieldExpression(expression) {
			break
		}
		if expression.AsYieldExpression().AsteriskToken != nil {
			// a delegation's own count depends on the inner iterable,
			// which this straight-line scan does not read
			break
		}
		count++
	}
	return count
}

// GeneratorCallResult: the value a generator CALL has. It is the
// Generator object itself — not the body's return value, and not one
// element — so nothing the tuple layer or the object graph spells
// stands for it, and the reading is the opaque one: a value that
// entered from outside this walk's determination.
//
// That is the whole point of this row. Without it the call takes the
// inline route (evaluate_call_expression.go), which walks the body and
// answers what `return e` gave — a value the caller NEVER sees, since a
// generator's return only surfaces as the `value` of a `done: true`
// next(). The routes that CAN say something about a generator —
// `.next()`, the spread, `Array.from` — read the element through
// GeneratorElementOf above, on top of this admission.
//
// The body's own writes still happen when the caller drains it, so the
// arguments and captured names it may write forget at the call, exactly
// as they do for any callee whose body this walk does not run.
func GeneratorCallResult(ctx *FlowContext, env Env, call *ast.Node) *abstractdomain.AbstractValue {
	declaration := GeneratorDeclarationOf(ctx, call)
	if declaration == nil {
		return nil
	}
	if assignability.CollectingReasons() {
		assignability.NoteReason(assignability.ReasonNote{
			Site: "expression",
			Node: call,
			Said: CalleeWords(call.AsCallExpression().Expression) +
				"() builds a generator — the body resumes at each next(), which is not a body this walk runs",
			Unsupported: false,
		})
	}
	// the callee's body runs LATER, at each resumption, and its writes
	// land then; a name it may write cannot keep its current reading
	forgetGeneratorWrites(ctx, env, call, declaration)
	out := abstractdomain.Opaque
	return &out
}

// forgetGeneratorWrites: the names a generator body may write forget at
// the call. The body has not run yet — a generator call only builds the
// object — but the walk cannot place the resumptions that run it, so
// every write it holds is a write that may already have landed by the
// time anything downstream reads.
func forgetGeneratorWrites(ctx *FlowContext, env Env, call *ast.Node, declaration *ast.Node) {
	written := map[string]struct{}{}
	AssignedNames(ctx.P.Checker, declaration, written)
	for name := range written {
		if _, ok := env.Get(name); ok {
			HavocEnv(ctx.Aliases, env, name)
		}
	}
	callExpression := call.AsCallExpression()
	if callExpression.Arguments == nil {
		return
	}
	for _, argument := range callExpression.Arguments.Nodes {
		if !ast.IsIdentifier(argument) {
			continue
		}
		if _, ok := env.Get(argument.Text()); ok && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
			HavocEnv(ctx.Aliases, env, argument.Text())
		}
	}
}

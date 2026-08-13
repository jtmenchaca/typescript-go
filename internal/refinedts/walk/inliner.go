// from interprocedural/inliner.ts
//
// THE inliner: a body runs synchronously on the caller's
// environment — parameters and the body's own declared names shadow
// (and restore), a reference parameter's final state writes back to
// its argument through ONE epilogue, and a changed rest parameter
// forgets every remaining holder. The stored-closure and
// callback-node shapes both run here; the contract inline
// (evaluate_call.ts) shares the epilogue and the shadow discipline.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// ParameterKnown is the known a parameter wears at a call: its
// positional argument's — or, for a trailing REST parameter, the
// exact LIST of the remaining arguments' knowns (the rest parameter
// is bound to an array of the leftover arguments in order —
// tmp/ecma262/spec.html sec-functiondeclarationinstantiation).
func ParameterKnown(parameter *ast.Node, index int, call *ast.Node, argKnowns []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	pd := parameter.AsParameterDeclaration()
	if pd.DotDotDotToken == nil {
		if index >= 0 && index < len(argKnowns) {
			return argKnowns[index]
		}
		return silence.Residue()
	}
	// a spread at the call keeps the walk from counting the rest: an
	// exact spread was expanded into positions upstream, but an
	// inexpansible one collapsed to a single slot, and the LIST built
	// here would state a wrong length
	callExpr := call.AsCallExpression()
	for _, argument := range callExpr.Arguments.Nodes {
		if ast.IsSpreadElement(argument) {
			return silence.Residue()
		}
	}
	rest := argKnowns
	if index < len(argKnowns) {
		rest = argKnowns[index:]
	} else {
		rest = nil
	}
	return abstractdomain.KnownList(rest, abstractdomain.TrustProved)
}

// InlineStoredClosure is a stored closure invoked directly by name:
// `const f = (…) => …` called as `f(…)`. The call runs synchronously
// on this environment, so the body is walked with the arguments
// bound and parameter names shadow-saved; a re-entered symbol is
// recursion and answers unknown. Only a CONST binding qualifies — a
// let could have been rebound between declaration and call. Returns
// nil when the callee is not such a closure.
func InlineStoredClosure(ctx *FlowContext, env Env, call *ast.Node, argKnowns []abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	callExpr := call.AsCallExpression()
	var closure Callback
	var symbol *ast.Symbol
	// a const bound to `f.bind(...)` runs the TARGET function with the
	// prebound arguments prepended — the bound [[Call]]'s argument list
	// is the list-concatenation of the prebound arguments and this
	// call's own (tmp/ecma262/spec.html sec-function.prototype.bind,
	// sec-bound-function-exotic-objects-call-thisargument-argumentslist)
	var preboundArguments []*ast.Node
	direct := callExpr.Expression
	if ast.IsParenthesizedExpression(direct) {
		direct = direct.AsParenthesizedExpression().Expression
	}
	if ast.IsArrowFunction(direct) || ast.IsFunctionExpression(direct) {
		// an IIFE: the literal runs here and nowhere else — it was never
		// collected as a named contract, so its stated positions checkAssignability
		// at this call
		closure = direct
		for i, parameter := range closure.Parameters() {
			pd := parameter.AsParameterDeclaration()
			if pd.Type == nil {
				continue
			}
			result := annotations.AnnotationOfType(ctx.P, pd.Type, ctx.Registry, ctx.Objects)
			if result.Stated == nil {
				continue
			}
			var argument abstractdomain.AbstractValue
			if i < len(argKnowns) {
				argument = argKnowns[i]
			} else {
				argument = silence.Residue()
			}
			var at *ast.Node
			if i < len(callExpr.Arguments.Nodes) {
				at = callExpr.Arguments.Nodes[i]
			} else {
				at = call
			}
			CheckAssignability(ctx, argument, *result.Stated, at, "argument", nil)
		}
	} else {
		if !ast.IsIdentifier(callExpr.Expression) {
			return nil
		}
		found := ctx.P.Checker.GetSymbolAtLocation(callExpr.Expression)
		if found == nil {
			return nil
		}
		declaration := found.ValueDeclaration
		if declaration == nil || !ast.IsVariableDeclaration(declaration) {
			return nil
		}
		vd := declaration.AsVariableDeclaration()
		if vd.Initializer == nil || !ast.IsVariableDeclarationList(declaration.Parent) ||
			(declaration.Parent.Flags&ast.NodeFlagsConst) == 0 {
			return nil
		}
		initializer := vd.Initializer
		if ast.IsArrowFunction(initializer) || ast.IsFunctionExpression(initializer) {
			closure = initializer
		} else {
			bound := BoundFunctionOf(ctx, initializer)
			if bound != nil {
				closure = bound.Target
				preboundArguments = bound.PreboundArguments
			} else {
				// a const initialized by a factory CALL whose every return is
				// the same pinned function: `const add = makeAdder()` calls
				// that function here, exactly like a const-bound arrow
				init := initializer
				for ast.IsParenthesizedExpression(init) || ast.IsAsExpression(init) {
					if ast.IsParenthesizedExpression(init) {
						init = init.AsParenthesizedExpression().Expression
					} else {
						init = init.AsAsExpression().Expression
					}
				}
				if !ast.IsCallExpression(init) {
					return nil
				}
				made := narrowing.FactoryPinnedFunction(ctx.P.Checker, init)
				if made == nil {
					return nil
				}
				closure = made
			}
		}
		symbol = found
	}
	body := closure.Body()
	if body == nil {
		return nil
	}
	// the prebound VALUES were fixed where the bind ran, in an
	// environment this call site does not hold — only a syntactic
	// literal carries; everything else binds unknown
	preboundKnowns := make([]abstractdomain.AbstractValue, len(preboundArguments))
	for i, argument := range preboundArguments {
		if SyntacticLiteral(argument) {
			silentCtx := *ctx
			silentCtx.Report = func(d assignability.RefinementDiagnostic) {}
			preboundKnowns[i] = evaluateExpression(&silentCtx, NewEnv(), argument)
		} else {
			preboundKnowns[i] = silence.Residue()
		}
	}
	boundOffset := len(preboundKnowns)
	allArgKnowns := argKnowns
	if boundOffset > 0 {
		allArgKnowns = append(append([]abstractdomain.AbstractValue{}, preboundKnowns...), argKnowns...)
	}
	inlining := ctx.Inlining
	if inlining == nil {
		inlining = map[*ast.Symbol]struct{}{}
	}
	if symbol != nil {
		if _, ok := inlining[symbol]; ok {
			out := silence.CutUnknown() // recursion: honest silence
			return &out
		}
		inlining[symbol] = struct{}{}
	}

	// an IIFE's stated RETURN judges too — read it before the walk
	var resultStated *annotations.DeclaredRefinement
	closureType := closure.Type()
	if closureType != nil && symbol == nil {
		result := annotations.AnnotationOfType(ctx.P, closureType, ctx.Registry, ctx.Objects)
		if result.Stated != nil {
			resultStated = result.Stated
		}
	}

	// parameters shadow — save what they cover, bind the arguments;
	// the body's OWN declarations shadow too, or a callee-local
	// `const total = …` would overwrite a same-named caller binding
	// and survive the restore
	type savedEntry struct {
		value abstractdomain.AbstractValue
		has   bool
	}
	saved := map[string]savedEntry{}
	closureParameters := closure.Parameters()
	for i, parameter := range closureParameters {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		if v, ok := env.Get(name.Text()); ok {
			saved[name.Text()] = savedEntry{value: v, has: true}
		} else {
			saved[name.Text()] = savedEntry{}
		}
		env.Set(name.Text(), ParameterKnown(parameter, i, call, allArgKnowns))
	}
	{
		locals := map[string]struct{}{}
		declaredNames(body, locals)
		for name := range locals {
			if _, already := saved[name]; !already {
				if v, ok := env.Get(name); ok {
					saved[name] = savedEntry{value: v, has: true}
				} else {
					saved[name] = savedEntry{}
				}
			}
		}
	}

	result := silence.Residue()
	if ast.IsBlock(body) {
		var sink []abstractdomain.AbstractValue
		inner := *ctx
		inner.ReturnSink = &sink
		inner.Inlining = inlining
		AnalyzeStatements(&inner, env, body.AsBlock().Statements.Nodes, resultStated)
		if len(sink) > 0 {
			joined := sink[0]
			for _, v := range sink[1:] {
				joined = abstractdomain.JoinKnown(joined, v)
			}
			result = joined
		}
	} else {
		inner := *ctx
		inner.Inlining = inlining
		result = evaluateExpression(&inner, env, body)
		if resultStated != nil {
			CheckAssignability(ctx, result, *resultStated, body, "a returned value", nil)
		}
	}

	// a REFERENCE parameter's final state lands back on its identifier
	// argument — the restore must not erase a mutation made through
	// the parameter's name
	posts := map[int]abstractdomain.AbstractValue{}
	for i, parameter := range closureParameters {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		if v, ok := env.Get(name.Text()); ok {
			posts[i] = v
		} else {
			posts[i] = silence.Residue()
		}
	}
	for name, entry := range saved {
		if entry.has {
			env.Set(name, entry.value)
		} else {
			env.Delete(name)
		}
	}
	for i, parameter := range closureParameters {
		post, ok := posts[i]
		if !ok {
			continue
		}
		entry := ParameterKnown(parameter, i, call, allArgKnowns)
		if abstractdomain.SameKnown(post, entry) {
			continue
		}
		pd := parameter.AsParameterDeclaration()
		if pd.DotDotDotToken == nil && i < boundOffset {
			// a PREBOUND parameter's argument expression was spelled at
			// the bind, not here; the walk holds no link from it to this
			// call, so every named root it mentions forgets
			roots := map[string]struct{}{}
			var collect func(node *ast.Node)
			collect = func(node *ast.Node) {
				if ast.IsIdentifier(node) {
					if _, ok := env.Get(node.Text()); ok {
						roots[node.Text()] = struct{}{}
					}
				}
				node.ForEachChild(func(child *ast.Node) bool {
					collect(child)
					return false
				})
			}
			collect(preboundArguments[i])
			for name := range roots {
				HavocEnv(ctx.Aliases, env, name)
			}
			continue
		}
		var argument *ast.Node
		argIndex := i - boundOffset
		if argIndex >= 0 && argIndex < len(callExpr.Arguments.Nodes) {
			argument = callExpr.Arguments.Nodes[argIndex]
		}
		restFrom := argIndex
		if restFrom < 0 {
			restFrom = 0
		}
		var restArguments []*ast.Node
		if restFrom < len(callExpr.Arguments.Nodes) {
			restArguments = callExpr.Arguments.Nodes[restFrom:]
		}
		WriteBackParameter(ctx, env, writeBackParameterParams{
			parameter:     parameter,
			post:          post,
			entry:         entry,
			argument:      argument,
			restArguments: restArguments,
		})
	}
	if symbol != nil {
		delete(inlining, symbol)
	}
	return &result
}

// unwrapArgument is an argument expression with its parentheses and
// as-casts peeled — the write-back targets the expression that names
// caller state.
func unwrapArgument(e *ast.Node) *ast.Node {
	cursor := e
	for ast.IsParenthesizedExpression(cursor) || ast.IsAsExpression(cursor) {
		if ast.IsParenthesizedExpression(cursor) {
			cursor = cursor.AsParenthesizedExpression().Expression
		} else {
			cursor = cursor.AsAsExpression().Expression
		}
	}
	return cursor
}

// forgetRestArguments: a changed REST list means an element's
// referent may have been written — each remaining argument's holder
// forgets; the list itself belongs to no caller name.
func forgetRestArguments(ctx *FlowContext, env Env, restArguments []*ast.Node) {
	for _, argument := range restArguments {
		source := unwrapArgument(argument)
		if ast.IsIdentifier(source) {
			if _, ok := env.Get(source.Text()); ok {
				HavocEnv(ctx.Aliases, env, source.Text())
				continue
			}
		}
		if dataflowfacts.ReferenceTyped(ctx.P.Checker, source) {
			ForgetThrough(ctx, env, source)
		}
	}
}

// writeBackParameterParams is the destructured-parameters struct for
// WriteBackParameter (3+ fields, per convention).
type writeBackParameterParams struct {
	parameter *ast.Node
	// post is what the parameter's name held when the body finished.
	post abstractdomain.AbstractValue
	// entry is what the parameter started the body as.
	entry abstractdomain.AbstractValue
	// argument is this parameter's own argument expression, if any
	// (nil means absent).
	argument *ast.Node
	// restArguments are the arguments a rest parameter collected.
	restArguments []*ast.Node
}

// WriteBackParameter is THE write-back a parameter owes its argument
// after an inline — one epilogue for every inlining shape (stored
// closure, contract call, callback node). A parameter that left the
// body unchanged owes nothing. A changed REST parameter forgets
// every remaining argument's holder. Otherwise the final state lands
// on an identifier argument's name, and a reference-typed projection
// argument makes its holder forget — a function literal argument is
// not caller state and stays untouched.
func WriteBackParameter(ctx *FlowContext, env Env, p writeBackParameterParams) {
	if abstractdomain.SameKnown(p.post, p.entry) {
		return
	}
	if p.parameter.AsParameterDeclaration().DotDotDotToken != nil {
		forgetRestArguments(ctx, env, p.restArguments)
		return
	}
	if p.argument == nil {
		return
	}
	target := unwrapArgument(p.argument)
	if ast.IsIdentifier(target) {
		if _, ok := env.Get(target.Text()); ok {
			UpdateTrackedEnv(ctx.Aliases, env, target.Text(), p.post)
			return
		}
	}
	if !ast.IsArrowFunction(target) && !ast.IsFunctionExpression(target) && dataflowfacts.ReferenceTyped(ctx.P.Checker, target) {
		ForgetThrough(ctx, env, target)
	}
}

// declaredNames is every binding NAME a subtree declares — an
// inline walks the callee's body on a copy of the caller's
// environment, so the callee's own names must not read as (or write
// back to) caller state. Read from the syntactic-facts seam —
// computed once per node, where it used to rescan the body on every
// inline.
func declaredNames(node *ast.Node, into map[string]struct{}) {
	for name := range dataflowfacts.DeclaredNameSet(node) {
		into[name] = struct{}{}
	}
}

// InlineCallbackNode is inlineCallbackNode in the TS source.
func InlineCallbackNode(ctx *FlowContext, env Env, call *ast.Node, callback Callback, argKnowns []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	callExpr := call.AsCallExpression()
	type savedEntry struct {
		value abstractdomain.AbstractValue
		has   bool
	}
	saved := map[string]savedEntry{}
	callbackParameters := callback.Parameters()
	for i, parameter := range callbackParameters {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		if v, ok := env.Get(name.Text()); ok {
			saved[name.Text()] = savedEntry{value: v, has: true}
		} else {
			saved[name.Text()] = savedEntry{}
		}
		env.Set(name.Text(), ParameterKnown(parameter, i, call, argKnowns))
	}
	body := callback.Body()
	// the body's own declarations shadow too — see InlineStoredClosure
	if body != nil {
		locals := map[string]struct{}{}
		declaredNames(body, locals)
		for name := range locals {
			if _, already := saved[name]; !already {
				if v, ok := env.Get(name); ok {
					saved[name] = savedEntry{value: v, has: true}
				} else {
					saved[name] = savedEntry{}
				}
			}
		}
	}
	result := silence.Residue()
	if body != nil {
		var sink []abstractdomain.AbstractValue
		inner := *ctx
		inner.ReturnSink = &sink
		if ast.IsBlock(body) {
			AnalyzeStatements(&inner, env, body.AsBlock().Statements.Nodes, nil)
			if len(sink) > 0 {
				joined := sink[0]
				for _, v := range sink[1:] {
					joined = abstractdomain.JoinKnown(joined, v)
				}
				result = joined
			}
		} else {
			result = evaluateExpression(&inner, env, body)
		}
	}
	posts := map[int]abstractdomain.AbstractValue{}
	for i, parameter := range callbackParameters {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		if v, ok := env.Get(name.Text()); ok {
			posts[i] = v
		} else {
			posts[i] = silence.Residue()
		}
	}
	for name, entry := range saved {
		if entry.has {
			env.Set(name, entry.value)
		} else {
			env.Delete(name)
		}
	}
	for i, parameter := range callbackParameters {
		post, ok := posts[i]
		if !ok {
			continue
		}
		var argument *ast.Node
		if i < len(callExpr.Arguments.Nodes) {
			argument = callExpr.Arguments.Nodes[i]
		}
		var restArguments []*ast.Node
		if i < len(callExpr.Arguments.Nodes) {
			restArguments = callExpr.Arguments.Nodes[i:]
		}
		WriteBackParameter(ctx, env, writeBackParameterParams{
			parameter:     parameter,
			post:          post,
			entry:         ParameterKnown(parameter, i, call, argKnowns),
			argument:      argument,
			restArguments: restArguments,
		})
	}
	return result
}

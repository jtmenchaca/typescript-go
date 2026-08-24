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

// ArgumentNodesOf is THE SYNTACTIC argument-reading seam: the
// expressions a call-like node hands its callee, one slot per WRITTEN
// argument. It reads syntax alone, so a spread stays one slot here —
// how many positions it really occupies is a question about its held
// value, which this function does not have.
//
// A reader that only wants "which expressions did the programmer write
// at this call" asks here. A reader that PLACES a parameter against an
// argument asks for the effective arguments instead
// (EffectiveArgumentsOf, spread_expansion.go), whose entries are
// positions rather than written arguments and whose nodes and values
// move together.
//
// A CallExpression's slots are its own argument expressions.
//
// A TAGGED TEMPLATE's are the template object first and the `${…}`
// substitutions after it, in source order — the tag call's argument
// list is the list-concatenation of « siteObj » and the substitution
// values (specifications/javascript/spec.html
// sec-runtime-semantics-argumentlistevaluation, the
// `TemplateLiteral : SubstitutionTemplate` and
// `TemplateLiteral : NoSubstitutionTemplate` alternatives; the note at
// sec-tagged-templates states the same shape). Position 0 has NO
// source expression: the template object is built by GetTemplateObject
// from the literal itself, not written by the programmer, so the slot
// is nil. Every caller already reads a nil argument slot as "no caller
// state behind this position" and writes nothing back to it, which is
// exactly right — a write into the frozen template object reaches no
// name this file tracks (sec-gettemplateobject freezes it).
//
// Anything else has no argument list at all.
func ArgumentNodesOf(call *ast.Node) []*ast.Node {
	if call == nil {
		return nil
	}
	if ast.IsCallExpression(call) {
		arguments := call.AsCallExpression().Arguments
		if arguments == nil {
			return nil
		}
		return arguments.Nodes
	}
	if ast.IsTaggedTemplateExpression(call) {
		substitutions := TemplateSubstitutions(call.AsTaggedTemplateExpression().Template)
		slots := make([]*ast.Node, 0, len(substitutions)+1)
		slots = append(slots, nil) // the template object, synthesized
		return append(slots, substitutions...)
	}
	return nil
}

// CalleeExpressionOf is the expression a call-like node calls: a
// CallExpression's `Expression`, a tagged template's `Tag`. The two
// forms name their callee under different field names, and every
// reader that wants "who is being called" asks here rather than
// casting to one of them.
func CalleeExpressionOf(call *ast.Node) *ast.Node {
	if call == nil {
		return nil
	}
	if ast.IsCallExpression(call) {
		return call.AsCallExpression().Expression
	}
	if ast.IsTaggedTemplateExpression(call) {
		return call.AsTaggedTemplateExpression().Tag
	}
	return nil
}

// ParameterKnown is the known a parameter wears at a call: its
// positional argument's, `undefined` where the call passes no
// argument for that position, or — for a trailing REST parameter —
// the exact LIST of the remaining arguments' knowns (the rest
// parameter is bound to an array of the leftover arguments in order —
// specifications/javascript/spec.html sec-functiondeclarationinstantiation).
//
// The positions are the EFFECTIVE ones, so a call spreading an exact
// source places its parameters and states its rest list the way a call
// writing those arguments out one at a time does. A list marked inexact
// carries a spread whose count is unread: no position past it is placed
// and no rest length is stated, so both readings answer residue.
func ParameterKnown(parameter *ast.Node, index int, effective EffectiveArguments) abstractdomain.AbstractValue {
	pd := parameter.AsParameterDeclaration()
	if pd.DotDotDotToken == nil {
		if index >= 0 && index < len(effective.Knowns) {
			return effective.Knowns[index]
		}
		// a position the call passes no argument for binds exactly
		// `undefined` (specifications/javascript/spec.html
		// sec-functiondeclarationinstantiation: the argument list is
		// shorter than the parameter list, and IteratorBindingInitialization
		// binds the missing positions to undefined). A parameter with a
		// DEFAULT binds the default's value instead, which this function
		// does not evaluate, so that spelling keeps its silence — and so
		// does a position past an unread spread, which the walk cannot
		// prove the call leaves empty.
		if pd.Initializer != nil {
			return silence.Residue()
		}
		if !effective.Exact {
			return silence.Residue()
		}
		return abstractdomain.Undef
	}
	// an unread spread keeps the walk from counting the rest: it holds
	// one position for a run of arguments of unknown length, so the LIST
	// built here would state a wrong length
	if !effective.Exact {
		return silence.Residue()
	}
	var rest []abstractdomain.AbstractValue
	if index >= 0 && index < len(effective.Knowns) {
		rest = effective.Knowns[index:]
	}
	return abstractdomain.KnownList(rest, abstractdomain.TrustProved)
}

// BoundParameterKnown is what an INLINED parameter is actually bound to:
// ParameterKnown's argument value met with the parameter's own declared
// type (entryStateMeet, entry_env.go). Every inlining shape binds through
// this one function — the contract body, the stored closure, the callback
// node — and every write-back baseline reads it too, so the "did the body
// change this parameter" comparison is against the value the body really
// started from.
//
// The meet costs no exactness the declaration permits: the annotation is a
// ceiling and a value inside it passes through whole. It removes one claim
// the declaration cannot carry — an object literal's COMPLETE key set
// bound to a parameter whose type is an open map (`Record<K, V>`), where
// some other call may hand a key this one never wrote.
func BoundParameterKnown(ctx *FlowContext, parameter *ast.Node, index int, effective EffectiveArguments) abstractdomain.AbstractValue {
	return entryStateMeet(
		ctx.P.Checker,
		parameter,
		ParameterArgumentOrDefault(ctx, parameter, index, effective),
		InitialStateOfPlainParameter(ctx.P, parameter),
	)
}

// ParameterArgumentOrDefault is ParameterKnown, with the ONE case that
// function deliberately declines filled in here instead: a MISSING
// argument on a parameter carrying its own default (`person: T = {age:
// 18}`) runs that default expression, silently, on a fresh environment —
// the same "the default runs exactly when the slot is absent" rule
// withDefault (destructuring.go) already applies to a destructured
// element's own default, extended to the WHOLE-PARAMETER default a plain
// identifier or record parameter carries. ParameterKnown itself stays
// pure syntax-reading (no ctx, no evaluation) for its other callers — the
// call-site argument vector, the trust floor — which only ever ask what
// the CALLER sent, never what a callee's own default would compute.
func ParameterArgumentOrDefault(ctx *FlowContext, parameter *ast.Node, index int, effective EffectiveArguments) abstractdomain.AbstractValue {
	known := ParameterKnown(parameter, index, effective)
	pd := parameter.AsParameterDeclaration()
	if pd.DotDotDotToken != nil || pd.Initializer == nil {
		return known
	}
	// the position IS missing (not merely unresolved by an unread spread):
	// ParameterKnown answers residue for BOTH a genuinely-absent argument
	// and a spread-uncertain one, and only the first should run the
	// default — a call passing an unread spread may still be sending this
	// position, so running the default there would be a wrong claim, not
	// a weak one
	if index >= 0 && index < len(effective.Knowns) {
		return known
	}
	if !effective.Exact {
		return known
	}
	silent := *ctx
	silent.Report = func(assignability.RefinementDiagnostic) {}
	return evaluateExpression(&silent, NewEnv(), pd.Initializer)
}

// BindInlineParameter binds ONE parameter's names into callEnv — an
// identifier parameter takes BoundParameterKnown's single value, and a
// destructured parameter (`[a]`, `{age}`) takes the SAME argument value
// destructured leaf by leaf, each leaf's own binding computed against
// what the pattern position itself states: ReadDestructuring already
// reads a source value's slots exactly the way a leaf's runtime binding
// works (repetition indexing, key reads, rest collection), so the
// pattern case only needs to feed it the position's own raw argument
// value (ParameterArgumentOrDefault, which also runs a WHOLE-PARAMETER
// default when the position is genuinely missing) instead of leaving
// every leaf unbound.
//
// Every inline route (the contract body walk, the pure-recovery
// shortcut) binds a parameter through this one function so a
// destructured parameter's leaves are never silently skipped the way an
// identifier-only loop would skip them.
func BindInlineParameter(ctx *FlowContext, callEnv Env, parameter *ast.Node, index int, effective EffectiveArguments) {
	decl := parameter.AsParameterDeclaration()
	name := decl.Name()
	if ast.IsIdentifier(name) {
		callEnv.Set(name.Text(), BoundParameterKnown(ctx, parameter, index, effective))
		return
	}
	if name == nil {
		return
	}
	argument := ParameterArgumentOrDefault(ctx, parameter, index, effective)
	ReadDestructuring(name, argument, func(leaf string, held abstractdomain.AbstractValue, at *ast.Node) {
		callEnv.Set(leaf, held)
	})
}

// InlineStoredClosure is a stored closure invoked directly by name:
// `const f = (…) => …` called as `f(…)`. The call runs synchronously
// on this environment, so the body is walked with the arguments
// bound and parameter names shadow-saved; a re-entered symbol is
// recursion and answers unknown. A CONST binding qualifies outright;
// a LET binding qualifies only where the call's own immediately
// preceding sibling statement is a plain reassignment of it to an
// arrow or function expression (immediatelyPrecedingClosureAssignment)
// — a let could have been rebound ANYWHERE ELSE between declaration
// and call, so every other let-bound callee still declines. Returns
// nil when the callee is not such a closure.
func InlineStoredClosure(ctx *FlowContext, env Env, call *ast.Node, effective EffectiveArguments) *abstractdomain.AbstractValue {
	calleeExpression := CalleeExpressionOf(call)
	if calleeExpression == nil {
		return nil
	}
	var closure Callback
	var symbol *ast.Symbol
	// a const bound to `f.bind(...)` runs the TARGET function with the
	// prebound arguments prepended — the bound [[Call]]'s argument list
	// is the list-concatenation of the prebound arguments and this
	// call's own (specifications/javascript/spec.html sec-function.prototype.bind,
	// sec-bound-function-exotic-objects-call-thisargument-argumentslist)
	var preboundArguments []*ast.Node
	direct := calleeExpression
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
			if i < len(effective.Knowns) {
				argument = effective.Knowns[i]
			} else {
				argument = silence.Residue()
			}
			// the value and the node it hangs on come from the same
			// position, so a diagnostic never names another argument's
			// expression; a position with no expression behind it (an item
			// expanded out of a spread) hangs on the call
			at := call
			if i < len(effective.Nodes) && effective.Nodes[i] != nil {
				at = effective.Nodes[i]
			}
			CheckAssignability(ctx, argument, *result.Stated, at, "argument", nil)
		}
	} else {
		if !ast.IsIdentifier(calleeExpression) {
			return nil
		}
		found := ctx.P.Checker.GetSymbolAtLocation(calleeExpression)
		if found == nil {
			return nil
		}
		declaration := found.ValueDeclaration
		if declaration == nil || !ast.IsVariableDeclaration(declaration) {
			return nil
		}
		vd := declaration.AsVariableDeclaration()
		isConst := ast.IsVariableDeclarationList(declaration.Parent) &&
			(declaration.Parent.Flags&ast.NodeFlagsConst) != 0
		var initializer *ast.Node
		if isConst && vd.Initializer != nil {
			initializer = vd.Initializer
		} else if pinned := immediatelyPrecedingClosureAssignment(calleeExpression); pinned != nil {
			// a LET name reassigned once, straight-line, immediately before
			// this call — `let call = f; call = (x) => x + 1; call(10)` — is
			// not the general reassignment case InlineStoredClosure declines
			// for everywhere else: the file-wide ReassignedNames flag stays
			// true (it must — a branch-conditional rebind elsewhere in the
			// same file is exactly what the const gate guards against), but
			// THIS call's own immediately-preceding sibling statement pins
			// which literal it holds with no scan past a block boundary, no
			// branch crossed, and no call in between that could rebind it.
			initializer = pinned
		} else {
			return nil
		}
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
	// the prebound arguments occupy the FIRST positions, this call's own
	// the rest — nodes and values grow by the same count, so the pair
	// stays aligned. A prebound position's node is nil: its expression
	// was spelled where the bind ran, not at this call, so nothing here
	// writes back through it (the loop below forgets its roots instead).
	boundOffset := len(preboundKnowns)
	if boundOffset > 0 {
		prefixed := EffectiveArguments{Exact: effective.Exact}
		prefixed.Nodes = append(prefixed.Nodes, make([]*ast.Node, boundOffset)...)
		prefixed.Knowns = append(prefixed.Knowns, preboundKnowns...)
		prefixed.Nodes = append(prefixed.Nodes, effective.Nodes...)
		prefixed.Knowns = append(prefixed.Knowns, effective.Knowns...)
		effective = prefixed
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
		env.Set(name.Text(), BoundParameterKnown(ctx, parameter, i, effective))
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
		entry := BoundParameterKnown(ctx, parameter, i, effective)
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
		// the effective list already carries the prebound prefix, so the
		// parameter's index IS its position: the node written back through
		// is the one whose value bound this parameter
		var argument *ast.Node
		if i < len(effective.Nodes) {
			argument = effective.Nodes[i]
		}
		var restArguments []*ast.Node
		if i >= 0 && i < len(effective.Nodes) {
			restArguments = effective.Nodes[i:]
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

// immediatelyPrecedingClosureAssignment answers the arrow or function
// expression a LET-bound callee identifier was reassigned to, ONE
// statement before the call that reads it — `let call = f; call = (x)
// => x + 1; call(10)` pins the arrow for the THIRD statement's call,
// with no scan past a block boundary and no branch crossed.
//
// The callee's use-site node is what identifies "the call" here: its
// own enclosing statement (climbing through nothing but parenthesized/
// as/satisfies/non-null wrappers and a bare CallExpression) must be a
// direct member of the SAME statement list as the candidate assignment,
// at the very next index — a call nested one level deeper (inside an
// `if`, a block, a callback) does not qualify, because a branch taken
// between the assignment and the call is exactly the case the file-wide
// ReassignedNames gate exists to catch, and this reader must not
// silently re-open it. Nil wherever the immediately preceding statement
// is not that one shape.
func immediatelyPrecedingClosureAssignment(calleeExpression *ast.Node) *ast.Node {
	if calleeExpression == nil || !ast.IsIdentifier(calleeExpression) {
		return nil
	}
	name := calleeExpression.Text()
	statementListParent := func(kind ast.Kind) bool {
		return kind == ast.KindSourceFile || kind == ast.KindBlock ||
			kind == ast.KindModuleBlock || kind == ast.KindCaseClause || kind == ast.KindDefaultClause
	}
	// climb from the identifier to the statement that reads it: a bare
	// `call(10)` as its own ExpressionStatement, or a declaration's
	// initializer (`const ok = call(10)`) — either way the statement is
	// the nearest ancestor that is itself a direct list member
	statement := calleeExpression.Parent
	for statement != nil && statement.Parent != nil && !statementListParent(statement.Parent.Kind) {
		statement = statement.Parent
	}
	if statement == nil || statement.Parent == nil {
		return nil
	}
	// Node.StatementList() reads exactly this NodeList off any of the
	// four statement-list-holding kinds the climb above stopped at
	siblings := statement.Parent.StatementList().Nodes
	index := -1
	for i, sibling := range siblings {
		if sibling == statement {
			index = i
			break
		}
	}
	if index <= 0 {
		return nil
	}
	previous := siblings[index-1]
	if !ast.IsExpressionStatement(previous) {
		return nil
	}
	expression := previous.AsExpressionStatement().Expression
	if !ast.IsBinaryExpression(expression) {
		return nil
	}
	bin := expression.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken || !ast.IsIdentifier(bin.Left) || bin.Left.Text() != name {
		return nil
	}
	right := bin.Right
	for ast.IsParenthesizedExpression(right) {
		right = right.AsParenthesizedExpression().Expression
	}
	if ast.IsArrowFunction(right) || ast.IsFunctionExpression(right) {
		return right
	}
	return nil
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
		// a synthesized slot (a tagged template's template object) names
		// no caller state, so there is nothing to forget behind it
		if argument == nil {
			continue
		}
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
func InlineCallbackNode(ctx *FlowContext, env Env, call *ast.Node, callback Callback, effective EffectiveArguments) abstractdomain.AbstractValue {
	argumentNodes := effective.Nodes
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
		env.Set(name.Text(), BoundParameterKnown(ctx, parameter, i, effective))
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
		if i < len(argumentNodes) {
			argument = argumentNodes[i]
		}
		var restArguments []*ast.Node
		if i < len(argumentNodes) {
			restArguments = argumentNodes[i:]
		}
		WriteBackParameter(ctx, env, writeBackParameterParams{
			parameter:     parameter,
			post:          post,
			entry:         BoundParameterKnown(ctx, parameter, i, effective),
			argument:      argument,
			restArguments: restArguments,
		})
	}
	return result
}

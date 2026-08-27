// Function.prototype.call on a contracted callee with a written
// `this` parameter: `withThis.call({ age: 40 })`. ECMA-262 pins the
// binding (sec-function.prototype.call): the first argument becomes
// the [[Call]]'s thisArgument, and every argument after it becomes
// the positional argument list — `f.call(t, a, b)` runs f with `this`
// bound to t and its own parameters bound to (a, b), exactly as
// `f.apply(t, [a, b])` (apply_call.go, sec-function.prototype.apply)
// and a bound `f.bind(t)(a, b)` (bind_call.go,
// sec-function.prototype.bind) do — the same [[Call]] clause
// (sec-ordinary-function-call) every one of the three ultimately
// reaches. Both siblings build the same (thisArgument, argumentList)
// pair this file binds and hand it to thisParameterCallBind below.
//
// This is a narrow, self-contained inline — not a route through
// InlineContractBody's memoized machinery (that machinery keys off
// CalleeExpressionOf resolving the CALLEE ITSELF to a symbol, which
// `withThis.call` does not: `call` is Function.prototype's own
// method, spelling no user contract of its own). The body walks once
// per call site, silently, on a fresh child environment seeded with
// `this` and the declared parameters, and the caller's own
// environment is left untouched EXCEPT for the epilogue at the end:
// a `this`-member or reference-parameter write lands back on the
// caller's own tracked slots — WriteBackParameter's own rule, applied
// to the this-argument position the same way InlineContractBody's
// epilogue applies it to every ordinary parameter.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// ThisParameterCallResult recognizes `<callee>.call(thisArg, ...rest)`
// where <callee> is an identifier resolving (through ContractOf) to a
// function declaration or function expression carrying its own
// written `this` parameter, and answers the call's value by walking
// the body with `this` bound to thisArg and the declared parameters
// bound to rest — the same binding ECMA-262's
// sec-function.prototype.call pins. Nil wherever the shape does not
// match: a callee `.call` reaches through a symbol with no contract,
// a callee with no `this` parameter (an ordinary `.call` on such a
// function is plain TypeScript, read through the built-in
// Function.prototype.call signature tsc already types), or a callee
// whose body this walk cannot read.
//
// The caller (EvaluateCallExpression) tries this BEFORE ContractOf's
// own resolution of the whole call expression: `withThis.call`
// resolves through the property name `call`, which names
// Function.prototype's own method, not withThis's contract — so the
// ordinary contract lookup has nothing to find for the call AS
// WRITTEN, and this reads the RECEIVER's contract instead.
func ThisParameterCallResult(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	callee := call.Expression
	if !ast.IsPropertyAccessExpression(callee) {
		return nil
	}
	access := callee.AsPropertyAccessExpression()
	methodName := access.Name()
	if methodName == nil || !ast.IsIdentifier(methodName) || methodName.Text() != "call" {
		return nil
	}
	receiver := access.Expression
	if !ast.IsIdentifier(receiver) {
		return nil
	}
	shape := thisParameterCalleeOf(ctx, receiver)
	if shape == nil {
		return nil
	}
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	return thisParameterCallBind(ctx, env, receiver, *shape, arguments)
}

// thisParameterCalleeShape is what every one of .call, .apply, and
// .bind resolves before it can bind anything: the callee's own
// contract, its this-parameter (nil for an ORDINARY function — see
// hasThisParameter), and its ordinary parameters — read once here so
// the three recognizers (this file, apply_call.go, bind_call.go)
// share one resolution and one set of decline rules.
type thisParameterCalleeShape struct {
	contract       *FunctionContract
	thisParameter  *ast.Node // nil when !hasThisParameter
	ordinaryParams []*ast.Node
	body           *ast.Node
	// hasThisParameter is false for a callee with NO written `this`
	// parameter — `.call`/`.apply` still bind ECMA-262's own way (the
	// receiver argument fills sec-ordinarycallbindthis's thisArgument;
	// the REST of the arguments fill the ordinary parameters starting
	// at position 0, not 1). The bound thisArgument is never read back
	// into the body: an arrow target never has its own `this` at all,
	// and any other body that MENTIONS `this` without declaring the
	// parameter is refused, the same rule BoundFunctionOf already
	// applies to `.bind` on an ordinary function.
	hasThisParameter bool
}

// thisParameterCalleeOf resolves an identifier receiver to its
// contract and checks the shape every .call/.apply/.bind recognizer
// needs: a function declaration or expression with a body this walk
// can read, either carrying its own written `this` parameter, or —
// the ordinary-function case — one whose body an arrow-or-no-`this`
// gate clears (BoundFunctionOf's own precedent: `.bind` already reads
// an ordinary function this way, and `.call`/`.apply` reach the SAME
// [[Call]] clause sec-ordinary-function-call pins for all three).
// Nil wherever any of those fails.
func thisParameterCalleeOf(ctx *FlowContext, receiver *ast.Node) *thisParameterCalleeShape {
	if !ast.IsIdentifier(receiver) {
		return nil
	}
	contract := ContractOf(ctx, receiver)
	if contract == nil || contract.Declaration == nil {
		return nil
	}
	if !ast.IsFunctionDeclaration(contract.Declaration) && !ast.IsFunctionExpression(contract.Declaration) {
		return nil
	}
	body := contract.Declaration.Body()
	if body == nil {
		return nil
	}
	declaredParams := contract.Declaration.Parameters()
	if len(declaredParams) > 0 && isThisParameterNode(declaredParams[0]) {
		return &thisParameterCalleeShape{
			contract:         contract,
			thisParameter:    declaredParams[0],
			ordinaryParams:   declaredParams[1:],
			body:             body,
			hasThisParameter: true,
		}
	}
	// no written `this` parameter: sound only where the body never
	// reads `this` (an arrow target has none of its own to read,
	// whatever the caller passes)
	if !ast.IsArrowFunction(contract.Declaration) && MentionsThis(body) {
		return nil
	}
	return &thisParameterCalleeShape{
		contract:         contract,
		thisParameter:    nil,
		ordinaryParams:   declaredParams,
		body:             body,
		hasThisParameter: false,
	}
}

// thisParameterCallBind is the one binding every .call and .bind
// recognizer runs once it has resolved a this-parameter callee and
// its own (thisArg, ...rest) WRITTEN argument list — sec-function.
// prototype.call's own pinned binding (the first value becomes the
// [[Call]]'s thisArgument, the rest becomes the positional argument
// list). Each argument node is evaluated here, in source order,
// through EffectiveArgumentsOf — the discipline every other call site
// in this walk shares.
//
// arguments are the CALLER'S OWN written argument nodes at THIS call
// site, thisArg-first. bind_call.go concatenates the bound partials'
// own nodes ahead of the later call's own nodes before calling here,
// the same list-concatenation sec-bound-function-exotic-objects-
// call-thisargument-argumentslist pins.
func thisParameterCallBind(ctx *FlowContext, env Env, receiver *ast.Node, shape thisParameterCalleeShape, arguments []*ast.Node) *abstractdomain.AbstractValue {
	effective := EffectiveArgumentsOf(arguments, func(argument *ast.Node) abstractdomain.AbstractValue {
		return evaluateExpression(ctx, env, argument)
	})
	return thisParameterCallBindKnown(ctx, env, receiver, shape, effective)
}

// thisParameterCallBindKnown is thisParameterCallBind's own core,
// taking an already-built EffectiveArguments directly — the seam
// apply_call.go uses, since ExactSpreadItems already hands over
// argArray's items as VALUES with no caller expression node behind
// each one (CreateListFromArrayLike reads argArray's own elements,
// not a written argument list), so there is nothing left to evaluate
// through a node.
//
// arguments[0] (thisArg) is used only for the this-argument's own
// diagnostic node and the epilogue's write-back target; a position
// with no caller node behind it (an item .apply expanded out of an
// array, a bound partial from an earlier .bind call) carries nil and
// writes nothing back, the same rule EffectiveArgumentsOf's own nil
// slots already carry.
func thisParameterCallBindKnown(ctx *FlowContext, env Env, receiver *ast.Node, shape thisParameterCalleeShape, effective EffectiveArguments) *abstractdomain.AbstractValue {
	contract := shape.contract
	thisParameter := shape.thisParameter
	ordinaryParams := shape.ordinaryParams
	body := shape.body

	var thisArgument abstractdomain.AbstractValue
	if len(effective.Knowns) > 0 {
		thisArgument = effective.Knowns[0]
	} else {
		// OrdinaryCallBindThis (sec-ordinarycallbindthis): a [[Call]]
		// invoked with no thisArgument at all binds `this` to
		// undefined under strict mode, which every checked
		// this-parameter body runs as (TypeScript's this-parameter
		// typing has no sloppy-mode coercion meaning)
		thisArgument = abstractdomain.Undef
	}
	var thisArgumentNode *ast.Node
	if len(effective.Nodes) > 0 {
		thisArgumentNode = effective.Nodes[0]
	}
	restNodes := effective.Nodes
	restKnowns := effective.Knowns
	if len(restNodes) > 0 {
		restNodes = restNodes[1:]
	}
	if len(restKnowns) > 0 {
		restKnowns = restKnowns[1:]
	}
	restEffective := EffectiveArguments{Nodes: restNodes, Knowns: restKnowns, Exact: effective.Exact}

	// an ORDINARY callee (no written `this` parameter) still consumes
	// the receiver argument positionally (sec-ordinarycallbindthis
	// binds SOME thisArgument whether or not the callee reads it), but
	// there is no this-parameter to meet it against or bind it under —
	// thisParameterCalleeOf already refused any body that reads `this`
	// without declaring the parameter, so leaving "this" unset in
	// callEnv is sound: nothing in the body looks for it.
	var boundThis abstractdomain.AbstractValue
	callEnv := NewEnv()
	if shape.hasThisParameter {
		// the THIS-PARAMETER's own declared type is the ceiling the same
		// way an ordinary parameter's is (entryStateMeet) — the caller's
		// exact value passes through whole where it fits, and only a
		// claim the declaration cannot carry (an open-map completeness
		// flag) is stripped
		boundThis = entryStateMeet(ctx.P.Checker, thisParameter, thisArgument, InitialStateOfPlainParameter(ctx.P, thisParameter))
		callEnv.Set("this", boundThis)
	}
	for i, parameter := range ordinaryParams {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		// the same declared-type meet InlineContractBody's own callEnv
		// binds every parameter through
		callEnv.Set(name.Text(), BoundParameterKnown(ctx, parameter, i, restEffective))
	}

	// obligations check against the CALLER's own arguments, the same
	// way a direct call's do — position 0 (thisArg) checks against
	// the this-parameter's own statement WHEN one is declared, positions
	// 1.. (0.. for an ordinary callee, which reads no thisArg at all)
	// against the ordinary parameters, both through the shared
	// CheckAssignability door CheckContractArguments already opens for
	// a direct call
	if shape.hasThisParameter {
		checkThisParameterArgument(ctx, thisParameter, thisArgument, thisArgumentNode)
	}
	checkOrdinaryArguments(ctx, ordinaryParams, restEffective)

	// RECURSION GUARD: a this-parameter body that calls back into
	// itself through .call/.apply/.bind (directly or through another
	// such indirection one of these three recognizers would match
	// again) re-enters this function with the same symbol still
	// marked — the same re-entry-is-recursion discipline
	// InlineContractBody's own Inlining set enforces, so this walk
	// terminates rather than recursing on the checker's own call
	// stack.
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := ctx.P.Checker.GetSymbolAtLocation(receiver)
	if symbol != nil {
		inlining := ctx.Inlining
		if inlining == nil {
			inlining = map[*ast.Symbol]struct{}{}
		}
		if _, ok := inlining[symbol]; ok {
			out := RecursionMarker(ctx, symbol, contract.Declaration)
			return &out
		}
		inlining[symbol] = struct{}{}
		defer delete(inlining, symbol)
	}

	var sink []abstractdomain.AbstractValue
	silent := *ctx
	silent.Report = func(assignability.RefinementDiagnostic) {}
	silent.ReturnSink = &sink
	silent.Declared = map[string]*annotations.DeclaredRefinement{}
	if symbol != nil {
		if silent.Inlining == nil {
			silent.Inlining = map[*ast.Symbol]struct{}{}
		}
		silent.Inlining[symbol] = struct{}{}
	}
	if ast.IsBlock(body) {
		AnalyzeStatements(&silent, callEnv, body.AsBlock().Statements.Nodes, nil)
	} else {
		sink = append(sink, evaluateExpression(&silent, callEnv, body))
	}

	// THE WRITE-BACK EPILOGUE: a written this-member or reference
	// parameter leaves the caller's tracked object stale otherwise —
	// InlineContractBody's own epilogue (inline_contract_body.go) is
	// the template, applied here to the shifted position list a
	// .call/.apply/.bind indirection reads through. bodyWrites is the
	// same body-mutation fact InlineContractBody itself reads
	// (BodyWritesOf, callee_effects.go); a name the body only
	// NARROWS carries nothing back.
	bodyWrites := BodyWritesOf(ctx, contract.Declaration)
	post := func(name string) abstractdomain.AbstractValue {
		held, ok := callEnv.Get(name)
		if !ok {
			return silence.Residue()
		}
		return held
	}
	if shape.hasThisParameter {
		if _, writes := bodyWrites["this"]; writes && thisArgumentNode != nil {
			WriteBackParameter(ctx, env, writeBackParameterParams{
				parameter: thisParameter,
				post:      post("this"),
				entry:     boundThis,
				argument:  thisArgumentNode,
			})
		}
	}
	for i, parameter := range ordinaryParams {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		if _, writes := bodyWrites[name.Text()]; !writes {
			continue
		}
		if i >= len(restEffective.Nodes) {
			continue
		}
		argument := restEffective.Nodes[i]
		if argument == nil {
			continue
		}
		WriteBackParameter(ctx, env, writeBackParameterParams{
			parameter:     parameter,
			post:          post(name.Text()),
			entry:         BoundParameterKnown(ctx, parameter, i, restEffective),
			argument:      argument,
			restArguments: restEffective.Nodes[i:],
		})
	}

	returned := silence.Residue()
	if len(sink) > 0 {
		returned = JoinSinkSummarized(sink)
	}
	var statedResult *abstractdomain.AbstractValue
	if contract.Result != nil && contract.Result.Kind != annotations.DeclaredVariable {
		v := AbstractValueOfDeclared(*contract.Result)
		statedResult = &v
	}
	if statedResult == nil {
		return &returned
	}
	met := abstractdomain.MeetKnown(returned, *statedResult)
	return &met
}

// isThisParameterNode: a parameter declaration whose name is
// literally the identifier "this" — TypeScript's own this-parameter
// syntax marks it this way (dataflowfacts.EnclosingThisParameterFunction
// reads the identical shape from the other direction, climbing UP
// from a `this` keyword site rather than reading a known
// declaration's own first parameter).
func isThisParameterNode(parameter *ast.Node) bool {
	name := parameter.AsParameterDeclaration().Name()
	return name != nil && ast.IsIdentifier(name) && name.Text() == "this"
}

// checkThisParameterArgument checks the call's own first argument
// (thisArg) against the this-parameter's stated type, at the
// argument's own node when one was written — a `.call()` with no
// arguments at all, or a this-argument .apply/.bind expanded with no
// caller expression behind it, has no node to hang the diagnostic on,
// and nothing the caller wrote to check either.
func checkThisParameterArgument(ctx *FlowContext, thisParameter *ast.Node, thisArgument abstractdomain.AbstractValue, thisArgumentNode *ast.Node) {
	pd := thisParameter.AsParameterDeclaration()
	if pd.Type == nil || thisArgumentNode == nil {
		return
	}
	result := annotations.AnnotationOfType(ctx.P, pd.Type, ctx.Registry, ctx.Objects)
	if result.Stated == nil {
		return
	}
	CheckAssignability(ctx, thisArgument, *result.Stated, thisArgumentNode, "the bound receiver", nil)
}

// checkOrdinaryArguments checks the call's arguments PAST the
// this-argument against the callee's own declared parameters, one
// position per ordinary parameter — the same door
// CheckContractArguments opens for a direct call, applied to the
// shifted position list a `.call()` indirection reads through.
func checkOrdinaryArguments(ctx *FlowContext, ordinaryParams []*ast.Node, effective EffectiveArguments) {
	argumentNodes := effective.Nodes
	argKnowns := effective.Knowns
	for i, parameter := range ordinaryParams {
		if i >= len(argKnowns) || i >= len(argumentNodes) {
			break
		}
		argument := argumentNodes[i]
		if argument == nil {
			continue
		}
		pd := parameter.AsParameterDeclaration()
		if pd.Type == nil {
			continue
		}
		result := annotations.AnnotationOfType(ctx.P, pd.Type, ctx.Registry, ctx.Objects)
		if result.Stated == nil {
			continue
		}
		CheckAssignability(ctx, argKnowns[i], *result.Stated, argument, "argument", nil)
	}
}

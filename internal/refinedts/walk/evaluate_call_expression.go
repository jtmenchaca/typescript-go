// from evaluation/evaluate_call_expression.ts
//
// A call expression: snapshot the site, expand spreads, try builtin
// models and stated contracts, inline a body in reach, then fall
// through to what an unmodeled call still wears.
//
// BLOCKED (one call site only): recordCallSnapshot
// (control_flow/call_site_snapshots.ts) is a later control_flow stage
// not yet landed — called here by its TS name as a plain package
// function per PORT.md's cross-file convention; this file will not
// build until that stage lands.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// EvaluateCallExpression is evaluateCallExpression in the TS source.
func EvaluateCallExpression(ctx *FlowContext, env Env, e *ast.Node) abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	// the read-once snapshot: under an owner walk, the state a call's
	// arguments evaluate in is recorded once here, and the call-site
	// joins and callback seeding read it instead of re-walking the
	// enclosing function per query (call_site_snapshots.ts). Only the
	// QUERYABLE shapes record — a direct call of a name (the
	// call-site join asks those) or a call handing a function literal
	// (callback seeding asks those) — so the fast corpus never pays
	// for snapshots nothing will read.
	if ctx.SnapshotOwner != nil {
		queryable := ast.IsIdentifier(call.Expression)
		if !queryable {
			for _, argument := range arguments {
				if ast.IsArrowFunction(argument) || ast.IsFunctionExpression(argument) {
					queryable = true
					break
				}
			}
		}
		if queryable {
			RecordCallSnapshot(ctx.P, ctx.SnapshotOwner, e, env)
		}
	}
	// Function.prototype.call/.apply/.bind on a contracted callee with
	// its own written `this` parameter: `withThis.call(t, ...rest)`,
	// `withThis.apply(t, [...])`, and a direct or const-stored
	// `withThis.bind(t, ...)(...)` all reach here BEFORE the builtin
	// dispatcher below, because that dispatcher's own method-call
	// chain gates only on ContractOf finding no contract for the
	// CALLEE EXPRESSION AS WRITTEN (`withThis.call`, whose property
	// name `call` resolves to Function.prototype's own library
	// method, never a user contract) — so it WOULD enter its
	// always-answering unmodeled-method fallback for exactly these
	// shapes unless one of these three recognizers claims the call
	// first. Each reads the RECEIVER's own contract instead of the
	// call expression's, and walks arguments as part of its own
	// binding, so a match returns directly rather than falling
	// through to any later evaluation of them.
	if thisCallResult := ThisParameterCallResult(ctx, env, e); thisCallResult != nil {
		return *thisCallResult
	}
	if applyCallResult := ApplyCallResult(ctx, env, e); applyCallResult != nil {
		return *applyCallResult
	}
	if bindCallResult := BindCallResult(ctx, env, e); bindCallResult != nil {
		return *bindCallResult
	}
	// a SPREAD argument expands its exact sequence into positional
	// arguments — Math.max(...values) reads every element. The builtin
	// models want the VALUES alone, so they read the effective list's
	// knowns; the inline path below wants the values placed against
	// their nodes and reads the whole effective list.
	spreadArguments := func(args []*ast.Node) []abstractdomain.AbstractValue {
		return EffectiveArgumentsOf(args, func(argument *ast.Node) abstractdomain.AbstractValue {
			return evaluateExpression(ctx, env, argument)
		}).Knowns
	}
	if builtin := ReadBuiltinCall(ctx, env, e, spreadArguments); builtin != nil {
		// a builtin path that determined NOTHING may still wear the
		// call's resolved return annotation — the Map.get shape: the
		// model havocs and answers unknown, possibly maybe-wrapped,
		// but the declaration's stated set holds of whatever comes
		// back, wherever it came from
		bare := *builtin
		if bare.Kind == abstractdomain.KindPossiblyUndefined {
			bare = *bare.Inner
		}
		if bare.Kind == abstractdomain.KindUnknown {
			worn := AnnotationOfReturnType(ctx, e)
			if worn == nil {
				worn = MapValueAnnotation(ctx, e)
			}
			if worn != nil {
				return *worn
			}
		}
		return *builtin
	}
	// a GENERATOR call: the callee's body does NOT run here. The call
	// builds a Generator object, and the body resumes only when
	// something drains it. So the inline route below must not have it —
	// the body's `return e` is a value the caller never sees, and
	// answering it for the call would be a wrong answer, not a missing
	// one. The call ADMITS as the opaque tier (a value from outside this
	// walk's determination), which is what keeps the decline off the
	// CALLING body: the caller keeps walking, and the routes that CAN
	// say something about the generator — `.next()`, `[...g()]`,
	// `Array.from(g())` — read its element through GeneratorElementOf
	// on top of this admission.
	if generated := GeneratorCallResult(ctx, env, e); generated != nil {
		// the arguments still evaluate: their own effects happen at the
		// call whether or not the body runs
		for _, argument := range arguments {
			evaluateExpression(ctx, env, argument)
		}
		return *generated
	}
	contract := ContractOf(ctx, call.Expression)
	// a contracted METHOD call reaches here: its receiver expression
	// still runs (a call result, a constructor) — walk it for its
	// effects before the arguments
	if contract != nil && ast.IsPropertyAccessExpression(call.Expression) {
		evaluateExpression(ctx, env, call.Expression.AsPropertyAccessExpression().Expression)
	}
	// the arguments as the POSITIONS they occupy: every argument
	// expression is evaluated once here, in source order, and a spread
	// whose source is an exact sequence contributes one position per
	// item. Nodes and values are built together, so every reader below
	// that maps a parameter index to an argument node lands on the node
	// whose value bound that parameter.
	effective := EffectiveArgumentsOf(arguments, func(argument *ast.Node) abstractdomain.AbstractValue {
		return evaluateExpression(ctx, env, argument)
	})
	argKnowns := effective.Knowns
	if contract == nil {
		// `super.m(x)` and a derived constructor's `super(x)` name no
		// symbol ContractOf can follow, so the contract above is nil for
		// them; super_binding.go walks the enclosing class's heritage to
		// the base declaration the call runs, and its stated positions
		// hold of these arguments exactly as any other call's do. The
		// resolved contract stands in for the rest of this function: the
		// obligations below, and the inline route further down.
		//
		// The receiver of such a call is the caller's own `this` — the
		// dispatch to the base member is static, the instance is not
		// re-bound — so the two seams the inline route reads a receiver
		// through both answer for it: SummaryCallReceiver reads the
		// tracked `this` entry, and the served-call forget fires
		// ForgetThisHeld. A super call is a plain call shape besides —
		// no template-object slot — so the effective list above is the
		// one it binds.
		if superContract := SuperCallContract(ctx, call.Expression); superContract != nil {
			contract = superContract
		}
	}
	if contract != nil {
		CheckContractArguments(ctx, contract, effective)
	}
	// a call through a PARAMETER the caller bound to a function
	// literal: the very callback runs here, inlined
	if contract == nil && ast.IsIdentifier(call.Expression) && ctx.CallableParams != nil {
		if callback, ok := ctx.CallableParams[call.Expression.Text()]; ok {
			return InlineCallbackNode(ctx, env, e, callback, effective)
		}
	}
	// a const-bound closure called by name runs HERE, synchronously —
	// so it is walked here, on this environment: captures read the
	// current facts, writes to outer names land, and the returned
	// value is the join of what the body returns
	if contract == nil {
		if inlined := InlineStoredClosure(ctx, env, e, effective); inlined != nil {
			return *inlined
		}
	}
	// a function HANDED OVER may also be stored and run later — its
	// writes to names this scope tracks can land at any time, so
	// those names forget at the hand-over. The scan reaches arrows
	// NESTED inside object and array literal arguments too: an
	// esbuild-style `{ plugins: [{ setup(b) { … } }] }` mutates a
	// closed-over Set through a hook the top-level scan never saw
	// (prisma's disallowed-imports guard folded dead on it)
	for _, argument := range arguments {
		var handedFunctions []*ast.Node
		var collectHanded func(node *ast.Node)
		collectHanded = func(node *ast.Node) {
			if ast.IsArrowFunction(node) || ast.IsFunctionExpression(node) || ast.IsMethodDeclaration(node) {
				handedFunctions = append(handedFunctions, node)
				return // its own nested functions are inside it already
			}
			// stop at nested CALLS: their arguments are their own
			// hand-overs, handled where those calls evaluate
			if ast.IsCallExpression(node) && node != argument {
				return
			}
			node.ForEachChild(func(child *ast.Node) bool {
				collectHanded(child)
				return false
			})
		}
		collectHanded(argument)
		for _, handed := range handedFunctions {
			written := map[string]struct{}{}
			AssignedNames(ctx.P.Checker, handed, written)
			for name := range written {
				if _, ok := env.Get(name); ok {
					HavocEnv(ctx.Aliases, env, name)
					continue
				}
				// an UNTRACKED written root (a class name whose static
				// place entries live under dotted keys) sweeps its entries
				// — the handed function may run at any later time
				ForgetPlaceEntriesEnv(env, name)
			}
		}
	}
	// a callee WITH a body runs inline: its effects on reference
	// arguments, aliases, and closed-over names apply precisely, and
	// the unstated (or variable-stated) result is recovered from what
	// the body returns. An EFFECT-FREE callee (the summary proves the
	// body writes nothing and calls nothing that could) skips the
	// inline whole: there are no effects to apply, and the result is
	// the stated set or a memoized recovery.
	if contract != nil && contract.Declaration.Body() != nil {
		// the stated result is the contract; the recovered value rode
		// the CALLER's knowledge through the body. Both hold of the
		// same value, so the call wears their MEET — a stated result
		// no longer discards what the arguments made provable.
		var statedResult *abstractdomain.AbstractValue
		if contract.Result != nil && contract.Result.Kind != annotations.DeclaredVariable {
			v := AbstractValueOfDeclared(*contract.Result)
			statedResult = &v
		}
		summary := Summarize(ctx, *contract)
		// a SUPER-rooted callee takes the full inline whether or not the
		// body is self-contained: RecoverPure reads the callee's symbol off
		// the call's own callee name, which `super.m` and a bare `super(…)`
		// do not spell, so the recovery would answer residue where the
		// opaque reading stood. The inline route reads the declaration's
		// own name instead.
		superRooted := SuperCalleeRoot(call.Expression) != nil
		if summary.EffectFree {
			tracing.Count("inlineSkipped", 0)
			if summary.SelfContained && !superRooted {
				recovered := RecoverPure(ctx, e, *contract, effective, true)
				if statedResult != nil {
					recovered = abstractdomain.MeetKnown(recovered, *statedResult)
				}
				return wornReturnTypeIfUnknown(ctx, e, recovered)
			}
			// effect-free but reads the caller's world: fall through to
			// the full inline, whose copied environment keeps captures
		} else if summary.HasConstantWrites {
			// the transfer applies the body's writes without re-walking
			tracing.Count("summaryApplied", 0)
			ApplyConstantWrites(ctx, env, effective, summary.ConstantWrites)
			if statedResult != nil {
				return *statedResult
			}
			return wornReturnTypeIfUnknown(ctx, e, silence.Residue())
		}
		recovered := InlineContractCall(ctx, env, e, contract, effective)
		if statedResult != nil {
			recovered = abstractdomain.MeetKnown(recovered, *statedResult)
		}
		return wornReturnTypeIfUnknown(ctx, e, recovered)
	}
	// a body-less callee (an ambient declaration): reference
	// arguments forget — the implementation is outside the checked
	// world — and a stated result earns no knowledge
	for _, argument := range arguments {
		if ast.IsIdentifier(argument) {
			if _, ok := env.Get(argument.Text()); ok && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
				HavocEnv(ctx.Aliases, env, argument.Text())
			}
		}
	}
	// …EXCEPT a generic whose result IS the variable: `identity<T>`
	// or `first<T>(xs: T[]): T` links inputs to output by the type
	// alone — the only T values a parametric implementation can hand
	// back are ones it received. The claim is the .d.ts's, so it
	// carries LIBRARY grade in the ledger; a refutation through it
	// is still a refutation.
	if contract != nil && contract.Result != nil && contract.Result.Kind == annotations.DeclaredVariable && contract.Result.StarDepth == 0 {
		resultSymbol := contract.Result.Symbol
		var joined *abstractdomain.AbstractValue
		complete := true
		// the positions, not the written arguments: a stated position is
		// matched against the value that really bound it
		for i := 0; i < len(argKnowns) && complete; i++ {
			var stated *annotations.DeclaredRefinement
			if i < len(contract.Params) {
				stated = contract.Params[i]
			}
			if stated == nil || stated.Kind != annotations.DeclaredVariable || stated.Symbol != resultSymbol {
				continue
			}
			var piece *abstractdomain.AbstractValue
			if stated.StarDepth == 0 {
				piece = &argKnowns[i]
			} else if stated.StarDepth == 1 {
				piece = ElementJoinOf(argKnowns[i])
			}
			if piece == nil || piece.Kind == abstractdomain.KindUnknown {
				complete = false
				break
			}
			if joined == nil {
				joined = piece
			} else {
				j := abstractdomain.JoinKnown(*joined, *piece)
				joined = &j
			}
		}
		if complete && joined != nil && joined.Kind != abstractdomain.KindUnknown {
			return abstractdomain.AtTrustLevel(*joined, abstractdomain.TrustLibrary)
		}
	}
	return UnmodeledCallResult(ctx, env, e)
}

// wornReturnTypeIfUnknown: an inline or recovery answer that
// determined NOTHING still wears the call's declared return ground.
// The callee's body is in reach on every path that lands here, so the
// declared return type is a claim tsc itself checked that body
// against — the same standing unmodeled_call_result.go's own
// body-in-reach arm rests on — and a weaker true claim beats the
// unknown. The stated-annotation reading runs first (the Map.get
// precedent above), the sort ground second. An answer that
// determined SOMETHING rides through untouched.
//
// An UNINSTANTIATED type variable counts as determining nothing: a
// generic callee's stated `T | undefined` meets the recovery as a
// bare variable, but no checked position can relate that T to its
// own stated set — the call site's RESOLVED type IS the
// instantiation (recharts' useAppSelector<T> handing back
// `PolarLayout | undefined` at one site and `LayoutType | undefined`
// at another), so the ground outranks the variable.
func wornReturnTypeIfUnknown(ctx *FlowContext, e *ast.Node, result abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	bare := result
	if bare.Kind == abstractdomain.KindPossiblyUndefined {
		bare = *bare.Inner
	}
	if bare.Kind != abstractdomain.KindUnknown && bare.Kind != abstractdomain.KindVariable {
		return result
	}
	if worn := AnnotationOfReturnType(ctx, e); worn != nil {
		return *worn
	}
	if ground := ReturnTypeGround(ctx, e); ground != nil {
		return *ground
	}
	return result
}

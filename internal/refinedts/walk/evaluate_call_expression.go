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
	// a SPREAD argument expands its exact sequence into positional
	// arguments — Math.max(...values) reads every element
	spreadArguments := func(args []*ast.Node) []abstractdomain.AbstractValue {
		var out []abstractdomain.AbstractValue
		for _, argument := range args {
			if ast.IsSpreadElement(argument) {
				spread := evaluateExpression(ctx, env, argument.AsSpreadElement().Expression)
				if spread.Kind == abstractdomain.KindValues && spread.KindTag == abstractdomain.PrimitiveArray {
					for _, v := range spread.Values {
						out = append(out, abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(spread)))
					}
					continue
				}
				if spread.Kind == abstractdomain.KindList {
					out = append(out, spread.Items...)
					continue
				}
				out = append(out, silence.Residue()) // an inexpansible spread loses its slots
				continue
			}
			out = append(out, evaluateExpression(ctx, env, argument))
		}
		return out
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
	contract := ContractOf(ctx, call.Expression)
	// a contracted METHOD call reaches here: its receiver expression
	// still runs (a call result, a constructor) — walk it for its
	// effects before the arguments
	if contract != nil && ast.IsPropertyAccessExpression(call.Expression) {
		evaluateExpression(ctx, env, call.Expression.AsPropertyAccessExpression().Expression)
	}
	argKnowns := make([]abstractdomain.AbstractValue, len(arguments))
	for i, argument := range arguments {
		argKnowns[i] = evaluateExpression(ctx, env, argument)
	}
	if contract != nil {
		CheckContractArguments(ctx, e, contract, argKnowns)
	}
	// a call through a PARAMETER the caller bound to a function
	// literal: the very callback runs here, inlined
	if contract == nil && ast.IsIdentifier(call.Expression) && ctx.CallableParams != nil {
		if callback, ok := ctx.CallableParams[call.Expression.Text()]; ok {
			return InlineCallbackNode(ctx, env, e, callback, argKnowns)
		}
	}
	// a const-bound closure called by name runs HERE, synchronously —
	// so it is walked here, on this environment: captures read the
	// current facts, writes to outer names land, and the returned
	// value is the join of what the body returns
	if contract == nil {
		if inlined := InlineStoredClosure(ctx, env, e, argKnowns); inlined != nil {
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
				}
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
		if summary.EffectFree {
			tracing.Count("inlineSkipped", 0)
			if summary.SelfContained {
				recovered := RecoverPure(ctx, e, *contract, argKnowns, true)
				if statedResult == nil {
					return recovered
				}
				return abstractdomain.MeetKnown(recovered, *statedResult)
			}
			// effect-free but reads the caller's world: fall through to
			// the full inline, whose copied environment keeps captures
		} else if summary.HasConstantWrites {
			// the transfer applies the body's writes without re-walking
			tracing.Count("summaryApplied", 0)
			ApplyConstantWrites(ctx, env, e, summary.ConstantWrites)
			if statedResult != nil {
				return *statedResult
			}
			return silence.Residue()
		}
		recovered := InlineContractCall(ctx, env, e, contract, argKnowns)
		if statedResult == nil {
			return recovered
		}
		return abstractdomain.MeetKnown(recovered, *statedResult)
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
		for i := 0; i < len(arguments) && complete; i++ {
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

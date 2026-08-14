// from interprocedural/inline_contract_body.ts
//
// The body walk behind an inlined contract call: shadow the callee's
// names, run the body silently, write mutations back, and remember
// the outcome for identical observed states. Recursion answers with
// a marker after forgetting reference arguments.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// SummaryCallReceiver is the abstract value a METHOD call's receiver
// holds in the CALLER's environment — what the summary route fills a
// method's this-field entries from.
//
// `a.m(x)` reads a; `this.m(x)` reads the tracked `this`. The read runs
// on the caller's own env, which is where the receiver's field knowledge
// lives, and it happens BEFORE the callee's body is walked, so what it
// answers is the state at the call.
//
// It runs only where the receiver READS WITHOUT EFFECT — a name, `this`,
// or a chain of names. A receiver that is a call result, a constructor,
// or anything else that runs would be evaluated a SECOND time here
// (evaluate_call_expression already walked it for its effects), and a
// second evaluation would double whatever it did. Those answer silence,
// which fills every this-entry TOP — a loss of precision, never of
// soundness, since the entries are what the summary quantifies over.
func SummaryCallReceiver(ctx *FlowContext, env Env, callExpr *ast.CallExpression) abstractdomain.AbstractValue {
	if callExpr == nil || !ast.IsPropertyAccessExpression(callExpr.Expression) {
		return silence.Residue()
	}
	receiver := callExpr.Expression.AsPropertyAccessExpression().Expression
	if receiver == nil || !ReadsWithoutEffect(receiver) {
		return silence.Residue()
	}
	return evaluateExpression(ctx, env, receiver)
}

// InlineContractBody is inlineContractBody in the TS source.
func InlineContractBody(ctx *FlowContext, env Env, call *ast.Node, contract *FunctionContract, argKnowns []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	tracing.Count("inlineContractCall", 0)
	body := contract.Declaration.Body()
	if body == nil {
		return silence.Residue()
	}
	callExpr := call.AsCallExpression()
	var calleeName *ast.Node
	if ast.IsIdentifier(callExpr.Expression) {
		calleeName = callExpr.Expression
	} else if ast.IsPropertyAccessExpression(callExpr.Expression) {
		calleeName = callExpr.Expression.AsPropertyAccessExpression().Name()
	}
	if calleeName == nil {
		return silence.Residue()
	}
	// the amplifier ledger: which callees the inline count concentrates
	// on, so a slow file names its multiplier instead of a total
	if tracing.Recording(tracing.GrainStep) {
		tracing.Count("inline."+calleeName.Text(), 0)
	}
	symbol := ctx.P.Checker.GetSymbolAtLocation(calleeName)
	if symbol == nil {
		return silence.Residue()
	}
	inlining := ctx.Inlining
	if inlining == nil {
		inlining = map[*ast.Symbol]struct{}{}
	}
	if _, ok := inlining[symbol]; ok {
		// a RECURSIVE call's effects still happen at runtime: a
		// reference argument may be mutated and a closed-over name may
		// be written, so both forget before the marker returns —
		// tailwindcss's `transform(child, copy.nodes, …)` left
		// `copy.nodes` frozen at [] and a live length guard folded dead
		for _, argument := range callExpr.Arguments.Nodes {
			if ast.IsIdentifier(argument) {
				if _, ok := env.Get(argument.Text()); ok && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
					HavocEnv(ctx.Aliases, env, argument.Text())
					continue
				}
			}
			if dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
				// a projection argument (`copy.children`) mutates through
				// its holder — the holder forgets
				ForgetThrough(ctx, env, argument)
			}
		}
		written := map[string]struct{}{}
		AssignedNames(ctx.P.Checker, contract.Declaration, written)
		for name := range written {
			if _, ok := env.Get(name); ok {
				HavocEnv(ctx.Aliases, env, name)
			}
		}
		return RecursionMarker(symbol)
	}

	// the DETERMINISTIC replay: identical key, identical walk,
	// identical outcome. A callback argument's behavior is fixed by
	// its CALL NODE, so the node rides in the key and repeat walks of
	// one registration site replay instead of re-walking (tailwindcss
	// utilities.ts walked staticUtility 1,499 times per site with the
	// old blanket exclusion). The callback's body reads the caller's
	// world directly when the callee invokes it, so its observed names
	// join the callee's in the key. Knowledge carrying compiler
	// objects still has no plain spelling and runs unmemoized rather
	// than mis-keyed.
	memoKey := computeInlineMemoKey(ctx, env, call, callExpr, contract, calleeName, argKnowns)
	if memoKey != "" {
		inlineMemoMu.Lock()
		byKey := inlineMemoOf(ctx.P)[contract.Declaration]
		_, memoSeen := byKey[memoKey]
		inlineMemoMu.Unlock()
		if tracing.Recording(tracing.GrainStep) && !memoSeen {
			tracing.Count("inline.freshkey."+calleeName.Text(), 0)
		}
	}
	if memoKey != "" {
		inlineMemoMu.Lock()
		held, ok := inlineMemoOf(ctx.P)[contract.Declaration][memoKey]
		inlineMemoMu.Unlock()
		if ok {
			tracing.Count("inlineMemoHit", 0)
			return ReplayInline(ctx, env, call, *contract, argKnowns, held)
		}
	}
	// a FRESH key tries the kernel-summary route before walking: a
	// lowerable body answers by APPLYING its compiled summary to this
	// call's entry states — the body's IR crossed the wire once, when
	// the declaration's summary was compiled — remembered under the
	// same key so repeats replay without re-asking. Ordered AFTER the
	// memo hit — a replay is cheaper than a kernel ask — and only
	// here, so the route pays exactly once per distinct state.
	// The summary's admitted bodies have no caller-visible effect
	// beyond the return (KernelSummaryDirect's comment carries the
	// argument), so the remembered outcome carries no posts.
	if summarized, ok := KernelSummaryDirectOn(ctx, argKnowns, contract, SummaryCallReceiver(ctx, env, callExpr)); ok {
		tracing.Count("inline.summaryDirect", 0)
		// THE SERVED-CALL FORGET: a summary whose body writes receiver
		// fields, writes a parameter bundle's fields, or returns its
		// receiver moves object knowledge the caller holds — the same
		// knowledge the OPAQUE path forgets through ForgetThrough. A
		// served answer forgets exactly the same way, and such calls are
		// never memoized: a replay would skip the forget.
		if receiverTouched, writtenArguments := SummaryReceiverEffects(ctx, contract.Declaration); receiverTouched || len(writtenArguments) > 0 {
			if receiverTouched && ast.IsPropertyAccessExpression(callExpr.Expression) {
				ForgetThrough(ctx, env, callExpr.Expression.AsPropertyAccessExpression().Expression)
			}
			var callArguments []*ast.Node
			if callExpr.Arguments != nil {
				callArguments = callExpr.Arguments.Nodes
			}
			for _, index := range writtenArguments {
				if index < len(callArguments) {
					ForgetThrough(ctx, env, callArguments[index])
				}
			}
			return AsCalleeResult(*contract, summarized)
		}
		if memoKey != "" {
			inlineMemoMu.Lock()
			memo := inlineMemoOf(ctx.P)[contract.Declaration]
			if memo == nil {
				memo = map[string]InlineOutcome{}
				inlineMemoOf(ctx.P)[contract.Declaration] = memo
			}
			if len(memo) >= callResultsKept {
				for k := range memo {
					delete(memo, k)
					break
				}
			}
			memo[memoKey] = InlineOutcome{Returned: summarized}
			inlineMemoMu.Unlock()
		}
		return AsCalleeResult(*contract, summarized)
	}
	inlining[symbol] = struct{}{}

	// the callee's own names shadow the caller's
	shadowed := map[string]struct{}{}
	for _, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if ast.IsIdentifier(name) {
			shadowed[name.Text()] = struct{}{}
		}
	}
	declaredNames(body, shadowed)

	callEnv := NewEnv()
	env.Range(func(k string, v abstractdomain.AbstractValue) bool {
		if _, isShadowed := shadowed[k]; !isShadowed {
			callEnv.Set(k, v)
		}
		return true
	})
	callableParams := map[string]Callback{}
	for i, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		callEnv.Set(name.Text(), ParameterKnown(parameter, i, call, argKnowns))
		if i < len(callExpr.Arguments.Nodes) {
			argument := callExpr.Arguments.Nodes[i]
			if ast.IsArrowFunction(argument) || ast.IsFunctionExpression(argument) {
				callableParams[name.Text()] = argument
			}
		}
	}
	var sink []abstractdomain.AbstractValue
	var thrown []Env
	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}
	silent.ReturnSink = &sink
	silent.ThrowSink = &thrown
	silent.CallableParams = callableParams
	silent.Inlining = inlining
	silent.Declared = map[string]*annotations.DeclaredRefinement{}
	dropsBefore := MarkerDropCount()
	if ast.IsBlock(body) {
		AnalyzeStatements(&silent, callEnv, body.AsBlock().Statements.Nodes, nil)
	} else {
		sink = append(sink, evaluateExpression(&silent, callEnv, body))
	}
	delete(inlining, symbol)

	// what the caller observes afterwards: the normal exit joined with
	// every exceptional one
	post := func(name string) abstractdomain.AbstractValue {
		held, ok := callEnv.Get(name)
		if !ok {
			held = silence.Residue()
		}
		for _, snapshot := range thrown {
			snapshotValue, hasSnapshot := snapshot.Get(name)
			if !hasSnapshot {
				snapshotValue = silence.Residue()
			}
			held = abstractdomain.JoinKnown(held, snapshotValue)
		}
		return held
	}
	bodyWrites := BodyWritesOf(ctx, contract.Declaration)
	var postByName []postByNameEntry
	// Range walks a snapshot, so the UpdateTrackedEnv writes below —
	// which touch the very environment being visited — cannot disturb
	// the visit.
	env.Range(func(name string, before abstractdomain.AbstractValue) bool {
		if _, isShadowed := shadowed[name]; isShadowed {
			return true
		}
		// narrowing-only names keep the caller's state — only a name
		// the body may WRITE carries its exit state back
		if _, writes := bodyWrites[name]; !writes {
			return true
		}
		after := post(name)
		if !abstractdomain.SameKnown(after, before) {
			postByName = append(postByName, postByNameEntry{Name: name, After: after})
			UpdateTrackedEnv(ctx.Aliases, env, name, after)
		}
		return true
	})
	// a parameter IS its argument: the parameter's final state lands on
	// the identifier argument's name (a changed projection argument
	// makes its holder forget instead)
	var paramPosts []paramPost
	captured := CapturedOf(contract.Declaration)
	for i, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			paramPosts = append(paramPosts, paramPost{})
			continue
		}
		paramPosts = append(paramPosts, paramPost{Value: post(name.Text()), Has: true})
		if i >= len(callExpr.Arguments.Nodes) {
			continue
		}
		argument := callExpr.Arguments.Nodes[i]
		// a parameter CAPTURED into a nested function value may be
		// mutated whenever that closure later runs — the argument's
		// facts forget instead of restating the inline's entry state
		if _, isCaptured := captured[name.Text()]; isCaptured && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
			if ast.IsIdentifier(argument) {
				if _, ok := env.Get(argument.Text()); ok {
					HavocEnv(ctx.Aliases, env, argument.Text())
					continue
				}
			}
			ForgetThrough(ctx, env, argument)
			continue
		}
		// a parameter the body only NARROWS carries nothing back — its
		// exit state holds along the fall-through path, not the call
		if _, writes := bodyWrites[name.Text()]; !writes {
			continue
		}
		WriteBackParameter(ctx, env, writeBackParameterParams{
			parameter:     parameter,
			post:          post(name.Text()),
			entry:         ParameterKnown(parameter, i, call, argKnowns),
			argument:      argument,
			restArguments: callExpr.Arguments.Nodes[i:],
		})
	}
	summarized := silence.Residue()
	if len(sink) > 0 {
		summarized = JoinSinkSummarized(sink)
	}
	returned := summarized
	if MarkerKey(summarized) == symbol {
		returned = silence.Residue()
	}
	// an answer leaning on a recursion induction holds only inside it —
	// never remembered; everything else replays for identical keys
	if memoKey != "" && MarkerDropCount() == dropsBefore && MarkerKey(returned) == nil {
		inlineMemoMu.Lock()
		memo := inlineMemoOf(ctx.P)[contract.Declaration]
		if memo == nil {
			memo = map[string]InlineOutcome{}
			inlineMemoOf(ctx.P)[contract.Declaration] = memo
		}
		if len(memo) >= callResultsKept {
			for k := range memo {
				delete(memo, k)
				break
			}
		}
		memo[memoKey] = InlineOutcome{Returned: returned, PostByName: postByName, ParamPosts: paramPosts}
		inlineMemoMu.Unlock()
	}
	return AsCalleeResult(*contract, returned)
}


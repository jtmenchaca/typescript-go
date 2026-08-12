// from interprocedural/inline_contract_body.ts
//
// The body walk behind an inlined contract call: shadow the callee's
// names, run the body silently, write mutations back, and remember
// the outcome for identical observed states. Recursion answers with
// a marker after forgetting reference arguments.

package walk

import (
	"sort"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

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
				if _, ok := env[argument.Text()]; ok && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
					ctx.Aliases.Havoc(env, argument.Text())
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
			if _, ok := env[name]; ok {
				ctx.Aliases.Havoc(env, name)
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
		byKey := inlineMemo[contract.Declaration]
		_, memoSeen := byKey[memoKey]
		inlineMemoMu.Unlock()
		if tracing.Recording(tracing.GrainStep) && !memoSeen {
			tracing.Count("inline.freshkey."+calleeName.Text(), 0)
		}
	}
	if memoKey != "" {
		inlineMemoMu.Lock()
		held, ok := inlineMemo[contract.Declaration][memoKey]
		inlineMemoMu.Unlock()
		if ok {
			tracing.Count("inlineMemoHit", 0)
			return ReplayInline(ctx, env, call, *contract, argKnowns, held)
		}
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

	callEnv := Env{}
	for k, v := range env {
		if _, isShadowed := shadowed[k]; !isShadowed {
			callEnv[k] = v
		}
	}
	callableParams := map[string]Callback{}
	for i, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		callEnv[name.Text()] = ParameterKnown(parameter, i, call, argKnowns)
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
		held, ok := callEnv[name]
		if !ok {
			held = silence.Residue()
		}
		for _, snapshot := range thrown {
			snapshotValue, hasSnapshot := snapshot[name]
			if !hasSnapshot {
				snapshotValue = silence.Residue()
			}
			held = abstractdomain.JoinKnown(held, snapshotValue)
		}
		return held
	}
	bodyWrites := BodyWritesOf(ctx, contract.Declaration)
	var postByName []postByNameEntry
	for name, before := range env {
		if _, isShadowed := shadowed[name]; isShadowed {
			continue
		}
		// narrowing-only names keep the caller's state — only a name
		// the body may WRITE carries its exit state back
		if _, writes := bodyWrites[name]; !writes {
			continue
		}
		after := post(name)
		if !abstractdomain.SameKnown(after, before) {
			postByName = append(postByName, postByNameEntry{Name: name, After: after})
			dataflowfacts.UpdateTracked(ctx.Aliases, env, name, after)
		}
	}
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
				if _, ok := env[argument.Text()]; ok {
					ctx.Aliases.Havoc(env, argument.Text())
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
		memo := inlineMemo[contract.Declaration]
		if memo == nil {
			memo = map[string]InlineOutcome{}
			inlineMemo[contract.Declaration] = memo
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

// computeInlineMemoKey builds the DETERMINISTIC replay key —
// "" where nothing can be safely spelled (the TS source's try/catch
// around JSON.stringify answered the same way). SpellForMemoKey
// writes one flat string per value — compiler symbols by pointer
// identity, non-finite floats as words — so the key builds without
// a JSON pass, and this runs on EVERY inline call, hits included.
func computeInlineMemoKey(ctx *FlowContext, env Env, call *ast.Node, callExpr *ast.CallExpression, contract *FunctionContract, calleeName *ast.Node, argKnowns []abstractdomain.AbstractValue) string {
	var callbacks []*ast.Node
	for _, a := range callExpr.Arguments.Nodes {
		if ast.IsArrowFunction(a) || ast.IsFunctionExpression(a) {
			callbacks = append(callbacks, a)
		}
	}
	var observed []string
	if len(callbacks) == 0 {
		observed = ObservedOf(contract.Declaration)
	} else {
		set := map[string]struct{}{}
		for _, name := range ObservedOf(contract.Declaration) {
			set[name] = struct{}{}
		}
		for _, callback := range callbacks {
			for _, name := range dataflowfacts.ObservedNamesOf(callback) {
				set[name] = struct{}{}
			}
		}
		for name := range set {
			observed = append(observed, name)
		}
		sort.Strings(observed)
	}
	// one flat key: args then observed env rows, each spelled by the
	// builder speller — \x1e separates spells, and names (identifiers,
	// no control bytes) bind with '=' — injective without a JSON pass
	var key strings.Builder
	if len(callbacks) > 0 {
		key.WriteByte('@')
		key.WriteString(strconv.Itoa(CallNodeIdOf(call)))
		key.WriteByte('|')
	}
	for _, arg := range argKnowns {
		spelled, ok := abstractdomain.SpellForMemoKey(arg)
		if !ok {
			if tracing.Recording(tracing.GrainStep) {
				tracing.Count("inline.unkeyed."+calleeName.Text(), 0)
			}
			return ""
		}
		key.WriteString(spelled)
		key.WriteByte('\x1e')
	}
	key.WriteByte(';')
	for _, name := range observed {
		held, ok := env[name]
		if !ok {
			continue
		}
		heldSpell, ok := abstractdomain.SpellForMemoKey(held)
		if !ok {
			if tracing.Recording(tracing.GrainStep) {
				tracing.Count("inline.unkeyed."+calleeName.Text(), 0)
			}
			return ""
		}
		key.WriteString(name)
		key.WriteByte('=')
		key.WriteString(heldSpell)
		key.WriteByte('\x1e')
	}
	return key.String()
}

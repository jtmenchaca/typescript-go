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
//
// A SUPER-rooted callee — `super.m(x)`, a derived constructor's
// `super(x)` — runs the base member on the SAME instance the caller's
// `this` names: the dispatch is static, the receiver is not. So it reads
// the caller's tracked `this` entry, the very value `this.m(x)` reads
// through the ThisKeyword arm of evaluateExpression, gated the same way
// that arm gates it — a `this` with its own dynamic receiver is not the
// tracked one, and there `super` names no class either.
func SummaryCallReceiver(ctx *FlowContext, env Env, call *ast.Node) abstractdomain.AbstractValue {
	callee := CalleeExpressionOf(call)
	if callee == nil {
		return silence.Residue()
	}
	if root := SuperCalleeRoot(callee); root != nil {
		if dataflowfacts.EnclosingThisClass(root) == nil {
			return silence.Residue()
		}
		held, ok := env.Get("this")
		if !ok {
			return silence.Residue()
		}
		return held
	}
	if !ast.IsPropertyAccessExpression(callee) {
		return silence.Residue()
	}
	receiver := callee.AsPropertyAccessExpression().Expression
	if receiver == nil {
		return silence.Residue()
	}
	if ReadsWithoutEffect(receiver) {
		return evaluateExpression(ctx, env, receiver)
	}
	if constructed := constructedReceiverValue(ctx, env, receiver); constructed != nil {
		return *constructed
	}
	return silence.Residue()
}

// constructedReceiverValue reads a `new C(...)` RECEIVER —
// `new Person().years()` — as the instance the construction holds.
//
// The caller's walk already ran the construction once for its effects
// (evaluate_call_expression walks a contracted method call's receiver
// before the arguments) and dropped the value; without this reading the
// method's this-field entries all fill TOP and the field knowledge the
// constructor established never reaches the call. Deriving the value
// again is safe exactly when every constructor argument is effect-free
// to read twice — a literal or an effect-free chain of names —
// because ConstructedInstance itself walks the constructor's body on a
// fresh environment and touches nothing the caller tracks. The re-run
// is SILENT: whatever the first evaluation had to report is reported
// already, and a second copy of each diagnostic would be a new claim.
//
// Nil for every other receiver — anything that RUNS beyond the gated
// construction keeps the residue the caller supplies.
func constructedReceiverValue(ctx *FlowContext, env Env, receiver *ast.Node) *abstractdomain.AbstractValue {
	core := Unwrapped(receiver)
	if core == nil || !ast.IsNewExpression(core) {
		return nil
	}
	newExpr := core.AsNewExpression()
	if newExpr.Arguments != nil {
		for _, argument := range newExpr.Arguments.Nodes {
			if !readsTwiceWithoutEffect(argument) {
				return nil
			}
		}
	}
	silent := *ctx
	silent.Report = func(assignability.RefinementDiagnostic) {}
	return EvaluateNewExpression(&silent, env, core)
}

// readsTwiceWithoutEffect: an argument a second evaluation cannot
// double — an effect-free chain of names, or a literal (a negated
// number included).
func readsTwiceWithoutEffect(argument *ast.Node) bool {
	if argument == nil {
		return false
	}
	if ReadsWithoutEffect(argument) {
		return true
	}
	switch argument.Kind {
	case ast.KindNumericLiteral, ast.KindStringLiteral,
		ast.KindNoSubstitutionTemplateLiteral, ast.KindBigIntLiteral,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		return true
	}
	if ast.IsPrefixUnaryExpression(argument) {
		prefix := argument.AsPrefixUnaryExpression()
		return prefix.Operator == ast.KindMinusToken && prefix.Operand.Kind == ast.KindNumericLiteral
	}
	return false
}

// classMethodWalkTarget is whether a call's callee is a class INSTANCE
// method (or get accessor) whose receiver resolves — through
// SummaryCallReceiver, the same read the kernel-summary route and the
// memo key already take — to a KindObject the walk actually holds
// field facts for. A plain function call, a static method, an
// object-literal method (ObjectLiteralMethodWalkCall's own case,
// checked first so the two routes never both claim one call), and a
// receiver SummaryCallReceiver could not read without running it
// (silence.Residue, Kind != KindObject) all decline — the caller falls
// through to the ordinary walk-route body walk with no `this` binding,
// exactly as it did before this route existed.
func classMethodWalkTarget(declaration *ast.Node, receiver abstractdomain.AbstractValue) bool {
	if declaration == nil {
		return false
	}
	if !ast.IsMethodDeclaration(declaration) && !ast.IsGetAccessorDeclaration(declaration) && !ast.IsSetAccessorDeclaration(declaration) {
		return false
	}
	if declaration.Parent == nil || !ast.IsClassLike(declaration.Parent) {
		return false
	}
	return receiver.Kind == abstractdomain.KindObject
}

// ClassMethodWalkCall runs a class instance method's body on the walk
// route — no IR, no kernel — with `this` bound to the RECEIVER'S OWN
// per-call value (SummaryCallReceiver's reading: the constructed
// instance's exact field state, not the class's generic invariant
// FieldInvariantsOf answers). readThisFieldInvariant's own
// `thisIsObject` bypass (this_property_access.go) is what makes this
// safe: a `this` bound to a KindObject value steps the invariant
// reader aside in favor of the general object-key read, so `this.#age`
// inside `years()` answers this call's OWN 40 or 200 rather than the
// class-wide join both calls would otherwise share.
//
// The pattern is ObjectLiteralMethodWalkCall's, re-rooted at a class
// receiver instead of an object-literal one: a fresh call environment,
// the method's own parameters bound the way InlineContractBody binds
// them, the return collected through ReturnSink, and every
// `this.key = value` write captured through ThisWriteSink and folded
// back into the receiver's own tracked object with setObjectKey — so a
// method that WRITES through `this` still lands its write on the
// caller's held instance rather than dropping it silently.
//
// Declines (ok=false) wherever classMethodWalkTarget declines, or the
// declaration carries no block body — the caller keeps its own
// fallback for every shape this function does not recognize.
func ClassMethodWalkCall(
	ctx *FlowContext, env Env, call *ast.Node, contract *FunctionContract, effective EffectiveArguments, receiver abstractdomain.AbstractValue,
) (abstractdomain.AbstractValue, bool) {
	if !classMethodWalkTarget(contract.Declaration, receiver) {
		return abstractdomain.AbstractValue{}, false
	}
	body := contract.Declaration.Body()
	if body == nil || !ast.IsBlock(body) {
		return abstractdomain.AbstractValue{}, false
	}
	callee := CalleeExpressionOf(call)
	if callee == nil {
		return abstractdomain.AbstractValue{}, false
	}
	// RECURSION GUARD: a method that calls back into itself (through
	// this receiver or another holding the same declaration) re-enters
	// this function with the same symbol marked —
	// accessorReadModifyWriteCore's own discipline, applied to the
	// method's own name symbol.
	calleeName := contract.Declaration.Name()
	if calleeName == nil || !ast.IsIdentifier(calleeName) {
		return abstractdomain.AbstractValue{}, false
	}
	symbol := ctx.P.Checker.GetSymbolAtLocation(calleeName)
	if symbol == nil {
		return abstractdomain.AbstractValue{}, false
	}
	inlining := ctx.Inlining
	if inlining == nil {
		inlining = map[*ast.Symbol]struct{}{}
	}
	if _, already := inlining[symbol]; already {
		return silence.Residue(), true
	}
	inlining[symbol] = struct{}{}
	defer delete(inlining, symbol)

	callEnv := NewEnv()
	callEnv.Set("this", receiver)
	for i, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		callEnv.Set(name.Text(), BoundParameterKnown(ctx, parameter, i, effective))
	}
	var returnSink []abstractdomain.AbstractValue
	sink := map[string][]abstractdomain.AbstractValue{}
	silent := *ctx
	silent.Report = func(assignability.RefinementDiagnostic) {}
	silent.ReturnSink = &returnSink
	silent.ThisWriteSink = sink
	silent.Inlining = inlining
	AnalyzeStatements(&silent, callEnv, body.AsBlock().Statements.Nodes, nil)

	// fold the method's `this.key = value` writes into the receiver's
	// own object value — setObjectKey's own rule (index_operators.go),
	// the same rebuild WriteProperty runs for a `this.key = v` write —
	// only where the receiver's RECEIVER EXPRESSION is itself a plain
	// name or `this` the caller tracks (rootOfReceiver); a receiver
	// SummaryCallReceiver derived from a fresh `new C(...)` construction
	// names no tracked caller slot to fold back into, so the write
	// simply lands on the local `receiver` copy and is discarded with
	// it, exactly as ConstructedInstance's own one-shot read already is.
	if receiverExpression := receiverExpressionOf(callee); receiverExpression != nil {
		if name, rooted := rootOfReceiver(receiverExpression); rooted {
			if _, hasReceiver := env.Get(name); hasReceiver {
				nextReceiver := receiver
				for key, writes := range sink {
					joined := writes[0]
					for _, w := range writes[1:] {
						joined = abstractdomain.JoinKnown(joined, w)
					}
					nextReceiver = abstractdomain.KnownObject(setObjectKey(nextReceiver.Keys, key, joined), nil, false, abstractdomain.TrustProved, false)
				}
				UpdateTrackedEnv(ctx.Aliases, env, name, nextReceiver)
			}
		}
	}

	returned := silence.Residue()
	if len(returnSink) > 0 {
		returned = returnSink[0]
		for _, v := range returnSink[1:] {
			returned = abstractdomain.JoinKnown(returned, v)
		}
	}
	return AsCalleeResult(*contract, returned), true
}

// receiverExpressionOf is a call-like node's receiver expression — the
// property access's own Expression for a method call, nil for a super
// call or a plain function call (neither names a receiver
// rootOfReceiver could resolve).
func receiverExpressionOf(callee *ast.Node) *ast.Node {
	if callee == nil || !ast.IsPropertyAccessExpression(callee) {
		return nil
	}
	return callee.AsPropertyAccessExpression().Expression
}

// InlineContractBody is inlineContractBody in the TS source.
func InlineContractBody(ctx *FlowContext, env Env, call *ast.Node, contract *FunctionContract, effective EffectiveArguments) abstractdomain.AbstractValue {
	tracing.Count("inlineContractCall", 0)
	body := contract.Declaration.Body()
	if body == nil {
		return silence.Residue()
	}
	// the placement seam: one entry per parameter position, values and
	// nodes from the same construction, for a plain call, a spreading
	// call, and a tagged template alike (EffectiveArgumentsOf)
	argumentNodes := effective.Nodes
	argKnowns := effective.Knowns
	callee := CalleeExpressionOf(call)
	var calleeName *ast.Node
	if callee == nil {
		return silence.Residue()
	}
	// a SUPER-rooted callee names its member on the base declaration, not
	// at the call site: `super.m` reads no symbol off `m` and a bare
	// `super(…)` spells no name at all. The declaration super_binding.go
	// resolved carries both readings — its own name node labels the
	// counter, and its own symbol keys the recursion set and shards the
	// memo. Where the declaration reaches no symbol, the inline declines
	// here rather than keying on a name that means something else.
	superRooted := SuperCalleeRoot(callee) != nil
	if superRooted {
		calleeName = contract.Declaration.Name()
	} else if ast.IsIdentifier(callee) {
		calleeName = callee
	} else if ast.IsPropertyAccessExpression(callee) {
		calleeName = callee.AsPropertyAccessExpression().Name()
	}
	if calleeName == nil || !ast.IsIdentifier(calleeName) {
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
		for _, argument := range argumentNodes {
			// a position with no caller expression behind it — a tagged
			// template's template object, an item expanded out of a spread —
			// names no caller state, so there is nothing to forget behind it
			if argument == nil {
				continue
			}
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

	// an OBJECT-LITERAL METHOD call tries its own precise walk-route
	// BEFORE the memo/kernel-summary routes below: those two both serve
	// a call that writes a receiver field by FORGETTING the whole
	// receiver afterward (SummaryReceiverEffects' "served-call forget"
	// below) — correct for a class instance, whose `this.key` reads
	// answer through the class's standing field invariant regardless of
	// any one call's history, but wrong for an object literal, where
	// `person.age` after `person.bump()` reads the tracked KindObject's
	// OWN key, and forgetting it is the only place the exact written
	// value (41) lived. ObjectLiteralMethodWalkCall folds the write back
	// onto the receiver instead of forgetting it, and declines cleanly
	// (ok=false) for every callee it does not recognize — a class
	// method, a bare hand-out, an unresolvable receiver — so this call
	// changes nothing for any shape besides the one it fixes.
	if result, handled := ObjectLiteralMethodWalkCall(ctx, env, call, contract, effective); handled {
		return result
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
	//
	// The RECEIVER is read once, here: it feeds the summary route's
	// this-entries below AND the key — a method's outcome is a function
	// of the fields its body reads off the instance, so two calls on
	// receivers holding different field values must never share an
	// outcome (`new Sealed(40).years()` and `new Sealed(200).years()`
	// spell one argument list and one caller state).
	receiver := SummaryCallReceiver(ctx, env, call)
	memoKey := computeInlineMemoKey(ctx, env, call, contract, calleeName, effective, receiver)
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
			return ReplayInline(ctx, env, *contract, effective, held)
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
	//
	// EXACT (effective.Exact) picks the direct-apply variant that
	// declines rather than TOP-serves a rest parameter's read over
	// THIS call's own exact tail (KernelSummaryDirectExactOn's
	// comment) — an inexact call keeps the plain spelling, since an
	// unread spread leaves no exact tail to fall back on anyway.
	kernelSummaryDirect := KernelSummaryDirectOn
	if effective.Exact {
		kernelSummaryDirect = KernelSummaryDirectExactOn
	}
	if summarized, ok := kernelSummaryDirect(ctx, argKnowns, contract, receiver); ok {
		tracing.Count("inline.summaryDirect", 0)
		// THE SERVED-CALL FORGET: a summary whose body writes receiver
		// fields, writes a parameter bundle's fields, or returns its
		// receiver moves object knowledge the caller holds — the same
		// knowledge the OPAQUE path forgets through ForgetThrough. A
		// served answer forgets exactly the same way, and such calls are
		// never memoized: a replay would skip the forget.
		if receiverTouched, writtenArguments := SummaryReceiverEffects(ctx, contract.Declaration); receiverTouched || len(writtenArguments) > 0 {
			// a SUPER-rooted callee's receiver is the caller's `this`, and a
			// SuperKeyword matches no ForgetThrough arm — handing it there
			// would forget NOTHING while the base body wrote receiver fields.
			// The this-root forget is the one that speaks for that receiver:
			// havoc the alias class and reseed from the class's field
			// invariants, fired on the same receiverTouched terms a property
			// receiver gets.
			if receiverTouched && SuperCalleeRoot(callee) != nil {
				ForgetThisHeld(ctx, env, callee)
			} else if receiverTouched && ast.IsPropertyAccessExpression(callee) {
				ForgetThrough(ctx, env, callee.AsPropertyAccessExpression().Expression)
			}
			for _, index := range writtenArguments {
				if index < len(argumentNodes) && argumentNodes[index] != nil {
					ForgetThrough(ctx, env, argumentNodes[index])
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
	// the kernel-summary route above declined (no dylib, or the body did
	// not lower) — a CLASS method call still has a walk-route answer:
	// `this` bound to THIS CALL'S OWN receiver value (the `receiver`
	// SummaryCallReceiver already read above, for the memo key and the
	// kernel-summary route) rather than the class-wide
	// FieldInvariantsOf join every call used to share —
	// `new Sealed(40).years()` and `new Sealed(200).years()` must answer
	// 40 and 200, not one poisoned join of both. Declines
	// (classMethodWalkTarget) wherever the receiver is not a KindObject
	// the walk could resolve, which is exactly where the general body
	// walk below already fell back to the field-invariant reading
	// through `this` unbound.
	if result, handled := ClassMethodWalkCall(ctx, env, call, contract, effective, receiver); handled {
		return result
	}
	inlining[symbol] = struct{}{}

	// the callee's own names shadow the caller's — a destructured
	// parameter's own LEAVES shadow too (`[a]`'s `a`), the same names
	// BindInlineParameter binds below
	shadowed := map[string]struct{}{}
	for _, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if ast.IsIdentifier(name) {
			shadowed[name.Text()] = struct{}{}
			continue
		}
		if name != nil && (ast.IsObjectBindingPattern(name) || ast.IsArrayBindingPattern(name)) {
			for _, leaf := range boundPatternNames(name) {
				shadowed[leaf] = struct{}{}
			}
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
			// a destructured parameter (`[a]`, `{age}`) binds every leaf
			// its pattern names — BindInlineParameter destructures the
			// SAME argument value ParameterKnown places at this position,
			// so `a`/`age` land in callEnv instead of staying unbound
			BindInlineParameter(ctx, callEnv, parameter, i, effective)
			continue
		}
		// the parameter wears its argument MET WITH ITS OWN DECLARED TYPE.
		// The caller's exact value is the point of inlining and the meet
		// keeps it: the annotation is a ceiling, so a value inside it
		// passes through whole (entryStateMeet's own argument). What the
		// meet removes is a claim the declaration cannot carry — an
		// object literal's COMPLETE key set bound to a parameter declared
		// `Record<K, V>`. The keys stay; the closed-world claim goes,
		// because one call site's key set is not the parameter's.
		callEnv.Set(name.Text(), BoundParameterKnown(ctx, parameter, i, effective))
		if i < len(argumentNodes) && argumentNodes[i] != nil {
			argument := argumentNodes[i]
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
		if i >= len(argumentNodes) {
			continue
		}
		argument := argumentNodes[i]
		// a position with no caller expression behind it has nothing to
		// write back through: a tagged template's template object is
		// synthesized and frozen (sec-gettemplateobject), and an item
		// expanded out of a spread is an element of the source's VALUE,
		// reachable only through the source array — which the walk never
		// wrote back through when the spread held its own single slot
		// either, since a SpreadElement node matches no target ForgetThrough
		// recognizes
		if argument == nil {
			continue
		}
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
			entry:         BoundParameterKnown(ctx, parameter, i, effective),
			argument:      argument,
			restArguments: argumentNodes[i:],
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

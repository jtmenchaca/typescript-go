// `Promise.withResolvers<T>()` on the default-library Promise
// (sec-promise.withresolvers, ES2024): NewPromiseCapability's own
// three-part answer handed straight back as `{ promise, resolve,
// reject }` — no executor to run, unlike `new Promise(executor)`.
// resolve/reject settle the SAME promise later, from ordinary
// sibling statements rather than a nested closure, so this file's
// own device is a bind-time PAIRING (FlowContext.ResolverTargets)
// rather than promise_constructor_models.go's forward body-scan: the
// resolve/reject binding's declared symbol maps to the promise
// binding's tracked name, and a later `resolve(arg)`/`reject()` call
// on that symbol writes the settled value into the promise's env
// slot through UpdateTrackedEnv — the ordinary, once-only, no-
// double-evaluation write every tracked mutation already takes
// (array_method_models.go's push/pop and kin).
//
// Two call shapes settle a promise this way:
//   - a bare `resolve(arg)` — the destructured binding, paired at
//     BindDestructuringDeclaration time (destructure_binding.go).
//   - `<name>.resolve(arg)` — the whole capability kept as one
//     object (`const over = Promise.withResolvers()`); no pairing
//     needed, the write lands on the SAME tracked object's own
//     "promise" key, exactly the shape readArrayWriteMethods already
//     mutates in place.
//
// Reject calls need no reading at all, the same over-approximation
// promise_constructor_models.go already takes: a rejection means the
// await's continuation never runs.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// promiseWithResolversCapabilityOf recognizes `Promise.withResolvers()`
// on the default-library Promise, no arguments — the one call shape
// this file reads. Nil for every other call.
func promiseWithResolversCapabilityOf(ctx *FlowContext, e *ast.Node) bool {
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return false
	}
	pa := call.Expression.AsPropertyAccessExpression()
	if !(ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Promise" &&
		pa.Name().Text() == "withResolvers" && resolvesToDefaultLib(ctx, pa.Expression)) {
		return false
	}
	return call.Arguments == nil || len(call.Arguments.Nodes) == 0
}

// readPromiseWithResolvers reads `Promise.withResolvers<T>()` itself:
// a fresh capability object, its promise settled to nothing yet (the
// walk's own residue, replaced the moment a paired resolve call is
// walked). Nil when the call is not that shape.
func readPromiseWithResolvers(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if !promiseWithResolversCapabilityOf(ctx, e) {
		return nil
	}
	pending := silence.Residue()
	promiseValue := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &pending}
	out := abstractdomain.KnownObject([]abstractdomain.ObjectKey{
		{Name: "promise", Value: promiseValue},
		{Name: "resolve", Value: abstractdomain.HostFunction},
		{Name: "reject", Value: abstractdomain.HostFunction},
	}, nil, true, abstractdomain.TrustSpec, false)
	return &out
}

// pairResolverBinding runs once, at the destructuring site, for the
// resolve/reject member of a `Promise.withResolvers()` result — links
// the member's OWN declared symbol to the promise binding's tracked
// name in ctx.ResolverTargets, so a later call on that symbol finds
// its promise.
func pairResolverBinding(ctx *FlowContext, memberName *ast.Node, promiseTrackedName string) {
	if memberName == nil || !ast.IsIdentifier(memberName) {
		return
	}
	symbol := symbolAt(ctx.P.Checker, memberName)
	if symbol == nil {
		return
	}
	if ctx.ResolverTargets == nil {
		ctx.ResolverTargets = map[*ast.Symbol]string{}
	}
	ctx.ResolverTargets[symbol] = promiseTrackedName
}

// PairPromiseWithResolversBindings runs from bindObjectPattern once a
// `Promise.withResolvers()` destructuring's elements are all bound —
// pairs every resolve/reject element with the promise element bound
// in the SAME pattern. No-op when the pattern holds no "promise"
// member (there is nothing to settle into) or the initializer is not
// this call shape.
func PairPromiseWithResolversBindings(ctx *FlowContext, initializer *ast.Node, elements []*ast.Node) {
	if initializer == nil || !ast.IsCallExpression(initializer) || !promiseWithResolversCapabilityOf(ctx, initializer) {
		return
	}
	var promiseTrackedName string
	hasPromise := false
	for _, element := range elements {
		be := element.AsBindingElement()
		key, hasKey := bindingElementKeyPure(be)
		if hasKey && key == "promise" {
			if beName := be.Name(); beName != nil && ast.IsIdentifier(beName) {
				promiseTrackedName = beName.Text()
				hasPromise = true
			}
		}
	}
	if !hasPromise {
		return
	}
	for _, element := range elements {
		be := element.AsBindingElement()
		key, hasKey := bindingElementKeyPure(be)
		if !hasKey || (key != "resolve" && key != "reject") {
			continue
		}
		pairResolverBinding(ctx, be.Name(), promiseTrackedName)
	}
}

// bindingElementKeyPure is bindingElementKey's SYNTACTIC-only half —
// a plain identifier property name, never a computed one. Pairing
// runs before any env write, so it cannot evaluate a computed key
// the way bindingElementKey itself does; a computed "promise"/
// "resolve" name is vanishingly unlikely for this ES2024 built-in's
// own destructuring anyway, and declining there costs nothing but the
// pairing (the destructuring itself still binds normally).
func bindingElementKeyPure(be *ast.BindingElement) (string, bool) {
	if be.PropertyName != nil {
		if ast.IsIdentifier(be.PropertyName) {
			return be.PropertyName.Text(), true
		}
		return "", false
	}
	beName := be.Name()
	if beName != nil && ast.IsIdentifier(beName) {
		return beName.Text(), true
	}
	return "", false
}

// readPromiseResolverCall reads a BARE `resolve(arg)`/`reject()` call
// whose callee's declared symbol was paired at the destructuring site
// (PairPromiseWithResolversBindings). Nil for every other callee —
// the chain moves on, including for reject (over-approximated as "no
// reading needed", the same as the constructor's own reject calls).
func readPromiseResolverCall(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if len(ctx.ResolverTargets) == 0 {
		return nil
	}
	call := e.AsCallExpression()
	callee := Unwrapped(call.Expression)
	if !ast.IsIdentifier(callee) {
		return nil
	}
	symbol := symbolAt(ctx.P.Checker, callee)
	if symbol == nil {
		return nil
	}
	promiseTrackedName, paired := ctx.ResolverTargets[symbol]
	if !paired {
		return nil
	}
	answer := abstractdomain.HostFunction
	if callee.Text() != "resolve" {
		// a paired reject: no settlement to read, but the call is
		// still this reader's own recognized shape
		nothing := abstractdomain.AtTrustLevel(abstractdomain.Undef, abstractdomain.TrustSpec)
		return &nothing
	}
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	// `resolve()` with no argument fulfills with undefined
	// (sec-createresolvingfunctions)
	settledArgument := abstractdomain.AtTrustLevel(abstractdomain.Undef, abstractdomain.TrustSpec)
	if len(arguments) >= 1 {
		settledArgument = evaluateExpression(ctx, env, arguments[0])
	}
	settled, ok := promiseSettledValueOf(settledArgument)
	if !ok {
		return nil
	}
	if held, hasHeld := env.Get(promiseTrackedName); hasHeld {
		next := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &settled}
		if held.Kind == abstractdomain.KindPromise && held.Inner != nil &&
			held.Inner.Kind != abstractdomain.KindUnknown {
			// a second resolve call joins — [[AlreadyCalled]] lets
			// exactly one settlement win, and the walk does not know
			// which
			joined := abstractdomain.JoinKnown(*held.Inner, settled)
			next = abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &joined}
		}
		UpdateTrackedEnv(ctx.Aliases, env, promiseTrackedName, next)
	}
	return &answer
}

// readPromiseWithResolversPropertyCall reads `<name>.resolve(arg)` /
// `<name>.reject()` where the receiver is a tracked capability object
// this file itself built — the whole `{ promise, resolve, reject }`
// kept as one binding (`const over = Promise.withResolvers()`). The
// write lands on the SAME tracked name's own "promise" key, the
// shape readArrayWriteMethods already mutates in place.
func readPromiseWithResolversPropertyCall(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiver, method := site.Ctx, site.Env, site.E, site.Receiver, site.Method
	if !(site.HasTrackedName && receiver.Kind == abstractdomain.KindObject && (method == "resolve" || method == "reject")) {
		return nil
	}
	hasPromiseKey, hasResolveKey := false, false
	for _, key := range receiver.Keys {
		if key.Name == "promise" && key.Value.Kind == abstractdomain.KindPromise {
			hasPromiseKey = true
		}
		if key.Name == "resolve" && key.Value.Kind == abstractdomain.KindHostFunction {
			hasResolveKey = true
		}
	}
	if !hasPromiseKey || !hasResolveKey {
		return nil
	}
	answer := abstractdomain.HostFunction
	if method != "resolve" {
		nothing := abstractdomain.AtTrustLevel(abstractdomain.Undef, abstractdomain.TrustSpec)
		return &nothing
	}
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	settledArgument := abstractdomain.AtTrustLevel(abstractdomain.Undef, abstractdomain.TrustSpec)
	if len(arguments) >= 1 {
		settledArgument = evaluateExpression(ctx, env, arguments[0])
	}
	settled, ok := promiseSettledValueOf(settledArgument)
	if !ok {
		return nil
	}
	nextKeys := make([]abstractdomain.ObjectKey, len(receiver.Keys))
	copy(nextKeys, receiver.Keys)
	for i, key := range nextKeys {
		if key.Name != "promise" {
			continue
		}
		next := settled
		if key.Value.Kind == abstractdomain.KindPromise && key.Value.Inner != nil &&
			key.Value.Inner.Kind != abstractdomain.KindUnknown {
			joined := abstractdomain.JoinKnown(*key.Value.Inner, settled)
			nextKeys[i].Value = abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &joined}
			continue
		}
		nextKeys[i].Value = abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &next}
	}
	nextObject := abstractdomain.KnownObject(nextKeys, receiver.Stated, receiver.Complete, abstractdomain.TrustLevelOf(receiver), receiver.BareProto)
	UpdateTrackedEnv(ctx.Aliases, env, site.TrackedName, nextObject)
	return &answer
}

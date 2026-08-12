// from control_flow/call_site_bindings.ts
//
// What a function's call sites pin onto its parameters. One
// interface: CallSiteBindings. Callbacks (map, transform, codec,
// Array.from, a user-function argument) and non-exported named
// joins are internal adapters — callers do not choose between them.
//
// CallSiteCtx is NOT redefined here — it already lives in
// parameter_answer.go (the leaf shape "everything CallSiteBindings
// needs to evaluate a site"), reused per that file's own comment.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// CallSiteBindings is callSiteBindings in the TS source: what `fn`'s
// call sites pin onto its parameters. A FunctionDeclaration wears the
// join of every visible call; an inline callback wears what that one
// call pins. (nil, false) when callers are not all in view.
func CallSiteBindings(ctx CallSiteCtx, fn *ast.Node) (Env, bool) {
	if ast.IsFunctionDeclaration(fn) {
		return declaredJoin(ctx.P, ctx.Registry, ctx.Objects, ctx.Contracts, ctx.Kernel, fn)
	}
	return callbackSitePins(ctx.P, ctx.Registry, ctx.Objects, ctx.Contracts, ctx.Kernel, fn)
}

// callbackSitePins is callbackSitePins in the TS source: every
// parameter binding a callback's call site pins — the element, the
// index, the receiver itself; reduce's accumulator stays unbound on
// purpose (its honest value is the accumulation, not the element).
// The walk runs to the CALL with the same machinery a hover uses, the
// receiver evaluates there, and the parameters bind the way the
// inliner binds them. (nil, false) where the function is not a
// recognized callback's argument or the call is out of reach.
func callbackSitePins(
	p *program.CheckerProgram,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	contracts map[*ast.Symbol]*FunctionContract,
	kernel *kernelbridge.RefinedTSKernel,
	fn *ast.Node, // ArrowFunction | FunctionExpression
) (Env, bool) {
	// `z.codec(A, B, {decode, encode})`: decode's parameter wears A's
	// compiled set and encode's wears B's — the codec is a pipe whose
	// transform runs on A's output and whose reverse runs on B's input
	// (vendored schemas.ts:2403)
	if ast.IsPropertyAssignment(fn.Parent) {
		pa := fn.Parent.AsPropertyAssignment()
		paName := pa.Name()
		if paName != nil && ast.IsIdentifier(paName) &&
			(paName.Text() == "decode" || paName.Text() == "encode") &&
			ast.IsObjectLiteralExpression(fn.Parent.Parent) {
			codecCall := fn.Parent.Parent.Parent
			if codecCall != nil && ast.IsCallExpression(codecCall) {
				callExpr := codecCall.AsCallExpression()
				if ast.IsPropertyAccessExpression(callExpr.Expression) &&
					callExpr.Expression.AsPropertyAccessExpression().Name().Text() == "codec" &&
					callExpr.Arguments != nil && len(callExpr.Arguments.Nodes) == 3 &&
					callExpr.Arguments.Nodes[2] == fn.Parent.Parent {
					var source *ast.Node
					if paName.Text() == "decode" {
						source = callExpr.Arguments.Nodes[0]
					} else {
						source = callExpr.Arguments.Nodes[1]
					}
					compiled := annotations.CompileAnnotation(p, source, registry)
					if annotations.IsUnsupported(compiled) {
						return nil, false
					}
					return SchemaCallbackPins(schemaCallbackPinsParams{c: p.Checker, fn: fn, compiled: *compiled.Annotation}), true
				}
			}
		}
	}

	// `Array.from({ length: n }, mapper)`: an object literal spelling
	// only `length` has no iterator, so the array-like path runs — the
	// mapper is called with « kValue, 𝔽(k) » where every element read
	// off the bare object is undefined and k counts from 0
	// (sec-array.from)
	if ast.IsCallExpression(fn.Parent) {
		parentCall := fn.Parent.AsCallExpression()
		if parentCall.Arguments != nil && len(parentCall.Arguments.Nodes) > 1 && parentCall.Arguments.Nodes[1] == fn &&
			ast.IsPropertyAccessExpression(parentCall.Expression) {
			pa := parentCall.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Array" && pa.Name().Text() == "from" &&
				ast.IsObjectLiteralExpression(parentCall.Arguments.Nodes[0]) {
				objLit := parentCall.Arguments.Nodes[0].AsObjectLiteralExpression()
				if objLit.Properties != nil && len(objLit.Properties.Nodes) == 1 &&
					ast.IsPropertyAssignment(objLit.Properties.Nodes[0]) {
					prop0Name := objLit.Properties.Nodes[0].AsPropertyAssignment().Name()
					if prop0Name != nil && ast.IsIdentifier(prop0Name) && prop0Name.Text() == "length" {
						return ArrayFromMapperPins(arrayFromMapperPinsParams{c: p.Checker, fn: fn}), true
					}
				}
			}
		}
	}

	call := fn.Parent
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	callExpr := call.AsCallExpression()
	// a callback handed to a USER function in view: the callee's body
	// shows where the parameter is invoked, and each invocation's
	// arguments — evaluated with the callee's other parameters bound
	// from THIS call — are what the callback's parameters wear. A
	// callback that escapes any other way claims nothing.
	if ast.IsIdentifier(callExpr.Expression) {
		position := -1
		if callExpr.Arguments != nil {
			for i, a := range callExpr.Arguments.Nodes {
				if a == fn {
					position = i
					break
				}
			}
		}
		symbol := symbolAt(p.Checker, callExpr.Expression)
		var declaration *ast.Node
		if symbol != nil {
			declaration = symbol.ValueDeclaration
		}
		if position >= 0 && declaration != nil && ast.IsFunctionDeclaration(declaration) &&
			declaration.Body() != nil && !ast.HasSyntacticModifier(declaration, ast.ModifierFlagsExport) {
			parameters := declaration.Parameters()
			var holder *ast.Node
			if position < len(parameters) {
				holder = parameters[position]
			}
			var holderName *ast.Node
			if holder != nil {
				holderName = holder.AsParameterDeclaration().Name()
			}
			if holderName != nil && ast.IsIdentifier(holderName) {
				SetTransferKernel(kernel)
				narrowing.SetNarrowKernel(kernel)
				flowCtx := &FlowContext{
					P: p, Kernel: kernel, Registry: registry, Objects: objects, Contracts: contracts,
					Report:   func(d assignability.RefinementDiagnostic) {},
					Aliases:  dataflowfacts.NewAliasClasses(),
					Declared: map[string]*annotations.DeclaredRefinement{},
				}
				// the recorded snapshot IS the state at the call — pass 3's
				// own walk already carried it here; only a call no walk
				// recorded pays the per-query walk below
				snapshot, hasSnapshot := CallSnapshotOf(p, call)
				outerEnv := snapshot
				if !hasSnapshot {
					outerEnv = Env{}
					outerSite, hasOuterSite := SiteOf(p, contracts, call)
					if hasOuterSite {
						for i, outer := range outerSite.Parameters {
							if outer == nil || !ast.IsIdentifier(outer.AsParameterDeclaration().Name()) {
								continue
							}
							outerName := outer.AsParameterDeclaration().Name().Text()
							var stated *annotations.DeclaredRefinement
							if outerSite.Contract != nil && i < len(outerSite.Contract.Params) {
								stated = outerSite.Contract.Params[i]
							}
							if stated == nil {
								outerEnv[outerName] = silence.Residue()
							} else {
								outerEnv[outerName] = AbstractValueOfDeclared(*stated)
							}
						}
					}
					// the walk to the call carries the state its arguments are
					// evaluated in — a top-level call walks the file's own
					// statements; a failed walk just leaves less known
					func() {
						defer func() { recover() }() // a refused question mid-walk leaves the partial state
						var statements []*ast.Node
						if hasOuterSite {
							statements = outerSite.Statements
						} else {
							statements = p.Entry.Statements.Nodes
						}
						AnalyzeToToken(flowCtx, outerEnv, statements, call, false)
					}()
				}
				calleeEnv := Env{}
				for i, parameter := range declaration.Parameters() {
					if !ast.IsIdentifier(parameter.AsParameterDeclaration().Name()) || i == position {
						continue
					}
					name := parameter.AsParameterDeclaration().Name().Text()
					var argument *ast.Node
					if callExpr.Arguments != nil && i < len(callExpr.Arguments.Nodes) {
						argument = callExpr.Arguments.Nodes[i]
					}
					if argument == nil {
						calleeEnv[name] = abstractdomain.Undef
					} else {
						calleeEnv[name] = evaluateExpression(flowCtx, outerEnv, argument)
					}
				}
				fnParameters := fn.Parameters()
				joins := make([]*abstractdomain.AbstractValue, len(fnParameters))
				clean := true
				func() {
					defer func() {
						if recover() != nil {
							clean = false
						}
					}()
					target := symbolAt(p.Checker, holderName)
					var visit func(node *ast.Node)
					visit = func(node *ast.Node) {
						if ast.IsIdentifier(node) && node.Text() == holderName.Text() &&
							node != holderName && symbolAt(p.Checker, node) == target {
							parent := node.Parent
							if ast.IsCallExpression(parent) && parent.AsCallExpression().Expression == node {
								parentCallExpr := parent.AsCallExpression()
								for k := range fnParameters {
									var argument *ast.Node
									if parentCallExpr.Arguments != nil && k < len(parentCallExpr.Arguments.Nodes) {
										argument = parentCallExpr.Arguments.Nodes[k]
									}
									var value abstractdomain.AbstractValue
									if argument == nil {
										value = abstractdomain.Undef
									} else {
										value = evaluateExpression(flowCtx, cloneEnv(calleeEnv), argument)
									}
									if joins[k] == nil {
										v := value
										joins[k] = &v
									} else {
										joined := abstractdomain.JoinKnown(*joins[k], value)
										joins[k] = &joined
									}
								}
							} else {
								clean = false
							}
						}
						node.ForEachChild(func(child *ast.Node) bool {
							visit(child)
							return false
						})
					}
					visit(declaration.Body())
				}()
				if clean {
					bindings := Env{}
					for k, parameter := range fnParameters {
						value := joins[k]
						if value == nil || value.Kind == abstractdomain.KindUnknown {
							continue
						}
						BindParameter(p.Checker, parameter, *value, bindings)
					}
					if len(bindings) > 0 {
						return bindings, true
					}
				}
			}
		}
		return nil, false
	}
	if callExpr.Arguments == nil || len(callExpr.Arguments.Nodes) == 0 || callExpr.Arguments.Nodes[0] != fn {
		return nil, false
	}
	if !ast.IsPropertyAccessExpression(callExpr.Expression) {
		return nil, false
	}
	method := callExpr.Expression.AsPropertyAccessExpression().Name().Text()

	// `.transform(cb)`: the parameter wears the OUTPUT set of the
	// receiver chain — the annotation reader already compiles it, and
	// no walk is needed (an annotation is static). A libraryAdapter schema's
	// runtime is the library's, so its set rides at library grade.
	if method == "transform" {
		compiled := annotations.CompileAnnotation(p, callExpr.Expression.AsPropertyAccessExpression().Expression, registry)
		if annotations.IsUnsupported(compiled) {
			return nil, false
		}
		return SchemaCallbackPins(schemaCallbackPinsParams{c: p.Checker, fn: fn, compiled: *compiled.Annotation}), true
	}
	_, isArrayCallback := ArrayCallbackMethods[method]
	if !isArrayCallback && method != "then" {
		return nil, false
	}

	site, hasSite := SiteOf(p, contracts, call)
	if !hasSite {
		return nil, false
	}
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	flowCtx := &FlowContext{
		P: p, Kernel: kernel, Registry: registry, Objects: objects, Contracts: contracts,
		Report:   func(d assignability.RefinementDiagnostic) {},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	// the recorded snapshot IS the state at the owner call — pass 3's
	// own walk already carried it here, enclosing callbacks included;
	// only a call no walk recorded pays the seeding walk below
	snapshot, hasSnapshot := CallSnapshotOf(p, call)
	env := snapshot
	if !hasSnapshot {
		env = Env{}
		for i, outer := range site.Parameters {
			if outer == nil || !ast.IsIdentifier(outer.AsParameterDeclaration().Name()) {
				continue
			}
			outerName := outer.AsParameterDeclaration().Name().Text()
			var stated *annotations.DeclaredRefinement
			if site.Contract != nil && i < len(site.Contract.Params) {
				stated = site.Contract.Params[i]
			}
			if stated == nil {
				env[outerName] = silence.Residue()
			} else {
				env[outerName] = AbstractValueOfDeclared(*stated)
			}
		}
		// a non-exported enclosing function's parameters wear their
		// call-site join here too — the receiver of this callback often
		// IS such a parameter
		if site.Fn != nil && ast.IsFunctionDeclaration(site.Fn) {
			outer, hasOuter := declaredJoin(p, registry, objects, contracts, kernel, site.Fn)
			if hasOuter {
				allNil := true
				if site.Contract != nil {
					for _, held := range site.Contract.Params {
						if held != nil {
							allNil = false
							break
						}
					}
				}
				if allNil {
					for name, known := range outer {
						env[name] = known
					}
				}
			}
		}

		// the call may itself sit INSIDE other callbacks (a transform
		// building an object of maps): each encloser's call site pins its
		// own parameters, initialized outermost first
		InitializeEnclosingCallbacks(
			CallSiteCtx{P: p, Registry: registry, Objects: objects, Contracts: contracts, Kernel: kernel},
			env,
			fn.Parent,
		)
	}

	result := func() (bindings Env, ok bool) {
		defer func() {
			if recover() != nil {
				// a refused question binds nothing here — never a crash
				bindings, ok = nil, false
			}
		}()
		if !hasSnapshot && !AnalyzeToToken(flowCtx, env, site.Statements, call, false) {
			return nil, false
		}
		receiver := evaluateExpression(flowCtx, env, callExpr.Expression.AsPropertyAccessExpression().Expression)
		// `.then(cb)`: the parameter wears what the promise resolves to
		if method == "then" {
			fnParameters := fn.Parameters()
			if receiver.Kind != abstractdomain.KindPromise || len(fnParameters) == 0 {
				return nil, false
			}
			out := Env{}
			BindParameter(p.Checker, fnParameters[0], *receiver.Inner, out)
			return out, true
		}
		var joined *abstractdomain.AbstractValue
		if method == "reduce" {
			v := reduceAccumulatorJoin(flowCtx, env, call, fn, receiver)
			joined = v
		}
		var accumulator *abstractdomain.AbstractValue
		if joined != nil && joined.Kind != abstractdomain.KindUnknown {
			accumulator = joined
		}
		return ArrayCallbackPins(arrayCallbackPinsParams{
			c:           p.Checker,
			fn:          fn,
			receiver:    receiver,
			method:      method,
			accumulator: accumulator,
		}), true
	}
	return result()
}

// declaredJoin is declaredJoin in the TS source: a plain non-exported
// function's parameters wear the JOIN of what its call sites pass.
// Every caller of a non-exported function is in view, so the join
// over the evaluated arguments at each site is exactly what a
// parameter can hold. The gate is total: the function must not be
// exported, and EVERY use of its name must be a direct call or a
// recognized callback argument (`xs.map(f)` binds the element) — any
// other use (a read, a reassignment, an argument to an unmodeled
// call) means callers are not all in view, and nothing binds.
func declaredJoin(
	p *program.CheckerProgram,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	contracts map[*ast.Symbol]*FunctionContract,
	kernel *kernelbridge.RefinedTSKernel,
	fn *ast.Node, // FunctionDeclaration
) (Env, bool) {
	// read-once: the join is asked once per declaration per program —
	// pass 3 asks before the body walks, hover asks again later, and
	// the use-collection scan of the whole file (4.1% of the sampled
	// wall on the mongo contract-builder) must not re-run per ask.
	// Sound because the ordering walks enclosers first: every snapshot
	// this join reads is recorded before the first ask.
	bindingsMemoMu.Lock()
	held, ok := bindingsMemo[p]
	if !ok {
		held = map[*ast.Node]Env{}
		bindingsMemo[p] = held
	}
	remembered, hasRemembered := held[fn]
	bindingsMemoMu.Unlock()
	if hasRemembered {
		if remembered == nil {
			return nil, false
		}
		return remembered, true
	}
	computed, ok := declaredJoinUncached(p, registry, objects, contracts, kernel, fn)
	bindingsMemoMu.Lock()
	if ok {
		held[fn] = computed
	} else {
		held[fn] = nil
	}
	bindingsMemoMu.Unlock()
	return computed, ok
}

// bindingsMemo is the TS source's `WeakMap<CheckerProgram,
// Map<ts.FunctionDeclaration, Map<string, AbstractValue> | null>>` —
// Go has no weak maps; a regular map guarded by a mutex substitutes
// (same pattern as call_site_snapshots.go's snapshotStores), keyed on
// the program pointer. A nil map value (rather than the TS source's
// dedicated MISSING_BINDINGS sentinel — a Go map already
// distinguishes "absent" from "present but nil" via the second
// hasRemembered return) records the null answer.
var (
	bindingsMemoMu sync.Mutex
	bindingsMemo   = map[*program.CheckerProgram]map[*ast.Node]Env{}
)

func declaredJoinUncached(
	p *program.CheckerProgram,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	contracts map[*ast.Symbol]*FunctionContract,
	kernel *kernelbridge.RefinedTSKernel,
	fn *ast.Node, // FunctionDeclaration
) (Env, bool) {
	name := fn.Name()
	if name == nil {
		return nil, false
	}
	exported := ast.HasSyntacticModifier(fn, ast.ModifierFlagsExport)
	if exported {
		return nil, false
	}
	target := symbolAt(p.Checker, name)
	if target == nil {
		return nil, false
	}
	var directCalls []*ast.Node
	var callbackCalls []*ast.Node
	escapes := false
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if ast.IsIdentifier(node) && node.Text() == name.Text() && node != name && symbolAt(p.Checker, node) == target {
			parent := node.Parent
			if ast.IsCallExpression(parent) && parent.AsCallExpression().Expression == node {
				directCalls = append(directCalls, parent)
			} else if ast.IsCallExpression(parent) {
				parentCallExpr := parent.AsCallExpression()
				if parentCallExpr.Arguments != nil && len(parentCallExpr.Arguments.Nodes) > 0 && parentCallExpr.Arguments.Nodes[0] == node &&
					ast.IsPropertyAccessExpression(parentCallExpr.Expression) {
					_, isArrayCallback := ArrayCallbackMethods[parentCallExpr.Expression.AsPropertyAccessExpression().Name().Text()]
					if isArrayCallback {
						callbackCalls = append(callbackCalls, parent)
					} else {
						escapes = true
					}
				} else {
					escapes = true
				}
			} else {
				escapes = true
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(p.Entry.AsNode())
	if escapes || len(directCalls)+len(callbackCalls) == 0 {
		return nil, false
	}
	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	flowCtx := &FlowContext{
		P: p, Kernel: kernel, Registry: registry, Objects: objects, Contracts: contracts,
		Report:   func(d assignability.RefinementDiagnostic) {},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	fnParameters := fn.Parameters()
	// one slot per parameter; nil until a site contributes
	joins := make([]*abstractdomain.AbstractValue, len(fnParameters))
	contribute := func(slot int, value abstractdomain.AbstractValue) {
		if slot >= len(joins) {
			return
		}
		if joins[slot] == nil {
			v := value
			joins[slot] = &v
		} else {
			joined := abstractdomain.JoinKnown(*joins[slot], value)
			joins[slot] = &joined
		}
	}
	// walk the file to a call site, with the site's own enclosing
	// parameters and callbacks initialized the way a hover there would
	reach := func(call *ast.Node) (Env, bool) {
		// the recorded snapshot IS the state at the call site — no
		// per-site walk; the 484-walks-per-declaration quadratic this
		// replaces is findings/read-once.md's amplifier
		snapshot, hasSnapshot := CallSnapshotOf(p, call)
		if hasSnapshot {
			return snapshot, true
		}
		site, hasSite := SiteOf(p, contracts, call)
		if !hasSite {
			return nil, false
		}
		env := Env{}
		for i, outer := range site.Parameters {
			if outer == nil || !ast.IsIdentifier(outer.AsParameterDeclaration().Name()) {
				continue
			}
			outerName := outer.AsParameterDeclaration().Name().Text()
			var stated *annotations.DeclaredRefinement
			if site.Contract != nil && i < len(site.Contract.Params) {
				stated = site.Contract.Params[i]
			}
			if stated == nil {
				env[outerName] = silence.Residue()
			} else {
				env[outerName] = AbstractValueOfDeclared(*stated)
			}
		}
		InitializeEnclosingCallbacks(
			CallSiteCtx{P: p, Registry: registry, Objects: objects, Contracts: contracts, Kernel: kernel},
			env,
			call,
		)
		if AnalyzeToToken(flowCtx, env, site.Statements, call, false) {
			return env, true
		}
		return nil, false
	}
	ok := func() bool {
		defer func() { recover() }() // a refused question contributes nothing — never a crash
		for _, call := range directCalls {
			env, reached := reach(call)
			if !reached {
				return false
			}
			callExpr := call.AsCallExpression()
			for i := range fnParameters {
				var arg *ast.Node
				if callExpr.Arguments != nil && i < len(callExpr.Arguments.Nodes) {
					arg = callExpr.Arguments.Nodes[i]
				}
				if arg == nil {
					contribute(i, abstractdomain.Undef)
				} else {
					contribute(i, evaluateExpression(flowCtx, env, arg))
				}
			}
		}
		for _, call := range callbackCalls {
			env, reached := reach(call)
			if !reached {
				return false
			}
			callExpr := call.AsCallExpression()
			if !ast.IsPropertyAccessExpression(callExpr.Expression) {
				return false
			}
			receiver := evaluateExpression(flowCtx, env, callExpr.Expression.AsPropertyAccessExpression().Expression)
			offset := 0
			if callExpr.Expression.AsPropertyAccessExpression().Name().Text() == "reduce" {
				offset = 1
			}
			contribute(0+offset, ElementOf(receiver))
			contribute(1+offset, abstractdomain.KnownSet(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
			contribute(2+offset, receiver)
		}
		return true
	}()
	if !ok {
		return nil, false
	}
	bindings := Env{}
	for i, parameter := range fnParameters {
		value := joins[i]
		if value == nil || value.Kind == abstractdomain.KindUnknown {
			continue
		}
		BindParameter(p.Checker, parameter, *value, bindings)
	}
	if len(bindings) == 0 {
		return nil, false
	}
	return bindings, true
}

// reduceAccumulatorJoin is reduceAccumulatorJoin in the TS source: a
// reduce accumulator's honest value — the JOIN of everything it holds
// across the fold — the initial value and each step's result — run
// over an exact receiver the way the accumulation engine runs it
// (closures.ts). Nil where the receiver is not exact.
func reduceAccumulatorJoin(
	ctx *FlowContext,
	env Env,
	call *ast.Node,
	fn *ast.Node, // ArrowFunction | FunctionExpression
	receiver abstractdomain.AbstractValue,
) *abstractdomain.AbstractValue {
	body := fn.Body()
	if body == nil {
		return nil
	}
	fnParameters := fn.Parameters()
	callExpr := call.AsCallExpression()
	runStep := func(accumulator, item abstractdomain.AbstractValue, index *abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		callEnv := cloneEnv(env)
		bindings := Env{}
		if len(fnParameters) > 0 {
			BindParameter(ctx.P.Checker, fnParameters[0], accumulator, bindings)
		}
		if len(fnParameters) > 1 {
			BindParameter(ctx.P.Checker, fnParameters[1], item, bindings)
		}
		if len(fnParameters) > 2 && index != nil {
			BindParameter(ctx.P.Checker, fnParameters[2], *index, bindings)
		}
		for name, known := range bindings {
			callEnv[name] = known
		}
		if ast.IsBlock(body) {
			var sink []abstractdomain.AbstractValue
			sinkCtx := *ctx
			sinkCtx.ReturnSink = &sink
			AnalyzeStatement(&sinkCtx, callEnv, body, nil)
			if len(sink) > 0 {
				out := sink[0]
				for _, v := range sink[1:] {
					out = abstractdomain.JoinKnown(out, v)
				}
				return out
			}
			return silence.Residue()
		}
		return evaluateExpression(ctx, callEnv, body)
	}
	var items []abstractdomain.AbstractValue
	hasItems := false
	if receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveArray {
		hasItems = true
		for _, v := range receiver.Values {
			items = append(items, abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveNumber, abstractdomain.TrustLevelOf(receiver)))
		}
	} else if receiver.Kind == abstractdomain.KindList {
		hasItems = true
		items = append(items, receiver.Items...)
	}
	initialized := callExpr.Arguments != nil && len(callExpr.Arguments.Nodes) >= 2
	if !hasItems {
		// a SET receiver: the accumulator ranges over the certified
		// invariant the accumulation engine solves — the same engine
		// the reduce RESULT already rides (closures.ts)
		element := ElementOf(receiver)
		if element.Kind == abstractdomain.KindUnknown {
			return nil
		}
		var initial abstractdomain.AbstractValue
		if initialized {
			initial = evaluateExpression(ctx, env, callExpr.Arguments.Nodes[1])
		} else {
			initial = element
		}
		solved := SolveAccumulation(ctx, initial, func(accumulator abstractdomain.AbstractValue) abstractdomain.AbstractValue {
			return runStep(accumulator, element, nil)
		})
		if solved.Kind == abstractdomain.KindUnknown {
			return nil
		}
		return &solved
	}
	var accumulator abstractdomain.AbstractValue
	if initialized {
		accumulator = evaluateExpression(ctx, env, callExpr.Arguments.Nodes[1])
	} else if len(items) > 0 {
		accumulator = items[0]
	} else {
		accumulator = silence.Residue()
	}
	joined := accumulator
	var folded []abstractdomain.AbstractValue
	if initialized {
		folded = items
	} else if len(items) > 0 {
		folded = items[1:]
	}
	for i := 0; i < len(folded); i++ {
		var indexValue float64
		if initialized {
			indexValue = float64(i)
		} else {
			indexValue = float64(i + 1)
		}
		index := abstractdomain.KnownValues([]float64{indexValue}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		accumulator = runStep(accumulator, folded[i], &index)
		joined = abstractdomain.JoinKnown(joined, accumulator)
	}
	return &joined
}

// InitializeEnclosingCallbacks overlays what every callback enclosing
// `from` (itself included, when it is one) has pinned at its own call
// site — outermost first, so the innermost bindings win.
func InitializeEnclosingCallbacks(ctx CallSiteCtx, env Env, from *ast.Node) {
	var chain []*ast.Node
	for node := from; node != nil && !ast.IsSourceFile(node); node = node.Parent {
		if ast.IsArrowFunction(node) || ast.IsFunctionExpression(node) {
			chain = append(chain, node)
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		bindings, ok := CallSiteBindings(ctx, chain[i])
		if ok {
			for name, known := range bindings {
				env[name] = known
			}
		}
	}
}

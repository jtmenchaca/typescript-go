// from interprocedural/callback_outcome.ts
//
// The result of one synchronous callback call over map / filter /
// reduce / forEach / flatMap / find. `trackedName` is the receiver's
// own name when the receiver is a tracked binding — the callback's
// OWNER parameter (value, index, array) is that very receiver, so
// binding it makes the callback a write channel to it.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// parameterName reads a callback parameter's plain identifier name,
// (name, true), or ("", false) when absent or not a plain name.
func parameterName(callback Callback, index int) (string, bool) {
	parameters := callback.Parameters()
	if index < 0 || index >= len(parameters) {
		return "", false
	}
	name := parameters[index].AsParameterDeclaration().Name()
	if ast.IsIdentifier(name) {
		return name.Text(), true
	}
	return "", false
}

// CallbackOutcome is callbackOutcome in the TS source.
func CallbackOutcome(
	ctx *FlowContext,
	env Env,
	rawReceiver abstractdomain.AbstractValue,
	method string,
	call *ast.Node, // CallExpression
	arrow Callback,
	trackedName string,
	hasTrackedName bool,
	analyzers LoopAnalyzers,
) abstractdomain.AbstractValue {
	// the callback methods iterate ARRAYS: a word of another sort
	// carried here (a string through `any`) must not feed its
	// codepoints to the callback as numeric elements
	receiver := rawReceiver
	if rawReceiver.Kind == abstractdomain.KindValues && rawReceiver.KindTag != abstractdomain.PrimitiveArray {
		receiver = silence.ResidueOf("a word of another sort carried here must not feed its codepoints to the callback as numeric elements")
	}
	body := arrow.Body()
	if body == nil {
		return silence.Residue()
	}
	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}
	// the callback's body is THIS arrow's walk for snapshot purposes —
	// calls inside it must record under the arrow, not the outer
	// owner's SnapshotOwner (otherwise nested map/forEach bodies never
	// feed CallSiteBindings for the helpers they invoke)
	silent.SnapshotOwner = arrow

	callExpr := call.AsCallExpression()

	// a callback stored by `f.bind(...)`: the element binds the
	// target's parameter at the prebound offset (the bound [[Call]]
	// prepends the prebound arguments — see boundFunctionOf). The
	// prebound VALUES were fixed where the bind ran, in an environment
	// this walk does not hold — only a syntactic literal carries;
	// everything else binds unknown.
	var bound *BoundFunction
	if len(callExpr.Arguments.Nodes) > 0 {
		bound = StoredBoundFunctionOf(ctx, callExpr.Arguments.Nodes[0])
	}
	offset := 0
	if bound != nil && bound.Target == arrow {
		offset = bound.Offset
	}
	preboundBindings := map[string]abstractdomain.AbstractValue{}
	if bound != nil && bound.Target == arrow {
		arrowParameters := arrow.Parameters()
		for i, argument := range bound.PreboundArguments {
			var value abstractdomain.AbstractValue
			if SyntacticLiteral(argument) {
				value = analyzers.EvaluateExpression(&silent, NewEnv(), argument)
			} else {
				value = silence.ResidueOf("the prebound value was fixed where bind ran, in an environment this walk does not hold — only a syntactic literal carries")
			}
			var parameter *ast.Node
			if i < len(arrowParameters) {
				parameter = arrowParameters[i]
			}
			BindParameter(ctx.P.Checker, parameter, value, preboundBindings)
		}
	}
	parameterAt := func(index int) *ast.Node {
		parameters := arrow.Parameters()
		i := index + offset
		if i < 0 || i >= len(parameters) {
			return nil
		}
		return parameters[i]
	}
	nameAt := func(index int) (string, bool) {
		return parameterName(arrow, index+offset)
	}

	evalBody := func(reporting *FlowContext, bindings map[string]abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		// every body evaluation owns snapshots for calls inside this
		// arrow — including the reporting pass that runs on `ctx`
		owned := *reporting
		owned.SnapshotOwner = arrow
		callEnv := env.Clone()
		for name, known := range preboundBindings {
			callEnv.Set(name, known)
		}
		for name, known := range bindings {
			callEnv.Set(name, known)
		}
		if ast.IsBlock(body) {
			// the return sink collects what the block returns; its
			// judgments still report through `reporting`
			var sink []abstractdomain.AbstractValue
			sinkCtx := owned
			sinkCtx.ReturnSink = &sink
			analyzers.AnalyzeStatement(&sinkCtx, callEnv, body, nil)
			if len(sink) == 0 {
				return silence.Residue()
			}
			joined := sink[0]
			for _, v := range sink[1:] {
				joined = abstractdomain.JoinKnown(joined, v)
			}
			return joined
		}
		return analyzers.EvaluateExpression(&owned, callEnv, body)
	}

	reportPins := func(accumulator *abstractdomain.AbstractValue) map[string]abstractdomain.AbstractValue {
		return ArrayCallbackPins(arrayCallbackPinsParams{
			c:           ctx.P.Checker,
			fn:          arrow,
			receiver:    receiver,
			method:      method,
			accumulator: accumulator,
			bindOffset:  offset,
		})
	}

	// The body may write outer names — a callback is a write site. A
	// value-sorted word it only hands to calls travels by copy.
	forgetWrites := func() {
		written := map[string]struct{}{}
		AssignedNames(ctx.P.Checker, body, written)
		for name := range written {
			if _, ok := env.Get(name); ok {
				HavocEnv(ctx.Aliases, env, name)
			}
		}
	}

	element := ElementOf(receiver)
	elementParameter, hasElementParameter := nameAt(0)

	// the OWNER parameter is the receiver itself: bound, the callback
	// can write the array through it — invisibly to the outer-name
	// forget — so a bound owner drops the receiver's facts unless the
	// body provably only READS it, or the method models the writes
	// precisely (forEach over an exact tuple)
	ownerIndex := 2
	if method == "reduce" {
		ownerIndex = 3
	}
	ownerParameter, hasOwnerParameter := nameAt(ownerIndex)
	forgetOwner := func() {
		if hasOwnerParameter && hasTrackedName && WritesThrough(body, ownerParameter) {
			HavocEnv(ctx.Aliases, env, trackedName)
		}
	}

	finish := func(result abstractdomain.AbstractValue) abstractdomain.AbstractValue {
		forgetWrites()
		forgetOwner()
		return result
	}

	walk := &CallbackWalk{
		Ctx:                 ctx,
		Env:                 env,
		Receiver:            receiver,
		Method:              method,
		Call:                call,
		Arrow:               arrow,
		TrackedName:         trackedName,
		HasTrackedName:      hasTrackedName,
		Analyzers:           analyzers,
		Body:                body,
		Silent:              &silent,
		PreboundBindings:    preboundBindings,
		Element:             element,
		ElementParameter:    elementParameter,
		HasElementParameter: hasElementParameter,
		OwnerParameter:      ownerParameter,
		HasOwnerParameter:   hasOwnerParameter,
		ParameterAt:         parameterAt,
		NameAt:              nameAt,
		EvalBody:            evalBody,
		ReportPins:          reportPins,
		ForgetWrites:        forgetWrites,
		Finish:              finish,
	}

	switch method {
	case "map", "flatMap":
		return MapOutcome(walk, method)
	case "filter":
		return FilterOutcome(walk)
	case "reduce":
		return reduceOutcome(walk, callExpr)
	case "find":
		return findOutcome(walk, callExpr)
	case "forEach":
		return forEachOutcome(walk, callExpr)
	default:
		return finish(silence.Residue())
	}
}

// reduceOutcome is the "reduce" case of callbackOutcome's method
// switch — split out as its own function to keep CallbackOutcome
// under the file-length target; behavior is 1:1 with the TS source's
// inline switch case.
func reduceOutcome(walk *CallbackWalk, call *ast.CallExpression) abstractdomain.AbstractValue {
	ctx, env, receiver, body, silent := walk.Ctx, walk.Env, walk.Receiver, walk.Body, walk.Silent
	element, analyzers := walk.Element, walk.Analyzers
	ownerParameter, hasOwnerParameter := walk.OwnerParameter, walk.HasOwnerParameter
	parameterAt, nameAt, evalBody, reportPins, finish := walk.ParameterAt, walk.NameAt, walk.EvalBody, walk.ReportPins, walk.Finish

	parameterAcc := parameterAt(0)
	parameterVal := parameterAt(1)
	if parameterAcc == nil || parameterVal == nil {
		return finish(silence.Residue())
	}
	// a stated SUM MEASURE answers the plain fold outright: the
	// parse checked this very left fold (plain +, initialState 0), and
	// the fold is deterministic, so the total IS the measure
	if receiver.Kind == abstractdomain.KindSet && receiver.Measures != nil && receiver.Measures.HasSum &&
		len(call.Arguments.Nodes) >= 2 && !ast.IsBlock(body) && ast.IsBinaryExpression(body) {
		be := body.AsBinaryExpression()
		accName := parameterAcc.AsParameterDeclaration().Name()
		valName := parameterVal.AsParameterDeclaration().Name()
		if be.OperatorToken.Kind == ast.KindPlusToken &&
			ast.IsIdentifier(be.Left) && ast.IsIdentifier(be.Right) &&
			ast.IsIdentifier(accName) && ast.IsIdentifier(valName) &&
			((be.Left.Text() == accName.Text() && be.Right.Text() == valName.Text()) ||
				(be.Left.Text() == valName.Text() && be.Right.Text() == accName.Text())) &&
			ast.IsNumericLiteral(call.Arguments.Nodes[1]) &&
			float64(jsnum.FromString(call.Arguments.Nodes[1].Text())) == 0 {
			return finish(abstractdomain.KnownValues(
				[]float64{receiver.Measures.Sum},
				abstractdomain.PrimitiveNumber,
				abstractdomain.MinTrustLevel(abstractdomain.TrustLevelOf(receiver), abstractdomain.TrustSpec),
			))
		}
	}
	items := ItemsOf(receiver)
	initialized := len(call.Arguments.Nodes) >= 2
	// no initial value: the initialState is the first element (an empty
	// receiver throws at runtime — nothing to vouch for there)
	var initial abstractdomain.AbstractValue
	if initialized {
		initial = analyzers.EvaluateExpression(ctx, env, call.Arguments.Nodes[1])
	} else if items != nil {
		if len(items) > 0 {
			initial = items[0]
		} else {
			initial = silence.ResidueOf("no initial value on an empty receiver throws at runtime — nothing to vouch for there")
		}
	} else {
		initial = element
	}
	// an exact sequence: run the ABSTRACT fold, item by item
	// (silently). Each step is a sound abstraction of the concrete
	// step, so the FINAL accumulator stands even when a middle step
	// was unknown — a NaN item absorbing an unknown running sum is
	// exactly this case.
	var exactResult *abstractdomain.AbstractValue
	representative := initial
	if items != nil {
		folded := items
		firstIndex := 0
		// no initial value on a PROVABLY EMPTY receiver: sec-array.
		// prototype.reduce step 4 throws a TypeError there ("If length
		// = 0 and initialValue is not present, throw a TypeError
		// exception") — nothing completes, so `folded` stays the full
		// (empty) `items` rather than slicing past it. `initial` is
		// already the decline set above; the fold below runs zero
		// times and falls through to that decline, the same stance
		// oneArgumentReduceCallOf documents for this shape.
		if !initialized && len(items) > 0 {
			folded = items[1:]
			firstIndex = 1
		}
		accumulator := initial
		for i, item := range folded {
			bindings := map[string]abstractdomain.AbstractValue{}
			BindParameter(ctx.P.Checker, parameterAcc, accumulator, bindings)
			BindParameter(ctx.P.Checker, parameterVal, item, bindings)
			if indexParameter, ok := nameAt(2); ok {
				bindings[indexParameter] = abstractdomain.KnownValues([]float64{float64(i + firstIndex)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			}
			if hasOwnerParameter {
				bindings[ownerParameter] = receiver
			}
			accumulator = evalBody(silent, bindings)
			representative = abstractdomain.JoinKnown(representative, accumulator)
		}
		if accumulator.Kind != abstractdomain.KindUnknown {
			exactResult = &accumulator
		} else if len(folded) == 0 {
			// the fold ran zero times: accumulator is `initial` untouched,
			// which is already the exact, provably-known outcome for a
			// provably empty receiver with no initial value — the residue
			// this file's own comment above calls "the decline set above."
			// Falling through to SolveAccumulation would ask a question
			// about `element`'s recurrence that has nothing to do with why
			// this receiver is unknown (it is unknown because the runtime
			// throws, not because a fold step declined), discarding the
			// named reason for an unrelated, unnamed one.
			exactResult = &accumulator
		}
	}
	solved := silence.Residue()
	if exactResult == nil && element.Kind != abstractdomain.KindUnknown {
		solved = SolveAccumulation(ctx, initial, func(accumulator abstractdomain.AbstractValue) abstractdomain.AbstractValue {
			bindings := map[string]abstractdomain.AbstractValue{}
			BindParameter(ctx.P.Checker, parameterAcc, accumulator, bindings)
			BindParameter(ctx.P.Checker, parameterVal, element, bindings)
			return evalBody(silent, bindings)
		})
	}
	// ONE reporting pass — same pin law as hover
	if exactResult != nil {
		evalBody(ctx, reportPins(&representative))
		return finish(*exactResult)
	}
	evalBody(ctx, reportPins(&solved))
	return finish(solved)
}

// findOutcome is the "find" case of callbackOutcome's method switch.
func findOutcome(walk *CallbackWalk, call *ast.CallExpression) abstractdomain.AbstractValue {
	ctx, receiver, silent := walk.Ctx, walk.Receiver, walk.Silent
	ownerParameter, hasOwnerParameter := walk.OwnerParameter, walk.HasOwnerParameter
	parameterAt, nameAt, evalBody, reportPins, finish := walk.ParameterAt, walk.NameAt, walk.EvalBody, walk.ReportPins, walk.Finish

	// an exact sequence with a DECIDED predicate finds its item —
	// or the absent value when every verdict is false
	parameter0 := parameterAt(0)
	items := ItemsOf(receiver)
	if items != nil && parameter0 != nil {
		var found *abstractdomain.AbstractValue
		decided := true
		for i := 0; i < len(items) && found == nil; i++ {
			bindings := map[string]abstractdomain.AbstractValue{}
			BindParameter(ctx.P.Checker, parameter0, items[i], bindings)
			if indexParameter, ok := nameAt(1); ok {
				bindings[indexParameter] = abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			}
			if hasOwnerParameter {
				bindings[ownerParameter] = receiver
			}
			verdict, known := abstractdomain.TruthinessDecided(evalBody(silent, bindings))
			if !known {
				decided = false
				break
			}
			if verdict {
				found = &items[i]
			}
		}
		if decided {
			evalBody(ctx, reportPins(nil))
			if found != nil {
				return finish(*found)
			}
			return finish(abstractdomain.Undef)
		}
	}
	// the body's judgments report once; the result may be
	// undefined, which leaves the model — unknown, never wrong
	evalBody(ctx, reportPins(nil))
	return finish(silence.ResidueOf("the body's judgments report once; the result may be undefined, which leaves the model — unknown, never wrong"))
}

// forEachExactFold runs a forEach over an exactly known sequence —
// an exact array, or a complete literal-built Map/Set — element by
// element, the way reduceOutcome's exact fold does: each step is a
// sound abstraction of the concrete step, run in ORDER against one
// carried environment, so an outer accumulation (`sum = sum + age`)
// ends exact instead of havocked. One reporting pass walks the body
// under the JOIN of every step's entry state — the join admits each
// step's values and every transfer is monotone, so anything a step
// would have reported fires there too — and the carried finals
// replace the written names; no forget runs. (false, not handled)
// declines to the paths below untouched.
func forEachExactFold(walk *CallbackWalk, call *ast.CallExpression) (abstractdomain.AbstractValue, bool) {
	ctx, env, receiver, body, silent := walk.Ctx, walk.Env, walk.Receiver, walk.Body, walk.Silent
	analyzers := walk.Analyzers
	ownerParameter, hasOwnerParameter := walk.OwnerParameter, walk.HasOwnerParameter
	parameterAt := walk.ParameterAt

	declined := abstractdomain.AbstractValue{}
	// a second argument (thisArg) is not modeled here
	if len(call.Arguments.Nodes) != 1 {
		return declined, false
	}
	// the fold's per-step (value, key/index) pairs, in iteration order:
	// an array hands (element, index); a Map hands (value, key); a Set
	// hands its value twice (sec-set.prototype.foreach)
	var values []abstractdomain.AbstractValue
	var keys []abstractdomain.AbstractValue
	if receiver.Kind == abstractdomain.KindCollection && receiver.Complete {
		for _, entry := range receiver.Entries {
			if receiver.CollectionFlavor == abstractdomain.FlavorMap {
				values = append(values, entry.Value)
			} else {
				values = append(values, entry.Key)
			}
			keys = append(keys, entry.Key)
		}
	} else {
		items := ItemsOf(receiver)
		if items == nil {
			return declined, false
		}
		for i, item := range items {
			values = append(values, item)
			keys = append(keys, abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
		}
	}
	// a body that writes THROUGH the owner parameter rewrites the
	// receiver mid-iteration — the exact-tuple path below models that
	if hasOwnerParameter && WritesThrough(body, ownerParameter) {
		return declined, false
	}
	// a body that writes the receiver's own name (or an alias) moves
	// elements the cursor has not reached — the havoc path takes those
	if walk.HasTrackedName {
		bodyWritten := map[string]struct{}{}
		AssignedNames(ctx.P.Checker, body, bodyWritten)
		for member := range ctx.Aliases.ClassOf(walk.TrackedName) {
			if _, isWritten := bodyWritten[member]; isWritten {
				return declined, false
			}
		}
	}

	parameterValue := parameterAt(0)
	parameterKey := parameterAt(1)
	// the names the call convention binds — parameters and prebounds —
	// shadow same-named outer bindings, so the commit skips them and
	// their entry values stand
	shadowed := map[string]abstractdomain.AbstractValue{}
	for name, known := range walk.PreboundBindings {
		shadowed[name] = known
	}
	BindParameter(ctx.P.Checker, parameterValue, silence.Residue(), shadowed)
	BindParameter(ctx.P.Checker, parameterKey, silence.Residue(), shadowed)
	if hasOwnerParameter {
		shadowed[ownerParameter] = silence.Residue()
	}

	runOne := func(reporting *FlowContext, into Env, value, key abstractdomain.AbstractValue) {
		// every body evaluation owns snapshots for calls inside this
		// arrow — including the reporting pass (evalBody's own rule)
		owned := *reporting
		owned.SnapshotOwner = walk.Arrow
		bindings := map[string]abstractdomain.AbstractValue{}
		for name, known := range walk.PreboundBindings {
			bindings[name] = known
		}
		BindParameter(ctx.P.Checker, parameterValue, value, bindings)
		BindParameter(ctx.P.Checker, parameterKey, key, bindings)
		if hasOwnerParameter {
			bindings[ownerParameter] = receiver
		}
		for name, known := range bindings {
			into.Set(name, known)
		}
		if ast.IsBlock(body) {
			var sink []abstractdomain.AbstractValue
			owned.ReturnSink = &sink
			analyzers.AnalyzeStatement(&owned, into, body, nil)
			return
		}
		analyzers.EvaluateExpression(&owned, into, body)
	}

	carried := env.Clone()
	joined := env.Clone()
	for i := range values {
		joined.Range(func(name string, held abstractdomain.AbstractValue) bool {
			if v, ok := carried.Get(name); ok {
				joined.Set(name, abstractdomain.JoinKnown(held, v))
			}
			return true
		})
		runOne(silent, carried, values[i], keys[i])
	}

	// one reporting pass under the joins; a zero-element forEach never
	// runs its body, so nothing in it reports
	if len(values) > 0 {
		valueJoin := values[0]
		keyJoin := keys[0]
		for i := 1; i < len(values); i++ {
			valueJoin = abstractdomain.JoinKnown(valueJoin, values[i])
			keyJoin = abstractdomain.JoinKnown(keyJoin, keys[i])
		}
		report := joined.Clone()
		runOne(ctx, report, valueJoin, keyJoin)
	}

	// the exact finals replace the walk's own names — no forget
	env.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		if _, isShadowed := shadowed[name]; isShadowed {
			return true
		}
		if v, ok := carried.Get(name); ok {
			env.Set(name, v)
		}
		return true
	})
	// sec-array.prototype.foreach's last step is "Return undefined" —
	// forEach's own call result is that exact value unconditionally,
	// not a decline
	return abstractdomain.Undef, true
}

// forEachOutcome is the "forEach" case of callbackOutcome's method
// switch.
func forEachOutcome(walk *CallbackWalk, call *ast.CallExpression) abstractdomain.AbstractValue {
	ctx, env, body, silent := walk.Ctx, walk.Env, walk.Body, walk.Silent
	receiver, trackedName, hasTrackedName := walk.Receiver, walk.TrackedName, walk.HasTrackedName
	elementParameter, hasElementParameter := walk.ElementParameter, walk.HasElementParameter
	ownerParameter, hasOwnerParameter := walk.OwnerParameter, walk.HasOwnerParameter
	preboundBindings, analyzers := walk.PreboundBindings, walk.Analyzers
	nameAt, evalBody, reportPins, forgetWrites, finish := walk.NameAt, walk.EvalBody, walk.ReportPins, walk.ForgetWrites, walk.Finish

	// an exactly known sequence with no owner writes runs the exact
	// fold: outer accumulation lands, element by element, no havoc
	if answered, handled := forEachExactFold(walk, call); handled {
		return answered
	}

	// an exact tuple with a bound owner runs element by element:
	// writes through the owner land, and the FINAL tuple replaces
	// the receiver's facts — no havoc needed
	if hasOwnerParameter && hasTrackedName && hasElementParameter && receiver.Kind == abstractdomain.KindValues {
		tuple := receiver
		exact := true
		for i := 0; i < len(receiver.Values); i++ {
			if tuple.Kind != abstractdomain.KindValues || i >= len(tuple.Values) {
				exact = false
				break
			}
			bindings := map[string]abstractdomain.AbstractValue{
				elementParameter: abstractdomain.KnownValues([]float64{tuple.Values[i]}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved),
				ownerParameter:   tuple,
			}
			if indexParameter, ok := nameAt(1); ok {
				bindings[indexParameter] = abstractdomain.KnownValues([]float64{float64(i)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			}
			callEnv := env.Clone()
			for name, known := range preboundBindings {
				callEnv.Set(name, known)
			}
			for name, known := range bindings {
				callEnv.Set(name, known)
			}
			if ast.IsBlock(body) {
				var sink []abstractdomain.AbstractValue
				sinkCtx := *silent
				sinkCtx.ReturnSink = &sink
				analyzers.AnalyzeStatement(&sinkCtx, callEnv, body, nil)
			} else {
				analyzers.EvaluateExpression(silent, callEnv, body)
			}
			if v, ok := callEnv.Get(ownerParameter); ok {
				tuple = v
			} else {
				tuple = silence.Residue()
			}
		}
		evalBody(ctx, reportPins(nil))
		forgetWrites()
		if exact && tuple.Kind == abstractdomain.KindValues {
			UpdateTrackedEnv(ctx.Aliases, env, trackedName, tuple)
		} else {
			HavocEnv(ctx.Aliases, env, trackedName)
		}
		// sec-array.prototype.foreach's last step is "Return undefined"
		// — forEach's own call result is that exact value
		// unconditionally, not a decline
		return abstractdomain.Undef
	}
	evalBody(ctx, reportPins(nil))
	return finish(abstractdomain.Undef)
}

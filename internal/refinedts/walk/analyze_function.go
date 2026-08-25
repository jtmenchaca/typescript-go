// from control_flow/analyze_function.ts
//
// Judge one contract-bearing function: entry env, this/field
// invariants, dependent signature ledger, result check.
//
// EntryDependentConstraints (dataflow_facts/entry_dependent_constraints.ts)
// now lives in THIS package (walk/entry_dependent_constraints.go), not
// dataflowfacts — see that file's header for why: it needs
// annotations.DeclaredRefinement, and dataflowfacts cannot import
// annotations without closing a cross-package cycle.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/diagnose"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// AnalyzeFunction is analyzeFunction in the TS source: judge one
// contract-bearing function — parameters wear their stated sets, the
// body walks, returns check against the stated result. An arrow's
// expression body IS its returned value.
func AnalyzeFunction(outer *FlowContext, contract *FunctionContract, callSiteInitialStates map[string]abstractdomain.AbstractValue) {
	if !tracing.Recording(tracing.GrainStep) {
		analyzeFunctionBody(outer, contract, callSiteInitialStates)
		return
	}
	tracing.Span("analyzeFunction", func() any {
		analyzeFunctionBody(outer, contract, callSiteInitialStates)
		return nil
	}, tracing.GrainStep)
}

func analyzeFunctionBody(outer *FlowContext, contract *FunctionContract, callSiteInitialStates map[string]abstractdomain.AbstractValue) {
	body := contract.Declaration.Body()
	if body == nil {
		return
	}
	// each body carries its own declared invariants
	ctx := *outer
	ctx.Declared = map[string]*annotations.DeclaredRefinement{}
	ctx.CallSiteSeeded = callSiteInitialStates != nil
	// this IS the declaration's dedicated walk: its call sites
	// record their environments as read-once snapshots
	ctx.SnapshotOwner = contract.Declaration
	// a generator's stated yield position judges every `yield e` in
	// THIS body (yield_contract.go) — set per body, nil for every
	// non-generator, so a nested walk never wears an outer
	// generator's claim; an ungrounded contract judges nothing here,
	// the same as its result. The stated RESUME position (N) seeds
	// the same way, for a `yield e` read as its own value.
	ctx.YieldStated = nil
	ctx.YieldResumeStated = nil
	if contract.Grounded {
		ctx.YieldStated = contract.Yield
		ctx.YieldResumeStated = contract.YieldResume
	}

	env := NewEnv()
	parameters := contract.Declaration.Parameters()
	for i, parameter := range parameters {
		decl := parameter.AsParameterDeclaration()
		var stated *annotations.DeclaredRefinement
		if i < len(contract.Params) {
			stated = contract.Params[i]
		}
		if stated == nil {
			continue
		}
		// a DEFAULT value flows into the stated position whenever the
		// caller omits the argument — checked here, once, where it is
		// written. The parameter's OWN default checks against the
		// parameter's statement whatever its name spells: a
		// destructured parameter (`function f({x}: T = {x: 1})`) states
		// the same position an identifier parameter does, so the check
		// no longer waits on the name being an identifier.
		if decl.Initializer != nil {
			CheckAssignability(&ctx, evaluateExpression(&ctx, env, decl.Initializer), *stated, decl.Initializer, "a default value", nil)
		}
		// a default written INSIDE the pattern (`function f({x = 1}: T)`,
		// `function f([a = 0]: [N])`) fills its own key, not the whole
		// parameter, so it checks against that key's statement — walked
		// at every depth
		if name := decl.Name(); name != nil && ast.IsBindingPattern(name) {
			checkPatternDefaults(&ctx, env, name, stated)
		}
	}
	BindEntryEnv(BindEntryEnvInput{
		P: ctx.P, Env: env, Parameters: parameters, StatedParams: contract.Params,
		CallSiteInitialStates: callSiteInitialStates,
		OnStated: func(name string, stated *annotations.DeclaredRefinement) {
			ctx.Declared[name] = stated
		},
	})
	// a class METHOD's body holds `this`: the field invariants initialState
	// its keys (fields.ts), so `this.#x` reads answer and guards
	// narrow — an arrow field keeps the surrounding instance the
	// same way
	initialThisState := InitialThisStateOf(&ctx, contract.Declaration)
	if initialThisState != nil {
		env.Set("this", *initialThisState)
	}
	// a DEPENDENT signature holds inside its own body: the stated
	// relation between parameters initialStates the order ledger at entry, so
	// `hi - lo` and replayed comparisons know it without a guard
	initialStates := EntryDependentConstraints(ctx.P.Checker, parameters, contract.Params)
	registerDifferenceConstraints(&ctx, initialStates)
	bodyCtx := &ctx
	if len(initialStates) > 0 {
		next := ctx
		next.DifferenceConstraints = append(append([]dataflowfacts.DifferenceConstraint{}, ctx.DifferenceConstraints...), initialStates...)
		bodyCtx = &next
	}
	// an ungrounded contract's own result judges nothing (plain TS)
	var result *annotations.DeclaredRefinement
	if contract.Grounded {
		result = contract.Result
	}
	diagnose.LogIf(diagnose.EventOn("walk.function"), "walk.function",
		"function", functionLabel(contract.Declaration),
		"grounded", contract.Grounded,
		"resultPassed", result != nil,
	)
	if ast.IsBlock(body) {
		AnalyzeStatements(bodyCtx, env, body.AsBlock().Statements.Nodes, result)
		return
	}
	returned := evaluateExpression(bodyCtx, env, body)
	if result != nil {
		CheckAssignability(bodyCtx, returned, *result, body, "a returned value", nil)
	}
}

// checkPatternDefaults checks every default value written inside a
// binding pattern against the statement the position it fills makes,
// at any depth. `function f({x = 1}: T)` fills T's `x` key when the
// caller omits it, exactly as a plain parameter's default fills the
// parameter — so it owes the same check, and got none before this.
//
// The statement is walked down alongside the pattern: an object
// pattern's element takes its key's statement out of the DeclaredObject
// it is destructuring, and a nested pattern recurses under that key.
// Where the walk cannot name the position a default fills — an array
// pattern (an element index states no key here), a computed or
// non-identifier property name, a rest element (it holds the
// remainder, not one key), or a statement that is not an object —
// nothing is checked and nothing is claimed: the default is left
// unjudged rather than judged against the wrong statement.
func checkPatternDefaults(ctx *FlowContext, env Env, pattern *ast.Node, stated *annotations.DeclaredRefinement) {
	// a maybe-wrapped statement states its inner shape for the keys
	// the pattern names; absence is the caller's question, not the
	// default's — the default runs exactly when the value is absent
	for stated != nil && stated.Kind == annotations.DeclaredPossiblyUndefined {
		stated = stated.Inner
	}
	var keys map[string]annotations.ObjectKeySpec
	if stated != nil && stated.Kind == annotations.DeclaredObject && stated.Object != nil {
		keys = map[string]annotations.ObjectKeySpec{}
		for _, key := range stated.Object.Keys {
			keys[key.Name] = key
		}
	}
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		if element == nil || !ast.IsBindingElement(element) {
			// an omitted hole (`[, b]`) binds nothing
			continue
		}
		binding := element.AsBindingElement()
		// a rest element holds the REMAINDER, which no single key
		// states
		if binding.DotDotDotToken != nil {
			continue
		}
		// which key this element reads: its property name when it
		// renames (`{a: b}`), otherwise its own bound name
		var keyName string
		hasKey := false
		if binding.PropertyName != nil {
			if ast.IsIdentifier(binding.PropertyName) {
				keyName, hasKey = binding.PropertyName.Text(), true
			} else if ast.IsStringLiteral(binding.PropertyName) {
				keyName, hasKey = binding.PropertyName.Text(), true
			}
		} else if name := binding.Name(); name != nil && ast.IsIdentifier(name) {
			keyName, hasKey = name.Text(), true
		}
		// the statement this element's position makes, when the key
		// is named and the surrounding statement spells it
		var elementStated *annotations.DeclaredRefinement
		if hasKey && keys != nil {
			if key, found := keys[keyName]; found {
				switch key.Value.Kind {
				case annotations.KeyValueSet:
					elementStated = &annotations.DeclaredRefinement{
						Kind:     annotations.DeclaredSet,
						Set:      key.Value.Set,
						KindTag:  key.Value.KindTag,
						Measures: key.Value.Measures,
						Word:     key.Value.Word,
					}
				case annotations.KeyValueObject:
					elementStated = &annotations.DeclaredRefinement{
						Kind:   annotations.DeclaredObject,
						Object: key.Value.Object,
					}
				}
			}
		}
		if binding.Initializer != nil && elementStated != nil {
			CheckAssignability(ctx, evaluateExpression(ctx, env, binding.Initializer), *elementStated,
				binding.Initializer, "a default value", nil)
		}
		// a nested pattern's own defaults check one level down
		if name := binding.Name(); name != nil && ast.IsBindingPattern(name) {
			checkPatternDefaults(ctx, env, name, elementStated)
		}
	}
}

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

	env := NewEnv()
	parameters := contract.Declaration.Parameters()
	for i, parameter := range parameters {
		decl := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(decl.Name()) {
			continue
		}
		var stated *annotations.DeclaredRefinement
		if i < len(contract.Params) {
			stated = contract.Params[i]
		}
		// a DEFAULT value flows into the stated position whenever the
		// caller omits the argument — checked here, once, where it is
		// written
		if decl.Initializer != nil && stated != nil {
			CheckAssignability(&ctx, evaluateExpression(&ctx, env, decl.Initializer), *stated, decl.Initializer, "a default value", nil)
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
	if ast.IsBlock(body) {
		AnalyzeStatements(bodyCtx, env, body.AsBlock().Statements.Nodes, result)
		return
	}
	returned := evaluateExpression(bodyCtx, env, body)
	if result != nil {
		CheckAssignability(bodyCtx, returned, *result, body, "a returned value", nil)
	}
}

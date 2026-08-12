// from control_flow/flow_state_at_position.ts
//
// What the flow layer KNOWS at a hovered identifier — the inferred
// knowledge pass 3 computes, spelled in hover braces. This file is
// the dispatcher: kernel check, then one exit family at a time.
// Reach, type-seed, parameter bindings, and wording live beside it.
//
// CROSS-DIRECTORY: CallSiteBindings, InitializeEnclosingCallbacks are
// control_flow/call_site_bindings.ts's exported functions — that
// file is not yet ported (642 lines; deferred past this port unit,
// reported in full). InitializeEnclosingCallbacks' expected
// signature: `func(ctx CallSiteCtx, env Env, from *ast.Node)`.

package walk

import (
	"unicode"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/narrowing"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/scanner"
)

// AnnotateEdit mirrors one entry of annotateActionAt's `edits` array.
type AnnotateEdit struct {
	NewText string
	Start   int
	Length  int
}

// AnnotateAction mirrors annotateActionAt's return shape.
type AnnotateAction struct {
	Title string
	Edits []AnnotateEdit
}

// AnnotateActionAt is annotateActionAt in the TS source: the
// annotate-from-inference action — a plain parameter of a
// non-exported function, wearing the join of what its call sites
// pass, gains a STATED contract.
func AnnotateActionAt(p *program.CheckerProgram, registry annotations.AnnotationRegistry, objects annotations.ObjectRegistry, contracts map[*ast.Symbol]*FunctionContract, token *ast.Node) (AnnotateAction, bool) {
	kernel := kernelbridge.KernelIfLoaded()
	if kernel == nil {
		return AnnotateAction{}, false
	}
	parameter := token.Parent
	if !ast.IsParameterDeclaration(parameter) || parameter.AsParameterDeclaration().Name() != token {
		return AnnotateAction{}, false
	}
	if parameter.AsParameterDeclaration().Type != nil {
		return AnnotateAction{}, false
	}
	fn := parameter.Parent
	if !ast.IsFunctionDeclaration(fn) {
		return AnnotateAction{}, false
	}
	bindings, ok := CallSiteBindings(CallSiteCtx{P: p, Registry: registry, Objects: objects, Contracts: contracts, Kernel: kernel}, fn)
	if !ok {
		return AnnotateAction{}, false
	}
	held, ok := bindings[token.Text()]
	if !ok {
		return AnnotateAction{}, false
	}
	set, ok := abstractdomain.SetOfKnown(held)
	if !ok {
		return AnnotateAction{}, false
	}
	chain, ok := refinementsets.FormatAsSchemaChain(set)
	if !ok {
		return AnnotateAction{}, false
	}
	text := token.Text()
	schemaName := text
	if len(text) > 0 {
		runes := []rune(text)
		runes[0] = unicode.ToUpper(runes[0])
		schemaName = string(runes)
	}
	sourceFile := ast.GetSourceFileOfNode(token)
	fnStart := nodeStart(fn)
	lineStarts := scanner.GetECMALineStarts(sourceFile)
	line := scanner.GetECMALineOfPosition(sourceFile, fnStart)
	lineStart := int(lineStarts[line])
	sourceText := sourceFile.Text()
	indent := ""
	if lineStart <= fnStart && fnStart <= len(sourceText) {
		indent = sourceText[lineStart:fnStart]
	}
	parameterStart := nodeStart(parameter)
	return AnnotateAction{
		Title: "State the contract: " + text + " wears " + chain,
		Edits: []AnnotateEdit{
			{NewText: "const " + schemaName + " = " + chain + ";\n" + indent, Start: fnStart, Length: 0},
			{NewText: text + ": z.infer<typeof " + schemaName + ">", Start: parameterStart, Length: parameter.End() - parameterStart},
		},
	}, true
}

// AnswerFlowAt is answerFlowAt in the TS source.
func AnswerFlowAt(
	p *program.CheckerProgram,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	contracts map[*ast.Symbol]*FunctionContract,
	token *ast.Node,
) Answer {
	kernel := kernelbridge.KernelIfLoaded()
	if kernel == nil {
		return No(Unknown{Why: "kernel-not-loaded"})
	}

	symbol := p.Checker.GetSymbolAtLocation(token)
	var declaration *ast.Node
	if symbol != nil && len(symbol.Declarations) > 0 {
		declaration = symbol.Declarations[0]
	}

	if fromParameter, ok := AnswerParameter(p, registry, objects, contracts, kernel, token, declaration); ok {
		return fromParameter
	}

	if fromCrossFile, ok := AnswerCrossFile(p, kernel, token, declaration); ok {
		return fromCrossFile
	}

	binding := declaration
	hostType := p.Checker.GetTypeAtLocation(token)
	shownByHost := (hostType.Flags() & (checker.TypeFlagsNumberLiteral | checker.TypeFlagsStringLiteral |
		checker.TypeFlagsBooleanLiteral | checker.TypeFlagsBigIntLiteral |
		checker.TypeFlagsNull | checker.TypeFlagsUndefined)) != 0

	site, hasSite := SiteOf(p, contracts, token)
	if !hasSite {
		if fromExpressionBody, ok := AnswerExpressionBodiedParameter(p, kernel, token, binding); ok {
			return fromExpressionBody
		}
		return AnswerUnreached(p, kernel, token, binding, shownByHost)
	}

	parent := token.Parent
	writes := false
	if ast.IsVariableDeclaration(parent) && parent.AsVariableDeclaration().Name() == token {
		writes = true
	} else if ast.IsBindingElement(parent) && parent.AsBindingElement().Name() == token {
		writes = true
	} else if ast.IsBinaryExpression(parent) {
		bin := parent.AsBinaryExpression()
		if bin.Left == token && bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
			writes = true
		}
	} else if ast.IsPrefixUnaryExpression(parent) {
		unary := parent.AsPrefixUnaryExpression()
		if unary.Operand == token && (unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken) {
			writes = true
		}
	} else if ast.IsPostfixUnaryExpression(parent) {
		unary := parent.AsPostfixUnaryExpression()
		if unary.Operand == token && (unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken) {
			writes = true
		}
	}

	SetTransferKernel(kernel)
	narrowing.SetNarrowKernel(kernel)
	ctx := &FlowContext{
		P: p, Kernel: kernel, Registry: registry, Objects: objects, Contracts: contracts,
		Report:   func(d assignability.RefinementDiagnostic) {},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	env := Env{}
	siteCtx := CallSiteCtx{P: p, Registry: registry, Objects: objects, Contracts: contracts, Kernel: kernel}
	if site.Contract == nil && site.Fn != nil {
		InitializeEnclosingCallbacks(siteCtx, env, site.Fn)
	}
	callSite := Env{}
	if site.Fn != nil && ast.IsFunctionDeclaration(site.Fn) {
		if joined, ok := CallSiteBindings(siteCtx, site.Fn); ok {
			for name, known := range joined {
				callSite[name] = known
			}
		}
	}
	var callSiteInitialStates map[string]abstractdomain.AbstractValue
	if len(callSite) > 0 {
		callSiteInitialStates = callSite
	}
	var statedParams []*annotations.DeclaredRefinement
	if site.Contract != nil {
		statedParams = site.Contract.Params
	}
	BindEntryEnv(BindEntryEnvInput{
		P: p, Env: env, Parameters: site.Parameters, StatedParams: statedParams,
		CallSiteInitialStates: callSiteInitialStates,
	})
	if site.Fn != nil {
		if initialThisState := InitialThisStateOf(ctx, site.Fn); initialThisState != nil {
			env["this"] = *initialThisState
		}
	}

	reached := false
	var notes []assignability.ReasonNote
	assignability.BeginReasonNotes()
	broke, brokeMsg := func() (broke bool, msg string) {
		defer func() {
			if r := recover(); r != nil {
				broke = true
				if err, ok := r.(error); ok {
					msg = err.Error()
				} else if s, ok := r.(string); ok {
					msg = s
				} else {
					msg = "panic"
				}
			}
		}()
		reached = AnalyzeToToken(ctx, env, site.Statements, token, writes)
		return false, ""
	}()
	notes = assignability.EndReasonNotes()
	if broke {
		return No(Unknown{Why: "broke", Error: brokeMsg})
	}

	inParameterOfSite := false
	if site.Fn != nil {
		for node := token.Parent; node != nil; node = node.Parent {
			if ast.IsParameterDeclaration(node) {
				inParameterOfSite = node.Parent == site.Fn
				break
			}
			if ast.IsSourceFile(node) {
				break
			}
		}
	}
	if !reached && !inParameterOfSite {
		return AnswerUnreached(p, kernel, token, binding, shownByHost)
	}

	known, hasKnown := env[token.Text()]
	if !hasKnown {
		holder := binding
		for holder != nil && (ast.IsBindingElement(holder) || ast.IsArrayBindingPattern(holder) || ast.IsObjectBindingPattern(holder)) {
			holder = holder.Parent
		}
		if holder == nil || !ast.IsVariableDeclaration(holder) {
			holder = binding
		}
		var loop *ast.Node
		if holder != nil && ast.IsVariableDeclaration(holder) && holder.Parent != nil && ast.IsVariableDeclarationList(holder.Parent) {
			list := holder.Parent
			if list.Parent != nil && (ast.IsForOfStatement(list.Parent) || ast.IsForInStatement(list.Parent)) {
				loop = list.Parent
			}
		}
		if loop != nil {
			bodyEntry := Env{}
			SolveLoop(ctx, env, loop, nil, LoopAnalyzers{
				AnalyzeStatement:   AnalyzeStatement,
				EvaluateExpression: evaluateExpression,
				IterationElement:   IterationElementOf,
			}, bodyEntry)
			known, hasKnown = bodyEntry[token.Text()]
		}
	}
	if !hasKnown {
		if seeded, ok := TypeSeedAnswer(p, kernel, token, binding); ok {
			return seeded
		}
		return NoAnswer(p, token, binding, hostType, notes)
	}
	return AnswerHeldRow(p, kernel, token, binding, hostType, notes, known, shownByHost)
}

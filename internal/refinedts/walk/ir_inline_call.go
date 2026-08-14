// from control_flow/ir_inline_call.ts
//
// Call inlining for the flow IR: a resolvable pure callee lowers as
// its body in fresh slots, and an assignment of a call copies the
// result slot into the target.

package walk

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

var inlineSite = 0

// syntheticNodeFactory builds the one synthesized node this file
// needs (a bodiless expression's implicit return) — the Go twin of
// the TS source's global `ts.factory`.
var syntheticNodeFactory = &ast.NodeFactory{}

// InlineCallResult mirrors inlineCall's return shape.
type InlineCallResult struct {
	Stmts    []kernelbridge.IrStatement
	RetIndex int
	RetSort  BindingKind // "number" | "string"
}

// InlineCall is inlineCall in the TS source: a call to a resolvable
// callee, lowered as its body INLINED into fresh slots: arguments
// assign into parameter slots, the body lowers under a CLOSED name
// map (a free name declines, never captures the caller), and returns
// ride the callee's own done/ret pair — the same proved encoding
// every summary uses. The caller reads the result slot afterwards; a
// fall-off path leaves it absent, which IS the undefined return.
func InlineCall(context *LoweringContext, call *ast.Node) (InlineCallResult, bool) {
	resolveCallee, allocate, inlining := context.ResolveCallee, context.Allocate, context.Inlining
	if resolveCallee == nil || allocate == nil || inlining == nil {
		return InlineCallResult{}, false
	}
	callExpr := call.AsCallExpression()
	if callExpr.Arguments != nil {
		for _, a := range callExpr.Arguments.Nodes {
			if ast.IsSpreadElement(a) || ContainsWrite(a) {
				return InlineCallResult{}, false
			}
		}
	}
	callee := resolveCallee(callExpr.Expression)
	if callee == nil || callee.Body() == nil {
		return InlineCallResult{}, false
	}
	if _, seen := inlining[callee]; seen {
		return InlineCallResult{}, false
	}
	var callArguments []*ast.Node
	if callExpr.Arguments != nil {
		callArguments = callExpr.Arguments.Nodes
	}
	parameters := callee.Parameters()
	if len(callArguments) > len(parameters) {
		return InlineCallResult{}, false
	}
	paramNames := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) || pd.Initializer != nil || pd.DotDotDotToken != nil {
			return InlineCallResult{}, false
		}
		paramNames = append(paramNames, pd.Name().Text())
	}
	body := callee.Body()
	var statements []*ast.Node
	if ast.IsBlock(body) {
		statements = append([]*ast.Node{}, body.AsBlock().Statements.Nodes...)
	} else {
		statements = []*ast.Node{syntheticNodeFactory.NewReturnStatement(body)}
	}
	var locals []*ast.Node
	var patterns []*ast.Node
	if ast.IsBlock(body) {
		collected, collectedPatterns, ok := collectSummaryLocals(body)
		if !ok {
			return InlineCallResult{}, false
		}
		locals, patterns = collected, collectedPatterns
	}
	// the result slot's sort: string only where EVERY return spells a
	// SEQUENCE by its own syntax — a string literal, a template, or a
	// `+` chain of those; a mix has no one truthiness reading, so it
	// declines. This scan runs BEFORE the callee's slots exist, so it
	// reads no names: a return of a bare name is "other" here, exactly
	// as it was before templates and concatenation were recognized.
	sawString := false
	sawOther := false
	var scanReturns func(node *ast.Node)
	scanReturns = func(node *ast.Node) {
		if ast.IsReturnStatement(node) {
			rs := node.AsReturnStatement()
			if rs.Expression != nil {
				if SpelledSequenceShape(rs.Expression) {
					sawString = true
				} else {
					sawOther = true
				}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scanReturns(child)
			return false
		})
	}
	for _, statement := range statements {
		scanReturns(statement)
	}
	if sawString && sawOther {
		return InlineCallResult{}, false
	}
	retSort := BindingKindNumber
	if sawString {
		retSort = BindingKindString
	}
	site := inlineSite
	inlineSite++
	names := map[string]int{}
	var argAssigns []kernelbridge.IrStatement
	for i, paramName := range paramNames {
		var argument *ast.Node
		if i < len(callArguments) {
			argument = callArguments[i]
		}
		sort := BindingKindUnknown
		if argument != nil {
			sort = SortOfArg(context, argument)
		}
		var typeofTag TypeofTag
		if argument != nil {
			typeofTag = TypeofOfArg(context, argument)
		}
		slot, ok := allocate(fmt.Sprintf("#in%d:%s", site, paramName), sort, typeofTag)
		if !ok {
			return InlineCallResult{}, false
		}
		names[paramName] = slot
		if argument != nil {
			effect, ok := RhsEffect(context, sort, argument)
			if !ok {
				return InlineCallResult{}, false
			}
			argAssigns = append(argAssigns, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: slot, Effect: effect})
		}
	}
	// the callee's locals lay out exactly as the summary route's do: a
	// scalar takes one slot, a fixed-shape record one per LEAF ("p.a.b"),
	// a flattened array two ("a.len", "a.elem"), and a destructured name
	// one wearing its leaf's sort. The closed name map carries the
	// spelled names so the inlined body's own reads resolve.
	parameterNames := map[string]struct{}{}
	for _, paramName := range paramNames {
		parameterNames[paramName] = struct{}{}
	}
	var slotChecker *checker.Checker
	if context.Flow != nil && context.Flow.P != nil {
		slotChecker = context.Flow.P.Checker
	}
	for _, slot := range localSlotsOf(slotChecker, body, locals, patterns, parameterNames) {
		if _, exists := names[slot.Name]; exists {
			continue
		}
		index, ok := allocate(fmt.Sprintf("#in%d:%s", site, slot.Name), slot.Sort, slot.TypeofTag)
		if !ok {
			return InlineCallResult{}, false
		}
		names[slot.Name] = index
	}
	done, doneOk := allocate(fmt.Sprintf("#in%d:#done", site), BindingKindNumber, TypeofTagNumber)
	ret, retOk := allocate(fmt.Sprintf("#in%d:#ret", site), retSort, TypeofTagNone)
	if !doneOk || !retOk {
		return InlineCallResult{}, false
	}
	inlining[callee] = struct{}{}
	inner := &LoweringContext{
		Bindings:      context.Bindings,
		Sorts:         context.Sorts,
		Typeofs:       context.Typeofs,
		Narrow:        context.Narrow,
		Result:        &LoweringResult{Done: done, Ret: ret},
		Names:         names,
		ResolveCallee: resolveCallee,
		Inlining:      inlining,
		// the inlined body's own call sites take the summary route where
		// their callees have one — the table is the caller's, so every
		// callee this whole lowering names shares one index space
		Flow:         context.Flow,
		SummaryTable: context.SummaryTable,
	}
	// A NESTED inlining allocates through the owner's allocate, which
	// grows the owner's vectors. This context copied those vectors when it
	// was built, so it re-reads them after each growth — otherwise a slot
	// handed out below would index past this context's own Sorts.
	inner.Allocate = func(name string, sort BindingKind, typeofTag TypeofTag) (int, bool) {
		slot, allocated := allocate(name, sort, typeofTag)
		if !allocated {
			return 0, false
		}
		inner.Bindings = append(inner.Bindings, name)
		inner.Sorts = append(inner.Sorts, sort)
		inner.Typeofs = append(inner.Typeofs, typeofTag)
		return slot, true
	}
	lowered, ok := LowerStatements(inner, statements)
	delete(inlining, callee)
	if !ok {
		return InlineCallResult{}, false
	}
	stmts := append([]kernelbridge.IrStatement{}, argAssigns...)
	stmts = append(stmts, kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementAssign,
		Target: done,
		Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0}))},
	})
	stmts = append(stmts, lowered...)
	return InlineCallResult{Stmts: stmts, RetIndex: ret, RetSort: retSort}, true
}

// CallAssignmentOf is callAssignmentOf in the TS source: `let x =
// f(…)` / `x = f(…)` where f resolves — the callee inlines and the
// target copies its result slot.
func CallAssignmentOf(context *LoweringContext, s *ast.Node) ([]kernelbridge.IrStatement, bool) {
	var target int
	targetOk := false
	var rhs *ast.Node
	if ast.IsVariableStatement(s) {
		declarations := s.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return nil, false
		}
		d := declarations[0].AsVariableDeclaration()
		if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
			return nil, false
		}
		target, targetOk = IndexOf(context, d.Name())
		rhs = d.Initializer
	} else if ast.IsExpressionStatement(s) {
		e := s.AsExpressionStatement().Expression
		if !ast.IsBinaryExpression(e) || e.AsBinaryExpression().OperatorToken.Kind != ast.KindEqualsToken {
			return nil, false
		}
		target, targetOk = IndexOf(context, e.AsBinaryExpression().Left)
		rhs = e.AsBinaryExpression().Right
	}
	if !targetOk || rhs == nil {
		return nil, false
	}
	head := Unwrapped(rhs)
	if !ast.IsCallExpression(head) {
		return nil, false
	}
	inlined, ok := InlineCall(context, head)
	if !ok {
		return nil, false
	}
	out := append([]kernelbridge.IrStatement{}, inlined.Stmts...)
	out = append(out, kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementAssign,
		Target: target,
		Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: inlined.RetIndex},
	})
	return out, true
}

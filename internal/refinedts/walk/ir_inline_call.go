// from control_flow/ir_inline_call.ts
//
// Call inlining for the flow IR: a resolvable pure callee lowers as
// its body in fresh slots, and an assignment of a call copies the
// result slot into the target.

package walk

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast"
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
	if ast.IsBlock(body) {
		result, ok := CollectLocals(body)
		if !ok {
			return InlineCallResult{}, false
		}
		locals = result.Locals
	}
	// the result slot's sort: string only where EVERY return spells a
	// string literal; a mix has no one truthiness reading — decline
	sawString := false
	sawOther := false
	var scanReturns func(node *ast.Node)
	scanReturns = func(node *ast.Node) {
		if ast.IsReturnStatement(node) {
			rs := node.AsReturnStatement()
			if rs.Expression != nil {
				if ast.IsStringLiteral(Unwrapped(rs.Expression)) {
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
	// a fixed-shape record local flattens into one slot per key here
	// too — the closed name map carries "p.lo" so the inlined body's
	// own steps resolve, mirroring lowerSummary's flattening
	inlineObjectLocals := ObjectLocalsOf(body, locals)
	for _, declaration := range locals {
		name := declaration.AsVariableDeclaration().Name().AsIdentifier().Text
		if _, exists := names[name]; exists {
			continue
		}
		if local, flattened := inlineObjectLocals[declaration]; flattened {
			for _, key := range local.Keys {
				slot, ok := allocate(fmt.Sprintf("#in%d:%s", site, key.SlotName), ObjectLocalKeySort(key), ObjectLocalKeyTypeof(key))
				if !ok {
					return InlineCallResult{}, false
				}
				names[key.SlotName] = slot
			}
			continue
		}
		slot, ok := allocate(fmt.Sprintf("#in%d:%s", site, name), LocalSort(declaration), LocalTypeof(declaration))
		if !ok {
			return InlineCallResult{}, false
		}
		names[name] = slot
	}
	done, doneOk := allocate(fmt.Sprintf("#in%d:#done", site), BindingKindNumber, TypeofTagNumber)
	ret, retOk := allocate(fmt.Sprintf("#in%d:#ret", site), retSort, TypeofTagNone)
	if !doneOk || !retOk {
		return InlineCallResult{}, false
	}
	inlining[callee] = struct{}{}
	lowered, ok := LowerStatements(&LoweringContext{
		Bindings:      context.Bindings,
		Sorts:         context.Sorts,
		Typeofs:       context.Typeofs,
		Narrow:        context.Narrow,
		Result:        &LoweringResult{Done: done, Ret: ret},
		Names:         names,
		ResolveCallee: resolveCallee,
		Allocate:      allocate,
		Inlining:      inlining,
	}, statements)
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

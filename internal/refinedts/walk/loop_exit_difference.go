// from control_flow/loop_exit_difference.ts
//
// The refuted loop condition's negated difference row for everything
// after the loop, including the unit-step closure that pins i = n.
//
// CROSS-DIRECTORY: DifferenceConstraint.Minuend/Subtrahend/Bound/Strict
// and dataflowfacts.NoteExitConstraints are already ported
// (dataflowfacts/linear_constraints.go, exit_constraints.go).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// NoteNegatedLoopExitInput mirrors noteNegatedLoopExit's
// destructured parameter.
type NoteNegatedLoopExitInput struct {
	Ctx                  *FlowContext
	Loop                 *ast.Node
	Condition            *ast.Node
	ConditionConstraints []dataflowfacts.DifferenceConstraint
	After                Env
}

// NoteNegatedLoopExit is noteNegatedLoopExit in the TS source: the
// REFUTED condition also NEGATES its difference row for everything
// after the loop: `i < xs.length` failing means i ≥ xs.length. Sound
// only when the condition IS one comparison (¬(A ∧ B) negates
// neither conjunct), the exit came through the test (no break), and
// both places provably exclude NaN.
func NoteNegatedLoopExit(input NoteNegatedLoopExitInput) {
	ctx := input.Ctx
	loop := input.Loop
	var statement *ast.Node
	switch {
	case ast.IsForStatement(loop):
		statement = loop.AsForStatement().Statement
	case ast.IsWhileStatement(loop):
		statement = loop.AsWhileStatement().Statement
	case ast.IsForOfStatement(loop), ast.IsForInStatement(loop):
		statement = loop.AsForInOrOfStatement().Statement
	}
	if ast.IsDoStatement(loop) || ContainsBreak(statement) || len(input.ConditionConstraints) != 1 {
		return
	}
	bare := input.Condition
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	var kind ast.Kind
	hasKind := false
	if ast.IsBinaryExpression(bare) {
		kind = bare.AsBinaryExpression().OperatorToken.Kind
		hasKind = true
	}
	single := hasKind && (kind == ast.KindLessThanToken || kind == ast.KindLessThanEqualsToken ||
		kind == ast.KindGreaterThanToken || kind == ast.KindGreaterThanEqualsToken)
	real := func(baseName, path string) bool {
		known, ok := input.After.Get(baseName)
		if !ok {
			return false
		}
		if path == "" {
			return (known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone) ||
				(known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber)
		}
		if path == ".length" {
			return known.Kind == abstractdomain.KindSet || known.Kind == abstractdomain.KindList ||
				(known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveArray)
		}
		return false
	}
	row := input.ConditionConstraints[0]
	if !(single && real(row.Minuend.BaseName, row.Minuend.Path) && real(row.Subtrahend.BaseName, row.Subtrahend.Path)) {
		return
	}
	exitConstraints := []dataflowfacts.DifferenceConstraint{{
		Minuend:    row.Subtrahend,
		Subtrahend: row.Minuend,
		Bound:      -row.Bound,
		Strict:     !row.Strict,
	}}
	// the UNIT-STEP closure: `for (i = v; i < n; i++)` never steps
	// past n — i starts at v ≤ n's floor, and every increment ran
	// under i < n, so i ≤ n survives the loop. With the negated
	// row above, the exit pins i = n.
	if ast.IsForStatement(loop) && row.Strict && row.Bound == 0 &&
		row.Subtrahend.Path == "" && loop.AsForStatement().Incrementor != nil {
		forStmt := loop.AsForStatement()
		stepped := func() (string, bool) {
			inc := forStmt.Incrementor
			if ast.IsPostfixUnaryExpression(inc) {
				u := inc.AsPostfixUnaryExpression()
				if u.Operator == ast.KindPlusPlusToken && ast.IsIdentifier(u.Operand) {
					return u.Operand.Text(), true
				}
			}
			if ast.IsPrefixUnaryExpression(inc) {
				u := inc.AsPrefixUnaryExpression()
				if u.Operator == ast.KindPlusPlusToken && ast.IsIdentifier(u.Operand) {
					return u.Operand.Text(), true
				}
			}
			if ast.IsBinaryExpression(inc) {
				bin := inc.AsBinaryExpression()
				if bin.OperatorToken.Kind == ast.KindPlusEqualsToken && ast.IsIdentifier(bin.Left) &&
					ast.IsNumericLiteral(bin.Right) && jsnum.FromString(bin.Right.AsNumericLiteral().Text) == 1 {
					return bin.Left.Text(), true
				}
			}
			return "", false
		}
		steppedName, hasStepped := stepped()
		entry := func() (float64, bool) {
			init := forStmt.Initializer
			if init == nil {
				return 0, false
			}
			// `for (let i = 0; …)` and `for (i = 0; …)` both pin the
			// entry value
			if ast.IsVariableDeclarationList(init) {
				declarations := init.AsVariableDeclarationList().Declarations.Nodes
				if len(declarations) != 1 {
					return 0, false
				}
				decl := declarations[0].AsVariableDeclaration()
				if ast.IsIdentifier(decl.Name()) && decl.Name().Text() == row.Subtrahend.BaseName &&
					decl.Initializer != nil && ast.IsNumericLiteral(decl.Initializer) {
					return float64(jsnum.FromString(decl.Initializer.AsNumericLiteral().Text)), true
				}
				return 0, false
			}
			if ast.IsBinaryExpression(init) {
				bin := init.AsBinaryExpression()
				if bin.OperatorToken.Kind == ast.KindEqualsToken && ast.IsIdentifier(bin.Left) &&
					bin.Left.Text() == row.Subtrahend.BaseName && ast.IsNumericLiteral(bin.Right) {
					return float64(jsnum.FromString(bin.Right.AsNumericLiteral().Text)), true
				}
			}
			return 0, false
		}
		entryValue, hasEntry := entry()
		bound := func() (float64, bool) {
			if row.Minuend.Path == ".length" {
				return 0, true
			}
			if row.Minuend.Path != "" {
				return 0, false
			}
			r := RangeOfKnown(envOrResidue(input.After, row.Minuend.BaseName))
			if r == nil {
				return 0, false
			}
			return r.Lo, true
		}
		boundValue, hasBound := bound()
		if hasStepped && steppedName == row.Subtrahend.BaseName && hasEntry && hasBound && entryValue <= boundValue {
			exitConstraints = append(exitConstraints, dataflowfacts.DifferenceConstraint{
				Minuend:    row.Minuend,
				Subtrahend: row.Subtrahend,
				Bound:      0,
				Strict:     false,
			})
		}
	}
	registerDifferenceConstraints(ctx, exitConstraints)
	dataflowfacts.NoteExitConstraints(loop, exitConstraints)
}

// from control_flow/answers/reach_answer.ts
//
// Exits 4 and 6: no walkable site, or the walk stopped short.
// literalConstClaim → typeSeedAnswer → out-of-reach. Exit 4 also
// reads an expression-bodied parameter when nothing in the body
// writes the name.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// writeReachesToken answers whether a write to the token's name can
// run before the token itself does, inside the holder's body — the
// question the entry-state claim below rests on.
//
// A BLOCK body runs statements in sequence and this reading walks
// none of them, so any write anywhere in it voids the claim. An
// EXPRESSION body is one expression evaluated left to right, so only
// a write standing to the LEFT of the token can precede it:
// `(x = f(x))` writes before the outer read, while `x.length` and
// `x + (y = 1)` leave x holding what entry gave it. A write inside a
// nested function is not run in place and cannot be ordered against
// the token at all, so it voids the claim wherever it stands.
func writeReachesToken(body *ast.Node, token *ast.Node) bool {
	name := token.Text()
	if ast.IsBlock(body) {
		written := map[string]struct{}{}
		AssignedNamesDirect(body, written)
		_, isWritten := written[name]
		return isWritten
	}
	tokenStart := nodeStart(token)
	reaches := false
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if reaches {
			return
		}
		if ast.IsFunctionLike(node) {
			written := map[string]struct{}{}
			AssignedNamesDirect(node, written)
			if _, isWritten := written[name]; isWritten {
				reaches = true
			}
			return
		}
		if target, isWrite := writtenTargetOf(node); isWrite &&
			ast.IsIdentifier(target) && target.Text() == name && nodeStart(target) < tokenStart {
			reaches = true
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return reaches
		})
	}
	scan(body)
	return reaches
}

// writtenTargetOf reads the place a write form writes to: an
// assignment's left side, or the operand of ++/--.
func writtenTargetOf(node *ast.Node) (*ast.Node, bool) {
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
			return bin.Left, true
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return unary.Operand, true
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			return unary.Operand, true
		}
	}
	return nil, false
}

// AnswerExpressionBodiedParameter is answerExpressionBodiedParameter
// in the TS source.
func AnswerExpressionBodiedParameter(p *program.CheckerProgram, kernel *kernelbridge.RefinedTSKernel, token *ast.Node, declaration *ast.Node) (Answer, bool) {
	var parameterHolding *ast.Node
	for node := declaration; node != nil; node = node.Parent {
		if ast.IsParameterDeclaration(node) {
			parameterHolding = node
			break
		}
		if !ast.IsBindingElement(node) && !ast.IsObjectBindingPattern(node) && !ast.IsArrayBindingPattern(node) {
			break
		}
	}
	var holderBody *ast.Node
	if parameterHolding != nil && ast.IsFunctionLike(parameterHolding.Parent) {
		holderBody = parameterHolding.Parent.Body()
	}
	if parameterHolding == nil || holderBody == nil {
		return Answer{}, false
	}
	if writeReachesToken(holderBody, token) {
		return Answer{}, false
	}
	var held abstractdomain.AbstractValue
	hasHeld := false
	ReadDestructuring(parameterHolding.AsParameterDeclaration().Name(), InitialStateOfPlainParameter(p, parameterHolding), func(name string, slot abstractdomain.AbstractValue, at *ast.Node) {
		if name == token.Text() {
			held = silence.SeededBinding(p.Checker, slot, at)
			hasHeld = true
		}
	})
	if !hasHeld || held.Kind == abstractdomain.KindUnknown {
		return Answer{}, false
	}
	plain := held
	if held.Kind == abstractdomain.KindSet {
		plain.Set = refinementsets.SimplifyScalar(kernelSimplificationAdapter{kernel}, held.Set)
	}
	if words, hasWords := abstractdomain.FormatAbstractValue(plain); hasWords {
		answer := Claim(words, abstractdomain.TrustLevelOf(plain), false)
		answer.SortWord = abstractdomain.ScalarSortWordOfKnown(plain)
		return answer, true
	}
	if held.Kind == abstractdomain.KindPossiblyUndefined {
		return No(Unknown{Why: "noted", Said: Sentence.PresentAndAbsent, Unsupported: false}), true
	}
	return No(Unknown{Why: "noted", Said: Sentence.WalkStatesNothing, Unsupported: false}), true
}

// AnswerUnreached is answerUnreached in the TS source: exit 4 (no
// site) or exit 6 (walk stopped short) — literal const, then type
// seed, then out-of-reach.
func AnswerUnreached(p *program.CheckerProgram, kernel *kernelbridge.RefinedTSKernel, token *ast.Node, declaration *ast.Node, shownByHost bool) Answer {
	if claim, ok := LiteralConstClaim(p, declaration, shownByHost); ok {
		return claim
	}
	if seeded, ok := TypeSeedAnswer(p, kernel, token, declaration); ok {
		return seeded
	}
	return No(Unknown{Why: "out-of-reach"})
}

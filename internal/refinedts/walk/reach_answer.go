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
	written := map[string]struct{}{}
	AssignedNamesDirect(holderBody, written)
	if _, isWritten := written[token.Text()]; isWritten {
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
		return Claim(words, abstractdomain.TrustLevelOf(plain), false), true
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

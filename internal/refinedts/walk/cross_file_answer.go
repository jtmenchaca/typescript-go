// from control_flow/answers/cross_file_answer.ts
//
// Exit 3: a name declared outside the entry file (import, re-export).
// readHostType at the token; words → claim; nothing → not-tracked.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// AnswerCrossFile is answerCrossFile in the TS source.
func AnswerCrossFile(p *program.CheckerProgram, kernel *kernelbridge.RefinedTSKernel, token *ast.Node, declaration *ast.Node) (Answer, bool) {
	// cross-file means CROSS-FILE: any declaration in the entry file —
	// a variable, a binding element, a parameter — belongs to the
	// walk's own exits, and answering it here would render the type as
	// a claim past their gates
	if declaration != nil && ast.GetSourceFileOfNode(declaration) == p.Entry {
		return Answer{}, false
	}
	worn, ok := typereading.ReadHostType(p.Checker, typereading.TypeAtLocation(p.Checker, token), token, 0)
	if ok && worn.Kind != abstractdomain.KindUnknown {
		plain := worn
		if worn.Kind == abstractdomain.KindSet {
			plain.Set = refinementsets.SimplifyScalar(kernelSimplificationAdapter{kernel}, worn.Set)
		}
		if words, hasWords := abstractdomain.FormatAbstractValue(plain); hasWords {
			answer := Claim(words, abstractdomain.TrustLevelOf(plain), false)
			answer.SortWord = abstractdomain.ScalarSortWordOfKnown(plain)
			return answer, true
		}
		return No(Unknown{Why: "noted", Said: Sentence.WalkStatesNothing, Unsupported: false}), true
	}
	return No(Unknown{Why: "not-tracked"}), true
}

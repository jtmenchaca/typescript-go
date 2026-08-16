// from service/hover_flow_answer.ts
//
// The flow-walk half of answerAt: what the walk knows when nothing is
// stated at the name — transform images, unread chains, inferred
// literal unions, and the not-tracked parameter sentence.

package service

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// FlowAnswerAt is flowAnswerAt in the TS source: what the flow walk
// knows at this token when no stated refinement answered first.
func FlowAnswerAt(
	p *program.CheckerProgram,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	contracts map[*ast.Symbol]*walk.FunctionContract,
	token *ast.Node,
	symbol *ast.Symbol,
) walk.Answer {
	// nothing stated: what the flow walk knows here
	flow := walk.AnswerFlowAt(p, registry, objects, contracts, token)
	if flow.HasKnown {
		return flow
	}
	for _, declaration := range symbol.Declarations {
		if !ast.IsVariableDeclaration(declaration) || declaration.AsVariableDeclaration().Initializer == nil {
			continue
		}
		initializer := declaration.AsVariableDeclaration().Initializer
		// a `.transform(cb)` schema: the parse output IS cb(validated
		// input) — the binding's answer is the image of the callback
		// over the receiver's compiled set
		if image, ok := TransformImage(p, registry, initializer); ok {
			return image
		}
		// any other annotation statement the reader did NOT compile —
		// the chain roots in an annotation module and produces a
		// schema, not a parsed value. The walk has nothing to say
		// about surface calls, so the reader's gap is the truer
		// sentence.
		if UnreadAnnotationChain(p, initializer) {
			return walk.No(walk.Unknown{
				Why:         "noted",
				Said:        "an annotation form the reader does not read yet",
				Unsupported: true,
			})
		}
	}
	// the walk holds nothing, but tsc INFERRED a union of literals for
	// this name: the inferred type states the set in TypeScript's own
	// words, and this position wears it
	if inferred, ok := LiteralUnionOfType(p, token); ok {
		return inferred
	}
	// an untracked PARAMETER: its values arrive at call sites the walk
	// did not enter, so inside this file the type is the whole
	// statement — a literal-free type is a finished determination, not
	// a reader gap
	if flow.UnknownValue.Why == "not-tracked" {
		isParameter := false
		for _, declaration := range symbol.Declarations {
			if ast.IsParameterDeclaration(declaration) {
				isParameter = true
				break
			}
		}
		if isParameter && !LiteralBearingType(p, p.Checker.GetTypeAtLocation(token), 0, map[*checker.Type]bool{}, token) {
			return walk.No(walk.Unknown{
				Why:  "noted",
				Said: "the type states no refinement to read",
			})
		}
	}
	return flow
}

// from annotations/contract_file_facts.ts
//
// Pass 2 for one file: function/method/arrow contracts and const
// aliases of contracted functions.
//
// This file needs FunctionContract (flow_context.go, this same
// package) — TS's own import from evaluation/flow_context.ts. Since
// walk already imports annotations one-way for DeclaredRefinement and
// kin, porting this file INTO annotations would close a two-way
// cycle (annotations -> walk -> annotations); per PORT.md's cycle
// rule it joins package walk instead, the same resolution
// worn_annotation.go took (walk-integration-punchlist.md item 10).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// contractSignature is readSignature's return shape in the TS source.
type contractSignature struct {
	Params   []*annotations.DeclaredRefinement
	Result   *annotations.DeclaredRefinement
	Grounded bool
}

// CompileContractFileFacts is compileContractFileFacts in the TS
// source.
func CompileContractFileFacts(
	p *program.CheckerProgram,
	file *ast.SourceFile,
	registry annotations.AnnotationRegistry,
	objects annotations.ObjectRegistry,
	mergedContracts map[*ast.Symbol]*FunctionContract,
	reporting bool,
	report func(d assignability.RefinementDiagnostic),
) map[*ast.Symbol]*FunctionContract {
	contracts := map[*ast.Symbol]*FunctionContract{}

	var positionGrounds func(s *annotations.DeclaredRefinement) bool
	positionGrounds = func(s *annotations.DeclaredRefinement) bool {
		if s == nil {
			return false
		}
		if s.Kind == annotations.DeclaredSet || s.Kind == annotations.DeclaredObject || s.Kind == annotations.DeclaredObjectArray {
			return true
		}
		if s.Kind == annotations.DeclaredVariable && s.BoundGrounded {
			return true
		}
		if s.Kind == annotations.DeclaredPossiblyUndefined {
			return positionGrounds(s.Inner)
		}
		return false
	}

	readSignature := func(fn *ast.Node) contractSignature {
		parameters := fn.Parameters()
		params := make([]*annotations.DeclaredRefinement, len(parameters))
		for i, parameter := range parameters {
			pd := parameter.AsParameterDeclaration()
			if pd.Type == nil {
				continue
			}
			read := annotations.AnnotationOfType(p, pd.Type, registry, objects)
			if read.Stated == nil {
				if read.Unsupported != "" {
					if reporting {
						report(assignability.At(pd.Type, 7004, read.Unsupported))
					}
				}
				continue
			}
			// an OPTIONAL parameter (`options?: {...}`) admits the absent
			// value at the position itself — a caller passing undefined is
			// legal, so the maybe wraps the statement
			if pd.QuestionToken != nil && read.Stated.Kind != annotations.DeclaredPossiblyUndefined {
				params[i] = &annotations.DeclaredRefinement{Kind: annotations.DeclaredPossiblyUndefined, Inner: read.Stated}
				continue
			}
			params[i] = read.Stated
		}
		var result *annotations.DeclaredRefinement
		returnType := fn.Type()
		if returnType != nil {
			read := annotations.AnnotationOfType(p, returnType, registry, objects)
			if read.Stated != nil {
				result = read.Stated
			} else if read.Unsupported != "" && reporting {
				report(assignability.At(returnType, 7004, read.Unsupported))
			}
		}
		grounded := false
		for _, param := range params {
			if positionGrounds(param) {
				grounded = true
				break
			}
		}
		if !grounded {
			grounded = positionGrounds(result)
		}
		return contractSignature{Params: params, Result: result, Grounded: grounded}
	}

	register := func(nameNode *ast.Node, declaration *ast.Node, signature contractSignature) {
		symbol := p.Checker.GetSymbolAtLocation(nameNode)
		if symbol == nil {
			return
		}
		contract := &FunctionContract{
			Declaration: declaration,
			Params:      signature.Params,
			Result:      signature.Result,
			Grounded:    signature.Grounded,
		}
		contracts[symbol] = contract
		mergedContracts[symbol] = contract
	}

	var collect func(node *ast.Node)
	collect = func(node *ast.Node) {
		switch {
		case ast.IsFunctionDeclaration(node) && node.Name() != nil:
			register(node.Name(), node, readSignature(node))
		case ast.IsMethodDeclaration(node):
			register(node.Name(), node, readSignature(node))
		case (ast.IsPropertyAssignment(node) || ast.IsPropertyDeclaration(node)) &&
			propertyInitializer(node) != nil &&
			(ast.IsArrowFunction(propertyInitializer(node)) || ast.IsFunctionExpression(propertyInitializer(node))):
			initializer := propertyInitializer(node)
			register(node.Name(), initializer, readSignature(initializer))
		case ast.IsVariableDeclaration(node) && ast.IsIdentifier(node.Name()):
			vd := node.AsVariableDeclaration()
			if vd.Initializer != nil &&
				(ast.IsArrowFunction(vd.Initializer) || ast.IsFunctionExpression(vd.Initializer)) &&
				ast.IsVariableDeclarationList(node.Parent) &&
				(node.Parent.Flags&ast.NodeFlagsConst) != 0 {
				// a CONST arrow registers whether or not a position states a
				// refinement — function declarations register unconditionally,
				// and the asymmetry left every imported `const f = (x) => …`
				// unknown at its cross-file call sites (the recharts mathSign
				// sweep finding, 2026-08-08). CONST only: a let-bound closure
				// reassigned between calls would prove through its stale first
				// body (the soundness battery's own case)
				register(node.Name(), vd.Initializer, readSignature(vd.Initializer))
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			collect(child)
			return false
		})
	}
	tracing.Span("facts.compile.signatures", func() any {
		collect(file.AsNode())
		return nil
	}, tracing.GrainStep)

	// const aliases of contracted functions (old pass-2 second loop)
	tracing.Span("facts.compile.aliases", func() any {
		for _, statement := range file.Statements.Nodes {
			if !ast.IsVariableStatement(statement) {
				continue
			}
			declarationList := statement.AsVariableStatement().DeclarationList
			if (declarationList.Flags & ast.NodeFlagsConst) == 0 {
				continue
			}
			for _, declaration := range declarationList.AsVariableDeclarationList().Declarations.Nodes {
				vd := declaration.AsVariableDeclaration()
				if vd.Initializer == nil || !ast.IsIdentifier(vd.Initializer) || !ast.IsIdentifier(vd.Name()) {
					continue
				}
				target := symbolAt(p.Checker, vd.Initializer)
				if target == nil {
					continue
				}
				contract, ok := mergedContracts[target]
				if !ok {
					continue
				}
				alias := p.Checker.GetSymbolAtLocation(vd.Name())
				if alias == nil {
					continue
				}
				if _, has := mergedContracts[alias]; has {
					continue
				}
				contracts[alias] = contract
				mergedContracts[alias] = contract
			}
		}
		return nil
	}, tracing.GrainStep)

	return contracts
}

// propertyInitializer reads a PropertyAssignment's or
// PropertyDeclaration's initializer — the two node kinds share the
// field but not a common downcast in tsgo's generated AST.
func propertyInitializer(node *ast.Node) *ast.Node {
	if ast.IsPropertyAssignment(node) {
		return node.AsPropertyAssignment().Initializer
	}
	return node.AsPropertyDeclaration().Initializer
}

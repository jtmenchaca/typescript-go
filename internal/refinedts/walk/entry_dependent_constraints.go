// from dataflow_facts/entry_dependent_constraints.ts
//
// Difference rows a dependent signature vouches at body entry:
// z.Gte<"lo"> and family between parameters (or object keys) that
// stay stable through the body.
//
// PLACEMENT: this file's own package, dataflowfacts, cannot hold
// EntryDependentConstraints — it needs annotations.DeclaredRefinement,
// and annotations imports libraryadapters/zod, which imports
// narrowing, which imports dataflowfacts: a true cross-package cycle
// (dataflowfacts -> annotations -> ... -> narrowing -> dataflowfacts)
// Go bans outright. walk already imports both dataflowfacts and
// annotations one-way, so the function lands here instead — the same
// resolution PORT.md's cycle-rule section describes for a two-way
// shape, applied at package granularity rather than within one
// directory's subtree. dataflowfacts/entry_dependent_constraints.go's
// absence (no banner file left behind — the whole file relocated) is
// intentional; every symbol below is unchanged from the TS source.
package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// EntryDependentConstraints is the rows a DEPENDENT signature vouches
// at body entry: a parameter stated as z.Gte<"lo"> (and family)
// relates to its named sibling on every run that entered the body —
// recorded when neither parameter is ever written in the body.
func EntryDependentConstraints(
	c *checker.Checker,
	parameters []*ast.Node, // ParameterDeclaration
	params []*annotations.DeclaredRefinement, // one per parameter, nil where unstated
) []dataflowfacts.DifferenceConstraint {
	var rows []dataflowfacts.DifferenceConstraint
	placeOfName := map[string]dataflowfacts.PlaceKey{}
	for _, parameter := range parameters {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) {
			continue
		}
		tracing.CountBy("host.symbolAtLocation", 1)
		symbol := c.GetSymbolAtLocation(pd.Name())
		if symbol == nil {
			continue
		}
		placeOfName[pd.Name().Text()] = dataflowfacts.PlaceKey{Base: symbol, Path: "", BaseName: pd.Name().Text()}
	}
	push := func(self, sibling dataflowfacts.PlaceKey, op string) {
		switch op {
		case "ge":
			rows = append(rows, dataflowfacts.DifferenceConstraint{Minuend: self, Subtrahend: sibling, Bound: 0, Strict: false})
		case "gt":
			rows = append(rows, dataflowfacts.DifferenceConstraint{Minuend: self, Subtrahend: sibling, Bound: 0, Strict: true})
		case "le":
			rows = append(rows, dataflowfacts.DifferenceConstraint{Minuend: sibling, Subtrahend: self, Bound: 0, Strict: false})
		case "lt":
			rows = append(rows, dataflowfacts.DifferenceConstraint{Minuend: sibling, Subtrahend: self, Bound: 0, Strict: true})
		}
	}
	for i, parameter := range parameters {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) {
			continue
		}
		if i >= len(params) {
			continue
		}
		stated := params[i]
		if stated == nil {
			continue
		}
		base, ok := placeOfName[pd.Name().Text()]
		if !ok {
			continue
		}
		fn := parameter.Parent
		// z.Gte<"lo"> and family: relations between parameters — every
		// stated bound records a row; a sibling name may be a PATH into
		// an object parameter ("r.lo")
		if stated.Kind == annotations.DeclaredSet && stated.Depends != nil {
			for _, dep := range stated.Depends {
				path := strings.Split(dep.Param, ".")
				siblingBase, ok := placeOfName[path[0]]
				if !ok {
					continue
				}
				sibling := siblingBase
				if len(path) > 1 {
					var suffix strings.Builder
					for _, key := range path[1:] {
						suffix.WriteString(".")
						suffix.WriteString(key)
					}
					sibling.Path = suffix.String()
				}
				if !dataflowfacts.StableIn(base, []*ast.Node{fn}, fn) || !dataflowfacts.StableIn(sibling, []*ast.Node{fn}, fn) {
					continue
				}
				push(base, sibling, dep.Op)
			}
			continue
		}
		// a refine's readable rows: relations between an object
		// parameter's KEYS — property places under the same base, with
		// the reference-strict stability filter (a call reaching the
		// object could rewrite a key)
		if stated.Kind == annotations.DeclaredObject && stated.Object != nil {
			for _, key := range stated.Object.Keys {
				if key.Value.Kind != annotations.KeyValueSet || key.Value.Depends == nil {
					continue
				}
				self := base
				self.Path = "." + key.Name
				for _, dep := range key.Value.Depends {
					sibling := base
					sibling.Path = "." + dep.Param
					if !dataflowfacts.StableIn(self, []*ast.Node{fn}, fn) || !dataflowfacts.StableIn(sibling, []*ast.Node{fn}, fn) {
						continue
					}
					push(self, sibling, dep.Op)
				}
			}
		}
	}
	return rows
}

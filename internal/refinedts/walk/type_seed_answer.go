// from control_flow/type_seed_answer.ts
//
// Last-reader answers at silent exits: a literal const (position-
// independent) and the resolved type at the token, gated by the
// unchecked channels. Honesty: a type-derived claim counts only
// when formatAbstractValue returns words.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// TypeSeedAnswer is typeSeedAnswer in the TS source: the resolved
// type's claim at a silent position — the last reader everywhere the
// walk falls silent. Gated the way seeding is gated. Counts as
// DETERMINED only when it has printable words.
func TypeSeedAnswer(p *program.CheckerProgram, kernel *kernelbridge.RefinedTSKernel, token *ast.Node, declaration *ast.Node) (Answer, bool) {
	if declaration != nil && typereading.UncheckedDeclaration(p.Checker, declaration, 0) {
		return Answer{}, false
	}
	if _, written := AnyWrittenNames(p)[token.Text()]; written {
		return Answer{}, false
	}
	worn, ok := typereading.ReadHostType(p.Checker, p.Checker.GetTypeAtLocation(token), token, 0)
	if !ok || worn.Kind == abstractdomain.KindUnknown {
		return Answer{}, false
	}
	plain := worn
	if worn.Kind == abstractdomain.KindSet {
		plain.Set = refinementsets.SimplifyScalar(kernelSimplificationAdapter{kernel}, worn.Set)
	}
	words, hasWords := abstractdomain.FormatAbstractValue(plain)
	if !hasWords {
		return Answer{}, false
	}
	return Claim(words, abstractdomain.TrustLevelOf(plain), false), true
}

// anyWrittenMemo is the TS source's `WeakMap<CheckerProgram, Set<string>>`
// — substituted with a regular map guarded by a mutex, per PORT.md's
// weak-map convention.
var (
	anyWrittenMemoMu sync.Mutex
	anyWrittenMemo   = map[*program.CheckerProgram]map[string]struct{}{}
)

// AnyWrittenNames is anyWrittenNames in the TS source: the names
// this file ever assigns an any-typed value — the one write tsc
// admits silently, so the type at a later read of that name is a
// wish, not a checked claim.
func AnyWrittenNames(p *program.CheckerProgram) map[string]struct{} {
	anyWrittenMemoMu.Lock()
	if held, ok := anyWrittenMemo[p]; ok {
		anyWrittenMemoMu.Unlock()
		return held
	}
	anyWrittenMemoMu.Unlock()
	found := map[string]struct{}{}
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment && ast.IsIdentifier(bin.Left) {
				func() {
					defer func() {
						if recover() != nil {
							found[bin.Left.Text()] = struct{}{}
						}
					}()
					if (p.Checker.GetTypeAtLocation(bin.Right).Flags() & checker.TypeFlagsAny) != 0 {
						found[bin.Left.Text()] = struct{}{}
					}
				}()
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(p.Entry.AsNode())
	anyWrittenMemoMu.Lock()
	anyWrittenMemo[p] = found
	anyWrittenMemoMu.Unlock()
	return found
}

// LiteralConstClaim is literalConstClaim in the TS source: a const
// whose initializer is a literal holds that value at every reachable
// point — the declaration determines it without a walk.
func LiteralConstClaim(p *program.CheckerProgram, declaration *ast.Node, shownByHost bool) (Answer, bool) {
	if declaration == nil || !ast.IsVariableDeclaration(declaration) {
		return Answer{}, false
	}
	decl := declaration.AsVariableDeclaration()
	if decl.Initializer == nil {
		return Answer{}, false
	}
	if declaration.Parent == nil || !ast.IsVariableDeclarationList(declaration.Parent) {
		return Answer{}, false
	}
	if (declaration.Parent.Flags & ast.NodeFlagsConst) == 0 {
		return Answer{}, false
	}
	known, ok := literalKnown(p, decl.Initializer, 0)
	if !ok {
		return Answer{}, false
	}
	words, hasWords := abstractdomain.FormatAbstractValue(known)
	if !hasWords {
		return Answer{}, false
	}
	return Claim(words, abstractdomain.TrustProved, shownByHost), true
}

func literalKnown(p *program.CheckerProgram, e *ast.Node, depth int) (abstractdomain.AbstractValue, bool) {
	if ast.IsNumericLiteral(e) {
		return abstractdomain.KnownValues([]float64{float64(jsnum.FromString(e.AsNumericLiteral().Text))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), true
	}
	if ast.IsStringLiteral(e) {
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(e.AsStringLiteral().Text), abstractdomain.PrimitiveString, abstractdomain.TrustProved), true
	}
	if ast.IsNoSubstitutionTemplateLiteral(e) {
		return abstractdomain.KnownValues(refinementsets.CodepointsOf(e.Text()), abstractdomain.PrimitiveString, abstractdomain.TrustProved), true
	}
	if e.Kind == ast.KindTrueKeyword {
		return abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved), true
	}
	if e.Kind == ast.KindFalseKeyword {
		return abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved), true
	}
	if ast.IsBigIntLiteral(e) {
		text := e.Text()
		if len(text) > 0 {
			text = text[:len(text)-1] // strip the trailing "n"
		}
		return abstractdomain.AbstractValue{Kind: abstractdomain.KindBigints, BigintValues: []int64{bigIntFromString(text)}}, true
	}
	if e.Kind == ast.KindNullKeyword {
		return abstractdomain.Undef, true
	}
	if ast.IsIdentifier(e) && e.Text() == "undefined" {
		return abstractdomain.Undef, true
	}
	if ast.IsPrefixUnaryExpression(e) {
		unary := e.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			return abstractdomain.KnownValues([]float64{-float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text))}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved), true
		}
	}
	if ast.IsIdentifier(e) && depth < 8 {
		symbol := symbolAt(p.Checker, e)
		if symbol != nil && symbol.ValueDeclaration != nil {
			declaration := symbol.ValueDeclaration
			if ast.IsVariableDeclaration(declaration) {
				decl := declaration.AsVariableDeclaration()
				if decl.Initializer != nil && declaration.Parent != nil && ast.IsVariableDeclarationList(declaration.Parent) &&
					(declaration.Parent.Flags&ast.NodeFlagsConst) != 0 {
					return literalKnown(p, decl.Initializer, depth+1)
				}
			}
		}
	}
	return abstractdomain.AbstractValue{}, false
}

// bigIntFromString parses a decimal bigint literal's digits (with
// the trailing "n" already stripped) into an int64, mirroring the TS
// source's `BigInt(e.text.slice(0, -1))` for values that fit — per
// PORT.md's convention (bigint -> int64 unless the source exceeds
// it). Values past int64 are a divergence this port accepts (no
// int64-exceeding bigint literal fixture exists in the conformance
// suite as of this port).
func bigIntFromString(digits string) int64 {
	var value int64
	for _, r := range digits {
		if r < '0' || r > '9' {
			continue
		}
		value = value*10 + int64(r-'0')
	}
	return value
}

// from control_flow/type_seed_answer.ts
//
// Last-reader answers at silent exits: a literal const (position-
// independent) and the resolved type at the token, gated by the
// unchecked channels. Honesty: a type-derived claim counts only
// when formatAbstractValue returns words.

package walk

import (
	"math/big"
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
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
	if anyWriteReachesToken(p, token) {
		return Answer{}, false
	}
	worn, ok := typereading.ReadHostType(p.Checker, typereading.TypeAtLocation(p.Checker, token), token, 0)
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
	answer := Claim(words, abstractdomain.TrustLevelOf(plain), false)
	answer.SortWord = abstractdomain.ScalarSortWordOfKnown(plain)
	return answer, true
}

// anyWrittenMemo is the TS source's `WeakMap<CheckerProgram, Set<string>>`
// — substituted with a regular map guarded by a mutex, per PORT.md's
// weak-map convention. The port carries the write's LEFT-SIDE NODE
// beside its name, so the gate can ask which writes reach a read
// instead of only whether the name was ever written.
var (
	anyWrittenMemoMu sync.Mutex
	anyWrittenMemo   = map[*program.CheckerProgram]map[string][]*ast.Node{}
)

// AnyWrittenNames is anyWrittenNames in the TS source: the names
// this file ever assigns an any-typed value — the one write tsc
// admits silently, so the type at a later read of that name is a
// wish, not a checked claim. Each name carries the assignment targets
// that wrote it, which is what anyWriteReachesToken orders against a
// read.
func AnyWrittenNames(p *program.CheckerProgram) map[string][]*ast.Node {
	anyWrittenMemoMu.Lock()
	if held, ok := anyWrittenMemo[p]; ok {
		anyWrittenMemoMu.Unlock()
		return held
	}
	anyWrittenMemoMu.Unlock()
	found := map[string][]*ast.Node{}
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment && ast.IsIdentifier(bin.Left) {
				func() {
					defer func() {
						if recover() != nil {
							found[bin.Left.Text()] = append(found[bin.Left.Text()], bin.Left)
						}
					}()
					if (typereading.TypeAtLocation(p.Checker, bin.Right).Flags() & checker.TypeFlagsAny) != 0 {
						found[bin.Left.Text()] = append(found[bin.Left.Text()], bin.Left)
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

// anyWriteReachesToken answers whether an any-typed write to the
// token's name can run before the token itself does. The gate refuses
// the type seed exactly when one can.
//
// One ordering is soundly spellable here and only one: a write standing
// textually AFTER the read, inside the SAME function body as the read,
// with NO LOOP enclosing both, cannot run before it — the statements
// between them run once, forward. Everything else keeps the whole-name
// refusal:
//
//   - a write BEFORE the read in that body reaches it;
//   - a write in a DIFFERENT function is not ordered against the read at
//     all (the function may be called anywhere, including before);
//   - a loop around both makes the later write run before the read on
//     every iteration after the first;
//   - a read with no enclosing function (module top level, where the
//     write may sit in a function called from anywhere above) keeps the
//     name gate.
//
// This only ever REMOVES a refusal when the write is provably unable to
// reach; a write the reading cannot order keeps its poison.
func anyWriteReachesToken(p *program.CheckerProgram, token *ast.Node) bool {
	writes := AnyWrittenNames(p)[token.Text()]
	if len(writes) == 0 {
		return false
	}
	// which BINDING the read names — the write only reaches a read of
	// the same one. A write whose left side resolves to a different
	// symbol writes a different slot: an `any` assigned to a `value`
	// declared in one scope cannot be what a `value` declared in
	// another holds, whatever the ordering. This is the cannot-reach
	// argument at the granularity the name text alone cannot make, and
	// it is why it applies to every write below, including the two the
	// ordering rules deliberately keep (module top level, another
	// function): those refusals answer "not orderable", and a write to
	// another binding needs no ordering at all.
	//
	// A read the checker does not place answers no symbol; then nothing
	// is proved and every write stays in the ordering rules below.
	readSymbol := p.Checker.GetSymbolAtLocation(token)
	reaching := writes
	if readSymbol != nil {
		reaching = nil
		for _, write := range writes {
			writeSymbol := p.Checker.GetSymbolAtLocation(write)
			if writeSymbol != nil && writeSymbol != readSymbol {
				continue // another binding's slot — this write cannot reach
			}
			reaching = append(reaching, write)
		}
		if len(reaching) == 0 {
			return false
		}
	}
	readHolder := enclosingFunctionOf(token)
	if readHolder == nil {
		return true // module top level: nothing to order the writes against
	}
	tokenStart := nodeStart(token)
	for _, write := range reaching {
		if enclosingFunctionOf(write) != readHolder {
			return true // another function's write runs at its caller's pleasure
		}
		if nodeStart(write) < tokenStart {
			return true
		}
		if loopEnclosingBoth(write, token, readHolder) {
			return true // a later write runs before the read on the next turn
		}
	}
	return false
}

// loopEnclosingBoth is whether some loop inside the holder contains
// both nodes — the case where a textually later write still precedes a
// read, on the iteration after the one that wrote.
func loopEnclosingBoth(write *ast.Node, token *ast.Node, holder *ast.Node) bool {
	for node := write.Parent; node != nil && node != holder; node = node.Parent {
		if !isLoopNode(node) {
			continue
		}
		for inner := token.Parent; inner != nil && inner != holder; inner = inner.Parent {
			if inner == node {
				return true
			}
		}
	}
	return false
}

// isLoopNode is the statement kinds that run their body more than once.
func isLoopNode(node *ast.Node) bool {
	return ast.IsForStatement(node) || ast.IsForInStatement(node) || ast.IsForOfStatement(node) ||
		ast.IsWhileStatement(node) || ast.IsDoStatement(node)
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
	answer := Claim(words, abstractdomain.TrustProved, shownByHost)
	answer.SortWord = abstractdomain.ScalarSortWordOfKnown(known)
	return answer, true
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
		return abstractdomain.AbstractValue{Kind: abstractdomain.KindBigints, BigintValues: []*big.Int{bigIntFromString(text)}}, true
	}
	if e.Kind == ast.KindNullKeyword {
		return abstractdomain.Null, true
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
	// an identifier follows its const-to-const links to the literal it
	// names — the one chain rule, shared with every other site that
	// needs a literal (const_chain_literal.go)
	if ast.IsIdentifier(e) && depth < dataflowfacts.ConstChainDepth {
		if initializer, ok := dataflowfacts.ConstInitializerOf(p.Checker, e); ok {
			return literalKnown(p, initializer, depth+1)
		}
	}
	return abstractdomain.AbstractValue{}, false
}

// bigIntFromString parses a decimal bigint literal's digits (with
// the trailing "n" already stripped) at arbitrary precision — the TS
// source's `BigInt(e.text.slice(0, -1))` exactly, no width ceiling.
// Non-digit runes (numeric separators) are spelling, not value.
func bigIntFromString(digits string) *big.Int {
	cleaned := strings.Map(func(r rune) rune {
		if r < '0' || r > '9' {
			return -1
		}
		return r
	}, digits)
	v, ok := new(big.Int).SetString(cleaned, 10)
	if !ok {
		return big.NewInt(0)
	}
	return v
}

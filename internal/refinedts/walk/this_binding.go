// from evaluation/this_binding.ts
//
// What a `this` write does to the tracked instance: whether `this`
// appears, whether a write target is a placed chain, forgetting
// held facts and reseeding from field invariants, plus the
// effect-free-read test and the per-site cast-allow comment.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
)

// ReadsWithoutEffect: an effect-free read — a name (`this` included),
// or a property chain of names — the only comparison sides a
// resolver may evaluate twice.
func ReadsWithoutEffect(e *ast.Node) bool {
	if ast.IsIdentifier(e) || e.Kind == ast.KindThisKeyword {
		return true
	}
	if ast.IsPropertyAccessExpression(e) {
		return ReadsWithoutEffect(e.AsPropertyAccessExpression().Expression)
	}
	return false
}

// CastAllowedByComment: whether the cast's own line, or the line
// above it, carries the `@refinedts-allow-cast` comment — the
// per-site vouch: this one cast is believed, and no diagnostic is
// caused by it.
func CastAllowedByComment(ctx *FlowContext, e *ast.Node) bool {
	sourceFile := ast.GetSourceFileOfNode(e)
	text := sourceFile.Text()
	lineMap := sourceFile.ECMALineMap()
	line := lineOfPosition(lineMap, core.TextPos(e.Pos()))
	from := int(lineMap[max(0, line-1)])
	var to int
	if line+1 < len(lineMap) {
		to = int(lineMap[line+1])
	} else {
		to = len(text)
	}
	return strings.Contains(text[from:to], "@refinedts-allow-cast")
}

// lineOfPosition mirrors sourceFile.getLineAndCharacterOfPosition's
// line half: the 0-based line index a position falls on, found by
// scanning the line-start table tsgo's ECMALineMap() already
// computes (there is no ready-made Go twin of
// getLineAndCharacterOfPosition itself).
func lineOfPosition(lineMap []core.TextPos, pos core.TextPos) int {
	lo, hi := 0, len(lineMap)-1
	line := 0
	for lo <= hi {
		mid := (lo + hi) / 2
		if lineMap[mid] <= pos {
			line = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return line
}

// MentionsThis: whether `this` appears anywhere in the subtree — the
// receiver of a method call, an argument, a closure that captures it.
func MentionsThis(node *ast.Node) bool {
	if node.Kind == ast.KindThisKeyword {
		return true
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if !found {
			found = MentionsThis(child)
		}
		return false
	})
	return found
}

// PlacedThisChain: a write target writes PLACES on the tracked
// `this`: a pure property chain rooted directly at ThisKeyword. An
// element step or any richer shape falls outside, and the caller
// forgets instead.
func PlacedThisChain(target *ast.Node) bool {
	cursor := target
	for ast.IsPropertyAccessExpression(cursor) {
		cursor = cursor.AsPropertyAccessExpression().Expression
	}
	return cursor.Kind == ast.KindThisKeyword && ast.IsPropertyAccessExpression(target)
}

// ForgetThisHeld: forget what is held about `this`: havoc its alias
// class (a `const self = this` alias forgets with it, and every live
// row rooted there invalidates), then reseed the binding with the
// class's field invariants — sound, because the invariant is the
// join of every write the class's own text can make. Narrowings die
// here; that is the point.
func ForgetThisHeld(ctx *FlowContext, env Env, site *ast.Node) {
	if _, ok := env.Get("this"); !ok {
		return
	}
	HavocEnv(ctx.Aliases, env, "this")
	reseed := InitialThisStateOf(ctx, site)
	if reseed != nil {
		env.Set("this", *reseed)
	}
}

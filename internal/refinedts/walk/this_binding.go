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

// MentionsSuper: whether `super` appears anywhere in the subtree,
// naming THIS class's base. A `super` inside a nested class names that
// class's own base instead, so the walk stops at a nested class
// boundary — the same reason `this` stops where it is rebound.
func MentionsSuper(node *ast.Node) bool {
	if node.Kind == ast.KindSuperKeyword {
		return true
	}
	found := false
	node.ForEachChild(func(child *ast.Node) bool {
		if found || ast.IsClassLike(child) {
			return false
		}
		found = MentionsSuper(child)
		return false
	})
	return found
}

// ThisMentioningCalls collects every call-like node in the subtree —
// the node itself included — whose own subtree mentions `this`: the
// calls a reader has to account for before it may keep what is held
// about `this`. A call whose subtree does not mention `this` cannot
// reach the instance through this expression's text and is left out.
//
// The walk does NOT stop at a nested class the way MentionsSuper does:
// a call written inside a nested class body still runs when this
// expression runs, and an arrow there still carries the surrounding
// `this`.
func ThisMentioningCalls(node *ast.Node, into []*ast.Node) []*ast.Node {
	if IsCallLike(node) && MentionsThis(node) {
		into = append(into, node)
	}
	node.ForEachChild(func(child *ast.Node) bool {
		into = ThisMentioningCalls(child, into)
		return false
	})
	return into
}

// SuperRootedCalls collects every call-like node in the subtree — the
// node itself included — whose callee is rooted at `super`: a bare
// `super(…)` and each `super.m(…)`. These reach the SAME instance as
// `this` does without spelling `this`, so the `this` forget has to
// account for them beside the this-mentioning calls.
//
// The walk stops at a nested class the way MentionsSuper does: a
// `super` written inside a nested class body names THAT class's base
// and runs on that class's instance, not this one.
func SuperRootedCalls(node *ast.Node, into []*ast.Node) []*ast.Node {
	if IsCallLike(node) && SuperRootedCallee(CalleeOfCallLike(node)) {
		into = append(into, node)
	}
	node.ForEachChild(func(child *ast.Node) bool {
		if ast.IsClassLike(child) {
			return false
		}
		into = SuperRootedCalls(child, into)
		return false
	})
	return into
}

// SuperMentions collects every `super` keyword node in the subtree,
// stopping at a nested class body the way MentionsSuper does. The
// super gate compares this list against the roots of the super calls
// it proved: a `super` reached any OTHER way — read as a value, handed
// to a callee, stepped through `super[k]` — is one the gate has no row
// for, so the receiver forgets.
func SuperMentions(node *ast.Node, into []*ast.Node) []*ast.Node {
	if node.Kind == ast.KindSuperKeyword {
		return append(into, node)
	}
	node.ForEachChild(func(child *ast.Node) bool {
		if ast.IsClassLike(child) {
			return false
		}
		into = SuperMentions(child, into)
		return false
	})
	return into
}

// HandsThisOutOfSuperCall: whether a super call hands the instance to
// the body it runs anywhere other than as the receiver `super` already
// names — an argument mentioning `this`, or a member step that is not
// a plain `super.m` name. Every such shape gives the base body a
// reference it can store and write after the call returns, which no
// receiver-effect row covers. The `this`-call twin is HandsThisOut;
// this one differs only in that the receiver is spelled `super`.
func HandsThisOutOfSuperCall(node *ast.Node) bool {
	callee := CalleeOfCallLike(node)
	if callee == nil {
		return true
	}
	// `super[k](…)` reaches the base through a step no receiver row
	// speaks for; only the plain `super.m` name and the bare `super`
	// are covered
	if callee.Kind != ast.KindSuperKeyword && !ast.IsPropertyAccessExpression(callee) {
		return true
	}
	if ast.IsTaggedTemplateExpression(node) {
		return true
	}
	var arguments *ast.NodeList
	if ast.IsCallExpression(node) {
		arguments = node.AsCallExpression().Arguments
	} else if ast.IsNewExpression(node) {
		arguments = node.AsNewExpression().Arguments
	} else {
		return true
	}
	if arguments == nil {
		return false
	}
	for _, argument := range arguments.Nodes {
		if MentionsThis(argument) {
			return true
		}
	}
	return false
}

// IsCallLike: the three expression forms that RUN another body — the
// forms evaluateExpression's `this` forget fires on.
func IsCallLike(node *ast.Node) bool {
	return ast.IsCallExpression(node) || ast.IsNewExpression(node) ||
		ast.IsTaggedTemplateExpression(node)
}

// CalleeOfCallLike reads the expression a call-like node CALLS: a
// call's and a construction's callee, a tagged template's tag.
func CalleeOfCallLike(node *ast.Node) *ast.Node {
	if ast.IsCallExpression(node) {
		return node.AsCallExpression().Expression
	}
	if ast.IsNewExpression(node) {
		return node.AsNewExpression().Expression
	}
	if ast.IsTaggedTemplateExpression(node) {
		return node.AsTaggedTemplateExpression().Tag
	}
	return nil
}

// HandsThisOut: whether a call-like node hands `this` to the body it
// runs anywhere OTHER than as the receiver of a property-access
// callee — an argument that mentions `this`, a tagged template whose
// substitutions mention it, or a callee that is not a plain property
// chain rooted at `this`. Every such shape gives the callee a
// reference it can STORE and write after the call returns, which no
// receiver-effect row covers.
func HandsThisOut(node *ast.Node) bool {
	callee := CalleeOfCallLike(node)
	if callee == nil {
		return true
	}
	// the receiver of `this.m()` / `this.a.m()` is the one place a
	// summary's receiver row speaks for; `this` reached any other way
	// through the callee (an element step, a call in the chain) is not
	// covered
	if MentionsThis(callee) && !PlacedThisChain(callee) {
		return true
	}
	if ast.IsTaggedTemplateExpression(node) {
		return MentionsThis(node.AsTaggedTemplateExpression().Template)
	}
	var arguments *ast.NodeList
	if ast.IsCallExpression(node) {
		arguments = node.AsCallExpression().Arguments
	} else {
		arguments = node.AsNewExpression().Arguments
	}
	if arguments == nil {
		return false
	}
	for _, argument := range arguments.Nodes {
		if MentionsThis(argument) {
			return true
		}
	}
	return false
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

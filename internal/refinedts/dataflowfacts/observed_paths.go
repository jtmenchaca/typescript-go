// FIELD-LEVEL observation: which first-level property keys a body
// reads off each name it observes from its caller, rather than the
// whole value. ObservedNamesOf (syntactic_facts.go) already answers
// WHICH names a body can observe — every identifier its subtree
// mentions; this file narrows WHAT PART of each name the body
// actually reads, so a memo key built from it can spell only the
// keys a callee consults instead of an evolving object's every key.
//
// A name maps to nil ("observe the whole value") wherever the body
// uses it outside a first-level property read: passed whole to a
// call, aliased, spread, returned, compared, in/typeof'd, indexed by
// anything but a string literal, or WRITTEN to at all (`name.key =`
// counts as whole-value: the inline replays post-states, so a
// write's target must not be narrowed away). Purely syntactic — no
// checker, no symbol resolution — so the scan runs on a bare parsed
// tree the way ObservedNamesOf does.
package dataflowfacts

import (
	"sort"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
)

var (
	observedPathsMu    sync.Mutex
	observedPathsCache = map[*ast.Node]map[string][]string{}
)

// ObservedPathsOf is, per free name ObservedNamesOf reports for
// `declaration`'s body, the sorted set of first-level property keys
// the body reads off that name — nil where the body uses the name
// itself (whole-value) anywhere. Cached per declaration.
func ObservedPathsOf(declaration *ast.Node) map[string][]string {
	observedPathsMu.Lock()
	if held, ok := observedPathsCache[declaration]; ok {
		observedPathsMu.Unlock()
		return held
	}
	observedPathsMu.Unlock()

	result := observedPathsOfNode(declaration.Body())

	observedPathsMu.Lock()
	observedPathsCache[declaration] = result
	observedPathsMu.Unlock()
	return result
}

// ObservedPathsOfNode is the same scan for an arbitrary function-like
// node (an inline callback argument, which has no FunctionContract of
// its own) — computeInlineMemoKey unions a callback's observed names
// into the key the same way it does the callee's; this is the paths
// variant for that same node.
func ObservedPathsOfNode(fn *ast.Node) map[string][]string {
	observedPathsMu.Lock()
	if held, ok := observedPathsCache[fn]; ok {
		observedPathsMu.Unlock()
		return held
	}
	observedPathsMu.Unlock()

	result := observedPathsOfNode(fn.Body())

	observedPathsMu.Lock()
	observedPathsCache[fn] = result
	observedPathsMu.Unlock()
	return result
}

// observedPathsOfNode runs the actual scan over a function body (nil
// body answers an empty map, matching ObservedOf's nil handling).
func observedPathsOfNode(body *ast.Node) map[string][]string {
	if body == nil {
		return map[string][]string{}
	}
	keysByName := map[string]map[string]struct{}{}
	wholeValue := map[string]struct{}{}

	markWhole := func(name string) {
		wholeValue[name] = struct{}{}
	}
	markKey := func(name string, key string) {
		if _, isWhole := wholeValue[name]; isWhole {
			return
		}
		keys, ok := keysByName[name]
		if !ok {
			keys = map[string]struct{}{}
			keysByName[name] = keys
		}
		keys[key] = struct{}{}
	}

	// nameOf reads the plain name off an identifier or `this` — "" for
	// anything else (a parenthesized/cast wrapper is unwrapped by the
	// caller before this is reached).
	nameOf := func(n *ast.Node) (string, bool) {
		if ast.IsIdentifier(n) {
			return n.Text(), true
		}
		if n.Kind == ast.KindThisKeyword {
			return "this", true
		}
		return "", false
	}

	// unwrap strips the wrappers that carry no runtime meaning for a
	// place — matching TrackedPlaceOf's own unwrap in access_paths.go.
	unwrap := func(n *ast.Node) *ast.Node {
		for {
			switch {
			case ast.IsParenthesizedExpression(n):
				n = n.AsParenthesizedExpression().Expression
			case ast.IsAsExpression(n):
				n = n.AsAsExpression().Expression
			case ast.IsNonNullExpression(n):
				n = n.AsNonNullExpression().Expression
			default:
				return n
			}
		}
	}

	// markWriteTarget marks a write's root name whole-value — a
	// property write (`name.key = …`) still forces the WHOLE name
	// whole: the inline replays post-states, so a write's target must
	// not be narrowed away.
	var markWriteTarget func(target *ast.Node)
	markWriteTarget = func(target *ast.Node) {
		target = unwrap(target)
		if name, ok := nameOf(target); ok {
			markWhole(name)
			return
		}
		if ast.IsPropertyAccessExpression(target) {
			markWriteTarget(target.AsPropertyAccessExpression().Expression)
			return
		}
		if ast.IsElementAccessExpression(target) {
			markWriteTarget(target.AsElementAccessExpression().Expression)
			return
		}
		if ast.IsArrayLiteralExpression(target) {
			for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
				switch {
				case ast.IsSpreadElement(element):
					markWriteTarget(element.AsSpreadElement().Expression)
				case ast.IsBinaryExpression(element):
					markWriteTarget(element.AsBinaryExpression().Left)
				case !ast.IsOmittedExpression(element):
					markWriteTarget(element)
				}
			}
			return
		}
		if ast.IsObjectLiteralExpression(target) {
			for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
				switch {
				case ast.IsPropertyAssignment(property):
					markWriteTarget(property.AsPropertyAssignment().Initializer)
				case ast.IsShorthandPropertyAssignment(property):
					markWhole(property.Name().Text())
				case ast.IsSpreadAssignment(property):
					markWriteTarget(property.AsSpreadAssignment().Expression)
				}
			}
		}
	}

	// scanGeneral and scan are mutually recursive with visitUse (a call
	// argument reached by scanGeneral may itself be a write, and a
	// statement reached by scan may hold a call argument scanGeneral
	// must dispatch) — declared up front so any of the three can name
	// the others.
	var scanGeneral func(n *ast.Node)
	var scan func(node *ast.Node) bool

	// visitUse classifies one USE of an expression (never a write
	// target — those go through markWriteTarget) and fully handles its
	// own recursion, including any non-literal element-access argument
	// (`cfg[k]`, where `k` may itself read another observed name). A
	// first-level property/literal-element read off a bare name or
	// `this` records the key; a chain past the first level (`a.b.c`)
	// or a computed non-literal index marks the root whole-value;
	// anything else that bottoms out on a bare name/this marks it
	// whole-value too.
	var visitUse func(n *ast.Node)
	visitUse = func(n *ast.Node) {
		u := unwrap(n)
		if name, ok := nameOf(u); ok {
			markWhole(name)
			return
		}
		if ast.IsPropertyAccessExpression(u) {
			pae := u.AsPropertyAccessExpression()
			base := unwrap(pae.Expression)
			if name, ok := nameOf(base); ok {
				markKey(name, pae.Name().Text())
				return
			}
			visitUse(pae.Expression)
			return
		}
		if ast.IsElementAccessExpression(u) {
			eae := u.AsElementAccessExpression()
			base := unwrap(eae.Expression)
			key := StringLiteralOf(eae.ArgumentExpression)
			if name, ok := nameOf(base); ok {
				if key != nil {
					markKey(name, *key)
				} else {
					// a computed, non-literal index reads beyond the
					// first level — the whole value observes, and the
					// index expression itself still needs scanning
					// (it may itself read another observed name, e.g.
					// `cfg[k]` also observes k whole)
					markWhole(name)
					scan(eae.ArgumentExpression)
				}
				return
			}
			visitUse(eae.Expression)
			if key == nil {
				scan(eae.ArgumentExpression)
			}
			return
		}
		// anything else (a call, a binary op, a literal, …) is not a
		// place at all — walk its children generally so nested
		// identifier/this/property reads still get classified
		scanGeneral(u)
	}

	// scanGeneral dispatches EVERY child of `n` through `scan` (not a
	// structural recursion of its own), so a call argument, comparison
	// operand, or any other expression still reaches every write/read
	// form the switch in `scan` recognizes — `scan` itself decides
	// whether to recurse further or, for a property/element access or
	// bare name, stop and classify.
	scanGeneral = func(n *ast.Node) {
		n.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}

	scan = func(node *ast.Node) bool {
		switch {
		case ast.IsBinaryExpression(node):
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				markWriteTarget(bin.Left)
				scan(bin.Right)
				return false
			}
		case ast.IsPrefixUnaryExpression(node):
			unary := node.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				markWriteTarget(unary.Operand)
				return false
			}
		case ast.IsPostfixUnaryExpression(node):
			unary := node.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				markWriteTarget(unary.Operand)
				return false
			}
		case ast.IsDeleteExpression(node):
			markWriteTarget(node.AsDeleteExpression().Expression)
			return false
		case ast.IsPropertyAccessExpression(node), ast.IsElementAccessExpression(node):
			visitUse(node)
			return false
		case ast.IsIdentifier(node):
			markWhole(node.Text())
			return false
		case node.Kind == ast.KindThisKeyword:
			markWhole("this")
			return false
		}
		node.ForEachChild(scan)
		return false
	}
	scan(body)

	result := map[string][]string{}
	for name, keys := range keysByName {
		if _, isWhole := wholeValue[name]; isWhole {
			continue
		}
		list := make([]string, 0, len(keys))
		for key := range keys {
			list = append(list, key)
		}
		sort.Strings(list)
		result[name] = list
	}
	for name := range wholeValue {
		result[name] = nil
	}
	return result
}

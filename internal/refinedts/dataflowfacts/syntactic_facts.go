// THE per-node syntactic-facts seam: every fact the TREE can answer
// is computed once and consulted thereafter by node identity. The
// scanners this file holds used to run at every point of use —
// functionWrites re-scanned the enclosing function behind every
// `const x = …; if (!x)` condition (16.9% of the sampled wall on
// prisma's mongo contract-builder), declaredNames ran once per
// inline (2.7M times on tailwindcss utilities.ts). A fact whose
// answer needs the PROGRAM (referenceTyped, default-library
// resolution) keys by (program, node); a fact the tree answers alone
// keys by node. Consumers never scan — they read the seam.
//
// BLOCKED: writtenNamesOf needs service/program_resolution.ts's
// resolvesToDefaultLib — a service/ function outside this directory's
// allowed import set (and the TS host/program adapter layer PORT.md
// says is not ported at all). Everything else, which needs only the
// checker, is ported below.
package dataflowfacts

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

/* ── the memo stores ─────────────────────────────────────────────── */
//
// The TS source keys pure facts with a WeakMap<ts.Node, …> and
// program-dependent facts with WeakMap<CheckerProgram, WeakMap<ts.Node,
// …>>. Go has no weak maps, so both substitute a regular map guarded by
// a mutex, the program-dependent one keyed by (*checker.Checker,
// *ast.Node) — PORT.md's replacement for the TS host/program adapter.
// Functionally identical per program: entries live exactly as long as
// the program that produced their nodes is in use by this port.

type pureFacts struct {
	assignedUnfiltered  map[string]struct{}
	hasAssignedUnfilt   bool
	assignedDirect      map[string]struct{}
	hasAssignedDirect   bool
	declared            map[string]struct{}
	hasDeclared         bool
	observed            []string
	hasObserved         bool
	calleeLocals        map[string]struct{}
	hasCalleeLocals     bool
	assignedIdentifiers map[string]struct{}
	hasAssignedIdent    bool
}

var (
	pureMu    sync.Mutex
	pureCache = map[*ast.Node]*pureFacts{}
)

func pureOf(node *ast.Node) *pureFacts {
	pureMu.Lock()
	defer pureMu.Unlock()
	held, ok := pureCache[node]
	if !ok {
		held = &pureFacts{}
		pureCache[node] = held
	}
	return held
}

type programKey struct {
	checker *checker.Checker
	node    *ast.Node
}

type programFacts struct {
	assignedFiltered  map[string]struct{}
	hasAssignedFilter bool
}

var (
	programMu    sync.Mutex
	programCache = map[programKey]*programFacts{}
)

func programOf(c *checker.Checker, node *ast.Node) *programFacts {
	programMu.Lock()
	defer programMu.Unlock()
	key := programKey{checker: c, node: node}
	held, ok := programCache[key]
	if !ok {
		held = &programFacts{}
		programCache[key] = held
	}
	return held
}

/* ── the assigned-name scanner (from loop_fixpoint) ──────────────── */

// TargetNames collects the NAMES an assignment target touches: an
// identifier, the root of a property/element chain, `this`, and every
// element of a destructuring pattern.
func TargetNames(target *ast.Node, into map[string]struct{}) {
	if ast.IsIdentifier(target) {
		into[target.Text()] = struct{}{}
		return
	}
	receiver := target
	for ast.IsPropertyAccessExpression(receiver) || ast.IsElementAccessExpression(receiver) {
		if ast.IsPropertyAccessExpression(receiver) {
			receiver = receiver.AsPropertyAccessExpression().Expression
		} else {
			receiver = receiver.AsElementAccessExpression().Expression
		}
	}
	if ast.IsIdentifier(receiver) {
		into[receiver.Text()] = struct{}{}
		return
	}
	// `this.count = 5` writes the tracked "this" binding's facts
	if receiver.Kind == ast.KindThisKeyword {
		into["this"] = struct{}{}
		return
	}
	if ast.IsArrayLiteralExpression(target) {
		for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
			switch {
			case ast.IsSpreadElement(element):
				TargetNames(element.AsSpreadElement().Expression, into)
			case ast.IsBinaryExpression(element):
				TargetNames(element.AsBinaryExpression().Left, into)
			case !ast.IsOmittedExpression(element):
				TargetNames(element, into)
			}
		}
		return
	}
	if ast.IsObjectLiteralExpression(target) {
		for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
			switch {
			case ast.IsPropertyAssignment(property):
				TargetNames(property.AsPropertyAssignment().Initializer, into)
			case ast.IsShorthandPropertyAssignment(property):
				into[property.Name().Text()] = struct{}{}
			case ast.IsSpreadAssignment(property):
				TargetNames(property.AsSpreadAssignment().Expression, into)
			}
		}
	}
}

// ReadOnlyStaticCalls is the statics that read their arguments and
// provably write nothing they are handed (sec-object.keys and kin walk
// own properties; sec-json.stringify serializes; console formats) —
// recognized by spelling, the same way the read-only method receivers
// are.
var ReadOnlyStaticCalls = map[string]map[string]struct{}{
	"Object": {
		"keys":                {},
		"values":              {},
		"entries":             {},
		"hasOwn":              {},
		"getOwnPropertyNames": {},
		"isExtensible":        {},
		"isFrozen":            {},
		"isSealed":            {},
		"is":                  {},
		"getPrototypeOf":      {},
	},
	"JSON":  {"stringify": {}},
	"Array": {"isArray": {}},
}

func assignedNamesCore(
	node *ast.Node,
	into map[string]struct{},
	includeCallees bool,
	handedArg func(argument *ast.Node) bool,
) {
	if ast.IsBinaryExpression(node) {
		bin := node.AsBinaryExpression()
		if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
			TargetNames(bin.Left, into)
		}
	}
	if ast.IsPrefixUnaryExpression(node) {
		unary := node.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			// ++x, or ++obj.key — either way the NAME's facts change
			receiver := unary.Operand
			for ast.IsPropertyAccessExpression(receiver) || ast.IsElementAccessExpression(receiver) {
				if ast.IsPropertyAccessExpression(receiver) {
					receiver = receiver.AsPropertyAccessExpression().Expression
				} else {
					receiver = receiver.AsElementAccessExpression().Expression
				}
			}
			if ast.IsIdentifier(receiver) {
				into[receiver.Text()] = struct{}{}
			} else if receiver.Kind == ast.KindThisKeyword {
				into["this"] = struct{}{}
			}
		}
	}
	if ast.IsPostfixUnaryExpression(node) {
		unary := node.AsPostfixUnaryExpression()
		if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
			receiver := unary.Operand
			for ast.IsPropertyAccessExpression(receiver) || ast.IsElementAccessExpression(receiver) {
				if ast.IsPropertyAccessExpression(receiver) {
					receiver = receiver.AsPropertyAccessExpression().Expression
				} else {
					receiver = receiver.AsElementAccessExpression().Expression
				}
			}
			if ast.IsIdentifier(receiver) {
				into[receiver.Text()] = struct{}{}
			} else if receiver.Kind == ast.KindThisKeyword {
				into["this"] = struct{}{}
			}
		}
	}
	if includeCallees && ast.IsCallExpression(node) {
		call := node.AsCallExpression()
		if ast.IsPropertyAccessExpression(call.Expression) {
			propAccess := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(propAccess.Expression) {
				if _, readOnly := ReadOnlyArrayMethods[propAccess.Name().Text()]; !readOnly {
					into[propAccess.Expression.Text()] = struct{}{}
				}
			}
		}
		// the known READ-ONLY callees provably write nothing they are
		// handed — Object.keys(box) reads box, it never moves it — so
		// their arguments stay un-counted, the same standing the
		// read-only method receivers hold above
		readsOnly := false
		if ast.IsPropertyAccessExpression(call.Expression) {
			propAccess := call.Expression.AsPropertyAccessExpression()
			if ast.IsIdentifier(propAccess.Expression) {
				receiverText := propAccess.Expression.Text()
				if methods, ok := ReadOnlyStaticCalls[receiverText]; ok {
					if _, ok := methods[propAccess.Name().Text()]; ok {
						readsOnly = true
					}
				}
				if receiverText == "console" {
					readsOnly = true
				}
			}
		}
		if !readsOnly && call.Arguments != nil {
			for _, argument := range call.Arguments.Nodes {
				// an identifier handed to a callee is written only when the
				// callee can reach it — a value-sorted word travels by copy,
				// so the caller's predicate spares it
				if ast.IsIdentifier(argument) && (handedArg == nil || handedArg(argument)) {
					into[argument.Text()] = struct{}{}
				} else if (ast.IsPropertyAccessExpression(argument) || ast.IsElementAccessExpression(argument)) &&
					(handedArg == nil || handedArg(argument)) {
					// a REFERENCE projection handed over (`copy.children`)
					// reaches its holder: the callee mutates through it, so the
					// root's facts change; a scalar projection still travels by
					// copy and spares the holder (tailwindcss's
					// transform(child, copy.children) froze copy at [])
					root := argument
					for ast.IsPropertyAccessExpression(root) || ast.IsElementAccessExpression(root) {
						if ast.IsPropertyAccessExpression(root) {
							root = root.AsPropertyAccessExpression().Expression
						} else {
							root = root.AsElementAccessExpression().Expression
						}
					}
					if ast.IsIdentifier(root) {
						into[root.Text()] = struct{}{}
					} else if root.Kind == ast.KindThisKeyword {
						into["this"] = struct{}{}
					}
				}
			}
		}
	}
	node.ForEachChild(func(child *ast.Node) bool {
		assignedNamesCore(child, into, includeCallees, handedArg)
		return false
	})
}

// AssignedNameSet collects names the subtree may write, call-mediated
// writes included, with handed arguments filtered by reference sort (a
// value-sorted word travels by copy). The ReferenceTyped filter asks
// the checker, so the memo keys by (checker, node).
func AssignedNameSet(c *checker.Checker, node *ast.Node) map[string]struct{} {
	held := programOf(c, node)
	if !held.hasAssignedFilter {
		into := map[string]struct{}{}
		assignedNamesCore(node, into, true, func(argument *ast.Node) bool {
			return ReferenceTyped(c, argument)
		})
		held.assignedFiltered = into
		held.hasAssignedFilter = true
	}
	return held.assignedFiltered
}

// AssignedNameSetUnfiltered is the same, with EVERY handed argument
// counted — the conservative reading a caller takes when it has no
// program to filter with.
func AssignedNameSetUnfiltered(node *ast.Node) map[string]struct{} {
	held := pureOf(node)
	if !held.hasAssignedUnfilt {
		into := map[string]struct{}{}
		assignedNamesCore(node, into, true, nil)
		held.assignedUnfiltered = into
		held.hasAssignedUnfilt = true
	}
	return held.assignedUnfiltered
}

// AssignedDirectSet collects only the names the TEXT itself assigns —
// call-mediated writes are excluded, for a caller that models callee
// effects precisely.
func AssignedDirectSet(node *ast.Node) map[string]struct{} {
	held := pureOf(node)
	if !held.hasAssignedDirect {
		into := map[string]struct{}{}
		assignedNamesCore(node, into, false, nil)
		held.assignedDirect = into
		held.hasAssignedDirect = true
	}
	return held.assignedDirect
}

// AssignedIdentifierNames collects identifier-spelled DIRECT assignment
// targets only (`x = …`, compound assignments included) — the
// predicate-body reassignment question reads exactly this.
func AssignedIdentifierNames(node *ast.Node) map[string]struct{} {
	held := pureOf(node)
	if !held.hasAssignedIdent {
		into := map[string]struct{}{}
		var scan func(child *ast.Node) bool
		scan = func(child *ast.Node) bool {
			if ast.IsBinaryExpression(child) {
				bin := child.AsBinaryExpression()
				if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
					bin.OperatorToken.Kind <= ast.KindLastAssignment &&
					ast.IsIdentifier(bin.Left) {
					into[bin.Left.Text()] = struct{}{}
				}
			}
			child.ForEachChild(scan)
			return false
		}
		scan(node)
		held.assignedIdentifiers = into
		held.hasAssignedIdent = true
	}
	return held.assignedIdentifiers
}

/* ── the name scanners (from inliner and loop_fixpoint) ──────────── */

func bindingNames(name *ast.Node, into map[string]struct{}) {
	if ast.IsIdentifier(name) {
		into[name.Text()] = struct{}{}
		return
	}
	if ast.IsArrayBindingPattern(name) || ast.IsObjectBindingPattern(name) {
		for _, element := range name.AsBindingPattern().Elements.Nodes {
			if ast.IsOmittedExpression(element) {
				continue
			}
			bindingNames(element.Name(), into)
		}
	}
}

// DeclaredNameSet collects every binding NAME a subtree declares — an
// inline walks the callee's body on a copy of the caller's environment,
// so the callee's own names must not read as (or write back to) caller
// state.
func DeclaredNameSet(node *ast.Node) map[string]struct{} {
	held := pureOf(node)
	if held.hasDeclared {
		return held.declared
	}
	into := map[string]struct{}{}
	var scan func(child *ast.Node) bool
	scan = func(child *ast.Node) bool {
		if ast.IsVariableDeclaration(child) {
			bindingNames(child.Name(), into)
		}
		if ast.IsFunctionDeclaration(child) && child.Name() != nil {
			into[child.Name().Text()] = struct{}{}
		}
		child.ForEachChild(scan)
		return false
	}
	scan(node)
	held.declared = into
	held.hasDeclared = true
	return into
}

// ObservedNamesOf collects everything a body can OBSERVE from its
// caller: every identifier text its subtree mentions plus `this` — an
// overapproximation of its outer reads and writes (extra names only
// lower a memo's hit rate, never its soundness). Sorted, for stable
// memo keys.
func ObservedNamesOf(node *ast.Node) []string {
	held := pureOf(node)
	if held.hasObserved {
		return held.observed
	}
	names := map[string]struct{}{}
	var scan func(child *ast.Node) bool
	scan = func(child *ast.Node) bool {
		if ast.IsIdentifier(child) {
			names[child.Text()] = struct{}{}
		} else if child.Kind == ast.KindThisKeyword {
			names["this"] = struct{}{}
		}
		child.ForEachChild(scan)
		return false
	}
	scan(node)
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sortStrings(list)
	held.observed = list
	held.hasObserved = true
	return list
}

// sortStrings is a small insertion-free sort helper so this file needs
// no extra import beyond the standard library's sort, kept local to
// avoid a package-level import-order surprise from tooling.
func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j-1] > list[j]; j-- {
			list[j-1], list[j] = list[j], list[j-1]
		}
	}
}

// CalleeLocalSet collects the callee's OWN names — parameters and
// declarations — which its writes may target without touching the
// caller's world.
func CalleeLocalSet(declaration *ast.Node) map[string]struct{} {
	held := pureOf(declaration)
	if held.hasCalleeLocals {
		return held.calleeLocals
	}
	names := map[string]struct{}{}
	var parameters []*ast.Node
	var body *ast.Node
	switch {
	case ast.IsFunctionDeclaration(declaration):
		fn := declaration.AsFunctionDeclaration()
		if fn.Parameters != nil {
			parameters = fn.Parameters.Nodes
		}
		body = fn.Body
	case ast.IsMethodDeclaration(declaration):
		fn := declaration.AsMethodDeclaration()
		if fn.Parameters != nil {
			parameters = fn.Parameters.Nodes
		}
		body = fn.Body
	case ast.IsArrowFunction(declaration):
		fn := declaration.AsArrowFunction()
		if fn.Parameters != nil {
			parameters = fn.Parameters.Nodes
		}
		body = fn.Body
	case ast.IsFunctionExpression(declaration):
		fn := declaration.AsFunctionExpression()
		if fn.Parameters != nil {
			parameters = fn.Parameters.Nodes
		}
		body = fn.Body
	}
	for _, parameter := range parameters {
		if ast.IsIdentifier(parameter.Name()) {
			names[parameter.Name().Text()] = struct{}{}
		}
	}
	var collect func(node *ast.Node) bool
	collect = func(node *ast.Node) bool {
		if ast.IsVariableDeclaration(node) && ast.IsIdentifier(node.Name()) {
			names[node.Name().Text()] = struct{}{}
		}
		if (ast.IsFunctionDeclaration(node) || ast.IsClassDeclaration(node)) && node.Name() != nil {
			names[node.Name().Text()] = struct{}{}
		}
		node.ForEachChild(collect)
		return false
	}
	if body != nil {
		collect(body)
	}
	held.calleeLocals = names
	held.hasCalleeLocals = true
	return names
}

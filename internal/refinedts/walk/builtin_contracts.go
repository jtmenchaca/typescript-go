// from evaluation/builtin_contracts.ts
//
// The contracts the built-ins state about their OWN argument
// positions — the table the surveys asked for. Each row transcribes
// its clause from the vendored spec (tmp/ecma262/spec.html) or names
// its outside source, and each fires through the one assignability
// path or speaks one plain sentence, so a contract refutation reads
// like any other refutation.
//
// Rows:
// - Array(len) / new Array(len): one Number argument must survive
//   ToUint32 exactly — an integer from 0 to 4294967295 — or the
//   constructor throws a RangeError (sec-array, oldids
//   sec-array-len: "If SameValueZero(intLength, length) is false,
//   throw a RangeError exception"). One NON-number argument builds a
//   one-element array instead, so the contract judges only
//   number-sorted arguments.
// - Math.f(...items): a spread hands every element as its own
//   argument, and engines fail past tens of thousands of arguments
//   (an engine limit, not a spec one — V8 and JSC both cap argument
//   counts near 2^16). An array whose length window is unbounded, or
//   bounded above the cap, admits calls that fail.
// - window.open(url, "_blank", features): unless the features say
//   noopener or noreferrer, the opened page gets a `window.opener`
//   handle back to the opener (HTML Standard, "window open steps" —
//   not vendored, so the row is graded by its source).

package walk

import (
	"encoding/json"
	"regexp"
	"strconv"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// engineArgumentCap is ENGINE_ARGUMENT_CAP in the TS source: the
// argument-count ceiling shared by the major engines' call paths — an
// engine fact, conservative at 65535.
const engineArgumentCap = 65535

func numberSorted(known abstractdomain.AbstractValue) bool {
	return known.Kind == abstractdomain.KindNaN ||
		(known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveNumber) ||
		(known.Kind == abstractdomain.KindSet && known.SetKindTag == abstractdomain.SetKindTagNone) ||
		known.Kind == abstractdomain.KindPossiblyNaN
}

func exactString(known abstractdomain.AbstractValue) (string, bool) {
	if known.Kind == abstractdomain.KindValues && known.KindTag == abstractdomain.PrimitiveString {
		return stringOf(known.Values), true
	}
	return "", false
}

var shellRedirectionPrefix = regexp.MustCompile(`^\d*[<>]{1,2}`)
var noopenerWord = regexp.MustCompile(`\b(noopener|noreferrer)\b`)

var spawnLikeCalleeNames = map[string]bool{
	"spawn": true, "spawnSync": true, "execFile": true, "execFileSync": true, "execa": true,
}

func callArguments(e *ast.Node) ([]*ast.Node, bool) {
	if ast.IsCallExpression(e) {
		call := e.AsCallExpression()
		if call.Arguments == nil {
			return nil, false
		}
		return call.Arguments.Nodes, true
	}
	if ast.IsNewExpression(e) {
		newExpr := e.AsNewExpression()
		if newExpr.Arguments == nil {
			return nil, false
		}
		return newExpr.Arguments.Nodes, true
	}
	return nil, false
}

func calleeOf(e *ast.Node) *ast.Node {
	if ast.IsCallExpression(e) {
		return e.AsCallExpression().Expression
	}
	if ast.IsNewExpression(e) {
		return e.AsNewExpression().Expression
	}
	return nil
}

// CheckBuiltinContracts is checkBuiltinContracts in the TS source:
// judge the spec-stated contracts of one built-in call or
// construction. Reads the arguments, reports through the one
// assignability path, and never answers a value — the callers keep
// their own result models.
func CheckBuiltinContracts(ctx *FlowContext, env Env, e *ast.Node) {
	args, hasArgs := callArguments(e)
	if !hasArgs {
		return
	}
	callee := calleeOf(e)
	// Array(len) — both spellings, the call and the construction
	if ast.IsIdentifier(callee) && callee.Text() == "Array" &&
		resolvesToDefaultLib(ctx, callee) && len(args) == 1 && !ast.IsSpreadElement(args[0]) {
		length := evaluateExpression(ctx, env, args[0])
		if numberSorted(length) {
			CheckAssignability(ctx, length, annotations.DeclaredRefinement{
				Kind: annotations.DeclaredSet,
				Set:  setPtr(refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0), refinementsets.AtMost(4294967295))),
			}, args[0], "the array length", nil)
		}
		return
	}
	// a spread handing every element as its own ARGUMENT: Math.f(...xs)
	// and the array mutators xs.push(...ys)/xs.unshift(...ys) — the
	// engine's argument cap applies to all of them
	{
		var spreadTakerName string
		hasSpreadTaker := false
		if ast.IsCallExpression(e) {
			call := e.AsCallExpression()
			if ast.IsPropertyAccessExpression(call.Expression) {
				pa := call.Expression.AsPropertyAccessExpression()
				if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "Math" && resolvesToDefaultLib(ctx, pa.Expression) {
					spreadTakerName, hasSpreadTaker = "Math."+pa.Name().Text(), true
				} else if (pa.Name().Text() == "push" || pa.Name().Text() == "unshift") && resolvesToDefaultLib(ctx, pa.Name()) {
					spreadTakerName, hasSpreadTaker = pa.Name().Text(), true
				}
			}
		}
		if hasSpreadTaker {
			for _, argument := range args {
				if !ast.IsSpreadElement(argument) {
					continue
				}
				spread := evaluateExpression(ctx, env, argument.AsSpreadElement().Expression)
				if spread.Kind != abstractdomain.KindSet || spread.SetKindTag != abstractdomain.SetKindTagNone {
					continue
				}
				window, ok := refinementsets.AsRepetition(spread.Set)
				if !ok {
					continue
				}
				if window.Hi != nil && *window.Hi <= engineArgumentCap {
					continue
				}
				var hiText string
				if window.Hi == nil {
					hiText = "any number of items"
				} else {
					hiText = "up to " + strconv.Itoa(*window.Hi) + " items"
				}
				ctx.Report(assignability.At(
					argument, 7001,
					"the spread hands each item to "+spreadTakerName+"() "+
						"as its own argument, and the array can hold "+
						hiText+
						" — engines fail past about "+strconv.Itoa(engineArgumentCap)+" arguments",
				))
			}
			return
		}
	}
	// spawn/execFile argv: a shell redirection token as an ELEMENT —
	// a shell-less exec hands "2>NUL" to the program verbatim (Node
	// child_process: no shell interpretation without shell:true), so
	// the redirection never happens and the program sees a bogus
	// argument
	if ast.IsCallExpression(e) {
		var name string
		hasName := false
		if ast.IsIdentifier(callee) {
			name, hasName = callee.Text(), true
		} else if ast.IsPropertyAccessExpression(callee) {
			name, hasName = callee.AsPropertyAccessExpression().Name().Text(), true
		}
		if hasName && spawnLikeCalleeNames[name] {
			for _, argument := range args {
				if !ast.IsArrayLiteralExpression(argument) {
					continue
				}
				for _, element := range argument.AsArrayLiteralExpression().Elements.Nodes {
					known := evaluateExpression(ctx, env, element)
					text, ok := exactString(known)
					if !ok {
						continue
					}
					if !shellRedirectionPrefix.MatchString(text) {
						continue
					}
					quoted, _ := json.Marshal(text)
					ctx.Report(assignability.At(
						element, 7001,
						"the argv element "+string(quoted)+" is a shell "+
							"redirection, and an exec without a shell hands it to the "+
							"program as a plain argument — the redirection never happens",
					))
				}
			}
			return
		}
	}
	// window.open(url, "_blank", features)
	if ast.IsCallExpression(e) && ast.IsPropertyAccessExpression(callee) {
		pa := callee.AsPropertyAccessExpression()
		if ast.IsIdentifier(pa.Expression) && pa.Expression.Text() == "window" &&
			pa.Name().Text() == "open" && resolvesToDefaultLib(ctx, pa.Name()) &&
			len(args) >= 2 {
			anySpread := false
			for _, a := range args {
				if ast.IsSpreadElement(a) {
					anySpread = true
					break
				}
			}
			if !anySpread {
				target, _ := exactString(evaluateExpression(ctx, env, args[1]))
				if target != "_blank" {
					return
				}
				var features string
				featuresOk := true
				if len(args) >= 3 {
					features, featuresOk = exactString(evaluateExpression(ctx, env, args[2]))
				} else {
					features = ""
				}
				if !featuresOk {
					return
				}
				if noopenerWord.MatchString(features) {
					return
				}
				var featuresPhrase string
				if features == "" {
					featuresPhrase = "there are no features here"
				} else {
					featuresPhrase = "the features here don't"
				}
				ctx.Report(assignability.At(
					e, 7001,
					`open with "_blank" gives the new page a window.opener handle `+
						"back to this one unless the features say noopener — "+
						featuresPhrase,
				))
				return
			}
		}
	}
}

func setPtr(s refinementsets.RefinedSet) *refinementsets.RefinedSet {
	return &s
}

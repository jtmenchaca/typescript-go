// from interprocedural/inline_replay.ts
//
// Remembered inline outcomes and the replay that reapplies them:
// the same tracked updates, the same parameter write-backs, the
// same returned knowledge — without the body walk. The memo key
// guaranteed every observed entry state matches, so this IS what
// the walk would have done. Also the caller's view of a returned
// value (async wrapping).

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// postByNameEntry is one (name, after) pair of an InlineOutcome's
// PostByName — the TS source's `readonly (readonly [string,
// AbstractValue])[]`.
type postByNameEntry struct {
	Name  string
	After abstractdomain.AbstractValue
}

// paramPost is one entry of an InlineOutcome's ParamPosts — the TS
// source's `readonly (AbstractValue | null)[]`.
type paramPost struct {
	Value abstractdomain.AbstractValue
	Has   bool
}

// InlineOutcome is one inline's caller-visible outcome — what a
// replay reapplies instead of re-walking the body. Sound because the
// memo key spells the states of every name the body can observe:
// identical key, identical walk, identical outcome.
type InlineOutcome struct {
	Returned   abstractdomain.AbstractValue
	PostByName []postByNameEntry
	ParamPosts []paramPost
}

// inlineMemoMu guards inlineMemo: the TS source keys this with a
// `WeakMap<ts.Node, Map<string, InlineOutcome>>`; substituted the
// same way as function_summaries.go's effectSummaries (a Go map
// keyed on the stable *ast.Node pointer, guarded by a mutex).
var (
	inlineMemoMu sync.Mutex
	inlineMemo   = map[*ast.Node]map[string]InlineOutcome{}
)

// callNodeIdsMu guards callNodeIds: a stable small id per call node,
// so a callback-carrying call's memo key can spell the node without
// serializing it.
var (
	callNodeIdsMu  sync.Mutex
	callNodeIds    = map[*ast.Node]int{}
	nextCallNodeID = 1
)

// CallNodeIdOf is callNodeIdOf in the TS source.
func CallNodeIdOf(node *ast.Node) int {
	callNodeIdsMu.Lock()
	defer callNodeIdsMu.Unlock()
	if held, ok := callNodeIds[node]; ok {
		return held
	}
	id := nextCallNodeID
	nextCallNodeID++
	callNodeIds[node] = id
	return id
}

// ReplayInline reapplies a remembered inline: the same tracked
// updates, the same parameter write-backs, the same returned
// knowledge — without the body walk. The key guaranteed every
// observed entry state matches, so this IS what the walk would have
// done.
func ReplayInline(ctx *FlowContext, env Env, call *ast.Node, contract FunctionContract, argKnowns []abstractdomain.AbstractValue, held InlineOutcome) abstractdomain.AbstractValue {
	for _, entry := range held.PostByName {
		if _, ok := env[entry.Name]; ok {
			dataflowfacts.UpdateTracked(ctx.Aliases, env, entry.Name, entry.After)
		}
	}
	captured := CapturedOf(contract.Declaration)
	bodyWrites := BodyWritesOf(ctx, contract.Declaration)
	callExpr := call.AsCallExpression()
	for i, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		if i >= len(callExpr.Arguments.Nodes) {
			continue
		}
		argument := callExpr.Arguments.Nodes[i]
		// the same capture rule the live inline applies — a captured
		// reference argument forgets on replay too
		if _, isCaptured := captured[name.Text()]; isCaptured && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
			if ast.IsIdentifier(argument) {
				if _, ok := env[argument.Text()]; ok {
					ctx.Aliases.Havoc(env, argument.Text())
					continue
				}
			}
			ForgetThrough(ctx, env, argument)
			continue
		}
		// the narrowing-only rule holds on replay as well
		if _, writes := bodyWrites[name.Text()]; !writes {
			continue
		}
		if i >= len(held.ParamPosts) || !held.ParamPosts[i].Has {
			continue
		}
		WriteBackParameter(ctx, env, writeBackParameterParams{
			parameter:     parameter,
			post:          held.ParamPosts[i].Value,
			entry:         ParameterKnown(parameter, i, call, argKnowns),
			argument:      argument,
			restArguments: callExpr.Arguments.Nodes[i:],
		})
	}
	return AsCalleeResult(contract, held.Returned)
}

// AsCalleeResult is the caller's view of the returned knowledge: an
// async callee's caller sees a PROMISE of it; a returned promise is
// adopted, never double-wrapped.
func AsCalleeResult(contract FunctionContract, returned abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	isAsync := ast.GetCombinedModifierFlags(contract.Declaration)&ast.ModifierFlagsAsync != 0
	if isAsync && returned.Kind != abstractdomain.KindPromise {
		if returned.Kind == abstractdomain.KindUnknown {
			return silence.Residue()
		}
		inner := returned
		return abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
	}
	return returned
}

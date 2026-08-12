// from interprocedural/function_summaries.ts
//
// Function summaries: what a body provably does to what it was
// handed — effect summaries with constant writes, pure-result
// recovery with its memo, and the recursion markers a
// self-referential recovery joins through. Split from
// evaluate_call.ts per the v2 tree.

package walk

import (
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// callResultsKept is CALL_RESULTS_KEPT in the TS source
// (service/cache_tuning.ts): per function, how many argument
// combinations the checker remembers a result for. service/ is not
// ported yet (go-port-tracker.md: "pending (last)"), so this reads
// the current literal value inline, per the same convention
// abstractdomain.TrustLevelAdmitted uses for STRICTNESS.
const callResultsKept = 256

// ConstantWrite is a TRANSFER for the simplest impure shape: a body
// whose statements are unconditional `param.key = <numeric literal>`
// writes.
type ConstantWrite struct {
	ParamIndex int
	Key        string
	Value      float64
}

// EffectSummary is the per-contract effect summary: whether a body
// is effect-free, self-contained, and (for the simplest impure
// shape) its constant writes.
type EffectSummary struct {
	EffectFree        bool
	SelfContained     bool
	ConstantWrites    []ConstantWrite
	HasConstantWrites bool
}

// impureSummary is IMPURE in the TS source.
var impureSummary = EffectSummary{EffectFree: false, SelfContained: false}

var (
	effectSummariesMu sync.Mutex
	effectSummaries   = map[*ast.Node]EffectSummary{}
	summarizing       = map[*ast.Node]struct{}{}
)

// Summarize is summarize in the TS source.
func Summarize(ctx *FlowContext, contract FunctionContract) EffectSummary {
	declaration := contract.Declaration
	effectSummariesMu.Lock()
	if held, ok := effectSummaries[declaration]; ok {
		effectSummariesMu.Unlock()
		return held
	}
	if _, cycling := summarizing[declaration]; cycling {
		effectSummariesMu.Unlock()
		// a cycle: assume the best; the cycle's own writes (if any)
		// already made every member impure at its own scan
		return EffectSummary{EffectFree: true, SelfContained: true}
	}
	summarizing[declaration] = struct{}{}
	effectSummariesMu.Unlock()
	var summary EffectSummary
	if tracing.Recording(tracing.GrainStep) {
		summary = tracing.Span("effectScan", func() EffectSummary {
			return scanBody(ctx, contract)
		}, tracing.GrainStep)
		tracing.Count("effectScan", 0)
	} else {
		summary = scanBody(ctx, contract)
	}
	effectSummariesMu.Lock()
	delete(summarizing, declaration)
	effectSummaries[declaration] = summary
	effectSummariesMu.Unlock()
	return summary
}

func scanBody(ctx *FlowContext, contract FunctionContract) EffectSummary {
	body := contract.Declaration.Body()
	if body == nil {
		return impureSummary
	}
	parameters := map[string]struct{}{}
	for _, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			return impureSummary
		}
		parameters[name.Text()] = struct{}{}
	}
	locals := map[string]struct{}{}
	declaredNames(body, locals)
	effectFree := true
	selfContained := true
	// callee NAMES the call branch already judged: a call to a pure
	// self-contained contracted function keeps the caller a function
	// of its arguments, so the name itself must not read as "something
	// of the caller's world" when the identifier scan reaches it
	judgedCallees := map[*ast.Node]struct{}{}
	var scan func(node *ast.Node)
	scan = func(node *ast.Node) {
		if !effectFree {
			return
		}
		// the write constructs — the model's only direct effects
		if ast.IsBinaryExpression(node) {
			be := node.AsBinaryExpression()
			if be.OperatorToken.Kind >= ast.KindFirstAssignment && be.OperatorToken.Kind <= ast.KindLastAssignment {
				effectFree = false
				return
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			operator := node.AsPrefixUnaryExpression().Operator
			if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
				effectFree = false
				return
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			operator := node.AsPostfixUnaryExpression().Operator
			if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
				effectFree = false
				return
			}
		}
		if ast.IsDeleteExpression(node) || ast.IsNewExpression(node) {
			effectFree = false
			return
		}
		// a nested function VALUE could be handed anywhere — impure
		if ast.IsArrowFunction(node) || ast.IsFunctionExpression(node) || ast.IsFunctionDeclaration(node) {
			effectFree = false
			return
		}
		if ast.IsCallExpression(node) {
			callExpr := node.AsCallExpression()
			callee := callExpr.Expression
			// the READ-ONLY lib statics prove nothing they are handed is
			// written — a predicate calling Array.isArray or
			// Number.isInteger stays effect-free (counting these impure
			// re-opened the narrowing write-back through nested
			// predicates)
			readOnlyStatic := false
			if ast.IsPropertyAccessExpression(callee) {
				pae := callee.AsPropertyAccessExpression()
				if ast.IsIdentifier(pae.Expression) && resolvesToDefaultLib(ctx, pae.Expression) {
					receiverName := pae.Expression.Text()
					methodName := pae.Name().Text()
					switch {
					case receiverName == "Math":
						readOnlyStatic = true
					case receiverName == "Array" && methodName == "isArray":
						readOnlyStatic = true
					case receiverName == "Number" && (methodName == "isInteger" || methodName == "isFinite" || methodName == "isNaN" || methodName == "isSafeInteger"):
						readOnlyStatic = true
					case receiverName == "Object" && (methodName == "keys" || methodName == "values" || methodName == "entries" || methodName == "hasOwn" || methodName == "is"):
						readOnlyStatic = true
					case receiverName == "JSON" && methodName == "stringify":
						readOnlyStatic = true
					}
				}
			}
			if !readOnlyStatic {
				called := ContractOf(ctx, callee)
				_, isParam := parameters[calleeIdentifierText(callee)]
				if called == nil || (ast.IsIdentifier(callee) && isParam) || !Summarize(ctx, *called).EffectFree {
					effectFree = false
					return
				}
				if Summarize(ctx, *called).SelfContained {
					// the callee is a fixed pure function of ITS arguments —
					// its name is accounted for here, not by the identifier
					// scan (contractOf already refuses reassigned names)
					if ast.IsIdentifier(callee) {
						judgedCallees[callee] = struct{}{}
					}
				} else {
					selfContained = false
				}
			}
			// arguments still scan below
		}
		if ast.IsIdentifier(node) && selfContained {
			if _, judged := judgedCallees[node]; !judged {
				name := node.Text()
				_, isParam := parameters[name]
				_, isLocal := locals[name]
				if !isParam && !isLocal && name != "Infinity" && name != "NaN" && name != "undefined" && !resolvesToDefaultLib(ctx, node) {
					// reads something of the caller's world (an outer binding, a
					// schema, another function): the result is not a function of
					// the arguments alone
					selfContained = false
				}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			scan(child)
			return false
		})
	}
	scan(body)
	if effectFree {
		return EffectSummary{EffectFree: effectFree, SelfContained: selfContained}
	}
	if cw, ok := constantWriteSummary(contract, parameters); ok {
		return cw
	}
	return impureSummary
}

// calleeIdentifierText: the callee's text when it is a plain
// identifier, "" otherwise — a small helper so the parameter-name
// membership test above reads plainly (TS: `ts.isIdentifier(callee)
// && parameters.has(callee.text)`, evaluated as one guard).
func calleeIdentifierText(callee *ast.Node) string {
	if ast.IsIdentifier(callee) {
		return callee.Text()
	}
	return ""
}

// constantWriteSummary is the constant-write transfer: a body whose
// statements are ALL unconditional `param.key = <numeric literal>`
// writes, optionally ending in a return (the stated result covers
// it). No branches, no loops, no calls, no throw — anything richer
// keeps the full inline.
func constantWriteSummary(contract FunctionContract, parameters map[string]struct{}) (EffectSummary, bool) {
	body := contract.Declaration.Body()
	if body == nil || !ast.IsBlock(body) {
		return EffectSummary{}, false
	}
	indexOf := map[string]int{}
	for i, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if ast.IsIdentifier(name) {
			indexOf[name.Text()] = i
		}
	}
	statements := body.AsBlock().Statements.Nodes
	var writes []ConstantWrite
	for i, statement := range statements {
		if ast.IsReturnStatement(statement) {
			// a trailing return whose value the STATED result already
			// enforces; a richer result would need the body — bail
			if contract.Result != nil && contract.Result.Kind != annotations.DeclaredVariable && i == len(statements)-1 {
				continue
			}
			return EffectSummary{}, false
		}
		if !ast.IsExpressionStatement(statement) {
			return EffectSummary{}, false
		}
		e := statement.AsExpressionStatement().Expression
		if !ast.IsBinaryExpression(e) {
			return EffectSummary{}, false
		}
		be := e.AsBinaryExpression()
		if be.OperatorToken.Kind != ast.KindEqualsToken || !ast.IsPropertyAccessExpression(be.Left) {
			return EffectSummary{}, false
		}
		pae := be.Left.AsPropertyAccessExpression()
		if !ast.IsIdentifier(pae.Expression) || !ast.IsNumericLiteral(be.Right) {
			return EffectSummary{}, false
		}
		paramIndex, ok := indexOf[pae.Expression.Text()]
		if !ok {
			return EffectSummary{}, false
		}
		if _, isParam := parameters[pae.Expression.Text()]; !isParam {
			return EffectSummary{}, false
		}
		writes = append(writes, ConstantWrite{
			ParamIndex: paramIndex,
			Key:        pae.Name().Text(),
			Value:      float64(jsnum.FromString(be.Right.Text())),
		})
	}
	if len(writes) == 0 {
		return EffectSummary{}, false
	}
	return EffectSummary{EffectFree: false, SelfContained: false, ConstantWrites: writes, HasConstantWrites: true}, true
}

// ApplyConstantWrites applies a constant-write transfer at a call
// site: the same writes the inline would compute, applied
// class-aware to the identifier arguments — O(writes) instead of a
// body re-walk.
func ApplyConstantWrites(ctx *FlowContext, env Env, call *ast.Node, writes []ConstantWrite) {
	callExpr := call.AsCallExpression()
	for _, write := range writes {
		if write.ParamIndex < 0 || write.ParamIndex >= len(callExpr.Arguments.Nodes) {
			continue
		}
		target := callExpr.Arguments.Nodes[write.ParamIndex]
		for target != nil && (ast.IsParenthesizedExpression(target) || ast.IsAsExpression(target)) {
			if ast.IsParenthesizedExpression(target) {
				target = target.AsParenthesizedExpression().Expression
			} else {
				target = target.AsAsExpression().Expression
			}
		}
		if target == nil {
			continue
		}
		if ast.IsIdentifier(target) {
			if held, ok := env[target.Text()]; ok {
				if held.Kind == abstractdomain.KindObject {
					next := make([]abstractdomain.ObjectKey, 0, len(held.Keys)+1)
					found := false
					for _, k := range held.Keys {
						if k.Name == write.Key {
							next = append(next, abstractdomain.ObjectKey{Name: write.Key, Value: abstractdomain.KnownValues([]float64{write.Value}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)})
							found = true
						} else {
							next = append(next, k)
						}
					}
					if !found {
						next = append(next, abstractdomain.ObjectKey{Name: write.Key, Value: abstractdomain.KnownValues([]float64{write.Value}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)})
					}
					dataflowfacts.UpdateTracked(ctx.Aliases, env, target.Text(), abstractdomain.KnownObject(next, nil, false, abstractdomain.TrustProved, false))
				} else {
					ctx.Aliases.Havoc(env, target.Text())
				}
				continue
			}
		}
		if dataflowfacts.ReferenceTyped(ctx.P.Checker, target) {
			ForgetThrough(ctx, env, target)
		}
	}
}

// recoveryMemoMu guards recoveryMemo: the memoized recovery for a
// SELF-CONTAINED effect-free callee. The TS source keys this with a
// `WeakMap<ts.Node, Map<string, AbstractValue>>`; substituted the
// same way as effectSummaries above.
var recoveryMemoMu sync.Mutex
var recoveryMemo = map[*ast.Node]map[string]abstractdomain.AbstractValue{}

// jsonStringifyArgKnowns is the TS source's `JSON.stringify(argKnowns)`
// inside RecoverPure's try/catch: a memo key from the argument
// knowledge, "" where a value refuses a spelling — those calls run
// unmemoized rather than mis-keyed. Spelled by the builder speller,
// not encoding/json: json refuses NaN/±Inf outright, and ±Inf is the
// bare number set's own bound, so marshaling silently unkeyed the
// common case (the same disease the inline memo key had).
func jsonStringifyArgKnowns(argKnowns []abstractdomain.AbstractValue) string {
	var b strings.Builder
	for _, arg := range argKnowns {
		spelled, ok := abstractdomain.SpellForMemoKey(arg)
		if !ok {
			return ""
		}
		b.WriteString(spelled)
		b.WriteByte('\x1e')
	}
	return b.String()
}

// RecoverPure is recoverPure in the TS source.
func RecoverPure(ctx *FlowContext, call *ast.Node, contract FunctionContract, argKnowns []abstractdomain.AbstractValue, memoize bool) abstractdomain.AbstractValue {
	if !tracing.Recording(tracing.GrainStep) {
		return recoverPureBody(ctx, call, contract, argKnowns, memoize)
	}
	return tracing.Span("recoverPure", func() abstractdomain.AbstractValue {
		return recoverPureBody(ctx, call, contract, argKnowns, memoize)
	}, tracing.GrainStep)
}

func recoverPureBody(ctx *FlowContext, call *ast.Node, contract FunctionContract, argKnowns []abstractdomain.AbstractValue, memoize bool) abstractdomain.AbstractValue {
	body := contract.Declaration.Body()
	if body == nil {
		return silence.Residue()
	}
	var memo map[string]abstractdomain.AbstractValue
	key := ""
	if memoize {
		// knowledge carrying object references (a refinement variable's
		// symbol, a stated annotation's nodes) has no plain spelling —
		// those calls run unmemoized rather than mis-keyed
		key = jsonStringifyArgKnowns(argKnowns)
		if key != "" {
			recoveryMemoMu.Lock()
			memo = recoveryMemo[contract.Declaration]
			if memo == nil {
				memo = map[string]abstractdomain.AbstractValue{}
				recoveryMemo[contract.Declaration] = memo
			}
			held, ok := memo[key]
			recoveryMemoMu.Unlock()
			if ok {
				return held
			}
		}
	}
	// the kernel summary: a lowerable body walks ENGINE-SIDE from the
	// argument states — one proved answer (walk_sound) instead of a JS
	// re-walk per distinct argument tuple. A body or argument set the
	// lowering cannot spell falls through to the inline walk below.
	// Composition resolves through the contract registry: a called
	// name that is a PURE contracted function hands its declaration to
	// the lowering, which inlines its body into fresh slots.
	if summarized, ok := SummaryResult(contract.Declaration, argKnowns, func(callee *ast.Node) *ast.Node {
		called := ContractOf(ctx, callee)
		if called == nil || called.Declaration.Body() == nil {
			return nil
		}
		if !Summarize(ctx, *called).EffectFree {
			return nil
		}
		return called.Declaration
	}); ok {
		if memo != nil && key != "" {
			recoveryMemoMu.Lock()
			memo[key] = summarized
			recoveryMemoMu.Unlock()
		}
		return summarized
	}
	callExpr := call.AsCallExpression()
	var calleeName *ast.Node
	if ast.IsIdentifier(callExpr.Expression) {
		calleeName = callExpr.Expression
	} else if ast.IsPropertyAccessExpression(callExpr.Expression) {
		calleeName = callExpr.Expression.AsPropertyAccessExpression().Name()
	}
	if calleeName == nil {
		return silence.Residue()
	}
	symbol := ctx.P.Checker.GetSymbolAtLocation(calleeName)
	if symbol == nil {
		return silence.Residue()
	}
	inlining := ctx.Inlining
	if inlining == nil {
		inlining = map[*ast.Symbol]struct{}{}
	}
	if _, ok := inlining[symbol]; ok {
		return RecursionMarker(symbol)
	}
	inlining[symbol] = struct{}{}
	callEnv := Env{}
	for i, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			continue
		}
		callEnv[name.Text()] = ParameterKnown(parameter, i, call, argKnowns)
	}
	var sink []abstractdomain.AbstractValue
	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}
	silent.ReturnSink = &sink
	silent.Inlining = inlining
	silent.Declared = map[string]*annotations.DeclaredRefinement{}
	dropsBefore := MarkerDropCount()
	value := silence.Residue()
	if ast.IsBlock(body) {
		AnalyzeStatements(&silent, callEnv, body.AsBlock().Statements.Nodes, nil)
		if len(sink) > 0 {
			value = JoinSinkSummarized(sink)
		}
	} else {
		value = evaluateExpression(&silent, callEnv, body)
	}
	if markerKey(value) == symbol {
		value = silence.Residue()
	}
	delete(inlining, symbol)
	// a summary that dropped a marker (or IS one) holds only inside
	// its own induction — never memoized
	isMarker := markerKey(value) != nil
	if memo != nil && MarkerDropCount() == dropsBefore && !isMarker {
		recoveryMemoMu.Lock()
		if len(memo) >= callResultsKept {
			for k := range memo {
				delete(memo, k)
				break
			}
		}
		memo[key] = value
		recoveryMemoMu.Unlock()
	}
	return value
}

/* ── recursive summaries: the pass-through induction ─────────────── */

// A recursive call inside its own inline answers a MARKER instead of
// unknown. A marker that reaches the callee's return sink as a WHOLE
// entry is a pass-through (`return f(x - 1)`): every terminating
// run's value originates at some base-case return, so dropping the
// marker and joining the bases IS the claim — the same induction the
// loop solver's certificate runs over iterates. A marker that flows
// into anything else (arithmetic, a join through a binding) launders
// to unknown through the transfers, declining honestly. A summary
// that dropped a marker holds only inside its own induction, so it
// poisons memoization.
//
// The TS source's markerBySymbol/markerSymbols are WeakMaps keyed by
// (respectively) ts.Symbol and the AbstractValue object itself — an
// AbstractValue is a Go struct, not comparable by pointer identity
// the way a JS object is, so markerSymbols here keys on the marker's
// OWN symbol pointer directly (markerKey), which is exactly the
// identity the TS WeakMap tracked (every marker is
// `{ kind: "unknown" }` freshly built once per symbol in
// RecursionMarker, so its identity IS the symbol that produced it).
var (
	markerMu       sync.Mutex
	markerBySymbol = map[*ast.Symbol]abstractdomain.AbstractValue{}
	markerDrops    int
)

// markerKey answers the symbol a value's marker identity keys on, or
// nil when the value is not (recognizably) a marker. Because
// AbstractValue is not pointer-comparable, this walks markerBySymbol
// to find a structurally-equal marker — the marker shape is always
// exactly `{Kind: KindUnknown}` with no other fields, so equality is
// unambiguous, and the map is small (one entry per recursive
// symbol in view). This is the port's stand-in for the TS source's
// exported `markerSymbols` WeakMap (`markerSymbols.get(value)`) —
// call sites elsewhere in this package (inline_contract_body.go,
// inline_replay.go) call MarkerKey where the TS source reads
// markerSymbols directly.
func markerKey(value abstractdomain.AbstractValue) *ast.Symbol {
	markerMu.Lock()
	defer markerMu.Unlock()
	for symbol, marker := range markerBySymbol {
		if abstractdomain.SameKnown(marker, value) {
			return symbol
		}
	}
	return nil
}

// MarkerKey is the exported form of markerKey — the port's stand-in
// for the TS source's exported `markerSymbols` WeakMap read as
// `markerSymbols.get(value)`.
func MarkerKey(value abstractdomain.AbstractValue) *ast.Symbol {
	return markerKey(value)
}

// MarkerDropCount reports how many recursion markers have been
// dropped so far — a memo comparing before/after knows whether a
// walk's answer leaned on its own induction (never cacheable).
func MarkerDropCount() int {
	markerMu.Lock()
	defer markerMu.Unlock()
	return markerDrops
}

// RecursionMarker is recursionMarker in the TS source.
func RecursionMarker(symbol *ast.Symbol) abstractdomain.AbstractValue {
	markerMu.Lock()
	defer markerMu.Unlock()
	held, ok := markerBySymbol[symbol]
	if !ok {
		held = abstractdomain.AbstractValue{Kind: abstractdomain.KindUnknown}
		markerBySymbol[symbol] = held
	}
	return held
}

// JoinSinkSummarized is the sink joined with pass-through markers
// dropped; a sink of ONLY markers claims nothing.
func JoinSinkSummarized(sink []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	var bases []abstractdomain.AbstractValue
	for _, entry := range sink {
		if markerKey(entry) != nil {
			markerMu.Lock()
			markerDrops++
			markerMu.Unlock()
			continue
		}
		bases = append(bases, entry)
	}
	if len(bases) == 0 {
		return silence.Residue()
	}
	joined := bases[0]
	for _, b := range bases[1:] {
		joined = abstractdomain.JoinKnown(joined, b)
	}
	return joined
}

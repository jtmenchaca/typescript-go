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
	"github.com/microsoft/typescript-go/internal/refinedts/program"
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
		if ast.IsIdentifier(name) {
			parameters[name.Text()] = struct{}{}
			continue
		}
		// a DESTRUCTURED parameter (`[a]: number[]`, `{age}: Person`)
		// binds no whole-name identifier at all — its own leaves are
		// what the body reads (`return a;`), so those are what the
		// identifier scan below must recognize as "the arguments alone,"
		// not "something of the caller's world." Bailing to impureSummary
		// here (the old rule) answered EffectFree=false for EVERY
		// destructured parameter, whatever its body did — a body reading
		// only its own bound leaves is exactly the pure, self-contained
		// shape this function exists to recognize.
		if ast.IsObjectBindingPattern(name) || ast.IsArrayBindingPattern(name) {
			for _, leaf := range boundPatternNames(name) {
				parameters[leaf] = struct{}{}
			}
			continue
		}
		return impureSummary
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
		// a `this` read: a METHOD's result can depend on its receiver's
		// own fields (`this.#age`), which the parameters alone never
		// name — RecoverPure's own route (recoverPureBody,
		// function_summaries.go) has no receiver to hand the kernel
		// summary or the walk-route recovery; it calls SummaryResultIn,
		// which always fills a method's this-entries from
		// unknownReceiver() (kernel_summaries.go), TOPPING every field a
		// real receiver would have named exactly. Marking `this` non-self-
		// contained routes the call to the full inline instead
		// (evaluate_call_expression.go's EffectFree branch), whose
		// InlineContractBody reads the call's own receiver through
		// SummaryCallReceiver and threads it to KernelSummaryDirectOn /
		// ClassMethodWalkCall — the routes that answer the receiver's own
		// field value rather than a class-wide TOP.
		if node.Kind == ast.KindThisKeyword {
			selfContained = false
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
//
// A write names the PARAMETER it lands on, so the positions read here
// are the effective ones: a call spreading an exact source writes
// through the argument that really bound that parameter. A position
// with no expression behind it — a tagged template's template object,
// an item expanded out of a spread — is nil and the guard below skips
// it, which is the same nothing a spread's own slot did before.
func ApplyConstantWrites(ctx *FlowContext, env Env, effective EffectiveArguments, writes []ConstantWrite) {
	arguments := effective.Nodes
	for _, write := range writes {
		if write.ParamIndex < 0 || write.ParamIndex >= len(arguments) {
			continue
		}
		target := arguments[write.ParamIndex]
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
			if held, ok := env.Get(target.Text()); ok {
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
					UpdateTrackedEnv(ctx.Aliases, env, target.Text(), abstractdomain.KnownObject(next, nil, false, abstractdomain.TrustProved, false))
				} else {
					HavocEnv(ctx.Aliases, env, target.Text())
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
// SELF-CONTAINED effect-free callee, keyed by the CHECK first — a
// recovery composes through the entry's own contract registry, and a
// cross-entry store made verdicts vary with scheduling order (the
// same defect inline_replay.go's inlineMemo comment names).
var recoveryMemoMu sync.Mutex
var recoveryMemo = map[*program.CheckerProgram]map[*ast.Node]map[string]abstractdomain.AbstractValue{}

// jsonStringifyArgKnowns is the TS source's `JSON.stringify(argKnowns)`
// inside RecoverPure's try/catch: a memo key from the argument
// knowledge, "" where a value refuses a spelling — those calls run
// unmemoized rather than mis-keyed. Spelled by the builder speller,
// not encoding/json: json refuses NaN/±Inf outright, and ±Inf is the
// bare number set's own bound, so marshaling silently unkeyed the
// common case (the same disease the inline memo key had).
//
// The list's EXACTNESS spells too, for the reason the inline memo key
// spells it: a REST parameter binds a rest list off an exact list and
// residue off an inexact one, so two lists spelling the same values
// can still bind differently.
func jsonStringifyArgKnowns(effective EffectiveArguments) string {
	var b strings.Builder
	if !effective.Exact {
		b.WriteString("~\x1e")
	}
	for _, arg := range effective.Knowns {
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
func RecoverPure(ctx *FlowContext, call *ast.Node, contract FunctionContract, effective EffectiveArguments, memoize bool) abstractdomain.AbstractValue {
	if !tracing.Recording(tracing.GrainStep) {
		return recoverPureBody(ctx, call, contract, effective, memoize)
	}
	return tracing.Span("recoverPure", func() abstractdomain.AbstractValue {
		return recoverPureBody(ctx, call, contract, effective, memoize)
	}, tracing.GrainStep)
}

func recoverPureBody(ctx *FlowContext, call *ast.Node, contract FunctionContract, effective EffectiveArguments, memoize bool) abstractdomain.AbstractValue {
	argKnowns := effective.Knowns
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
		key = jsonStringifyArgKnowns(effective)
		if key != "" {
			recoveryMemoMu.Lock()
			shelf := recoveryMemo[ctx.P]
			if shelf == nil {
				shelf = map[*ast.Node]map[string]abstractdomain.AbstractValue{}
				recoveryMemo[ctx.P] = shelf
			}
			memo = shelf[contract.Declaration]
			if memo == nil {
				memo = map[string]abstractdomain.AbstractValue{}
				shelf[contract.Declaration] = memo
			}
			held, ok := memo[key]
			recoveryMemoMu.Unlock()
			if ok {
				return held
			}
		}
	}
	// the kernel summary: the declaration's compiled summary applied
	// at the argument states — one proved answer (walk_sound +
	// summarize_eq) instead of a JS re-walk per distinct argument
	// tuple. A body the lowering cannot spell falls through to the
	// inline walk below. Composition resolves through the walk's own
	// contract registry, which needs the flow context — not the bare
	// checker program.
	//
	// EXACT rides along: this call's own effective arguments are what
	// SummaryResultExactIn compares a TOP-fed rest parameter against
	// (its own comment) — an inexact call (an unread spread) never
	// qualifies, exactly as ParameterKnown itself declines a rest
	// reading past one.
	summaryResult := SummaryResultIn
	if effective.Exact {
		summaryResult = SummaryResultExactIn
	}
	if summarized, ok := summaryResult(ctx, contract.Declaration, argKnowns); ok {
		if memo != nil && key != "" {
			recoveryMemoMu.Lock()
			memo[key] = summarized
			recoveryMemoMu.Unlock()
		}
		return summarized
	}
	callee := CalleeExpressionOf(call)
	var calleeName *ast.Node
	if callee != nil && ast.IsIdentifier(callee) {
		calleeName = callee
	} else if callee != nil && ast.IsPropertyAccessExpression(callee) {
		calleeName = callee.AsPropertyAccessExpression().Name()
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
		// no reference-argument forget here, deliberately — unlike the
		// inline route's recursion arm (inline_contract_body.go, the
		// tailwindcss fix): this route serves only through Summarize's
		// EffectFree && SelfContained gate, and an effect-free callee
		// cannot have mutated a reference argument or a closed-over name
		// on any path, recursive ones included. The gate is the guard.
		return RecursionMarker(ctx, symbol, contract.Declaration)
	}
	inlining[symbol] = struct{}{}
	callEnv := NewEnv()
	for i, parameter := range contract.Declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if !ast.IsIdentifier(name) {
			// a destructured parameter (`[a]`, `{age}`) binds every leaf
			// its pattern names, the same route the inline body walk
			// binds through — an identifier-only loop here left `a`
			// unbound whole, not merely widened to the plain type
			BindInlineParameter(ctx, callEnv, parameter, i, effective)
			continue
		}
		// the same declared-type meet the inline route binds through: one
		// call site's complete key set is not an open-map parameter's
		callEnv.Set(name.Text(), BoundParameterKnown(ctx, parameter, i, effective))
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
	if IsMarkerOf(value, symbol) {
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
// identity the TS WeakMap tracked (every marker is built once per
// symbol in RecursionMarker — today the callee's own declared return
// type ground where one is readable, `{Kind: KindUnknown}` where none
// is — so its identity IS the symbol that produced it, whatever it
// happens to hold).
var (
	markerMu       sync.Mutex
	markerBySymbol = map[*ast.Symbol]abstractdomain.AbstractValue{}
	markerDrops    int
)

// markerKey answers SOME symbol a value's marker identity could key
// on, or nil when the value is not (recognizably) a marker at all.
// Because AbstractValue is not pointer-comparable, this walks
// markerBySymbol to find a structurally-equal marker — SameKnown
// compares by SHAPE, not by which call built the value, so two
// DIFFERENT symbols whose markers happen to hold the same shape (a
// bare `{Kind: KindUnknown}` where neither declared return type read;
// two callees that both ground to plain `number`, e.g.) are the same
// value by that equality — markerBySymbol growing past one live entry
// with a shared shape (the registry is never cleared; every recursive
// function analyzed anywhere in the process leaves its symbol keyed
// here) makes which symbol comes back a matter of Go's map iteration
// order, not identity. This is the port's stand-in for the TS
// source's exported `markerSymbols` WeakMap (`markerSymbols.get(value)`),
// sound only where markerBySymbol holds at most one entry OF A GIVEN
// SHAPE — callers that need to test ONE PARTICULAR symbol's marker
// must use IsMarkerOf instead, which never scans and is never
// ambiguous.
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

// MarkerKey is the exported form of markerKey — safe for an "is this
// value A marker at all" test (every call site outside this file
// compares its result against nil, never against a specific symbol);
// see markerKey's own doc for why comparing the result against ONE
// symbol is unsound once more than one symbol's marker has ever been
// built in this process. Callers asking "is value THIS symbol's own
// marker" want IsMarkerOf.
func MarkerKey(value abstractdomain.AbstractValue) *ast.Symbol {
	return markerKey(value)
}

// IsMarkerOf answers whether value is EXACTLY symbol's own recursion
// marker — a direct forward-map read (markerBySymbol[symbol]) compared
// against value, never a reverse scan, so it stays exact regardless of
// how many other symbols' markers the process-lifetime registry also
// holds. This is the identity test inline_contract_body.go's own
// induction-answer guard needs (was `MarkerKey(summarized) == symbol`,
// ambiguous the moment a second recursive symbol's marker exists
// anywhere in markerBySymbol).
func IsMarkerOf(value abstractdomain.AbstractValue, symbol *ast.Symbol) bool {
	markerMu.Lock()
	defer markerMu.Unlock()
	held, ok := markerBySymbol[symbol]
	return ok && abstractdomain.SameKnown(held, value)
}

// MarkerDropCount reports how many recursion markers have been
// dropped so far — a memo comparing before/after knows whether a
// walk's answer leaned on its own induction (never cacheable).
func MarkerDropCount() int {
	markerMu.Lock()
	defer markerMu.Unlock()
	return markerDrops
}

// RecursionMarker is recursionMarker in the TS source, widened past
// the bare `{Kind: KindUnknown}` sentinel: an in-flight recursive
// call's VALUE is, at worst, the callee's own declared return type's
// GROUND — tsc already checked the body against that signature, the
// same standing wornReturnTypeIfUnknown's own worn-ground exemption
// rests on (evaluate_call_expression.go). `declaration` is the
// callee's function-like node (contract.Declaration at every
// production call site); DeclaredReturnTypeGround reads its resolved
// signature's return type through the same typeGroundOf part-walk
// ReturnTypeGround uses for a call site, anchored on the declaration
// itself since an in-flight call has no settled call-site type yet.
// `ctx` or `declaration` being nil (a unit test building a marker
// with no program in reach) or the ground reader declining (an
// unspellable return type) both fall back to the old bare unknown —
// there is nothing sound to wear, so the marker wears nothing, same
// as before this widening.
//
// The marker stays a marker by IDENTITY, not by shape: IsMarkerOf
// reads markerBySymbol[symbol] directly (function_summaries.go's own
// doc on markerKey/IsMarkerOf), so a ground-carrying marker is
// exactly as recognizable as the old bare-unknown one — every
// consumer that tests "is this value a recursion marker" already
// keys on the registry, never on Kind == KindUnknown.
func RecursionMarker(ctx *FlowContext, symbol *ast.Symbol, declaration *ast.Node) abstractdomain.AbstractValue {
	markerMu.Lock()
	held, ok := markerBySymbol[symbol]
	markerMu.Unlock()
	if ok {
		return held
	}
	held = abstractdomain.AbstractValue{Kind: abstractdomain.KindUnknown}
	if ground := DeclaredReturnTypeGround(ctx, declaration); ground != nil {
		held = *ground
	}
	markerMu.Lock()
	defer markerMu.Unlock()
	// a concurrent caller may have raced this one to the same symbol
	// while the lock was released for the ground read above — the
	// FIRST value stored wins, so every later reader of this symbol's
	// marker (a forward-map read, IsMarkerOf) sees one stable value
	if existing, raced := markerBySymbol[symbol]; raced {
		return existing
	}
	markerBySymbol[symbol] = held
	return held
}

// JoinSinkSummarized is the sink joined with every marker COUNTED as
// an unknown contribution rather than excluded: a marker means "this
// branch's own return is still being derived, in the enclosing walk
// that is inlining it" — `countdown(n-1)` reached from inside
// `countdown`'s own recursive branch reads exactly this way — and a
// branch whose value is not yet known joins as unknown the same way
// any other undetermined operand would, poisoning the result to
// honest imprecision rather than silently answering as though the
// recursive branch had contributed nothing at all. Before this, a
// sink of `[0 (the base case), marker (the recursive branch)]` joined
// to the base case ALONE — `countdown(depth)` answered the exact
// literal `0` for every `depth`, including `NaN`/`Infinity`, where the
// recursive branch genuinely never resolves to 0 or anything else.
// A sink of ONLY markers still claims nothing DETERMINED (unknown
// either way), but the value handed back is the FIRST marker entry
// itself, not a freshly built residue: InlineContractBody's own
// induction-naming guard (IsMarkerOf(summarized, symbol)) needs this
// return to still carry this call's own marker identity so it can
// recognize "the whole answer leaned on the induction" and rebuild it
// with that sentence, rather than losing the identity to a plain
// unknown a caller cannot tell apart from any other decline. Every
// entry in an all-marker sink is SOME symbol's marker (possibly more
// than one, in nested recursion); the first stands for the sink the
// same way bases[0] seeds the ordinary join below — a caller testing
// ONE particular symbol's identity (IsMarkerOf) still gets an exact
// answer regardless of which marker this picks, since a value that is
// not that symbol's own marker answers false either way.
func JoinSinkSummarized(sink []abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	var bases []abstractdomain.AbstractValue
	var firstMarker *abstractdomain.AbstractValue
	sawMarker := false
	for _, entry := range sink {
		if markerKey(entry) != nil {
			markerMu.Lock()
			markerDrops++
			markerMu.Unlock()
			sawMarker = true
			if firstMarker == nil {
				v := entry
				firstMarker = &v
			}
			continue
		}
		bases = append(bases, entry)
	}
	if len(bases) == 0 {
		if firstMarker != nil {
			return *firstMarker
		}
		return silence.Residue()
	}
	joined := bases[0]
	for _, b := range bases[1:] {
		joined = abstractdomain.JoinKnown(joined, b)
	}
	if sawMarker {
		joined = abstractdomain.JoinKnown(joined, silence.Residue())
	}
	return joined
}

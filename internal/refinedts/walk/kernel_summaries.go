// from interprocedural/kernel_summaries.ts
//
// The callee summary: a function body lowers to the kernel's flow IR
// ONCE — returns encoded through a result slot and a done flag over
// the existing proved grammar (lowering_to_kernel_ir) — and every
// call walks it kernel-side from the call's own argument states.
// Every piece the walk composes is individually proved and the
// composition theorem (walk_sound, set_functions/walk.lean) covers
// the whole body, returns included, because the encoding uses only
// the proved statements: `return e` is an assignment pair, and the
// continuation after a returning branch runs under an ordinary
// branch on the flag.
//
// A body the lowering cannot spell — a call, an object, a string
// method, a loop that returns — declines here and keeps today's JS
// inline walk. The ledger counts which route served.

package walk

import (
	"strings"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// slotBudget is SLOT_BUDGET in the TS source: slots past this stop
// paying for themselves — a body carrying more locals than this is
// not the small computation summaries serve.
const slotBudget = 32

// kernelSummary is Summary in the TS source (renamed to avoid
// colliding with function_summaries.go's exported Summarize/
// EffectSummary vocabulary — the TWO "summary" concepts in this
// directory are unrelated: an effect summary and a kernel-lowering
// summary).
type kernelSummary struct {
	Stmts      []kernelbridge.IrStatement
	ParamCount int
	DoneIndex  int
	RetIndex   int
	// SlotCount is every slot, composition's grown ones included —
	// the walk's state vector is this long.
	SlotCount int
}

// kernelSummariesMu guards kernelSummaries: lowered bodies per
// declaration, keyed inside by the parameter sorts the call
// supplied — the sorts gate which tests the lowering admits, so
// different sort vectors are different lowerings. A summaryEntry
// with Ok false remembers a body that declined (the TS source's Map
// value of `null`, distinguished from "no entry yet" the same way
// class_field_invariants.go's invariantMemoSet distinguishes
// re-entry from "no answer computed").
type summaryEntry struct {
	Summary kernelSummary
	Ok      bool
}

var (
	kernelSummariesMu sync.Mutex
	kernelSummaries   = map[*ast.Node]map[string]summaryEntry{}
)

// absentState is ABSENT in the TS source: the definitely-undefined
// entry state — no real value, absent.
var absentState = kernelbridge.KnownStateWire{
	Set:    refinementsets.MakeRefinedSet(refinementsets.OneOf(nil)),
	Absent: true,
}

// doneDownState is DONE_DOWN in the TS source: the done flag's entry
// state — exactly "not yet returned".
var doneDownState = kernelbridge.KnownStateWire{
	Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
}

// mayContainZero is mayContainZero in the TS source: whether a
// scalar set may admit 0 — the flag-still-down question. The forms
// list is an intersection, so EVERY form must admit 0; a shape this
// reader cannot judge answers true, which only wraps the result in a
// spurious maybe, never drops a real one.
func mayContainZero(set refinementsets.RefinedSet) bool {
	admits := func(f refinementsets.Refinement) bool {
		switch f.Form {
		case refinementsets.FormOneOf:
			for _, w := range f.W {
				if w == 0 {
					return true
				}
			}
			return false
		case refinementsets.FormAtLeast:
			return f.A <= 0
		case refinementsets.FormAbove:
			return f.A < 0
		case refinementsets.FormAtMost:
			return f.A >= 0
		case refinementsets.FormBelow:
			return f.A > 0
		case refinementsets.FormInteger, refinementsets.FormMultipleOf:
			return true
		case refinementsets.FormUnion:
			return mayContainZero(*f.A_) || mayContainZero(*f.B)
		case refinementsets.FormDifference:
			// a subtrahend that is EXACTLY a value list holding 0 removes
			// it for certain; any other subtrahend may or may not, so the
			// minuend answers
			removesZero := false
			if len(f.B.Forms) == 1 && f.B.Forms[0].Form == refinementsets.FormOneOf {
				for _, w := range f.B.Forms[0].W {
					if w == 0 {
						removesZero = true
						break
					}
				}
			}
			return !removesZero && mayContainZero(*f.A_)
		default:
			return true
		}
	}
	for _, f := range set.Forms {
		if !admits(f) {
			return false
		}
	}
	return true
}

// typeofOfKnown is the typeof evidence an argument's knowledge
// carries: a values knowledge names its primitive kind outright;
// anything else claims nothing — a set spelled by narrowing could
// stand for a boolean riding the number sort, so it must not pin
// "number".
func typeofOfKnown(k abstractdomain.AbstractValue) TypeofTag {
	switch k.Kind {
	case abstractdomain.KindValues:
		switch k.KindTag {
		case abstractdomain.PrimitiveNumber:
			return TypeofTagNumber
		case abstractdomain.PrimitiveString:
			return TypeofTagString
		case abstractdomain.PrimitiveBoolean:
			return TypeofTagBoolean
		default:
			return TypeofTagNone
		}
	case abstractdomain.KindPossiblyUndefined, abstractdomain.KindPossiblyNaN:
		return typeofOfKnown(*k.Inner)
	default:
		return TypeofTagNone
	}
}

func lowerSummary(
	declaration *ast.Node,
	paramSorts []BindingKind,
	paramTypeofs []TypeofTag,
	resolveCallee func(callee *ast.Node) *ast.Node,
) (kernelSummary, bool) {
	body := declaration.Body()
	if body == nil {
		return kernelSummary{}, false
	}
	kernel := EngineKernelHeld()
	if kernel == nil {
		return kernelSummary{}, false
	}
	// parameters: plain identifiers, no defaults, no rest
	var paramNames []string
	for _, parameter := range declaration.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) || pd.Initializer != nil || pd.DotDotDotToken != nil {
			return kernelSummary{}, false
		}
		paramNames = append(paramNames, pd.Name().Text())
	}
	// a concise arrow body IS a single return
	var statements []*ast.Node
	if ast.IsBlock(body) {
		statements = append(statements, body.AsBlock().Statements.Nodes...)
	} else {
		statements = append(statements, syntheticReturnStatement(body))
	}
	var localList CollectLocalsResult
	if ast.IsBlock(body) {
		result, ok := CollectLocals(body)
		if !ok {
			return kernelSummary{}, false
		}
		localList = result
	}
	paramNameSet := map[string]struct{}{}
	for _, name := range paramNames {
		paramNameSet[name] = struct{}{}
	}
	var localNames []string
	declaredOf := map[string]*ast.Node{}
	for _, d := range localList.Locals {
		name := d.AsVariableDeclaration().Name().Text()
		if _, isParam := paramNameSet[name]; isParam {
			continue
		}
		if _, seen := declaredOf[name]; !seen {
			localNames = append(localNames, name)
		}
		declaredOf[name] = d
	}
	// MUTABLE vectors: composition allocates fresh slots past #ret
	bindings := append(append(append([]string{}, paramNames...), localNames...), "#done", "#ret")
	if len(bindings) > slotBudget {
		return kernelSummary{}, false
	}
	sorts := make([]BindingKind, 0, len(bindings))
	sorts = append(sorts, paramSorts...)
	for _, name := range localNames {
		if declared, ok := declaredOf[name]; ok {
			sorts = append(sorts, LocalSort(declared))
		} else {
			sorts = append(sorts, BindingKindUnknown)
		}
	}
	sorts = append(sorts, BindingKindNumber, BindingKindUnknown)
	typeofs := make([]TypeofTag, 0, len(bindings))
	typeofs = append(typeofs, paramTypeofs...)
	for _, name := range localNames {
		if declared, ok := declaredOf[name]; ok {
			typeofs = append(typeofs, LocalTypeof(declared))
		} else {
			typeofs = append(typeofs, TypeofTagNone)
		}
	}
	typeofs = append(typeofs, TypeofTagNumber, TypeofTagNone)
	doneIndex := len(bindings) - 2
	retIndex := len(bindings) - 1
	allocate := func(name string, sort BindingKind, typeofTag TypeofTag) (int, bool) {
		if len(bindings) >= slotBudget {
			return 0, false
		}
		bindings = append(bindings, name)
		sorts = append(sorts, sort)
		typeofs = append(typeofs, typeofTag)
		return len(bindings) - 1, true
	}
	context := &LoweringContext{
		Bindings:      bindings,
		Sorts:         sorts,
		Typeofs:       typeofs,
		Narrow:        kernel.Narrow,
		Result:        &LoweringResult{Done: doneIndex, Ret: retIndex},
		ResolveCallee: resolveCallee,
		Allocate:      allocate,
		Inlining:      map[*ast.Node]struct{}{declaration: {}},
	}
	stmts, ok := LowerStatements(context, statements)
	if !ok {
		return kernelSummary{}, false
	}
	return kernelSummary{
		Stmts:      stmts,
		ParamCount: len(paramNames),
		DoneIndex:  doneIndex,
		RetIndex:   retIndex,
		SlotCount:  len(context.Bindings),
	}, true
}

// syntheticReturnStatement builds the single-statement body a
// concise arrow's expression reads as — the TS source's
// `ts.factory.createReturnStatement(body)`. Built as a bare Node
// with just enough shape for LowerStatements' ast.IsReturnStatement/
// AsReturnStatement().Expression reads (the lowering never asks for
// this synthetic node's position, parent, or any other field).
func syntheticReturnStatement(expression *ast.Node) *ast.Node {
	factory := ast.NewNodeFactory(ast.NodeFactoryHooks{})
	return factory.NewReturnStatement(expression)
}

// SummaryResult is the summary route: lower once, walk per call.
// (result, false) — no claim — wherever the body, the arguments, or
// the kernel decline; the JS inline walk then serves exactly as
// before.
func SummaryResult(
	declaration *ast.Node,
	argKnowns []abstractdomain.AbstractValue,
	resolveCallee func(callee *ast.Node) *ast.Node,
) (abstractdomain.AbstractValue, bool) {
	kernel := EngineKernelHeld()
	if kernel == nil {
		return abstractdomain.AbstractValue{}, false
	}
	// parameter sorts and typeof evidence from the arguments the call
	// actually hands over — both gate which tests the lowering admits,
	// so both key the memo
	parameters := declaration.Parameters()
	paramSorts := make([]BindingKind, len(parameters))
	paramTypeofs := make([]TypeofTag, len(parameters))
	for i := range parameters {
		if i >= len(argKnowns) {
			paramSorts[i] = BindingKindUnknown
			paramTypeofs[i] = TypeofTagNone
			continue
		}
		argument := argKnowns[i]
		if sort, ok := SortFromKnown(argument); ok {
			paramSorts[i] = sort
		} else {
			paramSorts[i] = BindingKindUnknown
		}
		paramTypeofs[i] = typeofOfKnown(argument)
	}
	sortsParts := make([]string, len(paramSorts))
	for i, s := range paramSorts {
		sortsParts[i] = string(s)
	}
	typeofParts := make([]string, len(paramTypeofs))
	for i, t := range paramTypeofs {
		typeofParts[i] = string(t)
	}
	sortsKey := strings.Join(sortsParts, ",") + "|" + strings.Join(typeofParts, ",")

	kernelSummariesMu.Lock()
	byKey := kernelSummaries[declaration]
	if byKey == nil {
		byKey = map[string]summaryEntry{}
		kernelSummaries[declaration] = byKey
	}
	entry, has := byKey[sortsKey]
	kernelSummariesMu.Unlock()
	if !has {
		summary, ok := lowerSummary(declaration, paramSorts, paramTypeofs, resolveCallee)
		entry = summaryEntry{Summary: summary, Ok: ok}
		kernelSummariesMu.Lock()
		byKey[sortsKey] = entry
		kernelSummariesMu.Unlock()
	}
	if !entry.Ok {
		return abstractdomain.AbstractValue{}, false
	}
	summary := entry.Summary
	// entry states: the arguments' own knowledge; every other slot —
	// locals and composition's grown ones — starts absent, and each
	// inlined done flag is assigned {0} in the statements themselves;
	// a state the wire cannot spell declines THIS call, not the summary
	states := make([]kernelbridge.KnownStateWire, 0, summary.SlotCount)
	for i := 0; i < summary.ParamCount; i++ {
		if i >= len(argKnowns) {
			states = append(states, absentState)
			continue
		}
		wire, ok := StateOfKnown(argKnowns[i])
		if !ok {
			return abstractdomain.AbstractValue{}, false
		}
		states = append(states, wire)
	}
	for len(states) < summary.SlotCount {
		states = append(states, absentState)
	}
	states[summary.DoneIndex] = doneDownState
	exits, ok := runKernelWalk(kernel, states, summary.Stmts)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	if summary.RetIndex >= len(exits) || summary.DoneIndex >= len(exits) {
		return abstractdomain.AbstractValue{}, false
	}
	retExit := exits[summary.RetIndex]
	doneExit := exits[summary.DoneIndex]
	// a TOP result determines nothing the inline walk could not say
	// better — decline rather than answer weaker than the fallback
	if retExit.Top {
		return abstractdomain.AbstractValue{}, false
	}
	// the result slot's own absent flag is the ENTRY state surviving
	// the joins — path correlation the encoding routes through the
	// done flag instead: every RETURNED value was written into the
	// set, and only a fall-off path leaves undefined, which is exactly
	// the flag-still-down case decided below
	answer := KnownOfState(kernelbridge.KnownStateWire{Set: retExit.Set, Absent: false, Nan: retExit.Nan})
	if answer.Kind == abstractdomain.KindUnknown {
		return abstractdomain.AbstractValue{}, false
	}
	// a path may fall off the end (the flag can still be down at exit):
	// the return is undefined on it
	allReturned := !doneExit.Top && !doneExit.Absent && !mayContainZero(doneExit.Set)
	if !allReturned {
		answer = abstractdomain.PossiblyUndefined(answer, "", false, false)
	}
	// the claim's grade floors at the arguments' own standing
	floor := abstractdomain.TrustProved
	for i := 0; i < summary.ParamCount; i++ {
		if i < len(argKnowns) {
			floor = abstractdomain.MinTrustLevel(floor, abstractdomain.TrustLevelOf(argKnowns[i]))
		}
	}
	tracing.Count("summaryServed", 0)
	return abstractdomain.AtTrustLevel(answer, floor), true
}

// runKernelWalk asks kernel.Walk, turning a refused question (the
// TS source's try/catch around `kernel.walk(states, summary.stmts)`)
// into an (exits, false) pair.
func runKernelWalk(kernel *kernelbridge.RefinedTSKernel, states []kernelbridge.KnownStateWire, stmts []kernelbridge.IrStatement) (exits []kernelbridge.KnownStateWire, ok bool) {
	defer func() {
		if recover() != nil {
			exits, ok = nil, false
		}
	}()
	return kernel.Walk(states, stmts), true
}

// from control_flow/kernel_delegation.ts
//
// The engine route: where a branch or loop statement lowers to the
// kernel's flow IR, the kernel walks it whole from the pre-statement
// states, and its proved exit claims MEET the walked environment —
// both claims hold, so every position downstream flows through what
// the engine established. The interior walk keeps every hover and
// judgment; the engine tightens what leaves the statement. A decline
// anywhere (a non-scalar participant, an unreadable form, a refused
// question) leaves the existing states untouched.

package walk

import (
	"reflect"
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

/* ── the kernel seam ─────────────────────────────────────────────── */

var (
	engineKernelMu sync.Mutex
	engineKernel   *kernelbridge.RefinedTSKernel
)

// SetEngineKernel is setEngineKernel in the TS source.
func SetEngineKernel(kernel *kernelbridge.RefinedTSKernel) {
	engineKernelMu.Lock()
	engineKernel = kernel
	engineKernelMu.Unlock()
}

// EngineKernelHeld is engineKernelHeld in the TS source: the engine
// kernel a sibling route reads — the summaries walk the same kernel
// this module's statement route walks.
func EngineKernelHeld() *kernelbridge.RefinedTSKernel {
	engineKernelMu.Lock()
	defer engineKernelMu.Unlock()
	return engineKernel
}

// RouteStats is routeStats in the TS source: route tallies, for
// measurement — statements offered to the route, harvests that
// lowered, and meets that changed a state.
type routeStatsType struct {
	Offered int
	Fired   int
	Met     int
}

var (
	routeStatsMu sync.Mutex
	RouteStats   routeStatsType
)

/* ── knowledge states across the boundary ────────────────────────── */

var emptySet = refinementsets.MakeRefinedSet(refinementsets.OneOf(nil))

// StateOfKnown is stateOfKnown in the TS source: the kernel state a
// scalar knowledge state denotes; (zero, false) where the knowledge
// leaves the scalar world (objects, sorts, sequences).
func StateOfKnown(k abstractdomain.AbstractValue) (kernelbridge.KnownStateWire, bool) {
	switch k.Kind {
	case abstractdomain.KindUnknown:
		return kernelbridge.KnownStateWire{Top: true}, true
	case abstractdomain.KindUndef:
		return kernelbridge.KnownStateWire{Set: emptySet, Absent: true, Nan: false}, true
	case abstractdomain.KindNaN:
		return kernelbridge.KnownStateWire{Set: emptySet, Absent: false, Nan: true}, true
	case abstractdomain.KindPossiblyUndefined:
		inner, ok := StateOfKnown(*k.Inner)
		if !ok || inner.Top {
			return kernelbridge.KnownStateWire{}, false
		}
		inner.Absent = true
		return inner, true
	case abstractdomain.KindPossiblyNaN:
		inner, ok := StateOfKnown(*k.Inner)
		if !ok || inner.Top {
			return kernelbridge.KnownStateWire{}, false
		}
		inner.Nan = true
		return inner, true
	case abstractdomain.KindValues, abstractdomain.KindSet:
		// string words are tuples — fully inside the kernel's world;
		// only arrays (nested structure) stay out
		if k.Kind == abstractdomain.KindValues && k.KindTag == abstractdomain.PrimitiveArray {
			return kernelbridge.KnownStateWire{}, false
		}
		if k.Kind == abstractdomain.KindSet && k.SetKindTag != abstractdomain.SetKindTagNone {
			return kernelbridge.KnownStateWire{}, false
		}
		set, ok := abstractdomain.SetOfKnown(k)
		if !ok {
			return kernelbridge.KnownStateWire{}, false
		}
		return kernelbridge.KnownStateWire{Set: set, Absent: false, Nan: false}, true
	default:
		return kernelbridge.KnownStateWire{}, false
	}
}

// KnownOfState is knownOfState in the TS source: a kernel state read
// back as checker knowledge.
func KnownOfState(s kernelbridge.KnownStateWire) abstractdomain.AbstractValue {
	if s.Top {
		return silence.Residue()
	}
	k := abstractdomain.KnownSet(s.Set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	if s.Nan {
		k = abstractdomain.PossiblyNaN(k)
	}
	if s.Absent {
		k = abstractdomain.PossiblyUndefined(k, "", false, false)
	}
	return k
}

/* ── the meet budget ─────────────────────────────────────────────── */

// formNodes is formNodes in the TS source: deep node count of a
// set's syntax — the budget's measure.
func formNodes(form refinementsets.Refinement) int {
	switch form.Form {
	case refinementsets.FormOneOf:
		return 1 + len(form.W)
	case refinementsets.FormConcatenation, refinementsets.FormUnion, refinementsets.FormDifference:
		return 1 + setNodes(*form.A_) + setNodes(*form.B)
	case refinementsets.FormStar:
		return 1 + setNodes(*form.A_)
	case refinementsets.FormRepeat:
		return 1 + setNodes(*form.A_)
	default:
		return 1
	}
}

// setNodes is setNodes in the TS source.
func setNodes(set refinementsets.RefinedSet) int {
	total := 1
	for _, form := range set.Forms {
		total += formNodes(form)
	}
	return total
}

// MeetNodeBudget is MEET_NODE_BUDGET in the TS source: past this, a
// met state stops paying for itself — repeated meets CONCATENATE
// form lists, and sequential branch joins would grow the state
// without bound (measured: the sequential-join axis blew the
// kernel's heap at depth 64 before this cap). The meet is optional
// precision, so past the budget the held state stands.
const MeetNodeBudget = 48

// MeetEngineState is meetEngineState in the TS source: the MEET of
// held knowledge with an engine claim — both hold, so the set parts
// intersect and a flag survives only where both sides keep it. Exact
// values are already maximal; a non-scalar holding stays untouched,
// and an oversized meet keeps the held state.
func MeetEngineState(existing abstractdomain.AbstractValue, engine kernelbridge.KnownStateWire) abstractdomain.AbstractValue {
	if engine.Top {
		return existing
	}
	held, heldOk := StateOfKnown(existing)
	if !heldOk {
		return existing
	}
	if held.Top {
		if setNodes(engine.Set) > MeetNodeBudget {
			return existing
		}
		return KnownOfState(engine)
	}
	if existing.Kind == abstractdomain.KindValues {
		return existing
	}
	if setNodes(held.Set)+setNodes(engine.Set) > MeetNodeBudget {
		return existing
	}
	mergedForms := append(append([]refinementsets.Refinement{}, held.Set.Forms...), engine.Set.Forms...)
	return KnownOfState(kernelbridge.KnownStateWire{
		Set:    refinementsets.MakeRefinedSet(mergedForms...),
		Absent: held.Absent && engine.Absent,
		Nan:    held.Nan && engine.Nan,
	})
}

/* ── the route ───────────────────────────────────────────────────── */

// BindingSlot is one walked binding: its spelled name, and — for an
// object field tracked as `o.k` — the holder and key it reads
// through.
type BindingSlot struct {
	Name      string
	Holder    string
	HasHolder bool
	Key       string
}

// EngineEntry mirrors the TS EngineEntry interface.
type EngineEntry struct {
	Slots  []BindingSlot
	States []kernelbridge.KnownStateWire
	Stmts  []kernelbridge.IrStatement
	Grade  abstractdomain.TrustLevel
}

// heldOf is heldOf in the TS source: the knowledge a slot reads —
// the binding's own, or its holder's key.
func heldOf(env Env, slot BindingSlot) (abstractdomain.AbstractValue, bool) {
	if !slot.HasHolder {
		v, ok := env.Get(slot.Name)
		return v, ok
	}
	holder, ok := env.Get(slot.Holder)
	if !ok || holder.Kind != abstractdomain.KindObject {
		return abstractdomain.AbstractValue{}, false
	}
	for _, key := range holder.Keys {
		if key.Name == slot.Key {
			return key.Value, true
		}
	}
	return abstractdomain.AbstractValue{}, false
}

// sortOfForms is sortOfForms in the TS source: the sort a
// refinement spelling itself pins — ray, integrality and
// divisibility forms speak only of numbers, sequence forms only of
// words. A spelling of bare exact values (`oneOf`) pins neither — a
// one-letter word and a number share it — so it defers.
func sortOfForms(forms []refinementsets.Refinement) BindingKind {
	scalar := false
	sequence := false
	var visit func(fs []refinementsets.Refinement)
	visit = func(fs []refinementsets.Refinement) {
		for _, f := range fs {
			switch f.Form {
			case refinementsets.FormAtLeast, refinementsets.FormAbove,
				refinementsets.FormAtMost, refinementsets.FormBelow,
				refinementsets.FormInteger, refinementsets.FormMultipleOf:
				scalar = true
			case refinementsets.FormEmptyTuple, refinementsets.FormConcatenation,
				refinementsets.FormStar, refinementsets.FormRepeat, refinementsets.FormRepeatWord:
				sequence = true
			case refinementsets.FormUnion, refinementsets.FormDifference:
				visit(f.A_.Forms)
				visit(f.B.Forms)
			default:
				// oneOf pins neither sort
			}
		}
	}
	visit(forms)
	if scalar && !sequence {
		return BindingKindNumber
	}
	if sequence && !scalar {
		return BindingKindString
	}
	return BindingKindUnknown
}

// SortFromKnown is sortFromKnown in the TS source: the sort a slot's
// own knowledge pins, where it pins one; BindingKindUnknown defers
// to the host's type at the occurrence.
func SortFromKnown(k abstractdomain.AbstractValue) (BindingKind, bool) {
	switch k.Kind {
	case abstractdomain.KindValues:
		if k.KindTag == abstractdomain.PrimitiveString {
			return BindingKindString, true
		}
		if k.KindTag == abstractdomain.PrimitiveArray {
			return BindingKindUnknown, false
		}
		return BindingKindNumber, true
	case abstractdomain.KindSet:
		// the spelling itself often names the sort — one less type
		// question at the occurrence
		if k.SetKindTag != abstractdomain.SetKindTagNone {
			return BindingKindUnknown, false
		}
		sort := sortOfForms(k.Set.Forms)
		return sort, sort != BindingKindUnknown
	case abstractdomain.KindPossiblyUndefined, abstractdomain.KindPossiblyNaN:
		return SortFromKnown(*k.Inner)
	default:
		return BindingKindUnknown, false
	}
}

// EngineEntryOf is engineEntryOf in the TS source: harvest a branch
// or loop statement for the engine — the tracked names and
// single-step object fields it references become the walk's
// bindings, their knowledge the entry states, the statement itself
// the lowered body. Every binding carries the sort its occurrences
// wear — the knowledge's own where it pins one, the host's type
// otherwise. (nil, false) — no claim — whenever any participant or
// form declines.
func EngineEntryOf(env Env, statement *ast.Node, sortAt func(node *ast.Node) BindingKind) (*EngineEntry, bool) {
	kernel := EngineKernelHeld()
	if kernel == nil {
		return nil, false
	}
	if !ast.IsIfStatement(statement) && !ast.IsWhileStatement(statement) && !ast.IsForStatement(statement) {
		return nil, false
	}
	routeStatsMu.Lock()
	RouteStats.Offered++
	routeStatsMu.Unlock()
	var slots []BindingSlot
	occurrence := map[string]*ast.Node{}
	seen := map[string]struct{}{}
	var visit func(node *ast.Node)
	visit = func(node *ast.Node) {
		if ast.IsPropertyAccessExpression(node) {
			access := node.AsPropertyAccessExpression()
			if ast.IsIdentifier(access.Expression) && ast.IsIdentifier(access.Name()) {
				holderValue, holderOk := env.Get(access.Expression.Text())
				if holderOk && holderValue.Kind == abstractdomain.KindObject {
					name := access.Expression.Text() + "." + access.Name().Text()
					if _, already := seen[name]; !already {
						seen[name] = struct{}{}
						slots = append(slots, BindingSlot{Name: name, Holder: access.Expression.Text(), HasHolder: true, Key: access.Name().Text()})
						occurrence[name] = node
					}
					return // the holder itself is not a walk binding here
				}
			}
		}
		if ast.IsIdentifier(node) {
			if _, tracked := env.Get(node.Text()); tracked {
				if _, already := seen[node.Text()]; !already {
					seen[node.Text()] = struct{}{}
					slots = append(slots, BindingSlot{Name: node.Text()})
					occurrence[node.Text()] = node
				}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			visit(child)
			return false
		})
	}
	visit(statement)
	if len(slots) == 0 {
		return nil, false
	}
	var states []kernelbridge.KnownStateWire
	var sorts []BindingKind
	grade := abstractdomain.TrustProved
	for _, slot := range slots {
		held, ok := heldOf(env, slot)
		if !ok {
			return nil, false
		}
		state, ok := StateOfKnown(held)
		if !ok {
			return nil, false
		}
		// an accreted entry state past the budget declines the route —
		// its questions would only be refused or worse
		if !state.Top && setNodes(state.Set) > MeetNodeBudget {
			return nil, false
		}
		grade = abstractdomain.MinTrustLevel(grade, abstractdomain.TrustLevelOf(held))
		states = append(states, state)
		if sort, ok := SortFromKnown(held); ok {
			sorts = append(sorts, sort)
		} else {
			sorts = append(sorts, sortAt(occurrence[slot.Name]))
		}
	}
	bindings := make([]string, len(slots))
	for i, slot := range slots {
		bindings[i] = slot.Name
	}
	stmts, ok := func() (stmts []kernelbridge.IrStatement, ok bool) {
		defer func() {
			if recover() != nil {
				stmts, ok = nil, false // a refused narrowing question: no claim
			}
		}()
		return LowerStatements(&LoweringContext{
			Bindings: bindings,
			Sorts:    sorts,
			Narrow:   kernel.Narrow,
		}, []*ast.Node{statement})
	}()
	if !ok || stmts == nil {
		return nil, false
	}
	routeStatsMu.Lock()
	RouteStats.Fired++
	routeStatsMu.Unlock()
	return &EngineEntry{Slots: slots, States: states, Stmts: stmts, Grade: grade}, true
}

// writtenTargets is writtenTargets in the TS source: the binding
// indices a lowered statement list WRITES. Only these are worth
// meeting: an unwritten binding's branch exit is the join of
// complementary narrowings — a superset of what is held, so a meet
// can never tighten it, only restate it (measured: meeting
// tested-only bindings bloated the held states until the checker's
// own joins embedded them exponentially).
func writtenTargets(stmts []kernelbridge.IrStatement, into map[int]struct{}) {
	for _, s := range stmts {
		switch s.Kind {
		case kernelbridge.IrStatementAssign:
			into[s.Target] = struct{}{}
		case kernelbridge.IrStatementBranch:
			writtenTargets(s.Then, into)
			writtenTargets(s.Else, into)
		case kernelbridge.IrStatementLoop:
			for i, wrote := range s.Written {
				if wrote {
					into[i] = struct{}{}
				}
			}
		}
	}
}

// The TS source's `met !== held` is an object-identity comparison —
// meetEngineState returns the SAME object back when nothing changed
// (the `return existing` early-outs). Go's AbstractValue holds
// slices, so it has no `==`; reflect.DeepEqual is the value-based
// stand-in here. This only feeds RouteStats.Met, a measurement
// counter — never a correctness branch — so a value comparison
// (equal contents count as "unchanged", same as identity would once
// the object is freshly rebuilt from equal parts) is a sound
// substitute.

// EngineMeetInto is engineMeetInto in the TS source: walk the
// harvested statement kernel-side and meet the exit claims into the
// environment — written bindings only. Silence on any failure — the
// existing states already stand on their own.
func EngineMeetInto(env Env, entry *EngineEntry) {
	kernel := EngineKernelHeld()
	if kernel == nil {
		return
	}
	written := map[int]struct{}{}
	writtenTargets(entry.Stmts, written)
	if len(written) == 0 {
		return
	}
	exit, ok := func() (exit []kernelbridge.KnownStateWire, ok bool) {
		defer func() {
			if recover() != nil {
				exit, ok = nil, false
			}
		}()
		return kernel.Walk(entry.States, entry.Stmts), true
	}()
	if !ok || len(exit) != len(entry.Slots) {
		return
	}
	for i, slot := range entry.Slots {
		if _, isWritten := written[i]; !isWritten {
			continue
		}
		if !slot.HasHolder {
			held, ok := env.Get(slot.Name)
			if !ok {
				continue
			}
			met := abstractdomain.AtTrustLevel(MeetEngineState(held, exit[i]), entry.Grade)
			if !reflect.DeepEqual(met, held) {
				routeStatsMu.Lock()
				RouteStats.Met++
				routeStatsMu.Unlock()
			}
			env.Set(slot.Name, met)
			continue
		}
		// an object field meets back through its holder; the rebuilt
		// object drops its variants — a tightened joint key may
		// contradict an arm, and variants only ever ADD precision
		holder, ok := env.Get(slot.Holder)
		if !ok || holder.Kind != abstractdomain.KindObject {
			continue
		}
		var held abstractdomain.AbstractValue
		heldFound := false
		for _, key := range holder.Keys {
			if key.Name == slot.Key {
				held, heldFound = key.Value, true
				break
			}
		}
		if !heldFound {
			continue
		}
		met := abstractdomain.AtTrustLevel(MeetEngineState(held, exit[i]), entry.Grade)
		if reflect.DeepEqual(met, held) {
			continue
		}
		rebuilt := holder
		rebuilt.Variants = nil
		newKeys := make([]abstractdomain.ObjectKey, 0, len(holder.Keys)+1)
		replaced := false
		for _, key := range holder.Keys {
			if key.Name == slot.Key {
				newKeys = append(newKeys, abstractdomain.ObjectKey{Name: key.Name, Value: met})
				replaced = true
			} else {
				newKeys = append(newKeys, key)
			}
		}
		if !replaced {
			newKeys = append(newKeys, abstractdomain.ObjectKey{Name: slot.Key, Value: met})
		}
		rebuilt.Keys = newKeys
		env.Set(slot.Holder, rebuilt)
	}
}

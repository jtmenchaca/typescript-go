// A whole function body lowered for the SUMMARY compiler.
//
// The whole-body route (kernel_summaries.go) lowers per call, keyed by
// the sorts the arguments supplied — a different sort vector is a
// different lowering. A SUMMARY quantifies over all entries instead, so
// it is compiled once per declaration and the sorts cannot come from
// any call: they are read from the declaration's own parameter type
// annotations, which every entry shares.
//
// What this adds beyond the per-call lowering is the callee TABLE: a
// body whose call sites lower to IrStatementCall carries one blob per
// callee, and the table rides out beside the statements so the compile
// can splice each callee's already-built summary.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// summarySlotBudget is the summary route's own slot ceiling — the same
// figure the whole-body route uses, kept here so the two routes admit
// the same bodies.
const summarySlotBudget = 32

// (Parameter sorts and typeof evidence read through kernel_summaries
// .go's declaredParamSort / declaredParamTypeof: an unannotated or
// richer-typed parameter is UNKNOWN — a summary quantifies over all
// entries, so a sort nothing promises may not be assumed.)

// bodySlot is one slot the body's locals contribute: its spelled name,
// the sort its occurrences wear, and what typeof answers for it.
type bodySlot struct {
	Name      string
	Sort      BindingKind
	TypeofTag TypeofTag
}

// collectSummaryLocals is CollectLocals widened by exactly one shape:
// an OBJECT BINDING PATTERN (`const { x, y } = p`) contributes one
// ordinary scalar local per bound name rather than declining the body.
// That is what the destructuring lowering needs — each bound name gets
// its own slot, written from the record leaf it reads.
//
// Everything else is CollectLocals unchanged: single-identifier
// declarations in source order, recursing into branch arms and blocks,
// never into nested functions (a nested function anywhere declines —
// its captures read and write outside the lowered world). An ARRAY
// binding pattern still declines: its elements read positions the
// element slot does not distinguish.
//
// (CollectLocals itself lives in tracked_bindings.go, which the whole-
// body route shares; widening it there would change that route's
// admitted set too. This variant belongs to the summary route alone.)
func collectSummaryLocals(body *ast.Node) (locals []*ast.Node, patterns []*ast.Node, ok bool) {
	declined := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if declined {
			return false
		}
		if ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) ||
			ast.IsArrowFunction(node) || ast.IsClassDeclaration(node) ||
			ast.IsClassExpression(node) {
			declined = true
			return false
		}
		if ast.IsVariableDeclaration(node) {
			name := node.Name()
			if ast.IsObjectBindingPattern(name) {
				for _, element := range name.AsBindingPattern().Elements.Nodes {
					binding := element.AsBindingElement()
					if binding.DotDotDotToken != nil || !ast.IsIdentifier(binding.Name()) {
						declined = true
						return false
					}
				}
				patterns = append(patterns, node)
				node.ForEachChild(visit)
				return false
			}
			if !ast.IsIdentifier(name) {
				declined = true
				return false
			}
			locals = append(locals, node)
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	if declined {
		return nil, nil, false
	}
	return locals, patterns, true
}

// destructuredSlotsOf lays out the slots a destructuring declaration
// needs: one per bound name, wearing the SORT of the record leaf it
// reads. A pattern whose source is not a flattened record, or that names
// a leaf the record does not have, contributes unknown-sorted slots —
// the read then finds no leaf slot and the lowering declines, which is
// the total-or-decline law doing its work.
func destructuredSlotsOf(pattern *ast.Node, records map[*ast.Node]ObjectLocal) []bodySlot {
	decl := pattern.AsVariableDeclaration()
	// the leaves of the record this pattern reads, by their one-step key
	leafOfKey := map[string]ObjectLocalKey{}
	if decl.Initializer != nil {
		initializer := Unwrapped(decl.Initializer)
		if ast.IsIdentifier(initializer) {
			source := initializer.Text()
			for _, record := range records {
				if record.Name != source {
					continue
				}
				for _, key := range record.Keys {
					if len(key.Path) == 1 {
						leafOfKey[key.Path[0]] = key
					}
				}
			}
		}
	}
	var out []bodySlot
	for _, element := range decl.Name().AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		read := binding.Name().Text()
		if binding.PropertyName != nil && ast.IsIdentifier(binding.PropertyName) {
			read = binding.PropertyName.Text()
		}
		slot := bodySlot{Name: binding.Name().Text(), Sort: BindingKindUnknown, TypeofTag: TypeofTagNone}
		if leaf, found := leafOfKey[read]; found {
			slot.Sort = ObjectLocalKeySort(leaf)
			slot.TypeofTag = ObjectLocalKeyTypeof(leaf)
		}
		out = append(out, slot)
	}
	return out
}

// localSlotsOf lays out a body's locals as slots: a scalar local takes
// one, a flattened record one PER LEAF ("p.a.b"), and a flattened array
// TWO ("a.len", "a.elem"). A local the recognizers declined keeps its
// single whole-name slot, whose key or index reads then find no slot
// and decline the lowering — the existing behaviour.
func localSlotsOf(
	body *ast.Node,
	locals []*ast.Node,
	patterns []*ast.Node,
	parameterNames map[string]struct{},
) []bodySlot {
	objectLocals := ObjectLocalsOf(body, locals)
	// the collections flatten first: the array recognizer needs them to
	// admit the bridge (`[...m.values()]` reads the map's slots)
	collectionLocals := MapLocalsOf(body, locals)
	arrayLocals := ArrayLocalsOf(body, locals, collectionLocals)
	var order []string
	declaredOf := map[string]*ast.Node{}
	for _, declaration := range locals {
		name := declaration.AsVariableDeclaration().Name().Text()
		if _, isParameter := parameterNames[name]; isParameter {
			continue
		}
		if previous, seen := declaredOf[name]; seen {
			// a name declared TWICE keeps the last declaration's reading; a
			// pair that disagrees about SHAPE has no one slot family, so
			// both lose their flattening and the name stays whole
			_, wasRecord := objectLocals[previous]
			_, isRecord := objectLocals[declaration]
			_, wasArray := arrayLocals[previous]
			_, isArray := arrayLocals[declaration]
			_, wasCollection := collectionLocals[previous]
			_, isCollection := collectionLocals[declaration]
			if wasRecord != isRecord || wasArray != isArray ||
				wasCollection != isCollection {
				delete(objectLocals, previous)
				delete(objectLocals, declaration)
				delete(arrayLocals, previous)
				delete(arrayLocals, declaration)
				delete(collectionLocals, previous)
				delete(collectionLocals, declaration)
			}
		} else {
			order = append(order, name)
		}
		declaredOf[name] = declaration
	}
	var out []bodySlot
	for _, name := range order {
		declared, has := declaredOf[name]
		if !has {
			out = append(out, bodySlot{Name: name, Sort: BindingKindUnknown, TypeofTag: TypeofTagNone})
			continue
		}
		if local, flattened := objectLocals[declared]; flattened {
			for _, key := range local.Keys {
				out = append(out, bodySlot{
					Name:      key.SlotName,
					Sort:      ObjectLocalKeySort(key),
					TypeofTag: ObjectLocalKeyTypeof(key),
				})
			}
			continue
		}
		if local, flattened := arrayLocals[declared]; flattened {
			out = append(out,
				bodySlot{Name: local.LenSlotName, Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
				bodySlot{Name: local.ElemSlotName, Sort: ArrayElementSort(local), TypeofTag: ArrayElementTypeof(local)},
			)
			continue
		}
		if local, flattened := collectionLocals[declared]; flattened {
			for _, slot := range MapLocalSlots(local) {
				out = append(out, bodySlot{
					Name:      slot.Name,
					Sort:      slot.Sort,
					TypeofTag: slot.TypeofTag,
				})
			}
			continue
		}
		out = append(out, bodySlot{
			Name:      name,
			Sort:      LocalSort(declared),
			TypeofTag: LocalTypeof(declared),
		})
	}
	// the destructured names last, each wearing its leaf's sort. A name
	// some other local already claimed keeps that local's slot — one name,
	// one slot.
	held := map[string]struct{}{}
	for _, slot := range out {
		held[slot.Name] = struct{}{}
	}
	for _, pattern := range patterns {
		for _, slot := range destructuredSlotsOf(pattern, objectLocals) {
			if _, already := held[slot.Name]; already {
				continue
			}
			if _, isParameter := parameterNames[slot.Name]; isParameter {
				continue
			}
			held[slot.Name] = struct{}{}
			out = append(out, slot)
		}
	}
	return out
}

// A for-of element binding arrives with the other locals (CollectLocals
// walks into the initializer), and neither flattening recognizer admits
// it: it has no initializer, so it is neither an object literal nor an
// array literal. It therefore takes an ordinary whole-name slot, which
// is exactly what ArrayForOfLowering writes the per-pass element into.

// capturedSlot is one READ-ONLY capture an arrow argument closes over:
// the name it is spelled under in the enclosing body, and the sort and
// typeof evidence its caller slot wears. Each one becomes an EXTRA
// entry of the arrow's summary, laid out immediately after the declared
// parameters — so entry k for k < len(parameters) is the k-th
// parameter, and entry len(parameters)+j is the j-th capture, in the
// order the free-variable scan reported them (source order of first
// read). The call site binds each to a `var` of the caller slot the
// name resolves to, which is why the layout has to be the scan's own
// deterministic order and not a map's.
type capturedSlot struct {
	Name      string
	Sort      BindingKind
	TypeofTag TypeofTag
}

// lowerSummaryBody lowers a declaration's whole body for the summary
// compiler: the parameter slots first (the compiler's arity), then the
// locals' slots, then the done flag and the result slot, with the
// callee table the body's call statements built. LowerSummaryBody
// (kernel_summaries.go) is the memoized door in front of it.
//
// Total-or-decline, exactly as every other lowering here: a body that
// leaves the grammar answers false and the caller keeps its existing
// route.
func lowerSummaryBody(ctx *FlowContext, declaration *ast.Node) (LoweredSummary, bool) {
	return lowerSummaryBodyWithCaptures(ctx, declaration, nil, nil)
}

// lowerArrowSummary lowers an ARROW (or function expression) ARGUMENT
// closure-converted: its declared parameters first, then one entry per
// READ-ONLY capture in the scan's order, then the locals, the done flag
// and the result slot. Everything past the extra entries is the
// ordinary body lowering — a capture is just another entry as far as
// the compiled summary is concerned, which is exactly why closure
// conversion needs nothing new kernel-side.
//
// The declared parameters wear the SITE's sorts rather than the
// declaration's annotations. A top-level declaration's summary must read
// sorts from its own annotations, since it quantifies over callers a
// lowering cannot see; an arrow argument has exactly ONE call site, and
// that site fills entry 0 with a `var` of the receiver's element slot
// whose sort the caller's layout already carries. Reading the sort off
// the annotation instead would make every unannotated `x => x + 1`
// unknown-sorted and decline its own arithmetic — the parameter would be
// the one entry whose sort the site knows and the summary refuses. The
// captures already ride their caller slots' sorts for the same reason;
// this puts the parameters on the same footing.
//
// The async gate is NOT consulted here beyond what summaryLowerable
// says: a lowered async body's #ret holds the SETTLED inner value (the
// ret-as-inner convention), so an async arrow converts exactly like a
// sync one and the awaiting site adds nothing.
func lowerArrowSummary(
	ctx *FlowContext,
	arrow *ast.Node,
	parameters []parameterSlotSort,
	captures []capturedSlot,
) (LoweredSummary, bool) {
	return lowerSummaryBodyWithCaptures(ctx, arrow, parameters, captures)
}

// parameterSlotSort is the sort and typeof evidence ONE declared
// parameter entry wears when the call site knows them. A nil entry list
// (the declaration route) leaves every parameter reading its own
// annotation.
type parameterSlotSort struct {
	Sort      BindingKind
	TypeofTag TypeofTag
}

// lowerSummaryBodyWithCaptures is the one lowering both doors share:
// nil parameters and nil captures is the plain declaration route, a
// supplied pair is the closure-converted arrow route.
func lowerSummaryBodyWithCaptures(
	ctx *FlowContext,
	declaration *ast.Node,
	parameterSorts []parameterSlotSort,
	captures []capturedSlot,
) (LoweredSummary, bool) {
	if !summaryLowerable(declaration) {
		return LoweredSummary{}, false
	}
	body := declaration.Body()
	kernel := EngineKernelHeld()
	if kernel == nil {
		return LoweredSummary{}, false
	}
	// parameters: plain identifiers, no defaults, no rest
	parameters := declaration.Parameters()
	paramNames := make([]string, 0, len(parameters))
	paramSorts := make([]BindingKind, 0, len(parameters))
	paramTypeofs := make([]TypeofTag, 0, len(parameters))
	for index, parameter := range parameters {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) || pd.Initializer != nil || pd.DotDotDotToken != nil {
			return LoweredSummary{}, false
		}
		paramNames = append(paramNames, pd.Name().Text())
		// the site's sort where the arrow route supplied one, the
		// declaration's own annotation otherwise. A supplied sort is what
		// the entry the site fills already wears, so the summary quantifies
		// over exactly the values that entry can take.
		if index < len(parameterSorts) {
			paramSorts = append(paramSorts, parameterSorts[index].Sort)
			paramTypeofs = append(paramTypeofs, parameterSorts[index].TypeofTag)
			continue
		}
		paramSorts = append(paramSorts, declaredParamSort(parameter))
		paramTypeofs = append(paramTypeofs, declaredParamTypeof(parameter))
	}
	// the captures ride as EXTRA entries immediately after the declared
	// parameters, in the scan's own order — the call site fills entry
	// len(parameters)+j with a var of the caller slot capture j resolved
	// to, so the two orders must agree exactly. A capture whose name a
	// parameter already claims is the parameter's, not the capture's:
	// the inner binding shadows, and the scan never reported it free.
	declaredCount := len(paramNames)
	for _, capture := range captures {
		shadowed := false
		for _, name := range paramNames[:declaredCount] {
			if name == capture.Name {
				shadowed = true
				break
			}
		}
		if shadowed {
			return LoweredSummary{}, false
		}
		paramNames = append(paramNames, capture.Name)
		paramSorts = append(paramSorts, capture.Sort)
		paramTypeofs = append(paramTypeofs, capture.TypeofTag)
	}
	// a concise arrow body IS a single return
	var statements []*ast.Node
	if ast.IsBlock(body) {
		statements = append(statements, body.AsBlock().Statements.Nodes...)
	} else {
		statements = append(statements, syntheticReturnStatement(body))
	}
	var locals []*ast.Node
	var patterns []*ast.Node
	if ast.IsBlock(body) {
		collected, collectedPatterns, ok := collectSummaryLocals(body)
		if !ok {
			return LoweredSummary{}, false
		}
		locals, patterns = collected, collectedPatterns
	}
	parameterNames := map[string]struct{}{}
	for _, name := range paramNames {
		parameterNames[name] = struct{}{}
	}
	slots := localSlotsOf(body, locals, patterns, parameterNames)
	// MUTABLE vectors: composition allocates fresh slots past #ret
	bindings := append([]string{}, paramNames...)
	sorts := append([]BindingKind{}, paramSorts...)
	typeofs := append([]TypeofTag{}, paramTypeofs...)
	for _, slot := range slots {
		bindings = append(bindings, slot.Name)
		sorts = append(sorts, slot.Sort)
		typeofs = append(typeofs, slot.TypeofTag)
	}
	bindings = append(bindings, "#done", "#ret")
	sorts = append(sorts, BindingKindNumber, BindingKindUnknown)
	typeofs = append(typeofs, TypeofTagNumber, TypeofTagNone)
	if len(bindings) > summarySlotBudget {
		return LoweredSummary{}, false
	}
	doneIndex := len(bindings) - 2
	retIndex := len(bindings) - 1
	table := &SummaryTableBuilder{}
	context := &LoweringContext{
		Bindings: bindings,
		Sorts:    sorts,
		Typeofs:  typeofs,
		Narrow:   kernel.Narrow,
		Result:   &LoweringResult{Done: doneIndex, Ret: retIndex},
		ResolveCallee: func(callee *ast.Node) *ast.Node {
			called := ContractOf(ctx, callee)
			if called == nil || !summaryLowerable(called.Declaration) {
				return nil
			}
			return called.Declaration
		},
		Inlining:     map[*ast.Node]struct{}{declaration: {}},
		Flow:         ctx,
		SummaryTable: table,
	}
	// allocate grows the CONTEXT's own vectors, not copies of them: a slot
	// handed out past the initial layout must be readable through
	// context.Sorts at the index it was given, and a Go slice header
	// copied before the growth would not carry it.
	context.Allocate = func(name string, sort BindingKind, typeofTag TypeofTag) (int, bool) {
		if len(context.Bindings) >= summarySlotBudget {
			return 0, false
		}
		context.Bindings = append(context.Bindings, name)
		context.Sorts = append(context.Sorts, sort)
		context.Typeofs = append(context.Typeofs, typeofTag)
		return len(context.Bindings) - 1, true
	}
	stmts, ok := LowerStatements(context, statements)
	if !ok {
		return LoweredSummary{}, false
	}
	// ParamCount counts the declared parameters AND the capture entries:
	// both are entries the caller fills, and the apply route's "everything
	// past ParamCount enters absent" rule has to leave the captures alone.
	return LoweredSummary{
		Stmts:      stmts,
		ParamCount: len(paramNames),
		DoneIndex:  doneIndex,
		RetIndex:   retIndex,
		SlotCount:  len(context.Bindings),
		Table:      table.Blobs,
	}, true
}

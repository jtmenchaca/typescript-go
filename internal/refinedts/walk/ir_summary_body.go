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

/* ── record parameters ───────────────────────────────────────────── */

// recordParamMember is one member of a parameter's TYPE LITERAL
// annotation: the key it is spelled under, the slot name the body reads
// it by ("p.lo"), and the sort and typeof evidence its OWN annotation
// states.
type recordParamMember struct {
	Key       string
	SlotName  string
	Sort      BindingKind
	TypeofTag TypeofTag
}

// recordParamMembersOf reads a parameter's annotation as a SYNTACTIC
// TYPE LITERAL of scalar members — `p: { lo: number, hi: string }` — and
// answers one member per property signature, spelled "p.lo"/"p.hi".
//
// Nothing but that shape is admitted: a class name, an interface name,
// a type alias, a union, an intersection, an optional member (`lo?:`),
// a method signature, an index signature, a call signature, a nested
// literal, or a member whose own annotation is not number/boolean/string
// answers false and the parameter keeps its single whole-name slot —
// exactly today's behaviour. The rule is deliberately syntactic: a
// declaration's summary quantifies over every caller, and only what the
// annotation itself spells is promised to every entry.
//
// Each member's sort and typeof read through declaredParamSort's own
// reading, member-wise: number and boolean ride the number sort (their
// typeof differs), string rides the string sort.
func recordParamMembersOf(parameter *ast.Node) ([]recordParamMember, bool) {
	pd := parameter.AsParameterDeclaration()
	if pd.Type == nil || !ast.IsIdentifier(pd.Name()) {
		return nil, false
	}
	if !ast.IsTypeLiteralNode(pd.Type) {
		return nil, false
	}
	holder := pd.Name().Text()
	members := pd.Type.AsTypeLiteralNode().Members.Nodes
	if len(members) == 0 {
		return nil, false
	}
	seen := map[string]struct{}{}
	out := make([]recordParamMember, 0, len(members))
	for _, member := range members {
		if !ast.IsPropertySignatureDeclaration(member) {
			return nil, false
		}
		signature := member.AsPropertySignatureDeclaration()
		// `lo?: number` admits absence, which a scalar entry slot cannot
		// carry apart from its value; the whole parameter declines
		if signature.PostfixToken != nil || signature.Initializer != nil {
			return nil, false
		}
		if signature.Type == nil || !ast.IsIdentifier(signature.Name()) {
			return nil, false
		}
		var sort BindingKind
		var tag TypeofTag
		switch signature.Type.Kind {
		case ast.KindNumberKeyword:
			sort, tag = BindingKindNumber, TypeofTagNumber
		case ast.KindBooleanKeyword:
			// booleans ride the number sort — declaredParamSort's own rule
			sort, tag = BindingKindNumber, TypeofTagBoolean
		case ast.KindStringKeyword:
			sort, tag = BindingKindString, TypeofTagString
		default:
			return nil, false
		}
		key := signature.Name().Text()
		if _, already := seen[key]; already {
			return nil, false
		}
		seen[key] = struct{}{}
		out = append(out, recordParamMember{
			Key:       key,
			SlotName:  holder + "." + key,
			Sort:      sort,
			TypeofTag: tag,
		})
	}
	return out, true
}

// SummaryParameterEntries is the ONE expansion both the layout and the
// call sites read: the entry slots one declared parameter contributes.
//
// A scalar (or richer-typed, or unannotated) parameter contributes
// exactly ONE entry under its own spelled name, wearing declaredParamSort
// / declaredParamTypeof — today's layout, unchanged. A parameter whose
// annotation is a type literal of scalar members contributes ONE ENTRY
// PER MEMBER, spelled "p.lo"/"p.hi", each sorted by its own member
// annotation.
//
// (false) where the parameter itself declines outright: a binding
// pattern, a default, or a rest — the same three the lowering has always
// refused, kept here so the two seams cannot disagree about how many
// entries a parameter is worth.
//
// Both the body layout (lowerSummaryBodyWithCaptures) and the call-site
// argument vector (summaryCallStatement) build their entry lists by
// walking the declared parameters through THIS function, so the callee's
// arity, the caller's argument order, and the apply side's entry states
// are three readings of one answer.
func SummaryParameterEntries(parameter *ast.Node) ([]bodySlot, bool) {
	pd := parameter.AsParameterDeclaration()
	if !ast.IsIdentifier(pd.Name()) || pd.Initializer != nil || pd.DotDotDotToken != nil {
		return nil, false
	}
	if members, isRecord := recordParamMembersOf(parameter); isRecord {
		out := make([]bodySlot, 0, len(members))
		for _, member := range members {
			out = append(out, bodySlot{
				Name:      member.SlotName,
				Sort:      member.Sort,
				TypeofTag: member.TypeofTag,
			})
		}
		return out, true
	}
	return []bodySlot{{
		Name:      pd.Name().Text(),
		Sort:      declaredParamSort(parameter),
		TypeofTag: declaredParamTypeof(parameter),
	}}, true
}

// recordParameterUsesAreDeclaredReads scans a body for every occurrence
// of an EXPANDED parameter's name and answers whether each one is a
// READ of a declared member — `p.lo` in value position.
//
// Everything else declines the body:
//
//   - a whole-name use (`f(p)`, `return p`, `q = p`, `p[e]`, `p?.lo`) —
//     after the expansion there is no one value for it to denote;
//   - a member the annotation never declared (`p.mid`) — no slot holds
//     it, and reading it would silently answer another slot's state;
//   - a WRITE to a member (`p.lo = 1`, `p.lo += 1`, `p.lo++`, `delete
//     p.lo`) — the caller's own object would move, and a summary carries
//     no effect back out through its entries;
//   - a deep path (`p.lo.x`) — the members are scalars, so no such leaf
//     exists.
func recordParameterUsesAreDeclaredReads(body *ast.Node, name string, members []recordParamMember) bool {
	declared := map[string]struct{}{}
	for _, member := range members {
		declared[member.Key] = struct{}{}
	}
	// a node that WRITES through this parameter's spelling
	writesThroughParameter := func(node *ast.Node) bool {
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment && bin.OperatorToken.Kind <= ast.KindLastAssignment {
				if root, _, ok := propertyPathOf(Unwrapped(bin.Left)); ok && root == name {
					return true
				}
			}
		}
		if ast.IsPrefixUnaryExpression(node) {
			unary := node.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if root, _, ok := propertyPathOf(Unwrapped(unary.Operand)); ok && root == name {
					return true
				}
			}
		}
		if ast.IsPostfixUnaryExpression(node) {
			unary := node.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				if root, _, ok := propertyPathOf(Unwrapped(unary.Operand)); ok && root == name {
					return true
				}
			}
		}
		if ast.IsDeleteExpression(node) {
			if root, _, ok := propertyPathOf(Unwrapped(node.AsDeleteExpression().Expression)); ok && root == name {
				return true
			}
		}
		return false
	}
	ok := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		if writesThroughParameter(node) {
			ok = false
			return true
		}
		// a declared member READ consumes the root and the step name, so
		// neither reaches the bare-name test below
		if root, path, isPath := propertyPathOf(Unwrapped(node)); isPath && root == name {
			if len(path) != 1 {
				ok = false
				return true
			}
			if _, isDeclared := declared[path[0]]; !isDeclared {
				ok = false
				return true
			}
			return false
		}
		// every other occurrence of the bare name is the WHOLE record in a
		// position the expansion cannot spell
		if ast.IsIdentifier(node) && node.Text() == name {
			ok = false
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return ok
}

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
	// parameters: plain identifiers, no defaults, no rest. A type-literal
	// parameter EXPANDS to one entry per member (SummaryParameterEntries)
	// — the entry vector is no longer one-to-one with the declared
	// parameters, so the arrow route's per-parameter sorts are indexed by
	// DECLARATION position while the entry vector runs ahead of it.
	parameters := declaration.Parameters()
	paramNames := make([]string, 0, len(parameters))
	paramSorts := make([]BindingKind, 0, len(parameters))
	paramTypeofs := make([]TypeofTag, 0, len(parameters))
	for index, parameter := range parameters {
		entries, entriesOk := SummaryParameterEntries(parameter)
		if !entriesOk {
			return LoweredSummary{}, false
		}
		if members, expanded := recordParamMembersOf(parameter); expanded {
			// an EXPANDED parameter's every use in the body must be a read of
			// a declared member; a whole-p use, an undeclared member, or a
			// write through it declines the body outright
			if !recordParameterUsesAreDeclaredReads(body, parameter.AsParameterDeclaration().Name().Text(), members) {
				return LoweredSummary{}, false
			}
			// the arrow route fills ONE entry per declared parameter with a
			// site sort, which an expanded parameter has no single entry for
			if index < len(parameterSorts) {
				return LoweredSummary{}, false
			}
			for _, entry := range entries {
				paramNames = append(paramNames, entry.Name)
				paramSorts = append(paramSorts, entry.Sort)
				paramTypeofs = append(paramTypeofs, entry.TypeofTag)
			}
			continue
		}
		paramNames = append(paramNames, entries[0].Name)
		// the site's sort where the arrow route supplied one, the
		// declaration's own annotation otherwise. A supplied sort is what
		// the entry the site fills already wears, so the summary quantifies
		// over exactly the values that entry can take.
		if index < len(parameterSorts) {
			paramSorts = append(paramSorts, parameterSorts[index].Sort)
			paramTypeofs = append(paramTypeofs, parameterSorts[index].TypeofTag)
			continue
		}
		paramSorts = append(paramSorts, entries[0].Sort)
		paramTypeofs = append(paramTypeofs, entries[0].TypeofTag)
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
	// an EXPANDED parameter's entries are spelled "p.lo", so the HOLDER
	// name is not among them; a local named `p` would then take its own
	// slot beside the leaves. (Such a body already declined above — the
	// declaration's own `p` is a whole-name occurrence the use scan
	// refuses — so this only keeps the two readings agreeing.)
	for _, parameter := range parameters {
		if _, expanded := recordParamMembersOf(parameter); expanded {
			parameterNames[parameter.AsParameterDeclaration().Name().Text()] = struct{}{}
		}
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
	// ParamCount counts the ENTRIES the caller fills, not the declared
	// parameters: an expanded type-literal parameter contributes one entry
	// per member, and the captures contribute one each. The apply route's
	// "everything past ParamCount enters absent" rule reads this number, so
	// it has to be the entry count or a record parameter's later leaves
	// would enter absent.
	return LoweredSummary{
		Stmts:      stmts,
		ParamCount: len(paramNames),
		DoneIndex:  doneIndex,
		RetIndex:   retIndex,
		SlotCount:  len(context.Bindings),
		Table:      table.Blobs,
	}, true
}

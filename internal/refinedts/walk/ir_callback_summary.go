// Arrow callbacks, closure-converted: a collection method whose
// callback is an arrow argument lowers as ONE call statement applying
// the arrow's own compiled summary.
//
// The census that motivated this: collection callbacks capture `this`
// and outer locals READ-ONLY. Both convert without any new kernel form,
// because a capture is nothing but an extra ENTRY of the arrow's
// summary. An arrow with parameters p₀…pₙ and captures c₀…cₘ compiles
// to a summary of arity n+m+1+locals, whose entries are the parameters
// first and the captures immediately after; the call site fills the
// parameter entries from the receiver's element slot and the capture
// entries with a `var` of the caller slot each capture name resolves
// to. Nothing kernel-side learns that some entries came from a closure.
//
// What CONVERTS — an arrow (or function expression) argument whose free
// names are only:
//
//   - its own parameters;
//   - `this`-rooted method calls (`this.m(…)`) that resolve through the
//     lowering context's ResolveCallee exactly as any other call does —
//     the receiver is not a value the arrow reads, only a name the
//     resolution consumes;
//   - READ-ONLY captures of caller locals that HAVE a slot.
//
// What DECLINES:
//
//   - a captured WRITE (`total += x`, `seen[k] = 1`, `i++` on an outer
//     name) — the write would move the caller's state and an entry
//     carries nothing back;
//   - a capture with no slot in the enclosing lowering — there is no
//     `var` to bind the entry to;
//   - a free name that is neither parameter, capture-with-slot, nor
//     `this` — a module import, a global, an outer function;
//   - a nested function inside the arrow, and a `this` used as a VALUE
//     rather than as a call receiver;
//   - a body the ordinary summary lowering declines.
//
// ASYNC arrows convert exactly like sync ones. A lowered async body's
// #ret holds the SETTLED inner value (the ret-as-inner convention), so
// `xs.map(async cb)` and `xs.map(cb)` lower identically and the await
// over the result adds nothing.
//
// The recognized statement forms — each total-or-decline, each over a
// FLATTENED array receiver whose "xs.len"/"xs.elem" slots resolve:
//
//	ys = xs.map(cb)     → ys.len := var xs.len; call cb at xs.elem → ys.elem
//	xs.forEach(cb)      → call cb at xs.elem, no ret
//	ys = xs.filter(cb)  → ys.elem := var xs.elem; ys.len := integer ≥ 0
//	ys = await Promise.all(xs.map(cb))  → the map lowering above
//
// The join-of-elements argument is what makes one call statement cover
// a whole traversal: the element slot holds the JOIN of everything the
// array can hold, cb's summary quantifies over ALL entries, so applying
// it at the join covers cb's image of every individual element. One
// application, every iteration.
package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// arrowBlobEntry is one arrow's compiled answer. Ok false is a
// remembered decline, the same discipline summary_registry.go keeps for
// declarations.
type arrowBlobEntry struct {
	Blob       kernelbridge.SummaryBlob
	Lowered    LoweredSummary
	Parameters []parameterSlotSort
	Captures   []capturedSlot
	Ok         bool
}

// arrowBlobs keys per-SITE blobs by the arrow's own node. An arrow is
// not a declaration the contract registry knows — it exists at exactly
// one call site and its capture layout belongs to the body that spells
// it — so it is deliberately NOT stored in the declaration-keyed
// registry. Node identity is the key: the same arrow node re-lowered
// (a body compiled twice) answers the blob it already built.
var (
	arrowBlobsMu sync.Mutex
	arrowBlobs   = map[*ast.Node]arrowBlobEntry{}
)

// arrowFunctionOf is the arrow or function expression an argument is,
// through parens and casts — or nil. A function EXPRESSION converts on
// the same terms an arrow does; what matters is that the body is
// lowerable and the free names are captures, not which spelling the
// source used. (A function expression's own `this` differs at runtime,
// which is why a `this` used as a call receiver still has to resolve
// through ResolveCallee rather than being assumed.)
func arrowFunctionOf(argument *ast.Node) *ast.Node {
	if argument == nil {
		return nil
	}
	head := Unwrapped(argument)
	if ast.IsArrowFunction(head) || ast.IsFunctionExpression(head) {
		return head
	}
	return nil
}

// arrowParameterNames is an arrow's declared parameter names, or
// declines: a default, a rest, or a binding pattern is not one entry
// the call site can fill.
func arrowParameterNames(arrow *ast.Node) ([]string, bool) {
	var names []string
	for _, parameter := range arrow.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) || pd.Initializer != nil || pd.DotDotDotToken != nil {
			return nil, false
		}
		names = append(names, pd.Name().Text())
	}
	return names, true
}

// arrowBoundNames is every name bound INSIDE the arrow's body: its
// parameters, and every local it declares (including the names an
// object binding pattern binds). A read of one of these is not a free
// name, so the capture scan skips it.
func arrowBoundNames(arrow *ast.Node) map[string]struct{} {
	bound := map[string]struct{}{}
	for _, parameter := range arrow.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if ast.IsIdentifier(pd.Name()) {
			bound[pd.Name().Text()] = struct{}{}
		}
	}
	body := arrow.Body()
	if body == nil {
		return bound
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsVariableDeclaration(node) || ast.IsBindingElement(node) {
			name := node.Name()
			if name != nil && ast.IsIdentifier(name) {
				bound[name.Text()] = struct{}{}
			}
		}
		// a for-of / for-in element binding and a catch clause's parameter
		// arrive through the same declaration nodes above; nothing else
		// binds a plain name in the lowered subset
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return bound
}

// writtenNamesIn is every name the subtree WRITES: an assignment's
// left side, a compound assignment's, and a step's operand — each read
// through its spelled head, so `total += 1` reports "total" and
// `o.k = 1` reports "o".
//
// It walks into nested functions too, which over-reports: a write to a
// NESTED function's own local would be read as a captured write here.
// The over-report changes no outcome — a nested function declines the
// arrow outright in the scan below — and erring toward decline is the
// safe direction for a rule whose whole job is to catch writes.
func writtenNamesIn(node *ast.Node) map[string]struct{} {
	written := map[string]struct{}{}
	note := func(target *ast.Node) {
		head := Unwrapped(target)
		for ast.IsPropertyAccessExpression(head) {
			head = Unwrapped(head.AsPropertyAccessExpression().Expression)
		}
		for ast.IsElementAccessExpression(head) {
			head = Unwrapped(head.AsElementAccessExpression().Expression)
		}
		if ast.IsIdentifier(head) {
			written[head.Text()] = struct{}{}
		}
	}
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if ast.IsBinaryExpression(child) {
			bin := child.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment {
				note(bin.Left)
			}
		}
		if ast.IsPrefixUnaryExpression(child) {
			unary := child.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				note(unary.Operand)
			}
		}
		if ast.IsPostfixUnaryExpression(child) {
			unary := child.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				note(unary.Operand)
			}
		}
		child.ForEachChild(visit)
		return false
	}
	visit(node)
	return written
}

// freeNameScan is what the scan reports: the free names read, in SOURCE
// ORDER OF FIRST READ (the order the capture entries are laid out in),
// and whether the arrow left the convertible subset at all.
type freeNameScan struct {
	Reads []string
	Ok    bool
}

// scanFreeNames walks an arrow's body and reports its free reads in
// first-read order, declining where the arrow leaves the convertible
// subset:
//
//   - a nested function inside the arrow — its own captures would need
//     converting too, and the lowering declines nested functions anyway;
//   - `this` in any position other than the RECEIVER of a call — a
//     `this` read as a value is a whole object no slot holds;
//   - a WRITE to any name the arrow did not bind — the caller's state
//     would move and no entry carries it back.
//
// A `this.m(…)` call contributes NO free name: the receiver is consumed
// by the resolution, and whether the callee resolves is decided by the
// ordinary call lowering (which asks ResolveCallee) rather than here.
func scanFreeNames(arrow *ast.Node) freeNameScan {
	body := arrow.Body()
	if body == nil {
		return freeNameScan{}
	}
	bound := arrowBoundNames(arrow)
	written := writtenNamesIn(body)
	// a write to a name the arrow did not bind is a captured write
	for name := range written {
		if _, isBound := bound[name]; !isBound {
			return freeNameScan{}
		}
	}
	var reads []string
	seen := map[string]struct{}{}
	declined := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if declined {
			return true
		}
		if ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) ||
			ast.IsArrowFunction(node) || ast.IsClassDeclaration(node) ||
			ast.IsClassExpression(node) {
			declined = true
			return true
		}
		// `this.m(…)` — the receiver is consumed whole; the arguments still
		// scan, since they may read captures
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			callee := Unwrapped(call.Expression)
			if ast.IsPropertyAccessExpression(callee) {
				receiver := Unwrapped(callee.AsPropertyAccessExpression().Expression)
				if receiver.Kind == ast.KindThisKeyword {
					if call.Arguments != nil {
						for _, argument := range call.Arguments.Nodes {
							visit(argument)
						}
					}
					return false
				}
			}
		}
		// `this` anywhere else — as a value, as a property read, as an
		// argument — is a whole object no slot holds
		if node.Kind == ast.KindThisKeyword {
			declined = true
			return true
		}
		// a property access's NAME half is not a read of a binding
		if ast.IsPropertyAccessExpression(node) {
			visit(node.AsPropertyAccessExpression().Expression)
			return false
		}
		if ast.IsIdentifier(node) {
			name := node.Text()
			if _, isBound := bound[name]; !isBound {
				if _, already := seen[name]; !already {
					seen[name] = struct{}{}
					reads = append(reads, name)
				}
			}
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	if declined {
		return freeNameScan{}
	}
	return freeNameScan{Reads: reads, Ok: true}
}

// capturesOf turns the scan's free reads into capture entries, or
// declines: every free name must resolve to a slot in the ENCLOSING
// lowering, since the call site binds each entry to a `var` of that
// slot. A free name with no slot — an import, a global, an outer
// function — has no `var` to bind, so the arrow declines.
//
// The capture list keeps the scan's order, which is the layout the
// entries take after the declared parameters.
func capturesOf(context *LoweringContext, scan freeNameScan) ([]capturedSlot, []int, bool) {
	if !scan.Ok {
		return nil, nil, false
	}
	var captures []capturedSlot
	var slots []int
	for _, name := range scan.Reads {
		index, found := slotIndexOfName(context, name)
		if !found {
			return nil, nil, false
		}
		if index >= len(context.Sorts) || index >= len(context.Typeofs) {
			return nil, nil, false
		}
		captures = append(captures, capturedSlot{
			Name:      name,
			Sort:      context.Sorts[index],
			TypeofTag: context.Typeofs[index],
		})
		slots = append(slots, index)
	}
	return captures, slots, true
}

// convertedArrow is a converted callback: the blob its summary compiled
// to, the slot layout the entries follow, and the caller slots the
// capture entries read.
type convertedArrow struct {
	Node         *ast.Node
	Blob         kernelbridge.SummaryBlob
	Lowered      LoweredSummary
	CaptureSlots []int
}

// arrowParameterSorts is the sort and typeof evidence each DECLARED
// parameter entry wears at this site. The first parameter takes the
// receiver's element slot — the entry the call statement fills with a
// `var` of it — so the summary is compiled over exactly the values that
// entry can hold. Every parameter past the first gets ABSENT at the call
// site (the two-slot flattening holds no per-position index and no whole
// array), and an absent entry has no sort to promise, so those read
// unknown.
//
// Without this the arrow's own arithmetic would decline: an unannotated
// `x => x + 1` reads its parameter through declaredParamSort, which is
// unknown for an unannotated parameter, and arithmetic admits only the
// number sort. The declaration route must stay annotation-read — its
// summary quantifies over callers no lowering can see — but an arrow
// argument has exactly one site and that site knows the sort.
func arrowParameterSorts(context *LoweringContext, arrow *ast.Node, elementSlot int) []parameterSlotSort {
	declared := len(arrow.Parameters())
	if declared == 0 {
		return nil
	}
	sorts := make([]parameterSlotSort, declared)
	for index := range sorts {
		sorts[index] = parameterSlotSort{Sort: BindingKindUnknown, TypeofTag: TypeofTagNone}
	}
	if elementSlot >= 0 && elementSlot < len(context.Sorts) && elementSlot < len(context.Typeofs) {
		sorts[0] = parameterSlotSort{
			Sort:      context.Sorts[elementSlot],
			TypeofTag: context.Typeofs[elementSlot],
		}
	}
	return sorts
}

// convertArrow closure-converts an arrow argument and compiles it: scan
// the free names, resolve each to a caller slot, lower the body with the
// declared parameters wearing the site's element sort and the captures
// laid out after them, and ask the kernel to compile it — exactly as
// buildSummaryBlob does for a declaration, through
// kernelbridge.AskSummarize with the lowering's own slot count as the
// arity and the table its call statements built.
//
// The blob is remembered under the arrow NODE, not in the declaration-
// keyed registry: an arrow belongs to its one site.
func convertArrow(context *LoweringContext, argument *ast.Node, elementSlot int) (convertedArrow, bool) {
	arrow := arrowFunctionOf(argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	if context.Flow == nil {
		return convertedArrow{}, false
	}
	if _, ok := arrowParameterNames(arrow); !ok {
		return convertedArrow{}, false
	}
	captures, captureSlots, ok := capturesOf(context, scanFreeNames(arrow))
	if !ok {
		return convertedArrow{}, false
	}
	parameterSorts := arrowParameterSorts(context, arrow, elementSlot)
	arrowBlobsMu.Lock()
	held, has := arrowBlobs[arrow]
	arrowBlobsMu.Unlock()
	if has {
		if !held.Ok {
			return convertedArrow{}, false
		}
		// the remembered blob was compiled against ITS entry layout — the
		// parameter sorts the site supplied AND the captures it resolved. A
		// re-lowering that resolved a different one is a different arrow's
		// worth of entries, so it declines rather than reusing the blob.
		if !sameParameterSorts(held.Parameters, parameterSorts) || !sameCaptures(held.Captures, captures) {
			return convertedArrow{}, false
		}
		return convertedArrow{Node: arrow, Blob: held.Blob, Lowered: held.Lowered, CaptureSlots: captureSlots}, true
	}
	lowered, loweredOk := lowerArrowSummary(context.Flow, arrow, parameterSorts, captures)
	if !loweredOk {
		arrowBlobsMu.Lock()
		arrowBlobs[arrow] = arrowBlobEntry{Parameters: parameterSorts, Captures: captures}
		arrowBlobsMu.Unlock()
		return convertedArrow{}, false
	}
	blob, asked := kernelbridge.AskSummarize(lowered.SlotCount, lowered.Stmts, lowered.Table)
	if !asked {
		arrowBlobsMu.Lock()
		arrowBlobs[arrow] = arrowBlobEntry{Parameters: parameterSorts, Captures: captures}
		arrowBlobsMu.Unlock()
		return convertedArrow{}, false
	}
	arrowBlobsMu.Lock()
	arrowBlobs[arrow] = arrowBlobEntry{
		Blob:       blob,
		Lowered:    lowered,
		Parameters: parameterSorts,
		Captures:   captures,
		Ok:         true,
	}
	arrowBlobsMu.Unlock()
	return convertedArrow{Node: arrow, Blob: blob, Lowered: lowered, CaptureSlots: captureSlots}, true
}

// sameParameterSorts is whether two declared-parameter layouts agree
// entry for entry — the other half of the check a remembered blob's
// reuse turns on. A blob compiled with a number-sorted first entry may
// not be reused at a site whose element slot is a string.
func sameParameterSorts(held []parameterSlotSort, wanted []parameterSlotSort) bool {
	if len(held) != len(wanted) {
		return false
	}
	for index := range held {
		if held[index].Sort != wanted[index].Sort || held[index].TypeofTag != wanted[index].TypeofTag {
			return false
		}
	}
	return true
}

// sameCaptures is whether two capture layouts agree name for name in
// order — the check a remembered blob's reuse turns on.
func sameCaptures(held []capturedSlot, wanted []capturedSlot) bool {
	if len(held) != len(wanted) {
		return false
	}
	for index := range held {
		if held[index].Name != wanted[index].Name || held[index].Sort != wanted[index].Sort {
			return false
		}
	}
	return true
}

// arrowCallStatement builds the ONE call statement a converted arrow
// takes: the declared parameter entries take `element` (the receiver's
// element slot as an effect), the capture entries take a `var` of each
// captured caller slot, every remaining entry enters absent with the
// done flag at {0} — exactly the entry states applySummary sends, so a
// spliced compile and a direct apply agree — and Rets maps the arrow's
// #ret out-state onto `target`, or nothing where target is -1.
func arrowCallStatement(
	context *LoweringContext,
	converted convertedArrow,
	element kernelbridge.LoopEffect,
	target int,
) (kernelbridge.IrStatement, bool) {
	if context.SummaryTable == nil {
		return kernelbridge.IrStatement{}, false
	}
	lowered := converted.Lowered
	declared := len(converted.Node.Parameters())
	if declared+len(converted.CaptureSlots) > lowered.ParamCount {
		return kernelbridge.IrStatement{}, false
	}
	args := make([]kernelbridge.LoopEffect, 0, lowered.SlotCount)
	// the declared parameters: the element the traversal hands each pass.
	// A callback declaring an index or the whole array beyond its first
	// parameter gets ABSENT there — the two-slot flattening holds no
	// per-position index and no whole array, and absent is the honest
	// entry for a value the site cannot supply.
	for index := 0; index < declared; index++ {
		if index == 0 {
			args = append(args, element)
			continue
		}
		args = append(args, kernelbridge.AbsentConst())
	}
	// the capture entries, in the scan's order
	for _, slot := range converted.CaptureSlots {
		args = append(args, varEffect(slot))
	}
	for len(args) < lowered.SlotCount {
		if len(args) == lowered.DoneIndex {
			args = append(args, kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
			})
			continue
		}
		args = append(args, kernelbridge.AbsentConst())
	}
	rets := make([]int, lowered.RetIndex+1)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 {
		rets[lowered.RetIndex] = target
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(converted.Node, converted.Blob),
		Args:   args,
		Rets:   rets,
	}, true
}

// collectionCall is one recognized `xs.m(cb)` shape: the receiver's
// spelled name, the method, and the single callback argument.
type collectionCall struct {
	Receiver string
	Method   string
	Callback *ast.Node
}

// collectionCallOf reads `xs.m(cb)` with exactly one argument and a
// plain (non-optional) receiver identifier — the only receiver shape
// the two-slot flattening resolves.
func collectionCallOf(node *ast.Node) (collectionCall, bool) {
	head := Unwrapped(node)
	if !ast.IsCallExpression(head) {
		return collectionCall{}, false
	}
	call := head.AsCallExpression()
	if call.QuestionDotToken != nil {
		return collectionCall{}, false
	}
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return collectionCall{}, false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return collectionCall{}, false
	}
	if !ast.IsIdentifier(property.Expression) || !ast.IsIdentifier(property.Name()) {
		return collectionCall{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return collectionCall{}, false
	}
	argument := call.Arguments.Nodes[0]
	if ast.IsSpreadElement(argument) {
		return collectionCall{}, false
	}
	return collectionCall{
		Receiver: property.Expression.Text(),
		Method:   property.Name().Text(),
		Callback: argument,
	}, true
}

// targetArraySlotsOf resolves the slots a mapped/filtered RESULT lands
// in: its "ys.len" and "ys.elem" pair, allocated where the enclosing
// layout did not lay them out. A result that is neither laid out nor
// allocatable declines the whole statement — there is nowhere to write.
func targetArraySlotsOf(context *LoweringContext, name string) (lenSlot int, elemSlot int, ok bool) {
	if lenSlot, elemSlot, found := arraySlotsOf(context, name); found {
		return lenSlot, elemSlot, true
	}
	if context.Allocate == nil {
		return 0, 0, false
	}
	lenSlot, lenOk := slotIndexOfName(context, name+arrayLenSuffix)
	if !lenOk {
		lenSlot, lenOk = context.Allocate(name+arrayLenSuffix, BindingKindNumber, TypeofTagNumber)
		if !lenOk {
			return 0, 0, false
		}
	}
	elemSlot, elemOk := slotIndexOfName(context, name+arrayElemSuffix)
	if !elemOk {
		// the element sort is UNKNOWN: what the callback returns is the
		// kernel's answer, not something the site reads off syntax. An
		// unknown-sorted slot admits the definedness test alone, which is
		// the honest standing for a value nothing here promised a sort for.
		elemSlot, elemOk = context.Allocate(name+arrayElemSuffix, BindingKindUnknown, TypeofTagNone)
		if !elemOk {
			return 0, 0, false
		}
	}
	return lenSlot, elemSlot, true
}

// nonNegativeIntegerSet is "an integer at least 0" — what a FILTER's
// result length is known to be and no more. The two-slot flattening has
// no spelling for "at most the source's length": that upper bound is a
// relation between two slots, and a stored set relates a slot to
// constants alone. The precision is honestly lost here rather than
// claimed.
func nonNegativeIntegerSet() refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0))
}

// mapStatements is `ys = xs.map(cb)` over a flattened source: the
// result's length is the source's exactly (map preserves length), and
// the result's element is cb's return.
//
// The one call statement covers the WHOLE traversal. The source's
// element slot holds the join of every element the array can hold, and
// cb's summary quantifies over all entries — so applying cb at that
// join answers a set covering cb's image of each individual element.
// Every per-pass image is admitted by the one answer.
func mapStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	sourceLen, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	targetLen, targetElem, targetOk := targetArraySlotsOf(context, target)
	if !targetOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, varEffect(sourceElem), targetElem)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: targetLen, Effect: varEffect(sourceLen)},
		call,
	}, true
}

// filterStatements is `ys = xs.filter(cb)`: every surviving element is
// one the source held, so the result's element slot copies the source's
// — and the result's length is an integer ≥ 0, the honest loss above.
//
// cb still has to CONVERT even though its truthiness answer is not
// modeled. The conversion is not there to read the predicate; it is
// there so an effectful callback cannot hide: a cb that writes a
// capture, reads an unslotted name, or leaves the lowered subset
// declines the whole statement rather than passing as a pure predicate.
func filterStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	if _, convertedOk := convertArrow(context, source.Callback, sourceElem); !convertedOk {
		return nil, false
	}
	targetLen, targetElem, targetOk := targetArraySlotsOf(context, target)
	if !targetOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: targetElem, Effect: varEffect(sourceElem)},
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: targetLen,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: nonNegativeIntegerSet()},
		},
	}, true
}

// forEachStatement is `xs.forEach(cb)`: the single call statement at
// the element entry with NO ret — forEach's own value is undefined and
// nothing reads it.
//
// Nothing rides back into the caller's slots, and that is by rule
// rather than by omission: captures are READ-ONLY, so a cb that would
// write one declined at conversion. What the cb's body does beyond its
// own calls touches only the cb's own slots, which end with the
// application.
func forEachStatement(context *LoweringContext, source collectionCall) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, varEffect(sourceElem), -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{call}, true
}

// promiseAllMapOf reads `Promise.all(xs.map(cb))` — through an await
// where one wraps it — and answers the inner map call.
//
// The await adds NOTHING. A lowered async body's #ret already holds the
// settled inner value (the ret-as-inner convention), so the array
// Promise.all settles to has exactly the elements the map lowering
// already wrote into the result's element slot: cb's ret. Awaiting the
// whole is the identity on that slot.
func promiseAllMapOf(expression *ast.Node) (collectionCall, bool) {
	head := Unwrapped(expression)
	if ast.IsAwaitExpression(head) {
		head = Unwrapped(head.AsAwaitExpression().Expression)
	}
	if !ast.IsCallExpression(head) {
		return collectionCall{}, false
	}
	call := head.AsCallExpression()
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return collectionCall{}, false
	}
	property := access.AsPropertyAccessExpression()
	if !ast.IsIdentifier(property.Expression) || property.Expression.Text() != "Promise" {
		return collectionCall{}, false
	}
	if !ast.IsIdentifier(property.Name()) || property.Name().Text() != "all" {
		return collectionCall{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return collectionCall{}, false
	}
	inner, innerOk := collectionCallOf(call.Arguments.Nodes[0])
	if !innerOk || inner.Method != "map" {
		return collectionCall{}, false
	}
	return inner, true
}

// SummaryCallbackStatementOf is the lowering-side entry: a statement
// whose right side is a recognized collection call over a flattened
// array with a closure-converting callback, lowered to the call
// statement (and the length write the shape carries).
//
// Total-or-decline. Anything unrecognized — a receiver that is not
// flattened, a callback that captures a write, a method not listed, a
// result with nowhere to land — answers false, and the statement then
// takes whatever route it had.
func SummaryCallbackStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || statement == nil {
		return nil, false
	}
	// `xs.forEach(cb);` — the value goes nowhere, but the traversal runs
	if ast.IsExpressionStatement(statement) {
		expression := Unwrapped(statement.AsExpressionStatement().Expression)
		if source, ok := collectionCallOf(expression); ok && source.Method == "forEach" {
			return forEachStatement(context, source)
		}
	}
	// `ys = e` / `const ys = e` — the shape both call routes read. The
	// target here is an ARRAY, whose two slots the name resolves to, so
	// the scalar target callAssignmentShapeOf answers with is not what
	// this uses; the NAME is.
	target, rhs, shapeOk := callbackAssignmentNameOf(context, statement)
	if !shapeOk {
		return nil, false
	}
	// `ys = await Promise.all(xs.map(cb))` / the unawaited spelling —
	// the map lowering, since the await is the identity on the result
	if inner, ok := promiseAllMapOf(rhs); ok {
		return mapStatements(context, inner, target)
	}
	source, sourceOk := collectionCallOf(rhs)
	if !sourceOk {
		return nil, false
	}
	switch source.Method {
	case "map":
		return mapStatements(context, source, target)
	case "filter":
		return filterStatements(context, source, target)
	}
	return nil, false
}

// callbackAssignmentNameOf is the `ys = e` / `const ys = e` shape read
// for its target NAME rather than its slot.
//
// callAssignmentShapeOf (ir_summary_call.go) reads the same two shapes
// but resolves the target through IndexOf, which answers a SCALAR slot
// — a flattened array target has no such slot, so that reader declines
// exactly the statements this one has to admit. The two readers share
// the shapes and differ only in what they resolve the target to; this
// one is used where the target is an array.
func callbackAssignmentNameOf(context *LoweringContext, statement *ast.Node) (name string, rhs *ast.Node, ok bool) {
	if ast.IsVariableStatement(statement) {
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return "", nil, false
		}
		d := declarations[0].AsVariableDeclaration()
		if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
			return "", nil, false
		}
		return d.Name().Text(), d.Initializer, true
	}
	if !ast.IsExpressionStatement(statement) {
		return "", nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return "", nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return "", nil, false
	}
	left := Unwrapped(bin.Left)
	if !ast.IsIdentifier(left) {
		return "", nil, false
	}
	return left.Text(), bin.Right, true
}

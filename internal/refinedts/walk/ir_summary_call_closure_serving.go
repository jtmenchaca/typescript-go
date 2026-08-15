// split from ir_summary_call.go — the served closure call's gate and its capture census

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── the served closure call ─────────────────────────────────────── */

// ClosureCallStatementOf SERVES a call through a body-local closure —
// `cleanup()`, `endStream()`, `onClose()` — instead of havocking the
// closure's write set.
//
// WHAT MAKES IT POSSIBLE. A summary's entries used to be the callee's
// parameters alone, so a closure writing the captured `settled` had no
// entry spelling that write and nothing for a ret to map back through.
// The capture rows are that spelling: the closure's summary allocates one
// entry per captured name beside its parameters
// (lowerSummaryBodyWithCaptures' capture loop), and a WRITTEN capture's
// row rides out in BundleEntries exactly as a record-parameter leaf does.
//
// WHERE THE ENTRIES COME FROM. The caller and the closure share the
// scope: a captured `settled` IS the caller's `settled` slot, identified
// by spelled name against the caller's own slot table — the same
// resolution ClosureWriteSlots already performs to compute the havoc set.
// So entry j takes `var <that slot>` on the way in, and a written row's
// exit maps back onto that same slot on the way out. Nothing is threaded
// through a path or a holder; a capture's identity is its spelling.
//
// THE SERVING GATE, and every arm of it is a decline back to the
// write-set havoc rather than a decline of the body:
//
//   - the census must READ the closure whole (closureCapturedCensus) —
//     a nested function, a `this` in any position, an element step
//     through a capture, or a captured object handed to code refuses it;
//   - EVERY capture must resolve to a caller slot. One that does not —
//     an import, a module-level const, an outer function's local the
//     caller never laid out — has no `var` to bind its entry to and no
//     slot for its write-back to land on. A partial fill is not an
//     option: an unresolved capture's entry would enter absent, which
//     CLAIMS the name is undefined inside the closure. An OBJECT capture
//     resolves the same way one level down: its leaf vocabulary is the
//     caller's own flattened leaves under that name — at whatever depth
//     the caller laid them out, since a nested literal flattens to
//     "p.a.b" and leafSlotsUnder hands that back under the path "a.b" —
//     and a capture whose caller value was never flattened, or one
//     reading a path the caller never laid out, has no vocabulary at all;
//   - the closure's body must LOWER (lowerArrowSummary) and the kernel
//     must compile it. Either refusal leaves the site exactly where it
//     was.
//
// WHAT IS NOT WEAKER THAN THE HAVOC. The havoc route wrote `unknown`
// into every slot the closure assigns. This route writes each written
// capture's own EXIT into that same slot, and an exit is what the
// closure actually left there — never weaker, since the kernel's own
// walk answers top wherever the body could not say more.
func ClosureCallStatementOf(
	context *LoweringContext,
	call *ast.Node,
	target int,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || call == nil || !ast.IsCallExpression(call) {
		return nil, false
	}
	if context.Flow == nil || context.SummaryTable == nil {
		return nil, false
	}
	closure, ok := localClosureOf(context, call.AsCallExpression().Expression)
	if !ok {
		return nil, false
	}
	// a closure whose own build is running — `step()` calling itself —
	// cannot splice itself, and the write-set havoc below is its floor
	if _, building := context.Inlining[closure]; building {
		return nil, false
	}
	// an ARGUMENT at a served closure call moves nothing the entries
	// carry: the closure's parameters are laid out from its own
	// annotations, and this route fills them from the arguments below
	callExpression := call.AsCallExpression()
	var callArguments []*ast.Node
	if callExpression.Arguments != nil {
		callArguments = callExpression.Arguments.Nodes
	}
	for _, argument := range callArguments {
		if ast.IsSpreadElement(argument) || ContainsWrite(argument) {
			return nil, false
		}
	}
	if len(callArguments) > len(closure.Parameters()) {
		return nil, false
	}
	captures, captureSlots, capturesOk := closureCapturesOf(context, closure)
	if !capturesOk {
		return nil, false
	}
	converted, convertedOk := convertLocalClosure(context, closure, captures)
	if !convertedOk {
		return nil, false
	}
	statement, statementOk := closureCallStatement(
		context, converted, closure, captureSlots, callArguments, target)
	if !statementOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{statement}, true
}

// closureCapturesOf runs the census over a closure and resolves every
// captured name to the caller's own slot — the entry layout and the
// caller slots the fill and the write-backs read, in ONE order.
//
// The order is the census's own (source order of first use), and it is
// the order the layout lays the entries out in, so capture j's entry and
// capture j's caller slot are the same j at both seams. A map's iteration
// would not be.
//
// (false) where the census refused, or where ANY captured name has no
// caller slot — the whole-or-nothing gate ClosureCallStatementOf's doc
// states.
func closureCapturesOf(
	context *LoweringContext,
	closure *ast.Node,
) ([]capturedSlot, []int, bool) {
	reads, objects, writes, ok := closureCapturedCensus(closure)
	if !ok {
		return nil, nil, false
	}
	var captures []capturedSlot
	var slots []int
	// the OBJECT captures first, each expanded into its leaves. Their
	// SHAPE comes from the CALLER: the leaf vocabulary is whatever the
	// caller's own slot family holds under that name (a flattened record
	// local's leaves), and a member the census read that the caller never
	// laid out has no slot to enter from — the whole capture refuses,
	// exactly as a scalar capture with no caller slot does.
	for _, object := range objects {
		leaves, leavesOk := leafSlotsUnder(context, object.Name)
		if !leavesOk {
			// the caller's value under this name is NOT flattened — an
			// ordinary scalar slot, an unrecognized local, a parameter the
			// layout never expanded, an import. There is no leaf vocabulary
			// to lay entries out from, so the site keeps the write-set havoc.
			return nil, nil, false
		}
		slotOfPath := map[string]int{}
		for _, leaf := range leaves {
			slotOfPath[leaf.Path] = leaf.Index
		}
		// a method call the callee resolution says MAY move the receiver
		// havocs every leaf inside the summary, so every leaf must ride out
		// Written for the caller to take the moved values back
		moves := false
		methodWrites := map[string]struct{}{}
		for _, method := range object.MethodCalls {
			if capturedMethodMoves(context, closure, object.Name, method) {
				methodWrites[method] = struct{}{}
				moves = true
			}
		}
		capture := capturedSlot{
			Name:         object.Name,
			MethodCalls:  object.MethodCalls,
			MethodWrites: methodWrites,
		}
		for _, member := range object.Members {
			slot, held := slotOfPath[member]
			if !held {
				// a member the closure reads that the caller's flattening never
				// laid out: no slot to fill the entry from, and filling it
				// absent would CLAIM the member is undefined inside the closure
				return nil, nil, false
			}
			if slot >= len(context.Sorts) || slot >= len(context.Typeofs) {
				return nil, nil, false
			}
			_, writtenHere := object.Written[member]
			capture.Members = append(capture.Members, capturedLeaf{
				Member:    member,
				Sort:      context.Sorts[slot],
				TypeofTag: context.Typeofs[slot],
				Written:   writtenHere || moves,
			})
			slots = append(slots, slot)
		}
		if len(capture.Members) == 0 {
			// a capture used only as a method receiver reads no leaf and
			// carries no entry — the layout would allocate nothing for it and
			// the havoc it needs would have no slot to land on
			return nil, nil, false
		}
		captures = append(captures, capture)
	}
	for _, name := range reads {
		index, found := slotIndexOfName(context, name)
		if !found {
			// no caller slot: no `var` to bind the entry to, and no place
			// for a write-back to land
			return nil, nil, false
		}
		if index >= len(context.Sorts) || index >= len(context.Typeofs) {
			return nil, nil, false
		}
		// a capture the caller FLATTENED (a record, an array, a
		// collection) has leaves the entry does not hold, and a write
		// through one of them moves a slot no row names
		if leaves := flattenedSlotsUnder(context, name); len(leaves) > 0 {
			return nil, nil, false
		}
		_, written := writes[name]
		captures = append(captures, capturedSlot{
			Name:      name,
			Sort:      context.Sorts[index],
			TypeofTag: context.Typeofs[index],
			Written:   written,
		})
		slots = append(slots, index)
	}
	// a write the census reported for a name the read list does not
	// carry would be a row with no entry — the census appends every
	// written name to its reads, so this states the invariant rather
	// than fixing anything
	for name := range writes {
		held := false
		for _, capture := range captures {
			if capture.Name == name {
				held = true
				break
			}
		}
		if !held {
			return nil, nil, false
		}
	}
	return captures, slots, true
}

// capturedMethodMoves answers whether `<capture>.<method>(…)` inside a
// closure may move a member of the captured object.
//
// The reading is callee_effects' own, unchanged in substance: a callee
// this package can SUMMARIZE answers from its summary — a body that
// lowered, wrote no this-field and returned no receiver moved nothing on
// the object it ran on (SummaryReceiverEffects) — and everything else
// answers TRUE. An unresolved callee, a declined lowering, a builtin
// (`removeListener`, `end`) whose declaration this package holds no body
// for: each is a doubt, and every doubt moves the object.
//
// True costs the leaves their believability from that statement on; it
// never costs the closure its serving, which is the difference between
// this and the census refusing.
func capturedMethodMoves(
	context *LoweringContext,
	closure *ast.Node,
	name string,
	method string,
) bool {
	if context == nil || context.Flow == nil {
		return true
	}
	call, found := capturedMethodCallIn(closure, name, method)
	if !found {
		return true
	}
	contract := ContractOf(context.Flow, call.AsCallExpression().Expression)
	if contract == nil || contract.Declaration == nil {
		return true
	}
	// a callee whose lowering is already running cannot be summarized from
	// underneath itself — the memo fills only when it finishes
	if _, building := context.Inlining[contract.Declaration]; building {
		return true
	}
	if _, lowered := LowerSummaryBody(context.Flow, contract.Declaration); !lowered {
		return true
	}
	receiverTouched, _ := SummaryReceiverEffects(context.Flow, contract.Declaration)
	return receiverTouched
}

// capturedMethodCallIn finds ONE call node spelling
// `<name>.<method>(…)` inside a closure's body — the site the contract
// lookup needs, since a contract resolves from an expression and the
// census reports only spellings.
//
// The FIRST such call is enough: every call under one spelling resolves
// through the same property access on the same name, so they answer one
// contract.
func capturedMethodCallIn(closure *ast.Node, name string, method string) (*ast.Node, bool) {
	body := closure.Body()
	if body == nil {
		return nil, false
	}
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsCallExpression(node) {
			callee := Unwrapped(node.AsCallExpression().Expression)
			if callee != nil && ast.IsPropertyAccessExpression(callee) {
				access := callee.AsPropertyAccessExpression()
				receiver := Unwrapped(access.Expression)
				if receiver != nil && ast.IsIdentifier(receiver) &&
					receiver.Text() == name && ast.IsIdentifier(access.Name()) &&
					access.Name().Text() == method {
					found = node
					return true
				}
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return found, found != nil
}

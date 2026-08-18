// split from lowering_to_kernel_ir.go — the flattening and assignment routes

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// lowerFlatteningRoutes is the run of STRAIGHT-LINE routes lowerStatementList
// tries in order once the return and throw routes have passed: the flattened
// declarations, the record and collection writes, the ordinary assignment, and
// the call statements. Every one of them has the same shape — read the
// statement, flush this statement's hoists, append, and the statement is done —
// so they are one block here, in the order they were tried in.
//
// Answers (out, true) where a route took the statement, and (out, false) where
// every one of them declined, with `out` unchanged: a declining route appends
// nothing and truncates its own hoists back to `mark`, exactly as it did
// inside the walk. The caller carries on with the composite routes.
func lowerFlatteningRoutes(
	context *LoweringContext,
	s *ast.Node,
	out []kernelbridge.IrStatement,
	mark int,
) ([]kernelbridge.IrStatement, bool) {
	flush := func(out []kernelbridge.IrStatement) []kernelbridge.IrStatement {
		return append(out, TakeHoisted(context)...)
	}
	dropHoists := func() { DropHoistedFrom(context, mark) }
	// `const p = { lo: 0, hi: n }` — a record local flattened into one
	// slot per LEAF lowers as N ordinary assignments at the
	// declaration's position, in literal order. Tried ahead of the
	// single-name reader, which has no slot for `p` itself.
	if assignments, ok := ObjectDeclarationAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `const a = [1, 2, 3]` — an array local flattened into its two
	// slots: the count and the join of the elements.
	if assignments, ok := ArrayDeclarationAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `const m = new Map(…)` / `new Set(…)` — a collection flattened
	// into its size, values, and (Map) keys slots.
	if assignments, ok := MapDeclarationAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `const { x, y } = p` — a flattened record read leaf by leaf into
	// the destructured names.
	if assignments, ok := DestructuringAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `const { x = 1 } = p` — the leaf-exact destructuring with
	// per-element defaults under the definedness branch.
	if viaDefaults, ok := DestructuringWithDefaultsOf(context, s); ok {
		out = flush(out)
		out = append(out, viaDefaults...)
		return out, true
	}
	dropHoists()
	// `const { circleTangency } = getTangentCircle(...)` — a call whose
	// CALLEE HAS A COMPILED SUMMARY carrying a per-member RetShape
	// (returnedLiteralShape): each bound name that matches a returned
	// member reads that member's own exit, not unknown. Tried ahead of
	// PatternAssignmentsOf, which would otherwise claim this same shape
	// first and havoc every bound name — PatternAssignmentsOf's own
	// unknown answer is exactly what stays true for a callee this route
	// cannot read a summary for (an opaque/imported one, a scalar return,
	// a nested/nested/rest pattern element), so it remains the fallback.
	if viaRetMembers, ok := destructuredCallDeclarationStatement(context, s); ok {
		out = flush(out)
		out = append(out, viaRetMembers...)
		return out, true
	}
	dropHoists()
	// `const { a } = call()` and every other pattern source the exact
	// route above declined: the bound names take unknown — which is
	// what is true of them — and the source's call lowers through the
	// call machinery. AFTER the leaf-exact route, so a flattened
	// record's pattern keeps its real values.
	if viaPattern, ok := PatternAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, viaPattern...)
		return out, true
	}
	dropHoists()
	// `p = q` / `p = { … }` — a whole record written leaf for leaf.
	// Ahead of the single-name reader, which has no slot for `p`.
	if assignments, ok := RecordAssignmentOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `a.push(v)` — the length steps, the element slot joins.
	if assignments, ok := ArrayPushAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `a.pop()` / `a.shift()` — the length shrinks (never below zero);
	// the element slot keeps its join.
	if assignments, ok := ArrayShrinkAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `a[i] = v` — the element slot joins; the length is untouched.
	if assignments, ok := ArrayIndexWriteOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `m.set(k, v)` / `s.add(v)` — the size may step, the keys and
	// values slots join.
	if assignments, ok := MapSetAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `m.delete(k)` — the size may shrink (never below zero); the
	// keys and values slots keep their joins.
	if assignments, ok := MapDeleteAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `m.clear()` — the size takes exactly zero; the keys and values
	// slots return to the empty collection's state.
	if assignments, ok := MapClearAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `m.getOrInsert(k, v)` — the size may step, the keys and values
	// slots join; a bound result reads the joined values slot.
	if assignments, ok := MapGetOrInsertAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `const f = () => { … }` — a closure held in a local: creation
	// runs nothing, the name takes unknown, admitted only when the
	// closure touches no tracked state.
	if viaClosure, ok := FunctionValuedDeclarationOf(context, s); ok {
		out = flush(out)
		out = append(out, viaClosure...)
		return out, true
	}
	dropHoists()
	// `let a = 1, b = 2` — several ordinary declarators in one
	// statement, each lowering by the single declarator's own rule.
	if assignments, ok := MultiDeclarationAssignmentsOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `x1 = x2 = e` — the chain writes every link, innermost first,
	// each outer one copying the slot inside it. Ahead of the ordinary
	// assignment rule, whose one-target read takes the outer link and
	// then declines on a right side no effect grammar spells.
	if assignments, ok := ChainedAssignmentStatementOf(context, s); ok {
		out = flush(out)
		out = append(out, assignsOf(assignments)...)
		return out, true
	}
	dropHoists()
	// `x = count + f(y)` / `let x = f(g(y)) + 1`: the RHS reading hoists
	// each call it met, left to right, and those statements go out ahead
	// of the assignment that reads their temps
	//
	// AssignmentOf is ALSO FoldBody's own single-statement reader
	// (ir_loop_fold.go), which substitutes its Effect into LATER
	// statements' operands via SubstituteVars — so the copy-to-varState
	// upgrade cannot live inside AssignmentOf/AssignmentOfExpression
	// themselves (a varState nested into a later arithmetic operand is
	// exactly what the kernel refuses). It is safe HERE, at this
	// terminal statement-list use, which never re-nests the effect.
	if assignment, ok := AssignmentOf(context, s); ok {
		out = flush(out)
		out = append(out, kernelbridge.IrStatement{Kind: kernelbridge.IrStatementAssign, Target: assignment.Target, Effect: asVarStateEffect(assignment.Effect)})
		return out, true
	}
	dropHoists()
	// `this.value = e` where value is a SETTER: the write runs a
	// body, so it lowers to the setter's own call statement — the
	// value at entry 0, the written this-fields riding back through
	// rets. Tried after AssignmentOf, whose target resolution has no
	// slot for an accessor-backed name.
	if viaSetter, ok := SetterWriteOf(context, s); ok {
		out = flush(out)
		out = append(out, viaSetter...)
		return out, true
	}
	dropHoists()
	// `await f(…)` in every statement position, plus the two promise
	// shapes: a promise HELD in a local and only ever awaited, and
	// `await Promise.all([…])` whose value is unused. Tried ahead of
	// the plain call route, which has no reading for an await node.
	if viaAwait, ok := AwaitStatementOf(context, s); ok {
		out = flush(out)
		out = append(out, viaAwait...)
		return out, true
	}
	dropHoists()
	// a CALLBACK-taking call (`xs.map(cb)` and its siblings) whose
	// callback converts: the hook lowers the whole site. Tried ahead
	// of the plain call route, whose callee resolution has no reading
	// for a collection method.
	if viaCallback, ok := SummaryCallbackStatementOf(context, s); ok {
		out = flush(out)
		out = append(out, viaCallback...)
		return out, true
	}
	dropHoists()
	// `f(…)` / `x = f(…)` where the callee has a compiled summary:
	// the call statement, applying the summary kernel-side. Tried
	// ahead of the inlining route, which lowers the callee's body
	// into fresh slots instead.
	if viaSummary, ok := SummaryCallStatementOf(context, s); ok {
		out = flush(out)
		out = append(out, viaSummary...)
		return out, true
	}
	dropHoists()
	if viaCall, ok := CallAssignmentOf(context, s); ok {
		out = flush(out)
		out = append(out, viaCall...)
		return out, true
	}
	dropHoists()
	return out, false
}

// assignsOf turns a per-slot assignment list into the IR statements
// that write them, in order — the one shape every flattening lowering
// hands back.
func assignsOf(assignments []AssignmentTarget) []kernelbridge.IrStatement {
	out := make([]kernelbridge.IrStatement, 0, len(assignments))
	for _, assignment := range assignments {
		out = append(out, kernelbridge.IrStatement{
			Kind:   kernelbridge.IrStatementAssign,
			Target: assignment.Target,
			Effect: assignment.Effect,
		})
	}
	return out
}

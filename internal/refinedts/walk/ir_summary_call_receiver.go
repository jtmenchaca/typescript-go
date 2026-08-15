// split from ir_summary_call.go — receiver threading: the call's receiver path, its this-entries, and its havoc slots

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── receiver threading ──────────────────────────────────────────── */

// bundleRetsAndArgs threads the CALL'S RECEIVER through the callee's
// this-field bundle entries, writing into `args` and `rets` in place.
//
// The callee's summary carries one BundleEntry per expanded bundle
// entry. The "this."-prefixed ones are the callee's own receiver fields,
// and WHICH caller values they hold is decided by the receiver the call
// was spelled on:
//
//	this.m(…)                → the caller's own "this.<field>" slots
//	wrapper.m(…)             → the caller's "wrapper.<field>" slots
//	this.injector.load(…)    → the caller's "this.injector.<field>" slots
//
// so the rule is one rule: the receiver's spelled dotted path prefixes
// the field name, and the caller's slot of that spelling fills the
// entry. A chain is not a special case — it is the same prefix one step
// longer.
//
// A field the CALLER has no slot for fills with the UNKNOWN effect,
// which evaluates top: the caller knows nothing about that field, and
// nothing is what the entry must say. It is emphatically NOT the absent
// constant the unfilled padding uses — absent claims the field IS
// undefined, which is a claim about a value the caller never had.
//
// A WRITTEN entry (the callee's body assigns that field) maps its own
// out — the entry's Index, since the compiled out vector is the whole
// binding row — back to the caller's slot for the same spelling. Where
// the caller has no slot the ret stays -1 and the write lands nowhere:
// nothing lowered can read that spelling, so no knowledge survives the
// call that the write would falsify. (The opaque-call alternative would
// have havocked those same leaves, so dropping the write-back is not
// weaker than declining the site.)
//
// Non-"this." entries are the wave-3 record-parameter leaves, already
// filled from the argument vector above; this leaves them alone.
//
// (false) only where the callee HAS this-entries and the receiver has no
// spelled path to fill them from — a computed step, an optional step, a
// call's result. Filling those with unknown would be sound, but the
// receiver is then an object the site cannot name at all, and the opaque
// tier havocs what it was handed rather than pretending the entries were
// threaded.
func bundleRetsAndArgs(
	context *LoweringContext,
	call *ast.Node,
	calleeShape LoweredSummary,
	args []kernelbridge.LoopEffect,
	rets []int,
) bool {
	thisEntries := make([]BundleEntry, 0, len(calleeShape.BundleEntries))
	for _, entry := range calleeShape.BundleEntries {
		if strings.HasPrefix(entry.Path, "this.") {
			thisEntries = append(thisEntries, entry)
		}
	}
	if len(thisEntries) == 0 {
		return true
	}
	receiverPath, pathOk := receiverPathOf(call)
	if !pathOk {
		return false
	}
	for _, entry := range thisEntries {
		if entry.Index < 0 || entry.Index >= len(args) {
			return false
		}
		field := strings.TrimPrefix(entry.Path, "this.")
		slot, held := slotIndexOfName(context, receiverPath+"."+field)
		if !held {
			// the caller has no slot for this field: unknown, never absent
			args[entry.Index] = unknownEffect
			continue
		}
		args[entry.Index] = varEffect(slot)
		if entry.Written && entry.Index < len(rets) {
			rets[entry.Index] = slot
		}
	}
	return true
}

// receiverPathOf spells the object a call was made ON, as the dotted
// path the caller's slots are named under:
//
//	this.m(…)              → "this"
//	wrapper.m(…)           → "wrapper"
//	this.injector.load(…)  → "this.injector"
//
// The path is the callee expression MINUS its last step, which is the
// method name and never a slot. A bare `f(…)` has no receiver at all.
//
// The declines, each because no slot spelling exists for what was
// written:
//
//   - a COMPUTED step (`this.parts[i].load()`, `o[k].m()`) — nothing
//     spells which object the index picked;
//   - an OPTIONAL step (`this.injector?.load()`) — the receiver may be
//     absent, and no slot carries "the fields of a maybe-absent object";
//   - a receiver that is not rooted in `this` or an identifier (a call's
//     result, a literal, a parenthesized function) — there is no name
//     for the caller's slots to have been laid out under.
func receiverPathOf(call *ast.Node) (string, bool) {
	callee := Unwrapped(call.AsCallExpression().Expression)
	if !ast.IsPropertyAccessExpression(callee) {
		// a bare `f(…)`: no receiver, so no path
		return "", false
	}
	access := callee.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", false
	}
	return dottedPathOf(Unwrapped(access.Expression))
}

// dottedPathOf reads an expression as the dotted slot spelling it names
// — "this", "wrapper", "this.injector", "a.b.c" — or (false) where a
// step is computed or optional, or the root is neither `this` nor an
// identifier.
func dottedPathOf(node *ast.Node) (string, bool) {
	var steps []string
	current := node
	for ast.IsPropertyAccessExpression(current) {
		access := current.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
			return "", false
		}
		steps = append(steps, access.Name().Text())
		current = Unwrapped(access.Expression)
	}
	switch {
	case ast.IsIdentifier(current):
		steps = append(steps, current.Text())
	case current.Kind == ast.KindThisKeyword:
		steps = append(steps, "this")
	default:
		return "", false
	}
	for left, right := 0, len(steps)-1; left < right; left, right = left+1, right-1 {
		steps[left], steps[right] = steps[right], steps[left]
	}
	return strings.Join(steps, "."), true
}

// receiverBundleHavocSlots is the OPAQUE tier's half of the same
// receiver reading: the caller slots a call's receiver names, for a call
// that took the havoc route instead of the call statement.
//
// It exists because the statement enumerator's mention rule (rule (b),
// havocSlotsOfStatement) finds a flattened local by IDENTIFIER, and a
// `this`-rooted receiver has no identifier at its root — `this` is a
// keyword. So `this.injector.load(x)`, whose receiver is the bundle
// "this.injector", walked the enumerator and contributed NOTHING: the
// callee may write any of that bundle's fields, and every one of those
// slots kept its stale knowledge across the call. An identifier-rooted
// receiver (`wrapper.get(k)`) is already covered — the enumerator sees
// `wrapper` and asks flattenedSlotsUnder for it — so this adds the
// `this`-rooted case and, for a chain, the exact bundle path the
// receiver names rather than only its root.
//
// The slots are every one spelled under the receiver's path: its leaves
// (leafSlotsUnder and the collection recognizers, through
// flattenedSlotsUnder) plus the path's OWN slot where it has one — a
// field the callee may replace outright.
func receiverBundleHavocSlots(context *LoweringContext, call *ast.Node) []int {
	receiverPath, ok := receiverPathOf(call)
	if !ok {
		return nil
	}
	out := flattenedSlotsUnder(context, receiverPath)
	if slot, held := slotIndexOfName(context, receiverPath); held {
		out = append(out, slot)
	}
	return out
}

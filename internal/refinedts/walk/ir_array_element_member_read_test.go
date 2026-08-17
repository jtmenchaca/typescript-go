// Pins for PathSlotIndexOf's arrayElementMemberLeafOf arm
// (ir_object_slots_slot_index.go) — resolving `xs[i].a` over a
// record-elemented flattened array parameter to its "xs.elem.a" leaf.

package walk

import (
	"testing"
)

// TestArrayElementMemberRead_ReturnsTheJoinedMemberAcrossCalls pins the
// SOUNDNESS story the doc on ArrayLocal.ElementMembers and
// arrayElementMemberLeafOf both state: the elem-leaf denotes the JOIN
// over every element, so an exact call CANNOT read a narrower answer
// than another element of the same array might carry — the entry
// quantifies over every element, not the one at index i. Two calls with
// different exact indices both answer from the same slot.
func TestArrayElementMemberRead_ReturnsTheJoinedMemberAcrossCalls(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	source := "function f(xs: { a: number, b: number }[], i: number) { return xs[i].a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Fatalf("outcome=%q construct=%q, want complete", outcome, construct)
	}
}

// TestArrayElementMemberRead_TwoMemberBodyBothServe pins that BOTH
// members of a two-member element record resolve — not just the first
// one a fixture happens to read — since arrayElementMemberLeafOf builds
// its spelling from the accessed NAME, not a position.
func TestArrayElementMemberRead_TwoMemberBodyBothServe(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	source := "function f(xs: { a: number, b: number }[], i: number) { return xs[i].a + xs[i].b; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome=%q construct=%q, want complete — both xs[i].a and xs[i].b should resolve", outcome, construct)
	}
}

// TestArrayElementMemberRead_ConstFromIndexAliasesTheElement pins
// AGENT-BRIEF step 4 LANDED: `const p = xs[i]` over a flattened
// record-elemented array parameter now serves `p.a` — complete, not
// porous.
//
// THE DESIGN LANDED (the choice the pre-existing caveat comment above
// this test asked whoever built step 4 to make explicit): slot ALIASING,
// resolved at LOOKUP time, not through ObjectLocalKey/declarationLeavesOf
// (the route the object-literal/constructed/declared-type/joined-arm
// families all take). `p` is bound to the SAME element the array's own
// "xs.elem.<member>" slots already carry — it is not a copy, so no new
// slot is ever allocated for it. Two seams do the work, both in
// ir_object_slots_slot_index.go (this agent's territory):
//
//  1. PathSlotIndexOf, on a plain one-step path whose ordinary lookup
//     fails ("p.a" was never laid out anywhere), tries
//     elementAliasSlotIndexOf: resolve p's OWN declaration through the
//     checker (symbolAt on the read site's root identifier — the
//     alias-following reach AGENT-BRIEF's own facts section names), and
//     where that declaration is an ADMITTED alias of a record-elemented
//     flattened array parameter, redirect to THAT array's own
//     "xs.elem.<member>" slot index. "p.a" and "xs.elem.a" therefore
//     resolve to the literal same index — one slot, two spellings.
//  2. ObjectDeclarationAssignmentsOf, on the declaration statement
//     itself, recognizes the same admitted alias and answers (nil, true)
//     — HANDLED with zero assignments, so the statement lowers to
//     nothing rather than falling to the havoc floor (which would
//     otherwise havoc a whole-name slot p never has and mark the body
//     porous at "declaration" for a statement that moves no state).
//
// The alias is admitted (elementAliasTargetOf, memoized per declaration
// node) only where: the declaration is `const` (NodeFlagsConst on the
// VariableDeclarationList — a `let` declines outright, AGENT-BRIEF rule
// 4); the initializer is a PLAIN (non-optional) ElementAccessExpression
// directly on a plain identifier resolving to a flattened array
// PARAMETER with ElementMembers; and — reusing
// usesAreAllDeclaredKeySteps, the SAME use-scan the object-literal family
// already runs — EVERY use of p in the enclosing function body is a
// declared-member path step. A hand-over (`f(p)`), a bare `return p`, or
// any occurrence usesAreAllDeclaredKeySteps does not recognize declines
// the WHOLE aliasing, exactly as it already declines an ordinary
// object-literal local's whole-name escape.
//
// Element-leaf WRITES (`xs[i].a = v`, `p.a = v`) joining INTO the slot
// rather than replacing it are the family's write-side counterpart and
// are UNBUILT here (out of this file's current build; a write through
// either spelling must DECLINE, never replace, until it lands — no write
// route exists yet for either spelling, so both simply fall through to
// whatever the pre-existing routes already do with an unrecognized
// target, which is a decline, never a replace).
func TestArrayElementMemberRead_ConstFromIndexAliasesTheElement(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	ClearElementAliasedLocals()
	source := "function f(xs: { a: number, b: number }[], i: number) { const p = xs[i]; return p.a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Fatalf("const p = xs[i]; return p.a: outcome=%q construct=%q, want complete", outcome, construct)
	}
}

// TestArrayElementMemberRead_ConstFromIndexBothMembersAlias pins the
// two-member half of the same dependency-ordered pin list AGENT-BRIEF
// names: `p.a + p.b` both resolve through the alias, not just the first
// member a fixture happens to read — the same guarantee
// TestArrayElementMemberRead_TwoMemberBodyBothServe already pins for the
// direct `xs[i].a` spelling.
func TestArrayElementMemberRead_ConstFromIndexBothMembersAlias(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	ClearElementAliasedLocals()
	source := "function f(xs: { a: number, b: number }[], i: number) { const p = xs[i]; return p.a + p.b; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome=%q construct=%q, want complete — both p.a and p.b should resolve through the alias", outcome, construct)
	}
}

// TestArrayElementMemberRead_HandedOverAliasDeclines pins AGENT-BRIEF
// step 4's rule 3: a hand-over of the aliased local (`f(p)`) is a use
// usesAreAllDeclaredKeySteps does not recognize as a declared-member
// step, so the WHOLE aliasing declines — p never resolves through
// elementAliasSlotIndexOf at all, and the body stays porous exactly as
// an object-literal local escaping whole already does. `g` is an
// otherwise-unresolvable callee (no declaration in this source), so the
// call itself also declines on its own account; what this pins is that
// the READ `p.a` on the line ABOVE the hand-over is not served either —
// the alias is all-or-nothing over the whole body, not per-occurrence.
func TestArrayElementMemberRead_HandedOverAliasDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	ClearElementAliasedLocals()
	source := "function f(xs: { a: number, b: number }[], i: number) { const p = xs[i]; g(p); return p.a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome=%q construct=%q — a hand-over of p (g(p)) served as complete; the alias must decline whole where any use escapes the declared-member-step scan", outcome, construct)
	}
	t.Logf("const p = xs[i]; g(p); return p.a: outcome=%q construct=%q (expected porous — the hand-over declines the whole alias)", outcome, construct)
}

// TestArrayElementMemberRead_ReassignedAliasDeclines pins AGENT-BRIEF
// step 4's rule 4: `let p = xs[i]; p = xs[j];` declines outright — a
// reassignable alias is never admitted at all, since
// aliasCandidateDeclarationOf gates on NodeFlagsConst before any use scan
// runs.
func TestArrayElementMemberRead_ReassignedAliasDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	ClearElementAliasedLocals()
	source := "function f(xs: { a: number, b: number }[], i: number, j: number) { let p = xs[i]; p = xs[j]; return p.a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome=%q construct=%q — a let-bound, reassigned alias served as complete; a reassignable binding must never admit the aliasing", outcome, construct)
	}
	t.Logf("let p = xs[i]; p = xs[j]; return p.a: outcome=%q construct=%q (expected porous — reassignment declines the alias outright)", outcome, construct)
}

// TestArrayElementMemberRead_WriteThroughAliasDeclines pins the write-side
// half of AGENT-BRIEF step 2's own soundness warning: `p.a = 5` must NOT
// serve, because IndexOf resolves a write TARGET through the identical
// seam a read uses (ir_lowering_context.go's IndexOf, shared by every
// route in this package) — a write through the alias spelling would
// REPLACE the slot AssignmentOfExpression's ordinary `=` route builds,
// not JOIN into it, which would corrupt what every OTHER element's own
// read through "xs[j].a" is allowed to assume about the array's
// weak-summary elem slot. No join-write route is built for either
// spelling ("p.a = v" or "xs[i].a = v") in this file, so
// elementAliasHasWriteThrough refuses the WHOLE aliasing wherever the
// body writes through the alias name at all — the honest porous answer,
// never a silent unsound replace.
func TestArrayElementMemberRead_WriteThroughAliasDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	ClearElementAliasedLocals()
	source := "function f(xs: { a: number, b: number }[], i: number) { const p = xs[i]; p.a = 5; return p.a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome=%q construct=%q — p.a = 5 served as complete; a write through the alias spelling must decline the whole aliasing (no join route exists), never replace the shared slot", outcome, construct)
	}
	t.Logf("const p = xs[i]; p.a = 5; return p.a: outcome=%q construct=%q (expected porous — the write-through declines the whole alias)", outcome, construct)
}

// TestArrayElementMemberRead_UndeclaredMemberDeclines pins that reading a
// member name the element record never declared (`xs[i].c`) does NOT
// spell a slot — arrayElementMemberLeafOf builds the spelling
// "xs.elem.c" unconditionally from syntax, and it is slotIndexOfName's
// own lookup against the laid-out bodySlots that must refuse it, since no
// "xs.elem.c" entry was ever laid out for a two-member {a,b} record.
func TestArrayElementMemberRead_UndeclaredMemberDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	source := "function f(xs: { a: number, b: number }[], i: number) { return xs[i].c; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome=%q construct=%q — xs[i].c read as complete, but `c` is not a declared member of {a: number, b: number}; this would be a WRONG answer, not merely weak", outcome, construct)
	}
}

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

// TestArrayElementMemberRead_ConstFromIndexThenMemberReadStaysPorous pins
// AGENT-BRIEF step 4's OWN premise: `const p = xs[i]` over a flattened
// record-elemented array does NOT yet seed p's member leaves. Declines at
// construct "declaration" — the declaration recognizer itself, before
// `p.a` is ever reached.
//
// The hook this names precisely, for whoever lands the write side: the
// family source is ObjectLocalIn's own `declarationLeavesOf`
// (ir_object_slots_recognizer.go, NOT this agent's territory — the array
// declaration-lowering file, ir_array_declarations.go, IS, but a
// DECLARATION whose initializer is `xs[i]` produces a RECORD-SHAPED local
// via the OBJECT-slot family mechanism, not the array-slot one, so the
// seeding is a fifth `declarationLeavesOf` source alongside the literal/
// constructed/declared-type/joined-arm ones already there). The new
// source: a declaration `const p = xs[i]` where `xs` is a flattened array
// whose ElementMembers is non-nil (ArrayLocal.ElementMembers, this
// agent's step 1) answers ONE ObjectLocalKey per member, each key's
// SlotName reading "p.<member>" as any other object-local leaf would, but
// with NO Initializer of its own to lower — the value comes from the
// elem-leaf JOIN, not from a literal row. The declaration-side LOWERING
// for those keys (ObjectLocalDeclarationAssignmentsOf's sibling route)
// would then write each "p.<member>" slot as a verbatim copy of the
// matching "xs.elem.<member>" slot — the same whole-state copy
// ArrayDeclarationAssignmentsOf's own COPY arm (`const b = [...a]`) already
// performs between two array locals' slot pairs, one member wide.
//
// Element-leaf WRITES (`xs[i].a = v`) joining INTO the slot rather than
// replacing it (the doc on ElementMembers states the rule) are the same
// family's write-side counterpart and are UNBUILT here for the same
// out-of-territory reason.
//
// CAVEAT ON THE COPY DESIGN ABOVE (added by the coordinating pass): the
// verbatim-copy lowering sketched above is UNSOUND as stated, because
// `const p = xs[i]` binds an ALIAS of the element object, not a copy of
// its members — a later write through the local (`p.a = 5`, exactly
// updateDepthOfTargets' `target.depth = …` shape) mutates the element
// the elem-leaf join must see, and a later element write (`xs[j].a =
// v`) lands in a value the local's copied slot no longer reflects, so
// the copied slot could keep claiming a set that excludes the written
// value. The sound shapes are (a) slot ALIASING — "p.<member>" resolves
// to the SAME slot index as "xs.elem.<member>" (two spellings, one
// slot), with writes through either spelling joining in — or (b) the
// copy as sketched, gated on a scan proving the body writes through
// neither the local nor any element of the source array. Whoever lands
// step 4 must pick one and say so here.
func TestArrayElementMemberRead_ConstFromIndexThenMemberReadStaysPorous(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearSummaryOutcomes()
	ClearResolvedArrayParameters()
	ClearResolvedRecordMembers()
	source := "function f(xs: { a: number, b: number }[], i: number) { const p = xs[i]; return p.a; }\n"
	ctx, p := namedTypeCtx(t, source)
	declaration := namedTypeFunction(t, p, "f")
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome == SummaryComplete {
		t.Errorf("outcome=%q construct=%q — this now serves; step 4 (const p = xs[i] seeding) must have landed, and this pin's own doc is stale", outcome, construct)
	}
	t.Logf("const p = xs[i]; return p.a: outcome=%q construct=%q (expected porous — step 4 unbuilt, see doc)", outcome, construct)
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

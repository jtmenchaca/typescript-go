// Pin fixtures for wholeRecordUseAt's arms — TASK 2 of the
// parameter-entries fix wave (AGENT-BRIEF). Written first against the
// arms that still refuse today (an assignment right side, an array
// element position, a property VALUE position, a spread call argument
// `f(...p)`), then widened to cover the SOUND arms this pass adds
// (equality/identity operands, a `typeof` operand) — both read the
// reference and produce a fresh value, storing nothing, the same
// argument the pre-existing truthiness arm already makes. The plain
// call/new ARGUMENT position (`f(p)`, no spread) is pinned too, as the
// control case the brief asks to check before assuming the ×26
// residual sits in argument positions at all: it already answers
// recordParameterHandedOver, not a refusal.
package walk

import (
	"testing"
)

// recordParameterUseFixture parses a throwaway function body and reads
// recordParameterUseOf's answer for parameter `p`'s own name against
// the given declared members — no checker needed, since
// recordParameterUseOf reads pure syntax over an already-computed
// member list.
func recordParameterUseFixture(t *testing.T, source string, members []recordParamMember) recordParameterUse {
	t.Helper()
	declaration := summaryDeclarationOf(t, source)
	fn := declaration.AsFunctionDeclaration()
	use, _ := recordParameterUseOf(fn.Body, "p", members)
	return use
}

func recordParameterUseTwoMembers() []recordParamMember {
	return []recordParamMember{
		{Key: "lo", Path: []string{"lo"}, SlotName: "p.lo", Sort: BindingKindNumber},
		{Key: "hi", Path: []string{"hi"}, SlotName: "p.hi", Sort: BindingKindNumber},
	}
}

func TestWholeRecordUseAt_AnAssignmentRightSideRefuses(t *testing.T) {
	use := recordParameterUseFixture(t,
		"function f(p: { lo: number, hi: number }) { const q = p; return q.lo; }",
		recordParameterUseTwoMembers())
	if use != recordParameterUnreadable {
		t.Errorf("`const q = p` classified as %v, want recordParameterUnreadable", use)
	}
}

func TestWholeRecordUseAt_AnArrayElementPositionRefuses(t *testing.T) {
	use := recordParameterUseFixture(t,
		"function f(p: { lo: number, hi: number }) { const xs = [p]; return xs.length; }",
		recordParameterUseTwoMembers())
	if use != recordParameterUnreadable {
		t.Errorf("`[p]` classified as %v, want recordParameterUnreadable", use)
	}
}

func TestWholeRecordUseAt_APropertyValuePositionRefuses(t *testing.T) {
	use := recordParameterUseFixture(t,
		"function f(p: { lo: number, hi: number }) { const wrap = { a: p }; return wrap.a; }",
		recordParameterUseTwoMembers())
	if use != recordParameterUnreadable {
		t.Errorf("`{ a: p }` classified as %v, want recordParameterUnreadable", use)
	}
}

func TestWholeRecordUseAt_ASpreadCallArgumentRefuses(t *testing.T) {
	use := recordParameterUseFixture(t,
		"function f(p: { lo: number, hi: number }) { g(...p); }",
		recordParameterUseTwoMembers())
	if use != recordParameterUnreadable {
		t.Errorf("`g(...p)` classified as %v, want recordParameterUnreadable", use)
	}
}

// TestWholeRecordUseAt_AStrictEqualityComparisonOperandReadsWhole pins
// the WIDENED arm this pass adds: `p === q` reads both operand values
// (IsStrictlyEqual, sec-isstrictlyequal, tmp/ecma262/spec.html) and
// answers a fresh boolean — neither operand is stored anywhere, so the
// position now classifies as recordParameterReadWhole rather than
// refusing.
func TestWholeRecordUseAt_AStrictEqualityComparisonOperandReadsWhole(t *testing.T) {
	use := recordParameterUseFixture(t,
		"function f(p: { lo: number, hi: number }, q: { lo: number, hi: number }) { if (p === q) { return 1; } return 0; }",
		recordParameterUseTwoMembers())
	if use != recordParameterReadWhole {
		t.Errorf("`p === q` classified as %v, want recordParameterReadWhole", use)
	}
}

// TestWholeRecordUseAt_ALooseNullCheckComparisonOperandReadsWhole pins
// the loose-equality twin (`p == null`) — the abstract equality
// comparison (sec-abstract-equality-comparison) reads both operands
// and answers a fresh boolean the same way strict equality does.
func TestWholeRecordUseAt_ALooseNullCheckComparisonOperandReadsWhole(t *testing.T) {
	use := recordParameterUseFixture(t,
		"function f(p: { lo: number, hi: number } | undefined) { if (p == null) { return 1; } return p.lo; }",
		recordParameterUseTwoMembers())
	if use != recordParameterReadWhole {
		t.Errorf("`p == null` classified as %v, want recordParameterReadWhole", use)
	}
}

// TestWholeRecordUseAt_ATypeofOperandReadsWhole pins the `typeof`
// widening: the typeof operator (sec-typeof-operator) reads the
// operand's value and answers a fresh string tag; the operand itself
// never escapes.
func TestWholeRecordUseAt_ATypeofOperandReadsWhole(t *testing.T) {
	use := recordParameterUseFixture(t,
		"function f(p: { lo: number, hi: number }) { if (typeof p === 'object') { return 1; } return 0; }",
		recordParameterUseTwoMembers())
	if use != recordParameterReadWhole {
		t.Errorf("`typeof p` classified as %v, want recordParameterReadWhole", use)
	}
}

// TestWholeRecordUseAt_APlainCallArgumentAlreadyHandsOver is the
// CONTROL case: a bare call argument (no spread) already answers
// recordParameterHandedOver, not a refusal — verified here before
// assuming the ×26 residual sits in argument positions at all.
func TestWholeRecordUseAt_APlainCallArgumentAlreadyHandsOver(t *testing.T) {
	use := recordParameterUseFixture(t,
		"function f(p: { lo: number, hi: number }) { g(p); }",
		recordParameterUseTwoMembers())
	if use != recordParameterHandedOver {
		t.Errorf("`g(p)` classified as %v, want recordParameterHandedOver — call-argument positions already serve; the residual is elsewhere", use)
	}
}

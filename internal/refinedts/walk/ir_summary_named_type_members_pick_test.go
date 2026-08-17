// Pins for a Pick<T, K> parameter annotation — namedTypeMembersOf reads
// a type reference named "Pick" via pickMembersOf, expanding to the
// picked keys' own members (or an unknown-sorted MayBeAbsent leaf where
// no expandable source arm names the key).
package walk

import (
	"testing"
)

func TestNamedTypeMembersOf_PickOverAPlainInterfaceExpandsThePickedKeysOnly(t *testing.T) {
	ClearResolvedRecordMembers()
	p := entryEnvTestProgram(t, `
interface Base { lo: number; hi: string; extra: boolean; }
function f(p: Pick<Base, 'lo' | 'hi'>) { return p.lo; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, ok := SummaryParameterEntriesIn(ctx, parameter)
	if !ok {
		t.Fatalf("Pick<Base, 'lo' | 'hi'> declined the expansion — members: %+v", members)
	}
	if len(members) != 2 {
		t.Fatalf("len(members) = %d, want 2 (lo, hi — not extra): %+v", len(members), members)
	}
	wantName := []string{"p.lo", "p.hi"}
	wantSort := []BindingKind{BindingKindNumber, BindingKindString}
	for i, m := range members {
		if m.Name != wantName[i] {
			t.Errorf("member %d name = %q, want %q", i, m.Name, wantName[i])
		}
		if m.Sort != wantSort[i] {
			t.Errorf("member %d sort = %q, want %q", i, m.Sort, wantSort[i])
		}
	}
}

func TestNamedTypeMembersOf_AnAliasOfPickExpandsTheSameWay(t *testing.T) {
	ClearResolvedRecordMembers()
	p := entryEnvTestProgram(t, `
interface Base { lo: number; hi: string; extra: boolean; }
type Picked = Pick<Base, 'lo' | 'hi'>;
function f(p: Picked) { return p.lo; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, ok := SummaryParameterEntriesIn(ctx, parameter)
	if !ok {
		t.Fatalf("alias of Pick<Base, 'lo' | 'hi'> declined the expansion — members: %+v", members)
	}
	if len(members) != 2 {
		t.Fatalf("len(members) = %d, want 2 (lo, hi — not extra): %+v", len(members), members)
	}
}

func TestNamedTypeMembersOf_PickOverAnIntersectionWithOneUnexpandableArmStillExpandsFromTheOther(t *testing.T) {
	ClearResolvedRecordMembers()
	p := entryEnvTestProgram(t, `
interface HasLo { lo: number; }
type Combined = HasLo & { hi: string; };
function f(p: Pick<Combined, 'lo' | 'hi'>) { return p.lo; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, ok := SummaryParameterEntriesIn(ctx, parameter)
	if !ok {
		t.Fatalf("Pick over a readable intersection declined the expansion — members: %+v", members)
	}
	if len(members) != 2 {
		t.Fatalf("len(members) = %d, want 2 (lo, hi): %+v", len(members), members)
	}
}

func TestNamedTypeMembersOf_APickedKeyFoundInNoExpandableArmContributesAnUnknownMayBeAbsentLeaf(t *testing.T) {
	ClearResolvedRecordMembers()
	// Combined's intersection has a class arm (unexpandable) alongside
	// the readable HasLo arm — 'ghost' is picked but named by NEITHER
	// arm's readable member list, so it can only ever contribute
	// unknown-sorted MayBeAbsent, never a refusal of the whole Pick.
	p := entryEnvTestProgram(t, `
interface HasLo { lo: number; }
class Unexpandable { ghost: number = 0; }
type Combined = HasLo & Unexpandable;
function f(p: Pick<Combined, 'lo' | 'ghost'>) { return p.lo; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, ok := SummaryParameterEntriesIn(ctx, parameter)
	if !ok {
		t.Fatalf("Pick with an unexpandable arm declined the whole expansion — members: %+v", members)
	}
	if len(members) != 2 {
		t.Fatalf("len(members) = %d, want 2 (lo, ghost): %+v", len(members), members)
	}
	wantName := []string{"p.lo", "p.ghost"}
	wantSort := []BindingKind{BindingKindNumber, BindingKindUnknown}
	wantTag := []TypeofTag{TypeofTagNumber, TypeofTagNone}
	for i, m := range members {
		if m.Name != wantName[i] {
			t.Errorf("member %d name = %q, want %q", i, m.Name, wantName[i])
		}
		if m.Sort != wantSort[i] {
			t.Errorf("member %d sort = %q, want %q", i, m.Sort, wantSort[i])
		}
		if m.TypeofTag != wantTag[i] {
			t.Errorf("member %d typeof = %q, want %q", i, m.TypeofTag, wantTag[i])
		}
	}
}

func TestNamedTypeMembersOf_PickOverAGenericOmitIntersectionSkipsTheOmitArm(t *testing.T) {
	ClearResolvedRecordMembers()
	// Text.tsx's own shape (tmp/recharts-src/src/component/Text.tsx:35,
	// 187, 339): Props = Omit<SVGProps<SVGTextElement>, '...'> & TextProps
	// — the Omit arm is a type reference carrying type arguments, so
	// pickSourceMembersOf's intersection arm skips it (the SAME skip a
	// class arm takes, on a different unexpandable shape); the plain
	// TextProps arm still supplies every picked key.
	p := entryEnvTestProgram(t, `
interface SVGProps { textAnchor: string; fill: string; }
type OmitLike<T, K extends string> = T;
interface TextProps { children: string; breakAll: boolean; style: number; }
type Props = OmitLike<SVGProps, 'textAnchor'> & TextProps;
function f(p: Pick<Props, 'children' | 'breakAll' | 'style'>) { return p.children; }
`)
	declaration := entryEnvFunctionNamed(t, p, "f")
	parameter := declaration.Parameters()[0]
	ctx := &FlowContext{P: p}
	members, ok := SummaryParameterEntriesIn(ctx, parameter)
	if !ok {
		t.Fatalf("Pick over Omit<...> & TextProps declined the expansion — members: %+v", members)
	}
	if len(members) != 3 {
		t.Fatalf("len(members) = %d, want 3 (children, breakAll, style): %+v", len(members), members)
	}
	// booleans ride the number sort — their typeof (TypeofTagBoolean)
	// carries the distinction (declaredParamSort's own rule).
	wantName := []string{"p.children", "p.breakAll", "p.style"}
	wantSort := []BindingKind{BindingKindString, BindingKindNumber, BindingKindNumber}
	wantTag := []TypeofTag{TypeofTagString, TypeofTagBoolean, TypeofTagNumber}
	for i, m := range members {
		if m.Name != wantName[i] {
			t.Errorf("member %d name = %q, want %q", i, m.Name, wantName[i])
		}
		if m.Sort != wantSort[i] {
			t.Errorf("member %d sort = %q, want %q", i, m.Sort, wantSort[i])
		}
		if m.TypeofTag != wantTag[i] {
			t.Errorf("member %d typeof = %q, want %q", i, m.TypeofTag, wantTag[i])
		}
	}
}

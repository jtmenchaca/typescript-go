// split from ir_opaque_havoc_test.go — FirstHavoc and the construct's name

package walk

import (
	"testing"
)

/* ── FirstHavoc ──────────────────────────────────────────────────── */

func TestOpaqueHavoc_FirstHavocIsSetOnceByTheEarliestHavockedConstruct(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x", "m.size", "m.vals", "m.keys"},
		Sorts: []BindingKind{
			BindingKindNumber, BindingKindNumber, BindingKindNumber, BindingKindNumber,
		},
		Typeofs: []TypeofTag{
			TypeofTagNumber, TypeofTagNumber, TypeofTagNumber, TypeofTagNumber,
		},
	}
	if context.FirstHavoc != "" {
		t.Fatalf("FirstHavoc starts %q, want empty", context.FirstHavoc)
	}
	statements := havocParse(t, `
		fetchish(m);
		for (const k in m) { x **= 1; }
	`)
	if _, ok := OpaqueHavocStatements(context, statements[0]); !ok {
		t.Fatalf("the call statement declined")
	}
	if context.FirstHavoc != "call fetchish" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "call fetchish")
	}
	// the SECOND havoc leaves the name where it stands — first wins, so
	// the name always points at the earliest place the body stopped being
	// read whole
	if _, ok := OpaqueHavocStatements(context, statements[1]); !ok {
		t.Fatalf("the for-in declined")
	}
	if context.FirstHavoc != "call fetchish" {
		t.Errorf("FirstHavoc = %q after a second havoc, want it unchanged at %q",
			context.FirstHavoc, "call fetchish")
	}
}

func TestOpaqueHavoc_NoteFirstHavocIgnoresAnEmptyNameAndANilContext(t *testing.T) {
	context := &LoweringContext{}
	NoteFirstHavoc(context, "")
	if context.FirstHavoc != "" {
		t.Errorf("FirstHavoc = %q after an empty note, want empty", context.FirstHavoc)
	}
	NoteFirstHavoc(nil, "for-in") // must not panic
	NoteFirstHavoc(context, "for-in")
	if context.FirstHavoc != "for-in" {
		t.Errorf("FirstHavoc = %q, want %q", context.FirstHavoc, "for-in")
	}
}

func TestOpaqueHavoc_TheConstructNameSpellsTheSourceShape(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"x", "p.a"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	cases := []struct {
		source string
		want   string
	}{
		{"for (const k in p) { }", "for-in"},
		{"for await (const v of src) { }", "for await"},
		{"try { x **= 1; } finally { }", "try"},
		{"o[key] = p;", "computed member"},
		{"this.injector.load(p);", "call this.injector.load"},
	}
	for _, held := range cases {
		fresh := *context
		fresh.FirstHavoc = ""
		statements := havocParse(t, held.source)
		if _, ok := OpaqueHavocStatements(&fresh, statements[0]); !ok {
			t.Errorf("%q declined", held.source)
			continue
		}
		if fresh.FirstHavoc != held.want {
			t.Errorf("%q named %q, want %q", held.source, fresh.FirstHavoc, held.want)
		}
	}
}

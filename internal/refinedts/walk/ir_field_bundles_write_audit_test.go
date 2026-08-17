// THE WRITE AUDIT: what a body that STORES into one of its bundle's
// fields is allowed to believe afterward.
//
// The census reports a written field in Writes. The layout gives every
// READ field an entry and names the written ones among them
// (thisBundleLayout.Written). Two things must then hold, and neither is
// visible from the census alone:
//
//   - a field the body writes and LATER READS must answer from the
//     store, never from the entry the caller filled. A read answering
//     the entry is a wrong answer about the program's own state.
//   - a field the body writes must not leave the caller believing the
//     pre-call value, because the caller's object really did move.
//
// These probe the lowered IR directly rather than the census, because
// the census is only the report — the layout and the call sites are
// where a correctly-reported write can still be dropped.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

// bundleLayoutOf answers the this-bundle layout of a class's first
// method — the seam between the census and the slot vector.
func bundleLayoutOf(t *testing.T, source string) thisBundleLayout {
	t.Helper()
	statements := bundleParse(t, source)
	for _, member := range statements[0].AsClassDeclaration().Members.Nodes {
		if ast.IsMethodDeclaration(member) {
			return thisBundleOf(&FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}, member)
		}
	}
	t.Fatalf("no method in %q", source)
	return thisBundleLayout{}
}

// A field the body WRITES is named in Written, whether or not it also
// reads it. That naming is what a consumer keying on "did this slot
// move" has to read; a write missing from it is a slot believed across
// a store.
func TestWriteAudit_AWrittenFieldIsNamedInTheLayout(t *testing.T) {
	layout := bundleLayoutOf(t,
		"class C { count: number; m(n: number) { this.count = n; return this.count; } }")
	if layout.Escaped {
		t.Fatalf("the body escaped — nothing here reaches outside the bundle")
	}
	if _, named := layout.Written["this.count"]; !named {
		t.Errorf("Written = %v, want it to name this.count — the body stores into it", layout.Written)
	}
}

// The same through a DESTRUCTURING store, which is the shape the escape
// audit found unrecorded. The layout must name it the same way.
func TestWriteAudit_ADestructuredStoreIsNamedInTheLayout(t *testing.T) {
	layout := bundleLayoutOf(t,
		"class C { count: number; m(source: { x: number }) { ({ x: this.count } = source); return this.count; } }")
	if layout.Escaped {
		t.Fatalf("the body escaped unexpectedly: %+v", layout)
	}
	if _, named := layout.Written["this.count"]; !named {
		t.Errorf("Written = %v, want it to name this.count — the pattern stores into it", layout.Written)
	}
}

// A loop-bound store, likewise.
func TestWriteAudit_AForOfBoundStoreIsNamedInTheLayout(t *testing.T) {
	layout := bundleLayoutOf(t,
		"class C { count: number; m(xs: number[]) { for (this.count of xs) { } return this.count; } }")
	if layout.Escaped {
		t.Fatalf("the body escaped unexpectedly: %+v", layout)
	}
	if _, named := layout.Written["this.count"]; !named {
		t.Errorf("Written = %v, want it to name this.count — the loop stores into it once per iteration", layout.Written)
	}
}

// A COMPUTED STORE — `this[k] = v` — names no field, so nothing bounds
// WHICH slot moved. The declaration bounds the SET, so the honest
// answer is to expand the bundle with every field and mark them all
// written, bracketing the store with havocing.
//
// A computed store moves a field nothing names, so every field is havocked
// around code-running and element-storing statements and marked written.
func TestWriteAudit_AComputedStoreExpandsUnderBracketedHavocs(t *testing.T) {
	layout := bundleLayoutOf(t,
		"class C { count: number; other: number; m(k: string) { this[k] = 1; return this.count + this.other; } }")
	if layout.Escaped {
		t.Errorf("layout.Escaped = true, want false — the body stays in bundle")
	}
	if !layout.Expanded {
		t.Errorf("layout.Expanded = false, want true — the store expands with every field")
	}
	if !contains(layout.CaptureHavocNames, "this.count") || !contains(layout.CaptureHavocNames, "this.other") {
		t.Errorf("CaptureHavocNames = %v, want it to contain this.count and this.other", layout.CaptureHavocNames)
	}
	if !contains(layout.Written, "this.count") || !contains(layout.Written, "this.other") {
		t.Errorf("Written = %v, want it to contain this.count and this.other", layout.Written)
	}
}

// Helper to check if a map/slice contains a key/value
func contains(m interface{}, key string) bool {
	switch v := m.(type) {
	case map[string]struct{}:
		_, ok := v[key]
		return ok
	case map[string]bool:
		return v[key]
	case []string:
		for _, s := range v {
			if s == key {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// The computed READ is a different case and must NOT cost the bundle:
// `this[k]` reads some field without naming it, which loses nothing
// about the slots' values. Only the STORE moves state.
func TestWriteAudit_AComputedReadStillAllowsTheBundle(t *testing.T) {
	layout := bundleLayoutOf(t,
		"class C { count: number; m(k: string) { const x = this[k]; return this.count; } }")
	if layout.Escaped {
		t.Errorf("a computed READ escaped the bundle: %+v — reading an unnamed field moves nothing", layout)
	}
}

// A WRITE-ONLY field takes an entry AND is named written: the row is
// what the write-back rides, and without it the caller's own slot kept
// its stale value across a call that changed it. The entry is safe to
// lay out because every call site fills every this-entry — from the
// caller's slot of the same spelling, or with unknown where it holds
// none (bundleRetsAndArgs, accessorCallStatement) — so no entry ever
// takes another field's value. The literal arm stated this first
// (literalThisBundleOf); the class arm now lays out the same rows.
func TestWriteAudit_AWriteOnlyFieldTakesAnEntryAndARow(t *testing.T) {
	layout := bundleLayoutOf(t,
		"class C { count: number; other: number; m(n: number) { this.count = n; return this.other; } }")
	if layout.Escaped {
		t.Fatalf("the body escaped unexpectedly: %+v", layout)
	}
	held := false
	for _, entry := range layout.Entries {
		if entry.Name == "this.count" {
			held = true
		}
	}
	if !held {
		t.Errorf("entries = %+v — the write-only field has no row for its write-back to ride", layout.Entries)
	}
	if _, named := layout.Written["this.count"]; !named {
		t.Errorf("Written = %v, want it to name the write-only field", layout.Written)
	}
}

// A SETTER-BACKED store (`this.age = n` where the class declares `set
// age`) folds the setter's own census into the layout: the setter's
// written backing field takes an entry and a written row in the METHOD's
// layout, so the embedded setter call statement has a slot to thread its
// write-back onto — the e-411 privateSetterWrite shape.
func TestWriteAudit_ASetterBackedStoreLaysOutTheBackingField(t *testing.T) {
	layout := bundleLayoutOf(t,
		"class C { #held = 0; set age(n: number) { this.#held = n; } m(n: number) { this.age = n; } }")
	if layout.Escaped {
		t.Fatalf("the body escaped — the accessor fold must resolve `set age` instead: %+v", layout)
	}
	held := false
	for _, entry := range layout.Entries {
		if entry.Name == "this.#held" {
			held = true
		}
	}
	if !held {
		t.Errorf("entries = %+v — the setter's backing field has no slot", layout.Entries)
	}
	if _, named := layout.Written["this.#held"]; !named {
		t.Errorf("Written = %v, want it to name this.#held — the setter's body stores into it", layout.Written)
	}
}

// A store through a name that resolves to NO accessor of the class
// keeps the refusal the escape used to make.
func TestWriteAudit_AStoreIntoANeverDeclaredMemberStaysEscaped(t *testing.T) {
	layout := bundleLayoutOf(t,
		"class C { count: number; m(n: number) { (this as any).ghost = n; } }")
	if !layout.Escaped {
		t.Errorf("layout = %+v, want Escaped — nothing declares ghost, as field or accessor", layout)
	}
}

// The census itself DEFERS an undeclared store instead of escaping: the
// name is reported in AccessorStores and Believable refuses it, so every
// consumer without the fold keeps exactly the old refusal.
func TestWriteAudit_TheCensusDefersAnUndeclaredStoreToItsConsumer(t *testing.T) {
	statements := bundleParse(t,
		"class C { #held = 0; set age(n: number) { this.#held = n; } m(n: number) { this.age = n; } }")
	class := statements[0].AsClassDeclaration()
	var body *ast.Node
	for _, member := range class.Members.Nodes {
		if ast.IsMethodDeclaration(member) {
			body = member.Body()
		}
	}
	if body == nil {
		t.Fatalf("no method body")
	}
	fields, _ := ClassFieldsOf(nil, statements[0])
	census := FieldCensusOf(body, "this", BundleFieldsAs("this", fields))
	if census.Escapes {
		t.Fatalf("census = %+v, want the store deferred, not escaped", census)
	}
	if !contains(census.AccessorStores, "age") {
		t.Errorf("AccessorStores = %v, want it to name age", census.AccessorStores)
	}
	if census.Believable() {
		t.Errorf("Believable() = true — a deferred accessor store must refuse every consumer without the fold")
	}
}

package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// mapKnown builds a KindCollection Map AbstractValue over the given
// entries — the fixture shape MapPresenceNarrowings reads via `held`.
func mapKnown(entries ...abstractdomain.CollectionEntry) abstractdomain.AbstractValue {
	return abstractdomain.AbstractValue{
		Kind:             abstractdomain.KindCollection,
		CollectionFlavor: abstractdomain.FlavorMap,
		Entries:          entries,
		Complete:         false,
	}
}

func stringKeyOf(text string) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues(refinementsets.CodepointsOf(text), abstractdomain.PrimitiveString, abstractdomain.TrustProved)
}

func TestMapPresenceNarrowings_HeldHasAddsTheMissingEntry(t *testing.T) {
	c, file := checkerFor(t, `
function f(m: Map<string, number>) {
  if (m.has("k")) {
    return 0;
  }
  return 1;
}
`)
	_ = c
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	env := map[string]abstractdomain.AbstractValue{"m": mapKnown()}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := MapPresenceNarrowings(held, condition, false)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 — a held m.has(\"k\") proves the key present", len(rows))
	}
	if rows[0].Binding != "m" {
		t.Fatalf("rows[0].Binding = %q, want \"m\"", rows[0].Binding)
	}
	found := false
	for _, entry := range rows[0].Known.Entries {
		if abstractdomain.SameKnown(entry.Key, stringKeyOf("k")) {
			found = true
		}
	}
	if !found {
		t.Errorf("rows[0].Known.Entries = %+v, want an entry keyed \"k\"", rows[0].Known.Entries)
	}
}

func TestMapPresenceNarrowings_ExitGuardReadsTheRefutedSideAsHeld(t *testing.T) {
	// the A8.guard.exit shape: `if (!m.has("k")) return 0;` — the
	// continuation after the exit runs where the ORIGINAL condition is
	// FALSE, i.e. m.has("k") is true, exactly like
	// LengthGuardNarrowings' own exit-guard call (negated=true).
	c, file := checkerFor(t, `
function f(m: Map<string, number>) {
  if (!m.has("k")) {
    return 0;
  }
  return 1;
}
`)
	_ = c
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	env := map[string]abstractdomain.AbstractValue{"m": mapKnown()}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := MapPresenceNarrowings(held, condition, true)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 — refuting !m.has(\"k\") proves the key present", len(rows))
	}
}

func TestMapPresenceNarrowings_AlreadyNamedKeyAddsNoRow(t *testing.T) {
	c, file := checkerFor(t, `
function f(m: Map<string, number>) {
  if (m.has("k")) {
    return 0;
  }
  return 1;
}
`)
	_ = c
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	// the key is already named — a sharper held value must not be
	// re-widened back to Unknown by a guard that only reconfirms it
	sharperValue := abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	env := map[string]abstractdomain.AbstractValue{
		"m": mapKnown(abstractdomain.CollectionEntry{Key: stringKeyOf("k"), Value: sharperValue}),
	}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := MapPresenceNarrowings(held, condition, false)
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 — an already-named key adds nothing, got %+v", len(rows), rows)
	}
}

func TestMapPresenceNarrowings_NonLiteralKeyIsNotRead(t *testing.T) {
	c, file := checkerFor(t, `
function f(m: Map<string, number>, k: string) {
  if (m.has(k)) {
    return 0;
  }
  return 1;
}
`)
	_ = c
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	env := map[string]abstractdomain.AbstractValue{"m": mapKnown()}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	// a symbolic key finds no entry back by SameKnown either (collectionKey's
	// own exact-value gate in walk/collection_models.go), so a guard naming
	// one here would prove a fact no later read could ever match
	rows := MapPresenceNarrowings(held, condition, false)
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 — a non-literal key is not read, got %+v", len(rows), rows)
	}
}

func TestMapPresenceNarrowings_NonMapReceiverIsIgnored(t *testing.T) {
	c, file := checkerFor(t, `
function f(s: Set<string>) {
  if (s.has("k")) {
    return 0;
  }
  return 1;
}
`)
	_ = c
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	env := map[string]abstractdomain.AbstractValue{
		"s": {Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorSet},
	}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := MapPresenceNarrowings(held, condition, false)
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 — a Set has no entry VALUE, so its presence carries nothing get() reads back", len(rows))
	}
}

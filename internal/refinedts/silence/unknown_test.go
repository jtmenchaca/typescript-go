// A bare UNKNOWN birth outside the allowlist is a suite failure in
// the TS source (a whole-tree scan for `return UNKNOWN` and for
// UNKNOWN imports outside silence/lattice/the atom). That scan reads
// TS source text and has no Go-shaped twin yet — Go's Unknown/Opaque
// are package-level abstractdomain vars constructible from any
// importer, not a grep target, and there is no established Go
// convention yet for an equivalent allowlist. This file ports the
// functional half instead: each of the three constructors returns
// exactly the unknown atom.

package silence

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

func isUnknownAtom(v abstractdomain.AbstractValue) bool {
	return v.Kind == abstractdomain.KindUnknown && !v.Opaque
}

func TestResidue_ReturnsUnknown(t *testing.T) {
	if !isUnknownAtom(Residue()) {
		t.Fatalf("expected Unknown, got %+v", Residue())
	}
}

func TestCutUnknown_ReturnsUnknown(t *testing.T) {
	if !isUnknownAtom(CutUnknown()) {
		t.Fatalf("expected Unknown, got %+v", CutUnknown())
	}
}

func TestDissolveUnknown_ReturnsUnknown(t *testing.T) {
	if !isUnknownAtom(DissolveUnknown()) {
		t.Fatalf("expected Unknown, got %+v", DissolveUnknown())
	}
}

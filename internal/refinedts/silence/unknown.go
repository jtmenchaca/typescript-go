// THE walk-unknown constructors. A plain unknown is born here, not
// by naming the lattice atom. Cuts, residue, and dissolve are the
// same runtime value with different names at the call site — that is
// what makes the ledger enumerable.
//
// residue: the model or transfer declined; do not seed.
// cutUnknown: induction seed; do not seed (PV2-020/021).
// dissolveUnknown: lattice / unknown operand; do not seed.
// afterReaders (after_readers.ts): seed from host type when allowed.
// OPAQUE stays the opaque constructor on abstract_value.

package silence

import "github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"

// Residue is residue in the TS source.
func Residue() abstractdomain.AbstractValue {
	return abstractdomain.Unknown
}

// ResidueOf is Residue with the reader's own sentence attached: the
// same unknown, naming why the position is unknown rather than
// leaving it bare. reason is the plain sentence a call site has
// already composed about its own decline — pass the same wording the
// site already writes elsewhere (a NoteReason call, a code comment),
// never a new category name. Every existing Residue() call site stays
// bare; this is additive, for call sites that convert one at a time.
func ResidueOf(reason string) abstractdomain.AbstractValue {
	return abstractdomain.AbstractValue{Kind: abstractdomain.KindUnknown, ResidueReason: reason}
}

// CutUnknown is cutUnknown in the TS source.
func CutUnknown() abstractdomain.AbstractValue {
	return abstractdomain.Unknown
}

// DissolveUnknown is dissolveUnknown in the TS source.
func DissolveUnknown() abstractdomain.AbstractValue {
	return abstractdomain.Unknown
}

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
//
// BLOCKED: the TS twin (unknown.ts) is three zero-argument functions
// — residue, cutUnknown, dissolveUnknown — that each return exactly
// UNKNOWN, and UNKNOWN itself is a value of the AbstractValue union
// (abstract_domain/abstract_value.ts, kind "unknown"). AbstractValue
// is not yet ported to Go (in flight concurrently per
// go-port-tracker.md); this file intentionally carries no Go
// declarations yet rather than inventing a placeholder AbstractValue
// type ahead of that port landing. Per PORT.md, the independent-parts
// rule does not apply when there are none: every branch of this file
// returns the same AbstractValue-typed constant, so there is no
// fragment that stands independent of that type.
//
// Port residue, cutUnknown, and dissolveUnknown here once
// internal/refinedts/abstractdomain exports AbstractValue and its
// UNKNOWN constant to return.
package silence

// The FIELD CENSUS: what an object with a declared shape is worth as a
// bundle of slots, and what one body does with it.
//
// Wave 4 makes every object with a declared shape a slot bundle — a
// method's `this`, a class/interface-typed parameter. Two seams read
// that bundle: the LAYOUT, which decides how many entry slots the body
// gets and what each one is sorted as, and the CALL SITES, which fill
// those entries from the caller's own knowledge and map written fields
// back out. THE CONTRACT OF THIS FILE IS THAT BOTH READ IT. A layout
// built from one reading of a class's fields and a call site built from
// another would disagree about the slot vector's length or order, and
// the apply side would fill entry k from a caller value belonging to
// entry j — a silent unsoundness with no syntax to point at. So the
// field set (ClassFieldsOf / BundleTypeFieldsOf) and the per-body use
// report (FieldCensusOf) live here, are deterministic, read only syntax
// plus the checker's symbol resolution, and answer in declaration order.
//
// The census REPORTS shape; it never decides policy. A field with an
// annotation nothing can sort still appears, wearing an unknown sort; a
// computed member access and an escaping mention are reported as flags,
// not as declines. Whether an unknown-sorted field can carry an entry,
// whether a computed access havocs the bundle or refuses the body — the
// consumer rules on that, from one report.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

/* ── the per-body use report ─────────────────────────────────────── */

// FieldCensus is what ONE body does with ONE bundle: which declared
// fields it reads, which it writes, and the two shapes that put the
// whole bundle out of the lowering's sight.
//
// Reads and Writes hold the fields themselves — spelled and sorted as
// the declaration gave them — in the declaration order of the field set
// the census was asked about, never in the order the body happens to
// mention them. That order is what makes two readings of one census
// produce the same slot vector.
//
// A written field appears in BOTH lists when the body also reads it; a
// write-only field appears only in Writes. The layout gives every field
// in either list a slot; the call site maps the Writes back out through
// rets, the way a return value rides.
//
// Computed is any `receiver[e]` element access on the receiver, read or
// written: the expression names no field.
//
// ComputedWrite is the half of that which MOVES state — `this[k] = v`,
// `this[k]++`, `delete this[k]`. The two are separate because they cost
// different things. A computed READ names no field but changes nothing,
// so every slot keeps its value and the bundle is worth exactly what it
// was. A computed STORE moves a slot nothing names, so no slot of this
// receiver can be believed past it; the DECLARATION still bounds the
// set, so a consumer can havoc every slot of this one object rather than
// declining outright.
//
// Escapes is any occurrence of the receiver the lowering cannot follow:
// a bare mention (`f(this)`, `return wrapper`, `xs.push(this)`), an
// alias (`const w = wrapper`), an optional-chained or non-declared
// member. A bundle that escapes may be written through a name the census
// never saw, so its slots cannot be trusted past that point.
type FieldCensus struct {
	Reads         []BundleField
	Writes        []BundleField
	Computed      bool
	ComputedWrite bool
	Escapes       bool
	// CapturedMethodCalls: the receiver METHODS a nested closure calls
	// (`xs.forEach(x => this.insert(x))`). Such a capture is not an
	// escape by itself — the closure may run at any later time, so the
	// consumer must treat every field those methods can write
	// (transitively) as movable at EVERY call statement in the body,
	// and refuse the bundle when that write set cannot be computed.
	// Deduplicated, in first-mention order.
	CapturedMethodCalls []string
	// DirectMethodCalls: the receiver methods the body calls in plain
	// statement position (`this.register(x)`, and `this[S](…)` where the
	// class declares S as a method, under its `#sym:` spelling). These
	// lower as real call statements and need nothing from the census —
	// the field exists for CaptureWriteSet's closure, where a captured
	// method's own direct calls carry its transitive writes.
	DirectMethodCalls []string
	// ReturnsSelf: the body ends `return this` (this-receivers only —
	// the fluent-builder shape). Not an escape: nothing moves during
	// the body. The CALLER gains an alias it may write through later,
	// so the serving seams must forget the caller's receiver knowledge
	// (the direct route) or decline the composed call (the statement
	// route) — LoweredSummary.ReturnsReceiver carries the requirement.
	ReturnsSelf bool
	// AccessorStores / AccessorReads: receiver members the body stores
	// into / reads that the field set never DECLARED as fields — the
	// spelling a get/set ACCESSOR is used by (`this.#age = v` where the
	// class declares `set #age`). The census cannot see the class, so it
	// reports the names instead of ruling; the one consumer that can
	// resolve them to accessor declarations (thisBundleOf, through
	// accessorCensusFold) folds those bodies' own censuses in, and every
	// consumer without that machinery refuses through Believable —
	// exactly the escape these occurrences used to be. Deduplicated, in
	// first-mention order.
	AccessorStores []string
	AccessorReads  []string
}

// FieldCensusOf scans a body for what it does with `receiverName` —
// "this" for a method's own receiver (ThisKeyword receivers are
// recognized under that spelling), or a parameter's identifier for a
// class/interface-typed parameter.
//
// The four dimensions, as implemented:
//
//   - a READ is `<receiver>.<field>` in any position that is not a write
//     target. `this.injector.load(…)` is the read of injector — the
//     receiver of a method call is a field value like any other — while
//     `load` is a METHOD name in callee position, which resolves through
//     its own summary rather than a slot, so an undeclared name there
//     contributes nothing and is NOT an escape (a declared field called
//     as a function still reads that field).
//   - a WRITE is `<receiver>.<field>` as an assignment target (plain or
//     compound), the operand of `++`/`--`, or the operand of `delete`. A
//     compound write and an update also READ, so both lists get the field.
//   - COMPUTED is `<receiver>[e]` — an element access whose own receiver
//     is this receiver AND whose key is not a stable symbol const. When
//     that access is a STORE position rather than a read, ComputedWrite
//     is set too: a read names no field and moves nothing, while a store
//     moves a slot nothing names.
//   - a SYMBOL-KEYED access (`this[INSTANCE_ID_SYMBOL]`) is NOT computed:
//     the key identity names one field, so the access reads or writes the
//     `#sym:` field exactly as a dotted step reads or writes its own. It
//     needs a checker to resolve the const, so it is FieldCensusWith's
//     arm and the checker-less FieldCensusOf keeps reporting Computed.
//   - an ESCAPE is every other occurrence of the receiver: a bare
//     identifier or `this` that is not the receiver of one of the above,
//     an optional chain (`this?.x` — its receiver may be absent, which no
//     slot spells), and a member the field set never declared (no slot
//     holds it, and reading it would answer another slot's state).
//
// The scan never enters a NESTED function: a closure runs at a time this
// scan cannot place, so a field it touches is not this body's read or
// write. A receiver mention inside one is an ESCAPE — the closure carries
// the bundle out of the lowering's sight.
//
// Nothing here declines. A body that escapes its receiver and computes
// into it reports both flags with whatever reads and writes it also did;
// the consumer rules on what that is worth.
func FieldCensusOf(body *ast.Node, receiverName string, fields []BundleField) FieldCensus {
	return FieldCensusWith(nil, body, receiverName, fields)
}

// FieldCensusWith is FieldCensusOf carrying the checker the STABLE
// SYMBOL KEY needs. With a checker, `<receiver>[S]` where S resolves to
// a module-level Symbol const reads and writes the `#sym:S` field rather
// than reporting a computed move; with a nil checker every element
// access on the receiver is computed, which is the reading every caller
// had before the symbol key existed.
//
// The walk itself is fieldCensusScan: its state and notes live in
// ir_field_bundles_scan.go, the dispatch order in
// ir_field_bundles_visit.go, and the arms in the read/write/call arm
// files beside them.
func FieldCensusWith(c *checker.Checker, body *ast.Node, receiverName string, fields []BundleField) FieldCensus {
	if body == nil {
		return FieldCensus{}
	}
	scan := newFieldCensusScan(c, receiverName, fields)
	scan.visit(body)
	return scan.censusInDeclarationOrder(fields)
}

// Believable answers whether a body's slots for this bundle may be
// trusted at all. Two reports kill it, for one reason: the body moved
// the object through something no slot names.
//
//   - ESCAPES — the receiver reached code the scan cannot follow, which
//     may have stored through it under a name never seen.
//   - COMPUTED WRITE — `this[k] = v` stored into a field the expression
//     does not name, so every slot of this receiver is suspect.
//
// A computed READ is not here: naming no field costs precision on that
// one access and moves nothing.
//
// Every consumer that decides whether to expand a bundle reads THIS,
// not the flags directly — the layout and the two call sites have to
// agree about which bodies expand, and three spellings of one condition
// is three chances to drift.
func (census FieldCensus) Believable() bool {
	// a method-calling capture is not believable HERE: the consumer that
	// can compute the captured methods' transitive write set (and havoc
	// those fields at every call statement) admits it through its own
	// gate — every consumer without that machinery refuses, exactly as
	// it refused when the shape was an escape. An ACCESSOR store or read
	// is the same deferral: only the consumer that folds the accessor's
	// own census in may admit it.
	return !census.Escapes && !census.ComputedWrite && len(census.CapturedMethodCalls) == 0 &&
		len(census.AccessorStores) == 0 && len(census.AccessorReads) == 0
}

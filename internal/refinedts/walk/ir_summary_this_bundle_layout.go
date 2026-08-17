// split from ir_summary_body.go — the `this` bundle

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── the `this` bundle ───────────────────────────────────────────── */

// thisBundleLayout is what a METHOD's own receiver is worth as entry
// slots: the fields the body READS become entries spelled
// "this.<field>", laid out after the declared parameters, and the fields
// it WRITES are named so the outs can carry them back.
//
// `expanded` false is the whole decline of the EXPANSION, never of the
// body: the method still lowers, its this-reads simply find no slot and
// hit the opaque floor exactly as they do today. `escaped` says WHY it
// declined when the reason was the receiver leaving the lowering's sight
// — the caller notes it as the body's first havoc, because a bundle that
// escaped may be written through a name nothing here saw.
type thisBundleLayout struct {
	Entries  []bodySlot
	Written  map[string]struct{}
	Expanded bool
	Escaped  bool
	// CaptureHavocNames: the slot spellings ("this.count") of the fields a
	// method-calling CAPTURE can move — the captured methods' transitive
	// write set. Non-empty puts the body's statement walk in havoc mode
	// (LoweringContext.CaptureHavocSlots); each named field is also in
	// Written, so the call sites read its exit state instead of keeping
	// the caller's own.
	CaptureHavocNames []string
	// ReturnsSelf: the body ends `return this` — the serving seams must
	// forget the caller's receiver knowledge (LoweredSummary
	// .ReturnsReceiver carries the requirement out).
	ReturnsSelf bool
}

// thisBundleOf reads a declaration's `this` bundle: (nothing) for
// anything that is not a method, and otherwise the field census of the
// enclosing class run over the method's body.
//
// The rules, per the wave-4 design:
//
//   - only a METHOD has a `this` bundle. An arrow keeps its enclosing
//     `this`, but the arrow route lays out CAPTURES after the declared
//     parameters, and a bundle would have to share that ground — so the
//     two are exclusive and the caller asserts it rather than laying out
//     both.
//   - the READ fields become entries, in the class's own declaration
//     order (FieldCensusOf answers in that order, which is what keeps
//     the layout and the apply side building one vector).
//   - COMPUTED access expands anyway: `this[k]` names no field, but the
//     havoc floor already stands in for the statement that performed it,
//     and the declaration still bounds which slots could be meant.
//   - an ESCAPING receiver does NOT expand. The bundle may move through a
//     name the census never saw, so an entry's state could be stale
//     mid-body while the slot still reads as known — the one shape where
//     expanding would claim more than it knows.
func thisBundleOf(ctx *FlowContext, declaration *ast.Node) thisBundleLayout {
	if declaration == nil {
		return thisBundleLayout{}
	}
	// an ACCESSOR is a class member with a body and a `this`, exactly
	// like a method — its bundle is what lets a getter over a backing
	// field compile at all (the accessor-call route reads it)
	if !ast.IsMethodDeclaration(declaration) &&
		!ast.IsGetAccessorDeclaration(declaration) &&
		!ast.IsSetAccessorDeclaration(declaration) &&
		!ast.IsConstructorDeclaration(declaration) {
		return thisBundleLayout{}
	}
	body := declaration.Body()
	if body == nil {
		return thisBundleLayout{}
	}
	classLike := declaration.Parent
	// an OBJECT LITERAL's shorthand method: the receiver's fields are the
	// literal's own scalar rows rather than a class's declarations —
	// literalThisBundleOf (method_this_writes.go) is the literal arm
	if classLike != nil && ast.IsObjectLiteralExpression(classLike) && ast.IsMethodDeclaration(declaration) {
		return literalThisBundleOf(ctx, declaration, classLike)
	}
	if classLike == nil || !ast.IsClassLike(classLike) {
		return thisBundleLayout{}
	}
	// declared members UNION constructor parameter properties — nest's
	// classes declare most fields as `constructor(private readonly …)`
	fields, isClass := ClassBundleFields(ctx, classLike)
	if !isClass || len(fields) == 0 {
		return thisBundleLayout{}
	}
	census := FieldCensusOf(body, "this", BundleFieldsAs("this", fields))
	// the body's ACCESSOR use resolves against the class's own get/set
	// declarations, and those bodies' fields join this layout — the
	// setter-write shape's slots (accessorCensusFold's comment). An
	// unresolvable or untame accessor keeps the escape.
	accessorReads, accessorWrites, foldOk := accessorCensusFold(classLike, fields, census)
	if !foldOk {
		return thisBundleLayout{Escaped: true}
	}
	// a METHOD-CALLING capture is admissible HERE, because this consumer
	// has the havoc machinery: the captured methods' transitive write set
	// becomes the havoc slots every code-running statement brackets, and
	// each of those fields is marked written so the call sites read its
	// exit state. An incomputable write set keeps the escape.
	var captureHavocNames []string
	switch {
	case census.Escapes:
		return thisBundleLayout{Escaped: true}
	case census.ComputedWrite:
		// `this[k] = v` moves a slot nothing names — the DECLARATION
		// bounds the set, so EVERY field joins the havoc set and every
		// code-running or element-storing statement brackets them
		for _, field := range fields {
			captureHavocNames = append(captureHavocNames, "this."+field.Name)
		}
	case len(census.CapturedMethodCalls) > 0:
		wipes, computable := CaptureWriteSet(classLike, fields, census.CapturedMethodCalls)
		if !computable {
			return thisBundleLayout{Escaped: true}
		}
		for _, field := range wipes {
			captureHavocNames = append(captureHavocNames, "this."+field.Name)
		}
	}
	// A CONSTRUCTOR RUNS MORE THAN ITS BODY. The class's field
	// INITIALIZERS and its PARAMETER PROPERTIES are statements the runtime
	// runs on the way in, and the constructor prelude
	// (lowerSummaryBodyWithCaptures) emits exactly those assignments ahead
	// of the body's own. The census, though, walks the BODY, so a field
	// written only by its initializer — `private _statusCode = 200;` in a
	// class whose constructor only calls super — appears in neither Reads
	// nor Writes, and the bundle it belongs to never expands. That is a
	// field the constructor demonstrably leaves a value in, reported as a
	// field the constructor never touched.
	//
	// So a constructor's prelude-written fields join the census's own
	// writes here, read off the class's declarations rather than off the
	// body. They are the same fields the prelude will assign, resolved by
	// the same two rules the prelude applies (an initialized property
	// declaration, a parameter property), so the layout and the prelude
	// name one set: every slot the prelude writes exists, and every slot
	// laid out for a prelude write is one the prelude fills.
	preludeWritten := constructorPreludeFields(declaration, fields)
	if len(census.Reads) == 0 && len(census.Writes) == 0 &&
		len(accessorReads) == 0 && len(accessorWrites) == 0 &&
		len(captureHavocNames) == 0 && len(preludeWritten) == 0 {
		return thisBundleLayout{}
	}
	written := map[string]struct{}{}
	for _, field := range census.Writes {
		written[field.SlotName] = struct{}{}
	}
	for _, field := range accessorWrites {
		written[field.SlotName] = struct{}{}
	}
	for _, name := range captureHavocNames {
		written[name] = struct{}{}
	}
	for _, field := range preludeWritten {
		written[field.SlotName] = struct{}{}
	}
	// the READ fields carry entries, and so does every WRITTEN one — a
	// write with no entry has no row, and a row is what the write-back
	// rides: `spoil() { this.age = 200 }` writes age without reading it,
	// and with no row the caller's own `age` slot kept its stale value
	// across a call that changed it (the literal arm closed this first —
	// literalThisBundleOf's comment — and a COMPLETE summary without the
	// row also told SummaryReceiverEffects the receiver was untouched).
	// A write-only entry is honest for the same reason the literal arm
	// states: the call site fills every this-entry from the receiver's
	// own slot (or unknown where it has none, bundleRetsAndArgs), so the
	// entry enters holding what the field held, and the exit is the
	// kernel's own join over the body's paths.
	//
	// The ACCESSOR-fold fields ride the same two lists: a getter's reads
	// are entries the embedded call statement threads in, a setter's
	// writes are rows its write-back threads out.
	//
	// A CONSTRUCTOR's prelude-written fields carry entries the same way:
	// the prelude ASSIGNS every one of them before any statement runs, so
	// the entry's incoming value is overwritten before anything can read
	// it. The entry exists so the prelude has a slot to write and the exit
	// row has a slot to report — which is what a `new C()` local's leaves
	// are read from.
	entries := make([]bodySlot, 0, len(census.Reads)+len(census.Writes)+len(preludeWritten))
	laidOut := map[string]struct{}{}
	appendEntry := func(field BundleField) {
		if _, already := laidOut[field.SlotName]; already {
			return
		}
		laidOut[field.SlotName] = struct{}{}
		entries = append(entries, bodySlot{
			Name:      field.SlotName,
			Sort:      field.Sort,
			TypeofTag: field.TypeofTag,
		})
	}
	for _, field := range census.Reads {
		appendEntry(field)
	}
	for _, field := range census.Writes {
		appendEntry(field)
	}
	for _, field := range accessorReads {
		appendEntry(field)
	}
	for _, field := range accessorWrites {
		appendEntry(field)
	}
	for _, field := range preludeWritten {
		appendEntry(field)
	}
	return thisBundleLayout{
		Entries:           entries,
		Written:           written,
		Expanded:          len(entries) > 0,
		CaptureHavocNames: captureHavocNames,
		ReturnsSelf:       census.ReturnsSelf,
	}
}

// constructorPreludeFields is the field set a CONSTRUCTOR's prelude
// writes before its body's first statement: every class member that is
// an initialized property declaration, and every parameter property.
//
// It exists so the LAYOUT and the PRELUDE read one list. The prelude
// (lowerSummaryBodyWithCaptures) emits an assignment per initialized
// property and per parameter property, each onto the slot named
// "this.<field>"; this function names exactly those fields off the same
// declarations, so a slot the prelude looks up always exists, and a slot
// laid out for a prelude write is always one the prelude fills. Reading
// them apart is what let the two disagree: the layout asked the body
// census, which never sees an initializer.
//
// Answered in the given field set's order — the declaration order the
// slot vector is built in — and only for fields that set holds, so a
// property the field reading declined (a computed key spelling no slot)
// contributes nothing here either.
//
// Anything that is not a constructor answers nothing: a method has no
// prelude, and its fields are exactly what its body touches.
func constructorPreludeFields(declaration *ast.Node, fields []BundleField) []BundleField {
	if declaration == nil || !ast.IsConstructorDeclaration(declaration) {
		return nil
	}
	classLike := declaration.Parent
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil
	}
	assigned := map[string]struct{}{}
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		property := member.AsPropertyDeclaration()
		// an UNINITIALIZED declaration (`private _headers?: Headers;`)
		// writes nothing — the field enters the body absent, and claiming a
		// value was left in it would be the one thing this must not say
		if property.Initializer == nil || property.Name() == nil || !ast.IsIdentifier(property.Name()) {
			continue
		}
		assigned[property.Name().Text()] = struct{}{}
	}
	for _, parameter := range declaration.Parameters() {
		if !isParameterPropertyDeclaration(parameter) {
			continue
		}
		pd := parameter.AsParameterDeclaration()
		if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) {
			continue
		}
		assigned[pd.Name().Text()] = struct{}{}
	}
	if len(assigned) == 0 {
		return nil
	}
	written := make([]BundleField, 0, len(assigned))
	for _, field := range fields {
		if _, isAssigned := assigned[field.Name]; isAssigned {
			written = append(written, field)
		}
	}
	return written
}

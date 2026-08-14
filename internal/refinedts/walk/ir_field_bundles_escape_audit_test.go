// THE ESCAPE AUDIT: every syntactic way a receiver can reach code the
// census cannot follow, asked one shape at a time.
//
// The census's escape flag is soundness-critical in ONE direction. A
// flagged escape that did not happen costs precision — the bundle
// refuses to expand and the body gets fewer slots. A MISSED escape is a
// wrong answer: the bundle expands, the layout gives its fields entry
// slots, and the call sites keep believing those slots across a
// statement that moved the object through a name the census never saw.
// The same holds for a WRITE recorded as a READ: the slot keeps its
// entry value past the store, and every downstream question answers
// from a state the program left behind.
//
// So these cases are written from the dangerous side. Each one moves or
// hides a field through a form the four dimensions might not name, and
// asserts the census reports it as a write, a computed move, or an
// escape — never as a plain read and never as nothing at all.
package walk

import (
	"testing"
)

// auditCensus runs the census over a function body against a field set
// spelled under a named receiver.
func auditCensus(t *testing.T, source string, receiverName string, fieldNames ...string) FieldCensus {
	t.Helper()
	fields := make([]BundleField, len(fieldNames))
	for index, name := range fieldNames {
		fields[index] = BundleField{
			Name:      name,
			SlotName:  receiverName + "." + name,
			Sort:      BindingKindNumber,
			TypeofTag: TypeofTagNumber,
		}
	}
	return FieldCensusOf(bundleFunctionBodyOf(t, source), receiverName, fields)
}

// auditMethodCensus is the same over a class METHOD's body, where the
// receiver is `this` and the fields are the class's own.
func auditMethodCensus(t *testing.T, source string) FieldCensus {
	t.Helper()
	statements := bundleParse(t, source)
	fields, ok := ClassFieldsOf(nil, statements[0])
	if !ok {
		t.Fatalf("ClassFieldsOf declined %q", source)
	}
	return FieldCensusOf(bundleMethodBodyOf(t, source), "this", fields)
}

// hasField answers whether a field list names a field.
func hasField(fields []BundleField, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

// moved: the census reported SOME form of loss of knowledge about the
// field — a write, a computed move, or an escape. Any one of the three
// makes the consumer stop believing the slot; none of them means the
// slot is believed across a statement that changed it.
func moved(census FieldCensus, name string) bool {
	return census.Escapes || census.Computed || hasField(census.Writes, name)
}

/* ── destructuring assignment through a field ───────────────────── */

// `({ x: this.count } = source)` STORES into this.count. The target is
// an object literal, not a property access, so the write forms do not
// name it. If the walk descends and meets `this.count` as a plain read,
// the slot keeps its entry value past a store.
func TestEscapeAudit_ObjectAssignmentPatternWritingAField(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(source: { x: number }) { ({ x: this.count } = source); } }")
	if !moved(census, "count") {
		t.Errorf("an object assignment pattern storing into this.count reported no write, no computed move, and no escape: %+v — the slot would be believed across the store", census)
	}
}

// `[this.count] = pair` is the array-pattern spelling of the same store.
func TestEscapeAudit_ArrayAssignmentPatternWritingAField(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(pair: number[]) { [this.count] = pair; } }")
	if !moved(census, "count") {
		t.Errorf("an array assignment pattern storing into this.count reported nothing: %+v", census)
	}
}

// a REST target: `({ ...this.rest } = source)` — the rest element's
// target is a field access too.
func TestEscapeAudit_RestElementWritingAField(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(source: { x: number }) { ({ ...this.count } = source); } }")
	if !moved(census, "count") {
		t.Errorf("a rest element storing into this.count reported nothing: %+v", census)
	}
}

// a DEFAULTED pattern element whose target is a field:
// `({ x: this.count = 1 } = source)`.
func TestEscapeAudit_DefaultedPatternElementWritingAField(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(source: { x?: number }) { ({ x: this.count = 1 } = source); } }")
	if !moved(census, "count") {
		t.Errorf("a defaulted pattern element storing into this.count reported nothing: %+v", census)
	}
}

/* ── loop bindings through a field ──────────────────────────────── */

// `for (this.count of xs)` stores into the field once per iteration.
// The target is not a binary expression, so the write forms miss it.
func TestEscapeAudit_ForOfBindingAField(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(xs: number[]) { for (this.count of xs) { } } }")
	if !moved(census, "count") {
		t.Errorf("`for (this.count of xs)` reported no write, no computed move, and no escape: %+v", census)
	}
}

// `for (this.count in o)` is the same store under the other loop form.
func TestEscapeAudit_ForInBindingAField(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(o: object) { for (this.count in o) { } } }")
	if !moved(census, "count") {
		t.Errorf("`for (this.count in o)` reported nothing: %+v", census)
	}
}

/* ── a shadowed receiver name ───────────────────────────────────── */

// A named receiver can be SHADOWED. In `function m(wrapper: W) { for
// (const wrapper of xs) { use(wrapper.metatype); } }` the inner
// `wrapper` is a different object entirely. Counting its field read as
// the bundle's makes the layout hand the parameter's slot to a read of
// an unrelated value — a wrong answer, not a lost one.
//
// Either outcome is acceptable SO LONG AS the bundle is not trusted:
// reporting the escape is honest, and so is reporting no read at all.
// What is not acceptable is a plain read of `metatype`.
func TestEscapeAudit_AShadowedReceiverIsNotTheBundle(t *testing.T) {
	census := auditCensus(t,
		"function m(wrapper: W, xs: W[]) { for (const wrapper of xs) { use(wrapper.metatype); } }",
		"wrapper", "metatype")
	if hasField(census.Reads, "metatype") && !census.Escapes {
		t.Errorf("a SHADOWED `wrapper` contributed a plain read of metatype: %+v — the slot would answer for an unrelated object", census)
	}
}

// the same shadow through a catch binding
func TestEscapeAudit_ACatchBindingShadowingTheReceiver(t *testing.T) {
	census := auditCensus(t,
		"function m(wrapper: W) { try { g(); } catch (wrapper) { use(wrapper.metatype); } }",
		"wrapper", "metatype")
	if hasField(census.Reads, "metatype") && !census.Escapes {
		t.Errorf("a catch binding shadowing `wrapper` contributed a plain read: %+v", census)
	}
}

// a shadow that WRITES: the outer bundle must not record the write of a
// name that is not it, but recording it is merely imprecise. The
// dangerous direction is the read case above; this pins the behaviour.
func TestEscapeAudit_AShadowedReceiverWriting(t *testing.T) {
	census := auditCensus(t,
		"function m(wrapper: W, xs: W[]) { for (const wrapper of xs) { wrapper.metatype = 1; } }",
		"wrapper", "metatype")
	if hasField(census.Reads, "metatype") && !census.Escapes {
		t.Errorf("a shadowed write contributed a read of the outer bundle: %+v", census)
	}
}

/* ── the receiver reaching code the scan cannot follow ──────────── */

// `this` captured by a nested ARROW: the arrow keeps the enclosing
// `this`, runs at a time the scan cannot place, and may store through
// it. The scan must call this an escape.
func TestEscapeAudit_ThisCapturedByANestedArrow(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(xs: number[]) { xs.forEach(() => { this.count = 0; }); } }")
	if !census.Escapes {
		t.Errorf("`this` captured by a nested arrow did not escape: %+v — the arrow stores into count at a time the scan cannot place", census)
	}
}

// the same capture READ-ONLY is ADMITTED as a read: a read moves
// nothing, so it may run at any later time without invalidating any
// belief the body holds. What must hold instead: the field is recorded
// as read, and nothing else is claimed.
func TestEscapeAudit_ThisReadByANestedArrow(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(xs: number[]) { xs.forEach(() => { use(this.count); }); } }")
	if census.Escapes {
		t.Errorf("a READ-ONLY arrow capture escaped: %+v — reading moves nothing", census)
	}
	if !hasField(census.Reads, "count") {
		t.Errorf("the arrow's read of count was not recorded: %+v", census)
	}
	if len(census.Writes) != 0 {
		t.Errorf("a read-only capture recorded writes: %+v", census)
	}
}

// an arrow that CALLS a method is collected, not blindly escaped — and
// the PROPERTY that matters holds: no consumer without the capture-
// havoc machinery may believe the bundle (Believable refuses), and the
// one consumer that admits it must compute the methods' transitive
// write set first (thisBundleOf keeps the escape when it cannot).
func TestEscapeAudit_AMethodCallingArrowIsCollectedAndNotBelievable(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(xs: number[]) { xs.forEach(() => { this.bump(); }); } }")
	if census.Escapes {
		t.Errorf("an arrow calling this.bump() escaped at the census: %+v — the call is collected, not blind", census)
	}
	if len(census.CapturedMethodCalls) != 1 || census.CapturedMethodCalls[0] != "bump" {
		t.Errorf("CapturedMethodCalls = %v, want [bump]", census.CapturedMethodCalls)
	}
	if census.Believable() {
		t.Errorf("a method-calling capture answered believable — a consumer without the havoc machinery would trust fields the method can move")
	}
}

// a named receiver crossing into a nested FUNCTION expression — the
// receiver name is not rebound the way `this` is, so the closure holds
// the very same object.
func TestEscapeAudit_ANamedReceiverCapturedByANestedFunction(t *testing.T) {
	census := auditCensus(t,
		"function m(wrapper: W) { return function () { wrapper.metatype = 1; }; }",
		"wrapper", "metatype")
	if !census.Escapes {
		t.Errorf("`wrapper` captured by a nested function did not escape: %+v", census)
	}
}

// the receiver handed to a call as a bare argument
func TestEscapeAudit_TheReceiverPassedAsAnArgument(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m() { register(this); } }")
	if !census.Escapes {
		t.Errorf("`this` passed as an argument did not escape: %+v — the callee may store through it", census)
	}
}

// the receiver aliased to a local, then written through the alias
func TestEscapeAudit_TheReceiverAliasedToALocal(t *testing.T) {
	census := auditCensus(t,
		"function m(wrapper: W) { const w = wrapper; w.metatype = 1; }",
		"wrapper", "metatype")
	if !census.Escapes {
		t.Errorf("`wrapper` aliased to a local did not escape: %+v — the write through `w` is a write to the bundle", census)
	}
}

// the receiver spread into an object or a call — `{...this}` reads every
// field, and `f(...xs, this)` hands it over
func TestEscapeAudit_TheReceiverSpread(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m() { return { ...this }; } }")
	if !census.Escapes {
		t.Errorf("`{ ...this }` did not escape: %+v", census)
	}
}

// an UNDECLARED member read: no slot holds it, so answering it from a
// slot would answer some other field's state
func TestEscapeAudit_AnUndeclaredMemberRead(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m() { return this.other; } }")
	if !census.Escapes {
		t.Errorf("reading an undeclared member did not escape: %+v", census)
	}
}

// an OPTIONAL chain on the receiver — `this?.count` admits an absent
// receiver, which no slot spells
func TestEscapeAudit_AnOptionalChainOnTheReceiver(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m() { return this?.count; } }")
	if !census.Escapes {
		t.Errorf("`this?.count` did not escape: %+v", census)
	}
}

// a computed WRITE names no field, but the declaration bounds the set
func TestEscapeAudit_AComputedWriteIsReported(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(k: string) { this[k] = 1; } }")
	if !census.Computed && !census.Escapes {
		t.Errorf("`this[k] = 1` reported neither a computed move nor an escape: %+v", census)
	}
}

// `Object.assign(this, source)` moves every field through a callee the
// scan cannot follow — the receiver is a bare argument, so the bare
// mention rule is what must catch it
func TestEscapeAudit_ObjectAssignIntoTheReceiver(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(source: object) { Object.assign(this, source); } }")
	if !census.Escapes {
		t.Errorf("`Object.assign(this, source)` did not escape: %+v", census)
	}
}

// a field read through a PARENTHESIZED or cast receiver is still the
// receiver — the census must not lose it to the wrapper and call it a
// bare mention, nor lose the field
func TestEscapeAudit_AParenthesizedReceiverStillReadsItsField(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m() { return (this).count; } }")
	if census.Escapes {
		t.Errorf("`(this).count` escaped: %+v — a parenthesized receiver is the receiver", census)
	}
	if !hasField(census.Reads, "count") {
		t.Errorf("`(this).count` did not record a read of count: %+v", census)
	}
}

// a write through a parenthesized receiver is a WRITE, not a read
func TestEscapeAudit_AParenthesizedReceiverWriteIsAWrite(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m() { (this).count = 1; } }")
	if !moved(census, "count") {
		t.Errorf("`(this).count = 1` recorded no write, no computed move, and no escape: %+v", census)
	}
}

// a field read on a NESTED path — `this.count.inner` reads count and
// then steps further. The step is beyond the slot, but the read of
// count itself is genuine and must not be lost.
func TestEscapeAudit_ANestedPathStillReadsTheOuterField(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m() { return this.count.inner; } }")
	if !hasField(census.Reads, "count") && !census.Escapes {
		t.Errorf("`this.count.inner` recorded neither a read of count nor an escape: %+v", census)
	}
}

/* ── destructuring FROM the receiver ────────────────────────────── */

// `const { count } = this` is the read of count wearing a pattern —
// admitted as exactly that read, never an escape.
func TestEscapeAudit_DestructuringFromThisIsARead(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(): number { const { count } = this; return count + 1; } }")
	if census.Escapes {
		t.Errorf("destructuring a declared field from this escaped: %+v", census)
	}
	if !hasField(census.Reads, "count") {
		t.Errorf("the pattern's read of count was not recorded: %+v", census)
	}
}

// a pattern with a DEFAULT, a REST, or an undeclared member keeps the
// escape — those read shapes no slot spells.
func TestEscapeAudit_DestructuringFromThisWithADefaultStillEscapes(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m(): number { const { count = 1 } = this; return count; } }")
	if !census.Escapes {
		t.Errorf("a defaulted pattern from this did not escape: %+v", census)
	}
	rest := auditMethodCensus(t,
		"class C { count: number; m(): object { const { ...r } = this; return r; } }")
	if !rest.Escapes {
		t.Errorf("a rest pattern from this did not escape: %+v", rest)
	}
	undeclared := auditMethodCensus(t,
		"class C { count: number; m(): number { const { other } = this; return 1; } }")
	if !undeclared.Escapes {
		t.Errorf("a pattern reading an undeclared member did not escape: %+v", undeclared)
	}
}

/* ── the receiver itself being replaced ─────────────────────────── */

// A named receiver can be REASSIGNED: `wrapper = other` makes every
// later `wrapper.metatype` a read of a DIFFERENT object. The entries the
// layout filled belong to the argument the caller passed, so a later
// read answering from them answers about the wrong object.
func TestEscapeAudit_AReassignedReceiver(t *testing.T) {
	census := auditCensus(t,
		"function m(wrapper: W, other: W) { wrapper = other; return wrapper.metatype; }",
		"wrapper", "metatype")
	if hasField(census.Reads, "metatype") && !census.Escapes {
		t.Errorf("a REASSIGNED receiver still contributed a plain read of metatype: %+v — the entry belongs to the caller's argument, not to `other`", census)
	}
}

// the compound form — `wrapper ||= other`, `wrapper ??= other` — stores
// into the receiver name just the same
func TestEscapeAudit_ACompoundReassignedReceiver(t *testing.T) {
	census := auditCensus(t,
		"function m(wrapper: W, other: W) { wrapper ??= other; return wrapper.metatype; }",
		"wrapper", "metatype")
	if hasField(census.Reads, "metatype") && !census.Escapes {
		t.Errorf("a compound-reassigned receiver still contributed a plain read: %+v", census)
	}
}

// a destructured reassignment of the receiver name: `({ w: wrapper } =
// source)` replaces the object the name denotes
func TestEscapeAudit_ADestructuredReassignmentOfTheReceiver(t *testing.T) {
	census := auditCensus(t,
		"function m(wrapper: W, source: { w: W }) { ({ w: wrapper } = source); return wrapper.metatype; }",
		"wrapper", "metatype")
	if hasField(census.Reads, "metatype") && !census.Escapes {
		t.Errorf("a destructured reassignment of the receiver still contributed a plain read: %+v", census)
	}
}

// a WRITE to a nested path — `this.count.inner = 1` does NOT store into
// the count slot, it stores into the object count points at. The count
// slot's value (the reference) is unchanged, so a read is right; what
// must not happen is silence about the deeper store when the consumer
// believes leaves below count.
func TestEscapeAudit_AWriteThroughANestedPath(t *testing.T) {
	census := auditMethodCensus(t,
		"class C { count: number; m() { this.count.inner = 1; } }")
	if !hasField(census.Reads, "count") && !moved(census, "count") {
		t.Errorf("`this.count.inner = 1` reported nothing at all about count: %+v", census)
	}
}

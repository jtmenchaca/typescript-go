// split from ir_summary_body.go — the arrow route's captures

package walk

import (
	"strings"
)

// capturedSlot is one READ-ONLY capture an arrow argument closes over:
// the name it is spelled under in the enclosing body, and the sort and
// typeof evidence its caller slot wears. Each one becomes an EXTRA
// entry of the arrow's summary, laid out immediately after the declared
// parameters — so entry k for k < len(parameters) is the k-th
// parameter, and entry len(parameters)+j is the j-th capture, in the
// order the free-variable scan reported them (source order of first
// read). The call site binds each to a `var` of the caller slot the
// name resolves to, which is why the layout has to be the scan's own
// deterministic order and not a map's.
type capturedSlot struct {
	Name      string
	Sort      BindingKind
	TypeofTag TypeofTag
	// Written: the closure's body ASSIGNS this captured name. The entry
	// still enters from the caller's own slot — a captured `settled` IS
	// the caller's `settled`, read before it is written — and the row
	// additionally rides out in BundleEntries so the call site maps its
	// EXIT back onto that same caller slot.
	//
	// This is the one bit that turns a capture from a value passed in
	// into a place written through, and it is why a written capture is
	// laid out exactly as a record-parameter leaf is: both are entries
	// whose exits belong to a slot the caller already holds.
	Written bool
	// Members: an OBJECT capture's leaves, in the census's own order.
	//
	// A capture with no members is the SCALAR one above — one entry
	// under the name, carrying the name's own value. A capture WITH
	// members is a bundle: no entry stands for the name itself, and one
	// entry stands for each leaf, spelled
	// "#capture.<name>.<member>". That is the record-parameter shape
	// applied to a capture, and it is laid out the same way for the same
	// reason — the caller already holds a slot per leaf, so each leaf's
	// value comes in from that slot and each moved leaf's exit goes back
	// to it.
	//
	// Sort and TypeofTag above belong to the SCALAR case only. A
	// bundle's evidence is per leaf, so it rides in the member rows.
	Members []capturedLeaf
	// MethodCalls: the members this closure calls AS METHODS on the
	// capture. MethodWrites is the subset the callee resolution found
	// MAY move the receiver — nil means the resolution was not
	// performed at all, which the layout reads as "every call may
	// move", the doubt direction.
	MethodCalls  []string
	MethodWrites map[string]struct{}
}

// capturedLeaf is ONE member of an object capture: the member name, the
// evidence the caller's leaf slot wears, and whether the closure moves
// it.
//
// Written here means the same thing it means on a record-parameter leaf
// row: the closure assigned this member directly, or code the closure
// handed the object to may have moved it. Either way the caller takes
// the leaf's exit back rather than keeping its own value.
type capturedLeaf struct {
	Member    string
	Sort      BindingKind
	TypeofTag TypeofTag
	Written   bool
}

// capturedSlotName is the BundleEntries path a capture row rides under.
//
// The path vocabulary already spells two rooted families — "this.<field>"
// for a method's receiver bundle, "<holder>.<member>" for a record or
// class-typed parameter's leaves — and both are read back by splitting on
// their root. A capture is neither: it is the caller's OWN name, spelled
// exactly as the caller spells it, with no holder in front. So it takes
// its own prefix rather than borrowing a rooted one, which keeps the
// readers total — bundleRetsAndArgs asks for "this.", bundleParamRetsAndArgs
// asks for a holder, and neither can mistake a capture row for its own.
func capturedSlotName(name string) string { return "#capture." + name }

// capturedNameOfSlot is capturedSlotName read backwards: the caller name
// a capture row stands for, and whether the path is a capture row at all.
//
// An OBJECT capture's leaf rides under "#capture.<name>.<member>", so
// this answers "<name>.<member>" for one — the whole spelling below the
// prefix, which is exactly the key the call site's write-back map holds
// its leaf slots under. One reader serves both kinds of row.
func capturedNameOfSlot(path string) (string, bool) {
	if !strings.HasPrefix(path, "#capture.") {
		return "", false
	}
	return strings.TrimPrefix(path, "#capture."), true
}

// capturedLeafSlotName is the entry name ONE leaf of an OBJECT capture
// rides under: "#capture.disconnectSource.writableEnded".
//
// The record-parameter leaves are spelled "<holder>.<member>" with no
// prefix, because a parameter's holder is a name the callee declared and
// no caller slot competes for it. A capture's holder is the CALLER's own
// name, so an unprefixed "stream.writableEnded" would be exactly the
// spelling the caller's own flattened local already wears — one string
// standing for two different bodies' slots. The prefix keeps the two
// apart, and it is the same prefix the scalar capture rows wear, so one
// reader recognizes both kinds.
func capturedLeafSlotName(name string, member string) string {
	return "#capture." + name + "." + member
}

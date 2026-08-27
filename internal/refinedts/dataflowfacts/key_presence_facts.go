// KEY PRESENCE: what `m.has(k)` establishes about a later `m.get(k)`.
//
// sec-map.prototype.has and sec-map.prototype.get walk the SAME
// [[MapData]] list with the SAME test — `SameValue(entry.[[Key]],
// CanonicalizeKeyedCollectionKey(key))` — and `get` returns
// `entry.[[Value]]` for the entry `has` found, falling to *undefined*
// only when no entry matches. So a `has` that answered true names an
// entry that is still there for `get` to find, and the `V | undefined`
// the host signature states carries no absence at that read — unless
// something between the two could have changed [[MapData]].
//
// This file records that as a PLACE FACT rather than as knowledge
// about the map's own value. The receiver need not be a tracked
// collection: a plain `m: Map<string, Age>` parameter is never read
// into a KindCollection (typereading builds no collections), and it is
// exactly those parameters the guard rows are written against. What
// the fact needs is only the receiver's identity and the key's
// spelling, both of which a parameter has.
//
// The fact is held in the walk's own Env under a DOTTED PLACE KEY
// rooted at the receiver binding — the representation the environment
// already sweeps. ForgetPlaceEntriesEnv drops every dotted key under a
// root on every write to it, and every mutation route in the walk
// (a `set`/`delete`/`clear` model, an unmodeled method's havoc, the
// receiver handed to a callee, an assignment) reaches that sweep. So
// the mutation-forgets-the-fact rule needs no machinery of its own:
// it is the sweep the environment already runs. JoinEnvs drops a name
// absent from either arm, so a fact established on one branch does not
// survive the merge — the same rule every other place entry follows.
//
// Only a key the two sides spell IDENTICALLY is read: the same string
// literal, or the same plain identifier naming an unwritten binding.
// SameValue on the canonicalized key is what the two clauses share,
// and identical spelling is the reading that both sides agree on
// without evaluating anything. A computed key, a member chain, or a
// key whose binding is written between the guard and the read records
// nothing.
package dataflowfacts

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
)

// keyPresencePlacePrefix is the dotted segment a presence fact hangs
// under, between the receiver root and the key's spelling. A dot leads
// it so ForgetPlaceEntriesEnv's `root + "."` prefix sweep catches it,
// and the word itself cannot collide with a property name a real place
// entry would use — no TypeScript member is spelled with a leading
// "has(".
const keyPresencePlacePrefix = ".has("

// KeyPresencePlaceSegment is keyPresencePlacePrefix's exported spelling
// — what walk's own await/yield sweep matches on to find every
// presence-place entry in the environment, regardless of which root it
// hangs under. An await ends the current job (sec-await) and a yield
// does the same for a generator body (sec-generatoryield): either
// suspension hands control to code this function does not own, so a
// `has` fact recorded before it may no longer hold at the resume — the
// walk cannot tell an exclusively-owned receiver from a shared one
// today, so every presence place is swept rather than only some.
const KeyPresencePlaceSegment = keyPresencePlacePrefix

// KeyPresencePlace is the Env key a `m.has(k)` fact is written under
// and a later `m.get(k)` reads back: the receiver's root binding, the
// presence segment, and the key's spelling. Empty where the pair names
// no place this file will record — an unspellable receiver or key.
func KeyPresencePlace(receiver *ast.Node, key *ast.Node) string {
	root := keyPresenceReceiverRoot(receiver)
	if root == "" {
		return ""
	}
	spelling := KeyPresenceSpelling(key)
	if spelling == "" {
		return ""
	}
	return root + keyPresencePlacePrefix + spelling + ")"
}

// keyPresenceReceiverRoot is the binding a presence fact is rooted at.
// Only a plain identifier answers: the fact's whole forget discipline
// is ForgetPlaceEntriesEnv's sweep of one root name, and a receiver
// that is not a name has no root for that sweep to reach.
func keyPresenceReceiverRoot(receiver *ast.Node) string {
	if receiver == nil || !ast.IsIdentifier(receiver) {
		return ""
	}
	return receiver.Text()
}

// KeyPresenceSpelling is the key's identity for the fact: a string
// literal answers its own text wrapped so it can never read as an
// identifier, and a plain identifier answers its name. Everything else
// answers empty — a computed key names a value this reader cannot
// settle, and settling it wrongly would claim presence of an entry the
// map may not hold.
func KeyPresenceSpelling(key *ast.Node) string {
	if key == nil {
		return ""
	}
	bare := key
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if literal := StringLiteralOf(bare); literal != nil {
		return "\"" + *literal + "\""
	}
	if ast.IsIdentifier(bare) {
		return bare.Text()
	}
	return ""
}

// KeyPresenceMirrorPlace is the SECOND place a symbolic-key fact is
// written under: the same presence spelling rooted at the KEY's own
// binding rather than the map's. Both places are written by the guard
// and both are required at the read, so a write to EITHER name reaches
// ForgetPlaceEntriesEnv's sweep of that root and the fact is gone —
// which is what makes `m.has(key)` … `key = "other"` … `m.get(key)`
// record nothing at the read, with no staleness machinery beyond the
// sweep the environment already runs.
//
// Empty for a literal key: a literal names the same entry no matter
// what any binding does, so it depends on the map root alone.
func KeyPresenceMirrorPlace(receiver *ast.Node, key *ast.Node) string {
	keyRoot := KeyPresenceKeyRoot(key)
	if keyRoot == "" {
		return ""
	}
	root := keyPresenceReceiverRoot(receiver)
	if root == "" {
		return ""
	}
	return keyRoot + keyPresencePlacePrefix + root + ")"
}

// KeyPresenceMirrorPlaceOfCall is KeyPresenceMirrorPlace for a whole
// `receiver.has(key)` / `receiver.get(key)` call node.
func KeyPresenceMirrorPlaceOfCall(e *ast.Node) string {
	if e == nil || !ast.IsCallExpression(e) {
		return ""
	}
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return ""
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return ""
	}
	access := call.Expression.AsPropertyAccessExpression()
	return KeyPresenceMirrorPlace(access.Expression, call.Arguments.Nodes[0])
}

// KeyPresencePlacesHeld answers the presence places a condition
// establishes at the requested polarity: every conjunctive leaf that
// is a `receiver.has(key)` call the two spellings above can name. A
// symbolic-key leaf contributes its mirror place too, so both names
// the fact depends on carry it.
//
// negated mirrors LengthGuardNarrowings and MapPresenceNarrowings:
// pass false to read the condition's HELD side, true to read its
// REFUTED side. The exit-guard shape — `if (!m.has(k)) return;` — is
// read by negating the SOURCE condition, so the leaf under it arrives
// non-negated and records the same fact the inline guard does.
//
// A leaf the shared tree negated records NOTHING: a refuted `has`
// proves the key ABSENT, and absence is not what this fact carries.
func KeyPresencePlacesHeld(condition *ast.Node, negated bool) []string {
	var places []string
	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negated)) {
		if leaf.Negated {
			continue
		}
		place := keyPresencePlaceOfCall(leaf.Test)
		if place == "" {
			continue
		}
		places = append(places, place)
		if mirror := KeyPresenceMirrorPlaceOfCall(leaf.Test); mirror != "" {
			places = append(places, mirror)
		}
	}
	return places
}

// keyPresencePlaceOfCall reads one `receiver.has(key)` call into its
// presence place, or empty for any other expression.
func keyPresencePlaceOfCall(e *ast.Node) string {
	if e == nil || !ast.IsCallExpression(e) {
		return ""
	}
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return ""
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.Name().Text() != "has" {
		return ""
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return ""
	}
	return KeyPresencePlace(access.Expression, call.Arguments.Nodes[0])
}

// KeyPresenceSurvivesRemoval: does this held place — one dotted entry
// rooted at the receiver — survive a `delete(removedKey)` on it?
//
// sec-map.prototype.delete removes ONLY the entry whose key is
// SameValue-equal to its argument, and SameValue on two different
// string literals is false. So a presence fact whose key spelling is a
// string literal DIFFERENT from the removed key's own string literal
// names an entry the removal cannot touch, and the fact stays true.
//
// Everything else answers false and sweeps with the mutation: a
// symbolic removed key can equal any spelling at runtime, a
// symbolic-keyed fact can equal the removed literal, and a non-presence
// place entry is ordinary knowledge a mutation of the receiver stales.
func KeyPresenceSurvivesRemoval(place string, receiver *ast.Node, removedKey *ast.Node) bool {
	removed := KeyPresenceSpelling(removedKey)
	if len(removed) == 0 || removed[0] != '"' {
		return false
	}
	root := keyPresenceReceiverRoot(receiver)
	if root == "" {
		return false
	}
	literalPlaces := root + keyPresencePlacePrefix + "\""
	if !strings.HasPrefix(place, literalPlaces) {
		return false
	}
	return place != root+keyPresencePlacePrefix+removed+")"
}

// KeyPresenceKeyRoot is the KEY's own binding, where the key is a
// plain identifier — the second name a symbolic-key fact depends on.
// `m.has(key)` followed by `key = "other"` and then `m.get(key)` is
// two different lookups, so the fact has to go stale on a write to the
// key binding as well as on one to the map. Empty for a literal key,
// which names the same entry however the surrounding code changes.
func KeyPresenceKeyRoot(key *ast.Node) string {
	if key == nil {
		return ""
	}
	bare := key
	for ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if ast.IsIdentifier(bare) {
		return bare.Text()
	}
	return ""
}

// KeyPresencePlaceOfGet reads a `receiver.get(key)` call into the
// presence place a guard would have written — the ask a `.get`
// evaluation makes of the environment to learn whether its key was
// established present. Empty for any other call.
func KeyPresencePlaceOfGet(e *ast.Node) string {
	return keyPresencePlaceOfRead(e, "get")
}

// KeyPresencePlaceOfHas reads a `receiver.has(key)` call into the SAME
// presence place, for the ask a later `has` makes of the environment.
//
// The fact answers `has` even more directly than it answers `get`. It
// records that an entry with this key is in [[MapData]]; `has` walks
// that list with the SameValue test and returns *true* exactly when one
// is (sec-map.prototype.has), so a standing fact makes the read exactly
// `true` — no absence to reason about at all. The place is the one the
// guard wrote, so every mutation route already sweeps it: a fact that
// survived to this read is a fact still true at it.
//
// The shape this closes is the get-or-insert one, where the second read
// is a `has` rather than a `get`: `if (!m.has(k)) m.set(k, def);` then
// `m.has(k)`. Both arms leave the key present — the guarded arm set it,
// the other arm found it — but with no reader for a `has` the second
// read fell back to the two-word boolean and every downstream use of it
// widened.
func KeyPresencePlaceOfHas(e *ast.Node) string {
	return keyPresencePlaceOfRead(e, "has")
}

// keyPresencePlaceOfRead is the place a `receiver.<method>(key)` read
// asks about — the shared body of the two readers above.
func keyPresencePlaceOfRead(e *ast.Node, method string) string {
	if e == nil || !ast.IsCallExpression(e) {
		return ""
	}
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return ""
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.Name().Text() != method {
		return ""
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return ""
	}
	return KeyPresencePlace(access.Expression, call.Arguments.Nodes[0])
}

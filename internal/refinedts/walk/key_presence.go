// The walk's side of the key-presence fact: the marker a held
// `m.has(k)` writes into the environment, and the ask a `m.get(k)`
// evaluation makes of it.
//
// The fact itself — its place spelling, and why a dotted place key
// rooted at the receiver gets the forget discipline for free — is
// dataflowfacts/key_presence_facts.go. This file holds only what needs
// walk's own Env: the stored value, and the read that turns a recorded
// place into "this get carries no absence".
package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// keyPresenceEstablished is what a presence place holds. The value is
// the `true` a `has` that answered positively actually returned
// (sec-map.prototype.has's own `return *true*`), spelled the way the
// domain spells a boolean, so a place entry that is somehow read as an
// ordinary value reads as that boolean rather than as anything
// surprising. Nothing consults its contents — the fact is the place's
// PRESENCE — but an inert, honest value is what belongs in an
// environment every other reader can range over.
var keyPresenceEstablished = abstractdomain.KnownValues(
	[]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)

// KeyPresenceEstablishedFor: was this `m.get(k)` call's exact key
// established present by a guard that still stands?
//
// The place a guard would have written is computed from the call
// itself, and answered true only when the environment still holds it.
// For a symbolic key BOTH places are required — the one rooted at the
// map and the one rooted at the key binding — so a write to either
// name, which sweeps that root's dotted entries, takes the answer back
// to false.
//
// A false answer changes nothing: the read keeps the `V | undefined`
// its host signature states, which is what it wore before this fact
// existed.
func KeyPresenceEstablishedFor(env Env, e *ast.Node) bool {
	return keyPresenceHeldAt(env, e, dataflowfacts.KeyPresencePlaceOfGet(e))
}

// KeyPresenceEstablishedForHas is the same ask for a `m.has(k)` READ:
// does a standing guard already establish this exact key present? A
// true answer makes the read exactly *true* (sec-map.prototype.has
// returns *true* for a key whose entry is in [[MapData]], which is what
// the fact records), rather than the two-word boolean an unpinned
// receiver would otherwise answer.
func KeyPresenceEstablishedForHas(env Env, e *ast.Node) bool {
	return keyPresenceHeldAt(env, e, dataflowfacts.KeyPresencePlaceOfHas(e))
}

// RecordKeyPresenceAfterWrite writes the presence fact a completed
// `m.set(k, v)` / `s.add(k)` establishes: after the write, an entry
// with this exact key is in [[MapData]]/[[SetData]]
// (sec-map.prototype.set appends or overwrites the matching record;
// sec-set.prototype.add likewise), which is the very fact a held
// `has(k)` guard records. Called AFTER whatever sweep the write's own
// model runs (UpdateTrackedEnv's ForgetPlaceEntriesEnv, or none on an
// untracked receiver), so the fresh fact is not the sweep's casualty.
// A receiver or key the place spelling cannot name writes nothing.
func RecordKeyPresenceAfterWrite(env Env, receiver *ast.Node, key *ast.Node) {
	place := dataflowfacts.KeyPresencePlace(receiver, key)
	if place == "" {
		return
	}
	env.Set(place, keyPresenceEstablished)
	if mirror := dataflowfacts.KeyPresenceMirrorPlace(receiver, key); mirror != "" {
		env.Set(mirror, keyPresenceEstablished)
	}
}

// DropKeyPresenceOnRemoval sweeps the presence facts a `delete`/
// `clear` invalidates, for the untracked arms whose model does not
// already sweep (the tracked ones go through UpdateTrackedEnv). The
// whole root sweeps EXCEPT the facts KeyPresenceSurvivesRemoval keeps:
// a literal-keyed fact provably different from a literal removed key —
// the one comparison the spellings themselves decide. Every other
// pairing sweeps, because a removal's key can name the same runtime
// entry as a standing fact under a DIFFERENT spelling — `m.has(k)`
// then `m.delete("a")` removes k's entry whenever k is "a". A nil
// removedKey (a `clear`, which removes everything) keeps nothing. A
// symbolic fact's mirror place (rooted at the key binding) may
// survive the root sweep, and harmlessly: the read requires both
// places.
func DropKeyPresenceOnRemoval(env Env, receiver *ast.Node, removedKey *ast.Node) {
	if receiver == nil || !ast.IsIdentifier(receiver) {
		return
	}
	prefix := receiver.Text() + "."
	var doomed []string
	env.Range(func(key string, _ abstractdomain.AbstractValue) bool {
		if strings.HasPrefix(key, prefix) &&
			!dataflowfacts.KeyPresenceSurvivesRemoval(key, receiver, removedKey) {
			doomed = append(doomed, key)
		}
		return true
	})
	for _, key := range doomed {
		env.Delete(key)
	}
}

// DropAllKeyPresenceFacts sweeps every presence-place entry from the
// environment — every `receiver.has(key)` guard fact, under whichever
// root it hangs. Called at an await and at a yield: both suspend the
// current job (sec-await; sec-generatoryield for a generator body), and
// anything reachable from outside this function — a Map/Set the
// function does not exclusively own — may be mutated by another job
// that runs while this one is suspended, exactly the TOCTOU the E5
// row's first function states. A held fact makes no claim about
// exclusive ownership today, so ownership is not distinguished here:
// every presence place is dropped, at the cost of a determination this
// walk could in principle keep for a receiver never handed elsewhere —
// never at the cost of soundness.
func DropAllKeyPresenceFacts(env Env) {
	var doomed []string
	env.Range(func(key string, _ abstractdomain.AbstractValue) bool {
		if strings.Contains(key, dataflowfacts.KeyPresencePlaceSegment) {
			doomed = append(doomed, key)
		}
		return true
	})
	for _, key := range doomed {
		env.Delete(key)
	}
}

// keyPresenceSurvivorsHeld collects the presence places currently held
// in the environment that survive a removal of removedKey — read
// BEFORE a tracked model's refresh, whose UpdateTrackedEnv sweep drops
// every dotted entry under the root, so the survivors can be written
// back after it (the sweep cannot tell a surviving fact from a stale
// one; this collector can, by KeyPresenceSurvivesRemoval's rule).
func keyPresenceSurvivorsHeld(env Env, receiver *ast.Node, removedKey *ast.Node) []string {
	if receiver == nil || !ast.IsIdentifier(receiver) {
		return nil
	}
	prefix := receiver.Text() + "."
	var survivors []string
	env.Range(func(key string, _ abstractdomain.AbstractValue) bool {
		if strings.HasPrefix(key, prefix) &&
			dataflowfacts.KeyPresenceSurvivesRemoval(key, receiver, removedKey) {
			survivors = append(survivors, key)
		}
		return true
	})
	return survivors
}

// keyPresenceHeldAt answers whether the named place — and, for a
// symbolic key, its mirror — still stands in the environment.
func keyPresenceHeldAt(env Env, e *ast.Node, place string) bool {
	if place == "" {
		return false
	}
	if _, held := env.Get(place); !held {
		return false
	}
	mirror := dataflowfacts.KeyPresenceMirrorPlaceOfCall(e)
	if mirror == "" {
		return true // a literal key depends on the map root alone
	}
	_, heldMirror := env.Get(mirror)
	return heldMirror
}

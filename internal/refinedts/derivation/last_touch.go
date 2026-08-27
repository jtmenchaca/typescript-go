// THE LAST-TOUCH LEDGER (DERIVATION-TRACE.md, "The projection rule" —
// the last-touch sibling of the binding ledger above).
//
// A trace leaf that declines, or answers an unspellable residue, over a
// plain-name construct names the binding behind it when one exists (the
// binding ledger, above). But a name can carry nothing for a reason that
// leaves NO producing subtree at all: an unmodeled call havocked it, a
// write through a path the walk could not place forgot its place
// entries, or a plain write replaced it outright. RecordTouch remembers
// the latest of those three kinds of write per place, so the leaf can
// still say WHERE the value was last touched even with no chain to
// offer.
//
// OFF COSTS NOTHING: the ledger exists only on a Recorder, and a
// Recorder exists only while a collector is installed — the same
// current()-nil-check every other ledger write makes.
package derivation

// The three last-touch kinds, spelled once. Exactly these three words.
const (
	TouchWritten   = "written"
	TouchForgotten = "forgotten"
	TouchHavocked  = "havocked"
)

// RecordTouch remembers the latest environment write to reach `place`:
// its kind, and — when the caller named one via TouchSite — the
// construct that did it and that construct's range. Recorded only
// while a recorder is active; off is a nil test and costs nothing.
func RecordTouch(place string, kind string, construct string, rng string) {
	recorder := current()
	if recorder == nil || place == "" || kind == "" {
		return
	}
	if recorder.touches == nil {
		recorder.touches = map[string]lastTouch{}
	}
	recorder.touches[place] = lastTouch{kind: kind, construct: construct, rng: rng}
}

// touchSite is the construct/range the walk's caller most recently
// named for the mutation about to run, on this recorder. It is READ
// once by the chokepoint the mutation reaches (HavocEnv,
// UpdateTrackedEnv, ForgetPlaceEntriesEnv) and is not cleared by that
// read — TouchSite's own closer clears it, so a call tree that fans
// out to several chokepoints under one named site (HavocEnv's own call
// to ForgetPlaceEntriesEnv, for instance) tags all of them with the
// same site.
type touchSite struct {
	construct string
	rng       string
}

// TouchSite names the construct/range the mutation about to run
// belongs to, for every last-touch chokepoint reached before the
// returned closer runs. A walk call site that holds the mutating node
// (readUnmodeledMethod's ForgetThrough call, a collection write arm, an
// argument havoc) wraps its mutation:
//
//	closeSite := derivation.TouchSite(derivation.Construct(node), derivation.Range(node))
//	defer closeSite()
//	HavocEnv(ctx.Aliases, env, name)
//
// A chokepoint reached with no site set (TouchSite never called on the
// path that reached it) records the touch with empty construct/range
// rather than guessing — the honest reading when the walk has not been
// taught to name that seam yet.
//
// Nested calls: the inner TouchSite's closer restores whatever site was
// active before it, so a chokepoint reached through two nested named
// sites is tagged by the innermost — the one actually holding the
// mutating node.
func TouchSite(construct string, rng string) func() {
	recorder := current()
	if recorder == nil {
		return func() {}
	}
	previous := recorder.site
	recorder.site = &touchSite{construct: construct, rng: rng}
	return func() {
		recorder.site = previous
	}
}

// currentTouchSite is the construct/range a chokepoint reads when it
// records a touch — empty when no TouchSite is active.
func (r *Recorder) currentTouchSite() (construct string, rng string) {
	if r == nil || r.site == nil {
		return "", ""
	}
	return r.site.construct, r.site.rng
}

// RecordTouchFromSite is the chokepoint helper: record `kind` for
// `place` using whatever site TouchSite most recently named on this
// recorder, or empty construct/range when none is active. This is what
// HavocEnv, ForgetPlaceEntriesEnv, and UpdateTrackedEnv call — they
// hold no node of their own, so they read whatever their caller named.
func RecordTouchFromSite(place string, kind string) {
	recorder := current()
	if recorder == nil {
		return
	}
	construct, rng := recorder.currentTouchSite()
	RecordTouch(place, kind, construct, rng)
}

// LastTouchOf is the last-touch attribute value for `place`, spelled
// exactly `<kind> by <construct>  @<range>` — construct and range
// omitted (just `<kind>`) when the recorded touch named no site. Empty
// when the place was never touched at all.
func LastTouchOf(place string) string {
	recorder := current()
	if recorder == nil || place == "" {
		return ""
	}
	touch, held := recorder.touches[place]
	if !held {
		return ""
	}
	return spellLastTouch(touch)
}

func spellLastTouch(touch lastTouch) string {
	if touch.construct == "" {
		return touch.kind
	}
	return touch.kind + " by " + touch.construct + "  @" + touch.rng
}

// attachLastTouch stamps refinery.last-touch on a trace's leaf, at the
// moment the root closes — the same trace-assembly point ChainFor runs
// at, and the same bare-name leaf test BareNameLeafOf uses (a childless
// span, declined or answering unspellable, whose construct is a plain
// identifier). A leaf with no recorded touch for that name, or a leaf
// that is not a bare name at all, gets no attribute — this rides every
// emitted document without the renderer needing a change, since the
// text projection enumerates AttrLastTouch alongside the other
// declined-span attributes and the JSON encoder marshals the whole
// Attributes map either way.
func (r *Recorder) attachLastTouch(root *Span) {
	if r == nil || root == nil {
		return
	}
	leaf := DeepestDeclined(root)
	if leaf == nil {
		leaf = deepestUnspellableAnswer(root)
	}
	if leaf == nil || len(leaf.Children) > 0 {
		return
	}
	name := leaf.Attributes[AttrConstruct]
	if !isPlainIdentifier(name) {
		return
	}
	spelled := LastTouchOf(name)
	if spelled == "" {
		return
	}
	leaf.Attributes[AttrLastTouch] = spelled
}

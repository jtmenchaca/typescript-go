// THE BINDING LEDGER (DERIVATION-TRACE.md, "The projection rule").
//
// A read whose derivation stops at a bare name has nothing below it: the
// name was resolved out of the environment, and the construct that
// actually produced the value sits at the binding statement, on another
// line and outside the judged position's range. The judge's own reclaim
// (AbsorbInto) works by range containment and cannot reach there; the
// guard ledger holds narrowings, not producers; and nothing re-evaluates
// an initializer at the read.
//
// So the producing subtree is remembered where it is WRITTEN — every
// environment write, keyed by the place written — and reclaimed where it
// is READ, into the document's `chain`. A chained root whose own leaf is
// again a bare name reclaims recursively, nearest binding first, cycles
// refused by place.
//
// THE CHAIN SHARPENS THE WORK ITEM, NEVER THE SENTENCE. The projection
// still reads the MAIN root's deepest declined span, exactly as before —
// `chain` is an additional root under the same document, never merged
// into the main tree.
//
// OFF COSTS NOTHING: the ledger exists only on a Recorder, and a
// Recorder exists only while a collector is installed.

package derivation

// RecordBinding remembers the span subtree that produced the value
// written into `place`. Called from the walk's environment writes
// (walk/assignments.go WriteBinding and its siblings) with the range of
// the node that produced the value — the initializer, the right-hand
// side, the destructured element.
//
// The producing subtree has ALREADY CLOSED by the time the write runs:
// evaluateExpression opened, closed, and finished its own trace before
// WriteBinding was called. So the span is found among the recorder's
// finished traces by range — the same reclaim shape AbsorbInto uses,
// except that the ledger only REMEMBERS the span here and leaves the
// trace in place; a read reclaims it later, and a place never read
// costs one map entry.
//
// A write with no producing span (a parameter bound at entry, a value
// the walk never opened a span for) records nothing, and a later read of
// that place legitimately carries an empty chain.
func RecordBinding(place string, producedRange string) {
	recorder := current()
	if recorder == nil || place == "" || producedRange == "" {
		return
	}
	span := recorder.finishedRootAt(producedRange)
	if span == nil {
		return
	}
	if recorder.bindings == nil {
		recorder.bindings = map[string]*Span{}
	}
	recorder.bindings[place] = span
}

// finishedRootAt is the most recently closed trace root whose range is
// exactly the given one — the producing span for a write at that node.
// Read from the end so a place written twice on the same range remembers
// the latest write.
func (r *Recorder) finishedRootAt(rng string) *Span {
	for at := len(r.finished) - 1; at >= 0; at-- {
		root := r.finished[at].Root
		if root != nil && root.Attributes[AttrRange] == rng {
			return root
		}
	}
	return nil
}

// BareNameLeafOf is the place a span tree's derivation stopped at, or ""
// when it did not stop at a bare name.
//
// "Stopped at a bare name" is decided by the same leaf the projection
// reads — the deepest declined span — plus two tests it must pass: it
// opened no child of its own (nothing below it was derived), and its
// construct is a plain identifier. A leaf that declined on a compound
// construct (`x[0]`, `a + b`) named the construct that blocked it, and
// there is no binding behind that to chain.
func BareNameLeafOf(root *Span) string {
	leaf := DeepestDeclined(root)
	if leaf == nil {
		// a derivation can bottom out ANSWERING a residue rather than
		// declining: evaluateExpression resolves a bare name out of the
		// environment and answers the unreadable value it holds, with no
		// decline anywhere in the tree. The construct that produced that
		// residue still sits at the binding statement, so such a leaf
		// chains exactly as a declined one does — without this, the
		// -explain of a judged `return scaled` stopped at
		// "answer = unspellable" and named nothing to fix.
		leaf = deepestUnspellableAnswer(root)
	}
	if leaf == nil || len(leaf.Children) > 0 {
		return ""
	}
	name := leaf.Attributes[AttrConstruct]
	if !isPlainIdentifier(name) {
		return ""
	}
	return name
}

// deepestUnspellableAnswer is the deepest childless span that ANSWERED
// the unspellable residue — the answered twin of DeepestDeclined, read
// last-child-first the same way so the latest degradation wins.
func deepestUnspellableAnswer(span *Span) *Span {
	if span == nil {
		return nil
	}
	for at := len(span.Children) - 1; at >= 0; at-- {
		if found := deepestUnspellableAnswer(span.Children[at]); found != nil {
			return found
		}
	}
	if len(span.Children) == 0 && span.Attributes[AttrAnswer] == "unspellable" {
		return span
	}
	return nil
}

// isPlainIdentifier answers whether a construct spelling is a bare name —
// an ECMAScript identifier and nothing else. A compound construct fails
// on its first non-identifier byte.
func isPlainIdentifier(text string) bool {
	if text == "" {
		return false
	}
	for at := 0; at < len(text); at++ {
		c := text[at]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == '$':
		case c >= '0' && c <= '9' && at > 0:
		default:
			return false
		}
	}
	return true
}

// ChainFor is the binding-ledger roots behind a trace whose leaf is a
// bare name: the derivation of that name's binding statement, then — when
// THAT root's own leaf is again a bare name — the derivation behind it,
// and so on. Nearest binding first.
//
// CYCLES ARE REFUSED BY PLACE. `let a = b; let b = a;` and every loop
// carry-around writes each place from the other, so a chain walked by
// place alone would not terminate. A place already chained is not
// chained again, and the walk stops there.
//
// The reclaimed spans stop being traces in their own right — a binding's
// derivation belongs to the position that read it, not beside it as a
// second judged position — so the roots are removed from `finished` and
// shed the root-only attributes, exactly as AttachGuard does.
func (r *Recorder) ChainFor(root *Span) []*Span {
	if r == nil || root == nil {
		return nil
	}
	var chain []*Span
	chained := map[string]struct{}{}
	claimed := map[*Span]struct{}{}
	from := root
	for {
		place := BareNameLeafOf(from)
		if place == "" {
			break
		}
		if _, already := chained[place]; already {
			break
		}
		chained[place] = struct{}{}
		bound := r.bindings[place]
		if bound == nil {
			break
		}
		// a span already in this chain, or already inside the main root,
		// is not added twice — the same refusal spanPresent makes for
		// guards
		if _, held := claimed[bound]; held || spanPresent(root, bound) {
			break
		}
		claimed[bound] = struct{}{}
		r.claimRoot(bound)
		chain = append(chain, bound)
		from = bound
	}
	return chain
}

// claimRoot drops a span from `finished` and sheds the root-only
// attributes, for a span that a judged position has claimed as part of
// its own derivation.
func (r *Recorder) claimRoot(span *Span) {
	kept := r.finished[:0]
	for _, trace := range r.finished {
		if trace.Root == span {
			delete(span.Attributes, AttrLanguage)
			delete(span.Attributes, AttrPosition)
			continue
		}
		kept = append(kept, trace)
	}
	r.finished = kept
}

// split from ir_summary_body_lowering.go — the havoc vector wiring

package walk

// wireSummaryCaptureHavoc fills the context's havoc vector — the slots
// no statement that runs code may believe across — and notes the one
// whole-expansion porosity.
func wireSummaryCaptureHavoc(
	context *LoweringContext,
	bundle thisBundleLayout,
	layout summaryEntryLayout,
	captures []capturedSlot,
) {
	// an ESCAPING receiver is the ONE whole decline of the expansion, and
	// it is POROUS rather than declined: the body still lowers, its
	// this-reads simply find no slot and hit the opaque floor. The note
	// goes in before the statements lower so it names the earliest reason
	// the body stopped being read whole — the receiver left the lowering's
	// sight before any statement could.
	if bundle.Escaped {
		NoteFirstHavoc(context, "this escapes")
	}
	// a method-calling capture's havoc set, resolved to slot indices —
	// non-empty puts the statement walk in havoc mode. A havocked FIELD with
	// no slot (a write-only field the layout gave no entry) needs none:
	// no slot means no belief to invalidate.
	for _, havocName := range bundle.CaptureHavocNames {
		if slot, held := slotIndexOfName(context, havocName); held {
			context.CaptureHavocSlots = append(context.CaptureHavocSlots, slot)
		}
	}
	// a HANDED-OVER record parameter's leaves ride the same vector: the
	// body passed the whole object to code, so any code-running statement
	// may have moved every leaf and none of them may be believed across
	// one. The rows also went out Written, so the caller takes the moved
	// values back through the call statement's rets rather than keeping
	// what it sent.
	for _, havocName := range layout.HandOverHavocNames {
		if slot, held := slotIndexOfName(context, havocName); held {
			context.CaptureHavocSlots = append(context.CaptureHavocSlots, slot)
		}
	}
	// A METHOD CALL ON AN OBJECT CAPTURE — `disconnectSource.removeListener(…)`,
	// `response.end()` — rides the same vector, and this is where the sound
	// reading is written down.
	//
	// WHY HAVOC AND NOT A REFUSAL. The caller's leaf slots are a CLOSED set:
	// the caller flattened exactly these members and believes nothing about
	// the object beyond them. So "the callee may have moved any member" is,
	// for the caller, exactly "every leaf of this capture moved" — a
	// statement the row vocabulary can make, because every leaf already has
	// an entry and a row. Havocking them inside the summary makes the
	// summary's own walk stop believing them from that statement on, and
	// each havocked leaf goes out Written (closureCapturesOf marks it), so
	// the caller reads the exit rather than keeping the value it sent.
	// Refusing instead would be sound too, and strictly weaker: the whole
	// closure would fall back to the write-set havoc, which forgets the same
	// leaves AND every other slot the closure touches.
	//
	// A member the callee writes that the caller never flattened moves
	// nothing anyone believes — no slot on either side spells it — which is
	// what makes the closed set enough.
	//
	// WHERE THE CALLEE RESOLVES AND SAYS UNTOUCHED, nothing is havocked:
	// receiverWritten answers from the callee's own summary
	// (SummaryReceiverEffects), and a body that lowered and wrote no
	// this-field and returned no receiver moved no member of the object it
	// was called on. An unresolved callee, or one whose body declined to
	// lower, answers written — the doubt direction — and takes the havoc.
	for _, capture := range captures {
		if len(capture.Members) == 0 || len(capture.MethodCalls) == 0 {
			continue
		}
		moves := false
		for _, method := range capture.MethodCalls {
			if capture.MethodWrites == nil {
				moves = true
				break
			}
			if _, written := capture.MethodWrites[method]; written {
				moves = true
				break
			}
		}
		if !moves {
			continue
		}
		for _, leaf := range capture.Members {
			if slot, held := layout.CaptureLeafSlots[capture.Name+"."+leaf.Member]; held {
				context.CaptureHavocSlots = append(context.CaptureHavocSlots, slot)
			}
		}
	}
}

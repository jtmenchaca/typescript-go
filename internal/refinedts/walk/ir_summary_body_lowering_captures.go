// split from ir_summary_body_lowering.go — the capture and this-bundle entries

package walk

// appendSummaryCaptureEntries rides the captures as EXTRA entries
// immediately after the declared parameters, in the scan's own order —
// the call site fills entry len(parameters)+j with a var of the caller
// slot capture j resolved to, so the two orders must agree exactly. A
// capture whose name a parameter already claims is the parameter's, not
// the capture's: the inner binding shadows, and the scan never reported
// it free.
//
// A WRITTEN capture takes one more thing: a bundle row, so the call
// site maps its exit back onto the caller's own slot. THE ROW AND THE
// ENTRY ARE ALLOCATED IN ONE STEP HERE, and that is the lockstep
// discipline the whole write-back machinery rests on — the row's
// Index is `len(layout.Names)` read at the moment the entry is
// appended, so the row can never name a position the entry did not
// take. Every other bundle family in this layout (the record leaves
// before, the this-fields after) allocates the same way for the same
// reason: one allocator, and every seam that reads the rows reads
// what this loop wrote rather than re-deriving an index of its own.
//
// An OBJECT capture takes the SAME step, once per leaf: the leaf's
// row and the leaf's entry are appended together, so a leaf row can
// never name a position its entry did not take either. The lockstep
// argument is the whole argument, and extending it to leaves is
// extending the argument rather than adding a second one — the loop
// below still writes `len(layout.Names)` at the moment of the append,
// and there is still exactly one allocator.
func appendSummaryCaptureEntries(
	captures []capturedSlot,
	layout *summaryEntryLayout,
) (declined string, ok bool) {
	declaredCount := len(layout.Names)
	layout.CaptureLeafSlots = map[string]int{}
	for _, capture := range captures {
		shadowed := false
		for _, name := range layout.Names[:declaredCount] {
			if name == capture.Name {
				shadowed = true
				break
			}
		}
		if shadowed {
			return "a capture shadowed by a parameter", false
		}
		if len(capture.Members) > 0 {
			for _, leaf := range capture.Members {
				// the ENTRY is spelled the way the BODY reads the leaf —
				// "stream.writableEnded", which is what SpelledNameOf answers
				// for the member access, so the statement walk resolves it
				// through slotIndexOfName like any other slot. The ROW is
				// spelled "#capture.stream.writableEnded", because a row is
				// read at the CALL SITE, where "stream.writableEnded" is
				// already the caller's own leaf and the two must not collide.
				// The scalar capture rows keep the same two spellings for the
				// same reason.
				spelled := capture.Name + "." + leaf.Member
				if leaf.Written {
					layout.BundleEntries = append(layout.BundleEntries, BundleEntry{
						Path:    capturedLeafSlotName(capture.Name, leaf.Member),
						Index:   len(layout.Names),
						Written: true,
					})
				}
				layout.CaptureLeafSlots[spelled] = len(layout.Names)
				layout.Names = append(layout.Names, spelled)
				layout.Sorts = append(layout.Sorts, leaf.Sort)
				layout.Typeofs = append(layout.Typeofs, leaf.TypeofTag)
			}
			continue
		}
		if capture.Written {
			layout.BundleEntries = append(layout.BundleEntries, BundleEntry{
				Path:    capturedSlotName(capture.Name),
				Index:   len(layout.Names),
				Written: true,
			})
		}
		layout.Names = append(layout.Names, capture.Name)
		layout.Sorts = append(layout.Sorts, capture.Sort)
		layout.Typeofs = append(layout.Typeofs, capture.TypeofTag)
	}
	return "", true
}

// appendSummaryThisBundleEntries rides the `this` bundle as EXTRA
// entries after the declared parameters — the same ground the arrow
// route's captures take, which is why the two are EXCLUSIVE: only a
// method has a this bundle, and only an arrow or function expression
// carries captures, so no declaration ever lays out both. The assertion
// states it rather than leaving the two silently sharing indices.
func appendSummaryThisBundleEntries(
	bundle thisBundleLayout,
	captureCount int,
	layout *summaryEntryLayout,
) (declined string, ok bool) {
	if bundle.Expanded && captureCount > 0 {
		return "a this bundle beside arrow captures", false
	}
	for _, entry := range bundle.Entries {
		_, isWritten := bundle.Written[entry.Name]
		layout.BundleEntries = append(layout.BundleEntries, BundleEntry{
			Path:    entry.Name,
			Index:   len(layout.Names),
			Written: isWritten,
		})
		layout.Names = append(layout.Names, entry.Name)
		layout.Sorts = append(layout.Sorts, entry.Sort)
		layout.Typeofs = append(layout.Typeofs, entry.TypeofTag)
	}
	return "", true
}

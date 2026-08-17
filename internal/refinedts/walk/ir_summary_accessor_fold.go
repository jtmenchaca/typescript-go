// split from ir_summary_this_bundle_layout.go — the accessor census fold

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// accessorCensusFold resolves a census's deferred accessor names —
// AccessorStores to the class's own SET accessors, AccessorReads to its
// GET accessors — and answers the declared fields those bodies read and
// write, transitively through further accessor use. The consumer lays
// the reads and writes out as slots, which is what lets the accessor
// call statements embedded in the body (GetterReadEffect /
// SetterWriteStatements) thread their entry and written rows against
// real positions instead of dropping them.
//
// `over.write(200)` running `this.#age = value` is the shape this
// exists for: the setter's body writes `this.#held`, a field `write`'s
// own text never spells, so without the fold `write`'s layout held no
// slot for it, the setter's write-back mapped to no row, and `write`'s
// COMPLETE summary told its callers the receiver was untouched — the
// caller's stale `#held` knowledge survived a write that changed it.
//
// The refusals, each answering (nil, nil, false) — exactly the escape
// these occurrences were before the census learned to defer them:
//
//   - a name resolving to no matching accessor of this class (a static
//     accessor, an inherited one, a plain missing member);
//   - an accessor with no body (declare / overload shapes);
//   - an accessor body whose own census escapes, computes a write, or
//     calls methods (directly or through a capture) — the fold carries
//     no havoc machinery, so a body needing one refuses;
//   - an accessor returning `this`.
//
// Further accessor use inside an accessor body joins the worklist, with
// a visited set so mutually-recursive accessors terminate.
func accessorCensusFold(
	classLike *ast.Node,
	fields []BundleField,
	census FieldCensus,
) (reads []BundleField, writes []BundleField, ok bool) {
	if len(census.AccessorStores) == 0 && len(census.AccessorReads) == 0 {
		return nil, nil, true
	}
	if classLike == nil || !ast.IsClassLike(classLike) {
		return nil, nil, false
	}
	spelled := BundleFieldsAs("this", fields)
	type accessorAsk struct {
		name    string
		isStore bool
	}
	worklist := make([]accessorAsk, 0, len(census.AccessorStores)+len(census.AccessorReads))
	for _, name := range census.AccessorStores {
		worklist = append(worklist, accessorAsk{name: name, isStore: true})
	}
	for _, name := range census.AccessorReads {
		worklist = append(worklist, accessorAsk{name: name})
	}
	visited := map[accessorAsk]struct{}{}
	readNames := map[string]struct{}{}
	writeNames := map[string]struct{}{}
	for len(worklist) > 0 {
		ask := worklist[0]
		worklist = worklist[1:]
		if _, seen := visited[ask]; seen {
			continue
		}
		visited[ask] = struct{}{}
		declaration := accessorMemberOf(classLike, ask.name, ask.isStore)
		if declaration == nil {
			return nil, nil, false
		}
		body := declaration.Body()
		if body == nil {
			return nil, nil, false
		}
		sub := FieldCensusOf(body, "this", spelled)
		if sub.Escapes || sub.ComputedWrite || sub.ReturnsSelf ||
			len(sub.CapturedMethodCalls) > 0 || len(sub.DirectMethodCalls) > 0 {
			return nil, nil, false
		}
		for _, field := range sub.Reads {
			readNames[field.Name] = struct{}{}
		}
		for _, field := range sub.Writes {
			writeNames[field.Name] = struct{}{}
		}
		for _, name := range sub.AccessorStores {
			worklist = append(worklist, accessorAsk{name: name, isStore: true})
		}
		for _, name := range sub.AccessorReads {
			worklist = append(worklist, accessorAsk{name: name})
		}
	}
	// back to the field set's own declaration order — the order every
	// slot vector is built in — and under the census's own "this."
	// slot spelling, which is what the layout's written map and entry
	// names hold
	for _, field := range spelled {
		if _, read := readNames[field.Name]; read {
			reads = append(reads, field)
		}
	}
	for _, field := range spelled {
		if _, wrote := writeNames[field.Name]; wrote {
			writes = append(writes, field)
		}
	}
	return reads, writes, true
}

// accessorMemberOf finds the class's own non-static get/set accessor
// declaration named `name` — a plain or private identifier spelling —
// or nil where none matches.
func accessorMemberOf(classLike *ast.Node, name string, isStore bool) *ast.Node {
	for _, member := range classLike.ClassLikeData().Members.Nodes {
		if isStore && !ast.IsSetAccessorDeclaration(member) {
			continue
		}
		if !isStore && !ast.IsGetAccessorDeclaration(member) {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		memberName := member.Name()
		if memberName == nil ||
			(!ast.IsIdentifier(memberName) && !ast.IsPrivateIdentifier(memberName)) ||
			memberName.Text() != name {
			continue
		}
		return member
	}
	return nil
}

// Arrow callbacks, closure-converted: a collection method whose
// callback is an arrow argument lowers as ONE call statement applying
// the arrow's own compiled summary.
//
// The census that motivated this: collection callbacks capture `this`
// and outer locals READ-ONLY. Both convert without any new kernel form,
// because a capture is nothing but an extra ENTRY of the arrow's
// summary. An arrow with parameters p₀…pₙ and captures c₀…cₘ compiles
// to a summary of arity n+m+1+locals, whose entries are the parameters
// first and the captures immediately after; the call site fills the
// parameter entries from the receiver's element slot and the capture
// entries with a `var` of the caller slot each capture name resolves
// to. Nothing kernel-side learns that some entries came from a closure.
//
// What CONVERTS — an arrow (or function expression) argument whose free
// names are only:
//
//   - its own parameters;
//   - `this`-rooted method calls (`this.m(…)`) that resolve through the
//     lowering context's ResolveCallee exactly as any other call does —
//     the receiver is not a value the arrow reads, only a name the
//     resolution consumes;
//   - READ-ONLY captures of caller locals that HAVE a slot.
//
// What DECLINES:
//
//   - a captured WRITE (`total += x`, `seen[k] = 1`, `i++` on an outer
//     name) — the write would move the caller's state and an entry
//     carries nothing back;
//   - a capture with no slot in the enclosing lowering — there is no
//     `var` to bind the entry to;
//   - a free name that is neither parameter, capture-with-slot, nor
//     `this` — a module import, a global, an outer function;
//   - a nested function inside the arrow, and a `this` used as a VALUE
//     rather than as a call receiver;
//   - a body the ordinary summary lowering declines.
//
// ASYNC arrows convert exactly like sync ones. A lowered async body's
// #ret holds the SETTLED inner value (the ret-as-inner convention), so
// `xs.map(async cb)` and `xs.map(cb)` lower identically and the await
// over the result adds nothing.
//
// A callback does NOT have to be spelled inline. A callback argument
// that NAMES a function — `xs.forEach(handler)`, `xs.map(this.render)`
// — converts on exactly the arrow's terms, with the named declaration's
// own parameters and body standing where the arrow's would (see
// callbackFunctionOf). What the reference costs is the receiver: a
// method handed over BARE loses its binding, so at the traversal's call
// the runtime `this` is undefined rather than the object the method was
// read off. The rule that keeps that honest is the narrowest sound one
// — a referenced body that mentions `this` anywhere DECLINES, whatever
// the spelling it was reached through, and a body free of `this` cannot
// tell the difference between being called bare and being called on its
// owner, so it converts.
//
// The recognized statement forms — each total-or-decline. The array
// forms stand over a FLATTENED array receiver whose "xs.len"/"xs.elem"
// slots resolve:
//
//	ys = xs.map(cb)     → ys.len := var xs.len; call cb at xs.elem → ys.elem
//	xs.forEach(cb)      → call cb at xs.elem, no ret
//	ys = xs.filter(cb)  → ys.elem := var xs.elem; ys.len := integer ≥ 0
//	ys = xs.find(cb)    → call cb at xs.elem with no ret (the predicate's
//	                      own answer is not what find returns), then
//	                      ys := xs.elem OR-ABSENT — find hands back an
//	                      element the array held, or undefined where no
//	                      element passed, and the or-absent effect is
//	                      exactly that pair
//	ys = xs.reduce(cb, seed)  → call cb with the ACCUMULATOR entry first
//	                      and the element second (reduce shifts every
//	                      slot one over, which is the same shift
//	                      ArrayCallbackPins spells), ret → ys. The seed
//	                      fills the accumulator entry; the FOLD is not
//	                      unrolled — the entry is the JOIN of the seed
//	                      and cb's own ret, which covers the accumulator
//	                      at every pass, the same join-of-elements
//	                      argument the element slot already rides
//	ys = xs.flatMap(cb) → call cb at xs.elem for its EFFECTS, ys := unknown
//	                      — flatMap's result is the CONCATENATION of cb's
//	                      per-element arrays, and the two-slot flattening
//	                      has no spelling for an array of arrays flattened
//	                      one level. The callback still converts, which is
//	                      the point: an effectful or unconvertible cb
//	                      declines the statement instead of passing
//	                      unread, and the result honestly answers nothing
//	ys = await Promise.all(xs.map(cb))  → the map lowering above
//	xs.find(cb); / xs.map(cb); / xs.reduce(cb, seed);  → the RESULT
//	                      DISCARDED: one call statement at the element
//	                      entry with no ret, exactly what forEach emits.
//	                      The traversal runs, so the callback runs, and a
//	                      callback that does not convert declines the
//	                      statement rather than passing unread. reduce
//	                      keeps its shifted layout with the accumulator
//	                      entry absent — no target slot means nothing for
//	                      that entry to join against
//
// over a FLATTENED Map or Set whose "m.size"/"m.vals"(/"m.keys") family
// resolves:
//
//	m.forEach(cb)       → call cb at m.vals (and m.keys second, for a
//	                      Map; the value again for a Set), no ret
//
// and over a PROMISE-HELD local whose "p.inner" slot the await lowering
// allocated:
//
//	p.then(cb)          → call cb at p.inner, no ret
//	q = p.then(cb)      → the same, ret → a fresh "q.inner" slot, which
//	                      makes q a promise-held local a later `await q`
//	                      reads back
//
// The join-of-elements argument is what makes one call statement cover
// a whole traversal: the element (or values) slot holds the JOIN of
// everything the collection can hold, cb's summary quantifies over ALL
// entries, so applying it at the join covers cb's image of every
// individual element. One application, every iteration.
//
// An array callback's SECOND parameter is the INDEX, and it enters the
// integer-≥0 constant set rather than absent: every concrete index is a
// non-negative integer, so the entry is honest, and it is what lets
// `(x, i) => x + i` convert instead of declining on the `i` read. The
// THIRD parameter — the collection itself — stays absent, since no slot
// holds a whole array or a whole Map.
package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// arrowBlobEntry is one arrow's compiled answer. Ok false is a
// remembered decline, the same discipline summary_registry.go keeps for
// declarations.
type arrowBlobEntry struct {
	Blob       kernelbridge.SummaryBlob
	Lowered    LoweredSummary
	Parameters []parameterSlotSort
	Captures   []capturedSlot
	Ok         bool
}

// arrowBlobs keys per-SITE blobs by the arrow's own node. An arrow is
// not a declaration the contract registry knows — it exists at exactly
// one call site and its capture layout belongs to the body that spells
// it — so it is deliberately NOT stored in the declaration-keyed
// registry. Node identity is the key: the same arrow node re-lowered
// (a body compiled twice) answers the blob it already built.
var (
	arrowBlobsMu sync.Mutex
	arrowBlobs   = map[*ast.Node]arrowBlobEntry{}
)

// arrowFunctionOf is the arrow or function expression an argument is,
// through parens and casts — or nil. A function EXPRESSION converts on
// the same terms an arrow does; what matters is that the body is
// lowerable and the free names are captures, not which spelling the
// source used. (A function expression's own `this` differs at runtime,
// which is why a `this` used as a call receiver still has to resolve
// through ResolveCallee rather than being assumed.)
func arrowFunctionOf(argument *ast.Node) *ast.Node {
	if argument == nil {
		return nil
	}
	head := Unwrapped(argument)
	if ast.IsArrowFunction(head) || ast.IsFunctionExpression(head) {
		return head
	}
	return nil
}

// mentionsThis is whether a subtree reads `this` ANYWHERE — as a value,
// as a call receiver, as a property root. Unlike scanFreeNames, which
// lets `this.m(…)` pass because the receiver is consumed by the callee
// resolution, this one admits no position at all: it answers the
// question a REFERENCE asks, which is whether the body would notice
// being called with a different receiver, and a `this.m(…)` call would
// notice as surely as a `this.field` read.
//
// A nested function is walked into as well, and that over-reports: an
// inner function expression's own `this` is its own. The over-report
// only ever declines more, which is the safe direction here, and the
// free-name scan declines nested functions outright anyway.
func mentionsThis(node *ast.Node) bool {
	if node == nil {
		return false
	}
	found := false
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if found {
			return true
		}
		if child.Kind == ast.KindThisKeyword {
			found = true
			return true
		}
		child.ForEachChild(visit)
		return false
	}
	visit(node)
	return found
}

// referencedFunctionOf is the declaration a callback argument NAMES,
// where the argument is a bare identifier (`xs.forEach(handler)`) or a
// `this.<name>` property read (`xs.forEach(this.onItem)`), resolved
// through the lowering context's own ResolveCallee — the same
// resolution the call routes use, so a name never stands for one
// declaration here and another there.
//
// The RECEIVER RULE, and why it is the one below. Handing a method over
// by name does not hand its receiver over with it: `xs.forEach(this.f)`
// calls f with `this` undefined (or the traversal's own thisArg), not
// with the object f was read off. A conversion that lowered f's body as
// though `this` were still bound would read the caller's this-bundle
// slots for values the run never sees there — a wrong answer, not a
// weak one.
//
// So the rule is a body census rather than a spelling census: a
// referenced body that mentions `this` in ANY position declines, naming
// a method reference losing its receiver. A body with no `this` at all
// cannot observe which receiver it was called on, so the bare call and
// the bound call agree on every value, and the conversion is sound
// whether the name was reached as a free function, an arrow held in a
// const, or a method read off `this`. Free functions and const-held
// arrows pass this census by construction, which is why they are the
// shapes that convert in practice.
//
// A body-less declaration, a generator, and an overridden method all
// answer nil through summaryLowerable and ResolveCallee's own gates.
func referencedFunctionOf(context *LoweringContext, argument *ast.Node) *ast.Node {
	if context == nil || context.ResolveCallee == nil || argument == nil {
		return nil
	}
	head := Unwrapped(argument)
	if head == nil {
		return nil
	}
	// the two reference spellings a slot-shaped lowering can name: a bare
	// identifier, and one `this.` step. Anything deeper (`a.b.f`) or
	// computed (`a[k]`) is left alone — ResolveCallee would answer for
	// the property name, and the extra steps are receiver structure this
	// route makes no claim about.
	switch {
	case ast.IsIdentifier(head):
	case ast.IsPropertyAccessExpression(head):
		access := head.AsPropertyAccessExpression()
		if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Name()) {
			return nil
		}
		if Unwrapped(access.Expression).Kind != ast.KindThisKeyword {
			return nil
		}
	default:
		return nil
	}
	declaration := context.ResolveCallee(head)
	if declaration == nil || !summaryLowerable(declaration) {
		return nil
	}
	// a method reference losing its receiver: the body would read a
	// `this` the bare call does not supply
	if mentionsThis(declaration.Body()) {
		return nil
	}
	return declaration
}

// callbackFunctionOf is the function a callback argument stands for,
// whichever way it was spelled: the inline arrow or function expression
// arrowFunctionOf reads, or the declaration a name refers to. Every
// route in this file that used to ask arrowFunctionOf asks this
// instead, so an inline arrow and a named reference convert on one set
// of terms rather than two.
//
// The answer is a node with .Parameters() and .Body(), which is all the
// conversion below reads — lowerArrowSummary lowers a function
// declaration through the same door it lowers an arrow, and the blob
// cache keys on the node either way. A referenced declaration reached
// from two different traversals is ONE node, so the cache's
// layout-agreement check (sameParameterSorts / sameCaptures) is what
// keeps two sites from sharing a blob they disagree about — the same
// protection an arrow gets, now doing real work, since a named function
// genuinely can be passed to two collections of different element sorts.
func callbackFunctionOf(context *LoweringContext, argument *ast.Node) *ast.Node {
	if arrow := arrowFunctionOf(argument); arrow != nil {
		return arrow
	}
	return referencedFunctionOf(context, argument)
}

// arrowParameterNames is an arrow's declared parameter names, or
// declines: a default, a rest, or a binding pattern is not one entry
// the call site can fill.
func arrowParameterNames(arrow *ast.Node) ([]string, bool) {
	var names []string
	for _, parameter := range arrow.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if !ast.IsIdentifier(pd.Name()) || pd.Initializer != nil || pd.DotDotDotToken != nil {
			return nil, false
		}
		names = append(names, pd.Name().Text())
	}
	return names, true
}

// arrowBoundNames is every name bound INSIDE the arrow's body: its
// parameters, and every local it declares (including the names an
// object binding pattern binds). A read of one of these is not a free
// name, so the capture scan skips it.
func arrowBoundNames(arrow *ast.Node) map[string]struct{} {
	bound := map[string]struct{}{}
	for _, parameter := range arrow.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if ast.IsIdentifier(pd.Name()) {
			bound[pd.Name().Text()] = struct{}{}
		}
	}
	body := arrow.Body()
	if body == nil {
		return bound
	}
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsVariableDeclaration(node) || ast.IsBindingElement(node) {
			name := node.Name()
			if name != nil && ast.IsIdentifier(name) {
				bound[name.Text()] = struct{}{}
			}
		}
		// a for-of / for-in element binding and a catch clause's parameter
		// arrive through the same declaration nodes above; nothing else
		// binds a plain name in the lowered subset
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return bound
}

// writtenNamesIn is every name the subtree WRITES: an assignment's
// left side, a compound assignment's, and a step's operand — each read
// through its spelled head, so `total += 1` reports "total" and
// `o.k = 1` reports "o".
//
// It walks into nested functions too, which over-reports: a write to a
// NESTED function's own local would be read as a captured write here.
// The over-report changes no outcome — a nested function declines the
// arrow outright in the scan below — and erring toward decline is the
// safe direction for a rule whose whole job is to catch writes.
func writtenNamesIn(node *ast.Node) map[string]struct{} {
	written := map[string]struct{}{}
	note := func(target *ast.Node) {
		head := Unwrapped(target)
		for ast.IsPropertyAccessExpression(head) {
			head = Unwrapped(head.AsPropertyAccessExpression().Expression)
		}
		for ast.IsElementAccessExpression(head) {
			head = Unwrapped(head.AsElementAccessExpression().Expression)
		}
		if ast.IsIdentifier(head) {
			written[head.Text()] = struct{}{}
		}
	}
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if ast.IsBinaryExpression(child) {
			bin := child.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment {
				note(bin.Left)
			}
		}
		if ast.IsPrefixUnaryExpression(child) {
			unary := child.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				note(unary.Operand)
			}
		}
		if ast.IsPostfixUnaryExpression(child) {
			unary := child.AsPostfixUnaryExpression()
			if unary.Operator == ast.KindPlusPlusToken || unary.Operator == ast.KindMinusMinusToken {
				note(unary.Operand)
			}
		}
		child.ForEachChild(visit)
		return false
	}
	visit(node)
	return written
}

// freeNameScan is what the scan reports: the free names read, in SOURCE
// ORDER OF FIRST READ (the order the capture entries are laid out in),
// and whether the arrow left the convertible subset at all.
type freeNameScan struct {
	Reads []string
	Ok    bool
}

// scanFreeNames walks an arrow's body and reports its free reads in
// first-read order, declining where the arrow leaves the convertible
// subset:
//
//   - a nested function inside the arrow — its own captures would need
//     converting too, and the lowering declines nested functions anyway;
//   - `this` in any position other than the RECEIVER of a call — a
//     `this` read as a value is a whole object no slot holds;
//   - a WRITE to any name the arrow did not bind — the caller's state
//     would move and no entry carries it back.
//
// A `this.m(…)` call contributes NO free name: the receiver is consumed
// by the resolution, and whether the callee resolves is decided by the
// ordinary call lowering (which asks ResolveCallee) rather than here.
func scanFreeNames(arrow *ast.Node) freeNameScan {
	body := arrow.Body()
	if body == nil {
		return freeNameScan{}
	}
	bound := arrowBoundNames(arrow)
	written := writtenNamesIn(body)
	// a write to a name the arrow did not bind is a captured write
	for name := range written {
		if _, isBound := bound[name]; !isBound {
			return freeNameScan{}
		}
	}
	var reads []string
	seen := map[string]struct{}{}
	declined := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if declined {
			return true
		}
		if ast.IsFunctionDeclaration(node) || ast.IsFunctionExpression(node) ||
			ast.IsArrowFunction(node) || ast.IsClassDeclaration(node) ||
			ast.IsClassExpression(node) {
			declined = true
			return true
		}
		// `this.m(…)` — the receiver is consumed whole; the arguments still
		// scan, since they may read captures
		if ast.IsCallExpression(node) {
			call := node.AsCallExpression()
			callee := Unwrapped(call.Expression)
			if ast.IsPropertyAccessExpression(callee) {
				receiver := Unwrapped(callee.AsPropertyAccessExpression().Expression)
				if receiver.Kind == ast.KindThisKeyword {
					if call.Arguments != nil {
						for _, argument := range call.Arguments.Nodes {
							visit(argument)
						}
					}
					return false
				}
			}
		}
		// `this` anywhere else — as a value, as a property read, as an
		// argument — is a whole object no slot holds
		if node.Kind == ast.KindThisKeyword {
			declined = true
			return true
		}
		// a property access's NAME half is not a read of a binding
		if ast.IsPropertyAccessExpression(node) {
			visit(node.AsPropertyAccessExpression().Expression)
			return false
		}
		if ast.IsIdentifier(node) {
			name := node.Text()
			if _, isBound := bound[name]; !isBound {
				if _, already := seen[name]; !already {
					seen[name] = struct{}{}
					reads = append(reads, name)
				}
			}
			return false
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	if declined {
		return freeNameScan{}
	}
	return freeNameScan{Reads: reads, Ok: true}
}

// capturesOf turns the scan's free reads into capture entries, or
// declines: every free name must resolve to a slot in the ENCLOSING
// lowering, since the call site binds each entry to a `var` of that
// slot. A free name with no slot — an import, a global, an outer
// function — has no `var` to bind, so the arrow declines.
//
// The capture list keeps the scan's order, which is the layout the
// entries take after the declared parameters.
func capturesOf(context *LoweringContext, scan freeNameScan) ([]capturedSlot, []int, bool) {
	if !scan.Ok {
		return nil, nil, false
	}
	var captures []capturedSlot
	var slots []int
	for _, name := range scan.Reads {
		index, found := slotIndexOfName(context, name)
		if !found {
			return nil, nil, false
		}
		if index >= len(context.Sorts) || index >= len(context.Typeofs) {
			return nil, nil, false
		}
		captures = append(captures, capturedSlot{
			Name:      name,
			Sort:      context.Sorts[index],
			TypeofTag: context.Typeofs[index],
		})
		slots = append(slots, index)
	}
	return captures, slots, true
}

// convertedArrow is a converted callback: the blob its summary compiled
// to, the slot layout the entries follow, and the caller slots the
// capture entries read.
type convertedArrow struct {
	Node    *ast.Node
	Blob    kernelbridge.SummaryBlob
	Lowered LoweredSummary
	// Entries: what each DECLARED parameter entry is filled with, in
	// declaration order — the same layout the blob was compiled under.
	Entries      []callbackEntry
	CaptureSlots []int
}

// callbackEntry is what ONE declared callback parameter is filled with
// at this site: the effect the call statement passes, and the sort and
// typeof evidence the summary is compiled under for that entry. The two
// halves must agree — a summary compiled with a number-sorted entry is
// applied to an effect the site promises is a number — so they are laid
// out together rather than derived twice.
//
// An entry the site cannot supply is spelled with an ABSENT effect and
// the unknown sort; an absent entry promises nothing, so the body's own
// reads of that parameter answer unknown.
type callbackEntry struct {
	Effect kernelbridge.LoopEffect
	Sort   BindingKind
	Typeof TypeofTag
}

// absentCallbackEntry is the entry a parameter the site cannot supply
// takes — the third parameter of an array or collection callback (the
// collection itself, which no slot holds), and every parameter past it.
func absentCallbackEntry() callbackEntry {
	return callbackEntry{
		Effect: kernelbridge.AbsentConst(),
		Sort:   BindingKindUnknown,
		Typeof: TypeofTagNone,
	}
}

// slotCallbackEntry is the entry a parameter fed from a CALLER SLOT
// takes: a `var` of that slot, wearing the sort and typeof the caller's
// layout already carries for it. A slot outside the layout answers the
// absent entry rather than an out-of-range read.
func slotCallbackEntry(context *LoweringContext, slot int) callbackEntry {
	if slot < 0 || slot >= len(context.Sorts) || slot >= len(context.Typeofs) {
		return absentCallbackEntry()
	}
	return callbackEntry{
		Effect: varEffect(slot),
		Sort:   context.Sorts[slot],
		Typeof: context.Typeofs[slot],
	}
}

// indexCallbackEntry is the entry an ARRAY callback's SECOND parameter
// takes — the index the traversal is at. The two-slot flattening holds
// no per-position index, but it does not have to: every concrete index
// a traversal hands out is a non-negative integer, so the integer-≥0
// constant set is an honest entry rather than a claim about which
// position the pass is on. Entering that instead of absent is what lets
// `(x, i) => x + i` convert — an absent entry has no number sort, and
// arithmetic admits only the number sort.
func indexCallbackEntry() callbackEntry {
	return callbackEntry{
		Effect: kernelbridge.LoopEffect{
			Kind: kernelbridge.LoopEffectConst,
			Set:  nonNegativeIntegerSet(),
		},
		Sort:   BindingKindNumber,
		Typeof: TypeofTagNumber,
	}
}

// arrayCallbackEntries is the entry layout an ARRAY callback's declared
// parameters take: the element, the index, then the array itself (which
// no slot holds) and anything past it absent.
func arrayCallbackEntries(context *LoweringContext, declared int, elementSlot int) []callbackEntry {
	entries := make([]callbackEntry, declared)
	for index := range entries {
		switch index {
		case 0:
			entries[index] = slotCallbackEntry(context, elementSlot)
		case 1:
			entries[index] = indexCallbackEntry()
		default:
			entries[index] = absentCallbackEntry()
		}
	}
	return entries
}

// collectionCallbackEntries is the entry layout a MAP or SET callback's
// declared parameters take under `forEach(cb)`: the value, then the KEY
// for a Map. A Set's second parameter is the VALUE AGAIN — Set.forEach
// calls back with (value, value, set), the second argument standing in
// for the key a Set does not have — so it reads the same slot. The third
// parameter is the collection itself, which no slot holds.
func collectionCallbackEntries(context *LoweringContext, declared int, valsSlot int, keysSlot int, isMap bool) []callbackEntry {
	entries := make([]callbackEntry, declared)
	for index := range entries {
		switch index {
		case 0:
			entries[index] = slotCallbackEntry(context, valsSlot)
		case 1:
			if isMap {
				entries[index] = slotCallbackEntry(context, keysSlot)
				continue
			}
			entries[index] = slotCallbackEntry(context, valsSlot)
		default:
			entries[index] = absentCallbackEntry()
		}
	}
	return entries
}

// arrowParameterSorts is the sort and typeof evidence each DECLARED
// parameter entry wears at this site, read off the entry layout the site
// built — so the summary is compiled over exactly the values each entry
// can hold.
//
// Without this the arrow's own arithmetic would decline: an unannotated
// `x => x + 1` reads its parameter through declaredParamSort, which is
// unknown for an unannotated parameter, and arithmetic admits only the
// number sort. The declaration route must stay annotation-read — its
// summary quantifies over callers no lowering can see — but an arrow
// argument has exactly one site and that site knows the sort.
func arrowParameterSorts(entries []callbackEntry) []parameterSlotSort {
	if len(entries) == 0 {
		return nil
	}
	sorts := make([]parameterSlotSort, len(entries))
	for index, entry := range entries {
		sorts[index] = parameterSlotSort{Sort: entry.Sort, TypeofTag: entry.Typeof}
	}
	return sorts
}

// convertArrow closure-converts an arrow argument and compiles it: scan
// the free names, resolve each to a caller slot, lower the body with the
// declared parameters wearing the site's element sort and the captures
// laid out after them, and ask the kernel to compile it — exactly as
// buildSummaryBlob does for a declaration, through
// kernelbridge.AskSummarize with the lowering's own slot count as the
// arity and the table its call statements built.
//
// The blob is remembered under the arrow NODE, not in the declaration-
// keyed registry: an arrow belongs to its one site.
func convertArrow(context *LoweringContext, argument *ast.Node, entries []callbackEntry) (convertedArrow, bool) {
	arrow := callbackFunctionOf(context, argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	if context.Flow == nil {
		return convertedArrow{}, false
	}
	if _, ok := arrowParameterNames(arrow); !ok {
		return convertedArrow{}, false
	}
	if len(entries) != len(arrow.Parameters()) {
		return convertedArrow{}, false
	}
	captures, captureSlots, ok := capturesOf(context, scanFreeNames(arrow))
	if !ok {
		return convertedArrow{}, false
	}
	parameterSorts := arrowParameterSorts(entries)
	arrowBlobsMu.Lock()
	held, has := arrowBlobs[arrow]
	arrowBlobsMu.Unlock()
	if has {
		if !held.Ok {
			return convertedArrow{}, false
		}
		// the remembered blob was compiled against ITS entry layout — the
		// parameter sorts the site supplied AND the captures it resolved. A
		// re-lowering that resolved a different one is a different arrow's
		// worth of entries, so it declines rather than reusing the blob.
		if !sameParameterSorts(held.Parameters, parameterSorts) || !sameCaptures(held.Captures, captures) {
			return convertedArrow{}, false
		}
		return convertedArrow{
			Node: arrow, Blob: held.Blob, Lowered: held.Lowered,
			Entries: entries, CaptureSlots: captureSlots,
		}, true
	}
	lowered, loweredOk := lowerArrowSummary(context.Flow, arrow, parameterSorts, captures)
	if !loweredOk {
		arrowBlobsMu.Lock()
		arrowBlobs[arrow] = arrowBlobEntry{Parameters: parameterSorts, Captures: captures}
		arrowBlobsMu.Unlock()
		return convertedArrow{}, false
	}
	blob, asked := kernelbridge.AskSummarize(lowered.SlotCount, lowered.Stmts, lowered.Table)
	if !asked {
		arrowBlobsMu.Lock()
		arrowBlobs[arrow] = arrowBlobEntry{Parameters: parameterSorts, Captures: captures}
		arrowBlobsMu.Unlock()
		return convertedArrow{}, false
	}
	arrowBlobsMu.Lock()
	arrowBlobs[arrow] = arrowBlobEntry{
		Blob:       blob,
		Lowered:    lowered,
		Parameters: parameterSorts,
		Captures:   captures,
		Ok:         true,
	}
	arrowBlobsMu.Unlock()
	return convertedArrow{
		Node: arrow, Blob: blob, Lowered: lowered,
		Entries: entries, CaptureSlots: captureSlots,
	}, true
}

// sameParameterSorts is whether two declared-parameter layouts agree
// entry for entry — the other half of the check a remembered blob's
// reuse turns on. A blob compiled with a number-sorted first entry may
// not be reused at a site whose element slot is a string.
func sameParameterSorts(held []parameterSlotSort, wanted []parameterSlotSort) bool {
	if len(held) != len(wanted) {
		return false
	}
	for index := range held {
		if held[index].Sort != wanted[index].Sort || held[index].TypeofTag != wanted[index].TypeofTag {
			return false
		}
	}
	return true
}

// sameCaptures is whether two capture layouts agree name for name in
// order — the check a remembered blob's reuse turns on.
//
// WRITTEN is compared beside the name and the sort, and it has to be: a
// blob compiled when a capture was read-only carries NO bundle row for
// it, so a site that reused that blob while treating the capture as
// written would map a write-back through a row the compile never made —
// the caller would keep believing a slot the closure moved. The flag is
// part of the entry layout, so it is part of the identity.
//
// AN OBJECT capture's LEAF VOCABULARY is compared the same way and for
// the same reason, one step down: the member list IS the entry layout
// for that capture, so a blob compiled against three leaves may not be
// reused where the caller's flattening now offers two — entry k would
// take a value belonging to entry j. Each leaf's own Sort and Written
// join the comparison beside its name, because each is what its entry
// and its row were compiled under. A scalar capture and an object
// capture under one name are different layouts and differ here on the
// member count.
func sameCaptures(held []capturedSlot, wanted []capturedSlot) bool {
	if len(held) != len(wanted) {
		return false
	}
	for index := range held {
		if held[index].Name != wanted[index].Name ||
			held[index].Sort != wanted[index].Sort ||
			held[index].Written != wanted[index].Written {
			return false
		}
		if len(held[index].Members) != len(wanted[index].Members) {
			return false
		}
		for leaf := range held[index].Members {
			if held[index].Members[leaf].Member != wanted[index].Members[leaf].Member ||
				held[index].Members[leaf].Sort != wanted[index].Members[leaf].Sort ||
				held[index].Members[leaf].TypeofTag != wanted[index].Members[leaf].TypeofTag ||
				held[index].Members[leaf].Written != wanted[index].Members[leaf].Written {
				return false
			}
		}
	}
	return true
}

// arrowCallStatement builds the ONE call statement a converted arrow
// takes: the declared parameter entries take the effects the site's
// entry layout supplies, the capture entries take a `var` of each
// captured caller slot, every remaining entry enters absent with the
// done flag at {0} — exactly the entry states applySummary sends, so a
// spliced compile and a direct apply agree — and Rets maps the arrow's
// #ret out-state onto `target`, or nothing where target is -1.
func arrowCallStatement(
	context *LoweringContext,
	converted convertedArrow,
	target int,
) (kernelbridge.IrStatement, bool) {
	if context.SummaryTable == nil {
		return kernelbridge.IrStatement{}, false
	}
	lowered := converted.Lowered
	declared := len(converted.Node.Parameters())
	if declared != len(converted.Entries) {
		return kernelbridge.IrStatement{}, false
	}
	if declared+len(converted.CaptureSlots) > lowered.ParamCount {
		return kernelbridge.IrStatement{}, false
	}
	args := make([]kernelbridge.LoopEffect, 0, lowered.SlotCount)
	// the declared parameters, each the effect the site's layout laid out
	// for it — the element or value the traversal hands each pass, the
	// index or key beside it, and absent where the site holds nothing.
	// The blob was compiled under these same entries' sorts.
	for _, entry := range converted.Entries {
		args = append(args, entry.Effect)
	}
	// the capture entries, in the scan's order
	for _, slot := range converted.CaptureSlots {
		args = append(args, varEffect(slot))
	}
	for len(args) < lowered.SlotCount {
		if len(args) == lowered.DoneIndex {
			args = append(args, kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
			})
			continue
		}
		args = append(args, kernelbridge.AbsentConst())
	}
	rets := make([]int, lowered.RetIndex+1)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 {
		rets[lowered.RetIndex] = target
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(converted.Node, converted.Blob),
		Args:   args,
		Rets:   rets,
	}, true
}

// convertArrayArrow converts a callback under the ARRAY entry layout —
// the element, the index, then absent. The arrow's own parameter count
// decides how many entries the layout spells, so a one-parameter
// callback takes exactly the element and a three-parameter one takes the
// array position as absent.
func convertArrayArrow(context *LoweringContext, argument *ast.Node, elementSlot int) (convertedArrow, bool) {
	arrow := callbackFunctionOf(context, argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	entries := arrayCallbackEntries(context, len(arrow.Parameters()), elementSlot)
	return convertArrow(context, argument, entries)
}

// convertReduceArrow converts a callback under REDUCE's entry layout —
// the accumulator, then the element, then the index, then absent. Every
// slot the array layout spells shifts one to the right, which is the
// same shift ArrayCallbackPins applies for `reduce` on the other
// callback-binding path; the two routes agree on the layout because
// they agree on the reason for it, which is `reduce`'s own signature
// (accumulator, element, index, array).
func convertReduceArrow(
	context *LoweringContext,
	argument *ast.Node,
	elementSlot int,
	accumulator kernelbridge.LoopEffect,
	accumulatorSort BindingKind,
	accumulatorTypeof TypeofTag,
) (convertedArrow, bool) {
	arrow := callbackFunctionOf(context, argument)
	if arrow == nil {
		return convertedArrow{}, false
	}
	declared := len(arrow.Parameters())
	entries := make([]callbackEntry, declared)
	for index := range entries {
		switch index {
		case 0:
			entries[index] = callbackEntry{
				Effect: accumulator,
				Sort:   accumulatorSort,
				Typeof: accumulatorTypeof,
			}
		case 1:
			entries[index] = slotCallbackEntry(context, elementSlot)
		case 2:
			entries[index] = indexCallbackEntry()
		default:
			entries[index] = absentCallbackEntry()
		}
	}
	return convertArrow(context, argument, entries)
}

// collectionCall is one recognized `xs.m(cb)` shape: the receiver's
// spelled name, the method, and the single callback argument.
type collectionCall struct {
	Receiver string
	Method   string
	Callback *ast.Node
}

// collectionCallOf reads `xs.m(cb)` with exactly one argument and a
// plain (non-optional) receiver identifier — the only receiver shape
// the two-slot flattening resolves.
func collectionCallOf(node *ast.Node) (collectionCall, bool) {
	head := Unwrapped(node)
	if !ast.IsCallExpression(head) {
		return collectionCall{}, false
	}
	call := head.AsCallExpression()
	if call.QuestionDotToken != nil {
		return collectionCall{}, false
	}
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return collectionCall{}, false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return collectionCall{}, false
	}
	if !ast.IsIdentifier(property.Expression) || !ast.IsIdentifier(property.Name()) {
		return collectionCall{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return collectionCall{}, false
	}
	argument := call.Arguments.Nodes[0]
	if ast.IsSpreadElement(argument) {
		return collectionCall{}, false
	}
	return collectionCall{
		Receiver: property.Expression.Text(),
		Method:   property.Name().Text(),
		Callback: argument,
	}, true
}

// reduceCallOf reads `xs.reduce(cb, seed)` — the TWO-argument shape
// collectionCallOf refuses, since every other recognized method takes
// exactly one. The receiver rules are the same ones collectionCallOf
// applies: a plain non-optional identifier receiver, a non-optional
// method step, and a callback that is not a spread.
//
// The SEED is answered beside the call rather than folded in, because
// what fills the accumulator entry is a caller-side effect the entry
// layout builds; the reader's job is only to hand back the node.
//
// The one-argument form `xs.reduce(cb)` — no seed, the first element
// standing in — is NOT read here. Its accumulator starts as an element
// rather than a value the site can name, and reduce over an empty array
// with no seed THROWS, which is a control-flow outcome this lowering
// does not model. The two-argument form has neither problem.
func reduceCallOf(node *ast.Node) (source collectionCall, seed *ast.Node, ok bool) {
	head := Unwrapped(node)
	if !ast.IsCallExpression(head) {
		return collectionCall{}, nil, false
	}
	call := head.AsCallExpression()
	if call.QuestionDotToken != nil {
		return collectionCall{}, nil, false
	}
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return collectionCall{}, nil, false
	}
	property := access.AsPropertyAccessExpression()
	if property.QuestionDotToken != nil {
		return collectionCall{}, nil, false
	}
	if !ast.IsIdentifier(property.Expression) || !ast.IsIdentifier(property.Name()) {
		return collectionCall{}, nil, false
	}
	if property.Name().Text() != "reduce" {
		return collectionCall{}, nil, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 2 {
		return collectionCall{}, nil, false
	}
	callback := call.Arguments.Nodes[0]
	seedNode := call.Arguments.Nodes[1]
	if ast.IsSpreadElement(callback) || ast.IsSpreadElement(seedNode) {
		return collectionCall{}, nil, false
	}
	return collectionCall{
		Receiver: property.Expression.Text(),
		Method:   "reduce",
		Callback: callback,
	}, seedNode, true
}

// targetArraySlotsOf resolves the slots a mapped/filtered RESULT lands
// in: its "ys.len" and "ys.elem" pair, allocated where the enclosing
// layout did not lay them out. A result that is neither laid out nor
// allocatable declines the whole statement — there is nowhere to write.
//
// A target the layout gave a WHOLE-NAME scalar slot to, with no
// ".len"/".elem" pair beside it, declines rather than allocating one.
// The array recognizer refused that name — it is read somewhere as a
// whole array — so its scalar slot is what those reads resolve to, and
// this route never writes it. Allocating a pair anyway would leave the
// bare-name reads answering that slot's stale entry state while the
// array's real values sat in slots nothing consults: a wrong answer
// rather than a weak one.
func targetArraySlotsOf(context *LoweringContext, name string) (lenSlot int, elemSlot int, ok bool) {
	if lenSlot, elemSlot, found := arraySlotsOf(context, name); found {
		return lenSlot, elemSlot, true
	}
	if _, whole := slotIndexOfName(context, name); whole {
		return 0, 0, false
	}
	if context.Allocate == nil {
		return 0, 0, false
	}
	lenSlot, lenOk := slotIndexOfName(context, name+arrayLenSuffix)
	if !lenOk {
		lenSlot, lenOk = context.Allocate(name+arrayLenSuffix, BindingKindNumber, TypeofTagNumber)
		if !lenOk {
			return 0, 0, false
		}
	}
	elemSlot, elemOk := slotIndexOfName(context, name+arrayElemSuffix)
	if !elemOk {
		// the element sort is UNKNOWN: what the callback returns is the
		// kernel's answer, not something the site reads off syntax. An
		// unknown-sorted slot admits the definedness test alone, which is
		// the honest standing for a value nothing here promised a sort for.
		elemSlot, elemOk = context.Allocate(name+arrayElemSuffix, BindingKindUnknown, TypeofTagNone)
		if !elemOk {
			return 0, 0, false
		}
	}
	return lenSlot, elemSlot, true
}

// nonNegativeIntegerSet is "an integer at least 0" — what a FILTER's
// result length is known to be and no more. The two-slot flattening has
// no spelling for "at most the source's length": that upper bound is a
// relation between two slots, and a stored set relates a slot to
// constants alone. The precision is honestly lost here rather than
// claimed.
func nonNegativeIntegerSet() refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0))
}

// mapStatements is `ys = xs.map(cb)` over a flattened source: the
// result's length is the source's exactly (map preserves length), and
// the result's element is cb's return.
//
// The one call statement covers the WHOLE traversal. The source's
// element slot holds the join of every element the array can hold, and
// cb's summary quantifies over all entries — so applying cb at that
// join answers a set covering cb's image of each individual element.
// Every per-pass image is admitted by the one answer.
func mapStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	sourceLen, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	targetLen, targetElem, targetOk := targetArraySlotsOf(context, target)
	if !targetOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, targetElem)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: targetLen, Effect: varEffect(sourceLen)},
		call,
	}, true
}

// filterStatements is `ys = xs.filter(cb)`: every surviving element is
// one the source held, so the result's element slot copies the source's
// — and the result's length is an integer ≥ 0, the honest loss above.
//
// cb still has to CONVERT even though its truthiness answer is not
// modeled. The conversion is not there to read the predicate; it is
// there so an effectful callback cannot hide: a cb that writes a
// capture, reads an unslotted name, or leaves the lowered subset
// declines the whole statement rather than passing as a pure predicate.
func filterStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	if _, convertedOk := convertArrayArrow(context, source.Callback, sourceElem); !convertedOk {
		return nil, false
	}
	targetLen, targetElem, targetOk := targetArraySlotsOf(context, target)
	if !targetOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		{Kind: kernelbridge.IrStatementAssign, Target: targetElem, Effect: varEffect(sourceElem)},
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: targetLen,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectConst, Set: nonNegativeIntegerSet()},
		},
	}, true
}

// forEachStatement is `xs.forEach(cb)`: the single call statement at
// the element entry with NO ret — forEach's own value is undefined and
// nothing reads it.
//
// Nothing rides back into the caller's slots, and that is by rule
// rather than by omission: captures are READ-ONLY, so a cb that would
// write one declined at conversion. What the cb's body does beyond its
// own calls touches only the cb's own slots, which end with the
// application.
func forEachStatement(context *LoweringContext, source collectionCall) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{call}, true
}

// scalarTargetSlotOf resolves the slot a NON-ARRAY result lands in: the
// one `find`, `reduce`, and `flatMap` write. Where the enclosing layout
// already laid the name out, that slot is it; otherwise one is
// allocated under the name itself.
//
// The allocated sort is UNKNOWN, which is the same honesty
// targetArraySlotsOf's element allocation keeps: what the callback
// answers is the kernel's, not something this site reads off syntax. An
// unknown-sorted slot admits the definedness test alone.
//
// A name the layout already gave a FLATTENED family to — a record's
// leaves, an array's ".len"/".elem" pair, a collection's size/vals/keys,
// a promise's ".inner" — declines rather than allocating a whole-name
// slot beside it. This is the mirror of targetArraySlotsOf's rule and it
// exists for the same reason: the reads of that name resolve to the
// family, so a fresh whole-name slot would hold the result where nothing
// consults it while those reads went on answering the family's own
// values. Declining leaves the statement to its havoc floor, which is
// weak rather than wrong.
func scalarTargetSlotOf(context *LoweringContext, name string) (int, bool) {
	if slot, found := slotIndexOfName(context, name); found {
		return slot, true
	}
	if len(flattenedSlotsUnder(context, name)) > 0 {
		return 0, false
	}
	if context.Allocate == nil {
		return 0, false
	}
	return context.Allocate(name, BindingKindUnknown, TypeofTagNone)
}

// findStatements is `ys = xs.find(cb)` over a flattened array: the
// callback runs on the element join for its EFFECTS — its truthiness
// answer is not what find hands back — and the result is an element the
// array held OR undefined, which is exactly the or-absent effect over
// the source's element slot.
//
// The precision this keeps is real and the precision it drops is named.
// Kept: every value `ys` can hold is one `xs.elem` can hold, or absent,
// so a later `if (ys !== undefined)` narrows to the element set. Dropped:
// nothing says WHICH element, and nothing says the predicate held of it
// — a `find(x => x > 10)` result is not narrowed to "> 10" here, because
// the two-slot flattening carries the elements' join and not a
// per-element relation to a predicate's answer.
//
// cb must still CONVERT, for the same reason filter's must: a callback
// that writes a capture or leaves the lowered subset declines the whole
// statement rather than passing as a pure predicate.
//
// The two slots must AGREE ON SORT. What lands in the target is the
// source's element values, so a target the layout sorted differently
// would be read under a sort the values it now holds do not wear —
// `const first = words.find(cb)` over a string-sorted array writes word
// tuples, and the layout sorts a call-initialized local as a number
// (LocalSort reads the initializer's syntax, and a call is not one of
// the shapes it rules out), which would then admit `first` into
// arithmetic. That is a wrong answer rather than a weak one, so the
// mismatch declines. An UNKNOWN-sorted target takes the write either
// way: unknown promises no reading, so nothing can be read out of it
// under the wrong one.
func findStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	targetSlot, targetOk := scalarTargetSlotOf(context, target)
	if !targetOk {
		return nil, false
	}
	if context.Sorts[targetSlot] != BindingKindUnknown &&
		context.Sorts[targetSlot] != context.Sorts[sourceElem] {
		return nil, false
	}
	// the predicate runs, and its own answer goes nowhere
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	element := varEffect(sourceElem)
	return []kernelbridge.IrStatement{
		call,
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: targetSlot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &element},
		},
	}, true
}

// seedEffectOf reads a reduce SEED and reports the sort the reading
// committed to. The seed is the one value the site names outright, so
// both worlds of the effect grammar are tried rather than one: the
// SEQUENCE reading first, which answers a string literal's exact tuple
// and a string-sorted name's read, then the numeric reading, which
// answers a number, a boolean, and everything else RhsEffect reads.
//
// Reading the seed at the unknown sort alone is what starved
// `xs.reduce((acc, w) => acc + w, "")`: RhsEffect's string-literal and
// sequence arms both stand behind a string-sorted target, so a string
// seed fell to the numeric reader, which has no spelling for a word,
// and the whole statement declined. A number seed lowered all along.
//
// The sort answers unknown wherever the numeric reader took a value
// that is not a spelled number — the reading is honest either way, and
// the caller only ever narrows the accumulator entry with a sort it can
// also see on the target slot.
func seedEffectOf(context *LoweringContext, seed *ast.Node) (kernelbridge.LoopEffect, BindingKind, bool) {
	if sequence, ok := SequenceEffectOf(context, seed); ok {
		return sequence, BindingKindString, true
	}
	effect, ok := RhsEffect(context, BindingKindUnknown, seed)
	if !ok {
		return kernelbridge.LoopEffect{}, BindingKindUnknown, false
	}
	// a spelled number or boolean is a number-sorted seed; a tracked name
	// wears whatever its own slot wears, and absent wears nothing
	if head := Unwrapped(seed); head != nil {
		if slot, tracked := IndexOf(context, head); tracked {
			return effect, context.Sorts[slot], true
		}
		if _, isNumber := NumberOf(head); ast.IsNumericLiteral(head) || isNumber ||
			head.Kind == ast.KindTrueKeyword || head.Kind == ast.KindFalseKeyword {
			return effect, BindingKindNumber, true
		}
	}
	return effect, BindingKindUnknown, true
}

// reduceStatements is `ys = xs.reduce(cb, seed)` over a flattened
// array: ONE call statement whose accumulator entry covers the
// accumulator at EVERY pass, with cb's ret landing in the target.
//
// The fold is not unrolled, and the argument for why one application
// suffices is the join-of-elements argument one level up. The
// accumulator entry is filled with the JOIN of the seed's own effect
// and the target slot's var — and the target slot is where cb's ret
// lands, so after the statement the slot holds cb's image of that join.
// Every intermediate accumulator the real fold produces is either the
// seed (pass 0) or a value cb returned from an earlier pass, and both
// are admitted by that join; cb's summary quantifies over all entries,
// so applying it at the join covers cb's image of each intermediate.
//
// The target slot is ASSIGNED THE SEED first, so the var read in the
// join is not the slot's stale entry state. Reading a slot that held
// something unrelated would make the join a claim about a value the
// reduce never had.
//
// The accumulator's SORT is the seed's, and only where the TARGET SLOT
// wears that sort too. The entry is a join of the seed and the target
// slot's var, so the sort has to be one both halves can be read under;
// where the slot disagrees the entry takes unknown, which costs
// arithmetic inside cb and claims nothing about what the slot holds.
func reduceStatements(
	context *LoweringContext,
	source collectionCall,
	seed *ast.Node,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	targetSlot, targetOk := scalarTargetSlotOf(context, target)
	if !targetOk {
		return nil, false
	}
	seedEffect, seedSort, seedOk := seedEffectOf(context, seed)
	if !seedOk {
		return nil, false
	}
	// the seed is written into the target slot below, so a seed whose sort
	// the slot does not wear declines: a word tuple in a number-sorted slot
	// would be admitted into arithmetic by every reader that consults the
	// sort. An unknown-sorted slot takes any seed — unknown promises no
	// reading, so nothing is read out of it under the wrong one.
	if seedSort != BindingKindUnknown && context.Sorts[targetSlot] != BindingKindUnknown &&
		context.Sorts[targetSlot] != seedSort {
		return nil, false
	}
	// the accumulator entry: the seed, joined with whatever cb's ret put
	// in the target on an earlier pass
	accumulator := joinEffect(seedEffect, varEffect(targetSlot))
	// the accumulator's sort is the SEED's, and only where the target slot
	// wears it too: the join above reads that slot, so a sort the slot
	// does not carry would promise cb a reading of a value the slot cannot
	// hold. The gate above already refused a disagreement, so what is left
	// to rule out is an unknown-sorted slot, whose var half of the join
	// carries no reading for the seed's sort to stand on.
	accumulatorSort := BindingKindUnknown
	if seedSort != BindingKindUnknown && context.Sorts[targetSlot] == seedSort {
		accumulatorSort = seedSort
	}
	converted, convertedOk := convertReduceArrow(
		context, source.Callback, sourceElem,
		accumulator, accumulatorSort, TypeofTagNone,
	)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, targetSlot)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		// the slot starts at the seed, so the join above reads a value the
		// reduce actually had rather than the slot's entry state
		{Kind: kernelbridge.IrStatementAssign, Target: targetSlot, Effect: seedEffect},
		call,
	}, true
}

// flatMapStatements is `ys = xs.flatMap(cb)` over a flattened array:
// the callback converts and RUNS, and the result answers unknown.
//
// The result shape has no spelling here. flatMap concatenates cb's
// per-element arrays one level down, so `ys.elem` would have to be the
// element of cb's RETURNED array — and cb's ret is one slot holding
// that array as a whole, which the two-slot flattening never opened.
// Claiming `ys.elem := cb's ret` would say the elements of ys are the
// ARRAYS cb returned, which is wrong rather than weak.
//
// So the target takes unknown and the conversion is kept for its own
// sake: cb's body lowers, its calls compose, and a cb that writes a
// capture or leaves the subset declines the statement instead of
// slipping past unread. That is the whole reason this method is
// recognized at all — the effects are what is worth having, and the
// result honestly says nothing.
func flatMapStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	targetSlot, targetOk := scalarTargetSlotOf(context, target)
	if !targetOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		call,
		{Kind: kernelbridge.IrStatementAssign, Target: targetSlot, Effect: unknownEffect},
	}, true
}

// convertDiscardedArrow converts a bare-position traversal's callback
// under the layout its own METHOD spells: reduce's shifted entries with
// an absent accumulator, and the ordinary element-then-index layout for
// every other method. Reading the method here is what keeps the two
// layouts from being chosen by anything but the method's own signature.
func convertDiscardedArrow(context *LoweringContext, source collectionCall, elementSlot int) (convertedArrow, bool) {
	if source.Method != "reduce" {
		return convertArrayArrow(context, source.Callback, elementSlot)
	}
	return convertReduceArrow(
		context, source.Callback, elementSlot,
		kernelbridge.AbsentConst(), BindingKindUnknown, TypeofTagNone,
	)
}

// discardedResultStatement is a traversal in BARE EXPRESSION position —
// `xs.find(cb);`, `xs.map(cb);`, `xs.reduce(cb, seed);` — where the
// method's own answer is thrown away. The traversal still runs, so the
// callback still runs, and the one call statement at the element entry
// is exactly what forEach's route emits: no ret, nothing written.
//
// Running it is what keeps an unconvertible callback honest. Without
// this arm the statement declined at the receiver and fell to the havoc
// floor, which havocs the mentioned slots and never asks whether the
// callback converts — so a cb that writes a capture or leaves the
// lowered subset passed unread, indistinguishable from one that
// converts cleanly. Here it declines the statement instead, and a cb
// that does convert contributes its calls to the body.
//
// REDUCE keeps its own shifted layout here, with the accumulator entry
// ABSENT. With no target slot there is nothing for cb's ret to land in,
// so the join that fills that entry in the assigning route has no slot
// to read — and converting reduce's cb under the ordinary array layout
// instead would put the ELEMENT where the accumulator belongs, which
// describes entry 0 as a value it never holds. An absent entry promises
// nothing, so cb's reads of the accumulator answer unknown: weaker than
// the assigning route, and true.
func discardedResultStatement(context *LoweringContext, source collectionCall) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertDiscardedArrow(context, source, sourceElem)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{call}, true
}

// collectionForEachStatement is `m.forEach(cb)` / `s.forEach(cb)` on a
// FLATTENED Map or Set: one call statement at the collection's slots,
// with no ret — forEach's own value is undefined and nothing reads it.
//
// The entry layout follows the JS callback signature. A Map calls back
// with (value, key, map): the first entry is the values slot's var, the
// second the keys slot's var, and the third the map itself, which no
// slot holds. A Set calls back with (value, value, set) — the second
// argument stands in for the key a Set does not have, and it is the same
// value — so the second entry reads the values slot again.
//
// One application covers the whole traversal for the same reason an
// array's does: the values slot holds the JOIN of everything the
// collection can hold, and cb's summary quantifies over all entries, so
// applying it at that join covers cb's image of each individual entry.
//
// `map` and `filter` on a collection do NOT come here — a Map and a Set
// have no such methods, so those spellings decline like any other
// unrecognized receiver.
func collectionForEachStatement(context *LoweringContext, source collectionCall) ([]kernelbridge.IrStatement, bool) {
	_, valsSlot, keysSlot, keysOk, ok := mapSlotsOf(context, source.Receiver)
	if !ok {
		return nil, false
	}
	arrow := callbackFunctionOf(context, source.Callback)
	if arrow == nil {
		return nil, false
	}
	entries := collectionCallbackEntries(context, len(arrow.Parameters()), valsSlot, keysSlot, keysOk)
	converted, convertedOk := convertArrow(context, source.Callback, entries)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{call}, true
}

// promiseAllMapOf reads `Promise.all(xs.map(cb))` — through an await
// where one wraps it — and answers the inner map call.
//
// The await adds NOTHING. A lowered async body's #ret already holds the
// settled inner value (the ret-as-inner convention), so the array
// Promise.all settles to has exactly the elements the map lowering
// already wrote into the result's element slot: cb's ret. Awaiting the
// whole is the identity on that slot.
func promiseAllMapOf(expression *ast.Node) (collectionCall, bool) {
	head := Unwrapped(expression)
	if ast.IsAwaitExpression(head) {
		head = Unwrapped(head.AsAwaitExpression().Expression)
	}
	if !ast.IsCallExpression(head) {
		return collectionCall{}, false
	}
	call := head.AsCallExpression()
	access := Unwrapped(call.Expression)
	if !ast.IsPropertyAccessExpression(access) {
		return collectionCall{}, false
	}
	property := access.AsPropertyAccessExpression()
	if !ast.IsIdentifier(property.Expression) || property.Expression.Text() != "Promise" {
		return collectionCall{}, false
	}
	if !ast.IsIdentifier(property.Name()) || property.Name().Text() != "all" {
		return collectionCall{}, false
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
		return collectionCall{}, false
	}
	inner, innerOk := collectionCallOf(call.Arguments.Nodes[0])
	if !innerOk || inner.Method != "map" {
		return collectionCall{}, false
	}
	return inner, true
}

// SummaryCallbackStatementOf is the lowering-side entry: a statement
// whose right side is a recognized call over a flattened array, a
// flattened Map or Set, or a promise-held local, with a closure-
// converting callback — lowered to the call statement and whatever slot
// writes the shape carries beside it.
//
// Total-or-decline. Anything unrecognized — a receiver that is not
// flattened, a callback that captures a write, a method not listed, a
// result with nowhere to land — answers false, and the statement then
// takes whatever route it had.
func SummaryCallbackStatementOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || statement == nil {
		return nil, false
	}
	// the statement forms whose value goes NOWHERE
	if ast.IsExpressionStatement(statement) {
		expression := Unwrapped(statement.AsExpressionStatement().Expression)
		if source, ok := collectionCallOf(expression); ok {
			switch source.Method {
			// `xs.forEach(cb);` / `m.forEach(cb);` — the traversal runs. The
			// receiver decides which flattening answers: an array's two slots
			// or a collection's size/vals/keys family. A receiver that is
			// neither declines both.
			case "forEach":
				if lowered, arrayOk := forEachStatement(context, source); arrayOk {
					return lowered, true
				}
				return collectionForEachStatement(context, source)
			// `p.then(cb);` — the callback runs on the settled value and its
			// own answer goes nowhere
			case "then":
				return thenStatements(context, source, "")
			// `xs.find(cb);` / `xs.map(cb);` / `.filter` / `.flatMap` with the
			// RESULT DISCARDED — the traversal still runs, so the callback
			// runs, and what it answers goes nowhere
			case "map", "filter", "find", "flatMap":
				return discardedResultStatement(context, source)
			}
		}
		// `xs.reduce(cb, seed);` — the two-argument shape, its result
		// discarded. The fold still runs the callback once per element.
		if reduceSource, _, ok := reduceCallOf(expression); ok {
			return discardedResultStatement(context, reduceSource)
		}
	}
	// `ys = e` / `const ys = e` — the shape both call routes read. The
	// target here is an ARRAY, whose two slots the name resolves to, so
	// the scalar target callAssignmentShapeOf answers with is not what
	// this uses; the NAME is.
	target, rhs, shapeOk := callbackAssignmentNameOf(context, statement)
	if !shapeOk {
		return nil, false
	}
	// `ys = await Promise.all(xs.map(cb))` / the unawaited spelling —
	// the map lowering, since the await is the identity on the result
	if inner, ok := promiseAllMapOf(rhs); ok {
		return mapStatements(context, inner, target)
	}
	// `ys = xs.reduce(cb, seed)` — the two-argument shape collectionCallOf
	// refuses, read by its own reader ahead of the one-argument switch
	if reduceSource, seed, ok := reduceCallOf(rhs); ok {
		return reduceStatements(context, reduceSource, seed, target)
	}
	source, sourceOk := collectionCallOf(rhs)
	if !sourceOk {
		return nil, false
	}
	// every method callback_pins.go's ArrayCallbackMethods names has a
	// case here, so the two routes agree about which array methods carry
	// a modeled callback — `reduce` above, and the rest below
	switch source.Method {
	case "map":
		return mapStatements(context, source, target)
	case "filter":
		return filterStatements(context, source, target)
	case "find":
		return findStatements(context, source, target)
	case "flatMap":
		return flatMapStatements(context, source, target)
	case "then":
		return thenStatements(context, source, target)
	}
	return nil, false
}

// SummaryCallbackReturnOf is the lowering-side entry for the RETURN
// position: `return xs.reduce(cb, seed)`, `return xs.find(cb)`,
// `return xs.flatMap(cb)` over a flattened array receiver. It answers
// the statements that leave the method's result in the slot the caller
// names, which is the body's own result slot rather than a local.
//
// Why this is a second entry rather than a case in
// SummaryCallbackStatementOf. That entry resolves its target through a
// NAME — callbackAssignmentNameOf hands back the identifier a
// declaration or an assignment writes, and scalarTargetSlotOf /
// targetArraySlotsOf turn that name into slots, allocating where the
// layout laid none out. A return writes no name. Its target is a slot
// the body already owns, so the name-resolving half of the route has
// nothing to resolve and the slot-writing half is all that applies.
// Routing a return through the name entry would mean inventing a name
// for the result slot, which the layout would then answer for
// inconsistently; taking the slot directly is the same lowering with
// the resolution step removed.
//
// Only the SCALAR-result methods serve here. A returned `xs.map(cb)` or
// `xs.filter(cb)` is an ARRAY, and the two-slot flattening writes an
// array into a ".len"/".elem" pair — which the result slot has not got.
// Writing cb's element image into the bare ret slot would say the
// returned value IS one element rather than the array of them, which is
// wrong rather than weak, so those two are left to the opaque return's
// unknown. The row they leave in the report names them.
//
// Total-or-decline, exactly as the statement entry is: an unflattened
// receiver, an unconvertible callback, or a seed the result slot's sort
// cannot carry answers false, and the return then takes its own routes
// and, failing those, the opaque return.
func SummaryCallbackReturnOf(context *LoweringContext, returned *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || returned == nil || context.Result == nil {
		return nil, false
	}
	target := context.Result.Ret
	if target < 0 {
		return nil, false
	}
	// `return xs.reduce(cb, seed)` — the two-argument shape, read by its
	// own reader ahead of the one-argument switch, exactly as the
	// statement entry orders them
	if reduceSource, seed, ok := reduceCallOf(returned); ok {
		return reduceSlotStatements(context, reduceSource, seed, target)
	}
	source, sourceOk := collectionCallOf(returned)
	if !sourceOk {
		return nil, false
	}
	switch source.Method {
	case "find":
		return findSlotStatements(context, source, target)
	case "flatMap":
		return flatMapSlotStatements(context, source, target)
	}
	return nil, false
}

// reduceSlotStatements is reduceStatements with the target given as a
// SLOT rather than a name. The fold's own argument is unchanged — the
// accumulator entry is the join of the seed's effect and the target
// slot's var, and cb's ret lands in that slot — so the two routes emit
// the same statements and differ only in how the slot was reached.
func reduceSlotStatements(
	context *LoweringContext,
	source collectionCall,
	seed *ast.Node,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	seedEffect, seedSort, seedOk := seedEffectOf(context, seed)
	if !seedOk {
		return nil, false
	}
	// the seed is written into the target slot below, so a seed whose
	// sort the slot does not wear declines — the same gate the named
	// route applies, for the same reason: a word tuple left in a
	// number-sorted slot would be admitted into arithmetic by every
	// reader that consults the sort
	if seedSort != BindingKindUnknown && context.Sorts[targetSlot] != BindingKindUnknown &&
		context.Sorts[targetSlot] != seedSort {
		return nil, false
	}
	accumulator := joinEffect(seedEffect, varEffect(targetSlot))
	accumulatorSort := BindingKindUnknown
	if seedSort != BindingKindUnknown && context.Sorts[targetSlot] == seedSort {
		accumulatorSort = seedSort
	}
	converted, convertedOk := convertReduceArrow(
		context, source.Callback, sourceElem,
		accumulator, accumulatorSort, TypeofTagNone,
	)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, targetSlot)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		// the slot starts at the seed, so the join above reads a value the
		// reduce actually had rather than the slot's entry state
		{Kind: kernelbridge.IrStatementAssign, Target: targetSlot, Effect: seedEffect},
		call,
	}, true
}

// findSlotStatements is findStatements with the target given as a SLOT.
// The result is an element the array held OR undefined — the or-absent
// effect over the source's element slot — and the sort gate is the named
// route's: a target sorted differently from the element would be read
// under a sort the values it now holds do not wear.
func findSlotStatements(
	context *LoweringContext,
	source collectionCall,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	if context.Sorts[targetSlot] != BindingKindUnknown &&
		context.Sorts[targetSlot] != context.Sorts[sourceElem] {
		return nil, false
	}
	// the predicate runs, and its own answer goes nowhere
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	element := varEffect(sourceElem)
	return []kernelbridge.IrStatement{
		call,
		{
			Kind:   kernelbridge.IrStatementAssign,
			Target: targetSlot,
			Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &element},
		},
	}, true
}

// flatMapSlotStatements is flatMapStatements with the target given as a
// SLOT. The callback converts and RUNS — which is the whole value of
// recognizing the method — and the result answers unknown, since the
// concatenated array cb's per-element arrays make has no spelling the
// two-slot flattening opened.
func flatMapSlotStatements(
	context *LoweringContext,
	source collectionCall,
	targetSlot int,
) ([]kernelbridge.IrStatement, bool) {
	_, sourceElem, sourceOk := arraySlotsOf(context, source.Receiver)
	if !sourceOk {
		return nil, false
	}
	converted, convertedOk := convertArrayArrow(context, source.Callback, sourceElem)
	if !convertedOk {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, -1)
	if !callOk {
		return nil, false
	}
	return []kernelbridge.IrStatement{
		call,
		{Kind: kernelbridge.IrStatementAssign, Target: targetSlot, Effect: unknownEffect},
	}, true
}

// thenStatements is `p.then(cb)` where p is a PROMISE-HELD local — a
// local the await lowering flattened to one "p.inner" slot holding the
// settled value.
//
// The call is at that slot: `then`'s callback runs on exactly the value
// p settles to, and "p.inner" is where the lowering already put it. cb
// must declare EXACTLY ONE parameter — `then`'s second argument is a
// rejection handler and its callback takes only the settled value, so a
// second declared parameter is a shape this does not model.
//
// A statement that ASSIGNS the result — `q = p.then(cb)` — makes q a
// promise-held local too: `then` answers a promise, and by the
// ret-as-inner convention cb's #ret IS what that promise settles to. So
// q gets its own "q.inner" slot, cb's ret is mapped there, and the slot
// is registered so a later `await q` reads it back through the same
// route `const p = f(…)` set up. A bare `p.then(cb);` emits the call
// with no ret.
//
// What DECLINES: a receiver that is not a promise-held local (a call
// result, an imported promise, a chained `p.then(a).then(b)` whose
// receiver is a call rather than a name), `.catch` and `.finally`
// (their callbacks run on the REJECTION, which nothing here holds), a
// callback with a parameter count other than one, and a target name
// already holding a promise slot — one name, one slot.
func thenStatements(
	context *LoweringContext,
	source collectionCall,
	target string,
) ([]kernelbridge.IrStatement, bool) {
	innerSlot, held := promiseInnerSlotOf(context, source.Receiver)
	if !held {
		return nil, false
	}
	arrow := callbackFunctionOf(context, source.Callback)
	if arrow == nil {
		return nil, false
	}
	if len(arrow.Parameters()) != 1 {
		return nil, false
	}
	entries := []callbackEntry{slotCallbackEntry(context, innerSlot)}
	converted, convertedOk := convertArrow(context, source.Callback, entries)
	if !convertedOk {
		return nil, false
	}
	// a bare `p.then(cb);` — the callback runs, its answer goes nowhere
	if target == "" {
		call, callOk := arrowCallStatement(context, converted, -1)
		if !callOk {
			return nil, false
		}
		return []kernelbridge.IrStatement{call}, true
	}
	if context.Allocate == nil {
		return nil, false
	}
	// one name, one slot: a target already flattened is a second promise
	// under the same spelling, and reusing the first's slot would let two
	// unrelated settled values share it
	if _, already := promiseInnerSlotOf(context, target); already {
		return nil, false
	}
	// the sort is UNKNOWN: what cb returns is the kernel's answer, not
	// something this site reads off syntax. An unknown-sorted slot admits
	// the definedness test alone — coverage lost, never soundness.
	targetSlot, allocated := context.Allocate(target+promiseInnerSuffix, BindingKindUnknown, TypeofTagNone)
	if !allocated {
		return nil, false
	}
	call, callOk := arrowCallStatement(context, converted, targetSlot)
	if !callOk {
		return nil, false
	}
	holdPromiseInnerSlot(context, target, targetSlot)
	return []kernelbridge.IrStatement{call}, true
}

// callbackAssignmentNameOf is the `ys = e` / `const ys = e` shape read
// for its target NAME rather than its slot.
//
// callAssignmentShapeOf (ir_summary_call.go) reads the same two shapes
// but resolves the target through IndexOf, which answers a SCALAR slot
// — a flattened array target has no such slot, so that reader declines
// exactly the statements this one has to admit. The two readers share
// the shapes and differ only in what they resolve the target to; this
// one is used where the target is an array.
func callbackAssignmentNameOf(context *LoweringContext, statement *ast.Node) (name string, rhs *ast.Node, ok bool) {
	if ast.IsVariableStatement(statement) {
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) != 1 {
			return "", nil, false
		}
		d := declarations[0].AsVariableDeclaration()
		if !ast.IsIdentifier(d.Name()) || d.Initializer == nil {
			return "", nil, false
		}
		return d.Name().Text(), d.Initializer, true
	}
	if !ast.IsExpressionStatement(statement) {
		return "", nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return "", nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return "", nil, false
	}
	left := Unwrapped(bin.Left)
	if !ast.IsIdentifier(left) {
		return "", nil, false
	}
	return left.Text(), bin.Right, true
}

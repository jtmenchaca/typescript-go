// split from effect_expression.go — the closure capture census

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// closureCapturedCensus is closureAssignedNames' READ half, over ONE
// closure body: the names the body uses that it did not itself bind —
// its captures — reported in source order of first use, with the write
// set it also assigns.
//
// The two halves are one census because a capture ROW needs both. The
// entry the layout allocates carries a value IN, so every captured name
// the body READS has to be there; the row that maps back OUT is the one
// the body WRITES. `settled` in nest's `onClose` is both — read by the
// `if (settled || …)` guard and written by `settled = true` — so the
// halves are not two disjoint lists and the order is one order.
//
// WHAT IS COUNTED AS BOUND, and therefore not a capture: the closure's
// own parameters, every `var`/`let`/`const` its body declares (including
// a binding pattern's names), a nested function's own name, and a
// `catch` binding. Everything else spelled as a bare identifier in a
// value position is free.
//
// WHAT DECLINES the census outright (ok false), each because a capture
// ENTRY could not stand for what the body does:
//
//   - a NESTED function or class literal inside the body — its own
//     captures would need rows of their own, and the layout allocates
//     one level;
//   - `this` in ANY position — a scalar entry holds no receiver, and
//     even `this.m(…)` moves fields no capture row spells;
//   - an ELEMENT write or read through a captured name (`xs[i] = v`,
//     `xs[i]`) — nothing spells which position the index picked;
//   - a captured name used as a CALL ARGUMENT or stored whole — the
//     receiving code may write through the reference, which no row
//     carries back.
//
// A MEMBER step on a captured name — `stream.writableEnded`,
// `disconnectSource.removeListener(…)`, `p.a = 1`, and a DEEPER path
// `p.a.b` where the caller's own flattening spells that leaf — is NOT a
// decline: it makes the name an OBJECT capture, reported in the
// `objects` list rather than as a scalar row, with its members spelled
// as PATHS below the holder. The two kinds are disjoint by
// construction — a name becomes an object capture the moment a member
// step is seen on it, and the walk then never notes it as a scalar
// read — so the scalar seams (the entry vocabulary, the write-back)
// keep reading one kind of row and the leaf rows are laid out from the
// object report beside them. A name used BOTH ways (`f(p)` beside
// `p.a`) already declined at the hand-over arm.
//
// Over-collection on the READ side is safe (an extra entry takes the
// caller's own slot value and changes nothing), so a name read only
// inside a dead branch still gets its row. Under-collection on the
// WRITE side is not, which is why the write half stays
// closureAssignedNames' own syntactic reading rather than a second one.
func closureCapturedCensus(closure *ast.Node) (
	reads []string,
	objects []capturedObject,
	writes map[string]struct{},
	ok bool,
) {
	if closure == nil || !ast.IsFunctionLike(closure) || closure.Body() == nil {
		return nil, nil, nil, false
	}
	body := closure.Body()
	bound := closureBoundNames(closure)
	writes = map[string]struct{}{}
	closureAssignedNames(closure, writes)
	// a write to a name the closure BOUND is its own local's, not a
	// capture — the row list carries only what crosses the boundary
	for name := range writes {
		if _, isBound := bound[name]; isBound {
			delete(writes, name)
		}
	}
	census := &captureCensus{
		bound: bound,
		seen:  map[string]struct{}{},
		// the OBJECT captures, in source order of the first member step, each
		// carrying its member order for the same reason
		objectOrder: []string{},
		objectOf:    map[string]*capturedObject{},
	}
	census.visit(body)
	if census.declined {
		return nil, nil, nil, false
	}
	// A NAME USED BOTH WAYS refuses the whole census. The hand-over arm
	// notes a bare-identifier call argument as a SCALAR read
	// (`f(disconnectSource)`), which is right for a scalar and wrong for
	// an object: the callee may store into the object, and this census
	// would then lay leaf entries out for a bundle whose members code it
	// cannot see may have moved. The two kinds are disjoint or there is
	// no census.
	for _, name := range census.reads {
		if _, isObject := census.objectOf[name]; isObject {
			return nil, nil, nil, false
		}
	}
	// closureAssignedNames records BOTH spellings of a step (`p` and
	// `p.a`), so an object capture's leaf write arrives here under a
	// dotted name and its holder under a bare one. Neither belongs to
	// the SCALAR write set: the dotted spelling is the object's own row
	// (capturedMemberWrite already noted it), and the bare one names a
	// holder no single slot stands for.
	for name := range writes {
		if root, _, isPath := splitOneStep(name); isPath {
			if _, isObject := census.objectOf[root]; isObject {
				delete(writes, name)
			}
			continue
		}
		if _, isObject := census.objectOf[name]; isObject {
			delete(writes, name)
		}
	}
	// every WRITTEN capture must also have a row, since its entry is
	// what the write-back maps through. A name written without ever
	// being read is still an entry — it enters holding the caller's
	// value and exits holding the closure's.
	for name := range writes {
		census.note(name)
	}
	for _, name := range census.objectOrder {
		objects = append(objects, *census.objectOf[name])
	}
	return census.reads, objects, writes, true
}

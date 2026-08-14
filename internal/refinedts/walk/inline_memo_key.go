// The inline replay key — split from inline_contract_body.go when the
// field-narrowed spelling grew it past the file-length ceiling. One
// deterministic flat string per (arguments, observed state): argument
// values spell whole; an observed OBJECT spells only the fields the
// callee's body reads (dataflowfacts.ObservedPathsOf), whole-value on
// any whole use — the soundness statement sits at the narrowing
// branch below.

package walk

import (
	"sort"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// computeInlineMemoKey builds the DETERMINISTIC replay key —
// "" where nothing can be safely spelled (the TS source's try/catch
// around JSON.stringify answered the same way). SpellForMemoKey
// writes one flat string per value — compiler symbols by pointer
// identity, non-finite floats as words — so the key builds without
// a JSON pass, and this runs on EVERY inline call, hits included.
// The nodes and the values both come from the ONE effective-argument
// list the binding itself uses (EffectiveArgumentsOf), so a tagged
// template's slots key the same way a plain call's do, and the key's
// argument spellings are exactly the values ParameterKnown binds. A nil
// node is a position with no expression to scan — a synthesized
// template object, or an item expanded out of a spread.
//
// EXPANSION AND THE KEY: a spread changes how many positions one call
// node occupies, and the key spells every effective VALUE in order, so
// two calls of one declaration whose spreads expanded differently spell
// different keys — different lengths, or different values at some
// position. A hit therefore replays an inline of the same expansion,
// never of a different one. A spread that did NOT expand holds one
// position spelling residue, which an exact list can hold at that
// position too — so the list's EXACTNESS rides in the key as well,
// since it is what a rest parameter's binding turns on (the marker
// written below).
//
// A TAGGED call and a plain call of the same declaration need no
// separate discriminator in the key. The memo is already sharded per
// contract.Declaration, so the only pair that could collide is two
// calls of one declaration; and the key spells every argument VALUE,
// where a tagged call's position 0 is the template object — the exact
// list of that literal's cooked strings. A plain call keys the same
// only by passing an equal exact list into the same position, and then
// the parameter bindings really are equal, so the replayed outcome is
// the one the walk would have produced. The key's argument spellings
// carry the distinction; the call node's own id rides only where a
// callback argument makes the body's behavior node-dependent.
func computeInlineMemoKey(ctx *FlowContext, env Env, call *ast.Node, contract *FunctionContract, calleeName *ast.Node, effective EffectiveArguments) string {
	var callbacks []*ast.Node
	for _, a := range effective.Nodes {
		if a == nil {
			continue
		}
		if ast.IsArrowFunction(a) || ast.IsFunctionExpression(a) {
			callbacks = append(callbacks, a)
		}
	}
	var observed []string
	// paths mirrors `observed` one name at a time: a non-nil entry is
	// the sorted first-level keys the union of bodies reads off that
	// name; a nil entry (present in the map) means some body used the
	// name whole — the union of two contributors takes the WIDER
	// reading, since either body observing it whole means the caller's
	// key must cover the whole value for the pair to be sound.
	paths := map[string][]string{}
	unionPaths := func(from map[string][]string) {
		for name, keys := range from {
			existingKeys, seen := paths[name]
			if !seen {
				paths[name] = keys
				continue
			}
			if keys == nil || existingKeys == nil {
				paths[name] = nil
				continue
			}
			paths[name] = unionSortedKeys(existingKeys, keys)
		}
	}
	if len(callbacks) == 0 {
		observed = ObservedOf(contract.Declaration)
		unionPaths(dataflowfacts.ObservedPathsOf(contract.Declaration))
	} else {
		set := map[string]struct{}{}
		for _, name := range ObservedOf(contract.Declaration) {
			set[name] = struct{}{}
		}
		unionPaths(dataflowfacts.ObservedPathsOf(contract.Declaration))
		for _, callback := range callbacks {
			for _, name := range dataflowfacts.ObservedNamesOf(callback) {
				set[name] = struct{}{}
			}
			unionPaths(dataflowfacts.ObservedPathsOfNode(callback))
		}
		for name := range set {
			observed = append(observed, name)
		}
		sort.Strings(observed)
	}
	// one flat key: args then observed env rows, each spelled by the
	// builder speller — \x1e separates spells, and names (identifiers,
	// no control bytes) bind with '=' — injective without a JSON pass
	var key strings.Builder
	if len(callbacks) > 0 {
		key.WriteByte('@')
		key.WriteString(strconv.Itoa(CallNodeIdOf(call)))
		key.WriteByte('|')
	}
	// an INEXACT list keys apart from an exact one: the values alone do
	// not decide the binding, because a REST parameter reads a rest LIST
	// off an exact list and residue off an inexact one. `f(a, ...xs)`
	// with an unread spread and `f(a, b)` with b holding residue spell
	// the same values, and their rest bindings differ — so the exactness
	// rides in the key and the two never share an outcome.
	if !effective.Exact {
		key.WriteString("~|")
	}
	for _, arg := range effective.Knowns {
		spelled, ok := abstractdomain.SpellForMemoKey(arg)
		if !ok {
			if tracing.Recording(tracing.GrainStep) {
				tracing.Count("inline.unkeyed."+calleeName.Text(), 0)
			}
			return ""
		}
		key.WriteString(spelled)
		key.WriteByte('\x1e')
	}
	key.WriteByte(';')
	for _, name := range observed {
		held, ok := env.Get(name)
		if !ok {
			continue
		}
		// SOUNDNESS OF THE NARROWED KEY: the memo replays an outcome
		// that is a function of what the body READS. Observing exactly
		// the read keys (plus the whole value for any name any
		// contributing body used whole) keys the memo on no less than
		// the body's true input, so two caller states with equal keys
		// are indistinguishable to the body — a churn on a key the
		// body never reads cannot change the replayed outcome, so it
		// must not change the key.
		fieldKeys := paths[name]
		var heldSpell string
		narrowed := false
		if fieldKeys != nil && held.Kind == abstractdomain.KindObject {
			heldSpell, narrowed = spellObjectFieldsForMemoKey(held, fieldKeys)
			if narrowed && tracing.Recording(tracing.GrainStep) {
				tracing.Count("inline.fieldkey", 0)
			}
		}
		if !narrowed {
			// nil entry, a spell failure, or a non-object holder: the
			// whole-value path today's memo always took
			heldSpell, ok = abstractdomain.SpellForMemoKey(held)
		} else {
			ok = true
		}
		if !ok {
			if tracing.Recording(tracing.GrainStep) {
				tracing.Count("inline.unkeyed."+calleeName.Text(), 0)
			}
			return ""
		}
		key.WriteString(name)
		key.WriteByte('=')
		key.WriteString(heldSpell)
		key.WriteByte('\x1e')
	}
	return key.String()
}

// unionSortedKeys merges two sorted, duplicate-free key lists into one.
func unionSortedKeys(a, b []string) []string {
	set := make(map[string]struct{}, len(a)+len(b))
	for _, k := range a {
		set[k] = struct{}{}
	}
	for _, k := range b {
		set[k] = struct{}{}
	}
	merged := make([]string, 0, len(set))
	for k := range set {
		merged = append(merged, k)
	}
	sort.Strings(merged)
	return merged
}

// spellObjectFieldsForMemoKey spells only the listed keys' values off
// an object-kind env value — a key ABSENT from the object spells a
// fixed marker distinct from any real spelling, so "key missing" and
// "key present with an unkeyable value" never collide. False bubbles
// any inner spell failure up to the whole-value fallback, exactly
// like SpellForMemoKey's own false.
func spellObjectFieldsForMemoKey(held abstractdomain.AbstractValue, fieldKeys []string) (string, bool) {
	var b strings.Builder
	b.WriteString("fo(")
	for i, fieldKey := range fieldKeys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(fieldKey))
		b.WriteByte('=')
		var fieldValue *abstractdomain.AbstractValue
		for _, objectKey := range held.Keys {
			if objectKey.Name == fieldKey {
				fieldValue = &objectKey.Value
				break
			}
		}
		if fieldValue == nil {
			b.WriteString("\x01absent")
			continue
		}
		spelled, ok := abstractdomain.SpellForMemoKey(*fieldValue)
		if !ok {
			return "", false
		}
		b.WriteString(spelled)
	}
	b.WriteByte(')')
	return b.String(), true
}

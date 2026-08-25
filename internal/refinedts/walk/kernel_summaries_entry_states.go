// Building the entry-state vector applySummary sends into the kernel:
// one KnownStateWire per slot, read from a call's own arguments and
// receiver, following the same three-way decline/fill rule at every
// bundle, array, binding-pattern, and expanded-member position.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// summaryEntryStates builds the entry states a call sends, one per
// ENTRY — which is no longer one per declared parameter: a type-literal
// parameter expands to one entry per member (SummaryParameterEntries,
// ir_summary_body.go), and its argKnown is one OBJECT abstract value
// that has to be read apart field by field.
//
// argKnowns is indexed by DECLARED PARAMETER; the state vector runs
// ahead of it wherever a parameter expanded. Everything else is the
// existing scalar rule verbatim: a missing argument enters absent, and a
// value the wire cannot spell declines THIS call (never the summary,
// which quantifies over every entry).
//
// An expanded member's fill reads the argument three ways:
//
//   - a POSITIVELY NON-OBJECT argument (a definite scalar — a "values"
//     kind holding a number, string, or boolean) DECLINES this call: the
//     caller's own value can never carry the member the layout expects,
//     so no entry state exists to send and the whole call is refused
//     rather than served on a fabricated TOP;
//   - a KNOWN OBJECT that does not name a declared member ALSO DECLINES:
//     the caller's own object told the checker its keys, and none of
//     them is the one the layout expects, so again there is no state to
//     send;
//   - anything else — an argument whose kind is unknown or opaque —
//     fills that one entry TOP, thisEntryState's rule below, which the
//     class bundle above already takes through BundleParamEntryStates.
//     TOP, and never absent: an argument the caller knows nothing about
//     made no claim that the member is undefined, and TOP is what the
//     entry quantifier already covers, so filling it costs precision and
//     never soundness.
//
// The SLOT VECTOR's shape is unaffected by any of the three: one entry
// per declared member is what the layout agreement requires, and only a
// DECLINE (never a fill) changes how many entries a call sends.
//
// Past the declared parameters the vector holds a METHOD's this-entries,
// which the RECEIVER fills field by field (thisEntryStates below), or an
// arrow route's captures, which this route never has; anything still
// short of ParamCount fills absent.
func summaryEntryStates(
	ctx *FlowContext,
	declaration *ast.Node,
	summary LoweredSummary,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) ([]kernelbridge.KnownStateWire, bool) {
	states := make([]kernelbridge.KnownStateWire, 0, summary.SlotCount)
	for index, parameter := range declaration.Parameters() {
		if len(states) >= summary.ParamCount {
			break
		}
		// a CLASS-TYPED parameter's bundle rows fill from that
		// parameter's own argument, field by field, in the layout's own
		// order — the scalar rule would push one whole-name state at the
		// wrong index. A non-object or missing argument tops every row
		// (BundleParamEntryStates' rule): a class instance the caller
		// knows nothing about is the routine case, and the bundle exists
		// so the body can read fields the caller never had.
		if _, census, _, isBundle := BundleParamCensus(ctx, declaration.Body(), parameter); isBundle && census.Believable() && len(census.Reads) > 0 {
			var argument abstractdomain.AbstractValue
			if index < len(argKnowns) {
				argument = argKnowns[index]
			}
			row, filled := BundleParamEntryStates(argument, census.Reads)
			if !filled {
				return nil, false
			}
			states = append(states, row...)
			continue
		}
		// a REST parameter's entry is TOP whatever the call passed: the
		// bound array is always defined and its contents unspellable —
		// never absent, which would claim an array that always exists is
		// undefined
		if parameter.AsParameterDeclaration().DotDotDotToken != nil {
			states = append(states, kernelbridge.KnownStateWire{Top: true})
			continue
		}
		// an ARRAY-TYPED parameter carries the entries the layout emitted:
		// a length plus either one scalar elem entry, or — where the
		// element itself expands as a record (ElementMembers, step 1/2 of
		// the records-as-array-elements build) — one "xs.elem.<member>"
		// entry per member, in place of the single scalar one. Every entry
		// enters TOP: the direct apply reads an argument's abstract value,
		// which carries no length, no element join, and no per-member
		// join this route can spell, and TOP is what the entry quantifier
		// already covers. Never absent, which would claim an array the
		// caller passed is undefined; never a narrower count, which would
		// slide every later parameter's entries by the difference —
		// reading the SAME local arrayParamSlotsIn's memo already resolved
		// (rather than counting members again here) is what keeps this
		// seam and the layout seam pushing entries of one agreed width.
		if local, flattened := arrayParamSlotsIn(ctx, parameter); flattened {
			count := 1 + max(1, len(local.ElementMembers))
			for i := 0; i < count; i++ {
				states = append(states, kernelbridge.KnownStateWire{Top: true})
			}
			continue
		}
		// a BINDING-PATTERN parameter: one state per bound entry, each
		// read from the argument object's member by the entry's Key —
		// thisEntryState's TOP fallbacks for a non-object argument or an
		// unnamed member, exactly the bundle rule
		if pd := parameter.AsParameterDeclaration(); pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) {
			entries, entriesOk := SummaryParameterEntriesIn(ctx, parameter)
			if !entriesOk {
				return nil, false
			}
			var argument abstractdomain.AbstractValue
			if index < len(argKnowns) {
				argument = argKnowns[index]
			}
			for _, entry := range entries {
				// a TOP-entry row (a defaulted or rest pattern element) never
				// fills from the argument's member: the bound value is
				// member-or-default (or a fresh rest object), and a definite
				// member state would claim what the runtime did not bind
				// (bodySlot.TopEntry's doc)
				if entry.TopEntry {
					states = append(states, kernelbridge.KnownStateWire{Top: true})
					continue
				}
				states = append(states, thisEntryState(argument, entry.Key))
			}
			continue
		}
		members, expanded := recordParamMembersIn(ctx, parameter)
		if !expanded {
			if index >= len(argKnowns) {
				// a DEFINITELY-MISSING argument on a DEFAULTED parameter
				// enters holding the default: the body's definedness branch
				// then joins two identical values and the answer stays
				// exact. Only a CONST or CONSTSTATE default can cross —
				// anything else indexes the callee's binding space.
				if effect, defaulted := summary.DefaultEffects[len(states)]; defaulted {
					if state, constant := constEffectState(effect); constant {
						states = append(states, state)
						continue
					}
				}
				states = append(states, absentState)
				continue
			}
			wire, ok := StateOfKnown(argKnowns[index])
			if !ok {
				return nil, false
			}
			states = append(states, wire)
			continue
		}
		if index >= len(argKnowns) {
			for range members {
				states = append(states, absentState)
			}
			continue
		}
		// each member entry reads the argument's own field, one PATH STEP
		// at a time for a nested member — expandedMemberEntryState's rule,
		// applied at every step: DECLINES this call on a positively
		// non-object argument or a known object missing the step, TOPS
		// only where the step's own kind is unknown or opaque.
		argument := argKnowns[index]
		for _, member := range members {
			state, filled := expandedMemberEntryState(argument, member.Path)
			if !filled {
				return nil, false
			}
			states = append(states, state)
		}
	}
	// the METHOD's this-entries, filled from the receiver's own field
	// knowledge — the same objectKeyIndex/StateOfKnown reading the
	// record-parameter apply above takes
	for _, entry := range summary.BundleEntries {
		if entry.Index < len(states) || entry.Index >= summary.ParamCount {
			continue
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			continue
		}
		for len(states) < entry.Index {
			states = append(states, absentState)
		}
		states = append(states, thisEntryState(receiver, field))
	}
	for len(states) < summary.ParamCount {
		states = append(states, absentState)
	}
	return states, true
}

// thisFieldNameOf is the field behind a this-entry's slot spelling:
// "this.container" is container. Anything not spelled under `this` is
// some other bundle's row (a record parameter's leaf) and answers false.
func thisFieldNameOf(path string) (string, bool) {
	const under = "this."
	if len(path) <= len(under) || path[:len(under)] != under {
		return "", false
	}
	return path[len(under):], true
}

// thisEntryState is what ONE this-field entry enters holding, read from
// the call's receiver.
//
// A receiver that is an OBJECT carrying knowledge of the field enters
// with that field's own state. Everything else enters TOP — a receiver
// that is not an object, one whose knowledge does not name this field,
// and a field whose value the wire cannot spell.
//
// TOP, and never ABSENT: an unknown field is UNKNOWN, while absent would
// claim the field is undefined — a claim no receiver made, and one the
// body's reads would then narrow on. TOP is what the entry quantifier
// already covers, so filling it costs precision and never soundness.
func thisEntryState(receiver abstractdomain.AbstractValue, field string) kernelbridge.KnownStateWire {
	if receiver.Kind != abstractdomain.KindObject {
		return kernelbridge.KnownStateWire{Top: true}
	}
	at, has := objectKeyIndex(receiver, field)
	if !has {
		return kernelbridge.KnownStateWire{Top: true}
	}
	wire, ok := StateOfKnown(receiver.Keys[at].Value)
	if !ok {
		return kernelbridge.KnownStateWire{Top: true}
	}
	return wire
}

// isDefiniteScalarArgument answers whether an argument is a definite
// SCALAR — a "values" kind holding a number, string, or boolean — the
// one shape an expanded record parameter's member can never read a
// value from, whatever the member's name.
func isDefiniteScalarArgument(argument abstractdomain.AbstractValue) bool {
	if argument.Kind != abstractdomain.KindValues {
		return false
	}
	switch argument.KindTag {
	case abstractdomain.PrimitiveNumber, abstractdomain.PrimitiveString, abstractdomain.PrimitiveBoolean:
		return true
	}
	return false
}

// expandedMemberEntryState is what ONE expanded record parameter's
// member entry enters holding, read from the call's own argument,
// walking the member's key PATH one step per nesting level — ["lo"] for
// a flat member, ["inner","deep"] for a member nestedMemberLeavesOf
// recursed into.
//
// THE THREE-WAY RULE APPLIES AT EVERY STEP, interior or leaf. A
// POSITIVELY NON-OBJECT value at that step (isDefiniteScalarArgument)
// DECLINES THIS CALL: it can never carry the next segment, so there is
// no state to send. A KNOWN OBJECT not naming the next segment also
// DECLINES: the caller told the checker its own keys, and the segment
// is not among them. Everything else — the step's own kind is unknown
// or opaque — TOPS, thisEntryState's rule, because nothing here rules
// the rest of the path either present or absent. An INTERIOR step that
// TOPs stops the walk there: unknown of the parent means unknown of
// every child, so the leaf entry also fills TOP rather than reading
// further into a value nothing is known about.
//
// A single-segment path — every member before nested families existed,
// and every flat member after — reads exactly ONE step and answers
// thisEntryState's own reading, so nothing already landed changes
// behavior.
func expandedMemberEntryState(argument abstractdomain.AbstractValue, path []string) (kernelbridge.KnownStateWire, bool) {
	if len(path) == 0 {
		return kernelbridge.KnownStateWire{}, false
	}
	current := argument
	for index, step := range path {
		leaf := index == len(path)-1
		if isDefiniteScalarArgument(current) {
			return kernelbridge.KnownStateWire{}, false
		}
		if current.Kind != abstractdomain.KindObject {
			// unknown or opaque at this step: TOP covers the rest of the
			// path, leaf or interior alike — nothing here rules further
			return kernelbridge.KnownStateWire{Top: true}, true
		}
		keyAt, has := objectKeyIndex(current, step)
		if !has {
			return kernelbridge.KnownStateWire{}, false
		}
		if leaf {
			return thisEntryState(current, step), true
		}
		current = current.Keys[keyAt].Value
	}
	return kernelbridge.KnownStateWire{}, false
}

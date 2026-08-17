// split from ir_summary_call.go — the resolved callee's call statement: the entry vector and the ret vector

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// summaryCallStatement builds the call statement for a resolved callee
// with a compiled summary: each argument lowers as an effect over the
// caller's bindings (any that does not declines the whole call), and
// Rets names the caller slot the callee's RETURN out-state writes — -1
// everywhere else, which drops those out-states.
//
// `target` is the caller slot the call's value lands in, or -1 for a
// bare call statement whose value nothing reads.
func summaryCallStatement(context *LoweringContext, call *ast.Node, target int) (kernelbridge.IrStatement, bool) {
	if context.Flow == nil || context.SummaryTable == nil {
		return kernelbridge.IrStatement{}, false
	}
	callee := summaryCalleeOf(context, call)
	if callee == nil {
		return kernelbridge.IrStatement{}, false
	}
	blob, has := SummaryBlobFor(context.Flow, callee)
	if !has {
		return kernelbridge.IrStatement{}, false
	}
	// SummaryBlobFor's "has" answers COMPILE success only — a porous
	// body still compiles a blob, and "porous blobs no longer answer
	// calls" (applySummary's top-level serving rule) is the guard an
	// EMBEDDED call needs too: splicing a porous callee's blob into
	// this body's statements would compose that callee's weakened ret
	// while this body still records itself complete. The same gate
	// HoistCallEffect enforces, at the statement-route embed.
	if outcome, _, recorded := SummaryOutcomeOf(callee); !recorded || outcome != SummaryComplete {
		return kernelbridge.IrStatement{}, false
	}
	outIndex, shapeOk := SummaryOutShapeFor(context.Flow, callee)
	if !shapeOk {
		return kernelbridge.IrStatement{}, false
	}
	// a NEW expression serves through the same statement, with three
	// differences the code below branches on: its this-entries stay
	// ABSENT (a fresh instance's fields before the initializers run —
	// never any caller receiver's slots), its unwritten ret maps nowhere
	// (the instance is a value, not undefined — the caller assigns
	// unknown separately), and an expanded-parameter argument declines
	// (the threading below reads a CallExpression).
	isNew := ast.IsNewExpression(call)
	var callArguments []*ast.Node
	if isNew {
		if newArguments := call.AsNewExpression().Arguments; newArguments != nil {
			callArguments = newArguments.Nodes
		}
	} else {
		if a := call.AsCallExpression().Arguments; a != nil {
			callArguments = a.Nodes
		}
	}
	parameters := callee.Parameters()
	// arity: extra arguments are admitted only into a trailing REST
	// parameter, and each extra one must MOVE NOTHING — its value lands
	// in the rest array, whose entry is unknown regardless, so only its
	// evaluation effects matter and an inert one has none
	restParameter := len(parameters) > 0 &&
		parameters[len(parameters)-1].AsParameterDeclaration().DotDotDotToken != nil
	if len(callArguments) > len(parameters) {
		if !restParameter {
			return kernelbridge.IrStatement{}, false
		}
		for _, extra := range callArguments[len(parameters):] {
			if !writeAndCallFree(extra) {
				return kernelbridge.IrStatement{}, false
			}
		}
	}
	// an argument that WRITES would move the caller's state on the way
	// in, which the effect grammar does not carry
	for _, argument := range callArguments {
		if ast.IsSpreadElement(argument) || ContainsWrite(argument) {
			return kernelbridge.IrStatement{}, false
		}
	}
	// one entry effect per callee SLOT — the callee's arity is its whole
	// binding vector (see buildSummaryBlob), so the parameters come from
	// the call, every local and the result slot enter absent, and the
	// done flag enters {0}: exactly the entry states the apply side
	// sends, so the spliced compile and the direct apply agree
	calleeShape, shapeKnown := LowerSummaryBody(context.Flow, callee)
	if !shapeKnown {
		return kernelbridge.IrStatement{}, false
	}
	// a callee that RETURNS ITS RECEIVER hands the composed caller an
	// alias it may write through later — writes this route's rets could
	// never carry back. The call declines to the opaque tier, whose
	// receiver-bundle havoc is the honest answer.
	if calleeShape.ReturnsReceiver {
		return kernelbridge.IrStatement{}, false
	}
	// the callee's parameters no longer map 1:1 onto entries: a type-
	// literal parameter EXPANDS to one entry per member. The entry list is
	// built by walking the declared parameters through the very expansion
	// the layout used (SummaryParameterEntries), so a drift between the
	// two is impossible — one function answers both.
	args := make([]kernelbridge.LoopEffect, 0, calleeShape.SlotCount)
	for index, parameter := range parameters {
		entries, entriesOk := SummaryParameterEntries(parameter)
		if !entriesOk {
			return kernelbridge.IrStatement{}, false
		}
		// a CLASS-TYPED parameter expanded to one entry per read field;
		// placeholders hold the positions and bundleParamRetsAndArgs
		// below overwrites them from the argument's own spelled path
		if _, census, _, isBundle := BundleParamCensus(context.Flow, callee.Body(), parameter); isBundle && census.Believable() && len(census.Reads) > 0 {
			if isNew {
				// the placeholder rows below are overwritten by the
				// threading this route skips for a new — absent rows would
				// CLAIM the argument's fields are undefined
				return kernelbridge.IrStatement{}, false
			}
			for range census.Reads {
				args = append(args, kernelbridge.AbsentConst())
			}
			continue
		}
		// a BINDING-PATTERN parameter: one argument effect per bound
		// entry, each reading the argument object's member by the entry's
		// Key — a binding-pattern element always binds a DEPTH-1 member
		// (its PropertyName is a single identifier), so the pseudo
		// member's Path is the one-segment path Key already names
		if pd := parameter.AsParameterDeclaration(); pd.Name() != nil && ast.IsObjectBindingPattern(pd.Name()) {
			if index >= len(callArguments) {
				for range entries {
					args = append(args, kernelbridge.AbsentConst())
				}
				continue
			}
			pseudo := make([]recordParamMember, len(entries))
			for at, entry := range entries {
				pseudo[at] = recordParamMember{Key: entry.Key, Path: []string{entry.Key}, Sort: entry.Sort, TypeofTag: entry.TypeofTag}
			}
			leafEffects, leavesOk := recordArgumentEffects(context, pseudo, callArguments[index])
			if !leavesOk {
				return kernelbridge.IrStatement{}, false
			}
			args = append(args, leafEffects...)
			continue
		}
		// an ARRAY-TYPED parameter's entries take the caller's own
		// flattened array slots — "<argument>.len" and "<argument>.elem",
		// or, where the element type expands as a record (ElementMembers),
		// "<argument>.len" plus one "<argument>.elem.<member>" per member —
		// where the argument is a bare name the caller flattened the same
		// way. Anything else — a literal, a call's result, a name the
		// caller kept whole — fills every entry UNKNOWN rather than absent:
		// the callee is passed a real array, and absent would claim it is
		// undefined. The effect count always matches the entry width the
		// layout emitted (1+len(ElementMembers), or 2 for the scalar pair),
		// which is what keeps this vector the same length the layout laid
		// out — a narrower count would slide every later parameter's
		// entries by the difference (kernel_summaries.go's
		// summaryEntryStates states the same rule for the TOP-fill seam).
		if local, flattened := arrayParamSlotsIn(context.Flow, parameter); flattened {
			if len(local.ElementMembers) > 0 {
				effects := make([]kernelbridge.LoopEffect, 1+len(local.ElementMembers))
				for at := range effects {
					effects[at] = unknownEffect
				}
				if index < len(callArguments) {
					if head := Unwrapped(callArguments[index]); ast.IsIdentifier(head) {
						name := head.Text()
						if lenSlot, hasLen := slotIndexOfName(context, name+".len"); hasLen {
							// each leaf is resolved by the CALLEE member's own
							// path under the caller's spelling, so entry k can
							// only ever take the caller leaf that names it —
							// and all-or-nothing, since a partial match would
							// mix resolved leaves with unknowns of a shape the
							// callee's entry order no longer separates
							resolved := []kernelbridge.LoopEffect{varStateEffect(lenSlot)}
							complete := true
							for _, member := range local.ElementMembers {
								leaf, hasLeaf := slotIndexOfName(context, name+".elem."+strings.Join(member.Path, "."))
								if !hasLeaf {
									complete = false
									break
								}
								resolved = append(resolved, varStateEffect(leaf))
							}
							if complete {
								effects = resolved
							}
						}
					}
				}
				args = append(args, effects...)
				continue
			}
			lenEffect, elemEffect := unknownEffect, unknownEffect
			if index < len(callArguments) {
				if head := Unwrapped(callArguments[index]); ast.IsIdentifier(head) {
					if lenSlot, elemSlot, isArray := arraySlotsOf(context, head.Text()); isArray {
						lenEffect, elemEffect = varStateEffect(lenSlot), varStateEffect(elemSlot)
					}
				}
			}
			args = append(args, lenEffect, elemEffect)
			continue
		}
		members, expanded := recordParamMembersOf(parameter)
		if expanded {
			// a missing argument leaves every leaf absent — the same "entered
			// absent" the scalar case gives an omitted argument
			if index >= len(callArguments) {
				for range entries {
					args = append(args, kernelbridge.AbsentConst())
				}
				continue
			}
			leafEffects, leavesOk := recordArgumentEffects(context, members, callArguments[index])
			if !leavesOk {
				return kernelbridge.IrStatement{}, false
			}
			args = append(args, leafEffects...)
			continue
		}
		if index >= len(callArguments) {
			// a DEFINITELY-MISSING argument on a DEFAULTED parameter rides
			// the default itself — a CONST effect means the same thing in
			// every binding space, so the callee's lowered default is this
			// caller's argument effect verbatim. The body's definedness
			// branch then joins two identical values and stays exact.
			if effect, defaulted := calleeShape.DefaultEffects[len(args)]; defaulted {
				if effect.Kind == kernelbridge.LoopEffectConst || effect.Kind == kernelbridge.LoopEffectConstState {
					args = append(args, effect)
					continue
				}
			}
			args = append(args, kernelbridge.AbsentConst())
			continue
		}
		argument := callArguments[index]
		effect, ok := RhsEffect(context, SortOfArg(context, argument), argument)
		if !ok {
			return kernelbridge.IrStatement{}, false
		}
		args = append(args, asVarStateEffect(effect))
	}
	for len(args) < calleeShape.SlotCount {
		if len(args) == calleeShape.DoneIndex {
			args = append(args, kernelbridge.LoopEffect{
				Kind: kernelbridge.LoopEffectConst,
				Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0})),
			})
			continue
		}
		args = append(args, kernelbridge.AbsentConst())
	}
	// Rets: -1 says nothing reads that out-state. The return out-state
	// maps where the site has a slot for it, and a WRITTEN this-field
	// entry maps back into the caller's own slot for that field
	// (bundleRetsAndArgs below).
	//
	// The vector runs to the callee's WHOLE slot count, not to its ret
	// index: a callee whose returns carry MEMBER slots has those slots
	// past #ret (returnedLiteralShape's allocation sits after it), and a
	// vector stopping at #ret would leave rows the kernel answers with no
	// position to be named at. Every row past #ret stays -1 unless the
	// member threading below claims it — the statement route's caller has
	// one scalar slot for the call's value, and a fresh object's members
	// map onto no caller slot it already holds.
	retsLength := outIndex + 1
	if calleeShape.SlotCount > retsLength {
		retsLength = calleeShape.SlotCount
	}
	rets := make([]int, retsLength)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 && !isNew {
		rets[outIndex] = target
	}
	// A MEMBER-CARRYING RETURN at a statement-route call site: the callee
	// built a fresh object whose members ride their own exits, and the
	// caller's target is ONE scalar slot. There is nothing to write those
	// members into — no caller slot spells "the k-th key of the value this
	// call is about to produce" — so the rows stay -1 and the target keeps
	// the scalar #ret's unknown, exactly as before this shape existed.
	//
	// The value is not lost: the DIRECT APPLY route (applySummary) rebuilds
	// the object from these same exits, and that is the route every
	// expression-position call takes. What this seam owes is only that the
	// two agree about WHICH slot is which member, which they do by reading
	// one list — the callee's own RetMembers.
	if !threadRetMemberRets(context, calleeShape, target, rets) {
		return kernelbridge.IrStatement{}, false
	}
	// the RECEIVER decides the this-entry fill: the callee's own
	// "this.<field>" entries take the caller's "<receiverPath>.<field>"
	// slots, and the ones it writes ride back out through rets. A NEW
	// skips both threadings whole: its instance is FRESH — the absent
	// fill already in place is exactly a field before its initializer —
	// and its writes land on an object no caller slot spells yet.
	if !isNew {
		if !bundleRetsAndArgs(context, call, calleeShape, args, rets) {
			return kernelbridge.IrStatement{}, false
		}
		if !bundleParamRetsAndArgs(context, call, callee, calleeShape, args, rets) {
			return kernelbridge.IrStatement{}, false
		}
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(callee, blob),
		Args:   args,
		Rets:   rets,
	}, true
}

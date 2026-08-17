// split from ir_summary_call.go — the callee's parameter bundles: their fill from the arguments and their write-backs

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// bundleParamRetsAndArgs is bundleRetsAndArgs' half for the CALLEE'S
// PARAMETER bundles — the class-typed ones and the record-expanded ones,
// which are one rule here. A "this."-rooted row is filled from the
// call's receiver; a "<holder>."-rooted row is filled from the ARGUMENT
// passed at that parameter's position, and the rule is the same one step
// over: the argument's own spelled dotted path prefixes the field name,
// and the caller's slot of that spelling fills the entry.
//
//	f(wrapper)          → the caller's "wrapper.<field>" slots
//	f(this.wrapper)     → the caller's "this.wrapper.<field>" slots
//
// An argument that is not a spelled path (a call's result, a literal, a
// computed or optional step) fills its rows UNKNOWN, never absent: the
// caller holds no name for that object, and nothing is what the entry
// must say. Unlike the receiver case this does not decline the site —
// the argument is still passed by value and the rest of the call is
// exactly as sound.
//
// A RECORD-EXPANDED parameter takes the write-back half alone, and this
// is the difference from the class-typed rows. Its args were already
// filled by recordArgumentEffects, which reads BOTH shapes the record
// route admits — an object literal (case (a)) and a flattened record
// local (case (b)) — and a literal's leaves are effects no dotted path
// spells. Overwriting them from the path would lose the literal's own
// values, so only rets moves here. A written leaf maps back exactly where
// case (b) gave the caller a slot to map into, which is the caller's
// "q.<member>"; a case-(a) literal has no such slot, the row's ret stays
// -1, and the write lands nowhere because the object the callee wrote is
// one the caller kept no name for.
func bundleParamRetsAndArgs(
	context *LoweringContext,
	call *ast.Node,
	callee *ast.Node,
	calleeShape LoweredSummary,
	args []kernelbridge.LoopEffect,
	rets []int,
) bool {
	if context.Flow == nil {
		return true
	}
	callExpr := call.AsCallExpression()
	var callArguments []*ast.Node
	if callExpr.Arguments != nil {
		callArguments = callExpr.Arguments.Nodes
	}
	for index, parameter := range callee.Parameters() {
		holder, _, _, isBundle := BundleParamCensus(context.Flow, callee.Body(), parameter)
		if !isBundle {
			// a RECORD-EXPANDED parameter is a bundle of another kind: its
			// leaves are spelled under the parameter's own name, so the holder
			// is that name and the rows read back by the same field split
			if recordHolder, expanded := recordParamHolderOf(context.Flow, parameter); expanded {
				if !recordParamRets(context, calleeShape, recordHolder, index, callArguments, args, rets) {
					return false
				}
			}
			continue
		}
		argumentPath := ""
		hasPath := false
		if index < len(callArguments) {
			argumentPath, hasPath = dottedPathOf(Unwrapped(callArguments[index]))
		}
		for _, entry := range calleeShape.BundleEntries {
			field, isRow := BundleParamFieldNameOf(entry.Path, holder)
			if !isRow {
				continue
			}
			if entry.Index < 0 || entry.Index >= len(args) {
				return false
			}
			if !hasPath {
				args[entry.Index] = unknownEffect
				continue
			}
			slot, held := slotIndexOfName(context, argumentPath+"."+field)
			if !held {
				args[entry.Index] = unknownEffect
				continue
			}
			args[entry.Index] = varStateEffect(slot)
			if entry.Written && entry.Index < len(rets) {
				rets[entry.Index] = slot
			}
		}
	}
	return true
}

// recordParamHolderOf is the name a record-expanded parameter's leaves
// are spelled under — the parameter's own identifier. A binding-pattern
// parameter expands under the placeholder holder recordParamMembersIn
// uses and owns no name the caller could have flattened, so it answers
// false and takes no threading.
func recordParamHolderOf(ctx *FlowContext, parameter *ast.Node) (string, bool) {
	if _, expanded := recordParamMembersIn(ctx, parameter); !expanded {
		return "", false
	}
	name := parameter.AsParameterDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		return "", false
	}
	return name.Text(), true
}

// recordParamRets threads the WRITE-BACKS for one record-expanded
// parameter: each leaf row the callee's body moved maps its out back onto
// the caller slot holding that same leaf.
//
// The caller's slot is found the way recordArgumentEffects case (b) found
// the value it sent — the argument is a bare identifier naming a
// flattened record local, and the leaf sits at "<argument>.<member>".
// Every other argument shape (an object literal, a call's result, a
// dotted path the caller never flattened) leaves the row's ret at -1: no
// slot of the caller spells that leaf, so no belief of the caller's
// survives the call for the write to falsify.
//
// This never touches args. The record route filled them from the argument
// itself, and its literal case carries values no path could restate.
func recordParamRets(
	context *LoweringContext,
	calleeShape LoweredSummary,
	holder string,
	index int,
	callArguments []*ast.Node,
	args []kernelbridge.LoopEffect,
	rets []int,
) bool {
	if index >= len(callArguments) {
		return true
	}
	head := Unwrapped(callArguments[index])
	if !ast.IsIdentifier(head) {
		return true
	}
	leaves, leavesOk := leafSlotsUnder(context, head.Text())
	if !leavesOk {
		return true
	}
	slotOfPath := map[string]int{}
	for _, leaf := range leaves {
		slotOfPath[leaf.Path] = leaf.Index
	}
	for _, entry := range calleeShape.BundleEntries {
		member, isRow := BundleParamFieldNameOf(entry.Path, holder)
		if !isRow || !entry.Written {
			continue
		}
		if entry.Index < 0 || entry.Index >= len(args) {
			return false
		}
		slot, held := slotOfPath[member]
		if !held {
			continue
		}
		if entry.Index < len(rets) {
			rets[entry.Index] = slot
		}
	}
	return true
}

// recordArgumentEffects maps ONE argument onto an expanded parameter's
// leaf entries, IN THE PARAMETER'S MEMBER ORDER — the order
// SummaryParameterEntries laid the entries out, so effect j fills member
// j's slot whatever order the argument spelled its keys.
//
// Two argument shapes lower, and nothing else:
//
//	(a) an OBJECT LITERAL whose keys are exactly the members — each
//	    member's value lowers as an ordinary effect through the shared
//	    RHS grammar, under the member's own sort;
//	(b) a FLATTENED RECORD LOCAL of exactly those leaves — each member
//	    reads the caller slot spelled "q.<member>" as a var.
//
// Anything else declines the whole call: a call's result, a parameter
// the caller itself holds unexpanded, a literal with an extra or missing
// key, a spread. There is no partial fill — a leaf left at its absent
// entry state would read inside the callee as undefined, which is not
// what the caller passed.
func recordArgumentEffects(
	context *LoweringContext,
	members []recordParamMember,
	argument *ast.Node,
) ([]kernelbridge.LoopEffect, bool) {
	head := Unwrapped(argument)
	// (a) `f({ lo: 1, hi: n })`
	if ast.IsObjectLiteralExpression(head) {
		valueOfKey := map[string]*ast.Node{}
		for _, property := range head.AsObjectLiteralExpression().Properties.Nodes {
			if !ast.IsPropertyAssignment(property) {
				return nil, false
			}
			assignment := property.AsPropertyAssignment()
			if !ast.IsIdentifier(assignment.Name()) || assignment.Initializer == nil {
				return nil, false
			}
			key := assignment.Name().Text()
			if _, already := valueOfKey[key]; already {
				return nil, false
			}
			valueOfKey[key] = assignment.Initializer
		}
		// EXACTLY the members: an extra key is a shape the parameter did
		// not declare, a missing one leaves a leaf unwritten. An object
		// LITERAL's own properties are one level deep by syntax, so a
		// NESTED member (len(Path) > 1) can never be one of this literal's
		// own keys — it takes the same refusal a missing flat key would,
		// spelled here as an explicit depth check rather than a dotted
		// Key failing to match a flat lookup by accident.
		if len(valueOfKey) != len(members) {
			return nil, false
		}
		out := make([]kernelbridge.LoopEffect, 0, len(members))
		for _, member := range members {
			if len(member.Path) != 1 {
				return nil, false
			}
			value, has := valueOfKey[member.Key]
			if !has {
				return nil, false
			}
			effect, ok := RhsEffect(context, member.Sort, value)
			if !ok {
				return nil, false
			}
			out = append(out, asVarStateEffect(effect))
		}
		return out, true
	}
	// (b) `f(q)` where q is a flattened record local of exactly these
	// leaves. leafSlotsUnder is the same reader the record-to-record
	// assignment uses, so "the caller flattened q" and "q's leaves have
	// slots" are one question. leafSlot.Path is ALREADY the leaf's full
	// dotted path below the holder, so the lookup here is keyed the same
	// way — the member's FULL PATH, never the bare Key, since a nested
	// member's own leaf sits under its full path in the flattened local
	// too.
	if ast.IsIdentifier(head) {
		leaves, leavesOk := leafSlotsUnder(context, head.Text())
		if !leavesOk || len(leaves) != len(members) {
			return nil, false
		}
		slotOfPath := map[string]int{}
		for _, leaf := range leaves {
			slotOfPath[leaf.Path] = leaf.Index
		}
		out := make([]kernelbridge.LoopEffect, 0, len(members))
		for _, member := range members {
			slot, has := slotOfPath[strings.Join(member.Path, ".")]
			if !has {
				return nil, false
			}
			out = append(out, varStateEffect(slot))
		}
		return out, true
	}
	return nil, false
}

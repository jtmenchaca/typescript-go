// Accessors as CALLS: `o.x` where x is a getter runs a body, and
// `o.x = e` where x is a setter runs another one.
//
// Wave 4 makes an object with a declared shape a bundle of slots, and
// the field census (ir_field_bundles.go) deliberately leaves accessors
// OUT of that bundle: a get/set accessor with a body runs code, and a
// slot cannot stand for code. So today a `this.x` backed by a getter
// finds no slot, falls to the opaque floor, and the body goes porous —
// which is exactly the shape wave 4 exists to remove, because a nest
// method's hot reads are accessor-backed.
//
// What this file does instead is read the accessor for what it is: a
// zero-argument CALL for the read, a one-argument call for the write.
// The accessor's declaration compiles to its own summary through the
// ordinary registry, and the site splices it with IrStatementCall — the
// same statement `wrapper.get(k)` already lowers to. Nothing new reaches
// the kernel: the call statement and its receiver threading are the
// wave-4 seam already proved through summarize_eq.
//
// The one shape difference from an ordinary call is that a getter read
// has NO CallExpression node to hand HoistCallEffect. `o.x` is an
// expression the effect grammar wants an EFFECT for, while the callee's
// answer arrives as a call STATEMENT writing a slot. So the read
// allocates a temp, appends the call statement to the lowering's hoist
// list, and answers `var temp` — the hoist machinery's own shape,
// reached without a syntax node to hoist from.
//
// Every decline here keeps exactly today's behaviour: the read finds no
// slot and the floor serves it, as it did before this file existed.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── resolving the accessor ──────────────────────────────────────── */

// AccessorDeclarationsOf reads a PROPERTY ACCESS as an accessor pair:
// the get and/or set accessor declarations the accessed NAME resolves
// to, or (nil, nil, false).
//
// The resolution is ContractBySymbol's, one step shorter: that function
// resolves a CALLEE to a held contract, and this one resolves a
// PROPERTY NAME to the accessor declarations its symbol carries — a
// symbol's Declarations list holds the getter and the setter as separate
// nodes, so an accessor pair answers both and a get-only or set-only
// property answers the one it has.
//
// THE GATES, each a twin of one ContractBySymbol/ContractOf applies —
// neither exports a helper for it, so each is replicated here beside the
// reason it exists:
//
//   - a nil context, or one with no program or no checker, resolves
//     nothing (ContractBySymbol's first gate verbatim: a context without
//     a program has no checker to resolve symbols through). The
//     nil-tolerance is load-bearing — a lowering that runs without a
//     checker must decline the accessor, never crash.
//   - a COMPUTED name (`o[k]`, `get [key]()`) spells no property to
//     resolve, so an element access never reaches here and a computed
//     accessor declaration is skipped below. This is the field census's
//     own computed rule (ir_field_bundles.go's ClassFieldsOf: "a member
//     whose name is COMPUTED — nothing spells the slot").
//   - an OPTIONAL step (`o?.x`) may read a property of nothing at all,
//     and no call statement stands for "the call that may not have
//     happened". receiverPathOf declines the same shape for the same
//     reason.
//   - a name whose symbol carries MORE THAN ONE declaration of the same
//     accessor kind — the single-symbol stability gate
//     namedTypeMembersOf states: two declarations mean the reading would
//     have to pick one, and which one it picked would not be a property
//     of the syntax. (A get/set PAIR is two declarations of DIFFERENT
//     kinds and is the shape this answers for; two getters is the
//     unstable one.)
//   - an accessor declared on a class whose method NAME is OVERRIDDEN
//     anywhere in view — ContractOf's virtual-dispatch gate, replicated:
//     the resolved base body must not stand for every instance, so no one
//     declaration stands for the access.
//   - an accessor with NO BODY (a declaration file's `get x(): number;`,
//     an abstract accessor) — there is no code to summarize.
func AccessorDeclarationsOf(ctx *FlowContext, access *ast.Node) (getter *ast.Node, setter *ast.Node, ok bool) {
	// a context without a program has no checker to resolve symbols
	// through — ContractBySymbol's own first gate
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, nil, false
	}
	if access == nil || !ast.IsPropertyAccessExpression(access) {
		return nil, nil, false
	}
	property := access.AsPropertyAccessExpression()
	// an optional step reads a property of a maybe-absent object; no call
	// statement stands for a call that may not have happened
	if property.QuestionDotToken != nil {
		return nil, nil, false
	}
	name := property.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return nil, nil, false
	}
	symbol := symbolAt(ctx.P.Checker, name)
	if symbol == nil {
		return nil, nil, false
	}
	for _, declaration := range symbol.Declarations {
		if declaration == nil {
			continue
		}
		switch {
		case ast.IsGetAccessorDeclaration(declaration):
			if !accessorUsable(declaration) {
				return nil, nil, false
			}
			// two getters for one name: the reading would have to pick, and
			// which it picked would not be a property of the syntax
			if getter != nil {
				return nil, nil, false
			}
			getter = declaration
		case ast.IsSetAccessorDeclaration(declaration):
			if !accessorUsable(declaration) {
				return nil, nil, false
			}
			if setter != nil {
				return nil, nil, false
			}
			setter = declaration
		}
	}
	if getter == nil && setter == nil {
		return nil, nil, false
	}
	if accessorOverridden(ctx, getter) || accessorOverridden(ctx, setter) {
		return nil, nil, false
	}
	return getter, setter, true
}

// accessorUsable is whether ONE accessor declaration is a body this
// lowering could ever call: a plain identifier name (a computed one
// spells nothing) and a body to summarize (a declaration-file or
// abstract accessor has none).
func accessorUsable(declaration *ast.Node) bool {
	name := declaration.Name()
	if name == nil || (!ast.IsIdentifier(name) && !ast.IsPrivateIdentifier(name)) {
		return false
	}
	return declaration.Body() != nil
}

// accessorOverridden is ContractOf's virtual-dispatch gate over an
// accessor: a name overridden anywhere in view dispatches to a body this
// resolution never saw, so the resolved declaration must not stand for
// every instance.
//
// The name set is OverriddenMethodNames' — the same registry reading
// ContractOf takes for methods. An accessor and a method share the
// question exactly: both are class members reached by name through a
// receiver whose runtime class the site does not pin.
func accessorOverridden(ctx *FlowContext, declaration *ast.Node) bool {
	if declaration == nil {
		return false
	}
	name := declaration.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return false
	}
	if ctx == nil {
		return false
	}
	_, overridden := OverriddenMethodNames(&ctx.Contracts)[name.Text()]
	return overridden
}

/* ── the getter read ─────────────────────────────────────────────── */

// GetterReadEffect lowers `o.x` where x is a GETTER with a compiled
// summary: the read is a zero-argument call, hoisted ahead of the
// statement it appears in, and the expression's value is the temp the
// call's return landed in.
//
// The shape, step by step:
//
//  1. resolve the accessor (AccessorDeclarationsOf) and demand its blob
//     from the registry (SummaryBlobFor) — building it on this first ask,
//     bottom-up, exactly as an ordinary callee's is built;
//  2. build the ENTRY VECTOR from the getter's own LoweredSummary: a
//     getter declares no parameters, so every entry it has is a bundle
//     entry, and each "this.<field>" one takes the caller's
//     "<receiverPath>.<field>" slot as a var — the fill rules
//     bundleRetsAndArgs states, applied here because there is no
//     CallExpression to hand that function;
//  3. allocate a temp for the return, point the statement's rets at it,
//     and append the statement to context.Hoisted;
//  4. answer `var temp`.
//
// Declines — each leaving the read exactly where it stood, at the floor:
// no accessor, no getter (a set-only property), no blob (the getter's
// own body declined), no hoist room (context.CanHoist false, or
// Allocate absent or past the budget), a receiver with no spelled path,
// and a lowering with no Flow or no SummaryTable.
//
// GATED ON CanHoist. A read inside a loop head, a branch test, or any
// other position whose statements the lowering has nowhere to put a
// hoisted statement is not a place a call may run: the call would then
// execute on a path the IR does not spell. The flag is the owner's
// answer to "is there a statement position here", and this route asks
// before it builds anything.
func GetterReadEffect(context *LoweringContext, access *ast.Node) (kernelbridge.LoopEffect, bool) {
	if context == nil || !context.CanHoist {
		return kernelbridge.LoopEffect{}, false
	}
	if context.Flow == nil || context.SummaryTable == nil || context.Allocate == nil {
		return kernelbridge.LoopEffect{}, false
	}
	getter, _, resolved := AccessorDeclarationsOf(context.Flow, access)
	if !resolved || getter == nil {
		return kernelbridge.LoopEffect{}, false
	}
	blob, hasBlob := SummaryBlobFor(context.Flow, getter)
	if !hasBlob {
		return kernelbridge.LoopEffect{}, false
	}
	shape, shapeKnown := LowerSummaryBody(context.Flow, getter)
	if !shapeKnown {
		return kernelbridge.LoopEffect{}, false
	}
	receiverPath, pathOk := accessorReceiverPathOf(access)
	if !pathOk {
		return kernelbridge.LoopEffect{}, false
	}
	// the return's landing slot: one fresh temp per read, named after the
	// path it read so a body's IR reads back to the syntax it came from
	temp, allocated := context.Allocate(
		accessorTempName(receiverPath, access),
		context.AccessorSortOf(getter),
		context.AccessorTypeofOf(getter),
	)
	if !allocated {
		return kernelbridge.LoopEffect{}, false
	}
	statement, built := accessorCallStatement(context, shape, blob, getter, receiverPath, nil, temp)
	if !built {
		return kernelbridge.LoopEffect{}, false
	}
	context.Hoisted = append(context.Hoisted, statement)
	return varEffect(temp), true
}

/* ── the setter write ────────────────────────────────────────────── */

// SetterWriteStatements lowers `o.x = e` where x is a SETTER with a
// compiled summary: the statements that call the setter's body with the
// written value as its one parameter entry.
//
// The differences from the getter read, and they are only these:
//
//   - the setter's ONE declared parameter takes the value effect the
//     caller already lowered, at entry 0 — the parameter is entry 0 by
//     the layout's own ordering (declared parameters first, bundle
//     entries after), so no search is needed;
//   - there is NO ret: a setter's value is discarded by the language, so
//     nothing lands anywhere and no temp is allocated;
//   - the WRITTEN bundle entries ride back into the caller's own slots,
//     which is the whole point — `this.x = v` through a setter that
//     assigns `this._x` must move the caller's "this._x" slot, or the
//     caller's stale knowledge of _x survives a write that changed it.
//
// The write-back rule is bundleRetsAndArgs' verbatim: a written entry's
// own index in the out vector maps to the caller's slot of the same
// spelling, and where the caller holds no slot the ret stays -1 and the
// write lands nowhere (nothing lowered can read that spelling, so no
// knowledge survives the call that the write would falsify).
//
// Declines: no accessor, no setter (a get-only property — the write is
// a runtime error or a silent no-op, neither of which this claims), no
// blob, a setter whose declared parameter count is not exactly one, a
// receiver with no spelled path, and a lowering with no Flow or table.
// CanHoist is asked as the read asks it: the statements have to go
// somewhere, and the caller splices them into a statement position.
func SetterWriteStatements(
	context *LoweringContext,
	access *ast.Node,
	value kernelbridge.LoopEffect,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || !context.CanHoist {
		return nil, false
	}
	if context.Flow == nil || context.SummaryTable == nil {
		return nil, false
	}
	_, setter, resolved := AccessorDeclarationsOf(context.Flow, access)
	if !resolved || setter == nil {
		return nil, false
	}
	// a setter takes exactly one value; anything else is not the form the
	// entry vector was laid out for
	if len(setter.Parameters()) != 1 {
		return nil, false
	}
	blob, hasBlob := SummaryBlobFor(context.Flow, setter)
	if !hasBlob {
		return nil, false
	}
	shape, shapeKnown := LowerSummaryBody(context.Flow, setter)
	if !shapeKnown {
		return nil, false
	}
	if shape.ParamCount < 1 {
		return nil, false
	}
	receiverPath, pathOk := accessorReceiverPathOf(access)
	if !pathOk {
		return nil, false
	}
	statement, built := accessorCallStatement(context, shape, blob, setter, receiverPath, &value, -1)
	if !built {
		return nil, false
	}
	return []kernelbridge.IrStatement{statement}, true
}

/* ── the shared statement builder ────────────────────────────────── */

// accessorCallStatement builds the IrStatementCall an accessor call
// lowers to, for both directions.
//
// It is summaryCallStatement's own construction with the argument walk
// replaced: an accessor has no argument LIST to lower (a getter has
// none at all; a setter's one value the caller already lowered), so the
// entry vector is built directly from the callee's shape.
//
// THE FILL RULES, taken from bundleRetsAndArgs and applied entry by
// entry:
//
//   - a "this.<field>" bundle entry takes a VAR of the caller's
//     "<receiverPath>.<field>" slot;
//   - a "this.<field>" entry the caller has NO slot for takes the
//     UNKNOWN effect — never the absent constant, which would claim the
//     field IS undefined, a claim about a value the caller never had;
//   - a WRITTEN "this.<field>" entry maps its own index back to the
//     caller's slot for the same spelling; where the caller has no slot
//     the ret stays -1 and the write lands nowhere;
//   - every entry the bundle rows do not name — the setter's parameter,
//     the callee's locals, its grown slots — takes the absent constant,
//     which is what the apply side's own padding sends, EXCEPT the done
//     flag, which enters {0} exactly as summaryCallStatement's padding
//     spells it.
//
// A NON-"this." bundle row (a record-parameter leaf) declines the whole
// statement: an accessor's parameter is one value, not a record, so such
// a row means the callee's layout is not the shape this site can fill.
//
// `value` non-nil fills entry 0 — the setter's declared parameter.
// `target` >= 0 points the return out-state at a caller slot; -1 drops
// it, which is the setter's case.
func accessorCallStatement(
	context *LoweringContext,
	shape LoweredSummary,
	blob kernelbridge.SummaryBlob,
	declaration *ast.Node,
	receiverPath string,
	value *kernelbridge.LoopEffect,
	target int,
) (kernelbridge.IrStatement, bool) {
	if shape.SlotCount <= 0 {
		return kernelbridge.IrStatement{}, false
	}
	// the entry vector: absent everywhere the rules below do not speak,
	// with the done flag's {0} exactly where the padding puts it
	args := make([]kernelbridge.LoopEffect, shape.SlotCount)
	for index := range args {
		if index == shape.DoneIndex {
			args[index] = doneDownEffect()
			continue
		}
		args[index] = kernelbridge.AbsentConst()
	}
	// rets: -1 says nothing reads that out-state
	rets := make([]int, shape.RetIndex+1)
	for index := range rets {
		rets[index] = -1
	}
	if target >= 0 {
		if shape.RetIndex >= len(rets) {
			return kernelbridge.IrStatement{}, false
		}
		rets[shape.RetIndex] = target
	}
	// the setter's one declared parameter is entry 0 — the layout puts
	// declared parameters first and bundle entries after
	if value != nil {
		args[0] = *value
	}
	for _, entry := range shape.BundleEntries {
		if entry.Index < 0 || entry.Index >= len(args) {
			return kernelbridge.IrStatement{}, false
		}
		field, isThis := thisFieldNameOf(entry.Path)
		if !isThis {
			// a record-parameter leaf: an accessor's parameter is one value,
			// so a layout carrying such a row is not this site's shape
			return kernelbridge.IrStatement{}, false
		}
		slot, held := slotIndexOfName(context, receiverPath+"."+field)
		if !held {
			// the caller knows nothing about this field, and nothing is what
			// the entry must say
			args[entry.Index] = unknownEffect
			continue
		}
		args[entry.Index] = varEffect(slot)
		if entry.Written && entry.Index < len(rets) {
			rets[entry.Index] = slot
		}
	}
	return kernelbridge.IrStatement{
		Kind:   kernelbridge.IrStatementCall,
		Callee: context.SummaryTable.CalleeIndex(declaration, blob),
		Args:   args,
		Rets:   rets,
	}, true
}

// doneDownEffect is the done flag's entry effect — exactly {0}, "not yet
// returned". summaryCallStatement's padding spells the same constant for
// the same slot; the two must agree or a spliced compile would start
// with the flag already up.
func doneDownEffect() kernelbridge.LoopEffect {
	return constNumber(0)
}

/* ── the receiver, and the temp's name ───────────────────────────── */

// accessorReceiverPathOf spells the object an accessor was reached
// THROUGH, as the dotted path the caller's slots are named under:
//
//	this.x          → "this"
//	wrapper.x       → "wrapper"
//	this.holder.x   → "this.holder"
//
// It is receiverPathOf's rule on a property access rather than a call:
// the accessed expression MINUS its last step, which is the accessor
// name and never a slot. The declines are the same three, each because
// no slot spelling exists for what was written — a computed step, an
// optional step, and a root that is neither `this` nor an identifier.
func accessorReceiverPathOf(access *ast.Node) (string, bool) {
	if access == nil || !ast.IsPropertyAccessExpression(access) {
		return "", false
	}
	property := access.AsPropertyAccessExpression()
	// AN OPEN GAP, and it is left open deliberately. `o?.x` where x is an
	// accessor is a determinable shape — when the receiver is present the
	// accessor runs, and a conditional call would spell it — but the
	// absence half is the maybe-receiver machinery's, which lives outside
	// this file. Nothing here may claim the call happened, so the path
	// spelling declines and the read keeps the floor. The resolution's own
	// optional gate (AccessorDeclarationsOf) refuses the same shape first;
	// this is the second wall behind it.
	if property.QuestionDotToken != nil {
		return "", false
	}
	return dottedPathOf(Unwrapped(property.Expression))
}

// accessorTempName spells the slot a getter read lands in: the path the
// read named, under a marker no source name can collide with. `#` is the
// same character the base layout's own "#done"/"#ret" wear, and no
// JavaScript identifier or dotted path spells one.
//
// Two reads of the SAME path in one body get two temps, each with this
// same name. That is deliberate and it is sound: a getter runs code, so
// two reads may answer differently, and one shared slot would claim the
// second read gave the first read's value. slotIndexOfName answers the
// FIRST binding of a spelling, so the name is never resolved back to —
// only the index the allocation handed out is used.
func accessorTempName(receiverPath string, access *ast.Node) string {
	name := access.AsPropertyAccessExpression().Name().Text()
	var builder strings.Builder
	builder.WriteString("#get.")
	builder.WriteString(receiverPath)
	builder.WriteString(".")
	builder.WriteString(name)
	return builder.String()
}

/* ── the temp's sort ─────────────────────────────────────────────── */

// AccessorSortOf and AccessorTypeofOf read what a getter's RETURN wears,
// from the declaration's own type annotation — never from any call, the
// same law declaredParamSort states for parameters: a summary quantifies
// over all entries, so only what the annotation itself promises may be
// assumed of the value that comes back.
//
// They are methods on the context rather than free functions because the
// lowering asks them where it asks everything else about a slot, and a
// context that has no reading of its own answers the same unknown a
// missing annotation does.
//
// An unannotated getter's temp is unknown-sorted, which admits only the
// definedness test — the read still lowers, and its value simply says
// nothing more than "some value came back". That is strictly better than
// the floor, which havocs every slot the statement could have touched.
func (context *LoweringContext) AccessorSortOf(getter *ast.Node) BindingKind {
	sort, _ := accessorReturnEvidence(getter)
	return sort
}

// AccessorTypeofOf is the typeof half of the same reading.
func (context *LoweringContext) AccessorTypeofOf(getter *ast.Node) TypeofTag {
	_, tag := accessorReturnEvidence(getter)
	return tag
}

// accessorReturnEvidence reads a getter's declared return annotation as
// a sort and typeof pair, through annotationSort — the ONE annotation
// reading the field census takes, so a getter returning `number` and a
// field declared `number` sort identically.
func accessorReturnEvidence(getter *ast.Node) (BindingKind, TypeofTag) {
	if getter == nil || !ast.IsGetAccessorDeclaration(getter) {
		return BindingKindUnknown, TypeofTagNone
	}
	return annotationSort(getter.AsGetAccessorDeclaration().Type)
}

// SetterWriteOf is the statement door for an accessor-backed write:
// `this.value = e` / `holder.value = e` where the property's symbol
// declares a SET accessor. The right side lowers under the setter
// parameter's own declared sort, and the write is the setter's call
// statement (SetterWriteStatements) — the value at entry 0, the
// written this-fields riding back through rets.
//
// The door reads THREE assigning shapes, each the same call statement
// with a different value at entry 0:
//
//   - `o.x = e` — the plain write, the value being e itself;
//   - `o.x += e` and its arithmetic siblings — the read-modify-write
//     the runtime performs: the GETTER's call, the arithmetic, the
//     SETTER's call (setterCompoundWriteOf);
//   - `o.x++` / `--o.x` — the same, with the constant 1 for e
//     (setterUpdateWriteOf).
func SetterWriteOf(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	// `o.x++` / `--o.x`: no binary node at all, so the update route reads
	// it ahead of the binary shapes below
	if written, ok := setterUpdateWriteOf(context, e); ok {
		return written, true
	}
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		// `o.x += e` and its siblings: the compound reads through the
		// getter before it writes through the setter
		return setterCompoundWriteOf(context, bin)
	}
	access := Unwrapped(bin.Left)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	if context.Flow == nil {
		return nil, false
	}
	value, valueOk := setterValueEffect(context, access, bin.Right)
	if !valueOk {
		return nil, false
	}
	return SetterWriteStatements(context, access, value)
}

// setterValueEffect lowers a written RIGHT SIDE under the SETTER
// parameter's own declared sort — the entry it fills is that
// parameter's, so the sort the value is read at is the parameter's and
// never the site's.
//
// Declines where the access resolves to no setter (a get-only property
// or a plain field, neither of which this file's routes claim) and
// where the right side itself fails to lower.
func setterValueEffect(
	context *LoweringContext,
	access *ast.Node,
	right *ast.Node,
) (kernelbridge.LoopEffect, bool) {
	if context == nil || context.Flow == nil {
		return kernelbridge.LoopEffect{}, false
	}
	_, setter, resolved := AccessorDeclarationsOf(context.Flow, access)
	if !resolved || setter == nil {
		return kernelbridge.LoopEffect{}, false
	}
	sort := BindingKindUnknown
	if parameters := setter.Parameters(); len(parameters) == 1 {
		sort = declaredParamSort(parameters[0])
	}
	return RhsEffect(context, sort, right)
}

/* ── the compound: a read, the arithmetic, a write ───────────────── */

// setterCompoundOps is the compound assignment's operator, as the
// effect grammar's own arithmetic. It is compoundOps' table
// (ir_assignment.go) read for the accessor route: the same four
// operators, because the same effect grammar carries them, and a
// compound through a setter must compute what a compound through a slot
// computes or the two spell different arithmetic for the same source.
var setterCompoundOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusEqualsToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusEqualsToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskEqualsToken: kernelbridge.LoopOpMul,
	ast.KindSlashEqualsToken:    kernelbridge.LoopOpDiv,
}

// setterCompoundWriteOf lowers `o.x += e` where x resolves to a get/set
// PAIR: the read-modify-write the runtime itself performs, spelled as
// the two calls it really is —
//
//	#get.o.x := call getter(…)        (hoisted, GetterReadEffect's own shape)
//	          call setter(#get.o.x + e)
//
// The getter's call statement rides out through context.Hoisted, which
// the statement dispatch flushes AHEAD of whatever this route returns
// (TakeHoisted) — so the read runs before the write, which is the order
// the language runs them in.
//
// THE PAIR IS REQUIRED, both halves. A compound through a get-only
// property writes nothing the language defines, and a compound through
// a set-only property reads `undefined` from a property with no getter
// — the arithmetic is then NaN, a value this route does not claim. So
// the route wants a getter AND a setter, and declines otherwise.
//
// The `||=`/`&&=`/`??=` family is NOT read here. Those short-circuit:
// the setter may not run at all, and no call statement stands for a
// call that may not have happened — the same reason the optional step
// declines at the resolution. The refusal is named so the report points
// at the syntax.
//
// Every other decline is the two halves' own: GetterReadEffect's
// (CanHoist, the allocator, the getter's blob) and
// SetterWriteStatements' (the setter's blob, the receiver path, the
// statement builder). Neither is second-guessed here — where either
// half declines, so does the compound, and the statement falls to the
// floor exactly as it did before this route existed.
func setterCompoundWriteOf(
	context *LoweringContext,
	bin *ast.BinaryExpression,
) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil {
		return nil, false
	}
	access := Unwrapped(bin.Left)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	switch bin.OperatorToken.Kind {
	case ast.KindBarBarEqualsToken, ast.KindAmpersandAmpersandEqualsToken,
		ast.KindQuestionQuestionEqualsToken:
		// a short-circuiting compound: the setter runs on SOME runs and not
		// others, and one call statement claims it ran on every one
		if _, setter, resolved := AccessorDeclarationsOf(context.Flow, access); resolved && setter != nil {
			NoteDeclinedConstruct(context, "a short-circuiting compound assignment through a setter")
		}
		return nil, false
	}
	op, isArithmetic := setterCompoundOps[bin.OperatorToken.Kind]
	if !isArithmetic {
		return nil, false
	}
	right, rightOk := EffectOf(context, bin.Right)
	if !rightOk {
		return nil, false
	}
	return setterReadModifyWrite(context, access, op, right)
}

/* ── the update: the compound with a constant operand ────────────── */

// setterUpdateWriteOf lowers `o.x++` and `--o.x` where x resolves to a
// get/set PAIR: the compound case with the constant 1 for its operand.
//
// PREFIX AND POSTFIX LOWER THE SAME. The two differ only in the VALUE
// the expression itself answers — the stepped value for a prefix, the
// value before the step for a postfix — and in a statement position
// nothing reads that value. The EFFECT is identical: the getter runs,
// one is added or subtracted, the setter runs. An update read for its
// value belongs to the expression route, which does not claim it
// (setterAssignmentEffect below reads only the assigning form, whose
// value is the right side by the language's own rule).
func setterUpdateWriteOf(context *LoweringContext, e *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if context == nil || context.Flow == nil {
		return nil, false
	}
	var operator ast.Kind
	var operand *ast.Node
	switch {
	case ast.IsPostfixUnaryExpression(e):
		unary := e.AsPostfixUnaryExpression()
		operator, operand = unary.Operator, unary.Operand
	case ast.IsPrefixUnaryExpression(e):
		unary := e.AsPrefixUnaryExpression()
		operator, operand = unary.Operator, unary.Operand
	default:
		return nil, false
	}
	if operator != ast.KindPlusPlusToken && operator != ast.KindMinusMinusToken {
		return nil, false
	}
	access := Unwrapped(operand)
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	op := kernelbridge.LoopOpAdd
	if operator == ast.KindMinusMinusToken {
		op = kernelbridge.LoopOpSub
	}
	return setterReadModifyWrite(context, access, op, constNumber(1))
}

// setterReadModifyWrite is the one body the compound and the update
// share: the getter's read, the arithmetic against a lowered operand,
// and the setter's call statement.
//
// The GETTER IS ASKED FIRST, and the order matters twice. It matters
// for the run — the language reads before it writes — and it matters
// for the lowering, because GetterReadEffect appends its call statement
// to context.Hoisted, and a decline AFTER that append would leave a
// hoist behind for a statement that lowered no other way. The statement
// dispatch's own DropHoistedFrom truncates back to the statement's mark
// on every decline, so a half-read compound leaves nothing — the same
// contract every hoisting reader in the dispatch runs under.
func setterReadModifyWrite(
	context *LoweringContext,
	access *ast.Node,
	op kernelbridge.LoopEffectOp,
	operand kernelbridge.LoopEffect,
) ([]kernelbridge.IrStatement, bool) {
	getter, setter, resolved := AccessorDeclarationsOf(context.Flow, access)
	// both halves, or nothing: a compound through a get-only property
	// writes what the language does not define, and one through a
	// set-only property reads a property that has no getter
	if !resolved || getter == nil || setter == nil {
		return nil, false
	}
	held, readOk := GetterReadEffect(context, access)
	if !readOk {
		return nil, false
	}
	stepped := kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectBinary,
		Op:   op,
		A:    &held,
		B:    &operand,
	}
	return SetterWriteStatements(context, access, stepped)
}

/* ── the setter in expression position ───────────────────────────── */

// SetterAssignmentEffect lowers `o.x = e` used as a VALUE — `f(o.x =
// e)`, `y = (o.x = e)`, an arm of a ternary — rather than as a bare
// statement.
//
// The language's own rule makes this the getter read's shape exactly.
// An assignment expression's value is its RIGHT SIDE, not whatever the
// setter did with it: `f(o.x = 3)` passes 3 to f however the setter
// stored it, and reads the property back not at all. So the whole
// lowering is
//
//	         call setter(e)      (hoisted)
//	the expression's value := e
//
// — the setter's call statement pushed into context.Hoisted, which the
// statement route flushes ahead of its own statements, and the answered
// effect being the value the caller already lowered.
//
// A SETTER IN EXPRESSION POSITION WITH NO STATEMENT POSITION is the one
// shape this cannot carry, and it is named rather than dropped. Where
// CanHoist is false — a loop head, a branch test, any reading with no
// statement stream (ir_lowering_context.go's own list) — there is
// nowhere to put the call, and a call that runs on a path the IR does
// not spell is a wrong answer, not a weak one. That decline calls
// NoteDeclinedConstruct so the outcome report names the syntax someone
// can act on.
//
// The COMPOUND and UPDATE forms in expression position are not read
// here. Their value is the stepped value (or, for a postfix, the value
// before the step), which is the GETTER's temp and not the right side —
// a different reading, and one that also has to answer which of the two
// the position wanted. This route claims the plain assignment alone,
// whose value the language pins without any reading of the accessor at
// all.
func SetterAssignmentEffect(
	context *LoweringContext,
	e *ast.Node,
) (kernelbridge.LoopEffect, bool) {
	if context == nil || context.Flow == nil {
		return kernelbridge.LoopEffect{}, false
	}
	head := Unwrapped(e)
	if !ast.IsBinaryExpression(head) {
		return kernelbridge.LoopEffect{}, false
	}
	bin := head.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return kernelbridge.LoopEffect{}, false
	}
	access := Unwrapped(bin.Left)
	if !ast.IsPropertyAccessExpression(access) {
		return kernelbridge.LoopEffect{}, false
	}
	_, setter, resolved := AccessorDeclarationsOf(context.Flow, access)
	if !resolved || setter == nil {
		return kernelbridge.LoopEffect{}, false
	}
	if !context.CanHoist {
		NoteDeclinedConstruct(context, "a setter in expression position")
		return kernelbridge.LoopEffect{}, false
	}
	value, valueOk := setterValueEffect(context, access, bin.Right)
	if !valueOk {
		return kernelbridge.LoopEffect{}, false
	}
	written, writeOk := SetterWriteStatements(context, access, value)
	if !writeOk {
		NoteDeclinedConstruct(context, "a setter in expression position")
		return kernelbridge.LoopEffect{}, false
	}
	context.Hoisted = append(context.Hoisted, written...)
	// the assignment's value is the RIGHT SIDE by the language's rule —
	// what the setter stored is the setter's business, and nothing here
	// reads the property back
	return value, true
}

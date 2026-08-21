// split from ir_accessor_calls.go — the compound, the update, and the read-modify-write they share

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

/* ── the compound: a read, the arithmetic, a write ───────────────── */

// setterCompoundOps is the compound assignment's operator, as the
// effect grammar's own arithmetic. It is compoundOps' table
// (ir_assignment.go) read for the accessor route: the same operators,
// because the same effect grammar carries them, and a compound through
// a setter must compute what a compound through a slot computes or the
// two spell different arithmetic for the same source.
var setterCompoundOps = map[ast.Kind]kernelbridge.LoopEffectOp{
	ast.KindPlusEqualsToken:     kernelbridge.LoopOpAdd,
	ast.KindMinusEqualsToken:    kernelbridge.LoopOpSub,
	ast.KindAsteriskEqualsToken: kernelbridge.LoopOpMul,
	ast.KindSlashEqualsToken:    kernelbridge.LoopOpDiv,
	// the bitwise and shift compounds, matching compoundOps
	ast.KindAmpersandEqualsToken:                         kernelbridge.LoopOpBitAnd,
	ast.KindBarEqualsToken:                               kernelbridge.LoopOpBitOr,
	ast.KindCaretEqualsToken:                             kernelbridge.LoopOpBitXor,
	ast.KindLessThanLessThanEqualsToken:                  kernelbridge.LoopOpShl,
	ast.KindGreaterThanGreaterThanEqualsToken:            kernelbridge.LoopOpSar,
	ast.KindGreaterThanGreaterThanGreaterThanEqualsToken: kernelbridge.LoopOpShr,
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

/* ── the WALK-route read-modify-write (no IR, no kernel summary) ─── */
//
// `box.age += 5` on the plain walk (a FlowContext/Env body that never
// lowers to a kernel summary — a bare function, not a loop counting
// toward one) reaches ReadAssignment's property-compound arm
// (assignment_operators.go), which reads `box.age` and writes it back
// through WriteProperty. Before this function existed, that read found
// no key (ConstructedInstance never census-keys an accessor — "methods
// and getters are keys too" says the incompleteness, not a slot) and
// the write then ADDED "age" as a fresh plain-object key holding
// unknown — the getter/setter never ran, so a backing field the setter
// writes (`this.held = value`) never moved. The functions below run
// the two bodies for real, the same way thisParameterCallBind
// (this_parameter_call.go) already runs an arbitrary function body with
// `this` bound to a synthetic receiver: a fresh call environment,
// `this` seeded with the receiver's own held object, ReturnSink for the
// getter's value, ThisWriteSink for the setter's writes, folded back
// into the receiver's object value with setObjectKey
// (index_operators.go) — the exact key-rebuild WriteProperty itself
// already does for a `this.key = v` write, applied here to the OUTER
// receiver instead of `this`.
//
// THE PAIR IS REQUIRED, both halves — same reason
// setterReadModifyWrite above declines a get-only or set-only
// property: a read-modify-write through a getter with no setter writes
// nothing the language defines, and one through a setter with no
// getter reads `undefined` from a property with no getter, so the
// arithmetic is NaN — not a value this route claims.

// accessorWalkTarget is the resolved receiver + accessor pair a compound
// read-modify-write needs, shared between the pure pre-check
// (AccessorTargetOf) and the routes that actually run the two bodies —
// resolved once so a caller that checks before evaluating its right
// side never re-derives (or disagrees with) what it already found.
type accessorWalkTarget struct {
	name     string
	receiver abstractdomain.AbstractValue
	// accessorName is the PROPERTY's own spelling ("age") — the key
	// ConstructedInstance seeds once at construction time with the
	// getter's own one-time return (constructed_instance.go's "a GET
	// ACCESSOR is a key too" pass). Every write route folds the
	// SETTER body's OWN written keys back into the receiver
	// (runAccessorSetter) — never this one, since the setter's body
	// spells its own backing field, not the accessor's public name — so
	// runAccessorSetter reads this field to keep that snapshot from
	// going stale after a write moves the real backing field under it.
	accessorName   string
	getterBody     *ast.Node
	setterBody     *ast.Node
	setterParam    *ast.Node
	accessorSymbol *ast.Symbol
}

// resolveAccessorWalkTarget is the one resolution both
// AccessorTargetOf and the read-modify-write routes run: a tracked
// receiver name holding an OBJECT, whose accessed property resolves
// (AccessorDeclarationsOf) to a getter/setter PAIR, each with a
// block body, the setter's one parameter a plain identifier. Any
// missing piece declines — the caller falls through to the plain-
// property arm exactly as it did before this route existed.
func resolveAccessorWalkTarget(ctx *FlowContext, env Env, access *ast.Node) (accessorWalkTarget, bool) {
	pae := access.AsPropertyAccessExpression()
	name, rooted := rootOfReceiver(pae.Expression)
	if !rooted {
		return accessorWalkTarget{}, false
	}
	receiver, hasReceiver := env.Get(name)
	if !hasReceiver || receiver.Kind != abstractdomain.KindObject {
		return accessorWalkTarget{}, false
	}
	getter, setter, resolved := AccessorDeclarationsOf(ctx, access)
	if !resolved || getter == nil || setter == nil {
		return accessorWalkTarget{}, false
	}
	getterBody, setterBody := getter.Body(), setter.Body()
	if getterBody == nil || setterBody == nil || !ast.IsBlock(getterBody) || !ast.IsBlock(setterBody) {
		return accessorWalkTarget{}, false
	}
	setterParams := setter.Parameters()
	if len(setterParams) != 1 {
		return accessorWalkTarget{}, false
	}
	setterParamName := setterParams[0].AsParameterDeclaration().Name()
	if !ast.IsIdentifier(setterParamName) {
		return accessorWalkTarget{}, false
	}
	return accessorWalkTarget{
		name:           name,
		receiver:       receiver,
		accessorName:   pae.Name().Text(),
		getterBody:     getterBody,
		setterBody:     setterBody,
		setterParam:    setterParams[0],
		accessorSymbol: symbolAt(ctx.P.Checker, pae.Name()),
	}, true
}

// AccessorTargetOf is whether a property-access compound's LEFT side
// resolves to a get/set accessor pair on a tracked receiver — a pure
// pre-check with no evaluation and no effect, so a caller may run it
// BEFORE evaluating the compound's right side without risking a
// double-evaluated effect on decline (assignment_operators.go's own
// reason for checking this ahead of the right side).
func AccessorTargetOf(ctx *FlowContext, env Env, access *ast.Node) bool {
	if !ast.IsPropertyAccessExpression(access) {
		return false
	}
	_, ok := resolveAccessorWalkTarget(ctx, env, access)
	return ok
}

// accessorInlining is the recursion-guard map every accessor run shares:
// a getter or setter that calls back into the same accessor (through
// this receiver or another) must re-enter with the same symbol already
// marked — thisParameterCallBindKnown's own discipline
// (this_parameter_call.go), applied to the accessor's own name symbol
// rather than a called function's. (nil, false) where the symbol is
// already inlining — the caller answers silence itself in that case, so
// this only prepares the map and reports whether the accessor is fresh.
func accessorInlining(ctx *FlowContext, target accessorWalkTarget) (map[*ast.Symbol]struct{}, bool) {
	inlining := ctx.Inlining
	if inlining == nil {
		inlining = map[*ast.Symbol]struct{}{}
	}
	if target.accessorSymbol != nil {
		if _, already := inlining[target.accessorSymbol]; already {
			return inlining, false
		}
	}
	return inlining, true
}

// runAccessorGetter is the getter's read, shared by every route that
// needs the accessor's CURRENT value before deciding what to do with
// it — the ordinary compound's read-modify-write and the short-circuit
// logical assignment's own decision both start here. A fresh call
// environment, `this` bound to the receiver's own held object, the
// return collected through ReturnSink exactly as InlineStoredClosure
// collects a block body's value.
func runAccessorGetter(ctx *FlowContext, target accessorWalkTarget, inlining map[*ast.Symbol]struct{}) abstractdomain.AbstractValue {
	readEnv := NewEnv()
	readEnv.Set("this", target.receiver)
	var readSink []abstractdomain.AbstractValue
	silentRead := *ctx
	silentRead.Report = func(assignability.RefinementDiagnostic) {}
	silentRead.ReturnSink = &readSink
	silentRead.Inlining = inlining
	silentRead.ThisWriteSink = nil
	AnalyzeStatements(&silentRead, readEnv, target.getterBody.AsBlock().Statements.Nodes, nil)
	held := silence.Residue()
	if len(readSink) > 0 {
		held = readSink[0]
		for _, v := range readSink[1:] {
			held = abstractdomain.JoinKnown(held, v)
		}
	}
	return held
}

// runAccessorSetter is the setter's write: `this` bound the same way the
// getter read it, the setter's one parameter bound to the value being
// stored, ThisWriteSink capturing every `this.key = value` the body
// performs. Answers the receiver REBUILT with those writes folded in —
// setObjectKey's own rule (index_operators.go), the same rebuild
// WriteProperty runs for a `this.key = v` write, applied to the OUTER
// receiver rather than `this`.
//
// THE ACCESSOR'S OWN SNAPSHOT KEY. ConstructedInstance seeds a key named
// after the accessor ITSELF ("age") once, at construction, holding the
// getter's one-time return over the fields built so far
// (constructed_instance.go's "a GET ACCESSOR is a key too" pass) — a
// plain PROPERTY READ of `box.age` afterward (ReadObjectKeyAccess) finds
// that key directly and never re-runs the getter. The setter's own body
// writes its BACKING field ("#age"), never the accessor's public
// spelling, so the loop above never touches that snapshot — left alone,
// every write through this route would silently leave "age" pointing at
// its stale construction-time value while "#age" moved. Closed here,
// the ONE place every accessor write route (plain, compound, and the
// short-circuit logical family) shares: where a getter body is in hand
// (the read-modify-write routes, which resolve a getter/setter PAIR by
// construction), it is RE-RUN once more against the just-written
// receiver for an exact refreshed answer — sound and no less precise
// than the read the caller would get by asking again. Where none is in
// hand (AccessorWalkPlainWrite's setter-only route), the stale key is
// REMOVED rather than left wrong: a later plain read then falls through
// to undetermined instead of answering a value the write already made
// false.
func runAccessorSetter(ctx *FlowContext, target accessorWalkTarget, inlining map[*ast.Symbol]struct{}, value abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	setterParamName := target.setterParam.AsParameterDeclaration().Name()
	writeEnv := NewEnv()
	writeEnv.Set("this", target.receiver)
	writeEnv.Set(setterParamName.Text(), value)
	sink := map[string][]abstractdomain.AbstractValue{}
	silentWrite := *ctx
	silentWrite.Report = func(assignability.RefinementDiagnostic) {}
	silentWrite.ReturnSink = nil
	silentWrite.Inlining = inlining
	silentWrite.ThisWriteSink = sink
	AnalyzeStatements(&silentWrite, writeEnv, target.setterBody.AsBlock().Statements.Nodes, nil)

	nextReceiver := target.receiver
	for key, writes := range sink {
		joined := writes[0]
		for _, w := range writes[1:] {
			joined = abstractdomain.JoinKnown(joined, w)
		}
		nextReceiver = abstractdomain.KnownObject(setObjectKey(nextReceiver.Keys, key, joined), nil, false, abstractdomain.TrustProved, false)
	}
	return refreshedAccessorSnapshot(ctx, target, inlining, nextReceiver)
}

// refreshedAccessorSnapshot answers nextReceiver with its accessor-name
// key ("age") brought current: re-run fresh through the getter where one
// is known, dropped otherwise. A receiver carrying no such key (the
// accessor's own name was never snapshotted, or nothing named
// accessorName is tracked) passes through unchanged — nothing to refresh
// or drop.
func refreshedAccessorSnapshot(
	ctx *FlowContext, target accessorWalkTarget, inlining map[*ast.Symbol]struct{}, nextReceiver abstractdomain.AbstractValue,
) abstractdomain.AbstractValue {
	if target.accessorName == "" || nextReceiver.Kind != abstractdomain.KindObject {
		return nextReceiver
	}
	hasSnapshot := false
	for _, key := range nextReceiver.Keys {
		if key.Name == target.accessorName {
			hasSnapshot = true
			break
		}
	}
	if !hasSnapshot {
		return nextReceiver
	}
	if target.getterBody != nil {
		refreshed := runAccessorGetter(ctx, accessorWalkTarget{receiver: nextReceiver, getterBody: target.getterBody}, inlining)
		return abstractdomain.KnownObject(setObjectKey(nextReceiver.Keys, target.accessorName, refreshed), nil, false, abstractdomain.TrustProved, false)
	}
	dropped := make([]abstractdomain.ObjectKey, 0, len(nextReceiver.Keys)-1)
	for _, key := range nextReceiver.Keys {
		if key.Name != target.accessorName {
			dropped = append(dropped, key)
		}
	}
	return abstractdomain.KnownObject(dropped, nil, false, abstractdomain.TrustProved, false)
}

// accessorReadModifyWriteCore is the getter-read / setter-write body
// AccessorWalkReadModifyWrite runs: the getter's value goes in as
// `held`, compoundResult (assignment_operators.go) picks the
// arithmetic/string/bitwise/pow transfer the token names, and the
// setter runs with the result — UNCONDITIONALLY, which is right for
// every ordinary compound token (the runtime always calls the setter
// for `+=`/`&=`/etc). The `||=`/`&&=`/`??=` family does NOT share this
// body — AccessorLogicalReadModifyWrite below is their own route, since
// their setter call is conditional on the getter's own verdict.
func accessorReadModifyWriteCore(
	ctx *FlowContext, env Env, target accessorWalkTarget, leftNode, rightNode *ast.Node, kind ast.Kind, operand abstractdomain.AbstractValue,
) abstractdomain.AbstractValue {
	inlining, fresh := accessorInlining(ctx, target)
	if !fresh {
		return silence.Residue()
	}
	if target.accessorSymbol != nil {
		inlining[target.accessorSymbol] = struct{}{}
		defer delete(inlining, target.accessorSymbol)
	}
	held := runAccessorGetter(ctx, target, inlining)
	next := compoundResult(ctx, leftNode, rightNode, kind, held, operand)
	nextReceiver := runAccessorSetter(ctx, target, inlining, next)
	UpdateTrackedEnv(ctx.Aliases, env, target.name, nextReceiver)
	return next
}

// AccessorWalkReadModifyWrite runs a `box.age OP= right`-shaped
// compound — every compound token compoundResult reads (the five
// NumericOperator forms, the six bitwise/shift forms, `**=`, and `+=`'s
// string-concat arm) — through the getter/setter pair AccessorTargetOf
// already found. Declines only where resolveAccessorWalkTarget itself
// does — the caller (ReadAssignment) checks AccessorTargetOf before
// evaluating `right`, so this function's own decline path runs no
// effect twice.
func AccessorWalkReadModifyWrite(
	ctx *FlowContext, env Env, access, rightNode *ast.Node, kind ast.Kind, right abstractdomain.AbstractValue,
) (abstractdomain.AbstractValue, bool) {
	target, ok := resolveAccessorWalkTarget(ctx, env, access)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	next := accessorReadModifyWriteCore(ctx, env, target, access, rightNode, kind, right)
	return next, true
}

// AccessorLogicalReadModifyWrite runs a `box.age ||= right` /
// `box.age &&= right` / `box.age ??= right` compound through a get/set
// accessor pair — the SHORT-CIRCUIT composition of two facts this
// package already carries separately: a compound through an accessor
// runs the getter then the setter (AccessorWalkReadModifyWrite's own
// doc), and the `||=`/`&&=`/`??=` family's kept branch runs NEITHER a
// PutValue NOR an evaluation of the right side at all
// (sec-assignment-operators-runtime-semantics-evaluation's three
// LogicalAssignment algs — ReadAssignment's identifier arm carries the
// same reading for a plain binding). Composed, the getter's read decides
// which branch the runtime takes; only the WRITE branch evaluates
// `rightNode` and runs the setter.
//
// UNLIKE AccessorWalkReadModifyWrite, this function takes rightNode
// UNEVALUATED — the caller must not evaluate bin.Right ahead of this
// call, because the kept branch must never run it at all. The caller
// still checks AccessorTargetOf before calling (the same resolve-before-
// evaluate discipline every accessor route shares), which costs nothing
// here since NEITHER read evaluates program state.
func AccessorLogicalReadModifyWrite(
	ctx *FlowContext, env Env, access, rightNode *ast.Node, kind ast.Kind,
) (abstractdomain.AbstractValue, bool) {
	target, ok := resolveAccessorWalkTarget(ctx, env, access)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	inlining, fresh := accessorInlining(ctx, target)
	if !fresh {
		return silence.Residue(), true
	}
	if target.accessorSymbol != nil {
		inlining[target.accessorSymbol] = struct{}{}
		defer delete(inlining, target.accessorSymbol)
	}
	held := runAccessorGetter(ctx, target, inlining)

	if kind == ast.KindQuestionQuestionEqualsToken {
		// decided by PRESENCE, exactly as the identifier arm's own `??=`
		// case (assignment_operators.go) and ReadBinary's bare `??` arm
		if held.Kind == abstractdomain.KindUndef {
			right := evaluateExpression(ctx, env, rightNode)
			nextReceiver := runAccessorSetter(ctx, target, inlining, right)
			UpdateTrackedEnv(ctx.Aliases, env, target.name, nextReceiver)
			return right, true
		}
		switch held.Kind {
		case abstractdomain.KindValues, abstractdomain.KindObject, abstractdomain.KindNaN, abstractdomain.KindSet, abstractdomain.KindList:
			// the kept branch: no PutValue, no setter call, no evaluation
			// of the right side at all
			return held, true
		}
		if held.Kind == abstractdomain.KindPossiblyUndefined {
			right := evaluateExpression(ctx, env, rightNode)
			writtenReceiver := runAccessorSetter(ctx, target, inlining, right)
			joinedValue := abstractdomain.JoinKnown(*held.Inner, right)
			joinedReceiver := joinAccessorReceivers(target.receiver, writtenReceiver)
			UpdateTrackedEnv(ctx.Aliases, env, target.name, joinedReceiver)
			return joinedValue, true
		}
		// undecided: the setter MAY run (with the right side) or may not —
		// both the value and the receiver join the two runs, the same
		// over-approximation the identifier arm's own undecided case takes
		right := evaluateExpression(ctx, env, rightNode)
		writtenReceiver := runAccessorSetter(ctx, target, inlining, right)
		joinedValue := abstractdomain.UnknownOver([]abstractdomain.AbstractValue{held, right})
		joinedReceiver := joinAccessorReceivers(target.receiver, writtenReceiver)
		UpdateTrackedEnv(ctx.Aliases, env, target.name, joinedReceiver)
		return joinedValue, true
	}

	// `&&=` / `||=`: decided by TRUTHINESS
	isAnd := kind == ast.KindAmpersandAmpersandEqualsToken
	verdict, hasVerdict := abstractdomain.TruthinessDecided(held)
	if hasVerdict {
		wantsAnd := isAnd && !verdict
		wantsOr := !isAnd && verdict
		if wantsAnd || wantsOr {
			// the kept branch: GetValue-then-return, no PutValue at all —
			// the right side never evaluates and the setter never runs
			return held, true
		}
		right := evaluateExpression(ctx, env, rightNode)
		nextReceiver := runAccessorSetter(ctx, target, inlining, right)
		UpdateTrackedEnv(ctx.Aliases, env, target.name, nextReceiver)
		return right, true
	}
	// undecided: the setter may or may not run — join both the value and
	// the receiver across the two possible runs
	right := evaluateExpression(ctx, env, rightNode)
	writtenReceiver := runAccessorSetter(ctx, target, inlining, right)
	joined := abstractdomain.JoinKnown(held, right)
	var joinedValue abstractdomain.AbstractValue
	if joined.Kind != abstractdomain.KindUnknown {
		joinedValue = joined
	} else {
		joinedValue = abstractdomain.UnknownOver([]abstractdomain.AbstractValue{held, right})
	}
	joinedReceiver := joinAccessorReceivers(target.receiver, writtenReceiver)
	UpdateTrackedEnv(ctx.Aliases, env, target.name, joinedReceiver)
	return joinedValue, true
}

// joinAccessorReceivers is the receiver an UNDECIDED short-circuit
// leaves behind: the setter ran on some executions (writtenReceiver) and
// not on others (the receiver's own pre-write state, kept) — an
// over-approximation of "maybe written" the same way the identifier
// arm's undecided join over-approximates a plain binding's value, applied
// to the whole receiver object rather than one scalar slot.
func joinAccessorReceivers(before, written abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	return abstractdomain.JoinKnown(before, written)
}

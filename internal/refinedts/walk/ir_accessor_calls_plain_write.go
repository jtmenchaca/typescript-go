// The WALK-route plain write through a get/set accessor —
// `box.age = v`, no compound operator. ReadAssignment's plain
// `KindEqualsToken` + PropertyAccessExpression arm
// (assignment_operators.go) calls WriteProperty directly, which finds
// no "age" key on a KindObject receiver whose "age" is a set accessor
// (ConstructedInstance never census-keys an accessor as a plain
// field), so it ADDS "age" as a fresh key holding the written value —
// the setter body never runs, so a backing field the setter writes
// (`this.held = v`) never moves. This file is the fix: a plain
// property write resolves to a SETTER-only accessor pair first, runs
// the setter's own body with its one parameter bound to the evaluated
// right side, and folds the setter's `this.key = value` writes into
// the receiver the same way the read-modify-write route does.
//
// UNLIKE resolveAccessorWalkTarget (ir_accessor_calls_read_modify_write.go),
// this resolution needs no GETTER — a plain `=` never reads the
// accessor's current value, only writes through it, so a set-only
// property (SinkBox's `set age(v)` with no matching getter) is exactly
// the shape this route serves. A get-only property (no setter at all)
// declines: the language defines no store operation for it (a runtime
// TypeError in strict mode), so ReadAssignment's own fallback below
// keeps its prior answer for that shape rather than this route
// claiming one it cannot honestly serve either.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// accessorSetterTarget is the resolved receiver + setter a plain
// accessor write needs — the read-modify-write route's
// accessorWalkTarget with the getter fields dropped, since a plain
// write never reads the accessor's current value.
type accessorSetterTarget struct {
	name           string
	receiver       abstractdomain.AbstractValue
	setterBody     *ast.Node
	setterParam    *ast.Node
	accessorSymbol *ast.Symbol
}

// resolveAccessorSetterTarget is AccessorWalkPlainWrite's own
// resolution: a tracked receiver name holding an OBJECT, whose
// accessed property resolves (AccessorDeclarationsOf) to a setter with
// a block body and one identifier parameter. A getter may or may not
// exist alongside it — irrelevant here, since a plain write never
// reads one. Any missing piece declines — the caller falls through to
// the plain-property arm exactly as it did before this route existed.
func resolveAccessorSetterTarget(ctx *FlowContext, env Env, access *ast.Node) (accessorSetterTarget, bool) {
	pae := access.AsPropertyAccessExpression()
	name, rooted := rootOfReceiver(pae.Expression)
	if !rooted {
		return accessorSetterTarget{}, false
	}
	receiver, hasReceiver := env.Get(name)
	if !hasReceiver || receiver.Kind != abstractdomain.KindObject {
		return accessorSetterTarget{}, false
	}
	_, setter, resolved := AccessorDeclarationsOf(ctx, access)
	if !resolved || setter == nil {
		return accessorSetterTarget{}, false
	}
	setterBody := setter.Body()
	if setterBody == nil || !ast.IsBlock(setterBody) {
		return accessorSetterTarget{}, false
	}
	setterParams := setter.Parameters()
	if len(setterParams) != 1 {
		return accessorSetterTarget{}, false
	}
	setterParamName := setterParams[0].AsParameterDeclaration().Name()
	if !ast.IsIdentifier(setterParamName) {
		return accessorSetterTarget{}, false
	}
	return accessorSetterTarget{
		name:           name,
		receiver:       receiver,
		setterBody:     setterBody,
		setterParam:    setterParams[0],
		accessorSymbol: symbolAt(ctx.P.Checker, pae.Name()),
	}, true
}

// AccessorSetterTargetOf is whether a property-access assignment's
// LEFT side resolves to a setter on a tracked receiver — a pure
// pre-check with no evaluation and no effect, so a caller may run it
// BEFORE evaluating the assignment's right side without risking a
// double-evaluated effect on decline (the same resolve-before-evaluate
// discipline AccessorTargetOf follows for the compound route).
func AccessorSetterTargetOf(ctx *FlowContext, env Env, access *ast.Node) bool {
	if !ast.IsPropertyAccessExpression(access) {
		return false
	}
	_, ok := resolveAccessorSetterTarget(ctx, env, access)
	return ok
}

// AccessorWalkPlainWrite runs a `box.age = right`-shaped plain write
// through the setter AccessorSetterTargetOf already found: the
// setter's one parameter bound to `right` (already evaluated by the
// caller), its `this.key = value` writes folded back into the
// receiver with setObjectKey — the same rebuild WriteProperty already
// runs for a `this.key = v` write, applied to the OUTER receiver
// instead of `this`. Declines only where resolveAccessorSetterTarget
// itself does, or where the accessor is already running (the same
// recursion guard the read-modify-write route shares).
func AccessorWalkPlainWrite(
	ctx *FlowContext, env Env, access *ast.Node, right abstractdomain.AbstractValue,
) (abstractdomain.AbstractValue, bool) {
	target, ok := resolveAccessorSetterTarget(ctx, env, access)
	if !ok {
		return abstractdomain.AbstractValue{}, false
	}
	rmwTarget := accessorWalkTarget{
		name:           target.name,
		receiver:       target.receiver,
		setterBody:     target.setterBody,
		setterParam:    target.setterParam,
		accessorSymbol: target.accessorSymbol,
	}
	inlining, fresh := accessorInlining(ctx, rmwTarget)
	if !fresh {
		return right, true
	}
	if rmwTarget.accessorSymbol != nil {
		inlining[rmwTarget.accessorSymbol] = struct{}{}
		defer delete(inlining, rmwTarget.accessorSymbol)
	}
	nextReceiver := runAccessorSetter(ctx, rmwTarget, inlining, right)
	UpdateTrackedEnv(ctx.Aliases, env, rmwTarget.name, nextReceiver)
	return right, true
}

// from evaluation/unmodeled_method_havoc.ts
//
// Unmodeled method tail: forgetThrough on a tracked reference that
// may write, havoc of reference arguments, re.exec / s.match opaque
// maybe, the default-library "not modeled" note, and the scalar
// return-type ground for unmodeled default-library calls.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// declaredInDefaultLib is declaredInDefaultLib in the TS source:
// whether the callee's own declaration lives in the default library —
// what separates an unmodeled BUILT-IN from a user method the walk
// merely did not follow. The one strict test, shared:
// program.ts resolvesToDefaultLib.
func declaredInDefaultLib(ctx *FlowContext, callee *ast.Node) bool {
	return resolvesToDefaultLib(ctx, callee)
}

func scalarGroundOfType(t *checker.Type) *abstractdomain.AbstractValue {
	members := typePartsOf(t)
	var present []*checker.Type
	for _, member := range members {
		if (member.Flags() & (checker.TypeFlagsUndefined | checker.TypeFlagsNull)) == 0 {
			present = append(present, member)
		}
	}
	if len(present) == 0 {
		return nil
	}
	sortOfMember := func(member *checker.Type) string {
		if (member.Flags() & (checker.TypeFlagsAny | checker.TypeFlagsUnknown)) != 0 {
			return ""
		}
		if (member.Flags() & checker.TypeFlagsNumberLike) != 0 {
			return "number"
		}
		if (member.Flags() & checker.TypeFlagsStringLike) != 0 {
			return "string"
		}
		if (member.Flags() & checker.TypeFlagsBooleanLike) != 0 {
			return "boolean"
		}
		return ""
	}
	sort := sortOfMember(present[0])
	for _, m := range present[1:] {
		if sortOfMember(m) != sort {
			return nil
		}
	}
	if sort == "" {
		return nil
	}
	// EVERY present part a literal: the type states its exact words —
	// reading `"postgres" | "mongo"` as all strings refuted honest
	// ReadonlyMap.get results at their own stated positions
	var exact *abstractdomain.AbstractValue
	if sort == "string" {
		allLiteral := true
		for _, m := range present {
			if !m.IsStringLiteral() {
				allLiteral = false
				break
			}
		}
		if allLiteral {
			first, _ := present[0].AsLiteralType().Value().(string)
			set := refinementsets.StringTuple(first)
			for _, m := range present[1:] {
				v, _ := m.AsLiteralType().Value().(string)
				set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(v)))
			}
			v := abstractdomain.KnownSet(set, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
			exact = &v
		}
	} else if sort == "number" {
		allLiteral := true
		for _, m := range present {
			if !m.IsNumberLiteral() {
				allLiteral = false
				break
			}
		}
		if allLiteral {
			values := make([]float64, len(present))
			for i, m := range present {
				values[i], _ = m.AsLiteralType().Value().(float64)
			}
			v := abstractdomain.KnownValues(values, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			exact = &v
		}
	}
	var ground abstractdomain.AbstractValue
	if exact != nil {
		ground = *exact
	} else if sort == "number" {
		ground = abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.RefinedSet{}, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone))
	} else if sort == "string" {
		ground = abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	} else {
		ground = abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec)
	}
	if len(present) == len(members) {
		return &ground
	}
	out := abstractdomain.PossiblyUndefined(ground, "", false, false)
	return &out
}

// readUnmodeledMethod is readUnmodeledMethod in the TS source: the
// unmodeled-method aftermath: reference forget, argument havoc,
// opaque provenance, and default-library scalar return grounds.
// Always answers — this is the end of the method-call chain.
func readUnmodeledMethod(site MethodCallSite) abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, receiver, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Receiver, site.Method
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	// an unmodeled method on a tracked reference: the class forgets —
	// UNLESS the method is on the read-only lists, which vouch it
	// writes nothing it was handed (measuring a tracked array is not
	// a reason to forget it). A CALLABLE argument voids the vouch:
	// a handed callback can write the receiver through its owner
	// parameter, so the class forgets after all.
	callableArgument := false
	for _, argument := range arguments {
		if ast.IsNumericLiteral(argument) || ast.IsStringLiteralLike(argument) ||
			argument.Kind == ast.KindTrueKeyword || argument.Kind == ast.KindFalseKeyword ||
			argument.Kind == ast.KindNullKeyword {
			continue
		}
		if isCallableArgument(ctx, argument) {
			callableArgument = true
			break
		}
	}
	_, isReadOnlyArrayMethod := dataflowfacts.ReadOnlyArrayMethods[method]
	_, isStringReadMethod := dataflowfacts.StringReadMethods[method]
	if dataflowfacts.ReferenceTyped(ctx.P.Checker, receiverExpression) &&
		(callableArgument || (!isReadOnlyArrayMethod && !isStringReadMethod)) {
		// a mutating method writes the structure its receiver roots
		// at — `o.xs.push(v)` moves o.xs, and every name sharing the
		// root's reference moves with it. ONE resolver decides what a
		// write through an expression forgets (finding 14):
		// forgetThrough, the same one assignment targets use.
		ForgetThrough(ctx, env, receiverExpression)
	}
	for _, argument := range arguments {
		evaluateExpression(ctx, env, argument)
		// the method may keep and write a reference argument
		if ast.IsIdentifier(argument) {
			if _, ok := env.Get(argument.Text()); ok && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
				HavocEnv(ctx.Aliases, env, argument.Text())
			}
		}
	}
	// `re.exec(s)` / `s.match(re)`: null, or a match array whose
	// pieces come from the host's matching — a maybe around outside
	// provenance, so `match?.[1] ?? match?.[2]` chains read on
	// (sec-regexp.prototype.exec: null or an Array of strings)
	if ast.IsPropertyAccessExpression(call.Expression) {
		pa := call.Expression.AsPropertyAccessExpression()
		if (pa.Name().Text() == "exec" || pa.Name().Text() == "match") && declaredInDefaultLib(ctx, call.Expression) {
			return abstractdomain.PossiblyUndefined(abstractdomain.Opaque, abstractdomain.TrustSpec, true, false)
		}
	}
	// a default-library method with no model says which one —
	// `"abc".match`, `map.get`, `JSON.parse`; a user's own method
	// stays with the row's default, because "not modeled" would
	// misfile it as a host gap
	if declaredInDefaultLib(ctx, call.Expression) {
		NoteUnmodeledCall(ctx, e)
	}
	// a method of an OPAQUE value, or one with NO BODY anywhere in
	// reach (a .d.ts signature): its result ENTERS from outside the
	// file's determination, so reads through it stay opaque
	if receiver.Kind == abstractdomain.KindUnknown && receiver.Opaque {
		return abstractdomain.Opaque
	}
	if BodilessCallee(ctx, call.Expression) && !declaredInDefaultLib(ctx, call.Expression) {
		return abstractdomain.Opaque
	}
	// an unmodeled default-library call still states its RETURN
	// TYPE: a scalar one answers the sort's whole ground — any
	// double or NaN, any string, or a boolean — with a
	// null/undefined arm riding as the maybe wrapper; any other
	// type keeps no claim
	if declaredInDefaultLib(ctx, call.Expression) {
		if ground := scalarGroundOfType(ctx.P.Checker.GetTypeAtLocation(e)); ground != nil {
			return *ground
		}
	}
	return silence.Residue()
}

// isCallableArgument mirrors the TS source's try/catch around
// getTypeAtLocation/getCallSignatures: a type question that panics
// (the TS catch's only reachable case in practice) reads as callable,
// the conservative branch the TS source's catch returns too.
func isCallableArgument(ctx *FlowContext, argument *ast.Node) (result bool) {
	defer func() {
		if recover() != nil {
			result = true
		}
	}()
	t := ctx.P.Checker.GetTypeAtLocation(argument)
	if (t.Flags() & checker.TypeFlagsAny) != 0 {
		return true
	}
	return len(ctx.P.Checker.GetCallSignatures(t)) > 0
}

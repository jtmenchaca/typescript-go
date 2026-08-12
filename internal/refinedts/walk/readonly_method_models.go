// from evaluation/readonly_method_models.ts
//
// Read-only array and string method gate: exact string and array
// readers first, then sequence-copy and membership-ground fallbacks,
// then unknownOver keeping receiver facts.
//
// PARTIAL: readStringMethods (string_method_models.ts),
// readArrayReadMethods (array_method_models.ts), and
// readSequenceCopyMethods (sequence_copy_models.ts) are declared as
// plain same-package calls per this directory's own port convention
// — their files are not yet ported in this pass.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/primitives"
)

// readReadonlyMethods is readReadonlyMethods in the TS source: the
// read-only array/string gate: exact string and array readers, then
// the shared fallbacks. Nil when the method is outside the read-only
// lists (or the receiver is Object's statics).
func readReadonlyMethods(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, receiver, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Receiver, site.Method
	// the Object statics ("values", "keys", …) share names with
	// the array iterators — route them past the array gate to
	// their own handlers below
	objectStaticReceiver := ast.IsIdentifier(receiverExpression) &&
		receiverExpression.Text() == "Object" && resolvesToDefaultLib(ctx, receiverExpression)
	_, isReadOnlyArrayMethod := dataflowfacts.ReadOnlyArrayMethods[method]
	_, isStringReadMethod := dataflowfacts.StringReadMethods[method]
	if objectStaticReceiver || !(isReadOnlyArrayMethod || isStringReadMethod) {
		return nil
	}
	call := e.AsCallExpression()
	var argKnowns []abstractdomain.AbstractValue
	if call.Arguments != nil {
		argKnowns = make([]abstractdomain.AbstractValue, len(call.Arguments.Nodes))
		for i, argument := range call.Arguments.Nodes {
			argKnowns[i] = evaluateExpression(ctx, env, argument)
		}
	}
	receiverStringy := primitives.IsStringKind(ctx.P.Checker, receiverExpression) ||
		(receiver.Kind == abstractdomain.KindValues && receiver.KindTag == abstractdomain.PrimitiveString)
	// the ledger floor for this block's HOST-ORACLE reads
	oracleGrade := abstractdomain.MinTrustLevel(abstractdomain.TrustSpec, abstractdomain.TrustLevelOf(receiver))
	for _, k := range argKnowns {
		oracleGrade = abstractdomain.MinTrustLevel(oracleGrade, abstractdomain.TrustLevelOf(k))
	}
	if answered := readStringMethods(site, argKnowns, receiverStringy, oracleGrade); answered != nil {
		return answered
	}
	if answered := readArrayReadMethods(site, argKnowns, receiverStringy, oracleGrade); answered != nil {
		return answered
	}
	if answered := readSequenceCopyMethods(site, argKnowns, receiverStringy); answered != nil {
		return answered
	}
	if answered := readMembershipGroundMethods(site, oracleGrade); answered != nil {
		return answered
	}
	// result unknown; receiver facts KEPT — and a result whose
	// only unknowns are opaque carries their provenance
	operands := append([]abstractdomain.AbstractValue{receiver}, argKnowns...)
	out := abstractdomain.UnknownOver(operands)
	return &out
}

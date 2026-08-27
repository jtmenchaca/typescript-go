// object_key_access.go's own tests: a sort-union receiver's key read
// inherits the receiver's own residue when it has one (JSON.parse's
// grammar claim), and states the generic key-set gate when it has none
// (a discriminated object narrowed by "in" at a nested path
// apply_narrowing.go has no per-arm rule for yet).
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// TestReadObjectKeyAccess_UnionReceiverWithNoResidueStatesTheKeySetGate
// pins A9.guard.member's own gap (hasROutside): after `!("r" in s)`,
// `s` stays a sort union of both Shape arms — apply_narrowing.go's
// narrowAt only splits a kind union by an EXACT discriminant key
// (n.Exact), never by a Definedness("undefined") test at a nested path,
// and the dotted place-value entry assume_condition.go writes for `s.r`
// stays a bare unknown (narrowAt's own Definedness("undefined") arm only
// converts an already-wrapped PossiblyUndefined/Undef place). So `s.r`
// reaches ReadObjectKeyAccess with a KindKindUnion receiver carrying no
// residue of its own, and the read now states the same key-set gate a
// KindObject receiver with an unproven key set already carries.
func TestReadObjectKeyAccess_UnionReceiverWithNoResidueStatesTheKeySetGate(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): number {\n"+
		"  return s.r;\n"+
		"}\n"+
		"declare const s: { kind: \"circle\"; r: number } | { kind: \"square\"; side: number };\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, nil, &diagnostics, nil)
	env := NewEnv()
	env.Set("s", abstractdomain.KindUnionOf([]abstractdomain.AbstractValue{
		abstractdomain.KnownObject([]abstractdomain.ObjectKey{
			{Name: "kind", Value: abstractdomain.KnownValues(nil, abstractdomain.PrimitiveString, abstractdomain.TrustProved)},
			{Name: "r", Value: abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)},
		}, nil, true, abstractdomain.TrustProved, false),
		abstractdomain.KnownObject([]abstractdomain.ObjectKey{
			{Name: "kind", Value: abstractdomain.KnownValues(nil, abstractdomain.PrimitiveString, abstractdomain.TrustProved)},
			{Name: "side", Value: abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)},
		}, nil, true, abstractdomain.TrustProved, false),
	}))

	statements := compoundAssignFunctionStatements(t, p, "f")
	sReturn := statements[0].AsReturnStatement().Expression

	held := evaluateExpression(ctx, env, sReturn)
	want := "the receiver's key set is not proven complete, so a name outside its known keys is neither proven present nor proven absent"
	if held.Kind != abstractdomain.KindUnknown || held.ResidueReason != want {
		t.Fatalf("evaluateExpression(s.r) = %+v, want an unknown with ResidueReason %q", held, want)
	}
}

// TestReadObjectKeyAccess_UnionReceiverWithItsOwnResidueInheritsIt pins
// B7.est.guard's own gap (decoded.a): JSON.parse of unpinned text
// answers anyJSONValue's determined grammar union with its own
// ResidueReason set (coercion_models_json.go's jsonParseOfUnknownTextSaid)
// — a key read off it, with no dotted place-value entry to override it,
// inherits that SAME reason rather than falling to the generic key-set
// gate, which would name a construct (a key set) the receiver never
// claimed to have.
func TestReadObjectKeyAccess_UnionReceiverWithItsOwnResidueInheritsIt(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): number {\n"+
		"  return decoded.a;\n"+
		"}\n"+
		"declare const decoded: unknown;\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, nil, &diagnostics, nil)
	env := NewEnv()
	reason := "JSON.parse of unknown text yields whatever JSON value the text spells — the type is everything this file determines"
	union := abstractdomain.KindUnionOf([]abstractdomain.AbstractValue{
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone),
		abstractdomain.AtTrustLevel(abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustProved, false), abstractdomain.TrustSpec),
	})
	union.ResidueReason = reason
	env.Set("decoded", union)

	statements := compoundAssignFunctionStatements(t, p, "f")
	decodedReturn := statements[0].AsReturnStatement().Expression

	held := evaluateExpression(ctx, env, decodedReturn)
	if held.Kind != abstractdomain.KindUnknown || held.ResidueReason != reason {
		t.Fatalf("evaluateExpression(decoded.a) = %+v, want an unknown with ResidueReason %q", held, reason)
	}
}

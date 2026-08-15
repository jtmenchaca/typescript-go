// Tests for ir_array_search.go: `a.indexOf(v)` / `a.lastIndexOf(v)` /
// `a.includes(v)` / `a.at(i)` over a flattened array's two slots.
//
// ArrayNumericReadEffect is not yet wired into EffectOf's Opaque
// callback (that hook lives in ir_assignment_effect_read.go, outside
// this package's array files), so these tests call the dispatcher
// directly on a parsed expression statement's call node — the same
// shape the hook will hand it once landed.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// arrayReadCallNodeOf parses one statement and answers the call
// expression an expression-statement or a declaration's initializer
// carries — the node ArrayNumericReadEffect / ArrayJoinEffect reads.
func arrayReadCallNodeOf(t *testing.T, source string) *ast.Node {
	t.Helper()
	statements := loweringParse(t, source)
	if len(statements) != 1 {
		t.Fatalf("parsed %d statements, want 1", len(statements))
	}
	statement := statements[0]
	if ast.IsExpressionStatement(statement) {
		return Unwrapped(statement.AsExpressionStatement().Expression)
	}
	if ast.IsVariableStatement(statement) {
		declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
		if len(declarations) == 1 && declarations[0].AsVariableDeclaration().Initializer != nil {
			return Unwrapped(declarations[0].AsVariableDeclaration().Initializer)
		}
	}
	t.Fatalf("source %q did not parse to a call-bearing statement", source)
	return nil
}

func TestArraySearch_IndexOfAnswersTheMinusOneThroughLenMinusOneWindow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	node := arrayReadCallNodeOf(t, `a.indexOf(9);`)
	effect, ok := ArrayNumericReadEffect(context, node)
	if !ok {
		t.Fatalf("ArrayNumericReadEffect(a.indexOf(9)) ok = false, want true")
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{3}))},
		{Top: true},
	}, []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementAssign, Target: 0, Effect: effect}})
	result := loweringSetOf(t, exit[0])
	if !kernel.Member(result, []float64{-1}) {
		t.Errorf("member(indexOf-result, [-1]) = false, want true — the not-found sentinel")
	}
	if !kernel.Member(result, []float64{2}) {
		t.Errorf("member(indexOf-result, [2]) = false, want true — len-1 for a len-3 array")
	}
	if kernel.Member(result, []float64{3}) {
		t.Errorf("member(indexOf-result, [3]) = true, want false — at or past the length")
	}
	if kernel.Member(result, []float64{-2}) {
		t.Errorf("member(indexOf-result, [-2]) = true, want false — below the sentinel")
	}
}

func TestArraySearch_LastIndexOfAnswersTheSameWindowAsIndexOf(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	node := arrayReadCallNodeOf(t, `a.lastIndexOf(9);`)
	effect, ok := ArrayNumericReadEffect(context, node)
	if !ok {
		t.Fatalf("ArrayNumericReadEffect(a.lastIndexOf(9)) ok = false, want true")
	}
	if effect.Kind != kernelbridge.LoopEffectJoin {
		t.Fatalf("effect.Kind = %q, want %q — the same join(-1, len-1) indexOf answers", effect.Kind, kernelbridge.LoopEffectJoin)
	}
	if effect.B == nil || effect.B.Kind != kernelbridge.LoopEffectBinary || effect.B.Op != kernelbridge.LoopOpSub {
		t.Errorf("effect.B = %+v, want the len-1 binary sub", effect.B)
	}
}

func TestArraySearch_IndexOfOverAnUnboundedLenStillExcludesBelowMinusOne(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	node := arrayReadCallNodeOf(t, `a.indexOf(9);`)
	effect, ok := ArrayNumericReadEffect(context, node)
	if !ok {
		t.Fatalf("ArrayNumericReadEffect ok = false, want true")
	}
	// the len slot enters AtLeast(0) + Integer — the floor every
	// flattened array's len slot carries as a CONSEQUENCE of its own
	// writes (an exact non-negative count at the declaration, stepped
	// by non-negative amounts thereafter), not a floor Top itself
	// states. -1 is always a member (the join's own left side); -2
	// never is, whatever the len slot's own upper reach turns out to be.
	exit := kernel.Walk([]kernelbridge.KnownStateWire{
		{Set: refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.Integer)},
		{Top: true},
	}, []kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementAssign, Target: 0, Effect: effect}})
	result := loweringSetOf(t, exit[0])
	if !kernel.Member(result, []float64{-1}) {
		t.Errorf("member(indexOf-result, [-1]) = false, want true")
	}
	if kernel.Member(result, []float64{-2}) {
		t.Errorf("member(indexOf-result, [-2]) = true, want false")
	}
}

func TestArraySearch_IncludesAnswersTheBooleanPair(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	context := arrayLoweringContext(kernel,
		[]string{"a.len", "a.elem"},
		[]BindingKind{BindingKindNumber, BindingKindNumber})
	node := arrayReadCallNodeOf(t, `a.includes(9);`)
	effect, ok := ArrayNumericReadEffect(context, node)
	if !ok {
		t.Fatalf("ArrayNumericReadEffect(a.includes(9)) ok = false, want true")
	}
	if effect.Kind != kernelbridge.LoopEffectConst {
		t.Fatalf("effect.Kind = %q, want %q", effect.Kind, kernelbridge.LoopEffectConst)
	}
	exit := kernel.Walk([]kernelbridge.KnownStateWire{{Top: true}, {Top: true}},
		[]kernelbridge.IrStatement{{Kind: kernelbridge.IrStatementAssign, Target: 0, Effect: effect}})
	result := loweringSetOf(t, exit[0])
	if !kernel.Member(result, []float64{0}) || !kernel.Member(result, []float64{1}) {
		t.Errorf("includes-result = %+v, want exactly {0, 1}", result)
	}
	if kernel.Member(result, []float64{2}) {
		t.Errorf("member(includes-result, [2]) = true, want false")
	}
}

func TestArraySearch_IncludesDeclinesAWriteBearingArgument(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "n"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 3),
	}
	node := arrayReadCallNodeOf(t, `a.includes(n = 1);`)
	if _, ok := ArrayNumericReadEffect(context, node); ok {
		t.Errorf("ArrayNumericReadEffect(a.includes(n = 1)) ok = true, want false — the argument writes")
	}
}

func TestArraySearch_AtUnderADominatingLengthGuardIsThePlainElemRead(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "i"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 3),
	}
	undo := HoldBoundIndex(context, "i", "a")
	defer undo()
	node := arrayReadCallNodeOf(t, `a.at(i);`)
	effect, ok := ArrayNumericReadEffect(context, node)
	if !ok {
		t.Fatalf("ArrayNumericReadEffect(a.at(i)) ok = false, want true")
	}
	if effect.Kind != kernelbridge.LoopEffectVar || effect.Index != 1 {
		t.Errorf("effect = %+v, want a plain var read of slot 1 (a.elem)", effect)
	}
}

func TestArraySearch_AtWithNoBoundCarriesTheOrAbsentWrapping(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem", "i"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 3),
	}
	node := arrayReadCallNodeOf(t, `a.at(i);`)
	effect, ok := ArrayNumericReadEffect(context, node)
	if !ok {
		t.Fatalf("ArrayNumericReadEffect(a.at(i)) ok = false, want true")
	}
	if effect.Kind != kernelbridge.LoopEffectOrAbsent {
		t.Fatalf("effect.Kind = %q, want %q — nothing bounds i", effect.Kind, kernelbridge.LoopEffectOrAbsent)
	}
	if effect.A == nil || effect.A.Kind != kernelbridge.LoopEffectVar || effect.A.Index != 1 {
		t.Errorf("or-absent operand = %+v, want a var read of slot 1 (a.elem)", effect.A)
	}
}

func TestArraySearch_AtWithANegativeLiteralIndexCarriesTheOrAbsentWrapping(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 2),
	}
	node := arrayReadCallNodeOf(t, `a.at(-1);`)
	effect, ok := ArrayNumericReadEffect(context, node)
	if !ok {
		t.Fatalf("ArrayNumericReadEffect(a.at(-1)) ok = false, want true")
	}
	// a negative index is not a spelled name, so IndexIsBounded never
	// gets asked — the or-absent wrapping is the only sound answer
	// (sec-array.prototype.at reads length + relativeIndex, a position
	// the guard scope never proved bounded)
	if effect.Kind != kernelbridge.LoopEffectOrAbsent {
		t.Errorf("effect.Kind = %q, want %q", effect.Kind, kernelbridge.LoopEffectOrAbsent)
	}
}

func TestArraySearch_AnUnlistedArgumentCountDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"a.len", "a.elem"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  make([]TypeofTag, 2),
	}
	node := arrayReadCallNodeOf(t, `a.indexOf(9, 1);`)
	if _, ok := ArrayNumericReadEffect(context, node); ok {
		t.Errorf("ArrayNumericReadEffect(a.indexOf(9, 1)) ok = true, want false — the fromIndex shape is not read")
	}
}

func TestArraySearch_AnUnrecognizedReceiverDeclines(t *testing.T) {
	context := &LoweringContext{
		Bindings: []string{"b"},
		Sorts:    []BindingKind{BindingKindNumber},
		Typeofs:  make([]TypeofTag, 1),
	}
	node := arrayReadCallNodeOf(t, `a.indexOf(9);`)
	if _, ok := ArrayNumericReadEffect(context, node); ok {
		t.Errorf("ArrayNumericReadEffect over an unflattened receiver ok = true, want false")
	}
}

// Targeted tests for destructuring-family fixes:
//
//  1. SlotOfIndex's exact-primitive-array branch (destructuring.go) —
//     an EXACT array literal's length is as exact as a KindList's, so a
//     slot past its own known length must answer Undef (provably
//     absent), the same rule the KindList branch already applies. The
//     old code fell through to darkSlotOf's honest-Unknown instead.
//
//  2. bindArrayPattern's leaf branch (destructure_binding.go) never
//     called withDefaultValue at all — a default on an array-pattern
//     slot (`const [first = 18] = [] as number[]`) was silently
//     ignored, so a provably-absent slot bound `first` to Undef
//     instead of running the default expression — g-binding-
//     destructuring.ts's defaultOnArrayPattern row.
//
//  3. bindArrayPattern had no branch for a NESTED pattern at an array
//     element (`[[first]]`, `[{ age }]`) — the name inside such a
//     pattern was never bound at all, so a later read fell through to
//     the checker's own wide static type instead of the destructured
//     element — g-binding-destructuring.ts's nestedArrayUnderArray/
//     nestedObjectUnderArray rows.
//
//  4. WriteAssignmentPattern (assignments.go) — a destructuring
//     ASSIGNMENT `({ a } = x)` / `[a] = xs`, not a declaration, now
//     judges each bound leaf against its own declared type through
//     WriteBinding, the same way a plain identifier target already
//     does. The old code had no case for an object/array-literal LHS
//     in ReadAssignment, so it fell to ReadForgottenAssignment, which
//     only HAVOCS every bound name through ForgetThrough — g-binding-
//     destructuring.ts's objectDestructureAssignment/
//     arrayDestructureAssignment rows.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// TestSlotOfIndex_AnExactArrayPastItsKnownLengthIsProvablyAbsent is the
// narrow unit case: reading position 0 of an EXACT, EMPTY number array
// answers Undef, not honest-Unknown — the same reading SlotOfIndex's
// KindList branch already gives past ITS own known length.
func TestSlotOfIndex_AnExactArrayPastItsKnownLengthIsProvablyAbsent(t *testing.T) {
	empty := abstractdomain.KnownValues(nil, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	held := SlotOfIndex(empty, 0)
	if held.Kind != abstractdomain.KindUndef {
		t.Errorf("SlotOfIndex(exact empty array, 0).Kind = %v, want Undef", held.Kind)
	}
}

// TestSlotOfIndex_AnExactArrayInsideItsKnownLengthStillReadsTheElement
// guards the sibling arm: a slot WITHIN the exact array's length still
// answers the element itself, unaffected by the past-end fix.
func TestSlotOfIndex_AnExactArrayInsideItsKnownLengthStillReadsTheElement(t *testing.T) {
	one := abstractdomain.KnownValues([]float64{40}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	held := SlotOfIndex(one, 0)
	formatted, ok := abstractdomain.FormatAbstractValue(held)
	if !ok || formatted != "40" {
		t.Errorf("SlotOfIndex(exact [40], 0) = %q, %v, want %q, true", formatted, ok, "40")
	}
}

// destructuringFamilyVariableStatementNamed mirrors
// destructureBindingVariableStatementNamed (destructure_binding_test.go)
// — the Nth top-level VariableStatement inside a named function's body.
func destructuringFamilyVariableStatementNamed(t *testing.T, fn *ast.Node, index int) *ast.Node {
	t.Helper()
	body := fn.AsFunctionDeclaration().Body
	if body == nil {
		t.Fatalf("function has no body")
	}
	found := 0
	for _, statement := range body.AsBlock().Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		if found == index {
			return statement
		}
		found++
	}
	t.Fatalf("no variable statement at index %d", index)
	return nil
}

// destructuringFamilyExpressionStatementNamed finds the Nth top-level
// ExpressionStatement inside a named function's body — the assignment-
// pattern rows live here, not in a VariableStatement.
func destructuringFamilyExpressionStatementNamed(t *testing.T, fn *ast.Node, index int) *ast.Node {
	t.Helper()
	body := fn.AsFunctionDeclaration().Body
	if body == nil {
		t.Fatalf("function has no body")
	}
	found := 0
	for _, statement := range body.AsBlock().Statements.Nodes {
		if !ast.IsExpressionStatement(statement) {
			continue
		}
		if found == index {
			return statement
		}
		found++
	}
	t.Fatalf("no expression statement at index %d", index)
	return nil
}

// destructuringFamilyCtx mirrors destructureBindingCtx — a checker-
// backed FlowContext with an empty registry.
func destructuringFamilyCtx(p *program.CheckerProgram, report func(assignability.RefinementDiagnostic)) *FlowContext {
	if report == nil {
		report = func(assignability.RefinementDiagnostic) {}
	}
	return &FlowContext{
		P:        p,
		Report:   report,
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
}

// TestBindArrayPattern_ADefaultOnAnExactEmptyArrayReadsTheDefaultItself
// is the fixture's own shape (g-binding-destructuring.ts,
// defaultOnArrayPattern's in-set leg): `const [first = 18] = [] as
// number[]` binds first to exactly 18, not Undef — bindArrayPattern's
// leaf branch now calls withDefaultValue when the element carries an
// initializer, the same call bindObjectPattern's leaf branch already
// made; before this fix the initializer was never read at all, and
// the provably-absent slot (Undef) bound first directly.
func TestBindArrayPattern_ADefaultOnAnExactEmptyArrayReadsTheDefaultItself(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  const [first = 18] = [] as number[];
  first;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructuringFamilyCtx(p, nil)
	env := NewEnv()

	patternDecl := destructuringFamilyVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, patternDecl)

	held, ok := env.Get("first")
	if !ok {
		t.Fatalf("first was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "18" {
		t.Errorf("env[first] = %q, %v (Kind=%v), want %q, true — the default is the whole binding when the slot is provably absent", formatted, formatOk, held.Kind, "18")
	}
}

// TestBindArrayPattern_ADefaultOnAnExactEmptyArrayReadsItsOwnOutOfRangeValue
// mirrors the fixture's second destructure — `[overFirst = 200] = []`
// — confirming the default's own out-of-range value carries through to
// overFirst exactly, the value the fixture's `return overFirst` line
// depends on firing against Age.
func TestBindArrayPattern_ADefaultOnAnExactEmptyArrayReadsItsOwnOutOfRangeValue(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  const [overFirst = 200] = [] as number[];
  overFirst;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructuringFamilyCtx(p, nil)
	env := NewEnv()

	patternDecl := destructuringFamilyVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, patternDecl)

	held, ok := env.Get("overFirst")
	if !ok {
		t.Fatalf("overFirst was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "200" {
		t.Errorf("env[overFirst] = %q, %v (Kind=%v), want %q, true", formatted, formatOk, held.Kind, "200")
	}
}

// TestWriteAssignmentPattern_AnObjectAssignmentPatternJudgesItsLeafAgainstTheDeclaredType
// is the fixture's own shape (g-binding-destructuring.ts,
// objectDestructureAssignment's marked leg): `({ age } = over)` where
// `over.age` is 200 and `age` is declared `Age` must fire through
// WriteBinding, exactly as a plain `age = 200` assignment already does
// — the destructuring-pattern shape must not shield the write from
// judgment.
func TestWriteAssignmentPattern_AnObjectAssignmentPatternJudgesItsLeafAgainstTheDeclaredType(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  let age: number = 0;
  const over = { age: 200 };
  ({ age } = over);
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := destructuringFamilyCtx(p, func(d assignability.RefinementDiagnostic) {
		diagnostics = append(diagnostics, d)
	})
	env := NewEnv()

	ageDecl := destructuringFamilyVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, ageDecl)

	overDecl := destructuringFamilyVariableStatementNamed(t, fn, 1)
	AnalyzeVariableStatement(ctx, env, overDecl)

	assignStmt := destructuringFamilyExpressionStatementNamed(t, fn, 0)
	evaluateExpression(ctx, env, assignStmt.AsExpressionStatement().Expression)

	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("age was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "200" {
		t.Errorf("env[age] = %q, %v (Kind=%v), want %q, true — the assignment pattern must carry 200 into the slot, the same value a plain `age = 200` would", formatted, formatOk, held.Kind, "200")
	}
}

// TestWriteAssignmentPattern_AnArrayAssignmentPatternJudgesItsLeafAgainstTheDeclaredType
// mirrors the object case for `[age] = overs` — g-binding-
// destructuring.ts's arrayDestructureAssignment marked leg.
func TestWriteAssignmentPattern_AnArrayAssignmentPatternJudgesItsLeafAgainstTheDeclaredType(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  let age: number = 0;
  const overs = [200];
  [age] = overs;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructuringFamilyCtx(p, nil)
	env := NewEnv()

	ageDecl := destructuringFamilyVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, ageDecl)

	oversDecl := destructuringFamilyVariableStatementNamed(t, fn, 1)
	AnalyzeVariableStatement(ctx, env, oversDecl)

	assignStmt := destructuringFamilyExpressionStatementNamed(t, fn, 0)
	evaluateExpression(ctx, env, assignStmt.AsExpressionStatement().Expression)

	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("age was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "200" {
		t.Errorf("env[age] = %q, %v (Kind=%v), want %q, true — the assignment pattern must carry 200 into the slot", formatted, formatOk, held.Kind, "200")
	}
}

// TestBindArrayPattern_ANestedArrayPatternElementBindsTheInnerElement
// is the fixture's own shape (g-binding-destructuring.ts,
// nestedArrayUnderArray's in-set leg): `const [[first]] = pairs` where
// `pairs = [[40, 41]]` binds first to exactly 40 — bindArrayPattern's
// new nested-pattern branch recurses through destructureInto/
// ReadDestructuring rather than skipping the element outright; before
// this fix first was never bound at all, and a later read fell
// through to the checker's own wide `number` static type.
func TestBindArrayPattern_ANestedArrayPatternElementBindsTheInnerElement(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  const pairs = [[40, 41]];
  const [[first]] = pairs;
  first;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructuringFamilyCtx(p, nil)
	env := NewEnv()

	pairsDecl := destructuringFamilyVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, pairsDecl)

	patternDecl := destructuringFamilyVariableStatementNamed(t, fn, 1)
	AnalyzeVariableStatement(ctx, env, patternDecl)

	held, ok := env.Get("first")
	if !ok {
		t.Fatalf("first was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "40" {
		t.Errorf("env[first] = %q, %v (Kind=%v), want %q, true — the nested array pattern must bind the inner element exactly", formatted, formatOk, held.Kind, "40")
	}
}

// TestBindArrayPattern_ANestedObjectPatternElementBindsTheKeyedSlot
// mirrors the fixture's nestedObjectUnderArray row: `const [{ age }] =
// people` where `people = [{ age: 40 }]` binds age to exactly 40 — the
// same nested-pattern branch, recursing into an OBJECT pattern this
// time rather than an array one.
func TestBindArrayPattern_ANestedObjectPatternElementBindsTheKeyedSlot(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  const people = [{ age: 40 }];
  const [{ age }] = people;
  age;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructuringFamilyCtx(p, nil)
	env := NewEnv()

	peopleDecl := destructuringFamilyVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, peopleDecl)

	patternDecl := destructuringFamilyVariableStatementNamed(t, fn, 1)
	AnalyzeVariableStatement(ctx, env, patternDecl)

	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("age was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "40" {
		t.Errorf("env[age] = %q, %v (Kind=%v), want %q, true — the nested object pattern must bind the keyed slot exactly", formatted, formatOk, held.Kind, "40")
	}
}

// TestBindObjectPattern_AComputedKeyThatPinsExactlyOneStringReadsThatSlot
// is the fixture's own shape (g-binding-destructuring.ts,
// computedKeyReadFree's in-set leg): `const { [key]: age } = person`
// where `key = "age"` and `person = { age: 40 }` binds age to exactly
// 40 — bindingElementKey now recognizes a ComputedPropertyName whose
// expression pins exactly one string (exactStringName) and reads the
// same slot a plain `{ age }` pick would; before this fix a computed
// key was never recognized at all, and age fell through to the
// checker's own wide `number` static type via SeededBinding's
// last-reader fallback.
func TestBindObjectPattern_AComputedKeyThatPinsExactlyOneStringReadsThatSlot(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  const key = "age";
  const person = { age: 40 };
  const { [key]: age } = person;
  age;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructuringFamilyCtx(p, nil)
	env := NewEnv()

	keyDecl := destructuringFamilyVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, keyDecl)

	personDecl := destructuringFamilyVariableStatementNamed(t, fn, 1)
	AnalyzeVariableStatement(ctx, env, personDecl)

	patternDecl := destructuringFamilyVariableStatementNamed(t, fn, 2)
	AnalyzeVariableStatement(ctx, env, patternDecl)

	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("age was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "40" {
		t.Errorf("env[age] = %q, %v (Kind=%v), want %q, true — the computed key must resolve to the same slot a plain `age` pick would", formatted, formatOk, held.Kind, "40")
	}
}

// TestBindObjectPattern_AComputedKeyWithACallStillResolvesTheSlot
// mirrors the fixture's computedKeyWithSideEffect row: the key
// expression is a CALL (`nextKey()`) rather than a bare identifier
// read — bindingElementKey evaluates it through the ordinary
// evaluateExpression path (which runs the call and its effects) and
// still recovers the exact string it returns.
func TestBindObjectPattern_AComputedKeyWithACallStillResolvesTheSlot(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(): void {
  const nextKey = (): string => "age";
  const person: Record<string, number> = { age: 40 };
  const { [nextKey()]: age } = person;
  age;
}
`)
	fn := entryEnvFunctionNamed(t, p, "f")
	ctx := destructuringFamilyCtx(p, nil)
	env := NewEnv()

	nextKeyDecl := destructuringFamilyVariableStatementNamed(t, fn, 0)
	AnalyzeVariableStatement(ctx, env, nextKeyDecl)

	personDecl := destructuringFamilyVariableStatementNamed(t, fn, 1)
	AnalyzeVariableStatement(ctx, env, personDecl)

	patternDecl := destructuringFamilyVariableStatementNamed(t, fn, 2)
	AnalyzeVariableStatement(ctx, env, patternDecl)

	held, ok := env.Get("age")
	if !ok {
		t.Fatalf("age was never bound")
	}
	formatted, formatOk := abstractdomain.FormatAbstractValue(held)
	if !formatOk || formatted != "40" {
		t.Errorf("env[age] = %q, %v (Kind=%v), want %q, true — a computed key behind a call must still resolve to the exact slot", formatted, formatOk, held.Kind, "40")
	}
}

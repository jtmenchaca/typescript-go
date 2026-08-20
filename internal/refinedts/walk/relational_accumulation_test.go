// The relational-accumulation recognizer: accumulate-then-divide-by-
// count is read off two adjacent statements and lowered into ONE kernel
// program — the "loopAccum" statement carrying the per-pass term, then
// the division that consumes the relation it left. Shape assertions
// only: the lowered program's JSON and the entry states, never a kernel
// walk, so this file runs with no dylib present.

package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// relationalAccumulationContext is the walk context the cases share: a
// real checker program (the length read evaluates through the ordinary
// expression route, which asks the host's type at the occurrence) with
// an alias store and a swallowed diagnostic sink.
func relationalAccumulationContext(p *program.CheckerProgram) *FlowContext {
	return &FlowContext{
		P:         p,
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
}

// relationalAccumulationBodyOf parses one source file whose single
// function declaration holds the statements the recognizer reads, and
// answers those statements.
func relationalAccumulationBodyOf(t *testing.T, p *program.CheckerProgram, name string) []*ast.Node {
	t.Helper()
	fn := entryEnvFunctionNamed(t, p, name)
	body := fn.AsFunctionDeclaration().Body
	if body == nil {
		t.Fatalf("function %s has no body", name)
	}
	return body.AsBlock().Statements.Nodes
}

// relationalAccumulationEnv seeds the two names the recognizer reads off
// the environment: the accumulator at exactly 0, and the sequence as a
// repetition of a bounded element with at least one member — the shape
// `clamped` wears in the audio fixture after the clamp derivation.
func relationalAccumulationEnv(totalName, sequenceName string) Env {
	env := NewEnv()
	env.Set(totalName, abstractdomain.KnownValues(
		[]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	element := refinementsets.MakeRefinedSet(
		refinementsets.AtLeast(-1), refinementsets.AtMost(1))
	env.Set(sequenceName, abstractdomain.KnownSet(
		refinementsets.Repetition(element, 1, nil), nil,
		abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	return env
}

func TestRelationalAccumulation_ASumOverASequenceDividedByItsLengthLowersToOneLoopAccumProgram(t *testing.T) {
	p := entryEnvTestProgram(t, `
function audioLevel(clamped: number[]): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	const mean = total / clamped.length;
	return mean;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "audioLevel")
	// statements[0] is `let total = 0`; the loop is statements[1] and the
	// division statements[2] — the pair the recognizer reads
	accumulation, ok := RelationalAccumulationOf(
		relationalAccumulationContext(p), relationalAccumulationEnv("total", "clamped"), statements, 1)
	if !ok {
		t.Fatalf("the sum-then-divide pair declined to lower")
	}
	if accumulation.TotalName != "total" || accumulation.MeanName != "mean" {
		t.Errorf("(TotalName, MeanName) = (%q, %q), want (total, mean)",
			accumulation.TotalName, accumulation.MeanName)
	}
	if len(accumulation.Stmts) != 2 {
		t.Fatalf("len(Stmts) = %d, want 2 — the accumulation and the division it feeds: %+v",
			len(accumulation.Stmts), accumulation.Stmts)
	}
	// the loopAccum's own wire: three slot indices and the per-pass term,
	// which reads its iteration value from the src slot on both sides of
	// the multiply
	wantAccum := `{"loopAccum":{"total":0,"src":1,"len":2,"body":{"op":"binary64.mul","A":{"var":1},"B":{"var":1}}}}`
	if got := kernelbridge.StmtWire(accumulation.Stmts[0]); got != wantAccum {
		t.Errorf("StmtWire(loopAccum) =\n  %s\nwant\n  %s", got, wantAccum)
	}
	// the division reads the total and the count by slot, so the kernel
	// meets it against the relation the statement above left behind
	wantDiv := `{"assign":{"target":3,"e":{"op":"binary64.div","A":{"var":0},"B":{"var":2}}}}`
	if got := kernelbridge.StmtWire(accumulation.Stmts[1]); got != wantDiv {
		t.Errorf("StmtWire(division) =\n  %s\nwant\n  %s", got, wantDiv)
	}
	// four entry states in the fixed slot order: the exact start, the
	// element, the count, and the divided name's own top
	if len(accumulation.States) != 4 {
		t.Fatalf("len(States) = %d, want 4: %+v", len(accumulation.States), accumulation.States)
	}
	if !accumulation.States[3].Top {
		t.Errorf("States[3] = %+v, want top — nothing is known about the divided name at entry",
			accumulation.States[3])
	}
	for slot, state := range accumulation.States[:3] {
		if state.Top {
			t.Errorf("States[%d] is top — every read participant states a set", slot)
		}
	}
}

func TestRelationalAccumulation_ATermReadingTheAccumulatorDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(clamped: number[]): number {
	let total = 0;
	for (const s of clamped) { total += total * s; }
	const mean = total / clamped.length;
	return mean;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// `total * s` is not a sum of per-element terms: the relation
	// `total <= count * termHi` reads termHi off the element alone, and a
	// term carrying the running total has no such ceiling
	if _, ok := RelationalAccumulationOf(
		relationalAccumulationContext(p), relationalAccumulationEnv("total", "clamped"), statements, 1); ok {
		t.Errorf("a term reading the accumulator lowered; it must decline")
	}
}

func TestRelationalAccumulation_ADivisionByAnotherSequencesLengthDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(clamped: number[], other: number[]): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	const mean = total / other.length;
	return mean;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	env := relationalAccumulationEnv("total", "clamped")
	env.Set("other", abstractdomain.KnownSet(
		refinementsets.Repetition(refinementsets.MakeRefinedSet(refinementsets.AtLeast(0)), 1, nil),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	// the count the passes ran is clamped's, not other's — no relation
	// ties the total to this denominator at all
	if _, ok := RelationalAccumulationOf(relationalAccumulationContext(p), env, statements, 1); ok {
		t.Errorf("a division by a second sequence's length lowered; it must decline")
	}
}

func TestRelationalAccumulation_AnAccumulatorWithoutAnExactStartDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(clamped: number[], seed: number): number {
	let total = seed;
	for (const s of clamped) { total += s * s; }
	const mean = total / clamped.length;
	return mean;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	env := relationalAccumulationEnv("total", "clamped")
	// the accumulator arrives as a WINDOW rather than one value: the
	// total's own floor is then unrelated to the count, so the relation
	// the statement would carry is not the one the source states
	env.Set("total", abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(5)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	if _, ok := RelationalAccumulationOf(relationalAccumulationContext(p), env, statements, 1); ok {
		t.Errorf("an accumulator without an exact start lowered; it must decline")
	}
}

func TestRelationalAccumulation_ALoopBodyWithASecondStatementDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(clamped: number[]): number {
	let total = 0;
	let count = 0;
	for (const s of clamped) { total += s * s; count += 1; }
	const mean = total / clamped.length;
	return mean;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	env := relationalAccumulationEnv("total", "clamped")
	env.Set("count", abstractdomain.KnownValues(
		[]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	// the accumulation form carries ONE per-pass term into ONE slot; a
	// second write per pass moves a slot the statement never names
	if _, ok := RelationalAccumulationOf(relationalAccumulationContext(p), env, statements, 2); ok {
		t.Errorf("a two-statement loop body lowered; it must decline")
	}
}

func TestRelationalAccumulation_AForAwaitLoopDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
async function f(clamped: number[]): Promise<number> {
	let total = 0;
	for await (const s of clamped) { total += s * s; }
	const mean = total / clamped.length;
	return mean;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// `for await` binds the AWAITED value at each pass, which the element
	// state the src slot carries does not hold
	if _, ok := RelationalAccumulationOf(
		relationalAccumulationContext(p), relationalAccumulationEnv("total", "clamped"), statements, 1); ok {
		t.Errorf("a for-await loop lowered; it must decline")
	}
}

/* ── the RETURN shape: the division nested inside a returned expression ── */

func TestRelationalAccumulation_ADivisionNestedInAReturnedExpressionLowersTheSameProgram(t *testing.T) {
	p := entryEnvTestProgram(t, `
function audioLevel(clamped: number[]): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	return Math.sqrt(total / clamped.length);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "audioLevel")
	accumulation, ok := RelationalAccumulationOf(
		relationalAccumulationContext(p), relationalAccumulationEnv("total", "clamped"), statements, 1)
	if !ok {
		t.Fatalf("a division nested inside a returned expression declined to lower")
	}
	// the return binds NO name, so the quotient rides the node instead
	if accumulation.MeanName != "" {
		t.Errorf("MeanName = %q, want \"\" — a return binds nothing", accumulation.MeanName)
	}
	if accumulation.DivisionNode == nil {
		t.Fatalf("DivisionNode is nil — the return shape pins its quotient on the division node")
	}
	if !ast.IsBinaryExpression(accumulation.DivisionNode) {
		t.Errorf("DivisionNode is %v, want the binary division expression",
			accumulation.DivisionNode.Kind)
	}
	// the lowered program is the SAME two statements the declaration shape
	// builds — the shapes differ only in how the quotient reaches the walk
	wantAccum := `{"loopAccum":{"total":0,"src":1,"len":2,"body":{"op":"binary64.mul","A":{"var":1},"B":{"var":1}}}}`
	if got := kernelbridge.StmtWire(accumulation.Stmts[0]); got != wantAccum {
		t.Errorf("StmtWire(loopAccum) =\n  %s\nwant\n  %s", got, wantAccum)
	}
	wantDiv := `{"assign":{"target":3,"e":{"op":"binary64.div","A":{"var":0},"B":{"var":2}}}}`
	if got := kernelbridge.StmtWire(accumulation.Stmts[1]); got != wantDiv {
		t.Errorf("StmtWire(division) =\n  %s\nwant\n  %s", got, wantDiv)
	}
}

func TestRelationalAccumulation_AReturnWithoutTheDivisionDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(clamped: number[]): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	return total;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// zero occurrences: there is nothing to fold, and the accumulation
	// alone is what the ordinary per-statement route already answers
	if _, ok := RelationalAccumulationOf(
		relationalAccumulationContext(p), relationalAccumulationEnv("total", "clamped"), statements, 1); ok {
		t.Errorf("a return with no division lowered; it must decline")
	}
}

func TestRelationalAccumulation_AReturnWithTwoDivisionsDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(clamped: number[]): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	return (total / clamped.length) + (total / clamped.length);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// one published quotient cannot stand for two nodes — both would read
	// the same value, so the honest move is to fold neither
	if _, ok := RelationalAccumulationOf(
		relationalAccumulationContext(p), relationalAccumulationEnv("total", "clamped"), statements, 1); ok {
		t.Errorf("a return holding two divisions lowered; it must decline")
	}
}

func TestRelationalAccumulation_ADivisionInsideANestedFunctionBodyDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(clamped: number[]): () => number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	return () => total / clamped.length;
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// an arrow body is a separate scope running an unstated number of
	// times, so a division inside it can never be shown to evaluate
	// exactly once here
	if _, ok := RelationalAccumulationOf(
		relationalAccumulationContext(p), relationalAccumulationEnv("total", "clamped"), statements, 1); ok {
		t.Errorf("a division inside a nested function body lowered; it must decline")
	}
}

func TestRelationalAccumulation_AReturnWritingTheAccumulatorDeclines(t *testing.T) {
	p := entryEnvTestProgram(t, `
function f(clamped: number[]): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	return Math.sqrt((total = total + 1) / clamped.length);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	// the write gate covers the WHOLE returned expression: a participant
	// that moves inside it makes the program's own entry states stale
	if _, ok := RelationalAccumulationOf(
		relationalAccumulationContext(p), relationalAccumulationEnv("total", "clamped"), statements, 1); ok {
		t.Errorf("a return writing the accumulator lowered; it must decline")
	}
}

func TestRelationalAccumulation_TheNodeOverrideIsRestoredAfterTheStatementItScopes(t *testing.T) {
	p := entryEnvTestProgram(t, `
function audioLevel(clamped: number[]): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	return Math.sqrt(total / clamped.length);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "audioLevel")
	ctx := relationalAccumulationContext(p)
	env := relationalAccumulationEnv("total", "clamped")
	accumulation, ok := RelationalAccumulationOf(ctx, env, statements, 1)
	if !ok {
		t.Fatalf("the return shape declined to lower")
	}
	// the seam scopes the override by walking under a value COPY of the
	// context (analyze_statement.go's `pinning := *running`), so the
	// context the rest of the list walks under is never mutated — this
	// pins that discipline rather than the kernel's answer, which is why
	// it runs with no dylib
	if ctx.NodeOverrides != nil {
		t.Fatalf("NodeOverrides is set before anything scoped it: %+v", ctx.NodeOverrides)
	}
	answer := &RelationalAccumulationAnswer{
		Quotient: kernelbridge.KnownStateWire{
			Set: refinementsets.MakeRefinedSet(
				refinementsets.AtLeast(0), refinementsets.AtMost(1)),
		},
		Grade: abstractdomain.TrustProved,
	}
	override, pinned := RelationalQuotientOverride(ctx, env, accumulation, answer)
	if !pinned {
		t.Fatalf("the return shape pinned nothing")
	}
	if len(override) != 1 {
		t.Errorf("len(override) = %d, want 1 — the one division node", len(override))
	}
	if _, holds := override[accumulation.DivisionNode]; !holds {
		t.Errorf("the override does not key on the division node")
	}
	// building the override must not touch the caller's own context: the
	// seam sets the field on its own copy, never on this one
	if ctx.NodeOverrides != nil {
		t.Errorf("RelationalQuotientOverride mutated the caller's context: %+v", ctx.NodeOverrides)
	}
	pinning := *ctx
	pinning.NodeOverrides = override
	if ctx.NodeOverrides != nil {
		t.Errorf("scoping the override through a context copy leaked into the original: %+v",
			ctx.NodeOverrides)
	}
	if pinning.NodeOverrides == nil {
		t.Errorf("the scoped copy does not carry the override")
	}
}

func TestRelationalAccumulation_ARefusedQuotientPinsNothing(t *testing.T) {
	p := entryEnvTestProgram(t, `
function audioLevel(clamped: number[]): number {
	let total = 0;
	for (const s of clamped) { total += s * s; }
	return Math.sqrt(total / clamped.length);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "audioLevel")
	ctx := relationalAccumulationContext(p)
	env := relationalAccumulationEnv("total", "clamped")
	accumulation, ok := RelationalAccumulationOf(ctx, env, statements, 1)
	if !ok {
		t.Fatalf("the return shape declined to lower")
	}
	// a quotient the kernel refused to bound pins nothing at all, so the
	// division walks exactly as it would have — never weaker than today
	refused := &RelationalAccumulationAnswer{
		Quotient: kernelbridge.KnownStateWire{Top: true},
		Grade:    abstractdomain.TrustProved,
	}
	if _, pinned := RelationalQuotientOverride(ctx, env, accumulation, refused); pinned {
		t.Errorf("a top quotient pinned an override; it must pin nothing")
	}
}

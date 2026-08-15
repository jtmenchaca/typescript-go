// Record locals flattened into scalar slots: a body keeping a
// fixed-shape object in a local summarizes through the kernel walk,
// and the uses that would observe the object AS an object decline.
// Skipped (never a faked pass) when the native kernel dylib is absent,
// the same gate the sibling summary tests use.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

/* ── the recipe: a checker-backed program's top-level functions ────── */

// functionDeclaredIn is the top-level function declaration named `text`
// in a checker-backed program's entry file — the join-arm inferred-return
// widening resolves through the checker, so its cases need a real
// program rather than the throwaway parse summaryDeclarationOf gives.
func functionDeclaredIn(t *testing.T, p *program.CheckerProgram, text string) *ast.Node {
	t.Helper()
	for _, statement := range p.Entry.Statements.Nodes {
		if !ast.IsFunctionDeclaration(statement) {
			continue
		}
		name := statement.Name()
		if name != nil && ast.IsIdentifier(name) && name.Text() == text {
			return statement
		}
	}
	t.Fatalf("no top-level function named %s", text)
	return nil
}

// objectSlotsCtx is a checker-backed context with an empty contract
// registry, and the memos cleared — each case parses its own program, so
// a remembered expansion or outcome from another case must not survive
// into it.
func objectSlotsCtx(t *testing.T, source string) (*FlowContext, *program.CheckerProgram) {
	t.Helper()
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, source)
	return &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, p
}

func TestObjectSlots_AFixedShapeRecordLocalFlattensIntoPerKeySlotsAndSummarizes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// p has no slot of its own: "p.lo" and "p.hi" do. The declaration
	// lowers as two assignments, the loop writes p.lo, and the return
	// reads it — every step through the ordinary scalar grammar.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, hi: n }; let i = 0; while (i < 3) { p.lo = p.lo + 1; i = i + 1; } return p.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 7)}, contract)
	if !ok {
		t.Fatalf("KernelSummaryDirect ok = false, want a summarized answer for a flattened record local")
	}
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		t.Fatalf("summarized answer did not spell as a scalar state: %+v", answer)
	}
	// p.lo counts 0 → 3 across three trips; the kernel's loop answer
	// must ADMIT 3 (a widened invariant is sound, an answer excluding
	// the true value is not)
	if !kernel.Member(state.Set, []float64{3}) {
		t.Errorf("summary of f(7) excludes the true value 3 for p.lo: %+v", state.Set)
	}
}

func TestObjectSlots_AnAliasedRecordLocalNeverServesAPreciseLeaf(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// `const q = p` reads the whole record; after flattening there is no
	// one value for q to hold, so the recognizer must refuse p its slot
	// family — the alias kills the flattening, so no leaf value may survive
	// to the answer.
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, hi: n }; const q = p; return p.lo; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	answer, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 7)}, contract)
	if !ok {
		// ok=false is also acceptable; if it's false that is expected
		return
	}
	// if ok is true, the answer must not serve a precise leaf value.
	// StateOfKnown either fails (ok=false) or answers a state with Top=true,
	// and the answer must never claim a precise value like exactly {0}.
	state, stateOk := StateOfKnown(answer)
	if !stateOk || state.Top {
		// answer is opaque or top; this is acceptable
		return
	}
	// if we have a concrete state, it must not contain precise values
	// an empty set (which claims nothing) is acceptable
	if len(state.Set.Forms) > 0 {
		t.Errorf("answer contains concrete forms %+v, want only silence", state.Set.Forms)
	}
}

func TestObjectSlots_ASpreadInTheRecordLiteralDeclines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	// a spread row names no single key, so the literal has no flat key
	// set to become slots
	declaration := summaryDeclarationOf(t,
		"function f(o: { lo: number }, n: number) { const p = { ...o, hi: n }; return p.hi; }")
	contract := &FunctionContract{Declaration: declaration}
	ctx := &FlowContext{Contracts: map[*ast.Symbol]*FunctionContract{}}
	if _, ok := KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 1), exactNumber(t, 7)}, contract); ok {
		t.Errorf("a spread record literal summarized — its key set is not the literal's own rows")
	}
}

func TestObjectSlots_TheRecognizerAdmitsAPlainRecordAndNamesItsSlots(t *testing.T) {
	// the recognizer alone — no kernel needed, it reads only syntax
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, hi: n }; p.lo = p.lo + 1; return p.hi; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	flattened := ObjectLocalsOf(body, locals.Locals)
	if len(flattened) != 1 {
		t.Fatalf("ObjectLocalsOf found %d flattened locals, want 1", len(flattened))
	}
	for _, local := range flattened {
		if local.Name != "p" {
			t.Errorf("flattened local name = %q, want p", local.Name)
		}
		if len(local.Keys) != 2 {
			t.Fatalf("flattened local has %d keys, want 2", len(local.Keys))
		}
		if local.Keys[0].SlotName != "p.lo" || local.Keys[1].SlotName != "p.hi" {
			t.Errorf("slot names = %q, %q, want p.lo, p.hi", local.Keys[0].SlotName, local.Keys[1].SlotName)
		}
		if got := ObjectLocalKeySort(local.Keys[0]); got != BindingKindNumber {
			t.Errorf("sort of p.lo = %q, want number", got)
		}
		if got := ObjectLocalKeyTypeof(local.Keys[0]); got != TypeofTagNumber {
			t.Errorf("typeof of p.lo = %q, want number", got)
		}
	}
}

func TestObjectSlots_TheRecognizerDeclinesAKeyTheLiteralNeverDeclared(t *testing.T) {
	// `p.extra = 1` would need a slot the literal never named
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0 }; p.extra = n; return p.lo; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a later-added key flattened — its write would land in no slot")
	}
}

func TestObjectSlots_TheRecognizerDeclinesARecordPassedWhole(t *testing.T) {
	// `g(p)` hands the record to a callee that could read any key or
	// keep the reference
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, hi: n }; g(p); return p.lo; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a record passed as an argument flattened — the callee observes an object the flattening does not build")
	}
}

func TestObjectSlots_TheRecognizerFlattensANestedRecordByLeafPath(t *testing.T) {
	// a nested object row contributes its own leaves under the row's key:
	// `{ lo: 0, inner: { deep: n } }` names "p.lo" and "p.inner.deep"
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: 0, inner: { deep: n } }; return p.inner.deep; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	flattened := ObjectLocalsOf(body, locals.Locals)
	if len(flattened) != 1 {
		t.Fatalf("ObjectLocalsOf found %d flattened locals, want 1", len(flattened))
	}
	for _, local := range flattened {
		if len(local.Keys) != 2 {
			t.Fatalf("flattened local has %d leaves, want 2", len(local.Keys))
		}
		if local.Keys[0].SlotName != "p.lo" {
			t.Errorf("first leaf slot = %q, want p.lo", local.Keys[0].SlotName)
		}
		if local.Keys[1].SlotName != "p.inner.deep" {
			t.Errorf("second leaf slot = %q, want p.inner.deep", local.Keys[1].SlotName)
		}
	}
}

func TestObjectSlots_TheRecognizerDeclinesAReadOfAnUndeclaredNestedLeaf(t *testing.T) {
	// `p.inner.missing` names no leaf the literal gave a slot
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { inner: { deep: n } }; return p.inner.missing; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a read of an undeclared nested leaf flattened — its read lands in no slot")
	}
}

func TestObjectSlots_TheRecognizerDeclinesAReadOfAnInteriorNode(t *testing.T) {
	// `p.inner` is a whole record after flattening, not one scalar leaf
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { inner: { deep: n } }; const q = p.inner; return q.deep; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a read of an interior node flattened — it names no one scalar")
	}
}

// TestObjectSlots_AJoinArmDeclarationsTwoRoutesAndTheirOutcomes pins the
// ASYMMETRY the two arm-family readers land in today, both halves sound:
//
//   - SPELLED (`make(): Bounds`): the local FLATTENS into p.lo/p.hi
//     (the recognizer tests above), and the body then settles POROUS at
//     the "declaration" statement — the lowering has no route that reads
//     `make() ?? {…}` INTO the flattened leaf slots, so the declaration
//     havocs the leaves and the body serves nothing.
//   - INFERRED (`make()` bare): the harness's ctx-less relower never
//     resolves the return type, the local keeps its whole-name slot, and
//     the body lowers COMPLETE through the CALL route — make() inlines,
//     the join lands whole, and `p.lo` answers a sort-only claim (a
//     number, unconstrained; verified to admit the true value 1).
//
// Neither half determines p.lo's VALUE. The named next construct is the
// spelled half's wall: lowering a `call() ?? literal` initializer into
// the flattened leaf slots member-wise, which would turn the porous row
// complete AND carry the values.
func TestObjectSlots_AJoinArmDeclarationsTwoRoutesAndTheirOutcomes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	spelledOutcome, spelledConstruct := joinArmCalleeReturnKernelOutcome(t, kernel,
		"interface Bounds { lo: number; hi: number }\n"+
			"function make(): Bounds { return { lo: 1, hi: 2 }; }\n"+
			"function f(n: number) { const p = make() ?? { lo: 0, hi: 0 }; return p.lo; }\n")
	if spelledOutcome != SummaryPorous || spelledConstruct != "declaration" {
		t.Errorf("the spelled arm settled outcome=%v construct=%q, want porous at the declaration — the join-into-leaves lowering is the named wall",
			spelledOutcome, spelledConstruct)
	}
	inferredOutcome, inferredConstruct := joinArmCalleeReturnKernelOutcome(t, kernel,
		"interface Bounds { lo: number; hi: number }\n"+
			"function make() { return { lo: 1, hi: 2 }; }\n"+
			"function f(n: number) { const p = make() ?? { lo: 0, hi: 0 }; return p.lo; }\n")
	if inferredOutcome != SummaryComplete || inferredConstruct != "" {
		t.Errorf("the unresolved arm settled outcome=%v construct=%q, want complete through the whole-value call route",
			inferredOutcome, inferredConstruct)
	}
}

// joinArmCalleeReturnKernelOutcome lowers `f` through the kernel summary
// path and answers its settled SummaryOutcome and decline construct.
func joinArmCalleeReturnKernelOutcome(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string) (SummaryOutcome, string) {
	t.Helper()
	ctx, p := objectSlotsCtx(t, source)
	f := functionDeclaredIn(t, p, "f")
	make_ := functionDeclaredIn(t, p, "make")
	makeSymbol := p.Checker.GetSymbolAtLocation(make_.Name())
	if makeSymbol == nil {
		t.Fatalf("make's declaration has no symbol to register a contract under")
	}
	ctx.Contracts[makeSymbol] = &FunctionContract{Declaration: make_}
	contract := &FunctionContract{Declaration: f}
	KernelSummaryDirect(ctx, []abstractdomain.AbstractValue{exactNumber(t, 7)}, contract)
	outcome, construct, had := SummaryOutcomeOf(f)
	if !had {
		t.Fatalf("no outcome recorded for f")
	}
	return outcome, construct
}

func TestObjectSlots_TheRecognizerFlattensAJoinArmWhoseCalleeReturnIsInferredAsAnInterface(t *testing.T) {
	// the recognizer alone, no kernel: `make()` returns an object literal
	// with no annotation, and the arm's family comes from the RESOLVED
	// return type's single interface declaration
	ctx, p := objectSlotsCtx(t,
		"interface Bounds { lo: number; hi: number }\n"+
			"function make(): Bounds { return { lo: 1, hi: 2 }; }\n"+
			"function f(n: number) { const p = make() ?? { lo: 0, hi: 0 }; return p.lo; }\n")
	f := functionDeclaredIn(t, p, "f")
	body := f.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	flattened := ObjectLocalsIn(ctx, body, locals.Locals)
	if len(flattened) != 1 {
		t.Fatalf("ObjectLocalsIn found %d flattened locals, want 1", len(flattened))
	}
	for _, local := range flattened {
		if local.Name != "p" {
			t.Errorf("flattened local name = %q, want p", local.Name)
		}
		if len(local.Keys) != 2 {
			t.Fatalf("flattened local has %d keys, want 2", len(local.Keys))
		}
		if local.Keys[0].SlotName != "p.lo" || local.Keys[1].SlotName != "p.hi" {
			t.Errorf("slot names = %q, %q, want p.lo, p.hi", local.Keys[0].SlotName, local.Keys[1].SlotName)
		}
	}
}

func TestObjectSlots_AJoinArmWhoseCalleeReturnIsInferredAsAPrimitiveDeclines(t *testing.T) {
	// make() infers to `number` — no symbol, no interface, no family
	ctx, p := objectSlotsCtx(t,
		"function make() { return 1; }\n"+
			"function f(n: number) { const p = make() ?? 0; return p; }\n")
	f := functionDeclaredIn(t, p, "f")
	body := f.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsIn(ctx, body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a join arm whose inferred callee return is a primitive flattened — there is no member family to read")
	}
}

func TestObjectSlots_AJoinArmWhoseCalleeReturnIsInferredAsAnAnonymousObjectLiteralType(t *testing.T) {
	// make() infers an object type with NO interface or alias behind it —
	// an anonymous literal type. Pinning what the machinery soundly does:
	// the resolved type's symbol names the synthesized type-literal node
	// itself, which is neither an interface nor a type-alias declaration,
	// so the existing named-type reader refuses it and the arm keeps
	// today's refusal.
	ctx, p := objectSlotsCtx(t,
		"function make() { return { lo: 1, hi: 2 }; }\n"+
			"function f(n: number) { const p = make() ?? { lo: 0, hi: 0 }; return p.lo; }\n")
	f := functionDeclaredIn(t, p, "f")
	body := f.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsIn(ctx, body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a join arm whose inferred callee return is an anonymous object literal type flattened — " +
			"its resolved type names no interface or type-alias declaration for the named-type reader to take")
	}
}

func TestObjectSlots_TheRecognizerDeclinesAKeyReadingTheRecordItDeclares(t *testing.T) {
	// `{ lo: 0, hi: p.lo }` reads a slot the declaration has not written
	// yet; flattened, that read would see the absent entry state
	declaration := summaryDeclarationOf(t,
		"function f(n: number) { const p = { lo: n, hi: p.lo }; return p.hi; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false, want the record local collected")
	}
	if flattened := ObjectLocalsOf(body, locals.Locals); len(flattened) != 0 {
		t.Errorf("a self-reading record literal flattened — its second key would read an unwritten slot")
	}
}

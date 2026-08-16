// An object literal's METHOD writing its own record through `this`,
// landed at the call site: the flattening admits the method rows, the
// method's summary lays the receiver out as a `this` bundle read off the
// literal, and a called write either rides back exactly through rets or
// havocs the stale leaf — never leaves it standing. The kernel-gated
// case is skipped (never a faked pass) when the native dylib is absent,
// the same gate the sibling summary tests use.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

/* ── recognition: the literal's rows ─────────────────────────────── */

// literalOfFirstDeclaration is the object literal initializing the first
// statement's single declarator.
func literalOfFirstDeclaration(t *testing.T, statements []*ast.Node) *ast.Node {
	t.Helper()
	if len(statements) == 0 || !ast.IsVariableStatement(statements[0]) {
		t.Fatalf("parse did not yield a variable statement first")
	}
	declarations := statements[0].AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	literal := objectLiteralOfDeclaration(declarations[0])
	if literal == nil {
		t.Fatalf("the declarator holds no object literal")
	}
	return literal
}

// methodRowNamed is the literal's method row of that name.
func methodRowNamed(t *testing.T, literal *ast.Node, text string) *ast.Node {
	t.Helper()
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		if ast.IsMethodDeclaration(property) && property.Name() != nil &&
			ast.IsIdentifier(property.Name()) && property.Name().Text() == text {
			return property
		}
	}
	t.Fatalf("no method row named %s", text)
	return nil
}

func TestMethodThisWrites_FlatteningSkipsAServableMethodRowAndKeepsTheLeaves(t *testing.T) {
	literal := literalOfFirstDeclaration(t, loweringParse(t,
		`const person = { age: 40, bump(): void { this.age = this.age + 1; } };`))
	keys, ok := flatKeysOfLiteral(literal, "person", nil)
	if !ok {
		t.Fatalf("a literal with a servable method row declined — the row should be skipped, not refused")
	}
	if len(keys) != 1 || keys[0].SlotName != "person.age" {
		t.Errorf("keys = %+v, want exactly the one leaf person.age", keys)
	}
	methods := literalMethodPaths(literal, nil)
	if _, held := methods["bump"]; !held || len(methods) != 1 {
		t.Errorf("literalMethodPaths = %v, want exactly bump", methods)
	}
}

func TestMethodThisWrites_AGeneratorOrAsyncMethodRowDeclinesTheLiteral(t *testing.T) {
	for _, source := range []string{
		`const person = { age: 40, *bump(): Generator<number> { yield 1; } };`,
		`const person = { age: 40, async bump(): Promise<void> { this.age = 200; } };`,
	} {
		literal := literalOfFirstDeclaration(t, loweringParse(t, source))
		if _, ok := flatKeysOfLiteral(literal, "person", nil); ok {
			t.Errorf("flattened despite an unservable method row in %q — a generator resumes and an async body writes after the call returned", source)
		}
	}
}

func TestMethodThisWrites_AMethodMentioningTheRecordsOwnNameDeclinesTheLiteral(t *testing.T) {
	// `person.age = 2` inside the method writes through the OUTER name —
	// a spelling no summary entry carries, so the literal must not flatten
	literal := literalOfFirstDeclaration(t, loweringParse(t,
		`const person = { age: 40, bump(): void { person.age = 2; } };`))
	if _, ok := flatKeysOfLiteral(literal, "person", nil); ok {
		t.Errorf("flattened although a method writes the record through its own name")
	}
}

func TestMethodThisWrites_TheRecognizerAdmitsCallsThroughTheRecordAndRefusesTheBareHandOut(t *testing.T) {
	// the CALLED method is an admitted use
	declaration := summaryDeclarationOf(t,
		"function f(): number { const person = { age: 40, bump(): void { this.age = this.age + 1; } }; person.bump(); return person.age; }")
	body := declaration.Body()
	locals, ok := CollectLocals(body)
	if !ok {
		t.Fatalf("CollectLocals ok = false")
	}
	flattened := ObjectLocalsOf(body, locals.Locals)
	if len(flattened) != 1 {
		t.Fatalf("ObjectLocalsOf admitted %d locals, want 1 — the method call through the record is a spelled use", len(flattened))
	}
	for _, local := range flattened {
		if _, held := local.Methods["bump"]; !held {
			t.Errorf("the admitted local carries no bump method: %v", local.Methods)
		}
	}
	// the method handed out BARE is not: an unbound call would run with
	// the wrong `this`
	handedOut := summaryDeclarationOf(t,
		"function f(): number { const person = { age: 40, bump(): void { this.age = this.age + 1; } }; const g = person.bump; g(); return person.age; }")
	handedBody := handedOut.Body()
	handedLocals, handedOk := CollectLocals(handedBody)
	if handedOk {
		if flattened := ObjectLocalsOf(handedBody, handedLocals.Locals); len(flattened) != 0 {
			t.Errorf("the bare hand-out flattened anyway: %d locals", len(flattened))
		}
	}
	// a method-bearing record never assigns whole — the rebound record
	// would run other bodies under the same spellings
	reassigned := summaryDeclarationOf(t,
		"function f(): number { let p = { age: 1, m(): void { this.age = 2; } }; p = { age: 3, m(): void { this.age = 4; } }; p.m(); return p.age; }")
	reassignedBody := reassigned.Body()
	reassignedLocals, reassignedOk := CollectLocals(reassignedBody)
	if reassignedOk {
		if flattened := ObjectLocalsOf(reassignedBody, reassignedLocals.Locals); len(flattened) != 0 {
			t.Errorf("a whole-record assignment of a method-bearing record flattened: %d locals", len(flattened))
		}
	}
}

/* ── the method's `this` bundle ──────────────────────────────────── */

func TestMethodThisWrites_TheLiteralBundleLaysOutAWriteOnlyFieldWithItsRow(t *testing.T) {
	// `spoil` WRITES age without reading it. The class arm lays out read
	// fields only; the literal arm must lay the written field out too —
	// with no entry the write has no row and no ret, and the caller's
	// leaf keeps its stale 40, which is the unsoundness this closes.
	statements := loweringParse(t,
		`const outlaw = { age: 40, spoil(): void { this.age = 200; } };`)
	spoil := methodRowNamed(t, literalOfFirstDeclaration(t, statements), "spoil")
	bundle := thisBundleOf(nil, spoil)
	if !bundle.Expanded || bundle.Escaped {
		t.Fatalf("the write-only method's bundle did not expand: %+v", bundle)
	}
	if len(bundle.Entries) != 1 || bundle.Entries[0].Name != "this.age" {
		t.Fatalf("entries = %+v, want exactly this.age", bundle.Entries)
	}
	if _, written := bundle.Written["this.age"]; !written {
		t.Errorf("this.age is not marked Written although the body assigns it")
	}
}

func TestMethodThisWrites_AReadAndWriteFieldCarriesOneEntryMarkedWritten(t *testing.T) {
	statements := loweringParse(t,
		`const person = { age: 40, bump(): void { this.age = this.age + 1; } };`)
	bump := methodRowNamed(t, literalOfFirstDeclaration(t, statements), "bump")
	bundle := thisBundleOf(nil, bump)
	if !bundle.Expanded {
		t.Fatalf("bump's bundle did not expand: %+v", bundle)
	}
	if len(bundle.Entries) != 1 || bundle.Entries[0].Name != "this.age" {
		t.Fatalf("entries = %+v, want the one this.age entry", bundle.Entries)
	}
	if bundle.Entries[0].Sort != BindingKindNumber {
		t.Errorf("this.age entry sort = %v, want the number sort the leaf's own initializer reads", bundle.Entries[0].Sort)
	}
	if _, written := bundle.Written["this.age"]; !written {
		t.Errorf("this.age is not marked Written")
	}
}

func TestMethodThisWrites_AnEscapingReceiverAnswersEscapedNeverAnEmptyBundle(t *testing.T) {
	// `f(this)` hands the record out — the bundle may be written through
	// a name the census never saw, and an empty bundle would let the
	// summary serve with the write invisible
	statements := loweringParse(t,
		`const person = { age: 40, leak(): void { register(this); this.age = 2; } };`)
	leak := methodRowNamed(t, literalOfFirstDeclaration(t, statements), "leak")
	bundle := thisBundleOf(nil, leak)
	if !bundle.Escaped {
		t.Errorf("an escaping receiver did not answer Escaped: %+v", bundle)
	}
}

/* ── the capture-write census at the call site ───────────────────── */

func TestMethodThisWrites_CaptureWriteSlotsReadTheMethodNodeAndExcludeThisSpellings(t *testing.T) {
	statements := loweringParse(t,
		`const person = { age: 1, tick(): void { counter = counter + 1; this.age = 2; } };`)
	tick := methodRowNamed(t, literalOfFirstDeclaration(t, statements), "tick")
	// the caller happens to hold a "this.age" slot of its OWN (a class
	// method lowering this call): the record's this-write must not havoc
	// it — inside the method `this` is the record, never the caller's
	// receiver
	context := &LoweringContext{
		Bindings: []string{"counter", "this.age"},
		Sorts:    []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:  []TypeofTag{TypeofTagNumber, TypeofTagNumber},
	}
	slots := methodCaptureWriteSlots(context, tick)
	if _, held := slots[0]; !held || len(slots) != 1 {
		t.Errorf("methodCaptureWriteSlots = %v, want exactly counter's slot 0 — the this spelling excluded", slots)
	}
	// the census takes the METHOD NODE, never its .Body(): a bare block
	// holds no closures, so the body's own top-level writes go unseen —
	// the pinned closure-census lesson, restated for methods
	if slots := methodCaptureWriteSlots(context, tick.Body()); len(slots) != 0 {
		t.Errorf("methodCaptureWriteSlots(body) = %v — the empty set documents why the argument is the method node", slots)
	}
}

func TestMethodThisWrites_TheCallSiteServesWithCaptureHavocAndRefusesAFlattenedMention(t *testing.T) {
	// SERVABLE: tick writes a captured scalar beside its this-write. The
	// site may serve the summary, and the capture write rides ahead as
	// havoc so nothing believes `counter` across the call.
	statements := loweringParse(t, `
		const person = { age: 1, tick(): void { counter = counter + 1; this.age = 2; } };
		person.tick();
	`)
	tick := methodRowNamed(t, literalOfFirstDeclaration(t, statements), "tick")
	if len(statements) < 2 || !ast.IsExpressionStatement(statements[1]) {
		t.Fatalf("parse did not yield the call statement")
	}
	call := Unwrapped(statements[1].AsExpressionStatement().Expression)
	context := &LoweringContext{
		Bindings:      []string{"counter", "person.age"},
		Sorts:         []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:       []TypeofTag{TypeofTagNumber, TypeofTagNumber},
		ResolveCallee: func(callee *ast.Node) *ast.Node { return tick },
	}
	havoc, servable := LiteralMethodWriteStatements(context, call)
	if !servable {
		t.Fatalf("a servable method refused: %+v", havoc)
	}
	counterHavocked := false
	for _, statement := range havoc {
		if statement.Kind == kernelbridge.IrStatementAssign && statement.Target == 0 &&
			statement.Effect.Kind == kernelbridge.LoopEffectUnknown {
			counterHavocked = true
		}
	}
	if !counterHavocked {
		t.Errorf("the captured counter write is not havocked ahead of the served call: %+v", havoc)
	}

	// REFUSED: raid mentions a flattened SIBLING record — a route the
	// write census does not spell, so the summary must not serve, and the
	// refusal's havoc must cover the sibling's leaf.
	raidStatements := loweringParse(t, `
		const person = { age: 1, raid(): void { sibling.age = 0; } };
		person.raid();
	`)
	raid := methodRowNamed(t, literalOfFirstDeclaration(t, raidStatements), "raid")
	if len(raidStatements) < 2 || !ast.IsExpressionStatement(raidStatements[1]) {
		t.Fatalf("parse did not yield the raid call statement")
	}
	raidCall := Unwrapped(raidStatements[1].AsExpressionStatement().Expression)
	raidContext := &LoweringContext{
		Bindings:      []string{"person.age", "sibling.age"},
		Sorts:         []BindingKind{BindingKindNumber, BindingKindNumber},
		Typeofs:       []TypeofTag{TypeofTagNumber, TypeofTagNumber},
		ResolveCallee: func(callee *ast.Node) *ast.Node { return raid },
	}
	raidHavoc, raidServable := LiteralMethodWriteStatements(raidContext, raidCall)
	if raidServable {
		t.Fatalf("a flattened-mention body served — the census cannot spell what the mention moves")
	}
	siblingHavocked := false
	for _, statement := range raidHavoc {
		if statement.Kind == kernelbridge.IrStatementAssign && statement.Target == 1 &&
			statement.Effect.Kind == kernelbridge.LoopEffectUnknown {
			siblingHavocked = true
		}
	}
	if !siblingHavocked {
		t.Errorf("the refusal's havoc leaves sibling.age standing: %+v", raidHavoc)
	}
}

/* ── end to end, through the door ────────────────────────────────── */

// objectLiteralMethodIn is the method row named `text` in the first
// object literal of the named top-level function's body.
func objectLiteralMethodIn(t *testing.T, fn *ast.Node, text string) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsMethodDeclaration(node) && node.Parent != nil && ast.IsObjectLiteralExpression(node.Parent) {
			if name := node.Name(); name != nil && ast.IsIdentifier(name) && name.Text() == text {
				found = node
				return true
			}
		}
		node.ForEachChild(visit)
		return false
	}
	visit(fn.Body())
	if found == nil {
		t.Fatalf("no object-literal method named %s", text)
	}
	return found
}

func TestMethodThisWrites_ACalledMethodsWriteNeverLeavesTheStaleLeafStanding(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t,
		"function caller(): number {\n"+
			"  const outlaw = { age: 40, spoil(): void { this.age = 200; } };\n"+
			"  outlaw.spoil();\n"+
			"  return outlaw.age;\n"+
			"}\n")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	caller := functionDeclaredIn(t, p, "caller")
	spoil := objectLiteralMethodIn(t, caller, "spoil")
	spoilSymbol := p.Checker.GetSymbolAtLocation(spoil.Name())
	if spoilSymbol == nil {
		t.Fatalf("spoil's declaration has no symbol to register a contract under")
	}
	ctx.Contracts[spoilSymbol] = &FunctionContract{Declaration: spoil}
	call := callInFunctionBody(t, caller)
	context := &LoweringContext{
		Bindings:      []string{"outlaw.age", "#done", "#ret"},
		Sorts:         []BindingKind{BindingKindNumber, BindingKindNumber, BindingKindUnknown},
		Typeofs:       []TypeofTag{TypeofTagNumber, TypeofTagNumber, TypeofTagNone},
		Narrow:        kernel.Narrow,
		Result:        &LoweringResult{Done: 1, Ret: 2},
		Flow:          ctx,
		SummaryTable:  &SummaryTableBuilder{},
		ResolveCallee: func(callee *ast.Node) *ast.Node { return spoil },
	}
	lowered, ok := SummaryCallOrHavoc(context, call, -1)
	if !ok {
		t.Fatalf("the call site declined whole")
	}
	// two sound answers, and only two: the SERVED statement whose written
	// this.age entry maps back onto the caller's slot 0 (the exact 200
	// lands), or a HAVOC of slot 0 (the stale 40 dies). The unsound third
	// — the call lowering while slot 0 keeps its stale value — is the miss
	// this treatment closes.
	for _, statement := range lowered {
		if statement.Kind != kernelbridge.IrStatementCall {
			continue
		}
		for _, ret := range statement.Rets {
			if ret == 0 {
				return // the write rides back exactly
			}
		}
		t.Fatalf("the served call maps no exit onto the caller's outlaw.age slot: rets = %v", statement.Rets)
	}
	for _, statement := range lowered {
		if statement.Kind == kernelbridge.IrStatementAssign && statement.Target == 0 &&
			statement.Effect.Kind == kernelbridge.LoopEffectUnknown {
			return // the stale leaf dies
		}
	}
	t.Errorf("the call left outlaw.age's stale 40 standing: %+v", lowered)
}

// callInFunctionBody is the first call expression in a function's body.
func callInFunctionBody(t *testing.T, fn *ast.Node) *ast.Node {
	t.Helper()
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil || node == nil {
			return true
		}
		if ast.IsCallExpression(node) {
			found = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(fn.Body())
	if found == nil {
		t.Fatalf("no call expression in the body")
	}
	return found
}

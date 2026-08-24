// The template unit for the fleet's RTS7002-provenance sweep
// (AGENT-BRIEF.md): one converted call site (coercion_models.go's
// readJsonMethods, the JSON.parse-of-unknown-text branch) and the
// consumer that reads it (check_assignability.go's KindUnknown
// branch). Two rows: the converted site's own sentence must reach the
// diagnostic, and an UNCONVERTED site (untracked_identifier.go, still
// bare silence.Residue()) must keep reporting assignability.AlertText
// — the fallback that makes every later conversion in the fleet's
// sweep measurable as a message change, not a silent no-op.
package walk

import (
	"strings"
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

// residueReasonAgeWindow mirrors compoundAssignAgeWindow's stand-in
// convention: a plain refinement set standing in for a real annotation
// (z.number().int().min(0).max(120)), the exact shape CheckAssignability
// reads either way.
func residueReasonAgeWindow() *annotations.DeclaredRefinement {
	set := refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(120), refinementsets.Integer)
	return &annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &set}
}

// TestCheckAssignability_JSONParseOfUnknownTextNamesItsOwnReaderNotTheBareAlert
// pins the converted site: `JSON.parse(text)` with text an unread
// parameter reaches readJsonMethods' unknown-text branch, which now
// returns silence.ResidueOf(the same sentence its NoteReason call
// already writes) instead of bare silence.Residue(). The RTS7002
// diagnostic at the write to `age` must carry THAT sentence, naming
// JSON.parse, rather than the generic AlertText.
func TestCheckAssignability_JSONParseOfUnknownTextNamesItsOwnReaderNotTheBareAlert(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(text: string): void {\n"+
		"  let age = JSON.parse(text);\n"+
		"  age;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, nil, &diagnostics, map[string]*annotations.DeclaredRefinement{"age": residueReasonAgeWindow()})
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Fatalf("`let age = JSON.parse(text)` against a declared window raised no diagnostic, want RTS7002")
	}
	got := diagnostics[0].MessageText
	if got == assignability.AlertText {
		t.Errorf("diagnostic MessageText = the bare AlertText, want readJsonMethods' own JSON.parse sentence")
	}
	if !strings.Contains(got, "JSON.parse of unknown text") {
		t.Errorf("diagnostic MessageText = %q, want it to name JSON.parse of unknown text", got)
	}
}

// TestCheckAssignability_AnUnconvertedResidueSiteStillReportsTheBareAlert
// pins the fallback: `age` written directly from an unread parameter
// (untracked_identifier.go's last-reader path — not yet converted to
// ResidueOf) must still report the bare AlertText. This is the
// fleet's own regression gate: every future conversion is measurable
// as this test's counterpart flipping from AlertText to a named
// sentence, never a silent behavior change here.
func TestCheckAssignability_AnUnconvertedResidueSiteStillReportsTheBareAlert(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(text: unknown): void {\n"+
		"  let age = text;\n"+
		"  age;\n"+
		"}\n")
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, nil, &diagnostics, map[string]*annotations.DeclaredRefinement{"age": residueReasonAgeWindow()})
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Fatalf("`let age = text` (unread) against a declared window raised no diagnostic, want RTS7002")
	}
	if diagnostics[0].MessageText != assignability.AlertText {
		t.Errorf("diagnostic MessageText = %q, want the bare AlertText (this site is not yet converted to ResidueOf)", diagnostics[0].MessageText)
	}
}

// residueReasonExpectSentence runs source against a `Declared["age"]`
// window and asserts SOME diagnostic's MessageText carries
// wantSubstring — element_access.go's and collection_models.go's own
// conversions, pinned one per distinct sentence family the fleet brief
// asked for.
//
// Scans every diagnostic rather than assuming diagnostics[0] is the
// `age` write's own: a fixture can raise MORE than one diagnostic
// (builtin_contracts.go's Array(len) row runs its own CheckAssignability
// against the length argument, ahead of the tracked statement, and with
// no kernel seated — compoundAssignContext(p, nil, ...) — that row's own
// membership question panics/recovers into a 7002-turned-7001 finding of
// its own, landing in the sink before the position this test actually
// cares about). Position [0] assumed a single-diagnostic body; that
// assumption is what broke, not the sentence.
func residueReasonExpectSentence(t *testing.T, source string, wantSubstring string) {
	t.Helper()
	p := entryEnvTestProgram(t, source)
	var diagnostics []assignability.RefinementDiagnostic
	ctx := compoundAssignContext(p, nil, &diagnostics, map[string]*annotations.DeclaredRefinement{"age": residueReasonAgeWindow()})
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Fatalf("source raised no diagnostic, want RTS7002:\n%s", source)
	}
	for _, d := range diagnostics {
		if strings.Contains(d.MessageText, wantSubstring) {
			return
		}
	}
	var all []string
	for _, d := range diagnostics {
		all = append(all, d.MessageText)
	}
	t.Errorf("no diagnostic contained %q — got %d diagnostic(s): %q", wantSubstring, len(diagnostics), all)
}

// TestCheckAssignability_ElementAccess_RegexExecMaybeReceiverNamesItsOwnReader
// pins element_access.go's `re.exec(s)?.[1]` arm: the call's result
// rides the maybe wrapper, and a non-match match array read isn't a
// tracked list at this index — but the grade-provenance rules dispatch
// on the WRAPPER's own Kind (KindPossiblyUndefined) before ever
// looking at what the wrapper holds (check_assignability.go's
// `known.Kind == KindUndef || known.Kind == KindPossiblyUndefined`
// gate routes straight to RefutePossiblyAbsent). The declared `age`
// window is a plain number set — it does not admit undefined — so a
// possibly-undefined value at that sink is a REAL RTS7001 refutation
// regardless of what the present side names, stronger than the named
// residue this pin used to assert.
func TestCheckAssignability_ElementAccess_RegexExecMaybeReceiverNamesItsOwnReader(t *testing.T) {
	residueReasonExpectSentence(t, "function f(s: string): void {\n"+
		"  let age = /x/.exec(s)?.[1];\n"+
		"  age;\n"+
		"}\n", "may be 'undefined', which is not assignable to")
}

// TestCheckAssignability_ElementAccess_OptionalChainIdentifierElementNamesItsOwnReader
// pins element_access.go's `o?.[i]` arm on a tracked identifier
// receiver: the maybe wrapper threads through, and the present side
// isn't a tracked list at a known index — but the same wrapper-Kind
// dispatch as the sibling test above fires a real RTS7001 at the
// bounded `age` sink before the present side's own residue is ever
// consulted, stronger than the named residue this pin used to assert.
func TestCheckAssignability_ElementAccess_OptionalChainIdentifierElementNamesItsOwnReader(t *testing.T) {
	residueReasonExpectSentence(t, "function f(o: number[] | undefined, i: number): void {\n"+
		"  let age = o?.[i];\n"+
		"  age;\n"+
		"}\n", "may be 'undefined', which is not assignable to")
}

// TestCheckAssignability_ElementAccess_StableSymbolSlotMissNamesItsOwnReader
// pins element_access.go's stable-symbol-slot arm: a symbol-keyed read
// against an object literal whose own slot was written under a
// different registry symbol names its own reason rather than claiming
// a missing key.
func TestCheckAssignability_ElementAccess_StableSymbolSlotMissNamesItsOwnReader(t *testing.T) {
	residueReasonExpectSentence(t, "function f(): void {\n"+
		"  const k = Symbol.for(\"k\");\n"+
		"  const m = Symbol.for(\"m\");\n"+
		"  const o = { [k]: 1 };\n"+
		"  let age = o[m];\n"+
		"  age;\n"+
		"}\n", "spell the same registry symbol")
}

// TestCheckAssignability_ElementAccess_SymbolSlotSpellingKeyNamesItsOwnReader
// pins element_access.go's #sym: spelling arm: a string key that
// happens to spell the slot vocabulary's own prefix names its own
// reason rather than reading through to a string key.
//
// The receiver is Record<string, any> — StableSymbolSlotMiss (the
// sibling pin right above) passes with the same shape of fixture
// because its own receiver's indexed value resolves to `any`; a
// Record<string, number> made `age`'s own initializer type `number`,
// not `any`, so AfterReaders' ArrivedUnchecked gate (silence/
// after_readers.go: only an any-typed initializer short-circuits the
// host-type re-seed) missed, and the element_access.go residue this
// test means to pin was reseeded away into a plain number ground
// before check_assignability.go's ResidueReason read ever ran.
func TestCheckAssignability_ElementAccess_SymbolSlotSpellingKeyNamesItsOwnReader(t *testing.T) {
	residueReasonExpectSentence(t, "function f(): void {\n"+
		"  const o: Record<string, any> = { a: 1 };\n"+
		"  let age = o[\"#sym:x\"];\n"+
		"  age;\n"+
		"}\n", "names a symbol slot")
}

// TestCheckAssignability_ElementAccess_OpenMapReceiverNamesItsOwnReader
// pins element_access.go's missing-key arm on an open-map receiver
// (an index-signature type): a missing key on a Record admits any key
// at runtime, so the read names its own reason rather than claiming a
// definite absence.
//
// The receiver is Record<string, any> for the same reason the sibling
// pin above needs it: a Record<string, number> initializer type
// (`number`) misses AfterReaders' any-only ArrivedUnchecked gate, and
// the host-type re-seed discards this arm's own residue before
// check_assignability.go ever reads it.
func TestCheckAssignability_ElementAccess_OpenMapReceiverNamesItsOwnReader(t *testing.T) {
	residueReasonExpectSentence(t, "function f(o: Record<string, any>): void {\n"+
		"  let age = o[\"missing\"];\n"+
		"  age;\n"+
		"}\n", "no fixed key set")
}

// TestCheckAssignability_ElementAccess_AstralStringIndexNamesItsOwnReader
// pins element_access.go's astral-string arm: a UTF-16 unit index into
// a string carrying a surrogate pair doesn't name one scalar code
// point, so the read names its own reason.
//
// `s[i]` is cast `as any`: a plain `s[i]` infers `string`, not `any`,
// so AfterReaders' ArrivedUnchecked gate misses (silence/
// after_readers.go — only an any-typed initializer short-circuits the
// host-type re-seed) and the read's own residue is reseeded away into
// a plain string ground before assignability ever sees it. String
// indexing has no ": any" receiver escape the way Record<string, any>
// gives the two sibling pins above, so the cast is the only route —
// and cast_and_await.go's own sort-crossing branch THREADS a
// non-empty ResidueReason through unchanged
// (`if value.ResidueReason != "" { return silence.ResidueOf(...) }`),
// so this is not a weaker substitute for the source-level read.
func TestCheckAssignability_ElementAccess_AstralStringIndexNamesItsOwnReader(t *testing.T) {
	residueReasonExpectSentence(t, "function f(i: number): void {\n"+
		"  const s = \"\\u{1F600}\";\n"+
		"  let age = s[i] as any;\n"+
		"  age;\n"+
		"}\n", "astral (surrogate-pair) character")
}

// residueReasonTrackedValue walks source's function f's body on a fresh
// env (no kernel — every case below reads structurally, never asking a
// membership question) and hands back the tracked value for name — the
// direct pin for a fix that answers a VALUE (KindUndef) rather than a
// residue sentence, where residueReasonExpectSentence's diagnostic-text
// assertion is the wrong tool.
func residueReasonTrackedValue(t *testing.T, source string, name string) abstractdomain.AbstractValue {
	t.Helper()
	p := entryEnvTestProgram(t, source)
	ctx := compoundAssignContext(p, nil, &[]assignability.RefinementDiagnostic{}, nil)
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	held, tracked := env.Get(name)
	if !tracked {
		t.Fatalf("no tracked value for %s in:\n%s", name, source)
	}
	return held
}

// TestElementAccess_ArrayHolesNonExactNumericIndexReadsUndef pins the
// element_access.go fix (follow-up unit): `new Array(15000)[i]` with i
// a non-exact but PROVABLY NONNEGATIVE INTEGER `number` — IndexWindow(i)
// is non-nil, so the read now answers the same Undef the exact-index
// arm two lines above already gives, rather than a bare residue.
// sec-ordinaryget: the array-holes receiver's present-element set is
// empty, so [[GetOwnProperty]] misses at every index in the window and
// the read falls to the (empty) prototype chain.
//
// Two departures from a residueReasonTrackedValue call: `new
// Array(15000)`, past the real 10,000-element materialization ceiling
// (array_construction.go's arrayConstructionHoleLimit) — 5000 stays a
// hole-free KindList, never reaching the KindArrayHoles arm this test
// means to pin; and an EARLY-RETURN guard (`if (!(...)) return;`)
// rather than a nested `if` block around the declarations — `const
// holes`/`const v` declared INSIDE a nested if's own block are
// block-scoped (block_statement.go's shadow-restore), so they vanish
// from the OUTER env residueReasonTrackedValue reads the moment the
// block closes, regardless of whether the array-holes read itself is
// correct. `i` is also seeded explicitly (InitialStateOfPlainParameter,
// the same seed production entry-env code gives every parameter) since
// residueReasonTrackedValue's bare AnalyzeStatements call never binds
// parameters — an untracked `i` never accumulates the comparison rows
// IndexWindow needs to prove nonnegative-integer.
func TestElementAccess_ArrayHolesNonExactNumericIndexReadsUndef(t *testing.T) {
	source := "function f(i: number): void {\n" +
		"  if (!(i >= 0 && Number.isInteger(i))) return;\n" +
		"  const holes = new Array(15000);\n" +
		"  const v = holes[i];\n" +
		"  v;\n" +
		"}\n"
	p := entryEnvTestProgram(t, source)
	ctx := compoundAssignContext(p, nil, &[]assignability.RefinementDiagnostic{}, nil)
	env := NewEnv()
	fn := entryEnvFunctionNamed(t, p, "f")
	env.Set("i", InitialStateOfPlainParameter(p, fn.AsFunctionDeclaration().Parameters.Nodes[0]))
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	held, tracked := env.Get("v")
	if !tracked {
		t.Fatalf("no tracked value for v in:\n%s", source)
	}
	if held.Kind != abstractdomain.KindUndef {
		formatted, _ := abstractdomain.FormatAbstractValue(held)
		t.Errorf("new Array(15000)[i] with i a nonnegative integer = kind %v (%q), want KindUndef", held.Kind, formatted)
	}
}

// TestElementAccess_ArrayHolesNonNumericIndexNamesTheCollisionHazard
// pins the refusing control for the fix above: an index whose sort
// isn't pinned to number at all could ToPropertyKey to a name like
// "toString" — a real Array.prototype/Object.prototype collision
// hazard sec-ordinaryget's prototype-chain step would answer through —
// so this case still declines, naming the hazard rather than assuming
// Undef.
//
// new Array(15000): array_construction.go's own materialization
// ceiling (arrayConstructionHoleLimit) is 10,000, not 5,000 — this
// fixture's array size predates that constant (or was never checked
// against it), so `new Array(5000)` actually builds a hole-free
// KindList (every consumer reads it exactly), never reaching
// element_access.go's KindArrayHoles arm this test means to pin.
// 15000 is past the real ceiling.
func TestElementAccess_ArrayHolesNonNumericIndexNamesTheCollisionHazard(t *testing.T) {
	residueReasonExpectSentence(t, "function f(k: unknown): void {\n"+
		"  const holes = new Array(15000);\n"+
		"  let age = holes[k as any];\n"+
		"  age;\n"+
		"}\n", "inherited Array.prototype/Object.prototype member name")
}

// TestElementAccess_KindListOutOfBoundsExactIndexReadsUndef pins the
// element_access.go fix (follow-up unit): an exact integer index past
// a hole-free KindList's own length now answers Undef, matching the
// KindValues/PrimitiveArray twin a few arms below (sec-array-exotic-
// objects: OrdinaryGet finds no own property past the end and falls to
// the prototype chain — a plain decimal-numeral property key, never a
// name that could collide with an inherited member).
func TestElementAccess_KindListOutOfBoundsExactIndexReadsUndef(t *testing.T) {
	held := residueReasonTrackedValue(t, "function f(): void {\n"+
		"  const xs = \"a,b\".split(\",\");\n"+
		"  const v = xs[99];\n"+
		"  v;\n"+
		"}\n", "v")
	if held.Kind != abstractdomain.KindUndef {
		formatted, _ := abstractdomain.FormatAbstractValue(held)
		t.Errorf("a hole-free KindList's out-of-bounds exact index = kind %v (%q), want KindUndef", held.Kind, formatted)
	}
}

// residueReasonCollectionCtx is a bare FlowContext good enough for
// collection_models.go's readers: no kernel question runs on any of
// these paths (collectionKey/weakEntryIdentity/Complete are pure Go
// checks), so nil Kernel is fine — the same standing promiseShapesCtx
// takes for its own direct-call site.
func residueReasonCollectionCtx(p *program.CheckerProgram) *FlowContext {
	return &FlowContext{
		P:         p,
		Registry:  annotations.AnnotationRegistry{},
		Objects:   annotations.ObjectRegistry{},
		Contracts: map[*ast.Symbol]*FunctionContract{},
		Aliases:   dataflowfacts.NewAliasClasses(),
		Declared:  map[string]*annotations.DeclaredRefinement{},
		Report:    func(assignability.RefinementDiagnostic) {},
	}
}

// residueReasonFindCall is promiseShapesCall's pattern generalized: the
// index-th CallExpression under root whose callee names method (a
// property access OR — for the bare cases below — any call reachable
// by source order), used to hand collection_models.go's readers a REAL
// AST call node without routing the whole fixture through
// EvaluateCallExpression (whose Map.get havoc+worn standing, CALLS.md
// §6, replaces an ambient .get/.has call's residue with the declared
// return type's ground before any diagnostic sees it — the trace this
// follow-up's gate run surfaced).
func residueReasonFindCall(t *testing.T, root *ast.Node, method string, index int) *ast.Node {
	t.Helper()
	var found *ast.Node
	seen := 0
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsCallExpression(node) {
			callee := Unwrapped(node.AsCallExpression().Expression)
			if ast.IsPropertyAccessExpression(callee) &&
				callee.AsPropertyAccessExpression().Name().Text() == method {
				if seen == index {
					found = node
					return true
				}
				seen++
			}
		}
		node.ForEachChild(visit)
		return found != nil
	}
	root.ForEachChild(visit)
	if found == nil {
		t.Fatalf("no call to .%s (index %d) under the given root", method, index)
	}
	return found
}

// TestCheckAssignability_CollectionModels_WeakMapUntrackedKeyNamesItsOwnReader
// pins collection_models.go's readWeakEntrySet arm DIRECTLY: a WeakMap
// `.set` whose key isn't a plain, never-reassigned identifier can't be
// tracked by reference identity, so the write's own answer names its
// own reason.
//
// The key is a FRESH OBJECT LITERAL (`{}`), not a parameter name: a
// plain, never-reassigned identifier — even a parameter — IS exactly
// what weakEntryIdentity tracks (its own doc: "a plain, never-reassigned
// local identifier"), so `wm.set(k, 1)` with `k: object` a bare
// parameter reaches the IDENTITY-FOUND branch and answers a real
// KindCollection write, not this residue — a stronger determination,
// not this arm's own case. `{}` is not `ast.IsIdentifier` at all, so
// weakEntryIdentity fails immediately and the untracked-key residue
// this test means to pin is the one that actually runs.
//
// A source-level pin (`let age = wm.set(k, 1)`) cannot reach this
// site's ResidueReason: `.set`'s declared return is the receiver type
// itself (WeakMap<object, number>), not a bodiless ambient's bare
// return, and check_assignability.go's ResidueReason read is gated to
// known.Kind == KindUnknown at the CHECKED position — whatever
// evaluates `wm.set(k, 1)` as an expression's own value in a full walk
// does not preserve the residue this reader returns as a *value*
// (the gate run's "got bare AlertText" result). Calling the reader
// directly is the trace's own conclusion, not a weaker substitute.
func TestCheckAssignability_CollectionModels_WeakMapUntrackedKeyNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const wm = new WeakMap<object, number>();\n"+
		"  wm.set({}, 1);\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "set", 0)
	ctx := residueReasonCollectionCtx(p)
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "wm", HasTrackedName: true,
		Receiver: abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: true},
		Method:   "set",
	}
	got := readWeakEntrySet(site, site.Receiver)
	if got == nil {
		t.Fatalf("readWeakEntrySet(wm.set(k, 1)) = nil, want the untracked-key residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readWeakEntrySet(wm.set(k, 1)) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "never-reassigned identifier") {
		t.Errorf("ResidueReason = %q, want it to name the never-reassigned-identifier reader", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_WeakMapIncompleteMissNamesItsOwnReader
// pins collection_models.go's readWeakEntryGet arm DIRECTLY: a `.get`
// miss on a WeakMap the walk cannot prove complete stays honestly
// unknown, naming its own reason rather than claiming absence. Direct
// call for the same reason as the .set pin above — Map.get havoc+worn
// (CALLS.md §6) replaces a source-level `.get` read's value before any
// diagnostic reaches it.
func TestCheckAssignability_CollectionModels_WeakMapIncompleteMissNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const key = {};\n"+
		"  const wm = new WeakMap<object, number>();\n"+
		"  wm.get(key);\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "get", 0)
	ctx := residueReasonCollectionCtx(p)
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "wm", HasTrackedName: true,
		Receiver: abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: false},
		Method:   "get",
	}
	got := readWeakEntryGet(site, site.Receiver)
	if got == nil {
		t.Fatalf("readWeakEntryGet(wm.get(key)) = nil, want the incomplete-miss residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readWeakEntryGet(wm.get(key)) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "an untracked call could have set this exact key too") {
		t.Errorf("ResidueReason = %q, want it to name the incomplete-record reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_CollectionIncompleteMissNamesItsOwnReader
// pins collection_models.go's readCollectionGetHas arm DIRECTLY: a
// `.get` miss, with a primitive exact key, on an ordinary Map the walk
// cannot prove complete names its own reason. Direct call for the same
// Map.get havoc+worn reason as the two pins above.
func TestCheckAssignability_CollectionModels_CollectionIncompleteMissNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const m = new Map<string, number>();\n"+
		"  m.get(\"k\");\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "get", 0)
	ctx := residueReasonCollectionCtx(p)
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "m", HasTrackedName: true,
		Receiver: abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: false},
		Method:   "get",
	}
	got := readCollectionGetHas(site)
	if got == nil {
		t.Fatalf("readCollectionGetHas(m.get(\"k\")) = nil, want the incomplete-miss residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionGetHas(m.get(\"k\")) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "a miss only answers when every entry is named") {
		t.Errorf("ResidueReason = %q, want it to name the incomplete-record reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_CollectionUnreadableKeyNamesItsOwnReader
// pins collection_models.go's readCollectionGetHas arm DIRECTLY: a
// `.get` key that isn't one primitive exact value isn't a key
// collectionKey's value-equality reading can compare, so the read
// names its own reason. Direct call for the same Map.get havoc+worn
// reason as the pins above.
//
// The key is a FRESH OBJECT LITERAL, not a parameter name: a plain,
// never-reassigned object-typed identifier is exactly what
// weakEntryIdentity tracks (collection_models.go's own doc), and
// readCollectionGetHas tries readWeakEntryGet FIRST — generalized to
// ANY KindCollection with a trackable-identity key, not only an actual
// WeakMap/WeakSet — so `m.get(k)` with `k: object` a bare identifier
// reaches that reference-identity answer (a real, sound KindUndef for
// a fresh empty complete record) rather than this arm's own
// unreadable-key residue — a stronger determination, not a miss. `{}`
// is not `ast.IsIdentifier`, so weakEntryIdentity fails immediately and
// the unreadable-key residue this test means to pin is the one that
// actually runs.
func TestCheckAssignability_CollectionModels_CollectionUnreadableKeyNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const m = new Map<object, number>();\n"+
		"  m.get({});\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "get", 0)
	ctx := residueReasonCollectionCtx(p)
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "m", HasTrackedName: true,
		Receiver: abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: true},
		Method:   "get",
	}
	got := readCollectionGetHas(site)
	if got == nil {
		t.Fatalf("readCollectionGetHas(m.get(k)) = nil, want the unreadable-key residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionGetHas(m.get(k)) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "collectionKey's value-equality reading can compare") {
		t.Errorf("ResidueReason = %q, want it to name the unreadable-key reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_GetOrInsertMaybePresentNamesItsOwnReader
// pins collection_models.go's getOrInsert arm DIRECTLY: a key that may
// already be present against a record the walk cannot prove complete
// leaves the old value unnamed, so the answer names its own reason.
//
// The source-level pin reached comparison_decision.go's OWN
// kernel-declined sentence instead (AlertText + "The kernel declined
// the question") — a different residue site (this test's own
// nil-kernel walk hits a live comparison somewhere else in the body
// before getOrInsert's own residue reaches the `age` position). Direct
// call sidesteps that ordering entirely.
func TestCheckAssignability_CollectionModels_GetOrInsertMaybePresentNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const m = new Map<string, number>();\n"+
		"  m.getOrInsert(\"k\", 2);\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "getOrInsert", 0)
	ctx := residueReasonCollectionCtx(p)
	receiver := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorMap, Complete: false}
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "m", HasTrackedName: true,
		Receiver: receiver,
		Method:   "getOrInsert",
	}
	got := readCollectionMethods(site)
	if got == nil {
		t.Fatalf("readCollectionMethods(m.getOrInsert(\"k\", 2)) = nil, want the maybe-present residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionMethods(m.getOrInsert(\"k\", 2)) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "the old value") {
		t.Errorf("ResidueReason = %q, want it to name the maybe-present reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_GetOrInsertUnreadableKeyNamesItsOwnReader
// pins collection_models.go's getOrInsert arm: a key that isn't one
// primitive exact value still writes the collection, but the
// inserted-or-held value isn't named. (This row passed the gate as
// originally written — a source-level pin, unlike its five siblings
// above — left unchanged.)
func TestCheckAssignability_CollectionModels_GetOrInsertUnreadableKeyNamesItsOwnReader(t *testing.T) {
	residueReasonExpectSentence(t, "function f(k: object): void {\n"+
		"  const m = new Map<object, number>();\n"+
		"  let age = m.getOrInsert(k, 1);\n"+
		"  age;\n"+
		"}\n", "inserted-or-held value isn't named")
}

// TestCheckAssignability_CollectionModels_DeleteIncompleteMissNamesItsOwnReader
// pins collection_models.go's write-dispatch `.delete` arm DIRECTLY: a
// delete miss on a record the walk cannot prove complete names its own
// reason rather than claiming a definite miss.
//
// The source-level pin reached comparison_decision.go's own
// kernel-declined sentence instead, the same wrong-site-first failure
// GetOrInsertMaybePresent hit. Direct call sidesteps it.
func TestCheckAssignability_CollectionModels_DeleteIncompleteMissNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  const s = new Set<string>();\n"+
		"  s.delete(\"k\");\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "delete", 0)
	ctx := residueReasonCollectionCtx(p)
	receiver := abstractdomain.AbstractValue{Kind: abstractdomain.KindCollection, CollectionFlavor: abstractdomain.FlavorSet, Complete: false}
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call,
		TrackedName: "s", HasTrackedName: true,
		Receiver: receiver,
		Method:   "delete",
	}
	got := readCollectionMethods(site)
	if got == nil {
		t.Fatalf("readCollectionMethods(s.delete(\"k\")) = nil, want the incomplete-miss residue")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionMethods(s.delete(\"k\")) = %+v, want KindUnknown", *got)
	}
	if !strings.Contains(got.ResidueReason, "deleted or set this exact key too") {
		t.Errorf("ResidueReason = %q, want it to name the incomplete-record reason", got.ResidueReason)
	}
}

// TestCheckAssignability_CollectionModels_SpecFixedMapGetNamesItsOwnReader
// pins collection_models.go's spec-fixed-methods arm DIRECTLY: a `.get`
// on a Map receiver the walk no longer pins exactly (rejoined by a
// loop, or any other route that leaves the tracked name un-refetchable
// as a KindCollection) still answers the spec's own possibly-undefined
// shape, naming its own reason for the held value.
//
// Direct call for the same Map.get havoc+worn reason as the earlier
// .get pins — this arm is reached only once the tracked-name refetch
// in readCollectionGetHas/readCollectionMethods has ALREADY missed
// (collectionReceiver stays site.Receiver, not KindCollection), which
// a hand-built site reproduces directly by handing a non-collection
// Receiver with HasTrackedName true and TrackedName absent from env.
func TestCheckAssignability_CollectionModels_SpecFixedMapGetNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(): void {\n"+
		"  let m = new Map<string, number>();\n"+
		"  m.get(\"k\");\n"+
		"}\n")
	call := residueReasonFindCall(t, p.Entry.AsNode(), "get", 0)
	ctx := residueReasonCollectionCtx(p)
	pa := Unwrapped(call.AsCallExpression().Expression).AsPropertyAccessExpression()
	site := MethodCallSite{
		Ctx: ctx, Env: NewEnv(), E: call, ReceiverExpression: pa.Expression,
		// no tracked name: the same shape a loop-rejoined `m` reaches this
		// arm with — readCollectionGetHas/readCollectionMethods's own
		// tracked-name refetch already failed by the time this row runs
		Receiver: abstractdomain.Unknown,
		Method:   "get",
	}
	got := readCollectionMethods(site)
	if got == nil {
		t.Fatalf("readCollectionMethods(m.get(\"k\")) = nil, want the spec-fixed possibly-undefined residue")
	}
	if got.Kind != abstractdomain.KindPossiblyUndefined || got.Inner == nil {
		t.Fatalf("readCollectionMethods(m.get(\"k\")) = %+v, want KindPossiblyUndefined wrapping the residue", *got)
	}
	if got.Inner.Kind != abstractdomain.KindUnknown {
		t.Fatalf("readCollectionMethods(m.get(\"k\")).Inner = %+v, want KindUnknown", *got.Inner)
	}
	if !strings.Contains(got.Inner.ResidueReason, "a loop rejoined it") {
		t.Errorf("Inner.ResidueReason = %q, want it to name the not-pinned-exactly reason", got.Inner.ResidueReason)
	}
}

// The four rows below extend the pattern to comparison_decision.go's own
// conversions (AGENT-BRIEF.md's comparison_decision.go unit) — pinned by
// DIRECT CompareKnown CALL, comparison_decision_test.go's own precedent
// (TestCompareKnownStrictUndefUndef and kin), rather than through the
// RTS7002-on-`age`-write source pattern the two tests above use.
//
// The source-level pattern does not reach here: `let age = (x < y)`
// infers age's own type as `boolean`, and variable_statement.go's write
// runs the result through silence.SeededBinding → AfterReaders, whose
// ArrivedUnchecked gate only short-circuits an ANY-typed initializer
// (JSON.parse's return type, the working test above). A boolean
// initializer fails that gate, so AfterReaders re-seeds `age` from ITS
// OWN host type (boolean) via typereading.ReadHostType — overwriting
// CompareKnown's KindUnknown/ResidueReason with a decided boolean SET
// before assignability ever sees it, which then asks the (nil, in these
// fixtures) kernel a membership question and reports
// walk.KernelDeclinedAlertText instead of either sentence. Casting the
// initializer to `any` does not fix this either: EvaluateCast's
// sort-crossing branch (cast_and_await.go) drops ResidueReason on a
// KindUnknown value being cast, returning a bare silence.Residue().
// Reading CompareKnown's return value directly is the only route that
// observes the sentence this file's four sites carry.

// TestCompareKnown_OrderingAgainstAbsenceNamesItsOwnReader pins
// comparison_decision.go's line-74 family: `x < undefined` with x an
// AbstractValue.Unknown (unread) left operand. bExactAbsent is true and
// op is CompareLt, so CompareKnown returns ResidueOf("an ordering
// comparison against null or undefined has no row…") before any
// value-kind check runs.
func TestCompareKnown_OrderingAgainstAbsenceNamesItsOwnReader(t *testing.T) {
	ctx := &FlowContext{}
	got := CompareKnown(ctx, CompareLt, false, abstractdomain.Unknown, abstractdomain.Undef)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("CompareKnown(unknown < undefined) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "an ordering comparison against null or undefined has no row") {
		t.Errorf("ResidueReason = %q, want it to name the ordering-against-absence row", got.ResidueReason)
	}
}

// TestCompareKnown_UnknownAgainstNullNamesItsOwnReader pins the
// line-110 family: `x === null` with x an AbstractValue.Unknown left
// operand. One side is exact absence (null); the other reads
// KindUnknown, which is none of KindValues/KindObject/KindList/
// KindArrayHoles, so CompareKnown falls through to the "non-absent side
// is not a plain value" sentence instead of deciding the equality.
func TestCompareKnown_UnknownAgainstNullNamesItsOwnReader(t *testing.T) {
	ctx := &FlowContext{}
	got := CompareKnown(ctx, CompareEq, true, abstractdomain.Unknown, abstractdomain.Null)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("CompareKnown(unknown === null) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "the non-absent side is not a plain value") {
		t.Errorf("ResidueReason = %q, want it to name the non-absent-side row", got.ResidueReason)
	}
}

// TestCompareKnown_TwoUnknownsCompareNamesItsOwnReader pins the
// line-113 family: `x < y` with both operands AbstractValue.Unknown.
// Neither side is NaN or exact absence, and neither reads KindValues, so
// CompareKnown declines with "a side is not a plain known value" rather
// than reaching the string/array/number rows below it.
func TestCompareKnown_TwoUnknownsCompareNamesItsOwnReader(t *testing.T) {
	ctx := &FlowContext{}
	got := CompareKnown(ctx, CompareLt, false, abstractdomain.Unknown, abstractdomain.Unknown)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("CompareKnown(unknown < unknown) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "a side is not a plain known value") {
		t.Errorf("ResidueReason = %q, want it to name the not-a-plain-known-value row", got.ResidueReason)
	}
}

// TestCompareKnown_ArrayComparisonNamesItsOwnReader pins the line-155
// family: `[1] < [2]`, two array literals of one exact number each.
// array_literal.go's uniform-numeric case reads them as
// KindValues/PrimitiveArray, which both clears the earlier KindValues
// gate and matches KindTag across sides — so CompareKnown reaches the
// array-by-reference sentence instead of the number rows.
func TestCompareKnown_ArrayComparisonNamesItsOwnReader(t *testing.T) {
	ctx := &FlowContext{}
	arrayOf := func(v float64) abstractdomain.AbstractValue {
		return abstractdomain.KnownValues([]float64{v}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	}
	got := CompareKnown(ctx, CompareLt, false, arrayOf(1), arrayOf(2))
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("CompareKnown([1] < [2]) = %+v, want KindUnknown", got)
	}
	if !strings.Contains(got.ResidueReason, "arrays compare by reference") {
		t.Errorf("ResidueReason = %q, want it to name the array-by-reference row", got.ResidueReason)
	}
}

// residueReasonPanickingScalarDisjoint is a fake kernel whose
// ScalarDisjoint always panics — the same shape a genuine kernel
// refusal takes (kernel_bridge.go's questions panic on a refused
// question, per PORT.md), forcing transferSign's callScalarDisjoint
// recover() to report !ok deterministically rather than depending on
// a real kernel actually refusing.
func residueReasonPanickingScalarDisjoint() *kernelbridge.RefinedTSKernel {
	return &kernelbridge.RefinedTSKernel{
		ScalarDisjoint: func(a, b refinementsets.RefinedSet) bool {
			panic("residue_reason_test: forced ScalarDisjoint refusal")
		},
	}
}

// TestTransferSign_ADeclinedDisjointnessQuestionNamesTheThreeRaysNotABareResidue
// pins math_transfer.go's transferSign: with a kernel present and a
// readable operand set, a ScalarDisjoint refusal must name Math.sign's
// own three-ray disjointness mechanism rather than leaving the
// KindUnknown's ResidueReason blank.
func TestTransferSign_ADeclinedDisjointnessQuestionNamesTheThreeRaysNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonPanickingScalarDisjoint())
	operand := abstractdomain.KnownValues([]float64{5}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got := transferSign(operand, true)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("transferSign with a panicking ScalarDisjoint = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("transferSign's declined-disjointness unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "disjoint") {
		t.Errorf("ResidueReason = %q, want it to name the disjointness questions", got.ResidueReason)
	}
}

// residueReasonUnknownTransfer is a fake kernel whose Transfer always
// answers TransferAnswerUnknown — the wire shape a genuine kernel
// refusal takes (transfer_questions.go's DecodeTransferAnswer decodes
// exactly this kind from a real refusal), forcing the fold/split
// branches below to decline deterministically.
func residueReasonUnknownTransfer() *kernelbridge.RefinedTSKernel {
	return &kernelbridge.RefinedTSKernel{
		Transfer: func(question kernelbridge.TransferQuestion) kernelbridge.TransferAnswer {
			return kernelbridge.TransferAnswer{Kind: kernelbridge.TransferAnswerUnknown}
		},
	}
}

// TestMathImage_ADeclinedHypotFoldNamesThePairwiseFoldNotABareResidue
// pins math_transfer.go's hypot arm: two readable operands and a live
// kernel whose Transfer refuses every question must name Math.hypot's
// own pairwise-fold mechanism.
func TestMathImage_ADeclinedHypotFoldNamesThePairwiseFoldNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	a := abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	b := abstractdomain.KnownValues([]float64{4}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got, ok := TransferMathCall("hypot", []abstractdomain.AbstractValue{a, b})
	if !ok {
		t.Fatalf("Math.hypot was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.hypot with a refusing kernel = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.hypot's declined-fold unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "hypot") {
		t.Errorf("ResidueReason = %q, want it to name Math.hypot", got.ResidueReason)
	}
}

// TestMathImage_ADeclinedAtan2WindowNamesTheKernelWindowNotABareResidue
// pins math_transfer.go's atan2 arm: a live kernel but an operand that
// SetOfKnownForTransfer cannot read as a set (a multi-value word, none
// of the singleton/set/starDepth-0-variable shapes it reads) must name
// Math.atan2's own kernel-window mechanism rather than falling through
// to KnownOfAnswer's bare unknown.
func TestMathImage_ADeclinedAtan2WindowNamesTheKernelWindowNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	y := abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	x := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got, ok := TransferMathCall("atan2", []abstractdomain.AbstractValue{y, x})
	if !ok {
		t.Fatalf("Math.atan2 was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.atan2 with an unreadable operand = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.atan2's declined-window unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "atan2") {
		t.Errorf("ResidueReason = %q, want it to name Math.atan2", got.ResidueReason)
	}
}

// residueReasonStraddlingSet is an operand set that straddles a
// domain edge (both a value past it and a value before it) — the
// shape math_unary_transfer.go's log1p/log/sqrt arms read as "not
// provably inside the domain," routing into domainSplitImage.
func residueReasonStraddlingSet(low, high float64) refinementsets.RefinedSet {
	return refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{low, high}))
}

// TestUnaryMathImage_ALog1pSplitThatCannotBuildNamesThePastMinusOneSplitNotABareResidue
// pins math_unary_transfer.go's log1p arm: an operand straddling −1
// with a kernel whose Transfer refuses the clipped question must name
// log1p's own past-−1 split, not a bare unknown.
func TestUnaryMathImage_ALog1pSplitThatCannotBuildNamesThePastMinusOneSplitNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	operand := abstractdomain.KnownSet(residueReasonStraddlingSet(-2, 5), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	got, ok := UnaryMathImage("log1p", []abstractdomain.AbstractValue{operand}, abstractdomain.TrustProved)
	if !ok {
		t.Fatalf("Math.log1p was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.log1p with a straddling operand and a refusing kernel = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.log1p's declined-split unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "log1p") {
		t.Errorf("ResidueReason = %q, want it to name Math.log1p", got.ResidueReason)
	}
}

// TestUnaryMathImage_ALogSplitThatCannotBuildNamesThePositiveSplitNotABareResidue
// pins math_unary_transfer.go's log/log2/log10 arm: an operand
// straddling zero with a kernel whose Transfer refuses the clipped
// question must name that arm's own positive-operand split.
func TestUnaryMathImage_ALogSplitThatCannotBuildNamesThePositiveSplitNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	operand := abstractdomain.KnownSet(residueReasonStraddlingSet(-3, 3), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	got, ok := UnaryMathImage("log", []abstractdomain.AbstractValue{operand}, abstractdomain.TrustProved)
	if !ok {
		t.Fatalf("Math.log was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.log with a straddling operand and a refusing kernel = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.log's declined-split unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "positive") {
		t.Errorf("ResidueReason = %q, want it to name the positive-operand split", got.ResidueReason)
	}
}

// TestUnaryMathImage_ASqrtSplitThatCannotBuildNamesTheZeroStraddleNotABareResidue
// pins math_unary_transfer.go's sqrt arm: an operand straddling zero
// with a kernel whose Transfer refuses the clipped question must name
// sqrt's own zero-straddle split.
func TestUnaryMathImage_ASqrtSplitThatCannotBuildNamesTheZeroStraddleNotABareResidue(t *testing.T) {
	SetTransferKernel(residueReasonUnknownTransfer())
	operand := abstractdomain.KnownSet(residueReasonStraddlingSet(-4, 9), nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	got, ok := UnaryMathImage("sqrt", []abstractdomain.AbstractValue{operand}, abstractdomain.TrustProved)
	if !ok {
		t.Fatalf("Math.sqrt was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.sqrt with a straddling operand and a refusing kernel = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("Math.sqrt's declined-split unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "sqrt") {
		t.Errorf("ResidueReason = %q, want it to name Math.sqrt", got.ResidueReason)
	}
}

// The rows below extend the pattern to inline_contract_body.go's own
// conversions (the interprocedural inline-call unit): a shape reaching
// one of InlineContractBody's/ClassMethodWalkCall's/SummaryCallReceiver's
// converted silence.Residue() sites must carry THAT site's sentence in
// the RTS7002 diagnostic, not the bare AlertText.

// inlineContractBodyResidueContext is superArrayContracts' recipe (real
// compiled contracts, no kernel seated) with a diagnostic sink and a
// `Declared["age"]` window wired in on top — the same two additions
// compoundAssignContext makes over an empty-Contracts FlowContext,
// needed here because these rows call INTO another declared function's
// body, which only resolves through CompileContractFileFacts' real
// contract collection.
func inlineContractBodyResidueContext(t *testing.T, p *program.CheckerProgram, sink *[]assignability.RefinementDiagnostic) *FlowContext {
	t.Helper()
	ctx := superArrayContracts(t, p)
	ctx.Declared = map[string]*annotations.DeclaredRefinement{"age": residueReasonAgeWindow()}
	ctx.Report = func(d assignability.RefinementDiagnostic) {
		*sink = append(*sink, d)
	}
	return ctx
}

// inlineContractBodyExpectSentence runs source's function "f" (the
// caller) through AnalyzeStatements against a `Declared["age"]` window,
// with no engine kernel seated (requireNoEngineKernel), and asserts the
// RTS7002 diagnostic's MessageText carries wantSubstring rather than the
// bare AlertText.
func inlineContractBodyExpectSentence(t *testing.T, source string, wantSubstring string) {
	t.Helper()
	requireNoEngineKernel(t)
	p := entryEnvTestProgram(t, source)
	var diagnostics []assignability.RefinementDiagnostic
	ctx := inlineContractBodyResidueContext(t, p, &diagnostics)
	env := NewEnv()
	statements := compoundAssignFunctionStatements(t, p, "f")
	AnalyzeStatements(ctx, env, statements, nil)
	if len(diagnostics) == 0 {
		t.Fatalf("source raised no diagnostic, want RTS7002:\n%s", source)
	}
	got := diagnostics[0].MessageText
	if got == assignability.AlertText {
		t.Errorf("diagnostic MessageText = the bare AlertText, want the site's own sentence naming %q", wantSubstring)
	}
	if !strings.Contains(got, wantSubstring) {
		t.Errorf("diagnostic MessageText = %q, want it to contain %q", got, wantSubstring)
	}
}

// summaryCallReceiverConstructedTwiceCallSite finds the CallExpression
// node spelled `new Holder(...).read()` under root.
func summaryCallReceiverConstructedTwiceCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call new Holder(...).read()", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		if access.Name() == nil || !ast.IsIdentifier(access.Name()) || access.Name().Text() != "read" {
			return false
		}
		return ast.IsNewExpression(access.Expression)
	})
}

// TestSummaryCallReceiver_AConstructedReceiverThatRunsTwiceNamesItsOwnReader
// pins SummaryCallReceiver's fallthrough (inline_contract_body.go)
// DIRECTLY: a `new Holder(sideEffect()).read()` receiver is not
// ReadsWithoutEffect (a `new` expression), so SummaryCallReceiver tries
// constructedReceiverValue — which declines because the constructor
// argument `sideEffect()` is a call, not readable a second time without
// duplicating its effect (readsTwiceWithoutEffect). Called directly
// (not through the diagnostic pipeline): a completed trace this session
// showed wornReturnTypeIfUnknown or another residue site can intervene
// between this function's own answer and any diagnostic sink, so the
// only pin that observes THIS site's own sentence unambiguously is a
// direct call on SummaryCallReceiver's own return value — the same
// precedent TestInlineContractBody_ADeclarationWithNoBodyNamesItsOwnReader
// and TestJoinSinkSummarized_AMarkerJoinsAsUnknownRatherThanExcluded set
// in this file.
func TestSummaryCallReceiver_AConstructedReceiverThatRunsTwiceNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "class Holder {\n"+
		"  n: number;\n"+
		"  constructor(seed: number) { this.n = seed; }\n"+
		"  read(): number { return this.n; }\n"+
		"}\n"+
		"function sideEffect(): number { return 1; }\n"+
		"function f(): void {\n"+
		"  new Holder(sideEffect()).read();\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	call := summaryCallReceiverConstructedTwiceCallSite(t, p.Entry.AsNode())
	got := SummaryCallReceiver(ctx, NewEnv(), call)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("SummaryCallReceiver on new Holder(sideEffect()).read() = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("SummaryCallReceiver's constructed-receiver-runs-twice unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "a receiver that runs beyond a gated") {
		t.Errorf("ResidueReason = %q, want it to name the gated-construction rule", got.ResidueReason)
	}
}

// classMethodWalkCallRecursiveCallSite finds the CallExpression node
// spelled `this.bump()` under root — the recursive call's own node,
// which ClassMethodWalkCall reads through CalleeExpressionOf.
func classMethodWalkCallRecursiveCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call this.bump()", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Expression.Kind == ast.KindThisKeyword &&
			access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "bump"
	})
}

// TestClassMethodWalkCall_ASelfRecursiveMethodCallNamesItsOwnReader pins
// ClassMethodWalkCall's recursion guard (inline_contract_body.go)
// DIRECTLY, for the same reason SummaryCallReceiver's twin above is
// called directly: a class method that calls itself back through `this`
// re-enters ClassMethodWalkCall with its own symbol already marked
// inlining — simulated here by marking ctx.Inlining with bump's own
// resolved symbol BEFORE the call, the same symbol
// ctx.P.Checker.GetSymbolAtLocation(calleeName) resolves inside the
// function — and the guard's residue must name the re-entry.
func TestClassMethodWalkCall_ASelfRecursiveMethodCallNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "class Counter {\n"+
		"  n: number;\n"+
		"  constructor(seed: number) { this.n = seed; }\n"+
		"  bump(): number {\n"+
		"    this.n = this.n + 1;\n"+
		"    return this.bump();\n"+
		"  }\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	declaration := superArrayClassMethod(t, p, "Counter", "bump")
	call := classMethodWalkCallRecursiveCallSite(t, p.Entry.AsNode())
	calleeName := declaration.Name()
	symbol := ctx.P.Checker.GetSymbolAtLocation(calleeName)
	if symbol == nil {
		t.Fatalf("no symbol resolved for Counter.bump's own name")
	}
	ctx.Inlining = map[*ast.Symbol]struct{}{symbol: {}}
	receiver := abstractdomain.KnownObject(
		[]abstractdomain.ObjectKey{{Name: "n", Value: abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)}},
		nil, true, abstractdomain.TrustProved, false)
	contract := &FunctionContract{Declaration: declaration}
	got, handled := ClassMethodWalkCall(ctx, NewEnv(), call, contract, EffectiveArguments{}, receiver)
	if !handled {
		t.Fatalf("ClassMethodWalkCall declined (handled=false) on a re-entrant this.bump() call, want handled=true")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("ClassMethodWalkCall on a re-entrant this.bump() call = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("ClassMethodWalkCall's recursion-guard unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "calls back into itself") {
		t.Errorf("ResidueReason = %q, want it to name the re-entry rule", got.ResidueReason)
	}
}

// inlineContractBodyLoopForeverCallSite finds the CallExpression node
// spelled `loopForever(n)` under root — the RECURSIVE call inside
// loopForever's own body (the first, and only, call by that name in the
// fixture), which InlineContractBody's own memo/kernel-summary/walk
// routes read while walking loopForever's body.
func inlineContractBodyLoopForeverCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call loopForever(n)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		return ast.IsIdentifier(callee) && callee.Text() == "loopForever"
	})
}

// TestInlineContractBody_ARecursionInductionAnswerNamesItsOwnReader pins
// InlineContractBody's own recursion-marker-escaping guard DIRECTLY, for
// the same reason the two tests above are: a plain (non-method) function
// whose only return is a call to itself joins, in JoinSinkSummarized, to
// the marker itself (no other branch to join against) — and that marker
// now WEARS loopForever's own declared return ground (`number`, read
// through RecursionMarker's DeclaredReturnTypeGround call), not a bare
// `{Kind: KindUnknown}` sentinel. IsMarkerOf still recognizes it as
// loopForever's own marker by IDENTITY — a direct markerBySymbol[symbol]
// read, unaffected by what shape the marker holds — so `returned` is
// still rebuilt with the leans-on-induction sentence rather than
// escaping silently as the (now-distinguishable-looking) ground.
// requireNoEngineKernel keeps the kernel-summary route (which the
// gate's own trace showed answering first with a DIFFERENT residue,
// "AlertText + The kernel declined the question") out of play, so the
// walk-route recursion guard this test means to exercise is the one
// that actually runs.
func TestInlineContractBody_ARecursionInductionAnswerNamesItsOwnReader(t *testing.T) {
	requireNoEngineKernel(t)
	p := entryEnvTestProgram(t, "function loopForever(n: number): number {\n"+
		"  return loopForever(n);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	declaration := entryEnvFunctionNamed(t, p, "loopForever")
	call := inlineContractBodyLoopForeverCallSite(t, p.Entry.AsNode())
	contract := &FunctionContract{Declaration: declaration}
	effective := EffectiveArguments{
		Nodes:  []*ast.Node{call.AsCallExpression().Arguments.Nodes[0]},
		Knowns: []abstractdomain.AbstractValue{abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)},
		Exact:  true,
	}

	// the stronger claim first: loopForever's own marker (read the same
	// way InlineContractBody's recursion guard reads it — symbol off the
	// callee name, declaration off the contract) wears the declared
	// return ground, `number`, not a bare unknown — and IsMarkerOf still
	// recognizes it as loopForever's own, by identity.
	symbol := ctx.P.Checker.GetSymbolAtLocation(declaration.Name())
	if symbol == nil {
		t.Fatalf("no symbol resolved for loopForever's own name")
	}
	marker := RecursionMarker(ctx, symbol, declaration)
	wantGround := abstractdomain.PossiblyNaN(abstractdomain.AtTrustLevel(
		abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone),
		abstractdomain.TrustLibrary,
	))
	if !abstractdomain.SameKnown(marker, wantGround) {
		spelled, _ := abstractdomain.FormatAbstractValue(marker)
		t.Fatalf("RecursionMarker(loopForever) = %q, want loopForever's declared return ground (number)", spelled)
	}
	if !IsMarkerOf(marker, symbol) {
		t.Fatalf("IsMarkerOf(loopForever's own ground-carrying marker, loopForever's symbol) = false, want true — a ground-carrying marker is still a marker by identity")
	}

	// the end-to-end claim: the induction answer InlineContractBody
	// hands OUTWARD is still the never-memoized decline, ground
	// discarded — the marker's ground rides only as far as the
	// recognition test above; a leans-on-induction answer holds only
	// inside its own induction.
	got := InlineContractBody(ctx, NewEnv(), call, contract, effective)
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("InlineContractBody(loopForever(1)) = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("InlineContractBody's recursion-induction unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "an answer leaning on a recursion induction") {
		t.Errorf("ResidueReason = %q, want it to name the recursion-induction rule", got.ResidueReason)
	}
}

// inlineContractBodyBodylessCallSite finds the CallExpression node
// spelled `pinBodyless(...)` anywhere under root — the call this test
// hands to InlineContractBody directly (superArrayFirstNode's generic
// walk-and-find, reused for a callee name instead of a `new C().m()`
// shape).
func inlineContractBodyBodylessCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call pinBodyless(...)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		return ast.IsIdentifier(callee) && callee.Text() == "pinBodyless"
	})
}

// TestInlineContractBody_ADeclarationWithNoBodyNamesItsOwnReader pins
// InlineContractBody's own body-nil decline directly: every PRODUCTION
// caller (evaluate_call_expression.go, evaluate_tagged_template.go)
// already gates `contract.Declaration.Body() != nil` before calling
// InlineContractCall/InlineContractBody at all, so this branch is
// unreachable through the ordinary walk — it is called here directly,
// the same way TestJoinSinkSummarized_AMarkerJoinsAsUnknownRatherThan
// Excluded pins JoinSinkSummarized directly, one function-call layer in
// from the full walk.
func TestInlineContractBody_ADeclarationWithNoBodyNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "declare function pinBodyless(x: number): number;\n"+
		"function f(): void {\n"+
		"  pinBodyless(1);\n"+
		"}\n")
	declaration := entryEnvFunctionNamed(t, p, "pinBodyless")
	call := inlineContractBodyBodylessCallSite(t, p.Entry.AsNode())
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}, Report: func(assignability.RefinementDiagnostic) {}}
	contract := &FunctionContract{Declaration: declaration}
	got := InlineContractBody(ctx, NewEnv(), call, contract, EffectiveArguments{})
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("InlineContractBody on a bodyless declaration = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("InlineContractBody's body-nil unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "no body to inline") {
		t.Errorf("ResidueReason = %q, want it to name the missing body", got.ResidueReason)
	}
}

// residueReasonMinMaxKernel loads the real native kernel and wraps its
// Transfer to RECORD every TransferOpMin/TransferOpMax question asked
// — so a test can assert the KERNEL fold actually ran (not just that
// some answer came back) — pinning the W1 kernel-first flip against a
// silent same-shaped-answer false pass. Skipped when the dylib is
// absent, the same gate trig_reduction_test.go uses.
func residueReasonMinMaxKernel(t *testing.T) (*kernelbridge.RefinedTSKernel, *int) {
	t.Helper()
	if !kernelbridge.KernelArtifactsPresent(kernelbridge.DylibPath) {
		t.Skip("native kernel dylib absent — build it first")
	}
	real, err := kernelbridge.LoadKernel(kernelbridge.DylibPath)
	if err != nil {
		t.Fatalf("LoadKernel: %v", err)
	}
	asked := new(int)
	wrapped := *real
	realTransfer := real.Transfer
	wrapped.Transfer = func(question kernelbridge.TransferQuestion) kernelbridge.TransferAnswer {
		if question.Op == kernelbridge.TransferOpMin || question.Op == kernelbridge.TransferOpMax {
			*asked++
		}
		return realTransfer(question)
	}
	return &wrapped, asked
}

// TestMathImage_MinMaxAsksTheKernelFirstOnAllExactOperands pins the
// W1 flip: Math.min/Math.max with all-exact operands must POSE the
// kernel's TransferOpMin/TransferOpMax fold (asked > 0), not skip
// straight to the local all-exact host computation — the kernel-first
// half of the rule (narrow_questions.go's Returned()/MayThrow shape).
// The exact answer must still come out right, whichever route served
// it, since the kernel fold and the host row state the same fact.
func TestMathImage_MinMaxAsksTheKernelFirstOnAllExactOperands(t *testing.T) {
	kernel, asked := residueReasonMinMaxKernel(t)
	SetTransferKernel(kernel)
	a := abstractdomain.KnownValues([]float64{3}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	b := abstractdomain.KnownValues([]float64{7}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got, ok := TransferMathCall("max", []abstractdomain.AbstractValue{a, b})
	if !ok {
		t.Fatalf("Math.max was not transferred")
	}
	if *asked == 0 {
		t.Errorf("Math.max(3, 7) with all-exact operands never posed TransferOpMax — the kernel-first route was skipped")
	}
	if got.Kind != abstractdomain.KindValues || len(got.Values) != 1 || got.Values[0] != 7 {
		t.Errorf("Math.max(3, 7) = %+v, want the exact value 7", got)
	}
}

// TestMathImage_MinMaxFallsBackToTheHostRowWhenTheKernelCannotBePosed
// pins the fallback half: an operand SetOfKnownForTransfer cannot read
// (a multi-value word) alongside an all-exact partner must still
// answer via the local host computation — the REFUSAL half of the
// rule — rather than declining outright, and must NOT pose the kernel
// question for that pair (there is no readable set to pose it with).
func TestMathImage_MinMaxFallsBackToTheHostRowWhenTheKernelCannotBePosed(t *testing.T) {
	kernel, asked := residueReasonMinMaxKernel(t)
	SetTransferKernel(kernel)
	unreadable := abstractdomain.KnownValues([]float64{1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	got, ok := TransferMathCall("max", []abstractdomain.AbstractValue{unreadable})
	if !ok {
		t.Fatalf("Math.max was not transferred")
	}
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("Math.max of one unreadable multi-value operand = %+v, want KindUnknown (not all-exact, not kernel-readable)", got)
	}
	if *asked != 0 {
		t.Errorf("Math.max posed TransferOpMax on an operand SetOfKnownForTransfer cannot read (%d questions asked), want 0", *asked)
	}
}

// The rows below extend the pattern to callback_outcome.go's own
// conversions (the array-callback outcome unit): a shape reaching one
// of CallbackOutcome's/reduceOutcome's/findOutcome's converted
// silence.Residue() sites must carry THAT site's sentence, not a bare
// unknown. Two rows call CallbackOutcome directly (the way
// TestJoinSinkSummarized_AMarkerJoinsAsUnknownRatherThanExcluded and
// TestInlineContractBody_ADeclarationWithNoBodyNamesItsOwnReader above
// pin their own functions directly): CallbackOutcome's own outcome
// functions each re-derive their returned unknown from ItemsOf/ElementOf
// rather than propagating a converted receiver/binding verbatim, so the
// two rows below are built to read a site's sentence at a point the
// dispatch DOES carry it through untouched — a prebound binding a
// callback body hands straight back, and reduce's own no-initial-value
// default.

// callbackOutcomeBoundMapCallSite finds the CallExpression node spelled
// `[<n>].map(bound)` under root — the call this test hands to
// CallbackOutcome directly, alongside the separately-resolved `f`
// declaration as the arrow (CallbackOf's own answer for a stored-bind
// argument, re-derived here rather than routed through CallbackOf so the
// test controls exactly which node plays which role).
func callbackOutcomeBoundMapCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call [..].map(bound)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "map"
	})
}

// TestCallbackOutcome_APreboundNonLiteralArgumentNamesItsOwnReader pins
// CallbackOutcome's own prebound-binding decline: `f.bind(null,
// sideEffect())` binds `f`'s first parameter at bind time to a CALL
// (not SyntacticLiteral), so the value this walk has no environment to
// re-evaluate binds through silence.ResidueOf rather than silently
// reading zero. `f`'s body returns that very parameter (`x`), so
// map-over-one-exact-item's own output list carries the sentence
// straight through as one of its Items — the one consumer in this
// dispatch that propagates a bound value untouched instead of
// re-deriving its own residue from ItemsOf/ElementOf.
//
// x: unknown — an EXPLICIT unknown parameter annotation, not `number`:
// BindParameter (callback_pins.go) seeds every bound name through
// silence.SeededBinding -> AfterReaders (after_readers.go), which reads
// a PRESENT (non-Opaque) KindUnknown as "silence to fill from the host
// type" and replaces it with ReadHostType's answer at the parameter's
// OWN declared type — a `number` parameter reseeds this site's own
// ResidueOf residue into a bare possiblyNaN-wrapped number ground before
// it ever reaches preboundBindings, discarding the very ResidueReason
// this test means to observe (confirmed empirically: the gate's own run
// showed x arriving possiblyNaN-wrapped, Inner set, not KindUnknown).
// ReadHostType on `unknown` answers (_, false), so AfterReaders' `if !ok
// { return held }` returns this site's residue untouched — the same
// `: unknown` dodge already used for a callee's OWN return type, applied
// here to a callee's PARAMETER type instead.
func TestCallbackOutcome_APreboundNonLiteralArgumentNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function f(x: unknown, y: number): unknown {\n"+
		"  return x;\n"+
		"}\n"+
		"function sideEffect(): number { return 1; }\n"+
		"function g(): void {\n"+
		"  const bound = f.bind(null, sideEffect());\n"+
		"  [1].map(bound);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	fArrow := entryEnvFunctionNamed(t, p, "f")
	call := callbackOutcomeBoundMapCallSite(t, p.Entry.AsNode())
	receiver := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	got := CallbackOutcome(ctx, NewEnv(), receiver, "map", call, fArrow, "", false, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	if got.Kind != abstractdomain.KindList || len(got.Items) != 1 {
		t.Fatalf("[1].map(bound) with bound = f.bind(null, sideEffect()) = %+v, want a one-item KindList (x is not a flat number since it carries residue)", got)
	}
	item := got.Items[0]
	if item.Kind != abstractdomain.KindUnknown {
		t.Fatalf("the mapped item (f's own x, the prebound value) = %+v, want KindUnknown", item)
	}
	if item.ResidueReason == "" {
		t.Fatalf("the prebound non-literal argument's unknown carries no ResidueReason")
	}
	if !strings.Contains(item.ResidueReason, "only a syntactic literal carries") {
		t.Errorf("ResidueReason = %q, want it to name the syntactic-literal-only rule", item.ResidueReason)
	}
}

// callbackOutcomeReduceCallSite finds the CallExpression node spelled
// `<receiver>.reduce(<callback>)` under root.
func callbackOutcomeReduceCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call xs.reduce(cb)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "reduce"
	})
}

// TestReduceOutcome_AnEmptyReceiverWithNoInitialValueNamesItsOwnReaderRatherThanPanicking
// pins reduceOutcome's empty-receiver-no-initial-value shape: sec-array.
// prototype.reduce step 4 ("If length = 0 and initialValue is not
// present, throw a TypeError exception") means `[].reduce(cb)` never
// completes at runtime. The receiver here is a KindValues array with a
// zero-length Values slice — ItemsOf turns that into a non-nil,
// zero-length items slice, the exact shape that used to reach an
// unguarded `items[1:]` and panic (`slice bounds out of range [1:0]`)
// before any caller observed a result. The guarded read must instead
// return the decline this branch already names ("nothing to vouch for
// there") rather than crashing.
func TestReduceOutcome_AnEmptyReceiverWithNoInitialValueNamesItsOwnReaderRatherThanPanicking(t *testing.T) {
	p := entryEnvTestProgram(t, "function g(): void {\n"+
		"  const empty: number[] = [];\n"+
		"  empty.reduce((acc: number, v: number) => acc + v);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	arrow := superArrayFirstNode(t, p.Entry.AsNode(), "the reduce callback arrow", ast.IsArrowFunction)
	call := callbackOutcomeReduceCallSite(t, p.Entry.AsNode())
	receiver := abstractdomain.KnownValues(nil, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	got := CallbackOutcome(ctx, NewEnv(), receiver, "reduce", call, arrow, "", false, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("[].reduce(cb) with no initial value = %+v, want KindUnknown (no panic)", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("the empty-receiver-no-initial-value unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "nothing to vouch for there") {
		t.Errorf("ResidueReason = %q, want it to name the empty-receiver-throws rule", got.ResidueReason)
	}
}

// callbackOutcomeFindCallSite finds the CallExpression node spelled
// `<receiver>.find(<callback>)` under root.
func callbackOutcomeFindCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call xs.find(cb)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "find"
	})
}

// TestFindOutcome_AnUndecidedSearchNamesItsOwnReader pins findOutcome's
// own fallback: a receiver that is not an exact sequence (ItemsOf nil)
// never enters the decided-predicate loop, so the search stays
// undecided — the result may be undefined or the found element, which
// leaves the model, and the residue must name that rather than leaving
// it bare.
func TestFindOutcome_AnUndecidedSearchNamesItsOwnReader(t *testing.T) {
	p := entryEnvTestProgram(t, "function g(): void {\n"+
		"  const empty: number[] = [];\n"+
		"  empty.find((v: number) => v > 0);\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	arrow := superArrayFirstNode(t, p.Entry.AsNode(), "the find callback arrow", ast.IsArrowFunction)
	call := callbackOutcomeFindCallSite(t, p.Entry.AsNode())
	// KindSet (not KindList/KindValues) reads as NOT an exact sequence —
	// ItemsOf answers nil for it — so findOutcome's decided-predicate
	// loop never runs and falls straight to this test's own target line.
	receiver := abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
	got := CallbackOutcome(ctx, NewEnv(), receiver, "find", call, arrow, "", false, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	if got.Kind != abstractdomain.KindUnknown {
		t.Fatalf("empty.find(cb) over a non-exact receiver = %+v, want KindUnknown", got)
	}
	if got.ResidueReason == "" {
		t.Fatalf("find's undecided-search unknown carries no ResidueReason")
	}
	if !strings.Contains(got.ResidueReason, "leaves the model") {
		t.Errorf("ResidueReason = %q, want it to name the undecided-search rule", got.ResidueReason)
	}
}

// callbackOutcomeForEachCallSite finds the CallExpression node spelled
// `<receiver>.forEach(<callback>)` under root.
func callbackOutcomeForEachCallSite(t *testing.T, root *ast.Node) *ast.Node {
	t.Helper()
	return superArrayFirstNode(t, root, "call xs.forEach(cb)", func(node *ast.Node) bool {
		if !ast.IsCallExpression(node) {
			return false
		}
		callee := node.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(callee) {
			return false
		}
		access := callee.AsPropertyAccessExpression()
		return access.Name() != nil && ast.IsIdentifier(access.Name()) && access.Name().Text() == "forEach"
	})
}

// TestForEachOutcome_TheCallResultIsUndefinedNotResidue pins forEach's
// own call result: sec-array.prototype.foreach's last step is
// unconditionally "Return undefined" — never a decline. An exact
// one-element array with no owner parameter runs the exact fold
// (forEachExactFold), whose own handled-return used to answer
// silence.Residue() (an unknown carrying no ResidueReason) where the
// spec pins undefined exactly.
func TestForEachOutcome_TheCallResultIsUndefinedNotResidue(t *testing.T) {
	p := entryEnvTestProgram(t, "function g(): void {\n"+
		"  const xs: number[] = [1];\n"+
		"  xs.forEach((v: number) => { v; });\n"+
		"}\n")
	ctx := superArrayContracts(t, p)
	arrow := superArrayFirstNode(t, p.Entry.AsNode(), "the forEach callback arrow", ast.IsArrowFunction)
	call := callbackOutcomeForEachCallSite(t, p.Entry.AsNode())
	receiver := abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveArray, abstractdomain.TrustProved)
	got := CallbackOutcome(ctx, NewEnv(), receiver, "forEach", call, arrow, "", false, LoopAnalyzers{
		AnalyzeStatement:   AnalyzeStatement,
		EvaluateExpression: evaluateExpression,
		IterationElement:   IterationElementOf,
	})
	if got.Kind != abstractdomain.KindUndef {
		t.Fatalf("xs.forEach(cb) call result = %+v, want abstractdomain.Undef (sec-array.prototype.foreach: \"Return undefined\")", got)
	}
}

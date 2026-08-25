// The template unit for the fleet's RTS7002-provenance sweep
// (AGENT-BRIEF.md): one converted call site (coercion_models.go's
// readJsonMethods, the JSON.parse-of-unknown-text branch) and the
// consumer that reads it (check_assignability.go's KindUnknown
// branch). Two rows: the converted site's own sentence must reach the
// diagnostic, and an UNCONVERTED site (untracked_identifier.go, still
// bare silence.Residue()) must keep reporting assignability.AlertText
// — the fallback that makes every later conversion in the fleet's
// sweep measurable as a message change, not a silent no-op.
//
// This file holds the RTS7002-sink pattern (residueReasonAgeWindow,
// residueReasonExpectSentence, residueReasonTrackedValue — every
// sibling residue file in this split uses these) plus the
// element_access.go family and the array-holes/out-of-bounds reads.
// Sibling files: residue_reason_collections_test.go
// (collection_models.go), residue_reason_math_test.go
// (comparison_decision.go / math_transfer.go / math_unary_transfer.go),
// residue_reason_inline_contract_test.go (inline_contract_body.go /
// class-method / summary-call-receiver), and
// residue_reason_callback_outcome_test.go (callback_outcome.go).
package walk

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
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

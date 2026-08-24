// Interface tests for CompileAnnotation. Ported from
// schema_chain_compiler.test.ts's static-chain cases; the entry/
// program helpers replace programFromSource + the real surface
// module (service/ tier, unported -- see
// annotations_test_helpers_test.go's header).

package annotations

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// compileTopLevel compiles every top-level `const NAME = z...` in
// order, returning the named one's Compiled -- the TS test's
// `compiled` helper, minus the registry threading which
// CompileAnnotation.CompileAnnotation itself needs explicit.
func compileTopLevel(t *testing.T, source string, name string) Compiled {
	t.Helper()
	p := newTestProgram(t, source)
	registry := AnnotationRegistry{}
	var found *Compiled
	for _, statement := range p.program.Entry.Statements.Nodes {
		if !ast.IsVariableStatement(statement) {
			continue
		}
		for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
			varDecl := declaration.AsVariableDeclaration()
			if varDecl.Initializer == nil || !ast.IsIdentifier(varDecl.Name()) {
				continue
			}
			if !RootsInSurface(p.program, varDecl.Initializer) {
				continue
			}
			c := CompileAnnotation(p.program, varDecl.Initializer, registry)
			if IsUnsupported(c) {
				if varDecl.Name().AsIdentifier().Text == name {
					found = &c
				}
				continue
			}
			symbol := p.program.Checker.GetSymbolAtLocation(varDecl.Name())
			if symbol != nil {
				registry[symbol] = c.Annotation
			}
			if varDecl.Name().AsIdentifier().Text == name {
				found = &c
			}
		}
	}
	if found == nil {
		t.Fatalf("no annotation named %s", name)
	}
	return *found
}

func TestCompileAnnotation_NumberChainCompilesToItsForms(t *testing.T) {
	c := compileTopLevel(t, "const X = z.number();\n", "X")
	if IsUnsupported(c) {
		t.Fatalf("unexpected unsupported: %s", c.Unsupported.Unsupported)
	}
	set := derefSet(c.Annotation.Set)
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormAtLeast {
		t.Errorf("z.number() forms = %+v, want a single atLeast(-Inf)", set.Forms)
	}
}

func TestCompileAnnotation_SurfaceRulesRefuseAtCompile(t *testing.T) {
	// a refine outside the readable guard language stays a runtime
	// check: the annotation compiles with its base set (positions
	// checked against it alert instead of accepting)
	unread := compileTopLevel(t, "const X = z.number().refine(() => true);\n", "X")
	if IsUnsupported(unread) {
		t.Fatalf("unexpected unsupported: %s", unread.Unsupported.Unsupported)
	}
	plain := compileTopLevel(t, "const X = z.number();\n", "X")
	if !sameSetStructurally(derefSet(unread.Annotation.Set), derefSet(plain.Annotation.Set)) {
		t.Errorf("unread refine's base set diverged from the bare z.number() set")
	}
	if !unread.Annotation.Unread {
		t.Errorf("expected Unread=true on the unparsed refine")
	}
}

func TestCompileAnnotation_StringChainsCompileRepetitionBoundsAndPatterns(t *testing.T) {
	star := compileTopLevel(t, "const X = z.string();\n", "X")
	if IsUnsupported(star) {
		t.Fatalf("unexpected unsupported: %s", star.Unsupported.Unsupported)
	}
	if derefSet(star.Annotation.Set).Forms[0].Form != refinementsets.FormStar {
		t.Errorf("z.string() forms[0] = %v, want star", derefSet(star.Annotation.Set).Forms[0].Form)
	}

	// length bounds are REPETITION on sequence chains -- never
	// atLeast; bounded repetition is the native form
	exact := compileTopLevel(t, "const X = z.string().length(2);\n", "X")
	if IsUnsupported(exact) {
		t.Fatalf("unexpected unsupported: %s", exact.Unsupported.Unsupported)
	}
	if derefSet(exact.Annotation.Set).Forms[0].Form != refinementsets.FormRepeat {
		t.Errorf("z.string().length(2) forms[0] = %v, want repeat", derefSet(exact.Annotation.Set).Forms[0].Form)
	}

	bounded := compileTopLevel(t, "const X = z.string().min(1).max(3);\n", "X")
	if IsUnsupported(bounded) {
		t.Fatalf("unexpected unsupported: %s", bounded.Unsupported.Unsupported)
	}
	if derefSet(bounded.Annotation.Set).Forms[0].Form != refinementsets.FormRepeat {
		t.Errorf("min/max chain forms[0] = %v, want repeat", derefSet(bounded.Annotation.Set).Forms[0].Form)
	}

	// the C* ground (z.string()'s own bare base) drops beside the
	// grammar's own forms -- chain_method.go's "regex" case now runs
	// WithoutStringGround exactly as withString's siblings (.startsWith/
	// .endsWith/.includes/.not) already do: the ground conjunct adds
	// nothing (the pattern is already a language over C) while stacking
	// it beside the grammar's own concatenation form blinds the
	// kernel's one-shape sequence-subset prover -- measured live
	// (g_timestamp_live_ask_capture_test.go, walk package):
	// kernel.SeqSubset on the 2-form pair DECLINED; the 1-form pair the
	// ground-dropped compile now produces proves true, matching
	// timestamp_operand_probe_test.go's own hand-built single-form B.
	pattern := compileTopLevel(t, `const X = z.string().regex(/^[0-9]+$/);`+"\n", "X")
	if IsUnsupported(pattern) {
		t.Fatalf("unexpected unsupported: %s", pattern.Unsupported.Unsupported)
	}
	if got := len(derefSet(pattern.Annotation.Set).Forms); got != 1 {
		t.Errorf("regex chain forms count = %d, want 1 (the redundant C* ground dropped)", got)
	}

	backrefs := compileTopLevel(t, `const X = z.string().regex(/(a)\1/);`+"\n", "X")
	if !IsUnsupported(backrefs) {
		t.Fatalf("expected backreferences to refuse")
	}
}

// requireArrayRepeat compiles a z.array chain and asserts its set is
// EXACTLY one Repeat form (never a bare atLeast/atMost ray, and never
// the collapsed bare-element shape z.string().length(1) uses) whose
// window is [wantLo, wantHi] and whose element is the array's own
// member set (min(-1).max(1) on z.number(), read back via the same
// atLeast/atMost forms the element chain compiles to on its own).
func requireArrayRepeat(t *testing.T, source string, wantLo int, wantHi *int) {
	t.Helper()
	c := compileTopLevel(t, source, "X")
	if IsUnsupported(c) {
		t.Fatalf("unexpected unsupported: %s", c.Unsupported.Unsupported)
	}
	set := derefSet(c.Annotation.Set)
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormRepeat {
		t.Fatalf("%s forms = %+v, want exactly one repeat form", source, set.Forms)
	}
	rep, ok := refinementsets.AsRepetition(set)
	if !ok {
		t.Fatalf("%s: AsRepetition failed on a Repeat-tagged set", source)
	}
	if rep.Lo != wantLo {
		t.Errorf("%s: rep.Lo = %d, want %d", source, rep.Lo, wantLo)
	}
	if (rep.Hi == nil) != (wantHi == nil) || (rep.Hi != nil && wantHi != nil && *rep.Hi != *wantHi) {
		t.Errorf("%s: rep.Hi = %v, want %v", source, rep.Hi, wantHi)
	}
	element := compileTopLevel(t, "const E = z.number().min(-1).max(1);\n", "E")
	if IsUnsupported(element) {
		t.Fatalf("unexpected unsupported building the element fixture")
	}
	if !sameSetStructurally(rep.Element, derefSet(element.Annotation.Set)) {
		t.Errorf("%s: rep.Element = %+v, want the z.number().min(-1).max(1) set %+v", source, rep.Element, derefSet(element.Annotation.Set))
	}
}

// z.array(z.number().min(-1).max(1)) chained with EVERY combination
// of .min/.max/.length must carry the Repeat form with the correct
// [lo, hi] window AND the element -- never a bare numeric ray. This
// pins the min(1).max(1) drop: TightenRepetition's rebuild used to
// route the exact-length-1 window through Repetition's (1,1)
// collapse (correct only for a CODEPOINT element, where a
// 1-character string IS the scalar layer), losing the Repeat wrapper
// and every element-consuming reader (destructuring, relational
// accumulation) along with it. Repetition now collapses at (1,1)
// only when the element is demonstrably Codepoints.
func TestCompileAnnotation_ArrayChainsCarryRepeatAcrossEveryLengthCombination(t *testing.T) {
	one := 1
	three := 3
	// floored-only: z.array(E).min(1) -- [1, unbounded]
	requireArrayRepeat(t, "const X = z.array(z.number().min(-1).max(1)).min(1);\n", 1, nil)
	// floor+ceiling: z.array(E).min(1).max(3) -- [1, 3]
	requireArrayRepeat(t, "const X = z.array(z.number().min(-1).max(1)).min(1).max(3);\n", 1, &three)
	// exact .length(N): z.array(E).length(1) -- [1, 1], the exact
	// shape the drop hit (the reported defect's own repro)
	requireArrayRepeat(t, "const X = z.array(z.number().min(-1).max(1)).length(1);\n", 1, &one)
	// ceiling-only: z.array(E).max(1) -- [0, 1]
	requireArrayRepeat(t, "const X = z.array(z.number().min(-1).max(1)).max(1);\n", 0, &one)
	// min(1).max(1): the exact reported defect's spelling -- same
	// [1, 1] window .length(1) reaches by a different chain
	requireArrayRepeat(t, "const X = z.array(z.number().min(-1).max(1)).min(1).max(1);\n", 1, &one)
}

func TestCompileAnnotation_TupleNestsRightRestAppendsTheStar(t *testing.T) {
	two := compileTopLevel(t, "const X = z.tuple([z.number().int(), z.number()]);\n", "X")
	if IsUnsupported(two) {
		t.Fatalf("unexpected unsupported: %s", two.Unsupported.Unsupported)
	}
	if derefSet(two.Annotation.Set).Forms[0].Form != refinementsets.FormConcatenation {
		t.Errorf("tuple forms[0] = %v, want concatenation", derefSet(two.Annotation.Set).Forms[0].Form)
	}
	varied := compileTopLevel(t, "const X = z.tuple([z.number().int()]).rest(z.number());\n", "X")
	if IsUnsupported(varied) {
		t.Fatalf("unexpected unsupported: %s", varied.Unsupported.Unsupported)
	}
	top := derefSet(varied.Annotation.Set).Forms[0]
	if top.Form != refinementsets.FormConcatenation {
		t.Fatalf("rest chain top form = %v, want concatenation", top.Form)
	}
	if top.B.Forms[0].Form != refinementsets.FormStar {
		t.Errorf("rest chain tail form = %v, want star", top.B.Forms[0].Form)
	}
}

func TestCompileAnnotation_StringLiteralAndEnumCompileToCodepointTuples(t *testing.T) {
	one := compileTopLevel(t, `const X = z.literal("a");`+"\n", "X")
	if IsUnsupported(one) {
		t.Fatalf("unexpected unsupported: %s", one.Unsupported.Unsupported)
	}
	got := derefSet(one.Annotation.Set).Forms[0]
	if got.Form != refinementsets.FormOneOf || len(got.W) != 1 || got.W[0] != 97 {
		t.Errorf("z.literal(\"a\") forms[0] = %+v, want oneOf([97])", got)
	}
	two := compileTopLevel(t, `const X = z.enum(["a", "b"]);`+"\n", "X")
	if IsUnsupported(two) {
		t.Fatalf("unexpected unsupported: %s", two.Unsupported.Unsupported)
	}
	if derefSet(two.Annotation.Set).Forms[0].Form != refinementsets.FormUnion {
		t.Errorf("z.enum forms[0] = %v, want union", derefSet(two.Annotation.Set).Forms[0].Form)
	}
}

// a union of ONLY numeric z.literal members collapses to one flat
// oneOf -- matching Python's Literal[1, 2, 3] reading (surface.rs's
// literal_alias_set, one one_of([1,2,3]) form), never the nested
// union(union(oneOf[1], oneOf[2]), oneOf[3]) each pairwise Union call
// would otherwise leave stacked (chain_root_constructor.go's "union"
// case, refinementsets.MergeScalarOneOfArms).
func TestCompileAnnotation_NumericLiteralUnionCollapsesToOneFlatOneOf(t *testing.T) {
	c := compileTopLevel(t, "const X = z.union([z.literal(1), z.literal(2), z.literal(3)]);\n", "X")
	if IsUnsupported(c) {
		t.Fatalf("unexpected unsupported: %s", c.Unsupported.Unsupported)
	}
	set := derefSet(c.Annotation.Set)
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormOneOf {
		t.Fatalf("forms = %+v, want exactly one oneOf form", set.Forms)
	}
	if got := set.Forms[0].W; len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("oneOf members = %+v, want [1, 2, 3]", got)
	}
}

// a string-literal union of single-codepoint members stays the
// nested union of singleton tuples -- Python's own string_literal_set
// never flattens a Literal["a", "b", ...] union either (surface.rs),
// so this is not a case the merge collapses even though each arm's
// compiled set is structurally a bare oneOf singleton exactly like
// the numeric case above (the wire shapes coincide; the SORT does
// not, and only the source call's own literal argument tells them
// apart -- chain_root_constructor.go's isNumericLiteralCall).
func TestCompileAnnotation_StringLiteralUnionStaysNested(t *testing.T) {
	c := compileTopLevel(t, `const X = z.union([z.literal("a"), z.literal("b")]);`+"\n", "X")
	if IsUnsupported(c) {
		t.Fatalf("unexpected unsupported: %s", c.Unsupported.Unsupported)
	}
	set := derefSet(c.Annotation.Set)
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormUnion {
		t.Fatalf("forms = %+v, want a single union form (not merged)", set.Forms)
	}
}

// a union mixing a numeric literal arm with a NON-literal arm (a
// window) does not collapse -- the merge only fires when EVERY
// present member is a bare numeric z.literal.
func TestCompileAnnotation_MixedLiteralAndWindowUnionStaysNested(t *testing.T) {
	c := compileTopLevel(t, "const X = z.union([z.literal(1), z.number().gte(0)]);\n", "X")
	if IsUnsupported(c) {
		t.Fatalf("unexpected unsupported: %s", c.Unsupported.Unsupported)
	}
	set := derefSet(c.Annotation.Set)
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormUnion {
		t.Fatalf("forms = %+v, want a single union form (not merged)", set.Forms)
	}
}

// z.number().int().gte(0).lte(100).multipleOf(5) carries no redundant
// unbounded ray beside the tighter atLeast(0) -- the numeric chain's
// own CanonicalScalarForms call (chain_numeric_method.go's withForm)
// folds it away exactly as the string chain's WithoutStringGround
// drops its own redundant C* ground. Matching Python's
// Annotated[int, Field(ge=0, le=100, multiple_of=5)] reading, which
// never seeds the unbounded ray at all.
func TestCompileAnnotation_NumericWindowDropsTheRedundantUnboundedRay(t *testing.T) {
	c := compileTopLevel(t, "const X = z.number().int().gte(0).lte(100).multipleOf(5);\n", "X")
	if IsUnsupported(c) {
		t.Fatalf("unexpected unsupported: %s", c.Unsupported.Unsupported)
	}
	set := derefSet(c.Annotation.Set)
	for _, f := range set.Forms {
		if f.Form == refinementsets.FormAtLeast && f.A < -1e300 {
			t.Errorf("forms = %+v, kept a redundant unbounded atLeast ray beside the tighter atLeast(0)", set.Forms)
		}
	}
	if len(set.Forms) != 4 {
		t.Errorf("forms = %+v, want exactly 4 (atLeast(0), atMost(100), integer, multipleOf(5))", set.Forms)
	}
}

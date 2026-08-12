package refinementsets

import (
	"math"
	"reflect"
	"testing"
)

// equalSet does a structural comparison of two RefinedSets, following
// pointer fields by value (reflect.DeepEqual would compare *RefinedSet
// pointers by pointee, which it already does correctly -- but we keep
// this helper for readability at call sites and to mirror the TS
// tests' toEqual, which is a structural / deep equality check).
func equalSet(a, b RefinedSet) bool {
	return reflect.DeepEqual(a, b)
}

func equalForm(a, b Refinement) bool {
	return reflect.DeepEqual(a, b)
}

func TestFormsCarryTheWireShape(t *testing.T) {
	if !equalForm(AtLeast(0), Refinement{Form: FormAtLeast, A: 0}) {
		t.Errorf("AtLeast(0) mismatch")
	}
	if !equalForm(Below(math.Inf(-1)), Refinement{Form: FormBelow, A: math.Inf(-1)}) {
		t.Errorf("Below(-Inf) mismatch")
	}
	if !equalForm(Integer, Refinement{Form: FormInteger}) {
		t.Errorf("Integer mismatch")
	}
	if !equalForm(MultipleOf(0.25), Refinement{Form: FormMultipleOf, A: 0.25}) {
		t.Errorf("MultipleOf(0.25) mismatch")
	}
	if !equalForm(OneOf([]float64{1, 2}), Refinement{Form: FormOneOf, W: []float64{1, 2}}) {
		t.Errorf("OneOf([1,2]) mismatch")
	}
	starForm := Star(Numbers)
	if starForm.Form != FormStar {
		t.Errorf("Star(Numbers).Form = %v, want star", starForm.Form)
	}
	if !equalSet(*starForm.A_, MakeRefinedSet(AtLeast(math.Inf(-1)))) {
		t.Errorf("Star(Numbers).A_ mismatch")
	}
	diff := Difference(Numbers, MakeRefinedSet(OneOf([]float64{0})))
	if diff.Form != FormDifference {
		t.Errorf("Difference(...).Form = %v, want difference", diff.Form)
	}
}

func TestInfinitiesAreElementsNaNIsRefused(t *testing.T) {
	if AtLeast(math.Inf(1)).Form != FormAtLeast {
		t.Errorf("AtLeast(+Inf).Form mismatch")
	}
	if OneOf([]float64{math.Inf(-1), 0}).Form != FormOneOf {
		t.Errorf("OneOf([-Inf, 0]).Form mismatch")
	}
	mustPanicWith(t, "NaN", func() { AtLeast(math.NaN()) })
	mustPanicWith(t, "NaN", func() { OneOf([]float64{1, math.NaN()}) })
}

func mustPanicWith(t *testing.T, substr string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected a panic containing %q, got none", substr)
			return
		}
		msg, ok := r.(string)
		if !ok || !contains(msg, substr) {
			t.Errorf("panic %v does not contain %q", r, substr)
		}
	}()
	fn()
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFoldRayFormsKeepsTightestRayPerClassDropsNone(t *testing.T) {
	// atLeast(0) && atLeast(5) && atMost(100) && atMost(50) -> two rays
	got := FoldRayForms([]Refinement{AtLeast(0), AtLeast(5), AtMost(100), AtMost(50)})
	want := []Refinement{AtLeast(5), AtMost(50)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FoldRayForms(mixed rays) = %+v, want %+v", got, want)
	}
	// the strict form wins its tie
	got = FoldRayForms([]Refinement{AtLeast(3), Above(3)})
	want = []Refinement{Above(3)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FoldRayForms(tie) = %+v, want %+v", got, want)
	}
	// a lone vacuous ray SURVIVES -- the scalar anchor of R-bar
	got = FoldRayForms([]Refinement{AtLeast(math.Inf(-1))})
	want = []Refinement{AtLeast(math.Inf(-1))}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FoldRayForms(vacuous) = %+v, want %+v", got, want)
	}
	// non-ray forms pass through in order
	got = FoldRayForms([]Refinement{Integer, AtLeast(0), OneOf([]float64{1, 2}), AtLeast(4)})
	want = []Refinement{AtLeast(4), Integer, OneOf([]float64{1, 2})}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FoldRayForms(non-ray passthrough) = %+v, want %+v", got, want)
	}
}

func TestMultipleOfRefusesZeroAndNonFiniteDivisors(t *testing.T) {
	mustPanicWith(t, "d != 0", func() { MultipleOf(0) })
	mustPanicWith(t, "finite", func() { MultipleOf(math.Inf(1)) })
	mustPanicWith(t, "finite", func() { MultipleOf(math.NaN()) })
}

func TestWordOfReadsTheCanonicalSingletonShapes(t *testing.T) {
	got, ok := WordOf(MakeRefinedSet(EmptyTuple))
	if !ok || !reflect.DeepEqual(got, []float64(nil)) {
		t.Errorf("WordOf(emptyTuple) = %v, %v, want [], true", got, ok)
	}
	got, ok = WordOf(MakeRefinedSet(OneOf([]float64{7})))
	if !ok || !reflect.DeepEqual(got, []float64{7}) {
		t.Errorf("WordOf(oneOf(7)) = %v, %v, want [7], true", got, ok)
	}
	abc := StringTuple("abc")
	got, ok = WordOf(abc)
	if !ok || !reflect.DeepEqual(got, CodepointsOf("abc")) {
		t.Errorf("WordOf(stringTuple(abc)) = %v, %v", got, ok)
	}
	got, ok = WordOf(StringTuple(""))
	if !ok || len(got) != 0 {
		t.Errorf("WordOf(stringTuple(\"\")) = %v, %v, want [], true", got, ok)
	}
	// a long right-nested literal reads without recursion depth limits
	long := repeatString("a", 20000)
	got, ok = WordOf(StringTuple(long))
	if !ok || !reflect.DeepEqual(got, CodepointsOf(long)) {
		t.Errorf("WordOf(long stringTuple) failed")
	}
	// left-nested concatenation reads in order too
	nested := MakeRefinedSet(Concatenation(
		MakeRefinedSet(Concatenation(MakeRefinedSet(OneOf([]float64{1})), MakeRefinedSet(OneOf([]float64{2})))),
		MakeRefinedSet(OneOf([]float64{3})),
	))
	got, ok = WordOf(nested)
	if !ok || !reflect.DeepEqual(got, []float64{1, 2, 3}) {
		t.Errorf("WordOf(left-nested concat) = %v, %v, want [1 2 3], true", got, ok)
	}
}

func TestWordOfStaysNullOffTheSingletonShapes(t *testing.T) {
	if _, ok := WordOf(MakeRefinedSet(OneOf([]float64{1, 2}))); ok {
		t.Errorf("WordOf(oneOf(1,2)) should not resolve")
	}
	if _, ok := WordOf(MakeRefinedSet(Star(Numbers))); ok {
		t.Errorf("WordOf(star(numbers)) should not resolve")
	}
	if _, ok := WordOf(MakeRefinedSet(AtLeast(0))); ok {
		t.Errorf("WordOf(atLeast(0)) should not resolve")
	}
	if _, ok := WordOf(MakeRefinedSet(OneOf([]float64{1}), Integer)); ok {
		t.Errorf("WordOf(oneOf(1), integer) should not resolve")
	}
	if _, ok := WordOf(MakeRefinedSet(Concatenation(MakeRefinedSet(OneOf([]float64{1})), Numbers))); ok {
		t.Errorf("WordOf(concat with numbers) should not resolve")
	}
	// adversarial: non-singletons buried at any depth stay null -- a
	// misread here would answer a subset question as the wrong
	// membership question
	adversarial := MakeRefinedSet(Concatenation(
		MakeRefinedSet(OneOf([]float64{1})),
		MakeRefinedSet(Concatenation(MakeRefinedSet(OneOf([]float64{2, 3})), MakeRefinedSet(OneOf([]float64{4})))),
	))
	if _, ok := WordOf(adversarial); ok {
		t.Errorf("WordOf(adversarial) should not resolve")
	}
	// an empty oneOf is the VOID, not the empty word
	if _, ok := WordOf(MakeRefinedSet(OneOf([]float64{}))); ok {
		t.Errorf("WordOf(oneOf()) should not resolve")
	}
	if _, ok := WordOf(MakeRefinedSet(Concatenation(MakeRefinedSet(OneOf([]float64{1})), MakeRefinedSet(OneOf([]float64{}))))); ok {
		t.Errorf("WordOf(concat with empty oneOf) should not resolve")
	}
	// a multi-form set is an intersection, even of singleton forms
	if _, ok := WordOf(MakeRefinedSet(EmptyTuple, EmptyTuple)); ok {
		t.Errorf("WordOf(emptyTuple, emptyTuple) should not resolve")
	}
	if _, ok := WordOf(MakeRefinedSet(Concatenation(
		MakeRefinedSet(OneOf([]float64{1})),
		MakeRefinedSet(EmptyTuple, EmptyTuple),
	))); ok {
		t.Errorf("WordOf(concat with multi-form) should not resolve")
	}
	// a repeat window is not a word even at fixed length
	two := 2
	if _, ok := WordOf(MakeRefinedSet(Refinement{Form: FormRepeat, A_: ptrSet(MakeRefinedSet(OneOf([]float64{1}))), Lo: 2, Hi: &two})); ok {
		t.Errorf("WordOf(repeat window) should not resolve")
	}
}

func ptrSet(s RefinedSet) *RefinedSet {
	return &s
}

func repeatString(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

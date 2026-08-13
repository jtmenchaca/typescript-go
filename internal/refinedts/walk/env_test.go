package walk

import (
	"fmt"
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// literal is a stand-in AbstractValue distinguishable by its number.
func envTestLiteral(n float64) abstractdomain.AbstractValue {
	return abstractdomain.KnownValues(
		[]float64{n},
		abstractdomain.PrimitiveNumber,
		abstractdomain.TrustProved,
	)
}

func envTestMustGet(t *testing.T, env Env, name string) abstractdomain.AbstractValue {
	t.Helper()
	v, ok := env.Get(name)
	if !ok {
		t.Fatalf("expected %q to be bound", name)
	}
	return v
}

func TestEnvSetGetLen(t *testing.T) {
	env := NewEnv()
	if env.Len() != 0 {
		t.Fatalf("a new env holds %d names, want 0", env.Len())
	}
	env.Set("a", envTestLiteral(1))
	env.Set("b", envTestLiteral(2))
	env.Set("a", envTestLiteral(3))
	if env.Len() != 2 {
		t.Fatalf("env holds %d names, want 2", env.Len())
	}
	if !abstractdomain.SameKnown(envTestMustGet(t, env, "a"), envTestLiteral(3)) {
		t.Fatalf("a did not take its second write")
	}
	if _, ok := env.Get("missing"); ok {
		t.Fatalf("an unbound name reported as bound")
	}
}

// Two references to ONE handle see each other's writes — the
// call-by-sharing the plain map had.
func TestEnvAliasingThroughOneHandle(t *testing.T) {
	env := NewEnv()
	alias := env
	env.Set("x", envTestLiteral(1))
	if !abstractdomain.SameKnown(envTestMustGet(t, alias, "x"), envTestLiteral(1)) {
		t.Fatalf("the alias did not see the write")
	}
	alias.Set("y", envTestLiteral(2))
	if !abstractdomain.SameKnown(envTestMustGet(t, env, "y"), envTestLiteral(2)) {
		t.Fatalf("the original did not see the alias's write")
	}
	alias.Delete("x")
	if _, ok := env.Get("x"); ok {
		t.Fatalf("the original still held a name the alias deleted")
	}
}

// After Clone the two handles are independent: neither observes the
// other's writes, and both keep everything written before the clone.
func TestEnvCloneIndependence(t *testing.T) {
	env := NewEnv()
	env.Set("shared", envTestLiteral(1))
	clone := env.Clone()

	env.Set("only-original", envTestLiteral(2))
	clone.Set("only-clone", envTestLiteral(3))
	env.Set("shared", envTestLiteral(4))

	if !abstractdomain.SameKnown(envTestMustGet(t, clone, "shared"), envTestLiteral(1)) {
		t.Fatalf("the clone saw the original's rewrite of shared")
	}
	if !abstractdomain.SameKnown(envTestMustGet(t, env, "shared"), envTestLiteral(4)) {
		t.Fatalf("the original lost its own rewrite of shared")
	}
	if _, ok := clone.Get("only-original"); ok {
		t.Fatalf("the clone saw a name written to the original after cloning")
	}
	if _, ok := env.Get("only-clone"); ok {
		t.Fatalf("the original saw a name written to the clone after cloning")
	}
	if env.Len() != 2 || clone.Len() != 2 {
		t.Fatalf("counts diverged: original %d, clone %d, want 2 and 2", env.Len(), clone.Len())
	}

	clone.Delete("shared")
	if _, ok := env.Get("shared"); !ok {
		t.Fatalf("the clone's delete reached the original")
	}
}

// Enough names to push the trie past one level, checking that every
// one survives and that deletes remove exactly their own.
func TestEnvManyNames(t *testing.T) {
	env := NewEnv()
	const total = 300
	for i := 0; i < total; i++ {
		env.Set(fmt.Sprintf("name%03d", i), envTestLiteral(float64(i)))
	}
	if env.Len() != total {
		t.Fatalf("env holds %d names, want %d", env.Len(), total)
	}
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("name%03d", i)
		if !abstractdomain.SameKnown(envTestMustGet(t, env, name), envTestLiteral(float64(i))) {
			t.Fatalf("%s lost its value", name)
		}
	}
	clone := env.Clone()
	for i := 0; i < total; i += 2 {
		env.Delete(fmt.Sprintf("name%03d", i))
	}
	if env.Len() != total/2 {
		t.Fatalf("after deleting the evens the env holds %d names, want %d", env.Len(), total/2)
	}
	if clone.Len() != total {
		t.Fatalf("the clone lost names to the original's deletes: %d, want %d", clone.Len(), total)
	}
	for i := 1; i < total; i += 2 {
		name := fmt.Sprintf("name%03d", i)
		if _, ok := env.Get(name); !ok {
			t.Fatalf("%s was deleted though it is odd", name)
		}
	}
}

// Range visits in sorted name order, and the order does not depend on
// the order the names were written in.
func TestEnvRangeSorted(t *testing.T) {
	written := []string{"zeta", "alpha", "mid", "b", "Alpha", "a.b", "a"}
	env := NewEnv()
	for i, name := range written {
		env.Set(name, envTestLiteral(float64(i)))
	}
	var seen []string
	env.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		seen = append(seen, name)
		return true
	})
	want := []string{"Alpha", "a", "a.b", "alpha", "b", "mid", "zeta"}
	if len(seen) != len(want) {
		t.Fatalf("Range visited %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("Range visited %v, want %v", seen, want)
		}
	}

	other := NewEnv()
	for i := len(written) - 1; i >= 0; i-- {
		other.Set(written[i], envTestLiteral(float64(i)))
	}
	var again []string
	other.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		again = append(again, name)
		return true
	})
	for i := range want {
		if again[i] != want[i] {
			t.Fatalf("insertion order changed the visit order: %v vs %v", again, seen)
		}
	}
}

// Returning false stops the visit.
func TestEnvRangeStops(t *testing.T) {
	env := NewEnv()
	for _, name := range []string{"a", "b", "c", "d"} {
		env.Set(name, envTestLiteral(1))
	}
	var seen []string
	env.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		seen = append(seen, name)
		return name != "b"
	})
	if len(seen) != 2 || seen[0] != "a" || seen[1] != "b" {
		t.Fatalf("Range visited %v, want [a b]", seen)
	}
}

// Deleting and writing DURING a Range is safe: the visit runs over the
// snapshot taken when Range started, and the writes stand afterwards.
func TestEnvRangeMutationDuringVisit(t *testing.T) {
	env := NewEnv()
	for _, name := range []string{"a", "b", "c", "d"} {
		env.Set(name, envTestLiteral(1))
	}
	var seen []string
	env.Range(func(name string, _ abstractdomain.AbstractValue) bool {
		seen = append(seen, name)
		env.Delete(name)
		env.Set("added-"+name, envTestLiteral(2))
		return true
	})
	if len(seen) != 4 {
		t.Fatalf("Range visited %v, want all four original names", seen)
	}
	for _, name := range []string{"a", "b", "c", "d"} {
		if _, ok := env.Get(name); ok {
			t.Fatalf("%s survived its delete", name)
		}
		if _, ok := env.Get("added-" + name); !ok {
			t.Fatalf("added-%s did not survive the range", name)
		}
	}
	if env.Len() != 4 {
		t.Fatalf("env holds %d names after the range, want 4", env.Len())
	}
}

// A nil handle reads as empty, the way indexing a nil map did.
func TestEnvNilHandleReads(t *testing.T) {
	var env Env
	if _, ok := env.Get("anything"); ok {
		t.Fatalf("a nil env reported a binding")
	}
	if env.Len() != 0 {
		t.Fatalf("a nil env has length %d, want 0", env.Len())
	}
	visited := false
	env.Range(func(string, abstractdomain.AbstractValue) bool {
		visited = true
		return true
	})
	if visited {
		t.Fatalf("a nil env visited a binding")
	}
	if env.Names() != nil {
		t.Fatalf("a nil env answered names")
	}
	if env.Clone().Len() != 0 {
		t.Fatalf("cloning a nil env did not answer an empty env")
	}
}

// Writing through a nil handle panics, as writing a nil map did.
func TestEnvNilHandleWritePanics(t *testing.T) {
	var env Env
	defer func() {
		if recover() == nil {
			t.Fatalf("writing to a nil env did not panic")
		}
	}()
	env.Set("x", envTestLiteral(1))
}

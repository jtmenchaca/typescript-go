// The predicate honesty gate: StatedPredicateNarrowings believes a
// declared `v is T` only to the extent the predicate's own body
// proves it (predicateClaimProven, predicate_narrowings.go). Four
// pins: an unreadable body is never believed, a readable body that
// proves the claim IS believed, a readable body that proves LESS than
// the claim is never believed (the existing dishonestPredicate shape
// — pinned here to confirm the gate leaves it unchanged), and a
// kernel decline is never believed either.
package narrowing

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// ageWindowSource wraps one predicate declaration and a guarded call
// site around it, with `Age` the same `0 <= v <= 120` integer window
// the fixture uses — small enough for the kernel to decide outright.
func ageWindowSource(predicate string) string {
	return `
type Age = number & { __brand: "Age" };
function isInAgeWindow(v: number): v is Age {
  return Number.isInteger(v) && v >= 0 && v <= 120;
}
` + predicate + `
function guarded(value: number) {
  if (claim(value)) {
    return value;
  }
  return 0;
}
`
}

// firstCallNamed is the first CallExpression in the file whose callee
// is the given identifier — the guard condition's own call, in every
// source this file builds.
func firstCallNamed(file *ast.SourceFile, name string) *ast.Node {
	var found *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if found != nil {
			return true
		}
		if ast.IsCallExpression(node) {
			callee := node.AsCallExpression().Expression
			if ast.IsIdentifier(callee) && callee.Text() == name {
				found = node
				return true
			}
		}
		node.ForEachChild(visit)
		return found != nil
	}
	file.AsNode().ForEachChild(visit)
	return found
}

// trackAll is the harness's isTracked closure — every name is a
// tracked place, the same blanket ofCondition uses.
func trackAll(string) bool { return true }

func predicateCallNarrowingsAt(t *testing.T, c *checker.Checker, file *ast.SourceFile) (BranchNarrowings, bool) {
	t.Helper()
	call := firstCallNamed(file, "claim")
	if call == nil {
		t.Fatalf("no call to claim(...) found")
	}
	return PredicateCallNarrowings(c, call, trackAll)
}

// statedPredicateNarrowingsAt calls StatedPredicateNarrowings directly
// — the function the honesty gate lives in — bypassing
// PredicateCallNarrowings's own body-first routing (which never
// reaches StatedPredicateNarrowings at all when the body already
// speaks on both sides, e.g. a bare `typeof` test). The kernel-decline
// pin needs this direct call: a body reading that succeeds
// structurally would otherwise make PredicateCallNarrowings return its
// own answer before the gate this test targets ever runs.
func statedPredicateNarrowingsAt(t *testing.T, c *checker.Checker, file *ast.SourceFile) (BranchNarrowings, bool) {
	t.Helper()
	call := firstCallNamed(file, "claim")
	if call == nil {
		t.Fatalf("no call to claim(...) found")
	}
	return StatedPredicateNarrowings(c, call, trackAll)
}

// TestUnreadableBodyClaimNotBelieved is the new fixture shape's own
// rule: a reassigned parameter blocks PredicateBodyBranches entirely
// (no proof to compare), so the claim is not believed — the guard
// narrows nothing on `value`, the same verdict a plain `false` return
// from PredicateCallNarrowings gives its caller.
func TestUnreadableBodyClaimNotBelieved(t *testing.T) {
	loadNarrowKernel(t)
	source := ageWindowSource(`
function claim(v: number): v is Age {
  v = v;
  return true;
}
`)
	c, file := checkerFor(t, source)
	branches, ok := predicateCallNarrowingsAt(t, c, file)
	if ok && (len(branches.WhenTrue) > 0 || len(branches.WhenFalse) > 0) {
		t.Fatalf("unreadable body: got ok=%v branches=%+v, want no narrowing at all (claim not believed, body not readable)", ok, branches)
	}
}

// TestReadableProvenClaimBelieved is the honest single-expression
// shape's own body-reading channel already narrows to the SAME set
// the predicate claims — proving the gate does not need the declared-
// predicate fallback to believe an honest claim (PredicateBodyBranches
// alone determines it). This pins that the body path still works
// UNTOUCHED by the new gate in StatedPredicateNarrowings, which this
// call never even reaches (the body already spoke on both sides).
func TestReadableProvenClaimBelieved(t *testing.T) {
	loadNarrowKernel(t)
	source := ageWindowSource(`
function claim(v: number): v is Age {
  return Number.isInteger(v) && v >= 0 && v <= 120;
}
`)
	c, file := checkerFor(t, source)
	branches, ok := predicateCallNarrowingsAt(t, c, file)
	if !ok || len(branches.WhenTrue) == 0 {
		t.Fatalf("readable, proven body: got ok=%v branches=%+v, want a believed whenTrue narrowing", ok, branches)
	}
}

// TestReadableUnprovenClaimNotBelieved is the existing dishonest-
// predicate shape (f-type-nodes.ts's claimsAge/dishonestPredicate,
// line 233): the body READS (PredicateBodyBranches succeeds), but
// proves only `typeof v === "number"` — strictly less than Age's
// window. The body's own narrowing (a bare number ground) is what
// PredicateCallNarrowings returns; StatedPredicateNarrowings's
// declared-Age claim is never substituted in, because the body
// already spoke on both sides at line 89 of predicate_narrowings.go.
// Pinned here to confirm the new gate changes nothing about this row:
// the narrowed shape must still be the untightened number ground, not
// Age's own window.
func TestReadableUnprovenClaimNotBelieved(t *testing.T) {
	loadNarrowKernel(t)
	source := ageWindowSource(`
function claim(v: number): v is Age {
  return typeof v === "number";
}
`)
	c, file := checkerFor(t, source)
	branches, ok := predicateCallNarrowingsAt(t, c, file)
	if !ok || len(branches.WhenTrue) != 1 || !branches.WhenTrue[0].HasShape {
		t.Fatalf("readable, unproven body: got ok=%v branches=%+v, want the body's own bare-number shape", ok, branches)
	}
	// the believed shape must be the body's own untightened `typeof v
	// === "number"` ground — abstractdomain.KindPossiblyNaN wrapping the
	// whole Numbers set — never Age's own tighter window (a plain
	// KindSet carrying the integer/0/120 forms, no NaN wrapper at all).
	// A KindSet here would mean the declared predicate leaked in past
	// the gate on an unproven claim.
	if shape := branches.WhenTrue[0].Shape; shape.Kind != abstractdomain.KindPossiblyNaN {
		t.Fatalf("readable, unproven body: whenTrue shape kind = %v, want KindPossiblyNaN (the body's own bare number ground, not Age's tighter window)", shape.Kind)
	}
}

// TestKernelDeclineClaimNotBelieved is the refusal-posture pin,
// against StatedPredicateNarrowings directly (see
// statedPredicateNarrowingsAt's doc: PredicateCallNarrowings would
// never reach the gate for this body, since typeof reads
// structurally and answers on its own before StatedPredicateNarrowings
// is ever consulted). Even a claim whose OWN declared type is the
// bare-number ground the body already proves cannot be believed
// without the kernel to confirm the ScalarSubset comparison — a
// decline proves nothing either way, and believing on no evidence is
// exactly the unsoundness the gate exists to close. This is the one
// pin that must NOT call loadNarrowKernel; it explicitly clears the
// kernel and restores whatever the package held afterward (Go runs a
// package's tests sequentially by default; none here call
// t.Parallel).
func TestKernelDeclineClaimNotBelieved(t *testing.T) {
	restore := NarrowKernel()
	SetNarrowKernel(nil)
	t.Cleanup(func() { SetNarrowKernel(restore) })
	source := `
type Age = number;
function claim(v: unknown): v is Age {
  return typeof v === "number";
}
function guarded(value: unknown) {
  if (claim(value)) {
    return value;
  }
  return 0;
}
`
	c, file := checkerFor(t, source)
	branches, ok := statedPredicateNarrowingsAt(t, c, file)
	if ok && (len(branches.WhenTrue) > 0 || len(branches.WhenFalse) > 0) {
		t.Fatalf("kernel decline: got ok=%v branches=%+v, want no narrowing — a proof the walk cannot check is not evidence", ok, branches)
	}
}

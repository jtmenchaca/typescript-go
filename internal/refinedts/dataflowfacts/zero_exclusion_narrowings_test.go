package dataflowfacts

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

func integerWindow(lo, hi float64) abstractdomain.AbstractValue {
	return abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(lo), refinementsets.AtMost(hi)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)
}

func TestZeroExclusionNarrowings_LowEdgeAtZeroRetreatsToOne(t *testing.T) {
	_, file := checkerFor(t, `
function f(x: number) {
  if (x !== 0) {
    return x;
  }
  return 0;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	env := map[string]abstractdomain.AbstractValue{"x": integerWindow(0, 200)}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := ZeroExclusionNarrowings(held, condition, false)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 — a held x !== 0 over [0,200] integer retreats the low edge", len(rows))
	}
	lo, hi, isInt := closedIntegerEdges(rows[0].Known.Set)
	if !isInt || lo == nil || *lo != 1 || hi == nil || *hi != 200 {
		t.Errorf("rows[0].Known.Set = %+v, want [1,200] integer", rows[0].Known.Set)
	}
}

func TestZeroExclusionNarrowings_HighEdgeAtZeroRetreatsToNegativeOne(t *testing.T) {
	_, file := checkerFor(t, `
function f(x: number) {
  if (x !== 0) {
    return x;
  }
  return 0;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	env := map[string]abstractdomain.AbstractValue{"x": integerWindow(-200, 0)}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := ZeroExclusionNarrowings(held, condition, false)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 — a held x !== 0 over [-200,0] integer retreats the high edge", len(rows))
	}
	lo, hi, isInt := closedIntegerEdges(rows[0].Known.Set)
	if !isInt || lo == nil || *lo != -200 || hi == nil || *hi != -1 {
		t.Errorf("rows[0].Known.Set = %+v, want [-200,-1] integer", rows[0].Known.Set)
	}
}

func TestZeroExclusionNarrowings_ExitGuardReadsTheRefutedSideAsHeld(t *testing.T) {
	// the exit-guard shape: `if (x === 0) return;` — the continuation
	// after the exit runs where the ORIGINAL condition is FALSE, i.e.
	// x === 0 refuted, exactly like LengthGuardNarrowings' and
	// MapPresenceNarrowings' own exit-guard call (negated=true).
	_, file := checkerFor(t, `
function f(x: number) {
  if (x === 0) {
    return 0;
  }
  return x;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	env := map[string]abstractdomain.AbstractValue{"x": integerWindow(0, 200)}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := ZeroExclusionNarrowings(held, condition, true)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 — refuting x === 0 proves x !== 0 past the exit", len(rows))
	}
	lo, _, isInt := closedIntegerEdges(rows[0].Known.Set)
	if !isInt || lo == nil || *lo != 1 {
		t.Errorf("rows[0].Known.Set = %+v, want low edge 1", rows[0].Known.Set)
	}
}

func TestZeroExclusionNarrowings_InteriorZeroNarrowsNothing(t *testing.T) {
	_, file := checkerFor(t, `
function f(x: number) {
  if (x !== 0) {
    return x;
  }
  return 0;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	// 0 sits strictly INSIDE [-10, 10] — no single window spells the
	// puncture, so the weaker unpunctured claim stays exactly as sound
	env := map[string]abstractdomain.AbstractValue{"x": integerWindow(-10, 10)}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := ZeroExclusionNarrowings(held, condition, false)
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 — an interior zero has no single-window puncture, got %+v", len(rows), rows)
	}
}

func TestZeroExclusionNarrowings_NonIntegerSortNarrowsNothing(t *testing.T) {
	_, file := checkerFor(t, `
function f(x: number) {
  if (x !== 0) {
    return x;
  }
  return 0;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	// [0, 1] with no Integer form — a continuous window has no "one
	// step" to retreat by
	nonInteger := abstractdomain.KnownSet(
		refinementsets.MakeRefinedSet(refinementsets.AtLeast(0), refinementsets.AtMost(1)),
		nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone,
	)
	env := map[string]abstractdomain.AbstractValue{"x": nonInteger}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := ZeroExclusionNarrowings(held, condition, false)
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 — a non-integer sort has no discrete step to retreat by, got %+v", len(rows), rows)
	}
}

func TestZeroExclusionNarrowings_ExactValuesListDropsZero(t *testing.T) {
	_, file := checkerFor(t, `
function f(x: number) {
  if (x !== 0) {
    return x;
  }
  return 0;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	exact := abstractdomain.KnownValues([]float64{0, 1, 2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
	env := map[string]abstractdomain.AbstractValue{"x": exact}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	rows := ZeroExclusionNarrowings(held, condition, false)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 — a held x !== 0 drops the zero entry", len(rows))
	}
	want := []float64{1, 2}
	if len(rows[0].Known.Values) != len(want) {
		t.Fatalf("rows[0].Known.Values = %v, want %v", rows[0].Known.Values, want)
	}
	for i, v := range want {
		if rows[0].Known.Values[i] != v {
			t.Errorf("rows[0].Known.Values = %v, want %v", rows[0].Known.Values, want)
		}
	}
}

func TestZeroExclusionNarrowings_HeldEqualsZeroExcludesNothing(t *testing.T) {
	_, file := checkerFor(t, `
function f(x: number) {
  if (x === 0) {
    return x;
  }
  return 0;
}
`)
	fnDecl := file.Statements.Nodes[0]
	ifStmt := fnDecl.AsFunctionDeclaration().Body.AsBlock().Statements.Nodes[0].AsIfStatement()
	condition := ifStmt.Expression

	env := map[string]abstractdomain.AbstractValue{"x": integerWindow(0, 200)}
	held := func(name string) (abstractdomain.AbstractValue, bool) {
		v, ok := env[name]
		return v, ok
	}

	// a held x === 0 (leafNegated=false, isEquals) proves the OPPOSITE
	// of exclusion — nothing narrows here
	rows := ZeroExclusionNarrowings(held, condition, false)
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 — a held x === 0 proves x could be 0, got %+v", len(rows), rows)
	}
}

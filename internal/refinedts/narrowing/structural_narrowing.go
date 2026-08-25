// Structural narrowings: definedness, exact pins, and the
// condition-tree fold that composes them.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// AbsentLiteral is absentLiteral in the TS source: the absent value in
// source: `null`, or the global `undefined` — which is an INTRINSIC (no
// lib declaration), so the check is that the name does not resolve to
// any user-written binding.
func AbsentLiteral(c *checker.Checker, e *ast.Node) bool {
	if e.Kind == ast.KindNullKeyword {
		return true
	}
	if !ast.IsIdentifier(e) || e.Text() != "undefined" {
		return false
	}
	symbol := c.GetSymbolAtLocation(e)
	if symbol == nil {
		return true
	}
	for _, d := range symbol.Declarations {
		if !ast.GetSourceFileOfNode(d).AsSourceFile().IsDeclarationFile {
			return false
		}
	}
	return true
}

// StructuralRaw is structuralRaw in the TS source.
func StructuralRaw(c *checker.Checker, condition *ast.Node, isTracked func(name string) bool) BranchNarrowings {
	// the connectives come from the SHARED condition tree
	// (finding 16): && holds both truths, || both falsities, a
	// negated leaf swaps its sides — the composition the old walk
	// performed, now over the one resolved tree. De Morgan commutes
	// with this composition, so pushing ! onto the leaves changes no
	// answer.
	var fold func(t conditiontree.ConditionTree) BranchNarrowings
	fold = func(t conditiontree.ConditionTree) BranchNarrowings {
		if t.Kind == conditiontree.ConditionTreeAnd {
			a := fold(*t.A)
			b := fold(*t.B)
			return BranchNarrowings{WhenTrue: append(append([]Narrowed{}, a.WhenTrue...), b.WhenTrue...)}
		}
		if t.Kind == conditiontree.ConditionTreeOr {
			a := fold(*t.A)
			b := fold(*t.B)
			return BranchNarrowings{WhenFalse: append(append([]Narrowed{}, a.WhenFalse...), b.WhenFalse...)}
		}
		leaf := StructuralLeaf(c, t.Test, isTracked)
		if t.Negated {
			return BranchNarrowings{WhenTrue: leaf.WhenFalse, WhenFalse: leaf.WhenTrue}
		}
		return leaf
	}
	tree := conditiontree.ConditionTreeOf(condition, false)
	return fold(tree)
}

// StructuralLeaf is structuralLeaf in the TS source: one structural
// LEAF — every test below reads a single position, with `await` peeled
// here (peeling can expose structure the shared tree could not see,
// which re-enters the fold).
//
// Every place read here goes through TrackedPlaceOfWith, so the checker
// resolves a const-bound index the same way the literal spelling reads:
// `const i = 0; if (xs[i] === "a")` names the place `xs.[0]`, which is
// the place `xs[0]` names. The narrowing that lands on it is sound on
// the same terms as the literal one — the place is FIXED, because a
// const cannot be rebound, so the segment names one slot at every
// reachable point; and stability rides the BASE NAME, because any
// element write to the base kills every index place of that base
// (StableIn's reading). Resolving the index therefore adds places
// without weakening what holds of them.
func StructuralLeaf(c *checker.Checker, test *ast.Node, isTracked func(name string) bool) BranchNarrowings {
	e := Peeled(test)
	if e != test {
		return StructuralRaw(c, e, isTracked)
	}

	if ast.IsCallExpression(e) {
		call := e.AsCallExpression()
		// `Boolean(x)` in test position IS the test of x: a condition
		// applies ToBoolean anyway (sec-toboolean), so the default
		// library's Boolean adds nothing and the read re-enters the
		// full fold on the argument
		if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "Boolean" &&
			call.Arguments != nil && len(call.Arguments.Nodes) == 1 &&
			resolvesToDefaultLib(c, call.Expression) {
			return StructuralRaw(c, call.Arguments.Nodes[0], isTracked)
		}
		if fromArray, ok := ArrayShapeLeaf(c, e, isTracked); ok {
			return fromArray
		}
	}

	if ast.IsBinaryExpression(e) {
		bin := e.AsBinaryExpression()
		op := bin.OperatorToken.Kind
		// `x ?? d` with a FALSY fallback reads as the bare test of x:
		// absence lands on the fallback, whose falsity joins x's own —
		// truth proves x present and truthy, falsity proves x absent or
		// falsy, exactly the bare-place reading of x
		if op == ast.KindQuestionQuestionToken {
			isFalsyFallback := bin.Right.Kind == ast.KindFalseKeyword ||
				(ast.IsNumericLiteral(bin.Right) && numericTextIsZero(bin.Right)) ||
				stringLiteralIsEmpty(bin.Right) ||
				AbsentLiteral(c, bin.Right)
			if isFalsyFallback {
				return StructuralRaw(c, bin.Left, isTracked)
			}
			// `x ?? d` where x's TYPE excludes absence is a dead fallback:
			// the condition IS the bare test of x (the ?? never falls
			// through), so x reads as it would alone
			leftType := c.GetTypeAtLocation(bin.Left)
			const absentFlags = checker.TypeFlagsUndefined | checker.TypeFlagsNull | checker.TypeFlagsVoid
			var parts []*checker.Type
			if leftType.IsUnion() {
				parts = leftType.Types()
			} else {
				parts = []*checker.Type{leftType}
			}
			admitsAbsence := (leftType.Flags() & (checker.TypeFlagsAny | checker.TypeFlagsUnknown)) != 0
			if !admitsAbsence {
				for _, part := range parts {
					if (part.Flags() & absentFlags) != 0 {
						admitsAbsence = true
						break
					}
				}
			}
			if !admitsAbsence {
				return StructuralRaw(c, bin.Left, isTracked)
			}
		}
		// `"k" in o` held FALSE proves the key is nowhere on o's chain,
		// so reading it yields undefined (HasProperty walks the chain).
		// Held TRUE proves o is an OBJECT carrying that key — but not a
		// defined value under it, since `{k: undefined}` answers true.
		if op == ast.KindInKeyword {
			key := dataflowfacts.StringLiteralOf(bin.Left)
			target := dataflowfacts.TrackedPlaceOfWith(c, bin.Right, isTracked)
			if key == nil || target == nil {
				return None
			}
			absent := Narrowed{
				Binding:     target.Binding,
				Path:        append(append([]string{}, target.Path...), *key),
				Definedness: "undefined",
			}
			carrying := Narrowed{
				Binding: target.Binding,
				Path:    target.Path,
				Shape: abstractdomain.KnownObject(
					[]abstractdomain.ObjectKey{{Name: *key, Value: silence.Residue()}},
					nil, false, abstractdomain.TrustSpec, false,
				),
				HasShape: true,
			}
			return BranchNarrowings{WhenTrue: []Narrowed{carrying}, WhenFalse: []Narrowed{absent}}
		}

		if fromInstance, ok := InstanceofLeaf(c, e, isTracked); ok {
			return fromInstance
		}

		if fromTypeof, ok := TypeofLeaf(c, e, isTracked); ok {
			return fromTypeof
		}

		isEquals := op == ast.KindEqualsEqualsEqualsToken
		isNotEquals := op == ast.KindExclamationEqualsEqualsToken
		isLooseEquals := op == ast.KindEqualsEqualsToken
		isLooseNotEquals := op == ast.KindExclamationEqualsToken
		if !isEquals && !isNotEquals && !isLooseEquals && !isLooseNotEquals {
			return None
		}

		leftPlace := dataflowfacts.TrackedPlaceOfWith(c, bin.Left, isTracked)
		rightPlace := dataflowfacts.TrackedPlaceOfWith(c, bin.Right, isTracked)
		testedPlace := leftPlace
		if testedPlace == nil {
			testedPlace = rightPlace
		}
		if testedPlace == nil {
			return None
		}
		otherSide := bin.Right
		if leftPlace == nil {
			otherSide = bin.Left
		}

		// an absence test — a real runtime test both ways. The LOOSE
		// forms read here too: `v == null` is true exactly of undefined
		// and null (the spec's own equivalence), which is the model's
		// absent marker either way. (AbsenceTestPlace below is the same
		// recognizer exposed for the cross-channel disjunction reader.)
		if AbsentLiteral(c, otherSide) {
			absent := Narrowed{Binding: testedPlace.Binding, Path: testedPlace.Path, Definedness: "undefined"}
			present := Narrowed{Binding: testedPlace.Binding, Path: testedPlace.Path, Definedness: "defined"}
			if isEquals || isLooseEquals {
				return BranchNarrowings{WhenTrue: []Narrowed{absent}, WhenFalse: []Narrowed{present}}
			}
			return BranchNarrowings{WhenTrue: []Narrowed{present}, WhenFalse: []Narrowed{absent}}
		}
		// past the absence test, loose equality coerces — nothing else
		// is read from it
		if isLooseEquals || isLooseNotEquals {
			return None
		}

		// a held string equality pins the exact tuple
		if s := dataflowfacts.StringLiteralOf(otherSide); s != nil {
			equalTo := Narrowed{
				Binding: testedPlace.Binding, Path: testedPlace.Path,
				Exact: refinementsets.CodepointsOf(*s), ExactSort: abstractdomain.PrimitiveString,
			}
			if isEquals {
				return BranchNarrowings{WhenTrue: []Narrowed{equalTo}}
			}
			return BranchNarrowings{WhenFalse: []Narrowed{equalTo}}
		}
		// a held boolean-literal equality pins the exact word (true ↦ 1,
		// false ↦ 0 — the boolean sort's two values).
		//
		// The FAILING side excludes that word. Strict inequality with a
		// boolean also holds for every non-boolean value (`"x" !== false`
		// is true), so the exclusion states no set fact on its own — it is
		// carried as ExcludesBooleanWord, which applies only where the
		// held value is already known boolean-sorted and passes every
		// other shape untouched. On a `b: boolean`, that is what makes
		// `b !== false` prove b is exactly true.
		var b float64
		hasB := false
		if otherSide.Kind == ast.KindTrueKeyword {
			b, hasB = 1, true
		} else if otherSide.Kind == ast.KindFalseKeyword {
			b, hasB = 0, true
		}
		if hasB {
			equalTo := Narrowed{
				Binding: testedPlace.Binding, Path: testedPlace.Path,
				Exact: []float64{b}, ExactSort: abstractdomain.PrimitiveBoolean,
			}
			excludes := Narrowed{
				Binding: testedPlace.Binding, Path: testedPlace.Path,
				ExcludesBooleanWord: b, HasExcludesBooleanWord: true,
			}
			if isEquals {
				return BranchNarrowings{WhenTrue: []Narrowed{equalTo}, WhenFalse: []Narrowed{excludes}}
			}
			return BranchNarrowings{WhenTrue: []Narrowed{excludes}, WhenFalse: []Narrowed{equalTo}}
		}
		return None
	}

	// a call as a guard narrows by its callee's inlined body — the
	// general predicate rule: `if (isPct(x))` and `.refine(isPct)`
	// are the same reading
	if ast.IsCallExpression(e) {
		if expanded, ok := PredicateCallNarrowings(c, e, isTracked); ok {
			return expanded
		}
	}

	// a bare place as the whole condition is a truthiness test: held
	// truth proves the value PRESENT (ToBoolean of undefined and null
	// is false), which strips a maybe wrapper, and keeps only the
	// truthy words of a finite list. Held falsity keeps the falsy
	// ones — 0 stays, and so does absence.
	if testedPlace := dataflowfacts.TrackedPlaceOfWith(c, e, isTracked); testedPlace != nil {
		present := Narrowed{Binding: testedPlace.Binding, Path: testedPlace.Path, Definedness: "defined", Truthiness: "truthy"}
		falsy := Narrowed{Binding: testedPlace.Binding, Path: testedPlace.Path, Truthiness: "falsy"}
		return BranchNarrowings{WhenTrue: []Narrowed{present}, WhenFalse: []Narrowed{falsy}}
	}

	return None
}

// AbsenceTestPlace reads `P === undefined` / `P == null` (either
// operand order, loose or strict equality) as the tested tracked
// place — the recognizer the cross-channel disjunction reader
// (condition_analysis.go) shares with the structural pass above.
// (nil, false) for any other shape.
func AbsenceTestPlace(c *checker.Checker, e *ast.Node, isTracked func(name string) bool) (*dataflowfacts.TrackedPlace, bool) {
	bare := Peeled(e)
	if !ast.IsBinaryExpression(bare) {
		return nil, false
	}
	bin := bare.AsBinaryExpression()
	op := bin.OperatorToken.Kind
	if op != ast.KindEqualsEqualsEqualsToken && op != ast.KindEqualsEqualsToken {
		return nil, false
	}
	testedPlace := dataflowfacts.TrackedPlaceOfWith(c, bin.Left, isTracked)
	otherSide := bin.Right
	if testedPlace == nil {
		testedPlace = dataflowfacts.TrackedPlaceOfWith(c, bin.Right, isTracked)
		otherSide = bin.Left
	}
	if testedPlace == nil || !AbsentLiteral(c, otherSide) {
		return nil, false
	}
	return testedPlace, true
}

func numericTextIsZero(e *ast.Node) bool {
	return e.Text() == "0"
}

func stringLiteralIsEmpty(e *ast.Node) bool {
	s := dataflowfacts.StringLiteralOf(e)
	return s != nil && *s == ""
}

// StructuralReason is structuralReason in the TS source: decline
// sentences for truthiness and `in` (finding 9).
func StructuralReason(e *ast.Node) (said string, unsupported bool, ok bool) {
	if ast.IsIdentifier(e) || ast.IsPropertyAccessExpression(e) || ast.IsElementAccessExpression(e) {
		return "a truthiness test the walk cannot read as a tracked place", true, true
	}
	if ast.IsBinaryExpression(e) && e.AsBinaryExpression().OperatorToken.Kind == ast.KindInKeyword {
		return "an in test without a literal key is not read", true, true
	}
	return "", false, false
}

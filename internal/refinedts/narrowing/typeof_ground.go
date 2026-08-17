// The ground a typeof word names, the structural leaf that wears
// or sheds it, and the decline sentences for typeof tests.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// GroundOfTypeofWord is groundOfTypeofWord in the TS source: the ground
// a typeof word names, or (ok=false) where the walk has none for it.
// "object" admits null — its typeof is "object" too
// (sec-typeof-operator) — so it proves an object OR the absent value,
// which is exactly the maybe wrapper.
func GroundOfTypeofWord(word string) (abstractdomain.AbstractValue, bool) {
	switch word {
	case "number":
		return abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)), true
	case "string":
		return abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone), true
	case "boolean":
		return abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustSpec), true
	case "object":
		// arrays answer "object" too, and nothing here pins the brand.
		// sec-typeof-operator-runtime-semantics-evaluation: step 5 sends
		// null to "object" (the historical quirk), step 4 sends undefined
		// to "undefined" — so `typeof x === "object"` proves x is an
		// object OR exactly null, never undefined (typeof undefined is
		// its own, different word). The wrapper's own absent side is
		// therefore NullOnly, not the pre-flavor conflated claim.
		ground := abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false)
		if ground.Kind == abstractdomain.KindObject {
			ground.MaybeArray = true
		}
		return abstractdomain.PossiblyAbsent(ground, abstractdomain.AbsentFlavorNullOnly, "", false, false), true
	case "symbol":
		// a symbol of unknown identity — the sort IS the claim
		return abstractdomain.AbstractValue{Kind: abstractdomain.KindSymbol, Grade: abstractdomain.TrustSpec}, true
	case "function":
		// a function of unknown body — the sort IS the claim
		return abstractdomain.HostFunction, true
	default:
		return abstractdomain.AbstractValue{}, false
	}
}

// TypeofLeaf is typeofLeaf in the TS source: `typeof x === "word"` —
// (nil, false) when the expression is not a typeof comparison; (None,
// true) when the word or place cannot be read. The checker is what
// resolves a const-bound index in the tested place (`const i = 0;
// typeof xs[i]` tests the same slot `xs[0]` does).
func TypeofLeaf(c *checker.Checker, e *ast.Node, isTracked func(name string) bool) (BranchNarrowings, bool) {
	bin := e.AsBinaryExpression()
	op := bin.OperatorToken.Kind
	var typeofSide *ast.Node
	if ast.IsTypeOfExpression(bin.Left) {
		typeofSide = bin.Left
	} else if ast.IsTypeOfExpression(bin.Right) {
		typeofSide = bin.Right
	}
	if typeofSide == nil ||
		(op != ast.KindEqualsEqualsEqualsToken && op != ast.KindEqualsEqualsToken &&
			op != ast.KindExclamationEqualsEqualsToken && op != ast.KindExclamationEqualsToken) {
		return BranchNarrowings{}, false
	}

	var wordSide *ast.Node
	if typeofSide == bin.Left {
		wordSide = bin.Right
	} else {
		wordSide = bin.Left
	}
	word := dataflowfacts.StringLiteralOf(wordSide)
	tested := dataflowfacts.TrackedPlaceOfWith(c, typeofSide.AsTypeOfExpression().Expression, isTracked)
	if word == nil || tested == nil {
		return None, true
	}
	positive := op == ast.KindEqualsEqualsEqualsToken || op == ast.KindEqualsEqualsToken
	if *word == "undefined" {
		absent := Narrowed{Binding: tested.Binding, Path: tested.Path, Definedness: "undefined"}
		present := Narrowed{Binding: tested.Binding, Path: tested.Path, Definedness: "defined"}
		if positive {
			return BranchNarrowings{WhenTrue: []Narrowed{absent}, WhenFalse: []Narrowed{present}}, true
		}
		return BranchNarrowings{WhenTrue: []Narrowed{present}, WhenFalse: []Narrowed{absent}}, true
	}
	ground, ok := GroundOfTypeofWord(*word)
	if !ok {
		return None, true
	}
	wears := Narrowed{Binding: tested.Binding, Path: tested.Path, Shape: ground, HasShape: true}
	// the REFUTED side rules the word's kind out: a present value
	// failing `typeof x === "number"` is not a number, so a kind
	// union sheds its number arms (the words here are exactly
	// the kinds the union buckets)
	sheds := Narrowed{Binding: tested.Binding, Path: tested.Path, ExcludesKind: *word}
	if positive {
		return BranchNarrowings{WhenTrue: []Narrowed{wears}, WhenFalse: []Narrowed{sheds}}, true
	}
	return BranchNarrowings{WhenTrue: []Narrowed{sheds}, WhenFalse: []Narrowed{wears}}, true
}

// TypeofReason is typeofReason in the TS source: decline sentences for
// typeof tests (finding 9). ok=false when the expression is not a
// typeof test at all.
func TypeofReason(e *ast.Node) (said string, unsupported bool, ok bool) {
	if !ast.IsBinaryExpression(e) {
		return "", false, false
	}
	bin := e.AsBinaryExpression()
	if !ast.IsTypeOfExpression(bin.Left) && !ast.IsTypeOfExpression(bin.Right) {
		return "", false, false
	}
	// the recognizer READS typeof (a held word wears its ground, a
	// refuted one sheds the kind arm) — this coverage row fires only
	// where its own gates declined, and the reason is the gate's
	var literal *string
	if ast.IsStringLiteral(bin.Left) {
		text := bin.Left.Text()
		literal = &text
	} else if ast.IsStringLiteral(bin.Right) {
		text := bin.Right.Text()
		literal = &text
	}
	if literal != nil {
		if *literal == "undefined" {
			return "a typeof test on a place the walk does not track", true, true
		}
		if _, groundOk := GroundOfTypeofWord(*literal); groundOk {
			return "a typeof test on a place the walk does not track", true, true
		}
	}
	if literal == nil {
		return "a typeof test against a computed word is not read", true, true
	}
	return "a typeof test for '" + *literal + "' — no ground carries that word", true, true
}

// The refine-predicate reader: a predicate body read into the
// forms it proves about its one bound parameter, with the loosened
// length window a UTF-16 count admits. Split from
// condition_analysis.ts per the v2 tree.

package narrowing

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// lengthBodyWindow is lengthBodyWindow in the TS source: the loosened
// window a `name.length REL k` body proves: the predicate counts UTF-16
// units while the grammar counts scalars (one or two units each), so
// units ≥ k admits scalar counts from ⌈k/2⌉ and units ≤ k admits up to
// k. WIDER than the predicate by construction — the caller must keep
// the annotation UNREAD, so refutations land and acceptances keep the
// alert.
func lengthBodyWindow(body *ast.Node, name string) ([]refinementsets.Refinement, bool) {
	e := body
	for ast.IsParenthesizedExpression(e) {
		e = e.AsParenthesizedExpression().Expression
	}
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	bin := e.AsBinaryExpression()
	lengthSide := func(side *ast.Node) bool {
		return ast.IsPropertyAccessExpression(side) &&
			ast.IsIdentifier(side.AsPropertyAccessExpression().Expression) &&
			side.AsPropertyAccessExpression().Expression.Text() == name &&
			side.AsPropertyAccessExpression().Name().Text() == "length"
	}
	literalSide := func(side *ast.Node) (float64, bool) {
		n, ok := numericLiteralOf(side)
		if !ok || n != math.Trunc(n) || n < 0 {
			return 0, false
		}
		return n, true
	}
	var k float64
	kOk := false
	op := bin.OperatorToken.Kind
	if lengthSide(bin.Left) {
		k, kOk = literalSide(bin.Right)
	} else if lengthSide(bin.Right) {
		k, kOk = literalSide(bin.Left)
		// mirror: k REL len reads as len REL' k
		op = mirrorOp(op)
	}
	if !kOk {
		return nil, false
	}
	var lo, hi float64
	hasHi := false
	switch op {
	case ast.KindGreaterThanEqualsToken:
		lo = math.Ceil(k / 2)
	case ast.KindGreaterThanToken:
		lo = math.Ceil((k + 1) / 2)
	case ast.KindLessThanEqualsToken:
		lo, hi, hasHi = 0, k, true
	case ast.KindLessThanToken:
		if k < 1 {
			return nil, false
		}
		lo, hi, hasHi = 0, k-1, true
	case ast.KindEqualsEqualsEqualsToken:
		lo, hi, hasHi = math.Ceil(k/2), k, true
	default:
		return nil, false
	}
	var hiPtr *int
	if hasHi {
		v := int(hi)
		hiPtr = &v
	}
	return refinementsets.Repetition(refinementsets.Codepoints, int(lo), hiPtr).Forms, true
}

// ReadableRefine is readableRefine in the TS source: read a refine
// predicate into the forms it proves about its one bound parameter —
// the same reading a call-guard gets, kept where every claim is a form
// on the parameter itself. (nil, false) wherever the predicate says
// anything the reader cannot fully carry. Refuting claims are admitted:
// they hold for every value already inside ℝ̄, and a chain's base set
// has only such members.
func ReadableRefine(c *checker.Checker, predicate *ast.Node) ([]refinementsets.Refinement, bool, bool) {
	fn := PinnedFunctionOf(c, predicate)
	if fn == nil {
		return nil, false, false
	}
	body := BodyExpressionOf(fn)
	if body == nil {
		return nil, false, false
	}
	parameters := fn.Parameters()
	if len(parameters) == 0 || !ast.IsIdentifier(parameters[0].Name()) {
		return nil, false, false
	}
	name := parameters[0].Name().Text()
	// a length-comparison body folds LOOSENED (Q1 ruling): the window
	// is wider than the predicate, so the caller keeps the annotation
	// unread — refutations land, acceptances still alert
	if loosened, ok := lengthBodyWindow(body, name); ok {
		return loosened, false, true
	}
	if PredicateReadDepth(c) >= 3 {
		return nil, false, false
	}
	OpenPredicateRead(c)
	branches := NarrowingsOf(c, body, func(candidate string) bool { return candidate == name }, nil, GuardReadNowhere)
	ClosePredicateRead(c)
	if len(branches.WhenTrue) == 0 {
		return nil, false, false
	}
	var forms []refinementsets.Refinement
	for _, n := range branches.WhenTrue {
		if n.Binding != name || len(n.Path) != 0 {
			return nil, false, false
		}
		if n.Definedness != "" || n.Truthiness != "" {
			return nil, false, false
		}
		if n.Exact != nil {
			exactSort := n.ExactSort
			if exactSort == "" {
				exactSort = "number"
			}
			if exactSort != "number" {
				return nil, false, false
			}
			forms = append(forms, refinementsets.OneOf(append([]float64{}, n.Exact...)))
			continue
		}
		if len(n.Forms) == 0 {
			return nil, false, false
		}
		forms = append(forms, n.Forms...)
	}
	return forms, true, true
}

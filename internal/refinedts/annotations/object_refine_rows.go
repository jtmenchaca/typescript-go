// Dependent rows from `.refine((r) => r.hi >= r.lo)`: comparisons
// between keys, per-key forms against literals, and exclusions.
// SanitizeDepends drops rows whose sibling did not survive a mapping.
//
// Ported 1:1 from annotations/object_refine_rows.ts.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// refineRow is one dependent row -- {key, op, param}.
type refineRow struct {
	key   string
	op    string
	param string
}

// RefineReading is RefineReading in the TS source: what a refine
// predicate reads as: dependent ROWS between keys, per-key FORMS (a
// key compared with a numeric literal, an equality, a `% n === 0`),
// and per-key EXCLUSIONS (`!==` a literal).
type RefineReading struct {
	rows          []refineRow
	keyForms      map[string][]refinementsets.Refinement
	keyExclusions map[string][]float64
}

// SanitizeDepends is sanitizeDepends in the TS source: drop dependent
// rows whose sibling key did not survive a key mapping -- the
// relation is unstatable without both ends.
func SanitizeDepends(kept []ObjectKeySpec) []ObjectKeySpec {
	names := map[string]bool{}
	for _, key := range kept {
		names[key.Name] = true
	}
	out := make([]ObjectKeySpec, len(kept))
	for i, key := range kept {
		if key.Value.Kind == KeyValueSet && key.Value.Depends != nil {
			var filtered []DependentBound
			for _, dep := range key.Value.Depends {
				if names[dep.Param] {
					filtered = append(filtered, dep)
				}
			}
			key.Value.Depends = filtered
		}
		out[i] = key
	}
	return out
}

// stripParens is strip in the TS source.
func stripParens(e *ast.Node) *ast.Node {
	cursor := e
	for ast.IsParenthesizedExpression(cursor) {
		cursor = cursor.AsParenthesizedExpression().Expression
	}
	return cursor
}

// RefineRows is refineRows in the TS source. ok=false mirrors the TS
// null return.
func RefineRows(predicate *ast.Node, object *ObjectAnnotation) (RefineReading, bool) {
	if !ast.IsArrowFunction(predicate) && !ast.IsFunctionExpression(predicate) {
		return RefineReading{}, false
	}
	parameters := predicate.Parameters()
	if len(parameters) == 0 || !ast.IsIdentifier(parameters[0].Name()) {
		return RefineReading{}, false
	}
	value := parameters[0].Name().AsIdentifier().Text
	var body *ast.Node
	fnBody := predicate.Body()
	if fnBody == nil {
		return RefineReading{}, false
	}
	if ast.IsBlock(fnBody) {
		statements := fnBody.AsBlock().Statements
		if statements == nil || len(statements.Nodes) != 1 {
			return RefineReading{}, false
		}
		only := statements.Nodes[0]
		if !ast.IsReturnStatement(only) || only.AsReturnStatement().Expression == nil {
			return RefineReading{}, false
		}
		body = only.AsReturnStatement().Expression
	} else {
		body = fnBody
	}
	declared := map[string]bool{}
	for _, key := range object.Keys {
		declared[key.Name] = true
	}
	keyOf := func(e *ast.Node) (string, bool) {
		cursor := stripParens(e)
		if ast.IsPropertyAccessExpression(cursor) {
			access := cursor.AsPropertyAccessExpression()
			if ast.IsIdentifier(access.Expression) && access.Expression.AsIdentifier().Text == value && declared[access.Name().Text()] {
				return access.Name().Text(), true
			}
		}
		return "", false
	}
	literalOf := func(e *ast.Node) (float64, bool) {
		cursor := stripParens(e)
		if ast.IsNumericLiteral(cursor) {
			return NumberArg(nil, cursor)
		}
		if ast.IsPrefixUnaryExpression(cursor) {
			unary := cursor.AsPrefixUnaryExpression()
			if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
				v, ok := NumberArg(nil, unary.Operand)
				return -v, ok
			}
		}
		return 0, false
	}
	var rows []refineRow
	keyForms := map[string][]refinementsets.Refinement{}
	keyExclusions := map[string][]float64{}
	pushForm := func(key string, form refinementsets.Refinement) {
		keyForms[key] = append(keyForms[key], form)
	}
	var visit func(e *ast.Node) bool
	visit = func(e *ast.Node) bool {
		cursor := stripParens(e)
		if !ast.IsBinaryExpression(cursor) {
			return false
		}
		bin := cursor.AsBinaryExpression()
		kind := bin.OperatorToken.Kind
		if kind == ast.KindAmpersandAmpersandToken {
			return visit(bin.Left) && visit(bin.Right)
		}
		equal := kind == ast.KindEqualsEqualsEqualsToken || kind == ast.KindEqualsEqualsToken
		unequal := kind == ast.KindExclamationEqualsEqualsToken || kind == ast.KindExclamationEqualsToken
		if equal || unequal {
			// `r.k % n === 0` -- the exact-remainder statement
			moduloOf := func(side *ast.Node) (string, float64, bool) {
				inner := stripParens(side)
				if !ast.IsBinaryExpression(inner) || inner.AsBinaryExpression().OperatorToken.Kind != ast.KindPercentToken {
					return "", 0, false
				}
				innerBin := inner.AsBinaryExpression()
				key, keyOk := keyOf(innerBin.Left)
				d, dOk := literalOf(innerBin.Right)
				if keyOk && dOk && d != 0 {
					return key, d, true
				}
				return "", 0, false
			}
			modKey, modD, modOk := moduloOf(bin.Left)
			if !modOk {
				modKey, modD, modOk = moduloOf(bin.Right)
			}
			leftZeroVal, leftZeroOk := literalOf(bin.Left)
			rightZeroVal, rightZeroOk := literalOf(bin.Right)
			zeroSide := (leftZeroOk && leftZeroVal == 0) || (rightZeroOk && rightZeroVal == 0)
			if equal && modOk && zeroSide {
				pushForm(modKey, refinementsets.MultipleOf(modD))
				return true
			}
			key, keyOk := keyOf(bin.Left)
			if !keyOk {
				key, keyOk = keyOf(bin.Right)
			}
			literal, literalOk := literalOf(bin.Left)
			if !literalOk {
				literal, literalOk = literalOf(bin.Right)
			}
			if !keyOk || !literalOk {
				return false
			}
			if equal {
				pushForm(key, refinementsets.OneOf([]float64{literal}))
				return true
			}
			keyExclusions[key] = append(keyExclusions[key], literal)
			return true
		}
		var op string
		switch kind {
		case ast.KindGreaterThanEqualsToken:
			op = "ge"
		case ast.KindGreaterThanToken:
			op = "gt"
		case ast.KindLessThanEqualsToken:
			op = "le"
		case ast.KindLessThanToken:
			op = "lt"
		default:
			return false
		}
		leftKey, leftKeyOk := keyOf(bin.Left)
		rightKey, rightKeyOk := keyOf(bin.Right)
		if leftKeyOk && rightKeyOk {
			rows = append(rows, refineRow{key: leftKey, op: op, param: rightKey})
			return true
		}
		// a key against a numeric literal folds into the key's own
		// set
		leftLiteral, leftLiteralOk := literalOf(bin.Left)
		rightLiteral, rightLiteralOk := literalOf(bin.Right)
		if leftKeyOk && rightLiteralOk {
			var form refinementsets.Refinement
			switch op {
			case "ge":
				form = refinementsets.AtLeast(rightLiteral)
			case "gt":
				form = refinementsets.Above(rightLiteral)
			case "le":
				form = refinementsets.AtMost(rightLiteral)
			default:
				form = refinementsets.Below(rightLiteral)
			}
			pushForm(leftKey, form)
			return true
		}
		if rightKeyOk && leftLiteralOk {
			// `200 <= r.hi` mirrors to `r.hi >= 200`
			var form refinementsets.Refinement
			switch op {
			case "ge":
				form = refinementsets.AtMost(leftLiteral)
			case "gt":
				form = refinementsets.Below(leftLiteral)
			case "le":
				form = refinementsets.AtLeast(leftLiteral)
			default:
				form = refinementsets.Above(leftLiteral)
			}
			pushForm(rightKey, form)
			return true
		}
		return false
	}
	if !visit(body) {
		return RefineReading{}, false
	}
	return RefineReading{rows: rows, keyForms: keyForms, keyExclusions: keyExclusions}, true
}

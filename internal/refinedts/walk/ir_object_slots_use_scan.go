// split from ir_object_slots.go — the use scan a flattened record must pass

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// usesAreAllDeclaredKeySteps scans a body for every occurrence of the
// name and answers whether each one is a `name.a.b` path on a leaf the
// literal declared, or one of the recognized whole-record forms. The
// declaration's own name position and the literal's own rows are not
// uses. A `delete p.k` reads the record as a mutable object, so it
// declines even though its operand IS a path; every other whole-name
// occurrence — an alias, an argument, a return, an element access
// `p[e]` — declines, because after flattening there is no one value for
// it to denote.
//
// A SYMBOL-KEYED step `p[S]` is not one of those declines: under a
// stable symbol const it names ONE declared leaf, so it reads and writes
// that leaf exactly as `p.k` does. The refusal it used to take was the
// bare-name arm catching `p` inside an element access, which is the
// right answer for `p[e]` — an index nothing spells — and the wrong one
// for a key the vocabulary now holds. `p?.[S]` still declines: an
// absent receiver is what no leaf spells.
func usesAreAllDeclaredKeySteps(
	c *checker.Checker,
	body *ast.Node,
	declaration *ast.Node,
	name string,
	keys []ObjectLocalKey,
	sameShapeName func(other string) bool,
) bool {
	declared := declaredLeafPaths(keys)
	shape := recordShapeOf(keys)
	declarationName := declaration.AsVariableDeclaration().Name()
	ok := true
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		// `delete p.k` — the record's shape is observed, not just its
		// leaves' values. Checked BEFORE the path rule below, which would
		// otherwise admit the operand as an ordinary read.
		if ast.IsDeleteExpression(node) {
			operand := Unwrapped(node.AsDeleteExpression().Expression)
			if root, _, isPath := propertyPathOf(operand); isPath && root == name {
				ok = false
				return true
			}
		}
		// `p = q` / `p = { … }` — the whole record written leaf for leaf.
		// Both sides are consumed here so neither reaches the bare-name
		// test below. This scan runs once per record, so the SAME
		// assignment is seen from the target's side (name on the left) and
		// from the source's side (name on the right); both are admitted,
		// each against the other's shape.
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind == ast.KindEqualsToken {
				left := bin.Left
				right := Unwrapped(bin.Right)
				// this record is the TARGET
				if ast.IsIdentifier(left) && left.Text() == name {
					if ast.IsIdentifier(right) && sameShapeName(right.Text()) {
						return false
					}
					if ast.IsObjectLiteralExpression(right) {
						if rows, rowsOk := flatKeysOfLiteralWith(c, right, name, nil); rowsOk && recordShapeOf(rows) == shape {
							// the rows' own initializers still have to be scanned —
							// one of them could mention this record
							for _, row := range rows {
								visit(row.Initializer)
							}
							return false
						}
					}
					ok = false
					return true
				}
				// this record is the SOURCE: `q = p` where q is a flattened
				// record of the same leaf shape reads p leaf by leaf, never
				// as one value
				if ast.IsIdentifier(right) && right.Text() == name &&
					ast.IsIdentifier(left) && sameShapeName(left.Text()) {
					return false
				}
			}
		}
		// `const { x, y } = p` — the leaves read into fresh names. Every
		// bound name must be a one-step leaf of this record.
		if ast.IsVariableDeclaration(node) {
			decl := node.AsVariableDeclaration()
			if decl.Initializer != nil && ast.IsObjectBindingPattern(decl.Name()) {
				initializer := Unwrapped(decl.Initializer)
				if ast.IsIdentifier(initializer) && initializer.Text() == name {
					if _, patternOk := destructuredLeafNamesOf(decl.Name(), keys); patternOk {
						return false
					}
					ok = false
					return true
				}
			}
		}
		// `p.a.b` — a path; the root and every step name are consumed here,
		// so none reaches the bare-identifier test below
		if root, path, isPath := propertyPathOf(node); isPath && root == name {
			if _, isDeclared := declared[strings.Join(path, ".")]; !isDeclared {
				ok = false // a leaf the literal never gave a slot
				return true
			}
			return false
		}
		// `p[S]` under a stable symbol const — one declared leaf, read or
		// written the way a dotted step is. The root is consumed here so it
		// does not reach the bare-name test below.
		if ast.IsElementAccessExpression(node) {
			element := node.AsElementAccessExpression()
			if root := Unwrapped(element.Expression); root != nil &&
				ast.IsIdentifier(root) && root.Text() == name {
				symbolKey, isSymbolKey := SymbolKeyedFieldName(c, node)
				if !isSymbolKey {
					ok = false // an index nothing spells
					return true
				}
				if _, isDeclared := declared[symbolKey]; !isDeclared {
					ok = false // a leaf the literal never gave a slot
					return true
				}
				// the KEY expression is the const's own name, not a use of
				// the record — nothing to walk under it
				return false
			}
		}
		// Every other occurrence of the bare name — an alias `q = p`, an
		// argument `f(p)`, `return p`, an element access `p[e]`, an
		// optional step `p?.k`, a nested `q.p` never reaching here as a
		// root — is the WHOLE record in a position the flattening cannot
		// spell.
		if ast.IsIdentifier(node) && node.Text() == name && node != declarationName {
			ok = false
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(body)
	return ok
}

// destructuredLeafNamesOf reads an object binding pattern against a
// flattened record's leaves: every element must be a plain
// `{ x }` or `{ x: y }` row naming a ONE-STEP leaf of the record, with
// no default, no rest, no nested pattern. Answers each bound name
// paired with the leaf it reads.
type destructuredLeaf struct {
	BoundName string
	Key       ObjectLocalKey
}

func destructuredLeafNamesOf(pattern *ast.Node, keys []ObjectLocalKey) ([]destructuredLeaf, bool) {
	if !ast.IsObjectBindingPattern(pattern) {
		return nil, false
	}
	var out []destructuredLeaf
	for _, element := range pattern.AsBindingPattern().Elements.Nodes {
		binding := element.AsBindingElement()
		if binding.DotDotDotToken != nil || binding.Initializer != nil {
			return nil, false
		}
		if !ast.IsIdentifier(binding.Name()) {
			return nil, false
		}
		read := binding.Name().Text()
		if binding.PropertyName != nil {
			if !ast.IsIdentifier(binding.PropertyName) {
				return nil, false
			}
			read = binding.PropertyName.Text()
		}
		found := false
		for _, key := range keys {
			if len(key.Path) == 1 && key.Path[0] == read {
				out = append(out, destructuredLeaf{BoundName: binding.Name().Text(), Key: key})
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

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
	methods map[string]*ast.Node,
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
					// a record with METHOD rows never assigns whole, in
					// either direction: its method calls resolve to THIS
					// literal's declarations, and a rebound record would run
					// other bodies under the same spellings
					if len(methods) > 0 {
						ok = false
						return true
					}
					if ast.IsIdentifier(right) && sameShapeName(right.Text()) {
						return false
					}
					if ast.IsObjectLiteralExpression(right) {
						// a method-bearing RHS literal leaf-copies its scalar
						// rows but replaces the method identities — no leaf
						// rewrite spells that
						if len(literalMethodPaths(right, nil)) > 0 {
							ok = false
							return true
						}
						if rows, rowsOk := flatKeysOfLiteralWith(c, right, name, nil); rowsOk && recordShapeOf(rows) == shape {
							// the rows' own initializers still have to be scanned —
							// one of them could mention this record
							for _, row := range rows {
								visit(row.Initializer)
							}
							return false
						}
						// `p = { ...p, x1: e }` — the SELF-spread overlay: the
						// leading spread copies every unmentioned leaf onto
						// itself, so the write is the explicit rows alone
						// (RecordAssignmentOf's spread arm). Admitted where the
						// spread names THIS record first and every explicit row
						// writes a declared leaf; the row initializers still
						// scan as ordinary uses.
						if properties := right.AsObjectLiteralExpression().Properties.Nodes; len(properties) >= 2 &&
							ast.IsSpreadAssignment(properties[0]) {
							source := Unwrapped(properties[0].AsSpreadAssignment().Expression)
							if source != nil && ast.IsIdentifier(source) && source.Text() == name {
								if rows, rowsOk := flatKeysOfLiteralAfterLeadingSpread(c, right, name); rowsOk {
									declaredAll := true
									for _, row := range rows {
										if _, isDeclared := declared[strings.Join(row.Path, ".")]; !isDeclared {
											declaredAll = false
											break
										}
									}
									if declaredAll {
										for _, row := range rows {
											visit(row.Initializer)
										}
										return false
									}
								}
							}
						}
					}
					ok = false
					return true
				}
				// this record is the SOURCE: `q = p` where q is a flattened
				// record of the same leaf shape reads p leaf by leaf, never
				// as one value. A method-bearing record refuses this too —
				// the copy would hand q an object whose methods q's own
				// vocabulary never spelled.
				if ast.IsIdentifier(right) && right.Text() == name &&
					ast.IsIdentifier(left) && sameShapeName(left.Text()) {
					if len(methods) > 0 {
						ok = false
						return true
					}
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
		// `p.m(…)` — a METHOD row called through the record. The callee
		// spells no leaf: the summary door threads the method's `this`
		// entries onto the record's own slots and the write-backs land
		// there (method_this_writes.go). Only the CALLEE position admits
		// the spelling — the method handed out bare (`const f = p.m`)
		// still falls to the whole-name refusal below, because an unbound
		// call would run with the wrong `this`.
		if ast.IsCallExpression(node) {
			callExpression := node.AsCallExpression()
			callee := Unwrapped(callExpression.Expression)
			if root, path, isPath := propertyPathOf(callee); isPath && root == name {
				if _, isMethod := methods[strings.Join(path, ".")]; isMethod {
					// the arguments are still ordinary uses; the callee is
					// consumed here
					if callExpression.Arguments != nil {
						for _, argument := range callExpression.Arguments.Nodes {
							visit(argument)
						}
					}
					return false
				}
			}
		}
		// `p.a.b` — a path; the root and every step name are consumed here,
		// so none reaches the bare-identifier test below. `p?.a` reads as
		// the plain step too: `p` is this record's OWN root, and a
		// flattened record local is always defined, so the optional step
		// changes nothing about which leaf is read. A DEEPER optional step
		// (`p.a?.b`) is not this admit — propertyPathAdmittingRootOptionalStep
		// only tolerates the `?.` adjacent to the root itself, so `p.a?.b`
		// still falls through to the whole-name refusal below.
		if root, path, isPath := propertyPathAdmittingRootOptionalStep(node); isPath && root == name {
			if _, isDeclared := declared[strings.Join(path, ".")]; !isDeclared {
				ok = false // a leaf the literal never gave a slot
				return true
			}
			return false
		}
		// `p[S]` under a stable symbol const, or `p['a']`/`p["a"]` under a
		// GROUNDED string-literal (or no-substitution template) key — both
		// name one declared leaf, read or written the way a dotted step is.
		// The root is consumed here so it does not reach the bare-name test
		// below. A DYNAMIC key (a variable, a call, a template with a
		// substitution) still falls to the "an index nothing spells" refusal
		// — grounding is the whole point of the admission, and this scan
		// never guesses a key any more than GroundedComputedMemberSlotOf
		// (ir_computed_member_grounded.go, the read-side twin this
		// admission exists to feed) does.
		if ast.IsElementAccessExpression(node) {
			element := node.AsElementAccessExpression()
			if root := Unwrapped(element.Expression); root != nil &&
				ast.IsIdentifier(root) && root.Text() == name {
				if symbolKey, isSymbolKey := SymbolKeyedFieldName(c, node); isSymbolKey {
					if _, isDeclared := declared[symbolKey]; !isDeclared {
						ok = false // a leaf the literal never gave a slot
						return true
					}
					// the KEY expression is the const's own name, not a use of
					// the record — nothing to walk under it
					return false
				}
				if groundedKey, isGrounded := groundedComputedKeyOf(element.ArgumentExpression); isGrounded {
					if _, isDeclared := declared[groundedKey]; !isDeclared {
						ok = false // a leaf the literal never gave a slot
						return true
					}
					// the key is a literal token, not a use of the record —
					// nothing to walk under it, the same reasoning the symbol-
					// keyed arm above already carries
					return false
				}
				ok = false // an index nothing spells
				return true
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

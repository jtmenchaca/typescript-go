// split from ir_map_slots.go — the use scan
//
// The total-or-decline pass over every occurrence of a collection's
// name: one node at a time is ruled admitted (and its children queued)
// or refused, and a single refusal costs the whole collection its
// flattening.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// mapUseAdmission is what one occurrence of the collection name is
// admitted as while the recognizer scans, so the scan and the lowering
// agree on exactly one vocabulary.
type mapUseAdmission struct {
	// Children still to scan — an argument could mention the collection
	// again, and that occurrence gets its own ruling.
	Children []*ast.Node
	Admitted bool
}

// admitCollectionUse rules on one node standing at an occurrence of the
// collection name. Answers Admitted=false where the node is a use the
// slots cannot spell, leaving the caller to decline the whole
// collection.
func admitCollectionUse(node *ast.Node, name string, isMap bool) (mapUseAdmission, bool) {
	// `m.size` — consumed whole
	if sizeReadOf(node, name) {
		return mapUseAdmission{Admitted: true}, true
	}
	// `[...m.values()]` / `[...s]` in an array literal is the BRIDGE, and
	// the array recognizer rules on the resulting local; here the
	// collection's own occurrence inside the spread is admitted and
	// nothing below it is scanned again. A BARE spread of a Map spreads
	// its entries, which are pairs and have no one element slot — the
	// bridge refuses it, and so does this.
	if ast.IsSpreadElement(node) {
		if admitted, isBridge := admitBridgeSource(node.AsSpreadElement().Expression, name, isMap); isBridge {
			return mapUseAdmission{Admitted: admitted}, true
		}
	}
	// `Array.from(m.values())` — the same bridge, spelled as a call
	if ast.IsCallExpression(node) {
		if source := arrayFromReceiverOf(node.AsCallExpression()); source != nil {
			if admitted, isBridge := admitBridgeSource(source, name, isMap); isBridge {
				return mapUseAdmission{Admitted: admitted}, true
			}
		}
	}
	// `new Map(m)` / `new Set(s)` — this collection COPIED into a new
	// one. The copy reads only the slots, which hold everything the
	// collection can hold, so the source keeps its flattening and its own
	// occurrence here is consumed whole. The kind must MATCH: seeding a
	// Set from a Map reads entry pairs, which one value slot cannot hold,
	// and seeding a Map from a Set reads members as pairs, which they are
	// not.
	if ast.IsNewExpression(node) {
		if constructedIsMap, seed, isConstruction := constructionOfNewExpression(node); isConstruction {
			if source, isCopy := copySourceOf(seed); isCopy && source == name {
				return mapUseAdmission{Admitted: constructedIsMap == isMap}, true
			}
		}
	}
	// `a.union(b)` / `.intersection(b)` / `.difference(b)` /
	// `.symmetricDifference(b)` — this collection standing as either the
	// RECEIVER or the ARGUMENT of a two-sibling Set producer. Both
	// occurrences are consumed whole here: setProducerMapLocalOf reads
	// only the two operands' own slots, so a collection appearing on
	// either side of the call keeps its own flattening. A Map is never
	// admitted — none of the four methods exist on Map's interface.
	if receiver, argument, isProducerCall := setProducerCallOf(node); isProducerCall {
		if (receiver == name || argument == name) && !isMap {
			return mapUseAdmission{Admitted: true}, true
		}
	}
	// `m.set(k, v)` / `s.add(v)` / `m.get(k)` / `m.delete(k)` — the
	// operations; their arguments still scan
	if method, arguments, isCall := collectionMethodCallOf(node, name); isCall {
		admitted := false
		switch method {
		case "set":
			admitted = isMap && len(arguments) == 2
		case "add":
			admitted = !isMap && len(arguments) == 1
		case "get":
			admitted = isMap && len(arguments) == 1
		case "delete":
			admitted = len(arguments) == 1
		case "clear":
			// `m.clear()` — MapClearAssignmentsOf resets every slot to the
			// fresh-empty state, so the receiver's own occurrence is
			// consumed whole; there is no argument to scan
			admitted = len(arguments) == 0
		case "getOrInsert":
			// `m.getOrInsert(k, v)` — a Map-only get-or-default: the plain-
			// value form MapGetOrInsertAssignmentsOf reads. Its cousin
			// getOrInsertComputed (a callback second argument) is NOT
			// admitted here — reading what the callback returns needs the
			// callback-summary machinery, which this scan does not carry.
			admitted = isMap && len(arguments) == 2
		case "forEach":
			// `m.forEach(cb)` — collectionForEachStatement reads the vals
			// (and keys) slots and converts the callback, so the receiver's
			// own occurrence is consumed and the callback still scans. Whether
			// the callback converts is the lowering's question; a callback
			// that declines costs the body its lowering rather than claiming
			// anything wrong here.
			admitted = len(arguments) == 1
		case "values", "keys", "entries":
			// a view standing alone is only admitted in an iterated or
			// bridged position, which the forms above already consumed;
			// reaching here means it was used somewhere else
			admitted = false
		}
		if !admitted {
			return mapUseAdmission{}, true
		}
		return mapUseAdmission{Children: arguments, Admitted: true}, true
	}
	// `if (m.has(k)) { … } else { … }` — the has call standing as the
	// whole test. The read touches no slot and the opaque branch claims
	// nothing about the condition, so the collection keeps its
	// flattening. Only the test itself is consumed: the has argument and
	// both arms still scan.
	if test := testPositionOf(node); test != nil {
		if arguments, isHasTest := hasCallInTestPosition(test, name); isHasTest {
			ifStmt := node.AsIfStatement()
			children := append([]*ast.Node{}, arguments...)
			children = append(children, ifStmt.ThenStatement, ifStmt.ElseStatement)
			return mapUseAdmission{Children: children, Admitted: true}, true
		}
		// `if (s.isSubsetOf(other)) { … }` / `.isSupersetOf` /
		// `.isDisjointFrom` — the same opaque-branch admission `has`
		// gets: a write-free, boolean-returning Set predicate whose
		// result no slot spells, so the branch that tests nothing serves
		// it exactly as it serves `has`.
		if arguments, isSetPredicateTest := setPredicateCallInTestPosition(test, name, isMap); isSetPredicateTest {
			ifStmt := node.AsIfStatement()
			children := append([]*ast.Node{}, arguments...)
			children = append(children, ifStmt.ThenStatement, ifStmt.ElseStatement)
			return mapUseAdmission{Children: children, Admitted: true}, true
		}
	}
	// `for (const … of m)` / `of m.values()` — the collection in the
	// iterated position
	if ast.IsForOfStatement(node) {
		forOf := node.AsForInOrOfStatement()
		if forOf.AwaitModifier == nil {
			iterated := Unwrapped(forOf.Expression)
			bare := ast.IsIdentifier(iterated) && iterated.Text() == name
			_, isView := iteratorCallOf(iterated, name)
			if bare || isView {
				return mapUseAdmission{
					Children: []*ast.Node{forOf.Initializer, forOf.Statement},
					Admitted: true,
				}, true
			}
		}
	}
	return mapUseAdmission{}, false
}

// usesAreAllCollectionForms scans a body for every occurrence of the
// name and answers whether each one sits in a form the slots can spell.
// The declaration's own name position and the seed literal's own rows
// are not uses. Mirrors usesAreAllArrayForms exactly.
func usesAreAllCollectionForms(body *ast.Node, declaration *ast.Node, name string, isMap bool) bool {
	declarationName := declaration.AsVariableDeclaration().Name()
	ok := true
	var visit func(node *ast.Node) bool
	visitIfPresent := func(node *ast.Node) {
		if node != nil {
			visit(node)
		}
	}
	visit = func(node *ast.Node) bool {
		if !ok {
			return true
		}
		// `delete m.size` — the collection read as a mutable object.
		// Checked FIRST: its operand would otherwise pass the size rule.
		if ast.IsDeleteExpression(node) {
			operand := Unwrapped(node.AsDeleteExpression().Expression)
			if mentionsName(operand, name) {
				ok = false
				return true
			}
		}
		// `m.size = k` — a size write, which no operation here produces
		// and the slots would not track. Checked before the read rule.
		if ast.IsBinaryExpression(node) {
			bin := node.AsBinaryExpression()
			if bin.OperatorToken.Kind >= ast.KindFirstAssignment &&
				bin.OperatorToken.Kind <= ast.KindLastAssignment &&
				sizeReadOf(Unwrapped(bin.Left), name) {
				ok = false
				return true
			}
		}
		if admission, ruled := admitCollectionUse(node, name, isMap); ruled {
			if !admission.Admitted {
				ok = false
				return true
			}
			for _, child := range admission.Children {
				visitIfPresent(child)
			}
			return false
		}
		// Every other occurrence of the bare name — an alias, an argument,
		// a return, `m.has(k)`, `m[k]` — is the WHOLE
		// collection in a position the slots cannot spell.
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

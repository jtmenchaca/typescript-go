// Map and Set locals flattened into scalar slots for the flow IR.
//
// A body that keeps a Map or a Set in a local — `const m = new Map()` —
// sets into it, reads out of it, asks its size, and walks it. The
// kernel's walk is over a vector of scalar slots, so such a local is
// carried as THREE slots for a Map and TWO for a Set:
//
//   - "m.size", holding the entry count as an ordinary number;
//   - "m.vals", holding the JOIN of every value the collection can hold
//     (for a Set, its members);
//   - "m.keys" — Map only — holding the join of every key.
//
// Every proved transfer applies unchanged; the kernel never learns the
// slots came from one collection. The value and key slots are weak
// summaries exactly like an array's element slot: a write joins in
// rather than replacing, so a read always over-approximates.
//
// The recognized uses, total-or-decline over EVERY occurrence of the
// name:
//
//   - `m.size` → the size slot's var.
//   - `m.set(k, v)` → size := join(size, size + 1) — a set may OVERWRITE
//     an existing key, in which case the count does not move, so both
//     readings ride; keys := join(keys, k); vals := join(vals, v).
//   - `s.add(v)` → the same without the key half.
//   - `m.get(k)` → orAbsent(vals): the value or undefined, which is
//     exactly what a missing key yields.
//   - `m.delete(k)` / `s.delete(v)` → size := join(integer ≥ 0, size).
//     A delete may MISS, so the count either stays or drops by one; the
//     two-slot world has no spelling for "the old value minus at most
//     one", so the honest claim is the whole non-negative integer ray
//     joined with the old reading — removal never grows the collection,
//     and the join keeps the old reading admitted for the miss. Keys and
//     values are untouched: dropping an entry never adds a value, so the
//     joined summaries stay sound.
//   - `for (const v of s)` / `for (const v of m.values())` → the
//     ordinary loop lowering with the binding's per-pass effect the vals
//     slot's var.
//   - `for (const k of m.keys())` → the same against the keys slot.
//   - `for (const [k, v] of m)` / `of m.entries()`, the pattern exactly
//     two plain identifiers → k from keys, v from vals.
//   - `const a = [...m.values()]` / `Array.from(m.values())` / `[...s]`
//     → an ARRAY local bridged onto these slots (ir_array_slots.go).
//
// Everything else declines the collection: an alias, an argument, a
// return, `m.has(k)`, `clear()`, `forEach`, a computed method name, an
// element access `m[k]`, or any method not listed. `m.has(k)` declines
// rather than lowering to "no claim" because this tree's statement
// discipline has no opaque-test form — LowerGuard declines a head it
// cannot read, and a declined head declines the body, so a `has` result
// feeding a test would take the body down anyway. Declining the
// COLLECTION says the same thing one layer earlier and lets the rest of
// the body keep its former route.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// MapLocal is one flattened Map or Set local: the declaration it came
// from, the name it was spelled under, whether it is a Map (keys are
// tracked) or a Set, the slot names, and the seed literal's entries in
// source order.
type MapLocal struct {
	Declaration  *ast.Node // VariableDeclaration
	Name         string
	IsMap        bool
	SizeSlotName string // "m.size"
	ValsSlotName string // "m.vals"
	KeysSlotName string // "m.keys" — empty for a Set
	// SeedKeys, SeedVals: the seed literal's key and value expressions,
	// in source order. A Set's SeedKeys is nil. Both nil for an empty
	// `new Map()` / `new Set()`.
	SeedKeys []*ast.Node
	SeedVals []*ast.Node
}

// The three slot spellings a flattened collection wears below its name.
const (
	mapSizeSuffix = ".size"
	mapValsSuffix = ".vals"
	mapKeysSuffix = ".keys"
)

// collectionConstructionOf reads a declaration's initializer as
// `new Map(…)` / `new Set(…)`, answering whether it is a Map and the
// seed argument (nil where there is none). Anything else — a call
// without `new`, a qualified `globalThis.Map`, a WeakMap, a type
// argument list is fine but a receiver is not — declines.
func collectionConstructionOf(declaration *ast.Node) (isMap bool, seed *ast.Node, ok bool) {
	if !ast.IsVariableDeclaration(declaration) {
		return false, nil, false
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return false, nil, false
	}
	head := Unwrapped(initializer)
	if !ast.IsNewExpression(head) {
		return false, nil, false
	}
	expression := head.AsNewExpression()
	if !ast.IsIdentifier(expression.Expression) {
		return false, nil, false
	}
	switch expression.Expression.Text() {
	case "Map":
		isMap = true
	case "Set":
		isMap = false
	default:
		return false, nil, false
	}
	if expression.Arguments == nil || len(expression.Arguments.Nodes) == 0 {
		return isMap, nil, true
	}
	if len(expression.Arguments.Nodes) != 1 {
		return false, nil, false
	}
	return isMap, expression.Arguments.Nodes[0], true
}

// seedEntriesOf reads the seed argument of `new Map([[k, v], …])` or
// `new Set([v, …])` as its key and value expressions. Only an ARRAY
// LITERAL seeds — a variable or an iterator names no rows the slots can
// join. A Map's rows must each be a two-element array literal; a Set's
// rows are its members. A nested object or array value is not a scalar
// the slots can hold, so it declines.
func seedEntriesOf(seed *ast.Node, isMap bool) (keys []*ast.Node, vals []*ast.Node, ok bool) {
	if seed == nil {
		return nil, nil, true
	}
	literal := Unwrapped(seed)
	if !ast.IsArrayLiteralExpression(literal) {
		return nil, nil, false
	}
	for _, row := range literal.AsArrayLiteralExpression().Elements.Nodes {
		if ast.IsSpreadElement(row) || ast.IsOmittedExpression(row) {
			return nil, nil, false
		}
		value := Unwrapped(row)
		if !isMap {
			if ast.IsObjectLiteralExpression(value) || ast.IsArrayLiteralExpression(value) {
				return nil, nil, false
			}
			vals = append(vals, row)
			continue
		}
		// a Map row is `[k, v]`
		if !ast.IsArrayLiteralExpression(value) {
			return nil, nil, false
		}
		pair := value.AsArrayLiteralExpression().Elements.Nodes
		if len(pair) != 2 {
			return nil, nil, false
		}
		for _, half := range pair {
			if ast.IsSpreadElement(half) || ast.IsOmittedExpression(half) {
				return nil, nil, false
			}
			inner := Unwrapped(half)
			if ast.IsObjectLiteralExpression(inner) || ast.IsArrayLiteralExpression(inner) {
				return nil, nil, false
			}
		}
		keys = append(keys, pair[0])
		vals = append(vals, pair[1])
	}
	return keys, vals, true
}

// collectionMethodCallOf is `m.<method>(…)` with a plain (non-optional,
// non-computed) member name on the spelled receiver — the shape every
// recognized operation wears. Answers the method name and its arguments.
func collectionMethodCallOf(node *ast.Node, name string) (method string, arguments []*ast.Node, ok bool) {
	if !ast.IsCallExpression(node) {
		return "", nil, false
	}
	call := node.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return "", nil, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return "", nil, false
	}
	if !ast.IsIdentifier(access.Expression) || access.Expression.Text() != name {
		return "", nil, false
	}
	if !ast.IsIdentifier(access.Name()) {
		return "", nil, false
	}
	if call.Arguments != nil {
		for _, argument := range call.Arguments.Nodes {
			if ast.IsSpreadElement(argument) {
				return "", nil, false
			}
		}
		arguments = call.Arguments.Nodes
	}
	return access.Name().Text(), arguments, true
}

// sizeReadOf is `m.size` — the one property read the size slot answers.
func sizeReadOf(node *ast.Node, name string) bool {
	if !ast.IsPropertyAccessExpression(node) {
		return false
	}
	access := node.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return false
	}
	return ast.IsIdentifier(access.Expression) && access.Expression.Text() == name &&
		ast.IsIdentifier(access.Name()) && access.Name().Text() == "size"
}

// iteratorCallOf is `m.values()` / `m.keys()` / `m.entries()` — the
// zero-argument view methods an iteration or a bridge may stand on.
// Answers the view's name.
func iteratorCallOf(node *ast.Node, name string) (view string, ok bool) {
	method, arguments, isCall := collectionMethodCallOf(node, name)
	if !isCall || len(arguments) != 0 {
		return "", false
	}
	switch method {
	case "values", "keys", "entries":
		return method, true
	}
	return "", false
}

// admitBridgeSource rules on the expression a spread or an
// `Array.from` stands on, where it names THIS collection: a Set spread
// bare, or either collection's `values()` / `keys()` view. A bare Map
// yields pairs, which one element slot cannot hold, so it is a source
// the bridge refuses — reported as (false, true) so the caller declines
// the collection rather than reading past it. isBridge=false means the
// expression names something else entirely, and the caller keeps
// scanning.
func admitBridgeSource(source *ast.Node, name string, isMap bool) (admitted bool, isBridge bool) {
	head := Unwrapped(source)
	if ast.IsIdentifier(head) && head.Text() == name {
		return !isMap, true
	}
	view, isView := iteratorCallOf(head, name)
	if !isView {
		return false, false
	}
	switch view {
	case "values":
		return true, true
	case "keys":
		return isMap, true
	}
	// `entries()` yields pairs
	return false, true
}

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
		// a return, `m.has(k)`, `m.clear()`, `m.forEach(cb)`, `m[k]` — is
		// the WHOLE collection in a position the slots cannot spell.
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

// MapLocalOf is the recognizer: a declaration `const m = new Map()` (or
// `new Set()`, seeded or empty) whose every use in the body is one of
// the recognized forms becomes the slot family "m.size" / "m.vals" (and
// "m.keys" for a Map); anything else declines.
func MapLocalOf(body *ast.Node, declaration *ast.Node) (MapLocal, bool) {
	isMap, seed, isConstruction := collectionConstructionOf(declaration)
	if !isConstruction {
		return MapLocal{}, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return MapLocal{}, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	keys, vals, seedOk := seedEntriesOf(seed, isMap)
	if !seedOk {
		return MapLocal{}, false
	}
	// a seed row's own expression must not mention the collection — it
	// would read slots the declaration has not written yet
	for _, entry := range append(append([]*ast.Node{}, keys...), vals...) {
		if mentionsName(entry, name) {
			return MapLocal{}, false
		}
	}
	if !usesAreAllCollectionForms(body, declaration, name, isMap) {
		return MapLocal{}, false
	}
	local := MapLocal{
		Declaration:  declaration,
		Name:         name,
		IsMap:        isMap,
		SizeSlotName: name + mapSizeSuffix,
		ValsSlotName: name + mapValsSuffix,
		SeedKeys:     keys,
		SeedVals:     vals,
	}
	if isMap {
		local.KeysSlotName = name + mapKeysSuffix
	}
	return local, true
}

// MapLocalsOf runs the recognizer over a body's collected locals and
// answers the ones that flatten, keyed by declaration.
func MapLocalsOf(body *ast.Node, locals []*ast.Node) map[*ast.Node]MapLocal {
	out := map[*ast.Node]MapLocal{}
	for _, declaration := range locals {
		if local, ok := MapLocalOf(body, declaration); ok {
			out[declaration] = local
		}
	}
	return out
}

// MapValueSort is a flattened collection's VALUE sort, read from its
// seed rows the way ArrayElementSort reads an array literal's elements:
// every seeded value string-shaped by syntax makes a string slot;
// anything else the lowering reads numerically. An EMPTY collection has
// no value to read, so it takes the number sort the sets and the gets
// speak.
func MapValueSort(local MapLocal) BindingKind {
	return sortOfSeedRow(local.SeedVals)
}

// MapKeySort is the same reading for a Map's KEY slot. A Set has no key
// slot and answers the number sort, which nothing consults.
func MapKeySort(local MapLocal) BindingKind {
	return sortOfSeedRow(local.SeedKeys)
}

func sortOfSeedRow(entries []*ast.Node) BindingKind {
	if len(entries) == 0 {
		return BindingKindNumber
	}
	for _, entry := range entries {
		if !SpelledSequenceShape(entry) {
			return BindingKindNumber
		}
	}
	return BindingKindString
}

// MapValueTypeof is a flattened collection's value typeof evidence,
// from the seed's syntax alone — only an all-same reading claims
// anything, exactly as ArrayElementTypeof does.
func MapValueTypeof(local MapLocal) TypeofTag {
	return typeofOfSeedRow(local.SeedVals)
}

// MapKeyTypeof is the same reading for a Map's key slot.
func MapKeyTypeof(local MapLocal) TypeofTag {
	return typeofOfSeedRow(local.SeedKeys)
}

func typeofOfSeedRow(entries []*ast.Node) TypeofTag {
	if len(entries) == 0 {
		return TypeofTagNone
	}
	var held TypeofTag
	for index, entry := range entries {
		e := Unwrapped(entry)
		var tag TypeofTag
		switch {
		case SpelledSequenceShape(e):
			tag = TypeofTagString
		case e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword:
			tag = TypeofTagBoolean
		default:
			if _, isNumber := NumberOf(e); ast.IsNumericLiteral(e) || isNumber {
				tag = TypeofTagNumber
			} else {
				return TypeofTagNone
			}
		}
		if index == 0 {
			held = tag
			continue
		}
		if tag != held {
			return TypeofTagNone
		}
	}
	return held
}

// MapSlot is one slot a flattened collection contributes to the slot
// vector: its spelled name, the sort its occurrences wear, and what
// typeof answers for it. The same three fields the body's slot builder
// lays out for every other local.
type MapSlot struct {
	Name      string
	Sort      BindingKind
	TypeofTag TypeofTag
}

// MapLocalSlots is the slot layout a flattened collection contributes,
// in the order the slot vector must lay them out: size, vals, and — for
// a Map — keys. The one place the layout is spelled, so the body's slot
// builder and every resolver below agree.
func MapLocalSlots(local MapLocal) []MapSlot {
	out := []MapSlot{
		{Name: local.SizeSlotName, Sort: BindingKindNumber, TypeofTag: TypeofTagNumber},
		{Name: local.ValsSlotName, Sort: MapValueSort(local), TypeofTag: MapValueTypeof(local)},
	}
	if local.IsMap {
		out = append(out, MapSlot{
			Name: local.KeysSlotName, Sort: MapKeySort(local), TypeofTag: MapKeyTypeof(local),
		})
	}
	return out
}

// mapSlotsOf resolves a spelled collection name to its slots, or
// declines: a name with no "m.size"/"m.vals" pair is not a flattened
// collection here. keysOk is false for a Set, whose keys slot does not
// exist — every Map-only operation gates on it.
func mapSlotsOf(context *LoweringContext, name string) (sizeSlot int, valsSlot int, keysSlot int, keysOk bool, ok bool) {
	sizeSlot, sizeFound := slotIndexOfName(context, name+mapSizeSuffix)
	valsSlot, valsFound := slotIndexOfName(context, name+mapValsSuffix)
	if !sizeFound || !valsFound {
		return 0, 0, 0, false, false
	}
	keysSlot, keysOk = slotIndexOfName(context, name+mapKeysSuffix)
	return sizeSlot, valsSlot, keysSlot, keysOk, true
}

// MapSizeSlotOf resolves `m.size` to the size slot — the one property
// read a flattened collection answers. IndexOf routes through here so a
// `m.size` read and an `i < m.size` head both land on the ordinary
// number slot the guards and the loop head already speak.
func MapSizeSlotOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if !ast.IsPropertyAccessExpression(head) {
		return 0, false
	}
	access := head.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil {
		return 0, false
	}
	if !ast.IsIdentifier(access.Expression) || !ast.IsIdentifier(access.Name()) {
		return 0, false
	}
	if access.Name().Text() != "size" {
		return 0, false
	}
	sizeSlot, _, _, _, ok := mapSlotsOf(context, access.Expression.Text())
	if !ok {
		return 0, false
	}
	return sizeSlot, true
}

// MapValueSlotOf resolves `m.get(k)` to the value slot its read answers
// — the sort gate a caller consults before admitting the read into
// arithmetic or a sequence.
func MapValueSlotOf(context *LoweringContext, node *ast.Node) (int, bool) {
	head := Unwrapped(node)
	if !ast.IsCallExpression(head) {
		return 0, false
	}
	access := head.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return 0, false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return 0, false
	}
	method, arguments, isCall := collectionMethodCallOf(head, receiver.Text())
	if !isCall || method != "get" || len(arguments) != 1 {
		return 0, false
	}
	_, valsSlot, _, keysOk, ok := mapSlotsOf(context, receiver.Text())
	// `get` is a Map operation; a Set has no keys slot and no get
	if !ok || !keysOk {
		return 0, false
	}
	return valsSlot, true
}

// MapGetReadEffect is `m.get(k)` as an effect: the value slot's var
// wrapped in or-absent. A get on a key the collection does not hold
// answers undefined, and the absent outcome is where that lives — there
// is no per-key knowledge that could rule the miss out, so unlike an
// array's guarded index read this wrapping is unconditional.
func MapGetReadEffect(context *LoweringContext, node *ast.Node) (kernelbridge.LoopEffect, bool) {
	valsSlot, ok := MapValueSlotOf(context, node)
	if !ok {
		return kernelbridge.LoopEffect{}, false
	}
	vals := varEffect(valsSlot)
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectOrAbsent, A: &vals}, true
}

// MapDeclarationAssignmentsOf is the lowering-side entry for a
// flattened collection's declaration: the size slot takes the seed's
// row count as an exact constant, and the value (and key) slots take the
// JOIN of the seed's effects. An EMPTY construction writes the absent-
// carrying constant into the value and key slots — there is nothing to
// read, so a get must produce undefined, and the absent flag is where
// that lives. This mirrors the empty-array-literal treatment exactly.
func MapDeclarationAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsVariableStatement(statement) {
		return nil, false
	}
	declarations := statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes
	if len(declarations) != 1 {
		return nil, false
	}
	declaration := declarations[0]
	isMap, seed, isConstruction := collectionConstructionOf(declaration)
	if !isConstruction {
		return nil, false
	}
	if !ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) {
		return nil, false
	}
	name := declaration.AsVariableDeclaration().Name().Text()
	sizeSlot, valsSlot, keysSlot, keysOk, ok := mapSlotsOf(context, name)
	if !ok || keysOk != isMap {
		return nil, false
	}
	keys, vals, seedOk := seedEntriesOf(seed, isMap)
	if !seedOk {
		return nil, false
	}
	out := []AssignmentTarget{{Target: sizeSlot, Effect: constNumber(float64(len(vals)))}}
	valsEffect, valsEffectOk := joinedSeedEffect(context, valsSlot, vals)
	if !valsEffectOk {
		return nil, false
	}
	out = append(out, AssignmentTarget{Target: valsSlot, Effect: valsEffect})
	if isMap {
		keysEffect, keysEffectOk := joinedSeedEffect(context, keysSlot, keys)
		if !keysEffectOk {
			return nil, false
		}
		out = append(out, AssignmentTarget{Target: keysSlot, Effect: keysEffect})
	}
	return out, true
}

// joinedSeedEffect is a seed row's expressions joined into one effect
// for their slot, or the absent-carrying constant where the seed is
// empty.
func joinedSeedEffect(context *LoweringContext, slot int, entries []*ast.Node) (kernelbridge.LoopEffect, bool) {
	if len(entries) == 0 {
		return kernelbridge.AbsentConst(), true
	}
	var joined kernelbridge.LoopEffect
	for index, entry := range entries {
		effect, ok := RhsEffect(context, context.Sorts[slot], entry)
		if !ok {
			return kernelbridge.LoopEffect{}, false
		}
		if index == 0 {
			joined = effect
			continue
		}
		joined = joinEffect(joined, effect)
	}
	return joined, true
}

// MapSetAssignmentsOf is `m.set(k, v)` / `s.add(v)` as a statement.
//
// The size: a set on a key the collection ALREADY holds overwrites and
// leaves the count where it was; a set on a fresh key steps it by one.
// Nothing in the two-slot world tells the two apart, so BOTH readings
// ride — the join of the old size with the stepped one. `s.add(v)` has
// the same overwrite behaviour (adding a member twice keeps one) and
// takes the same join.
//
// The values and keys: the weak update, exactly as an array's push —
// after the set the slot holds everything the collection could hold,
// which is what a get or an iteration may answer.
func MapSetAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	call := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	access := call.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return nil, false
	}
	method, arguments, isCall := collectionMethodCallOf(call, receiver.Text())
	if !isCall {
		return nil, false
	}
	sizeSlot, valsSlot, keysSlot, keysOk, ok := mapSlotsOf(context, receiver.Text())
	if !ok {
		return nil, false
	}
	var keyArgument, valueArgument *ast.Node
	switch method {
	case "set":
		// a Map operation: the keys slot must exist
		if !keysOk || len(arguments) != 2 {
			return nil, false
		}
		keyArgument, valueArgument = arguments[0], arguments[1]
	case "add":
		// a Set operation: there is no keys slot
		if keysOk || len(arguments) != 1 {
			return nil, false
		}
		valueArgument = arguments[0]
	default:
		return nil, false
	}
	written, writtenOk := RhsEffect(context, context.Sorts[valsSlot], valueArgument)
	if !writtenOk {
		return nil, false
	}
	one := constNumber(1)
	sizeVar := varEffect(sizeSlot)
	stepped := kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectBinary, Op: kernelbridge.LoopOpAdd, A: &sizeVar, B: &one,
	}
	out := []AssignmentTarget{
		{Target: sizeSlot, Effect: joinEffect(varEffect(sizeSlot), stepped)},
		{Target: valsSlot, Effect: joinEffect(varEffect(valsSlot), written)},
	}
	if keyArgument != nil {
		key, keyOk := RhsEffect(context, context.Sorts[keysSlot], keyArgument)
		if !keyOk {
			return nil, false
		}
		out = append(out, AssignmentTarget{Target: keysSlot, Effect: joinEffect(varEffect(keysSlot), key)})
	}
	return out, true
}

// nonNegativeIntegers is the honest floor a delete leaves behind: the
// integers from zero up. A delete that MISSES leaves the count where it
// was and a delete that hits drops it by one, and the two-slot world has
// no spelling for "the old reading, or one less, but never below zero" —
// the decrement is not claimable. Joining this ray with the old reading
// keeps the miss admitted and never claims a value the collection cannot
// have.
func nonNegativeIntegers() kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0)),
	}
}

// MapDeleteAssignmentsOf is `m.delete(k)` / `s.delete(v)` as a
// statement: the size slot takes the non-negative integer ray joined
// with its old reading, and the value and key slots are untouched —
// removal never adds a value, so the joined summaries stay sound where
// they stand.
//
// The precise decrement is deliberately NOT claimed: a delete on a key
// the collection does not hold answers false and moves nothing, and
// nothing here tells that case from the hit.
func MapDeleteAssignmentsOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	call := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsCallExpression(call) {
		return nil, false
	}
	access := call.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(access) {
		return nil, false
	}
	receiver := access.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return nil, false
	}
	method, arguments, isCall := collectionMethodCallOf(call, receiver.Text())
	if !isCall || method != "delete" || len(arguments) != 1 {
		return nil, false
	}
	sizeSlot, _, _, _, ok := mapSlotsOf(context, receiver.Text())
	if !ok {
		return nil, false
	}
	return []AssignmentTarget{
		{Target: sizeSlot, Effect: joinEffect(nonNegativeIntegers(), varEffect(sizeSlot))},
	}, true
}

// MapIterationSlotOf resolves an iterated expression to the slot each
// pass hands the element binding: `s` and `m.values()` answer the value
// slot, `m.keys()` the key slot. A Map iterated BARE or through
// `entries()` hands a PAIR, which one slot cannot spell — pairIterated
// says so and MapForOfLowering takes the two-name route instead.
func MapIterationSlotOf(context *LoweringContext, iterated *ast.Node) (slot int, pairIterated bool, ok bool) {
	head := Unwrapped(iterated)
	if ast.IsIdentifier(head) {
		_, valsSlot, _, keysOk, found := mapSlotsOf(context, head.Text())
		if !found {
			return 0, false, false
		}
		// a bare Map iterates its ENTRIES; a bare Set iterates its members
		if keysOk {
			return 0, true, true
		}
		return valsSlot, false, true
	}
	access := head
	if !ast.IsCallExpression(access) {
		return 0, false, false
	}
	property := access.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(property) {
		return 0, false, false
	}
	receiver := property.AsPropertyAccessExpression().Expression
	if !ast.IsIdentifier(receiver) {
		return 0, false, false
	}
	view, isView := iteratorCallOf(head, receiver.Text())
	if !isView {
		return 0, false, false
	}
	_, valsSlot, keysSlot, keysOk, found := mapSlotsOf(context, receiver.Text())
	if !found {
		return 0, false, false
	}
	switch view {
	case "values":
		return valsSlot, false, true
	case "keys":
		if !keysOk {
			return 0, false, false
		}
		return keysSlot, false, true
	case "entries":
		if !keysOk {
			return 0, false, false
		}
		return 0, true, true
	}
	return 0, false, false
}

// MapEntrySlotsOf is the (keys, vals) slot pair a PAIR iteration hands
// out — `for (const [k, v] of m)` and `of m.entries()`.
func MapEntrySlotsOf(context *LoweringContext, iterated *ast.Node) (keysSlot int, valsSlot int, ok bool) {
	head := Unwrapped(iterated)
	name := ""
	if ast.IsIdentifier(head) {
		name = head.Text()
	} else if ast.IsCallExpression(head) {
		property := head.AsCallExpression().Expression
		if !ast.IsPropertyAccessExpression(property) {
			return 0, 0, false
		}
		receiver := property.AsPropertyAccessExpression().Expression
		if !ast.IsIdentifier(receiver) {
			return 0, 0, false
		}
		view, isView := iteratorCallOf(head, receiver.Text())
		if !isView || view != "entries" {
			return 0, 0, false
		}
		name = receiver.Text()
	}
	if name == "" {
		return 0, 0, false
	}
	_, valsSlot, keysSlot, keysOk, found := mapSlotsOf(context, name)
	if !found || !keysOk {
		return 0, 0, false
	}
	return keysSlot, valsSlot, true
}

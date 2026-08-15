// split from ir_map_slots.go — the slot layout and the reads
//
// The slot vector a flattened collection contributes, the resolution of
// a spelled name to those slots, and the two reads that stand on them:
// `m.size` and `m.get(k)`.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

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

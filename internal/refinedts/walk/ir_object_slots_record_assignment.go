// split from ir_object_slots.go — the whole-record reassignment lowering

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// RecordAssignmentOf is the record REASSIGNMENT lowering: `p = q`
// between two flattened records of the same leaf shape, and `p = { … }`
// where the literal spells exactly p's leaves. Both become one ordinary
// assignment per leaf, in the target's leaf order — a whole-record
// write the kernel never sees as a record.
//
// The shapes are read from the slot vector, not from a recognizer
// table: a leaf slot exists exactly where the recognizer admitted the
// record, so "every leaf of p has a slot and every matching leaf of the
// source has one too" IS the shape agreement. Declines on any leaf
// without a slot, exactly the total-or-decline law.
func RecordAssignmentOf(context *LoweringContext, statement *ast.Node) ([]AssignmentTarget, bool) {
	if !ast.IsExpressionStatement(statement) {
		return nil, false
	}
	e := Unwrapped(statement.AsExpressionStatement().Expression)
	if !ast.IsBinaryExpression(e) {
		return nil, false
	}
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken || !ast.IsIdentifier(bin.Left) {
		return nil, false
	}
	target := bin.Left.Text()
	leaves, ok := leafSlotsUnder(context, target)
	if !ok {
		return nil, false
	}
	right := Unwrapped(bin.Right)
	// `p = q`: each of p's leaves copies q's leaf of the same path
	if ast.IsIdentifier(right) {
		source := right.Text()
		sourceLeaves, sourceOk := leafSlotsUnder(context, source)
		if !sourceOk || len(sourceLeaves) != len(leaves) {
			return nil, false
		}
		sourceOfPath := map[string]int{}
		for _, leaf := range sourceLeaves {
			sourceOfPath[leaf.Path] = leaf.Index
		}
		out := make([]AssignmentTarget, 0, len(leaves))
		for _, leaf := range leaves {
			from, found := sourceOfPath[leaf.Path]
			if !found {
				return nil, false
			}
			out = append(out, AssignmentTarget{Target: leaf.Index, Effect: varStateEffect(from)})
		}
		return out, true
	}
	// `p = { … }`: the literal's leaves must be exactly p's, and each row
	// lowers through the shared RHS grammar into its leaf's slot. The
	// rows are matched to leaves BY PATH — a literal spelling its keys in
	// another order is the same record.
	if ast.IsObjectLiteralExpression(right) {
		rows, rowsOk := flatKeysOfLiteral(right, target, nil)
		if !rowsOk || len(rows) != len(leaves) {
			return nil, false
		}
		slotOfPath := map[string]int{}
		for _, leaf := range leaves {
			slotOfPath[leaf.Path] = leaf.Index
		}
		out := make([]AssignmentTarget, 0, len(rows))
		for _, row := range rows {
			slot, found := slotOfPath[strings.Join(row.Path, ".")]
			if !found {
				return nil, false
			}
			effect, effectOk := RhsEffect(context, context.Sorts[slot], row.Initializer)
			if !effectOk {
				return nil, false
			}
			out = append(out, AssignmentTarget{Target: slot, Effect: asVarStateEffect(effect)})
		}
		return out, true
	}
	return nil, false
}

// leafSlot is one flattened leaf found in the slot vector: its path
// below the holder and the slot it sits in.
type leafSlot struct {
	Path  string
	Index int
}

// leafSlotsUnder is every slot whose spelled name is a path below the
// holder — the flattened record's leaves, in slot order. Answers false
// where the holder has no leaves at all (an unflattened name, or one
// the recognizer declined).
func leafSlotsUnder(context *LoweringContext, holder string) ([]leafSlot, bool) {
	prefix := holder + "."
	// a name with a slot OF ITS OWN is a scalar, not a flattened record —
	// its assignments take the ordinary single-slot route
	if _, whole := slotIndexOfName(context, holder); whole {
		return nil, false
	}
	// an ARRAY's two slots ride the same "name.suffix" spelling but are
	// not record leaves — a whole-array assignment is not a leaf-for-leaf
	// write, so it takes no route here
	if _, _, isArray := arraySlotsOf(context, holder); isArray {
		return nil, false
	}
	var out []leafSlot
	collect := func(spelled string, index int) {
		if strings.HasPrefix(spelled, prefix) {
			out = append(out, leafSlot{Path: strings.TrimPrefix(spelled, prefix), Index: index})
		}
	}
	if context.Names != nil {
		// the closed map has no order of its own; the caller compares two
		// records leaf for leaf, so both sides are sorted by path
		for spelled, index := range context.Names {
			collect(spelled, index)
		}
	} else {
		for index, binding := range context.Bindings {
			collect(binding, index)
		}
	}
	sortLeafSlots(out)
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// sortLeafSlots orders leaves by path so two records compare leaf for
// leaf regardless of how their slots were laid out.
func sortLeafSlots(slots []leafSlot) {
	for i := 1; i < len(slots); i++ {
		for j := i; j > 0 && slots[j].Path < slots[j-1].Path; j-- {
			slots[j], slots[j-1] = slots[j-1], slots[j]
		}
	}
}

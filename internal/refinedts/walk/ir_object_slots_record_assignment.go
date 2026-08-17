// split from ir_object_slots.go — the whole-record reassignment lowering

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
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
		// `p = { ...p, x1: e, … }` — the leading SELF-spread copies every
		// unmentioned leaf onto itself, so the statement IS the explicit
		// rows: one ordinary assignment per spelled leaf, the rest
		// untouched. Admitted only with the spread FIRST (a later explicit
		// key overwrites the spread's copy, which per-leaf order preserves)
		// and only where every explicit key covers its WHOLE subtree of the
		// target's leaves — a nested literal replaces its key's subtree
		// entire, so a partial spelling would keep a stale sibling leaf.
		if rows, ok := selfSpreadOverlayRows(context, right, target); ok {
			return lowerRecordRows(context, target, rows, leavesByPath(leaves))
		}
		rows, rowsOk := flatKeysOfLiteral(right, target, nil)
		if !rowsOk || len(rows) != len(leaves) {
			return nil, false
		}
		return lowerRecordRows(context, target, rows, leavesByPath(leaves))
	}
	return nil, false
}

// leavesByPath indexes a leaf list by its path spelling.
func leavesByPath(leaves []leafSlot) map[string]int {
	slotOfPath := map[string]int{}
	for _, leaf := range leaves {
		slotOfPath[leaf.Path] = leaf.Index
	}
	return slotOfPath
}

// lowerRecordRows lowers explicit literal rows into their leaf slots, in
// row order, with THE EVALUATION-ORDER GUARD: JavaScript evaluates every
// initializer against the OLD record — the literal builds first, the
// binding rebinds after — while these per-leaf assignments land one at a
// time. A row whose initializer reads a leaf an EARLIER row already
// wrote would therefore read the new value where the runtime read the
// old one, so that shape declines. A row reading the record WHOLE (a
// bare mention) declines the same way — which leaf it reads is not
// spelled.
func lowerRecordRows(
	context *LoweringContext,
	target string,
	rows []ObjectLocalKey,
	slotOfPath map[string]int,
) ([]AssignmentTarget, bool) {
	written := map[string]struct{}{}
	out := make([]AssignmentTarget, 0, len(rows))
	for _, row := range rows {
		path := strings.Join(row.Path, ".")
		slot, found := slotOfPath[path]
		if !found {
			return nil, false
		}
		if readsAnyWrittenLeaf(row.Initializer, target, written) {
			return nil, false
		}
		effect, effectOk := RhsEffect(context, context.Sorts[slot], row.Initializer)
		if !effectOk {
			return nil, false
		}
		out = append(out, AssignmentTarget{Target: slot, Effect: asVarStateEffect(effect)})
		written[path] = struct{}{}
	}
	return out, true
}

// readsAnyWrittenLeaf scans an initializer for reads of the target
// record that an earlier row's write has made stale: a `target.<path>`
// read whose path is already written, or any BARE mention of the target
// (the whole record, whose leaves this cannot tell apart).
func readsAnyWrittenLeaf(initializer *ast.Node, target string, written map[string]struct{}) bool {
	if initializer == nil || len(written) == 0 {
		return false
	}
	stale := false
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if stale {
			return true
		}
		if root, path, isPath := propertyPathAdmittingRootOptionalStep(node); isPath && root == target {
			if _, hit := written[strings.Join(path, ".")]; hit {
				stale = true
			}
			return true
		}
		if ast.IsIdentifier(node) && node.Text() == target {
			stale = true
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(initializer)
	return stale
}

// selfSpreadOverlayRows recognizes `p = { ...p, <plain rows> }` — the
// spread of the TARGET ITSELF, first, followed by ordinary rows — and
// answers the explicit rows with each key's subtree verified COMPLETE
// against the target's own leaves (lowerRecordRows' caller comment says
// why a partial subtree cannot lower).
func selfSpreadOverlayRows(
	context *LoweringContext,
	literal *ast.Node,
	target string,
) ([]ObjectLocalKey, bool) {
	properties := literal.AsObjectLiteralExpression().Properties.Nodes
	if len(properties) < 2 || !ast.IsSpreadAssignment(properties[0]) {
		return nil, false
	}
	source := Unwrapped(properties[0].AsSpreadAssignment().Expression)
	if source == nil || !ast.IsIdentifier(source) || source.Text() != target {
		return nil, false
	}
	var c *checker.Checker
	if context != nil && context.Flow != nil {
		c = checkerOf(context.Flow)
	}
	rows, ok := flatKeysOfLiteralAfterLeadingSpread(c, literal, target)
	if !ok {
		return nil, false
	}
	leaves, leavesOk := leafSlotsUnder(context, target)
	if !leavesOk {
		return nil, false
	}
	// subtree completeness per explicit top-level key: the rows under a
	// key must spell exactly the target's leaves under that key
	rowPathsUnder := map[string]map[string]struct{}{}
	for _, row := range rows {
		key := row.Path[0]
		if rowPathsUnder[key] == nil {
			rowPathsUnder[key] = map[string]struct{}{}
		}
		rowPathsUnder[key][strings.Join(row.Path, ".")] = struct{}{}
	}
	leafPathsUnder := map[string]map[string]struct{}{}
	for _, leaf := range leaves {
		key := leaf.Path
		if dot := strings.Index(key, "."); dot >= 0 {
			key = key[:dot]
		}
		if leafPathsUnder[key] == nil {
			leafPathsUnder[key] = map[string]struct{}{}
		}
		leafPathsUnder[key][leaf.Path] = struct{}{}
	}
	for key, rowPaths := range rowPathsUnder {
		leafPaths, keyExists := leafPathsUnder[key]
		if !keyExists || len(rowPaths) != len(leafPaths) {
			return nil, false
		}
		for path := range rowPaths {
			if _, held := leafPaths[path]; !held {
				return nil, false
			}
		}
	}
	return rows, true
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

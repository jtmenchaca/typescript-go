// from bindings/class_field_invariants.ts
//
// Field invariants: what a class's PRIVATE field provably holds at
// every read. The invariant is the join of the field's initializer
// with every value the class's own text writes into it — collected
// by walking each member body once, silently, with the writes
// sinking. Privacy is the boundary that makes the collection
// complete: tsc refuses outside writes, and any use of `this` that
// is not a plain `this.key` (an alias, a hand-over, a computed
// write) declines the whole class rather than guessing.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// invariantMemoMu guards invariantMemo: one answer per class
// declaration. The TS source keys this memo with a
// `WeakMap<ts.ClassDeclaration | ts.ClassExpression, ... | null>`; Go
// has no weak maps, so this substitutes a regular map guarded by a
// mutex — functionally identical per program (entries live exactly
// as long as the program that produced the declarations is in use by
// this port; see annotations/annotation_of_type.go's
// readingTypeNodes for the same substitution).
var (
	invariantMemoMu sync.Mutex
	invariantMemo   = map[*ast.Node]map[string]abstractdomain.AbstractValue{}
	// invariantMemoSet tracks which declarations have an entry at
	// all — the TS Map distinguishes "no entry" from "entry holding
	// null" (re-entry in progress, or a computed answer that turned
	// out empty is still a real, non-nil map), which a bare Go map
	// lookup on invariantMemo cannot: a nil map value and a missing
	// key both read as (nil, false) through the plain `ok` idiom, but
	// `held.set(declaration, null)` (re-entry) must be reported
	// exactly the same way as a null result the TS source treats as
	// "no plain-key discipline, no invariant".
	invariantMemoSet = map[*ast.Node]bool{}
)

// writtenAtKind is the "plain" | "other" tag writtenAt answers in the
// TS source, plus a not-written case (TS: null).
type writtenAtKind int

const (
	writtenAtNone writtenAtKind = iota
	writtenAtPlain
	writtenAtOther
)

// writtenAt: is this node in a WRITE position — an assignment target
// (direct or inside a destructuring pattern), an update, or a delete?
func writtenAt(node *ast.Node) writtenAtKind {
	parent := node.Parent
	if parent == nil {
		return writtenAtNone
	}
	if ast.IsBinaryExpression(parent) {
		be := parent.AsBinaryExpression()
		if be.Left == node && be.OperatorToken.Kind == ast.KindEqualsToken {
			return writtenAtPlain
		}
		if be.Left == node && be.OperatorToken.Kind >= ast.KindFirstCompoundAssignment && be.OperatorToken.Kind <= ast.KindLastCompoundAssignment {
			return writtenAtOther
		}
	}
	if ast.IsPostfixUnaryExpression(parent) {
		operator := parent.AsPostfixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return writtenAtOther
		}
	}
	if ast.IsPrefixUnaryExpression(parent) {
		operator := parent.AsPrefixUnaryExpression().Operator
		if operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken {
			return writtenAtOther
		}
	}
	if ast.IsDeleteExpression(parent) {
		return writtenAtOther
	}
	// a pattern target: some ancestor is the LEFT of an assignment
	cursor := node
	for cursor.Parent != nil && (ast.IsArrayLiteralExpression(cursor.Parent) ||
		ast.IsObjectLiteralExpression(cursor.Parent) ||
		ast.IsPropertyAssignment(cursor.Parent) ||
		ast.IsShorthandPropertyAssignment(cursor.Parent) ||
		ast.IsSpreadElement(cursor.Parent)) {
		cursor = cursor.Parent
	}
	if cursor.Parent != nil && ast.IsBinaryExpression(cursor.Parent) {
		be := cursor.Parent.AsBinaryExpression()
		if be.Left == cursor && cursor != node {
			return writtenAtOther
		}
	}
	return writtenAtNone
}

// FieldInvariantsOf is the invariants of a class's private fields,
// or nil where `this` escapes the plain-key discipline. Memoized per
// declaration; the collection walk runs each member body once.
func FieldInvariantsOf(ctx *FlowContext, declaration *ast.Node) map[string]abstractdomain.AbstractValue {
	invariantMemoMu.Lock()
	if invariantMemoSet[declaration] {
		held := invariantMemo[declaration]
		invariantMemoMu.Unlock()
		return held
	}
	invariantMemoSet[declaration] = true
	invariantMemo[declaration] = nil // re-entry answers nothing
	invariantMemoMu.Unlock()
	computed := computeFieldInvariants(ctx, declaration)
	invariantMemoMu.Lock()
	invariantMemo[declaration] = computed
	invariantMemoMu.Unlock()
	return computed
}

func computeFieldInvariants(ctx *FlowContext, declaration *ast.Node) map[string]abstractdomain.AbstractValue {
	// every `this` must be a plain `this.key` receiver — anything
	// else (an alias, an argument, an element access) can write
	// fields where the collection cannot see
	escapes := false
	poisoned := map[string]struct{}{}
	var inspect func(node *ast.Node)
	inspect = func(node *ast.Node) {
		if escapes {
			return
		}
		if node.Kind == ast.KindThisKeyword {
			parent := node.Parent
			if !ast.IsPropertyAccessExpression(parent) || parent.AsPropertyAccessExpression().Expression != node {
				escapes = true
				return
			}
			if writtenAt(parent) == writtenAtOther {
				poisoned[parent.AsPropertyAccessExpression().Name().Text()] = struct{}{}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			inspect(child)
			return false
		})
	}
	inspect(declaration)
	if escapes {
		return nil
	}

	// the candidate fields: private and non-static — a `#name` is
	// private by the language itself, a plain name needs the `private`
	// modifier for tsc to refuse outside writes. A field with no
	// initializer holds `undefined` until a write lands, so the absent
	// value joins the writes.
	var candidateOrder []string
	candidates := map[string]abstractdomain.AbstractValue{}
	// method bodies run at times this scope cannot place, so nothing
	// walk-ordered survives into the collection walk
	silent := *ctx
	silent.Report = func(d assignability.RefinementDiagnostic) {}
	silent.ReturnSink = nil
	silent.ThrowSink = nil
	silent.ThisWriteSink = nil
	silent.Declared = map[string]*annotations.DeclaredRefinement{}
	silent.DifferenceConstraints = nil
	silent.GateAssumptions = nil

	members := declaration.ClassLikeData().Members.Nodes
	for _, member := range members {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		pd := member.AsPropertyDeclaration()
		name := pd.Name()
		if !ast.IsIdentifier(name) && !ast.IsPrivateIdentifier(name) {
			continue
		}
		flags := ast.GetCombinedModifierFlags(member)
		if flags&ast.ModifierFlagsStatic != 0 {
			continue
		}
		if flags&ast.ModifierFlagsPrivate == 0 && !ast.IsPrivateIdentifier(name) {
			continue
		}
		nameText := name.Text()
		if _, isPoisoned := poisoned[nameText]; isPoisoned {
			continue
		}
		env := Env{}
		var value abstractdomain.AbstractValue
		if pd.Initializer == nil {
			value = abstractdomain.Undef
		} else {
			value = evaluateExpression(&silent, env, pd.Initializer)
		}
		if _, seen := candidates[nameText]; !seen {
			candidateOrder = append(candidateOrder, nameText)
		}
		candidates[nameText] = value
	}
	if len(candidates) == 0 {
		return map[string]abstractdomain.AbstractValue{}
	}

	// the collection walk: each member body once, writes sinking —
	// field reads answer nothing during collection, so no invariant
	// rests on itself
	sink := map[string][]abstractdomain.AbstractValue{}
	collecting := silent
	collecting.ThisWriteSink = sink
	for _, member := range members {
		if !ast.IsConstructorDeclaration(member) && !ast.IsMethodDeclaration(member) &&
			!ast.IsGetAccessorDeclaration(member) && !ast.IsSetAccessorDeclaration(member) {
			continue
		}
		body := member.Body()
		if body == nil {
			continue
		}
		env := Env{}
		if ast.IsConstructorDeclaration(member) || ast.IsMethodDeclaration(member) || ast.IsSetAccessorDeclaration(member) {
			for _, parameter := range member.Parameters() {
				pname := parameter.AsParameterDeclaration().Name()
				if ast.IsIdentifier(pname) {
					env[pname.Text()] = silence.Residue()
				}
			}
		}
		AnalyzeStatements(&collecting, env, body.AsBlock().Statements.Nodes, nil)
	}

	invariants := map[string]abstractdomain.AbstractValue{}
	for _, name := range candidateOrder {
		joined := candidates[name]
		for _, written := range sink[name] {
			joined = abstractdomain.JoinKnown(joined, written)
		}
		if joined.Kind != abstractdomain.KindUnknown {
			invariants[name] = joined
		}
	}
	return invariants
}

// InitialThisStateOf is what `this` provably holds at `site`: an
// incomplete object whose keys are the enclosing class's declared
// instance fields — each wearing its field invariant where the
// collection above computed one, unknown otherwise (so a guard on
// the key still has a place to narrow). Nil wherever `this` is not a
// class instance.
func InitialThisStateOf(ctx *FlowContext, site *ast.Node) *abstractdomain.AbstractValue {
	declaration := dataflowfacts.EnclosingThisClass(site)
	if declaration == nil {
		return nil
	}
	var keyOrder []string
	keys := map[string]abstractdomain.AbstractValue{}
	for _, member := range declaration.ClassLikeData().Members.Nodes {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		pd := member.AsPropertyDeclaration()
		name := pd.Name()
		if !ast.IsIdentifier(name) && !ast.IsPrivateIdentifier(name) {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		nameText := name.Text()
		if _, ok := keys[nameText]; !ok {
			keyOrder = append(keyOrder, nameText)
		}
		// a field with no invariant holds whatever the object's life put
		// there — OUTSIDE this walk's determination, so the provenance
		// rides the read (OPAQUE), exactly as it does for a property
		// read through an opaque receiver
		keys[nameText] = abstractdomain.Opaque
	}
	invariants := FieldInvariantsOf(ctx, declaration)
	if invariants != nil {
		for name, held := range invariants {
			if _, ok := keys[name]; !ok {
				keyOrder = append(keyOrder, name)
			}
			keys[name] = held
		}
	}
	out := make([]abstractdomain.ObjectKey, len(keyOrder))
	for i, name := range keyOrder {
		out[i] = abstractdomain.ObjectKey{Name: name, Value: keys[name]}
	}
	result := abstractdomain.KnownObject(out, nil, false, abstractdomain.TrustProved, false)
	return &result
}

// from bindings/class_field_invariants.ts
//
// Field invariants: what a class's PRIVATE field provably holds at
// every read. The invariant is the join of the field's initializer
// with every value the class's own text writes into it — collected
// by walking each member body once, silently, with the writes
// sinking. Privacy is the boundary that makes the collection
// complete: tsc refuses outside writes, and any use of `this` that
// is not a plain `this.key` (an alias, a hand-over, a computed
// write) hands the instance out. After such an escape only the
// `#`-named fields keep invariants — those are private at RUNTIME, so
// no outside holder can write them and this walk still reads every
// write; a `private`-modifier field loses its invariant, because the
// modifier is erased and the escaped reference writes it freely.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
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

// numericStepWrite: is this write position a compound step whose
// result is a NUMBER on every run? `-=`, `*=`, `/=`, `%=`, `**=`, the
// bitwise and shift compounds, and `++`/`--` all run ToNumeric on
// what they read before they write, so the value that lands is a
// number (NaN included). `+=` is excluded — it concatenates when
// either side is a string — and so are the logical compounds, whose
// result is whatever the right side held.
func numericStepWrite(node *ast.Node) bool {
	parent := node.Parent
	if parent == nil {
		return false
	}
	if ast.IsBinaryExpression(parent) {
		be := parent.AsBinaryExpression()
		if be.Left != node {
			return false
		}
		switch be.OperatorToken.Kind {
		case ast.KindMinusEqualsToken, ast.KindAsteriskEqualsToken,
			ast.KindSlashEqualsToken, ast.KindPercentEqualsToken,
			ast.KindAsteriskAsteriskEqualsToken,
			ast.KindAmpersandEqualsToken, ast.KindBarEqualsToken,
			ast.KindCaretEqualsToken, ast.KindLessThanLessThanEqualsToken,
			ast.KindGreaterThanGreaterThanEqualsToken,
			ast.KindGreaterThanGreaterThanGreaterThanEqualsToken:
			return true
		}
		return false
	}
	if ast.IsPostfixUnaryExpression(parent) {
		operator := parent.AsPostfixUnaryExpression().Operator
		return operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken
	}
	if ast.IsPrefixUnaryExpression(parent) {
		operator := parent.AsPrefixUnaryExpression().Operator
		return operator == ast.KindPlusPlusToken || operator == ast.KindMinusMinusToken
	}
	return false
}

// FieldInvariantsOf is the invariants of a class's private fields —
// the `#`-named ones survive a `this` escape, the modifier-private
// ones do not. Memoized per declaration; the collection walk runs each
// member body once. Nil only on re-entry, where the answer would rest
// on itself.
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
	// else (an alias, an argument, an element access) hands the WHOLE
	// instance to code this walk does not read, so a write could land
	// on any field from outside.
	//
	// An escape does not veto the `#`-named fields, and the reason is
	// the LANGUAGE, not the type checker: a `#name` is private at
	// RUNTIME — a holder of the escaped reference that is not lexically
	// inside this class body cannot read or write `#name` at all (it is
	// a TypeError, not a type error). So every write to a `#` field is
	// spelled inside this class body, and the collection walk below
	// reads that whole body. The escaped reference can only reach those
	// fields by calling back into a method of this class, whose writes
	// are exactly what the walk already collects.
	//
	// A field private by MODIFIER only (`private x`) has no such
	// guarantee: the modifier is erased at runtime, so an alias typed
	// loosely writes it freely, and the collection would be reading
	// half the writes. Those fields keep the whole-class veto. A
	// constructor-only-written `private` field is NOT airtight either,
	// because the constructor can hand `this` out before it finishes
	// writing, and the holder writes the field afterwards.
	escapes := false
	poisoned := map[string]struct{}{}
	// the fields whose ONLY non-plain writes are numeric steps — those
	// widen to the number ground rather than dropping out
	numericallyStepped := map[string]struct{}{}
	var inspect func(node *ast.Node)
	inspect = func(node *ast.Node) {
		if node.Kind == ast.KindThisKeyword {
			parent := node.Parent
			if !ast.IsPropertyAccessExpression(parent) || parent.AsPropertyAccessExpression().Expression != node {
				escapes = true
			} else if writtenAt(parent) == writtenAtOther {
				key := parent.AsPropertyAccessExpression().Name().Text()
				// a NUMERIC compound step (`this.#n++`, `this.#n *= k`, and
				// kin) writes a number on every run whatever it read —
				// ToNumeric runs on both sides first (tmp/ecma262/spec.html
				// sec-compound-assignment-operators, sec-postfix-increment-
				// operator) — so the field widens to the number ground
				// instead of dropping out. `+=` is NOT one of these: it
				// concatenates when either side is a string. `delete`, the
				// logical compounds, and every other write keep the poison.
				if numericStepWrite(parent) {
					if _, already := poisoned[key]; !already {
						numericallyStepped[key] = struct{}{}
					}
				} else {
					delete(numericallyStepped, key)
					poisoned[key] = struct{}{}
				}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			inspect(child)
			return false
		})
	}
	inspect(declaration)

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
		// after an escape only the `#`-named fields survive: the language
		// keeps outside code from writing them at all, so this walk still
		// reads every write. A `private`-modifier field is writable
		// through the escaped reference at runtime.
		if escapes && !ast.IsPrivateIdentifier(name) {
			continue
		}
		nameText := name.Text()
		if _, isPoisoned := poisoned[nameText]; isPoisoned {
			continue
		}
		env := NewEnv()
		var value abstractdomain.AbstractValue
		if _, stepped := numericallyStepped[nameText]; stepped {
			// a numeric step runs an unknown number of times over the
			// object's life, so the field's own initializer no longer pins
			// the value — what survives every run is the number ground
			value = abstractdomain.PossiblyNaN(abstractdomain.KnownSet(
				refinementsets.RefinedSet{}, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
		} else if pd.Initializer == nil {
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
		env := NewEnv()
		if ast.IsConstructorDeclaration(member) || ast.IsMethodDeclaration(member) || ast.IsSetAccessorDeclaration(member) {
			for _, parameter := range member.Parameters() {
				pname := parameter.AsParameterDeclaration().Name()
				if ast.IsIdentifier(pname) {
					env.Set(pname.Text(), silence.Residue())
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

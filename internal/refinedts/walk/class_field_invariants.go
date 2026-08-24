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
//
// A NON-private field has no language fence at all, so its invariant
// stands only where the FILE is the fence: the public-field seal
// (class_public_field_seal.go) proves the class and its instances
// never leave the file and no non-`this` text writes the field.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// invariantMemoMu guards invariantMemo: one answer per (checker, class
// declaration). The TS source keys this memo with a
// `WeakMap<ts.ClassDeclaration | ts.ClassExpression, ... | null>`; Go
// has no weak maps, so this substitutes a regular map guarded by a
// mutex — functionally identical per program (entries live exactly
// as long as the program that produced the declarations is in use by
// this port; see annotations/annotation_of_type.go's
// readingTypeNodes for the same substitution).
//
// The CONTENT is computed through computeFieldInvariants, which walks
// method bodies with evaluateExpression/AnalyzeStatements — reads that
// go through ctx.P.Checker — so a bare-node key lets whichever
// parallel worker's checker finishes first hand its invariant map to
// every later reader, on any checker (typereading/host_type_memo.go
// and summary_registry.go's summaryKey carry the same (checker, node)
// discipline for the same reason: a checker is a worker-exclusive
// lazy resolver).
var (
	invariantMemoMu sync.Mutex
	invariantMemo   = map[invariantKey]map[string]abstractdomain.AbstractValue{}
	// invariantMemoSet tracks which (checker, declaration) pairs have
	// an entry at all — the TS Map distinguishes "no entry" from
	// "entry holding null" (re-entry in progress, or a computed answer
	// that turned out empty is still a real, non-nil map), which a
	// bare Go map lookup on invariantMemo cannot: a nil map value and
	// a missing key both read as (nil, false) through the plain `ok`
	// idiom, but `held.set(declaration, null)` (re-entry) must be
	// reported exactly the same way as a null result the TS source
	// treats as "no plain-key discipline, no invariant".
	invariantMemoSet = map[invariantKey]bool{}
)

// invariantKey pairs the checker with the class declaration — see the
// header above for why the checker rides along.
type invariantKey struct {
	checker     *checker.Checker
	declaration *ast.Node
}

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

// symbolKeyedThisAccess answers the field name behind a `this[S]` where
// the `this` handed in is the ELEMENT ACCESS's own receiver and S is a
// stable symbol const (ir_field_bundles.go's key identity). The name is
// the derived `#sym:` spelling, so the declaration, every write, and
// every read of one symbol land on one key in the maps below.
func symbolKeyedThisAccess(c *checker.Checker, thisNode *ast.Node) (string, bool) {
	parent := thisNode.Parent
	if parent == nil || !ast.IsElementAccessExpression(parent) {
		return "", false
	}
	if Unwrapped(parent.AsElementAccessExpression().Expression) != thisNode {
		return "", false
	}
	return SymbolKeyedFieldName(c, parent)
}

// ExportedSymbolConst is whether the symbol const a key names is
// EXPORTED from its module — the split that says how private the field
// it keys really is.
//
// A symbol property is reachable only by code holding the symbol VALUE.
// An UNEXPORTED module-level const never hands that value out, so the
// only writers of the field are the module's own text: closer to `#`
// privacy than to the `private` modifier, which is erased at runtime and
// guards nothing. An EXPORTED const hands the key to every importer, and
// any of them can write the field — so it is exactly as open as a public
// field, and takes the treatment a public field takes.
//
// It reads the const's own declaration statement, which is where the
// `export` modifier sits (a `const` inside a VariableStatement), and the
// module's `export { S }` list, which exports the same binding without
// touching the declaration. Anything it cannot read answers TRUE — an
// unread export is the shape that would wrongly claim privacy.
func ExportedSymbolConst(c *checker.Checker, key *ast.Node) bool {
	if c == nil || key == nil {
		return true
	}
	symbol := symbolAt(c, key)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return true
	}
	declaration := symbol.ValueDeclaration
	if declaration.Parent == nil || declaration.Parent.Parent == nil {
		return true
	}
	statement := declaration.Parent.Parent
	if ast.GetCombinedModifierFlags(statement)&ast.ModifierFlagsExport != 0 {
		return true
	}
	sourceFile := ast.GetSourceFileOfNode(declaration)
	if sourceFile == nil {
		return true
	}
	// `export { S }` names the binding from a separate clause, so the
	// declaration carries no modifier and the file's own export
	// statements are what say the const left the module
	name := declaration.AsVariableDeclaration().Name()
	if name == nil || !ast.IsIdentifier(name) {
		return true
	}
	spelled := name.Text()
	for _, statement := range sourceFile.Statements.Nodes {
		if !ast.IsExportDeclaration(statement) {
			continue
		}
		exportDeclaration := statement.AsExportDeclaration()
		if exportDeclaration.ModuleSpecifier != nil {
			// `export … from "other"` re-exports another module's bindings,
			// never this file's own const
			continue
		}
		clause := exportDeclaration.ExportClause
		if clause == nil || !ast.IsNamedExports(clause) {
			// a clause this reading does not spell — the const may be in it
			return true
		}
		for _, element := range clause.AsNamedExports().Elements.Nodes {
			specifier := element.AsExportSpecifier()
			// the LOCAL name is PropertyName under `export { S as T }` and
			// Name() under a bare `export { S }`
			local := specifier.PropertyName
			if local == nil {
				local = specifier.Name()
			}
			if local != nil && ast.IsIdentifier(local) && local.Text() == spelled {
				return true
			}
		}
	}
	return false
}

// constructorUnconditionallyWritesField: does this class's own
// constructor write `name` on EVERY construction path — a parameter
// property declaring the field (the runtime's own unconditional
// prelude fill), or a plain `this.name = value` (or `this.#name =
// value`) statement sitting directly in the constructor's own
// top-level statement list, never inside an `if`/`for`/`while`/
// `switch`/`try`/label or a nested function where some calls could
// skip it? Straight-line only: a write nested one level inside any of
// those constructs is not proven to run on every call, so it answers
// false and the ordinary Undef seed stands.
func constructorUnconditionallyWritesField(declaration *ast.Node, name string) bool {
	if declaration == nil || !ast.IsClassLike(declaration) {
		return false
	}
	var constructorDeclaration *ast.Node
	for _, member := range declaration.ClassLikeData().Members.Nodes {
		if ast.IsConstructorDeclaration(member) {
			constructorDeclaration = member
			break
		}
	}
	if constructorDeclaration == nil {
		return false
	}
	for _, parameter := range constructorDeclaration.Parameters() {
		if !isParameterPropertyDeclaration(parameter) {
			continue
		}
		pd := parameter.AsParameterDeclaration()
		if pd.Name() != nil && (ast.IsIdentifier(pd.Name()) || ast.IsPrivateIdentifier(pd.Name())) && pd.Name().Text() == name {
			return true
		}
	}
	body := constructorDeclaration.Body()
	if body == nil || !ast.IsBlock(body) {
		return false
	}
	for _, statement := range body.AsBlock().Statements.Nodes {
		if straightLineThisFieldWrite(statement, name) {
			return true
		}
	}
	return false
}

// straightLineThisFieldWrite: is `statement` itself (not something
// nested inside it) a `this.name = value` (or `this.#name = value`)
// expression statement? Only the plain assignment form counts — a
// compound assignment or update reads the field's PRIOR value first,
// which this walk cannot prove came from anywhere on a fresh instance.
func straightLineThisFieldWrite(statement *ast.Node, name string) bool {
	if statement == nil || !ast.IsExpressionStatement(statement) {
		return false
	}
	expression := statement.AsExpressionStatement().Expression
	if expression == nil || !ast.IsBinaryExpression(expression) {
		return false
	}
	bin := expression.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindEqualsToken {
		return false
	}
	if !ast.IsPropertyAccessExpression(bin.Left) {
		return false
	}
	pa := bin.Left.AsPropertyAccessExpression()
	if pa.Expression.Kind != ast.KindThisKeyword {
		return false
	}
	fieldName := pa.Name()
	if fieldName == nil || !(ast.IsIdentifier(fieldName) || ast.IsPrivateIdentifier(fieldName)) {
		return false
	}
	return fieldName.Text() == name
}

// FieldInvariantsOf is the invariants of a class's private fields —
// the `#`-named ones survive a `this` escape, the modifier-private
// ones do not. Memoized per declaration; the collection walk runs each
// member body once. Nil only on re-entry, where the answer would rest
// on itself.
func FieldInvariantsOf(ctx *FlowContext, declaration *ast.Node) map[string]abstractdomain.AbstractValue {
	key := invariantKey{checker: checkerOf(ctx), declaration: declaration}
	invariantMemoMu.Lock()
	if invariantMemoSet[key] {
		held := invariantMemo[key]
		invariantMemoMu.Unlock()
		return held
	}
	invariantMemoSet[key] = true
	invariantMemo[key] = nil // re-entry answers nothing
	invariantMemoMu.Unlock()
	computed := computeFieldInvariants(ctx, declaration)
	invariantMemoMu.Lock()
	invariantMemo[key] = computed
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
	c := checkerOf(ctx)
	var inspect func(node *ast.Node)
	inspect = func(node *ast.Node) {
		if node.Kind == ast.KindThisKeyword {
			parent := node.Parent
			// `this[S]` under a STABLE SYMBOL const is a plain key access
			// like `this.x`: the const names one property, so this walk
			// still reads every write to it and the instance goes nowhere.
			// Every other bracketed key hands the instance to code that can
			// reach any property, which is the escape.
			if symbolKey, isSymbolKey := symbolKeyedThisAccess(c, node); isSymbolKey {
				if writtenAt(parent) == writtenAtOther {
					if numericStepWrite(parent) {
						if _, already := poisoned[symbolKey]; !already {
							numericallyStepped[symbolKey] = struct{}{}
						}
					} else {
						delete(numericallyStepped, symbolKey)
						poisoned[symbolKey] = struct{}{}
					}
				}
			} else if !ast.IsPropertyAccessExpression(parent) || parent.AsPropertyAccessExpression().Expression != node {
				escapes = true
			} else if writtenAt(parent) == writtenAtOther {
				key := parent.AsPropertyAccessExpression().Name().Text()
				// a NUMERIC compound step (`this.#n++`, `this.#n *= k`, and
				// kin) writes a number on every run whatever it read —
				// ToNumeric runs on both sides first (specifications/javascript/spec.html
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
	//
	// A NON-PRIVATE field joins the candidates only behind the
	// public-field seal (class_public_field_seal.go): the class value
	// and its instances provably never leave the file, so this file's
	// text holds every write — and the seal's own write census names
	// the fields some non-`this` text writes anyway, which stay out.
	var publicSeal PublicFieldSeal
	if !escapes {
		publicSeal = PublicFieldSealOf(ctx, declaration)
	}
	var candidateOrder []string
	candidates := map[string]abstractdomain.AbstractValue{}
	// unconditionalConstructorWrite: a no-initializer field the
	// constructor's own straight-line body writes on every construction
	// path. Its candidate takes no Undef seed — the final join below
	// starts from the constructor's write alone instead of joining an
	// arm the class's own text never allows.
	unconditionalConstructorWrite := map[string]struct{}{}
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
		flags := ast.GetCombinedModifierFlags(member)
		if flags&ast.ModifierFlagsStatic != 0 {
			continue
		}
		// THE SYMBOL-KEYED MEMBER, and where its privacy actually comes
		// from. `private readonly [INSTANCE_ID_SYMBOL]: string` is keyed
		// by a module-level symbol const, and a symbol property is
		// reachable only by code holding the symbol VALUE. Where the
		// module does not EXPORT that const, the only holders are the
		// module's own text — closer to `#` privacy than to the `private`
		// modifier, which is erased and guards nothing at runtime. Where
		// the module DOES export it (nest exports INSTANCE_ID_SYMBOL and
		// INSTANCE_METADATA_SYMBOL), every importer holds the key and can
		// write the field, so it is as open as a public field is.
		//
		// NEITHER SIDE OF THAT SPLIT BECOMES A CANDIDATE HERE, and the
		// reason is the collection, not the privacy: the write sink
		// records `this.key = v` stores by their DOTTED name, and a
		// `this[S] = v` store never reaches it. A candidate whose writes
		// the collection cannot see would answer its initializer alone —
		// `Undef` for the very field nest's constructor fills with a
		// string. So the symbol field takes no invariant, and its reads
		// answer the opaque floor (InitialThisStateOf seeds the key, and
		// the element read routes to the same reader the dotted read
		// uses). What the recognition DOES buy is above: `this[S]` is no
		// longer an escape, so every OTHER private field in the class
		// keeps the invariant that access used to wipe out.
		_, isSymbolKey := symbolMemberFieldName(c, name)
		var nameText string
		switch {
		case isSymbolKey:
			continue
		case name != nil && (ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name)):
			nameText = name.Text()
			if flags&ast.ModifierFlagsPrivate == 0 && !ast.IsPrivateIdentifier(name) {
				// a PUBLIC (or protected, or readonly) field: writable by
				// every holder of the instance, so it keeps an invariant
				// only where the seal proves this file's text is every
				// holder there is, and nothing outside `this` writes it
				if !publicSeal.Sealed {
					continue
				}
				if _, written := publicSeal.OutsideWritten[nameText]; written {
					continue
				}
			}
			// after an escape only the `#`-named fields survive: the language
			// keeps outside code from writing them at all, so this walk still
			// reads every write. A `private`-modifier field is writable
			// through the escaped reference at runtime.
			if escapes && !ast.IsPrivateIdentifier(name) {
				continue
			}
		default:
			continue
		}
		if _, isPoisoned := poisoned[nameText]; isPoisoned {
			continue
		}
		env := NewEnv()
		var value abstractdomain.AbstractValue
		seedless := false
		if _, stepped := numericallyStepped[nameText]; stepped {
			// a numeric step runs an unknown number of times over the
			// object's life, so the field's own initializer no longer pins
			// the value — what survives every run is the number ground
			value = abstractdomain.PossiblyNaN(abstractdomain.KnownSet(
				refinementsets.RefinedSet{}, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
		} else if pd.Initializer == nil {
			// a field with no initializer holds `undefined` until a write
			// lands — UNLESS the constructor's own straight-line body
			// (constructorUnconditionallyWritesField below) writes it on
			// every construction path: then the field is never read before
			// that write in a way this walk can observe, so the seed takes
			// no Undef arm and the invariant rests on the constructor's own
			// write alone (unconditionalConstructorWrite marks it below,
			// read by the final join loop). A field a constructor only
			// writes on SOME path (inside an if/try/loop) keeps the Undef
			// seed — that arm is real.
			if constructorUnconditionallyWritesField(declaration, nameText) {
				unconditionalConstructorWrite[nameText] = struct{}{}
				seedless = true
			} else {
				value = abstractdomain.Undef
			}
		} else {
			value = evaluateExpression(&silent, env, pd.Initializer)
		}
		if _, seen := candidates[nameText]; !seen {
			candidateOrder = append(candidateOrder, nameText)
		}
		if !seedless {
			candidates[nameText] = value
		}
	}
	if len(candidates) == 0 {
		return getterInvariants(ctx, declaration, members, map[string]abstractdomain.AbstractValue{})
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
					// the class-wide invariant covers every call site, so a
					// parameter seeds from its DECLARED TYPE (entry_env.go's
					// InitialStateOfPlainParameter — the same reading a
					// function body's own entry uses), never blanket Unknown.
					// `age: number` joins the field's candidate as the number
					// ground, so `this.#age = age` folds a real set into the
					// invariant instead of poisoning the whole join to
					// Unknown (JoinKnown(anything, Unknown) = Unknown).
					env.Set(pname.Text(), InitialStateOfPlainParameter(ctx.P, parameter))
				}
			}
		}
		AnalyzeStatements(&collecting, env, body.AsBlock().Statements.Nodes, nil)
	}

	invariants := map[string]abstractdomain.AbstractValue{}
	for _, name := range candidateOrder {
		writes := sink[name]
		if _, seedless := unconditionalConstructorWrite[name]; seedless {
			// no Undef seed: the constructor's own unconditional write is
			// every value this field can ever hold, so the join starts
			// from the FIRST write rather than from an absent-arm seed
			// the class's own text never allows. No writes at all here
			// would mean constructorUnconditionallyWritesField found a
			// write the collection walk's OWN sink then failed to record —
			// a bug in that pairing, not a real empty invariant — so this
			// name is skipped rather than silently answering an empty
			// join.
			if len(writes) == 0 {
				continue
			}
			joined := writes[0]
			for _, written := range writes[1:] {
				joined = abstractdomain.JoinKnown(joined, written)
			}
			joined = widenInvariantCollections(joined)
			if joined.Kind != abstractdomain.KindUnknown {
				invariants[name] = joined
			}
			continue
		}
		joined := candidates[name]
		for _, written := range writes {
			joined = abstractdomain.JoinKnown(joined, written)
		}
		joined = widenInvariantCollections(joined)
		if joined.Kind != abstractdomain.KindUnknown {
			invariants[name] = joined
		}
	}
	return getterInvariants(ctx, declaration, members, invariants)
}

// getterInvariants adds one entry per non-static GET ACCESSOR to an
// already-collected field-invariant map: a getter has no write to
// sink and no initializer, so it takes no invariant from the field
// loop above at all, and `this.<getter>` reads fell to the opaque
// floor before this function existed (this file's own header case,
// `MeterWithGetter`). A getter's body is a pure computed read, so its
// derivation is the SAME one `constructed_instance.go`'s getter pass
// already runs at construction time: walk the body once, `this` bound
// to the fields already collected, and collect the return through
// ReturnSink. Skips a name the field loop already claimed (fields and
// accessors never share a name in valid TypeScript), a computed or
// unnamed accessor, and one with no body (a declaration file, or
// `abstract get x(): number;`).
func getterInvariants(
	ctx *FlowContext,
	declaration *ast.Node,
	members []*ast.Node,
	invariants map[string]abstractdomain.AbstractValue,
) map[string]abstractdomain.AbstractValue {
	var thisObject *abstractdomain.AbstractValue
	for _, member := range members {
		if !ast.IsGetAccessorDeclaration(member) {
			continue
		}
		ga := member.AsGetAccessorDeclaration()
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		name := ga.Name()
		if name == nil || (!ast.IsIdentifier(name) && !ast.IsPrivateIdentifier(name)) {
			continue
		}
		nameText := name.Text()
		if _, already := invariants[nameText]; already {
			continue
		}
		if ga.Body == nil {
			continue
		}
		if thisObject == nil {
			keys := make([]abstractdomain.ObjectKey, 0, len(invariants))
			for key, value := range invariants {
				keys = append(keys, abstractdomain.ObjectKey{Name: key, Value: value})
			}
			built := abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false)
			thisObject = &built
		}
		getterEnv := NewEnv()
		getterEnv.Set("this", *thisObject)
		var sink []abstractdomain.AbstractValue
		silent := *ctx
		silent.Report = func(assignability.RefinementDiagnostic) {}
		silent.ReturnSink = &sink
		silent.ThisWriteSink = nil
		AnalyzeStatements(&silent, getterEnv, ga.Body.AsBlock().Statements.Nodes, nil)
		if len(sink) == 0 {
			continue
		}
		joined := sink[0]
		for _, v := range sink[1:] {
			joined = abstractdomain.JoinKnown(joined, v)
		}
		joined = widenInvariantCollections(joined)
		if joined.Kind != abstractdomain.KindUnknown {
			invariants[nameText] = joined
		}
	}
	return invariants
}

// widenInvariantCollections sheds a collection invariant's pinned
// CONTENTS while keeping its identity. The collection above reads field
// REASSIGNMENTS (`this.cache = …`) — a content mutation
// (`this.cache.set(k, v)`, `.delete`, `.clear`) is a method call on the
// field's value, which the write sink never records. So "the field
// always holds its initializer" is true of the REFERENCE and false of
// the CONTENTS: a `private cache = new Map()` whose methods fill it
// would otherwise enter every method as a provably-EMPTY map, and
// `this.cache.get(k)` would answer exactly-undefined — the wrong answer
// the LRUCache dead-branch fires showed. Identity survives (it IS a
// Map, and stays one); Entries and Complete do not. A wrapper's inner
// value widens the same way, so `Map | undefined` fields cannot smuggle
// the pinned contents through.
func widenInvariantCollections(value abstractdomain.AbstractValue) abstractdomain.AbstractValue {
	if value.Kind == abstractdomain.KindCollection && (len(value.Entries) > 0 || value.Complete) {
		widened := value
		widened.Entries = nil
		widened.Complete = false
		return widened
	}
	if value.Inner != nil {
		inner := widenInvariantCollections(*value.Inner)
		if !abstractdomain.SameKnown(inner, *value.Inner) {
			widened := value
			widened.Inner = &inner
			return widened
		}
	}
	return value
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
		name := member.AsPropertyDeclaration().Name()
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		var nameText string
		switch {
		case name != nil && (ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name)):
			nameText = name.Text()
		default:
			// a symbol-keyed member gets a key under its derived `#sym:`
			// name, so a `this[S]` read has a place to narrow the same way a
			// dotted field does
			symbolKey, isSymbolKey := symbolMemberFieldName(checkerOf(ctx), name)
			if !isSymbolKey {
				continue
			}
			nameText = symbolKey
		}
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

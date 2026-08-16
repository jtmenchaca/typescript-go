// Object locals flattened into scalar slots for the flow IR.
//
// A body that keeps a fixed-shape record in a local — `const p = { lo:
// 0, hi: n }` — writes and reads it as `p.lo` / `p.hi`. The kernel's
// walk is over a vector of scalar slots, so such a local can be
// carried as ONE SLOT PER LEAF PATH, spelled "p.lo", "p.hi", and for a
// nested literal "p.a.b": the leaf paths a fixed-shape literal names,
// each holding one scalar. The kernel is untouched — it never learns
// several slots came from one object.
//
// The flattening is only sound while the object has no identity the
// body can observe. This file's recognizer answers the flat leaf set
// for a declaration, or declines: every use of the name in the body
// must be a `p.a.b` path on a leaf the literal declared (or an
// INTERIOR path in one of the whole-record forms below), and the
// literal itself must be plain `key: expression` rows whose values are
// scalars or further fixed-shape literals. Anything else — an alias, a
// call argument, a return of the whole record, a delete, a key added
// later, a computed or spread key — declines, because after flattening
// those uses would read or write an object that no longer exists as
// one value.
//
// The whole-record forms that DO lower, recognized here and lowered in
// lowering_to_kernel_ir.go:
//
//   - `p = q` between two flattened records of the same leaf shape:
//     per-leaf slot assigns.
//   - `p = { … }` where the literal has exactly p's leaf shape: the
//     same, each leaf taking its own row.
//   - `const { x, y } = p`: per-leaf slot reads.

package walk

import (
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

// ObjectLocal is one flattened record local: the declaration it came
// from, the name it was spelled under, and its LEAF paths in literal
// order paired with the initializer each leaf was given.
//
// Methods holds the literal's shorthand METHOD rows by dotted path
// ("bump", "a.m"), each mapping to its declaration node. A method spells
// no leaf; the use scan admits its path in CALLEE position only, and the
// call site lands its `this` writes on the record's own leaf slots
// (method_this_writes.go). Empty for the families whose leaves come from
// a declaration rather than a literal.
type ObjectLocal struct {
	Declaration *ast.Node // VariableDeclaration
	Name        string
	Keys        []ObjectLocalKey
	Methods     map[string]*ast.Node
}

// ObjectLocalKey is one leaf of a flattened record: the leaf's path
// below the holder (["a", "b"] for `p.a.b`), the key's own name (the
// LAST path step, which is what a destructuring row and a one-step read
// name), the slot name it is tracked under ("p.a.b"), and the
// expression the literal assigned it.
//
// Initializer is nil for the families whose leaves come from a
// DECLARATION rather than from a literal row — a constructor's exit
// rows, a declared record type. Those leaves carry their evidence in
// Sort/TypeofTag instead, which ObjectLocalKeySort and
// ObjectLocalKeyTypeof read in front of the initializer's syntax. A
// literal's leaf leaves both zero and keeps the syntax reading it has
// always had.
type ObjectLocalKey struct {
	Path        []string
	Key         string
	SlotName    string
	Initializer *ast.Node
	// Sort and TypeofTag are the DECLARED evidence for a leaf with no
	// initializer to read. Declared is what tells the two apart: a
	// literal leaf and a declared leaf whose sort happens to be the zero
	// BindingKind must not be confused.
	Declared  bool
	Sort      BindingKind
	TypeofTag TypeofTag
}

// objectLiteralOfDeclaration is the object literal a declaration's
// initializer is, through parens and casts — or nil.
func objectLiteralOfDeclaration(declaration *ast.Node) *ast.Node {
	if !ast.IsVariableDeclaration(declaration) {
		return nil
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil
	}
	literal := Unwrapped(initializer)
	if !ast.IsObjectLiteralExpression(literal) {
		return nil
	}
	return literal
}

// flatKeysOfLiteral reads an object literal's rows as a flat LEAF list,
// recursing into nested fixed-shape literals: a row whose value is
// another object literal contributes that literal's own leaves under
// the row's key, so `{ a: { b: 1 } }` names the single leaf "p.a.b".
// Every row must be a plain `key: expression` assignment with an
// identifier key OR a STABLE SYMBOL const key; spreads, other computed
// keys, shorthand rows, methods, accessors and array-literal values all
// decline — none of them names one leaf holding one scalar. `prefix` is
// the path already walked below the holder, `holder` the spelled root
// ("p").
//
// A SYMBOL-KEYED ROW (`{ [K_MODULE_ID]: id }`) contributes its leaf
// under the derived `#sym:` name, which is the same key identity the
// class census spells its symbol fields with (StableSymbolKeyName,
// ir_field_bundles.go). What makes it one leaf is what makes it one
// field there: the const cannot be rebound, its declaration runs once
// per module, and the symbol IS the property key at runtime, so two rows
// spelled with the same const are one key and rows spelled with
// different consts are different keys.
//
// The SPELLING composes with the leaf vocabulary unchanged. A leaf named
// `#sym:S` under holder `p` spells the slot "p.#sym:S", and every reader
// of that spelling reads it as one step below p: leafSlotsUnder trims
// the `p.` prefix and keeps the rest whole, and splitOneStep cuts at the
// FIRST dot, so a `#sym:` name — which holds no dot — stays one member.
// The `#` and `:` are what keep it apart from a plain property name and
// from a `#`-named private one, exactly as the field census's own note
// says.
func flatKeysOfLiteral(literal *ast.Node, holder string, prefix []string) ([]ObjectLocalKey, bool) {
	return flatKeysOfLiteralWith(nil, literal, holder, prefix)
}

// flatKeysOfLiteralWith is flatKeysOfLiteral carrying the checker the
// STABLE SYMBOL KEY needs. With a nil checker every computed key
// declines, which is the reading every caller had before the symbol key
// reached this vocabulary.
func flatKeysOfLiteralWith(
	c *checker.Checker,
	literal *ast.Node,
	holder string,
	prefix []string,
) ([]ObjectLocalKey, bool) {
	var keys []ObjectLocalKey
	seen := map[string]struct{}{}
	for _, property := range literal.AsObjectLiteralExpression().Properties.Nodes {
		// a SHORTHAND row (`{ a }`) names its key AND its value with the
		// same identifier — `a` is short for `a: a`. It has no
		// PropertyAssignment.Initializer to read (a different node kind
		// entirely), so its key and its value expression are both read
		// off ShorthandPropertyAssignment.Name() here, and the row then
		// joins the ordinary `key: expression` handling below through the
		// same `key`/`initializer` pair a plain row supplies.
		var keyName *ast.Node
		var initializer *ast.Node
		switch {
		case ast.IsPropertyAssignment(property):
			assignment := property.AsPropertyAssignment()
			if assignment.Initializer == nil {
				return nil, false
			}
			keyName = assignment.Name()
			initializer = assignment.Initializer
		case ast.IsShorthandPropertyAssignment(property):
			shorthand := property.AsShorthandPropertyAssignment()
			name := shorthand.Name()
			if name == nil || !ast.IsIdentifier(name) {
				return nil, false
			}
			keyName = name
			initializer = name
		case ast.IsMethodDeclaration(property):
			// a shorthand METHOD row (`bump() { … }`) spells no leaf —
			// calling it through the record is the one admitted use
			// (usesAreAllDeclaredKeySteps' callee arm), and its `this`
			// writes land at each call site through the method's own
			// summary (literalThisBundleOf). A row that treatment cannot
			// serve — a generator, an async body, a computed name — and a
			// body mentioning the record's own name (a write the census
			// would spell under the OUTER name, which no summary entry
			// carries) both decline the literal whole, exactly as every
			// method row did before.
			if !isServableLiteralMethodRow(property) {
				return nil, false
			}
			if holder != "" && mentionsName(property.Body(), holder) {
				return nil, false
			}
			continue
		default:
			return nil, false
		}
		var key string
		switch {
		case ast.IsIdentifier(keyName):
			key = keyName.Text()
		default:
			// a computed key names one leaf only through a stable symbol
			// const; every other computed key names nothing the vector holds
			symbolKey, isSymbolKey := symbolMemberFieldName(c, keyName)
			if !isSymbolKey {
				return nil, false
			}
			key = symbolKey
		}
		if _, already := seen[key]; already {
			return nil, false
		}
		seen[key] = struct{}{}
		path := append(append([]string{}, prefix...), key)
		value := Unwrapped(initializer)
		// a nested fixed-shape literal contributes its OWN leaves under
		// this key — one more level of the same rule
		if ast.IsObjectLiteralExpression(value) {
			nested, ok := flatKeysOfLiteralWith(c, value, holder, path)
			if !ok {
				return nil, false
			}
			keys = append(keys, nested...)
			continue
		}
		// an array value is the array flattening's business (a.len / a.elem),
		// not a scalar leaf — a record row holding one is not spelled here
		if ast.IsArrayLiteralExpression(value) {
			return nil, false
		}
		keys = append(keys, ObjectLocalKey{
			Path:        path,
			Key:         key,
			SlotName:    holder + "." + strings.Join(path, "."),
			Initializer: initializer,
		})
	}
	if len(keys) == 0 {
		return nil, false
	}
	return keys, true
}

// propertyPathOf reads a chained property access down to a root
// identifier or `this`: `p.a.b` answers ("p", ["a","b"]) and
// `this.count` answers ("this", ["count"]) — the spelling the bundle
// layout gives a method's field slots. Optional steps (`p?.a`) and
// computed steps decline — neither is a plain path read.
func propertyPathOf(node *ast.Node) (root string, path []string, ok bool) {
	return propertyPathReading(node, false)
}

// propertyPathAdmittingRootOptionalStep is propertyPathOf, but tolerates
// ONE optional step — provided its RECEIVER is the root identifier or
// `this` itself (`p?.a`, `this?.count`). The soundness fact: a FLATTENED
// RECORD LOCAL is always defined (`const p = { … }` binds an object,
// never null/undefined), so `p?.a` on such a root reads the identical
// leaf `p.a` would. A DEEPER optional step (`p.a?.b`, where the `?.`
// sits on a step that is not adjacent to the root) still declines here —
// the intermediate leaf `p.a` is not provably non-absent, so nothing
// admits reading past it.
//
// Every caller of this variant already knows the root it is asking
// about IS a flattened local's own name — the flattening's use scan and
// the lowering-side slot lookup both check `root == name` (or resolve
// the slot by spelling) after calling this, exactly as they did with
// propertyPathOf. This function only widens which SYNTAX yields a path;
// it does not itself decide which roots are flattened.
func propertyPathAdmittingRootOptionalStep(node *ast.Node) (root string, path []string, ok bool) {
	return propertyPathReading(node, true)
}

// propertyPathReading is the shared walk propertyPathOf and
// propertyPathAdmittingRootOptionalStep both run: `admitRootOptional`
// says whether the ONE step adjacent to the root — the last one the loop
// below visits, since the walk descends outermost-in — may carry a
// `?.` and still count as a plain path step. Every other optional step,
// at any other depth, always declines.
func propertyPathReading(node *ast.Node, admitRootOptional bool) (root string, path []string, ok bool) {
	var steps []string
	current := node
	for ast.IsPropertyAccessExpression(current) {
		access := current.AsPropertyAccessExpression()
		receiver := access.Expression
		receiverIsRoot := ast.IsIdentifier(receiver) || receiver.Kind == ast.KindThisKeyword
		if access.QuestionDotToken != nil {
			if !admitRootOptional || !receiverIsRoot {
				return "", nil, false
			}
		}
		// a step's own name is a plain identifier OR a private identifier
		// (`this.#age`) — the same two spellings SpelledNameOf admits, so a
		// deep or root-optional path through a `#`-named field resolves to
		// the same slot a one-step read of it would
		if !ast.IsIdentifier(access.Name()) && !ast.IsPrivateIdentifier(access.Name()) {
			return "", nil, false
		}
		steps = append(steps, access.Name().Text())
		current = access.Expression
	}
	if len(steps) == 0 {
		return "", nil, false
	}
	if !ast.IsIdentifier(current) && current.Kind != ast.KindThisKeyword {
		return "", nil, false
	}
	// steps were collected outermost-first; the path reads root-first
	for left, right := 0, len(steps)-1; left < right; left, right = left+1, right-1 {
		steps[left], steps[right] = steps[right], steps[left]
	}
	if current.Kind == ast.KindThisKeyword {
		return "this", steps, true
	}
	return current.Text(), steps, true
}

// declaredLeafPaths is the leaf-path set a flattened record declares,
// as a lookup on the joined spelling.
func declaredLeafPaths(keys []ObjectLocalKey) map[string]struct{} {
	out := map[string]struct{}{}
	for _, key := range keys {
		out[strings.Join(key.Path, ".")] = struct{}{}
	}
	return out
}

// recordShapeOf is a flattened record's leaf shape as a comparable
// spelling — the joined leaf paths in literal order. Two records assign
// leaf-for-leaf only where these agree.
func recordShapeOf(keys []ObjectLocalKey) string {
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = strings.Join(key.Path, ".")
	}
	return strings.Join(parts, "|")
}

// mentionsName is whether a subtree contains the identifier at all, in
// any position.
func mentionsName(node *ast.Node, name string) bool {
	found := false
	var visit func(child *ast.Node) bool
	visit = func(child *ast.Node) bool {
		if found {
			return true
		}
		if ast.IsIdentifier(child) && child.Text() == name {
			found = true
			return true
		}
		child.ForEachChild(visit)
		return false
	}
	visit(node)
	return found
}

// ObjectLocalKeySort is a flattened leaf's sort. A leaf that came from a
// DECLARATION — a constructor's exit row, a declared record member —
// carries its own sort and answers it; a leaf that came from a literal
// row reads the way LocalSort reads a scalar local's: a string literal
// is a string, anything else the lowering reads numerically.
func ObjectLocalKeySort(key ObjectLocalKey) BindingKind {
	if key.Declared {
		return key.Sort
	}
	if ast.IsStringLiteral(Unwrapped(key.Initializer)) {
		return BindingKindString
	}
	return BindingKindNumber
}

// ObjectLocalKeyTypeof is a flattened leaf's typeof evidence: the
// declared tag where the leaf came from a declaration, and the
// initializer's syntax alone otherwise — the twin of LocalTypeof.
func ObjectLocalKeyTypeof(key ObjectLocalKey) TypeofTag {
	if key.Declared {
		return key.TypeofTag
	}
	e := Unwrapped(key.Initializer)
	if ast.IsStringLiteral(e) {
		return TypeofTagString
	}
	if _, isNumber := NumberOf(e); ast.IsNumericLiteral(e) || isNumber {
		return TypeofTagNumber
	}
	if e.Kind == ast.KindTrueKeyword || e.Kind == ast.KindFalseKeyword {
		return TypeofTagBoolean
	}
	return TypeofTagNone
}

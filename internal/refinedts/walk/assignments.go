// from bindings/assignments.ts
//
// What a write does to the knowledge around it. A declared binding
// judges every write against its stated set — the declaration is an
// invariant, which is exactly what lets a loop skip widening it. A
// write through a property path replaces that key in place where the
// checker can place it, and havocs the whole object where it cannot:
// no stale key survives a write nobody can locate.
//
// Writes also decide what SHARES an object. A literal that embeds a
// tracked reference links the two names, so a write through either
// path is a write to the one object (VOCABULARY.md, alias class).

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// WriteBinding binds a written value: a declared binding judges every
// write against its stated set — the declared type is the invariant.
func WriteBinding(ctx *FlowContext, env Env, name string, value abstractdomain.AbstractValue, at *ast.Node, what string) {
	stated, hasStated := ctx.Declared[name]
	if hasStated {
		CheckAssignability(ctx, value, *stated, at, what, nil)
		// THE REFUSED-WRITE LAW (one fire per defect): the binding keeps
		// the MEET of the written claim and its declared set, so a later
		// read judges silently against the declaration rather than
		// restating the write's own fire one line later. An admitted
		// write is unchanged by the meet (it was already inside), so no
		// exactness is lost on the silent path.
		if stated.Kind == annotations.DeclaredSet && stated.Set != nil && stated.Temporal != nil &&
			value.Kind == abstractdomain.KindSet && value.SetKindTag == abstractdomain.SetKindTagNone {
			// the same law for a TEMPORAL declaration: MeetKnown's set-meet
			// arm cannot combine bounded temporal riders, so the binding
			// wears the stated claim itself — set and rider together — and
			// a later read proves A ⊆ A reflexively instead of re-asking
			// the kernel the write's own question. An exact literal write
			// (KindValues) never reaches here and keeps its exactness.
			value = abstractdomain.KnownSet(*stated.Set, stated.Temporal, abstractdomain.TrustLevelOf(value), abstractdomain.SetKindTagNone)
		} else if stated.Kind == annotations.DeclaredSet && stated.Set != nil &&
			value.Kind == abstractdomain.KindSet && value.SetKindTag == abstractdomain.SetKindTagNone {
			declared := abstractdomain.KnownSet(*stated.Set, nil, abstractdomain.TrustLevelOf(value), abstractdomain.SetKindTagNone)
			value = abstractdomain.MeetKnown(value, declared)
		} else if stated.Kind == annotations.DeclaredSet && stated.Set != nil &&
			value.Kind == abstractdomain.KindValues &&
			(value.KindTag == abstractdomain.PrimitiveNumber || value.KindTag == abstractdomain.PrimitiveBoolean) {
			// THE SAME LAW FOR AN ENUMERATED SCALAR WRITE. The arms above
			// read only a KindSet, so a finite word list written to a
			// declared position kept its own words whole: `const t:
			// TrueLiteral = x > 3` left t holding {false, true}, and the
			// refusal then landed a second time at the following `return t`
			// — one line below the defect, and reported as the READ rather
			// than the write. The declared position's own words are the
			// invariant, so the binding keeps the write's words that the
			// declaration admits.
			//
			// Read by MEMBERSHIP against the stated forms, which is what a
			// finite word list allows without a kernel round trip: a word
			// the declaration's own oneOf does not list is not admitted.
			// Where the declaration states anything other than a plain
			// oneOf (a range, an integer grid), no word is dropped here —
			// the write was already judged above, and dropping on an
			// unread shape would claim more than this reading proves.
			if declaredWords, ok := plainOneOfWords(*stated.Set); ok {
				var kept []float64
				for _, v := range value.Values {
					for _, w := range declaredWords {
						if v == w {
							kept = append(kept, v)
							break
						}
					}
				}
				if len(kept) > 0 && len(kept) < len(value.Values) {
					value = abstractdomain.KnownValues(kept, value.KindTag, abstractdomain.TrustLevelOf(value))
				}
			}
		}
		// The same law for a possibly-NaN write (CheckPossiblyNaN's own
		// path, nan_wrapper.go): a genuinely refined target (not
		// AddsNothingSet — a bare `number` position admits NaN and stays
		// silent there, never reaching 7001) excludes NaN outright, so a
		// refused write here has NaN proven not a member of the target.
		// Keeping the wrapper would let the following read (`return u;`)
		// re-carry the same possibly-NaN claim against the same target
		// and re-fire — the second error TESTING-TENETS rules against.
		// The binding keeps the declared set's real half met with the
		// write's own real half, unwrapped, exactly the KindSet arm
		// above's meet. Gated on the SAME condition CheckPossiblyNaN
		// itself uses to decide NaN is a live obstacle at all (target's
		// KindTag empty, not AddsNothingSet) — a bare `number` target
		// still admits NaN, so THAT write is genuinely admitted and must
		// keep its own PossiblyNaN wrapper unstripped.
		if stated.Kind == annotations.DeclaredSet && stated.Set != nil && stated.KindTag == "" &&
			!AddsNothingSet(*stated.Set) &&
			value.Kind == abstractdomain.KindPossiblyNaN && value.Inner != nil &&
			value.Inner.Kind == abstractdomain.KindSet && value.Inner.SetKindTag == abstractdomain.SetKindTagNone {
			declared := abstractdomain.KnownSet(*stated.Set, nil, abstractdomain.TrustLevelOf(value), abstractdomain.SetKindTagNone)
			value = abstractdomain.MeetKnown(*value.Inner, declared)
		}
	}
	// the write invalidates every difference row rooted at this name —
	// the row spoke about the value that just moved
	ctx.Aliases.Invalidate(name)
	// ...and every PLACE-VALUE entry rooted here (the dotted keys a
	// guard recorded): the path spoke about the value that just moved
	ForgetPlaceEntriesEnv(env, name)
	env.Set(name, value)
}

// plainOneOfWords is the words a set states when it states exactly a
// finite list and nothing else — the shape a literal type and a literal
// union compile to. Any other shape (a range, an integer grid, a
// sequence form, several forms at once) answers ok=false: this reading
// pins no words there, and the caller drops none.
func plainOneOfWords(set refinementsets.RefinedSet) ([]float64, bool) {
	if len(set.Forms) != 1 || set.Forms[0].Form != refinementsets.FormOneOf {
		return nil, false
	}
	return set.Forms[0].W, true
}

// WriteElement is a write through an index. `name[i] = v` must land
// inside the element set a declared SEQUENCE binding states — the
// element set is an invariant exactly like the whole set
// (WriteBinding), and the index does not matter: every slot wears it.
// A declared set that is a UNION of repetitions states one element set
// per arm, and the written value has to sit inside the one belonging
// to whichever arm the sequence is on — so every arm judges the write,
// and any arm the value falls outside of is the alert. Judging only;
// what the write does to the tracked value stays with the caller.
func WriteElement(ctx *FlowContext, target *ast.Node, value abstractdomain.AbstractValue, at *ast.Node) {
	eae := target.AsElementAccessExpression()
	if !ast.IsIdentifier(eae.Expression) {
		return
	}
	declared, hasDeclared := ctx.Declared[eae.Expression.Text()]
	if !hasDeclared || declared.Kind != annotations.DeclaredSet {
		return
	}
	windows, ok := RepetitionArmsOf(*declared.Set)
	if !ok {
		return
	}
	for _, window := range windows {
		element := window.Element
		CheckAssignability(
			ctx,
			value,
			annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &element},
			at,
			"the written element",
			nil,
		)
	}
}

// rootOfReceiver is the tracked name a write's receiver roots at: an
// identifier by its text, `this` by the binding the method walk
// initialStates — so `this.x = v` replaces the key exactly like
// `o.x = v`.
func rootOfReceiver(receiver *ast.Node) (string, bool) {
	if ast.IsIdentifier(receiver) {
		return receiver.Text(), true
	}
	if receiver.Kind == ast.KindThisKeyword {
		return "this", true
	}
	return "", false
}

// WriteProperty is a write through a property path. `name.key = v`
// replaces that key's facts — checked first where the key's set is
// declared, since a declared key is an invariant exactly like a
// declared binding. Any other path (a computed index, a deeper
// chain, a receiver the checker does not track) forgets the whole
// object, and with it every name sharing the reference — no stale
// key survives a write the checker cannot place.
func WriteProperty(ctx *FlowContext, env Env, target *ast.Node, value abstractdomain.AbstractValue, at *ast.Node) {
	// `C.prop = v` through a static SET accessor trivially fronting a
	// backing field: the value lands under the "<C>.<#backing>" place
	// key the getter read answers (static_accessor_backing.go)
	if WriteStaticAccessorBacking(ctx, env, target, value) {
		return
	}
	pae := target.AsPropertyAccessExpression()
	// a DEEPER chain rooted at a tracked object rebuilds the nested
	// key in place — `root.a.b = v` replaces b inside a inside root
	if ast.IsPropertyAccessExpression(pae.Expression) {
		path := []string{pae.Name().Text()}
		var receiver *ast.Node = pae.Expression
		for ast.IsPropertyAccessExpression(receiver) {
			inner := receiver.AsPropertyAccessExpression()
			path = append([]string{inner.Name().Text()}, path...)
			receiver = inner.Expression
		}
		rootName, rooted := rootOfReceiver(receiver)
		if !rooted {
			ForgetThrough(ctx, env, pae.Expression)
			return
		}
		if _, has := env.Get(rootName); !rooted || !has {
			ForgetThrough(ctx, env, pae.Expression)
			return
		}
		var rebuild func(holder abstractdomain.AbstractValue, keys []string) (abstractdomain.AbstractValue, bool)
		rebuild = func(holder abstractdomain.AbstractValue, keys []string) (abstractdomain.AbstractValue, bool) {
			if holder.Kind != abstractdomain.KindObject {
				return abstractdomain.AbstractValue{}, false
			}
			head, rest := keys[0], keys[1:]
			if len(rest) == 0 {
				next := make([]abstractdomain.ObjectKey, 0, len(holder.Keys)+1)
				found := false
				for _, k := range holder.Keys {
					if k.Name == head {
						next = append(next, abstractdomain.ObjectKey{Name: head, Value: value})
						found = true
					} else {
						next = append(next, k)
					}
				}
				if !found {
					next = append(next, abstractdomain.ObjectKey{Name: head, Value: value})
				}
				return abstractdomain.KnownObject(next, nil, false, abstractdomain.TrustProved, false), true
			}
			var child abstractdomain.AbstractValue
			hasChild := false
			for _, k := range holder.Keys {
				if k.Name == head {
					child, hasChild = k.Value, true
					break
				}
			}
			if !hasChild {
				return abstractdomain.AbstractValue{}, false
			}
			inner, ok := rebuild(child, rest)
			if !ok {
				return abstractdomain.AbstractValue{}, false
			}
			next := make([]abstractdomain.ObjectKey, 0, len(holder.Keys)+1)
			found := false
			for _, k := range holder.Keys {
				if k.Name == head {
					next = append(next, abstractdomain.ObjectKey{Name: head, Value: inner})
					found = true
				} else {
					next = append(next, k)
				}
			}
			if !found {
				next = append(next, abstractdomain.ObjectKey{Name: head, Value: inner})
			}
			return abstractdomain.KnownObject(next, nil, false, abstractdomain.TrustProved, false), true
		}
		rootHeld, hasRoot := env.Get(rootName)
		if !hasRoot {
			rootHeld = silence.Residue()
		}
		next, ok := rebuild(rootHeld, path)
		if !ok {
			HavocEnv(ctx.Aliases, env, rootName)
			return
		}
		UpdateTrackedEnv(ctx.Aliases, env, rootName, next)
		return
	}
	name, rooted := rootOfReceiver(pae.Expression)
	if !rooted {
		ForgetThrough(ctx, env, pae.Expression)
		return
	}
	held, hasHeld := env.Get(name)
	// a receiver with NO object knowledge — untracked, or tracked as
	// unknown (a cast from `unknown`/`any` carries nothing). The WRITE
	// is a fact regardless of where the object came from: a LOCAL
	// const binding whose shape tsc knows starts being tracked here,
	// its written key exact and every other key open. A value that
	// escaped this scope (a parameter, a captured name) is left alone:
	// the checker cannot see who else writes it.
	if !hasHeld || held.Kind == abstractdomain.KindUnknown {
		symbol := ctx.P.Checker.GetSymbolAtLocation(pae.Expression)
		var declaration *ast.Node
		if symbol != nil {
			declaration = symbol.ValueDeclaration
		}
		local := declaration != nil &&
			ast.IsVariableDeclaration(declaration) &&
			ast.IsIdentifier(declaration.AsVariableDeclaration().Name()) &&
			declaration.Parent != nil &&
			ast.IsVariableDeclarationList(declaration.Parent) &&
			(declaration.Parent.Flags&ast.NodeFlagsConst) != 0 &&
			ast.GetSourceFileOfNode(declaration) == ctx.P.Entry
		if !local {
			if hasHeld {
				HavocEnv(ctx.Aliases, env, name)
			}
			return
		}
		shape := typereading.TypeAtLocation(ctx.P.Checker, pae.Expression)
		keys := []abstractdomain.ObjectKey{}
		for _, member := range ctx.P.Checker.GetPropertiesOfType(shape) {
			keys = append(keys, abstractdomain.ObjectKey{Name: member.Name, Value: silence.Residue()})
		}
		found := false
		for _, k := range keys {
			if k.Name == pae.Name().Text() {
				found = true
				break
			}
		}
		if !found {
			return // not its key
		}
		for i, k := range keys {
			if k.Name == pae.Name().Text() {
				keys[i].Value = value
			}
		}
		env.Set(name, abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false))
		return
	}
	if held.Kind != abstractdomain.KindObject {
		HavocEnv(ctx.Aliases, env, name)
		return
	}
	key := pae.Name().Text()
	// a declared object's key set is an invariant: checkAssignability the write —
	// the key's DECLARED type (read off the property access, which the
	// binding's annotation types) carries the admitted sorts
	declared, hasDeclared := ctx.Declared[name]
	if hasDeclared && declared.Kind == annotations.DeclaredObject {
		var stated *annotations.ObjectKeySpec
		for i := range declared.Object.Keys {
			if declared.Object.Keys[i].Name == key {
				stated = &declared.Object.Keys[i]
				break
			}
		}
		if stated != nil {
			keyTarget := DeclaredOfKey(*stated)
			if keyTarget != nil {
				// the key's DECLARED type comes from its property symbol —
				// the assignment expression's own type is contaminated by
				// the right side (an `any` write reads back as any)
				propertySymbol := ctx.P.Checker.GetSymbolAtLocation(target)
				var positionType *checker.Type
				if propertySymbol != nil {
					positionType = ctx.P.Checker.GetTypeOfSymbolAtLocation(propertySymbol, target)
				}
				CheckAssignability(
					ctx,
					value,
					*keyTarget,
					at,
					"the key '"+key+"'",
					positionType,
				)
			}
		}
	} else {
		// no binding-level statement: the PROPERTY's own declaration may
		// state one — a class field typed by an annotation alias
		// (`hours: Hours = 0`) is an invariant on every write to it, the
		// same rule an annotated binding carries
		propertySymbol := ctx.P.Checker.GetSymbolAtLocation(target)
		var declaration *ast.Node
		if propertySymbol != nil {
			declaration = propertySymbol.ValueDeclaration
		}
		var typeNode *ast.Node
		if declaration != nil && (ast.IsPropertyDeclaration(declaration) || ast.IsPropertySignatureDeclaration(declaration)) {
			typeNode = declaration.Type()
		}
		if typeNode != nil {
			result := annotations.AnnotationOfType(ctx.P, typeNode, ctx.Registry, ctx.Objects)
			if result.Stated != nil {
				var positionType *checker.Type
				if propertySymbol != nil {
					positionType = ctx.P.Checker.GetTypeOfSymbolAtLocation(propertySymbol, target)
				}
				CheckAssignability(
					ctx,
					value,
					*result.Stated,
					at,
					"the field '"+key+"'",
					positionType,
				)
			}
		}
	}
	next := make([]abstractdomain.ObjectKey, 0, len(held.Keys)+1)
	found := false
	for _, k := range held.Keys {
		if k.Name == key {
			next = append(next, abstractdomain.ObjectKey{Name: key, Value: value})
			found = true
		} else {
			next = append(next, k)
		}
	}
	if !found {
		next = append(next, abstractdomain.ObjectKey{Name: key, Value: value})
	}
	nextObject := abstractdomain.KnownObject(next, nil, false, abstractdomain.TrustProved, false)
	// every name sharing this reference sees the write — the class-
	// aware update: same-shaped aliases follow, embedders and
	// candidate sharers join, the rest forget
	UpdateTrackedEnv(ctx.Aliases, env, name, nextObject)
}

// WriteAssignmentPattern is an assignment-PATTERN write — `({ a } = x)`,
// `[a] = xs` — walking the same object/array-literal-as-target shape
// ForgetThrough and dataflowfacts.TargetNames already recognize, but
// judging each leaf identifier against its OWN declared type through
// WriteBinding instead of havocking it. `SlotOf`/`SlotOfIndex`
// (destructuring.go) read what the source holds at each named or
// positional slot — the same readers a declaration pattern's bind
// uses — so an assignment pattern and a declaration pattern judge an
// identical write identically; only the target shape differs
// (PropertyAssignment/ShorthandPropertyAssignment/SpreadAssignment on
// an ObjectLiteralExpression, plain elements/SpreadElement on an
// ArrayLiteralExpression, versus BindingElement on a BindingPattern).
// A nested pattern recurses with its own slot as the new source. A
// target this walk cannot place (a property/element-access leaf, a
// computed key) forgets through ForgetThrough instead — the same
// "no stale key survives a write nobody can locate" rule every other
// write path keeps.
func WriteAssignmentPattern(ctx *FlowContext, env Env, target *ast.Node, source abstractdomain.AbstractValue, at *ast.Node) {
	if ast.IsArrayLiteralExpression(target) {
		for i, element := range target.AsArrayLiteralExpression().Elements.Nodes {
			if ast.IsOmittedExpression(element) {
				continue
			}
			if ast.IsSpreadElement(element) {
				ForgetThrough(ctx, env, element.AsSpreadElement().Expression)
				continue
			}
			leaf := element
			var defaultExpr *ast.Node
			if ast.IsBinaryExpression(element) {
				be := element.AsBinaryExpression()
				if be.OperatorToken.Kind == ast.KindEqualsToken {
					leaf = be.Left
					defaultExpr = be.Right
				}
			}
			held := SlotOfIndex(source, i)
			if defaultExpr != nil {
				held = withDefault(held)
			}
			writeAssignmentTargetLeaf(ctx, env, leaf, held, at)
		}
		return
	}
	if ast.IsObjectLiteralExpression(target) {
		for _, property := range target.AsObjectLiteralExpression().Properties.Nodes {
			switch {
			case ast.IsPropertyAssignment(property):
				pa := property.AsPropertyAssignment()
				var key string
				hasKey := ast.IsIdentifier(pa.Name()) || ast.IsStringLiteral(pa.Name()) || ast.IsNumericLiteral(pa.Name())
				if hasKey {
					key = pa.Name().Text()
				}
				leaf := pa.Initializer
				var defaultExpr *ast.Node
				if ast.IsBinaryExpression(leaf) {
					be := leaf.AsBinaryExpression()
					if be.OperatorToken.Kind == ast.KindEqualsToken {
						leaf = be.Left
						defaultExpr = be.Right
					}
				}
				var held abstractdomain.AbstractValue
				if hasKey {
					held = SlotOf(source, key)
				} else {
					held = darkSlotOf(source)
				}
				if defaultExpr != nil {
					held = withDefault(held)
				}
				writeAssignmentTargetLeaf(ctx, env, leaf, held, at)
			case ast.IsShorthandPropertyAssignment(property):
				spa := property.AsShorthandPropertyAssignment()
				WriteBinding(ctx, env, spa.Name().Text(), SlotOf(source, spa.Name().Text()), at, "an assigned value")
			case ast.IsSpreadAssignment(property):
				ForgetThrough(ctx, env, property.AsSpreadAssignment().Expression)
			}
		}
		return
	}
	// not a pattern at all — a plain identifier/property/element target
	ForgetThrough(ctx, env, target)
}

// writeAssignmentTargetLeaf is one leaf of an assignment pattern: an
// identifier judges through WriteBinding, a nested pattern recurses,
// anything else (a property/element-access leaf) forgets through
// ForgetThrough — the same per-leaf dispatch ForgetThrough's own
// array/object walk already performs, wearing a real value instead of
// a havoc.
func writeAssignmentTargetLeaf(ctx *FlowContext, env Env, leaf *ast.Node, held abstractdomain.AbstractValue, at *ast.Node) {
	if ast.IsIdentifier(leaf) {
		// the LEAF is the judged position: its own host type is the
		// bound name's (Age at `[age] = xs`), where the pattern's `at`
		// (the right-hand side) wears the SOURCE's type — judging there
		// read the array's own sort as the position and misfired
		// "a number, and the position states an array" on an in-set bind
		WriteBinding(ctx, env, leaf.Text(), held, leaf, "an assigned value")
		return
	}
	if ast.IsObjectLiteralExpression(leaf) || ast.IsArrayLiteralExpression(leaf) {
		WriteAssignmentPattern(ctx, env, leaf, held, at)
		return
	}
	ForgetThrough(ctx, env, leaf)
}

// embeddedReferences collects reference-typed names a literal
// EMBEDS — `{ inner: account }` stores the very reference, so binding
// the literal aliases the embedded name: a write through either path
// is a write to the one object. Spread sources are included
// conservatively (their nested references are shared by the copy).
func embeddedReferences(ctx *FlowContext, e *ast.Node, into *[]string) {
	if ast.IsIdentifier(e) {
		if dataflowfacts.ReferenceTyped(ctx.P.Checker, e) {
			*into = append(*into, e.Text())
		}
		return
	}
	if ast.IsObjectLiteralExpression(e) {
		for _, property := range e.AsObjectLiteralExpression().Properties.Nodes {
			if ast.IsPropertyAssignment(property) {
				embeddedReferences(ctx, property.AsPropertyAssignment().Initializer, into)
			} else if ast.IsShorthandPropertyAssignment(property) {
				spa := property.AsShorthandPropertyAssignment()
				if dataflowfacts.ReferenceTyped(ctx.P.Checker, spa.Name()) {
					*into = append(*into, spa.Name().Text())
				}
			} else if ast.IsSpreadAssignment(property) {
				// a spread copies SCALAR fields by value — only a nested
				// reference makes the copy share structure with its source
				sa := property.AsSpreadAssignment()
				t := typereading.TypeAtLocation(ctx.P.Checker, sa.Expression)
				// The TS source reads type.getProperties() (safe on any
				// type shape, [] when there are none). t.Symbol() is nil
				// for a union/primitive/any spread source, so walking
				// t.Symbol().Members directly panics; GetPropertiesOfType
				// is the checker's own safe equivalent (already used the
				// same way in typereading/host_type.go).
				sharesStructure := false
				for _, member := range ctx.P.Checker.GetPropertiesOfType(t) {
					if dataflowfacts.ReferenceType(ctx.P.Checker.GetTypeOfSymbolAtLocation(member, sa.Expression)) {
						sharesStructure = true
						break
					}
				}
				if sharesStructure {
					embeddedReferences(ctx, sa.Expression, into)
				}
			}
		}
		return
	}
	if ast.IsArrayLiteralExpression(e) {
		for _, element := range e.AsArrayLiteralExpression().Elements.Nodes {
			if ast.IsSpreadElement(element) {
				embeddedReferences(ctx, element.AsSpreadElement().Expression, into)
			} else if !ast.IsOmittedExpression(element) {
				embeddedReferences(ctx, element, into)
			}
		}
	}
}

// LinkEmbedded records the aliasing a binding's initializer creates:
// a literal embedding tracked references links the new name to each
// of them.
func LinkEmbedded(ctx *FlowContext, name string, initializer *ast.Node) {
	if !ast.IsObjectLiteralExpression(initializer) && !ast.IsArrayLiteralExpression(initializer) {
		return
	}
	var embedded []string
	embeddedReferences(ctx, initializer, &embedded)
	for _, other := range embedded {
		ctx.Aliases.Link(name, other)
	}
}

// ForgetThrough forgets whatever an assignment target may have
// written through — including every target inside a destructuring
// pattern.
func ForgetThrough(ctx *FlowContext, env Env, receiver *ast.Node) {
	// wrappers change no reference: look through them to the target —
	// a non-null assertion and a satisfies included, or a write
	// through `(o!).x` would forget nothing
	if ast.IsParenthesizedExpression(receiver) {
		ForgetThrough(ctx, env, receiver.AsParenthesizedExpression().Expression)
		return
	}
	if ast.IsAsExpression(receiver) {
		ForgetThrough(ctx, env, receiver.AsAsExpression().Expression)
		return
	}
	if ast.IsNonNullExpression(receiver) {
		ForgetThrough(ctx, env, receiver.AsNonNullExpression().Expression)
		return
	}
	if ast.IsSatisfiesExpression(receiver) {
		ForgetThrough(ctx, env, receiver.AsSatisfiesExpression().Expression)
		return
	}
	if ast.IsIdentifier(receiver) {
		if _, ok := env.Get(receiver.Text()); ok {
			HavocEnv(ctx.Aliases, env, receiver.Text())
		}
		return
	}
	// a write through `this` forgets the tracked instance the same way
	// a named holder forgets
	if receiver.Kind == ast.KindThisKeyword {
		if _, ok := env.Get("this"); ok {
			HavocEnv(ctx.Aliases, env, "this")
		}
		return
	}
	if ast.IsPropertyAccessExpression(receiver) {
		ForgetThrough(ctx, env, receiver.AsPropertyAccessExpression().Expression)
		return
	}
	if ast.IsElementAccessExpression(receiver) {
		ForgetThrough(ctx, env, receiver.AsElementAccessExpression().Expression)
		return
	}
	// [a, b] = …  — each element is a target
	if ast.IsArrayLiteralExpression(receiver) {
		for _, element := range receiver.AsArrayLiteralExpression().Elements.Nodes {
			if ast.IsSpreadElement(element) {
				ForgetThrough(ctx, env, element.AsSpreadElement().Expression)
			} else if ast.IsBinaryExpression(element) {
				ForgetThrough(ctx, env, element.AsBinaryExpression().Left)
			} else {
				ForgetThrough(ctx, env, element)
			}
		}
		return
	}
	// ({x} = …) — each property value is a target
	if ast.IsObjectLiteralExpression(receiver) {
		for _, property := range receiver.AsObjectLiteralExpression().Properties.Nodes {
			if ast.IsPropertyAssignment(property) {
				ForgetThrough(ctx, env, property.AsPropertyAssignment().Initializer)
			} else if ast.IsShorthandPropertyAssignment(property) {
				spa := property.AsShorthandPropertyAssignment()
				if _, ok := env.Get(spa.Name().Text()); ok {
					HavocEnv(ctx.Aliases, env, spa.Name().Text())
				}
			} else if ast.IsSpreadAssignment(property) {
				ForgetThrough(ctx, env, property.AsSpreadAssignment().Expression)
			}
		}
	}
}

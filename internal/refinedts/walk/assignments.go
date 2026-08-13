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
)

// WriteBinding binds a written value: a declared binding judges every
// write against its stated set — the declared type is the invariant.
func WriteBinding(ctx *FlowContext, env Env, name string, value abstractdomain.AbstractValue, at *ast.Node, what string) {
	stated, hasStated := ctx.Declared[name]
	if hasStated {
		CheckAssignability(ctx, value, *stated, at, what, nil)
	}
	// the write invalidates every difference row rooted at this name —
	// the row spoke about the value that just moved
	ctx.Aliases.Invalidate(name)
	// ...and every PLACE-VALUE entry rooted here (the dotted keys a
	// guard recorded): the path spoke about the value that just moved
	ForgetPlaceEntriesEnv(env, name)
	env.Set(name, value)
}

// WriteElement is a write through an index. `name[i] = v` must land
// inside the element set a declared SEQUENCE binding states — the
// element set is an invariant exactly like the whole set
// (WriteBinding), and the index does not matter: every slot wears it.
// Judging only; what the write does to the tracked value stays with
// the caller.
func WriteElement(ctx *FlowContext, target *ast.Node, value abstractdomain.AbstractValue, at *ast.Node) {
	eae := target.AsElementAccessExpression()
	if !ast.IsIdentifier(eae.Expression) {
		return
	}
	declared, hasDeclared := ctx.Declared[eae.Expression.Text()]
	if !hasDeclared || declared.Kind != annotations.DeclaredSet {
		return
	}
	window, ok := refinementsets.AsRepetition(*declared.Set)
	if !ok {
		return
	}
	CheckAssignability(
		ctx,
		value,
		annotations.DeclaredRefinement{Kind: annotations.DeclaredSet, Set: &window.Element},
		at,
		"the written element",
		nil,
	)
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
		shape := ctx.P.Checker.GetTypeAtLocation(pae.Expression)
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
				t := ctx.P.Checker.GetTypeAtLocation(sa.Expression)
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

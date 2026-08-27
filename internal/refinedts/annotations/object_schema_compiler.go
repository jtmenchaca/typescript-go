// z.object graphs: compiling a `z.object` / `.extend` / `.pick`
// chain into an object annotation. Type-node reading, per-key
// compilation, refine rows, and the kernel specification live
// beside this file.
//
// Ported 1:1 from annotations/object_schema_compiler.ts.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// compiledObject is CompiledObject in the TS source: {object} |
// Unsupported.
type compiledObject struct {
	Object      *ObjectAnnotation
	Unsupported string
}

// receiverObject is receiverObject in the TS source: the base object
// annotation a derived chain's receiver names -- a schema constant in
// the registry, or a direct z.object chain.
func receiverObject(p *program.CheckerProgram, receiver *ast.Node, registry AnnotationRegistry, objects ObjectRegistry) *ObjectAnnotation {
	if ast.IsIdentifier(receiver) {
		symbol := symbolAt(p.Checker, receiver)
		if symbol == nil {
			return nil
		}
		return objects[symbol]
	}
	if rootsInObject(p, receiver) {
		compiled := CompileObject(p, receiver, registry, objects)
		if compiled.Unsupported != "" {
			return nil
		}
		return compiled.Object
	}
	return nil
}

// derivedObjectChain is derivedObjectChain in the TS source: is this
// `X.extend({...})` or `X.pick({...})` over a schema the objects
// registry knows? Recognition is tight -- the receiver must already
// resolve -- so an unrelated library's `.extend` never draws a
// refusal.
func derivedObjectChain(p *program.CheckerProgram, expr *ast.Node, registry AnnotationRegistry, objects ObjectRegistry) bool {
	// `.refine(...)` wraps ride the same statement
	cursor := expr
	for ast.IsCallExpression(cursor) && ast.IsPropertyAccessExpression(cursor.AsCallExpression().Expression) &&
		cursor.AsCallExpression().Expression.AsPropertyAccessExpression().Name().Text() == "refine" {
		cursor = cursor.AsCallExpression().Expression.AsPropertyAccessExpression().Expression
	}
	if !ast.IsCallExpression(cursor) || !ast.IsPropertyAccessExpression(cursor.AsCallExpression().Expression) {
		return false
	}
	access := cursor.AsCallExpression().Expression.AsPropertyAccessExpression()
	name := access.Name().Text()
	if name != "extend" && name != "pick" {
		return false
	}
	args := cursor.AsCallExpression().Arguments.Nodes
	if len(args) != 1 || !ast.IsObjectLiteralExpression(args[0]) {
		return false
	}
	return receiverObject(p, access.Expression, registry, objects) != nil
}

// CompileObject is compileObject in the TS source: compile a
// `z.object({...})` expression -- or a chain DERIVED from one:
// `.extend({...})` merges the incoming shape over the base (incoming
// wins, vendored util.ts extend), `.pick({...})` keeps exactly the
// truthy-masked keys (vendored util.ts pick).
func CompileObject(p *program.CheckerProgram, expr *ast.Node, registry AnnotationRegistry, objects ObjectRegistry) compiledObject {
	// `.refine(predicate)` wraps the object statement: a body the
	// reader can parse (comparisons between keys, conjoined) becomes
	// DEPENDENT rows on the keys -- proved through, exactly like the
	// z.Gte family; any other body marks the annotation unread, since
	// the parse then checks more than the keys say
	if ast.IsCallExpression(expr) && ast.IsPropertyAccessExpression(expr.AsCallExpression().Expression) &&
		expr.AsCallExpression().Expression.AsPropertyAccessExpression().Name().Text() == "refine" {
		access := expr.AsCallExpression().Expression.AsPropertyAccessExpression()
		base := CompileObject(p, access.Expression, registry, objects)
		if base.Unsupported != "" {
			return base
		}
		args := expr.AsCallExpression().Arguments.Nodes
		var reading RefineReading
		readingOk := false
		if len(args) > 0 {
			reading, readingOk = RefineRows(args[0], base.Object)
		}
		if !readingOk {
			withUnread := *base.Object
			withUnread.Unread = true
			return compiledObject{Object: &withUnread}
		}
		// per-key literal facts fold into the key's own set
		keys := make([]ObjectKeySpec, len(base.Object.Keys))
		copy(keys, base.Object.Keys)
		for i, key := range keys {
			if key.Value.Kind != KeyValueSet {
				continue
			}
			forms := reading.keyForms[key.Name]
			exclusions := reading.keyExclusions[key.Name]
			if len(forms) == 0 && len(exclusions) == 0 {
				continue
			}
			widened := refinementsets.MakeRefinedSet(append(append([]refinementsets.Refinement{}, derefSet(key.Value.Set).Forms...), forms...)...)
			var set refinementsets.RefinedSet
			if len(exclusions) == 0 {
				set = widened
			} else {
				set = refinementsets.MakeRefinedSet(refinementsets.Difference(widened, refinementsets.MakeRefinedSet(refinementsets.OneOf(exclusions))))
			}
			key.Value.Set = setPtr(set)
			keys[i] = key
		}
		for _, row := range reading.rows {
			for i, key := range keys {
				if key.Name == row.key && key.Value.Kind == KeyValueSet {
					key.Value.Depends = append(append([]DependentBound{}, key.Value.Depends...), DependentBound{Op: row.op, Param: row.param})
					keys[i] = key
				}
			}
		}
		// `.refine` adds a predicate over the same keys — it neither
		// admits nor drops one, so the base's whole-key-set fact rides
		return compiledObject{Object: &ObjectAnnotation{Keys: keys, Unread: base.Object.Unread, WholeKeySet: base.Object.WholeKeySet}}
	}
	if ast.IsCallExpression(expr) && ast.IsPropertyAccessExpression(expr.AsCallExpression().Expression) {
		access := expr.AsCallExpression().Expression.AsPropertyAccessExpression()
		name := access.Name().Text()
		if name == "extend" || name == "pick" {
			base := receiverObject(p, access.Expression, registry, objects)
			args := expr.AsCallExpression().Arguments.Nodes
			if base == nil || len(args) == 0 || !ast.IsObjectLiteralExpression(args[0]) {
				return compiledObject{Unsupported: "not a derived object chain"}
			}
			argument := args[0]
			if name == "pick" {
				var kept []ObjectKeySpec
				for _, property := range argument.AsObjectLiteralExpression().Properties.Nodes {
					if !ast.IsPropertyAssignment(property) {
						return compiledObject{Unsupported: ".pick takes a literal {key: true} mask"}
					}
					assignment := property.AsPropertyAssignment()
					if !ast.IsIdentifier(assignment.Name()) || assignment.Initializer.Kind != ast.KindTrueKeyword {
						return compiledObject{Unsupported: ".pick takes a literal {key: true} mask"}
					}
					picked := assignment.Name().AsIdentifier().Text
					var held *ObjectKeySpec
					for i := range base.Keys {
						if base.Keys[i].Name == picked {
							held = &base.Keys[i]
							break
						}
					}
					if held == nil {
						return compiledObject{Unsupported: "'.pick' names '" + picked + "', which the schema does not declare"}
					}
					kept = append(kept, *held)
				}
				// `.pick` keeps exactly the masked keys, and the parse of
				// the picked schema strips to those — so the result's key
				// list is its whole key set whenever the base's was
				return compiledObject{Object: &ObjectAnnotation{Keys: SanitizeDepends(kept), Unread: base.Unread, WholeKeySet: base.WholeKeySet}}
			}
			var incoming []ObjectKeySpec
			for _, property := range argument.AsObjectLiteralExpression().Properties.Nodes {
				if !ast.IsPropertyAssignment(property) {
					return compiledObject{Unsupported: ".extend keys are plain `name: schema` assignments"}
				}
				assignment := property.AsPropertyAssignment()
				var name string
				if ast.IsIdentifier(assignment.Name()) {
					name = assignment.Name().AsIdentifier().Text
				} else if ast.IsStringLiteral(assignment.Name()) {
					name = assignment.Name().AsStringLiteral().Text
				} else {
					return compiledObject{Unsupported: ".extend keys are plain names"}
				}
				compiled, unsup := CompileKeyValue(p, assignment.Initializer, registry, objects)
				if unsup != nil {
					return compiledObject{Unsupported: unsup.Unsupported}
				}
				incoming = append(incoming, ObjectKeySpec{
					Name: name, Count: setPtr(compiled.count), MayBeAbsent: compiled.mayBeAbsent,
					At: assignment.Initializer, Value: compiled.value,
				})
			}
			overridden := map[string]bool{}
			for _, key := range incoming {
				overridden[key.Name] = true
			}
			var keys []ObjectKeySpec
			for _, key := range base.Keys {
				if !overridden[key.Name] {
					keys = append(keys, key)
				}
			}
			keys = append(keys, incoming...)
			// every incoming key either compiled or returned Unsupported
			// above — this arm drops none — so the merged list is the
			// whole key set exactly when the base's list was
			return compiledObject{Object: &ObjectAnnotation{Keys: keys, Unread: base.Unread, WholeKeySet: base.WholeKeySet}}
		}
	}
	if !rootsInObject(p, expr) || !ast.IsCallExpression(expr) {
		return compiledObject{Unsupported: "not a z.object statement"}
	}
	args := expr.AsCallExpression().Arguments.Nodes
	if len(args) == 0 || !ast.IsObjectLiteralExpression(args[0]) {
		return compiledObject{Unsupported: "z.object takes a literal {key: schema} record"}
	}
	var keys []ObjectKeySpec
	// a key the compiler DROPS below (the library-adapter continue)
	// leaves the key list short of the schema's own shape, so the
	// whole-key-set fact is off for this statement — the count of the
	// keys would be a count of what was read, not of what the parse
	// produces
	droppedAKey := false
	for _, property := range args[0].AsObjectLiteralExpression().Properties.Nodes {
		if !ast.IsPropertyAssignment(property) {
			return compiledObject{Unsupported: "z.object keys are plain `name: schema` assignments"}
		}
		assignment := property.AsPropertyAssignment()
		var name string
		if ast.IsIdentifier(assignment.Name()) {
			name = assignment.Name().AsIdentifier().Text
		} else if ast.IsStringLiteral(assignment.Name()) {
			name = assignment.Name().AsStringLiteral().Text
		} else {
			return compiledObject{Unsupported: "z.object keys are plain names"}
		}
		compiled, unsup := CompileKeyValue(p, assignment.Initializer, registry, objects)
		if unsup != nil {
			// a library-adapter key the reader cannot compile
			// (z.symbol, z.map, a shape outside the vocabulary)
			// drops out instead of voiding the whole object: an
			// unlisted key claims nothing, which is sound, and the
			// readable keys still read. The checker's OWN surface
			// still alerts loudly -- an authoring error there should
			// not vanish.
			if rootsInLibraryAdapter(p, assignment.Initializer) {
				droppedAKey = true
				continue
			}
			return compiledObject{Unsupported: unsup.Unsupported}
		}
		keys = append(keys, ObjectKeySpec{
			Name: name, Count: setPtr(compiled.count), MayBeAbsent: compiled.mayBeAbsent,
			At: assignment.Initializer, Value: compiled.value,
		})
	}
	// The two roots that reach this arm both leave the parsed value
	// carrying exactly the shape's own keys. `z.object` STRIPS: its
	// parse builds a fresh object and writes only the shape's keys
	// into it (WholeKeySet's own doc cites the two readings).
	// `z.strictObject` THROWS on an extra key, so a value that parsed
	// carries no key outside the shape either. rootsInObject admits
	// only those two — `z.looseObject`, and any `.catchall` /
	// `.passthrough` chain, never compile to an object annotation at
	// all, so no key-admitting statement reaches here.
	return compiledObject{Object: &ObjectAnnotation{Keys: keys, WholeKeySet: !droppedAKey}}
}

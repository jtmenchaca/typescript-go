// What one object-key value expression compiles to: a set, nested
// object, collection, or reference — plus the array-of-records
// reading a binding states at a judged position.
//
// Ported 1:1 from annotations/object_key_compiler.ts.

package annotations

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// naturals is NATURALS in the TS source: N -- the counts an
// unbounded collection admits.
var naturals = refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(0))

// boundedCounts is boundedCounts in the TS source: the counts a
// bounded collection admits: [lo, hi] intersect N.
func boundedCounts(lo float64, hi float64, hasHi bool) refinementsets.RefinedSet {
	if !hasHi {
		return refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(lo))
	}
	return refinementsets.MakeRefinedSet(refinementsets.Integer, refinementsets.AtLeast(lo), refinementsets.AtMost(hi))
}

// compiledKey is CompiledKey in the TS source.
type compiledKey struct {
	value       ObjectKeyValue
	count       refinementsets.RefinedSet
	mayBeAbsent bool
}

// ReferenceTarget is referenceTarget in the TS source: is this a
// `z.ref(Target)` call? Returns the target's symbol, or nil.
func ReferenceTarget(p *program.CheckerProgram, expr *ast.Node) *ast.Symbol {
	if !ast.IsCallExpression(expr) || !ast.IsPropertyAccessExpression(expr.AsCallExpression().Expression) {
		return nil
	}
	access := expr.AsCallExpression().Expression.AsPropertyAccessExpression()
	if access.Name().Text() != "ref" || !ast.IsIdentifier(access.Expression) || !resolvesToAnnotationRoot(p, access.Expression) {
		return nil
	}
	args := expr.AsCallExpression().Arguments.Nodes
	if len(args) == 0 {
		return nil
	}
	argument := args[0]
	// `z.ref(Target)` -- the schema itself; a thunked `() => Target`
	// is also read, since a self-reference cannot name itself eagerly
	named := argument
	if ast.IsArrowFunction(argument) {
		body := argument.Body()
		if body != nil && !ast.IsBlock(body) {
			if ast.IsParenthesizedExpression(body) {
				named = body.AsParenthesizedExpression().Expression
			} else {
				named = body
			}
		}
	}
	if !ast.IsIdentifier(named) {
		return nil
	}
	return symbolAt(p.Checker, named)
}

// collectionBoundsResult is the {base, lo, hi} triple
// CollectionBounds returns, hi optional.
type collectionBoundsResult struct {
	base  *ast.Node
	lo    float64
	hi    float64
	hasHi bool
}

// CollectionBounds is collectionBounds in the TS source: a `.min(n)`
// / `.max(n)` chain over a base call -- the bounds a ref array
// states, which become the cardinality.
func CollectionBounds(expr *ast.Node) collectionBoundsResult {
	lo := 0.0
	hasHi := false
	hi := 0.0
	current := expr
	for ast.IsCallExpression(current) && ast.IsPropertyAccessExpression(current.AsCallExpression().Expression) {
		access := current.AsCallExpression().Expression.AsPropertyAccessExpression()
		method := access.Name().Text()
		callArgs := current.AsCallExpression().Arguments.Nodes
		var n float64
		hasN := false
		if len(callArgs) > 0 && ast.IsNumericLiteral(callArgs[0]) {
			n, hasN = NumberArg(nil, callArgs[0])
		}
		if method == "min" && hasN {
			if n > lo {
				lo = n
			}
		} else if method == "max" && hasN {
			if !hasHi {
				hi = n
				hasHi = true
			} else if n < hi {
				hi = n
			}
		} else if method == "length" && hasN {
			if n > lo {
				lo = n
			}
			if !hasHi {
				hi = n
				hasHi = true
			} else if n < hi {
				hi = n
			}
		} else {
			break
		}
		current = access.Expression
	}
	return collectionBoundsResult{base: current, lo: lo, hi: hi, hasHi: hasHi}
}

// ArrayElement is arrayElement in the TS source: the element of a
// `z.array(X)` call, or nil.
func ArrayElement(p *program.CheckerProgram, expr *ast.Node) *ast.Node {
	if ast.IsCallExpression(expr) && ast.IsPropertyAccessExpression(expr.AsCallExpression().Expression) {
		access := expr.AsCallExpression().Expression.AsPropertyAccessExpression()
		if access.Name().Text() == "array" && ast.IsIdentifier(access.Expression) && resolvesToAnnotationRoot(p, access.Expression) {
			args := expr.AsCallExpression().Arguments.Nodes
			if len(args) > 0 {
				return args[0]
			}
		}
	}
	return nil
}

// readsAsRecordsStatement is readsAsRecordsStatement in the TS
// source: the ARRAY-OF-RECORDS a binding states: `const zRows =
// z.array(z.object({...}))` (optionally `.min/.max/.length`
// chained), or `z.array(z.ref(X))` for a named target. False where
// the initializer spells anything else.
func readsAsRecordsStatement(p *program.CheckerProgram, initializer *ast.Node, registry AnnotationRegistry, objects ObjectRegistry) bool {
	bounded := CollectionBounds(initializer)
	element := ArrayElement(p, bounded.base)
	if element == nil {
		return false
	}
	if ReferenceTarget(p, element) != nil {
		return true
	}
	return rootsInObject(p, element) || derivedObjectChain(p, element, registry, objects)
}

// ArrayOfRecords is arrayOfRecords in the TS source.
func ArrayOfRecords(p *program.CheckerProgram, symbol *ast.Symbol, registry AnnotationRegistry, objects ObjectRegistry) AnnotationOfTypeResult {
	declaration := symbol.ValueDeclaration
	if declaration == nil || !ast.IsVariableDeclaration(declaration) {
		return AnnotationOfTypeResult{}
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return AnnotationOfTypeResult{}
	}
	bounded := CollectionBounds(initializer)
	element := ArrayElement(p, bounded.base)
	if element == nil {
		return AnnotationOfTypeResult{}
	}
	// the named graph node -- the stricter opt-in
	target := ReferenceTarget(p, element)
	if target != nil {
		known := objects[target]
		if known == nil {
			return AnnotationOfTypeResult{}
		}
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{
			Kind: DeclaredObjectArray, Object: known, Lo: bounded.lo, Hi: bounded.hi, HiUnbounded: !bounded.hasHi,
		}}
	}
	// the element stated INLINE
	if rootsInObject(p, element) || derivedObjectChain(p, element, registry, objects) {
		compiled := CompileObject(p, element, registry, objects)
		if compiled.Unsupported != "" {
			return AnnotationOfTypeResult{Unsupported: compiled.Unsupported}
		}
		return AnnotationOfTypeResult{Stated: &DeclaredRefinement{
			Kind: DeclaredObjectArray, Object: compiled.Object, Lo: bounded.lo, Hi: bounded.hi, HiUnbounded: !bounded.hasHi,
		}}
	}
	return AnnotationOfTypeResult{}
}

// CompileKeyValue is compileKeyValue in the TS source.
func CompileKeyValue(p *program.CheckerProgram, expr *ast.Node, registry AnnotationRegistry, objects ObjectRegistry) (compiledKey, *Unsupported) {
	// `.optional()` / `.nullable()` / `.nullish()` -- absence is a
	// cardinality fact, not a value: the count becomes {0, 1}. They
	// denote the same graph and differ only as runtime checks
	// (TERMS.md §6; zod's own nullish() is optional(nullable(this)),
	// verified in the vendored source).
	if ast.IsCallExpression(expr) && ast.IsPropertyAccessExpression(expr.AsCallExpression().Expression) {
		access := expr.AsCallExpression().Expression.AsPropertyAccessExpression()
		name := access.Name().Text()
		if (name == "optional" || name == "nullable" || name == "nullish") && len(expr.AsCallExpression().Arguments.Nodes) == 0 {
			inner, unsup := CompileKeyValue(p, access.Expression, registry, objects)
			if unsup != nil {
				return compiledKey{}, unsup
			}
			return compiledKey{
				value:       inner.value,
				count:       refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
				mayBeAbsent: true,
			}, nil
		}
	}

	// a collection of references: the bounds are the CARDINALITY, not
	// tuple-layer lengths (the representation criterion)
	bounded := CollectionBounds(expr)
	element := ArrayElement(p, bounded.base)
	if element != nil {
		target := ReferenceTarget(p, element)
		if target != nil {
			var count refinementsets.RefinedSet
			if bounded.lo == 0 && !bounded.hasHi {
				count = naturals
			} else {
				count = boundedCounts(bounded.lo, bounded.hi, bounded.hasHi)
			}
			return compiledKey{
				value:       ObjectKeyValue{Kind: KeyValueReference, Target: target},
				count:       count,
				mayBeAbsent: bounded.lo == 0,
			}, nil
		}
	}

	// one reference: exactly one target element per object
	required := refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1}))
	if single := ReferenceTarget(p, expr); single != nil {
		return compiledKey{
			value: ObjectKeyValue{Kind: KeyValueReference, Target: single},
			count: refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{1})),
		}, nil
	}

	// a nested object -- direct, or derived (`X.pick`, `X.extend`)
	if rootsInObject(p, expr) || derivedObjectChain(p, expr, registry, objects) {
		nested := CompileObject(p, expr, registry, objects)
		if nested.Unsupported != "" {
			return compiledKey{}, &Unsupported{Unsupported: nested.Unsupported, At: expr}
		}
		return compiledKey{
			value: ObjectKeyValue{Kind: KeyValueObject, Object: nested.Object},
			count: required,
		}, nil
	}

	// a reference to a stated OBJECT annotation by name
	if ast.IsIdentifier(expr) {
		symbol := symbolAt(p.Checker, expr)
		if symbol != nil {
			if known := objects[symbol]; known != nil {
				return compiledKey{
					value: ObjectKeyValue{Kind: KeyValueObject, Object: known},
					count: required,
				}, nil
			}
		}
	}

	// a Map/Set statement key: z.map(K, V) / z.set(V) with size checks
	collection, hasCollection, unsup := CompileCollectionKey(p, expr, registry)
	if unsup != nil {
		return compiledKey{}, unsup
	}
	if hasCollection {
		return compiledKey{value: collection, count: required}, nil
	}

	// otherwise: a value key -- an ordinary refined set, one per
	// object
	set := CompileAnnotation(p, expr, registry)
	if IsUnsupported(set) {
		return compiledKey{}, set.Unsupported
	}
	return compiledKey{
		value: ObjectKeyValue{
			Kind:     KeyValueSet,
			Set:      set.Annotation.Set,
			Absent:   set.Annotation.Absent,
			Measures: set.Annotation.Measures,
			KindTag:  set.Annotation.KindTag,
			Word:     set.Annotation.Word,
			Unread:   set.Annotation.Unread,
		},
		count: required,
	}, nil
}

// CompileCollectionKey is compileCollectionKey in the TS source: a
// z.map(K, V) / z.set(V) chain with size checks, read into the
// collection key kind -- (zero value, false, nil) where the
// expression is no such chain.
func CompileCollectionKey(p *program.CheckerProgram, expr *ast.Node, registry AnnotationRegistry) (ObjectKeyValue, bool, *Unsupported) {
	sizeForms := []refinementsets.Refinement{refinementsets.Integer, refinementsets.AtLeast(0)}
	base := expr
	for ast.IsCallExpression(base) && ast.IsPropertyAccessExpression(base.AsCallExpression().Expression) {
		access := base.AsCallExpression().Expression.AsPropertyAccessExpression()
		method := access.Name().Text()
		callArgs := base.AsCallExpression().Arguments.Nodes
		if method == "nonempty" && len(callArgs) == 0 {
			sizeForms = append(sizeForms, refinementsets.Above(1))
			base = access.Expression
			continue
		}
		if (method == "min" || method == "max" || method == "size") && len(callArgs) >= 1 && ast.IsNumericLiteral(callArgs[0]) {
			n, _ := NumberArg(nil, callArgs[0])
			switch method {
			case "min":
				sizeForms = append(sizeForms, refinementsets.AtLeast(n))
			case "max":
				sizeForms = append(sizeForms, refinementsets.AtMost(n))
			default:
				sizeForms = append(sizeForms, refinementsets.OneOf([]float64{n}))
			}
			base = access.Expression
			continue
		}
		break
	}
	if !ast.IsCallExpression(base) || !ast.IsPropertyAccessExpression(base.AsCallExpression().Expression) {
		return ObjectKeyValue{}, false, nil
	}
	access := base.AsCallExpression().Expression.AsPropertyAccessExpression()
	root := access.Name().Text()
	if root != "map" && root != "set" {
		return ObjectKeyValue{}, false, nil
	}
	if libraryAdapterOfNode(p, access.Expression) == nil {
		return ObjectKeyValue{}, false, nil
	}
	callArgs := base.AsCallExpression().Arguments.Nodes
	if root == "set" && len(callArgs) == 1 {
		value := CompileAnnotation(p, callArgs[0], registry)
		if IsUnsupported(value) {
			return ObjectKeyValue{}, false, value.Unsupported
		}
		size := refinementsets.MakeRefinedSet(sizeForms...)
		return ObjectKeyValue{
			Kind:   KeyValueCollection,
			Flavor: "set",
			Value:  value.Annotation.Set,
			Size:   &size,
		}, true, nil
	}
	if root == "map" && len(callArgs) == 2 {
		key := CompileAnnotation(p, callArgs[0], registry)
		if IsUnsupported(key) {
			return ObjectKeyValue{}, false, key.Unsupported
		}
		value := CompileAnnotation(p, callArgs[1], registry)
		if IsUnsupported(value) {
			return ObjectKeyValue{}, false, value.Unsupported
		}
		size := refinementsets.MakeRefinedSet(sizeForms...)
		return ObjectKeyValue{
			Kind:   KeyValueCollection,
			Flavor: "map",
			Key:    key.Annotation.Set,
			Value:  value.Annotation.Set,
			Size:   &size,
		}, true, nil
	}
	return ObjectKeyValue{}, false, nil
}

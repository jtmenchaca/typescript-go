// from evaluation/schema_runtime_models.ts
//
// The schema-runtime models: .parse/.safeParse/.parseAsync on a
// stated schema — the proof-producing boundary — and .decode/.encode
// on a z.codec schema. Split from builtin_models.ts per the v2 tree.
//
// `isZodRoot` (an inline callback of the TS source's readSchemaRuntimeCall)
// asks `libraryAdapterOfNode(ctx.p, id)?.name === "zod"`.
// service/program_resolution.ts's own libraryAdapterOfNode has no Go
// port (service/ is not ported — PORT.md), but its body is just
// symbolAt's declarations tested against libraryadapters.LibraryAdapterOfFile
// (annotations/chain_roots.go's unexported libraryAdapterOfNode reads
// the identical shape) — isZodRootHere below inlines that reading
// directly against the exported LibraryAdapterOfFile rather than
// duplicating chain_roots.go's unexported twin or waiting on service/.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations/libraryadapters"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// readSchemaRuntimeCall is readSchemaRuntimeCall in the TS source.
func readSchemaRuntimeCall(site MethodCallSite) *abstractdomain.AbstractValue {
	ctx, env, e, receiverExpression, method := site.Ctx, site.Env, site.E, site.ReceiverExpression, site.Method
	call := e.AsCallExpression()
	var arguments []*ast.Node
	if call.Arguments != nil {
		arguments = call.Arguments.Nodes
	}
	// `.decode(x)` / `.encode(x)` on a z.codec schema: the codec's OWN
	// callback runs on the argument — inline it, the way the transform
	// image inlines (vendored schemas.ts:2403: decode runs the
	// transform, encode the reverse)
	if (method == "decode" || method == "encode") && len(arguments) == 1 && ast.IsIdentifier(receiverExpression) {
		schemaSymbol := symbolAt(ctx.P.Checker, receiverExpression)
		var declaration *ast.Node
		if schemaSymbol != nil && len(schemaSymbol.Declarations) > 0 {
			declaration = schemaSymbol.Declarations[0]
		}
		var initializer *ast.Node
		if declaration != nil && ast.IsVariableDeclaration(declaration) {
			initializer = declaration.AsVariableDeclaration().Initializer
		}
		if initializer != nil && ast.IsCallExpression(initializer) {
			initCall := initializer.AsCallExpression()
			if ast.IsPropertyAccessExpression(initCall.Expression) &&
				initCall.Expression.AsPropertyAccessExpression().Name().Text() == "codec" &&
				initCall.Arguments != nil && len(initCall.Arguments.Nodes) == 3 &&
				ast.IsObjectLiteralExpression(initCall.Arguments.Nodes[2]) {
				var found *ast.Node
				for _, property := range initCall.Arguments.Nodes[2].AsObjectLiteralExpression().Properties.Nodes {
					if ast.IsPropertyAssignment(property) {
						assignment := property.AsPropertyAssignment()
						if ast.IsIdentifier(assignment.Name()) && assignment.Name().Text() == method {
							found = property
							break
						}
					}
				}
				if found != nil {
					initializerExpr := found.AsPropertyAssignment().Initializer
					if ast.IsArrowFunction(initializerExpr) || ast.IsFunctionExpression(initializerExpr) {
						argument := evaluateExpression(ctx, env, arguments[0])
						result := InlineCallback(ctx, env, initializerExpr, argument, LoopAnalyzers{
							AnalyzeStatement:   AnalyzeStatement,
							EvaluateExpression: evaluateExpression,
							IterationElement:   IterationElementOf,
						}, nil)
						if result.Kind != abstractdomain.KindUnknown {
							out := abstractdomain.AtTrustLevel(result, abstractdomain.TrustLibrary)
							return &out
						}
					}
				}
			}
		}
	}
	if (method == "parse" || method == "safeParse" || method == "parseAsync") && len(arguments) == 1 {
		argument := arguments[0]
		argumentKnown := evaluateExpression(ctx, env, argument)
		if ast.IsIdentifier(argument) {
			if _, tracked := env[argument.Text()]; tracked && dataflowfacts.ReferenceTyped(ctx.P.Checker, argument) {
				// parse may return its input — the result shares the
				// argument's reference (the caller may hold both)
				ctx.Aliases.Havoc(env, argument.Text())
			}
		}
		outcome, hasOutcome := EvaluateParseOutcome(receiverExpression, argumentKnown, ParseEvalTools{
			ResolveConst: func(id *ast.Node) *ast.Node {
				symbol := ctx.P.Checker.GetSymbolAtLocation(id)
				if symbol == nil || symbol.ValueDeclaration == nil {
					return nil
				}
				declaration := symbol.ValueDeclaration
				if !ast.IsVariableDeclaration(declaration) {
					return nil
				}
				varDecl := declaration.AsVariableDeclaration()
				if varDecl.Initializer == nil {
					return nil
				}
				declList := declaration.Parent
				if declList == nil || !ast.IsVariableDeclarationList(declList) ||
					declList.Flags&ast.NodeFlagsConst == 0 {
					return nil
				}
				return varDecl.Initializer
			},
			IsZodRoot: func(id *ast.Node) bool {
				return isZodRootHere(ctx.P.Checker, id)
			},
			Inline: func(callback TransformCallback, inlineArgument abstractdomain.AbstractValue) abstractdomain.AbstractValue {
				return InlineCallback(ctx, env, callback, inlineArgument, LoopAnalyzers{
					AnalyzeStatement:   AnalyzeStatement,
					EvaluateExpression: evaluateExpression,
					IterationElement:   IterationElementOf,
				}, nil)
			},
		})
		// safeParse reifies the outcome: a throw IS {success: false}.
		// Parse-eval rows mirror ZOD's runtime — the library grade.
		if method == "safeParse" {
			if hasOutcome {
				if outcome.Kind == outcomeKindThrows {
					out := abstractdomain.KnownObject(
						[]abstractdomain.ObjectKey{
							{Name: "success", Value: abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)},
							{Name: "error", Value: silence.Residue()},
						},
						nil, true, abstractdomain.TrustLibrary, false,
					)
					return &out
				}
				out := abstractdomain.KnownObject(
					[]abstractdomain.ObjectKey{
						{Name: "success", Value: abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)},
						{Name: "data", Value: outcome.Known},
					},
					nil, true, abstractdomain.TrustLibrary, false,
				)
				return &out
			}
			var trackedName string
			hasTrackedName := site.HasTrackedName
			if hasTrackedName {
				trackedName = site.TrackedName
			}
			if hasTrackedName && dataflowfacts.ReferenceTyped(ctx.P.Checker, receiverExpression) {
				ctx.Aliases.Havoc(env, trackedName)
			}
			// an UNKNOWN input still has a known result shape: success
			// is one of the two booleans, and data wears the stated set
			// with absence riding beside it. The CORRELATION rides as
			// two variants — success carries data, failure carries the
			// error and no data — so a branch on `.success` (or on
			// `.data`) selects the runtime shape. Zod's contract, at
			// library grade.
			if ast.IsIdentifier(receiverExpression) {
				schemaSymbol := symbolAt(ctx.P.Checker, receiverExpression)
				var object *annotations.ObjectAnnotation
				var annotation *annotations.Annotation
				if schemaSymbol != nil {
					object = ctx.Objects[schemaSymbol]
					annotation = ctx.Registry[schemaSymbol]
				}
				var stated *abstractdomain.AbstractValue
				if object != nil {
					worn := WornOfObject(object)
					stated = &worn
				} else if annotation != nil && !annotation.Promise {
					worn := WornOfAnnotation(*annotation)
					stated = &worn
				}
				if stated != nil {
					out := abstractdomain.KnownWithVariants(
						abstractdomain.KnownObject(
							[]abstractdomain.ObjectKey{
								{Name: "success", Value: abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)},
								{Name: "data", Value: abstractdomain.PossiblyUndefined(*stated, abstractdomain.TrustProved, false, false)},
								{Name: "error", Value: silence.Residue()},
							},
							nil, false, abstractdomain.TrustLibrary, false,
						),
						[]abstractdomain.AbstractValue{
							abstractdomain.KnownObject(
								[]abstractdomain.ObjectKey{
									{Name: "success", Value: abstractdomain.KnownValues([]float64{1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)},
									{Name: "data", Value: *stated},
								},
								nil, true, abstractdomain.TrustLibrary, false,
							),
							abstractdomain.KnownObject(
								[]abstractdomain.ObjectKey{
									{Name: "success", Value: abstractdomain.KnownValues([]float64{0}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)},
									{Name: "error", Value: silence.Residue()},
								},
								nil, true, abstractdomain.TrustLibrary, false,
							),
						},
					)
					return &out
				}
			}
			out := silence.Residue()
			return &out
		}
		if hasOutcome && outcome.Kind == outcomeKindValue {
			exact := abstractdomain.AtTrustLevel(outcome.Known, abstractdomain.TrustLibrary)
			// parseAsync hands the validated value back inside a promise
			if method == "parseAsync" {
				out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &exact}
				return &out
			}
			return &exact
		}
		// an unmodeled `.parse` may still write its receiver — the
		// generic forget rule applies before the honest unknown
		var trackedName string
		hasTrackedName := site.HasTrackedName
		if hasTrackedName {
			trackedName = site.TrackedName
		}
		if hasTrackedName && dataflowfacts.ReferenceTyped(ctx.P.Checker, receiverExpression) {
			ctx.Aliases.Havoc(env, trackedName)
		}
		if ast.IsIdentifier(receiverExpression) {
			schemaSymbol := symbolAt(ctx.P.Checker, receiverExpression)
			var object *annotations.ObjectAnnotation
			var annotation *annotations.Annotation
			if schemaSymbol != nil {
				object = ctx.Objects[schemaSymbol]
				annotation = ctx.Registry[schemaSymbol]
			}
			if object != nil {
				stated := WornOfObject(object)
				// a collection key's exact input carries through: the
				// parse returns its input, and a failing entry or size
				// throws before the read (the date rule, for Map and Set)
				if stated.Kind == abstractdomain.KindObject && argumentKnown.Kind == abstractdomain.KindObject {
					keys := append([]abstractdomain.ObjectKey{}, stated.Keys...)
					carried := false
					for _, key := range object.Keys {
						if key.Value.Kind != annotations.KeyValueCollection {
							continue
						}
						held, hasHeld := objectKeyValue(argumentKnown, key.Name)
						if hasHeld && held.Kind == abstractdomain.KindCollection {
							replaced := abstractdomain.AtTrustLevel(held, abstractdomain.TrustLibrary)
							keys = replaceObjectKey(keys, key.Name, replaced)
							carried = true
						}
					}
					if carried {
						out := abstractdomain.KnownObject(keys, nil, false, abstractdomain.TrustProved, false)
						return &out
					}
				}
				out := WornOfObject(object)
				return &out
			}
			if annotation != nil {
				// a validation-only schema hands its input back unchanged
				// — exact knowledge survives the parse (the date rule)
				if annotation.Passthrough && argumentKnown.Kind != abstractdomain.KindUnknown {
					out := abstractdomain.AtTrustLevel(argumentKnown, abstractdomain.TrustLibrary)
					return &out
				}
				// a promise schema's parse result is a promise OBJECT
				// whose RESOLVED value wears the inner statement (vendored
				// core/schemas.ts:4541 — the resolved value is validated)
				if annotation.Promise {
					inner := WornOfAnnotation(*annotation)
					out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &inner}
					return &out
				}
				// a date schema hands back the Date it validated: an exact
				// argument keeps its exact time value (a failing bound
				// throws, and a thrown run never reaches the read); an
				// unknown one wears wornOfAnnotation — the Date wrapping
				// the stated millis window
				if annotation.Date {
					if argumentKnown.Kind == abstractdomain.KindDate {
						out := abstractdomain.AtTrustLevel(argumentKnown, abstractdomain.TrustLibrary)
						return &out
					}
					out := WornOfAnnotation(*annotation)
					return &out
				}
				worn := WornOfAnnotation(*annotation)
				if method == "parseAsync" {
					out := abstractdomain.AbstractValue{Kind: abstractdomain.KindPromise, Inner: &worn}
					return &out
				}
				return &worn
			}
		}
		out := silence.Residue()
		return &out
	}
	return nil
}

// isZodRootHere is the TS source's inline isZodRoot callback
// (`libraryAdapterOfNode(ctx.p, id)?.name === "zod"`), inlined against
// symbolAt + libraryadapters.LibraryAdapterOfFile (see file banner).
func isZodRootHere(c *checker.Checker, id *ast.Node) bool {
	symbol := symbolAt(c, id)
	if symbol == nil {
		return false
	}
	for _, declaration := range symbol.Declarations {
		adapter := libraryadapters.LibraryAdapterOfFile(ast.GetSourceFileOfNode(declaration).FileName())
		if adapter != nil && adapter.Name == "zod" {
			return true
		}
	}
	return false
}

// objectKeyValue reads one key's value off an "object" AbstractValue's
// ordered Keys slice — the TS source's `input[key]` on the record
// shape.
func objectKeyValue(known abstractdomain.AbstractValue, name string) (abstractdomain.AbstractValue, bool) {
	for _, key := range known.Keys {
		if key.Name == name {
			return key.Value, true
		}
	}
	return abstractdomain.AbstractValue{}, false
}

// replaceObjectKey returns keys with the named entry's value replaced
// (or appended, matching the TS source's `{ ...stated.keys, [key.name]:
// ... }` spread — stated's own keys always already hold the name here,
// since the loop above only ever visits a collection key the object
// annotation itself declares).
func replaceObjectKey(keys []abstractdomain.ObjectKey, name string, value abstractdomain.AbstractValue) []abstractdomain.ObjectKey {
	for i, key := range keys {
		if key.Name == name {
			keys[i].Value = value
			return keys
		}
	}
	return append(keys, abstractdomain.ObjectKey{Name: name, Value: value})
}

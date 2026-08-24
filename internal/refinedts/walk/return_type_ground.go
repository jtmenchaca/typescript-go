// from evaluation/return_type_ground.ts
//
// The last-reader type ground on a call: the stated annotation a
// resolved return type names, a Map.get value annotation, and the
// sort's ground where tsc states one cleanly.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

const absentTypeFlags = checker.TypeFlagsUndefined | checker.TypeFlagsNull | checker.TypeFlagsVoid

func typePartsOf(t *checker.Type) []*checker.Type {
	if t.IsUnion() {
		return t.Types()
	}
	return []*checker.Type{t}
}

// AnnotationOfReturnType is annotationOfReturnType in the TS source:
// the stated annotation a call's RESOLVED return type names: union
// parts split absence off (`| undefined` / `| null`), a `never` part
// carries no value and drops, and every REMAINING part must reach a
// type-alias declaration whose spelling the annotation reader reads
// (`z.infer<typeof X>` and kin). The claim is the declaration's —
// library grade.
//
// A present part that names NO alias has no partial answer to give:
// the widest sound claim for a value the annotation reader cannot
// spell is the unknown, and the union of a stated set with the
// unknown IS the unknown — the same nothing this returns. The absent
// and never parts are the ones a sound partial CAN peel, and both are
// peeled above, so nil here is the union's own answer, not a decline
// short of one.
func AnnotationOfReturnType(ctx *FlowContext, e *ast.Node) *abstractdomain.AbstractValue {
	// the callee's own DECLARED return type node first, read the way a
	// parameter's node already is: resolution can land PAST the alias
	// (`Age` = z.infer<typeof zAge> resolves to a type whose recorded
	// alias is z.infer's own, and that alias's declaration spells a
	// conditional the reader refuses), but the declaration's stated
	// return node still NAMES the alias the reader spells. A node the
	// reader cannot spell — a type variable, an unstated shape —
	// answers nothing here and the resolved-type walk below still
	// runs, so a generic's instantiated return keeps its existing
	// route.
	if ast.IsCallExpression(e) && ctx.P != nil && ctx.P.Checker != nil {
		if signature := ctx.P.Checker.GetResolvedSignature(e); signature != nil {
			if declaration := signature.Declaration(); declaration != nil {
				// only a TYPE REFERENCE (an alias like `Age`, or a stated
				// z.infer<...>) is read here: a bare keyword (`number`)
				// names the whole sort, and the resolved-type walk below
				// is the layer that spells sorts WITH their NaN
				// admission — reading `number` as the NaN-free number
				// set here would state a claim the runtime refutes
				// (fround(NaN) is NaN). A union node falls through the
				// same way, so `Age | undefined` keeps its absence peel.
				if returnNode := declaration.Type(); returnNode != nil && ast.IsTypeReferenceNode(returnNode) {
					read := annotations.AnnotationOfType(ctx.P, returnNode, ctx.Registry, ctx.Objects)
					if read.Stated != nil && read.Unsupported == "" && read.Stated.Kind == annotations.DeclaredSet {
						out := abstractdomain.AtTrustLevel(
							abstractdomain.KnownSet(*read.Stated.Set, read.Stated.Temporal, abstractdomain.TrustProved, setKindTagOf(read.Stated.KindTag)),
							abstractdomain.TrustLibrary,
						)
						return &out
					}
				}
			}
		}
	}
	t := typereading.TypeAtLocation(ctx.P.Checker, e)
	parts := typePartsOf(t)
	sawAbsent := false
	var worn *abstractdomain.AbstractValue
	for _, part := range parts {
		if (part.Flags() & absentTypeFlags) != 0 {
			sawAbsent = true
			continue
		}
		// `never` admits no value, so it contributes nothing to the union
		// and does not make the read absent either
		if (part.Flags() & checker.TypeFlagsNever) != 0 {
			continue
		}
		alias := part.GetTypeAlias()
		if alias == nil || alias.Symbol() == nil {
			return nil
		}
		var declaration *ast.Node
		for _, d := range alias.Symbol().Declarations {
			if ast.IsTypeAliasDeclaration(d) {
				declaration = d
				break
			}
		}
		if declaration == nil {
			return nil
		}
		read := annotations.AnnotationOfType(ctx.P, declaration.AsTypeAliasDeclaration().Type, ctx.Registry, ctx.Objects)
		if read.Stated == nil || read.Unsupported != "" {
			return nil
		}
		if read.Stated.Kind != annotations.DeclaredSet {
			return nil
		}
		piece := abstractdomain.KnownSet(*read.Stated.Set, read.Stated.Temporal, abstractdomain.TrustProved, setKindTagOf(read.Stated.KindTag))
		if worn == nil {
			worn = &piece
		} else {
			joined := abstractdomain.JoinKnown(*worn, piece)
			worn = &joined
		}
	}
	if worn == nil {
		return nil
	}
	graded := abstractdomain.AtTrustLevel(*worn, abstractdomain.TrustLibrary)
	if sawAbsent {
		out := abstractdomain.PossiblyUndefined(graded, "", false, false)
		return &out
	}
	return &graded
}

// declaredTypeNodeOfReceiver is the type node a `.get()` receiver's
// own DECLARATION spells. The receiver resolves through the checker,
// so a bare name (`cache`), a class field (`this.cache`), and any
// longer property chain (`this.store.byId`) all reach the same
// declaration question — what matters is that the resolved
// declaration spells its type, not how the receiver is written. A
// receiver whose symbol the checker does not place, or whose
// declaration carries no spelled type (an inferred field), answers
// nil the way an unresolvable name always did.
func declaredTypeNodeOfReceiver(ctx *FlowContext, receiver *ast.Node) *ast.Node {
	symbol := ctx.P.Checker.GetSymbolAtLocation(receiver)
	if symbol == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if declaration == nil {
		// an interface member has no value declaration; its property
		// signature is the spelling
		for _, d := range symbol.Declarations {
			if ast.IsPropertySignatureDeclaration(d) {
				declaration = d
				break
			}
		}
	}
	if declaration == nil {
		return nil
	}
	switch {
	case ast.IsParameterDeclaration(declaration):
		return declaration.AsParameterDeclaration().Type
	case ast.IsVariableDeclaration(declaration):
		return declaration.AsVariableDeclaration().Type
	case ast.IsPropertyDeclaration(declaration):
		return declaration.AsPropertyDeclaration().Type
	case ast.IsPropertySignatureDeclaration(declaration):
		return declaration.AsPropertySignatureDeclaration().Type
	}
	return nil
}

// MapValueAnnotation is mapValueAnnotation in the TS source: `x.get(k)`
// where x's DECLARED type node spells `Map<K, V>` with V a readable
// annotation: the read answers V's stated set, or absent (a get can
// miss). tsc's own generic discipline is what makes every stored
// value wear V at its write site, so the claim carries LIBRARY grade.
// The type NODE is read rather than the resolved type because
// instantiation erases the alias the annotation reader needs. The
// receiver is whatever the checker resolves to a declaration spelling
// that node — a name, `this.cache`, or a longer chain all read the
// same way.
func MapValueAnnotation(ctx *FlowContext, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return nil
	}
	pa := call.Expression.AsPropertyAccessExpression()
	var argCount int
	if call.Arguments != nil {
		argCount = len(call.Arguments.Nodes)
	}
	if pa.Name().Text() != "get" || argCount != 1 {
		return nil
	}
	typeNode := declaredTypeNodeOfReceiver(ctx, pa.Expression)
	if typeNode == nil || !ast.IsTypeReferenceNode(typeNode) {
		return nil
	}
	typeRef := typeNode.AsTypeReferenceNode()
	if !ast.IsIdentifier(typeRef.TypeName) {
		return nil
	}
	name := typeRef.TypeName.Text()
	if (name != "Map" && name != "ReadonlyMap") || typeRef.TypeArguments == nil || len(typeRef.TypeArguments.Nodes) != 2 {
		return nil
	}
	read := annotations.AnnotationOfType(ctx.P, typeRef.TypeArguments.Nodes[1], ctx.Registry, ctx.Objects)
	if read.Stated == nil || read.Unsupported != "" {
		return nil
	}
	if read.Stated.Kind != annotations.DeclaredSet {
		return nil
	}
	out := abstractdomain.PossiblyUndefined(
		abstractdomain.AtTrustLevel(
			abstractdomain.KnownSet(*read.Stated.Set, read.Stated.Temporal, abstractdomain.TrustProved, setKindTagOf(read.Stated.KindTag)),
			abstractdomain.TrustLibrary,
		),
		"", false, false,
	)
	return &out
}

// ReturnTypeGround is returnTypeGround in the TS source: the sort's
// ground read from a call's RETURN TYPE, where tsc states one
// cleanly: a LITERAL part answers its exact word or number (a literal
// union answers the union of its words — reading `"postgres" |
// "mongo"` as all strings refuted honest Map.get results at their own
// stated positions), a general part its whole ground — with `|
// undefined`/`| null` wrapping the maybe. A union MIXING sorts
// (`string | number`, a string word beside a boolean) answers the
// sort union of the parts' own grounds, each arm wearing what its own
// part states.
//
// A part naming NO scalar sort — a record, a Date, an array, a
// collection — is read through the resolved-type reader instead, which
// is the layer that already spells constructed sorts. An object type
// answers an INCOMPLETE object there: the shape is present, and no key
// claim is made beyond the members that reader itself read. So the
// arms below can mix a scalar ground with a constructed one, the way a
// `string | Point` return genuinely does. Nil only where a part names
// no scalar sort AND the type reader cannot spell it either — then
// that part is the unknown, and a union with the unknown IS the
// unknown.
//
// A GENERATOR / iterable return type never reaches here: the callers
// answer it before this reader runs (unmodeled_call_result.go's
// iterator row, and the generator route in
// evaluate_call_expression.go). The reason is that the type reader
// WOULD spell it — as an incomplete record of its `next`, `return` and
// `throw` members — and that record is a true claim about a value no
// caller reads that way. What callers read is what the iterator HANDS
// OVER, which is the element, and the element is read through the yield
// walk and the drain routes instead (generator_element.go).
func ReturnTypeGround(ctx *FlowContext, e *ast.Node) *abstractdomain.AbstractValue {
	return typeGroundOf(ctx, typereading.TypeAtLocation(ctx.P.Checker, e), e)
}

// DeclaredReturnTypeGround reads the SAME ground ReturnTypeGround does,
// but off a callee's own DECLARATION rather than a call's resolved
// type at one call site — the reading RecursionMarker needs: an
// in-flight recursive call has no settled call-site type yet (the
// call it names is the very one still being walked), but the callee's
// own signature is fixed the moment the declaration exists. tsc
// already checked the body against this signature, so the ground
// carries the same LIBRARY provenance typeGroundOf stamps on every
// other reading. Nil wherever the declaration carries no resolvable
// signature (a destructuring parameter list the checker cannot place,
// e.g.) or the return type names nothing typeGroundOf can spell —
// the same "nothing sound to say" nil every other caller of
// typeGroundOf already answers.
func DeclaredReturnTypeGround(ctx *FlowContext, declaration *ast.Node) *abstractdomain.AbstractValue {
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil || declaration == nil {
		return nil
	}
	signature := ctx.P.Checker.GetSignatureFromDeclaration(declaration)
	if signature == nil {
		return nil
	}
	returnType := ctx.P.Checker.GetReturnTypeOfSignature(signature)
	if returnType == nil {
		return nil
	}
	return typeGroundOf(ctx, returnType, declaration)
}

// typeGroundOf is ReturnTypeGround's part-walk over an ALREADY-HELD
// type — the same reading, callable where the type comes from
// somewhere other than a call's own location (a checked position's
// static type). `e` anchors the resolved-type reader's constructed
// arms.
func typeGroundOf(ctx *FlowContext, t *checker.Type, e *ast.Node) *abstractdomain.AbstractValue {
	parts := typePartsOf(t)
	sawAbsent := false
	var words []string
	var numberWords []float64
	// the constructed-sort arms, in the order their parts were read —
	// each one whatever the resolved-type reader spelled for that part
	var constructed []abstractdomain.AbstractValue
	// the GENERAL sorts the parts name, each at most once — a `string |
	// number` return names two, and both grounds hold
	generals := map[string]bool{}
	for _, part := range parts {
		if (part.Flags() & absentTypeFlags) != 0 {
			sawAbsent = true
			continue
		}
		if part.IsStringLiteral() {
			// tsgo holds a string literal's value as a plain `string`
			// (checker/types.go:885), so this assertion does match — but a
			// value the checker never pinned reads as nil, and dropping the
			// ok would append "" as though the return type stated the empty
			// word. An unpinned literal is a part nothing spells, which
			// leaves the whole ground nothing, the way the number branch
			// below already does.
			v, ok := part.AsLiteralType().Value().(string)
			if !ok {
				return nil
			}
			words = append(words, v)
			continue
		}
		if part.IsNumberLiteral() {
			// the word is read through numberLiteralValue: tsgo holds a
			// number literal's value as jsnum.Number, a NAMED float64, so a
			// bare .(float64) assertion never matches and the word would
			// read as 0 — an exact number the return type never states. A
			// value that reader does not spell leaves the whole ground
			// nothing, the way any unspellable part does.
			v, ok := numberLiteralValue(part.AsLiteralType().Value())
			if !ok {
				return nil
			}
			numberWords = append(numberWords, v)
			continue
		}
		var sort string
		switch {
		case (part.Flags() & checker.TypeFlagsNumberLike) != 0:
			sort = "number"
		case (part.Flags() & checker.TypeFlagsStringLike) != 0:
			sort = "string"
		case (part.Flags() & checker.TypeFlagsBooleanLike) != 0:
			sort = "boolean"
		}
		if sort == "" {
			// one part names no scalar sort — the resolved-type reader is
			// the layer that spells the constructed ones, so the part is
			// asked there. A part it cannot spell is the unknown, and the
			// union of anything with the unknown IS the unknown, so the
			// whole ground is nothing.
			shape, ok := typereading.ReadHostType(ctx.P.Checker, part, e, 0)
			if !ok || shape.Kind == abstractdomain.KindUnknown {
				return nil
			}
			constructed = append(constructed, shape)
			continue
		}
		generals[sort] = true
	}
	// the arms, in the order a spelled union reads: strings, then
	// numbers, then booleans. A literal beside its OWN general sort
	// folds into that general ground (the general already admits the
	// word); a literal beside a DIFFERENT sort is its own arm.
	var arms []abstractdomain.AbstractValue
	if generals["string"] {
		arms = append(arms, abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	} else if len(words) > 0 {
		set := refinementsets.StringTuple(words[0])
		for _, w := range words[1:] {
			set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(w)))
		}
		arms = append(arms, abstractdomain.KnownSet(set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
	}
	if generals["number"] {
		// the whole number ground is R-bar (refinementsets.Numbers, the
		// -infinity ray) -- never the bare RefinedSet{} zero value, which
		// is the untyped root that "holds every tuple" of every sort
		// (refinement_forms.go's OnOneTupleLayer doc) and so cannot answer
		// a scalar kernel question at all. A `number` return type states a
		// real, 1-tuple-shaped ground the same way z.number() does.
		arms = append(arms, abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.Numbers, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)))
	} else if len(numberWords) > 0 {
		arms = append(arms, abstractdomain.KnownValues(numberWords, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved))
	}
	if generals["boolean"] {
		arms = append(arms, abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved))
	}
	// the constructed arms last — a record or a Date is not a scalar
	// ground, so it reads after the scalar ones the same way a spelled
	// union puts `| Point` after `string | number`
	arms = append(arms, constructed...)
	if len(arms) == 0 {
		return nil
	}
	// one arm IS the ground; two or more are the sort union, which
	// KindUnionOf builds (and collapses back to the single arm itself
	// when only one survives)
	united := abstractdomain.KindUnionOf(arms)
	if united.Kind == abstractdomain.KindUnknown {
		return nil
	}
	// every ground this function hands back is read from a DECLARATION
	// tsc already checked — a resolved call/tag return type, or (through
	// typeGroundOf's other caller, static_type_within.go) a checked
	// position's static type. That is a claim with provenance, the same
	// standing unmodeled_call_result.go's opaqueWorn already stamped by
	// hand at ONE of its call sites (`AtTrustLevel(*ground,
	// TrustLibrary)`) — moved here so EVERY caller carries it, not only
	// the one that remembered to ask. Library grade, never proved: the
	// claim rests on tsc's own checking of the declaration, not on a
	// kernel-proved derivation from a value this walk actually held.
	// AtTrustLevel only ever lowers (MinTrustLevel), so a part that
	// somehow already carried a weaker grade keeps it.
	graded := abstractdomain.AtTrustLevel(united, abstractdomain.TrustLibrary)
	ground := &graded
	if sawAbsent {
		out := abstractdomain.PossiblyUndefined(*ground, "", false, false)
		return &out
	}
	return ground
}

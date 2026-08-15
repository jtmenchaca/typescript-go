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
	t := ctx.P.Checker.GetTypeAtLocation(e)
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
func ReturnTypeGround(ctx *FlowContext, e *ast.Node) *abstractdomain.AbstractValue {
	t := ctx.P.Checker.GetTypeAtLocation(e)
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
			v, _ := part.AsLiteralType().Value().(string)
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
		arms = append(arms, abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.RefinedSet{}, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)))
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
	ground := &united
	if sawAbsent {
		out := abstractdomain.PossiblyUndefined(*ground, "", false, false)
		return &out
	}
	return ground
}

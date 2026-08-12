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
// parts split absence off (`| undefined` / `| null`), and every
// present part must reach a type-alias declaration whose spelling the
// annotation reader reads (`z.infer<typeof X>` and kin). The claim is
// the declaration's — library grade — and nil anywhere a part
// resolves to nothing readable.
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

// MapValueAnnotation is mapValueAnnotation in the TS source: `x.get(k)`
// where x's DECLARED type node spells `Map<K, V>` with V a readable
// annotation: the read answers V's stated set, or absent (a get can
// miss). tsc's own generic discipline is what makes every stored
// value wear V at its write site, so the claim carries LIBRARY grade.
// The type NODE is read rather than the resolved type because
// instantiation erases the alias the annotation reader needs.
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
	receiver := pa.Expression
	if !ast.IsIdentifier(receiver) {
		return nil
	}
	symbol := ctx.P.Checker.GetSymbolAtLocation(receiver)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	var typeNode *ast.Node
	if ast.IsParameterDeclaration(declaration) {
		typeNode = declaration.AsParameterDeclaration().Type
	} else if ast.IsVariableDeclaration(declaration) {
		typeNode = declaration.AsVariableDeclaration().Type
	}
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
// undefined`/`| null` wrapping the maybe. Nil where the type names no
// scalar sort or mixes two sorts.
func ReturnTypeGround(ctx *FlowContext, e *ast.Node) *abstractdomain.AbstractValue {
	t := ctx.P.Checker.GetTypeAtLocation(e)
	parts := typePartsOf(t)
	sawAbsent := false
	var words []string
	var numberWords []float64
	general := ""
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
			v, _ := part.AsLiteralType().Value().(float64)
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
			return nil
		}
		if general != "" && general != sort {
			return nil
		}
		general = sort
	}
	// a literal beside a DIFFERENT sort (string | 0) is the union
	// machinery's row, not this one's; a literal beside its own general
	// sort folds into the ground
	if len(words) > 0 && (len(numberWords) > 0 || general == "number" || general == "boolean") {
		return nil
	}
	if len(numberWords) > 0 && (general == "string" || general == "boolean") {
		return nil
	}
	var ground *abstractdomain.AbstractValue
	switch {
	case general == "number":
		v := abstractdomain.PossiblyNaN(abstractdomain.KnownSet(refinementsets.RefinedSet{}, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone))
		ground = &v
	case general == "string":
		v := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
		ground = &v
	case general == "boolean":
		v := abstractdomain.KnownValues([]float64{0, 1}, abstractdomain.PrimitiveBoolean, abstractdomain.TrustProved)
		ground = &v
	case len(words) > 0:
		set := refinementsets.StringTuple(words[0])
		for _, w := range words[1:] {
			set = refinementsets.MakeRefinedSet(refinementsets.Union(set, refinementsets.StringTuple(w)))
		}
		v := abstractdomain.KnownSet(set, nil, abstractdomain.TrustProved, abstractdomain.SetKindTagNone)
		ground = &v
	case len(numberWords) > 0:
		v := abstractdomain.KnownValues(numberWords, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
		ground = &v
	}
	if ground == nil {
		return nil
	}
	if sawAbsent {
		out := abstractdomain.PossiblyUndefined(*ground, "", false, false)
		return &out
	}
	return ground
}

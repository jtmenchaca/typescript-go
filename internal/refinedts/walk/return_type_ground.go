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
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
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
		tracing.CountBy("host.resolvedSignature", 1)
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
	tracing.CountBy("host.symbolAtLocation", 1)
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

// mapValueTypeNodeOfReceiver is the type node spelling V for a `.get()`
// receiver that is a Map — the node MapValueAnnotation reads its stated
// set from.
//
// Three ways a receiver states V, and the last two are what a map with
// no written-out annotation has. Written out, the declaration spells
// `Map<K, V>` and V is its second type argument. Inferred from its own
// construction — `const m = new Map(source)` — the declaration spells
// nothing, and V comes from what the constructor was HANDED:
// AddEntriesFromIterable
// reads each item's "1" and sets it as the value
// (sec-add-entries-from-iterable), so every value in the built map is
// one of the source's second components, and the source's own declared
// element type states which. That is the same generic discipline the
// written-out case leans on, read one step earlier — at the
// construction rather than at the annotation the construction would
// have been assigned to.
//
// The source shapes read here are the ones that spell a value type
// syntactically: an array of PAIRS (`[K, V][]`, whose element is a
// two-slot tuple), and `Object.entries(record)` over a record whose own
// declared type states its value (`Record<K, V>`). And CLONED from
// another map — `const cloned = structuredClone(m)` — where the clone
// wears the original's type and the question moves to the original.
//
// A source that states no element type answers nil, and the read falls
// back the way an unresolvable receiver always did.
func mapValueTypeNodeOfReceiver(ctx *FlowContext, receiver *ast.Node) *ast.Node {
	if typeNode := declaredTypeNodeOfReceiver(ctx, receiver); typeNode != nil {
		return mapValueTypeArgument(typeNode)
	}
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := ctx.P.Checker.GetSymbolAtLocation(receiver)
	if symbol == nil || symbol.ValueDeclaration == nil {
		return nil
	}
	declaration := symbol.ValueDeclaration
	if !ast.IsVariableDeclaration(declaration) {
		return nil
	}
	initializer := declaration.AsVariableDeclaration().Initializer
	if initializer == nil {
		return nil
	}
	// a CLONE states the original's value type. structuredClone is
	// declared `structuredClone<T = any>(value: T, …): T`, and the
	// algorithm rebuilds a Map by re-setting the original's entries into
	// a fresh one — same values, fresh identity — so a clone of a
	// `Map<K, V>` is a `Map<K, V>` and the question moves to the
	// original's own receiver (structured_clone_model.go carries the
	// contents transfer; this carries the stated value type, which is
	// what a receiver the walk built no entries for still has).
	if ast.IsCallExpression(initializer) {
		call := initializer.AsCallExpression()
		if ast.IsIdentifier(call.Expression) && call.Expression.Text() == "structuredClone" &&
			resolvesToDefaultLib(ctx, call.Expression) &&
			call.Arguments != nil && len(call.Arguments.Nodes) >= 1 {
			return mapValueTypeNodeOfReceiver(ctx, call.Arguments.Nodes[0])
		}
		return nil
	}
	if !ast.IsNewExpression(initializer) {
		return nil
	}
	newExpr := initializer.AsNewExpression()
	if !ast.IsIdentifier(newExpr.Expression) ||
		(newExpr.Expression.Text() != "Map" && newExpr.Expression.Text() != "WeakMap") ||
		!resolvesToDefaultLib(ctx, newExpr.Expression) {
		return nil
	}
	// `new Map<K, V>(…)` / `new WeakMap<K, V>(…)` spells V at the
	// construction itself
	if newExpr.TypeArguments != nil && len(newExpr.TypeArguments.Nodes) == 2 {
		return newExpr.TypeArguments.Nodes[1]
	}
	if newExpr.Arguments == nil || len(newExpr.Arguments.Nodes) != 1 {
		return nil
	}
	return mapValueTypeNodeOfSource(ctx, newExpr.Arguments.Nodes[0])
}

// mapValueTypeArgument is V from a type node spelling `Map<K, V>`, and
// nil from anything else.
func mapValueTypeArgument(typeNode *ast.Node) *ast.Node {
	if !ast.IsTypeReferenceNode(typeNode) {
		return nil
	}
	typeRef := typeNode.AsTypeReferenceNode()
	if !ast.IsIdentifier(typeRef.TypeName) {
		return nil
	}
	name := typeRef.TypeName.Text()
	// WeakMap states the same two-argument `<K, V>` discipline: its get
	// walks [[WeakMapData]] and answers the matching record's [[Value]]
	// (sec-weakmap.prototype.get), so a stated V binds every stored
	// value exactly as Map's does — only the key identity rule differs,
	// and V is not about keys
	if name != "Map" && name != "ReadonlyMap" && name != "WeakMap" {
		return nil
	}
	if typeRef.TypeArguments == nil || len(typeRef.TypeArguments.Nodes) != 2 {
		return nil
	}
	return typeRef.TypeArguments.Nodes[1]
}

// mapValueTypeNodeOfSource is V from the expression a Map constructor
// was handed — the second component of what the source iterates.
//
// `Object.entries(record)` yields `[key, value]` pairs whose value is
// the record's own value type (sec-object.entries walks the record's own
// enumerable string-keyed properties), so a record declared
// `Record<K, V>` states V directly. Any other source states V through
// its own declared element type: an array of pairs `[K, V][]` has a
// two-slot tuple element, and that tuple's second slot is V.
func mapValueTypeNodeOfSource(ctx *FlowContext, source *ast.Node) *ast.Node {
	if ast.IsCallExpression(source) {
		call := source.AsCallExpression()
		if !ast.IsPropertyAccessExpression(call.Expression) {
			return nil
		}
		pa := call.Expression.AsPropertyAccessExpression()
		if !ast.IsIdentifier(pa.Expression) || pa.Expression.Text() != "Object" ||
			pa.Name().Text() != "entries" || call.Arguments == nil || len(call.Arguments.Nodes) != 1 {
			return nil
		}
		recordType := declaredTypeNodeOfReceiver(ctx, call.Arguments.Nodes[0])
		if recordType == nil || !ast.IsTypeReferenceNode(recordType) {
			return nil
		}
		recordRef := recordType.AsTypeReferenceNode()
		if !ast.IsIdentifier(recordRef.TypeName) || recordRef.TypeName.Text() != "Record" ||
			recordRef.TypeArguments == nil || len(recordRef.TypeArguments.Nodes) != 2 {
			return nil
		}
		return recordRef.TypeArguments.Nodes[1]
	}
	elementType := declaredTypeNodeOfReceiver(ctx, source)
	if elementType == nil || !ast.IsArrayTypeNode(elementType) {
		return nil
	}
	pairType := elementType.AsArrayTypeNode().ElementType
	if pairType == nil || !ast.IsTupleTypeNode(pairType) {
		return nil
	}
	elements := pairType.AsTupleTypeNode().Elements
	if elements == nil || len(elements.Nodes) != 2 {
		return nil
	}
	// a labelled slot (`[key: K, value: V]`) states the same type behind
	// its name, so the member's own type is what is read
	value := elements.Nodes[1]
	if ast.IsNamedTupleMember(value) {
		return value.AsNamedTupleMember().Type
	}
	return value
}

// MapValueAnnotation is mapValueAnnotation in the TS source: `x.get(k)`
// where x's receiver states `Map<K, V>` with V a readable annotation:
// the read answers V's stated set, or absent (a get can miss). tsc's own
// generic discipline is what makes every stored value wear V at its
// write site, so the claim carries LIBRARY grade.
// A type NODE is read rather than the resolved type because
// instantiation erases the alias the annotation reader needs, and
// mapValueTypeNodeOfReceiver is what finds the node stating V — from
// the receiver's own declaration where it spells one, and from the
// construction that built it where it does not. The receiver is
// whatever the checker resolves to such a declaration — a name,
// `this.cache`, or a longer chain all read the same way.
//
// keyEstablished drops the absence. `get` returns *undefined* only
// when its walk of [[MapData]] finds no entry whose key SameValue-
// matches (sec-map.prototype.get); `has` walks that same list with
// that same test and returns *true* exactly when one does
// (sec-map.prototype.has). So under a standing `m.has(k)` for the same
// key, the miss branch of `get` is unreachable and the read answers
// V's stated set with nothing absent in it. The caller decides whether
// such a guard stands — KeyPresenceEstablishedFor — because the
// standing is an environment fact and this reader sees only syntax.
// CheckMapValueWrite judges a `m.set(k, v)` WRITE against the value
// type the receiver's own `Map<K, V>` states — the other half of the
// generic discipline MapValueAnnotation reads.
//
// The two sides are one fact. MapValueAnnotation's `get` reading is
// sound only BECAUSE nothing enters a `Map<K, V>` without wearing V at
// its write site; that is tsc's own guarantee for the TYPE, and it is
// exactly what a refinement set adds a real obligation to. `Map<string,
// Age>` states that every stored value is an Age, so `m.set("x", 200)`
// stores a value the declaration forbids — and reading it back later
// through the `get` side would hand out a "proven Age" of 200. Judging
// only the read side and never the write is what let that through: the
// value was evaluated for its effects and then dropped unjudged.
//
// The receiver's value-type node is found the same way the read side
// finds it (mapValueTypeNodeOfReceiver — a written-out annotation, a
// construction's own type argument, or the source a construction was
// handed), so a map with no spelled V states no obligation and nothing
// is judged, exactly as the read side answers nothing for one.
func CheckMapValueWrite(ctx *FlowContext, e *ast.Node, written abstractdomain.AbstractValue, at *ast.Node) {
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return
	}
	pa := call.Expression.AsPropertyAccessExpression()
	if pa.Name().Text() != "set" || call.Arguments == nil || len(call.Arguments.Nodes) != 2 {
		return
	}
	valueTypeNode := mapValueTypeNodeOfReceiver(ctx, pa.Expression)
	if valueTypeNode == nil {
		return
	}
	read := annotations.AnnotationOfType(ctx.P, valueTypeNode, ctx.Registry, ctx.Objects)
	if read.Stated == nil || read.Unsupported != "" {
		return
	}
	CheckAssignability(ctx, written, *read.Stated, at, "the stored value", nil)
}

// MapStatedValueOfGetOrInsert is the getOrInsert side of the one
// `Map<K, V>` fact the two functions around it read and judge: the
// call answers the held value or inserts its default, and BOTH wear
// the receiver's stated V — the held value entered wearing V at its
// own write (CheckMapValueWrite's doc on why the read side rests on
// the write side), and the default is judged against V here, at ITS
// write. Answers V's stated set for the caller to join with the
// default's own value; nil where the receiver spells no V, which is
// also when nothing is judged — the same silence both siblings keep.
func MapStatedValueOfGetOrInsert(ctx *FlowContext, receiverExpression *ast.Node,
	inserted abstractdomain.AbstractValue, at *ast.Node) *abstractdomain.AbstractValue {
	if receiverExpression == nil {
		return nil
	}
	valueTypeNode := mapValueTypeNodeOfReceiver(ctx, receiverExpression)
	if valueTypeNode == nil {
		return nil
	}
	read := annotations.AnnotationOfType(ctx.P, valueTypeNode, ctx.Registry, ctx.Objects)
	if read.Stated == nil || read.Unsupported != "" || read.Stated.Kind != annotations.DeclaredSet {
		return nil
	}
	CheckAssignability(ctx, inserted, *read.Stated, at, "the stored value", nil)
	stated := abstractdomain.AtTrustLevel(
		abstractdomain.KnownSet(*read.Stated.Set, read.Stated.Temporal, abstractdomain.TrustProved, setKindTagOf(read.Stated.KindTag)),
		abstractdomain.TrustLibrary,
	)
	return &stated
}

func MapValueAnnotation(ctx *FlowContext, e *ast.Node, keyEstablished bool) *abstractdomain.AbstractValue {
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
	valueTypeNode := mapValueTypeNodeOfReceiver(ctx, pa.Expression)
	if valueTypeNode == nil {
		return nil
	}
	read := annotations.AnnotationOfType(ctx.P, valueTypeNode, ctx.Registry, ctx.Objects)
	if read.Stated == nil || read.Unsupported != "" {
		return nil
	}
	if read.Stated.Kind != annotations.DeclaredSet {
		return nil
	}
	stated := abstractdomain.AtTrustLevel(
		abstractdomain.KnownSet(*read.Stated.Set, read.Stated.Temporal, abstractdomain.TrustProved, setKindTagOf(read.Stated.KindTag)),
		abstractdomain.TrustLibrary,
	)
	if keyEstablished {
		// the guard proved the entry present, so `get`'s undefined branch
		// never runs — V's stated set is the whole answer
		return &stated
	}
	out := abstractdomain.PossiblyUndefined(stated, "", false, false)
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
	tracing.CountBy("host.signatureFromDeclaration", 1)
	signature := ctx.P.Checker.GetSignatureFromDeclaration(declaration)
	if signature == nil {
		return nil
	}
	tracing.CountBy("host.returnTypeOfSignature", 1)
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

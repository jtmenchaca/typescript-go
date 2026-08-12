// from annotations/library_adapters/zod/parse_evaluator.ts
//
// Exact parse evaluation: a zod schema read as the FUNCTION it is.
// Where the `.parse` argument is exactly known, the schema pipeline
// runs at check time — coercions map, checks decide, `.transform`
// callbacks inline through the flow walker — and the result is the
// exact value the runtime will produce. Anything outside the modeled
// vocabulary answers nil (no claim), never a guess.
//
// TRUST: these rows mirror ZOD's runtime, not the ECMA spec — the
// library-oracle boundary agreed for this libraryAdapter. Every modeled
// behavior is verified against zod 4.4.3 (hover-lab/
// zod-semantics-probe.mjs, hover-lab/stringbool-probe.mjs) and the
// vendored source (tmp/zod-src/packages/zod/src/v4/core/api.ts —
// stringbool defaults verbatim). Version drift lands here and in
// the adapter's test file, never in the core.
//
// This file joins package walk rather than living beside its TS home
// (annotations/library_adapters/zod/): it calls InlineCallback, the
// walk-package callback inliner, and PORT.md's two-way-cycle rule
// pulls the whole file to the higher side of that boundary the way
// worn_annotation.go already does.
package walk

import (
	"math"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/jsnum"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// TransformCallback is TransformCallback in the TS source: a callback a
// `.transform` hands over. Callback (ArrowFunction | FunctionExpression
// | FunctionDeclaration) is already the walk package's alias for
// `*ast.Node`; the TS type is narrower (no FunctionDeclaration), which
// this port does not re-narrow — every call site here only ever hands
// it an arrow or function expression, per callbackOf below.
type TransformCallback = *ast.Node

// ParseEvalTools is ParseEvalTools in the TS source: the three
// questions evaluateParseOutcome asks its caller — const resolution,
// zod-root recognition, and callback inlining — kept as an interface
// so the caller (schema_runtime_models.go) supplies them against
// *FlowContext without this file importing anything above walk.
type ParseEvalTools struct {
	// ResolveConst is a const identifier's initializer, or nil.
	ResolveConst func(id *ast.Node) *ast.Node
	// IsZodRoot reports whether this identifier resolves into the zod
	// package.
	IsZodRoot func(id *ast.Node) bool
	// Inline runs a transform callback with the argument's knowledge
	// bound — the flow walker's closure inlining.
	Inline func(callback TransformCallback, argument abstractdomain.AbstractValue) abstractdomain.AbstractValue
}

// outcomeKind is the Outcome union's Kind tag in the TS source
// (`{ kind: "value" } | { kind: "throws" } | { kind: "terminal" }`,
// with the whole union also admitting null).
type outcomeKind string

const (
	outcomeKindValue    outcomeKind = "value"
	outcomeKindThrows   outcomeKind = "throws"
	outcomeKindTerminal outcomeKind = "terminal"
)

// outcome is the Outcome type in the TS source: the evaluation
// outcome — a value, a proven runtime THROW (which a union may
// recover from), a terminal inexact knowledge (a transform callback
// said less than a value), or (outcome{}, false) — outside the
// modeled vocabulary, no claim. The (T, bool) pair stands in for the
// TS source's `| null` per the port convention.
type outcome struct {
	kind  outcomeKind
	value JsValue
	known abstractdomain.AbstractValue
}

func ok(value JsValue) (outcome, bool) {
	return outcome{kind: outcomeKindValue, value: value}, true
}

var throwsOutcome = outcome{kind: outcomeKindThrows}

// ParseOutcome is the exported result shape of EvaluateParseOutcome:
// the TS source's inline return type
// `{ kind: "value"; known } | { kind: "throws" }`, plus (ParseOutcome{},
// false) for null (the port convention's (T, bool) pair).
type ParseOutcome struct {
	Kind  outcomeKind // outcomeKindValue or outcomeKindThrows
	Known abstractdomain.AbstractValue
}

// EvaluateParseOutcome is evaluateParseOutcome in the TS source:
// evaluate a schema application exactly, PROVEN THROWS included —
// `.parse` treats a throw as no-claim (the value never lands), and
// `.safeParse` reifies it as {success: false}. (ParseOutcome{}, false)
// = no claim.
func EvaluateParseOutcome(schema *ast.Node, argument abstractdomain.AbstractValue, tools ParseEvalTools) (ParseOutcome, bool) {
	value := JsValueExact(argument, nil)
	if value == nil {
		return ParseOutcome{}, false
	}
	result, has := run(schema, value, tools)
	if !has {
		return ParseOutcome{}, false
	}
	if result.kind == outcomeKindThrows {
		return ParseOutcome{Kind: outcomeKindThrows}, true
	}
	if result.kind == outcomeKindTerminal {
		return ParseOutcome{Kind: outcomeKindValue, Known: result.known}, true
	}
	return ParseOutcome{Kind: outcomeKindValue, Known: AbstractValueOfJs(result.value, abstractdomain.TrustProved)}, true
}

// EvaluateParse is evaluateParse in the TS source: evaluate
// `schema.parse(argument)` exactly; (AbstractValue{}, false) = no
// claim.
func EvaluateParse(schema *ast.Node, argument abstractdomain.AbstractValue, tools ParseEvalTools) (abstractdomain.AbstractValue, bool) {
	result, has := EvaluateParseOutcome(schema, argument, tools)
	if has && result.Kind == outcomeKindValue {
		return result.Known, true
	}
	return abstractdomain.AbstractValue{}, false
}

// stringbool's default vocabulary — zod v4 core api.ts, verbatim;
// comparison lowercases by default
var stringboolTruthy = map[string]bool{
	"true": true, "1": true, "yes": true, "on": true, "y": true, "enabled": true,
}
var stringboolFalsy = map[string]bool{
	"false": true, "0": true, "no": true, "off": true, "n": true, "disabled": true,
}

// unwrap is the TS source's inline `unwrap`: strip parenthesization.
func unwrap(e *ast.Node) *ast.Node {
	for ast.IsParenthesizedExpression(e) {
		e = e.AsParenthesizedExpression().Expression
	}
	return e
}

// literalOf is literalOf in the TS source: a literal argument — number
// (with minus), string. Carried as `any` holding float64 or string,
// the same shapes JsValue names; (nil, false) for neither.
func literalOf(e *ast.Node) (any, bool) {
	bare := unwrap(e)
	if ast.IsNumericLiteral(bare) {
		return float64(jsnum.FromString(bare.AsNumericLiteral().Text)), true
	}
	if ast.IsPrefixUnaryExpression(bare) {
		unary := bare.AsPrefixUnaryExpression()
		if unary.Operator == ast.KindMinusToken && ast.IsNumericLiteral(unary.Operand) {
			return -float64(jsnum.FromString(unary.Operand.AsNumericLiteral().Text)), true
		}
	}
	if ast.IsStringLiteral(bare) {
		return bare.AsStringLiteral().Text, true
	}
	if ast.IsNoSubstitutionTemplateLiteral(bare) {
		return bare.AsNoSubstitutionTemplateLiteral().Text, true
	}
	return nil, false
}

// callbackOf is callbackOf in the TS source.
func callbackOf(e *ast.Node, tools ParseEvalTools) (TransformCallback, bool) {
	bare := unwrap(e)
	if ast.IsArrowFunction(bare) || ast.IsFunctionExpression(bare) {
		return bare, true
	}
	if ast.IsIdentifier(bare) {
		initializer := tools.ResolveConst(bare)
		if initializer != nil {
			return callbackOf(initializer, tools)
		}
	}
	return nil, false
}

// run is run in the TS source.
func run(expr *ast.Node, value JsValue, tools ParseEvalTools) (outcome, bool) {
	e := unwrap(expr)
	if ast.IsIdentifier(e) {
		initializer := tools.ResolveConst(e)
		if initializer == nil {
			return outcome{}, false
		}
		return run(initializer, value, tools)
	}
	if !ast.IsCallExpression(e) {
		return outcome{}, false
	}
	call := e.AsCallExpression()
	if !ast.IsPropertyAccessExpression(call.Expression) {
		return outcome{}, false
	}
	propertyAccess := call.Expression.AsPropertyAccessExpression()
	receiver := propertyAccess.Expression
	method := propertyAccess.Name().Text()
	var args []*ast.Node
	if call.Arguments != nil {
		args = call.Arguments.Nodes
	}

	// ── the roots: z.<name>(...) ────────────────────────────────────
	if ast.IsIdentifier(receiver) && tools.IsZodRoot(receiver) {
		return runRoot(method, args, value, tools)
	}
	// z.coerce.number(): Number(input), NaN refused (verified — "" is
	// 0, true is 1, "abc" throws)
	if ast.IsPropertyAccessExpression(receiver) {
		receiverAccess := receiver.AsPropertyAccessExpression()
		if ast.IsIdentifier(receiverAccess.Expression) && tools.IsZodRoot(receiverAccess.Expression) &&
			receiverAccess.Name().Text() == "coerce" && method == "number" && len(args) == 0 {
			if value == absentMarker {
				return outcome{}, false // null vs undefined coerce apart
			}
			switch value.(type) {
			case float64, string, bool:
				coerced := jsNumberOf(value)
				if math.IsNaN(coerced) {
					return throwsOutcome, true
				}
				return ok(coerced)
			default:
				return outcome{}, false
			}
		}
	}

	// ── the wrappers that see the RAW value before their inner ──────
	if (method == "optional" || method == "nullish") && len(args) == 0 {
		// absent passes through unmapped; the conflated marker cannot
		// tell undefined (optional's whole domain) from null, so only
		// nullish decides it
		if value == absentMarker {
			if method == "nullish" {
				return ok(absentMarker)
			}
			return outcome{}, false
		}
		return run(receiver, value, tools)
	}

	// ── the chain: evaluate the inner schema first ──────────────────
	inner, has := run(receiver, value, tools)
	if !has || inner.kind == outcomeKindThrows {
		return inner, has
	}
	if inner.kind == outcomeKindTerminal {
		// only a transform may follow an inexact value
		if method == "transform" && len(args) == 1 {
			callback, hasCallback := callbackOf(args[0], tools)
			if !hasCallback {
				return outcome{}, false
			}
			return outcome{kind: outcomeKindTerminal, known: tools.Inline(callback, inner.known)}, true
		}
		return outcome{}, false
	}
	current := inner.value

	switch method {
	case "transform":
		if len(args) != 1 {
			return outcome{}, false
		}
		callback, hasCallback := callbackOf(args[0], tools)
		if !hasCallback {
			return outcome{}, false
		}
		result := tools.Inline(callback, AbstractValueOfJs(current, abstractdomain.TrustProved))
		asValue := JsValueExact(result, nil)
		if asValue == nil {
			return outcome{kind: outcomeKindTerminal, known: result}, true
		}
		return ok(asValue)
	case "pipe":
		if len(args) == 1 {
			return run(args[0], current, tools)
		}
		return outcome{}, false
	case "readonly":
		return ok(current)
	case "describe", "meta", "brand":
		// metadata only — the value passes unchanged (vendored
		// classic/schemas.ts)
		return ok(current)
	case "trim":
		if s, isString := current.(string); isString {
			return ok(strings.TrimSpace(s))
		}
		return outcome{}, false
	case "toUpperCase":
		if s, isString := current.(string); isString {
			return ok(strings.ToUpper(s))
		}
		return outcome{}, false
	case "toLowerCase":
		if s, isString := current.(string); isString {
			return ok(strings.ToLower(s))
		}
		return outcome{}, false
	case "refine":
		// accepted exactly when the predicate returns truthy —
		// vendored core/schemas.ts: $ZodCustom runs def.fn and
		// handleRefineResult files an issue on a falsy result. The
		// walker evaluates the callback on the exact value; only a
		// decided boolean or number word decides the outcome.
		if len(args) < 1 || len(args) > 2 {
			return outcome{}, false
		}
		callback, hasCallback := callbackOf(args[0], tools)
		if !hasCallback {
			return outcome{}, false
		}
		verdict := tools.Inline(callback, AbstractValueOfJs(current, abstractdomain.TrustProved))
		if verdict.Kind == abstractdomain.KindValues && len(verdict.Values) == 1 &&
			(verdict.KindTag == abstractdomain.PrimitiveBoolean || verdict.KindTag == abstractdomain.PrimitiveNumber) {
			if verdict.Values[0] != 0 {
				return ok(current)
			}
			return throwsOutcome, true
		}
		return outcome{}, false
	default:
		return runCheck(method, args, current)
	}
}

// jsNumberOf mirrors JS's Number(value) for the number/string/boolean
// value this call site already gated to — the string case reuses
// coercion_models.go's jsStringToNumber (the same ECMA StringToNumber
// grammar z.coerce already needs) rather than a second hand-rolled
// parse.
func jsNumberOf(value JsValue) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case bool:
		if v {
			return 1
		}
		return 0
	case string:
		n, ok := jsStringToNumber(v)
		if !ok {
			return math.NaN()
		}
		return n
	default:
		return math.NaN()
	}
}

// runRoot is runRoot in the TS source.
func runRoot(name string, args []*ast.Node, value JsValue, tools ParseEvalTools) (outcome, bool) {
	switch name {
	case "number":
		if n, isNumber := value.(float64); isNumber && !math.IsNaN(n) {
			return ok(n)
		}
		if _, isNumber := value.(float64); isNumber || value == absentMarker {
			return throwsOutcome, true
		}
		return sortThrow(value)
	case "int":
		if n, isNumber := value.(float64); isNumber && isInteger(n) && math.Abs(n) <= 9007199254740991 {
			return ok(n)
		}
		return sortThrow(value)
	case "int32":
		if n, isNumber := value.(float64); isNumber && isInteger(n) && n >= -(1<<31) && n <= (1<<31)-1 {
			return ok(n)
		}
		return sortThrow(value)
	case "float64":
		if n, isNumber := value.(float64); isNumber && !math.IsInf(n, 0) && !math.IsNaN(n) {
			return ok(n)
		}
		return sortThrow(value)
	case "nan":
		if n, isNumber := value.(float64); isNumber && math.IsNaN(n) {
			return ok(n)
		}
		return sortThrow(value)
	case "string":
		if s, isString := value.(string); isString {
			return ok(s)
		}
		return sortThrow(value)
	case "boolean":
		if b, isBool := value.(bool); isBool {
			return ok(b)
		}
		return sortThrow(value)
	case "unknown":
		return ok(value)
	case "literal":
		if len(args) != 1 {
			return outcome{}, false
		}
		expected, hasExpected := literalOf(args[0])
		if !hasExpected {
			return outcome{}, false
		}
		if jsValuesEqual(value, expected) {
			return ok(value)
		}
		return sortThrow(value)
	case "stringbool":
		if len(args) != 0 {
			return outcome{}, false // custom vocabularies unread
		}
		s, isString := value.(string)
		if !isString {
			return sortThrow(value)
		}
		lowered := strings.ToLower(s)
		if stringboolTruthy[lowered] {
			return ok(true)
		}
		if stringboolFalsy[lowered] {
			return ok(false)
		}
		return throwsOutcome, true
	case "enum":
		if len(args) != 1 {
			return outcome{}, false
		}
		members, hasMembers := elementsOf(args[0])
		if !hasMembers {
			return outcome{}, false
		}
		literals := make([]any, 0, len(members))
		for _, member := range members {
			literal, hasLiteral := literalOf(member)
			if !hasLiteral {
				return outcome{}, false
			}
			literals = append(literals, literal)
		}
		switch value.(type) {
		case string, float64:
			for _, literal := range literals {
				if jsValuesEqual(value, literal) {
					return ok(value)
				}
			}
			return throwsOutcome, true
		default:
			return sortThrow(value)
		}
	case "tuple":
		if len(args) != 1 {
			return outcome{}, false
		}
		items, hasItems := elementsOf(args[0])
		if !hasItems {
			return outcome{}, false
		}
		values, isArray := value.([]JsValue)
		if !isArray {
			return sortThrow(value)
		}
		if len(values) != len(items) {
			return throwsOutcome, true
		}
		return mapElements(items, values, tools)
	case "array":
		if len(args) != 1 {
			return outcome{}, false
		}
		values, isArray := value.([]JsValue)
		if !isArray {
			return sortThrow(value)
		}
		schemas := make([]*ast.Node, len(values))
		for i := range values {
			schemas[i] = args[0]
		}
		return mapElements(schemas, values, tools)
	case "union":
		if len(args) != 1 {
			return outcome{}, false
		}
		options, hasOptions := elementsOf(args[0])
		if !hasOptions {
			return outcome{}, false
		}
		for _, option := range options {
			optionOutcome, hasOption := run(option, value, tools)
			if !hasOption || optionOutcome.kind == outcomeKindTerminal {
				return outcome{}, false
			}
			if optionOutcome.kind == outcomeKindValue {
				return optionOutcome, true
			}
		}
		return throwsOutcome, true // every member proved a throw
	case "discriminatedUnion":
		// the library rejects duplicate discriminator values at
		// construction (vendored core/schemas.ts), so an option that
		// ACCEPTS the value is exactly the discriminator-selected
		// option — running every option decides the same outcome the
		// lookup would
		if len(args) != 2 {
			return outcome{}, false
		}
		options, hasOptions := elementsOf(args[1])
		if !hasOptions {
			return outcome{}, false
		}
		var accepted *outcome
		for _, option := range options {
			optionOutcome, hasOption := run(option, value, tools)
			if !hasOption || optionOutcome.kind == outcomeKindTerminal {
				return outcome{}, false
			}
			if optionOutcome.kind == outcomeKindValue {
				if accepted != nil {
					return outcome{}, false // never under the gate
				}
				accepted = &optionOutcome
			}
		}
		if accepted != nil {
			return *accepted, true
		}
		return throwsOutcome, true
	case "object", "strictObject":
		if len(args) != 1 {
			return outcome{}, false
		}
		shape := unwrap(args[0])
		if !ast.IsObjectLiteralExpression(shape) {
			return outcome{}, false
		}
		// ABSENT is a private sentinel, so the type switch already
		// excludes it
		input, isObject := value.(map[string]JsValue)
		if !isObject {
			return sortThrow(value)
		}
		output := map[string]JsValue{}
		named := map[string]bool{}
		for _, property := range shape.AsObjectLiteralExpression().Properties.Nodes {
			if !ast.IsPropertyAssignment(property) {
				return outcome{}, false
			}
			assignment := property.AsPropertyAssignment()
			if !ast.IsIdentifier(assignment.Name()) {
				return outcome{}, false
			}
			key := assignment.Name().Text()
			named[key] = true
			held, hasKey := input[key]
			if !hasKey {
				// a missing key survives only an optional/nullish tail
				if hasWrapperTail(assignment.Initializer) {
					continue
				}
				return throwsOutcome, true
			}
			propertyOutcome, hasOutcome := run(assignment.Initializer, held, tools)
			if !hasOutcome || propertyOutcome.kind == outcomeKindTerminal {
				return outcome{}, false
			}
			if propertyOutcome.kind == outcomeKindThrows {
				return throwsOutcome, true
			}
			output[key] = propertyOutcome.value
		}
		for key := range input {
			if named[key] {
				continue
			}
			// z.object STRIPS unknown keys; strictObject throws (verified)
			if name == "strictObject" {
				return throwsOutcome, true
			}
		}
		return ok(output)
	default:
		return outcome{}, false
	}
}

// isInteger (Number.isInteger) already exists in this package —
// number_range.go's isInteger, identical semantics — reused rather
// than duplicated.

// jsValuesEqual compares two JsValue holders the way JS `===` compares
// number/string/boolean primitives — the only shapes literalOf and the
// enum/literal roots ever produce or compare.
func jsValuesEqual(a, b JsValue) bool {
	switch av := a.(type) {
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	default:
		return false
	}
}

// sortThrow is sortThrow in the TS source: a mismatched sort under a
// typed root: zod throws. The conflated absent marker stays undecided
// (null vs undefined differ in some roots), so it answers no-claim
// rather than a proven throw.
func sortThrow(value JsValue) (outcome, bool) {
	if value == absentMarker {
		return outcome{}, false
	}
	return throwsOutcome, true
}

// elementsOf is elementsOf in the TS source.
func elementsOf(e *ast.Node) ([]*ast.Node, bool) {
	bare := unwrap(e)
	if ast.IsAsExpression(bare) {
		bare = unwrap(bare.AsAsExpression().Expression) // as const
	}
	if !ast.IsArrayLiteralExpression(bare) {
		return nil, false
	}
	return bare.AsArrayLiteralExpression().Elements.Nodes, true
}

// mapElements is mapElements in the TS source.
func mapElements(schemas []*ast.Node, values []JsValue, tools ParseEvalTools) (outcome, bool) {
	out := make([]JsValue, 0, len(schemas))
	for i := range schemas {
		elementOutcome, has := run(schemas[i], values[i], tools)
		if !has || elementOutcome.kind == outcomeKindTerminal {
			return outcome{}, false
		}
		if elementOutcome.kind == outcomeKindThrows {
			return throwsOutcome, true
		}
		out = append(out, elementOutcome.value)
	}
	return ok(out)
}

// hasWrapperTail is hasWrapperTail in the TS source: does this key
// schema end in .optional() / .nullish()?
func hasWrapperTail(e *ast.Node) bool {
	bare := unwrap(e)
	if !ast.IsCallExpression(bare) {
		return false
	}
	callee := bare.AsCallExpression().Expression
	if !ast.IsPropertyAccessExpression(callee) {
		return false
	}
	name := callee.AsPropertyAccessExpression().Name().Text()
	return name == "optional" || name == "nullish"
}

// runCheck is runCheck in the TS source: the check methods: decide on
// the exact current value, mirroring zod's rules — string bounds count
// UTF-16 units (the host's own .length), array bounds count elements,
// number bounds compare.
func runCheck(method string, args []*ast.Node, value JsValue) (outcome, bool) {
	var literal any
	hasLiteral := false
	if len(args) == 1 {
		literal, hasLiteral = literalOf(args[0])
	}
	k, kIsNumber := literal.(float64)
	kIsNumber = kIsNumber && hasLiteral

	var sizeOf int
	hasSizeOf := false
	switch v := value.(type) {
	case string:
		sizeOf, hasSizeOf = utf16Length(v), true
	case []JsValue:
		sizeOf, hasSizeOf = len(v), true
	}

	pass := func(held bool) (outcome, bool) {
		if held {
			return ok(value)
		}
		return throwsOutcome, true
	}

	switch method {
	case "min", "gte":
		if !kIsNumber {
			return outcome{}, false
		}
		if n, isNumber := value.(float64); isNumber {
			return pass(n >= k)
		}
		if !hasSizeOf {
			return outcome{}, false
		}
		return pass(float64(sizeOf) >= k)
	case "max", "lte":
		if !kIsNumber {
			return outcome{}, false
		}
		if n, isNumber := value.(float64); isNumber {
			return pass(n <= k)
		}
		if !hasSizeOf {
			return outcome{}, false
		}
		return pass(float64(sizeOf) <= k)
	case "gt":
		if n, isNumber := value.(float64); kIsNumber && isNumber {
			return pass(n > k)
		}
		return outcome{}, false
	case "lt":
		if n, isNumber := value.(float64); kIsNumber && isNumber {
			return pass(n < k)
		}
		return outcome{}, false
	case "length":
		if kIsNumber && hasSizeOf {
			return pass(float64(sizeOf) == k)
		}
		return outcome{}, false
	case "nonempty":
		if hasSizeOf {
			return pass(sizeOf >= 1)
		}
		return outcome{}, false
	case "int":
		if n, isNumber := value.(float64); isNumber {
			return pass(isInteger(n))
		}
		return outcome{}, false
	case "positive":
		if n, isNumber := value.(float64); isNumber {
			return pass(n > 0)
		}
		return outcome{}, false
	case "negative":
		if n, isNumber := value.(float64); isNumber {
			return pass(n < 0)
		}
		return outcome{}, false
	case "nonnegative":
		if n, isNumber := value.(float64); isNumber {
			return pass(n >= 0)
		}
		return outcome{}, false
	case "nonpositive":
		if n, isNumber := value.(float64); isNumber {
			return pass(n <= 0)
		}
		return outcome{}, false
	case "finite":
		if n, isNumber := value.(float64); isNumber {
			return pass(!math.IsInf(n, 0) && !math.IsNaN(n))
		}
		return outcome{}, false
	case "multipleOf":
		// zod decides by float remainder with denormal corners; only
		// the integer case is claimed
		if n, isNumber := value.(float64); kIsNumber && isNumber &&
			isInteger(n) && isInteger(k) && k != 0 {
			return pass(math.Mod(n, k) == 0)
		}
		return outcome{}, false
	case "includes":
		s, kIsString := literal.(string)
		valueString, isString := value.(string)
		if hasLiteral && kIsString && isString {
			return pass(strings.Contains(valueString, s))
		}
		return outcome{}, false
	case "startsWith":
		s, kIsString := literal.(string)
		valueString, isString := value.(string)
		if hasLiteral && kIsString && isString {
			return pass(strings.HasPrefix(valueString, s))
		}
		return outcome{}, false
	case "endsWith":
		s, kIsString := literal.(string)
		valueString, isString := value.(string)
		if hasLiteral && kIsString && isString {
			return pass(strings.HasSuffix(valueString, s))
		}
		return outcome{}, false
	case "regex":
		if len(args) != 1 {
			return outcome{}, false
		}
		argument := unwrap(args[0])
		valueString, isString := value.(string)
		if !ast.IsRegularExpressionLiteral(argument) || !isString {
			return outcome{}, false
		}
		text := argument.Text()
		lastSlash := strings.LastIndex(text, "/")
		re, compiled := regexToGo(text[1:lastSlash], text[lastSlash+1:])
		if !compiled {
			return outcome{}, false
		}
		return pass(re.MatchString(valueString))
	default:
		return outcome{}, false
	}
}

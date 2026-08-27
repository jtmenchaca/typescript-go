// from evaluation/global_constant_access.ts
//
// Number and Math constants resolved through the default library —
// including `globalThis.Number`. Rows key on the resolved symbol.

package walk

import (
	"math"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// defaultLibGlobalParams is the destructured-parameters struct for
// defaultLibGlobal (3+ fields, per convention).
type defaultLibGlobalParams struct {
	ctx        *FlowContext
	expression *ast.Node
	name       string
}

// defaultLibGlobal: default-lib global `Number` / `Math`, including
// `globalThis.Number`. The constant rows key on the resolved symbol,
// not a bare identifier.
func defaultLibGlobal(p defaultLibGlobalParams) bool {
	if ast.IsIdentifier(p.expression) {
		return p.expression.Text() == p.name && resolvesToDefaultLib(p.ctx, p.expression)
	}
	if ast.IsPropertyAccessExpression(p.expression) {
		access := p.expression.AsPropertyAccessExpression()
		if ast.IsIdentifier(access.Name()) && access.Name().Text() == p.name {
			return resolvesToDefaultLib(p.ctx, access.Name()) || resolvesToDefaultLib(p.ctx, p.expression)
		}
	}
	return false
}

// ReadGlobalConstantAccess reads the Number/Math constant a property
// access names, resolved through the default library.
func ReadGlobalConstantAccess(ctx *FlowContext, e *ast.Node) *abstractdomain.AbstractValue {
	// the Number constants, resolved to the default library — NaN and
	// the infinities are the pinned specials; the rest are exact
	// floats
	if ast.IsPropertyAccessExpression(e) {
		access := e.AsPropertyAccessExpression()
		if defaultLibGlobal(defaultLibGlobalParams{ctx: ctx, expression: access.Expression, name: "Number"}) {
			var out abstractdomain.AbstractValue
			matched := true
			switch access.Name().Text() {
			case "NaN":
				out = abstractdomain.NaNValue
			case "POSITIVE_INFINITY":
				out = abstractdomain.KnownValues([]float64{math.Inf(1)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			case "NEGATIVE_INFINITY":
				out = abstractdomain.KnownValues([]float64{math.Inf(-1)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			case "MAX_VALUE":
				out = abstractdomain.KnownValues([]float64{math.MaxFloat64}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			case "MIN_VALUE":
				out = abstractdomain.KnownValues([]float64{5e-324}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			case "EPSILON":
				out = abstractdomain.KnownValues([]float64{2.220446049250313e-16}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			case "MAX_SAFE_INTEGER":
				out = abstractdomain.KnownValues([]float64{9007199254740991}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			case "MIN_SAFE_INTEGER":
				out = abstractdomain.KnownValues([]float64{-9007199254740991}, abstractdomain.PrimitiveNumber, abstractdomain.TrustProved)
			default:
				matched = false
			}
			if matched {
				return &out
			}
		}
	}
	// the Math constants — each is "the Number value for" its real
	// (21.3.1), i.e. the round-to-nearest double, exactly determined;
	// the host's own constants ARE those doubles
	if ast.IsPropertyAccessExpression(e) {
		access := e.AsPropertyAccessExpression()
		if defaultLibGlobal(defaultLibGlobalParams{ctx: ctx, expression: access.Expression, name: "Math"}) {
			var out abstractdomain.AbstractValue
			matched := true
			switch access.Name().Text() {
			case "PI":
				out = abstractdomain.KnownValues([]float64{math.Pi}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			case "E":
				out = abstractdomain.KnownValues([]float64{math.E}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			case "LN2":
				out = abstractdomain.KnownValues([]float64{math.Ln2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			case "LN10":
				out = abstractdomain.KnownValues([]float64{math.Log(10)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			case "LOG2E":
				out = abstractdomain.KnownValues([]float64{math.Log2E}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			case "LOG10E":
				out = abstractdomain.KnownValues([]float64{math.Log10E}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			case "SQRT2":
				out = abstractdomain.KnownValues([]float64{math.Sqrt2}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			case "SQRT1_2":
				out = abstractdomain.KnownValues([]float64{math.Sqrt(0.5)}, abstractdomain.PrimitiveNumber, abstractdomain.TrustSpec)
			default:
				matched = false
			}
			if matched {
				return &out
			}
		}
	}
	return nil
}

// unresolvedGlobalIdentifier is the SAME test evaluate_expression.go's
// bare `undefined` read already runs: the identifier is not a local
// binding this walk tracks, and its resolved symbol (if @types/node —
// or any other ambient declaration for it — happens to be installed)
// declares only in a DECLARATION file, never a user source file in
// THIS program. Node's globals (`process`, in particular) carry no
// declaration at all in a program built with no @types/node present
// (service/program_disk_host.go's own doc: the corpus programs load
// no @types package), so `symbol == nil` already answers "unresolved,
// therefore the intrinsic global" — the same answer this reads
// whether or not @types/node happens to be present, so the row's
// claim holds identically in both environments.
func unresolvedGlobalIdentifier(ctx *FlowContext, env Env, e *ast.Node) bool {
	if _, tracked := env.Get(e.Text()); tracked {
		return false
	}
	tracing.CountBy("host.symbolAtLocation", 1)
	symbol := ctx.P.Checker.GetSymbolAtLocation(e)
	if symbol == nil {
		return true
	}
	for _, d := range symbol.Declarations {
		if !ast.GetSourceFileOfNode(d).IsDeclarationFile {
			return false
		}
	}
	return true
}

// ReadNodeProcessEnvAccess reads `process.env.<KEY>` (the row's own
// dotted spelling — a bracketed `process.env["KEY"]` is a different
// AST shape, ElementAccessExpression, and is not this reader's
// concern): the environment is a map from Σ* to Σ*, so a read at any
// key is that key's value when it was SET, or absent when it was not
// — Node's own `process.env` doc: an OS environment variable, string
// when present, undefined when the key was never set (no ECMA clause
// covers it — this is a host-specific claim, not a language one). No
// @types/node package resolves `process`/`NodeJS.ProcessEnv` in this
// program (program_disk_host.go's doc), so this reads the construct
// SYNTACTICALLY — `process.env` narrows the receiver, then the outer
// property name is the key read — rather than through the declared-
// type machinery every other global here uses.
func ReadNodeProcessEnvAccess(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	if !ast.IsPropertyAccessExpression(e) {
		return nil
	}
	outer := e.AsPropertyAccessExpression()
	if !ast.IsPropertyAccessExpression(outer.Expression) {
		return nil
	}
	envAccess := outer.Expression.AsPropertyAccessExpression()
	if envAccess.Name().Text() != "env" || !ast.IsIdentifier(envAccess.Expression) ||
		envAccess.Expression.Text() != "process" {
		return nil
	}
	if !unresolvedGlobalIdentifier(ctx, env, envAccess.Expression) {
		return nil
	}
	inner := abstractdomain.KnownSet(refinementsets.Strings, nil, abstractdomain.TrustSpec, abstractdomain.SetKindTagNone)
	out := abstractdomain.PossiblyAbsent(inner, abstractdomain.AbsentFlavorUndefOnly, "", false, false)
	return &out
}

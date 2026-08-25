// from evaluation/builtin_models.ts
//
// What the checker knows about JavaScript's own built-ins. Every row
// here reads an exact answer off exact values — Math.floor of a known
// word, the length of a known string, the element at a known index —
// and every row that is not exactly specified says so rather than
// approximating (TRUST.md, and TERMS-v2 for which rows are the
// specification's own and which are transcribed).
//
// The rule for the whole file: recognise the call and answer, or
// answer nil and let the caller carry on. A method with a stated
// contract never reaches here — the contract is better than any
// built-in row, and calls.ts reads it by walking the body.
//
// This file is the DISPATCHER: the domain models live in the sibling
// *_models.go files, and each reader in the chain below answers the
// call or returns nil so the chain moves on.
//
// PARTIAL: the satellite readers this dispatcher chains
// (readArrayFrom/readArrayIsArray/readArrayOf/readArrayWriteMethods,
// readCallbackMethod, readCoercionGlobals/readJsonMethods,
// readCollectionGetHas/readCollectionMethods (this file's own
// ReadCollectionConstruction ported; the GetHas/Methods pair still
// needs MethodCallSite, defined HERE, so their own file can now
// complete), readConsoleSink, readDateMethods/readDateNow (same:
// ReadDateConstruction ported, these two still pending),
// readMathBuiltin, readObjectStaticMethods/readObjectStaticValues,
// readPromiseResolve, readReadonlyMethods, readSchemaRuntimeCall,
// readStringMatchWithConstRegex, readSymbolBuiltin,
// readUnmodeledMethod) are declared as plain same-package calls below
// per PORT.md's cross-file convention within one directory's own port
// unit — every one of them is this SAME evaluation/ directory's own
// file, not yet ported in this pass. Named in the report's "symbols
// later stages must define" list (though they are this same
// directory — the next pass over evaluation/'s remaining *_models.ts
// files completes them, using MethodCallSite from here).

package walk

import (
	"regexp"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/silence"
)

// MethodCallSite is the TS MethodCallSite interface: one method call,
// as the dispatcher hands it to a domain model: the receiver
// expression, the tracked name when the receiver is one, the
// receiver's evaluated value, and the method name.
type MethodCallSite struct {
	Ctx                *FlowContext
	Env                Env
	E                  *ast.Node // CallExpression
	ReceiverExpression *ast.Node
	// TrackedName: "" (with HasTrackedName false) when the receiver is
	// not a tracked name.
	TrackedName    string
	HasTrackedName bool
	Receiver       abstractdomain.AbstractValue
	Method         string
}

var builtinCalleeWhitespace = regexp.MustCompile(`\s+`)

// NoteUnmodeledCall is noteUnmodeledCall in the TS source: note a
// built-in call the reader has no model for, named as the source
// spells it — the row above it says this sentence instead of the
// generic default. A `f.bind(...)` with f's body in reach is modeled
// at its consumption sites, so it speaks that instead.
func NoteUnmodeledCall(ctx *FlowContext, e *ast.Node) {
	if !assignability.CollectingReasons() {
		return
	}
	call := e.AsCallExpression()
	callee := builtinCalleeWhitespace.ReplaceAllString(call.Expression.Text(), " ")
	if BoundFunctionOf(ctx, e) != nil {
		assignability.NoteReason(assignability.ReasonNote{
			Site:        "expression",
			Node:        e,
			Said:        callee + "() stores a bound function — its body reads at each call",
			Unsupported: false,
		})
		return
	}
	assignability.NoteReason(assignability.ReasonNote{
		Site:        "expression",
		Node:        e,
		Said:        callee + "() is not modeled",
		Unsupported: true,
	})
}

// ReadBuiltinCall is readBuiltinCall in the TS source.
func ReadBuiltinCall(ctx *FlowContext, env Env, e *ast.Node, spreadArguments func(args []*ast.Node) []abstractdomain.AbstractValue) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	// the built-ins' own argument contracts (builtin_contracts.ts) —
	// judged first, whatever the result model below answers
	CheckBuiltinContracts(ctx, env, e)
	if answered := readSymbolBuiltin(ctx, env, e); answered != nil {
		return answered
	}
	if answered := readCoercionGlobals(ctx, env, e, spreadArguments); answered != nil {
		return answered
	}
	if answered := readMathBuiltin(ctx, env, e, spreadArguments); answered != nil {
		return answered
	}
	if answered := readPromiseResolve(ctx, env, e); answered != nil {
		return answered
	}
	if answered := readPromiseStatics(ctx, env, e); answered != nil {
		return answered
	}
	if answered := readPromiseWithResolvers(ctx, env, e); answered != nil {
		return answered
	}
	if answered := readPromiseResolverCall(ctx, env, e); answered != nil {
		return answered
	}
	if answered := readArrayFrom(ctx, env, e); answered != nil {
		return answered
	}
	// `Array(…)` called as a function builds the same array the
	// construction does — sec-array runs one algorithm for both
	// spellings (array_construction.go)
	if answered := ReadArrayConstruction(ctx, env, e); answered != nil {
		return answered
	}
	// `createHash(alg)` / `createHmac(alg, key)` — the value a hashing
	// chain starts from. Read ahead of the method section because the
	// factory is reached both bare and through its module namespace,
	// and neither spelling is a modeled method call.
	if answered := ReadNodeDigestConstruction(ctx, env, e); answered != nil {
		return answered
	}
	// a method call — the receiver may be a tracked name or any
	// expression (a literal, a call result). A method WITH a stated
	// contract falls through to the contract path below instead.
	if ast.IsPropertyAccessExpression(call.Expression) && ContractOf(ctx, call.Expression) == nil {
		pa := call.Expression.AsPropertyAccessExpression()
		receiverExpression := pa.Expression
		var trackedName string
		hasTrackedName := false
		if ast.IsIdentifier(receiverExpression) {
			if _, ok := env.Get(receiverExpression.Text()); ok {
				trackedName, hasTrackedName = receiverExpression.Text(), true
			}
		}
		var receiver abstractdomain.AbstractValue
		if hasTrackedName {
			held, ok := env.Get(trackedName)
			if ok {
				receiver = held
			} else {
				receiver = silence.Residue()
			}
		} else {
			receiver = evaluateExpression(ctx, env, receiverExpression)
		}
		method := pa.Name().Text()
		site := MethodCallSite{
			Ctx: ctx, Env: env, E: e, ReceiverExpression: receiverExpression,
			TrackedName: trackedName, HasTrackedName: hasTrackedName, Receiver: receiver, Method: method,
		}
		if answered := readArrayIsArray(site); answered != nil {
			return answered
		}
		if answered := readObjectStaticValues(site); answered != nil {
			return answered
		}
		if answered := readObjectGroupBy(site); answered != nil {
			return answered
		}
		if answered := readDateNow(site); answered != nil {
			return answered
		}
		if answered := readDateParse(site); answered != nil {
			return answered
		}
		if answered := readJsonMethods(site); answered != nil {
			return answered
		}
		if answered := readSchemaRuntimeCall(site); answered != nil {
			return answered
		}
		if answered := readCallbackMethod(site); answered != nil {
			return answered
		}
		if answered := readPromiseInstanceMethod(site); answered != nil {
			return answered
		}
		if answered := readPromiseWithResolversPropertyCall(site); answered != nil {
			return answered
		}
		if answered := readArrayOf(site); answered != nil {
			return answered
		}
		if answered := readReadonlyMethods(site); answered != nil {
			return answered
		}
		if answered := readObjectStaticMethods(site); answered != nil {
			return answered
		}
		// a fresh (untracked) receiver's own `.fill(value)` VALUE — tried
		// before the tracked-mutation reader below, which requires
		// site.HasTrackedName and would otherwise never see this call at
		// all (an inline `Array(2).fill(41)` receiver names no tracked
		// identifier to mutate)
		if answered := readFreshArrayFill(site); answered != nil {
			return answered
		}
		if answered := readArrayWriteMethods(site); answered != nil {
			return answered
		}
		if answered := readArraySortReverseMethods(site); answered != nil {
			return answered
		}
		if answered := readConsoleSink(site); answered != nil {
			return answered
		}
		if answered := readCollectionGetHas(site); answered != nil {
			return answered
		}
		if answered := readDateMethods(site); answered != nil {
			return answered
		}
		// the text codecs' own pure reads (text_encoding_models.go)
		if answered := readTextEncoderEncode(site); answered != nil {
			return answered
		}
		if answered := readTextDecoderDecode(site); answered != nil {
			return answered
		}
		// an EXACT query's own `.get(name)` — the value the parsed URL
		// carries under that name, or null. Tried before the generic web
		// row, which answers the string sort for the same call.
		if answered := readExactSearchParamsGet(site); answered != nil {
			return answered
		}
		if web := WebMethodCall(ctx.P, e, receiverExpression, method); web != nil {
			return web
		}
		if answered := readCollectionMethods(site); answered != nil {
			return answered
		}
		// the chain-threading rows: an iterator's own next(), and the
		// hashing chain's update/digest pair. Each recognizes its
		// receiver by STATIC type, so a link answers whether or not the
		// link before it left a value behind.
		if answered := readIteratorNext(site); answered != nil {
			return answered
		}
		if answered := readNodeDigestMethods(site); answered != nil {
			return answered
		}
		if answered := readStringMatchWithConstRegex(site); answered != nil {
			return answered
		}
		if answered := readRegExpExecCall(site); answered != nil {
			return answered
		}
		answered := readUnmodeledMethod(site)
		return &answered
	}
	return nil
}

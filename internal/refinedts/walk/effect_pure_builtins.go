// split from effect_expression.go — pure builtin readers and the boolean pair

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/refinementsets"
)

// booleanPairEffect is the constant two-value set a boolean-valued
// operator produces — true rides 1 and false rides 0, the same
// encoding the true/false keyword constants below use.
func booleanPairEffect() kernelbridge.LoopEffect {
	return kernelbridge.LoopEffect{
		Kind: kernelbridge.LoopEffectConst,
		Set:  refinementsets.MakeRefinedSet(refinementsets.OneOf([]float64{0, 1})),
	}
}

// pureBuiltinReaders: `<root>.<name>` callees whose spec behavior is a
// READ — they inspect their arguments and global state and move
// nothing. The bool says whether the value is a PREDICATE (exactly
// true or false — the two-value set); false means the value has no
// scalar spelling and rides unknown. Reflect's write half
// (defineMetadata, set, deleteProperty…) is deliberately absent.
var pureBuiltinReaders = map[string]map[string]bool{
	"Reflect": {
		"getMetadata": false, "getOwnMetadata": false,
		"hasMetadata": true, "hasOwnMetadata": true,
		"getMetadataKeys": false, "getOwnMetadataKeys": false,
	},
	"Object": {
		"keys": false, "values": false, "entries": false,
		"getPrototypeOf": false, "getOwnPropertyNames": false,
		"getOwnPropertyDescriptor": false, "getOwnPropertySymbols": false,
	},
	"Array":  {"isArray": true},
	"Number": {"isInteger": true, "isFinite": true, "isNaN": true, "isSafeInteger": true},
}

// pureBuiltinEffect reads a call to a curated pure builtin: the callee
// must be exactly `<root>.<name>` on the global root identifier, and
// every argument must move nothing (write- and call-free) — an
// argument that runs code would need a statement, which an effect is
// not. Predicates answer the exact two-value set; the rest answer
// unknown, which is what a metadata object or key list is worth in a
// scalar slot.
func pureBuiltinEffect(call *ast.CallExpression) (kernelbridge.LoopEffect, bool) {
	if call.QuestionDotToken != nil || !ast.IsPropertyAccessExpression(call.Expression) {
		return kernelbridge.LoopEffect{}, false
	}
	access := call.Expression.AsPropertyAccessExpression()
	if access.QuestionDotToken != nil || !ast.IsIdentifier(access.Expression) || !ast.IsIdentifier(access.Name()) {
		return kernelbridge.LoopEffect{}, false
	}
	names, knownRoot := pureBuiltinReaders[access.Expression.Text()]
	if !knownRoot {
		return kernelbridge.LoopEffect{}, false
	}
	isPredicate, knownName := names[access.Name().Text()]
	if !knownName {
		return kernelbridge.LoopEffect{}, false
	}
	if call.Arguments != nil {
		for _, argument := range call.Arguments.Nodes {
			if !writeAndCallFree(argument) {
				return kernelbridge.LoopEffect{}, false
			}
		}
	}
	if isPredicate {
		return booleanPairEffect(), true
	}
	return kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}, true
}

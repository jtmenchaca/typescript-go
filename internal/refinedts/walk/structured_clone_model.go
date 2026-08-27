// `structuredClone(v)` — a deep copy whose CONTENTS equal the
// original's and whose identities are all fresh.
//
// The reading. The installed declaration is
// `structuredClone<T = any>(value: T, options?: StructuredSerializeOptions): T`
// (typescript/lib/lib.dom.d.ts:44125, and identically
// lib.webworker.d.ts:14975) — the result wears the argument's own type,
// which is the host's own statement that the copy holds what the
// original held. The algorithm behind it walks the value and rebuilds
// each part: a Map's entries are re-set into a fresh Map, a list's
// items into a fresh list, an object's own keys onto a fresh object.
//
// WHAT THIS MODEL CLAIMS, AND WHAT IT DOES NOT. It claims the copy's
// CONTENTS: the same entries, the same items, the same keys and their
// values, at the same grade. It claims nothing about IDENTITY — the
// clone is a different object by construction, which is exactly why
// this is a value transfer and never an identity transfer
// (identity_narrowings.go is the file that speaks about identity, and
// a clone is the one shape it must not be asked about).
//
// COMPLETENESS RIDES, and that is the point of the model rather than a
// side effect. The copy is built from the original's parts and nothing
// else, so if the walk knew every entry of the original it knows every
// entry of the copy. A record the walk could not pin completely clones
// to one it still cannot pin.
//
// THE KINDS. Only the kinds whose contents this walk actually carries
// are answered: a collection, an exact list, an object. Everything
// else — a primitive (cloned unchanged, but the caller's own value
// already says so), an unknown, a promise, a function (which the
// algorithm refuses outright, throwing at runtime) — is left to the
// existing readers rather than claimed here.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// readStructuredClone answers `structuredClone(v)` with v's own value,
// where v is one of the kinds whose contents the walk carries. Nil for
// any other callee, argument count, or value kind — the caller's chain
// moves on.
func readStructuredClone(ctx *FlowContext, env Env, e *ast.Node) *abstractdomain.AbstractValue {
	call := e.AsCallExpression()
	if !ast.IsIdentifier(call.Expression) || call.Expression.Text() != "structuredClone" {
		return nil
	}
	if !resolvesToDefaultLib(ctx, call.Expression) {
		return nil
	}
	if call.Arguments == nil || len(call.Arguments.Nodes) == 0 {
		return nil
	}
	// the transfer options argument is read for its own effects; it
	// names transferables, which change ownership rather than contents
	value := evaluateExpression(ctx, env, call.Arguments.Nodes[0])
	for _, rest := range call.Arguments.Nodes[1:] {
		evaluateExpression(ctx, env, rest)
	}
	switch value.Kind {
	case abstractdomain.KindCollection, abstractdomain.KindList, abstractdomain.KindObject:
		return &value
	default:
		return nil
	}
}

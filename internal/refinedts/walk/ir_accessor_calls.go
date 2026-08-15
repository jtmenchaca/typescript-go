// Accessors as CALLS: `o.x` where x is a getter runs a body, and
// `o.x = e` where x is a setter runs another one.
//
// Wave 4 makes an object with a declared shape a bundle of slots, and
// the field census (ir_field_bundles.go) deliberately leaves accessors
// OUT of that bundle: a get/set accessor with a body runs code, and a
// slot cannot stand for code. So today a `this.x` backed by a getter
// finds no slot, falls to the opaque floor, and the body goes porous —
// which is exactly the shape wave 4 exists to remove, because a nest
// method's hot reads are accessor-backed.
//
// What this file does instead is read the accessor for what it is: a
// zero-argument CALL for the read, a one-argument call for the write.
// The accessor's declaration compiles to its own summary through the
// ordinary registry, and the site splices it with IrStatementCall — the
// same statement `wrapper.get(k)` already lowers to. Nothing new reaches
// the kernel: the call statement and its receiver threading are the
// wave-4 seam already proved through summarize_eq.
//
// The one shape difference from an ordinary call is that a getter read
// has NO CallExpression node to hand HoistCallEffect. `o.x` is an
// expression the effect grammar wants an EFFECT for, while the callee's
// answer arrives as a call STATEMENT writing a slot. So the read
// allocates a temp, appends the call statement to the lowering's hoist
// list, and answers `var temp` — the hoist machinery's own shape,
// reached without a syntax node to hoist from.
//
// Every decline here keeps exactly today's behaviour: the read finds no
// slot and the floor serves it, as it did before this file existed.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

/* ── resolving the accessor ──────────────────────────────────────── */

// AccessorDeclarationsOf reads a PROPERTY ACCESS as an accessor pair:
// the get and/or set accessor declarations the accessed NAME resolves
// to, or (nil, nil, false).
//
// The resolution is ContractBySymbol's, one step shorter: that function
// resolves a CALLEE to a held contract, and this one resolves a
// PROPERTY NAME to the accessor declarations its symbol carries — a
// symbol's Declarations list holds the getter and the setter as separate
// nodes, so an accessor pair answers both and a get-only or set-only
// property answers the one it has.
//
// THE GATES, each a twin of one ContractBySymbol/ContractOf applies —
// neither exports a helper for it, so each is replicated here beside the
// reason it exists:
//
//   - a nil context, or one with no program or no checker, resolves
//     nothing (ContractBySymbol's first gate verbatim: a context without
//     a program has no checker to resolve symbols through). The
//     nil-tolerance is load-bearing — a lowering that runs without a
//     checker must decline the accessor, never crash.
//   - a COMPUTED name (`o[k]`, `get [key]()`) spells no property to
//     resolve, so an element access never reaches here and a computed
//     accessor declaration is skipped below. This is the field census's
//     own computed rule (ir_field_bundles.go's ClassFieldsOf: "a member
//     whose name is COMPUTED — nothing spells the slot").
//   - an OPTIONAL step (`o?.x`) may read a property of nothing at all,
//     and no call statement stands for "the call that may not have
//     happened". receiverPathOf declines the same shape for the same
//     reason.
//   - a name whose symbol carries MORE THAN ONE declaration of the same
//     accessor kind — the single-symbol stability gate
//     namedTypeMembersOf states: two declarations mean the reading would
//     have to pick one, and which one it picked would not be a property
//     of the syntax. (A get/set PAIR is two declarations of DIFFERENT
//     kinds and is the shape this answers for; two getters is the
//     unstable one.)
//   - an accessor declared on a class whose method NAME is OVERRIDDEN
//     anywhere in view — ContractOf's virtual-dispatch gate, replicated:
//     the resolved base body must not stand for every instance, so no one
//     declaration stands for the access.
//   - an accessor with NO BODY (a declaration file's `get x(): number;`,
//     an abstract accessor) — there is no code to summarize.
func AccessorDeclarationsOf(ctx *FlowContext, access *ast.Node) (getter *ast.Node, setter *ast.Node, ok bool) {
	// a context without a program has no checker to resolve symbols
	// through — ContractBySymbol's own first gate
	if ctx == nil || ctx.P == nil || ctx.P.Checker == nil {
		return nil, nil, false
	}
	if access == nil || !ast.IsPropertyAccessExpression(access) {
		return nil, nil, false
	}
	property := access.AsPropertyAccessExpression()
	// an optional step reads a property of a maybe-absent object; no call
	// statement stands for a call that may not have happened
	if property.QuestionDotToken != nil {
		return nil, nil, false
	}
	name := property.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return nil, nil, false
	}
	symbol := symbolAt(ctx.P.Checker, name)
	if symbol == nil {
		return nil, nil, false
	}
	for _, declaration := range symbol.Declarations {
		if declaration == nil {
			continue
		}
		switch {
		case ast.IsGetAccessorDeclaration(declaration):
			if !accessorUsable(declaration) {
				return nil, nil, false
			}
			// two getters for one name: the reading would have to pick, and
			// which it picked would not be a property of the syntax
			if getter != nil {
				return nil, nil, false
			}
			getter = declaration
		case ast.IsSetAccessorDeclaration(declaration):
			if !accessorUsable(declaration) {
				return nil, nil, false
			}
			if setter != nil {
				return nil, nil, false
			}
			setter = declaration
		}
	}
	if getter == nil && setter == nil {
		return nil, nil, false
	}
	if accessorOverridden(ctx, getter) || accessorOverridden(ctx, setter) {
		return nil, nil, false
	}
	return getter, setter, true
}

// accessorUsable is whether ONE accessor declaration is a body this
// lowering could ever call: a plain identifier name (a computed one
// spells nothing) and a body to summarize (a declaration-file or
// abstract accessor has none).
func accessorUsable(declaration *ast.Node) bool {
	name := declaration.Name()
	if name == nil || (!ast.IsIdentifier(name) && !ast.IsPrivateIdentifier(name)) {
		return false
	}
	return declaration.Body() != nil
}

// accessorOverridden is ContractOf's virtual-dispatch gate over an
// accessor: a name overridden anywhere in view dispatches to a body this
// resolution never saw, so the resolved declaration must not stand for
// every instance.
//
// The name set is OverriddenMethodNames' — the same registry reading
// ContractOf takes for methods. An accessor and a method share the
// question exactly: both are class members reached by name through a
// receiver whose runtime class the site does not pin.
func accessorOverridden(ctx *FlowContext, declaration *ast.Node) bool {
	if declaration == nil {
		return false
	}
	name := declaration.Name()
	if name == nil || !ast.IsIdentifier(name) {
		return false
	}
	if ctx == nil {
		return false
	}
	_, overridden := OverriddenMethodNames(&ctx.Contracts)[name.Text()]
	return overridden
}

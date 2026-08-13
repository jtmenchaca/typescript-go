// from interprocedural/contract_lookup.ts
//
// The by-symbol and by-declaration-node lookup behind a call's
// contract. The public gate (reassigned names, overridden methods)
// lives with the call entry; this file holds the index that makes
// the miss path cheap.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
)

// ContractBySymbol resolves a callee expression to a held contract
// by its symbol, falling back to a declaration-node index when the
// use-site symbol differs from the registration-site symbol.
func ContractBySymbol(ctx *FlowContext, callee *ast.Node) *FunctionContract {
	// a context without a program has no checker to resolve symbols
	// through and no registry to hold them — nothing to answer
	if ctx == nil || ctx.P == nil {
		return nil
	}
	var name *ast.Node
	if ast.IsIdentifier(callee) {
		name = callee
	} else if ast.IsPropertyAccessExpression(callee) {
		name = callee.AsPropertyAccessExpression().Name()
	} else {
		return nil
	}
	symbol := symbolAt(ctx.P.Checker, name)
	if symbol == nil {
		return nil
	}
	if held, ok := ctx.Contracts[symbol]; ok {
		return held
	}
	// a METHOD's use-site symbol can differ from its declaration-site
	// symbol; the declaration NODE is the stable identity — and a
	// property-assigned closure's symbol declares the PROPERTY, whose
	// initializer is the registered function
	declaration := symbol.ValueDeclaration
	if declaration == nil {
		return nil
	}
	var initializer *ast.Node
	if ast.IsPropertyAssignment(declaration) {
		initializer = declaration.AsPropertyAssignment().Initializer
	} else if ast.IsPropertyDeclaration(declaration) {
		initializer = declaration.AsPropertyDeclaration().Initializer
	} else if ast.IsVariableDeclaration(declaration) {
		initializer = declaration.AsVariableDeclaration().Initializer
	}
	// the fallback: a node-keyed index over every contract in the
	// PROGRAM, built once per contracts map on first miss — the linear
	// scan it replaces cost 4.2 BILLION comparisons on tailwindcss's
	// utilities.ts (6.7M lookups × ~630 contracts)
	index := ContractIndexOf(&ctx.Contracts)
	if held, ok := index[declaration]; ok {
		return held
	}
	if initializer != nil {
		if held, ok := index[initializer]; ok {
			return held
		}
	}
	return nil
}

// contractIndexesMu guards contractIndexes: the by-declaration-node
// index of a contracts map, held per map — the map is immutable once
// the file's contracts register. The TS source keys this with a
// `WeakMap<ReadonlyMap<ts.Symbol, FunctionContract>, Map<ts.Node,
// FunctionContract>>`; Go has no weak maps, so this substitutes a
// regular map guarded by a mutex (same substitution as
// annotations/annotation_of_type.go's readingTypeNodes) — keyed on
// the contracts map's own address. A Go map VALUE has no stable
// identity across calls (each parameter copy is a fresh local with
// its own address), so the caller passes `&ctx.Contracts` — the
// FlowContext field's address, stable for that FlowContext's whole
// walk — rather than the map by value.
var (
	contractIndexesMu sync.Mutex
	contractIndexes   = map[*map[*ast.Symbol]*FunctionContract]map[*ast.Node]*FunctionContract{}
)

// ContractIndexOf is the by-declaration-node index of a contracts
// map, held per map — the map is immutable once the file's contracts
// register. `contracts` is the map's address (see contractIndexes'
// comment), not the map by value.
func ContractIndexOf(contracts *map[*ast.Symbol]*FunctionContract) map[*ast.Node]*FunctionContract {
	contractIndexesMu.Lock()
	defer contractIndexesMu.Unlock()
	if held, ok := contractIndexes[contracts]; ok {
		return held
	}
	index := map[*ast.Node]*FunctionContract{}
	for _, contract := range *contracts {
		if _, ok := index[contract.Declaration]; !ok {
			index[contract.Declaration] = contract
		}
	}
	contractIndexes[contracts] = index
	return index
}

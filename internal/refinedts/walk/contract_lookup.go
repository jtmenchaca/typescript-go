// from interprocedural/contract_lookup.ts
//
// The by-symbol and by-declaration-node lookup behind a call's
// contract. The public gate (reassigned names, overridden methods)
// lives with the call entry; this file holds the index that makes
// the miss path cheap.

package walk

import (
	"reflect"
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
	// a PROPERTY'S initializer is a stable identity for the alias
	// resolution below; a VARIABLE'S is not — a `let` alias may be
	// reassigned, so only the node-index fallback reads it
	propertyInitializer := false
	if ast.IsPropertyAssignment(declaration) {
		initializer = declaration.AsPropertyAssignment().Initializer
		propertyInitializer = true
	} else if ast.IsPropertyDeclaration(declaration) {
		initializer = declaration.AsPropertyDeclaration().Initializer
		propertyInitializer = true
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
		// a property that ALIASES an existing function by name
		// (`{ bump: helperFn }`): the initializer is an identifier, not
		// a function node, so the node index misses — resolve the named
		// function's own symbol and answer its contract
		if propertyInitializer && ast.IsIdentifier(initializer) {
			target := symbolAt(ctx.P.Checker, initializer)
			if target != nil {
				if held, ok := ctx.Contracts[target]; ok {
					return held
				}
				if target.ValueDeclaration != nil {
					if held, ok := index[target.ValueDeclaration]; ok {
						return held
					}
				}
			}
		}
	}
	return nil
}

// contractIndexRow is one built index beside the map it indexed and
// the map's entry count at build time. Holding the map itself keeps
// it live, so its header address below stays unambiguous — a freed
// map's address could otherwise be reused by a new one; the size is
// what detects a map that grew after the row was built (tests
// register contracts into a context's map before walking).
type contractIndexRow struct {
	contracts map[*ast.Symbol]*FunctionContract
	size      int
	index     map[*ast.Node]*FunctionContract
}

// contractIndexesMu guards contractIndexes: the by-declaration-node
// index of a contracts map, held per map — the map is immutable once
// the file's contracts register. The TS source keys this with a
// `WeakMap<ReadonlyMap<ts.Symbol, FunctionContract>, Map<ts.Node,
// FunctionContract>>`; Go has no weak maps, so this substitutes a
// regular map guarded by a mutex (same substitution as
// annotations/annotation_of_type.go's readingTypeNodes) — keyed on
// the map's OWN header address (reflect.Value.Pointer), which is
// stable across every handle onto the same map. Keying on the caller's
// field address instead rebuilt the same program-wide index once per
// FlowContext — one per entry walk, one per call-site snapshot fill —
// which was the ContractIndexOf row of the 2026-08-17 alloc profile.
var (
	contractIndexesMu sync.Mutex
	contractIndexes   = map[uintptr]contractIndexRow{}
)

// ContractIndexOf is the by-declaration-node index of a contracts
// map, held per map — the map is immutable once the file's contracts
// register. `contracts` is the FlowContext field's address; the map
// it holds is the identity the row is kept under.
func ContractIndexOf(contracts *map[*ast.Symbol]*FunctionContract) map[*ast.Node]*FunctionContract {
	m := *contracts
	key := reflect.ValueOf(m).Pointer()
	contractIndexesMu.Lock()
	defer contractIndexesMu.Unlock()
	if held, ok := contractIndexes[key]; ok && held.size == len(m) {
		return held.index
	}
	index := make(map[*ast.Node]*FunctionContract, len(m))
	for _, contract := range m {
		if _, ok := index[contract.Declaration]; !ok {
			index[contract.Declaration] = contract
		}
	}
	contractIndexes[key] = contractIndexRow{contracts: m, size: len(m), index: index}
	return index
}

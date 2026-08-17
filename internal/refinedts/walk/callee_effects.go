// from interprocedural/callee_effects.ts
//
// Per-declaration effect facts an inline consults: which parameters
// a body captures into nested functions, which method names are
// overridden in view, which names the body may write, and which
// names it can observe from its caller. Each answer is held once
// on the declaration (or the contracts map) so the call site pays
// the walk at most once.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

// capturedParamsMu guards capturedParams: the parameter names a body
// CAPTURES into nested function values — closures that may escape
// (returned, stored) and mutate the argument long after the call.
// The inline never runs them, so the write-back must FORGET such
// arguments instead of restating their entry facts. The TS source
// keys this with a `WeakMap<ts.Node, ReadonlySet<string>>`; Go
// substitutes a regular map guarded by a mutex (see
// class_field_invariants.go's invariantMemo for the same pattern).
var (
	capturedParamsMu sync.Mutex
	capturedParams   = map[*ast.Node]map[string]struct{}{}
)

// CapturedOf is capturedOf in the TS source.
func CapturedOf(declaration *ast.Node) map[string]struct{} {
	capturedParamsMu.Lock()
	if held, ok := capturedParams[declaration]; ok {
		capturedParamsMu.Unlock()
		return held
	}
	capturedParamsMu.Unlock()
	parameterNames := map[string]struct{}{}
	for _, parameter := range declaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if ast.IsIdentifier(name) {
			parameterNames[name.Text()] = struct{}{}
		}
	}
	captured := map[string]struct{}{}
	var insideFunction func(node *ast.Node)
	insideFunction = func(node *ast.Node) {
		if ast.IsIdentifier(node) {
			if _, ok := parameterNames[node.Text()]; ok {
				captured[node.Text()] = struct{}{}
			}
		}
		node.ForEachChild(func(child *ast.Node) bool {
			insideFunction(child)
			return false
		})
	}
	var walkNode func(node *ast.Node)
	walkNode = func(node *ast.Node) {
		if ast.IsArrowFunction(node) || ast.IsFunctionExpression(node) ||
			ast.IsFunctionDeclaration(node) || ast.IsMethodDeclaration(node) {
			insideFunction(node)
			return
		}
		node.ForEachChild(func(child *ast.Node) bool {
			walkNode(child)
			return false
		})
	}
	if body := declaration.Body(); body != nil {
		walkNode(body)
	}
	capturedParamsMu.Lock()
	capturedParams[declaration] = captured
	capturedParamsMu.Unlock()
	return captured
}

// overrideNamesHeldMu guards overrideNamesHeld: whether a resolved
// METHOD is overridden anywhere in view — a `this.m()` dispatches
// virtually, so a base body that answers a constant must not stand
// for every instance. The TS source keys this with a
// `WeakMap<object, Set<string>>` on the CONTRACTS MAP's identity;
// keyed here on the map's address for the same reason
// contract_lookup.go's contractIndexes is (a Go map value has no
// stable address across calls — the caller passes `&ctx.Contracts`).
var (
	overrideNamesHeldMu sync.Mutex
	overrideNamesHeld   = map[*map[*ast.Symbol]*FunctionContract]map[string]struct{}{}
)

// OverriddenMethodNames is overriddenMethodNames in the TS source.
// `contracts` is the contracts map's address (see
// overrideNamesHeld's comment), not the map by value.
func OverriddenMethodNames(contracts *map[*ast.Symbol]*FunctionContract) map[string]struct{} {
	overrideNamesHeldMu.Lock()
	if held, ok := overrideNamesHeld[contracts]; ok {
		overrideNamesHeldMu.Unlock()
		return held
	}
	overrideNamesHeldMu.Unlock()
	names := map[string]struct{}{}
	for _, contract := range *contracts {
		declaration := contract.Declaration
		if ast.IsMethodDeclaration(declaration) {
			md := declaration.AsMethodDeclaration()
			if ast.IsIdentifier(md.Name()) && ast.GetCombinedModifierFlags(declaration)&ast.ModifierFlagsOverride != 0 {
				names[md.Name().Text()] = struct{}{}
			}
		}
	}
	overrideNamesHeldMu.Lock()
	overrideNamesHeld[contracts] = names
	overrideNamesHeldMu.Unlock()
	return names
}

// bodyWriteNamesMu guards bodyWriteNames: the names a body may
// WRITE — assignment targets, ++/--, method receivers, handed
// references — per declaration. A name outside this set only ever
// gets NARROWED inside the body, and narrowing holds along the
// body's own paths, not the caller's: writing it back leaked "value
// is an ArgRef object" onto callers whose call had just answered
// FALSE. Only mutation writes back. The TS source keys this with a
// `WeakMap<ts.Node, ReadonlySet<string>>`; substituted the same way
// as capturedParams above.
var (
	bodyWriteNamesMu sync.Mutex
	bodyWriteNames   = map[*ast.Node]map[string]struct{}{}
)

// BodyWritesOf is bodyWritesOf in the TS source.
//
// assignedNamesDirect (control_flow/assigned_names.ts) is BLOCKED:
// control_flow is not ported yet (go-port-tracker.md: wave 3). This
// function calls it as a same-package undefined symbol, per PORT.md's
// walk-component convention — the caller must fill it in when
// control_flow lands.
func BodyWritesOf(ctx *FlowContext, declaration *ast.Node) map[string]struct{} {
	bodyWriteNamesMu.Lock()
	if held, ok := bodyWriteNames[declaration]; ok {
		bodyWriteNamesMu.Unlock()
		return held
	}
	bodyWriteNamesMu.Unlock()
	written := map[string]struct{}{}
	body := declaration.Body()
	if body != nil {
		// the TEXT's own writes: assignments, ++/--, deletes
		AssignedNamesDirect(body, written)
		// call-mediated writes, but only through callees that MAY write:
		// handing a parameter to an effect-free predicate is a read, and
		// counting it as a write re-opened the narrowing leak through
		// nested predicates (isAuthoringSelectRef → isAuthoringTemplateRecord)
		var scan func(node *ast.Node)
		scan = func(node *ast.Node) {
			if ast.IsCallExpression(node) {
				callExpr := node.AsCallExpression()
				callee := callExpr.Expression
				// one resolution serves both the RECEIVER reading below and
				// the ARGUMENT reading further down — the same contract
				// lookup answers "what does this callee do to what it is
				// handed" for both.
				contract := ContractOf(ctx, callee)
				if ast.IsPropertyAccessExpression(callee) {
					pae := callee.AsPropertyAccessExpression()
					if ast.IsIdentifier(pae.Expression) {
						if receiverWritten(ctx, declaration, contract, pae.Name().Text()) {
							written[pae.Expression.Text()] = struct{}{}
						}
					}
				}
				calleeMayWrite := contract == nil || !Summarize(ctx, *contract).EffectFree
				if calleeMayWrite {
					for _, argument := range callExpr.Arguments.Nodes {
						root := argument
						for ast.IsPropertyAccessExpression(root) || ast.IsElementAccessExpression(root) {
							if ast.IsPropertyAccessExpression(root) {
								root = root.AsPropertyAccessExpression().Expression
							} else {
								root = root.AsElementAccessExpression().Expression
							}
						}
						if ast.IsIdentifier(root) {
							written[root.Text()] = struct{}{}
						}
					}
				}
			}
			node.ForEachChild(func(child *ast.Node) bool {
				scan(child)
				return false
			})
		}
		scan(body)
	}
	bodyWriteNamesMu.Lock()
	bodyWriteNames[declaration] = written
	bodyWriteNamesMu.Unlock()
	return written
}

// receiverWritten answers whether `receiver.method(…)` writes the
// receiver, for the method-receiver arm of BodyWritesOf.
//
// A callee this package can SUMMARIZE answers from its own effect
// summary: SummaryReceiverEffects reads the summary's written
// this-entries (and a returned receiver, which moves the caller's
// object knowledge the same way), which is the same machinery the
// ARGUMENT arm reads through Summarize — so a user-defined pure
// method stops counting as a write on its receiver.
//
// Only when the callee resolves to no contract, or the summary
// declines to lower, does the hand-curated read-only-name list
// decide, unchanged. The direction stays safe throughout: every
// doubt answers true, and a name counted as written merely narrows
// less.
func receiverWritten(ctx *FlowContext, declaration *ast.Node, contract *FunctionContract, methodName string) bool {
	// the declaration being scanned RIGHT NOW cannot be summarized from
	// inside its own scan: LowerSummaryBody memoizes only after it
	// finishes, so a self-call would lower this body a second time
	// underneath itself. A self-call falls to the name list, which
	// counts the receiver written unless the name reads read-only.
	if contract != nil && contract.Declaration != declaration {
		// SummaryReceiverEffects answers a bare false BOTH when the
		// summary says the receiver is untouched AND when the body did
		// not lower at all, so the lowering is asked first — and only a
		// COMPLETE body gets to say "not written". A POROUS body has
		// statements the lowering did not read (an escaped receiver, a
		// havocked construct), so its empty written-entry list is absence
		// of evidence: trusting it kept a caller's stale field knowledge
		// across `over.write(200)` when write's own body escaped. The
		// doubt direction is written. A declined lowering falls through
		// to the name list below.
		if _, lowered := LowerSummaryBody(ctx, contract.Declaration); lowered {
			if outcome, _, recorded := SummaryOutcomeOf(contract.Declaration); recorded && outcome == SummaryComplete {
				receiverTouched, _ := SummaryReceiverEffects(ctx, contract.Declaration)
				return receiverTouched
			}
			return true
		}
	}
	_, readOnly := dataflowfacts.ReadOnlyArrayMethods[methodName]
	return !readOnly
}

// ObservedOf is everything a body can OBSERVE from its caller, per
// declaration: every identifier text its subtree mentions plus
// `this` — an overapproximation of its outer reads and writes (extra
// names only lower the memo's hit rate, never its soundness). Read
// from the syntactic-facts seam.
func ObservedOf(declaration *ast.Node) []string {
	body := declaration.Body()
	if body == nil {
		return nil
	}
	return dataflowfacts.ObservedNamesOf(body)
}

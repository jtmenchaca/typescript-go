// Demand-filled call-site snapshots — no TS twin (Go-only mechanism,
// same precedent as declaredJoin's inProgress guard).
//
// The TS source's snapshots are PUSH-filled: an owner's dedicated
// pass-3 walk records them, and the walk schedule (callers-first,
// enclosers-first) is what makes a join's read find them warm. That
// held in the single-threaded TS checker because the schedule was
// deterministic; under this port's parallel sweep, and for owners no
// schedule ever walks (an anonymous arrow handed to React.forwardRef
// is nobody's contract), a join can ask before any owner walk ran and
// pay the per-query fallback once per call site — the CartesianAxis
// amplifier.
//
// This file converts the store to PULL: a miss runs the missing
// owner's own dedicated walk once — the same walk pass 3 performs,
// with a silent report sink — which write-through records every
// queryable call under the owner, then the store is read again. The
// result no longer depends on walk order: whichever side asks first,
// the recorded environment is the one the owner's dedicated walk
// produces. A call the fill still did not record (dead branch, a
// refused kernel question mid-walk) keeps the per-query fallback.

package walk

import (
	"sync"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// demandFilled marks owners whose fill walk already ran, per program —
// a fill runs at most once per owner, and marking BEFORE the walk
// makes a re-entrant ask during the fill (mutual recursion between
// joins) fall back to the per-query walk instead of recursing.
var (
	demandFilledMu sync.Mutex
	demandFilled   = map[*program.CheckerProgram]map[*ast.Node]bool{}
)

// CallSnapshotOrFill reads the recorded snapshot at a call; on a miss
// it runs the owner's dedicated walk once (write-through) and reads
// again. (nil, false) where no walk records this call — the caller
// keeps its per-query fallback.
func CallSnapshotOrFill(ctx CallSiteCtx, call *ast.Node) (Env, bool) {
	if snapshot, ok := CallSnapshotOf(ctx.P, call); ok {
		return snapshot, true
	}
	owner := outermostFunctionLike(call)
	if owner == nil {
		// a top-level call's owner is the entry file, whose pass-3
		// top-level walk already ran before any join asks
		return nil, false
	}
	demandFilledMu.Lock()
	filled := demandFilled[ctx.P]
	if filled == nil {
		filled = map[*ast.Node]bool{}
		demandFilled[ctx.P] = filled
	}
	if filled[owner] {
		demandFilledMu.Unlock()
		return nil, false
	}
	filled[owner] = true
	demandFilledMu.Unlock()
	fillSnapshotsUnder(ctx, owner)
	return CallSnapshotOf(ctx.P, call)
}

// outermostFunctionLike is the function-like ancestor of `token`
// sitting directly under the source file — the owner whose dedicated
// walk lexically covers every call under it (callBelongsToOwner).
// Nil for a top-level token.
func outermostFunctionLike(token *ast.Node) *ast.Node {
	var outermost *ast.Node
	for node := token.Parent; node != nil && !ast.IsSourceFile(node); node = node.Parent {
		if ast.IsFunctionDeclaration(node) || ast.IsArrowFunction(node) ||
			ast.IsFunctionExpression(node) || ast.IsMethodDeclaration(node) {
			outermost = node
		}
	}
	return outermost
}

// fillSnapshotsUnder runs the owner's dedicated walk with a silent
// report sink — the same seeding pass 3 uses: the owner's contract
// when it has one (a synthetic ungrounded contract otherwise), and
// the call-site join for a declared function's unstated parameters.
// Every queryable call the walk reaches records its environment.
func fillSnapshotsUnder(ctx CallSiteCtx, owner *ast.Node) {
	defer func() { recover() }() // a refused question keeps what recorded before it
	flowCtx := &FlowContext{
		P: ctx.P, Kernel: ctx.Kernel, Registry: ctx.Registry, Objects: ctx.Objects, Contracts: ctx.Contracts,
		Report:   func(d assignability.RefinementDiagnostic) {},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
	}
	contract := contractFor(ctx.Contracts, owner)
	if contract == nil {
		contract = &FunctionContract{Declaration: owner}
	}
	var initialStates map[string]abstractdomain.AbstractValue
	if ast.IsFunctionDeclaration(owner) {
		needsJoin := false
		for i := range owner.Parameters() {
			var stated *annotations.DeclaredRefinement
			if i < len(contract.Params) {
				stated = contract.Params[i]
			}
			if stated == nil {
				needsJoin = true
				break
			}
		}
		if needsJoin {
			if env, ok := CallSiteBindings(ctx, owner); ok {
				initialStates = env
			}
		}
	}
	tracing.Count("snapshot.fill", 0)
	AnalyzeFunction(flowCtx, contract, initialStates)
}

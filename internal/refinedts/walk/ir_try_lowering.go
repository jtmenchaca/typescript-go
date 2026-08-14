// `try { … } catch (e) { … }` lowered onto the kernel's PROVED forms —
// no new kernel statement exists for it, and none is needed:
//
//	branchBoth
//	  then: the try block's statements, read whole
//	  else: havoc(every slot the try block could write)
//	        catch-parameter := unknown
//	        the catch block's statements
//
// The opaque branch's run model admits either arm at every state, and
// that freedom is exactly what a try needs. A run that completes the
// try normally IS the then arm. A run that threw ran some PREFIX of the
// try and then the catch — and the else arm's havoc of the try's whole
// write set covers the state after every possible prefix, so walking
// the catch from the havocked state admits every real interrupted run.
// The thrown value itself has no spelling; the catch parameter honestly
// takes unknown.
//
// What still declines: a FINALLY (it runs on every completion, and
// sequencing it after the join would misplace its writes relative to a
// throw that escapes the catch — the corpus holds zero of them), a try
// arm that does not lower (a THROW inside this try declines there — its
// continuation belongs to the catch, which the then arm cannot spell),
// and a catch arm that does not lower.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// LowerTryStatement lowers a try/catch to the opaque branch above, or
// declines to the floor (which refuses try statements and names them —
// exactly today's behavior for every shape this route does not read).
func LowerTryStatement(context *LoweringContext, statement *ast.Node) ([]kernelbridge.IrStatement, bool) {
	if !ast.IsTryStatement(statement) {
		return nil, false
	}
	tryStmt := statement.AsTryStatement()
	if tryStmt.FinallyBlock != nil || tryStmt.CatchClause == nil || tryStmt.TryBlock == nil {
		return nil, false
	}
	tryArm, tryOk := LowerStatements(context, tryStmt.TryBlock.AsBlock().Statements.Nodes)
	if !tryOk {
		return nil, false
	}
	// the interrupted-prefix cover: every slot the try block could
	// write, unknown at the catch's entry
	prefixHavoc, enumerable := havocSlotsOfStatement(context, tryStmt.TryBlock)
	if !enumerable {
		return nil, false
	}
	catchArm := havocAssignments(prefixHavoc)
	clause := tryStmt.CatchClause.AsCatchClause()
	if clause.VariableDeclaration != nil {
		if name := clause.VariableDeclaration.AsVariableDeclaration().Name(); name != nil && ast.IsIdentifier(name) {
			if slot, has := slotIndexOfName(context, name.Text()); has {
				catchArm = append(catchArm, kernelbridge.IrStatement{
					Kind:   kernelbridge.IrStatementAssign,
					Target: slot,
					Effect: kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown},
				})
			}
		}
	}
	caught, catchOk := LowerStatements(context, clause.Block.AsBlock().Statements.Nodes)
	if !catchOk {
		return nil, false
	}
	catchArm = append(catchArm, caught...)
	return []kernelbridge.IrStatement{{
		Kind: kernelbridge.IrStatementBranchBoth,
		Then: tryArm,
		Else: catchArm,
	}}, true
}

// split from ir_summary_body_lowering.go — the constructor prelude

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

// summaryConstructorPrelude builds THE CONSTRUCTOR PRELUDE: a
// constructor's body begins life the runtime already lived — the class's
// field INITIALIZERS have run, and each PARAMETER PROPERTY holds its
// argument. Both are ordinary assignments onto the bundle's own slots,
// emitted ahead of the statements; an initializer the effect grammar
// cannot spell leaves its slot unknown (never a stale absent), and every
// touched field joins Written through the census's own store recognition
// upstream.
func summaryConstructorPrelude(
	context *LoweringContext,
	declaration *ast.Node,
	parameters []*ast.Node,
) []kernelbridge.IrStatement {
	var constructorPrelude []kernelbridge.IrStatement
	if ast.IsConstructorDeclaration(declaration) {
		if classLike := declaration.Parent; classLike != nil && ast.IsClassLike(classLike) {
			for _, member := range classLike.ClassLikeData().Members.Nodes {
				if !ast.IsPropertyDeclaration(member) {
					continue
				}
				property := member.AsPropertyDeclaration()
				if property.Initializer == nil || property.Name() == nil || !ast.IsIdentifier(property.Name()) {
					continue
				}
				slot, has := slotIndexOfName(context, "this."+property.Name().Text())
				if !has {
					continue
				}
				effect, lowered := RhsEffect(context, context.Sorts[slot], property.Initializer)
				if !lowered {
					effect = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}
				}
				constructorPrelude = append(constructorPrelude, kernelbridge.IrStatement{
					Kind: kernelbridge.IrStatementAssign, Target: slot, Effect: effect,
				})
				// A FIELD HOLDING A CLOSURE — `private handler = () => {
				// this.count++ }` — is the one initializer whose slot write is
				// not the end of the story. The prelude ADMITS every field
				// (an unreadable initializer takes unknown rather than
				// declining), so unlike an ordinary statement this one never
				// reaches the havoc floor, and the floor's walk INTO the arrow
				// is what would otherwise havoc the names it writes. Those
				// names are havocked here instead, right after the field's own
				// write: the closure may run at any later time, so nothing
				// after this may believe them. ClosureEscapesTrackedWrite
				// states the boundary rule both sites share.
				if ClosureEscapesTrackedWrite(context, property.Initializer) {
					if written, enumerable := havocSlotsOfStatement(context, property.Initializer); enumerable {
						constructorPrelude = append(constructorPrelude, havocAssignments(written)...)
					}
				}
			}
		}
		for _, parameter := range parameters {
			if !isParameterPropertyDeclaration(parameter) {
				continue
			}
			pd := parameter.AsParameterDeclaration()
			if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) {
				continue
			}
			fieldSlot, hasField := slotIndexOfName(context, "this."+pd.Name().Text())
			if !hasField {
				continue
			}
			effect := kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectUnknown}
			if paramSlot, hasParam := slotIndexOfName(context, pd.Name().Text()); hasParam {
				effect = kernelbridge.LoopEffect{Kind: kernelbridge.LoopEffectVar, Index: paramSlot}
			}
			constructorPrelude = append(constructorPrelude, kernelbridge.IrStatement{
				Kind: kernelbridge.IrStatementAssign, Target: fieldSlot, Effect: effect,
			})
		}
	}
	return constructorPrelude
}

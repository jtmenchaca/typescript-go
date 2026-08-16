// A constructor declaration never registers as a FunctionContract
// (contract_file_facts.go's collector has no ConstructorDeclaration
// case), so no pass ever walks a constructor's own body with LIVE
// reporting: ConstructedInstance's own body walk
// (constructed_instance.go) silences ctx.Report on purpose — it exists
// to compute the VALUE a `new C(...)` holds, not to judge the
// constructor's text — and analyzeFunctionBody is never called on a
// ConstructorDeclaration at all. A field the constructor writes
// out-of-set therefore type-checked silently.
//
// checkConstructorFieldWrites closes that gap narrowly: every
// unconditional, TOP-LEVEL `this.key = value` statement in the
// constructor's own block (skipping into no `if`/loop/try — those
// write conditionally, and a general judged walk of the whole body
// risks reporting an unrelated statement's effects a second time once
// some other route starts walking constructors) checks its right side
// against the field's own declared refinement — the same
// annotations.AnnotationOfType read the class-declaration arm just
// above already runs for a field INITIALIZER, and the same
// CheckAssignability judge WriteBinding and WriteProperty's own
// class-field arm both run for every other declared-type write.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
)

// checkConstructorFieldWrites judges every class's own constructor —
// its top-level `this.key = value` writes against the matching
// PropertyDeclaration's declared type, reported at the write site.
// Takes no caller env: the judge is a standalone read of the
// constructor's own declared text, the same "no call site" standing
// the class-declaration arm's field-initializer check already keeps.
func checkConstructorFieldWrites(ctx *FlowContext, classDeclaration *ast.Node) {
	classData := classDeclaration.ClassLikeData()
	var constructorDeclaration *ast.Node
	for _, member := range classData.Members.Nodes {
		if ast.IsConstructorDeclaration(member) {
			constructorDeclaration = member
			break
		}
	}
	if constructorDeclaration == nil {
		return
	}
	body := constructorDeclaration.Body()
	if body == nil || !ast.IsBlock(body) {
		return
	}
	// a fresh env, the constructor's own parameters bound to their
	// OWN declared-type ground (InitialStateOfPlainParameter — the
	// same "no call site, read the annotation alone" binding a plain
	// function's entry env gives an untracked-argument parameter), so
	// a write reading a parameter (`this.age = age`) judges against
	// what the parameter's own annotation states, not as untracked
	constructorEnv := NewEnv()
	for _, parameter := range constructorDeclaration.Parameters() {
		name := parameter.AsParameterDeclaration().Name()
		if ast.IsIdentifier(name) {
			constructorEnv.Set(name.Text(), InitialStateOfPlainParameter(ctx.P, parameter))
		}
	}
	for _, statement := range body.AsBlock().Statements.Nodes {
		if !ast.IsExpressionStatement(statement) {
			continue
		}
		expression := statement.AsExpressionStatement().Expression
		if !ast.IsBinaryExpression(expression) {
			continue
		}
		bin := expression.AsBinaryExpression()
		if bin.OperatorToken.Kind != ast.KindEqualsToken || !ast.IsPropertyAccessExpression(bin.Left) {
			continue
		}
		pa := bin.Left.AsPropertyAccessExpression()
		if pa.Expression.Kind != ast.KindThisKeyword {
			continue
		}
		name := pa.Name()
		if name == nil || !(ast.IsIdentifier(name) || ast.IsPrivateIdentifier(name)) {
			continue
		}
		fieldDeclaration := propertyDeclarationNamed(classData, name.Text())
		if fieldDeclaration == nil || fieldDeclaration.AsPropertyDeclaration().Type == nil {
			continue
		}
		read := annotations.AnnotationOfType(ctx.P, fieldDeclaration.AsPropertyDeclaration().Type, ctx.Registry, ctx.Objects)
		if read.Unsupported != "" || read.Stated == nil {
			continue
		}
		value := evaluateExpression(ctx, constructorEnv, bin.Right)
		CheckAssignability(ctx, value, *read.Stated, bin.Right, "the field '"+name.Text()+"'", nil)
	}
}

// propertyDeclarationNamed is the class's own non-static
// PropertyDeclaration spelled name — plain or private identifier —
// or nil where none matches.
func propertyDeclarationNamed(classData *ast.ClassLikeBase, name string) *ast.Node {
	for _, member := range classData.Members.Nodes {
		if !ast.IsPropertyDeclaration(member) {
			continue
		}
		if ast.GetCombinedModifierFlags(member)&ast.ModifierFlagsStatic != 0 {
			continue
		}
		memberName := member.Name()
		if memberName == nil || !(ast.IsIdentifier(memberName) || ast.IsPrivateIdentifier(memberName)) {
			continue
		}
		if memberName.Text() == name {
			return member
		}
	}
	return nil
}

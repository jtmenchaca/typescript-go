package checker

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/diagnostics"
)

func (c *Checker) GetStringType() *Type {
	return c.stringType
}

func (c *Checker) GetNumberType() *Type {
	return c.numberType
}

func (c *Checker) GetBooleanType() *Type {
	return c.booleanType
}

func (c *Checker) GetVoidType() *Type {
	return c.voidType
}

func (c *Checker) GetUndefinedType() *Type {
	return c.undefinedType
}

func (c *Checker) GetNullType() *Type {
	return c.nullType
}

func (c *Checker) GetAnyType() *Type {
	return c.anyType
}

func (c *Checker) GetErrorType() *Type {
	return c.errorType
}

func (c *Checker) GetNeverType() *Type {
	return c.neverType
}

func (c *Checker) GetUnknownType() *Type {
	return c.unknownType
}

func (c *Checker) GetBigIntType() *Type {
	return c.bigintType
}

func (c *Checker) GetESSymbolType() *Type {
	return c.esSymbolType
}

func (c *Checker) GetBaseTypeOfLiteralType(t *Type) *Type {
	return c.getBaseTypeOfLiteralType(t)
}

func (c *Checker) GetUnknownSymbol() *ast.Symbol {
	return c.unknownSymbol
}

func (c *Checker) GetUndefinedSymbol() *ast.Symbol {
	return c.undefinedSymbol
}

func (c *Checker) GetArgumentsSymbol() *ast.Symbol {
	return c.argumentsSymbol
}

func (c *Checker) GetUnknownSignature() *Signature {
	return c.unknownSignature
}

func (c *Checker) GetUnionType(types []*Type) *Type {
	return c.getUnionType(types)
}

func (c *Checker) GetNameTypeOfSymbol(symbol *ast.Symbol) *Type {
	if !c.valueSymbolLinks.Has(symbol) {
		return nil
	}
	return c.valueSymbolLinks.TryGet(symbol).nameType
}

func IsTypeUsableAsPropertyName(t *Type) bool {
	return isTypeUsableAsPropertyName(t)
}

func GetPropertyNameFromType(t *Type) string {
	return getPropertyNameFromType(t)
}

func (c *Checker) GetGlobalSymbol(name string, meaning ast.SymbolFlags, diagnostic *diagnostics.Message) *ast.Symbol {
	return c.getGlobalSymbol(name, meaning, diagnostic)
}

func (c *Checker) GetMergedSymbol(symbol *ast.Symbol) *ast.Symbol {
	return c.getMergedSymbol(symbol)
}

func (c *Checker) TryFindAmbientModule(moduleName string) *ast.Symbol {
	return c.tryFindAmbientModule(moduleName, true /* withAugmentations */)
}

func (c *Checker) GetImmediateAliasedSymbol(symbol *ast.Symbol) *ast.Symbol {
	return c.getImmediateAliasedSymbol(symbol)
}

func (c *Checker) GetTypeOnlyAliasDeclaration(symbol *ast.Symbol) *ast.Node {
	return c.getTypeOnlyAliasDeclaration(symbol)
}

func (c *Checker) ResolveExternalModuleName(moduleSpecifier *ast.Node) *ast.Symbol {
	return c.resolveExternalModuleName(moduleSpecifier, moduleSpecifier, true /*ignoreErrors*/)
}

func (c *Checker) ResolveExternalModuleSymbol(moduleSymbol *ast.Symbol) *ast.Symbol {
	return c.resolveExternalModuleSymbol(moduleSymbol, false /*dontResolveAlias*/)
}

func (c *Checker) GetTypeFromTypeNode(node *ast.Node) *Type {
	return c.getTypeFromTypeNode(node)
}

func (c *Checker) IsArrayLikeType(t *Type) bool {
	return c.isArrayLikeType(t)
}

func (c *Checker) GetPropertiesOfType(t *Type) []*ast.Symbol {
	return c.getPropertiesOfType(t)
}

func (c *Checker) GetPropertyOfType(t *Type, name string) *ast.Symbol {
	return c.getPropertyOfType(t, name)
}

func (c *Checker) TypeHasCallOrConstructSignatures(t *Type) bool {
	return c.typeHasCallOrConstructSignatures(t)
}

// Checks if a property can be accessed in a location.
// The location is given by the `node` parameter.
// The node does not need to be a property access.
// @param node location where to check property accessibility
// @param isSuper whether to consider this a `super` property access, e.g. `super.foo`.
// @param isWrite whether this is a write access, e.g. `++foo.x`.
// @param containingType type where the property comes from.
// @param property property symbol.
func (c *Checker) IsPropertyAccessible(node *ast.Node, isSuper bool, isWrite bool, containingType *Type, property *ast.Symbol) bool {
	return c.isPropertyAccessible(node, isSuper, isWrite, containingType, property)
}

func (c *Checker) GetTypeOfPropertyOfContextualType(t *Type, name string) *Type {
	return c.getTypeOfPropertyOfContextualType(t, name)
}

func GetDeclarationModifierFlagsFromSymbol(s *ast.Symbol) ast.ModifierFlags {
	return getDeclarationModifierFlagsFromSymbol(s)
}

func (c *Checker) WasCanceled() bool {
	return c.wasCanceled
}

func (c *Checker) GetSignaturesOfType(t *Type, kind SignatureKind) []*Signature {
	return c.getSignaturesOfType(t, kind)
}

func (c *Checker) GetDeclaredTypeOfSymbol(symbol *ast.Symbol) *Type {
	return c.getDeclaredTypeOfSymbol(symbol)
}

func (c *Checker) GetTypeOfSymbol(symbol *ast.Symbol) *Type {
	return c.getTypeOfSymbol(symbol)
}

func (c *Checker) GetConstraintOfTypeParameter(typeParameter *Type) *Type {
	return c.getConstraintOfTypeParameter(typeParameter)
}

func (c *Checker) GetTrueTypeOfConditionalType(t *Type) *Type {
	return c.getTrueTypeFromConditionalType(t)
}

func (c *Checker) GetFalseTypeOfConditionalType(t *Type) *Type {
	return c.getFalseTypeFromConditionalType(t)
}

func (c *Checker) GetDefaultFromTypeParameter(typeParameter *Type) *Type {
	return c.getDefaultFromTypeParameter(typeParameter)
}

func (c *Checker) GetResolutionModeOverride(node *ast.ImportAttributes, reportErrors bool) core.ResolutionMode {
	return c.getResolutionModeOverride(node, reportErrors)
}

func (c *Checker) GetEffectiveDeclarationFlags(n *ast.Node, flagsToCheck ast.ModifierFlags) ast.ModifierFlags {
	return c.getEffectiveDeclarationFlags(n, flagsToCheck)
}

func (c *Checker) GetBaseConstraintOfType(t *Type) *Type {
	return c.getBaseConstraintOfType(t)
}

func (c *Checker) GetTypePredicateOfSignature(sig *Signature) *TypePredicate {
	return c.getTypePredicateOfSignature(sig)
}

func IsTupleType(t *Type) bool {
	return isTupleType(t)
}

func (c *Checker) IsArrayType(t *Type) bool {
	return c.isArrayType(t)
}

func (c *Checker) GetReturnTypeOfSignature(sig *Signature) *Type {
	return c.getReturnTypeOfSignature(sig)
}

func (c *Checker) HasEffectiveRestParameter(signature *Signature) bool {
	return c.hasEffectiveRestParameter(signature)
}

func (c *Checker) GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(symbol *ast.Symbol) []*Type {
	return c.getLocalTypeParametersOfClassOrInterfaceOrTypeAlias(symbol)
}

func (c *Checker) GetContextualTypeForObjectLiteralElement(element *ast.Node, contextFlags ContextFlags) *Type {
	return c.getContextualTypeForObjectLiteralElement(element, contextFlags)
}

func (c *Checker) TypePredicateToString(t *TypePredicate) string {
	return c.typePredicateToString(t)
}

func (c *Checker) GetExpandedParameters(signature *Signature, skipUnionExpanding bool) [][]*ast.Symbol {
	return c.getExpandedParameters(signature, skipUnionExpanding)
}

func (c *Checker) GetResolvedSignature(node *ast.Node) *Signature {
	return c.getResolvedSignature(node, nil, CheckModeNormal)
}

// Return the type of the given property in the given type, or nil if no such property exists
func (c *Checker) GetTypeOfPropertyOfType(t *Type, name string) *Type {
	return c.getTypeOfPropertyOfType(t, name)
}

func (c *Checker) GetContextualTypeForArgumentAtIndex(node *ast.Node, argIndex int) *Type {
	return c.getContextualTypeForArgumentAtIndex(node, argIndex)
}

func (c *Checker) GetIndexSignaturesAtLocation(node *ast.Node) []*ast.Node {
	return c.getIndexSignaturesAtLocation(node)
}

func (c *Checker) GetResolvedSymbol(node *ast.Node) *ast.Symbol {
	return c.getResolvedSymbol(node)
}

func (c *Checker) GetJsxNamespace(location *ast.Node) string {
	return c.getJsxNamespace(location)
}

func (c *Checker) GetJsxFragmentFactory(location *ast.Node) string {
	entity := c.getJsxFragmentFactoryEntity(location)
	if entity != nil {
		return ast.GetFirstIdentifier(entity).Text()
	}
	return ""
}

func (c *Checker) ResolveName(name string, location *ast.Node, meaning ast.SymbolFlags, excludeGlobals bool) *ast.Symbol {
	return c.resolveName(location, name, meaning, nil, true, excludeGlobals)
}

func (c *Checker) GetSymbolFlags(symbol *ast.Symbol) ast.SymbolFlags {
	return c.getSymbolFlags(symbol)
}

func (c *Checker) GetBaseTypes(t *Type) []*Type {
	return c.getBaseTypes(t)
}

func (c *Checker) GetApparentType(t *Type) *Type {
	return c.getApparentType(t)
}

func (c *Checker) GetBaseConstructorTypeOfClass(t *Type) *Type {
	return c.getBaseConstructorTypeOfClass(t)
}

func (c *Checker) GetRestTypeOfSignature(sig *Signature) *Type {
	return c.getRestTypeOfSignature(sig)
}

func (c *Checker) GetTypeArguments(t *Type) []*Type {
	return c.getTypeArguments(t)
}

func (c *Checker) GetIndexInfoOfType(t *Type, keyType *Type) *IndexInfo {
	return c.getIndexInfoOfType(t, keyType)
}

func (c *Checker) GetIndexInfosOfType(t *Type) []*IndexInfo {
	return c.getIndexInfosOfType(t)
}

func (c *Checker) IsContextSensitive(node *ast.Node) bool {
	return c.isContextSensitive(node)
}

func (c *Checker) FillMissingTypeArguments(typeArguments []*Type, typeParameters []*Type, minTypeArgumentCount int, isJavaScriptImplicitAny bool) []*Type {
	return c.fillMissingTypeArguments(typeArguments, typeParameters, minTypeArgumentCount, isJavaScriptImplicitAny)
}

func (c *Checker) GetMinTypeArgumentCount(typeParameters []*Type) int {
	return c.getMinTypeArgumentCount(typeParameters)
}

func (c *Checker) GetWidenedLiteralType(t *Type) *Type {
	return c.getWidenedLiteralType(t)
}

func (c *Checker) IsTypeAssignableTo(source *Type, target *Type) bool {
	return c.isTypeAssignableTo(source, target)
}

func (c *Checker) GetUnionTypeEx(types []*Type, unionReduction UnionReduction) *Type {
	return c.getUnionTypeEx(types, unionReduction, nil, nil)
}

func (c *Checker) RequiresAddingImplicitUndefined(node *ast.Node) bool {
	enclosingDeclaration := ast.FindAncestor(node, ast.IsDeclaration)
	if enclosingDeclaration == nil {
		enclosingDeclaration = ast.GetSourceFileOfNode(node).AsNode()
	}
	symbol := node.Symbol()
	if symbol == nil {
		return false
	}
	return c.GetEmitResolver().RequiresAddingImplicitUndefined(node, symbol, enclosingDeclaration)
}

func (c *Checker) RemoveMissingOrUndefinedType(t *Type) *Type {
	return c.removeMissingOrUndefinedType(t)
}

func (c *Checker) GetWidenedType(t *Type) *Type {
	return c.getWidenedType(t)
}

func (c *Checker) CompareSymbols(s1, s2 *ast.Symbol) int {
	return c.compareSymbols(s1, s2)
}

// GetConstraintOfType wraps getConstraintOfType: the constraint of a
// type parameter, indexed access, or conditional type, falling back to
// the type's base constraint -- the general form behind
// ts.Type.prototype.getConstraint().
func (c *Checker) GetConstraintOfType(t *Type) *Type {
	return c.getConstraintOfType(t)
}

// GetTypeAlias returns the type's alias -- the TS source's own
// `type.aliasSymbol` question (a resolved type instance still carries
// the alias it was named through, e.g. `type Port = z.infer<typeof
// zPort>`), nil when the type names no alias.
func (t *Type) GetTypeAlias() *TypeAlias {
	return t.alias
}

// SymbolInDefaultLib reports whether a symbol declares in the
// checker's default library (the global Number, Math, ...). Symbols,
// never names -- a local `const Math = ...` is not the library, and
// neither is a user-written declaration file.
func (c *Checker) SymbolInDefaultLib(symbol *ast.Symbol) bool {
	if symbol == nil {
		return false
	}
	for _, d := range symbol.Declarations {
		if c.program.IsSourceFileDefaultLibrary(ast.GetSourceFileOfNode(d).Path()) {
			return true
		}
	}
	return false
}

// SymbolEntirelyInDefaultLib reports whether EVERY declaration of a
// symbol lives in the default library (and there is at least one) --
// the stricter gate a user augmentation of a global must defeat
// (service/program_resolution.ts's symbolEntirelyInDefaultLib).
func (c *Checker) SymbolEntirelyInDefaultLib(symbol *ast.Symbol) bool {
	if symbol == nil || len(symbol.Declarations) == 0 {
		return false
	}
	for _, d := range symbol.Declarations {
		if !c.program.IsSourceFileDefaultLibrary(ast.GetSourceFileOfNode(d).Path()) {
			return false
		}
	}
	return true
}

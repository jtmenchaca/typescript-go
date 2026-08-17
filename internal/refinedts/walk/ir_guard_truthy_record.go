// split from ir_guard.go — the always-truthy record-parameter fold
//
// A record-typed parameter's declared annotation is the ANNOTATION'S
// CLAIM, the same trust grade every declared type in this walk carries
// (declaredParamSort, ir_call_hoist.go's own "THE TRUST GRADE" note): a
// non-optional record annotation says the value is an object at every
// call, and an object is truthy at every call — spec-wise, ToBoolean
// (sec-toboolean, tmp/ecma262/spec.html) returns true for every Object
// argument, no exception.
//
// NON-OPTIONAL IS PROVED SYNTACTICALLY HERE, never inferred from the
// expansion succeeding: unionMembersOf now EXPANDS a `Record |
// undefined` union (the absent arm is excluded and every leaf wears
// MayBeAbsent), so "it expanded" stopped implying "it cannot be
// absent". annotationExcludesAbsent walks the annotation's own syntax —
// a union carrying an `undefined`/`null` arm, and any annotation this
// reading cannot positively clear, answers false and the fold stays
// off.

package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
)

// truthyRecordParameterName answers the bare identifier NAME that
// CONDITION spells, where that name is a NON-OPTIONAL record-typed
// parameter of the enclosing function — a value ToBoolean always reads
// as true. "" where condition is not this shape.
//
// Scoped to a BARE identifier only: `entry.value` is a member read, not
// the whole record, and takes its own ordinary reading; `entry` wrapped
// in anything this file's Unwrapped does not already peel (a cast, a
// paren) still resolves through Unwrapped before this is asked.
func truthyRecordParameterName(condition *ast.Node) string {
	if condition == nil || !ast.IsIdentifier(condition) {
		return ""
	}
	enclosing := enclosingFunctionLikeOf(condition)
	if enclosing == nil {
		return ""
	}
	name := condition.Text()
	for _, parameter := range enclosing.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) || pd.Name().Text() != name {
			continue
		}
		// a DEFAULTED or a `?`-marked parameter admits undefined on some
		// call even where the annotation itself does not spell it —
		// neither is this shape
		if pd.QuestionToken != nil || pd.Initializer != nil {
			return ""
		}
		if !annotationExcludesAbsent(pd.Type) {
			return ""
		}
		if _, expanded := recordParamMembersOf(parameter); expanded {
			return name
		}
		return ""
	}
	return ""
}

// undefinedOnlyRecordParameterName answers the bare identifier NAME
// where that name is an EXPANDED record-typed parameter whose only
// absent arm is `undefined` — `entry: LinkDataItem | SankeyNode |
// undefined`, or `entry?: Bounds`. "" for every other shape, a `null`
// arm included: `entry && entry.value` on an absent holder yields the
// HOLDER's own absent value, and a leaf slot's absent admission spells
// undefined — the right claim for an undefined holder and the wrong one
// for null.
func undefinedOnlyRecordParameterName(condition *ast.Node) string {
	if condition == nil || !ast.IsIdentifier(condition) {
		return ""
	}
	enclosing := enclosingFunctionLikeOf(condition)
	if enclosing == nil {
		return ""
	}
	name := condition.Text()
	for _, parameter := range enclosing.Parameters() {
		pd := parameter.AsParameterDeclaration()
		if pd.Name() == nil || !ast.IsIdentifier(pd.Name()) || pd.Name().Text() != name {
			continue
		}
		// a DEFAULTED parameter never holds undefined past the prelude —
		// its value is the default's, which this reading does not follow
		if pd.Initializer != nil {
			return ""
		}
		if !annotationAbsentIsUndefinedOnly(pd.Type) {
			return ""
		}
		if _, expanded := recordParamMembersOf(parameter); expanded {
			return name
		}
		return ""
	}
	return ""
}

// annotationAbsentIsUndefinedOnly: every union arm either excludes
// absent or IS the undefined keyword — no null anywhere. A non-union
// annotation must exclude absent outright (a `?`-marked parameter's
// undefined comes from the marker, not the annotation, and is still
// undefined-only).
func annotationAbsentIsUndefinedOnly(annotation *ast.Node) bool {
	if annotation == nil {
		return false
	}
	node := annotation
	if node.Kind == ast.KindParenthesizedType {
		node = node.AsParenthesizedTypeNode().Type
	}
	if ast.IsUnionTypeNode(node) {
		arms := node.AsUnionTypeNode().Types
		if arms == nil || len(arms.Nodes) == 0 {
			return false
		}
		for _, arm := range arms.Nodes {
			if arm.Kind == ast.KindUndefinedKeyword {
				continue
			}
			if !annotationExcludesAbsent(arm) {
				return false
			}
		}
		return true
	}
	return annotationExcludesAbsent(node)
}

// annotationExcludesAbsent: the annotation's own syntax positively rules
// out an absent value. A type literal and a plain (unparameterized) type
// reference name object shapes; a parenthesized annotation reads
// through; a UNION excludes absent exactly when every arm does. Anything
// else — an `undefined`/`null`/`void`/`any`/`unknown` arm, a keyword,
// a shape this reading does not recognize — answers false: the fold
// needs a proof, and no proof means no fold.
func annotationExcludesAbsent(annotation *ast.Node) bool {
	if annotation == nil {
		return false
	}
	node := annotation
	if node.Kind == ast.KindParenthesizedType {
		node = node.AsParenthesizedTypeNode().Type
	}
	switch {
	case ast.IsTypeLiteralNode(node):
		return true
	case ast.IsTypeReferenceNode(node):
		// a resolvable plain name is an interface or alias the member
		// reader will judge; `any`/`unknown`/`undefined` are keywords, not
		// references, and never reach this arm
		reference := node.AsTypeReferenceNode()
		return reference.TypeArguments == nil || len(reference.TypeArguments.Nodes) == 0
	case ast.IsUnionTypeNode(node):
		arms := node.AsUnionTypeNode().Types
		if arms == nil || len(arms.Nodes) == 0 {
			return false
		}
		for _, arm := range arms.Nodes {
			if !annotationExcludesAbsent(arm) {
				return false
			}
		}
		return true
	}
	return false
}

// split from ir_guard.go — the always-truthy record-parameter fold
//
// A record-typed parameter's declared annotation is the ANNOTATION'S
// CLAIM, the same trust grade every declared type in this walk carries
// (declaredParamSort, ir_call_hoist.go's own "THE TRUST GRADE" note): a
// non-optional record annotation says the value is an object at every
// call, and an object is truthy at every call — spec-wise, ToBoolean
// (sec-toboolean, tmp/ecma262/spec.html) returns true for every Object
// argument, no exception. `recordParamMembersOf` already decides
// non-optional for this exact shape: a union arm naming `undefined`/
// `null` makes the arms' member intersection empty, and an empty
// intersection AT AN ENTRY declines the expansion whole
// (unionMembersOf, ir_summary_composite_type_members.go) — so a
// parameter that reaches this fold has already been proven, by the same
// rule the record-parameter layout itself trusts, to admit no absent
// value.

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
		if _, expanded := recordParamMembersOf(parameter); expanded {
			return name
		}
		return ""
	}
	return ""
}

// from annotations/worn_annotation.ts
//
// What a compiled schema hands a consumer: the declared set or
// object shape, maybe-wrapped if the chain admits absent,
// library-graded if a library adapter (object schemas always).
// A date schema is a Date whose millis wear the stated window —
// callers do not re-derive that wrap. Parse, await-parseAsync,
// transform/codec pins, and the transform image all ask here.
//
// This file lives in walk, not annotations: it calls
// AbstractValueOfDeclared (assignability's walk-half), and
// annotations cannot import walk without closing the cycle walk →
// annotations. The TS home stays annotations/worn_annotation.ts.

package walk

import (
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
)

// WornOfAnnotation is the value a compiled annotation states. Kind
// tag, measures, and temporal ride with the set. A date schema is
// the Date object whose time value is that set — the host runtime,
// so millis sit at library grade.
func WornOfAnnotation(compiled annotations.Annotation) abstractdomain.AbstractValue {
	if compiled.Date {
		millis := abstractdomain.AtTrustLevel(
			abstractdomain.KnownSet(*compiled.Set, compiled.Temporal,
				abstractdomain.TrustProved, ""),
			abstractdomain.TrustLibrary,
		)
		dated := abstractdomain.AbstractValue{
			Kind:   abstractdomain.KindDate,
			Millis: &millis,
		}
		if compiled.Absent {
			return abstractdomain.PossiblyUndefined(dated,
				abstractdomain.TrustProved, false, false)
		}
		return dated
	}
	var measures *annotations.Measures
	if compiled.Measures != nil {
		measures = compiled.Measures
	}
	stated := AbstractValueOfDeclared(annotations.DeclaredRefinement{
		Kind:     annotations.DeclaredSet,
		Set:      compiled.Set,
		Temporal: compiled.Temporal,
		KindTag:  compiled.KindTag,
		Measures: measures,
	})
	worn := stated
	if compiled.Absent {
		worn = abstractdomain.PossiblyUndefined(stated,
			abstractdomain.TrustProved, false, false)
	}
	if compiled.LibraryAdapter == "" {
		return worn
	}
	return abstractdomain.AtTrustLevel(worn, abstractdomain.TrustLibrary)
}

// WornOfObject is an object schema's stated shape, always at library
// grade — object schemas are the library's runtime.
func WornOfObject(object *annotations.ObjectAnnotation) abstractdomain.AbstractValue {
	return abstractdomain.AtTrustLevel(
		AbstractValueOfDeclared(annotations.DeclaredRefinement{
			Kind:   annotations.DeclaredObject,
			Object: object,
		}),
		abstractdomain.TrustLibrary,
	)
}

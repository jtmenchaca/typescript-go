// from evaluation/maybe_receiver_access.ts
//
// THE optional-chain rule, stated once for every link form
// (finding 5): a link through an ABSENT receiver short-circuits to
// undefined; through a MAYBE receiver it reads the present side
// and the result wears the maybe. Nil where the receiver is
// neither — the link reads normally.

package walk

import "github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"

// ReadThroughMaybeReceiver applies the optional-chain rule.
func ReadThroughMaybeReceiver(
	receiver abstractdomain.AbstractValue,
	readPresent func(inner abstractdomain.AbstractValue) abstractdomain.AbstractValue,
) *abstractdomain.AbstractValue {
	if receiver.Kind == abstractdomain.KindUndef {
		out := abstractdomain.Undef
		return &out
	}
	if receiver.Kind == abstractdomain.KindPossiblyUndefined {
		// sec-optional-chaining-evaluation: "If baseValue is either
		// undefined or null, then Return undefined" — the short-circuit
		// answers EXACTLY undefined regardless of which one the receiver
		// held, never null. The wrapper's own absent side is therefore
		// UndefOnly, not the pre-flavor conflated claim (which would
		// wrongly let a flavored consumer treat this result as possibly
		// null).
		out := abstractdomain.PossiblyAbsent(readPresent(*receiver.Inner), abstractdomain.AbsentFlavorUndefOnly, "", false, false)
		return &out
	}
	return nil
}

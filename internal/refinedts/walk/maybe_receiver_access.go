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
		out := abstractdomain.PossiblyUndefined(readPresent(*receiver.Inner), "", false, false)
		return &out
	}
	return nil
}

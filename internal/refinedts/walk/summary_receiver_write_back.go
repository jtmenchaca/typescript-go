// The served-call WRITE-BACK: a COMPLETE summary's written this-fields
// carry their EXIT values back onto the caller's tracked receiver
// object, in place of the whole-receiver forget.
//
// The forget was the sound floor and it stays the fallback — but for a
// receiver the caller tracks as a KindObject under a plain name, the
// kernel's applySummary answer already carries the written fields' exit
// states (the whole out row crosses the wire), and wiping the object
// threw that answer away: `over.write(200)` then `over.read()` answered
// nothing while states[#held] on the wire said exactly 200.

package walk

import (
	"sort"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
)

// foldWrittenReceiverExits updates the receiver's tracked object with
// the served summary's written-field exit values. Answers whether the
// fold happened; false means the caller must apply the forget instead.
//
// The shapes that keep the forget: a receiver that is not a plain
// identifier (a chain, a call's result — no one binding to reseed), a
// name not tracked as a KindObject, and every case
// SummaryWrittenThisExits itself declines (a porous or
// receiver-returning body, a TOP or thrown exit).
//
// Aliases are forgotten FIRST, through the same ForgetThrough the
// opaque path applies — an alias's copy of the object is stale the
// moment the write lands — and then the receiver's own binding reseeds
// with the written fields replaced and every untouched key kept: a
// COMPLETE summary's bundle rows are the whole story of what the body
// moved, so a key outside them held.
func foldWrittenReceiverExits(
	ctx *FlowContext,
	env Env,
	receiverExpr *ast.Node,
	contract *FunctionContract,
	argKnowns []abstractdomain.AbstractValue,
	receiver abstractdomain.AbstractValue,
) bool {
	root := Unwrapped(receiverExpr)
	if root == nil || !ast.IsIdentifier(root) {
		return false
	}
	name := root.Text()
	current, held := env.Get(name)
	if !held || current.Kind != abstractdomain.KindObject {
		return false
	}
	exits, ok := SummaryWrittenThisExits(ctx, contract.Declaration, argKnowns, receiver)
	if !ok {
		return false
	}
	ForgetThrough(ctx, env, receiverExpr)
	keys := append([]abstractdomain.ObjectKey{}, current.Keys...)
	for at := range keys {
		if moved, wrote := exits[keys[at].Name]; wrote {
			keys[at].Value = moved
			delete(exits, keys[at].Name)
		}
	}
	// a written field the object did not name yet grows a key — in a
	// stable order, so two runs spell one object
	grown := make([]string, 0, len(exits))
	for fieldName := range exits {
		grown = append(grown, fieldName)
	}
	sort.Strings(grown)
	for _, fieldName := range grown {
		keys = append(keys, abstractdomain.ObjectKey{Name: fieldName, Value: exits[fieldName]})
	}
	updated := current
	updated.Keys = keys
	env.Set(name, updated)
	return true
}

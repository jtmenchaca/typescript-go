// REFERENCE IDENTITY: what `m === known` says about m.
//
// Strict equality on two Objects is decided by IsStrictlyEqual, whose
// object case is "If x and y are the same Object value, return *true*"
// (sec-isstrictlyequal step 4, via SameValueNonNumber's Object clause:
// "If x is y, return *true*; otherwise return *false*"). There is no
// structural comparison anywhere in that path — a JS Map, Set, array,
// or plain object compares ONLY by reference. So a held `m === known`
// says something total: on that arm, m and known ARE one object, and
// every fact the walk holds about known's contents is a fact about m's
// contents, because there is only one set of contents.
//
// That is what this file transfers. On the held side of an identity
// equality between a tracked binding and an expression the walk
// evaluates to a value it can name, the binding takes on that value.
// The transfer is exact, not a join: identity is not a refinement of
// the binding's old knowledge, it is a statement that the binding
// names the very object the other side names.
//
// WHY THE OTHER SIDE MUST BE A NAMED OBJECT VALUE. The transfer is
// sound for any pair, but it only STATES anything when one side is
// better known than the other. The gate is therefore on the source: it
// must be a PLAIN NAME (identityRow's own doc gives the two reasons)
// that evaluates to a kind whose contents the walk actually carries —
// today a built collection, an exact list, or an object with a complete
// key set. A source the walk cannot name transfers nothing, and the
// binding keeps what it had.
//
// WHY ONLY ONE DIRECTION IS WRITTEN. Identity is symmetric, and the
// SOURCE would equally take on the target's value. Writing the reverse
// row would replace a well-known module const's contents with a
// parameter's unread type on the true arm — strictly worse knowledge
// for the same runtime object. So each side is offered as a source and
// the better-known one wins; where both are named, neither is written,
// since neither is an improvement on the other and picking by syntactic
// position would be arbitrary.
//
// A DIFFERENCE STATES NOTHING. `m !== known` held says the two are
// different objects. It does not say what m holds — a second Map with
// the same entries is a different object and passes the test. So no row
// is written where the leaf's polarity states a difference. The `!==`
// fixture shape reads its fact through its FALSE arm: there the leaf
// arrives NEGATED, and a negated `!==` is a held identity, exactly the
// fact a non-negated `===` states. Both spellings are read here, each
// under the one polarity that holds identity.
package walk

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/conditiontree"
)

// IdentityNarrowing is one row IdentityNarrowings answers: a binding
// and the value the identity equality proved it names.
type IdentityNarrowing struct {
	Binding string
	Known   abstractdomain.AbstractValue
}

// IdentityNarrowings answers what a condition's held identity
// equalities prove: for each `a === b` leaf where one side is a plain
// identifier the environment tracks and the other evaluates to a
// nameable object value, the identifier's own knowledge becomes that
// value.
//
// negated mirrors the other guard readers in this package: pass false
// for the condition's held side, true for its refuted side. A leaf
// writes a row when its polarity HOLDS identity — a non-negated `===`
// or a negated `!==` — see the header on why a stated difference
// writes nothing.
//
// The source side is evaluated through the walk, so a module-level
// const holding a built collection reaches its value through
// UntrackedIdentifier's own const-follow. Evaluation is on the ENTRY
// environment the caller hands in, which is the state both sides of
// the comparison were read in.
func IdentityNarrowings(
	ctx *FlowContext,
	env Env,
	condition *ast.Node,
	negated bool,
) []IdentityNarrowing {
	var rows []IdentityNarrowing
	for _, leaf := range conditiontree.ConjunctiveLeaves(conditiontree.ConditionTreeOf(condition, negated)) {
		left, right, ok := identityEqualitySides(leaf.Test, leaf.Negated)
		if !ok {
			continue
		}
		if row, wrote := identityRow(ctx, env, left, right); wrote {
			rows = append(rows, row)
			continue
		}
		if row, wrote := identityRow(ctx, env, right, left); wrote {
			rows = append(rows, row)
		}
	}
	return rows
}

// identityEqualitySides reads one leaf into the two operands of a
// STRICT equality that the leaf's polarity HOLDS, or ok=false for
// anything else.
//
// Two spellings state a held identity, and which one does depends on
// the polarity the leaf arrives under. A NON-negated `===` says the
// two sides are one object. A NEGATED `!==` says the same thing: the
// leaf is `a !== b` tested refuted, and the refutation of "these are
// different objects" is "these are the same object". Those are the
// only two combinations. A negated `===` and a non-negated `!==` both
// state a difference, and a difference states nothing about contents —
// a second Map with the same entries is a different object and passes
// the test — so neither writes a row.
//
// Only STRICT spellings are read. Loose equality between two Objects
// reaches the same SameValueNonNumber comparison (sec-islooselyequal
// step 1 sends same-type operands to IsStrictlyEqual), but a loose
// equality whose sides are not both Objects coerces, and this reader
// does not settle the sides' types — `===`/`!==` are the spellings
// that need no such settlement.
func identityEqualitySides(test *ast.Node, negated bool) (*ast.Node, *ast.Node, bool) {
	bare := test
	for bare != nil && ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if bare == nil || !ast.IsBinaryExpression(bare) {
		return nil, nil, false
	}
	binary := bare.AsBinaryExpression()
	var identityHeld bool
	switch binary.OperatorToken.Kind {
	case ast.KindEqualsEqualsEqualsToken:
		identityHeld = !negated
	case ast.KindExclamationEqualsEqualsToken:
		identityHeld = negated
	default:
		return nil, nil, false
	}
	if !identityHeld {
		return nil, nil, false
	}
	return binary.Left, binary.Right, true
}

// identityRow builds the row transferring `source`'s value onto
// `target`, when target is a tracked plain identifier and source names
// a value worth transferring.
//
// Two conditions beyond the kind gate. The target must already be in
// the environment — a name the walk does not track has no entry for the
// arm to write, and inventing one would state a binding the join
// machinery never laid out. And the target must not already hold a
// value of a transferable kind: where it does, the two sides are both
// named and neither is an improvement (the header's symmetry note).
func identityRow(
	ctx *FlowContext,
	env Env,
	target *ast.Node,
	source *ast.Node,
) (IdentityNarrowing, bool) {
	bare := target
	for bare != nil && ast.IsParenthesizedExpression(bare) {
		bare = bare.AsParenthesizedExpression().Expression
	}
	if bare == nil || !ast.IsIdentifier(bare) {
		return IdentityNarrowing{}, false
	}
	name := bare.Text()
	held, tracked := env.Get(name)
	if !tracked {
		return IdentityNarrowing{}, false
	}
	if transferableIdentityValue(held) {
		return IdentityNarrowing{}, false
	}
	bareSource := source
	for bareSource != nil && ast.IsParenthesizedExpression(bareSource) {
		bareSource = bareSource.AsParenthesizedExpression().Expression
	}
	// the source must be a PLAIN NAME. Two reasons, and either alone
	// is enough. A name runs no code, and this reader is called once
	// per branch side — a source that could run something would run it
	// again on each, which is a second execution of an expression the
	// condition already evaluated. And identity between two names is
	// the shape the transfer is about: a call's result is a fresh value
	// each time it runs, so a binding proved identical to one call's
	// result has learned nothing that survives the next call.
	if bareSource == nil || !ast.IsIdentifier(bareSource) {
		return IdentityNarrowing{}, false
	}
	value := evaluateExpression(ctx, env, bareSource)
	if !transferableIdentityValue(value) {
		return IdentityNarrowing{}, false
	}
	return IdentityNarrowing{Binding: name, Known: value}, true
}

// transferableIdentityValue answers whether a value names contents the
// walk actually carries — the gate on what an identity transfer is
// worth writing.
//
// A built collection carries its entries; an exact list carries its
// items; an object with a COMPLETE key set carries every key it has.
// Each of those is knowledge about the one object both sides name, and
// so passes to the other side whole. An INCOMPLETE object is excluded:
// its unnamed keys are exactly the part the transfer would be silent
// about, and writing it over a target would state a key set that is not
// the object's.
//
// A COLLECTION is held to the same reading, and the entryless
// incomplete one is what makes the distinction matter. A declared
// `Map<string, number>` parameter now reads as exactly that — the
// collection KIND with no entries and Complete false
// (typereading/host_type.go's default-lib branch) — and such a value
// names not one entry. Answering true for it made every declared map
// parameter look "already named" at identityRow's symmetry gate, which
// then refused to transfer a module const's real entries onto it: the
// arm under `m === known` kept the parameter's empty record and read
// `m.get("a")` off nothing. So a collection transfers, and is
// transferable-INTO, only where it actually carries something — an
// entry, or the Complete proof that having none is itself the fact.
//
// Every other kind — a primitive, a set, an unknown — is either not an
// Object at all (so no two bindings can name one through identity in
// the sense this file transfers) or carries nothing to transfer.
func transferableIdentityValue(value abstractdomain.AbstractValue) bool {
	switch value.Kind {
	case abstractdomain.KindCollection:
		return len(value.Entries) > 0 || value.Complete
	case abstractdomain.KindList:
		return true
	case abstractdomain.KindObject:
		return value.Complete
	default:
		return false
	}
}

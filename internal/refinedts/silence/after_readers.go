// Unknown after every reader that could have answered was asked.
// Walk knowledge outranks the type; the seed fills silence only.
// Cuts and opaque values do not seed (PV2-020/021, PV2-010..012).

package silence

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
	"github.com/microsoft/typescript-go/internal/refinedts/typereading"
)

// SilenceRole is the TS source's `"model" | "join" | "cut" | "opaque"`
// union.
type SilenceRole string

const (
	RoleModel  SilenceRole = "model"
	RoleJoin   SilenceRole = "join"
	RoleCut    SilenceRole = "cut"
	RoleOpaque SilenceRole = "opaque"
)

// AfterReaders is afterReaders in the TS source. The TS `p:
// CheckerProgram` parameter (its `p.host.getTypeAtLocation` call) is
// `c *checker.Checker` directly, per PORT.md's host/checker
// convention — the checker IS the host, in-process.
//
// ResidueReason's lifecycle across the re-seed (the `typereading.
// ReadHostType` call below, reached once ArrivedUnchecked is false):
// an incoming KindUnknown held may carry a reason (silence.ResidueOf's
// provenance sentence), and that reason is dropped, not threaded, when
// the host-type seed replaces the value. This is sound rather than
// lossy for the same reason evaluate_call_expression.go's
// wornReturnTypeIfUnknown documents for the worn return type: ok=true
// on that call only for an already-determined value — KnownValues,
// KnownSet, KnownList, KnownObject, HostFunction, BooleanCodes/
// StringGround/NumberWithNaN's scalar grounds, or PresentUnion's
// joined arms — never KindUnknown (readHostTypeUncached's union
// branch's own unknown-collapse is caught by PresentUnion and turned
// back to ok=false before it reaches here, the same guard
// KindUnionOf/typeGroundOf give the return-type site). A depth-limit
// or unrecognized-shape miss returns ok=false, which this function
// answers by returning `held` untouched, reason intact. So a
// replacement here never leaves the result at KindUnknown, and
// check_assignability.go's `known.Kind == KindUnknown` branch — the
// only reader of ResidueReason (CheckAssignabilityOfArm,
// check_assignability.go) — is never reached by a seeded value: the
// reason dies WITH the unknown it described, because that unknown is
// gone.
//
// A seeded value CAN still end undetermined later, at a DIFFERENT
// report site: checkSetMembershipOfArm (set_membership.go) asks the
// kernel whether a determined seed lies within a stated set, and a
// declined or kernel-less ask reports the tree's own fixed
// KernelDeclinedAlertText (set_membership.go's `!reported.decided`
// branch) — a report that never reads ResidueReason at all, kernel
// present or not. So the seed's own undetermined-downstream outcome
// is answered by that site's fixed sentence, not by the reason this
// function let go of.
func AfterReaders(
	held abstractdomain.AbstractValue,
	c *checker.Checker,
	at *ast.Node,
	role SilenceRole,
) abstractdomain.AbstractValue {
	if role == "" {
		role = RoleModel
	}
	if role == RoleCut {
		return held
	}
	if role == RoleOpaque {
		return abstractdomain.Opaque
	}
	if held.Kind == abstractdomain.KindUnknown && held.Opaque {
		return held
	}
	presentUnknown := held.Kind == abstractdomain.KindUnknown ||
		(held.Kind == abstractdomain.KindPossiblyUndefined && held.Inner != nil && held.Inner.Kind == abstractdomain.KindUnknown)
	if !presentUnknown {
		return held
	}
	if ast.IsIdentifier(at) && typereading.ArrivedUnchecked(c, at) {
		return held
	}
	tracing.CountBy("host.typeAtLocation.direct", 1)
	seeded, ok := typereading.ReadHostType(c, c.GetTypeAtLocation(at), at, 0)
	if !ok {
		return held
	}
	if held.Kind == abstractdomain.KindPossiblyUndefined {
		present := seeded
		if seeded.Kind == abstractdomain.KindPossiblyUndefined && seeded.Inner != nil {
			present = *seeded.Inner
		}
		out := held
		out.Inner = &present
		return out
	}
	return seeded
}

// SeededBinding is seededBinding in the TS source.
func SeededBinding(
	c *checker.Checker,
	held abstractdomain.AbstractValue,
	at *ast.Node,
) abstractdomain.AbstractValue {
	return AfterReaders(held, c, at, RoleModel)
}

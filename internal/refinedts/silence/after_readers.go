// Unknown after every reader that could have answered was asked.
// Walk knowledge outranks the type; the seed fills silence only.
// Cuts and opaque values do not seed (PV2-020/021, PV2-010..012).

package silence

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
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

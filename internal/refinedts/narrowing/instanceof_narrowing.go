// `instanceof` against a default-library constructor, and the
// decline sentences for tests the walk did not read.

package narrowing

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/refinedts/abstractdomain"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/tracing"
)

// ambientConstructor is whether a constructor is a DEFAULT-LIBRARY
// class — the only kind whose %Symbol.hasInstance% is provably the
// inherited default, so `instanceof` holding IS OrdinaryHasInstance and
// proves an Object (the same gate operators.ts keeps for the value
// row). A THIRD-PARTY ambient class may override hasInstance at
// runtime, and a class declared in this program has a body the walk
// can read — neither earns the object claim here.
func ambientConstructor(c *checker.Checker, callee *ast.Node) bool {
	tracing.CountBy("host.symbolAtLocation", 1)
	tracing.CountBy("host.symbolInDefaultLib", 1)
	return c.SymbolEntirelyInDefaultLib(c.GetSymbolAtLocation(callee))
}

// InstanceofLeaf is instanceofLeaf in the TS source: `x instanceof C` —
// (BranchNarrowings{}, false) when the expression is not instanceof.
func InstanceofLeaf(c *checker.Checker, e *ast.Node, isTracked func(name string) bool) (BranchNarrowings, bool) {
	bin := e.AsBinaryExpression()
	if bin.OperatorToken.Kind != ast.KindInstanceOfKeyword {
		return BranchNarrowings{}, false
	}
	// holding proves x is an Object — OrdinaryHasInstance returns
	// false for every non-Object (sec-instanceofoperator). Only an
	// AMBIENT constructor answers: a class declared in this program
	// has a readable constructor saying what its instances hold, and
	// until the walk reads that, the row is a gap and not a bare object.
	tested := dataflowfacts.TrackedPlaceOfWith(c, bin.Left, isTracked)
	if tested == nil || !ambientConstructor(c, bin.Right) {
		return None, true
	}
	anObject := Narrowed{
		Binding:  tested.Binding,
		Path:     tested.Path,
		Shape:    abstractdomain.KnownObject(nil, nil, false, abstractdomain.TrustSpec, false),
		HasShape: true,
	}
	branches := BranchNarrowings{WhenTrue: []Narrowed{anObject}}
	// the SYMMETRIC half: the test answering FALSE proves the
	// constructor's prototype is nowhere on the value's prototype chain,
	// so a kind union drops the arm that brand names. Only a constructor
	// whose brand the domain spells as its own value kind can be
	// refuted — the others answer nothing about the arms this walk
	// holds, and their false arm states nothing rather than a claim the
	// reading cannot carry.
	if brand := refutableBrandOf(bin.Right); brand != "" {
		branches.WhenFalse = []Narrowed{{
			Binding:      tested.Binding,
			Path:         append([]string{}, tested.Path...),
			RefutedBrand: brand,
		}}
	}
	return branches, true
}

// refutableBrandOf is the default-library constructor name whose kind
// the abstract domain spells directly, or "" for every other
// constructor. The caller has already established that the callee is
// entirely default-library (ambientConstructor), which is what makes
// step 3's %Symbol.hasInstance% lookup provably the inherited default
// and the prototype-chain walk of sec-ordinaryhasinstance the whole
// story; this narrows further to the five whose refutation the domain's
// own kinds can act on.
func refutableBrandOf(callee *ast.Node) string {
	if !ast.IsIdentifier(callee) {
		return ""
	}
	switch callee.Text() {
	case "Map", "Set", "Array", "Date", "RegExp":
		return callee.Text()
	}
	return ""
}

// InstanceofReason is instanceofReason in the TS source: decline
// sentences for instanceof (finding 9).
func InstanceofReason(e *ast.Node) (said string, unsupported bool, ok bool) {
	if !ast.IsBinaryExpression(e) || e.AsBinaryExpression().OperatorToken.Kind != ast.KindInstanceOfKeyword {
		return "", false, false
	}
	// the recognizer reads instanceof as a shape on a tracked
	// place — firing here means the place or the class fell out
	return "an instanceof test the walk did not read — the tested " +
		"place is untracked, or the class unresolved", true, true
}

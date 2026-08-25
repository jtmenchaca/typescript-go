// from service/check.ts
//
// checkObjectGraphs: pass-1b's loop — the keys' cardinality paths are
// exactly what the kernel's judgment reads. A fired fault refutes the
// graph outright; those are theorems, so they are reported where the
// object is stated.

package service

import (
	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
	"github.com/microsoft/typescript-go/internal/refinedts/objectgraphs"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
	"github.com/microsoft/typescript-go/internal/refinedts/walk"
)

// checkObjectGraphs is check.ts's pass-1b loop: the keys' cardinality
// paths are exactly what the kernel's judgment reads. A fired fault
// refutes the graph outright; those are theorems, so they are
// reported where the object is stated.
func checkObjectGraphs(
	p *program.CheckerProgram,
	objects annotations.ObjectRegistry,
	kernel *kernelbridge.RefinedTSKernel,
	report func(d assignability.RefinementDiagnostic),
) {
	for symbol, object := range objects {
		var declaration *ast.Node
		if len(symbol.Declarations) > 0 {
			declaration = symbol.Declarations[0]
		}
		var site *ast.Node
		if declaration != nil && ast.IsVariableDeclaration(declaration) && declaration.AsVariableDeclaration().Initializer != nil {
			site = declaration.AsVariableDeclaration().Initializer
		} else {
			site = declaration
		}
		if site == nil {
			continue
		}
		// an imported object's graph is checked where it is STATED
		if ast.GetSourceFileOfNode(site) != p.Entry {
			continue
		}
		assembled := annotations.SpecificationOf(object, objects)
		if assembled.Unresolved != nil {
			report(assignability.At(assembled.Unresolved, 7004, "z.ref names a schema the checker did not read as an object"))
			continue
		}
		// a decline is an outcome, not an incident: a graph judgment
		// over the kernel's budget alerts at the statement, never
		// crashes — the kernel closure PANICS on a refused question
		// (PORT.md's kernel-panic convention), recovered here the way
		// the TS source's try/catch does. The alert states the plain
		// fact (walk.KernelDeclinedAlertText), never the raw Go panic
		// text the recover caught.
		verdict, declined := checkAssignabilityRecovered(kernel, assembled.Spec)
		if declined {
			report(assignability.At(site, 7002, walk.KernelDeclinedAlertText))
			continue
		}
		if !verdict.Structural {
			report(assignability.At(site, 7004, "this object's graph is not well formed"))
			continue
		}
		for _, fault := range verdict.Faults {
			key := keyNameOfPath(assembled.Spec, int(fault.Path))
			if key == "" {
				report(assignability.At(site, 7003, fault.MessageText))
			} else {
				report(assignability.At(site, 7003, "the key '"+key+"': "+fault.MessageText))
			}
		}
	}
}

// checkAssignabilityRecovered wraps objectgraphs.CheckAssignability's
// panic-on-decline (RefinedTSKernel's question methods panic on a
// refused question, mirroring the TS source's `throw`) into a
// (verdict, declined) pair — declined is false on success. The panic's
// own text is discarded here (never a checker fact about the graph
// being judged); the caller reports the tree's own plain decline
// sentence instead.
func checkAssignabilityRecovered(kernel *kernelbridge.RefinedTSKernel, spec objectgraphs.Specification) (verdict kernelbridge.JudgeAnswer, declined bool) {
	defer func() {
		if recover() != nil {
			declined = true
		}
	}()
	verdict = objectgraphs.CheckAssignability(kernel, spec, nil)
	return verdict, false
}

// keyNameOfPath is keyNameOfPath in the TS source: which key a
// fault's path belongs to — the object markings carry the name, so a
// graph fault reads as a fault about a key.
func keyNameOfPath(spec objectgraphs.Specification, pathIndex int) string {
	for _, marking := range spec.Objects {
		for _, key := range marking.Keys {
			if key.Path == pathIndex {
				return key.Name
			}
		}
	}
	return ""
}

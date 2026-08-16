// Pins the five e-class-and-function.ts class-member rows this unit
// fixes: privateFieldThroughConstructor, staticField, getterRead,
// setterWrite, staticBlock. Each runs the real checker-backed
// AnalyzeFunction path (parseVocabRun's own recipe,
// parse_and_chain_vocabulary_test.go) with the ISOLATED kernel
// parseVocabAssignabilityKernel seats — ctx.Kernel alone, none of the
// three global seats (SetEngineKernel/SetTransferKernel/
// narrowing.SetNarrowKernel) — so every answer below still comes from
// the WALK route this unit's fix touches (ClassMethodWalkCall, the
// ReadStaticFieldAccess reorder, AccessorWalkPlainWrite): applySummary
// reads the GLOBAL EngineKernelHeld(), left nil, and keeps declining, so
// a kernel-served answer can never mask a walk-route regression here.
// A genuinely nil ctx.Kernel does not leave the walk-determined VALUE
// unchanged, the way this file first assumed — CheckAssignability's own
// membership question (set_membership.go, checkExactValues) needs a
// real kernel to report the 7001 refutation these rows pin at all; left
// nil, the question panics on a nil pointer dereference and recovers
// into a decline, never the refutation.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
	"github.com/microsoft/typescript-go/internal/refinedts/kernelbridge"
)

const eClassAccessorRowsHeader = `import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
`

// eRowRun mirrors parseVocabRun with the ANNOTATION pass run first —
// production's own order (CompileFileFacts): the `: Age` RETURN
// refinement compiles only through the populated registry (Age =
// z.infer<typeof zAge> resolves through zAge's compiled annotation),
// which the .parse-vocabulary runner never needs. Without it the
// contract's Result is nil and the over legs have nothing to fire
// against, however exactly the walk determines them.
func eRowRun(t *testing.T, kernel *kernelbridge.RefinedTSKernel, source string, name string) []assignability.RefinementDiagnostic {
	t.Helper()
	p := parseVocabProgram(t, source)
	registry := annotations.AnnotationRegistry{}
	objects := annotations.ObjectRegistry{}
	annotations.CompileAnnotationFileFacts(p, p.Entry,
		annotations.AnnotationFileFactsMerged{Registry: registry, Objects: objects},
		kernel, false, func(assignability.RefinementDiagnostic) {})
	merged := map[*ast.Symbol]*FunctionContract{}
	contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
	fn := parseVocabFunctionNamed(t, p, name)
	symbol := p.Checker.GetSymbolAtLocation(fn.Name())
	if symbol == nil {
		t.Fatalf("the checker resolved no symbol for %s", name)
	}
	contract, ok := contracts[symbol]
	if !ok {
		t.Fatalf("CompileContractFileFacts registered no contract for %s", name)
	}
	if contract.Result == nil {
		t.Fatalf("the compiled contract for %s carries no Result — the annotation pass did not resolve Age", name)
	}
	var diagnostics []assignability.RefinementDiagnostic
	ctx := &FlowContext{
		P:         p,
		Registry:  registry,
		Objects:   objects,
		Contracts: contracts,
		Report: func(d assignability.RefinementDiagnostic) {
			diagnostics = append(diagnostics, d)
		},
		Aliases:  dataflowfacts.NewAliasClasses(),
		Declared: map[string]*annotations.DeclaredRefinement{},
		Kernel:   kernel,
	}
	AnalyzeFunction(ctx, contract, nil)
	return diagnostics
}

// TestERow_PrivateFieldThroughConstructor pins privateFieldThroughConstructor:
// `new Sealed(40).years()` must be silent (the private field carries
// exactly 40 through this call's own receiver, not the class-wide
// invariant join every call used to share before ClassMethodWalkCall).
func TestERow_PrivateFieldThroughConstructor(t *testing.T) {
	kernel := parseVocabAssignabilityKernel(t)
	source := eClassAccessorRowsHeader + `
class Sealed {
  #age: number;
  constructor(age: number) {
    this.#age = age;
  }
  years(): number {
    return this.#age;
  }
}
function privateFieldThroughConstructorOk(): Age {
  return new Sealed(40).years();
}
function privateFieldThroughConstructorOver(): Age {
  return new Sealed(200).years();
}
`
	parseVocabWantSilent(t, eRowRun(t, kernel, source,"privateFieldThroughConstructorOk"), "new Sealed(40).years()")
	parseVocabWantRefusedAtArgument(t, eRowRun(t, kernel, source,"privateFieldThroughConstructorOver"), "new Sealed(200).years()")
}

// TestERow_StaticField pins staticField: `Limits.ceiling` must answer
// exactly 40 (ReadStaticFieldAccess reached before ReadObjectKeyAccess's
// KindHostFunction branch can shadow it — a bare class reference's
// static TYPE carries construct signatures, which used to answer a
// non-nil residue and block ReadStaticFieldAccess from ever running).
func TestERow_StaticField(t *testing.T) {
	kernel := parseVocabAssignabilityKernel(t)
	source := eClassAccessorRowsHeader + `
class Limits {
  static ceiling = 40;
}
class OverLimits {
  static ceiling = 200;
}
function staticFieldOk(): Age {
  return Limits.ceiling;
}
function staticFieldOver(): Age {
  return OverLimits.ceiling;
}
`
	parseVocabWantSilent(t, eRowRun(t, kernel, source,"staticFieldOk"), "Limits.ceiling")
	parseVocabWantRefusedAtArgument(t, eRowRun(t, kernel, source,"staticFieldOver"), "OverLimits.ceiling")
}

// TestERow_GetterRead pins getterRead: `new Aged().age` must answer
// exactly 40 (ConstructedInstance's get-accessor pass keys "age" off
// the getter's own return, so the read finds the exact value instead
// of an absent key seeded wide from the plain `number` return type).
func TestERow_GetterRead(t *testing.T) {
	kernel := parseVocabAssignabilityKernel(t)
	source := eClassAccessorRowsHeader + `
class Aged {
  get age(): number {
    return 40;
  }
}
class OverAged {
  get age(): number {
    return 200;
  }
}
function getterReadOk(): Age {
  return new Aged().age;
}
function getterReadOver(): Age {
  return new OverAged().age;
}
`
	parseVocabWantSilent(t, eRowRun(t, kernel, source,"getterReadOk"), "new Aged().age")
	parseVocabWantRefusedAtArgument(t, eRowRun(t, kernel, source,"getterReadOver"), "new OverAged().age")
}

// TestERow_SetterWrite pins setterWrite: a plain `box.age = 200` write
// through a set-only accessor must run the setter body and land 200 in
// the backing field `held` (AccessorWalkPlainWrite) — before this fix
// the plain-property arm added "age" as a fresh unknown-valued key and
// left "held" stale at its 0 initializer, which happened to read
// in-set for the WRONG reason.
func TestERow_SetterWrite(t *testing.T) {
	kernel := parseVocabAssignabilityKernel(t)
	source := eClassAccessorRowsHeader + `
class SinkBox {
  held = 0;
  set age(v: number) {
    this.held = v;
  }
}
function setterWriteOk(): Age {
  const box = new SinkBox();
  box.age = 40;
  return box.held;
}
function setterWriteOver(): Age {
  const overBox = new SinkBox();
  overBox.age = 200;
  return overBox.held;
}
`
	parseVocabWantSilent(t, eRowRun(t, kernel, source,"setterWriteOk"), "box.age = 40; box.held")
	parseVocabWantRefusedAtArgument(t, eRowRun(t, kernel, source,"setterWriteOver"), "overBox.age = 200; overBox.held")
}

// TestERow_StaticBlock pins staticBlock: `Counted.total` after a
// static block writes `Counted.total = 40` must answer exactly 40 —
// the same ReadStaticFieldAccess dispatch-order fix staticField pins,
// exercised through class_static_field_invariants.go's static-block
// scan instead of a bare initializer.
func TestERow_StaticBlock(t *testing.T) {
	kernel := parseVocabAssignabilityKernel(t)
	source := eClassAccessorRowsHeader + `
class Counted {
  static total = 0;
  static {
    Counted.total = 40;
  }
}
class OverCounted {
  static total = 0;
  static {
    OverCounted.total = 200;
  }
}
function staticBlockOk(): Age {
  return Counted.total;
}
function staticBlockOver(): Age {
  return OverCounted.total;
}
`
	parseVocabWantSilent(t, eRowRun(t, kernel, source,"staticBlockOk"), "Counted.total")
	parseVocabWantRefusedAtArgument(t, eRowRun(t, kernel, source,"staticBlockOver"), "OverCounted.total")
}

// TestERow_SetterWrite_PlainWriteLandsTheExactValueInTheBackingField is
// a stricter pin than TestERow_SetterWrite's silent leg: it reads
// "held" DIRECTLY off the tracked object after `box.age = 40` and
// asserts it is exactly 40, not merely "whatever satisfied Age" —
// "held"'s stale 0 initializer would ALSO satisfy Age by coincidence,
// so a diagnostic-only pin could stay green while the setter still
// never ran. classFieldValuesContext/classFieldValuesProgram are
// class_field_values_test.go's own helpers (no kernel, no Age
// annotation needed — this pins the VALUE the setter route computes,
// not an assignability verdict).
func TestERow_SetterWrite_PlainWriteLandsTheExactValueInTheBackingField(t *testing.T) {
	p := classFieldValuesProgram(t,
		"class SinkBox {\n"+
			"  held = 0;\n"+
			"  set age(v: number) {\n"+
			"    this.held = v;\n"+
			"  }\n"+
			"}\n"+
			"function setterWriteFortyLandsInHeld(): number {\n"+
			"  const box = new SinkBox();\n"+
			"  box.age = 40;\n"+
			"  return box.held;\n"+
			"}\n"+
			"void setterWriteFortyLandsInHeld;\n")
	fn := classFieldValuesFirstNode(t, p, "setterWriteFortyLandsInHeld", func(node *ast.Node) bool {
		if !ast.IsFunctionDeclaration(node) {
			return false
		}
		name := node.AsFunctionDeclaration().Name()
		return name != nil && ast.IsIdentifier(name) && name.Text() == "setterWriteFortyLandsInHeld"
	})
	body := fn.AsFunctionDeclaration().Body
	if body == nil || !ast.IsBlock(body) {
		t.Fatalf("setterWriteFortyLandsInHeld has no block body")
	}
	ctx := classFieldValuesContext(p)
	env := NewEnv()
	AnalyzeStatements(ctx, env, body.AsBlock().Statements.Nodes, nil)
	held, ok := env.Get("box")
	if !ok {
		t.Fatalf("box is untracked after `box.age = 40`")
	}
	classFieldValuesExactNumber(t, classFieldValuesKey(t, held, "held"), 40)
}

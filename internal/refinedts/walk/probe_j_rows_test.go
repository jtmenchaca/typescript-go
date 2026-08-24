// TEMPORARY probe — traces known.Kind and the decline site for
// j-stdlib-surfaces.ts rows 249, 313, 328, 414, 463, 486. Deleted
// once the fix lands; not a permanent pin.
package walk

import (
	"fmt"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/annotations"
	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
	"github.com/microsoft/typescript-go/internal/refinedts/dataflowfacts"
)

func TestProbeJRows(t *testing.T) {
	kernel := parseVocabKernel(t)

	cases := []struct {
		name   string
		source string
		fn     string
	}{
		{
			"sanityCheck",
			`import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
function ok(): Age { return 40; }
function over(): Age { return 200; }
`,
			"over",
		},
		{
			"errorCause",
			`import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
function errorCause(): Age {
  return new Error("failure", { cause: 200 }).cause as Age;
}
`,
			"errorCause",
		},
		{
			"proxyGet",
			`import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
function proxyGet(): Age {
  const target = { age: 40 };
  const trapped = new Proxy(target, { get: () => 200 });
  return trapped.age;
}
`,
			"proxyGet",
		},
		{
			"symbolToString",
			`import * as z from "/surface/z.ts";
const zLabel = z.string().min(1).max(8);
type Label = z.infer<typeof zLabel>;
function symbolToString(): Label {
  return Symbol.for("tag").toString();
}
`,
			"symbolToString",
		},
		{
			"objectKeys",
			`import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
function objectKeys(): Age {
  return Object.keys({ age: 40 }) as unknown as Age;
}
`,
			"objectKeys",
		},
		{
			"arrayFromIterable",
			`import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
function arrayFromIterable(): Age {
  return Array.from([40, 41]) as unknown as Age;
}
`,
			"arrayFromIterable",
		},
		{
			"mapKeysStandalone",
			`import * as z from "/surface/z.ts";
const zAge = z.number().int().min(0).max(120);
type Age = z.infer<typeof zAge>;
function mapKeysStandalone(): Age {
  const ages = new Map<string, number>([["ann", 40]]);
  return ages.keys() as unknown as Age;
}
`,
			"mapKeysStandalone",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parseVocabProgram(t, tc.source)
			registry := annotations.AnnotationRegistry{}
			objects := annotations.ObjectRegistry{}
			merged := map[*ast.Symbol]*FunctionContract{}
			contracts := CompileContractFileFacts(p, p.Entry, registry, objects, merged, false, func(assignability.RefinementDiagnostic) {})
			fn := parseVocabFunctionNamed(t, p, tc.fn)
			symbol := p.Checker.GetSymbolAtLocation(fn.Name())
			contract, ok := contracts[symbol]
			if !ok {
				t.Fatalf("no contract registered for %s", tc.fn)
			}
			fmt.Printf("=== %s ===\n", tc.name)
			fmt.Printf("  Grounded=%v\n", contract.Grounded)
			if contract.Result != nil {
				fmt.Printf("  Result.Kind=%v\n", contract.Result.Kind)
			} else {
				fmt.Printf("  Result=nil\n")
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
			for _, d := range diagnostics {
				fmt.Printf("  code=%d text=%q\n", d.Code, d.MessageText)
			}
			if len(diagnostics) == 0 {
				fmt.Printf("  (no diagnostics reported)\n")
			}
		})
	}
}

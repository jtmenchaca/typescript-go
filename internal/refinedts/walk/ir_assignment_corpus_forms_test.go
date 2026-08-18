// Pins for the corpus assignment forms the census still refuses
// (tmp/recharts-trace.txt, "assignment"): mirrors of the bodies in
// tmp/recharts-src that name an assignment as their first blocked
// statement, each reduced to the assignment form itself.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/refinedts/program"
)

// reduceCSSCalcMirrorSource is tmp/recharts-src/src/util/ReduceCSSCalc.ts
// with its one import replaced by a local isNan, otherwise verbatim.
const reduceCSSCalcMirrorSource = `
const isNan = (value: unknown): boolean => Number.isNaN(value as number);

const MULTIPLY_OR_DIVIDE_REGEX = /(-?\d+(?:\.\d+)?[a-zA-Z%]*)([*\/])(-?\d+(?:\.\d+)?[a-zA-Z%]*)/;
const ADD_OR_SUBTRACT_REGEX = /(-?\d+(?:\.\d+)?[a-zA-Z%]*)([+-])(-?\d+(?:\.\d+)?[a-zA-Z%]*)/;
const CSS_LENGTH_UNIT_REGEX = /^(px|cm|vh|vw|em|rem|%|mm|in|pt|pc|ex|ch|vmin|vmax|Q)$/;
const NUM_SPLIT_REGEX = /(-?\d+(?:\.\d+)?)([a-zA-Z%]+)?/;

type SupportedUnits = 'cm' | 'mm' | 'pt' | 'pc' | 'in' | 'Q' | 'px';

const CONVERSION_RATES: Record<SupportedUnits, number> = {
  cm: 96 / 2.54,
  mm: 96 / 25.4,
  pt: 96 / 72,
  pc: 96 / 6,
  in: 96,
  Q: 96 / (2.54 * 40),
  px: 1,
};

const FIXED_CSS_LENGTH_UNITS: ReadonlyArray<SupportedUnits> = ['cm', 'mm', 'pt', 'pc', 'in', 'Q', 'px'];

function isSupportedUnit(unit: string): unit is SupportedUnits {
  return FIXED_CSS_LENGTH_UNITS.includes(unit as SupportedUnits);
}

const STR_NAN = 'NaN';

function convertToPx(value: number, unit: SupportedUnits): number {
  return value * CONVERSION_RATES[unit];
}

class DecimalCSS {
  static parse(str: string) {
    const [, numStr, unit] = NUM_SPLIT_REGEX.exec(str) ?? [];

    if (numStr == null) {
      return DecimalCSS.NaN;
    }

    return new DecimalCSS(parseFloat(numStr), unit ?? '');
  }

  static NaN = new DecimalCSS(NaN, '');

  constructor(
    public num: number,
    public unit: string,
  ) {
    this.num = num;
    this.unit = unit;

    if (isNan(num)) {
      this.unit = '';
    }

    if (unit !== '' && !CSS_LENGTH_UNIT_REGEX.test(unit)) {
      this.num = NaN;
      this.unit = '';
    }

    if (isSupportedUnit(unit)) {
      this.num = convertToPx(num, unit);
      this.unit = 'px';
    }
  }

  add(other: DecimalCSS) {
    if (this.unit !== other.unit) {
      return new DecimalCSS(NaN, '');
    }

    return new DecimalCSS(this.num + other.num, this.unit);
  }

  subtract(other: DecimalCSS) {
    if (this.unit !== other.unit) {
      return new DecimalCSS(NaN, '');
    }

    return new DecimalCSS(this.num - other.num, this.unit);
  }

  multiply(other: DecimalCSS) {
    if (this.unit !== '' && other.unit !== '' && this.unit !== other.unit) {
      return new DecimalCSS(NaN, '');
    }

    return new DecimalCSS(this.num * other.num, this.unit || other.unit);
  }

  divide(other: DecimalCSS) {
    if (this.unit !== '' && other.unit !== '' && this.unit !== other.unit) {
      return new DecimalCSS(NaN, '');
    }

    return new DecimalCSS(this.num / other.num, this.unit || other.unit);
  }

  toString() {
    return ` + "`${this.num}${this.unit}`" + `;
  }

  isNaN() {
    return isNan(this.num);
  }
}

function calculateArithmetic(expr: string | undefined): string {
  if (expr == null || expr.includes(STR_NAN)) {
    return STR_NAN;
  }

  let newExpr = expr;
  while (newExpr.includes('*') || newExpr.includes('/')) {
    const [, leftOperand, operator, rightOperand] = MULTIPLY_OR_DIVIDE_REGEX.exec(newExpr) ?? [];
    const lTs = DecimalCSS.parse(leftOperand ?? '');
    const rTs = DecimalCSS.parse(rightOperand ?? '');
    const result = operator === '*' ? lTs.multiply(rTs) : lTs.divide(rTs);
    if (result.isNaN()) {
      return STR_NAN;
    }
    newExpr = newExpr.replace(MULTIPLY_OR_DIVIDE_REGEX, result.toString());
  }

  while (newExpr.includes('+') || /.-\d+(?:\.\d+)?/.test(newExpr)) {
    const [, leftOperand, operator, rightOperand] = ADD_OR_SUBTRACT_REGEX.exec(newExpr) ?? [];
    const lTs = DecimalCSS.parse(leftOperand ?? '');
    const rTs = DecimalCSS.parse(rightOperand ?? '');
    const result = operator === '+' ? lTs.add(rTs) : lTs.subtract(rTs);
    if (result.isNaN()) {
      return STR_NAN;
    }
    newExpr = newExpr.replace(ADD_OR_SUBTRACT_REGEX, result.toString());
  }

  return newExpr;
}

const PARENTHESES_REGEX = /\(([^()]*)\)/;

function calculateParentheses(expr: string): string {
  let newExpr = expr;
  let match: ReturnType<typeof RegExp.prototype.exec> | null;
  while ((match = PARENTHESES_REGEX.exec(newExpr)) != null) {
    const [, parentheticalExpression] = match;
    newExpr = newExpr.replace(PARENTHESES_REGEX, calculateArithmetic(parentheticalExpression));
  }

  return newExpr;
}

function evaluateExpression(expression: string): string {
  let newExpr = expression.replace(/\s+/g, '');
  newExpr = calculateParentheses(newExpr);
  newExpr = calculateArithmetic(newExpr);

  return newExpr;
}

export function safeEvaluateExpression(expression: string): string {
  try {
    return evaluateExpression(expression);
  } catch {
    return STR_NAN;
  }
}

export function reduceCSSCalc(expression: string): string {
  const result = safeEvaluateExpression(expression.slice(5, -1));

  if (result === STR_NAN) {
    return '';
  }

  return result;
}
`

// cartesianAxisMirrorSource mirrors the two non-JSX bodies of
// tmp/recharts-src/src/cartesian/CartesianAxis.tsx that carry the
// census's refused assignment forms: AxisLine's whole-record
// reassignment (`props = { ...props, x1: x, … }`) and
// getTickLineCoord's chained/arithmetic writes into unannotated lets.
const cartesianAxisMirrorSource = `
type LineSvgProps = { x1?: number; y1?: number; x2?: number; y2?: number; fill?: string; className?: string };

function axisLineMirror(
  otherSvgProps: LineSvgProps | null,
  x: number,
  y: number,
  width: number,
  height: number,
  orientation: string,
  mirror: boolean,
): LineSvgProps {
  let props: LineSvgProps = {
    ...otherSvgProps,
    fill: 'none',
  };

  if (orientation === 'top' || orientation === 'bottom') {
    const needHeight = +((orientation === 'top' && !mirror) || (orientation === 'bottom' && mirror));
    props = {
      ...props,
      x1: x,
      y1: y + needHeight * height,
      x2: x + width,
      y2: y + needHeight * height,
    };
  } else {
    const needWidth = +((orientation === 'left' && !mirror) || (orientation === 'right' && mirror));
    props = {
      ...props,
      x1: x + needWidth * width,
      y1: y,
      x2: x + needWidth * width,
      y2: y + height,
    };
  }

  return props;
}

type CartesianTickItem = { value?: unknown; coordinate: number; tickCoord?: number; tickSize?: number };
const isNumber = (value: unknown): value is number => typeof value === 'number' && !Number.isNaN(value);

function getTickLineCoord(
  data: CartesianTickItem,
  x: number,
  y: number,
  width: number,
  height: number,
  orientation: 'top' | 'left' | 'right' | 'bottom',
  tickSize: number,
  mirror: boolean,
  tickMargin: number,
): {
  line: { x1: number; y1: number; x2: number; y2: number };
  tick: { x: number; y: number };
} {
  let x1, x2, y1, y2, tx, ty;

  const sign = mirror ? -1 : 1;
  const finalTickSize = data.tickSize || tickSize;
  const tickCoord = isNumber(data.tickCoord) ? data.tickCoord : data.coordinate;

  switch (orientation) {
    case 'top':
      x1 = x2 = data.coordinate;
      y2 = y + +!mirror * height;
      y1 = y2 - sign * finalTickSize;
      ty = y1 - sign * tickMargin;
      tx = tickCoord;
      break;
    case 'left':
      y1 = y2 = data.coordinate;
      x2 = x + +!mirror * width;
      x1 = x2 - sign * finalTickSize;
      tx = x1 - sign * tickMargin;
      ty = tickCoord;
      break;
    case 'right':
      y1 = y2 = data.coordinate;
      x2 = x + +mirror * width;
      x1 = x2 + sign * finalTickSize;
      tx = x1 + sign * tickMargin;
      ty = tickCoord;
      break;
    default:
      x1 = x2 = data.coordinate;
      y2 = y + +mirror * height;
      y1 = y2 + sign * finalTickSize;
      ty = y1 + sign * tickMargin;
      tx = tickCoord;
      break;
  }

  return { line: { x1, y1, x2, y2 }, tick: { x: tx, y: ty } };
}
`

// TestAssignmentCorpusForms_CartesianAxisMirrorBodyFates asserts the
// two mirrored CartesianAxis bodies' fates: AxisLine's whole-record
// reassignment (`props = { ...props, x1: x, … }`) lowers COMPLETE, and
// getTickLineCoord — once blocked at "assignment" by its own
// unknown-sorted, uninitialized lets (`let x1, x2, y1, y2, tx, ty;`,
// fixed by localSortAndTypeof / writtenSortOf,
// ir_summary_local_slot_layout.go: the checker's own control-flow-
// narrowed type at a read occurrence grounds the sort where no
// annotation or initializer does) — now clears the layout and blocks
// one construct deeper, on its own `switch (orientation) { … }`.
func TestAssignmentCorpusForms_CartesianAxisMirrorBodyFates(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, cartesianAxisMirrorSource)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	axisLine := entryEnvFunctionNamed(t, p, "axisLineMirror")
	RelowerSummaryBody(ctx, axisLine)
	if outcome, construct, _ := SummaryOutcomeOf(axisLine); outcome != SummaryComplete {
		t.Errorf("axisLineMirror outcome = %q (construct %q), want complete", outcome, construct)
	}
	tickLine := entryEnvFunctionNamed(t, p, "getTickLineCoord")
	RelowerSummaryBody(ctx, tickLine)
	if outcome, construct, _ := SummaryOutcomeOf(tickLine); outcome != SummaryPorous || construct != "switch" {
		t.Errorf("getTickLineCoord outcome = %q construct = %q — the layout remainder has moved; update the remainder note", outcome, construct)
	}
}

// firstConstructorIn finds the first constructor declaration with a
// body in the entry source.
func firstConstructorIn(t *testing.T, p *program.CheckerProgram) *ast.Node {
	t.Helper()
	var constructor *ast.Node
	var visit func(node *ast.Node) bool
	visit = func(node *ast.Node) bool {
		if constructor != nil {
			return true
		}
		if ast.IsConstructorDeclaration(node) && node.Body() != nil {
			constructor = node
			return true
		}
		node.ForEachChild(visit)
		return false
	}
	visit(p.Entry.AsNode())
	if constructor == nil {
		t.Fatalf("no constructor found")
	}
	return constructor
}

// TestAssignment_GlobalNaNRhsIntoThisFieldCompletes pins the DecimalCSS
// constructor's blocked form on its own: `this.num = NaN` — the global
// NaN as an assigned right side. Pre-fix this body was porous naming
// "assignment" (no effect reading spelled the identifier); the effect
// grammar now reads the unshadowed global NaN as the NaN state constant
// (NanConst, kernelbridge/loop_questions.go).
func TestAssignment_GlobalNaNRhsIntoThisFieldCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, "class C { num: number = 0; constructor(num: number) { this.num = NaN; } }")
	constructor := firstConstructorIn(t, p)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, constructor)
	outcome, construct, recorded := SummaryOutcomeOf(constructor)
	if !recorded {
		t.Fatalf("no outcome recorded — the lowering ran and must report a fate")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — the global NaN reads as the NaN state constant", outcome, construct)
	}
}

// TestAssignment_ShadowedNaNKeepsItsSlotRead guards the NaN arm's
// shadow gate: a PARAMETER named NaN is a slot, and `x = NaN` must stay
// the slot copy it always was — the global-constant reading serves only
// the unshadowed name.
func TestAssignment_ShadowedNaNKeepsItsSlotRead(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, "function f(NaN: number): number { let x; x = NaN; return x; }")
	declaration := entryEnvFunctionNamed(t, p, "f")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q (construct %q), want complete — the shadowing parameter's slot serves the read", outcome, construct)
	}
}

// TestAssignment_NumericReadOfUnknownSortedLetDetermines is the
// executable form of the getTickLineCoord remainder, now cleared: an
// unannotated, uninitialized `let` used to lay out unknown-sorted
// (LocalSort, tracked_bindings.go, reached through localSlotsIn's
// scalar fallback in ir_summary_local_slot_layout.go), so a later
// NUMERIC read of it (`x1 = x2 - 1`) refused on EffectOf's number-sort
// gate even where every write into the slot was numeric. Fixed by
// localSortAndTypeof (ir_summary_local_slot_layout.go): where no
// annotation and no initializer ground the sort, writtenSortOf asks the
// checker's own resolved type at every plain READ occurrence of the
// name in the body — TypeScript's control-flow analysis has already
// narrowed that occurrence's type from the assignments, even though the
// bare declaration name itself still resolves to `any`. Every read must
// agree, or the local stays unknown.
func TestAssignment_NumericReadOfUnknownSortedLetDetermines(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, "function f(c: number): number { let x2; x2 = c; let x1; x1 = x2 - 1; return x1; }")
	declaration := entryEnvFunctionNamed(t, p, "f")
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	RelowerSummaryBody(ctx, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if outcome != SummaryComplete {
		t.Errorf("outcome = %q construct = %q, want complete — the unknown-sorted-let layout remainder is fixed", outcome, construct)
	}
}

// TestAssignmentCorpusForms_ReduceCSSCalcMirrorConstructorLeavesTheAssignmentRow
// asserts the DecimalCSS constructor mirror is no longer blocked at an
// ASSIGNMENT: `this.num = NaN` reads as the NaN state constant, so the
// body's first blocked statement is now the `convertToPx` call — a
// call-family row, not this family's.
func TestAssignmentCorpusForms_ReduceCSSCalcMirrorConstructorLeavesTheAssignmentRow(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, reduceCSSCalcMirrorSource)
	ctx := &FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}
	constructor := firstConstructorIn(t, p)
	RelowerSummaryBody(ctx, constructor)
	outcome, construct, recorded := SummaryOutcomeOf(constructor)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	if construct == "assignment" {
		t.Errorf("outcome = %q construct = %q — the NaN right side must read as a constant, not refuse the assignment", outcome, construct)
	}
}

// Pin for census row "return (call getPercentValue)" ×2 and "return
// inside if" — util/DataUtils.ts's getPercentValue verbatim, the exact
// corpus body the brief names as the return-inside-if source.
package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestReturnGetPercentValue_BodyCompletes(t *testing.T) {
	kernel := kernelDelegationLoadKernel(t)
	SetEngineKernel(kernel)
	ClearResolvedRecordMembers()
	ClearSummaryOutcomes()
	p := entryEnvTestProgram(t, `
		export const isNan = (value: unknown): value is number => {
			return typeof value == 'number' && value != +value;
		};
		export const isPercent = (value: string | number | undefined) =>
			typeof value === 'string' && value.indexOf('%') === value.length - 1;
		export const isNumber = (value: unknown): value is number =>
			(typeof value === 'number') && !isNan(value);
		export const getPercentValue = (
			percent: number | string | undefined,
			totalValue: number | undefined,
			defaultValue = 0,
			validate = false,
		) => {
			if (!isNumber(percent) && typeof percent !== 'string') {
				return defaultValue;
			}
			let value: number;
			if (isPercent(percent)) {
				if (totalValue == null) {
					return defaultValue;
				}
				const index = (percent as string).indexOf('%');
				value = (totalValue * parseFloat((percent as string).slice(0, index))) / 100;
			} else {
				value = +percent;
			}
			if (isNan(value)) {
				value = defaultValue;
			}
			if (validate && totalValue != null && value > totalValue) {
				value = totalValue;
			}
			return value;
		};
	`)
	declaration := entryEnvArrowConstNamed(t, p, "getPercentValue")
	_, ok := RelowerSummaryBody(&FlowContext{P: p, Contracts: map[*ast.Symbol]*FunctionContract{}}, declaration)
	outcome, construct, recorded := SummaryOutcomeOf(declaration)
	if !recorded {
		t.Fatalf("no outcome recorded")
	}
	t.Logf("ok=%v outcome=%q construct=%q", ok, outcome, construct)
}

// split from ir_await_test.go — the Promise.all probes: the array-literal
// recognition and the sequenced statement lowering.

package walk

import (
	"testing"
)

func TestPromiseAllArray_TheArrayLiteralOfCallsIsRecognizedAndEveryOtherShapeDeclines(t *testing.T) {
	statements := awaitParse(t, `
		await Promise.all([f(1), g(2)]);
		await Promise.all(items);
		await Promise.all([...items]);
		await Promise.race([f(1)]);
		await Promise.all([f(1)], 2);
	`)
	operand, isAwait := AwaitedOperandOf(statements[0].AsExpressionStatement().Expression)
	if !isAwait {
		t.Fatalf("the first statement is not an await")
	}
	elements, recognized := promiseAllArrayOf(operand)
	if !recognized {
		t.Fatalf("promiseAllArrayOf(Promise.all([f(1), g(2)])) recognized = false, want true")
	}
	if len(elements) != 2 {
		t.Errorf("len(elements) = %d, want 2", len(elements))
	}
	for index := 1; index < len(statements); index++ {
		other, ok := AwaitedOperandOf(statements[index].AsExpressionStatement().Expression)
		if !ok {
			t.Fatalf("statement %d is not an await", index)
		}
		if _, recognized := promiseAllArrayOf(other); recognized {
			t.Errorf("statement %d took the Promise.all route — only an array literal of calls does", index)
		}
	}
}

func TestPromiseAllStatement_WithoutARegistryTheCallsTakeTheHavocFloor(t *testing.T) {
	// with no flow context there are no blobs, and each element call
	// takes the total-lowering floor: no target, scalar arguments,
	// nothing flattened mentioned — no statement per call
	context := awaitScalarContext()
	statements := awaitParse(t, `await Promise.all([f(1), g(2)]);`)
	lowered, ok := promiseAllStatementOf(context, statements[0])
	if !ok {
		t.Fatalf("a Promise.all without a registry declined, want the havoc floor per call")
	}
	if len(lowered) != 0 {
		t.Errorf("len(lowered) = %d, want 0 — nothing reads the calls' values", len(lowered))
	}
}

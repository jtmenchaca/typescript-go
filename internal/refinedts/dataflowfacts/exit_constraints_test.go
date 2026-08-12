package dataflowfacts

import "testing"

func TestExitConstraints(t *testing.T) {
	_, file := checkerFor(t, `
while (true) {}
`)
	loop := file.Statements.Nodes[0]
	if _, ok := ExitConstraintsOf(loop); ok {
		t.Fatalf("expected no rows before NoteExitConstraints")
	}
	rows := []DifferenceConstraint{{Bound: 5}}
	NoteExitConstraints(loop, rows)
	got, ok := ExitConstraintsOf(loop)
	if !ok {
		t.Fatalf("expected rows after NoteExitConstraints")
	}
	if len(got) != 1 || got[0].Bound != 5 {
		t.Errorf("got %+v", got)
	}
}

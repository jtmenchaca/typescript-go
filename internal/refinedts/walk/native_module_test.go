package walk

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/refinedts/assignability"
)

// TestNativeModuleCallName_ADotNodeImportNamesItself pins ext.1's own
// naming precedent (refinedpy's unmodeled_module_call("torch")): a
// call rooted at an import whose module specifier ends ".node" names
// that specifier text, rather than a generic "no model" wording.
func TestNativeModuleCallName_ADotNodeImportNamesItself(t *testing.T) {
	p := entryEnvTestProgram(t, `
import addon from "./build/Release/addon.node";
function f() {
	return addon.compute(1);
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	returnStatement := statements[0]
	call := returnStatement.AsReturnStatement().Expression
	ctx := &FlowContext{P: p}
	name, ok := NativeModuleCallName(ctx, call)
	if !ok {
		t.Fatalf("NativeModuleCallName did not recognize the .node import call")
	}
	if name != "./build/Release/addon.node" {
		t.Errorf("NativeModuleCallName name = %q, want the .node specifier", name)
	}
}

// TestNativeModuleCallName_ARequireBindingsCallNamesTheLoader pins the
// CommonJS loader idiom: require("bindings")("addon") names "bindings"
// — this is Node's own established native-addon loading convention,
// distinct from an ordinary require() of a JS module.
func TestNativeModuleCallName_ARequireBindingsCallNamesTheLoader(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function require(name: string): any;
function f() {
	return require("bindings")("addon");
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	returnStatement := statements[0]
	call := returnStatement.AsReturnStatement().Expression
	ctx := &FlowContext{P: p}
	name, ok := NativeModuleCallName(ctx, call)
	if !ok {
		t.Fatalf("NativeModuleCallName did not recognize require(\"bindings\")(...)")
	}
	if name != "bindings" {
		t.Errorf("NativeModuleCallName name = %q, want %q", name, "bindings")
	}
}

// TestNativeModuleCallName_AnOrdinaryImportDoesNotRecognize pins the
// negative: an import from an ordinary (non-native) module never
// answers a native name — this file's own recognition stays narrow to
// the two documented shapes, never a false positive on ordinary code.
func TestNativeModuleCallName_AnOrdinaryImportDoesNotRecognize(t *testing.T) {
	p := entryEnvTestProgram(t, `
import { readFileSync } from "node:fs";
function f() {
	return readFileSync("x");
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	returnStatement := statements[0]
	call := returnStatement.AsReturnStatement().Expression
	ctx := &FlowContext{P: p}
	if name, ok := NativeModuleCallName(ctx, call); ok {
		t.Errorf("NativeModuleCallName recognized an ordinary import as native: %q", name)
	}
}

// TestUnmodeledCallResult_ANativeModuleCallNamesItselfInTheReason pins
// the end-to-end wiring: UnmodeledCallResult's own reason note states
// the native module's name, not the generic "has no body to read"
// wording, when assignability.CollectingReasons() is on — called
// directly on the call expression (matching this file's own
// UnmodeledCallResult contract) rather than through the full
// statement-walk machinery, since the recognition itself is already
// pinned by the three tests above.
func TestUnmodeledCallResult_ANativeModuleCallNamesItselfInTheReason(t *testing.T) {
	p := entryEnvTestProgram(t, `
declare function require(name: string): any;
function f() {
	return require("bindings")("addon");
}
`)
	statements := relationalAccumulationBodyOf(t, p, "f")
	returnStatement := statements[0]
	call := returnStatement.AsReturnStatement().Expression
	ctx := &FlowContext{P: p}
	assignability.BeginReasonNotes()
	UnmodeledCallResult(ctx, NewEnv(), call)
	reasons := assignability.EndReasonNotes()
	found := false
	for _, r := range reasons {
		if r.Said == "a call into 'bindings', a native module this checker has no model for" {
			found = true
		}
	}
	if !found {
		t.Errorf("no reason named the native module; reasons: %#v", reasons)
	}
}

// Ask a live module; if it dies, replace it. A question that outruns
// the Lean heap prints "INTERNAL PANIC: out of memory" and the module
// ABORTS. It does not come back: every later call into it throws a
// WebAssembly exception, measured (bench/poison.ts — six questions
// with known answers, all six throwing afterwards).
//
// Soundness survives that, because a throw is silence and the walk
// degrades to unknown. SERVICE does not: one pathological expression
// used to leave every file checked after it with no refinement
// checking at all, and nothing said so — a 629-file library reported
// 18 refinement diagnostics because the kernel had died early on.
//
// So a dead module is detected, ANNOUNCED, and replaced. The question
// that killed it still answers as a refusal (the caller degrades to
// unknown); the next one gets a live kernel.
//
// The TS moduleDied() test recognizes `WebAssembly.RuntimeError` /
// `WebAssembly.Exception` — wasm-specific abort signals with no native
// analogue. This tree's only substrate is the native dylib
// (kernelbridge.NativeKernel, instantiate_kernel.go): Call1/Call2
// return a plain Go `error` for every failure — a missing symbol, a
// null-pointer answer, or (per the task's framing) a decline. There is
// no separate "the whole module died and must be replaced" signal the
// native loader exposes today: NativeKernel's worker goroutine keeps
// running after any single request's error (the `for req := range
// k.requests` loop in instantiate_kernel.go does not exit on an
// answer error), so nothing here currently observes a NativeKernel
// going permanently dead the way a wasm module does. The
// invalidate/adopt discipline is ported as far as the shape goes —
// KernelDied names the question, Invalidate clears the shared pointers
// exactly as the TS does — but KernelDied's body always answers false
// until a native "the runtime is gone" signal exists. Reported as a
// gap in the port report; the loop-goroutine's own crash (a panic
// inside serve) is a separate, larger failure this file does not
// attempt to cover either, matching the TS file's scope (module
// aborts, not process crashes).
package kernelbridge

import (
	"fmt"
	"os"
	"time"
)

// nowMs mirrors `performance.now()` — milliseconds, fractional.
func nowMs() float64 {
	return float64(time.Now().UnixNano()) / 1e6
}

var kernelInstance *RefinedTSKernel
var kernelNative *NativeKernel

// KernelDied is moduleDied in the TS source: did this error come from
// a module that is no longer running? See the file comment — the
// native loader has no such signal yet, so this always answers false;
// every Call1/Call2 error is treated as an ordinary refusal (the TS
// "else" path: `throw thrown` unchanged).
func KernelDied(err error) bool {
	return false
}

// Invalidate is invalidate in the TS source: stop sharing a module
// that aborted, so the next LoadedKernel call site builds a
// replacement. Only clears the shared pointers when they still name
// THIS module — a later instance must not be invalidated by an older
// one's straggling call.
func Invalidate(dead *RefinedTSKernel) {
	if kernelInstance == dead {
		kernelInstance = nil
		kernelNative = nil
	}
	fmt.Fprintln(os.Stderr,
		"RefinedTS: the kernel aborted (out of memory). It has been "+
			"invalidated and the next check builds a fresh one — an aborted module "+
			"answers nothing, so without this every later file would have been "+
			"checked against a dead kernel and reported no refinements at all. "+
			"The question that killed it is declined, so that one position "+
			"alerts rather than being checked.")
}

// LoadedKernel is loadedKernel in the TS source: the kernel when
// initialization has already completed.
func LoadedKernel() *RefinedTSKernel {
	return kernelInstance
}

// AdoptKernel is adoptKernel in the TS source: share one live
// instance; a later call after Invalidate builds a replacement.
//
// The TS signature is async (`Promise<RefinedTSKernel>`, memoized via
// a shared promise so concurrent callers await the same in-flight
// instantiation). The Go native path (instantiate_kernel.go's
// InstantiateNative) is synchronous — dlopen and the worker thread
// start-up happen inline — so this is a plain synchronous
// memoization instead of a promise cache; the "share one live
// instance" behavior is the same, just without the concurrent-caller
// coalescing a Promise gives for free. Callers on multiple goroutines
// racing the first AdoptKernel call could each build a kernel before
// this notices; the TS code does not have that race. Noted in the
// port report.
func AdoptKernel(instantiateKernel func() (*RefinedTSKernel, *NativeKernel, error)) (*RefinedTSKernel, error) {
	if kernelInstance != nil {
		return kernelInstance, nil
	}
	kernel, native, err := instantiateKernel()
	if err != nil {
		return nil, err
	}
	kernelInstance = kernel
	kernelNative = native
	return kernel, nil
}

// KernelFromCallsInput mirrors the destructured parameter of
// kernelFromCalls in the TS source. `kernel` (the KernelCalls
// function-pointer table) and rawCall1/rawCall2 (wasm-vs-native
// dispatch) collapse to a single NativeKernel — see kernel_asks.go's
// file comment.
type KernelFromCallsInput struct {
	Native *NativeKernel
	InitMs float64
}

// KernelFromCalls is kernelFromCalls in the TS source: the questions
// over a live substrate; every crossing goes through throughModule.
func KernelFromCalls(input KernelFromCallsInput) *RefinedTSKernel {
	native := input.Native
	alive := true
	var self *RefinedTSKernel

	throughModule := func(call func() (string, error)) (string, error) {
		raw, err := call()
		if err == nil {
			return raw, nil
		}
		if !KernelDied(err) {
			return "", err
		}
		if alive {
			alive = false
			if self != nil {
				Invalidate(self)
			}
		}
		return "", fmt.Errorf(
			"kernel: declined — the module aborted on this question " +
				"(out of memory) and was replaced",
		)
	}

	call1 := func(symbol string, input string) (string, error) {
		return throughModule(func() (string, error) { return native.Call1(symbol, input) })
	}
	call2 := func(symbol string, first string, second string) (string, error) {
		return throughModule(func() (string, error) { return native.Call2(symbol, first, second) })
	}

	// Every question that actually reaches the kernel is timed and its
	// wire measured. Nothing is declined on an estimate: what a
	// question costs is observed, not predicted (boundary/observed_cost.ts).
	timed := func(op string, bytes int, wire string, call func() (string, error)) (string, error) {
		startedAt := nowMs()
		raw, err := call()
		elapsed := nowMs() - startedAt
		displayWire := wire
		if len(wire) > 300 {
			displayWire = wire[:300] + "…"
		}
		RecordQuestion(QuestionCost{Op: op, Ms: elapsed, Bytes: bytes, Wire: displayWire, HasWire: true})
		return raw, err
	}

	ask1 := func(op string, symbol string, wireInput string, key ...string) (string, error) {
		k := wireInput
		if len(key) > 0 {
			k = key[0]
		}
		return AskCached(fmt.Sprintf("%s\x00%s", op, k), func() (string, error) {
			return timed(op, len(wireInput), wireInput, func() (string, error) {
				return call1(symbol, wireInput)
			})
		})
	}
	ask2 := func(op string, symbol string, first string, second string, key ...string) (string, error) {
		k := fmt.Sprintf("%s\x01%s", first, second)
		if len(key) > 0 {
			k = key[0]
		}
		return AskCached(fmt.Sprintf("%s\x00%s", op, k), func() (string, error) {
			return timed(op, len(first)+len(second), fmt.Sprintf("%s %s", first, second), func() (string, error) {
				return call2(symbol, first, second)
			})
		})
	}

	built := KernelAsks(KernelAsksInput{Ask1: ask1, Ask2: ask2})
	built.InitMs = input.InitMs
	// throughModule needs to name the instance it belongs to when it
	// invalidates it — the object exists only now
	self = built
	return built
}

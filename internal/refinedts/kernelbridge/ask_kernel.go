// Ask a live module; if it dies, replace it. In the TS/wasm source, a
// question that outruns the Lean heap prints "INTERNAL PANIC: out of
// memory" and the wasm module ABORTS without taking the host process
// down with it — wasm's isolation makes every later call into that
// module throw a catchable WebAssembly exception, measured
// (bench/poison.ts — six questions with known answers, all six
// throwing afterwards). Soundness survives that (a throw is silence
// and the walk degrades to unknown); SERVICE does not unless the dead
// module is detected, ANNOUNCED, and replaced — one pathological
// expression used to leave every file checked after it with no
// refinement checking at all, and nothing said so: a 629-file library
// reported 18 refinement diagnostics because the kernel had died early
// on.
//
// THIS TREE'S SUBSTRATE IS DIFFERENT IN THE WAY THAT MATTERS: the
// native dylib (kernelbridge.NativeKernel, instantiate_kernel.go) is
// dlopen'd straight into this process's own address space and runs on
// a locked worker OS thread this same process owns (the Lean runtime
// requires per-thread init; see instantiate_kernel.go's file comment).
// There is no wasm-style sandbox between the kernel and the host here.
// kernel_wrapper.c calls lean_set_exit_on_panic(true), so a Lean
// `panic!` no longer prints and serves a fabricated default value —
// it calls abort() on the worker thread, which kills THE WHOLE
// PROCESS, this file included. A dead kernel is therefore never
// something a later call observes and recovers from mid-session: by
// the time any code could ask "did the kernel just die," the process
// that would have asked is already gone. There is no "answer the
// question that killed it as a decline, then keep serving on a fresh
// kernel IN THE SAME PROCESS" path available to an in-process abort —
// that would need the kernel to run somewhere an abort does not take
// the caller down with it: a separate process or subprocess speaking
// the same wire over IPC, a genuine architecture change this file does
// not make.
//
// What IS implemented, given that constraint, is detection and
// quarantine ONE LEVEL UP, in whatever parent respawns this process.
// ask1/ask2 below write the LAST QUESTION RECORD (last_question_record.go)
// immediately before every question crosses to the dylib and clear it
// immediately after a successful answer, so a process that aborts
// mid-question leaves the record naming exactly that question. The
// parent (cmd/refined-lsp's coordinator, for the LSP path) detects the
// child's death, reads that record, restarts the child, and passes the
// killing question's cache key back in (cmd/tsgo's -quarantine-file
// flag, SetQuarantinedQuestions). ask1/ask2 check that quarantine list before
// the cache lookup and the FFI call, exactly like the nesting guard
// (wire_nesting_guard.go): the restarted process declines that ONE
// question by name instead of asking it again and dying the same way.
//
// ProbeKernelAlive (kernel_bridge.go) is the complementary PRE-death
// check: one trivial known-answer question on a freshly loaded dylib,
// before LoadKernel ever shares it — catching a dylib that loaded
// (dlopen + every symbol resolved) but answers wrong, while the failure
// is still just a Go `error` instead of a process abort.
package kernelbridge

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// nowMs mirrors `performance.now()` — milliseconds, fractional.
func nowMs() float64 {
	return float64(time.Now().UnixNano()) / 1e6
}

// kernelMu guards kernelInstance/kernelNative — the init-once singleton
// pair LoadKernel(via AdoptKernel) and Invalidate share. Several
// goroutines can call LoadKernel at process start (one per entry file
// racing the first ask); the lock makes the "share one live instance"
// property actually hold instead of letting each goroutine build its
// own kernel before the first one is visible.
var kernelMu sync.Mutex
var kernelInstance *RefinedTSKernel
var kernelNative *NativeKernel

// KernelDied is moduleDied in the TS source: did this error come from
// a module that is no longer running? On this tree's in-process native
// substrate, the answer is structurally always no: a mid-session Lean
// panic calls abort() (lean_set_exit_on_panic, kernel_wrapper.c) on the
// same process this code runs in, so a "the kernel died and this call
// observed it" moment never reaches a return statement — the process
// is gone with it. Every Call1/Call2 error this code CAN see is an
// ordinary refusal (a missing symbol, a null-pointer answer, a
// declined question) from a kernel that is still running, so this
// always answers false. A genuine mid-session death prints the Lean
// panic message to stderr and ends the process; detecting it and
// serving a KernelDied decline in its place instead of a process abort
// would need the kernel moved out-of-process (a separate design unit
// — see the file comment). What this tree does instead, pre-death,
// is ProbeKernelAlive at load time (kernel_bridge.go).
func KernelDied(err error) bool {
	return false
}

// Invalidate is invalidate in the TS source: stop sharing a module
// that aborted, so the next LoadedKernel call site builds a
// replacement. Only clears the shared pointers when they still name
// THIS module — a later instance must not be invalidated by an older
// one's straggling call. Ported from the TS wasm design for shape
// parity; on this tree's in-process substrate KernelDied never answers
// true (see its comment), so throughModule never calls this today —
// it would need the out-of-process seam to have a caller.
func Invalidate(dead *RefinedTSKernel) {
	kernelMu.Lock()
	if kernelInstance == dead {
		kernelInstance = nil
		kernelNative = nil
	}
	kernelMu.Unlock()
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
	kernelMu.Lock()
	defer kernelMu.Unlock()
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
// coalescing a Promise gives for free.
//
// kernelMu is held across the whole check-then-build: goroutines on
// multiple entry files can all race the first AdoptKernel call at sweep
// start, and without the lock spanning instantiateKernel() itself, each
// would see kernelInstance == nil and each would dlopen its own dylib
// and start its own worker thread — one goroutine ends up sharing the
// pointers, the rest leak a live kernel + OS thread. Holding the lock
// across instantiateKernel makes every racing caller after the first
// block until the winner has stored kernelInstance/kernelNative, then
// return that same instance instead of building a second one — the
// same "share one live instance" property the TS Promise memo gives via
// await-coalescing, achieved here by blocking instead.
func AdoptKernel(instantiateKernel func() (*RefinedTSKernel, *NativeKernel, error)) (*RefinedTSKernel, error) {
	kernelMu.Lock()
	defer kernelMu.Unlock()
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
	//
	// This is also the ask seam kernel_trace.go's Q/A lines fire from:
	// `wire` here is the exact request that crosses to the dylib
	// (post-cache-miss — AskCached only calls compute(), which reaches
	// this closure, on a miss), and `raw` is the exact answer wire that
	// comes back, before any decode. traceKernelQuestion/Answer are
	// nil-checked no-ops when no trace writer is set.
	timed := func(op string, bytes int, wire string, call func() (string, error)) (string, error) {
		traceKernelQuestion(op, wire)
		startedAt := nowMs()
		raw, err := call()
		elapsed := nowMs() - startedAt
		if err == nil {
			traceKernelAnswer(op, raw)
		}
		displayWire := wire
		if len(wire) > 300 {
			displayWire = wire[:300] + "…"
		}
		RecordQuestion(QuestionCost{Op: op, Ms: elapsed, Bytes: bytes, Wire: displayWire, HasWire: true})
		return raw, err
	}

	ask1 := func(op string, symbol string, wireInput string, key ...string) (string, error) {
		// THE SEAM GUARD (wire_nesting_guard.go): a wire whose sequence-
		// shaped nesting exceeds what the kernel's deciders are measured
		// to walk declines here, before the cache lookup and before the
		// FFI call — never a crash, never an unbounded ask. Checked
		// ahead of AskCached so a too-deep wire is never remembered as a
		// permanent cache entry either (a later, differently-shaped
		// question with the same op never collides with this one's key).
		if wireExceedsNestingCap(wireInput) {
			return "", fmt.Errorf(
				"kernel: declined — the set's nesting exceeds what the kernel's deciders read")
		}
		k := wireInput
		if len(key) > 0 {
			k = key[0]
		}
		cacheKey := fmt.Sprintf("%s\x00%s", op, k)
		// THE QUARANTINE GATE (quarantine.go): a question named by a
		// PRIOR run's last-question record — the one that killed that
		// run's kernel — declines here by name, ahead of the cache
		// lookup and the FFI call, exactly like the nesting guard above.
		// This is what keeps a restarted process from crash-looping on
		// the same file: the parent (cmd/refined-lsp) reads the dead
		// child's record and passes this same key back in as -quarantine.
		if isQuarantined(cacheKey) {
			return "", fmt.Errorf("%s", quarantineDeclineMessage)
		}
		return AskCached(cacheKey, func() (string, error) {
			// THE LAST-QUESTION RECORD (last_question_record.go): written
			// immediately before the question that could kill this
			// process, cleared immediately after it answers. A no-op
			// when no record path is configured (the batch-CLI default).
			WriteLastQuestion(op, cacheKey)
			raw, err := timed(op, len(wireInput), wireInput, func() (string, error) {
				return call1(symbol, wireInput)
			})
			if err == nil {
				ClearLastQuestion()
			}
			return raw, err
		})
	}
	ask2 := func(op string, symbol string, first string, second string, key ...string) (string, error) {
		// THE SEAM GUARD, both operands — see ask1's comment above.
		if wireExceedsNestingCap(first) || wireExceedsNestingCap(second) {
			return "", fmt.Errorf(
				"kernel: declined — the set's nesting exceeds what the kernel's deciders read")
		}
		k := fmt.Sprintf("%s\x01%s", first, second)
		if len(key) > 0 {
			k = key[0]
		}
		cacheKey := fmt.Sprintf("%s\x00%s", op, k)
		// THE QUARANTINE GATE — see ask1's comment above.
		if isQuarantined(cacheKey) {
			return "", fmt.Errorf("%s", quarantineDeclineMessage)
		}
		return AskCached(cacheKey, func() (string, error) {
			// THE LAST-QUESTION RECORD — see ask1's comment above.
			WriteLastQuestion(op, cacheKey)
			raw, err := timed(op, len(first)+len(second), fmt.Sprintf("%s %s", first, second), func() (string, error) {
				return call2(symbol, first, second)
			})
			if err == nil {
				ClearLastQuestion()
			}
			return raw, err
		})
	}

	built := KernelAsks(KernelAsksInput{Ask1: ask1, Ask2: ask2})
	built.InitMs = input.InitMs
	// throughModule needs to name the instance it belongs to when it
	// invalidates it — the object exists only now
	self = built
	return built
}

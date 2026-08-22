// Load the native dylib and hand the 25 C functions to the asks.
// Marshaling is a null-terminated string in and a C string out, freed
// by the seam's own free once copied — the same C ABI the TS bridge
// dlopens over Deno FFI (instantiate_kernel.ts); no wasm here, the
// dylib is the primary substrate.
//
// Every call crosses on ONE dedicated OS thread: the Lean runtime
// initializes per thread, and the first native call from a foreign
// thread was measured as a segfault (the S7 finding) — so a locked
// worker goroutine owns init and every ask, and callers hand it work
// over a channel. The channel round trip is nanoseconds against
// deciders measured in micro- to milliseconds.

package kernelbridge

/*
#include <dlfcn.h>
#include <stdlib.h>

typedef void  (*rts_init_fn)(void);
typedef void  (*rts_free_fn)(char*);
typedef char* (*rts_ask1_fn)(const char*);
typedef char* (*rts_ask2_fn)(const char*, const char*);

static void  rts_init(void* f)                                { ((rts_init_fn)f)(); }
static void  rts_free_answer(void* f, char* p)                { ((rts_free_fn)f)(p); }
static char* rts_ask1(void* f, const char* a)                 { return ((rts_ask1_fn)f)(a); }
static char* rts_ask2(void* f, const char* a, const char* b)  { return ((rts_ask2_fn)f)(a, b); }
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"
)

// The 31 question symbols the dylib exports (boundary/exports.lean),
// split by arity exactly as the TS FFI table splits them.
var oneArgSymbols = []string{
	"kernel_scalar_empty",
	"kernel_seq_empty",
	"kernel_seq_no_scalar_reread",
	"kernel_structural",
	"kernel_judge",
	"kernel_validate_chain",
	"kernel_calendar",
	"kernel_transfer",
	"kernel_envelope",
	"kernel_linear",
	"kernel_invariant",
	"kernel_invariant_widen",
	"kernel_solve_loop",
	"kernel_narrow",
	"kernel_join_state",
	"kernel_narrow_state",
	"kernel_walk",
	"kernel_summarize",
	"kernel_apply_summary",
	"kernel_bounds",
	"kernel_decimal",
}

var twoArgSymbols = []string{
	"kernel_member",
	"kernel_scalar_subset",
	"kernel_scalar_disjoint",
	"kernel_seq_subset",
	"kernel_seq_prefix",
	"kernel_seq_lex_lt",
	"kernel_seq_eq_words",
	"kernel_seq_starts_with",
	"kernel_seq_ends_with",
	"kernel_seq_includes",
	"kernel_members",
}

type request struct {
	symbol string
	first  string
	second string
	two    bool
	out    chan answer
}

type answer struct {
	raw string
	err error
}

// NativeKernel is one loaded dylib with its worker thread. Ask it
// with Call1/Call2 by symbol name; the answer is the kernel's JSON.
type NativeKernel struct {
	requests chan request
	// InitMs is what init_lean_wasm took, for the load report.
	InitMs float64
}

// InstantiateNative dlopens the dylib, initializes the Lean runtime
// on the worker thread, and resolves every question symbol — a
// missing symbol fails here, never at ask time.
func InstantiateNative(dylibPath string) (*NativeKernel, error) {
	kernel := &NativeKernel{requests: make(chan request)}
	ready := make(chan error)
	go kernel.serve(dylibPath, ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return kernel, nil
}

// serve owns the dylib for the kernel's lifetime, pinned to one OS
// thread. It resolves symbols, runs init, reports readiness, then
// answers requests until the channel closes.
func (k *NativeKernel) serve(dylibPath string, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cPath := C.CString(dylibPath)
	handle := C.dlopen(cPath, C.RTLD_NOW)
	C.free(unsafe.Pointer(cPath))
	if handle == nil {
		ready <- fmt.Errorf("kernel: dlopen failed for %s: %s",
			dylibPath, C.GoString(C.dlerror()))
		return
	}

	resolve := func(name string) (unsafe.Pointer, error) {
		cName := C.CString(name)
		defer C.free(unsafe.Pointer(cName))
		p := C.dlsym(handle, cName)
		if p == nil {
			return nil, fmt.Errorf("kernel: %s missing from %s: %s",
				name, dylibPath, C.GoString(C.dlerror()))
		}
		return p, nil
	}

	symbols := make(map[string]unsafe.Pointer,
		len(oneArgSymbols)+len(twoArgSymbols))
	var resolveErr error
	for _, name := range append(append([]string{}, oneArgSymbols...),
		twoArgSymbols...) {
		p, err := resolve(name)
		if err != nil {
			resolveErr = err
			break
		}
		symbols[name] = p
	}
	initFn, initErr := resolve("init_lean_wasm")
	freeFn, freeErr := resolve("free_wasm_string")
	if resolveErr != nil || initErr != nil || freeErr != nil {
		C.dlclose(handle)
		if resolveErr == nil {
			resolveErr = initErr
		}
		if resolveErr == nil {
			resolveErr = freeErr
		}
		ready <- resolveErr
		return
	}

	C.rts_init(initFn)
	ready <- nil

	for req := range k.requests {
		fn, held := symbols[req.symbol]
		if !held {
			req.out <- answer{err: fmt.Errorf(
				"kernel: no symbol %q", req.symbol)}
			continue
		}
		first := C.CString(req.first)
		var raw *C.char
		if req.two {
			second := C.CString(req.second)
			raw = C.rts_ask2(fn, first, second)
			C.free(unsafe.Pointer(second))
		} else {
			raw = C.rts_ask1(fn, first)
		}
		C.free(unsafe.Pointer(first))
		if raw == nil {
			req.out <- answer{err: fmt.Errorf(
				"kernel: %s answered a null pointer", req.symbol)}
			continue
		}
		out := C.GoString(raw)
		C.rts_free_answer(freeFn, raw)
		req.out <- answer{raw: out}
	}
	C.dlclose(handle)
}

// Call1 asks a one-argument question by symbol name.
func (k *NativeKernel) Call1(symbol string, input string) (string, error) {
	out := make(chan answer, 1)
	k.requests <- request{symbol: symbol, first: input, out: out}
	a := <-out
	return a.raw, a.err
}

// Call2 asks a two-argument question by symbol name.
func (k *NativeKernel) Call2(
	symbol string, first string, second string,
) (string, error) {
	out := make(chan answer, 1)
	k.requests <- request{
		symbol: symbol, first: first, second: second, two: true, out: out,
	}
	a := <-out
	return a.raw, a.err
}

// Close ends the worker thread; a closed kernel answers nothing.
func (k *NativeKernel) Close() {
	close(k.requests)
}

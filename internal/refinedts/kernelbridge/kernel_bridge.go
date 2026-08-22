// Loads the native kernel dylib and asks it the canonical set-function
// questions. The functions behind these calls are the PROVED kernel
// functions themselves (boundary/exports.lean puts `@[export]` on
// them) — the dylib is the kernel's own compiled code, not a mirror.
// Marshaling: JSON strings in, JSON strings out (wire_format.go /
// wire_decode.go).
//
// The TS source resolves EITHER the native dylib or the portable wasm
// build, relative to import.meta.url (glueUrl/wasmUrl/nativeUrl), and
// picks whichever is present (native preferred). This Go tree has only
// the native path — there is no wasm substrate and no Go analogue of
// import.meta.url, so DylibPath is a parameter instead of a
// self-locating constant. See instantiate_kernel.go's own test
// (../../../../../refined-lean/native/build/librefined_kernel.dylib,
// relative to the package) for the convention this mirrors.
package kernelbridge

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// DylibPath is the native kernel's default location, relative to this
// package — the same path instantiate_kernel_test.go already uses.
// Exported so callers outside this package (and this package's own
// tests) share one spelling of where the kernel lives. Correct only
// when the process's cwd IS this package's directory, which `go test`
// arranges for every package that imports it (they all happen to sit
// at the same tree depth, internal/refinedts/<pkg>) — never true for a
// built binary run from an arbitrary cwd. Binaries must resolve
// through ResolveDylibPath instead.
const DylibPath = "../../../../../refined-lean/native/build/librefined_kernel.dylib"

// dylibPathMu guards explicitDylibPath and kernelArtifactPath below —
// both are set once at process/sweep startup (SetDylibPath, LoadKernel)
// but read from any goroutine asking a question, so a setter racing a
// reader needs the same guard a genuine init-once value would.
var dylibPathMu sync.Mutex

// explicitDylibPath is the caller-stated dylib location — a plain
// setter, never an environment variable (the standing rule: behavior
// is configured by arguments, not ambient process state). Binaries
// pass it from a -kernel flag or derive it from their own layout.
var explicitDylibPath string

// SetDylibPath states where the native kernel dylib lives, for a
// process whose cwd does not sit at this package.
func SetDylibPath(path string) {
	dylibPathMu.Lock()
	defer dylibPathMu.Unlock()
	explicitDylibPath = path
}

// ResolveDylibPath is the binary-safe way to find the native kernel:
// the caller-stated path when set (SetDylibPath), else DylibPath's
// relative spelling for a process whose cwd already sits at this
// package (the `go test` case), else "", meaning the caller falls
// back to running with no kernel (every kernel question then
// declines, exactly as though the dylib were genuinely absent).
func ResolveDylibPath() string {
	dylibPathMu.Lock()
	path := explicitDylibPath
	dylibPathMu.Unlock()
	if path != "" {
		return path
	}
	if KernelArtifactsPresent(DylibPath) {
		return DylibPath
	}
	return ""
}

// KernelArtifactsPresent is kernelArtifactsPresent in the TS source,
// narrowed to the native-only substrate: true when the dylib at path
// exists.
func KernelArtifactsPresent(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// KernelArtifactPath is kernelArtifactPath in the TS source: the
// artifact the loaded kernel answers from — what the question store's
// salt must track, so a rebuilt kernel never serves another build's
// stored answers. Narrowed to the native-only substrate: always the
// dylib path handed to LoadKernel. Guarded by dylibPathMu — written
// once per LoadKernel (already serialized by kernelMu at the
// AdoptKernel call site) but read from question_cache.go's storeSalt
// under a DIFFERENT lock (questionCacheMu), so the variable itself
// still needs its own guard against that cross-lock read.
var kernelArtifactPath string

func init() {
	kernelArtifactPathFn = func() string {
		dylibPathMu.Lock()
		defer dylibPathMu.Unlock()
		return kernelArtifactPath
	}
}

// LoadKernel is loadKernel in the TS source: load, instantiate, and
// initialize the kernel dylib (once; subsequent calls share the
// instance). Returns an error if the dylib is absent. A module that
// aborted is not shared again — the next call builds a replacement
// (AdoptKernel/Invalidate).
//
// Between InstantiateNative and sharing the instance, ProbeKernelAlive
// asks one trivial, known-answer question on the freshly loaded dylib.
// This is a LOAD-TIME check, not a mid-session one: the dylib runs
// in-process (dlopen'd straight into this binary, on the locked worker
// OS thread instantiate_kernel.go's serve owns), and
// lean_set_exit_on_panic(true) (kernel_wrapper.c) means a Lean panic
// during any later ask ABORTS THIS PROCESS — there is no host thread
// left afterward to detect the death, quarantine the kernel, or answer
// KernelDied's decline for it. The probe cannot catch that failure
// mode (a panic serving fabricated output was already ruled out by the
// abort, and an abort is not observable, it is fatal). What it CAN
// catch: dlopen succeeded and every symbol resolved, but the worker
// answers the wrong boolean or malformed JSON to a question with a
// known answer — evidence the dylib itself is bad before any caller's
// real question ever reaches it. See KernelDied's comment for what a
// genuine mid-session death means and why quarantining it needs a
// different (out-of-process) architecture.
func LoadKernel(dylibPath string) (*RefinedTSKernel, error) {
	return AdoptKernel(func() (*RefinedTSKernel, *NativeKernel, error) {
		if !KernelArtifactsPresent(dylibPath) {
			return nil, nil, &kernelArtifactsAbsentError{path: dylibPath}
		}
		native, err := InstantiateNative(dylibPath)
		if err != nil {
			return nil, nil, err
		}
		if err := ProbeKernelAlive(native); err != nil {
			native.Close()
			return nil, nil, err
		}
		dylibPathMu.Lock()
		kernelArtifactPath = dylibPath
		dylibPathMu.Unlock()
		kernel := KernelFromCalls(KernelFromCallsInput{Native: native, InitMs: native.InitMs})
		return kernel, native, nil
	})
}

// ProbeKernelAlive asks one trivial, known-answer question — is the
// impossible set (max 5 AND min 10) scalar-empty — directly on the
// NativeKernel, bypassing the cache and cost recorder (a probe answer
// is not a real question and must not be remembered as one). The
// known answer is `true`; anything else (a transport error, malformed
// JSON, or the wrong boolean) fails the load before any caller ever
// shares this instance.
func ProbeKernelAlive(native *NativeKernel) error {
	const impossibleSet = `{"forms":[{"form":"atMost","a":{"num":5,"exp":0}},{"form":"atLeast","a":{"num":10,"exp":0}}]}`
	raw, err := native.Call1("kernel_scalar_empty", impossibleSet)
	if err != nil {
		return fmt.Errorf("kernel: load probe failed: %w", err)
	}
	var parsed map[string]any
	if jsonErr := json.Unmarshal([]byte(raw), &parsed); jsonErr != nil {
		return fmt.Errorf("kernel: load probe answered non-JSON: %q", raw)
	}
	if message, held := parsed["error"].(string); held {
		return fmt.Errorf("kernel: load probe declined: %s", message)
	}
	empty, held := parsed["empty"].(bool)
	if !held {
		return fmt.Errorf("kernel: load probe answered without a boolean \"empty\": %s", raw)
	}
	if !empty {
		return fmt.Errorf(
			"kernel: load probe answered false for a known-empty set — the dylib is serving wrong answers")
	}
	return nil
}

type kernelArtifactsAbsentError struct{ path string }

func (e *kernelArtifactsAbsentError) Error() string {
	return "Kernel artifacts not found at " + e.path + " — build the native dylib first."
}

// KernelIfLoaded is kernelIfLoaded in the TS source: the kernel when
// initialization has already completed — the synchronous seam quick
// info reads through. A hover before the first check simply says
// less; it never awaits (the Go twin never awaited to begin with,
// since LoadKernel is synchronous here — see AdoptKernel's file
// comment on the race this loses against the TS Promise cache).
func KernelIfLoaded() *RefinedTSKernel {
	return LoadedKernel()
}

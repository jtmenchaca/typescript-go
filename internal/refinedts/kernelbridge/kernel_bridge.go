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
// (../../../../refined-ts-lean/native/build/librefinedts_kernel.dylib,
// relative to the package) for the convention this mirrors.
package kernelbridge

import "os"

// DylibPath is the native kernel's default location, relative to this
// package — the same path instantiate_kernel_test.go already uses.
// Exported so callers outside this package (and this package's own
// tests) share one spelling of where the kernel lives. Correct only
// when the process's cwd IS this package's directory, which `go test`
// arranges for every package that imports it (they all happen to sit
// at the same tree depth, internal/refinedts/<pkg>) — never true for a
// built binary run from an arbitrary cwd. Binaries must resolve
// through ResolveDylibPath instead.
const DylibPath = "../../../../refined-ts-lean/native/build/librefinedts_kernel.dylib"

// explicitDylibPath is the caller-stated dylib location — a plain
// setter, never an environment variable (the standing rule: behavior
// is configured by arguments, not ambient process state). Binaries
// pass it from a -kernel flag or derive it from their own layout.
var explicitDylibPath string

// SetDylibPath states where the native kernel dylib lives, for a
// process whose cwd does not sit at this package.
func SetDylibPath(path string) {
	explicitDylibPath = path
}

// ResolveDylibPath is the binary-safe way to find the native kernel:
// the caller-stated path when set (SetDylibPath), else DylibPath's
// relative spelling for a process whose cwd already sits at this
// package (the `go test` case), else "", meaning the caller falls
// back to running with no kernel (every kernel question then
// declines, exactly as though the dylib were genuinely absent).
func ResolveDylibPath() string {
	if explicitDylibPath != "" {
		return explicitDylibPath
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
// dylib path handed to LoadKernel.
var kernelArtifactPath string

func init() {
	kernelArtifactPathFn = func() string { return kernelArtifactPath }
}

// LoadKernel is loadKernel in the TS source: load, instantiate, and
// initialize the kernel dylib (once; subsequent calls share the
// instance). Returns an error if the dylib is absent. A module that
// aborted is not shared again — the next call builds a replacement
// (AdoptKernel/Invalidate).
func LoadKernel(dylibPath string) (*RefinedTSKernel, error) {
	return AdoptKernel(func() (*RefinedTSKernel, *NativeKernel, error) {
		if !KernelArtifactsPresent(dylibPath) {
			return nil, nil, &kernelArtifactsAbsentError{path: dylibPath}
		}
		native, err := InstantiateNative(dylibPath)
		if err != nil {
			return nil, nil, err
		}
		kernelArtifactPath = dylibPath
		kernel := KernelFromCalls(KernelFromCallsInput{Native: native, InitMs: native.InitMs})
		return kernel, native, nil
	})
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

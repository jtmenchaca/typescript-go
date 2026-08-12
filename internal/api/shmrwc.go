package api

// A shared-memory byte stream for the synchronous API: two
// single-producer single-consumer rings in one mmapped file — ring 0
// carries client→server bytes, ring 1 server→client. Each ring is a
// head counter (written by the producer), a tail counter (written by
// the consumer) on its own cache line, and a power-of-two data area.
// The counters grow monotonically and wrap modulo the capacity, so
// head−tail is the bytes in flight. A read spins briefly and then
// sleeps in short steps, keeping burst latency in the microseconds
// while an idle server costs no core.
//
// The client creates and zeroes the file before spawning the server
// with --shm <path>; the server maps the same file. Message framing
// above this layer is unchanged — the MessagePack tuple protocol
// reads the rings as an ordinary byte stream.

/*
#cgo LDFLAGS: -framework CoreServices
#include <stddef.h>
#include <stdint.h>
// os/os_sync_wait_on_address.h (macOS 14.4+): futex-style wait on a
// shared-memory address — returns when *addr no longer holds value,
// on wake, or on timeout (nanoseconds); flags 1 = *_SHARED, clock 32
// = OS_CLOCK_MACH_ABSOLUTE_TIME.
extern int os_sync_wait_on_address_with_timeout(void *addr, uint64_t value, size_t size, uint32_t flags, uint32_t clockid, uint64_t timeout_ns);
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const (
	shmRingCapacity = 1 << 22 // 4 MiB per direction
	shmHeaderSize   = 128     // head at 0, tail at 64, data after
	shmRingSize     = shmHeaderSize + shmRingCapacity
	shmFileSize     = 2 * shmRingSize
)

type shmRing struct {
	head *uint32 // producer cursor
	tail *uint32 // consumer cursor
	data []byte
}

func ringAt(mapped []byte, offset int) shmRing {
	return shmRing{
		head: (*uint32)(unsafe.Pointer(&mapped[offset])),
		tail: (*uint32)(unsafe.Pointer(&mapped[offset+64])),
		data: mapped[offset+shmHeaderSize : offset+shmRingSize],
	}
}

// ShmRWC is the server's end: Read consumes ring 0, Write produces
// into ring 1.
type ShmRWC struct {
	mapped []byte
	file   *os.File
	in     shmRing
	out    shmRing
	closed atomic.Bool
}

// NewShmRWC maps the client-created ring file.
func NewShmRWC(path string) (*ShmRWC, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("shm: open %s: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.Size() < shmFileSize {
		file.Close()
		return nil, fmt.Errorf("shm: %s holds %d bytes, need %d", path, info.Size(), shmFileSize)
	}
	mapped, err := syscall.Mmap(int(file.Fd()), 0, shmFileSize,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("shm: mmap: %w", err)
	}
	return &ShmRWC{
		mapped: mapped,
		file:   file,
		in:     ringAt(mapped, 0),
		out:    ringAt(mapped, shmRingSize),
	}, nil
}

func (s *ShmRWC) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	spins := 0
	for {
		if s.closed.Load() {
			return 0, os.ErrClosed
		}
		head := atomic.LoadUint32(s.in.head)
		tail := atomic.LoadUint32(s.in.tail)
		available := head - tail
		if available > 0 {
			at := int(tail % shmRingCapacity)
			chunk := int(available)
			if chunk > len(b) {
				chunk = len(b)
			}
			if wrap := shmRingCapacity - at; chunk > wrap {
				chunk = wrap
			}
			copy(b[:chunk], s.in.data[at:at+chunk])
			atomic.StoreUint32(s.in.tail, tail+uint32(chunk))
			return chunk, nil
		}
		spins++
		if spins < 24_576 {
			// hot across the sweep's typical inter-ask gap (~0.4ms):
			// a bounded few-millisecond spin, so consecutive questions
			// never pay the kernel's wake-to-run latency. Bounded is
			// the point — an unbounded spin was measured SLOWER (it
			// pinned cores against the single-threaded client).
			runtime.Gosched()
		} else {
			// block on the head counter itself: zero CPU while waiting,
			// microsecond wake when the client stores a new head and
			// wakes the address. The timeout is only the safety net for
			// a client built without the wake call. (Sleep polling was
			// measured as a ~0.5–1ms tax on EVERY ask of a corpus
			// sweep — Go's timer rounds tiny sleeps up; a hot spin was
			// worse, stealing cores from the single-threaded client.)
			C.os_sync_wait_on_address_with_timeout(
				unsafe.Pointer(s.in.head), C.uint64_t(head), 4,
				1,  // OS_SYNC_WAIT_ON_ADDRESS_SHARED
				32, // OS_CLOCK_MACH_ABSOLUTE_TIME
				C.uint64_t(10_000_000), // 10ms
			)
		}
	}
}

func (s *ShmRWC) Write(b []byte) (int, error) {
	written := 0
	for written < len(b) {
		if s.closed.Load() {
			return written, os.ErrClosed
		}
		head := atomic.LoadUint32(s.out.head)
		tail := atomic.LoadUint32(s.out.tail)
		free := shmRingCapacity - (head - tail)
		if free == 0 {
			runtime.Gosched()
			continue
		}
		at := int(head % shmRingCapacity)
		chunk := len(b) - written
		if chunk > int(free) {
			chunk = int(free)
		}
		if wrap := shmRingCapacity - at; chunk > wrap {
			chunk = wrap
		}
		copy(s.out.data[at:at+chunk], b[written:written+chunk])
		// the atomic store publishes the data copied above: no reader
		// sees the new head before the bytes it counts
		atomic.StoreUint32(s.out.head, head+uint32(chunk))
		written += chunk
	}
	return written, nil
}

func (s *ShmRWC) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	err := syscall.Munmap(s.mapped)
	s.file.Close()
	return err
}

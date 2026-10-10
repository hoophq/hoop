package codec

import "unsafe"

// live holds every buffer the host may still touch, keyed by its address.
//
// TinyGo's collector is non-moving but it does reclaim unreferenced
// memory, and a buffer handed to the host is referenced by nothing on the
// Go side. The map is that reference. alloc and give insert; free, which
// the host calls when it is done, deletes. The same map answers "which
// buffer is at ptr" for the pointer-taking exports, so no export turns an
// integer back into a pointer.
var live = map[uint32][]byte{}

func pack(ptr, n uint32) uint64 { return uint64(ptr)<<32 | uint64(n) }

func unpack(packed uint64) (ptr, n uint32) { return uint32(packed >> 32), uint32(packed) }

func address(b []byte) uint32 {
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b))))
}

// alloc is the `alloc` export: n bytes the host will write.
//
//export alloc
func alloc(n uint32) uint32 {
	b := make([]byte, n)
	ptr := address(b)
	live[ptr] = b
	return ptr
}

// free is the `free` export. A second free of the same region is a no-op.
//
//export free
func free(ptr, n uint32) {
	delete(live, ptr)
}

// give hands b to the host as a packed region it frees with free(ptr,
// len). Empty is the packed 0, which the ABI reads as "no output".
func give(b []byte) uint64 {
	if len(b) == 0 {
		return 0
	}
	ptr := address(b)
	live[ptr] = b
	return pack(ptr, uint32(len(b)))
}

// take returns the region a host import handed back and releases it. The
// host wrote it through alloc, so it is in live; a region that is not is
// a host bug and traps.
func take(packed uint64) []byte {
	if packed == 0 {
		return nil
	}
	ptr, n := unpack(packed)
	b, ok := live[ptr]
	if !ok || uint32(len(b)) < n {
		panic("hoop-codec: the host returned a region alloc did not hand out")
	}
	delete(live, ptr)
	return b[:n]
}

// borrow views the region the host passed to an export. The host frees it
// after the export returns, so the slice must not outlive the call.
func borrow(ptr, n uint32) []byte {
	if n == 0 {
		return nil
	}
	b, ok := live[ptr]
	if !ok || uint32(len(b)) < n {
		panic("hoop-codec: an export received a region alloc did not hand out")
	}
	return b[:n]
}

// region passes a Go byte slice to a host import for the duration of one
// call. The caller keeps b alive across the call.
func region(b []byte) (ptr, n uint32) {
	if len(b) == 0 {
		return 0, 0
	}
	return address(b), uint32(len(b))
}

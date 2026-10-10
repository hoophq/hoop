//! The C ABI over linear memory: `alloc`/`free`, and the packed `u64`
//! every export and byte-returning import uses.
//!
//! There is no custom allocator. `alloc` is `Vec::with_capacity` with the
//! buffer forgotten, and `free` is `Vec::from_raw_parts` dropped, so the
//! bytes the host writes and the bytes a codec returns come from the same
//! global allocator as everything else in the module. One allocator means
//! one accounting the `memory_limit_pages` cap applies to.
//!
//! The pointer casts below are 32-bit by construction of the ABI. They
//! compile on a 64-bit host so a plug-in crate can `cargo test` its codec
//! there, but nothing on the host calls them: the pointer-taking exports
//! only make sense inside a wasm32 instance.

/// Packs a region into the `(ptr << 32) | len` the ABI returns.
pub fn pack(ptr: u32, len: u32) -> u64 {
    ((ptr as u64) << 32) | (len as u64)
}

/// The inverse of `pack`.
pub fn unpack(packed: u64) -> (u32, u32) {
    ((packed >> 32) as u32, packed as u32)
}

/// Allocates `len` bytes the host will write; the `alloc` export.
pub fn alloc(len: u32) -> u32 {
    let mut buf = Vec::<u8>::with_capacity(len as usize);
    let ptr = buf.as_mut_ptr();
    std::mem::forget(buf);
    ptr as usize as u32
}

/// Releases a region of exactly `len` bytes that `alloc` or `give` handed
/// out; the `free` export.
///
/// # Safety
/// `ptr` must come from `alloc(len)` or from a `give` of `len` bytes, and
/// must not be freed twice. The host holds that contract.
pub unsafe fn free(ptr: u32, len: u32) {
    drop(Vec::from_raw_parts(ptr as usize as *mut u8, 0, len as usize));
}

/// Hands `bytes` to the host as a packed region the host frees with
/// `free(ptr, len)`. Empty bytes are the packed `0`, which the ABI reads
/// as "no output".
pub fn give(bytes: Vec<u8>) -> u64 {
    if bytes.is_empty() {
        return 0;
    }
    // into_boxed_slice makes capacity == len, which `free` relies on to
    // rebuild the Vec with the layout it was allocated under.
    let boxed = bytes.into_boxed_slice();
    let len = boxed.len() as u32;
    let ptr = Box::into_raw(boxed) as *mut u8 as usize as u32;
    pack(ptr, len)
}

/// Takes ownership of a region a host import returned. The host allocated
/// it through our `alloc`, so the buffer is a Vec of exactly `len` bytes
/// and dropping it is the `free` the ABI asks for.
///
/// # Safety
/// `packed` must be the untouched return value of a host import.
pub unsafe fn take(packed: u64) -> Vec<u8> {
    let (ptr, len) = unpack(packed);
    if packed == 0 {
        return Vec::new();
    }
    Vec::from_raw_parts(ptr as usize as *mut u8, len as usize, len as usize)
}

/// Views the region the host passed to an export, without copying. The
/// host frees it after the export returns, so the slice must not outlive
/// the call.
///
/// # Safety
/// `(ptr, len)` must be the arguments of the export in progress.
pub unsafe fn borrow<'a>(ptr: u32, len: u32) -> &'a [u8] {
    if len == 0 {
        return &[];
    }
    std::slice::from_raw_parts(ptr as usize as *const u8, len as usize)
}

/// Standard base64 with padding, as RewriteResult carries its bytes.
/// Written out rather than pulled in: it is twenty lines, and the SDK's
/// dependency list is part of what a plug-in author audits.
pub fn base64(bytes: &[u8]) -> String {
    const ALPHABET: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(bytes.len().div_ceil(3) * 4);
    for chunk in bytes.chunks(3) {
        let n = (u32::from(chunk[0]) << 16)
            | (chunk.get(1).copied().map_or(0, u32::from) << 8)
            | chunk.get(2).copied().map_or(0, u32::from);
        out.push(ALPHABET[(n >> 18) as usize & 63] as char);
        out.push(ALPHABET[(n >> 12) as usize & 63] as char);
        out.push(if chunk.len() > 1 { ALPHABET[(n >> 6) as usize & 63] as char } else { '=' });
        out.push(if chunk.len() > 2 { ALPHABET[n as usize & 63] as char } else { '=' });
    }
    out
}

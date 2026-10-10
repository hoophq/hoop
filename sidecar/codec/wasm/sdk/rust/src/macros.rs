/// Emits the ABI exports for one `Codec` type.
///
/// ```ignore
/// hoop_codec::export_codec!(AcmeWire, manifest, [deny, filter, rewrite, credential, content]);
/// ```
///
/// `AcmeWire` implements `Codec` (and `Default`); `manifest` is a
/// `fn() -> Manifest`; the list names the capabilities whose exports
/// exist. The host refuses a capability without its export and an export
/// without its capability, so the list IS the set of optional exports, and
/// `describe` traps when the manifest's `capabilities` disagree with it.
///
/// Always emitted: `memory` (by the toolchain), `alloc`, `free`,
/// `describe`, `decode`, `open`, `close` and `_initialize`, which installs
/// a panic hook that forwards the message to the host log at level error
/// before the panic becomes a trap. Per capability: `deny`; `filter`;
/// `enable_rewrite`, `rewrite` and `flush`; `take_credential`; `content`.
///
/// The functions carry their ABI names only on wasm32. On a host target
/// they are ordinary functions, so `cargo test` of a plug-in crate never
/// exports a symbol called `free` next to libc's.
#[macro_export]
macro_rules! export_codec {
    ($codec:ty, $manifest:expr, [$($cap:ident),* $(,)?]) => {
        #[allow(dead_code)]
        static __HOOP_CONNS: $crate::Connections<$codec> = $crate::Connections::new();

        #[cfg_attr(target_arch = "wasm32", export_name = "alloc")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_alloc(len: u32) -> u32 {
            $crate::abi::alloc(len)
        }

        #[cfg_attr(target_arch = "wasm32", export_name = "free")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_free(ptr: u32, len: u32) {
            unsafe { $crate::abi::free(ptr, len) }
        }

        #[cfg_attr(target_arch = "wasm32", export_name = "_initialize")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_initialize() {
            $crate::install_panic_hook();
        }

        #[cfg_attr(target_arch = "wasm32", export_name = "describe")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_describe() -> u64 {
            let exported: &[$crate::Capability] = &[$($crate::export_codec!(@cap $cap)),*];
            let manifest: $crate::Manifest = ($manifest)();
            $crate::abi::give($crate::describe(&manifest, exported))
        }

        #[cfg_attr(target_arch = "wasm32", export_name = "open")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_open(conn: u32, ptr: u32, len: u32) -> u32 {
            __HOOP_CONNS.open(conn, unsafe { $crate::abi::borrow(ptr, len) })
        }

        #[cfg_attr(target_arch = "wasm32", export_name = "close")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_close(conn: u32) {
            __HOOP_CONNS.close(conn)
        }

        #[cfg_attr(target_arch = "wasm32", export_name = "decode")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_decode(conn: u32, dir: u32, ptr: u32, len: u32) -> u64 {
            $crate::abi::give(__HOOP_CONNS.decode(conn, dir, unsafe { $crate::abi::borrow(ptr, len) }))
        }

        $($crate::export_codec!(@export $cap);)*
    };

    (@cap deny) => { $crate::Capability::Deny };
    (@cap filter) => { $crate::Capability::Filter };
    (@cap rewrite) => { $crate::Capability::Rewrite };
    (@cap credential) => { $crate::Capability::Credential };
    (@cap content) => { $crate::Capability::Content };

    (@export deny) => {
        #[cfg_attr(target_arch = "wasm32", export_name = "deny")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_deny(conn: u32, dir: u32, ptr: u32, len: u32) -> u64 {
            $crate::abi::give(__HOOP_CONNS.deny(conn, dir, unsafe { $crate::abi::borrow(ptr, len) }))
        }
    };

    (@export filter) => {
        #[cfg_attr(target_arch = "wasm32", export_name = "filter")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_filter(conn: u32, dir: u32, ptr: u32, len: u32) -> u64 {
            $crate::abi::give(__HOOP_CONNS.filter(conn, dir, unsafe { $crate::abi::borrow(ptr, len) }))
        }
    };

    (@export rewrite) => {
        #[cfg_attr(target_arch = "wasm32", export_name = "enable_rewrite")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_enable_rewrite(conn: u32) {
            __HOOP_CONNS.enable_rewrite(conn)
        }

        #[cfg_attr(target_arch = "wasm32", export_name = "rewrite")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_rewrite(conn: u32, ptr: u32, len: u32) -> u64 {
            $crate::abi::give(__HOOP_CONNS.rewrite(conn, unsafe { $crate::abi::borrow(ptr, len) }))
        }

        #[cfg_attr(target_arch = "wasm32", export_name = "flush")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_flush(conn: u32) -> u64 {
            $crate::abi::give(__HOOP_CONNS.flush(conn))
        }
    };

    (@export credential) => {
        #[cfg_attr(target_arch = "wasm32", export_name = "take_credential")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_take_credential(conn: u32, ptr: u32, len: u32) -> u64 {
            $crate::abi::give(__HOOP_CONNS.take_credential(conn, unsafe { $crate::abi::borrow(ptr, len) }))
        }
    };

    (@export content) => {
        #[cfg_attr(target_arch = "wasm32", export_name = "content")]
        #[allow(dead_code)]
        pub extern "C" fn __hoop_content(conn: u32, ptr: u32, len: u32) -> u64 {
            $crate::abi::give(__HOOP_CONNS.content(conn, unsafe { $crate::abi::borrow(ptr, len) }))
        }
    };
}

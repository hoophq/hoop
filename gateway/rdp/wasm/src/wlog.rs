use log::{Log, Metadata, Record};

/// Logs a message to the console using [`_log`].
pub fn raw_log(level: u32, message: &String) {
    unsafe {
        let (ptr, len) = string_to_ptr(message);
        _log(level, ptr, len);
    }
}

#[link(wasm_import_module = "env")]
extern "C" {
    /// WebAssembly import which prints a string (linear memory offset,
    /// byteCount) to the console.
    ///
    /// Note: This is not an ownership transfer: Rust still owns the pointer
    /// and ensures it isn't deallocated during this call.
    #[link_name = "log"]
    fn _log(level: u32, ptr: u32, size: u32);
}

/// Returns a pointer and size pair for the given string in a way compatible
/// with WebAssembly numeric types.
///
/// Note: This doesn't change the ownership of the String. To intentionally
/// leak it, use [`std::mem::forget`] on the input after calling this.
unsafe fn string_to_ptr(s: &String) -> (u32, u32) {
    return (s.as_ptr() as u32, s.len() as u32);
}

pub struct WLog;

impl Log for WLog {
    fn enabled(&self, metadata: &Metadata) -> bool {
        // Enable all log levels. You can customize this to filter by level or target.
        true
    }

    fn log(&self, record: &Record) {
        if self.enabled(record.metadata()) {
            let message = format!(
                "[{}] {} - {}:{} - {}",
                record.level(),
                record.target(),
                record.file().unwrap_or("unknown"),
                record.line().unwrap_or(0),
                record.args()
            );
            raw_log(record.level() as u32, &message);
        }
    }

    fn flush(&self) {}
}

pub(crate) static LOGGER: WLog = WLog;
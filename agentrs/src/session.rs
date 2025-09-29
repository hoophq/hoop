use uuid::Uuid;

// Binary package format:
// [ sid(16 bytes) | len(4 bytes) | payload(len bytes) ]
// a 20 bytes header with a UUID and a length field,
// followed by the payload of the specified length.
#[derive(Debug, Clone, Copy)]
pub struct Header {
    pub sid: Uuid,
    pub len: u32,
}

const UUID_LEN: usize = 16;
const DATA_SIZE_LEN: usize = size_of::<u32>();
const HEADER_LEN: usize = UUID_LEN + DATA_SIZE_LEN;

impl Header {
    pub fn encode(self) -> [u8; 20] {
        let mut buf = [0u8; 20];
        buf[..16].copy_from_slice(self.sid.as_bytes());
        buf[16..].copy_from_slice(&self.len.to_be_bytes());
        buf
    }

    pub fn decode(buf: &[u8]) -> Option<(Header, usize)> {
        if buf.len() < HEADER_LEN {
            //#TODO should we return an error here
            return None;
        }

        let uuid_bytes = buf.get(..UUID_LEN);

        if uuid_bytes.unwrap_or(&[]).len() < UUID_LEN {
            return None;
        }

        let sid = match Uuid::from_slice(uuid_bytes?) {
            Ok(s) => s,
            Err(_) => return None,
        }; // #TODO should we return an error here
        //
        if sid.is_nil() {
            return None;
        }

        let len_bytes = buf.get(UUID_LEN..HEADER_LEN);
        let len = u32::from_be_bytes(len_bytes?.try_into().unwrap()); // slice size is guaranteed

        if len == 0 {
            return None;
        }

        Some((Header { sid, len }, HEADER_LEN))
    }
}

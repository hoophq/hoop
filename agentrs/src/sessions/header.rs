use uuid::Uuid;

// Binary package format:
// [ sid(16 bytes) | len(4 bytes) | payload(len bytes) ]
// a 20 bytes header with a UUID and a length field,
// followed by the payload of the specified length.
pub struct Header {
    pub sid: Uuid,
    pub len: u32,
}

impl Header {
    pub fn encode(self) -> [u8; 20] {
        let mut buf = [0u8; 20];
        buf[..16].copy_from_slice(self.sid.as_bytes());
        buf[16..].copy_from_slice(&self.len.to_be_bytes());
        buf
    }
    pub fn decode(buf: &[u8]) -> Option<(Header, usize)> {
        // Check we have at least 20 bytes for UUID (16) + length (4)
        if buf.len() < 20 {
            return None;
        }

        // Read UUID bytes
        let uuid_bytes = &buf[..16];
        let sid = Uuid::from_slice(uuid_bytes).ok()?;

        // Read length (big-endian, network order)
        let len_bytes = &buf[16..20];
        let len = u32::from_be_bytes(len_bytes.try_into().ok()?);

        // Return the header + how many bytes we consumed
        Some((Header { sid, len }, 20))
    }
}

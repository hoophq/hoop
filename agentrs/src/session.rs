use uuid::Uuid;

// Binary package format:
// [ sid(16 bytes) | len(4 bytes) | payload(len bytes) ]
// a 20 bytes header with a UUID and a length field,
// followed by the payload of the specified length.
#[derive(Debug, Clone, Copy)]
pub struct Header {
    pub sid: Uuid,
    pub len: u32,
    pub data_size: usize,
}
const UUID_LEN: usize = 16;
const DATA_SIZE_LEN: usize = size_of::<u32>();
const HEADER_LEN: usize = UUID_LEN + DATA_SIZE_LEN;

impl Header {
    pub fn encode(&self) -> Vec<u8> {
        let mut buf = Vec::with_capacity(self.data_size);

        // Add SID (16 bytes)
        buf.extend_from_slice(self.sid.as_bytes());
        // Add length (4 bytes, big endian)
        buf.extend_from_slice(&self.len.to_be_bytes());

        buf
    }

    pub fn decode(data: &[u8]) -> Option<Self> {
        if data.len() < 20 {
            return None;
        }

        let sid_bytes: [u8; 16] = data[0..16].try_into().ok()?;
        let sid = Uuid::from_bytes(sid_bytes);

        let len = u32::from_be_bytes([data[16], data[17], data[18], data[19]]);

        Some(Header {
            sid,
            len,
            data_size: HEADER_LEN,
        })
    }
}

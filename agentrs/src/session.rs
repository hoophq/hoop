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
    pub control: bool,
}
const UUID_LEN: usize = 16;
const DATA_SIZE_LEN: usize = size_of::<u32>();
pub(crate) const HEADER_LEN: usize = UUID_LEN + DATA_SIZE_LEN;
const CONTROL_FLAG: u32 = 1 << 31;
const LENGTH_MASK: u32 = !CONTROL_FLAG;

impl Header {
    pub fn encode(&self) -> Vec<u8> {
        assert!(self.len <= LENGTH_MASK, "frame payload exceeds wire limit");
        let wire_len = if self.control {
            self.len | CONTROL_FLAG
        } else {
            self.len
        };
        let mut buf = Vec::with_capacity(self.data_size);

        buf.extend_from_slice(self.sid.as_bytes());
        buf.extend_from_slice(&wire_len.to_be_bytes());

        buf
    }

    pub fn decode(data: &[u8]) -> Option<Self> {
        if data.len() < HEADER_LEN {
            return None;
        }

        let sid_bytes: [u8; UUID_LEN] = data[..UUID_LEN].try_into().ok()?;
        let sid = Uuid::from_bytes(sid_bytes);
        let wire_len = u32::from_be_bytes(data[UUID_LEN..HEADER_LEN].try_into().ok()?);
        let len = wire_len & LENGTH_MASK;
        let payload_len = usize::try_from(len).ok()?;
        if HEADER_LEN.checked_add(payload_len)? != data.len() {
            return None;
        }

        Some(Header {
            sid,
            len,
            data_size: HEADER_LEN,
            control: wire_len & CONTROL_FLAG != 0,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn frame(declared_len: u32, control: bool, payload: &[u8]) -> Vec<u8> {
        let header = Header {
            sid: Uuid::new_v4(),
            len: declared_len,
            data_size: HEADER_LEN,
            control,
        };
        let mut frame = header.encode();
        frame.extend_from_slice(payload);
        frame
    }

    #[test]
    fn decode_requires_exact_payload_length_and_preserves_kind() {
        let raw = frame(3, false, b"rdp");
        let decoded = Header::decode(&raw).expect("valid raw frame");
        assert!(!decoded.control);
        assert_eq!(decoded.len, 3);

        let control = frame(2, true, b"{}");
        assert!(
            Header::decode(&control)
                .expect("valid control frame")
                .control
        );

        assert!(Header::decode(&frame(2, false, b"rdp")).is_none());
        assert!(Header::decode(&frame(4, false, b"rdp")).is_none());
    }
}

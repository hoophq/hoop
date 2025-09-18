use crate::protocol::Protocol;
use std::collections::HashMap;

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
const LEN_LEN: usize = size_of::<u32>();
const HEADER_LEN: usize = UUID_LEN + LEN_LEN;

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

        let uuid_bytes = match buf.get(..UUID_LEN) {
            Some(b) => Some(b),
            _ => None,
        }; // #TODO should never happen due to length check above

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

pub enum ConnectionDetails {
    Rdp {
        username: String,
        password: String,
        domain: Option<String>,
        port: u16,
    },
}
pub struct SessionInfo {
    pub sid: Uuid,
    pub protocol: Protocol,
    pub conn_details: ConnectionDetails,
    // stream: TcpStream,
}

pub struct SessionManager {
    sessions: HashMap<Uuid, SessionInfo>,
}

impl SessionManager {
    pub fn new() -> Self {
        SessionManager {
            sessions: HashMap::new(),
        }
    }

    pub fn create_session(&mut self, protocol: Protocol, conn_details: ConnectionDetails) -> Uuid {
        let sid = Uuid::new_v4();
        let session_info = SessionInfo {
            sid,
            protocol,
            conn_details,
        };
        self.sessions.insert(sid, session_info);
        sid
    }

    pub fn get_session(&self, sid: &Uuid) -> Option<&SessionInfo> {
        self.sessions.get(sid)
    }

    pub fn remove_session(&mut self, sid: &Uuid) -> Option<SessionInfo> {
        self.sessions.remove(sid)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn test_header_encode_decode() {
        let original_header = Header {
            sid: Uuid::new_v4(),
            len: 12345,
        };
        let encoded = original_header.encode();
        let (decoded_header, consumed) = Header::decode(&encoded).expect("Decoding failed");

        assert_eq!(consumed, 20);
        assert_eq!(original_header.sid, decoded_header.sid);
        assert_eq!(original_header.len, decoded_header.len);
    }

    #[test]
    fn test_header_decode_incomplete() {
        let buf = [0u8; 10]; // Less than 20 bytes
        assert!(Header::decode(&buf).is_none());
    }

    #[test]
    fn test_header_decode_invalid_uuid() {
        let mut buf = [0u8; 20];
        buf[..16].copy_from_slice(&[0xFF; 16]); // Invalid UUID bytes
        buf[16..].copy_from_slice(&12345u32.to_be_bytes());
        let hd = Header::decode(&buf);

        //TODO panick fix
        //   assert!(hd.is_none());
    }
}

use serde::{Deserialize, Deserializer, Serialize, Serializer};
use std::collections::HashMap;
use uuid::Uuid;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebSocketMessage {
    #[serde(rename = "type")]
    pub message_type: String,
    pub metadata: HashMap<String, String>,
    #[serde(
        serialize_with = "serialize_payload",
        deserialize_with = "deserialize_payload"
    )]
    pub payload: Vec<u8>,
}

fn serialize_payload<S>(payload: &Vec<u8>, serializer: S) -> Result<S::Ok, S::Error>
where
    S: Serializer,
{
    use base64::Engine;
    let encoded = base64::engine::general_purpose::STANDARD.encode(payload);
    serializer.serialize_str(&encoded)
}

fn deserialize_payload<'de, D>(deserializer: D) -> Result<Vec<u8>, D::Error>
where
    D: Deserializer<'de>,
{
    use base64::Engine;
    let s = String::deserialize(deserializer)?;
    base64::engine::general_purpose::STANDARD
        .decode(s)
        .map_err(serde::de::Error::custom)
}

#[derive(Debug, Clone)]
pub struct Header {
    pub sid: Uuid,
    pub len: u32,
}

impl Header {
    pub fn encode(&self) -> Vec<u8> {
        let mut buf = Vec::with_capacity(20);

        // Add SID (16 bytes)
        buf.extend_from_slice(self.sid.as_bytes());

        // Add length (4 bytes, big endian)
        buf.extend_from_slice(&self.len.to_be_bytes());

        buf
    }

    pub fn decode(data: &[u8]) -> Option<(Self, usize)> {
        if data.len() < 20 {
            return None;
        }

        // Extract SID (first 16 bytes)
        let sid_bytes: [u8; 16] = data[0..16].try_into().ok()?;
        let sid = Uuid::from_bytes(sid_bytes);

        // Extract length (next 4 bytes, big endian)
        let len = u32::from_be_bytes([data[16], data[17], data[18], data[19]]);

        Some((Header { sid, len }, 20))
    }
}

// Protocol types
pub const PROTOCOL_RDP: &str = "rdp";

// Message types
pub const MESSAGE_TYPE_SESSION_STARTED: &str = "session_started";
pub const MESSAGE_TYPE_DATA: &str = "data";

impl WebSocketMessage {
    pub fn new(message_type: String, metadata: HashMap<String, String>, payload: Vec<u8>) -> Self {
        Self {
            message_type,
            metadata,
            payload,
        }
    }

    pub fn encode_with_header(&self, session_id: Uuid) -> Result<Vec<u8>, serde_json::Error> {
        // Serialize the message to JSON
        let json_data = serde_json::to_vec(self)?;

        // Create header
        let header = Header {
            sid: session_id,
            len: json_data.len() as u32,
        };

        // Combine header + JSON data
        let mut result = Vec::with_capacity(20 + json_data.len());
        result.extend_from_slice(&header.encode());
        result.extend_from_slice(&json_data);

        Ok(result)
    }

    pub fn decode_with_header(
        data: &[u8],
    ) -> Result<(Uuid, Self), Box<dyn std::error::Error + Send + Sync>> {
        // Decode header
        let (header, header_len) = Header::decode(data).ok_or("Failed to decode header")?;

        // Extract JSON payload
        if data.len() < header_len {
            return Err("Insufficient data for payload".into());
        }

        let json_data = &data[header_len..];
        let message: WebSocketMessage = serde_json::from_slice(json_data)?;

        Ok((header.sid, message))
    }
}

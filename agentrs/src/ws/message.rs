use serde::{Deserialize, Deserializer, Serialize, Serializer};
use std::collections::HashMap;
use uuid::Uuid;

use crate::{session::Header, ws::message_types::MessageType};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebSocketMessage {
    #[serde(rename = "type")]
    pub message_type: MessageType,
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

// Protocol types
pub const PROTOCOL_RDP: &str = "rdp";

impl WebSocketMessage {
    pub fn new(
        message_type: MessageType,
        metadata: HashMap<String, String>,
        payload: Vec<u8>,
    ) -> Self {
        Self {
            message_type,
            metadata,
            payload,
        }
    }

    pub fn encode_with_header(&self, session_id: Uuid) -> Result<Vec<u8>, serde_json::Error> {
        // Serialize the message to JSON
        let json_data = serde_json::to_vec(self)?;

        let data_size_header = 20;
        // Create header
        let header = Header {
            sid: session_id,
            len: json_data.len() as u32,
            data_size: data_size_header,
        };

        // Combine header + JSON data
        let mut result = Vec::with_capacity(data_size_header + json_data.len());
        result.extend_from_slice(&header.encode());
        result.extend_from_slice(&json_data);

        Ok(result)
    }

    pub fn decode_with_header(
        data: &[u8],
    ) -> Result<(Uuid, Self), Box<dyn std::error::Error + Send + Sync>> {
        // Decode header
        let header = Header::decode(data).ok_or("Failed to decode header")?;

        // Extract JSON payload
        if data.len() < header.data_size {
            return Err("Insufficient data for payload".into());
        }

        let json_data = &data[header.data_size..];
        let message: WebSocketMessage = serde_json::from_slice(json_data)?;

        Ok((header.sid, message))
    }
}

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
const DATA_SIZE_HEADER: usize = 20;

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

    pub fn encode_with_header(&self, sid: Uuid) -> Result<Vec<u8>, serde_json::Error> {
        // Serialize the message to JSON
        let json_data = serde_json::to_vec(self)?;

        // Create header
        let header = Header {
            sid,
            len: json_data.len() as u32,
            data_size: DATA_SIZE_HEADER,
            control: true,
        };

        // Combine header + JSON data
        let mut result = Vec::with_capacity(DATA_SIZE_HEADER + json_data.len());
        result.extend_from_slice(&header.encode());
        result.extend_from_slice(&json_data);

        Ok(result)
    }

    pub fn decode_with_header(
        data: &[u8],
    ) -> Result<(Uuid, Self), Box<dyn std::error::Error + Send + Sync>> {
        // Decode header
        let header = Header::decode(data).ok_or("Failed to decode header")?;
        if !header.control {
            return Err("Expected a control frame".into());
        }

        // Header::decode already proved the payload is present at the exact
        // declared length.
        let json_data = &data[header.data_size..];
        let message: WebSocketMessage = serde_json::from_slice(json_data)?;

        Ok((header.sid, message))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn control_decoder_rejects_wrong_lengths_and_raw_frames() {
        let sid = Uuid::new_v4();
        let message = WebSocketMessage::new(MessageType::Data, HashMap::new(), b"payload".to_vec());
        let frame = message
            .encode_with_header(sid)
            .expect("encode control frame");
        let (decoded_sid, decoded) =
            WebSocketMessage::decode_with_header(&frame).expect("decode control frame");
        assert_eq!(decoded_sid, sid);
        assert_eq!(decoded.payload, b"payload");

        let wire_len = u32::from_be_bytes(frame[16..20].try_into().unwrap());
        assert_ne!(wire_len & (1 << 31), 0, "control flag missing");

        let mut declared_short = frame.clone();
        declared_short[16..20].copy_from_slice(&(wire_len - 1).to_be_bytes());
        assert!(WebSocketMessage::decode_with_header(&declared_short).is_err());

        let mut trailing = frame.clone();
        trailing.push(0);
        assert!(WebSocketMessage::decode_with_header(&trailing).is_err());

        let json = serde_json::to_vec(&message).unwrap();
        let raw_header = Header {
            sid,
            len: json.len() as u32,
            data_size: DATA_SIZE_HEADER,
            control: false,
        };
        let mut raw_frame = raw_header.encode();
        raw_frame.extend_from_slice(&json);
        assert!(WebSocketMessage::decode_with_header(&raw_frame).is_err());
    }
}

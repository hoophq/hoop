use std::str::FromStr;

use serde::{Deserialize, Deserializer, Serialize, Serializer};

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub enum MessageType {
    SessionStarted,
    Data,
    Unknown,
}

// Message types str
pub const MESSAGE_TYPE_SESSION_STARTED: &str = "session_started";
pub const MESSAGE_TYPE_DATA: &str = "data";
impl ToString for MessageType {
    fn to_string(&self) -> String {
        match self {
            MessageType::SessionStarted => MESSAGE_TYPE_SESSION_STARTED.to_string(),
            MessageType::Data => MESSAGE_TYPE_DATA.to_string(),
            MessageType::Unknown => "unknown".to_string(),
        }
    }
}

impl FromStr for MessageType {
    type Err = String;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        match s {
            "session_started" => Ok(MessageType::SessionStarted),
            "data" => Ok(MessageType::Data),
            _ => Err(format!("Unknown message type: {}", s)),
        }
    }
}
impl Serialize for MessageType {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        serializer.serialize_str(&self.to_string())
    }
}

impl<'de> Deserialize<'de> for MessageType {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let s = String::deserialize(deserializer)?;
        MessageType::from_str(&s).map_err(serde::de::Error::custom)
    }
}

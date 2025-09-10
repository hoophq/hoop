use std::collections::HashMap;

use uuid::Uuid;

use crate::protocol::Protocol;

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
}

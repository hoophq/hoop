use uuid::Uuid;



#[derive(Clone)]
pub struct SessionInfo {
    pub session_id: Uuid,
    pub target_address: String,
    pub username: String,
    pub password: String,
    pub client_address: String,
}

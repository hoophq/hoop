use uuid::Uuid;
use zeroize::Zeroizing;

#[derive(Debug)]
pub struct SessionInfo {
    pub sid: Uuid,
    pub target_address: String,
    pub username: String,
    pub password: Zeroizing<String>,
    pub proxy_user: Zeroizing<String>,
    pub client_address: String,
    /// Agent-side PII guard config, resolved from the SessionStarted
    /// metadata (gateway policy) and agent env (endpoints). None = unguarded.
    pub guard: Option<crate::guard::GuardConfig>,
}

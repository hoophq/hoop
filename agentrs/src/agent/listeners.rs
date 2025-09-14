use std::sync::Arc;

use crate::conf;

pub struct AgentListener {
    config: Arc<conf::Conf>,
}

pub enum ProtocolType {
    Rdp,
}
pub struct Protocol {
    protocol_type: ProtocolType,
}

impl Protocol {
    pub fn new(protocol_type: ProtocolType) -> Self {
        Self { protocol_type }
    }

    pub fn get_protocol_type(&self) -> &ProtocolType {
        &self.protocol_type
    }

    pub fn default_port(&self) -> u16 {
        match self.protocol_type {
            ProtocolType::Rdp => 3389,
        }
    }
    pub const fn as_str(self) -> &'static str {
        match self.protocol_type {
            ProtocolType::Rdp => "rdp",
        }
    }
}

/// How well a harness can emulate a given Claude hook event+handler. Each harness computes its own
/// level in its module (`harness::<name>::hooks`); this enum is the shared vocabulary.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum SupportLevel {
    Native,
    Emulated,
    Degraded { reason: &'static str },
    Unsupported { reason: &'static str },
}

impl SupportLevel {
    pub fn label(&self) -> &'static str {
        match self {
            Self::Native => "native",
            Self::Emulated => "emulated",
            Self::Degraded { .. } => "degraded",
            Self::Unsupported { .. } => "unsupported",
        }
    }
}

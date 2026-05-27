mod add_fetch;
pub mod launch_fingerprint;
mod pipeline;
mod remove;
mod run;

pub use crate::staging::LaunchTarget;
pub use pipeline::run_sync;
pub use run::{run_add, run_lock, run_remove, sync_for_launch};

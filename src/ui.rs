use std::io::{self, IsTerminal, Read};
use std::time::Duration;

use indicatif::{HumanBytes, ProgressBar, ProgressDrawTarget, ProgressStyle};

/// Terminal progress / pretty output (TTY-gated unless disabled).
#[derive(Clone, Debug)]
pub struct Ui {
    pub show_progress: bool,
    pub quiet: bool,
}

impl Ui {
    pub fn new(quiet: bool, no_progress: bool) -> Self {
        let tty = io::stdout().is_terminal();
        Self {
            show_progress: tty && !no_progress && !quiet,
            quiet,
        }
    }

    /// Used in tests; no spinners, no extra println.
    pub fn test_stub() -> Self {
        Self {
            show_progress: false,
            quiet: true,
        }
    }

    pub fn message(&self, msg: impl AsRef<str>) {
        if self.quiet {
            return;
        }
        println!("{}", msg.as_ref());
    }

    /// Spinner on stdout (tracing stays on stderr).
    pub fn spinner(&self, msg: impl Into<String>) -> Option<ProgressBar> {
        if !self.show_progress {
            return None;
        }
        let pb = ProgressBar::new_spinner();
        pb.set_draw_target(ProgressDrawTarget::stdout());
        pb.set_style(
            ProgressStyle::with_template("{spinner:.green.bold} {wide_msg}")
                .expect("indicatif template")
                .tick_chars("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"),
        );
        pb.enable_steady_tick(Duration::from_millis(80));
        pb.set_message(msg.into());
        Some(pb)
    }

    pub fn finish_spinner(pb: Option<&ProgressBar>, done: impl Into<String>) {
        if let Some(pb) = pb {
            pb.set_style(ProgressStyle::with_template("✓ {wide_msg}").expect("indicatif template"));
            pb.finish_with_message(done.into());
        }
    }

    /// Stream body into a buffer; optional byte bar when `total` is `Content-Length`.
    pub fn read_to_end_with_progress(
        &self,
        mut r: impl Read,
        total: Option<u64>,
        label: &str,
    ) -> Result<Vec<u8>, io::Error> {
        let pb: Option<ProgressBar> = if self.show_progress {
            let pb = if let Some(len) = total {
                let p = ProgressBar::new(len);
                p.set_style(
                    ProgressStyle::with_template(
                        "{spinner:.cyan.bold} [{wide_bar:.cyan/blue}] {bytes}/{total_bytes} {msg}",
                    )
                    .expect("template")
                    .progress_chars("█▓▒░"),
                );
                p.set_message(label.to_string());
                p
            } else {
                let p = ProgressBar::new_spinner();
                p.set_style(
                    ProgressStyle::with_template("{spinner:.cyan.bold} {wide_msg}")
                        .expect("template")
                        .tick_chars("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"),
                );
                p.enable_steady_tick(Duration::from_millis(80));
                p.set_message(format!("{label} (size unknown)"));
                p
            };
            pb.set_draw_target(ProgressDrawTarget::stdout());
            Some(pb)
        } else {
            None
        };

        let mut buf = Vec::new();
        let mut chunk = [0u8; 64 * 1024];
        loop {
            let n = r.read(&mut chunk)?;
            if n == 0 {
                break;
            }
            buf.extend_from_slice(&chunk[..n]);
            if let Some(ref p) = pb {
                p.inc(n as u64);
            }
        }

        if let Some(p) = pb {
            p.finish_with_message(format!("{} — {}", label, HumanBytes(buf.len() as u64)));
        }

        Ok(buf)
    }
}

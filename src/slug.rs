//! ASCII slug helpers shared across path layout and hook package keys.

/// Lowercase alphanumeric preserved; other chars become `-`; trim leading/trailing `-`.
pub(crate) fn dashed_lower(s: &str) -> String {
    s.chars()
        .map(|ch| {
            if ch.is_ascii_alphanumeric() {
                ch.to_ascii_lowercase()
            } else {
                '-'
            }
        })
        .collect::<String>()
        .trim_matches('-')
        .to_owned()
}

/// Alphanumeric preserved; other chars become `-` (no collapse or trim).
pub(crate) fn dashed(s: &str) -> String {
    s.chars()
        .map(|ch| if ch.is_ascii_alphanumeric() { ch } else { '-' })
        .collect()
}

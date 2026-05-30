//! ASCII slug helpers shared across path layout, hook package keys, and Cursor compatibility.

/// Replace non-alphanumeric ASCII with `-`, collapse consecutive `-`, trim edges.
/// Matches `cursor-agent` `slugifyPath`.
pub(crate) fn collapse_dashes(s: &str) -> String {
    let replaced: String = s
        .chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '-' })
        .collect();
    let mut collapsed = String::with_capacity(replaced.len());
    let mut prev_dash = false;
    for c in replaced.chars() {
        if c == '-' {
            if !prev_dash {
                collapsed.push(c);
            }
            prev_dash = true;
        } else {
            collapsed.push(c);
            prev_dash = false;
        }
    }
    collapsed.trim_matches('-').to_owned()
}

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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn collapse_dashes_matches_cursor_rules() {
        assert_eq!(
            collapse_dashes("/Users/snowbear/WORK/GIT/agentpack"),
            "Users-snowbear-WORK-GIT-agentpack"
        );
        assert_eq!(collapse_dashes("/a//b/c"), "a-b-c");
        assert_eq!(collapse_dashes("---x---"), "x");
    }
}

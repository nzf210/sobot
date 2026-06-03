//! Shared utilities for btc-treasury.
//!
//! - `mask_secret` — safe display of API keys / secrets (never logs raw value)

/// Mask a secret for display: `abcd...wxyz` (first 4 + last 4 chars).
///
/// Shorter than 8 chars → `***` to avoid leaking any real characters.
/// Empty string → `<empty>`.
///
/// Use this everywhere an API key or secret might be logged or displayed in
/// Telegram. Never log the raw secret.
pub fn mask_secret(s: &str) -> String {
    if s.is_empty() {
        return "<empty>".to_string();
    }
    if s.len() <= 8 {
        return "***".to_string();
    }
    format!("{}...{}", &s[..4], &s[s.len() - 4..])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mask_secret_normal() {
        assert_eq!(mask_secret("abcd1234efgh5678"), "abcd...5678");
    }

    #[test]
    fn mask_secret_exactly_8_chars() {
        // 8 chars is <= 8, so returns ***
        assert_eq!(mask_secret("12345678"), "***");
    }

    #[test]
    fn mask_secret_short() {
        assert_eq!(mask_secret("abc"), "***");
    }

    #[test]
    fn mask_secret_empty() {
        assert_eq!(mask_secret(""), "<empty>");
    }

    #[test]
    fn mask_secret_9_chars() {
        assert_eq!(mask_secret("123456789"), "1234...6789");
    }
}

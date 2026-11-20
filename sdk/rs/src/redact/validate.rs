//! Post-match validators for rules whose regex is shape-only; see
//! `go/core/redact/validate.go`.
//!
//! credit-card: Luhn plus a token-boundary check. A run glued to
//! alphanumerics, or overlapping a UUID-shaped token, is part of an
//! identifier, not a card. A label joined by a hyphen or underscore
//! (`card-4111...`, `cc_4111...`) is a boundary: the number is still
//! redacted.
//!
//! us-ssn, us-itin: the same boundary plus structural checks. phone-us,
//! phone-e164, us-passport, us-driver-license: boundary only.

/// Whether `s[start..end]` of the rule `rule_id` is a real finding.
/// Rules without a validator always pass.
pub(crate) fn plausible(rule_id: &str, s: &str, start: usize, end: usize) -> bool {
    match rule_id.strip_suffix("-pii").unwrap_or(rule_id) {
        "credit-card" => luhn_valid(&s[start..end]) && !inside_token(s, start, end),
        "us-ssn" => ssn_valid(&s[start..end]) && !inside_token(s, start, end),
        "us-itin" => itin_valid(&s[start..end]) && !inside_token(s, start, end),
        "phone-us" | "phone-e164" | "us-passport" | "us-driver-license" => {
            !inside_token(s, start, end)
        }
        _ => true,
    }
}

/// `s` is NNN-NN-NNNN with a structurally possible SSN: area not 000,
/// 666 or 900-999; group not 00; serial not 0000.
fn ssn_valid(s: &str) -> bool {
    let b = s.as_bytes();
    if b.len() != 11 {
        return false;
    }
    let (area, group, serial) = (&s[0..3], &s[4..6], &s[7..11]);
    if area == "000" || area == "666" || b[0] == b'9' {
        return false;
    }
    group != "00" && serial != "0000"
}

/// `s` is 9NN-GG-NNNN with GG in an IRS-issued ITIN group range.
///
/// Source: IRS Internal Revenue Manual 3.21.263 (ITIN Real-Time System),
/// <https://www.irs.gov/irm/part3/irm_03-021-263r>: an ITIN begins with
/// 9 and "the fourth and fifth numbers will range from 50-65, 70-88,
/// 90-92, 94-99"; 89 and 93 are reserved for other programs.
fn itin_valid(s: &str) -> bool {
    let b = s.as_bytes();
    if b.len() != 11 || b[0] != b'9' {
        return false;
    }
    let g = u32::from(b[4] - b'0') * 10 + u32::from(b[5] - b'0');
    matches!(g, 50..=65 | 70..=88 | 90..=92 | 94..=99)
}

/// Digits of `s` (separators ignored) number at least 13 and pass the
/// Luhn mod-10 check.
fn luhn_valid(s: &str) -> bool {
    let (mut sum, mut n, mut double) = (0u32, 0usize, false);
    for c in s.bytes().rev() {
        if !c.is_ascii_digit() {
            continue;
        }
        let mut d = u32::from(c - b'0');
        if double {
            d *= 2;
            if d > 9 {
                d -= 9;
            }
        }
        sum += d;
        double = !double;
        n += 1;
    }
    n >= 13 && sum % 10 == 0
}

/// 8-4-4-4-12 hex groups with hyphens.
const UUID_LEN: usize = 36;

/// `s[start..end]` is part of a larger token: an alphanumeric directly
/// adjacent, or overlap with a UUID-shaped run of hex groups. A hyphen
/// or underscore neighbor alone is not.
fn inside_token(s: &str, start: usize, end: usize) -> bool {
    let b = s.as_bytes();
    if start > 0 && b[start - 1].is_ascii_alphanumeric() {
        return true;
    }
    if end < b.len() && b[end].is_ascii_alphanumeric() {
        return true;
    }
    let first = start.saturating_sub(UUID_LEN - 1);
    (first..end)
        .take_while(|u| u + UUID_LEN <= b.len())
        .any(|u| uuid_at(&b[u..u + UUID_LEN]))
}

fn uuid_at(w: &[u8]) -> bool {
    w.iter().enumerate().all(|(k, &c)| match k {
        8 | 13 | 18 | 23 => c == b'-',
        _ => c.is_ascii_hexdigit(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn card(s: &str) -> bool {
        // Whole input is the candidate match.
        plausible("credit-card", s, 0, s.len())
    }

    #[test]
    fn luhn() {
        assert!(luhn_valid("4111 1111 1111 1111"));
        assert!(luhn_valid("378282246310005"));
        assert!(!luhn_valid("1234 5678 9012 3456"));
        assert!(!luhn_valid("000000000000")); // 12 digits
    }

    #[test]
    fn boundary() {
        let s = "card-4111111111111111";
        assert!(plausible("credit-card", s, 5, s.len()));
        let s = "cc_4111111111111111_x";
        assert!(plausible("credit-card", s, 3, 19));
        let s = "abc4111111111111111";
        assert!(!plausible("credit-card", s, 3, s.len()));
        let s = "4111111111111111abc";
        assert!(!plausible("credit-card", s, 0, 16));
        let s = "bffab4c3-9d88-43ab-4111-111111111111";
        assert!(!plausible("credit-card", s, 19, s.len()));
        assert!(card("4111111111111111"));
    }

    #[test]
    fn ssn_structure() {
        assert!(ssn_valid("123-45-6789"));
        for bad in [
            "000-45-6789",
            "666-45-6789",
            "900-45-6789",
            "999-45-6789",
            "123-00-6789",
            "123-45-0000",
        ] {
            assert!(!ssn_valid(bad), "{bad}");
        }
    }

    #[test]
    fn itin_groups() {
        for g in [50, 65, 70, 88, 90, 92, 94, 99] {
            assert!(itin_valid(&format!("912-{g}-1234")), "{g}");
        }
        for g in [0, 49, 66, 69, 89, 93] {
            assert!(!itin_valid(&format!("912-{g:02}-1234")), "{g}");
        }
        assert!(!itin_valid("812-70-1234"));
    }

    #[test]
    fn boundary_only_rules() {
        for id in ["phone-us", "phone-e164", "us-passport", "us-driver-license"] {
            assert!(!plausible(id, "abc123456789", 3, 12), "{id}");
            assert!(plausible(id, "x-123456789", 2, 11), "{id}");
        }
        assert!(plausible("us-ssn", "ssn-123-45-6789", 4, 15));
        assert!(!plausible("us-ssn", "abc123-45-6789", 3, 14));
    }
}

//! The two S3 XML documents the backend reads: `ListObjectsV2` results and
//! error bodies. Both are flat, fixed-schema documents, so a tag scanner is
//! enough and no XML dependency is pulled in.

/// One `<Contents>` entry of a listing.
#[derive(Debug, PartialEq, Eq)]
pub(crate) struct Entry {
    pub key: String,
    pub size: u64,
}

/// One page of a `ListObjectsV2` result.
#[derive(Debug, PartialEq, Eq, Default)]
pub(crate) struct ListPage {
    pub entries: Vec<Entry>,
    pub truncated: bool,
    pub next_token: Option<String>,
}

/// Text of the first `<tag>…</tag>` in `s`, entity-decoded. `None` when the
/// tag is absent; `Some("")` for `<tag></tag>` or `<tag/>`.
fn tag(s: &str, name: &str) -> Option<String> {
    let open = format!("<{name}>");
    if let Some(i) = s.find(&open) {
        let rest = &s[i + open.len()..];
        let end = rest.find(&format!("</{name}>"))?;
        return Some(unescape(&rest[..end]));
    }
    s.contains(&format!("<{name}/>")).then(String::new)
}

/// Decode the five predefined XML entities and numeric references.
fn unescape(s: &str) -> String {
    if !s.contains('&') {
        return s.to_string();
    }
    let mut out = String::with_capacity(s.len());
    let mut rest = s;
    while let Some(i) = rest.find('&') {
        out.push_str(&rest[..i]);
        rest = &rest[i..];
        let Some(semi) = rest.find(';') else { break };
        let ent = &rest[1..semi];
        let ch = match ent {
            "amp" => Some('&'),
            "lt" => Some('<'),
            "gt" => Some('>'),
            "quot" => Some('"'),
            "apos" => Some('\''),
            _ => ent
                .strip_prefix("#x")
                .and_then(|h| u32::from_str_radix(h, 16).ok())
                .or_else(|| ent.strip_prefix('#').and_then(|d| d.parse().ok()))
                .and_then(char::from_u32),
        };
        match ch {
            Some(c) => {
                out.push(c);
                rest = &rest[semi + 1..];
            }
            None => {
                out.push('&');
                rest = &rest[1..];
            }
        }
    }
    out.push_str(rest);
    out
}

/// Decode a key S3 returned under `encoding-type=url`: `%XX` escapes and
/// `+` for space.
pub(crate) fn url_decode(s: &str) -> String {
    fn nib(c: u8) -> Option<u8> {
        (c as char).to_digit(16).map(|d| d as u8)
    }
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        match b[i] {
            b'+' => out.push(b' '),
            b'%' if i + 2 < b.len() => match (nib(b[i + 1]), nib(b[i + 2])) {
                (Some(h), Some(l)) => {
                    out.push(h << 4 | l);
                    i += 2;
                }
                _ => out.push(b'%'),
            },
            c => out.push(c),
        }
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// Parse a `ListObjectsV2` page requested with `encoding-type=url`.
pub(crate) fn parse_list(body: &str) -> Result<ListPage, String> {
    if !body.contains("<ListBucketResult") {
        return Err("response is not a ListBucketResult".into());
    }
    let mut page = ListPage::default();
    let mut rest = String::new();
    let mut cursor = body;
    while let Some(i) = cursor.find("<Contents>") {
        rest.push_str(&cursor[..i]);
        let after = &cursor[i + "<Contents>".len()..];
        let end = after.find("</Contents>").ok_or("unterminated <Contents>")?;
        let item = &after[..end];
        let key = tag(item, "Key").ok_or("<Contents> without <Key>")?;
        let size = tag(item, "Size")
            .ok_or("<Contents> without <Size>")?
            .trim()
            .parse::<u64>()
            .map_err(|e| format!("bad <Size>: {e}"))?;
        page.entries.push(Entry {
            key: url_decode(&key),
            size,
        });
        cursor = &after[end + "</Contents>".len()..];
    }
    rest.push_str(cursor);
    page.truncated = tag(&rest, "IsTruncated").is_some_and(|v| v.trim() == "true");
    page.next_token = tag(&rest, "NextContinuationToken").filter(|t| !t.is_empty());
    if page.truncated && page.next_token.is_none() {
        return Err("truncated listing without a continuation token".into());
    }
    Ok(page)
}

/// `(Code, Message)` of an S3 `<Error>` body, either possibly empty.
pub(crate) fn parse_error(body: &str) -> (String, String) {
    (
        tag(body, "Code").unwrap_or_default(),
        tag(body, "Message").unwrap_or_default(),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn list_page_entries_and_token() {
        let body = "<?xml version=\"1.0\"?><ListBucketResult xmlns=\"x\"><Name>b</Name>\
            <Prefix>p/</Prefix><IsTruncated>true</IsTruncated>\
            <Contents><Key>p/a.txt</Key><Size>3</Size></Contents>\
            <Contents><Key>p/b%20c+d%2Be</Key><Size>0</Size></Contents>\
            <NextContinuationToken>tok&amp;1</NextContinuationToken></ListBucketResult>";
        let p = parse_list(body).unwrap();
        assert_eq!(
            p.entries,
            vec![
                Entry {
                    key: "p/a.txt".into(),
                    size: 3
                },
                Entry {
                    key: "p/b c d+e".into(),
                    size: 0
                },
            ]
        );
        assert!(p.truncated);
        assert_eq!(p.next_token.as_deref(), Some("tok&1"));
    }

    #[test]
    fn empty_listing() {
        let p = parse_list("<ListBucketResult><IsTruncated>false</IsTruncated><KeyCount>0</KeyCount></ListBucketResult>").unwrap();
        assert!(p.entries.is_empty() && !p.truncated && p.next_token.is_none());
    }

    #[test]
    fn contents_children_do_not_leak_into_page_fields() {
        // A key literally named like a page tag stays an entry key.
        let body = "<ListBucketResult><IsTruncated>false</IsTruncated>\
            <Contents><Key>&lt;IsTruncated&gt;true&lt;/IsTruncated&gt;</Key><Size>1</Size></Contents>\
            </ListBucketResult>";
        let p = parse_list(body).unwrap();
        assert!(!p.truncated);
        assert_eq!(p.entries[0].key, "<IsTruncated>true</IsTruncated>");
    }

    #[test]
    fn truncated_without_token_is_an_error() {
        assert!(
            parse_list("<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>")
                .is_err()
        );
    }

    #[test]
    fn rejects_non_listing_and_malformed() {
        assert!(parse_list("<Error><Code>X</Code></Error>").is_err());
        assert!(parse_list(
            "<ListBucketResult><Contents><Key>a</Key></Contents></ListBucketResult>"
        )
        .is_err());
        assert!(parse_list(
            "<ListBucketResult><Contents><Key>a</Key><Size>x</Size></Contents></ListBucketResult>"
        )
        .is_err());
    }

    #[test]
    fn error_code_and_message() {
        let (c, m) = parse_error(
            "<Error><Code>NoSuchBucket</Code><Message>The &quot;b&quot; bucket</Message></Error>",
        );
        assert_eq!(
            (c.as_str(), m.as_str()),
            ("NoSuchBucket", "The \"b\" bucket")
        );
        assert_eq!(parse_error("not xml"), (String::new(), String::new()));
    }

    #[test]
    fn entities_numeric_and_unknown() {
        assert_eq!(unescape("a&#65;&#x42;&bogus;&amp"), "aAB&bogus;&amp");
    }

    #[test]
    fn url_decode_handles_truncated_escapes() {
        assert_eq!(url_decode("a%2"), "a%2");
        assert_eq!(url_decode("100%"), "100%");
        assert_eq!(url_decode("%zz+%C3%A9"), "%zz é");
    }
}

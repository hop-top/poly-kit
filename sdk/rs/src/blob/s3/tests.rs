use std::sync::Mutex;

use super::*;

fn creds() -> Credentials {
    Credentials::new(
        ["AKIA", "IOSFODNN7EXAMPLE"].concat(),
        ["wJalrXUtnFEMI/K7MDENG/", "bPxRfiCYEXAMPLEKEY"].concat(),
    )
}

/// Canned answers in order; records what it was asked.
#[derive(Debug, Default)]
struct Fake {
    answers: Mutex<Vec<HttpResponse>>,
    seen: Mutex<Vec<HttpRequest>>,
}

impl Fake {
    fn with(answers: Vec<HttpResponse>) -> Arc<Fake> {
        Arc::new(Fake {
            answers: Mutex::new(answers.into_iter().rev().collect()),
            ..Default::default()
        })
    }
}

impl Transport for Fake {
    fn send(&self, req: HttpRequest) -> Result<HttpResponse, TransportError> {
        self.seen.lock().unwrap().push(req);
        Ok(self
            .answers
            .lock()
            .unwrap()
            .pop()
            .expect("unexpected request"))
    }
}

fn resp(status: u16, headers: &[(&str, &str)], body: &str) -> HttpResponse {
    HttpResponse {
        status,
        headers: headers
            .iter()
            .map(|(k, v)| ((*k).into(), (*v).into()))
            .collect(),
        body: body.as_bytes().to_vec(),
    }
}

fn cfg() -> S3Config {
    S3Config::new("b", "us-east-1", creds()).with_endpoint("http://localhost:4566")
}

fn store(prefix: &str, fake: &Arc<Fake>) -> S3Store {
    S3Store::with_transport(cfg().with_prefix(prefix), fake.clone()).unwrap()
}

#[test]
fn prefix_trailing_slashes_ignored() {
    let f = Fake::with(vec![]);
    for (p, want) in [
        ("tool-data", "tool-data/a/b.txt"),
        ("tool-data/", "tool-data/a/b.txt"),
        ("", "a/b.txt"),
    ] {
        let s = store(p, &f);
        let full = s.key("a/b.txt").unwrap();
        assert_eq!(full, want);
        assert_eq!(s.caller_key(&full), "a/b.txt");
    }
}

#[test]
fn key_is_cleaned_like_path_join_and_cannot_escape() {
    let s = store("pre", &Fake::with(vec![]));
    assert_eq!(s.key("/a//b/./c/../d").unwrap(), "pre/a/b/d");
    for bad in ["", ".", "..", "a/../..", "../x"] {
        assert!(
            matches!(s.key(bad), Err(BlobError::KeyEscapesRoot(_))),
            "{bad:?}"
        );
    }
}

#[test]
fn list_prefix_keeps_trailing_slash() {
    let f = Fake::with(vec![]);
    assert_eq!(
        store("tool-data", &f).list_prefix("logs/"),
        "tool-data/logs/"
    );
    assert_eq!(store("", &f).list_prefix("logs/"), "logs/");
    assert_eq!(store("tool-data", &f).list_prefix(""), "tool-data/");
}

#[test]
fn path_style_request_is_signed_and_hides_nothing_in_the_url() {
    let f = Fake::with(vec![resp(200, &[], "")]);
    let s = store("pre", &f);
    s.put("a b/é.txt", "hi".as_bytes(), "text/plain").unwrap();
    let seen = f.seen.lock().unwrap();
    let r = &seen[0];
    assert_eq!(r.method, "PUT");
    assert_eq!(r.url, "http://localhost:4566/b/pre/a%20b/%C3%A9.txt");
    assert_eq!(r.body, b"hi");
    let h = |n: &str| {
        r.headers
            .iter()
            .find(|(k, _)| k == n)
            .map(|(_, v)| v.as_str())
    };
    assert_eq!(h("content-type"), Some("text/plain"));
    assert_eq!(h("x-amz-content-sha256"), Some(sha256_hex(b"hi").as_str()));
    let auth = h("authorization").unwrap();
    assert!(auth.contains("/us-east-1/s3/aws4_request"));
    assert!(auth.contains("SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date"));
    assert!(!r.url.contains("EXAMPLEKEY"));
    // No Host header is sent: the client derives it from the URL.
    assert!(h("host").is_none());
}

#[test]
fn empty_content_type_sends_no_content_type_header() {
    let f = Fake::with(vec![resp(200, &[], "")]);
    store("", &f).put("k", "x".as_bytes(), "").unwrap();
    assert!(f.seen.lock().unwrap()[0]
        .headers
        .iter()
        .all(|(k, _)| k != "content-type"));
}

#[test]
fn session_token_is_sent_and_signed() {
    let f = Fake::with(vec![resp(204, &[], "")]);
    let c = S3Config::new("b", "us-east-1", creds().with_session_token("tok"))
        .with_endpoint("http://localhost:4566");
    S3Store::with_transport(c, f.clone())
        .unwrap()
        .delete("k")
        .unwrap();
    let seen = f.seen.lock().unwrap();
    assert!(seen[0]
        .headers
        .iter()
        .any(|(k, v)| k == "x-amz-security-token" && v == "tok"));
    let auth = &seen[0]
        .headers
        .iter()
        .find(|(k, _)| k == "authorization")
        .unwrap()
        .1;
    assert!(auth.contains("x-amz-security-token"));
}

#[test]
fn virtual_hosted_addressing_for_aws_and_override() {
    let t = Target::resolve(&S3Config::new("bkt", "eu-west-1", creds())).unwrap();
    assert_eq!(
        (
            t.scheme.as_str(),
            t.authority.as_str(),
            t.path_prefix.as_str()
        ),
        ("https", "bkt.s3.eu-west-1.amazonaws.com", "")
    );
    let t =
        Target::resolve(&S3Config::new("bkt", "eu-west-1", creds()).with_path_style(true)).unwrap();
    assert_eq!(
        (t.authority.as_str(), t.path_prefix.as_str()),
        ("s3.eu-west-1.amazonaws.com", "/bkt")
    );
    let t = Target::resolve(&cfg().with_path_style(false)).unwrap();
    assert_eq!(t.authority, "b.localhost:4566");
}

#[test]
fn config_validation() {
    for bad in ["ftp://x", "http://h/sub", "not a url", "http://h/?q=1"] {
        let c = S3Config::new("b", "r", creds()).with_endpoint(bad);
        assert!(
            matches!(Target::resolve(&c), Err(BlobError::InvalidConfig(_))),
            "{bad}"
        );
    }
    assert!(Target::resolve(&S3Config::new("", "r", creds())).is_err());
    assert!(Target::resolve(&S3Config::new("b", "", creds())).is_err());
}

#[test]
fn from_lookup_reads_only_standard_vars() {
    let env = |pairs: &'static [(&'static str, &'static str)]| {
        move |k: &str| {
            pairs
                .iter()
                .find(|(n, _)| *n == k)
                .map(|(_, v)| (*v).to_string())
        }
    };
    let c = S3Config::from_lookup(
        "b",
        env(&[
            ("AWS_ACCESS_KEY_ID", "id"),
            ("AWS_SECRET_ACCESS_KEY", "sec"),
            ("AWS_DEFAULT_REGION", "eu-west-1"),
            ("AWS_ENDPOINT_URL", "http://localhost:4566"),
            ("AWS_SESSION_TOKEN", "tok"),
        ]),
    )
    .unwrap();
    assert_eq!(c.region, "eu-west-1");
    assert!(c.path_style && c.endpoint.as_deref() == Some("http://localhost:4566"));
    assert_eq!(c.credentials.session_token.as_deref(), Some("tok"));

    // AWS_ENDPOINT_URL_S3 wins; AWS_REGION wins over the default.
    let c = S3Config::from_lookup(
        "b",
        env(&[
            ("AWS_ACCESS_KEY_ID", "id"),
            ("AWS_SECRET_ACCESS_KEY", "sec"),
            ("AWS_REGION", "a"),
            ("AWS_DEFAULT_REGION", "b"),
            ("AWS_ENDPOINT_URL_S3", "http://s3.local"),
            ("AWS_ENDPOINT_URL", "http://other"),
        ]),
    )
    .unwrap();
    assert_eq!(
        (c.region.as_str(), c.endpoint.as_deref()),
        ("a", Some("http://s3.local"))
    );

    for missing in [
        &[("AWS_SECRET_ACCESS_KEY", "s"), ("AWS_REGION", "r")][..],
        &[("AWS_ACCESS_KEY_ID", "i"), ("AWS_REGION", "r")][..],
        &[("AWS_ACCESS_KEY_ID", "i"), ("AWS_SECRET_ACCESS_KEY", "s")][..],
        &[
            ("AWS_ACCESS_KEY_ID", ""),
            ("AWS_SECRET_ACCESS_KEY", "s"),
            ("AWS_REGION", "r"),
        ][..],
    ] {
        let leaked: &'static [(&str, &str)] = Box::leak(missing.to_vec().into_boxed_slice());
        assert!(matches!(
            S3Config::from_lookup("b", env(leaked)),
            Err(BlobError::InvalidConfig(_))
        ));
    }
}

#[test]
fn get_missing_is_not_found_and_other_404_is_not() {
    let f = Fake::with(vec![
        resp(
            404,
            &[],
            "<Error><Code>NoSuchKey</Code><Message>m</Message></Error>",
        ),
        resp(
            404,
            &[],
            "<Error><Code>NoSuchBucket</Code><Message>gone</Message></Error>",
        ),
    ]);
    let s = store("", &f);
    assert!(matches!(s.get("k"), Err(BlobError::NotFound(k)) if k == "k"));
    let e = s.get("k").unwrap_err();
    assert!(matches!(e, BlobError::Remote { .. }));
    assert_eq!(e.to_string(), "blob: get k: status 404: NoSuchBucket: gone");
}

#[test]
fn delete_ok_on_204_and_error_on_403() {
    let f = Fake::with(vec![
        resp(204, &[], ""),
        resp(403, &[], "<Error><Code>AccessDenied</Code></Error>"),
    ]);
    let s = store("", &f);
    s.delete("k").unwrap();
    let e = s.delete("k").unwrap_err();
    let BlobError::Remote { source, .. } = e else {
        panic!()
    };
    assert_eq!(
        source.downcast_ref::<ResponseError>().unwrap().code,
        "AccessDenied"
    );
}

#[test]
fn exists_and_stat_map_head() {
    let f = Fake::with(vec![
        resp(200, &[], ""),
        resp(404, &[], ""),
        resp(403, &[], ""),
        resp(
            200,
            &[
                ("content-length", "42"),
                ("content-type", "application/json"),
            ],
            "",
        ),
        resp(404, &[], ""),
    ]);
    let s = store("pre", &f);
    assert!(s.exists("a").unwrap());
    assert!(!s.exists("a").unwrap());
    assert!(s.exists("a").is_err());
    assert_eq!(
        s.stat("a/b.json").unwrap(),
        Object {
            key: "a/b.json".into(),
            size: 42,
            content_type: "application/json".into()
        }
    );
    assert!(matches!(s.stat("nope"), Err(BlobError::NotFound(k)) if k == "nope"));
    assert!(f.seen.lock().unwrap().iter().all(|r| r.method == "HEAD"));
}

#[test]
fn list_paginates_strips_prefix_and_leaves_content_type_empty() {
    let page1 = "<ListBucketResult><IsTruncated>true</IsTruncated>\
        <Contents><Key>pre/logs/a.log</Key><Size>1</Size></Contents>\
        <NextContinuationToken>T1</NextContinuationToken></ListBucketResult>";
    let page2 = "<ListBucketResult><IsTruncated>false</IsTruncated>\
        <Contents><Key>pre/logs/sub/b.log</Key><Size>2</Size></Contents></ListBucketResult>";
    let f = Fake::with(vec![resp(200, &[], page1), resp(200, &[], page2)]);
    let objs = store("pre", &f).list("logs/").unwrap();
    assert_eq!(
        objs,
        vec![
            Object {
                key: "logs/a.log".into(),
                size: 1,
                content_type: String::new()
            },
            Object {
                key: "logs/sub/b.log".into(),
                size: 2,
                content_type: String::new()
            },
        ]
    );
    let seen = f.seen.lock().unwrap();
    assert_eq!(
        seen[0].url,
        "http://localhost:4566/b?encoding-type=url&list-type=2&prefix=pre%2Flogs%2F"
    );
    assert!(
        seen[1].url.ends_with("&continuation-token=T1")
            || seen[1].url.contains("continuation-token=T1")
    );
}

#[test]
fn malformed_listing_is_a_remote_error() {
    let f = Fake::with(vec![resp(200, &[], "<html>proxy</html>")]);
    assert!(matches!(
        store("", &f).list(""),
        Err(BlobError::Remote { op: "list", .. })
    ));
}

#[test]
fn amz_date_formats_utc() {
    assert_eq!(amz_date(0), "19700101T000000Z");
    assert_eq!(amz_date(1_369_353_600), "20130524T000000Z");
    assert_eq!(amz_date(951_782_400 + 86_399), "20000229T235959Z"); // leap day
    assert_eq!(amz_now().len(), 16);
}

#[test]
fn guarded_transport_refuses_non_loopback_when_offline() {
    let s = S3Store::new(
        S3Config::new("b", "us-east-1", creds()).with_endpoint("https://s3.example.invalid"),
        NetPolicy::offline(),
    )
    .unwrap();
    let e = s.exists("k").unwrap_err();
    let BlobError::Remote { source, .. } = e else {
        panic!("{e:?}")
    };
    let net = source
        .downcast_ref::<crate::netpolicy::NetError>()
        .expect("NetError");
    assert!(net.is_offline());
}

#[test]
fn guarded_transport_works_from_inside_a_runtime() {
    let rt = tokio::runtime::Builder::new_current_thread()
        .build()
        .unwrap();
    rt.block_on(async {
        let s = S3Store::new(
            S3Config::new("b", "us-east-1", creds()).with_endpoint("https://s3.example.invalid"),
            NetPolicy::offline(),
        )
        .unwrap();
        assert!(s.exists("k").is_err()); // refused, not a panic
    });
}

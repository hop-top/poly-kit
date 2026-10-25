// The reqwest-free half of go/core/netpolicy: the policy marker, loopback
// classification, the dial check and the refusal text. Runs under the
// `netpolicy` feature alone, so it proves the core needs neither reqwest
// nor tokio.
#![cfg(feature = "netpolicy")]

use hop_top_kit::netpolicy::{is_loopback, NetPolicy, OfflineError, OFFLINE_MESSAGE};

// Go's isLoopback, row for row: hosts with and without a port, every
// loopback spelling net.ParseIP accepts, and the near misses it rejects.
// The expectations were taken from Go itself, not from reading it.
#[test]
fn loopback_table() {
    let loopback = [
        "127.0.0.1",
        "127.0.0.1:8080",
        "127.1.2.3",
        "127.0.0.1:",
        "localhost",
        "localhost:9000",
        "localhost:25",
        "::1",
        "[::1]:9000",
        "0:0:0:0:0:0:0:1",
        "::ffff:127.0.0.1",
        "[::ffff:127.0.0.1]:80",
        "[127.0.0.1]:80",
        "[localhost]:80",
    ];
    for host in loopback {
        assert!(is_loopback(host), "{host:?} should be loopback");
    }
    let remote = [
        "",
        ":80",
        "example.com",
        "example.com:443",
        "example.invalid",
        "my-host.internal",
        "localhost.evil.com",
        "localhost.",
        "LOCALHOST",
        "Localhost:80",
        "8.8.8.8",
        "10.0.0.5:443",
        "db.example.com:3306",
        "0.0.0.0",
        "::",
        "127.000.0.1",
        "127.0.0.01",
        "fe80::1%lo0",
        "[::1%lo0]:80",
        "1:2:3",
    ];
    for host in remote {
        assert!(!is_loopback(host), "{host:?} should be remote");
    }
}

// A URL host with no port keeps an IPv6 literal bracketed (`[::1]`).
// It is loopback when the brackets hold a loopback IPv6 address,
// v4-mapped included; brackets around anything else stay remote. Same
// rule for a dial address.
#[test]
fn bracketed_host_without_port() {
    for host in ["[::1]", "[::ffff:127.0.0.1]", "[0:0:0:0:0:0:0:1]"] {
        assert!(is_loopback(host), "{host:?} should be loopback");
        assert_eq!(NetPolicy::offline().check_dial("tcp", host), Ok(()));
    }
    for host in [
        "[127.0.0.1]",
        "[localhost]",
        "[2001:db8::1]",
        "[::1%lo0]",
        "[[::1]]",
    ] {
        assert!(!is_loopback(host), "{host:?} should be remote");
        let err = NetPolicy::offline()
            .check_dial("tcp", host)
            .expect_err("bracketed non-loopback dial permitted");
        assert_eq!(
            err.to_string(),
            format!("dial tcp {host}: network disabled by --offline")
        );
    }
}

// The offline table: an offline policy refuses every non-loopback host
// and DNS name, keeps loopback reachable, and an online policy permits
// all of it. Mirrors Go's RoundTripper tests.
#[test]
fn offline_table() {
    let off = NetPolicy::offline();
    let on = NetPolicy::online();
    for host in ["127.0.0.1:8080", "localhost:9000", "[::1]:9000"] {
        assert!(off.allows_host(host), "offline refused loopback {host}");
        assert!(on.allows_host(host), "online refused {host}");
    }
    for host in ["example.invalid", "my-host.internal", "api.example.com"] {
        assert!(!off.allows_host(host), "offline allowed {host}");
        assert!(on.allows_host(host), "online refused {host}");
    }
}

// Go's TestCheckDial_MatchesGuardExemptions: the dial decision agrees
// with the HTTP guard, and unix sockets are not network at all.
#[test]
fn check_dial_table() {
    let off = NetPolicy::offline();
    let blocked = [("tcp", "db.example.com:3306"), ("tcp", "10.0.0.5:443")];
    let allowed = [
        ("tcp", "127.0.0.1:5432"),
        ("tcp", "localhost:25"),
        ("tcp", "[::1]:9000"),
        ("unix", "/tmp/kit.sock"),
        ("unixgram", "/tmp/kit.sock"),
        ("unixpacket", "/tmp/kit.sock"),
    ];
    for (network, addr) in blocked {
        let err = off
            .check_dial(network, addr)
            .expect_err("offline dial to a remote host was permitted");
        assert_eq!(
            err.to_string(),
            format!("dial {network} {addr}: network disabled by --offline")
        );
    }
    for (network, addr) in allowed {
        assert_eq!(off.check_dial(network, addr), Ok(()), "{network} {addr}");
    }
    // An online policy permits everything.
    for (network, addr) in blocked {
        assert_eq!(
            NetPolicy::online().check_dial(network, addr),
            Ok(()),
            "online {network} {addr}"
        );
    }
    // A non-unix network is network, whatever the transport.
    assert!(off.check_dial("udp", "8.8.8.8:53").is_err());
}

// The refusal reads exactly as Go's: `METHOD destination: ErrOffline`.
#[test]
fn offline_error_text_matches_go() {
    assert_eq!(OFFLINE_MESSAGE, "network disabled by --offline");
    let e = OfflineError {
        method: "POST".into(),
        url: "https://example.invalid/v1/models/m:generate".into(),
    };
    assert_eq!(
        e.to_string(),
        "POST https://example.invalid/v1/models/m:generate: network disabled by --offline"
    );
}

// The refusal is a std error, recoverable through `Box<dyn Error>`, the
// way Go callers match ErrOffline with errors.Is.
#[test]
fn offline_error_survives_boxing() {
    let err = NetPolicy::offline()
        .check_dial("tcp", "db.example.com:3306")
        .expect_err("expected refusal");
    let boxed: Box<dyn std::error::Error + Send + Sync> = Box::new(err);
    let back = boxed
        .downcast_ref::<OfflineError>()
        .expect("typed refusal lost through Box<dyn Error>");
    assert_eq!(back.method, "dial tcp");
    assert_eq!(back.url, "db.example.com:3306");
}

// The default policy is online: a port that never wires the flag must
// behave exactly as it does today.
#[test]
fn default_policy_is_online() {
    assert!(!NetPolicy::default().is_offline());
    assert!(NetPolicy::offline().is_offline());
    assert!(!NetPolicy::online().is_offline());
    // The bool constructor mirrors Go's WithOffline(ctx, bool).
    assert!(NetPolicy::new(true).is_offline());
    assert!(!NetPolicy::new(false).is_offline());
}

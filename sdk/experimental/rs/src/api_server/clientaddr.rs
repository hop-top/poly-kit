//! The client address behind trusted proxies, a port of Go's
//! `ClientAddress`. Forwarding headers are believed only when the peer
//! itself is a trusted proxy, and only up to the first hop that is not
//! one; two disagreeing headers believe neither.

use std::net::{IpAddr, Ipv4Addr};

use axum::extract::Request;
use axum::extract::State;
use axum::http::HeaderMap;
use axum::middleware::Next;
use axum::response::Response;

use super::ctx::{peer, ClientIp, ForwardedProto, PeerAddr};

/// An address range: a CIDR, or one address.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Prefix {
    addr: IpAddr,
    bits: u8,
}

impl Prefix {
    fn contains(&self, a: IpAddr) -> bool {
        match (self.addr, a) {
            (IpAddr::V4(p), IpAddr::V4(a)) => {
                masked(u32::from(p) as u128, 32, self.bits)
                    == masked(u32::from(a) as u128, 32, self.bits)
            }
            (IpAddr::V6(p), IpAddr::V6(a)) => {
                masked(u128::from(p), 128, self.bits) == masked(u128::from(a), 128, self.bits)
            }
            _ => false,
        }
    }
}

impl std::fmt::Display for Prefix {
    /// `10.0.0.0/8`: the masked address and the length.
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let addr = match self.addr {
            IpAddr::V4(a) => IpAddr::V4(Ipv4Addr::from(
                masked(u32::from(a) as u128, 32, self.bits) as u32,
            )),
            IpAddr::V6(a) => IpAddr::V6(masked(u128::from(a), 128, self.bits).into()),
        };
        write!(f, "{addr}/{}", self.bits)
    }
}

fn masked(v: u128, width: u32, bits: u8) -> u128 {
    if bits == 0 {
        return 0;
    }
    let shift = width - u32::from(bits);
    (v >> shift) << shift
}

/// Parses trusted-proxy entries: CIDRs or bare addresses. An IPv4
/// address mapped into IPv6 is unmapped, so one spelling matches.
pub fn parse_trusted_proxies(entries: &[String]) -> Result<Vec<Prefix>, String> {
    let bad = |e: &str| format!("trusted proxy {e:?}: not a CIDR or an IP address");
    let mut out = Vec::new();
    for e in entries {
        let e = e.trim();
        if e.is_empty() {
            continue;
        }
        if let Some((a, b)) = e.split_once('/') {
            let addr: IpAddr = a.parse().map_err(|_| bad(e))?;
            let bits: u8 = b.parse().map_err(|_| bad(e))?;
            let max = if addr.is_ipv4() { 32 } else { 128 };
            if bits > max {
                return Err(bad(e));
            }
            let (addr, bits) = match addr {
                IpAddr::V6(v6) if v6.to_ipv4_mapped().is_some() && bits >= 96 => {
                    (IpAddr::V4(v6.to_ipv4_mapped().expect("mapped")), bits - 96)
                }
                other => (other, bits),
            };
            out.push(Prefix { addr, bits });
        } else {
            let addr: IpAddr = e.parse().map_err(|_| bad(e))?;
            let addr = unmap(addr);
            out.push(Prefix {
                addr,
                bits: if addr.is_ipv4() { 32 } else { 128 },
            });
        }
    }
    Ok(out)
}

fn unmap(a: IpAddr) -> IpAddr {
    match a {
        IpAddr::V6(v6) => v6.to_ipv4_mapped().map_or(a, IpAddr::V4),
        v4 => v4,
    }
}

fn trusted_addr(trusted: &[Prefix], a: IpAddr) -> bool {
    trusted.iter().any(|p| p.contains(a))
}

fn parse_ip(s: &str) -> Option<IpAddr> {
    // A zone is dropped, as the Go port does.
    let s = s.split('%').next().unwrap_or(s);
    s.parse::<IpAddr>().ok().map(unmap)
}

fn node_addr(node: &str) -> Option<IpAddr> {
    let node = node.trim();
    if let Ok(sa) = node.parse::<std::net::SocketAddr>() {
        return Some(unmap(sa.ip()));
    }
    if let Some(inner) = node.strip_prefix('[').and_then(|n| n.strip_suffix(']')) {
        return parse_ip(inner);
    }
    parse_ip(node)
}

fn split_list(values: &[String]) -> Vec<String> {
    values
        .iter()
        .flat_map(|v| v.split(',').map(|e| e.trim().to_string()))
        .collect()
}

fn valid_proto(p: &str) -> String {
    let p = p.trim().to_lowercase();
    let mut chars = p.chars();
    match chars.next() {
        Some(c) if c.is_ascii_lowercase() => {}
        _ => return String::new(),
    }
    if chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || matches!(c, '+' | '-' | '.')) {
        p
    } else {
        String::new()
    }
}

/// The first hop from the right that is not trusted: the client, and
/// its index. All-trusted yields the leftmost hop.
fn walk_hops(hops: &[String], trusted: &[Prefix]) -> Option<(IpAddr, usize)> {
    if hops.is_empty() {
        return None;
    }
    let mut last = None;
    for i in (0..hops.len()).rev() {
        let a = node_addr(&hops[i])?;
        if !trusted_addr(trusted, a) {
            return Some((a, i));
        }
        last = Some(a);
    }
    last.map(|a| (a, 0))
}

fn lone_proto(xfp: &[String]) -> String {
    if xfp.len() == 1 {
        valid_proto(&xfp[0])
    } else {
        String::new()
    }
}

fn values(h: &HeaderMap, name: &str) -> Vec<String> {
    h.get_all(name)
        .iter()
        .map(|v| String::from_utf8_lossy(v.as_bytes()).into_owned())
        .collect()
}

/// (client, proto, found)
pub(crate) fn forwarded_client(
    h: &HeaderMap,
    trusted: &[Prefix],
) -> (Option<IpAddr>, String, bool) {
    let fwd = values(h, "forwarded");
    let xff = values(h, "x-forwarded-for");
    let has_fwd = h.contains_key("forwarded");
    let has_xff = h.contains_key("x-forwarded-for");
    let xfp = split_list(&values(h, "x-forwarded-proto"));
    let none = (None, String::new(), false);

    if has_fwd || has_xff {
        let (mut fc, mut xc) = (None, None);
        let (mut fp, mut xp) = (String::new(), String::new());
        if has_fwd {
            if let Some(elems) = parse_forwarded(&fwd) {
                let fors: Vec<String> = elems.iter().map(|e| e.for_node.clone()).collect();
                if let Some((a, i)) = walk_hops(&fors, trusted) {
                    fc = Some(a);
                    fp = elems[i].proto.clone();
                }
            }
        }
        if has_xff {
            let hops = split_list(&xff);
            if let Some((a, i)) = walk_hops(&hops, trusted) {
                xc = Some(a);
                if xfp.len() == 1 {
                    xp = xfp[0].clone();
                } else if xfp.len() == hops.len() {
                    xp = xfp[i].clone();
                }
            }
        }
        return match (has_fwd, has_xff) {
            (true, true) => match (fc, xc) {
                (Some(f), Some(x)) if f == x => {
                    if fp.is_empty() {
                        fp = xp;
                    }
                    (Some(f), valid_proto(&fp), true)
                }
                _ => none,
            },
            (true, false) => (fc, valid_proto(&fp), fc.is_some()),
            _ => (xc, valid_proto(&xp), xc.is_some()),
        };
    }
    let real = split_list(&values(h, "x-real-ip"));
    if h.contains_key("x-real-ip") {
        if real.len() != 1 {
            return none;
        }
        return match walk_hops(&real, trusted) {
            Some((c, _)) => (Some(c), lone_proto(&xfp), true),
            None => none,
        };
    }
    (None, lone_proto(&xfp), false)
}

struct ForwardedElement {
    for_node: String,
    proto: String,
}

fn parse_forwarded(lines: &[String]) -> Option<Vec<ForwardedElement>> {
    let mut out = Vec::new();
    for line in lines {
        let mut s = line.as_str();
        loop {
            let (e, rest) = parse_forwarded_element(s)?;
            out.push(e);
            let rest = rest.trim_start_matches([' ', '\t']);
            if rest.is_empty() {
                break;
            }
            s = rest.strip_prefix(',')?;
        }
    }
    (!out.is_empty()).then_some(out)
}

fn is_token_char(c: char) -> bool {
    c.is_ascii_alphanumeric() || "!#$%&'*+-.^_`|~".contains(c)
}

fn cut_token(s: &str) -> (&str, &str) {
    let end = s.find(|c: char| !is_token_char(c)).unwrap_or(s.len());
    s.split_at(end)
}

fn cut_value(s: &str) -> Option<(String, &str)> {
    let Some(q) = s.strip_prefix('"') else {
        let (v, r) = cut_token(s);
        return (!v.is_empty()).then(|| (v.to_string(), r));
    };
    let mut out = String::new();
    let mut chars = q.char_indices();
    while let Some((i, c)) = chars.next() {
        match c {
            '"' => return Some((out, &q[i + 1..])),
            '\\' => out.push(chars.next()?.1),
            c => out.push(c),
        }
    }
    None
}

fn parse_forwarded_element(mut s: &str) -> Option<(ForwardedElement, &str)> {
    let mut e = ForwardedElement {
        for_node: String::new(),
        proto: String::new(),
    };
    let mut seen: Vec<String> = Vec::new();
    loop {
        s = s.trim_start_matches([' ', '\t']);
        let (name, rest) = cut_token(s);
        if name.is_empty() || !rest.starts_with('=') {
            return None;
        }
        let (value, rest) = cut_value(&rest[1..])?;
        let name = name.to_lowercase();
        if seen.contains(&name) {
            return None;
        }
        match name.as_str() {
            "for" => e.for_node = value,
            "proto" => e.proto = value,
            _ => {}
        }
        seen.push(name);
        let rest = rest.trim_start_matches([' ', '\t']);
        if let Some(next) = rest.strip_prefix(';') {
            s = next;
            continue;
        }
        if !rest.is_empty() && !rest.starts_with(',') {
            return None;
        }
        if !seen.iter().any(|n| n == "for") {
            return None;
        }
        return Some((e, rest));
    }
}

pub(crate) async fn client_address(
    State(trusted): State<std::sync::Arc<Vec<Prefix>>>,
    mut req: Request,
    next: Next,
) -> Response {
    let peer_sa = peer(&req);
    let mut ip = peer_sa.map(|p| unmap(p.ip()));
    if let (Some(p), false) = (ip, trusted.is_empty()) {
        if trusted_addr(&trusted, p) {
            let (client, proto, found) = forwarded_client(req.headers(), &trusted);
            if found || !proto.is_empty() {
                if !proto.is_empty() {
                    req.extensions_mut().insert(ForwardedProto(proto));
                }
                if found {
                    if let Some(sa) = peer_sa {
                        req.extensions_mut().insert(PeerAddr(sa));
                    }
                    ip = client;
                }
            }
        }
    }
    if let Some(ip) = ip {
        req.extensions_mut().insert(ClientIp(ip));
    }
    next.run(req).await
}

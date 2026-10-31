//! Certificates generated at run time with rcgen (ring backend): no key
//! is ever checked in. A test-only PKI: one CA, leaves issued by it.
#![allow(dead_code)]

use rcgen::{
    BasicConstraints, CertificateParams, CustomExtension, DnType, ExtendedKeyUsagePurpose, IsCa,
    Issuer, KeyPair, KeyUsagePurpose, SanType,
};

pub struct Ca {
    issuer: Issuer<'static, KeyPair>,
    pub pem: String,
}

/// What a leaf certificate carries.
#[derive(Default, Clone)]
pub struct Leaf {
    pub cn: Option<String>,
    pub uris: Vec<String>,
    pub dns: Vec<String>,
    pub emails: Vec<String>,
    pub ips: Vec<std::net::IpAddr>,
    pub org: Vec<String>,
    pub org_unit: Vec<String>,
    /// An extension (dotted-arc OID) holding a UTF8String.
    pub ext: Option<(Vec<u64>, String)>,
    pub client: bool,
}

pub struct Issued {
    pub cert_pem: String,
    pub key_pem: String,
    pub cert_der: Vec<u8>,
}

pub fn ca(name: &str) -> Ca {
    let key = KeyPair::generate().expect("ca key");
    let mut p = CertificateParams::new(Vec::<String>::new()).expect("ca params");
    p.is_ca = IsCa::Ca(BasicConstraints::Unconstrained);
    p.key_usages = vec![KeyUsagePurpose::KeyCertSign, KeyUsagePurpose::CrlSign];
    p.distinguished_name.push(DnType::CommonName, name);
    let cert = p.self_signed(&key).expect("ca cert");
    Ca {
        pem: cert.pem(),
        issuer: Issuer::new(p, key),
    }
}

fn utf8_string(s: &str) -> Vec<u8> {
    assert!(s.len() < 128);
    let mut v = vec![0x0C, s.len() as u8];
    v.extend_from_slice(s.as_bytes());
    v
}

impl Ca {
    pub fn issue(&self, leaf: &Leaf) -> Issued {
        let key = KeyPair::generate().expect("leaf key");
        let mut p = CertificateParams::new(Vec::<String>::new()).expect("params");
        // rcgen names an unnamed subject; a leaf here has only what it is given.
        p.distinguished_name = rcgen::DistinguishedName::new();
        if let Some(cn) = &leaf.cn {
            p.distinguished_name.push(DnType::CommonName, cn.as_str());
        }
        for o in &leaf.org {
            p.distinguished_name
                .push(DnType::OrganizationName, o.as_str());
        }
        for o in &leaf.org_unit {
            p.distinguished_name
                .push(DnType::OrganizationalUnitName, o.as_str());
        }
        for u in &leaf.uris {
            p.subject_alt_names
                .push(SanType::URI(u.as_str().try_into().expect("ia5")));
        }
        for d in &leaf.dns {
            p.subject_alt_names
                .push(SanType::DnsName(d.as_str().try_into().expect("ia5")));
        }
        for e in &leaf.emails {
            p.subject_alt_names
                .push(SanType::Rfc822Name(e.as_str().try_into().expect("ia5")));
        }
        for ip in &leaf.ips {
            p.subject_alt_names.push(SanType::IpAddress(*ip));
        }
        if let Some((oid, v)) = &leaf.ext {
            p.custom_extensions
                .push(CustomExtension::from_oid_content(oid, utf8_string(v)));
        }
        p.extended_key_usages = vec![if leaf.client {
            ExtendedKeyUsagePurpose::ClientAuth
        } else {
            ExtendedKeyUsagePurpose::ServerAuth
        }];
        let cert = p.signed_by(&key, &self.issuer).expect("leaf cert");
        Issued {
            cert_pem: cert.pem(),
            key_pem: key.serialize_pem(),
            cert_der: cert.der().to_vec(),
        }
    }

    /// A server certificate for `localhost` and 127.0.0.1.
    pub fn server(&self) -> Issued {
        self.issue(&Leaf {
            dns: vec!["localhost".into()],
            ips: vec!["127.0.0.1".parse().unwrap()],
            ..Leaf::default()
        })
    }

    /// A client certificate with a DNS SAN.
    pub fn client(&self, dns: &str) -> Issued {
        self.issue(&Leaf {
            dns: vec![dns.into()],
            client: true,
            ..Leaf::default()
        })
    }
}

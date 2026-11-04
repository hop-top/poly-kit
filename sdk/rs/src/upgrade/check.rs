//! The release check: is a newer build of this tool out? The check half
//! of Go `core/upgrade` (`check.go`, `state.go`), without the download
//! and binary replace. Best effort by design: every failure is embedded
//! in the [`CheckResult`], never raised, so a check cannot break the
//! command it rides on.
//!
//! The transport is the caller's: [`Checker::check`] hands the release
//! endpoint to a closure and reads the body it returns. Route that
//! request through [`GuardedClient`](crate::netpolicy) so the egress
//! policy applies, and set [`CheckConfig::offline`] from `--offline`: an
//! offline check is skipped before the closure, the cache or the state
//! directory is touched ("flips off the upgrade check").
//!
//! By default the check reads `releases/latest` and strips a leading
//! `v`. In a repository that tags several components, set
//! [`CheckConfig::tag_prefix`] (`"xray/v"` for `xray/v0.2.0`) so only this
//! tool's releases count, and [`CheckConfig::include_prereleases`] to offer
//! prereleases. Either lists releases instead (at most 3 pages of 100,
//! newest first, drafts skipped, highest semver wins), so the transport is
//! called once per page. They apply to [`CheckConfig::github`] only; a
//! [`CheckConfig::release_url`] endpoint wins over them.

use std::future::Future;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde_json::{json, Value};

use super::{compare_versions, BoxError};

/// How many bytes of release notes a result keeps (Go: 1000, then `…`).
const NOTES_LIMIT: usize = 1000;

/// The default cache lifetime (Go: 4 hours).
pub const DEFAULT_CACHE_TTL: Duration = Duration::from_secs(4 * 60 * 60);

/// What a check is configured with.
#[derive(Clone, Debug)]
pub struct CheckConfig {
    binary: String,
    current: String,
    github_repo: String,
    release_url: String,
    state_dir: Option<PathBuf>,
    cache_ttl: Duration,
    offline: bool,
    tag_prefix: String,
    include_prereleases: bool,
}

impl CheckConfig {
    /// A config for `binary` running `current_version`.
    pub fn new(binary: impl Into<String>, current_version: impl Into<String>) -> CheckConfig {
        CheckConfig {
            binary: binary.into(),
            current: current_version.into(),
            github_repo: String::new(),
            release_url: String::new(),
            state_dir: None,
            cache_ttl: DEFAULT_CACHE_TTL,
            offline: false,
            tag_prefix: String::new(),
            include_prereleases: false,
        }
    }

    /// Checks the latest release of the GitHub repository `owner/repo`.
    pub fn github(mut self, repo: impl Into<String>) -> CheckConfig {
        self.github_repo = repo.into();
        self
    }

    /// Checks a custom endpoint answering `{"version","url","notes"}`;
    /// it wins over [`CheckConfig::github`].
    pub fn release_url(mut self, url: impl Into<String>) -> CheckConfig {
        self.release_url = url.into();
        self
    }

    /// Where the cache lives; the default is
    /// `<XDG state>/<binary>/upgrade`.
    pub fn state_dir(mut self, dir: impl AsRef<Path>) -> CheckConfig {
        self.state_dir = Some(dir.as_ref().to_path_buf());
        self
    }

    /// How long an answer stays fresh.
    pub fn cache_ttl(mut self, ttl: Duration) -> CheckConfig {
        self.cache_ttl = ttl;
        self
    }

    /// Counts only releases whose tag starts with `prefix` (`"xray/v"`
    /// for `xray/v0.2.0`), stripped before the version comparison; other
    /// tags are not candidates. Lists releases instead of reading
    /// `releases/latest`, which may belong to another component.
    pub fn tag_prefix(mut self, prefix: impl Into<String>) -> CheckConfig {
        self.tag_prefix = prefix.into();
        self
    }

    /// Offers prereleases. Lists releases instead of reading
    /// `releases/latest`, which skips them. Off: never offered.
    pub fn include_prereleases(mut self, include: bool) -> CheckConfig {
        self.include_prereleases = include;
        self
    }

    /// Whether releases are listed instead of `releases/latest`.
    fn list_mode(&self) -> bool {
        self.release_url.is_empty()
            && !self.github_repo.is_empty()
            && (!self.tag_prefix.is_empty() || self.include_prereleases)
    }

    /// Switches the check off (`--offline`).
    pub fn offline(mut self, offline: bool) -> CheckConfig {
        self.offline = offline;
        self
    }

    fn state_path(&self) -> PathBuf {
        self.state_dir
            .clone()
            .unwrap_or_else(|| crate::xdg::state_home().join(&self.binary).join("upgrade"))
            .join("cache.json")
    }
}

/// Why a check did nothing.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Skipped {
    /// `--offline`: no request, no cache.
    Offline,
}

/// The outcome of a check. Always returned; a failure sits in
/// [`CheckResult::error`].
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CheckResult {
    /// The running version.
    pub current: String,
    /// The latest release's version, without a leading `v`.
    pub latest: String,
    /// The download URL for this OS and architecture, or the custom
    /// endpoint's.
    pub url: String,
    /// The checksums asset, when the release has one.
    pub checksum_url: String,
    /// The release notes, cut at 1000 characters.
    pub notes: String,
    /// When the answer was fetched, in seconds since the epoch.
    pub checked_at: u64,
    /// The git tag of `latest`; set only when releases are listed
    /// ([`CheckConfig::tag_prefix`], [`CheckConfig::include_prereleases`]).
    pub tag: String,
    /// Whether `latest` is newer than `current`.
    pub update_avail: bool,
    /// Whether the answer came from the cache.
    pub from_cache: bool,
    /// Set when the check did not run.
    pub skipped: Option<Skipped>,
    /// Why the check failed, when it did.
    pub error: Option<String>,
}

/// Where a check stands after the cache is consulted.
enum Step {
    Done(CheckResult),
    Fetch { url: String, custom: bool },
    List,
}

/// Runs release checks.
#[derive(Clone, Debug)]
pub struct Checker {
    cfg: CheckConfig,
}

impl Checker {
    /// A checker for `cfg`.
    pub fn new(cfg: CheckConfig) -> Checker {
        Checker { cfg }
    }

    /// Checks for a newer release: the cached answer while it is fresh,
    /// else `fetch(endpoint)` and its body. Never fails; see
    /// [`CheckResult::error`] and [`CheckResult::skipped`].
    ///
    /// Listing releases calls `fetch` once per page (at most 3).
    pub fn check<F>(&self, mut fetch: F) -> CheckResult
    where
        F: FnMut(&str) -> Result<String, BoxError>,
    {
        match self.begin() {
            Step::Done(r) => r,
            Step::Fetch { url, custom } => {
                let body = fetch(&url);
                self.finish(body, custom)
            }
            Step::List => {
                let mut list = ReleaseList::new(&self.cfg);
                for page in 1..=MAX_PAGES {
                    let body = fetch(&list.page_url(page));
                    if !list.take(page, body) {
                        break;
                    }
                }
                self.finish_list(list)
            }
        }
    }

    /// [`Checker::check`] for an async transport.
    pub async fn check_async<F, Fut>(&self, mut fetch: F) -> CheckResult
    where
        F: FnMut(String) -> Fut,
        Fut: Future<Output = Result<String, BoxError>>,
    {
        match self.begin() {
            Step::Done(r) => r,
            Step::Fetch { url, custom } => {
                let body = fetch(url).await;
                self.finish(body, custom)
            }
            Step::List => {
                let mut list = ReleaseList::new(&self.cfg);
                for page in 1..=MAX_PAGES {
                    let body = fetch(list.page_url(page)).await;
                    if !list.take(page, body) {
                        break;
                    }
                }
                self.finish_list(list)
            }
        }
    }

    fn base(&self) -> CheckResult {
        CheckResult {
            current: self.cfg.current.clone(),
            checked_at: now(),
            ..CheckResult::default()
        }
    }

    fn fail(&self, msg: impl Into<String>) -> CheckResult {
        CheckResult {
            error: Some(msg.into()),
            ..self.base()
        }
    }

    fn begin(&self) -> Step {
        if self.cfg.offline {
            return Step::Done(CheckResult {
                skipped: Some(Skipped::Offline),
                ..self.base()
            });
        }
        if let Some(mut cached) = self.load().filter(|c| self.tag_trusted(c)) {
            if now().saturating_sub(cached.checked_at) < self.cfg.cache_ttl.as_secs() {
                // The running build may have changed since the answer
                // was stored: judge against it, not the stored flag.
                cached.current = self.cfg.current.clone();
                cached.update_avail = is_newer(&cached.current, &cached.latest);
                cached.from_cache = true;
                return Step::Done(cached);
            }
        }
        if !self.cfg.release_url.is_empty() {
            Step::Fetch {
                url: self.cfg.release_url.clone(),
                custom: true,
            }
        } else if self.cfg.list_mode() {
            Step::List
        } else if !self.cfg.github_repo.is_empty() {
            Step::Fetch {
                url: format!(
                    "https://api.github.com/repos/{}/releases/latest",
                    self.cfg.github_repo
                ),
                custom: false,
            }
        } else {
            Step::Done(self.fail("upgrade: no release source configured"))
        }
    }

    fn finish(&self, body: Result<String, BoxError>, custom: bool) -> CheckResult {
        let body = match body {
            Ok(b) => b,
            Err(e) => return self.fail(e.to_string()),
        };
        let parsed = if custom {
            parse_custom(&body)
        } else {
            parse_github(&body)
        };
        let rel = match parsed {
            Ok(r) => r,
            Err(e) => return self.fail(e),
        };
        self.conclude(rel, String::new())
    }

    fn finish_list(&self, list: ReleaseList) -> CheckResult {
        match list.outcome() {
            Ok((rel, tag)) => self.conclude(rel, tag),
            Err(e) => self.fail(e),
        }
    }

    fn conclude(&self, rel: Release, tag: String) -> CheckResult {
        let r = CheckResult {
            latest: rel.version,
            url: rel.url,
            checksum_url: rel.checksum_url,
            notes: rel.notes,
            tag,
            ..self.base()
        };
        let r = CheckResult {
            update_avail: is_newer(&r.current, &r.latest),
            ..r
        };
        self.store(&r);
        r
    }

    /// In list mode a cached tag is trusted only when it starts with the
    /// prefix and names the cached version; anything else, a missing tag
    /// included, is a cache miss. (The cache may sit in a shared
    /// directory, and the tag becomes part of a link.)
    fn tag_trusted(&self, c: &CheckResult) -> bool {
        if !self.cfg.list_mode() {
            return true;
        }
        match tag_version(&self.cfg.tag_prefix, &c.tag) {
            Some(v) => {
                compare_versions(&v, &c.latest) == std::cmp::Ordering::Equal
                    && valid_semver(&c.latest)
            }
            None => false,
        }
    }

    /// The upgrade notice for `r`, `None` when there is nothing to say
    /// (no update, skipped, failed).
    pub fn notice(&self, r: &CheckResult) -> Option<String> {
        if !r.update_avail || r.skipped.is_some() || r.error.is_some() {
            return None;
        }
        let page = if !self.cfg.release_url.is_empty() {
            r.url.clone()
        } else if self.cfg.list_mode() {
            // `releases/latest` may be another component's release, or
            // absent while only prereleases exist: link the release.
            let base = format!("https://github.com/{}/releases", self.cfg.github_repo);
            if r.tag.is_empty() {
                base
            } else {
                format!("{base}/tag/{}", encode_tag(&r.tag))
            }
        } else {
            format!(
                "https://github.com/{}/releases/latest",
                self.cfg.github_repo
            )
        };
        Some(format!(
            "\nA new release of {} is available: v{} → v{}\n{page}\n",
            self.cfg.binary,
            r.current.trim_start_matches('v'),
            r.latest
        ))
    }

    /// Writes the notice for `r`, if any, to `out`.
    pub fn notify(&self, r: &CheckResult, out: &mut dyn Write) -> io::Result<()> {
        match self.notice(r) {
            Some(n) => out.write_all(n.as_bytes()),
            None => Ok(()),
        }
    }

    fn load(&self) -> Option<CheckResult> {
        let raw = std::fs::read_to_string(self.cfg.state_path()).ok()?;
        let v: Value = serde_json::from_str(&raw).ok()?;
        let s = |k: &str| v.get(k).and_then(Value::as_str).unwrap_or("").to_string();
        Some(CheckResult {
            current: s("current"),
            latest: v.get("latest")?.as_str()?.to_string(),
            url: s("url"),
            checksum_url: s("checksum_url"),
            notes: s("notes"),
            checked_at: v.get("checked_at")?.as_u64()?,
            update_avail: false,
            tag: s("tag"),
            ..CheckResult::default()
        })
    }

    /// Cache write failures are not the check's problem.
    fn store(&self, r: &CheckResult) {
        let path = self.cfg.state_path();
        let Some(dir) = path.parent() else { return };
        if std::fs::create_dir_all(dir).is_err() {
            return;
        }
        let rec = json!({
            "current": r.current,
            "latest": r.latest,
            "url": r.url,
            "checksum_url": r.checksum_url,
            "notes": r.notes,
            "checked_at": r.checked_at,
            "update_avail": r.update_avail,
        });
        let mut rec = rec;
        if !r.tag.is_empty() {
            rec["tag"] = json!(r.tag);
        }
        let _ = std::fs::write(path, rec.to_string());
    }
}

fn now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| d.as_secs())
}

/// Go `isNewer`: both versions present and `latest` orders after
/// `current`.
fn is_newer(current: &str, latest: &str) -> bool {
    !current.is_empty()
        && !latest.is_empty()
        && compare_versions(latest, current) == std::cmp::Ordering::Greater
}

struct Release {
    version: String,
    url: String,
    checksum_url: String,
    notes: String,
}

fn truncate_notes(s: &str) -> String {
    if s.chars().count() > NOTES_LIMIT {
        let cut: String = s.chars().take(NOTES_LIMIT).collect();
        format!("{cut}…")
    } else {
        s.to_string()
    }
}

fn parse_github(body: &str) -> Result<Release, String> {
    let v: Value =
        serde_json::from_str(body).map_err(|e| format!("upgrade: decode github response: {e}"))?;
    release_from(&v)
}

fn release_from(v: &Value) -> Result<Release, String> {
    let tag = v
        .get("tag_name")
        .and_then(Value::as_str)
        .ok_or("upgrade: decode github response: no tag_name")?;
    let assets: Vec<(String, String)> = v
        .get("assets")
        .and_then(Value::as_array)
        .map(|a| {
            a.iter()
                .filter_map(|x| {
                    Some((
                        x.get("name")?.as_str()?.to_lowercase(),
                        x.get("browser_download_url")?.as_str()?.to_string(),
                    ))
                })
                .collect()
        })
        .unwrap_or_default();
    Ok(Release {
        version: tag.trim_start_matches('v').to_string(),
        url: select_asset(&assets),
        checksum_url: select_checksum(&assets),
        notes: truncate_notes(v.get("body").and_then(Value::as_str).unwrap_or("")),
    })
}

/// The asset for this OS and architecture (Go `selectAsset`): both in
/// the name, else just the OS. `aarch64` and `arm64`, `x86_64` and
/// `amd64` are one architecture.
fn select_asset(assets: &[(String, String)]) -> String {
    let os = std::env::consts::OS;
    let arch: &[&str] = match std::env::consts::ARCH {
        "x86_64" => &["amd64", "x86_64"],
        "aarch64" => &["arm64", "aarch64"],
        other => &[other],
    };
    assets
        .iter()
        .find(|(n, _)| n.contains(os) && arch.iter().any(|a| n.contains(a)))
        .or_else(|| assets.iter().find(|(n, _)| n.contains(os)))
        .map(|(_, u)| u.clone())
        .unwrap_or_default()
}

fn select_checksum(assets: &[(String, String)]) -> String {
    const SUFFIXES: [&str; 3] = ["checksums.txt", "sha256sums", "sha256sums.txt"];
    assets
        .iter()
        .find(|(n, _)| SUFFIXES.iter().any(|s| n.ends_with(s)))
        .map(|(_, u)| u.clone())
        .unwrap_or_default()
}

fn parse_custom(body: &str) -> Result<Release, String> {
    let v: Value =
        serde_json::from_str(body).map_err(|e| format!("upgrade: decode custom release: {e}"))?;
    let s = |k: &str| v.get(k).and_then(Value::as_str).unwrap_or("").to_string();
    let version = s("version");
    if version.is_empty() {
        return Err("upgrade: decode custom release: no version".into());
    }
    Ok(Release {
        version: version.trim_start_matches('v').to_string(),
        url: s("url"),
        checksum_url: String::new(),
        notes: s("notes"),
    })
}

/// Releases requested per page when listing (GitHub's maximum).
const PAGE_SIZE: usize = 100;
/// Pages fetched at most when listing: bounds a check to 3 requests.
const MAX_PAGES: usize = 3;

/// Accumulates the highest matching release across listed pages.
struct ReleaseList<'a> {
    cfg: &'a CheckConfig,
    best: Option<(String, String, Release)>,
    error: Option<String>,
}

impl<'a> ReleaseList<'a> {
    fn new(cfg: &'a CheckConfig) -> ReleaseList<'a> {
        ReleaseList {
            cfg,
            best: None,
            error: None,
        }
    }

    fn page_url(&self, page: usize) -> String {
        format!(
            "https://api.github.com/repos/{}/releases?per_page={PAGE_SIZE}&page={page}",
            self.cfg.github_repo
        )
    }

    /// Takes a page's body; whether to ask for the next page. A failing
    /// first page fails the check; a later one keeps what is found.
    fn take(&mut self, page: usize, body: Result<String, BoxError>) -> bool {
        let items = body
            .map_err(|e| e.to_string())
            .and_then(|b| {
                serde_json::from_str::<Value>(&b)
                    .map_err(|e| format!("upgrade: decode github response: {e}"))
            })
            .and_then(|v| match v {
                Value::Array(a) => Ok(a),
                _ => Err("upgrade: decode github response: not a list".to_string()),
            });
        let items = match items {
            Ok(i) => i,
            Err(e) => {
                if page == 1 {
                    self.error = Some(e);
                }
                return false;
            }
        };
        for rel in &items {
            self.consider(rel);
        }
        items.len() >= PAGE_SIZE
    }

    fn consider(&mut self, rel: &Value) {
        if rel.get("draft").and_then(Value::as_bool) == Some(true) {
            return;
        }
        let Some(tag) = rel.get("tag_name").and_then(Value::as_str) else {
            return;
        };
        let Some(version) = tag_version(&self.cfg.tag_prefix, tag) else {
            return;
        };
        let flagged = rel.get("prerelease").and_then(Value::as_bool) == Some(true);
        if !self.cfg.include_prereleases && (flagged || is_prerelease(&version)) {
            return;
        }
        if let Some((best, _, _)) = &self.best {
            if compare_versions(&version, best) != std::cmp::Ordering::Greater {
                return;
            }
        }
        if let Ok(mut r) = release_from(rel) {
            r.version = version.clone();
            self.best = Some((version, tag.to_string(), r));
        }
    }

    fn outcome(self) -> Result<(Release, String), String> {
        match (self.best, self.error) {
            (Some((_, tag, rel)), _) => Ok((rel, tag)),
            (None, Some(e)) => Err(e),
            (None, None) => Err("upgrade: no matching release".into()),
        }
    }
}

/// The strict semver a tag names once `prefix` and one `v` are stripped;
/// `None` for another component's tag or a non-semver.
fn tag_version(prefix: &str, tag: &str) -> Option<String> {
    let rest = tag.strip_prefix(prefix)?;
    let rest = rest.strip_prefix('v').unwrap_or(rest);
    valid_semver(rest).then(|| rest.to_string())
}

/// semver.org 2.0.0, strictly (`1.2.3`, `1.2.3-rc.1`, `1.2.3+build`).
fn valid_semver(s: &str) -> bool {
    let (rest, build) = match s.split_once('+') {
        Some((r, b)) => (r, Some(b)),
        None => (s, None),
    };
    let (core, pre) = match rest.split_once('-') {
        Some((c, p)) => (c, Some(p)),
        None => (rest, None),
    };
    let nums: Vec<&str> = core.split('.').collect();
    let num_ok = |n: &str| {
        !n.is_empty() && n.bytes().all(|b| b.is_ascii_digit()) && (n == "0" || !n.starts_with('0'))
    };
    let ident_ok =
        |i: &str| !i.is_empty() && i.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-');
    nums.len() == 3
        && nums.iter().all(|n| num_ok(n))
        && pre.is_none_or(|p| {
            p.split('.')
                .all(|i| ident_ok(i) && (!i.bytes().all(|b| b.is_ascii_digit()) || num_ok(i)))
        })
        && build.is_none_or(|b| b.split('.').all(ident_ok))
}

fn is_prerelease(version: &str) -> bool {
    version.split('+').next().is_some_and(|v| v.contains('-'))
}

/// Percent-encodes each `/`-separated segment as JavaScript's
/// `encodeURIComponent` does.
fn encode_tag(tag: &str) -> String {
    tag.split('/')
        .map(|seg| {
            seg.bytes()
                .map(|b| match b {
                    b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' => (b as char).to_string(),
                    b'-' | b'_' | b'.' | b'!' | b'~' | b'*' | b'\'' | b'(' | b')' => {
                        (b as char).to_string()
                    }
                    _ => format!("%{b:02X}"),
                })
                .collect::<String>()
        })
        .collect::<Vec<_>>()
        .join("/")
}

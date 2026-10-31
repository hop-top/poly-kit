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
    pub fn check<F>(&self, fetch: F) -> CheckResult
    where
        F: FnOnce(&str) -> Result<String, BoxError>,
    {
        match self.begin() {
            Step::Done(r) => r,
            Step::Fetch { url, custom } => {
                let body = fetch(&url);
                self.finish(body, custom)
            }
        }
    }

    /// [`Checker::check`] for an async transport.
    pub async fn check_async<F, Fut>(&self, fetch: F) -> CheckResult
    where
        F: FnOnce(String) -> Fut,
        Fut: Future<Output = Result<String, BoxError>>,
    {
        match self.begin() {
            Step::Done(r) => r,
            Step::Fetch { url, custom } => {
                let body = fetch(url).await;
                self.finish(body, custom)
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
        if let Some(mut cached) = self.load() {
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
        let r = CheckResult {
            latest: rel.version,
            url: rel.url,
            checksum_url: rel.checksum_url,
            notes: rel.notes,
            ..self.base()
        };
        let r = CheckResult {
            update_avail: is_newer(&r.current, &r.latest),
            ..r
        };
        self.store(&r);
        r
    }

    /// The upgrade notice for `r`, `None` when there is nothing to say
    /// (no update, skipped, failed).
    pub fn notice(&self, r: &CheckResult) -> Option<String> {
        if !r.update_avail || r.skipped.is_some() || r.error.is_some() {
            return None;
        }
        let page = if !self.cfg.release_url.is_empty() {
            r.url.clone()
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

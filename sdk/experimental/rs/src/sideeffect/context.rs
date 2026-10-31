//! The dry-run flag on a [`Context`].

use crate::bus::Context;

/// The private key type: a value of this type on a context is the flag.
/// Context values are keyed by type, so nothing else can collide.
#[derive(Clone, Copy)]
struct DryRun(bool);

/// Returns a copy of `ctx` carrying the dry-run flag. The CLI wrapper
/// installs this on the root context when `--dry-run` is set.
///
/// `v = false` explicitly clears an inherited flag (rare; useful when
/// re-entering real-effect code from inside a dry-run scope).
pub fn with_dry_run(ctx: &Context, v: bool) -> Context {
    ctx.with_value(DryRun(v))
}

/// Reports whether `ctx` was tagged dry-run via [`with_dry_run`]. A
/// context never tagged reports false.
///
/// A free function, so library code can branch on dry-run without taking
/// a CLI dependency.
pub fn is_dry_run(ctx: &Context) -> bool {
    ctx.value::<DryRun>().is_some_and(|d| d.0)
}

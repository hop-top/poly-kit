use crate::bus::Context;

use super::BoxError;

/// Releases a lock taken by [`Locker::lock`]. Called once, when the run
/// for that schema has finished.
pub type Unlock = Box<dyn FnOnce() -> Result<(), BoxError> + Send>;

/// Optional capability of a [`SchemaDriver`](super::SchemaDriver) whose
/// store several processes can migrate at once; a driver reaches the
/// migrator through [`SchemaDriver::as_locker`](super::SchemaDriver::as_locker).
/// Go `upgrade.Locker`.
///
/// The migrator calls [`lock`](Self::lock) before it reads the driver's
/// version and holds the lock for the whole run of that schema (version
/// read, baseline, backup, every `up`, every version record, rollback,
/// backup prune), then calls the returned [`Unlock`] on every path.
///
/// `lock` blocks until the lock is acquired or `ctx` is done; in the
/// second case it returns the context's error and holds nothing. An error
/// from `lock` fails the schema before anything else
/// ([`MigrateError::Lock`](super::MigrateError::Lock)); an error from the
/// unlock goes to the warning sink and fails nothing. The unlock must not
/// borrow the driver: the migrator keeps using the driver while the lock
/// is held.
pub trait Locker: Send + Sync {
    /// Takes the lock, waiting while another holder has it.
    fn lock(&self, ctx: &Context) -> Result<Unlock, BoxError>;
}

#[cfg(not(target_arch = "wasm32"))]
/// A held lock; releases on drop so every exit path, a panic included,
/// unlocks. A failed unlock is passed to `warn`.
pub(super) struct Held<'a> {
    unlock: Option<Unlock>,
    schema: String,
    warn: &'a (dyn Fn(&str) + Send + Sync),
}

#[cfg(not(target_arch = "wasm32"))]
impl<'a> Held<'a> {
    /// Takes the driver's lock, if it has one. `Ok(None)` for a driver
    /// that is not a [`Locker`].
    pub(super) fn acquire(
        locker: Option<&dyn Locker>,
        schema: &str,
        ctx: &Context,
        warn: &'a (dyn Fn(&str) + Send + Sync),
    ) -> Result<Option<Held<'a>>, BoxError> {
        let Some(locker) = locker else {
            return Ok(None);
        };
        let unlock = locker.lock(ctx)?;
        Ok(Some(Held {
            unlock: Some(unlock),
            schema: schema.to_string(),
            warn,
        }))
    }
}

#[cfg(not(target_arch = "wasm32"))]
impl Drop for Held<'_> {
    fn drop(&mut self) {
        if let Some(unlock) = self.unlock.take() {
            if let Err(e) = unlock() {
                (self.warn)(&format!("warning: unlock {}: {e}", self.schema));
            }
        }
    }
}

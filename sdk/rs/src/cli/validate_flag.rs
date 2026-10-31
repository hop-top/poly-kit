//! Flag-value validators: reject an ill-formed value of a flag before
//! the handler runs, through the same error envelope the rest of the
//! tool uses. Ports Go `WithFlagValidator`.

use std::sync::Arc;

use crate::output::CliError;

use super::root::Root;

/// Inspects the value a flag was given: `None` accepts, a structured
/// error rejects.
pub type FlagValidator = Arc<dyn Fn(&str) -> Option<CliError> + Send + Sync>;

impl Root {
    /// Registers a validator for flag `name`. It fires once per leaf
    /// invocation, after the parse and before the handler, and only
    /// when the user actually set the flag (a default is not
    /// validated). A rejection is rendered like any handler error.
    /// Registering a name twice replaces the earlier validator; a name
    /// no command declares never fires.
    pub fn with_flag_validator<F>(&mut self, name: &str, f: F) -> &mut Root
    where
        F: Fn(&str) -> Option<CliError> + Send + Sync + 'static,
    {
        if name.is_empty() {
            return self;
        }
        let f: FlagValidator = Arc::new(f);
        match self.flag_validators.iter_mut().find(|(n, _)| n == name) {
            Some(slot) => slot.1 = f,
            None => self.flag_validators.push((name.to_string(), f)),
        }
        self
    }
}

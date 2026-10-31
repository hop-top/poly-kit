//! Filesystem-backed keypair store: `id_ed25519` (0600) and
//! `id_ed25519.pub` (0644) in a 0700 directory. Native only.

use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};

use super::{parse_private_key, IdentityError, Keypair};
use crate::xdg;

const PRIVATE: &str = "id_ed25519";
const PUBLIC: &str = "id_ed25519.pub";

fn io(context: &'static str) -> impl FnOnce(std::io::Error) -> IdentityError {
    move |source| IdentityError::Io { context, source }
}

/// Keypair persistence in one directory.
#[derive(Debug, Clone)]
pub struct Store {
    dir: PathBuf,
}

/// A store at `<data home>/kit/identity`.
///
/// # Errors
///
/// [`IdentityError::Io`] when the directory cannot be created.
pub fn default_store() -> Result<Store, IdentityError> {
    Store::new(xdg::raw_data_dir("kit").join("identity"))
}

impl Store {
    /// A store at `dir`, created (with parents, mode 0700 on Unix) when
    /// missing.
    ///
    /// # Errors
    ///
    /// [`IdentityError::Io`] when the directory cannot be created.
    pub fn new(dir: impl AsRef<Path>) -> Result<Store, IdentityError> {
        let dir = dir.as_ref().to_path_buf();
        let mut b = fs::DirBuilder::new();
        b.recursive(true);
        #[cfg(unix)]
        std::os::unix::fs::DirBuilderExt::mode(&mut b, 0o700);
        b.create(&dir).map_err(io("create store dir"))?;
        Ok(Store { dir })
    }

    /// Writes the keypair atomically: each file goes to a temp name, then
    /// is renamed over its target, so a crash never leaves a partial key.
    ///
    /// # Errors
    ///
    /// [`IdentityError::Marshal`] or [`IdentityError::Io`].
    pub fn save(&self, kp: &Keypair) -> Result<(), IdentityError> {
        let private = kp.marshal_private_key()?;
        let public = kp.marshal_public_key()?;
        atomic_write(&self.dir.join(PRIVATE), private.as_bytes(), 0o600)
            .map_err(io("write private key"))?;
        atomic_write(&self.dir.join(PUBLIC), public.as_bytes(), 0o644)
            .map_err(io("write public key"))
    }

    /// Reads the keypair.
    ///
    /// # Errors
    ///
    /// [`IdentityError::NotFound`] when none is stored; a parse error
    /// when the file is not an Ed25519 PKCS#8 key.
    pub fn load(&self) -> Result<Keypair, IdentityError> {
        match fs::read(self.dir.join(PRIVATE)) {
            Ok(data) => parse_private_key(&data),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Err(IdentityError::NotFound),
            Err(e) => Err(io("read private key")(e)),
        }
    }

    /// Loads the stored keypair, or generates and saves one when none
    /// exists. A key that exists but cannot be read is an error, never
    /// overwritten.
    ///
    /// # Errors
    ///
    /// As [`Store::load`] (other than not found), [`Keypair::generate`]
    /// and [`Store::save`].
    pub fn load_or_generate(&self) -> Result<Keypair, IdentityError> {
        match self.load() {
            Ok(kp) => Ok(kp),
            Err(IdentityError::NotFound) => {
                let kp = Keypair::generate()?;
                self.save(&kp)?;
                Ok(kp)
            }
            Err(e) => Err(e),
        }
    }

    /// True when a private key file exists.
    pub fn exists(&self) -> bool {
        self.dir.join(PRIVATE).exists()
    }
}

fn atomic_write(dst: &Path, data: &[u8], mode: u32) -> std::io::Result<()> {
    let mut tmp = dst.as_os_str().to_owned();
    tmp.push(".tmp");
    let tmp = PathBuf::from(tmp);
    // A stale temp file may carry looser permissions; start clean.
    let _ = fs::remove_file(&tmp);
    let mut opts = fs::OpenOptions::new();
    opts.write(true).create_new(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut opts, mode);
    #[cfg(not(unix))]
    let _ = mode;
    let mut f = opts.open(&tmp)?;
    f.write_all(data)?;
    f.sync_all()?;
    drop(f);
    #[cfg(unix)]
    fs::set_permissions(&tmp, std::os::unix::fs::PermissionsExt::from_mode(mode))?;
    fs::rename(&tmp, dst)
}

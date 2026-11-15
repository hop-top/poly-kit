use std::marker::PhantomData;

/// Typed document collection (`/:type/...`).
pub struct Collection<'a, T> {
    pub(crate) client: &'a reqwest::Client,
    pub(crate) base: &'a str,
    pub(crate) type_name: String,
    pub(crate) token: Option<&'a str>,
    pub(crate) _doc: PhantomData<fn() -> T>,
}

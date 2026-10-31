//! Shared fixtures for the sync tests: the entity, an in-memory
//! repository (Go's `replMemRepo`), a repository that fails on demand
//! (`errRepo`) and a recording logger.
#![allow(dead_code)]

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use hop_top_kit::bus::Context;
use hop_top_kit::domain::{Entity, Query, RepoError, Repository};
use hop_top_kit::sync::Logger;
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct Item {
    pub id: String,
    pub name: String,
}

impl Item {
    pub fn new(id: &str, name: &str) -> Self {
        Item {
            id: id.into(),
            name: name.into(),
        }
    }
}

impl Entity for Item {
    fn id(&self) -> &str {
        &self.id
    }
}

pub fn bg() -> Context {
    Context::background()
}

/// Go's `replMemRepo`: Create conflicts, Update is not-found when
/// absent, Delete is idempotent.
#[derive(Default)]
pub struct MemRepo {
    data: Mutex<BTreeMap<String, Item>>,
}

impl MemRepo {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }

    pub fn seed(&self, e: Item) {
        self.data.lock().unwrap().insert(e.id.clone(), e);
    }

    pub fn find(&self, id: &str) -> Option<Item> {
        self.data.lock().unwrap().get(id).cloned()
    }
}

impl Repository<Item> for MemRepo {
    fn create(&self, _: &Context, e: &Item) -> Result<(), RepoError> {
        let mut m = self.data.lock().unwrap();
        if m.contains_key(&e.id) {
            return Err(RepoError::Conflict);
        }
        m.insert(e.id.clone(), e.clone());
        Ok(())
    }
    fn get(&self, _: &Context, id: &str) -> Result<Item, RepoError> {
        self.find(id).ok_or(RepoError::NotFound)
    }
    fn list(&self, _: &Context, _: &Query) -> Result<Vec<Item>, RepoError> {
        Ok(Vec::new())
    }
    fn update(&self, _: &Context, e: &Item) -> Result<(), RepoError> {
        let mut m = self.data.lock().unwrap();
        match m.get_mut(&e.id) {
            Some(slot) => {
                *slot = e.clone();
                Ok(())
            }
            None => Err(RepoError::NotFound),
        }
    }
    fn delete(&self, _: &Context, id: &str) -> Result<(), RepoError> {
        self.data.lock().unwrap().remove(id);
        Ok(())
    }
}

/// Go's `errRepo`: each operation fails with the configured error.
#[derive(Default)]
pub struct ErrRepo {
    pub delete_err: Option<&'static str>,
    pub update_err: Option<&'static str>,
    pub create_err: Option<&'static str>,
    pub update_not_found: bool,
}

fn fail(msg: Option<&'static str>) -> Result<(), RepoError> {
    msg.map_or(Ok(()), |m| Err(RepoError::other(m)))
}

impl Repository<Item> for ErrRepo {
    fn create(&self, _: &Context, _: &Item) -> Result<(), RepoError> {
        fail(self.create_err)
    }
    fn get(&self, _: &Context, _: &str) -> Result<Item, RepoError> {
        Err(RepoError::NotFound)
    }
    fn list(&self, _: &Context, _: &Query) -> Result<Vec<Item>, RepoError> {
        Ok(Vec::new())
    }
    fn update(&self, _: &Context, _: &Item) -> Result<(), RepoError> {
        if self.update_not_found {
            return Err(RepoError::NotFound);
        }
        fail(self.update_err)
    }
    fn delete(&self, _: &Context, _: &str) -> Result<(), RepoError> {
        fail(self.delete_err)
    }
}

/// Collects every logged line as `msg k=v k=v`.
#[derive(Default)]
pub struct Lines(Mutex<Vec<String>>);

impl Lines {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }
    pub fn all(&self) -> String {
        self.0.lock().unwrap().join("\n")
    }
}

impl Logger for Lines {
    fn error(&self, msg: &str, fields: &[(&str, &str)]) {
        let mut line = msg.to_string();
        for (k, v) in fields {
            line.push_str(&format!(" {k}={v}"));
        }
        self.0.lock().unwrap().push(line);
    }
}

/// Polls `cond` until it holds or the deadline passes.
pub fn eventually(cond: impl Fn() -> bool) -> bool {
    let end = Instant::now() + Duration::from_secs(5);
    while Instant::now() < end {
        if cond() {
            return true;
        }
        std::thread::sleep(Duration::from_millis(5));
    }
    cond()
}

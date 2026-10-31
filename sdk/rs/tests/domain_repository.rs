//! The domain repository port: the entity constraint, the query, the
//! sentinel errors and a repository used as a trait object. Go's
//! `Repository[T]` has no tests of its own; these pin the contract its
//! doc comments state.
#![cfg(feature = "domain")]

use std::collections::BTreeMap;
use std::error::Error;
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::Context;
use hop_top_kit::domain::{Entity, Query, RepoError, Repository};

#[derive(Clone, Debug, PartialEq)]
struct Note {
    id: String,
    body: String,
}

impl Entity for Note {
    fn id(&self) -> &str {
        &self.id
    }
}

#[derive(Default)]
struct Mem(Mutex<BTreeMap<String, Note>>);

impl Repository<Note> for Mem {
    fn create(&self, _: &Context, e: &Note) -> Result<(), RepoError> {
        let mut m = self.0.lock().unwrap();
        if m.contains_key(e.id()) {
            return Err(RepoError::Conflict);
        }
        m.insert(e.id().to_string(), e.clone());
        Ok(())
    }
    fn get(&self, _: &Context, id: &str) -> Result<Note, RepoError> {
        self.0
            .lock()
            .unwrap()
            .get(id)
            .cloned()
            .ok_or(RepoError::NotFound)
    }
    fn list(&self, _: &Context, q: &Query) -> Result<Vec<Note>, RepoError> {
        let m = self.0.lock().unwrap();
        let it = m.values().skip(q.offset).cloned();
        Ok(if q.limit == 0 {
            it.collect()
        } else {
            it.take(q.limit).collect()
        })
    }
    fn update(&self, _: &Context, e: &Note) -> Result<(), RepoError> {
        match self.0.lock().unwrap().get_mut(e.id()) {
            Some(slot) => {
                *slot = e.clone();
                Ok(())
            }
            None => Err(RepoError::NotFound),
        }
    }
    fn delete(&self, _: &Context, id: &str) -> Result<(), RepoError> {
        self.0
            .lock()
            .unwrap()
            .remove(id)
            .map(|_| ())
            .ok_or(RepoError::NotFound)
    }
}

fn note(id: &str) -> Note {
    Note {
        id: id.into(),
        body: format!("body {id}"),
    }
}

#[test]
fn sentinel_texts_are_gos() {
    assert_eq!(RepoError::NotFound.to_string(), "domain: not found");
    assert_eq!(RepoError::Conflict.to_string(), "domain: conflict");
    assert_eq!(
        RepoError::Validation.to_string(),
        "domain: validation error"
    );
}

#[test]
fn predicates_are_errors_is() {
    assert!(RepoError::NotFound.is_not_found());
    assert!(!RepoError::NotFound.is_conflict());
    assert!(RepoError::Conflict.is_conflict());
    assert!(RepoError::Validation.is_validation());
    let other = RepoError::other("disk full");
    assert!(!other.is_not_found() && !other.is_conflict() && !other.is_validation());
    assert_eq!(other.to_string(), "disk full");
}

#[test]
fn other_keeps_its_source() {
    let io = std::io::Error::other("boom");
    let e = RepoError::from_source(io);
    assert_eq!(e.to_string(), "boom");
    assert!(e.source().is_some());
}

#[test]
fn query_default_means_no_limit_offset_sort_or_search() {
    let q = Query::default();
    assert_eq!((q.limit, q.offset), (0, 0));
    assert!(q.sort.is_empty() && q.search.is_empty());
}

#[test]
fn crud_through_a_trait_object() {
    let ctx = Context::background();
    let repo: Arc<dyn Repository<Note>> = Arc::new(Mem::default());

    repo.create(&ctx, &note("a")).unwrap();
    assert!(repo.create(&ctx, &note("a")).unwrap_err().is_conflict());
    assert_eq!(repo.get(&ctx, "a").unwrap(), note("a"));
    assert!(repo.get(&ctx, "zz").unwrap_err().is_not_found());

    let changed = Note {
        id: "a".into(),
        body: "new".into(),
    };
    repo.update(&ctx, &changed).unwrap();
    assert_eq!(repo.get(&ctx, "a").unwrap().body, "new");
    assert!(repo.update(&ctx, &note("zz")).unwrap_err().is_not_found());

    repo.create(&ctx, &note("b")).unwrap();
    repo.create(&ctx, &note("c")).unwrap();
    assert_eq!(repo.list(&ctx, &Query::default()).unwrap().len(), 3);
    let page = Query {
        limit: 1,
        offset: 1,
        ..Query::default()
    };
    assert_eq!(repo.list(&ctx, &page).unwrap(), vec![note("b")]);

    repo.delete(&ctx, "a").unwrap();
    assert!(repo.delete(&ctx, "a").unwrap_err().is_not_found());
}

#[test]
fn repository_is_send_and_sync_as_a_trait_object() {
    fn assert<T: Send + Sync + ?Sized>() {}
    assert::<dyn Repository<Note>>();
    assert::<RepoError>();
}

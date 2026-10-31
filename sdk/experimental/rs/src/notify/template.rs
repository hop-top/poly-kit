//! Event templates shared by the outbound sinks.
//!
//! Go renders `text/template` against the `bus.Event`. Rust has no such
//! engine in the standard library, and adding one for three sinks is
//! not worth a dependency, so this is the small subset the Go sinks'
//! tests and examples use: literal text and `{{.Field}}` actions.
//!
//! Fields: `.Topic`, `.Source`, `.Timestamp`, `.ID`, `.WorkspaceID`,
//! `.Subject`, `.DataContentType`, `.DataSchema`, and `.Payload`, which
//! may continue into a JSON object with dots (`.Payload.job.name`).
//! Whitespace inside the braces is ignored. A string renders as is,
//! `null` as `<nil>`, a missing payload member as `<no value>` (Go's
//! text), and any other JSON value as compact JSON.
//!
//! The syntax is checked when the template is built, so a bad one fails
//! at once; an unknown field fails at render time, as a missing struct
//! field does in Go.

use std::fmt;

use serde_json::Value;

use crate::bus::Event;

/// A template failed to parse or render.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TemplateError(String);

impl TemplateError {
    fn new(msg: impl Into<String>) -> Self {
        TemplateError(msg.into())
    }

    /// An error with a message of your own, for a custom [`Template`].
    pub fn from_message(msg: impl Into<String>) -> Self {
        TemplateError(msg.into())
    }
}

impl fmt::Display for TemplateError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for TemplateError {}

/// Renders a string from an event. Implementations must be safe for
/// concurrent use: a sink calls `render` once per drain.
pub trait Template: Send + Sync {
    /// The rendered text for `event`.
    fn render(&self, event: &Event) -> Result<String, TemplateError>;
}

impl<F> Template for F
where
    F: Fn(&Event) -> Result<String, TemplateError> + Send + Sync,
{
    fn render(&self, event: &Event) -> Result<String, TemplateError> {
        self(event)
    }
}

/// A template that renders the same text for every event.
#[derive(Clone, Debug)]
pub struct LiteralTemplate(String);

impl LiteralTemplate {
    /// Renders `s` regardless of the event: a fixed title or subject.
    pub fn new(s: impl Into<String>) -> Self {
        LiteralTemplate(s.into())
    }
}

impl Template for LiteralTemplate {
    fn render(&self, _: &Event) -> Result<String, TemplateError> {
        Ok(self.0.clone())
    }
}

#[derive(Clone, Debug)]
enum Part {
    Text(String),
    Field(Vec<String>),
}

/// A parsed `{{.Field}}` template: Go's `TextTemplate` /
/// `SlackTemplate` parse step.
#[derive(Clone, Debug)]
pub struct TextTemplate(Vec<Part>);

impl TextTemplate {
    /// Parses `src`. An unclosed `{{`, an empty action or an action that
    /// is not a `.Field` path is an error.
    pub fn parse(src: &str) -> Result<Self, TemplateError> {
        let mut parts = Vec::new();
        let mut rest = src;
        while let Some(i) = rest.find("{{") {
            if i > 0 {
                parts.push(Part::Text(rest[..i].to_string()));
            }
            let after = &rest[i + 2..];
            let Some(j) = after.find("}}") else {
                return Err(TemplateError::new("template: unclosed action"));
            };
            parts.push(Part::Field(parse_action(&after[..j])?));
            rest = &after[j + 2..];
        }
        if !rest.is_empty() {
            parts.push(Part::Text(rest.to_string()));
        }
        Ok(TextTemplate(parts))
    }
}

fn parse_action(action: &str) -> Result<Vec<String>, TemplateError> {
    let a = action.trim();
    let Some(path) = a.strip_prefix('.') else {
        return Err(TemplateError::new(format!(
            "template: unsupported action {{{{{a}}}}}: want {{{{.Field}}}}"
        )));
    };
    let segs: Vec<String> = path.split('.').map(str::to_string).collect();
    if segs
        .iter()
        .any(|s| s.is_empty() || s.contains(char::is_whitespace))
    {
        return Err(TemplateError::new(format!(
            "template: bad field path {{{{{a}}}}}"
        )));
    }
    Ok(segs)
}

impl Template for TextTemplate {
    fn render(&self, e: &Event) -> Result<String, TemplateError> {
        let mut out = String::new();
        for p in &self.0 {
            match p {
                Part::Text(t) => out.push_str(t),
                Part::Field(path) => out.push_str(&resolve(e, path)?),
            }
        }
        Ok(out)
    }
}

fn resolve(e: &Event, path: &[String]) -> Result<String, TemplateError> {
    let (head, tail) = (path[0].as_str(), &path[1..]);
    let plain = |s: &str| {
        if tail.is_empty() {
            Ok(s.to_string())
        } else {
            Err(TemplateError::new(format!(
                "template: can't evaluate field {} in type string",
                tail[0]
            )))
        }
    };
    match head {
        "Topic" => plain(e.topic.as_str()),
        "Source" => plain(&e.source),
        "Timestamp" => plain(&e.timestamp),
        "ID" => plain(&e.id),
        "WorkspaceID" => plain(&e.workspace_id),
        "Subject" => plain(&e.subject),
        "DataContentType" => plain(&e.data_content_type),
        "DataSchema" => plain(&e.data_schema),
        "Payload" => {
            let mut v = Some(&e.payload);
            for seg in tail {
                v = match v {
                    Some(Value::Object(m)) => m.get(seg),
                    Some(Value::Null) | None => None,
                    Some(_) => {
                        return Err(TemplateError::new(format!(
                            "template: can't evaluate field {seg} in a non-object payload"
                        )))
                    }
                };
            }
            Ok(match v {
                None => "<no value>".to_string(),
                Some(Value::Null) => "<nil>".to_string(),
                Some(Value::String(s)) => s.clone(),
                Some(other) => other.to_string(),
            })
        }
        other => Err(TemplateError::new(format!(
            "template: can't evaluate field {other} in type bus.Event"
        ))),
    }
}

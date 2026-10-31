//! [`ToolChoice`]: whether, and which, tool a tool-calling request must
//! call (Go `llm.ToolChoice`, `CheckToolChoice`).

use std::borrow::Cow;
use std::fmt;

use serde::Serialize;

use super::error::LlmError;
use super::types::ToolDef;

/// Whether the model must call a tool. A string, as in Go, so a mode
/// read from config or the wire stays representable; adapters reject
/// one that is not a constant below ([`check_tool_choice`]).
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize)]
#[serde(transparent)]
pub struct ToolChoiceMode(Cow<'static, str>);

impl ToolChoiceMode {
    /// The model decides (the providers' default).
    pub const AUTO: Self = Self(Cow::Borrowed("auto"));
    /// The model calls at least one tool, its pick.
    pub const ANY: Self = Self(Cow::Borrowed("any"));
    /// No tool calls although tools are defined.
    pub const NONE: Self = Self(Cow::Borrowed("none"));
    /// The model calls the tool [`ToolChoice::name`].
    pub const TOOL: Self = Self(Cow::Borrowed("tool"));

    /// A mode by its name; see the constants for the valid ones.
    pub fn new(mode: impl Into<String>) -> Self {
        Self(Cow::Owned(mode.into()))
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }

    fn known(&self) -> bool {
        [Self::AUTO, Self::ANY, Self::NONE, Self::TOOL].contains(self)
    }
}

impl fmt::Display for ToolChoiceMode {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

/// Constrains tool use on a tool-calling request
/// ([`Request::tool_choice`](super::Request::tool_choice)). `name` is
/// the tool's [`ToolDef::name`] as defined (before any provider name
/// mapping), set with [`ToolChoiceMode::TOOL`] only.
///
/// `None` on the request sends none, leaving the provider default.
/// Adapters reject an invalid choice ([`check_tool_choice`]) before
/// sending, and so does a call without tools (`complete`, `stream`).
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ToolChoice {
    pub mode: ToolChoiceMode,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub name: String,
}

impl ToolChoice {
    fn of(mode: ToolChoiceMode) -> Self {
        ToolChoice {
            mode,
            name: String::new(),
        }
    }

    /// [`ToolChoiceMode::AUTO`].
    pub fn auto() -> Self {
        Self::of(ToolChoiceMode::AUTO)
    }

    /// [`ToolChoiceMode::ANY`].
    pub fn any() -> Self {
        Self::of(ToolChoiceMode::ANY)
    }

    /// [`ToolChoiceMode::NONE`].
    pub fn none() -> Self {
        Self::of(ToolChoiceMode::NONE)
    }

    /// [`ToolChoiceMode::TOOL`]: call the tool defined as `name`.
    pub fn tool(name: impl Into<String>) -> Self {
        ToolChoice {
            mode: ToolChoiceMode::TOOL,
            name: name.into(),
        }
    }
}

/// Why `c` cannot go with `tools` (Go `CheckToolChoice`): a mode that is
/// not one of the [`ToolChoiceMode`] constants, no tools, a name missing
/// with [`ToolChoiceMode::TOOL`] or set with another mode, or naming no
/// tool in `tools`. `None` is valid. The error is
/// [`LlmError::Invalid`]; adapters prefix their scheme.
pub fn check_tool_choice(c: Option<&ToolChoice>, tools: &[ToolDef]) -> Result<(), LlmError> {
    let Some(c) = c else { return Ok(()) };
    let fail = |msg: String| Err(LlmError::Invalid(msg));
    if !c.mode.known() {
        return fail(format!("unknown tool_choice mode {:?}", c.mode.as_str()));
    }
    if tools.is_empty() {
        return fail(format!("tool_choice {:?} needs tools", c.mode.as_str()));
    }
    if c.mode != ToolChoiceMode::TOOL {
        if !c.name.is_empty() {
            return fail(format!(
                "tool_choice {:?} takes no tool name",
                c.mode.as_str()
            ));
        }
        return Ok(());
    }
    if c.name.is_empty() {
        return fail(format!(
            "tool_choice {:?} needs a tool name",
            c.mode.as_str()
        ));
    }
    if tools.iter().any(|t| t.name == c.name) {
        return Ok(());
    }
    fail(format!("tool_choice names unknown tool {:?}", c.name))
}

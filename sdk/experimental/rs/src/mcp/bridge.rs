//! The command tree the surface exposes, and the invocation path.
//!
//! Ports the slice of Go's `cmdsurface.Bridge` the MCP surface needs: a
//! flat list of [`Leaf`]s, per-surface enablement, the [`Policy`] gate,
//! and [`Bridge::invoke`]. The Go bridge additionally drives a real
//! cobra tree through a runner; here the leaf carries a [`Handler`]
//! closure, so the crate pulls in no CLI framework and adopters can
//! bridge clap, a hand-rolled tree, or a test double.

use std::collections::BTreeSet;

use serde_json::{Map, Value};

use super::safety::{Policy, SafetyClass, Surface};

/// One JSON Schema property on a leaf's `inputSchema`.
#[derive(Debug, Clone)]
pub struct FlagSchema {
    /// Flag name, used as the property key.
    pub name: String,
    /// JSON Schema primitive: `string`, `integer`, `number`, `boolean`,
    /// or `array`.
    pub json_type: String,
    /// Help text, emitted as `description`.
    pub description: String,
    /// Whether the flag is required.
    pub required: bool,
}

impl FlagSchema {
    /// A flag of the given JSON Schema type.
    pub fn new(
        name: impl Into<String>,
        json_type: impl Into<String>,
        description: impl Into<String>,
    ) -> Self {
        Self {
            name: name.into(),
            json_type: json_type.into(),
            description: description.into(),
            required: false,
        }
    }

    /// Marks the flag required.
    #[must_use]
    pub fn required(mut self) -> Self {
        self.required = true;
        self
    }

    /// Renders the property object. `array` flags additionally carry
    /// `items: {"type": "string"}`, mirroring Go's `flagProperty`.
    fn to_property(&self) -> Value {
        let mut prop = Map::new();
        prop.insert("type".into(), Value::String(self.json_type.clone()));
        prop.insert(
            "description".into(),
            Value::String(self.description.clone()),
        );
        if self.json_type == "array" {
            let mut items = Map::new();
            items.insert("type".into(), Value::String("string".into()));
            prop.insert("items".into(), Value::Object(items));
        }
        Value::Object(prop)
    }
}

/// The outcome of invoking a leaf.
#[derive(Debug, Clone, Default)]
pub struct CallResult {
    /// Captured stdout. Always rendered as the first content block,
    /// even when empty.
    pub stdout: String,
    /// Captured stderr. Rendered as a `[stderr] `-prefixed block when
    /// non-empty.
    pub stderr: String,
    /// Process exit code. Non-zero sets `isError`.
    pub exit_code: i32,
    /// Structured payload. Rendered as a JSON text block, and as
    /// `structuredContent` on the modern era.
    pub data: Option<Value>,
}

impl CallResult {
    /// A successful result carrying `stdout`.
    pub fn ok(stdout: impl Into<String>) -> Self {
        Self {
            stdout: stdout.into(),
            ..Self::default()
        }
    }
}

/// Why an invocation could not be performed.
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub enum InvokeError {
    /// No leaf matches the requested path.
    UnknownCommand,
    /// The leaf exists but is not exposed on this surface.
    SurfaceNotEnabled,
    /// The policy gate refused a destructive leaf on this surface.
    ///
    /// Renders as an `isError` result at HTTP 200 on both eras — never
    /// as a transport error (ADR 0043 §5).
    DestructiveBlocked {
        /// Space-joined leaf path, e.g. `widget delete`.
        command: String,
        /// The surface the call arrived on.
        surface: Surface,
    },
    /// The handler itself failed.
    Handler(String),
}

impl std::fmt::Display for InvokeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::UnknownCommand => f.write_str("cmdsurface: unknown command"),
            Self::SurfaceNotEnabled => f.write_str("cmdsurface: surface not enabled"),
            // Byte-pinned by the fixtures: legacy and modern
            // destructive-blocked cases both assert this exact text.
            Self::DestructiveBlocked { command, surface } => write!(
                f,
                "cmdsurface: destructive command blocked on this surface: \
                 {command} on {surface}"
            ),
            Self::Handler(msg) => f.write_str(msg),
        }
    }
}

impl std::error::Error for InvokeError {}

/// The function a leaf runs when invoked.
pub type Handler = Box<dyn Fn(&Map<String, Value>) -> Result<CallResult, String> + Send + Sync>;

/// One invocable command.
pub struct Leaf {
    /// Path segments, e.g. `["widget", "add"]`. The MCP tool name is
    /// these joined with `.`.
    pub path: Vec<String>,
    /// One-line description, emitted as the tool `description`.
    pub short: String,
    /// Flag schemas backing `inputSchema`.
    pub flags: Vec<FlagSchema>,
    /// Safety annotations driving the policy and pre-flight gates.
    pub class: SafetyClass,
    /// Surfaces this leaf is exposed on.
    pub enabled: BTreeSet<Surface>,
    /// The invocation body.
    pub handler: Handler,
}

impl std::fmt::Debug for Leaf {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Leaf")
            .field("path", &self.path)
            .field("short", &self.short)
            .field("flags", &self.flags)
            .field("class", &self.class)
            .field("enabled", &self.enabled)
            .finish_non_exhaustive()
    }
}

impl Leaf {
    /// A leaf at `path` described by `short`, running `handler`, exposed
    /// on the default surfaces (CLI + Lib + MCP).
    pub fn new<F>(path: &[&str], short: impl Into<String>, handler: F) -> Self
    where
        F: Fn(&Map<String, Value>) -> Result<CallResult, String> + Send + Sync + 'static,
    {
        Self {
            path: path.iter().map(|s| (*s).to_string()).collect(),
            short: short.into(),
            flags: Vec::new(),
            class: SafetyClass::default(),
            enabled: [Surface::Cli, Surface::Lib, Surface::Mcp]
                .into_iter()
                .collect(),
            handler: Box::new(handler),
        }
    }

    /// Attaches flag schemas.
    #[must_use]
    pub fn with_flags(mut self, flags: Vec<FlagSchema>) -> Self {
        self.flags = flags;
        self
    }

    /// Sets the safety class.
    #[must_use]
    pub fn with_class(mut self, class: SafetyClass) -> Self {
        self.class = class;
        self
    }

    /// Replaces the enabled-surface set.
    #[must_use]
    pub fn with_enabled(mut self, surfaces: impl IntoIterator<Item = Surface>) -> Self {
        self.enabled = surfaces.into_iter().collect();
        self
    }

    /// The dotted MCP tool name, e.g. `widget.add`.
    #[must_use]
    pub fn tool_name(&self) -> String {
        self.path.join(".")
    }

    /// Renders the MCP tool descriptor: `name`, `description`,
    /// `inputSchema`. Mirrors Go's `buildToolEnvelope`, and is shared by
    /// both eras so schema drift between them is impossible.
    #[must_use]
    pub fn tool_envelope(&self) -> Value {
        let mut properties = Map::new();
        let mut required: Vec<Value> = Vec::new();
        for flag in &self.flags {
            properties.insert(flag.name.clone(), flag.to_property());
            if flag.required {
                required.push(Value::String(flag.name.clone()));
            }
        }

        let mut schema = Map::new();
        schema.insert("type".into(), Value::String("object".into()));
        schema.insert("properties".into(), Value::Object(properties));
        // `required` is omitted entirely when empty (Go appends only
        // when len > 0), which the fixtures pin for flagless leaves.
        if !required.is_empty() {
            schema.insert("required".into(), Value::Array(required));
        }

        let mut tool = Map::new();
        tool.insert("name".into(), Value::String(self.tool_name()));
        tool.insert("description".into(), Value::String(self.short.clone()));
        tool.insert("inputSchema".into(), Value::Object(schema));
        Value::Object(tool)
    }
}

/// Holds the leaf set and the policy, and performs invocations.
#[derive(Debug, Default)]
pub struct Bridge {
    leaves: Vec<Leaf>,
    policy: Policy,
}

impl Bridge {
    /// An empty bridge carrying the conservative default policy.
    #[must_use]
    pub fn new() -> Self {
        Self {
            leaves: Vec::new(),
            policy: Policy::default_policy(),
        }
    }

    /// Replaces the policy.
    #[must_use]
    pub fn with_policy(mut self, policy: Policy) -> Self {
        self.policy = policy;
        self
    }

    /// Appends a leaf.
    ///
    /// Registration order does not decide enumeration order — see
    /// [`Bridge::leaves`], which is what `tools/list` walks.
    #[must_use]
    pub fn leaf(mut self, leaf: Leaf) -> Self {
        self.leaves.push(leaf);
        self.sort_leaves();
        self
    }

    /// The leaves in enumeration order: a depth-first walk over
    /// name-sorted siblings.
    ///
    /// This is the order `tools/list` emits, and it is fixture-pinned.
    /// Go derives it from cobra, whose `Commands()` sorts siblings by
    /// name (`EnableCommandSorting` defaults on) and whose discovery
    /// walk is depth-first — so a tree declaring `widget`, `secret`,
    /// `deploy`, `ping` enumerates as `deploy`, `ping`, `secret`,
    /// `widget.add`, `widget.delete`. Sorting the full dotted path
    /// would *not* reproduce it in general, because a parent's name
    /// orders its whole subtree ahead of any later sibling.
    #[must_use]
    pub fn leaves(&self) -> &[Leaf] {
        &self.leaves
    }

    /// Re-sorts into depth-first, name-sorted-sibling order.
    ///
    /// Comparing path segment-by-segment achieves exactly that: at the
    /// first differing segment the shorter-or-lesser name wins, which
    /// keeps every leaf of one subtree contiguous and ahead of the next
    /// sibling's subtree.
    fn sort_leaves(&mut self) {
        self.leaves.sort_by(|a, b| a.path.cmp(&b.path));
    }

    /// The active policy.
    #[must_use]
    pub fn policy(&self) -> &Policy {
        &self.policy
    }

    /// Finds a leaf by dotted tool name, regardless of enablement.
    #[must_use]
    pub fn resolve(&self, tool_name: &str) -> Option<&Leaf> {
        self.leaves.iter().find(|l| l.tool_name() == tool_name)
    }

    /// Finds a leaf that is exposed on `surface`.
    #[must_use]
    pub fn resolve_enabled(&self, tool_name: &str, surface: Surface) -> Option<&Leaf> {
        self.resolve(tool_name)
            .filter(|l| l.enabled.contains(&surface))
    }

    /// Invokes a leaf on `surface` with `arguments`.
    ///
    /// The policy gate runs here, so both era handlers inherit identical
    /// destructive-ceiling behavior: there is no path that reaches a
    /// leaf on one era which the other would have blocked.
    pub fn invoke(
        &self,
        tool_name: &str,
        surface: Surface,
        arguments: &Map<String, Value>,
    ) -> Result<CallResult, InvokeError> {
        let leaf = self.resolve(tool_name).ok_or(InvokeError::UnknownCommand)?;
        if !leaf.enabled.contains(&surface) {
            return Err(InvokeError::SurfaceNotEnabled);
        }
        if !self.policy.allowed(&leaf.class, surface) {
            return Err(InvokeError::DestructiveBlocked {
                command: leaf.path.join(" "),
                surface,
            });
        }
        (leaf.handler)(arguments).map_err(InvokeError::Handler)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn destructive_leaf() -> Leaf {
        Leaf::new(&["widget", "delete"], "Delete a widget", |_| {
            Ok(CallResult::ok("deleted\n"))
        })
        .with_class(SafetyClass {
            destructive: true,
            ..SafetyClass::default()
        })
    }

    #[test]
    fn destructive_leaf_is_blocked_on_mcp_by_default() {
        let bridge = Bridge::new().leaf(destructive_leaf());
        let err = bridge
            .invoke("widget.delete", Surface::Mcp, &Map::new())
            .unwrap_err();
        assert_eq!(
            err.to_string(),
            "cmdsurface: destructive command blocked on this surface: \
             widget delete on mcp"
        );
    }

    #[test]
    fn destructive_leaf_runs_on_local_surfaces() {
        let bridge = Bridge::new().leaf(destructive_leaf());
        let res = bridge
            .invoke("widget.delete", Surface::Cli, &Map::new())
            .unwrap();
        assert_eq!(res.stdout, "deleted\n");
    }

    #[test]
    fn unknown_and_disabled_leaves_are_distinguished() {
        let bridge = Bridge::new().leaf(
            Leaf::new(&["ping"], "Ping", |_| Ok(CallResult::ok("pong\n")))
                .with_enabled([Surface::Cli]),
        );
        assert_eq!(
            bridge.invoke("nope", Surface::Mcp, &Map::new()).unwrap_err(),
            InvokeError::UnknownCommand
        );
        assert_eq!(
            bridge.invoke("ping", Surface::Mcp, &Map::new()).unwrap_err(),
            InvokeError::SurfaceNotEnabled
        );
    }

    #[test]
    fn tool_envelope_omits_required_when_no_flag_is_required() {
        let leaf = Leaf::new(&["ping"], "Ping the server", |_| Ok(CallResult::default()));
        assert_eq!(
            leaf.tool_envelope(),
            json!({
                "name": "ping",
                "description": "Ping the server",
                "inputSchema": {"type": "object", "properties": {}}
            })
        );
    }

    #[test]
    fn tool_envelope_renders_array_items_and_required_list() {
        let leaf = Leaf::new(&["widget", "add"], "Add a widget", |_| {
            Ok(CallResult::default())
        })
        .with_flags(vec![
            FlagSchema::new("name", "string", "widget name").required(),
            FlagSchema::new("tag", "array", "tag list"),
        ]);
        assert_eq!(
            leaf.tool_envelope(),
            json!({
                "name": "widget.add",
                "description": "Add a widget",
                "inputSchema": {
                    "type": "object",
                    "properties": {
                        "name": {"type": "string", "description": "widget name"},
                        "tag": {
                            "type": "array",
                            "description": "tag list",
                            "items": {"type": "string"}
                        }
                    },
                    "required": ["name"]
                }
            })
        );
    }

    #[test]
    fn leaves_enumerate_depth_first_over_name_sorted_siblings() {
        // Registered in a deliberately scrambled order; enumeration
        // must match cobra's sorted depth-first walk.
        let bridge = Bridge::new()
            .leaf(Leaf::new(&["widget", "delete"], "d", |_| {
                Ok(CallResult::default())
            }))
            .leaf(Leaf::new(&["secret"], "s", |_| Ok(CallResult::default())))
            .leaf(Leaf::new(&["widget", "add"], "a", |_| {
                Ok(CallResult::default())
            }))
            .leaf(Leaf::new(&["deploy"], "d", |_| Ok(CallResult::default())))
            .leaf(Leaf::new(&["ping"], "p", |_| Ok(CallResult::default())));

        let names: Vec<String> = bridge.leaves().iter().map(Leaf::tool_name).collect();
        assert_eq!(
            names,
            ["deploy", "ping", "secret", "widget.add", "widget.delete"]
        );
    }

    #[test]
    fn subtree_stays_contiguous_ahead_of_a_later_sibling() {
        // The case that distinguishes a segment-wise path comparison
        // from sorting the dotted tool names as flat strings: '.' (0x2E)
        // sorts before every alphanumeric, so "widget.add" would come
        // before "widgets" as a flat string too — but "widget" vs
        // "widgetz" as SEGMENTS is what actually decides, and a child of
        // "widget" must stay ahead of the sibling "widgetz" regardless
        // of the child's own name.
        let bridge = Bridge::new()
            .leaf(Leaf::new(&["widgetz"], "z", |_| Ok(CallResult::default())))
            .leaf(Leaf::new(&["widget", "zzz"], "z", |_| {
                Ok(CallResult::default())
            }));
        let names: Vec<String> = bridge.leaves().iter().map(Leaf::tool_name).collect();
        assert_eq!(
            names,
            ["widget.zzz", "widgetz"],
            "the whole 'widget' subtree precedes the sibling 'widgetz'"
        );
    }

    #[test]
    fn handler_arguments_reach_the_leaf() {
        let bridge = Bridge::new().leaf(Leaf::new(&["echo"], "Echo", |args| {
            Ok(CallResult::ok(
                args.get("msg").and_then(Value::as_str).unwrap_or(""),
            ))
        }));
        let mut args = Map::new();
        args.insert("msg".into(), json!("hi"));
        let res = bridge.invoke("echo", Surface::Mcp, &args).unwrap();
        assert_eq!(res.stdout, "hi");
    }
}

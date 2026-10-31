//! A kit-owned OpenAPI 3.1 emitter. The documents it writes are
//! `serde_json::Value`s, byte-for-byte what `contracts/parity/api.json`
//! pins from Go's huma for the same model: the contract is the plain
//! document, with none of huma's typed-handler `$schema` links.
//!
//! Schemas are JSON Schema values the caller supplies (hand-written, or
//! from whatever derives them); they are emitted verbatim. No schema
//! crate appears in kit's API.

use std::collections::BTreeMap;

use serde_json::{json, Map, Value};

/// The OpenAPI version emitted.
pub const OPENAPI_VERSION: &str = "3.1.0";
/// Where the document is served.
pub const OPENAPI_PATH: &str = "/openapi.json";

/// A server the document names.
#[derive(Debug, Clone, Default)]
pub struct Server {
    /// The base URL.
    pub url: String,
    /// What it is.
    pub description: String,
}

/// A parameter of an operation.
#[derive(Debug, Clone, Default)]
pub struct Param {
    /// The parameter name.
    pub name: String,
    /// `query`, `path`, `header` or `cookie`.
    pub location: String,
    /// Whether it must be present (a path parameter always is).
    pub required: bool,
    /// What it means.
    pub description: String,
    /// Its JSON Schema.
    pub schema: Option<Value>,
}

/// A JSON request body.
#[derive(Debug, Clone, Default)]
pub struct RequestBody {
    /// What it is.
    pub description: String,
    /// Whether the request must carry one.
    pub required: bool,
    /// Its JSON Schema (`application/json`).
    pub schema: Option<Value>,
}

/// One response of an operation.
#[derive(Debug, Clone, Default)]
pub struct Response {
    /// What the response means.
    pub description: String,
    /// The `application/json` body schema, when it has a body.
    pub schema: Option<Value>,
}

/// One operation: a method on a path.
#[derive(Debug, Clone, Default)]
pub struct Operation {
    /// The unique operation id.
    pub operation_id: String,
    /// The HTTP method, upper-case.
    pub method: String,
    /// The path, with `{name}` segments.
    pub path: String,
    /// One line.
    pub summary: String,
    /// Longer text.
    pub description: String,
    /// Grouping tags.
    pub tags: Vec<String>,
    /// The parameters.
    pub parameters: Vec<Param>,
    /// The request body, if any.
    pub request_body: Option<RequestBody>,
    /// The responses by status code.
    pub responses: BTreeMap<String, Response>,
    /// `x-` extension members on the operation.
    pub extensions: BTreeMap<String, Value>,
}

/// An OpenAPI document under construction.
#[derive(Debug, Clone, Default)]
pub struct OpenApi {
    /// `info.title`.
    pub title: String,
    /// `info.version`.
    pub version: String,
    /// `info.description`.
    pub description: String,
    /// `servers`.
    pub servers: Vec<Server>,
    /// `components.schemas`.
    pub schemas: BTreeMap<String, Value>,
    /// The operations, in registration order.
    pub operations: Vec<Operation>,
}

fn insert_nonempty(m: &mut Map<String, Value>, k: &str, v: &str) {
    if !v.is_empty() {
        m.insert(k.into(), json!(v));
    }
}

fn media(schema: &Option<Value>) -> Value {
    match schema {
        Some(s) => json!({"application/json": {"schema": s}}),
        None => json!({"application/json": {}}),
    }
}

impl Operation {
    fn to_value(&self) -> Value {
        let mut m = Map::new();
        m.insert("operationId".into(), json!(self.operation_id));
        insert_nonempty(&mut m, "summary", &self.summary);
        insert_nonempty(&mut m, "description", &self.description);
        if !self.tags.is_empty() {
            m.insert("tags".into(), json!(self.tags));
        }
        if !self.parameters.is_empty() {
            let params: Vec<Value> = self
                .parameters
                .iter()
                .map(|p| {
                    let mut o = Map::new();
                    o.insert("name".into(), json!(p.name));
                    o.insert("in".into(), json!(p.location));
                    if p.required {
                        o.insert("required".into(), json!(true));
                    }
                    insert_nonempty(&mut o, "description", &p.description);
                    if let Some(s) = &p.schema {
                        o.insert("schema".into(), s.clone());
                    }
                    Value::Object(o)
                })
                .collect();
            m.insert("parameters".into(), Value::Array(params));
        }
        if let Some(b) = &self.request_body {
            let mut o = Map::new();
            insert_nonempty(&mut o, "description", &b.description);
            if b.required {
                o.insert("required".into(), json!(true));
            }
            o.insert("content".into(), media(&b.schema));
            m.insert("requestBody".into(), Value::Object(o));
        }
        let mut responses = Map::new();
        for (code, r) in &self.responses {
            let mut o = Map::new();
            o.insert("description".into(), json!(r.description));
            if r.schema.is_some() {
                o.insert("content".into(), media(&r.schema));
            }
            responses.insert(code.clone(), Value::Object(o));
        }
        m.insert("responses".into(), Value::Object(responses));
        for (k, v) in &self.extensions {
            m.insert(k.clone(), v.clone());
        }
        Value::Object(m)
    }
}

impl OpenApi {
    /// A document titled `title` at `version`.
    pub fn new(title: impl Into<String>, version: impl Into<String>) -> Self {
        Self {
            title: title.into(),
            version: version.into(),
            ..Self::default()
        }
    }

    /// Adds an operation.
    pub fn operation(&mut self, op: Operation) -> &mut Self {
        self.operations.push(op);
        self
    }

    /// Adds a named component schema.
    pub fn schema(&mut self, name: impl Into<String>, schema: Value) -> &mut Self {
        self.schemas.insert(name.into(), schema);
        self
    }

    /// The five CRUD operations of a resource mounted at `prefix`
    /// (`/widgets`), as Go's `ResourceRouter` describes them: list,
    /// create, get, update and delete, with the shared `limit`,
    /// `offset`, `sort` and `search` query parameters on list. The
    /// entity schema is registered as `schema_name` and referenced.
    pub fn crud(
        &mut self,
        prefix: &str,
        tag: &str,
        schema_name: &str,
        entity: Value,
        routes: &[&str],
    ) -> &mut Self {
        let base = format!("{}/", prefix.trim_end_matches('/'));
        let id = |id: &str| id.to_string();
        let op_id = |action: &str| {
            let clean = base.trim_matches('/').replace('/', "-");
            if clean.is_empty() {
                action.to_string()
            } else {
                format!("{clean}-{action}")
            }
        };
        let entity_ref = json!({"$ref": format!("#/components/schemas/{schema_name}")});
        let on = |name: &str| routes.is_empty() || routes.contains(&name);
        self.schemas.insert(schema_name.to_string(), entity);
        let ok = |code: &str, desc: &str, schema: Option<Value>| {
            (
                code.to_string(),
                Response {
                    description: desc.into(),
                    schema,
                },
            )
        };
        let id_param = Param {
            name: id("id"),
            location: "path".into(),
            required: true,
            schema: Some(json!({"type": "string"})),
            ..Param::default()
        };
        let tags = vec![tag.to_string()];
        if on("create") {
            self.operations.push(Operation {
                operation_id: op_id("create"),
                method: "POST".into(),
                path: base.clone(),
                summary: "Create resource".into(),
                tags: tags.clone(),
                request_body: Some(RequestBody {
                    required: true,
                    schema: Some(entity_ref.clone()),
                    ..RequestBody::default()
                }),
                responses: [ok("201", "Created", Some(entity_ref.clone()))].into(),
                ..Operation::default()
            });
        }
        if on("list") {
            let q = |name: &str, desc: &str, schema: Value| Param {
                name: name.into(),
                location: "query".into(),
                description: desc.into(),
                schema: Some(schema),
                ..Param::default()
            };
            self.operations.push(Operation {
                operation_id: op_id("list"),
                method: "GET".into(),
                path: base.clone(),
                summary: "List resources".into(),
                tags: tags.clone(),
                parameters: vec![
                    q(
                        "limit",
                        "Max results",
                        json!({"type": "integer", "default": 20}),
                    ),
                    q(
                        "offset",
                        "Skip N results",
                        json!({"type": "integer", "default": 0}),
                    ),
                    q("sort", "Sort field", json!({"type": "string"})),
                    q("search", "Search filter", json!({"type": "string"})),
                ],
                responses: [ok(
                    "200",
                    "OK",
                    Some(json!({"type": "array", "items": entity_ref})),
                )]
                .into(),
                ..Operation::default()
            });
        }
        if on("get") {
            self.operations.push(Operation {
                operation_id: op_id("get"),
                method: "GET".into(),
                path: format!("{base}{{id}}"),
                summary: "Get resource by ID".into(),
                tags: tags.clone(),
                parameters: vec![id_param.clone()],
                responses: [ok("200", "OK", Some(entity_ref.clone()))].into(),
                ..Operation::default()
            });
        }
        if on("update") {
            self.operations.push(Operation {
                operation_id: op_id("update"),
                method: "PUT".into(),
                path: format!("{base}{{id}}"),
                summary: "Update resource".into(),
                tags: tags.clone(),
                parameters: vec![id_param.clone()],
                request_body: Some(RequestBody {
                    required: true,
                    schema: Some(entity_ref.clone()),
                    ..RequestBody::default()
                }),
                responses: [ok("200", "OK", Some(entity_ref.clone()))].into(),
                ..Operation::default()
            });
        }
        if on("delete") {
            self.operations.push(Operation {
                operation_id: op_id("delete"),
                method: "DELETE".into(),
                path: format!("{base}{{id}}"),
                summary: "Delete resource".into(),
                tags,
                parameters: vec![id_param],
                responses: [ok("204", "No Content", None)].into(),
                ..Operation::default()
            });
        }
        self
    }

    /// The document.
    pub fn to_value(&self) -> Value {
        let mut info = Map::new();
        info.insert("title".into(), json!(self.title));
        info.insert("version".into(), json!(self.version));
        insert_nonempty(&mut info, "description", &self.description);
        let mut doc = Map::new();
        doc.insert("openapi".into(), json!(OPENAPI_VERSION));
        doc.insert("info".into(), Value::Object(info));
        if !self.servers.is_empty() {
            let servers: Vec<Value> = self
                .servers
                .iter()
                .map(|s| {
                    let mut o = Map::new();
                    o.insert("url".into(), json!(s.url));
                    insert_nonempty(&mut o, "description", &s.description);
                    Value::Object(o)
                })
                .collect();
            doc.insert("servers".into(), Value::Array(servers));
        }
        let schemas: Map<String, Value> = self.schemas.clone().into_iter().collect();
        doc.insert("components".into(), json!({"schemas": schemas}));
        if !self.operations.is_empty() {
            let mut paths: BTreeMap<&str, Map<String, Value>> = BTreeMap::new();
            for op in &self.operations {
                paths
                    .entry(op.path.as_str())
                    .or_default()
                    .insert(op.method.to_lowercase(), op.to_value());
            }
            let paths: Map<String, Value> = paths
                .into_iter()
                .map(|(p, ops)| (p.to_string(), Value::Object(ops)))
                .collect();
            doc.insert("paths".into(), Value::Object(paths));
        }
        Value::Object(doc)
    }
}

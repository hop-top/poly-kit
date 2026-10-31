//! The static checks of factors 1 to 12 (Go `static.go`).

use super::spec::{Command, ToolSpec};
use super::{CheckResult, Factor, Status};

pub(super) fn run(spec: &ToolSpec) -> Vec<CheckResult> {
    vec![
        self_describing(spec),
        structured_io(spec),
        skip(Factor::StreamDiscipline, "runtime check only"),
        contracts_errors(spec),
        preview(spec),
        idempotency(spec),
        state_transparency(spec),
        safe_delegation(spec),
        skip(Factor::ObservableOps, "runtime check only"),
        skip(Factor::Provenance, "runtime check only"),
        evolution(spec),
        auth_lifecycle(spec),
    ]
}

pub(super) fn pass(f: Factor, details: impl Into<String>) -> CheckResult {
    CheckResult::new(f, Status::Pass, details, "")
}

pub(super) fn fail(f: Factor, details: impl Into<String>, suggestion: &str) -> CheckResult {
    CheckResult::new(f, Status::Fail, details, suggestion)
}

pub(super) fn skip(f: Factor, details: impl Into<String>) -> CheckResult {
    CheckResult::new(f, Status::Skip, details, "")
}

/// The command tree flattened, parents first.
fn all(cmds: &[Command]) -> Vec<&Command> {
    let mut out = Vec::new();
    for c in cmds {
        out.push(c);
        out.extend(all(&c.children));
    }
    out
}

/// Commands that declare side effects.
fn mutating(cmds: &[Command]) -> Vec<&Command> {
    all(cmds)
        .into_iter()
        .filter(|c| {
            c.contract
                .as_ref()
                .is_some_and(|k| !k.side_effects.is_empty())
        })
        .collect()
}

fn dangerous(cmds: &[Command]) -> Vec<&Command> {
    all(cmds)
        .into_iter()
        .filter(|c| c.safety.as_ref().is_some_and(|s| s.level == "dangerous"))
        .collect()
}

fn self_describing(spec: &ToolSpec) -> CheckResult {
    let f = Factor::SelfDescribing;
    if spec.commands.is_empty() {
        return fail(
            f,
            "no commands defined",
            "Add a commands array with at least one named command",
        );
    }
    if spec.commands.iter().any(|c| c.name.is_empty()) {
        return fail(
            f,
            "command missing name",
            "Every command must have a name field",
        );
    }
    pass(f, "commands array non-empty, all named")
}

fn structured_io(spec: &ToolSpec) -> CheckResult {
    let f = Factor::StructuredIo;
    match all(&spec.commands)
        .into_iter()
        .find(|c| c.output_schema.is_some())
    {
        Some(c) => pass(f, format!("output_schema found on {}", c.name)),
        None => fail(
            f,
            "no command has output_schema",
            "Add output_schema to at least one command",
        ),
    }
}

fn contracts_errors(spec: &ToolSpec) -> CheckResult {
    let f = Factor::ContractsErrors;
    let mutating = mutating(&spec.commands);
    if mutating.is_empty() {
        if all(&spec.commands).iter().any(|c| c.contract.is_some()) {
            return pass(f, "contracts found");
        }
        return fail(
            f,
            "no contracts declared",
            "Add contract fields (idempotent, side_effects) to commands",
        );
    }
    // A command is only mutating by its contract, so every one has it.
    pass(f, "all mutating commands have contracts")
}

fn preview(spec: &ToolSpec) -> CheckResult {
    let f = Factor::Preview;
    let mutating = mutating(&spec.commands);
    if mutating.is_empty() {
        return pass(f, "no mutating commands to preview");
    }
    let with = mutating
        .iter()
        .filter(|c| !c.preview_modes.is_empty())
        .count();
    if with == 0 {
        return fail(
            f,
            "no mutating command has preview_modes",
            "Add preview_modes (e.g. --dry-run) to mutating commands",
        );
    }
    if with < mutating.len() {
        return pass(
            f,
            format!(
                "{with}/{} mutating commands have preview_modes",
                mutating.len()
            ),
        );
    }
    pass(f, "all mutating commands have preview_modes")
}

fn idempotency(spec: &ToolSpec) -> CheckResult {
    let f = Factor::Idempotency;
    let cmds = all(&spec.commands);
    if cmds.is_empty() {
        return fail(f, "no commands", "Add commands");
    }
    let declared = cmds
        .iter()
        .any(|c| c.contract.as_ref().is_some_and(|k| k.idempotent.is_some()));
    if !declared {
        return fail(
            f,
            "no command declares idempotent",
            "Add contract.idempotent to each command",
        );
    }
    pass(f, "idempotency declared on commands")
}

fn state_transparency(spec: &ToolSpec) -> CheckResult {
    let f = Factor::StateTransparency;
    match &spec.state_introspection {
        Some(s) if !s.config_commands.is_empty() => pass(f, "config_commands present"),
        _ => fail(
            f,
            "no config_commands in state_introspection",
            "Add state_introspection.config_commands",
        ),
    }
}

fn safe_delegation(spec: &ToolSpec) -> CheckResult {
    let f = Factor::SafeDelegation;
    if dangerous(&spec.commands).is_empty() {
        return pass(f, "no dangerous commands");
    }
    // A command is only dangerous by its safety block, so every one has it.
    pass(f, "all dangerous commands have safety metadata")
}

fn evolution(spec: &ToolSpec) -> CheckResult {
    let f = Factor::Evolution;
    if spec.schema_version.is_empty() {
        return fail(
            f,
            "schema_version not set",
            "Set schema_version in the toolspec",
        );
    }
    pass(f, format!("schema_version: {}", spec.schema_version))
}

fn auth_lifecycle(spec: &ToolSpec) -> CheckResult {
    let f = Factor::AuthLifecycle;
    match &spec.state_introspection {
        Some(s) if !s.auth_commands.is_empty() => pass(f, "auth_commands present"),
        _ => skip(f, "no auth_commands — skipped (tool may not need auth)"),
    }
}

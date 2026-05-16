"""Structured knowledge about CLI tools — Python port of Go toolspec."""

from __future__ import annotations

from dataclasses import dataclass, field

import yaml

# --- Types ---


@dataclass
class Contract:
    idempotent: bool = False
    side_effects: list[str] = field(default_factory=list)
    retryable: bool = False
    pre_conditions: list[str] = field(default_factory=list)


@dataclass
class Safety:
    level: str = "safe"
    requires_confirmation: bool = False
    permissions: list[str] = field(default_factory=list)


@dataclass
class OutputSchema:
    format: str = ""
    fields: list[str] = field(default_factory=list)
    example: str = ""


@dataclass
class StateIntrospection:
    config_commands: list[str] = field(default_factory=list)
    env_vars: list[str] = field(default_factory=list)
    auth_commands: list[str] = field(default_factory=list)


@dataclass
class Provenance:
    source: str = ""
    retrieved_at: str = ""
    confidence: float = 0.0


@dataclass
class Intent:
    domain: str = ""
    category: str = ""
    tags: list[str] = field(default_factory=list)


@dataclass
class Flag:
    name: str = ""
    short: str = ""
    type: str = ""
    description: str = ""
    deprecated: bool = False
    replaced_by: str = ""


@dataclass
class Command:
    name: str = ""
    aliases: list[str] = field(default_factory=list)
    flags: list[Flag] | None = None
    children: list[Command] | None = None
    contract: Contract | None = None
    safety: Safety | None = None
    preview_modes: list[str] | None = None
    output_schema: OutputSchema | None = None
    deprecated: bool = False
    deprecated_since: str = ""
    replaced_by: str = ""
    intent: Intent | None = None
    suggested_next: list[str] | None = None


@dataclass
class ErrorPattern:
    pattern: str = ""
    fix: str = ""
    source: str = ""
    cause: str = ""
    fixes: list[str] = field(default_factory=list)
    confidence: float = 0.0
    provenance: Provenance | None = None


@dataclass
class Workflow:
    name: str = ""
    steps: list[str] = field(default_factory=list)
    after: dict[str, list[str]] | None = None
    provenance: Provenance | None = None


@dataclass
class ToolSpec:
    name: str = ""
    schema_version: str = ""
    commands: list[Command] = field(default_factory=list)
    flags: list[Flag] | None = None
    error_patterns: list[ErrorPattern] | None = None
    workflows: list[Workflow] | None = None
    state_introspection: StateIntrospection | None = None


# --- YAML mapping helpers ---


def _map_flag(raw: dict) -> Flag:
    return Flag(
        name=raw.get("name", ""),
        short=raw.get("short", ""),
        type=raw.get("type", ""),
        description=raw.get("description", ""),
        deprecated=raw.get("deprecated", False),
        replaced_by=raw.get("replaced_by", ""),
    )


def _map_provenance(raw: dict | None) -> Provenance | None:
    if not raw:
        return None
    return Provenance(
        source=raw.get("source", ""),
        retrieved_at=raw.get("retrieved_at", ""),
        confidence=raw.get("confidence", 0.0),
    )


def _map_contract(raw: dict | None) -> Contract | None:
    if not raw:
        return None
    return Contract(
        idempotent=raw.get("idempotent", False),
        side_effects=raw.get("side_effects", []),
        retryable=raw.get("retryable", False),
        pre_conditions=raw.get("pre_conditions", []),
    )


def _map_safety(raw: dict | None) -> Safety | None:
    if not raw:
        return None
    return Safety(
        level=raw.get("level", "safe"),
        requires_confirmation=raw.get("requires_confirmation", False),
        permissions=raw.get("permissions", []),
    )


def _map_output_schema(raw: dict | None) -> OutputSchema | None:
    if not raw:
        return None
    return OutputSchema(
        format=raw.get("format", ""),
        fields=raw.get("fields", []),
        example=raw.get("example", ""),
    )


def _map_intent(raw: dict | None) -> Intent | None:
    if not raw:
        return None
    return Intent(
        domain=raw.get("domain", ""),
        category=raw.get("category", ""),
        tags=raw.get("tags", []),
    )


def _map_command(raw: dict) -> Command:
    flags_raw = raw.get("flags")
    children_raw = raw.get("children")
    return Command(
        name=raw.get("name", ""),
        aliases=raw.get("aliases", []),
        flags=([_map_flag(f) for f in flags_raw] if flags_raw else None),
        children=([_map_command(c) for c in children_raw] if children_raw else None),
        contract=_map_contract(raw.get("contract")),
        safety=_map_safety(raw.get("safety")),
        preview_modes=raw.get("preview_modes"),
        output_schema=_map_output_schema(raw.get("output_schema")),
        deprecated=raw.get("deprecated", False),
        deprecated_since=raw.get("deprecated_since", ""),
        replaced_by=raw.get("replaced_by", ""),
        intent=_map_intent(raw.get("intent")),
        suggested_next=raw.get("suggested_next"),
    )


def _map_state_introspection(
    raw: dict | None,
) -> StateIntrospection | None:
    if not raw:
        return None
    return StateIntrospection(
        config_commands=raw.get("config_commands", []),
        env_vars=raw.get("env_vars", []),
        auth_commands=raw.get("auth_commands", []),
    )


def _map_error_pattern(raw: dict) -> ErrorPattern:
    return ErrorPattern(
        pattern=raw.get("pattern", ""),
        fix=raw.get("fix", ""),
        source=raw.get("source", ""),
        cause=raw.get("cause", ""),
        fixes=raw.get("fixes", []),
        confidence=raw.get("confidence", 0.0),
        provenance=_map_provenance(raw.get("provenance")),
    )


def _map_workflow(raw: dict) -> Workflow:
    return Workflow(
        name=raw.get("name", ""),
        steps=raw.get("steps", []),
        after=raw.get("after"),
        provenance=_map_provenance(raw.get("provenance")),
    )


# --- Public API ---


def load_toolspec(path: str) -> ToolSpec:
    """Parse a YAML toolspec file into a ToolSpec object."""
    with open(path) as f:
        raw = yaml.safe_load(f)

    commands_raw = raw.get("commands", [])
    error_patterns_raw = raw.get("error_patterns")
    workflows_raw = raw.get("workflows")
    flags_raw = raw.get("flags")

    return ToolSpec(
        name=raw.get("name", ""),
        schema_version=str(raw.get("schema_version", "")),
        commands=[_map_command(c) for c in commands_raw],
        flags=([_map_flag(f) for f in flags_raw] if flags_raw else None),
        error_patterns=(
            [_map_error_pattern(e) for e in error_patterns_raw] if error_patterns_raw else None
        ),
        workflows=([_map_workflow(w) for w in workflows_raw] if workflows_raw else None),
        state_introspection=_map_state_introspection(raw.get("state_introspection")),
    )


_VALID_SAFETY_LEVELS = {"safe", "caution", "dangerous"}


def validate_toolspec(spec: ToolSpec) -> list[str]:
    """Validate a ToolSpec, returning a list of errors."""
    errors: list[str] = []

    if not spec.name:
        errors.append("name is required")
    if not spec.schema_version:
        errors.append("schema_version is required")
    if not spec.commands:
        errors.append("at least one command is required")

    def _validate_cmd(cmd: Command, path: str) -> None:
        if not cmd.name:
            errors.append(f"{path}: command name is required")
        if cmd.safety and cmd.safety.level not in _VALID_SAFETY_LEVELS:
            errors.append(
                f"{path}: invalid safety level "
                f'"{cmd.safety.level}" '
                f"(must be safe|caution|dangerous)"
            )
        if cmd.children:
            for child in cmd.children:
                _validate_cmd(
                    child,
                    f"{path}/{child.name or '<unnamed>'}",
                )

    for cmd in spec.commands or []:
        _validate_cmd(cmd, cmd.name or "<unnamed>")

    return errors


def find_command(spec: ToolSpec, name: str) -> Command | None:
    """BFS search for a command by name (mirrors Go)."""
    queue = list(spec.commands)
    while queue:
        c = queue.pop(0)
        if c.name == name:
            return c
        if c.children:
            queue.extend(c.children)
    return None

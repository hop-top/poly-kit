"""Tests for toolspec port — mirrors Go compliance + spec tests."""

from __future__ import annotations

from pathlib import Path

from hop_top_kit.toolspec import (
    Arg,
    Command,
    Contract,
    ErrorPattern,
    Flag,
    Intent,
    OutputSchema,
    Provenance,
    Safety,
    StateIntrospection,
    ToolSpec,
    Workflow,
    find_command,
    load_toolspec,
    validate_toolspec,
)

EXAMPLE_PATH = str(
    Path(__file__).resolve().parents[3] / "examples" / "spaced" / "spaced.toolspec.yaml"
)


class TestLoadToolspec:
    def test_parses_name_and_version(self):
        spec = load_toolspec(EXAMPLE_PATH)
        assert spec.name == "spaced"
        assert spec.schema_version == "1"

    def test_state_introspection(self):
        spec = load_toolspec(EXAMPLE_PATH)
        si = spec.state_introspection
        assert si is not None
        assert si.config_commands == ["spaced config show"]
        assert si.env_vars == [
            "SPACED_FORMAT",
            "SPACED_LOG_FORMAT",
            "SPACED_LOG_LEVEL",
        ]

    def test_commands_loaded(self):
        spec = load_toolspec(EXAMPLE_PATH)
        assert len(spec.commands) >= 3

    def test_launch_intent(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.intent is not None
        assert launch.intent.domain == "space"
        assert launch.intent.category == "operations"
        assert launch.intent.tags == ["launch", "mission"]

    def test_launch_contract(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.contract is not None
        assert launch.contract.idempotent is False
        assert launch.contract.retryable is False
        assert len(launch.contract.side_effects) == 2

    def test_launch_safety(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.safety is not None
        assert launch.safety.level == "dangerous"
        assert launch.safety.requires_confirmation is False

    def test_launch_preview_modes(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.preview_modes == ["--dry-run"]

    def test_launch_output_schema(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.output_schema is not None
        assert launch.output_schema.format == "text"
        assert "mission" in launch.output_schema.fields

    def test_launch_flags(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.flags is not None
        payload = next((f for f in launch.flags if f.name == "--payload"), None)
        assert payload is not None
        assert payload.type == "string[]"

    def test_launch_suggested_next(self):
        spec = load_toolspec(EXAMPLE_PATH)
        launch = _find(spec.commands, "launch")
        assert launch is not None
        assert launch.suggested_next == ["mission list", "telemetry"]

    def test_nested_children(self):
        spec = load_toolspec(EXAMPLE_PATH)
        mission = _find(spec.commands, "mission")
        assert mission is not None
        assert mission.children is not None
        lst = _find(mission.children, "list")
        assert lst is not None
        assert lst.contract is not None
        assert lst.contract.idempotent is True
        assert lst.safety is not None
        assert lst.safety.level == "safe"


class TestValidateToolspec:
    def test_valid_spec_no_errors(self):
        spec = load_toolspec(EXAMPLE_PATH)
        assert validate_toolspec(spec) == []

    def test_missing_name(self):
        spec = ToolSpec(name="", schema_version="1", commands=[])
        errs = validate_toolspec(spec)
        assert "name is required" in errs

    def test_missing_schema_version(self):
        spec = ToolSpec(name="test", schema_version="", commands=[])
        errs = validate_toolspec(spec)
        assert "schema_version is required" in errs

    def test_empty_commands(self):
        spec = ToolSpec(name="test", schema_version="1", commands=[])
        errs = validate_toolspec(spec)
        assert "at least one command is required" in errs

    def test_missing_command_name(self):
        spec = ToolSpec(
            name="test",
            schema_version="1",
            commands=[Command(name="")],
        )
        errs = validate_toolspec(spec)
        assert any("command name" in e for e in errs)

    def test_invalid_safety_level(self):
        spec = ToolSpec(
            name="test",
            schema_version="1",
            commands=[
                Command(
                    name="foo",
                    safety=Safety(
                        level="yolo",
                        requires_confirmation=False,
                    ),
                )
            ],
        )
        errs = validate_toolspec(spec)
        assert any("safety level" in e for e in errs)


class TestFindCommand:
    def test_top_level(self):
        spec = load_toolspec(EXAMPLE_PATH)
        cmd = find_command(spec, "launch")
        assert cmd is not None
        assert cmd.name == "launch"

    def test_nested(self):
        spec = load_toolspec(EXAMPLE_PATH)
        cmd = find_command(spec, "list")
        assert cmd is not None
        assert cmd.name == "list"

    def test_not_found(self):
        spec = load_toolspec(EXAMPLE_PATH)
        assert find_command(spec, "nonexistent") is None


def _find(commands: list[Command], name: str) -> Command | None:
    return next((c for c in commands if c.name == name), None)


# A spec setting every field Go's toolspec.ToolSpec carries
# (go/ai/toolspec/spec.go), keyed by its json tags.
FULL_SPEC_YAML = """\
name: full
schema_version: "1.1"
flags:
  - name: verbose
    short: v
    type: bool
    description: Verbose output
    required: false
    deprecated: true
    replaced_by: log-level
commands:
  - name: deploy
    short: Deploy a release
    aliases: [ship]
    flags:
      - name: env
        short: e
        type: string
        description: Target environment
        required: true
        deprecated: false
        replaced_by: ""
        default: staging
        choices: [staging, prod]
    args:
      - name: release
        required: true
      - name: region
      - name: targets
        variadic: true
    undeclared_args: true
    exit_codes: [OK, USAGE, CONFLICT]
    contract:
      idempotent: true
      side_effects: [writes remote state]
      retryable: true
      pre_conditions: [authenticated]
      idempotent_class: "yes"
    safety:
      level: dangerous
      requires_confirmation: true
      permissions: [deploy:write]
    preview_modes: [dryrun, plan]
    output_schema:
      format: json
      fields: [id, status]
      example: '{"id":"r1","status":"ok"}'
    deprecated: true
    deprecated_since: "2.0"
    replaced_by: release
    intent:
      domain: delivery
      category: operations
      tags: [prod]
    suggested_next: [status]
    children:
      - name: status
        short: Show deploy status
error_patterns:
  - pattern: "deploy failed"
    fix: check logs
    source: manual
    cause: bad_state
    fixes: [check logs, rollback]
    confidence: 0.75
    provenance:
      source: docs
      retrieved_at: "2026-01-02"
      confidence: 0.5
workflows:
  - name: release
    steps: [deploy, status]
    after:
      deploy: [status]
    provenance:
      source: llm
state_introspection:
  config_commands: [config show]
  env_vars: [FULL_TOKEN]
  auth_commands: [auth login]
"""


def _write(tmp_path: Path, body: str) -> str:
    p = tmp_path / "spec.yaml"
    p.write_text(body)
    return str(p)


class TestLoadToolspecAllFields:
    def test_every_go_field_loads(self, tmp_path):
        spec = load_toolspec(_write(tmp_path, FULL_SPEC_YAML))
        assert spec == ToolSpec(
            name="full",
            schema_version="1.1",
            flags=[
                Flag(
                    name="verbose",
                    short="v",
                    type="bool",
                    description="Verbose output",
                    required=False,
                    deprecated=True,
                    replaced_by="log-level",
                )
            ],
            commands=[
                Command(
                    name="deploy",
                    short="Deploy a release",
                    aliases=["ship"],
                    flags=[
                        Flag(
                            name="env",
                            short="e",
                            type="string",
                            description="Target environment",
                            required=True,
                            default="staging",
                            choices=["staging", "prod"],
                        )
                    ],
                    args=[
                        Arg(name="release", required=True),
                        Arg(name="region", required=False),
                        Arg(name="targets", variadic=True),
                    ],
                    undeclared_args=True,
                    exit_codes=["OK", "USAGE", "CONFLICT"],
                    contract=Contract(
                        idempotent=True,
                        side_effects=["writes remote state"],
                        retryable=True,
                        pre_conditions=["authenticated"],
                        idempotent_class="yes",
                    ),
                    safety=Safety(
                        level="dangerous",
                        requires_confirmation=True,
                        permissions=["deploy:write"],
                    ),
                    preview_modes=["dryrun", "plan"],
                    output_schema=OutputSchema(
                        format="json",
                        fields=["id", "status"],
                        example='{"id":"r1","status":"ok"}',
                    ),
                    deprecated=True,
                    deprecated_since="2.0",
                    replaced_by="release",
                    intent=Intent(
                        domain="delivery",
                        category="operations",
                        tags=["prod"],
                    ),
                    suggested_next=["status"],
                    children=[Command(name="status", short="Show deploy status")],
                )
            ],
            error_patterns=[
                ErrorPattern(
                    pattern="deploy failed",
                    fix="check logs",
                    source="manual",
                    cause="bad_state",
                    fixes=["check logs", "rollback"],
                    confidence=0.75,
                    provenance=Provenance(
                        source="docs",
                        retrieved_at="2026-01-02",
                        confidence=0.5,
                    ),
                )
            ],
            workflows=[
                Workflow(
                    name="release",
                    steps=["deploy", "status"],
                    after={"deploy": ["status"]},
                    provenance=Provenance(source="llm"),
                )
            ],
            state_introspection=StateIntrospection(
                config_commands=["config show"],
                env_vars=["FULL_TOKEN"],
                auth_commands=["auth login"],
            ),
        )

    def test_new_fields_default_when_absent(self, tmp_path):
        spec = load_toolspec(
            _write(
                tmp_path,
                "name: t\nschema_version: '1'\ncommands:\n"
                "  - name: c\n    flags: [{name: f}]\n    args: [{name: a}]\n"
                "    contract: {retryable: true}\n",
            )
        )
        cmd = spec.commands[0]
        assert cmd.short == ""
        assert cmd.undeclared_args is False
        assert cmd.exit_codes is None
        assert cmd.args == [Arg(name="a", required=False, variadic=False)]
        assert cmd.flags == [Flag(name="f", required=False, default="", choices=None)]
        assert cmd.contract == Contract(retryable=True, idempotent_class="")
        assert Command(name="c").args is None

    def test_positional_construction_still_works(self):
        # New fields sit last so the positional order the earlier
        # fields established is unchanged.
        assert Flag("v", "v", "bool", "Verbose", True, "x", True) == Flag(
            name="v",
            short="v",
            type="bool",
            description="Verbose",
            deprecated=True,
            replaced_by="x",
            required=True,
        )
        assert Arg("a", True) == Arg(name="a", required=True)
        assert Contract(True, ["w"], True, ["p"]) == Contract(
            idempotent=True, side_effects=["w"], retryable=True, pre_conditions=["p"]
        )

    def test_unknown_keys_ignored(self, tmp_path):
        spec = load_toolspec(
            _write(
                tmp_path,
                "name: t\nschema_version: '1'\ntelemetry: {enabled: true}\n"
                "commands:\n  - name: c\n    bogus: 1\n",
            )
        )
        assert spec.commands == [Command(name="c")]

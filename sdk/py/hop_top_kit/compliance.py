"""12-factor AI CLI compliance checker — Python port."""

from __future__ import annotations

import json
import os
import re
import subprocess
from dataclasses import dataclass, field
from enum import IntEnum

import yaml


class Factor(IntEnum):
    SELF_DESCRIBING = 1
    STRUCTURED_IO = 2
    STREAM_DISCIPLINE = 3
    CONTRACTS_ERRORS = 4
    PREVIEW = 5
    IDEMPOTENCY = 6
    STATE_TRANSPARENCY = 7
    SAFE_DELEGATION = 8
    OBSERVABLE_OPS = 9
    PROVENANCE = 10
    EVOLUTION = 11
    AUTH_LIFECYCLE = 12
    CONSENTING_TELEMETRY = 13


FACTOR_NAMES = {
    Factor.SELF_DESCRIBING: "Self-Describing",
    Factor.STRUCTURED_IO: "Structured I/O",
    Factor.STREAM_DISCIPLINE: "Stream Discipline",
    Factor.CONTRACTS_ERRORS: "Contracts & Errors",
    Factor.PREVIEW: "Preview",
    Factor.IDEMPOTENCY: "Idempotency",
    Factor.STATE_TRANSPARENCY: "State Transparency",
    Factor.SAFE_DELEGATION: "Safe Delegation",
    Factor.OBSERVABLE_OPS: "Observable Ops",
    Factor.PROVENANCE: "Provenance",
    Factor.EVOLUTION: "Evolution",
    Factor.AUTH_LIFECYCLE: "Auth Lifecycle",
    Factor.CONSENTING_TELEMETRY: "Consenting Telemetry",
}


def factor_name(f: Factor) -> str:
    return FACTOR_NAMES.get(f, f"Factor({int(f)})")


@dataclass
class CheckResult:
    factor: Factor
    name: str
    status: str  # pass, fail, skip, warn
    details: str = ""
    suggestion: str = ""


@dataclass
class Report:
    binary: str = ""
    toolspec: str = ""
    results: list[CheckResult] = field(default_factory=list)
    score: int = 0
    total: int = 12


# --- Internal helpers ---


def _pass(f: Factor, details: str) -> CheckResult:
    return CheckResult(f, factor_name(f), "pass", details)


def _fail(
    f: Factor,
    details: str,
    suggestion: str,
) -> CheckResult:
    return CheckResult(
        f,
        factor_name(f),
        "fail",
        details,
        suggestion,
    )


def _skip(f: Factor, details: str) -> CheckResult:
    return CheckResult(f, factor_name(f), "skip", details)


def _warn(f: Factor, details: str, suggestion: str) -> CheckResult:
    """A partial pass: the obligation is met in shape but not in substance.

    Carries a suggestion like _fail, because the point of a warn is that
    there is something to do about it.
    """
    return CheckResult(f, factor_name(f), "warn", details, suggestion)


def _all_commands(cmds: list[dict]) -> list[dict]:
    out: list[dict] = []
    for c in cmds:
        out.append(c)
        out.extend(_all_commands(c.get("children", [])))
    return out


def _mutating_commands(cmds: list[dict]) -> list[dict]:
    return [c for c in _all_commands(cmds) if c.get("contract", {}).get("side_effects")]


def _dangerous_commands(cmds: list[dict]) -> list[dict]:
    return [c for c in _all_commands(cmds) if c.get("safety", {}).get("level") == "dangerous"]


def _telemetry_opted_in(spec: dict) -> bool:
    """True iff the toolspec declares telemetry with enabled: true.

    Non-opt-in specs skip F13 entirely. A bare ``telemetry:`` key parses
    as None rather than a mapping, so coerce before reading ``enabled``.
    """
    return bool((spec.get("telemetry") or {}).get("enabled"))


# Subcommands an opt-in binary MUST expose under its consent command
# (typically ``<bin> telemetry``).
TELEMETRY_CONSENT_SUBCOMMANDS_CANONICAL = [
    "disable",
    "enable",
    "inspect",
    "reset",
    "status",
]

# Matches ``<UPPERCASE_APP>_TELEMETRY_MODE``. The kit literal
# ``KIT_TELEMETRY_MODE`` matches by construction, as does any
# app-prefixed form like ``SPACED_TELEMETRY_MODE``.
_TELEMETRY_MODE_ENV_SHAPE = re.compile(r"^[A-Z][A-Z0-9_]*_TELEMETRY_MODE$")


def _has_telemetry_mode_env(envs: list[str]) -> bool:
    return any(_TELEMETRY_MODE_ENV_SHAPE.match(e) for e in envs)


def _command_path_exists(cmds: list[dict], path: list[str]) -> bool:
    """True when the space-separated path exists in the command tree."""
    if not path:
        return False
    for c in cmds:
        if c.get("name") != path[0]:
            continue
        if len(path) == 1:
            return True
        return _command_path_exists(c.get("children", []), path[1:])
    return False


# --- Static Checks ---


def _check_self_describing(spec: dict) -> CheckResult:
    f = Factor.SELF_DESCRIBING
    cmds = spec.get("commands", [])
    if not cmds:
        return _fail(
            f,
            "no commands defined",
            "Add a commands array with at least one named command",
        )
    for c in cmds:
        if not c.get("name"):
            return _fail(
                f,
                "command missing name",
                "Every command must have a name field",
            )
    return _pass(f, "commands array non-empty, all named")


def _check_structured_io(spec: dict) -> CheckResult:
    f = Factor.STRUCTURED_IO
    for c in _all_commands(spec.get("commands", [])):
        if c.get("output_schema"):
            return _pass(f, f"output_schema found on {c['name']}")
    return _fail(
        f,
        "no command has output_schema",
        "Add output_schema to at least one command",
    )


def _check_contracts_errors(spec: dict) -> CheckResult:
    f = Factor.CONTRACTS_ERRORS
    mut = _mutating_commands(spec.get("commands", []))
    if not mut:
        for c in _all_commands(spec.get("commands", [])):
            if c.get("contract"):
                return _pass(f, "contracts found")
        return _fail(
            f,
            "no contracts declared",
            "Add contract fields to commands",
        )
    for c in mut:
        if not c.get("contract"):
            return _fail(
                f,
                f"{c['name']} has side_effects but no contract",
                "Add contract fields to mutating commands",
            )
    return _pass(f, "all mutating commands have contracts")


def _check_preview(spec: dict) -> CheckResult:
    f = Factor.PREVIEW
    mut = _mutating_commands(spec.get("commands", []))
    if not mut:
        return _pass(f, "no mutating commands to preview")
    with_preview = sum(1 for c in mut if c.get("preview_modes"))
    if with_preview == 0:
        return _fail(
            f,
            "no mutating command has preview_modes",
            "Add preview_modes (e.g. --dry-run)",
        )
    return _pass(
        f,
        f"{with_preview}/{len(mut)} mutating commands have preview_modes",
    )


def _check_idempotency(spec: dict) -> CheckResult:
    f = Factor.IDEMPOTENCY
    all_cmds = _all_commands(spec.get("commands", []))
    if not all_cmds:
        return _fail(f, "no commands", "Add commands")
    declared = sum(1 for c in all_cmds if "idempotent" in c.get("contract", {}))
    if declared == 0:
        return _fail(
            f,
            "no command declares idempotent",
            "Add contract.idempotent to each command",
        )
    return _pass(f, "idempotency declared on commands")


def _check_state_transparency(spec: dict) -> CheckResult:
    f = Factor.STATE_TRANSPARENCY
    si = spec.get("state_introspection", {})
    if not si or not si.get("config_commands"):
        return _fail(
            f,
            "no config_commands in state_introspection",
            "Add state_introspection.config_commands",
        )
    return _pass(f, "config_commands present")


def _check_safe_delegation(spec: dict) -> CheckResult:
    f = Factor.SAFE_DELEGATION
    dangerous = _dangerous_commands(spec.get("commands", []))
    if not dangerous:
        return _pass(f, "no dangerous commands")
    for c in dangerous:
        if not c.get("safety"):
            return _fail(
                f,
                f"{c['name']} is dangerous but has no safety block",
                "Add safety with requires_confirmation",
            )
    return _pass(
        f,
        "all dangerous commands have safety metadata",
    )


def _check_evolution(spec: dict) -> CheckResult:
    f = Factor.EVOLUTION
    if not spec.get("schema_version"):
        return _fail(
            f,
            "schema_version not set",
            "Set schema_version in the toolspec",
        )
    return _pass(f, f"schema_version: {spec['schema_version']}")


def _check_auth_lifecycle(spec: dict) -> CheckResult:
    f = Factor.AUTH_LIFECYCLE
    si = spec.get("state_introspection", {})
    if not si or not si.get("auth_commands"):
        return _skip(
            f,
            "no auth_commands — skipped (tool may not need auth)",
        )
    return _pass(f, "auth_commands present")


def _check_consenting_telemetry(spec: dict) -> CheckResult:
    """F13 static check.

    When the toolspec opts into telemetry (``telemetry.enabled: true``),
    asserts the block is well-formed across seven sub-conditions:

    1. categories non-empty
    2. consent_subcommands contains the canonical set
       {disable, enable, inspect, reset, status}
    3. kill_switch_envs contains DO_NOT_TRACK
    4. kill_switch_envs contains a ``<APP>_TELEMETRY_MODE`` entry
    5. prompt_version non-empty (canonical field name is locked —
       aliases like ``consent_version`` are not read)
    6. redact_rules non-empty
    7. every declared consent_subcommand maps to a real command in
       the commands tree

    Failures aggregate into a single result: one row per factor.
    """
    f = Factor.CONSENTING_TELEMETRY
    if not _telemetry_opted_in(spec):
        return _skip(f, "binary does not opt into telemetry")

    t = spec.get("telemetry") or {}
    failures: list[str] = []

    if not t.get("categories"):
        failures.append("telemetry.categories is empty")

    subs = t.get("consent_subcommands") or []
    missing_subs = [req for req in TELEMETRY_CONSENT_SUBCOMMANDS_CANONICAL if req not in subs]
    if missing_subs:
        failures.append(
            "telemetry.consent_subcommands missing required entries: " + ", ".join(missing_subs)
        )

    envs = t.get("kill_switch_envs") or []
    if "DO_NOT_TRACK" not in envs:
        failures.append("telemetry.kill_switch_envs missing DO_NOT_TRACK")
    if not _has_telemetry_mode_env(envs):
        failures.append(
            "telemetry.kill_switch_envs missing a <APP>_TELEMETRY_MODE "
            "entry (e.g. KIT_TELEMETRY_MODE or SPACED_TELEMETRY_MODE)"
        )

    if not str(t.get("prompt_version") or "").strip():
        failures.append(
            "telemetry.prompt_version is empty (canonical field name "
            "is `prompt_version`; aliases like `consent_version` "
            "are not accepted)"
        )

    if not str(t.get("redact_rules") or "").strip():
        failures.append("telemetry.redact_rules is empty")

    # The consent_command schema is ``<bin> telemetry [...]``; the
    # leading ``<bin>`` is the binary name, not a top-level command, so
    # strip it. Fall back to ``telemetry <sub>`` when the value is empty
    # or a bare binary name.
    consent_tokens = str(t.get("consent_command") or "").split()
    consent_path = consent_tokens[1:] if len(consent_tokens) > 1 else ["telemetry"]
    unmapped_subs = [
        " ".join([*consent_path, sub])
        for sub in subs
        if not _command_path_exists(spec.get("commands", []), [*consent_path, sub])
    ]
    if unmapped_subs:
        failures.append(
            "telemetry.consent_subcommands declared but not in commands tree: "
            + ", ".join(unmapped_subs)
        )

    if not failures:
        return _pass(
            f,
            "telemetry block well-formed; all consent subcommands declared",
        )

    return _fail(
        f,
        "; ".join(failures),
        "Fix the telemetry block: ensure categories, "
        "consent_subcommands {disable, enable, inspect, reset, "
        "status}, kill_switch_envs [DO_NOT_TRACK, <APP>_TELEMETRY_MODE], "
        "prompt_version, redact_rules are set, and that each "
        "consent_subcommand maps to a command in the commands tree.",
    )


def _run_static_checks(spec: dict) -> list[CheckResult]:
    return [
        _check_self_describing(spec),
        _check_structured_io(spec),
        _skip(Factor.STREAM_DISCIPLINE, "runtime check only"),
        _check_contracts_errors(spec),
        _check_preview(spec),
        _check_idempotency(spec),
        _check_state_transparency(spec),
        _check_safe_delegation(spec),
        _skip(Factor.OBSERVABLE_OPS, "runtime check only"),
        _skip(Factor.PROVENANCE, "runtime check only"),
        _check_evolution(spec),
        _check_auth_lifecycle(spec),
    ]


# --- Runtime Checks ---


def _run_bin(
    binary: str,
    args: list[str],
) -> tuple[str, str, int]:
    """Execute binary safely via subprocess (no shell)."""
    try:
        result = subprocess.run(
            [binary, *args],
            capture_output=True,
            text=True,
            timeout=10,
        )
        return result.stdout, result.stderr, result.returncode
    except Exception:
        return "", "", -1


def _find_read_command(spec: dict) -> str | None:
    for c in _all_commands(spec.get("commands", [])):
        contract = c.get("contract", {})
        if c.get("output_schema") and contract.get("idempotent"):
            return c["name"]
    return None


def _run_bin_env(
    binary: str,
    args: list[str],
    env: dict[str, str],
) -> tuple[str, str, int]:
    """Execute binary with extra environment entries, no shell.

    For probes whose obligation is stated against a policy the tool reads
    from the environment. The inherited environment is kept: a binary that
    needs PATH or HOME to start at all must still start.
    """
    try:
        result = subprocess.run(
            [binary, *args],
            capture_output=True,
            text=True,
            timeout=10,
            env={**os.environ, **env},
        )
        return result.stdout, result.stderr, result.returncode
    except Exception:
        return "", "", -1


# The environment name kit reads for its opt-in flag autocorrect policy. Set
# by the probe below to elicit each mode from a tool that supports it; a tool
# that does not simply ignores it, which is what makes the probe skip rather
# than fail.
AUTOCORRECT_ENV_VAR = "KIT_AUTOCORRECT"

# The envelope key a tool MUST populate when it rewrote a flag for the caller.
CORRECTED_FROM_FIELD = "corrected_from"


def _rt_contracts_errors_envelope(binary: str) -> CheckResult:
    """The original F4 obligation: non-zero exit plus a fix-carrying envelope."""
    f = Factor.CONTRACTS_ERRORS
    _, stderr, code = _run_bin(binary, ["--format", "json", "--bogus-arg-xyzzy"])
    if code == 0:
        return _fail(
            f, "bogus arg didn't cause error exit", "Unknown flags should cause non-zero exit"
        )
    obj = None
    if _is_valid_json(stderr):
        try:
            obj = json.loads(stderr.strip())
        except ValueError:
            obj = None
    if isinstance(obj, dict) and "code" in obj:
        if _error_carries_fix(obj):
            return _pass(f, "structured error with code field and recovery guidance")
        return _warn(
            f,
            "structured error carries no recovery guidance",
            "Populate suggested_fix (or alternatives) with a concrete "
            "correction so the caller does not need a --help round trip",
        )
    return _warn(
        f,
        "error output is not structured JSON",
        "Return JSON errors with a 'code' field on stderr",
    )


def _rt_contracts_errors_autocorrect(binary: str, spec: dict) -> CheckResult:
    """Check the per-mode semantics of an opt-in flag autocorrect.

    The obligations differ by mode, and each is the thing that mode's
    existence puts at risk:

      - off (and the default, which is off): a mistyped flag exits non-zero.
        This is the contract every other caller depends on, and a tool that
        quietly corrects by default has broken it for every script that was
        relying on the failure.
      - read: IF a correction was applied — exit 0 on an invocation that
        should have failed to parse — the envelope MUST carry corrected_from.
        A run that silently becomes a different run is unauditable, and stderr
        prose is not something a --format json consumer reads.

    Aimed at a READ command, not the root, because a correction is only ever
    legitimate on one: the side-effect gate is per-leaf, and a tool rewriting
    a flag on an unannotated root would be violating that gate rather than
    demonstrating the feature.

    The near-miss token is a one-edit typo of --format. ``--formt`` rather
    than ``--forma``: the latter is a PREFIX of --format, --format-opt and
    --format-help, so it is ambiguous by construction and no compliant tool
    would ever correct it — a probe built on it would skip for every tool and
    measure nothing.

    A tool with no autocorrect support ignores the environment variable and
    keeps exiting non-zero, which is indistinguishable from mode off and so
    reported as a skip rather than a failure: the factor does not require a
    tool to HAVE this feature, only to be honest about it if it does.
    """
    f = Factor.CONTRACTS_ERRORS
    near_miss = "--formt=json"

    read_cmd = _find_read_command(spec)
    if not read_cmd:
        return _skip(f, "no read command found to probe autocorrect on")

    # Obligation 1: the default is suggest-only.
    if _run_bin(binary, [read_cmd, near_miss])[2] == 0:
        return _fail(
            f,
            "a mistyped flag exited 0 with no autocorrect policy set",
            "Keep autocorrect off by default: a bad flag must exit non-zero "
            "unless the caller opted in",
        )

    # Obligation 2: explicit off behaves as the default does.
    if _run_bin_env(binary, [read_cmd, near_miss], {AUTOCORRECT_ENV_VAR: "off"})[2] == 0:
        return _fail(
            f,
            "a mistyped flag exited 0 under an explicit off policy",
            "Honor the off value: it must not be read as unset",
        )

    # Obligation 3: under read, a correction that WAS applied is declared.
    stdout, stderr, code = _run_bin_env(
        binary,
        [read_cmd, "--format", "json", near_miss],
        {AUTOCORRECT_ENV_VAR: "read"},
    )
    if code != 0:
        return _skip(
            f,
            "binary does not apply flag corrections under " + AUTOCORRECT_ENV_VAR + "=read",
        )
    # The envelope is on stderr, where kit writes every envelope, so the
    # command's own data on stdout stays clean. Check both: a tool that put it
    # on stdout still declared it, and this factor is about the declaration,
    # not the stream (Factor 3 owns the stream).
    if _correction_declared(stderr) or _correction_declared(stdout):
        return _pass(f, "applied flag correction declares " + CORRECTED_FROM_FIELD)
    return _fail(
        f,
        "a flag correction was applied but no " + CORRECTED_FROM_FIELD + " was reported",
        "Emit " + CORRECTED_FROM_FIELD + " in the structured envelope naming the token "
        "the caller typed, so an agent and an audit log can both see that the "
        "command that ran is not the command that was asked for",
    )


def _correction_declared(s: str) -> bool:
    """Report whether s carries a corrected_from field.

    Tolerant of surrounding output on purpose: a tool may write the notice
    alongside logs, and a probe that demanded the stream be exactly one JSON
    document would be testing Factor 3's obligation, not this one. Each line
    is tried as its own document, then the raw text as a fallback for a YAML
    or plaintext rendering.
    """
    for line in s.splitlines():
        t = line.strip()
        if not t.startswith("{"):
            continue
        try:
            obj = json.loads(t)
        except ValueError:
            continue
        if isinstance(obj, dict):
            v = obj.get(CORRECTED_FROM_FIELD)
            if isinstance(v, str) and v.strip():
                return True
    if re.search(r'"corrected_from"\s*:\s*"[^"\s]', s):
        return True
    return CORRECTED_FROM_FIELD + ":" in s or "Corrected from:" in s


def _aggregate_contracts_errors(*rs: CheckResult) -> CheckResult:
    """Fold the two F4 runtime sub-checks into one row.

    Precedence matches the F13 aggregator with one addition it needs and F13
    does not: a warn survives. The envelope sub-check reports warn for a tool
    whose error is unstructured or fix-less, and collapsing that to pass
    because the autocorrect arm skipped would hide the finding on exactly the
    tools that have not adopted autocorrect — which is most of them.
    """
    f = Factor.CONTRACTS_ERRORS
    failed: list[str] = []
    skipped: list[str] = []
    first_warn: CheckResult | None = None
    for r in rs:
        if r.status == "fail":
            failed.append(r.details or "")
        elif r.status == "skip":
            skipped.append(r.details or "")
        elif r.status == "warn" and first_warn is None:
            first_warn = r
    if failed:
        return _fail(
            f,
            "; ".join(failed),
            "Address each failing sub-condition (structured error envelope; "
            "autocorrect mode semantics)",
        )
    if first_warn is not None:
        return first_warn
    if len(skipped) == len(rs):
        return _skip(f, "; ".join(dict.fromkeys(skipped)))
    return _pass(
        f,
        "structured error with recovery guidance; autocorrect mode semantics honored",
    )


# Every word that exists only to frame a help invocation. A fix reduces to
# nothing once they are removed exactly when it is a pointer back at the help
# page.
_HELP_FRAMING = frozenset(
    {
        "--help",
        "-h",
        "help",
        "run",
        "see",
        "try",
        "use",
        "for",
        "usage",
        "the",
        "a",
        "an",
        "to",
        "and",
        "or",
        "then",
        "check",
        "consult",
        "with",
    }
)


def _is_concrete_fix(s: str) -> bool:
    """Report whether s is recovery guidance, not a pointer back at --help.

    A bare command-path word ("tool", "sub") counts as framing too:
    "tool sub --help" names a path, not a fix, so a token must carry a flag
    dash or punctuation of its own to count as content.
    """
    for w in re.split(r"[\s'\"`,.;:()]+", s.lower()):
        if not w or w in _HELP_FRAMING:
            continue
        if w.startswith("-") or re.search(r"[=<>\[\]{}/|@]", w):
            return True
    return False


def _error_carries_fix(obj: dict) -> bool:
    """Report whether a decoded error envelope offers the caller a way forward.

    Either field satisfies it. A single unambiguous correction belongs in
    suggested_fix; an ambiguous one belongs in alternatives, and a tool that
    declines to guess between candidates is behaving correctly, not
    incompletely. Empty strings and empty lists do not count — a present but
    blank field is the same dead end as an absent one.

    A bare ``--help`` pointer does not count either, in EITHER field. "Run
    tool --help for usage" is precisely the round trip this factor exists to
    eliminate, and accepting it would let a tool pass "with recovery
    guidance" for offering none. A fix that names --help alongside something
    concrete still counts — the concrete part is the guidance.
    """
    fix = obj.get("suggested_fix")
    if isinstance(fix, str) and _is_concrete_fix(fix):
        return True
    alts = obj.get("alternatives")
    if not isinstance(alts, list):
        return False
    return any(isinstance(a, str) and _is_concrete_fix(a) for a in alts)


def _is_valid_json(s: str) -> bool:
    try:
        json.loads(s.strip())
        return True
    except (json.JSONDecodeError, ValueError):
        return False


def _run_runtime_checks(
    binary: str,
    spec: dict,
) -> list[CheckResult]:
    results: list[CheckResult] = []

    # F1: --help
    f = Factor.SELF_DESCRIBING
    stdout, _, code = _run_bin(binary, ["--help"])
    if code != 0:
        results.append(_fail(f, f"--help exited {code}", "Ensure --help exits 0"))
    else:
        upper = stdout.upper()
        if "COMMANDS" not in upper and "USAGE" not in upper:
            results.append(
                _fail(f, "--help lacks COMMANDS/USAGE", "Help should list available commands")
            )
        else:
            results.append(_pass(f, "--help exits 0, contains command listing"))

    # F2: structured I/O
    f = Factor.STRUCTURED_IO
    read_cmd = _find_read_command(spec)
    if not read_cmd:
        results.append(_skip(f, "no read command found"))
    else:
        stdout, _, code = _run_bin(
            binary,
            [read_cmd, "--format", "json"],
        )
        if code != 0:
            results.append(
                _fail(
                    f,
                    f"{read_cmd} --format json exited {code}",
                    "Read commands should support --format json",
                )
            )
        elif not _is_valid_json(stdout):
            results.append(
                _fail(f, "output is not valid JSON", "--format json should produce valid JSON")
            )
        else:
            results.append(
                _pass(
                    f,
                    f"{read_cmd} --format json returns valid JSON",
                )
            )

    # F3: stream discipline
    f = Factor.STREAM_DISCIPLINE
    if not read_cmd:
        results.append(_skip(f, "no read command found"))
    else:
        stdout, stderr, _ = _run_bin(
            binary,
            [read_cmd, "--format", "json"],
        )
        if not stdout.strip():
            results.append(_fail(f, "stdout is empty", "Data should go to stdout"))
        elif _is_valid_json(stderr) and len(stderr.strip()) > 2:
            results.append(_fail(f, "stderr contains JSON", "Keep structured data on stdout"))
        else:
            results.append(_pass(f, "stdout has data, stderr clean"))

    # F4: bogus arg returns a structured error carrying a fix
    #
    # Two separate obligations, checked in order of severity:
    #
    #  1. Non-zero exit. A rejected flag that exits 0 is the worst outcome —
    #     the caller cannot tell the invocation failed. Hard fail.
    #  2. A structured envelope that carries recovery guidance. An error whose
    #     only content is "unknown flag: --x" is a dead end: the caller has to
    #     spend a --help round trip to learn what it should have typed. That
    #     round trip is the cost this factor exists to eliminate, so a
    #     structured error WITHOUT a fix is only a partial pass.
    #
    # Both are stated against the SUGGEST-ONLY behavior, which is what the
    # envelope sub-check elicits: an argument no real flag is close to, and no
    # autocorrect policy of its own. A tool invoked under an autocorrect
    # policy is a different measurement, made by the second sub-check and
    # folded into the same row.
    results.append(
        _aggregate_contracts_errors(
            _rt_contracts_errors_envelope(binary),
            _rt_contracts_errors_autocorrect(binary, spec),
        )
    )

    # F5: preview
    f = Factor.PREVIEW
    mut = _mutating_commands(spec.get("commands", []))
    if not mut:
        results.append(_skip(f, "no mutating commands"))
    else:
        found = False
        for c in mut:
            for mode in c.get("preview_modes", []):
                _, _, code = _run_bin(binary, [c["name"], mode])
                if code == 0:
                    results.append(_pass(f, f"{c['name']} {mode} exits 0"))
                    found = True
                    break
            if found:
                break
        if not found:
            results.append(
                _fail(
                    f,
                    "no mutating command succeeds with preview mode",
                    "Ensure --dry-run exits 0",
                )
            )

    # F7: config
    f = Factor.STATE_TRANSPARENCY
    _, _, code = _run_bin(binary, ["config", "show"])
    if code == 0:
        results.append(_pass(f, "config show exits 0"))
    else:
        _, _, code = _run_bin(binary, ["config"])
        if code == 0:
            results.append(_pass(f, "config exits 0"))
        else:
            results.append(_fail(f, "config command failed", "Add a config/config show command"))

    # F8: safe delegation
    f = Factor.SAFE_DELEGATION
    dangerous = _dangerous_commands(spec.get("commands", []))
    if not dangerous:
        results.append(_skip(f, "no dangerous commands"))
    else:
        results.append(_pass(f, "dangerous commands have safety metadata"))

    # F10: provenance
    f = Factor.PROVENANCE
    if not read_cmd:
        results.append(_skip(f, "no read command found"))
    else:
        stdout, _, code = _run_bin(
            binary,
            [read_cmd, "--format", "json"],
        )
        if code != 0:
            results.append(_skip(f, f"{read_cmd} failed"))
        else:
            try:
                obj = json.loads(stdout.strip())
                if "_meta" in obj:
                    results.append(_pass(f, "_meta field present"))
                else:
                    results.append(
                        _fail(
                            f,
                            "no _meta field in JSON output",
                            "Add _meta with provenance info",
                        )
                    )
            except (json.JSONDecodeError, ValueError):
                results.append(_skip(f, "output not JSON object"))

    # F11: --version
    f = Factor.EVOLUTION
    _, _, code = _run_bin(binary, ["--version"])
    if code != 0:
        results.append(_fail(f, f"--version exited {code}", "Ensure --version exits 0"))
    else:
        results.append(_pass(f, "--version exits 0"))

    # F12: auth
    f = Factor.AUTH_LIFECYCLE
    si = spec.get("state_introspection", {})
    if not si or not si.get("auth_commands"):
        results.append(_skip(f, "no auth_commands declared"))
    else:
        _, _, code = _run_bin(binary, ["auth", "status"])
        if code == 0:
            results.append(_pass(f, "auth status exits 0"))
        else:
            _, _, code = _run_bin(binary, ["auth"])
            if code == 0:
                results.append(_pass(f, "auth exits 0"))
            else:
                results.append(
                    _fail(f, "auth command failed", "Implement auth status/auth commands")
                )

    return results


# --- Public API ---


def run_static(toolspec_path: str) -> list[CheckResult]:
    """Check toolspec YAML for completeness."""
    with open(toolspec_path) as f:
        spec = yaml.safe_load(f)
    return _run_static_checks(spec)


def run_runtime(
    binary_path: str,
    toolspec_path: str,
) -> list[CheckResult]:
    """Execute binary and check behaviour."""
    with open(toolspec_path) as f:
        spec = yaml.safe_load(f)
    return _run_runtime_checks(binary_path, spec)


def run(
    binary_path: str,
    toolspec_path: str,
) -> Report:
    """Run both static + runtime checks."""
    with open(toolspec_path) as f:
        spec = yaml.safe_load(f)

    results = _run_static_checks(spec)
    results.append(_check_consenting_telemetry(spec))

    if binary_path:
        rt = _run_runtime_checks(binary_path, spec)
        results = _merge_results(results, rt)

    # The denominator counts factors *eligible* to contribute. The 12
    # pre-F13 factors always are, so a non-opt-in binary still scores
    # N/12; only an opt-in binary adds F13 and scores N/13. Skips inside
    # the eligible set (e.g. runtime-only factors on a static-only run)
    # still count toward the denominator.
    total = 13 if _telemetry_opted_in(spec) else 12

    score = sum(1 for r in results if r.status == "pass")
    return Report(
        binary=binary_path,
        toolspec=toolspec_path,
        results=results,
        score=score,
        total=total,
    )


def _merge_results(
    static: list[CheckResult],
    runtime: list[CheckResult],
) -> list[CheckResult]:
    by_factor: dict[Factor, CheckResult] = {}
    for r in static:
        by_factor[r.factor] = r
    for r in runtime:
        existing = by_factor.get(r.factor)
        if not existing or existing.status == "skip":
            by_factor[r.factor] = r

    return [by_factor[f] for f in Factor if f in by_factor]


def format_report(r: Report, fmt: str = "text") -> str:
    """Render report as text or JSON."""
    if fmt == "json":
        return _format_json(r)
    return _format_text(r)


def _status_icon(s: str) -> str:
    return {
        "pass": "PASS",
        "fail": "FAIL",
        "warn": "WARN",
        "skip": "SKIP",
    }.get(s, "????")


def _format_text(r: Report) -> str:
    lines = [
        "",
        "  12-Factor AI CLI Compliance Report",
        "  ══════════════════════════════════",
    ]
    if r.binary:
        lines.append(f"  Binary   : {r.binary}")
    if r.toolspec:
        lines.append(f"  Toolspec : {r.toolspec}")
    lines.append("")

    for cr in r.results:
        icon = _status_icon(cr.status)
        lines.append(f"  {icon}  F{int(cr.factor):2d} {cr.name:<20s} {cr.details}")
        if cr.suggestion:
            lines.append(f"       └─ {cr.suggestion}")

    lines.append("")
    lines.append(f"  Score: {r.score}/{r.total} factors passing")
    lines.append("")
    return "\n".join(lines)


def _format_json(r: Report) -> str:
    data = {
        "binary": r.binary,
        "toolspec": r.toolspec,
        "results": [
            {
                "factor": int(cr.factor),
                "name": cr.name,
                "status": cr.status,
                **({"details": cr.details} if cr.details else {}),
                **({"suggestion": cr.suggestion} if cr.suggestion else {}),
            }
            for cr in r.results
        ],
        "score": r.score,
        "total": r.total,
    }
    return json.dumps(data, indent=2) + "\n"

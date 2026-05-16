"""12-factor AI CLI compliance checker — Python port."""

from __future__ import annotations

import json
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

    # F4: bogus arg
    f = Factor.CONTRACTS_ERRORS
    _, _, code = _run_bin(binary, ["--bogus-arg-xyzzy"])
    if code == 0:
        results.append(
            _fail(
                f, "bogus arg didn't cause error exit", "Unknown flags should cause non-zero exit"
            )
        )
    else:
        results.append(
            CheckResult(
                f,
                factor_name(f),
                "warn",
                "error output is not structured JSON",
                "Return JSON errors with a 'code' field on stderr",
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

    if binary_path:
        rt = _run_runtime_checks(binary_path, spec)
        results = _merge_results(results, rt)

    score = sum(1 for r in results if r.status == "pass")
    return Report(
        binary=binary_path,
        toolspec=toolspec_path,
        results=results,
        score=score,
        total=12,
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

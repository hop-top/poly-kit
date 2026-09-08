"""Tests for the 12-factor AI CLI compliance checker."""

from __future__ import annotations

import json
import os
import tempfile

import pytest

import hop_top_kit.compliance as compliance
from hop_top_kit.compliance import (
    CheckResult,
    Factor,
    Report,
    _error_carries_fix,
    factor_name,
    format_report,
    run,
    run_static,
)

TOOLSPEC = os.path.join(
    os.path.dirname(__file__),
    "../../../examples/spaced/spaced.toolspec.yaml",
)


class TestFactorName:
    def test_all_factors_have_names(self):
        for f in Factor:
            name = factor_name(f)
            assert name, f"factor {f} should have name"
            assert not name.startswith("Factor(")


class TestRunStatic:
    def test_spaced_toolspec(self):
        results = run_static(TOOLSPEC)
        assert len(results) == 12

        by_factor = {r.factor: r for r in results}

        # Passing factors
        assert by_factor[Factor.SELF_DESCRIBING].status == "pass"
        assert by_factor[Factor.STRUCTURED_IO].status == "pass"
        assert by_factor[Factor.CONTRACTS_ERRORS].status == "pass"
        assert by_factor[Factor.PREVIEW].status == "pass"
        assert by_factor[Factor.IDEMPOTENCY].status == "pass"
        assert by_factor[Factor.STATE_TRANSPARENCY].status == "pass"
        assert by_factor[Factor.SAFE_DELEGATION].status == "pass"
        assert by_factor[Factor.EVOLUTION].status == "pass"

        # Runtime-only (skipped)
        assert by_factor[Factor.STREAM_DISCIPLINE].status == "skip"
        assert by_factor[Factor.OBSERVABLE_OPS].status == "skip"
        assert by_factor[Factor.PROVENANCE].status == "skip"

    def test_empty_spec(self):
        with tempfile.NamedTemporaryFile(
            mode="w",
            suffix=".yaml",
            delete=False,
        ) as f:
            f.write("name: empty\n")
            tmp = f.name
        try:
            results = run_static(tmp)
            failing = [r for r in results if r.status == "fail"]
            assert len(failing) > 0
        finally:
            os.unlink(tmp)


class TestRun:
    def test_static_only(self):
        report = run("", TOOLSPEC)
        assert report.total == 12
        assert report.score >= 1
        assert report.toolspec == TOOLSPEC


class TestFormatReport:
    @pytest.fixture()
    def sample_report(self) -> Report:
        return Report(
            binary="test-bin",
            toolspec="test.yaml",
            total=12,
            score=8,
            results=[
                CheckResult(
                    Factor.SELF_DESCRIBING,
                    "Self-Describing",
                    "pass",
                    "ok",
                ),
                CheckResult(
                    Factor.STRUCTURED_IO,
                    "Structured I/O",
                    "fail",
                    "missing",
                    "Add output_schema",
                ),
            ],
        )

    def test_text_format(self, sample_report: Report):
        out = format_report(sample_report, "text")
        assert "Self-Describing" in out
        assert "PASS" in out
        assert "FAIL" in out
        assert "8/12" in out

    def test_json_format(self, sample_report: Report):
        out = format_report(sample_report, "json")
        parsed = json.loads(out)
        assert parsed["score"] == 8
        assert parsed["total"] == 12
        assert len(parsed["results"]) == 2


# --- F13: Consenting Telemetry ---

WELL_FORMED_TELEMETRY = """name: probe
schema_version: "1"
commands:
  - name: ping
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  sinks: [bus]
  consent_command: "probe telemetry"
  consent_subcommands: [disable, enable, inspect, reset, status]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
"""


def _write_toolspec(body: str) -> str:
    with tempfile.NamedTemporaryFile(
        mode="w",
        suffix=".yaml",
        delete=False,
    ) as f:
        f.write(body)
        return f.name


def _f13_report(body: str) -> Report:
    """Run a toolspec body and return the whole report.

    Both the F13 row and the denominator are asserted per case, so
    callers need the report rather than just the result row.
    """
    tmp = _write_toolspec(body)
    try:
        return run("", tmp)
    finally:
        os.unlink(tmp)


def _f13(report: Report) -> CheckResult:
    by_factor = {r.factor: r for r in report.results}
    assert Factor.CONSENTING_TELEMETRY in by_factor, "F13 row must always be present"
    return by_factor[Factor.CONSENTING_TELEMETRY]


class TestConsentingTelemetry:
    def test_factor_name(self):
        assert factor_name(Factor.CONSENTING_TELEMETRY) == "Consenting Telemetry"

    def test_skips_when_not_opted_in(self):
        report = _f13_report('name: probe\nschema_version: "1"\ncommands:\n  - name: ping\n')
        assert _f13(report).status == "skip"
        assert report.total == 12, "non-opt-in specs stay at N/12"

    def test_skips_on_a_null_telemetry_key(self):
        # A bare `telemetry:` key parses as None, not a mapping. It is
        # still not an opt-in, so it must skip rather than raise.
        report = _f13_report(
            'name: probe\nschema_version: "1"\ncommands:\n  - name: ping\ntelemetry:\n'
        )
        assert _f13(report).status == "skip"
        assert report.total == 12

    def test_runs_when_opted_in(self):
        report = _f13_report(WELL_FORMED_TELEMETRY)
        r = _f13(report)
        assert r.status == "pass", r.details
        assert report.total == 13, "opt-in adds F13 to the denominator"

    def test_pass_well_formed(self):
        report = _f13_report(WELL_FORMED_TELEMETRY)
        r = _f13(report)
        assert r.status == "pass", r.details
        assert "well-formed" in r.details
        assert report.total == 13

    def test_fail_missing_category(self):
        report = _f13_report(
            WELL_FORMED_TELEMETRY.replace(
                "categories: [invocation]",
                "categories: []",
            )
        )
        r = _f13(report)
        assert r.status == "fail"
        assert "categories" in r.details
        assert r.suggestion
        assert report.total == 13

    def test_fail_missing_subcommand(self):
        report = _f13_report(
            WELL_FORMED_TELEMETRY.replace(
                "consent_subcommands: [disable, enable, inspect, reset, status]",
                "consent_subcommands: [status, enable, disable]",
            )
        )
        r = _f13(report)
        assert r.status == "fail"
        assert "reset" in r.details
        assert "inspect" in r.details
        assert report.total == 13

    def test_fail_missing_do_not_track(self):
        report = _f13_report(
            WELL_FORMED_TELEMETRY.replace(
                "kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]",
                "kill_switch_envs: [PROBE_TELEMETRY_MODE]",
            )
        )
        r = _f13(report)
        assert r.status == "fail"
        assert "DO_NOT_TRACK" in r.details
        assert report.total == 13

    def test_fail_missing_mode_env(self):
        report = _f13_report(
            WELL_FORMED_TELEMETRY.replace(
                "kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]",
                "kill_switch_envs: [DO_NOT_TRACK]",
            )
        )
        r = _f13(report)
        assert r.status == "fail"
        assert "TELEMETRY_MODE" in r.details
        assert report.total == 13

    def test_pass_with_kit_telemetry_mode(self):
        # The regex covers the kit literal without a special-case branch.
        report = _f13_report(
            WELL_FORMED_TELEMETRY.replace(
                "PROBE_TELEMETRY_MODE",
                "KIT_TELEMETRY_MODE",
            )
        )
        r = _f13(report)
        assert r.status == "pass", r.details
        assert report.total == 13

    def test_pass_with_app_prefix_mode(self):
        report = _f13_report(
            WELL_FORMED_TELEMETRY.replace(
                "PROBE_TELEMETRY_MODE",
                "SPACED_TELEMETRY_MODE",
            )
        )
        r = _f13(report)
        assert r.status == "pass", r.details
        assert report.total == 13

    def test_fail_empty_prompt_version(self):
        report = _f13_report(
            WELL_FORMED_TELEMETRY.replace(
                'prompt_version: "v1"',
                'prompt_version: ""',
            )
        )
        r = _f13(report)
        assert r.status == "fail"
        assert "prompt_version" in r.details
        # The field name is locked; the failure names the rejected alias.
        assert "consent_version" in r.details
        assert report.total == 13

    def test_fail_empty_redact_rules(self):
        report = _f13_report(
            WELL_FORMED_TELEMETRY.replace(
                "redact_rules: kit-default",
                'redact_rules: ""',
            )
        )
        r = _f13(report)
        assert r.status == "fail"
        assert "redact_rules" in r.details
        assert report.total == 13

    def test_fail_subcommand_missing_from_tree(self):
        # `inspect` is declared but has no node under `telemetry`.
        report = _f13_report(WELL_FORMED_TELEMETRY.replace("      - name: inspect\n", ""))
        r = _f13(report)
        assert r.status == "fail"
        assert "not in commands tree" in r.details
        assert "telemetry inspect" in r.details
        assert report.total == 13


class TestErrorCarriesFix:
    """Factor 4's recovery-guidance test, ported from the Go scorer.

    Either suggested_fix or alternatives satisfies the obligation, but a bare
    ``--help`` pointer satisfies neither: that round trip is exactly the cost
    the factor exists to eliminate.
    """

    @pytest.mark.parametrize(
        ("obj", "want"),
        [
            ({"suggested_fix": "--counters"}, True),
            ({"suggested_fix": "--status TODO"}, True),
            ({"alternatives": ["--a", "--b"]}, True),
            ({"alternatives": ["", "--b"]}, True),
            ({"suggested_fix": "use --format json (see --help)"}, True),
            ({}, False),
            ({"suggested_fix": "  "}, False),
            ({"alternatives": []}, False),
            ({"alternatives": ["", " "]}, False),
            ({"suggested_fix": 42}, False),
            ({"alternatives": "--a"}, False),
            # Bare help pointers, in either field.
            ({"suggested_fix": "run 'tool sub --help' for usage"}, False),
            ({"suggested_fix": "tool --help"}, False),
            ({"suggested_fix": "--help"}, False),
            ({"suggested_fix": "-h"}, False),
            ({"suggested_fix": "tool widget list --help"}, False),
            ({"alternatives": ["tool --help"]}, False),
        ],
    )
    def test_cases(self, obj, want):
        assert _error_carries_fix(obj) is want


# --- Factor-4 autocorrect arm ---------------------------------------------
#
# Each obligation gets its own stub, because the whole point of the arm is
# that a tool can be correct in one mode and wrong in another: a stub that
# behaved identically under every policy would prove nothing.

_USAGE_ENVELOPE = (
    '{"code":"USAGE","message":"unknown flag --formt","suggested_fix":"--format","exit_code":2}'
)


def _autocorrect_stub(tmp_path, off_stderr, off_code, read_stderr, read_code):
    """Write a stub that branches on KIT_AUTOCORRECT, as a real tool does."""
    path = tmp_path / "stub"
    path.write_text(
        "#!/bin/sh\n"
        'case "$KIT_AUTOCORRECT" in\n'
        "read)\n"
        "  cat >&2 <<'READ_EOF'\n" + read_stderr + "\nREAD_EOF\n"
        f"  exit {read_code}\n"
        "  ;;\n"
        "*)\n"
        "  cat >&2 <<'OFF_EOF'\n" + off_stderr + "\nOFF_EOF\n"
        f"  exit {off_code}\n"
        "  ;;\n"
        "esac\n"
    )
    path.chmod(0o755)
    return str(path)


def _autocorrect_spec():
    """Minimum spec the probe needs: one command _find_read_command picks."""
    return {
        "name": "stub",
        "schema_version": "1.0",
        "commands": [
            {
                "name": "list",
                "contract": {"idempotent": True, "side_effects": ["read"]},
                "output_schema": {"format": "json"},
            }
        ],
    }


def test_autocorrect_no_support_skips(tmp_path):
    """An honest tool without the feature is not in violation."""
    bin_ = _autocorrect_stub(tmp_path, _USAGE_ENVELOPE, 2, _USAGE_ENVELOPE, 2)
    got = compliance._rt_contracts_errors_autocorrect(bin_, _autocorrect_spec())
    assert got.status == "skip", got


def test_autocorrect_no_read_command_skips(tmp_path):
    """With nothing safe to probe on, the arm measures nothing."""
    bin_ = _autocorrect_stub(tmp_path, _USAGE_ENVELOPE, 2, _USAGE_ENVELOPE, 2)
    got = compliance._rt_contracts_errors_autocorrect(bin_, {"name": "stub"})
    assert got.status == "skip"
    assert "no read command" in got.details


def test_autocorrect_correcting_by_default_fails(tmp_path):
    """The regression the arm exists to catch: correction with no opt-in."""
    corrected = '{"code":"OK","corrected_from":"--formt"}'
    bin_ = _autocorrect_stub(tmp_path, corrected, 0, corrected, 0)
    got = compliance._rt_contracts_errors_autocorrect(bin_, _autocorrect_spec())
    assert got.status == "fail", got
    assert "no autocorrect policy" in got.details


def test_autocorrect_applied_with_corrected_from_passes(tmp_path):
    bin_ = _autocorrect_stub(
        tmp_path,
        _USAGE_ENVELOPE,
        2,
        '{"code":"OK","message":"applied","corrected_from":"--formt","exit_code":0}',
        0,
    )
    got = compliance._rt_contracts_errors_autocorrect(bin_, _autocorrect_spec())
    assert got.status == "pass", got


def test_autocorrect_applied_without_corrected_from_fails(tmp_path):
    """The run silently became a different run, unauditable by a json caller."""
    bin_ = _autocorrect_stub(
        tmp_path, _USAGE_ENVELOPE, 2, '{"code":"OK","message":"applied","exit_code":0}', 0
    )
    got = compliance._rt_contracts_errors_autocorrect(bin_, _autocorrect_spec())
    assert got.status == "fail", got
    assert compliance.CORRECTED_FROM_FIELD in got.details


def test_autocorrect_plaintext_notice_counts(tmp_path):
    """The obligation is that it is DECLARED, not that it is declared as JSON."""
    bin_ = _autocorrect_stub(
        tmp_path, _USAGE_ENVELOPE, 2, "OK: applied\nCorrected from: --formt", 0
    )
    got = compliance._rt_contracts_errors_autocorrect(bin_, _autocorrect_spec())
    assert got.status == "pass", got


def test_aggregate_contracts_errors_warn_survives_a_skip():
    """A fix-less-envelope warn must not be collapsed into a pass."""
    base = compliance._warn(
        compliance.Factor.CONTRACTS_ERRORS,
        "structured error carries no recovery guidance",
        "populate suggested_fix",
    )
    arm = compliance._skip(compliance.Factor.CONTRACTS_ERRORS, "no corrections applied")
    got = compliance._aggregate_contracts_errors(base, arm)
    assert got.status == "warn", got
    assert got.details == base.details


def test_aggregate_contracts_errors_fail_beats_everything():
    got = compliance._aggregate_contracts_errors(
        compliance._pass(compliance.Factor.CONTRACTS_ERRORS, "fine"),
        compliance._fail(
            compliance.Factor.CONTRACTS_ERRORS, "corrected with no policy set", "keep it off"
        ),
    )
    assert got.status == "fail", got
    assert "no policy set" in got.details


def test_aggregate_contracts_errors_all_skip_dedupes():
    got = compliance._aggregate_contracts_errors(
        compliance._skip(compliance.Factor.CONTRACTS_ERRORS, "same reason"),
        compliance._skip(compliance.Factor.CONTRACTS_ERRORS, "same reason"),
    )
    assert got.status == "skip", got
    assert got.details == "same reason"


def test_aggregate_contracts_errors_both_pass():
    got = compliance._aggregate_contracts_errors(
        compliance._pass(compliance.Factor.CONTRACTS_ERRORS, "structured"),
        compliance._pass(compliance.Factor.CONTRACTS_ERRORS, "declared"),
    )
    assert got.status == "pass", got


@pytest.mark.parametrize(
    "text,want",
    [
        ('{"code":"OK","corrected_from":"--formt"}', True),
        ("code: OK\ncorrected_from: --formt\n", True),
        ("OK: applied\nCorrected from: --formt\n", True),
        ('{"level":"info"}\n{"corrected_from":"--formt"}\n', True),
        ('{"code":"OK","message":"applied"}', False),
        ('{"corrected_from":""}', False),
        ('{"corrected_from":"   "}', False),
        ("", False),
    ],
)
def test_correction_declared(text, want):
    assert compliance._correction_declared(text) is want

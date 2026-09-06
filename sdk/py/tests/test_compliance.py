"""Tests for the 12-factor AI CLI compliance checker."""

from __future__ import annotations

import json
import os
import tempfile

import pytest

from hop_top_kit.compliance import (
    CheckResult,
    Factor,
    Report,
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

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

"""Tests for hop_top_kit.telemetry.consent."""

from __future__ import annotations

from pathlib import Path

import pytest

from hop_top_kit.telemetry import consent as cons


@pytest.fixture(autouse=True)
def isolated_config_home(tmp_path: Path, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    yield tmp_path


def _write(p: Path, body: str) -> None:
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(body)


class TestLoadConsent:
    def test_missing_file_denied(self):
        c = cons.load_consent()
        assert c.allowed is False
        assert c == cons.Consent.denied()

    def test_malformed_yaml_denied(self):
        _write(cons.consent_path(), "telemetry:\n  consent: : : :\n")
        c = cons.load_consent()
        assert c.allowed is False

    def test_unknown_state_denied(self):
        _write(
            cons.consent_path(),
            "telemetry:\n  consent:\n    state: maybe\n    prompt_version: 1\n",
        )
        c = cons.load_consent()
        assert c.allowed is False

    def test_happy_granted(self):
        _write(
            cons.consent_path(),
            """\
telemetry:
  consent:
    state: granted
    decided_at: "2026-01-15T10:00:00Z"
    prompt_version: 3
    decision_source: prompt
""",
        )
        c = cons.load_consent()
        assert c.allowed is True
        assert c.prompt_version == 3
        assert c.decision_source == "prompt"
        assert c.decided_at == "2026-01-15T10:00:00Z"

    def test_happy_denied(self):
        _write(
            cons.consent_path(),
            """\
telemetry:
  consent:
    state: denied
    prompt_version: 1
    decision_source: flag
""",
        )
        c = cons.load_consent()
        assert c.allowed is False
        assert c.prompt_version == 1
        assert c.decision_source == "flag"

    def test_missing_consent_block_denied(self):
        _write(cons.consent_path(), "telemetry:\n  something_else: 1\n")
        assert cons.load_consent().allowed is False

    def test_missing_telemetry_block_denied(self):
        _write(cons.consent_path(), "unrelated: true\n")
        assert cons.load_consent().allowed is False

    def test_top_level_not_dict_denied(self):
        _write(cons.consent_path(), "- just\n- a\n- list\n")
        assert cons.load_consent().allowed is False

    def test_telemetry_not_dict_denied(self):
        _write(cons.consent_path(), "telemetry: scalar\n")
        assert cons.load_consent().allowed is False

    def test_consent_block_not_dict_denied(self):
        _write(cons.consent_path(), "telemetry:\n  consent: scalar\n")
        assert cons.load_consent().allowed is False

    def test_unreadable_int_prompt_version_defaults_zero(self):
        _write(
            cons.consent_path(),
            "telemetry:\n  consent:\n    state: granted\n    prompt_version: not-a-number\n",
        )
        c = cons.load_consent()
        # Granted survives; prompt_version coerces to 0 on failure.
        assert c.allowed is True
        assert c.prompt_version == 0

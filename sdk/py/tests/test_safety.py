"""Tests for hop_top_kit.safety — delegation safety guard (Factor 10)."""

from unittest import mock

import pytest

from hop_top_kit.safety import SafetyLevel, safety_guard


class TestReadLevel:
    def test_read_always_passes(self):
        safety_guard(SafetyLevel.READ)

    def test_read_passes_without_force(self):
        safety_guard(SafetyLevel.READ, force=False)


class TestDangerousLevel:
    def test_dangerous_raises_without_force(self):
        with pytest.raises(SystemExit):
            safety_guard(SafetyLevel.DANGEROUS, force=False)

    def test_dangerous_passes_with_force(self):
        safety_guard(SafetyLevel.DANGEROUS, force=True)


class TestCautionLevel:
    def test_caution_passes_in_tty_without_force(self):
        with mock.patch("sys.stdin") as m:
            m.isatty.return_value = True
            safety_guard(SafetyLevel.CAUTION, force=False)

    def test_caution_raises_in_non_tty_without_force(self):
        with mock.patch("sys.stdin") as m:
            m.isatty.return_value = False
            with pytest.raises(SystemExit):
                safety_guard(SafetyLevel.CAUTION, force=False)

    def test_caution_passes_in_non_tty_with_force(self):
        with mock.patch("sys.stdin") as m:
            m.isatty.return_value = False
            safety_guard(SafetyLevel.CAUTION, force=True)


class TestDangerousInNonTTY:
    def test_dangerous_requires_force_in_non_tty(self):
        with mock.patch("sys.stdin") as m:
            m.isatty.return_value = False
            with pytest.raises(SystemExit):
                safety_guard(SafetyLevel.DANGEROUS, force=False)

    def test_dangerous_passes_with_force_in_non_tty(self):
        with mock.patch("sys.stdin") as m:
            m.isatty.return_value = False
            safety_guard(SafetyLevel.DANGEROUS, force=True)


class TestEnumValues:
    def test_level_values(self):
        assert SafetyLevel.READ.value == "read"
        assert SafetyLevel.CAUTION.value == "caution"
        assert SafetyLevel.DANGEROUS.value == "dangerous"

"""Tests for hop_top_kit.errcorrect — corrective error model (Factor 4)."""

from hop_top_kit.errcorrect import CorrectedError


class TestCorrectedErrorCreation:
    def test_minimal_fields(self):
        err = CorrectedError(code="NOT_FOUND", message="mission not found")
        assert err.code == "NOT_FOUND"
        assert err.message == "mission not found"
        assert err.cause == ""
        assert err.fix == ""
        assert err.alternatives == []
        assert err.retryable is False

    def test_all_fields(self):
        err = CorrectedError(
            code="NOT_FOUND",
            message="mission not found",
            cause="typo in mission name",
            fix="use 'starlink-1' instead",
            alternatives=["starlink-1", "starlink-2"],
            retryable=True,
        )
        assert err.cause == "typo in mission name"
        assert err.fix == "use 'starlink-1' instead"
        assert err.alternatives == ["starlink-1", "starlink-2"]
        assert err.retryable is True

    def test_is_exception(self):
        err = CorrectedError(code="FAIL", message="boom")
        assert isinstance(err, Exception)


class TestToDict:
    def test_minimal(self):
        err = CorrectedError(code="NOT_FOUND", message="not found")
        d = err.to_dict()
        assert d["code"] == "NOT_FOUND"
        assert d["message"] == "not found"
        assert d["cause"] == ""
        assert d["fix"] == ""
        assert d["alternatives"] == []
        assert d["retryable"] is False

    def test_full(self):
        err = CorrectedError(
            code="AMBIGUOUS",
            message="multiple matches",
            cause="prefix matched several",
            fix="be more specific",
            alternatives=["a", "b"],
            retryable=True,
        )
        d = err.to_dict()
        assert d["alternatives"] == ["a", "b"]
        assert d["retryable"] is True

    def test_dict_does_not_mutate(self):
        err = CorrectedError(code="X", message="y", alternatives=["a"])
        d = err.to_dict()
        d["alternatives"].append("z")
        assert err.alternatives == ["a"]


class TestFormatTerminal:
    def test_contains_code_and_message(self):
        err = CorrectedError(code="NOT_FOUND", message="mission not found")
        out = err.format_terminal()
        assert "NOT_FOUND" in out
        assert "mission not found" in out

    def test_shows_cause_and_fix(self):
        err = CorrectedError(
            code="NOT_FOUND",
            message="not found",
            cause="bad name",
            fix="try again",
        )
        out = err.format_terminal()
        assert "bad name" in out
        assert "try again" in out

    def test_shows_alternatives(self):
        err = CorrectedError(
            code="NOT_FOUND",
            message="not found",
            alternatives=["alpha", "beta"],
        )
        out = err.format_terminal()
        assert "alpha" in out
        assert "beta" in out

    def test_no_color_strips_ansi(self):
        err = CorrectedError(
            code="FAIL",
            message="boom",
            cause="reason",
            fix="do this",
        )
        out = err.format_terminal(no_color=True)
        assert "\033[" not in out

    def test_omits_empty_sections(self):
        err = CorrectedError(code="X", message="y")
        out = err.format_terminal(no_color=True)
        assert "Cause" not in out
        assert "Fix" not in out
        assert "Alternatives" not in out

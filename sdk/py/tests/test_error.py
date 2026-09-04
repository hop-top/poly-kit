"""Tests for hop_top_kit.output.error — mirrors go/console/output/error_test.go."""

from __future__ import annotations

import io
import json

import pytest
import yaml

from hop_top_kit.output import error as err_mod
from hop_top_kit.output.error import (
    CODE_CONFLICT,
    CODE_GENERIC,
    CODE_NOT_FOUND,
    CODE_PROVENANCE_MISSING,
    CODE_RATE_LIMITED,
    CODE_TRANSIENT,
    CODE_UNAUTHORIZED,
    CODE_USAGE,
    EXIT_GENERIC,
    EXIT_PROVENANCE_MISSING,
    EXIT_RATE_LIMITED,
    EXIT_TRANSIENT,
    TRANSIENCE_PERMANENT,
    TRANSIENCE_TRANSIENT,
    TRANSIENCE_UNKNOWN,
    CLIError,
    conflict_error,
    generic_error,
    not_found_error,
    provenance_missing_error,
    rate_limited_error,
    render_error,
    transience_for_code,
    transient_error,
    unauthorized_error,
    usage_error,
    wrap_error,
)


@pytest.mark.parametrize(
    ("got", "want_code", "want_exit", "want_transience"),
    [
        (generic_error("boom"), CODE_GENERIC, 1, TRANSIENCE_PERMANENT),
        (not_found_error("nope"), CODE_NOT_FOUND, 3, TRANSIENCE_PERMANENT),
        (conflict_error("dup"), CODE_CONFLICT, 4, TRANSIENCE_PERMANENT),
        (unauthorized_error("nope"), CODE_UNAUTHORIZED, 5, TRANSIENCE_PERMANENT),
        (usage_error("bad flag"), CODE_USAGE, 2, TRANSIENCE_PERMANENT),
        (rate_limited_error("budget"), CODE_RATE_LIMITED, 64, TRANSIENCE_TRANSIENT),
        (transient_error("upstream timeout"), CODE_TRANSIENT, 6, TRANSIENCE_TRANSIENT),
        (
            provenance_missing_error("/email"),
            CODE_PROVENANCE_MISSING,
            65,
            TRANSIENCE_PERMANENT,
        ),
    ],
    ids=[
        "Generic",
        "NotFound",
        "Conflict",
        "Unauthorized",
        "Usage",
        "RateLimited",
        "Transient",
        "ProvenanceMissing",
    ],
)
def test_constructors_set_code_exit_transience(got, want_code, want_exit, want_transience):
    assert got.code == want_code
    assert got.exit_code == want_exit
    assert got.transience == want_transience


def test_exit_code_table_is_unique():
    # Mirrors the harness exit-code table: 0-6 core taxonomy + kit
    # extension band 64/65. Uniqueness pins the taxonomy.
    exits = {
        generic_error("m").exit_code: CODE_GENERIC,
        not_found_error("m").exit_code: CODE_NOT_FOUND,
        conflict_error("m").exit_code: CODE_CONFLICT,
        unauthorized_error("m").exit_code: CODE_UNAUTHORIZED,
        usage_error("m").exit_code: CODE_USAGE,
        transient_error("m").exit_code: CODE_TRANSIENT,
        rate_limited_error("m").exit_code: CODE_RATE_LIMITED,
        provenance_missing_error("m").exit_code: CODE_PROVENANCE_MISSING,
    }
    assert len(exits) == 8
    assert EXIT_GENERIC == 1
    assert EXIT_TRANSIENT == 6
    assert EXIT_RATE_LIMITED == 64
    assert EXIT_PROVENANCE_MISSING == 65
    assert exits[1] == CODE_GENERIC
    assert exits[6] == CODE_TRANSIENT
    assert exits[65] == CODE_PROVENANCE_MISSING


@pytest.mark.parametrize(
    ("code", "want"),
    [
        (CODE_USAGE, TRANSIENCE_PERMANENT),
        (CODE_NOT_FOUND, TRANSIENCE_PERMANENT),
        (CODE_CONFLICT, TRANSIENCE_PERMANENT),
        (CODE_UNAUTHORIZED, TRANSIENCE_PERMANENT),
        (CODE_PROVENANCE_MISSING, TRANSIENCE_PERMANENT),
        (CODE_RATE_LIMITED, TRANSIENCE_TRANSIENT),
        (CODE_TRANSIENT, TRANSIENCE_TRANSIENT),
        (CODE_GENERIC, TRANSIENCE_UNKNOWN),
        ("ADOPTER_SPECIFIC", TRANSIENCE_UNKNOWN),
        ("", TRANSIENCE_UNKNOWN),
    ],
)
def test_transience_for_code(code, want):
    assert transience_for_code(code) == want


def test_wrap_error_defaults_transience_from_code():
    base = ValueError("boom")
    assert wrap_error(base, CODE_CONFLICT, 4).transience == TRANSIENCE_PERMANENT
    assert wrap_error(base, CODE_RATE_LIMITED, 64).transience == TRANSIENCE_TRANSIENT
    assert wrap_error(base, CODE_GENERIC, 1).transience == TRANSIENCE_UNKNOWN


def test_wrap_error_retains_cause_and_none_passthrough():
    base = ValueError("boom")
    e = wrap_error(base, CODE_CONFLICT, 4)
    assert e.message == "boom"
    assert e.__cause__ is base
    assert wrap_error(None, CODE_GENERIC, 1) is None


def test_with_transience_copies_and_sets():
    orig = CLIError(code="SHARED", message="m", exit_code=9)
    got = orig.with_transience(TRANSIENCE_TRANSIENT)
    assert got is not orig
    assert got.transience == TRANSIENCE_TRANSIENT
    # Shared module-level envelopes must never be mutated in place.
    assert orig.transience == ""
    # Every other rendered field carries over.
    assert got.code == orig.code
    assert got.message == orig.message
    assert got.exit_code == orig.exit_code


def test_render_error_structured_always_carries_transience():
    # A literal built without transience must still render a valid
    # transience class in structured formats (spec Factor 4).
    buf = io.StringIO()
    render_error(buf, "json", CLIError(code="ADOPTER_SPECIFIC", message="m", exit_code=9))
    got = json.loads(buf.getvalue())
    assert got["transience"] == TRANSIENCE_UNKNOWN

    buf = io.StringIO()
    render_error(buf, "yaml", CLIError(code="ADOPTER_SPECIFIC", message="m", exit_code=9))
    assert "transience: unknown" in buf.getvalue()

    # An explicit class renders untouched.
    buf = io.StringIO()
    render_error(buf, "json", rate_limited_error("budget"))
    assert json.loads(buf.getvalue())["transience"] == TRANSIENCE_TRANSIENT


def test_render_error_does_not_mutate_input():
    e = CLIError(code="ADOPTER_SPECIFIC", message="m", exit_code=9)
    render_error(io.StringIO(), "json", e)
    assert e.transience == ""


def test_render_error_json_wire_round_trip():
    buf = io.StringIO()
    render_error(buf, "json", provenance_missing_error("/email"))
    got = json.loads(buf.getvalue())
    assert got == {
        "code": CODE_PROVENANCE_MISSING,
        "message": "provenance not recorded for one or more output fields",
        "cause": "/email",
        "suggested_fix": got["suggested_fix"],  # exact text is not contract
        "exit_code": 65,
        "transience": TRANSIENCE_PERMANENT,
    }
    # Empty optionals stay off the wire (omitempty parity).
    buf = io.StringIO()
    render_error(buf, "json", transient_error("upstream timeout"))
    got = json.loads(buf.getvalue())
    assert set(got) == {"code", "message", "exit_code", "transience"}
    assert got["exit_code"] == 6


def test_render_error_yaml_wire_round_trip():
    buf = io.StringIO()
    render_error(buf, "yaml", transient_error("upstream timeout"))
    got = yaml.safe_load(buf.getvalue())
    assert got == {
        "code": CODE_TRANSIENT,
        "message": "upstream timeout",
        "exit_code": 6,
        "transience": TRANSIENCE_TRANSIENT,
    }


def test_render_error_plain_and_none():
    buf = io.StringIO()
    render_error(
        buf,
        "table",
        CLIError(
            code="NOT_FOUND",
            message="missing thing",
            cause="root",
            suggested_fix="try --all",
            alternatives=["other"],
            exit_code=3,
        ),
    )
    assert buf.getvalue() == (
        "NOT_FOUND: missing thing\nCause: root\nFix: try --all\nAlternative: other\n"
    )
    buf = io.StringIO()
    render_error(buf, "json", None)
    assert buf.getvalue() == ""


def test_cli_error_is_exception():
    e = not_found_error("missing thing")
    assert isinstance(e, Exception)
    assert "NOT_FOUND" in str(e)
    assert "missing thing" in str(e)
    with pytest.raises(CLIError):
        raise e


def test_module_import_via_package():
    # Envelope surface is re-exported from hop_top_kit.output.
    from hop_top_kit import output

    assert output.CLIError is CLIError
    assert output.EXIT_TRANSIENT == err_mod.EXIT_TRANSIENT

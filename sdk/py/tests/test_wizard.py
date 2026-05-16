"""Tests for hop_top_kit.wizard — mirrors Go wizard_test.go."""

import pytest

from hop_top_kit.wizard import (
    ActionError,
    ActionRequest,
    ErrorAction,
    Step,
    StepKind,
    ValidationError,
    Wizard,
    action,
    confirm,
    multi_select,
    result_bool,
    result_choice,
    result_string,
    result_strings,
    select,
    summary,
    text_input,
)

# --- construction ---


class TestConstruction:
    def test_valid_steps(self):
        w = Wizard(text_input("name", "Name"), confirm("ok", "OK?"))
        assert w is not None

    def test_duplicate_key(self):
        with pytest.raises(ValueError, match="duplicate"):
            Wizard(text_input("x", "A"), text_input("x", "B"))

    def test_empty_key(self):
        with pytest.raises(ValueError, match="key must not be empty"):
            Wizard(Step(key="", kind=StepKind.TEXT_INPUT, label="Name"))

    def test_bad_default_confirm(self):
        with pytest.raises(ValueError, match="bool"):
            Wizard(confirm("ok", "OK?").with_default("nope"))

    def test_action_without_fn(self):
        with pytest.raises(ValueError, match="action_fn"):
            Wizard(Step(key="act", kind=StepKind.ACTION, label="do"))

    def test_select_without_options(self):
        with pytest.raises(ValueError, match="options"):
            Wizard(
                Step(
                    key="sel",
                    kind=StepKind.SELECT,
                    label="pick",
                )
            )

    def test_summary_auto_key(self):
        w = Wizard(text_input("a", "A"), summary("Review"))
        assert w.current().key == "a"


# --- advance ---


class TestAdvance:
    def test_text_input(self):
        w = Wizard(text_input("name", "Name"))
        res, err = w.advance("Alice")
        assert err is None
        assert res is None
        assert result_string(w.results(), "name") == "Alice"
        assert w.done() is True

    def test_select_valid(self):
        opts = [{"value": "a", "label": "A"}, {"value": "b", "label": "B"}]
        w = Wizard(select("color", "Color", opts))
        w.advance("a")
        assert result_choice(w.results(), "color") == "a"

    def test_select_invalid(self):
        opts = [{"value": "a", "label": "A"}, {"value": "b", "label": "B"}]
        w = Wizard(select("color", "Color", opts))
        _, err = w.advance("z")
        assert isinstance(err, ValidationError)
        assert "invalid option" in str(err)

    def test_confirm(self):
        w = Wizard(confirm("ok", "Sure?"))
        w.advance(True)
        assert result_bool(w.results(), "ok") is True

    def test_confirm_rejects_non_bool(self):
        w = Wizard(confirm("ok", "Sure?"))
        _, err = w.advance("yes")
        assert isinstance(err, ValidationError)

    def test_multi_select(self):
        opts = [{"value": "x", "label": "X"}, {"value": "y", "label": "Y"}]
        w = Wizard(multi_select("tags", "Tags", opts))
        w.advance(["x", "y"])
        assert result_strings(w.results(), "tags") == ["x", "y"]

    def test_multi_select_invalid(self):
        opts = [{"value": "x", "label": "X"}, {"value": "y", "label": "Y"}]
        w = Wizard(multi_select("tags", "Tags", opts))
        _, err = w.advance(["x", "nope"])
        assert isinstance(err, ValidationError)
        assert "nope" in str(err)

    def test_action_returns_request(self):
        called = False

        def fn(_results):
            nonlocal called
            called = True

        w = Wizard(action("act", "Go", fn))
        res, err = w.advance(None)
        assert err is None
        assert isinstance(res, ActionRequest)
        assert res.step_key == "act"

    def test_summary_advances(self):
        w = Wizard(text_input("a", "A"), summary("Review"))
        w.advance("val")
        assert w.current().kind == StepKind.SUMMARY
        w.advance(None)
        assert w.done() is True

    def test_required_text(self):
        w = Wizard(text_input("name", "Name").with_required())
        _, err = w.advance("")
        assert isinstance(err, ValidationError)
        assert "required" in str(err)

    def test_required_multi_select(self):
        opts = [{"value": "x", "label": "X"}]
        w = Wizard(multi_select("tags", "Tags", opts).with_required())
        _, err = w.advance([])
        assert isinstance(err, ValidationError)
        assert "required" in str(err)

    def test_custom_text_validator(self):
        def validator(s):
            if s == "bad":
                raise ValueError("invalid email")

        w = Wizard(text_input("email", "Email").with_validate_text(validator))
        _, err = w.advance("bad")
        assert isinstance(err, ValidationError)
        assert "invalid email" in str(err)


# --- resolve_action ---


class TestResolveAction:
    def test_advances_on_success(self):
        w = Wizard(action("a", "A", lambda r: None), text_input("b", "B"))
        w.advance(None)
        err = w.resolve_action(None)
        assert err is None
        assert w.current().key == "b"

    def test_abort(self):
        w = Wizard(action("a", "A", lambda r: None).with_on_error(ErrorAction.ABORT))
        w.advance(None)
        err = w.resolve_action(RuntimeError("boom"))
        assert isinstance(err, ActionError)
        assert err.action == ErrorAction.ABORT

    def test_retry(self):
        w = Wizard(action("a", "A", lambda r: None).with_on_error(ErrorAction.RETRY))
        w.advance(None)
        err = w.resolve_action(RuntimeError("transient"))
        assert err is None
        assert w.current().key == "a"

    def test_skip(self):
        w = Wizard(
            action("a", "A", lambda r: None).with_on_error(ErrorAction.SKIP),
            text_input("b", "B"),
        )
        w.advance(None)
        err = w.resolve_action(RuntimeError("meh"))
        assert err is None
        assert w.current().key == "b"


# --- back ---


class TestBack:
    def test_goes_to_previous(self):
        w = Wizard(text_input("a", "A"), text_input("b", "B"))
        w.advance("hello")
        assert w.current().key == "b"
        w.back()
        assert w.current().key == "a"
        assert result_string(w.results(), "a") == ""

    def test_noop_at_start(self):
        w = Wizard(text_input("a", "A"))
        w.back()
        assert w.current().key == "a"

    def test_skips_hidden(self):
        w = Wizard(
            confirm("show", "Show?"),
            text_input("hidden", "Hidden").with_when("show", lambda v: bool(v)),
            text_input("last", "Last"),
        )
        w.advance(False)
        assert w.current().key == "last"
        w.back()
        assert w.current().key == "show"


# --- conditional ---


class TestConditional:
    def test_shows_when_true(self):
        w = Wizard(
            confirm("advanced", "Advanced?"),
            text_input("extra", "Extra").with_when("advanced", lambda v: bool(v)),
        )
        w.advance(True)
        assert w.current().key == "extra"

    def test_skips_when_false(self):
        w = Wizard(
            confirm("advanced", "Advanced?"),
            text_input("extra", "Extra").with_when("advanced", lambda v: bool(v)),
        )
        w.advance(False)
        assert w.done() is True

    def test_clears_stale_results(self):
        w = Wizard(
            confirm("show", "Show?"),
            text_input("extra", "Extra").with_when("show", lambda v: bool(v)),
            text_input("last", "Last"),
        )
        w.advance(True)
        w.advance("filled")
        assert w.results()["extra"] == "filled"

        w.back()  # back to extra
        w.back()  # back to show
        w.advance(False)

        assert w.current().key == "last"
        assert "extra" not in w.results()


# --- done ---


class TestDone:
    def test_tracks_completion(self):
        w = Wizard(text_input("a", "A"))
        assert w.done() is False
        w.advance("val")
        assert w.done() is True


# --- stepCount / stepIndex ---


class TestStepCountIndex:
    def test_excludes_hidden(self):
        w = Wizard(
            confirm("show", "Show?"),
            text_input("hidden", "Hidden").with_when("show", lambda v: bool(v)),
            text_input("visible", "Visible"),
        )
        assert w.step_count() == 2
        w.advance(True)
        assert w.step_count() == 3

    def test_step_index(self):
        w = Wizard(
            text_input("a", "A"),
            text_input("b", "B"),
            text_input("c", "C"),
        )
        assert w.step_index() == 0
        w.advance("x")
        assert w.step_index() == 1
        w.advance("y")
        assert w.step_index() == 2


# --- complete / dryRun ---


class TestComplete:
    def test_calls_on_complete(self):
        w = Wizard(text_input("a", "A"))
        got = {}

        def cb(r):
            got.update(r)

        w.set_on_complete(cb)
        w.advance("done")
        w.complete()
        assert got["a"] == "done"

    def test_dry_run_skips(self):
        w = Wizard(text_input("a", "A"))
        called = False

        def cb(r):
            nonlocal called
            called = True

        w.set_on_complete(cb)
        w.set_dry_run(True)
        assert w.dry_run() is True
        w.advance("val")
        w.complete()
        assert called is False


# --- result accessors ---


class TestResultAccessors:
    results = {
        "name": "Alice",
        "ok": True,
        "tags": ["a", "b"],
        "color": "red",
        "bad": 42,
    }

    def test_string(self):
        assert result_string(self.results, "name") == "Alice"
        assert result_string(self.results, "missing") == ""
        assert result_string(self.results, "bad") == ""

    def test_bool(self):
        assert result_bool(self.results, "ok") is True
        assert result_bool(self.results, "missing") is False
        assert result_bool(self.results, "bad") is False

    def test_strings(self):
        assert result_strings(self.results, "tags") == ["a", "b"]
        assert result_strings(self.results, "missing") is None
        assert result_strings(self.results, "bad") is None

    def test_choice(self):
        assert result_choice(self.results, "color") == "red"

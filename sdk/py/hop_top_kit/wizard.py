"""Headless sequential wizard engine -- Python port of kit/wizard."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field
from enum import Enum
from typing import Any

# --- enums ---


class StepKind(Enum):
    TEXT_INPUT = "text_input"
    SELECT = "select"
    CONFIRM = "confirm"
    MULTI_SELECT = "multi_select"
    ACTION = "action"
    SUMMARY = "summary"


class ErrorAction(Enum):
    ABORT = 0
    RETRY = 1
    SKIP = 2


# --- types ---


@dataclass
class Option:
    value: str
    label: str
    description: str = ""


@dataclass
class Condition:
    key: str
    pred: Callable[[Any], bool]


# --- errors ---


class ValidationError(Exception):
    def __init__(self, step_key: str, msg: str):
        super().__init__(f'validation failed for "{step_key}": {msg}')
        self.step_key = step_key


class ActionError(Exception):
    def __init__(
        self,
        step_key: str,
        cause: Exception,
        action: ErrorAction,
    ):
        super().__init__(f'action "{step_key}" failed: {cause}')
        self.step_key = step_key
        self.cause = cause
        self.action = action


class ActionRequest:
    def __init__(
        self,
        step_key: str,
        run: Callable[[dict[str, Any]], Any],
    ):
        self.step_key = step_key
        self.run = run


# --- Step ---


@dataclass
class Step:
    key: str
    kind: StepKind
    label: str
    description: str = ""
    group: str = ""
    required: bool = False
    default_value: Any = None
    options: list[Option] = field(default_factory=list)
    validate_text_fn: Callable[[str], None] | None = None
    validate_choice_fn: Callable[[str], None] | None = None
    validate_choices_fn: Callable[[list[str]], None] | None = None
    when: Condition | None = None
    action_fn: Callable[[dict[str, Any]], Any] | None = None
    on_error: ErrorAction = ErrorAction.ABORT
    format_fn: Callable[[dict[str, Any]], str] | None = None

    # --- chainable modifiers (return new Step) ---

    def _clone(self, **kwargs: Any) -> Step:
        import dataclasses

        return dataclasses.replace(self, **kwargs)

    def with_required(self) -> Step:
        return self._clone(required=True)

    def with_default(self, v: Any) -> Step:
        return self._clone(default_value=v)

    def with_group(self, g: str) -> Step:
        return self._clone(group=g)

    def with_description(self, d: str) -> Step:
        return self._clone(description=d)

    def with_validate_text(self, fn: Callable[[str], None]) -> Step:
        return self._clone(validate_text_fn=fn)

    def with_validate_choice(self, fn: Callable[[str], None]) -> Step:
        return self._clone(validate_choice_fn=fn)

    def with_validate_choices(self, fn: Callable[[list[str]], None]) -> Step:
        return self._clone(validate_choices_fn=fn)

    def with_when(
        self,
        key: str,
        pred: Callable[[Any], bool],
    ) -> Step:
        return self._clone(when=Condition(key=key, pred=pred))

    def with_on_error(self, a: ErrorAction) -> Step:
        return self._clone(on_error=a)

    def with_format(self, fn: Callable[[dict[str, Any]], str]) -> Step:
        return self._clone(format_fn=fn)


# --- builders ---


def _to_option(o: dict[str, str] | Option) -> Option:
    if isinstance(o, Option):
        return o
    return Option(
        value=o["value"],
        label=o["label"],
        description=o.get("description", ""),
    )


def text_input(key: str, label: str) -> Step:
    return Step(key=key, kind=StepKind.TEXT_INPUT, label=label)


def select(
    key: str,
    label: str,
    options: list[dict[str, str] | Option],
) -> Step:
    return Step(
        key=key,
        kind=StepKind.SELECT,
        label=label,
        options=[_to_option(o) for o in options],
    )


def confirm(key: str, label: str) -> Step:
    return Step(key=key, kind=StepKind.CONFIRM, label=label)


def multi_select(
    key: str,
    label: str,
    options: list[dict[str, str] | Option],
) -> Step:
    return Step(
        key=key,
        kind=StepKind.MULTI_SELECT,
        label=label,
        options=[_to_option(o) for o in options],
    )


def action(
    key: str,
    label: str,
    fn: Callable[[dict[str, Any]], Any],
) -> Step:
    return Step(
        key=key,
        kind=StepKind.ACTION,
        label=label,
        action_fn=fn,
    )


def summary(label: str) -> Step:
    return Step(key="", kind=StepKind.SUMMARY, label=label)


# --- validation helpers ---


def _validate_default(s: Step) -> None:
    if s.default_value is None:
        return
    k = s.kind
    if k in (StepKind.TEXT_INPUT, StepKind.SELECT):
        if not isinstance(s.default_value, str):
            raise ValueError(f'step "{s.key}": default must be string for {k.value}')
    elif k == StepKind.CONFIRM:
        if not isinstance(s.default_value, bool):
            raise ValueError(f'step "{s.key}": default must be bool for confirm')
    elif k == StepKind.MULTI_SELECT and not (
        isinstance(s.default_value, list) and all(isinstance(v, str) for v in s.default_value)
    ):
        raise ValueError(f'step "{s.key}": default must be list[str] for multi_select')


def _is_valid_option(opts: list[Option], val: str) -> bool:
    return any(o.value == val for o in opts)


# --- Wizard ---


class Wizard:
    def __init__(self, *steps: Step):
        self._steps = list(steps)
        self._current = 0
        self._results: dict[str, Any] = {}
        self._done = False
        self._dry_run = False
        self._on_complete: Callable[[dict[str, Any]], Any] | None = None

        seen: set[str] = set()
        for i, s in enumerate(self._steps):
            # auto-generate key for Summary
            if s.kind == StepKind.SUMMARY and s.key == "":
                s.key = f"__summary_{i}"

            if s.key == "":
                raise ValueError(f"step at index {i}: key must not be empty")

            if s.key in seen:
                raise ValueError(f'duplicate step key "{s.key}"')
            seen.add(s.key)

            _validate_default(s)

            if s.kind == StepKind.ACTION and s.action_fn is None:
                raise ValueError(f'step "{s.key}": action kind must have action_fn')

            if (
                s.kind
                in (
                    StepKind.SELECT,
                    StepKind.MULTI_SELECT,
                )
                and not s.options
            ):
                raise ValueError(f'step "{s.key}": select/multi_select must have options')

    def current(self) -> Step | None:
        while self._current < len(self._steps):
            s = self._steps[self._current]
            if s.when is not None and not s.when.pred(self._results.get(s.when.key)):
                self._results.pop(s.key, None)  # clear stale
                self._current += 1
                continue
            return s
        self._done = True
        return None

    def advance(self, value: Any) -> tuple[ActionRequest | None, ValidationError | None]:
        s = self.current()
        if s is None:
            return None, None

        k = s.kind

        if k == StepKind.ACTION:
            return (
                ActionRequest(s.key, s.action_fn),
                None,
            )

        if k == StepKind.SUMMARY:
            self._do_advance()
            return None, None

        if k in (StepKind.TEXT_INPUT, StepKind.SELECT):
            if not isinstance(value, str):
                return None, ValidationError(s.key, "expected string")
            if s.required and value == "":
                return None, ValidationError(s.key, "required")
            if k == StepKind.TEXT_INPUT and s.validate_text_fn is not None:
                try:
                    s.validate_text_fn(value)
                except Exception as e:
                    return None, ValidationError(s.key, str(e))
            if k == StepKind.SELECT:
                if not _is_valid_option(s.options, value):
                    return None, ValidationError(
                        s.key,
                        f'invalid option "{value}"',
                    )
                if s.validate_choice_fn is not None:
                    try:
                        s.validate_choice_fn(value)
                    except Exception as e:
                        return None, ValidationError(s.key, str(e))
            self._results[s.key] = value

        elif k == StepKind.CONFIRM:
            if not isinstance(value, bool):
                return None, ValidationError(s.key, "expected bool")
            self._results[s.key] = value

        elif k == StepKind.MULTI_SELECT:
            if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
                return None, ValidationError(s.key, "expected list[str]")
            if s.required and len(value) == 0:
                return None, ValidationError(s.key, "required")
            for v in value:
                if not _is_valid_option(s.options, v):
                    return None, ValidationError(
                        s.key,
                        f'invalid option "{v}"',
                    )
            if s.validate_choices_fn is not None:
                try:
                    s.validate_choices_fn(value)
                except Exception as e:
                    return None, ValidationError(s.key, str(e))
            self._results[s.key] = value

        self._do_advance()
        return None, None

    def _do_advance(self) -> None:
        self._current += 1
        if self.current() is None:
            self._done = True

    def resolve_action(self, err: Exception | None) -> ActionError | None:
        s = self.current()
        if s is None:
            return None

        if err is None:
            self._do_advance()
            return None

        if s.on_error == ErrorAction.ABORT:
            return ActionError(s.key, err, ErrorAction.ABORT)
        if s.on_error == ErrorAction.RETRY:
            return None
        if s.on_error == ErrorAction.SKIP:
            self._do_advance()
            return None

        return ActionError(s.key, err, s.on_error)

    def back(self) -> None:
        self._done = False
        while self._current > 0:
            self._current -= 1
            s = self._steps[self._current]
            if s.when is not None and not s.when.pred(self._results.get(s.when.key)):
                continue
            self._results.pop(s.key, None)
            return

    def results(self) -> dict[str, Any]:
        return dict(self._results)

    def done(self) -> bool:
        return self._done

    def step_count(self) -> int:
        n = 0
        for s in self._steps:
            if s.when is not None and not s.when.pred(self._results.get(s.when.key)):
                continue
            n += 1
        return n

    def step_index(self) -> int:
        idx = 0
        for i in range(min(self._current, len(self._steps))):
            s = self._steps[i]
            if s.when is not None and not s.when.pred(self._results.get(s.when.key)):
                continue
            idx += 1
        return idx

    def set_dry_run(self, v: bool) -> None:
        self._dry_run = v

    def dry_run(self) -> bool:
        return self._dry_run

    def set_on_complete(self, fn: Callable[[dict[str, Any]], Any]) -> None:
        self._on_complete = fn

    def complete(self) -> None:
        if self._dry_run or self._on_complete is None:
            return
        self._on_complete(self._results)


# --- result accessors ---


def result_string(results: dict[str, Any], key: str) -> str:
    v = results.get(key)
    return v if isinstance(v, str) else ""


def result_bool(results: dict[str, Any], key: str) -> bool:
    v = results.get(key)
    return v if isinstance(v, bool) else False


def result_strings(results: dict[str, Any], key: str) -> list[str] | None:
    v = results.get(key)
    if isinstance(v, list) and all(isinstance(x, str) for x in v):
        return v
    return None


def result_choice(results: dict[str, Any], key: str) -> str:
    return result_string(results, key)

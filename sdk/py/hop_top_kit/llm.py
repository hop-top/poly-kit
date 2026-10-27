"""
hop_top_kit.llm — LLM provider abstraction layer.

Mirrors the Go ``llm`` package interfaces as Python Protocol classes,
provides a client facade via :func:`create_llm`, URI-based provider
resolution, YAML/env-var configuration, and a pluggable registry with
fallback support.

Provider adapters (e.g. LiteLLM) are registered as submodules via
entry-point plugins; this module contains only the core plumbing.
"""

from __future__ import annotations

import importlib
import json
import mimetypes
import os
import time
from collections.abc import Callable, Iterable, Iterator
from dataclasses import dataclass, field
from enum import StrEnum
from typing import Any, Protocol, runtime_checkable

__all__ = [  # noqa: RUF022 — grouped by category
    # Protocols
    "Completer",
    "Streamer",
    "ToolCaller",
    "ToolStreamer",
    "ToolStream",
    "Provider",
    # Data types
    "PartType",
    "MediaSource",
    "InlineSource",
    "URLSource",
    "FileSource",
    "ContentPart",
    "Message",
    "Request",
    "Response",
    "Usage",
    "Price",
    "Token",
    "ToolDef",
    "ToolCall",
    "ToolResponse",
    "ThinkingBlock",
    "ThinkingConfig",
    "CacheControl",
    "StreamOutcome",
    # URI + config
    "URI",
    "ProviderConfig",
    "ResolvedConfig",
    "parse_uri",
    "load_config",
    "apply_api_key",
    # Registry
    "register",
    "resolve",
    # Client
    "create_llm",
    "Client",
    # Router config
    "EvaConfig",
    "RouterConfig",
    "parse_router_config",
    "default_router_config",
    # Errors
    "LLMError",
    "ProviderNotFoundError",
    "CapabilityNotSupportedError",
    "AuthError",
    "PermissionDeniedError",
    "RateLimitError",
    "ModelError",
    "FallbackExhaustedError",
    "RouterUnavailableError",
    "RouterTimeoutError",
    "ContractViolationError",
    "ThresholdInvalidError",
    "ToolNameReason",
    "ToolNameError",
    "HTTPStatusError",
    "APIError",
    "TransportError",
    "StreamTruncatedError",
    "ResponseTooLargeError",
    "KeyEndpointError",
    "UnsupportedModalityError",
    "InvalidFormatError",
    "MediaTooLargeError",
    "is_fallbackable",
]


# ---------------------------------------------------------------------------
# Protocols
# ---------------------------------------------------------------------------


@runtime_checkable
class Completer(Protocol):
    def complete(self, req: Request) -> Response: ...


@runtime_checkable
class Streamer(Protocol):
    def stream(self, req: Request) -> Iterator[Token]: ...


@runtime_checkable
class ToolCaller(Protocol):
    def call_with_tools(self, req: Request, tools: list[ToolDef]) -> ToolResponse: ...


@runtime_checkable
class ToolStream(Protocol):
    """A token iterator whose finished turn can be read back.

    Iterating yields text (and, with :attr:`Request.thinking`, thinking)
    tokens, then one ``done`` token, then stops. :meth:`outcome` returns
    the folded turn once the ``done`` token was yielded, else ``None``;
    after an error it stays ``None``.
    """

    def __iter__(self) -> Iterator[Token]: ...

    def __next__(self) -> Token: ...

    def outcome(self) -> StreamOutcome | None: ...

    def close(self) -> None: ...


@runtime_checkable
class ToolStreamer(Protocol):
    """Streams a tool-calling completion: text deltas as tokens, then the
    whole turn (tool calls, stop reason, usage) from
    :meth:`ToolStream.outcome`."""

    def stream_with_tools(self, req: Request, tools: list[ToolDef]) -> ToolStream: ...


class Provider(Protocol):
    def close(self) -> None: ...


# ---------------------------------------------------------------------------
# Data types
# ---------------------------------------------------------------------------


class PartType(StrEnum):
    """Classifies a content part in a multimodal message."""

    TEXT = "text"
    IMAGE = "image"
    AUDIO = "audio"
    VIDEO = "video"


@runtime_checkable
class MediaSource(Protocol):
    """Binary media input for a :class:`ContentPart`.

    Implementations: :class:`InlineSource`, :class:`URLSource`,
    :class:`FileSource`.
    """

    @property
    def url(self) -> str:
        """Non-empty when the source is URL-backed."""
        ...

    @property
    def mime_type(self) -> str:
        """Inferred MIME type, or empty string."""
        ...

    def read(self) -> bytes:
        """Return the media bytes."""
        ...


@dataclass(frozen=True)
class InlineSource:
    """In-memory media bytes with an explicit MIME type."""

    data: bytes
    mime_type: str = ""

    @property
    def url(self) -> str:
        return ""

    def read(self) -> bytes:
        return self.data


@dataclass(frozen=True)
class URLSource:
    """URL-backed media; :meth:`read` downloads lazily.

    The download is guarded (:mod:`hop_top_kit.llm_media`): http or https
    only, public addresses only, no redirects, at most *max_bytes*.
    Adapters that can pass a URL to the provider do so without reading.
    """

    url: str
    max_bytes: int = 32 << 20

    @property
    def mime_type(self) -> str:
        return ""

    def read(self) -> bytes:
        from hop_top_kit.llm_media import fetch

        return fetch(self.url, max_bytes=self.max_bytes)


@dataclass(frozen=True)
class FileSource:
    """Media file on disk; MIME type inferred from the extension."""

    path: str

    @property
    def url(self) -> str:
        return ""

    @property
    def mime_type(self) -> str:
        if not os.path.splitext(self.path)[1]:
            return ""
        return mimetypes.guess_type(self.path)[0] or ""

    def read(self) -> bytes:
        with open(self.path, "rb") as fh:
            return fh.read()


@dataclass
class ContentPart:
    """A single typed element within a multimodal message.

    Rule: if a :class:`Message` has ``parts``, they take precedence over
    its ``content``.
    """

    type: PartType
    text: str = ""
    source: MediaSource | None = None
    mime_type: str = ""
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass
class Message:
    """A single role+content pair in a conversation.

    Multimodal rule: if ``parts`` is non-empty, it takes precedence over
    ``content``.

    Tool-call linkage: an agentic loop replays a tool round as two kinds
    of message:

    - role ``"assistant"`` with ``tool_calls``: the calls the model made,
      copied from :attr:`ToolResponse.tool_calls`. ``content`` carries any
      text the model produced alongside them and may be empty.
    - role ``"tool"`` with ``tool_call_id``: one tool's result, in
      ``content``. ``tool_call_id`` equals the :attr:`ToolCall.id` it
      answers. Send one message per call.

    ``tool_calls`` is valid only on role ``"assistant"`` and
    ``tool_call_id`` only on role ``"tool"``; adapters raise
    :class:`LLMError` for either field on any other role, a tool result
    carrying ``parts``, and linkage the provider cannot express, rather
    than degrading it to plain text. Messages that set neither field map
    exactly as before.
    """

    role: str
    content: str = ""
    parts: list[ContentPart] = field(default_factory=list)
    # Tool invocations of an assistant turn.
    tool_calls: list[ToolCall] = field(default_factory=list)
    # Links a role "tool" result to the ToolCall it answers.
    tool_call_id: str = ""
    # Replays an assistant turn's thinking blocks exactly as returned (in
    # Response.thinking, ToolResponse.thinking or StreamOutcome.thinking).
    # Required with tool use when thinking is enabled. Assistant only.
    thinking: list[ThinkingBlock] = field(default_factory=list)
    # A prompt-cache breakpoint after this message: on a system message
    # its text, otherwise its last content block.
    cache_control: CacheControl | None = None


@dataclass(frozen=True)
class ThinkingBlock:
    """One block of a model's extended thinking.

    ``signature`` verifies ``thinking``; a redacted block carries only
    ``redacted``, the provider's opaque data. Pass blocks back exactly as
    received.
    """

    thinking: str = ""
    signature: str = ""
    redacted: str = ""


@dataclass(frozen=True)
class CacheControl:
    """A prompt-cache breakpoint. ``ttl`` is ``""`` (the provider default,
    five minutes on Anthropic), ``"5m"`` or ``"1h"``."""

    ttl: str = ""


@dataclass(frozen=True)
class ThinkingConfig:
    """Enables extended thinking with a token budget, which must be below
    the request's ``max_tokens``."""

    budget_tokens: int


@dataclass
class Request:
    """All parameters of a completion call.

    ``temperature`` ``None`` leaves the provider default; any number,
    ``0.0`` included, is sent. ``max_tokens`` 0 means the provider
    default. ``thinking`` enables extended thinking where the provider
    has it; thinking deltas then stream as :attr:`Token.thinking`.
    """

    messages: list[Message]
    model: str = ""
    temperature: float | None = None
    max_tokens: int = 0
    stop_sequences: list[str] = field(default_factory=list)
    extensions: dict[str, Any] = field(default_factory=dict)
    thinking: ThinkingConfig | None = None


@dataclass
class Response:
    content: str
    role: str = ""
    usage: Usage | None = None
    finish_reason: str = ""
    # The turn's thinking blocks, to replay unchanged.
    thinking: list[ThinkingBlock] = field(default_factory=list)


@dataclass(frozen=True)
class Price:
    """A model's rate per million tokens, by kind."""

    input: float = 0.0
    output: float = 0.0
    cache_read: float = 0.0
    cache_write: float = 0.0


@dataclass
class Usage:
    """Token consumption.

    ``cache_read_tokens`` and ``cache_creation_tokens`` are input tokens
    read from or written to the prompt cache, apart from
    ``prompt_tokens``; ``total_tokens`` counts every input and output
    token, cache ones included.
    """

    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0
    cache_read_tokens: int = 0
    cache_creation_tokens: int = 0

    def cost(self, price: Price) -> float:
        """This usage's cost at *price*, cache reads and writes at their own rates."""
        return (
            self.prompt_tokens * price.input
            + self.completion_tokens * price.output
            + self.cache_read_tokens * price.cache_read
            + self.cache_creation_tokens * price.cache_write
        ) / 1e6


@dataclass
class Token:
    """A streaming chunk. ``thinking`` is a thinking delta, sent only when
    the request enabled :attr:`Request.thinking`; ``content`` is then
    empty."""

    content: str = ""
    done: bool = False
    thinking: str = ""


@dataclass
class ToolDef:
    """A tool the model may call. ``parameters`` is a JSON schema: a dict,
    or JSON text or bytes."""

    name: str
    description: str
    parameters: Any = None
    # A prompt-cache breakpoint after this tool.
    cache_control: CacheControl | None = None


@dataclass
class ToolCall:
    """A single tool invocation returned by the model.

    ``id`` is the handle a tool result quotes back in
    :attr:`Message.tool_call_id`. ``arguments`` is the JSON arguments
    object: a JSON string or bytes, or a JSON-serializable value; empty
    means ``{}``.
    """

    id: str
    name: str
    arguments: Any = None


@dataclass
class ToolResponse:
    content: str = ""
    tool_calls: list[ToolCall] = field(default_factory=list)
    # The turn's thinking blocks, to replay unchanged.
    thinking: list[ThinkingBlock] = field(default_factory=list)
    finish_reason: str = ""
    usage: Usage | None = None


@dataclass
class StreamOutcome:
    """What a finished stream folded into. Replay ``thinking`` on the
    assistant message, unchanged, in a follow-up turn."""

    content: str = ""
    thinking: list[ThinkingBlock] = field(default_factory=list)
    tool_calls: list[ToolCall] = field(default_factory=list)
    finish_reason: str = ""
    usage: Usage = field(default_factory=Usage)


# ---------------------------------------------------------------------------
# URI + Config
# ---------------------------------------------------------------------------


@dataclass
class URI:
    scheme: str
    model: str
    host: str = ""
    params: dict[str, str] = field(default_factory=dict)


@dataclass
class ProviderConfig:
    api_key: str = ""
    base_url: str = ""
    model: str = ""
    params: dict[str, str] = field(default_factory=dict)
    extras: dict[str, Any] = field(default_factory=dict)


@dataclass
class ResolvedConfig:
    uri: URI
    provider: ProviderConfig
    fallbacks: list[str] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class LLMError(Exception):
    """Base error for the LLM subsystem."""


class ProviderNotFoundError(LLMError):
    def __init__(self, scheme: str) -> None:
        self.scheme = scheme
        super().__init__(f"provider not found for scheme {scheme!r}")


class CapabilityNotSupportedError(LLMError):
    def __init__(self, capability: str, provider: str) -> None:
        self.capability = capability
        self.provider = provider
        super().__init__(f"capability {capability!r} not supported by {provider!r}")


class AuthError(LLMError):
    """The provider refused the credential (HTTP 401). A 403 is the
    narrower :class:`PermissionDeniedError`, still an ``AuthError``.
    ``detail`` is the provider's error text, credentials scrubbed."""

    _what = "authentication error"

    def __init__(self, provider: str, detail: str = "") -> None:
        self.provider = provider
        self.detail = detail
        msg = f"{self._what} for provider {provider!r}"
        super().__init__(f"{msg}: {detail}" if detail else msg)


class PermissionDeniedError(AuthError):
    """The credential was accepted but may not do what was asked (HTTP
    403, ``permission_error``): no access to the model, workspace or
    feature. A subclass of :class:`AuthError`, so ``except AuthError``
    still catches it; catch this first to tell the two apart."""

    _what = "permission denied"


class RateLimitError(LLMError):
    def __init__(self, provider: str, retry_after: float = 0.0) -> None:
        self.provider = provider
        self.retry_after = retry_after
        super().__init__(f"rate limit hit for provider {provider!r} (retry after {retry_after}s)")


class ModelError(LLMError):
    def __init__(self, model: str, provider: str) -> None:
        self.model = model
        self.provider = provider
        super().__init__(f"model {model!r} not available on provider {provider!r}")


class FallbackExhaustedError(LLMError):
    def __init__(self, errors: list[Exception]) -> None:
        self.errors = errors
        super().__init__(f"all providers exhausted ({len(errors)} errors)")


class RouterUnavailableError(LLMError):
    def __init__(self, router: str, err: Exception | None = None) -> None:
        self.router = router
        self.err = err
        super().__init__(f"router {router!r} unavailable: {err}")


class RouterTimeoutError(LLMError):
    def __init__(self, router: str, timeout: float) -> None:
        self.router = router
        self.timeout = timeout
        super().__init__(f"router {router!r} timed out after {timeout}s")


class ContractViolationError(LLMError):
    def __init__(self, contract: str, violations: list[str]) -> None:
        self.contract = contract
        self.violations = violations
        super().__init__(f"contract {contract!r} violated: {'; '.join(violations)}")


class ThresholdInvalidError(LLMError):
    def __init__(self, threshold: float) -> None:
        self.threshold = threshold
        super().__init__(f"threshold {threshold:.4f} out of [0,1] range")


class ToolNameReason(StrEnum):
    """Why a :class:`ToolNameError` fails a request."""

    EMPTY = "empty"
    INVALID_UTF8 = "invalid_utf8"
    INVALID = "invalid"
    DUPLICATE = "duplicate"
    COLLISION = "collision"


class ToolNameError(LLMError):
    """A tool definition's name fails the request before anything is sent.

    ``reason`` is a :class:`ToolNameReason` value; ``other`` names the
    earlier tool a ``collision`` maps to the same wire name. Not
    fallbackable: the caller's tool set needs fixing. See
    :mod:`hop_top_kit.llm_toolnames`.
    """

    def __init__(self, provider: str, reason: str, name: str, other: str = "") -> None:
        self.provider = provider
        self.reason = reason
        self.name = name
        self.other = other
        super().__init__(self._message())

    def _message(self) -> str:
        p, n = self.provider, self.name
        if self.reason == ToolNameReason.EMPTY:
            return f"tool name is empty (provider {p!r})"
        if self.reason == ToolNameReason.INVALID_UTF8:
            return f"tool name {n!r} is not valid UTF-8 (provider {p!r})"
        if self.reason == ToolNameReason.INVALID:
            return f"tool name {n!r} is not valid for provider {p!r}"
        if self.reason == ToolNameReason.DUPLICATE:
            return f"tool name {n!r} is defined twice (provider {p!r})"
        if self.reason == ToolNameReason.COLLISION:
            return (
                f"tool names {self.other!r} and {n!r} map to the same provider name "
                f"(provider {p!r})"
            )
        return f"tool name {n!r}: {self.reason} (provider {p!r})"


class HTTPStatusError(LLMError):
    """An HTTP status the provider answered with: a 5xx (the server
    failed; ``overloaded`` is 529) or a 3xx (redirects are not followed).
    Fallbackable for 5xx only."""

    def __init__(self, status_code: int, status: str) -> None:
        self.status_code = status_code
        self.status = status
        super().__init__(f"http {status_code}: {status}")


class APIError(LLMError):
    """A 4xx the provider answered with that no other error names: the
    request needs fixing. Not fallbackable."""

    def __init__(self, provider: str, status_code: int, error_type: str, message: str) -> None:
        self.provider = provider
        self.status_code = status_code
        self.error_type = error_type
        self.message = message
        kind = f"{error_type}: " if error_type else ""
        super().__init__(f"{provider}: http {status_code}: {kind}{message}")


class TransportError(LLMError):
    """The request did not complete at the transport level (connection
    refused or reset, timeout, TLS failure). The message names the
    destination without userinfo or query. Fallbackable."""


class StreamTruncatedError(LLMError):
    """A stream ended before its final event: the response is incomplete."""

    def __init__(self, provider: str) -> None:
        self.provider = provider
        super().__init__(f"stream truncated (provider {provider!r}): ended before its final event")


class ResponseTooLargeError(LLMError):
    """A response outgrew its limit. ``what`` is the part that overran:
    ``"body"`` (a whole non-streamed body) or ``"event"`` (one SSE
    event)."""

    def __init__(self, provider: str, what: str, limit: int) -> None:
        self.provider = provider
        self.what = what
        self.limit = limit
        super().__init__(f"response {what} exceeds {limit} bytes (provider {provider!r})")


class KeyEndpointError(LLMError):
    """A provider key kit will not send to an endpoint.

    ``endpoint`` is masked by :func:`hop_top_kit.llm_endpoint.redact_uri`;
    the key never appears. ``insecure``: plain http to a remote host.
    ``malformed``: the endpoint carries userinfo, a backslash, a
    percent-escape, whitespace or a control character, or does not
    parse. Neither: the endpoint came from the URI and is not trusted.
    """

    def __init__(
        self, scheme: str, endpoint: str, *, insecure: bool = False, malformed: bool = False
    ) -> None:
        self.scheme = scheme
        self.endpoint = endpoint
        self.insecure = insecure
        self.malformed = malformed
        super().__init__(self._message())

    def _message(self) -> str:
        s, e = self.scheme, _go_quote(self.endpoint)
        if self.malformed:
            return (
                f"llm: refusing to send the {s} API key to {e}: an endpoint must parse and "
                "carry no userinfo, backslash, percent-escape, whitespace or control character"
            )
        if self.insecure:
            return (
                f"llm: refusing to send the {s} API key over plain http to {e}; "
                "use https or a loopback host"
            )
        return (
            f"llm: refusing to send the {s} API key to {e}, which only the URI names; "
            "set it as llm.yaml base_url or LLM_BASE_URL, or pass api_key in the URI"
        )


class UnsupportedModalityError(LLMError):
    def __init__(self, modality: str, provider: str) -> None:
        self.modality = modality
        self.provider = provider
        super().__init__(f"modality {modality!r} not supported by provider {provider!r}")


class MediaTooLargeError(LLMError):
    def __init__(self, size: int, limit: int, provider: str) -> None:
        self.size = size
        self.limit = limit
        self.provider = provider
        super().__init__(f"media too large ({size} bytes, limit {limit}) for provider {provider!r}")


class InvalidFormatError(LLMError):
    def __init__(self, fmt: str, provider: str) -> None:
        self.format = fmt
        self.provider = provider
        super().__init__(f"invalid format {fmt!r} for provider {provider!r}")


def _schema_object(parameters: Any) -> dict[str, Any] | None:
    """A tool's parameters as a JSON object (from a dict, or JSON text or
    bytes), or ``None`` when absent or not an object."""
    v = parameters
    if isinstance(v, (str, bytes, bytearray)):
        try:
            v = json.loads(v) if v else None
        except ValueError:
            return None
    return dict(v) if isinstance(v, dict) else None


def _go_quote(s: str) -> str:
    """*s* double-quoted as Go's ``%q`` quotes it, for messages shared
    with the other ports."""
    return json.dumps(s, ensure_ascii=False)


def is_fallbackable(err: Exception) -> bool:
    """Return True if *err* should trigger a fallback attempt.

    Rate limits, router failures, transport failures and 5xx statuses
    (``overloaded`` among them) are fallbackable. Errors not listed here,
    :class:`AuthError`, :class:`ModelError`, :class:`APIError`,
    :class:`ToolNameError` and :class:`KeyEndpointError` among them, are
    not.
    """
    if isinstance(err, HTTPStatusError):
        return err.status_code >= 500
    return isinstance(
        err, (RateLimitError, RouterUnavailableError, RouterTimeoutError, TransportError)
    )


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------

Factory = Callable[[ResolvedConfig], Any]

_registry: dict[str, Factory] = {}
# Endpoints each scheme's adapter declares: where a key kit resolved
# itself may go besides loopback, llm.yaml base_url and LLM_BASE_URL.
_endpoints: dict[str, tuple[str, ...]] = {}

# Built-in adapters, imported the first time their scheme is needed so
# importing this module stays light. Each registers its schemes.
_BUILTIN_ADAPTERS: dict[str, str] = {
    "anthropic": "hop_top_kit.llm_anthropic",
    **{
        s: "hop_top_kit.llm_openai"
        for s in (
            "openai",
            "openrouter",
            "xai",
            "lmstudio",
            "groq",
            "together",
            "fireworks",
            "deepseek",
            "mistral",
        )
    },
}


def register(scheme: str, factory: Factory, *, endpoints: Iterable[str] = ()) -> None:
    """Register a provider factory for *scheme*.

    *endpoints* are the API base URLs the adapter talks to by default; a
    key kit resolves from llm.yaml or the environment may go to an
    endpoint the URI names only when it shares an origin with one of
    them (or is loopback, llm.yaml ``base_url`` or ``LLM_BASE_URL``).

    Raises :class:`LLMError` if *scheme* is already registered.
    """
    if scheme in _registry:
        raise LLMError(f"scheme {scheme!r} already registered")
    _registry[scheme] = factory
    _endpoints[scheme] = tuple(endpoints)


def _ensure_builtin(scheme: str) -> None:
    """Import the built-in adapter serving *scheme*, if any and not yet
    registered."""
    mod = _BUILTIN_ADAPTERS.get(scheme)
    if mod is not None and scheme not in _registry:
        # Registering again covers a registry cleared after the import.
        importlib.import_module(mod)._register()


def declared_endpoints(scheme: str) -> tuple[str, ...]:
    """The endpoints *scheme*'s adapter declared at registration."""
    _ensure_builtin(scheme)
    return _endpoints.get(scheme, ())


def resolve(uri: str) -> Any:
    """Resolve a URI to a provider instance via the registry."""
    return _resolve_from_config(load_config(uri))


# ---------------------------------------------------------------------------
# Client facade
# ---------------------------------------------------------------------------


class Client:
    """Unified LLM client with fallback, hooks, and capability detection."""

    def __init__(
        self,
        provider: Any,
        fallback_providers: list[Any] | None = None,
        *,
        on_request: Callable[[Request], None] | None = None,
        on_response: Callable[[Response, float], None] | None = None,
        on_error: Callable[[Exception], None] | None = None,
        on_fallback: Callable[[int, int, Exception], None] | None = None,
        on_route: Callable[[str, float, str], None] | None = None,
        on_eva_result: Callable[[str, bool, list[str]], None] | None = None,
    ) -> None:
        self._provider = provider
        self._fallbacks = fallback_providers or []
        self._on_request = on_request
        self._on_response = on_response
        self._on_error = on_error
        self._on_fallback = on_fallback
        self._on_route = on_route
        self._on_eva_result = on_eva_result

    @property
    def provider(self) -> Any:
        """The primary provider instance."""
        return self._provider

    def capabilities(self) -> list[str]:
        """Return capability names supported by the primary provider."""
        caps: list[str] = []
        if isinstance(self._provider, Completer):
            caps.append("complete")
        if isinstance(self._provider, Streamer):
            caps.append("stream")
        if isinstance(self._provider, ToolCaller):
            caps.append("call_with_tools")
        if isinstance(self._provider, ToolStreamer):
            caps.append("stream_with_tools")
        return caps

    def complete(self, req: Request) -> Response:
        """Complete using primary provider, falling back on failure."""
        if self._on_request:
            self._on_request(req)

        providers = [self._provider, *self._fallbacks]
        errors: list[Exception] = []

        for i, prov in enumerate(providers):
            if not isinstance(prov, Completer):
                exc = CapabilityNotSupportedError("complete", type(prov).__name__)
                errors.append(exc)
                if i < len(providers) - 1 and self._on_fallback:
                    self._on_fallback(i, i + 1, exc)
                continue
            try:
                t0 = time.monotonic()
                resp = prov.complete(req)
                elapsed = time.monotonic() - t0
                if self._on_response:
                    self._on_response(resp, elapsed)
                return resp
            except Exception as exc:
                errors.append(exc)
                if self._on_error:
                    self._on_error(exc)
                if not is_fallbackable(exc):
                    raise
                # Fire fallback hook before trying next.
                if i < len(providers) - 1 and self._on_fallback:
                    self._on_fallback(i, i + 1, exc)
                continue

        raise FallbackExhaustedError(errors)

    def stream(self, req: Request) -> Iterator[Token]:
        """Stream tokens, falling back if provider lacks capability."""
        providers = [self._provider, *self._fallbacks]
        errors: list[Exception] = []

        for i, prov in enumerate(providers):
            if not isinstance(prov, Streamer):
                exc = CapabilityNotSupportedError("stream", type(prov).__name__)
                errors.append(exc)
                if i < len(providers) - 1 and self._on_fallback:
                    self._on_fallback(i, i + 1, exc)
                continue
            return prov.stream(req)

        raise CapabilityNotSupportedError("stream", type(self._provider).__name__)

    def call_with_tools(self, req: Request, tools: list[ToolDef]) -> ToolResponse:
        """Call with tools, falling back if provider lacks capability."""
        providers = [self._provider, *self._fallbacks]
        errors: list[Exception] = []

        for i, prov in enumerate(providers):
            if not isinstance(prov, ToolCaller):
                exc = CapabilityNotSupportedError("call_with_tools", type(prov).__name__)
                errors.append(exc)
                if i < len(providers) - 1 and self._on_fallback:
                    self._on_fallback(i, i + 1, exc)
                continue
            return prov.call_with_tools(req, tools)

        raise CapabilityNotSupportedError("call_with_tools", type(self._provider).__name__)

    def stream_with_tools(self, req: Request, tools: list[ToolDef]) -> ToolStream:
        """Stream a tool-calling turn, falling back if a provider lacks the
        capability."""
        for prov in [self._provider, *self._fallbacks]:
            if isinstance(prov, ToolStreamer):
                return prov.stream_with_tools(req, tools)
        raise CapabilityNotSupportedError("stream_with_tools", type(self._provider).__name__)

    def close(self) -> None:
        """Close primary and fallback providers."""
        if hasattr(self._provider, "close"):
            self._provider.close()
        for fb in self._fallbacks:
            if hasattr(fb, "close"):
                fb.close()


# ---------------------------------------------------------------------------
# Router config
# ---------------------------------------------------------------------------


@dataclass
class EvaConfig:
    """Evaluation/contract enforcement settings for RouteLLM."""

    contracts: list[str] = field(default_factory=list)
    enforce: bool = False


@dataclass
class RouterConfig:
    """RouteLLM router configuration extracted from provider extras."""

    base_url: str = "http://localhost:6060"
    grpc_port: int = 6061
    strong_model: str = ""
    weak_model: str = ""
    routers: list[str] = field(default_factory=list)
    router_config: dict[str, Any] = field(default_factory=dict)
    eva: EvaConfig = field(default_factory=EvaConfig)
    autostart: bool = False
    pid_file: str = ""


def default_router_config() -> RouterConfig:
    """Return a :class:`RouterConfig` with sensible defaults."""
    return RouterConfig()


def parse_router_config(extras: dict[str, Any]) -> RouterConfig:
    """Extract RouteLLM config from a provider's *extras* map.

    Looks for a ``routellm`` key in *extras*. Missing keys fall back
    to defaults. Environment variables override all other sources:

    - ``ROUTELLM_BASE_URL``
    - ``ROUTELLM_STRONG_MODEL``
    - ``ROUTELLM_WEAK_MODEL``
    - ``ROUTELLM_ROUTERS`` (comma-separated)
    """
    cfg = default_router_config()

    raw = extras.get("routellm")
    if raw is not None:
        if not isinstance(raw, dict):
            raise LLMError(f"routellm: expected dict, got {type(raw).__name__}")
        cfg.base_url = raw.get("base_url", cfg.base_url)
        cfg.grpc_port = int(raw.get("grpc_port", cfg.grpc_port))
        cfg.strong_model = raw.get("strong_model", cfg.strong_model)
        cfg.weak_model = raw.get("weak_model", cfg.weak_model)
        cfg.routers = raw.get("routers", cfg.routers)
        cfg.router_config = raw.get("router_config", cfg.router_config)
        cfg.autostart = bool(raw.get("autostart", cfg.autostart))
        cfg.pid_file = raw.get("pid_file", cfg.pid_file)

        eva_raw = raw.get("eva")
        if isinstance(eva_raw, dict):
            cfg.eva = EvaConfig(
                contracts=eva_raw.get("contracts", []),
                enforce=bool(eva_raw.get("enforce", False)),
            )

    # Environment variable overrides.
    if v := os.environ.get("ROUTELLM_BASE_URL"):
        cfg.base_url = v
    if v := os.environ.get("ROUTELLM_STRONG_MODEL"):
        cfg.strong_model = v
    if v := os.environ.get("ROUTELLM_WEAK_MODEL"):
        cfg.weak_model = v
    if v := os.environ.get("ROUTELLM_ROUTERS"):
        cfg.routers = [r.strip() for r in v.split(",") if r.strip()]

    return cfg


def create_llm(
    uri: str,
    *,
    fallback: list[str] | None = None,
    on_request: Callable[[Request], None] | None = None,
    on_response: Callable[[Response, float], None] | None = None,
    on_error: Callable[[Exception], None] | None = None,
    on_fallback: Callable[[int, int, Exception], None] | None = None,
    on_route: Callable[[str, float, str], None] | None = None,
    on_eva_result: Callable[[str, bool, list[str]], None] | None = None,
) -> Client:
    """Create a :class:`Client` from a provider URI with optional fallbacks.

    Args:
        uri: Provider URI (e.g. ``"openai://gpt-4"``).
        fallback: Optional list of fallback provider URIs.
        on_request: Hook called before each request.
        on_response: Hook called after a successful response.
        on_error: Hook called when a provider raises.
        on_fallback: Hook called when falling back ``(from_idx, to_idx, error)``.
        on_route: Hook called after a routing decision ``(router, score, model)``.
        on_eva_result: Hook called after eva evaluation ``(contract, passed, violations)``.

    Returns:
        A configured :class:`Client`.
    """
    cfg = load_config(uri)
    primary = _resolve_from_config(cfg)

    fallback_uris = cfg.fallbacks if fallback is None else fallback
    fallback_providers = []
    for fb_uri in fallback_uris:
        fb_cfg = load_config(fb_uri)
        fallback_providers.append(_resolve_from_config(fb_cfg))

    return Client(
        primary,
        fallback_providers,
        on_request=on_request,
        on_response=on_response,
        on_error=on_error,
        on_fallback=on_fallback,
        on_route=on_route,
        on_eva_result=on_eva_result,
    )


def _resolve_from_config(cfg: ResolvedConfig) -> Any:
    """Resolve a provider from a :class:`ResolvedConfig`."""
    _ensure_builtin(cfg.uri.scheme)
    factory = _registry.get(cfg.uri.scheme)
    if factory is None:
        raise ProviderNotFoundError(cfg.uri.scheme)
    return factory(cfg)


# URI parsing and configuration live in hop_top_kit.llm_config, which
# needs the types and errors above; re-exported here as before.
from hop_top_kit.llm_config import (  # noqa: E402
    apply_api_key,
    load_config,
    parse_uri,
)

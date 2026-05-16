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

import os
import time
from collections.abc import Callable, Iterator
from dataclasses import dataclass, field
from typing import Any, Protocol, runtime_checkable
from urllib.parse import parse_qs, urlparse

import yaml

from hop_top_kit.xdg import config_dir

__all__ = [  # noqa: RUF022 — grouped by category
    # Protocols
    "Completer",
    "Streamer",
    "ToolCaller",
    "Provider",
    # Data types
    "Message",
    "Request",
    "Response",
    "Usage",
    "Token",
    "ToolDef",
    "ToolCall",
    "ToolResponse",
    # URI + config
    "URI",
    "ProviderConfig",
    "ResolvedConfig",
    "parse_uri",
    "load_config",
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
    "RateLimitError",
    "ModelError",
    "FallbackExhaustedError",
    "RouterUnavailableError",
    "RouterTimeoutError",
    "ContractViolationError",
    "ThresholdInvalidError",
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


class Provider(Protocol):
    def close(self) -> None: ...


# ---------------------------------------------------------------------------
# Data types
# ---------------------------------------------------------------------------


@dataclass
class Message:
    role: str
    content: str


@dataclass
class Request:
    messages: list[Message]
    model: str = ""
    temperature: float = 0.0
    max_tokens: int = 0
    stop_sequences: list[str] = field(default_factory=list)
    extensions: dict[str, Any] = field(default_factory=dict)


@dataclass
class Response:
    content: str
    role: str = ""
    usage: Usage | None = None
    finish_reason: str = ""


@dataclass
class Usage:
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0


@dataclass
class Token:
    content: str = ""
    done: bool = False


@dataclass
class ToolDef:
    name: str
    description: str
    parameters: Any = None


@dataclass
class ToolCall:
    id: str
    name: str
    arguments: Any = None


@dataclass
class ToolResponse:
    content: str = ""
    tool_calls: list[ToolCall] = field(default_factory=list)


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


def parse_uri(raw: str) -> URI:
    """Parse a provider URI like ``openai://gpt-4?temperature=0.5``.

    Supports optional host (``ollama://localhost:11434/llama3``) and
    slash-containing model names (``openai://org/model``).
    """
    if not raw or "://" not in raw:
        raise LLMError(f"invalid LLM URI (missing scheme): {raw!r}")

    parsed = urlparse(raw)
    scheme = parsed.scheme
    if not scheme:
        raise LLMError(f"invalid LLM URI (empty scheme): {raw!r}")

    # Determine host vs model.
    # urlparse puts everything after :// into netloc when there's no path,
    # or splits netloc/path when a slash follows a host-like segment.
    host = ""
    model = ""

    try:
        port = parsed.port
    except ValueError:
        # Non-integer port (e.g. "mf:0.7") — not a real host:port.
        port = None

    # Heuristic: if there's a valid integer port, netloc is a real host.
    if port:
        hostname = parsed.hostname or ""
        host = f"{hostname}:{port}"
        model = parsed.path.lstrip("/")
    else:
        # No valid port — treat raw netloc + path as the model.
        # Use parsed.netloc (not parsed.hostname) to preserve
        # segments like "mf:0.7" that urlparse splits incorrectly.
        full = parsed.netloc or ""
        if parsed.path:
            full += parsed.path
        model = full.lstrip("/")

    # Query params — flatten single-value lists.
    params: dict[str, str] = {}
    for k, v_list in parse_qs(parsed.query).items():
        params[k] = v_list[0] if v_list else ""

    return URI(scheme=scheme, model=model, host=host, params=params)


def load_config(uri: str = "") -> ResolvedConfig:
    """Load LLM configuration from env vars, YAML file, or explicit *uri*.

    Resolution order (first wins per field):
    1. Explicit *uri* argument
    2. ``LLM_PROVIDER`` / ``LLM_API_KEY`` / ``LLM_BASE_URL`` / ``LLM_FALLBACK``
    3. ``{xdg.config_dir("hop")}/llm.yaml``
    """
    # Defaults from YAML file.
    yaml_data = _load_yaml_config()

    raw_uri = uri or os.environ.get("LLM_PROVIDER", "") or yaml_data.get("default", "")
    if not raw_uri:
        raise LLMError("no LLM provider configured (set LLM_PROVIDER env or provide a URI)")

    parsed = parse_uri(raw_uri)

    # Look up per-provider config from YAML ``providers`` dict.
    providers_map: dict[str, Any] = yaml_data.get("providers", {}) or {}
    prov_section = providers_map.get(parsed.scheme, {}) or {}

    api_key = os.environ.get("LLM_API_KEY", "") or prov_section.get("api_key", "")
    base_url = os.environ.get("LLM_BASE_URL", "") or prov_section.get("base_url", "")

    fallback_env = os.environ.get("LLM_FALLBACK", "")
    if fallback_env:
        fallbacks = [f.strip() for f in fallback_env.split(",") if f.strip()]
    else:
        fallbacks = yaml_data.get("fallback", []) or []

    provider_cfg = ProviderConfig(
        api_key=api_key,
        base_url=base_url,
        model=parsed.model,
        params=parsed.params,
    )

    return ResolvedConfig(uri=parsed, provider=provider_cfg, fallbacks=fallbacks)


def _load_yaml_config() -> dict[str, Any]:
    """Read ``{xdg.config_dir("hop")}/llm.yaml`` if it exists."""
    cfg_path = os.path.join(config_dir("hop"), "llm.yaml")
    if not os.path.isfile(cfg_path):
        return {}
    with open(cfg_path) as fh:
        data = yaml.safe_load(fh)
    return data if isinstance(data, dict) else {}


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
    def __init__(self, provider: str) -> None:
        self.provider = provider
        super().__init__(f"authentication error for provider {provider!r}")


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


def is_fallbackable(err: Exception) -> bool:
    """Return True if *err* should trigger a fallback attempt."""
    return isinstance(err, (RateLimitError, RouterUnavailableError, RouterTimeoutError))


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------

Factory = Callable[[ResolvedConfig], Any]

_registry: dict[str, Factory] = {}


def register(scheme: str, factory: Factory) -> None:
    """Register a provider factory for *scheme*.

    Raises :class:`LLMError` if *scheme* is already registered.
    """
    if scheme in _registry:
        raise LLMError(f"scheme {scheme!r} already registered")
    _registry[scheme] = factory


def resolve(uri: str) -> Any:
    """Resolve a URI to a provider instance via the registry."""
    cfg = load_config(uri)
    scheme = cfg.uri.scheme
    factory = _registry.get(scheme)
    if factory is None:
        raise ProviderNotFoundError(scheme)
    return factory(cfg)


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
    factory = _registry.get(cfg.uri.scheme)
    if factory is None:
        raise ProviderNotFoundError(cfg.uri.scheme)
    return factory(cfg)

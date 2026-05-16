"""Thin facade over the hop-top-uri package.

This module intentionally delegates URI parsing, action resolution,
completion, and handler rendering to the released URI package. Kit owns the
integration surface; URI owns the implementation details and contract parity.
"""

from __future__ import annotations

from functools import lru_cache
from importlib import import_module
from typing import Any

_BACKEND_MODULES = ("uri", "hop_top_uri")
_BACKEND_EXPORTS = {
    "ActionRoute",
    "AmbiguousVanityError",
    "CompletionResult",
    "DefaultPolicy",
    "HandlerSpec",
    "ParseOptions",
    "Policy",
    "Registry",
    "ResolvedAction",
    "TypeRegistration",
    "URI",
    "VanityAlias",
    "complete_with_scheme",
    "default_policy",
    "desktop_file",
    "desktop_filename",
    "new_registry",
    "new_registry_with_policy",
    "patch_plist",
    "plist_snippet",
    "snippet",
    "windows_reg_snippet",
}


class URIBackendNotInstalledError(ImportError):
    """Raised when the hop-top-uri backend package is unavailable."""


# Backward-compatible alias: existing callers ``except URIBackendNotInstalled``
# keep working. Prefer ``URIBackendNotInstalledError`` (PEP 8 / N818) in new code.
URIBackendNotInstalled = URIBackendNotInstalledError


@lru_cache(maxsize=1)
def _backend() -> Any:
    last_error: ModuleNotFoundError | None = None
    for module_name in _BACKEND_MODULES:
        try:
            return import_module(module_name)
        except ModuleNotFoundError as exc:
            if exc.name != module_name:
                raise
            last_error = exc

    msg = (
        "hop_top_kit.uri requires the hop-top-uri package. "
        "Install hop-top-kit with its declared dependencies, or install "
        "hop-top-uri directly."
    )
    raise URIBackendNotInstalledError(msg) from last_error


def parse(input: str, policy: Any = None, options: Any = None) -> Any:
    """Parse a URI using hop-top-uri's parser."""

    return _backend().parse(input, policy, options)


def resolve(parsed_uri: Any, policy: Any = None) -> Any:
    """Resolve a parsed URI action to a command plan without executing it."""

    backend = _backend()
    if policy is None:
        policy = backend.default_policy()
    return backend.resolve_action(parsed_uri, policy)


def resolve_action(parsed_uri: Any, policy: Any = None) -> Any:
    """Alias for ``resolve`` matching the underlying URI package name."""

    return resolve(parsed_uri, policy)


def complete(
    registry: Any,
    *,
    type_name: str = "",
    prefix: str = "",
    input: str = "",
    to_complete: str = "",
) -> Any:
    """Complete URI values through a hop-top-uri registry.

    ``input`` returns vanity completions, ``type_name``/``prefix`` returns a
    registered type completion, and ``to_complete`` delegates to URI scheme
    completion when callers need the scheme-aware helper.
    """

    if to_complete:
        if not type_name:
            raise ValueError("type_name is required when to_complete is set")
        return _backend().complete_with_scheme(registry, type_name, to_complete)

    if input:
        return registry.complete_vanity(input)

    if not type_name:
        raise ValueError("type_name or input is required")
    return registry.complete(type_name, prefix) or []


def handler_id(spec: Any) -> str:
    """Return the URI handler identifier for a HandlerSpec-like object."""

    return spec.handler_id()


def handler_snippet(platform: str, spec: Any) -> str:
    """Render a platform handler snippet through hop-top-uri."""

    return _backend().snippet(platform, spec)


def handler_generate(platform: str, spec: Any) -> str:
    """Alias for ``handler_snippet`` matching the CLI handler action name."""

    return handler_snippet(platform, spec)


def handler_desktop_filename(spec: Any) -> str:
    """Return the Linux desktop filename for a HandlerSpec-like object."""

    return _backend().desktop_filename(spec)


def __getattr__(name: str) -> Any:
    if name in _BACKEND_EXPORTS:
        try:
            return getattr(_backend(), name)
        except AttributeError as exc:
            raise AttributeError(f"hop_top_kit.uri backend does not expose {name!r}") from exc
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")


# F822 is suppressed because the backend re-exports listed below
# (``_BACKEND_EXPORTS``) are forwarded lazily through ``__getattr__`` and are
# not statically bound module attributes. They resolve at runtime against the
# installed ``hop-top-uri`` package.
__all__ = [  # noqa: F822
    "URI",
    "ActionRoute",
    "AmbiguousVanityError",
    "CompletionResult",
    "DefaultPolicy",
    "HandlerSpec",
    "ParseOptions",
    "Policy",
    "Registry",
    "ResolvedAction",
    "TypeRegistration",
    "URIBackendNotInstalled",
    "URIBackendNotInstalledError",
    "VanityAlias",
    "complete",
    "complete_with_scheme",
    "default_policy",
    "desktop_file",
    "desktop_filename",
    "handler_desktop_filename",
    "handler_generate",
    "handler_id",
    "handler_snippet",
    "new_registry",
    "new_registry_with_policy",
    "parse",
    "patch_plist",
    "plist_snippet",
    "resolve",
    "resolve_action",
    "snippet",
    "windows_reg_snippet",
]

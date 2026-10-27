"""
hop_top_kit.llm_config — provider URI parsing and configuration loading
for :mod:`hop_top_kit.llm`, which re-exports :func:`parse_uri`,
:func:`load_config` and :func:`apply_api_key`.

URIs parse as Go ``ParseURI`` (pinned by ``contracts/llm-v1``,
``parse_uri``). A key resolves from the URI's ``api_key`` param,
``LLM_API_KEY``, llm.yaml ``providers.<scheme>.api_key`` or
``<SCHEME>_API_KEY``, and goes only where kit trusts it to (``key_endpoint``).
"""

from __future__ import annotations

import os
from typing import Any

import yaml

from hop_top_kit.llm import (
    URI,
    KeyEndpointError,
    LLMError,
    ProviderConfig,
    ResolvedConfig,
    _go_quote,
    declared_endpoints,
)
from hop_top_kit.llm_endpoint import (
    check_key_endpoint,
    credentials_trusted,
    is_blank,
    redact_uri,
)
from hop_top_kit.xdg import config_dir

__all__ = ["apply_api_key", "load_config", "parse_uri"]


def parse_uri(raw: str) -> URI:
    """Parse a provider URI: ``scheme://[host:port/]model[?params]``.

    ``openai://gpt-4?temperature=0.5``, ``ollama://localhost:11434/llama3``
    (host form: a ``host:port`` before the first slash) and
    slash-containing models (``openrouter://openai/gpt-4.1-nano``). The
    query splits on ``&`` and is not unescaped; a param without ``=``
    is ``""``. Errors quote *raw* with credentials masked. Go
    ``ParseURI``, pinned by ``contracts/llm-v1`` (``parse_uri``).
    """
    if not raw:
        raise LLMError("empty URI")
    idx = raw.find("://")
    if idx < 0:
        raise LLMError(f"missing scheme in URI {_go_quote(redact_uri(raw))}")
    scheme = raw[:idx]
    if not scheme:
        raise LLMError(f"empty scheme in URI {_go_quote(redact_uri(raw))}")
    rest = raw[idx + 3 :]
    params: dict[str, str] = {}
    q = rest.find("?")
    if q >= 0:
        for pair in rest[q + 1 :].split("&") if rest[q + 1 :] else []:
            k, _, v = pair.partition("=")
            if k:
                params[k] = v
        rest = rest[:q]
    host, model = "", rest
    slash = rest.find("/")
    if slash > 0:
        hp = _split_host_port(rest[:slash])
        if hp is not None and hp[1]:
            h, port = hp
            host = f"[{h}]:{port}" if ":" in h else f"{h}:{port}"
            model = rest[slash + 1 :]
    return URI(scheme=scheme, model=model, host=host, params=params)


def _split_host_port(hp: str) -> tuple[str, str] | None:
    """Go ``net.SplitHostPort``: ``None`` where Go returns an error."""
    i = hp.rfind(":")
    if i < 0:
        return None
    if hp.startswith("["):
        end = hp.find("]")
        if end < 0 or end + 1 != i:
            return None
        host, j, k = hp[1:end], 1, end + 1
    else:
        host = hp[:i]
        if ":" in host:
            return None
        j = k = 0
    if "[" in hp[j:] or "]" in hp[k:]:
        return None
    return host, hp[i + 1 :]


def _env_key_name(scheme: str) -> str:
    """The provider key variable for *scheme*: ``<SCHEME>_API_KEY``."""
    return scheme.upper().replace("-", "_").replace(".", "_") + "_API_KEY"


def _resolve_key(parsed: URI, section: dict[str, Any]) -> tuple[str, bool]:
    """The key for *parsed* and whether the URI itself carried it.

    Order: the URI's ``api_key`` param, ``LLM_API_KEY``, llm.yaml
    ``providers.<scheme>.api_key``, then ``<SCHEME>_API_KEY``. A blank
    value counts as unset.
    """
    v = parsed.params.get("api_key", "")
    if not is_blank(v):
        return v, True
    for cand in (
        os.environ.get("LLM_API_KEY", ""),
        str(section.get("api_key", "") or ""),
        os.environ.get(_env_key_name(parsed.scheme), ""),
    ):
        if not is_blank(cand):
            return cand, False
    return "", False


def _uri_endpoint(u: URI) -> str:
    """The endpoint a URI itself names: its ``base_url`` param, else its host."""
    if u.params.get("base_url"):
        return u.params["base_url"]
    return f"http://{u.host}" if u.host else ""


def _guard_key_endpoint(parsed: URI, key: str, from_uri: bool, file_base: str) -> None:
    """Refuse *key* for the endpoint the URI names unless kit may send it
    there: a URI's own key goes to any https host; any other key only to
    loopback or an origin the adapter declares, llm.yaml ``base_url`` or
    ``LLM_BASE_URL``. Plain http to a remote host is refused whatever the
    key's source."""
    endpoint = _uri_endpoint(parsed)
    if is_blank(key) or not endpoint:
        return
    check_key_endpoint(parsed.scheme, key, endpoint)
    if from_uri:
        return
    trusted = [*declared_endpoints(parsed.scheme), file_base, os.environ.get("LLM_BASE_URL", "")]
    if not credentials_trusted(endpoint, trusted):
        raise KeyEndpointError(parsed.scheme, redact_uri(endpoint))


def _drop_blank_uri_key(u: URI) -> None:
    if "api_key" in u.params and is_blank(u.params["api_key"]):
        del u.params["api_key"]


def load_config(uri: str = "", *, hermetic: bool = False) -> ResolvedConfig:
    """Load LLM configuration from env vars, YAML file, or explicit *uri*.

    Resolution order (first wins per field):
    1. Explicit *uri* argument (its ``api_key`` and ``base_url`` params
       and host form included)
    2. ``LLM_PROVIDER`` / ``LLM_API_KEY`` / ``LLM_BASE_URL`` / ``LLM_FALLBACK``
    3. ``{xdg.config_dir("hop")}/llm.yaml``
    4. ``<SCHEME>_API_KEY`` (the key only)

    Raises :class:`KeyEndpointError` when the key may not go where the
    configuration points (see :func:`_guard_key_endpoint`).

    *hermetic* reads nothing ambient: no environment variable and no
    llm.yaml. The config is *uri* alone (required): its model, params,
    ``api_key`` and ``base_url`` params and host form; no fallbacks. The
    result carries :attr:`ResolvedConfig.hermetic` for the adapter.
    """
    if hermetic:
        return _hermetic_config(uri)
    yaml_data = _load_yaml_config()

    raw_uri = uri or os.environ.get("LLM_PROVIDER", "") or yaml_data.get("default", "")
    if not raw_uri:
        raise LLMError("no LLM provider configured (set LLM_PROVIDER env or provide a URI)")

    parsed = parse_uri(raw_uri)
    _drop_blank_uri_key(parsed)

    # Look up per-provider config from YAML ``providers`` dict.
    providers_map: dict[str, Any] = yaml_data.get("providers", {}) or {}
    prov_section = providers_map.get(parsed.scheme, {}) or {}
    file_base = str(prov_section.get("base_url", "") or "")

    api_key, from_uri = _resolve_key(parsed, prov_section)
    _guard_key_endpoint(parsed, api_key, from_uri, file_base)

    # Base URL: the URI's own choice outranks the environment, which
    # outranks the file.
    base_url = os.environ.get("LLM_BASE_URL", "") or file_base
    if parsed.host:
        base_url = f"http://{parsed.host}"
    if "base_url" in parsed.params:
        base_url = parsed.params["base_url"]
    check_key_endpoint(parsed.scheme, api_key, base_url)

    fallback_env = os.environ.get("LLM_FALLBACK", "")
    if fallback_env:
        fallbacks = [f.strip() for f in fallback_env.split(",") if f.strip()]
    else:
        fallbacks = yaml_data.get("fallback", []) or []

    provider_cfg = ProviderConfig(
        api_key=api_key,
        base_url=base_url,
        model=parsed.model,
        params=dict(parsed.params),
    )

    return ResolvedConfig(uri=parsed, provider=provider_cfg, fallbacks=fallbacks)


def _hermetic_config(uri: str) -> ResolvedConfig:
    """:func:`load_config` from *uri* alone, as Go ``Resolve`` builds it."""
    if not uri:
        raise LLMError("no LLM provider URI (a hermetic config reads no LLM_PROVIDER or llm.yaml)")
    parsed = parse_uri(uri)
    _drop_blank_uri_key(parsed)
    base_url = f"http://{parsed.host}" if parsed.host else ""
    base_url = parsed.params.get("base_url", base_url)
    api_key = parsed.params.get("api_key", "")
    # A key never crosses the network in clear.
    check_key_endpoint(parsed.scheme, api_key, base_url)
    return ResolvedConfig(
        uri=parsed,
        provider=ProviderConfig(
            api_key=api_key, base_url=base_url, model=parsed.model, params=dict(parsed.params)
        ),
        hermetic=True,
    )


def apply_api_key(uri: str) -> str:
    """Return *uri* carrying the key kit resolves for it, as an
    ``api_key`` param, after the same endpoint guard as
    :func:`load_config`.

    A URI with its own non-blank ``api_key``, or for which no key
    resolves, comes back unchanged (a blank ``api_key`` param is
    dropped). Go ``ApplyAPIKey`` without a secret store.
    """
    try:
        parsed = parse_uri(uri)
    except LLMError:
        # parse_uri quotes its input, which may carry a key.
        raise LLMError("llm: apply API key: provider URI must be scheme://model") from None
    if "api_key" in parsed.params and is_blank(parsed.params["api_key"]):
        uri = _strip_param(uri, "api_key")
        del parsed.params["api_key"]
    section = (_load_yaml_config().get("providers", {}) or {}).get(parsed.scheme, {}) or {}
    key, from_uri = _resolve_key(parsed, section)
    _guard_key_endpoint(parsed, key, from_uri, str(section.get("base_url", "") or ""))
    if from_uri or not key:
        return uri
    if any(c in key for c in "&?#"):
        raise LLMError(
            f"llm: {parsed.scheme} API key contains a character a provider URI cannot carry (& ? #)"
        )
    return f"{uri}{'&' if '?' in uri else '?'}api_key={key}"


def _strip_param(raw: str, name: str) -> str:
    """*raw* without any *name* param; the ``?`` goes too when nothing is left."""
    s = raw.find("://")
    q = raw.find("?", s + 3) if s >= 0 else -1
    if q < 0:
        return raw
    kept = [p for p in raw[q + 1 :].split("&") if p.partition("=")[0] != name]
    return f"{raw[:q]}?{'&'.join(kept)}" if kept else raw[:q]


def _load_yaml_config() -> dict[str, Any]:
    """Read ``{xdg.config_dir("hop")}/llm.yaml`` if it exists."""
    cfg_path = os.path.join(config_dir("hop"), "llm.yaml")
    if not os.path.isfile(cfg_path):
        return {}
    with open(cfg_path) as fh:
        data = yaml.safe_load(fh)
    return data if isinstance(data, dict) else {}

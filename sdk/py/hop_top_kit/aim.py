"""aim — AI model registry: types, query parser, source, cache, registry."""

from __future__ import annotations

import json
import os
import time
import urllib.request
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional, Protocol

from platformdirs import user_cache_dir

_DEFAULT_URL = "https://models.dev/api.json"
_DEFAULT_TIMEOUT, _DEFAULT_MAX_RESP = 30, 50 << 20
_DEFAULT_TTL, _LOCK_TIMEOUT, _LOCK_POLL = 86400, 30, 0.05
_CACHE_SUBDIR, _DATA_FILE, _ETAG_FILE, _LOCK_FILE = "hop/aim", "providers.json", "etag", ".lock"


@dataclass
class Modalities:
    input: list[str] = field(default_factory=list)
    output: list[str] = field(default_factory=list)


@dataclass
class Limit:
    context: int = 0
    max_output: int = 0


@dataclass
class Cost:
    input: float = 0.0
    output: float = 0.0


@dataclass
class Model:
    id: str = ""
    name: str = ""
    provider: str = ""
    family: str = ""
    input: list[str] = field(default_factory=list)
    output: list[str] = field(default_factory=list)
    tool_call: bool = False
    reasoning: bool = False
    open_weights: bool = False
    context: int = 0
    max_output: int = 0
    cost_input: float = 0.0
    cost_output: float = 0.0
    modalities: Optional[Modalities] = None
    limit: Optional[Limit] = None
    cost: Optional[Cost] = None

    def normalize(self) -> None:
        if self.modalities:
            self.input, self.output = self.modalities.input, self.modalities.output
        if self.limit:
            self.context, self.max_output = self.limit.context, self.limit.max_output
        if self.cost:
            self.cost_input, self.cost_output = self.cost.input, self.cost.output


@dataclass
class Provider:
    id: str = ""
    name: str = ""
    models: dict[str, Model] = field(default_factory=dict)


@dataclass
class Filter:
    input: str = ""
    output: str = ""
    provider: str = ""
    family: str = ""
    tool_call: Optional[bool] = None
    reasoning: Optional[bool] = None
    open_weights: Optional[bool] = None
    query: str = ""


_BOOL_TAGS = {"toolcall", "reasoning", "openweights"}
_KNOWN_TAGS = {"provider", "family", "in", "out"} | _BOOL_TAGS


def _parse_bool(v: str, tag: str) -> bool:
    if v in ("true", "false"):
        return v == "true"
    raise ValueError(f'invalid bool value "{v}" for tag "{tag}": must be true or false')


def _append_csv(a: str, b: str) -> str:
    return f"{a},{b}" if a else b


_TAG_TO_ATTR = {"provider": "provider", "family": "family", "in": "input", "out": "output"}
_BOOL_TO_ATTR = {"toolcall": "tool_call", "reasoning": "reasoning", "openweights": "open_weights"}


def parse_query(q: str) -> Filter:
    f, free, i, n = Filter(), [], 0, len(q)
    while i < n:
        if q[i] in " \t":
            i += 1
            continue
        if q[i] == '"':
            j = q.find('"', i + 1)
            if j < 0:
                raise ValueError("unterminated quote")
            free.append(q[i + 1 : j])
            i = j + 1
            continue
        end = i
        while end < n and q[end] not in " \t":
            end += 1
        val, i = q[i:end], end
        ci = val.find(":")
        if ci < 0:
            free.append(val)
            continue
        key, v = val[:ci], val[ci + 1 :]
        if not key:
            raise ValueError(f'malformed tag "{val}": missing key')
        if not v:
            raise ValueError(f'empty value for tag "{key}"')
        if key not in _KNOWN_TAGS:
            raise ValueError(f'unknown tag "{key}"')
        if key in _TAG_TO_ATTR:
            attr = _TAG_TO_ATTR[key]
            setattr(f, attr, _append_csv(getattr(f, attr), v))
        else:
            setattr(f, _BOOL_TO_ATTR[key], _parse_bool(v, key))
    if free:
        f.query = " ".join(free)
    return f


def _model_from_dict(d: dict) -> Model:
    raw_m = d.get("modalities") or {}
    raw_l = d.get("limit") or {}
    raw_c = d.get("cost") or {}
    mods = Modalities(raw_m.get("input", []), raw_m.get("output", [])) if raw_m else None
    lim = Limit(raw_l.get("context", 0), raw_l.get("output", 0)) if raw_l else None
    cst = Cost(raw_c.get("input", 0.0), raw_c.get("output", 0.0)) if raw_c else None
    m = Model(
        id=d.get("id", ""),
        name=d.get("name", ""),
        provider=d.get("provider", ""),
        family=d.get("family", ""),
        tool_call=d.get("tool_call", False),
        reasoning=d.get("reasoning", False),
        open_weights=d.get("open_weights", False),
        modalities=mods,
        limit=lim,
        cost=cst,
    )
    m.normalize()
    return m


def _providers_from_dict(raw: dict) -> dict[str, Provider]:
    out: dict[str, Provider] = {}
    for key, pd in raw.items():
        pid = pd.get("id", key)
        if key != pid:
            raise ValueError(f'aim: map key "{key}" != provider.id "{pid}"')
        models = {}
        for mid, md in pd.get("models", {}).items():
            model = _model_from_dict(md)
            if not model.provider:
                model.provider = key
            models[mid] = model
        out[key] = Provider(id=pid, name=pd.get("name", ""), models=models)
    return out


class Source(Protocol):
    def fetch(self) -> dict[str, Provider]: ...


class ModelsDevSource:
    def __init__(
        self,
        *,
        url: str = _DEFAULT_URL,
        timeout: int = _DEFAULT_TIMEOUT,
        max_size: int = _DEFAULT_MAX_RESP,
    ):
        self.url = url
        self.timeout = timeout
        self.max_size = max_size

    def fetch(self) -> dict[str, Provider]:
        providers, _, _ = self.fetch_with_etag("")
        return providers  # type: ignore[return-value]

    def fetch_with_etag(self, etag: str) -> tuple[dict[str, Provider] | None, str, bool]:
        """Returns (providers, new_etag, not_modified)."""
        req = urllib.request.Request(self.url)
        req.add_header("Accept", "application/json")
        if etag:
            req.add_header("If-None-Match", etag)
        try:
            resp = urllib.request.urlopen(req, timeout=self.timeout)
        except urllib.error.HTTPError as e:
            if e.code == 304:
                return None, etag, True
            raise ValueError(f"aim: unexpected status {e.code} from {self.url}") from e
        body = resp.read(self.max_size + 1)
        if len(body) > self.max_size:
            raise ValueError(f"aim: response exceeds max size ({self.max_size} bytes)")
        providers = _providers_from_dict(json.loads(body))
        return providers, resp.headers.get("ETag", ""), False


class Cache:
    def __init__(
        self,
        src: Source,
        *,
        ttl: int = _DEFAULT_TTL,
        cache_dir: str | None = None,
    ):
        self.src = src
        self.ttl = ttl
        self.dir = Path(cache_dir) if cache_dir else Path(user_cache_dir(), _CACHE_SUBDIR)

    def fetch(self) -> dict[str, Provider]:
        env = self._load()
        if env and time.time() - env["fetched_at"] < self.ttl:
            return env["providers"]
        try:
            return self._refresh()
        except Exception:
            if env:
                return env["providers"]
            raise

    def refresh(self, *, force: bool = False) -> dict[str, Provider]:
        if not force:
            env = self._load()
            if env and time.time() - env["fetched_at"] < self.ttl:
                return env["providers"]
        return self._refresh()

    def _refresh(self) -> dict[str, Provider]:
        self.dir.mkdir(parents=True, exist_ok=True)
        unlock = self._lock()
        try:
            if hasattr(self.src, "fetch_with_etag"):
                providers, new_etag, not_mod = self.src.fetch_with_etag(self._load_etag())
                if not_mod:
                    env = self._load()
                    if env:
                        env["fetched_at"] = time.time()
                        self._store(env)
                        return env["providers"]
                if new_etag:
                    self._store_etag(new_etag)
            else:
                providers = self.src.fetch()
            env = {"fetched_at": time.time(), "providers": providers}
            self._store(env)
            return providers  # type: ignore[return-value]
        finally:
            unlock()

    def _load(self) -> dict | None:
        p = self.dir / _DATA_FILE
        if not p.exists():
            return None
        try:
            raw = json.loads(p.read_text())
            providers = _providers_from_dict(raw["providers"])
            return {"fetched_at": raw["fetched_at"], "providers": providers}
        except Exception:
            p.unlink(missing_ok=True)
            return None

    def _store(self, env: dict) -> None:
        data = {
            "fetched_at": env["fetched_at"],
            "providers": _serialize_providers(env["providers"]),
        }
        tmp = self.dir / f"{_DATA_FILE}.tmp"
        tmp.write_text(json.dumps(data))
        os.replace(tmp, self.dir / _DATA_FILE)

    def _load_etag(self) -> str:
        p = self.dir / _ETAG_FILE
        return p.read_text() if p.exists() else ""

    def _store_etag(self, etag: str) -> None:
        tmp = self.dir / f"{_ETAG_FILE}.tmp"
        tmp.write_text(etag)
        os.replace(tmp, self.dir / _ETAG_FILE)

    def _lock(self) -> Callable[[], None]:
        p, deadline = self.dir / _LOCK_FILE, time.time() + _LOCK_TIMEOUT
        while True:
            try:
                fd = os.open(str(p), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o644)
                os.close(fd)
                return lambda: p.unlink(missing_ok=True)
            except FileExistsError:
                try:
                    if time.time() - p.stat().st_mtime > _LOCK_TIMEOUT:
                        p.unlink(missing_ok=True)
                        continue
                except FileNotFoundError:
                    continue
                if time.time() > deadline:
                    raise TimeoutError(f"aim: lock timeout after {_LOCK_TIMEOUT}s") from None
                time.sleep(_LOCK_POLL)


def _serialize_model(m: Model) -> dict:
    d: dict = {
        "id": m.id,
        "name": m.name,
        "provider": m.provider,
        "family": m.family,
        "tool_call": m.tool_call,
        "reasoning": m.reasoning,
        "open_weights": m.open_weights,
    }
    if m.modalities:
        d["modalities"] = {"input": m.modalities.input, "output": m.modalities.output}
    if m.limit:
        d["limit"] = {"context": m.limit.context, "output": m.limit.max_output}
    if m.cost:
        d["cost"] = {"input": m.cost.input, "output": m.cost.output}
    return d


def _serialize_providers(providers: dict[str, Provider]) -> dict:
    return {
        k: {
            "id": p.id,
            "name": p.name,
            "models": {mk: _serialize_model(m) for mk, m in p.models.items()},
        }
        for k, p in providers.items()
    }


class _MultiSource:
    def __init__(self, sources: list[Source]):
        self._sources = sources

    def fetch(self) -> dict[str, Provider]:
        merged: dict[str, Provider] = {}
        for s in self._sources:
            merged.update(s.fetch())
        return merged


def _modality_subset(filter_csv: str, modalities: list[str]) -> bool:
    available = {v.lower().strip() for v in modalities}
    for want in filter_csv.split(","):
        want = want.lower().strip()
        if want and want not in available:
            return False
    return True


def _matches_filter(m: Model, f: Filter) -> bool:
    if f.provider and m.provider not in {v.strip() for v in f.provider.split(",")}:
        return False
    if f.family and m.family not in {v.strip() for v in f.family.split(",")}:
        return False
    if f.input and not _modality_subset(f.input, m.input):
        return False
    if f.output and not _modality_subset(f.output, m.output):
        return False
    if f.tool_call is not None and m.tool_call != f.tool_call:
        return False
    if f.reasoning is not None and m.reasoning != f.reasoning:
        return False
    if f.open_weights is not None and m.open_weights != f.open_weights:
        return False
    if f.query:
        q = f.query.lower()
        if q not in m.id.lower() and q not in m.name.lower():
            return False
    return True


class Registry:
    def __init__(
        self,
        *,
        sources: list[Source] | None = None,
        cache_dir: str | None = None,
        ttl: int | None = None,
    ):
        kw: dict = {}
        if cache_dir:
            kw["cache_dir"] = cache_dir
        if ttl is not None:
            kw["ttl"] = ttl
        self._cache = Cache(_MultiSource(sources or [ModelsDevSource()]), **kw)
        self._providers: dict[str, Provider] | None = None

    def _ensure(self) -> None:
        if self._providers is None:
            try:
                self._providers = self._cache.fetch()
            except Exception:
                self._providers = {}

    def refresh(self) -> None:
        self._providers = self._cache.refresh(force=True)

    def providers(self) -> list[Provider]:
        self._ensure()
        return sorted(self._providers.values(), key=lambda p: p.id)

    def models(self, f: Filter | None = None) -> list[Model]:
        self._ensure()
        f = f or Filter()
        out = [
            m for p in self._providers.values() for m in p.models.values() if _matches_filter(m, f)
        ]
        return sorted(out, key=lambda m: (m.provider, m.id))

    def get(self, provider: str, model: str) -> Model | None:
        self._ensure()
        p = self._providers.get(provider)
        return p.models.get(model) if p else None

    def query(self, q: str) -> list[Model]:
        return self.models(parse_query(q))

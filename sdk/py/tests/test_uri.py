import sys
from types import SimpleNamespace

import pytest

from hop_top_kit import uri


class FakeSpec:
    def handler_id(self):
        return "vendor.app.python.tlc"


class FakeRegistry:
    def __init__(self, result=None):
        self.calls = []
        self.result = result

    def complete_vanity(self, value):
        self.calls.append(("vanity", value))
        return ["vanity:" + value]

    def complete(self, type_name, prefix):
        self.calls.append(("type", type_name, prefix))
        return self.result


def install_backend(monkeypatch):
    backend = SimpleNamespace(
        HandlerSpec=FakeSpec,
        Policy=object,
        parse=lambda value, policy=None, options=None: (
            "parse",
            value,
            policy,
            options,
        ),
        default_policy=lambda: "default-policy",
        resolve_action=lambda parsed_uri, policy: ("resolve", parsed_uri, policy),
        complete_with_scheme=lambda registry, type_name, to_complete: (
            "scheme",
            registry,
            type_name,
            to_complete,
        ),
        snippet=lambda platform, spec: f"{platform}:{spec.handler_id()}",
        desktop_filename=lambda spec: spec.handler_id() + ".desktop",
    )
    uri._backend.cache_clear()
    monkeypatch.setitem(sys.modules, "cite", backend)
    monkeypatch.delitem(sys.modules, "hop_top_cite", raising=False)
    return backend


def test_parse_delegates_to_backend(monkeypatch):
    install_backend(monkeypatch)

    assert uri.parse("tlc://org/repo/T-0001", "policy", "options") == (
        "parse",
        "tlc://org/repo/T-0001",
        "policy",
        "options",
    )


def test_resolve_delegates_to_backend(monkeypatch):
    install_backend(monkeypatch)

    assert uri.resolve("parsed", "policy") == ("resolve", "parsed", "policy")
    assert uri.resolve_action("parsed", "policy") == ("resolve", "parsed", "policy")
    assert uri.resolve("parsed") == ("resolve", "parsed", "default-policy")


def test_complete_delegates_by_mode(monkeypatch):
    install_backend(monkeypatch)

    registry = FakeRegistry(result=["T-0001"])

    assert uri.complete(registry, input="tl") == ["vanity:tl"]
    assert registry.calls[-1] == ("vanity", "tl")
    assert uri.complete(registry, type_name="task", prefix="T-") == ["T-0001"]
    assert registry.calls[-1] == ("type", "task", "T-")
    assert uri.complete(registry, type_name="task", to_complete="tlc://T-") == (
        "scheme",
        registry,
        "task",
        "tlc://T-",
    )


def test_complete_normalizes_missing_type_results(monkeypatch):
    install_backend(monkeypatch)

    assert uri.complete(FakeRegistry(result=None), type_name="task") == []


def test_complete_requires_a_mode(monkeypatch):
    install_backend(monkeypatch)

    with pytest.raises(ValueError, match="type_name or input"):
        uri.complete(FakeRegistry())

    with pytest.raises(ValueError, match="type_name is required"):
        uri.complete(FakeRegistry(), to_complete="tlc://")


def test_handler_helpers_delegate(monkeypatch):
    install_backend(monkeypatch)
    spec = FakeSpec()

    assert uri.handler_id(spec) == "vendor.app.python.tlc"
    assert uri.handler_snippet("linux", spec) == "linux:vendor.app.python.tlc"
    assert uri.handler_generate("linux", spec) == "linux:vendor.app.python.tlc"
    assert uri.handler_desktop_filename(spec) == "vendor.app.python.tlc.desktop"


def test_dynamic_backend_exports_are_lazy(monkeypatch):
    backend = install_backend(monkeypatch)

    assert uri.HandlerSpec is FakeSpec
    assert uri.Policy is backend.Policy


def test_missing_backend_error_is_actionable(monkeypatch):
    uri._backend.cache_clear()

    def missing(module_name):
        raise ModuleNotFoundError(name=module_name)

    monkeypatch.setattr(uri, "import_module", missing)

    with pytest.raises(uri.URIBackendNotInstalled, match="hop-top-cite"):
        uri.parse("tlc://org/repo/T-0001")

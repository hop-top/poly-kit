"""Tests for hop_top_kit.routellm_grpc servicers."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from hop_top_kit.routellm_grpc import (
    EvaServicer,
    HealthServicer,
    RouterServicer,
)

# ---------------------------------------------------------------------------
# Fakes
# ---------------------------------------------------------------------------


@dataclass
class _FakeModelPair:
    strong: str = "gpt-4"
    weak: str = "gpt-3.5-turbo"


class _FakeController:
    """Minimal stand-in for routellm Controller."""

    def __init__(
        self,
        strong: str = "gpt-4",
        weak: str = "gpt-3.5-turbo",
        routers: dict[str, Any] | None = None,
    ) -> None:
        self.default_model_pair = _FakeModelPair(strong=strong, weak=weak)
        self.routers: dict[str, Any] = routers or {}


# ---------------------------------------------------------------------------
# HealthServicer
# ---------------------------------------------------------------------------


class TestHealthServicer:
    def test_check_returns_serving(self) -> None:
        svc = HealthServicer()
        resp = svc.Check()
        assert resp["status"] == HealthServicer.SERVING
        assert resp["message"] == "ok"
        assert isinstance(resp["uptime_seconds"], int)

    def test_watch_yields_serving(self) -> None:
        svc = HealthServicer()
        results = list(svc.Watch())
        assert len(results) == 1
        assert results[0]["status"] == HealthServicer.SERVING


# ---------------------------------------------------------------------------
# RouterServicer
# ---------------------------------------------------------------------------


class TestRouterServicer:
    def test_get_config_returns_models(self) -> None:
        ctrl = _FakeController(
            strong="claude-opus-4-20250514",
            weak="claude-haiku",
            routers={"mf": object()},
        )
        svc = RouterServicer(ctrl)
        resp = svc.GetConfig()
        assert resp["strong_model"] == "claude-opus-4-20250514"
        assert resp["weak_model"] == "claude-haiku"
        assert len(resp["routers"]) == 1
        assert resp["routers"][0]["name"] == "mf"

    def test_list_routers_empty(self) -> None:
        ctrl = _FakeController(routers={})
        svc = RouterServicer(ctrl)
        resp = svc.ListRouters()
        assert resp["routers"] == []

    def test_update_config_updates_models(self) -> None:
        ctrl = _FakeController()
        svc = RouterServicer(ctrl)
        req = {"strong_model": "gpt-5", "weak_model": "gpt-4o-mini"}
        resp = svc.UpdateConfig(request=req)
        assert resp["success"] is True


# ---------------------------------------------------------------------------
# EvaServicer
# ---------------------------------------------------------------------------


class TestEvaServicer:
    def test_list_contracts_empty_initially(self) -> None:
        svc = EvaServicer()
        resp = svc.ListContracts()
        assert resp["contracts"] == []

    def test_add_and_list_contract(self) -> None:
        svc = EvaServicer()
        add_resp = svc.AddContract({"name": "tone", "content": "be polite"})
        assert add_resp["success"] is True
        assert "id" in add_resp

        contracts = svc.ListContracts()["contracts"]
        assert len(contracts) == 1
        assert contracts[0]["name"] == "tone"

    def test_remove_contract(self) -> None:
        svc = EvaServicer()
        cid = svc.AddContract({"name": "x", "content": "y"})["id"]
        assert svc.RemoveContract({"id": cid})["success"] is True
        assert svc.ListContracts()["contracts"] == []

    def test_remove_missing_contract(self) -> None:
        svc = EvaServicer()
        assert svc.RemoveContract({"id": "nope"})["success"] is False

    def test_evaluate_returns_pass(self) -> None:
        svc = EvaServicer()
        resp = svc.Evaluate({"prompt": "hello", "response": "hi"})
        assert resp["passed"] is True
        assert resp["confidence"] == 1.0

    def test_get_eval_results(self) -> None:
        svc = EvaServicer()
        svc.Evaluate({"prompt": "a", "response": "b"})
        results = svc.GetEvalResults()["results"]
        assert len(results) == 1
        assert results[0]["prompt"] == "a"

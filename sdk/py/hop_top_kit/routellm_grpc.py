"""gRPC server for routellm control plane (RouterService, EvaService, Health).

Uses manual dataclass message types until buf-generated protobuf code is
available.  Each servicer mirrors the corresponding proto service definition
in proto/routellm/v1/.
"""

from __future__ import annotations

import os
import threading
import time
import uuid
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any, Protocol

import yaml

# ---------------------------------------------------------------------------
# Optional grpcio imports -- fail gracefully so the module can still be
# imported (and tested) without grpcio installed.
# ---------------------------------------------------------------------------
try:
    from concurrent import futures

    import grpc
    from grpc import ServicerContext

    HAS_GRPC = True
except ImportError:
    grpc = None  # type: ignore[assignment]
    ServicerContext = object  # type: ignore[assignment,misc]
    HAS_GRPC = False

try:
    from grpc_reflection.v1alpha import reflection

    HAS_REFLECTION = True
except ImportError:
    HAS_REFLECTION = False


# ---------------------------------------------------------------------------
# Placeholder dataclass messages — superseded by buf-generated types
# once `buf generate` is wired into the build.
# ---------------------------------------------------------------------------


@dataclass
class RouterInfo:
    name: str = ""
    status: str = "loaded"
    threshold: float = 0.0
    config: dict[str, str] = field(default_factory=dict)


@dataclass
class Contract:
    id: str = ""
    name: str = ""
    content: str = ""
    enabled: bool = True


@dataclass
class Violation:
    contract_id: str = ""
    rule: str = ""
    message: str = ""
    severity: float = 0.0


@dataclass
class EvalResult:
    id: str = ""
    prompt: str = ""
    passed: bool = True
    confidence: float = 1.0
    violations: list[Violation] = field(default_factory=list)
    timestamp_unix: int = 0


# ---------------------------------------------------------------------------
# Controller / EvaRunner protocols -- loose coupling to actual implementations
# ---------------------------------------------------------------------------


class ControllerLike(Protocol):
    """Minimal interface expected from a routellm Controller."""

    default_model_pair: Any
    routers: dict[str, Any]


class EvaRunnerLike(Protocol):
    """Minimal interface expected from an eva Runner."""

    ...


# ---------------------------------------------------------------------------
# RouterServicer
# ---------------------------------------------------------------------------


class RouterServicer:
    """Implements routellm.v1.RouterService."""

    def __init__(self, controller: ControllerLike) -> None:
        self._ctrl = controller

    def GetConfig(self, request: Any = None, context: Any = None) -> dict:
        pair = self._ctrl.default_model_pair
        routers = [RouterInfo(name=name, status="loaded").__dict__ for name in self._ctrl.routers]
        return {
            "strong_model": pair.strong,
            "weak_model": pair.weak,
            "routers": routers,
        }

    def UpdateConfig(self, request: Any = None, context: Any = None) -> dict:
        """Apply a config update to the controller at runtime."""
        req = request or {}
        try:
            strong = req.get("strong_model")
            weak = req.get("weak_model")
            if strong and weak:
                # Update the default model pair on the controller.
                pair = self._ctrl.default_model_pair
                pair.strong = strong
                pair.weak = weak

            routers_cfg = req.get("routers")
            if routers_cfg and isinstance(routers_cfg, dict):
                for name, opts in routers_cfg.items():
                    if name in self._ctrl.routers and isinstance(opts, dict):
                        router = self._ctrl.routers[name]
                        for k, v in opts.items():
                            if hasattr(router, k):
                                setattr(router, k, v)

            return {"success": True, "message": "config updated"}
        except Exception as exc:
            return {"success": False, "message": str(exc)}

    def ListRouters(self, request: Any = None, context: Any = None) -> dict:
        routers = [RouterInfo(name=name, status="loaded").__dict__ for name in self._ctrl.routers]
        return {"routers": routers}


# ---------------------------------------------------------------------------
# EvaServicer
# ---------------------------------------------------------------------------


class EvaServicer:
    """Implements routellm.v1.EvaService."""

    def __init__(self, runner: EvaRunnerLike | None = None) -> None:
        self._runner = runner
        self._lock = threading.Lock()
        self._contracts: dict[str, Contract] = {}
        self._results: list[EvalResult] = []

    def ListContracts(self, request: Any = None, context: Any = None) -> dict:
        with self._lock:
            return {
                "contracts": [c.__dict__ for c in self._contracts.values()],
            }

    def AddContract(self, request: Any = None, context: Any = None) -> dict:
        req = request or {}
        name = req.get("name", "")
        content = req.get("content", "")
        cid = str(uuid.uuid4())[:8]
        with self._lock:
            self._contracts[cid] = Contract(
                id=cid,
                name=name,
                content=content,
                enabled=True,
            )
        return {"id": cid, "success": True}

    def RemoveContract(self, request: Any = None, context: Any = None) -> dict:
        req = request or {}
        cid = req.get("id", "")
        with self._lock:
            if cid in self._contracts:
                del self._contracts[cid]
                return {"success": True}
        return {"success": False}

    def Evaluate(self, request: Any = None, context: Any = None) -> dict:
        # Returns a fabricated pass result until the eva runner is wired.
        req = request or {}
        result = EvalResult(
            id=str(uuid.uuid4())[:8],
            prompt=req.get("prompt", ""),
            passed=True,
            confidence=1.0,
            timestamp_unix=int(time.time()),
        )
        self._results.append(result)
        return {
            "passed": result.passed,
            "confidence": result.confidence,
            "violations": [],
        }

    def GetEvalResults(
        self,
        request: Any = None,
        context: Any = None,
    ) -> dict:
        req = request or {}
        cid = req.get("contract_id", "")
        limit = req.get("limit", 100)
        since = req.get("since_unix", 0)

        filtered = [
            r
            for r in self._results
            if (not cid or r.prompt)  # placeholder filter
            and r.timestamp_unix >= since
        ][-limit:]

        return {
            "results": [
                {
                    "id": r.id,
                    "prompt": r.prompt,
                    "passed": r.passed,
                    "confidence": r.confidence,
                    "violations": [v.__dict__ for v in r.violations],
                    "timestamp_unix": r.timestamp_unix,
                }
                for r in filtered
            ],
        }


# ---------------------------------------------------------------------------
# HealthServicer
# ---------------------------------------------------------------------------


class HealthServicer:
    """Implements routellm.v1.HealthService."""

    # Mirror proto enum
    UNKNOWN = 0
    SERVING = 1
    NOT_SERVING = 2

    def __init__(self) -> None:
        self._start = time.time()

    def Check(self, request: Any = None, context: Any = None) -> dict:
        return {
            "status": self.SERVING,
            "message": "ok",
            "uptime_seconds": int(time.time() - self._start),
        }

    def Watch(self, request: Any = None, context: Any = None):
        """Server-streaming stub -- yields a single check then returns."""
        yield self.Check(request, context)


# ---------------------------------------------------------------------------
# ConfigWatcher — stat-based YAML config polling
# ---------------------------------------------------------------------------


class ConfigWatcher:
    """Polls a YAML config file for mtime changes, calling *on_change*
    with the parsed dict whenever the file is modified.

    Uses ``os.stat`` polling — no external dependencies required.
    """

    DEFAULT_INTERVAL: float = 5.0  # seconds

    def __init__(
        self,
        path: str,
        on_change: Callable[[dict], None],
        interval: float | None = None,
    ) -> None:
        self._path = path
        self._on_change = on_change
        self._interval = interval or self.DEFAULT_INTERVAL
        self._last_mtime: float = 0.0
        self._stop_event = threading.Event()
        self._thread: threading.Thread | None = None

    def start(self) -> None:
        """Start the polling thread."""
        self._stop_event.clear()
        self._thread = threading.Thread(
            target=self._poll,
            daemon=True,
            name="config-watcher",
        )
        self._thread.start()

    def stop(self) -> None:
        """Signal the polling thread to stop and wait for it."""
        self._stop_event.set()
        if self._thread is not None:
            self._thread.join()

    def _poll(self) -> None:
        while not self._stop_event.is_set():
            try:
                mtime = os.stat(self._path).st_mtime
            except OSError:
                self._stop_event.wait(self._interval)
                continue

            if mtime != self._last_mtime:
                self._last_mtime = mtime
                try:
                    with open(self._path) as fh:
                        data = yaml.safe_load(fh)
                    if isinstance(data, dict):
                        self._on_change(data)
                except Exception:
                    pass  # log in production; skip bad parses

            self._stop_event.wait(self._interval)


# ---------------------------------------------------------------------------
# Server bootstrap
# ---------------------------------------------------------------------------


def serve(
    controller: ControllerLike,
    eva_runner: EvaRunnerLike | None = None,
    port: int = 50051,
) -> None:
    """Start the gRPC server with all three services.

    Requires grpcio at runtime.  Raises ``RuntimeError`` if grpcio is not
    installed.
    """
    if not HAS_GRPC:
        raise RuntimeError(
            "grpcio is required to run the gRPC server.  "
            "Install it with: pip install grpcio grpcio-reflection"
        )

    # Servicers are instantiated here so the wiring is documented;
    # the buf-generated add_*_to_server helpers register them once
    # `buf generate` is available.
    _router = RouterServicer(controller)
    _eva = EvaServicer(eva_runner)
    _health = HealthServicer()

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))

    if HAS_REFLECTION:
        service_names = (
            "routellm.v1.RouterService",
            "routellm.v1.EvaService",
            "routellm.v1.HealthService",
            reflection.SERVICE_NAME,
        )
        reflection.enable_server_reflection(service_names, server)

    server.add_insecure_port(f"[::]:{port}")
    server.start()
    print(f"routellm gRPC server listening on :{port}")
    server.wait_for_termination()

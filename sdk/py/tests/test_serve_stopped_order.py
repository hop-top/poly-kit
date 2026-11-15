"""``stopped`` is a service's stop returning, so on a canceled run the
events follow reverse start order however the cancellation and the
start returns interleave with the supervisor's wait."""

from __future__ import annotations

import asyncio
from typing import Any

from hop_top_kit.bus import Event
from hop_top_kit.serve import ServiceConfig, ServiceRegistry, Supervisor

NAMES = ["alpha", "beta", "gamma", "delta"]
REVERSE = ["delta", "gamma", "beta", "alpha"]


class OnCancel:
    """Start returns as soon as the run is canceled."""

    def __init__(self, name: str, ready_after: float = 0.0) -> None:
        self.name = name
        self._ready_after = ready_after

    async def start(self, cancel: asyncio.Event, ready: Any) -> None:
        if self._ready_after:
            await asyncio.sleep(self._ready_after)
        ready()
        await cancel.wait()

    def ready(self) -> bool:
        return True

    async def stop(self) -> None:
        return None


class UntilStop(OnCancel):
    """Start returns only once its stop was called: the listener shape."""

    def __init__(self, name: str, ready_after: float = 0.0) -> None:
        super().__init__(name, ready_after)
        self._released = asyncio.Event()

    async def start(self, cancel: asyncio.Event, ready: Any) -> None:
        if self._ready_after:
            await asyncio.sleep(self._ready_after)
        ready()
        await self._released.wait()

    async def stop(self) -> None:
        self._released.set()


class Quitter(OnCancel):
    """Start returns cleanly by itself shortly after reporting ready."""

    async def start(self, cancel: asyncio.Event, ready: Any) -> None:
        ready()
        await asyncio.sleep(0.01)


class Recorder:
    def __init__(self, hook: Any = None) -> None:
        self.events: list[Event] = []
        self._hook = hook

    def publish(self, event: Event) -> None:
        self.events.append(event)
        if self._hook is not None:
            self._hook(event)

    def stopped(self) -> list[str]:
        return [e.payload["service"] for e in self.events if e.topic == "kit.serve.service.stopped"]


def _registry(*svcs: Any) -> ServiceRegistry:
    r = ServiceRegistry()
    for s in svcs:
        r.register(s)
    return r


def _enabled(*names: str) -> dict[str, ServiceConfig]:
    return {n: ServiceConfig(enabled=True) for n in names}


def _run(coro: Any) -> Any:
    async def bounded() -> Any:
        return await asyncio.wait_for(coro, timeout=10)

    return asyncio.run(bounded())


def test_cancel_at_aggregate_ready_publish_keeps_reverse_order():
    for _ in range(50):

        async def main() -> list[str]:
            cancel = asyncio.Event()
            rec = Recorder(
                lambda e: cancel.set() if e.topic == "kit.serve.supervisor.ready_reported" else None
            )
            sup = Supervisor(
                _registry(
                    OnCancel("alpha"), UntilStop("beta"), OnCancel("gamma"), UntilStop("delta")
                ),
                publisher=rec,
            )
            res = await sup.run(cancel, NAMES, _enabled(*NAMES))
            assert res.error is None
            return rec.stopped()

        assert _run(main()) == REVERSE


def test_cancel_while_last_service_starting_keeps_reverse_order():
    for _ in range(20):

        async def main() -> list[str]:
            cancel = asyncio.Event()
            rec = Recorder(
                lambda e: (
                    cancel.set()
                    if e.topic == "kit.serve.service.ready_reported"
                    and e.payload["service"] == "gamma"
                    else None
                )
            )
            sup = Supervisor(
                _registry(
                    OnCancel("alpha"),
                    UntilStop("beta"),
                    OnCancel("gamma"),
                    UntilStop("delta", ready_after=0.02),
                ),
                publisher=rec,
            )
            res = await sup.run(cancel, NAMES, _enabled(*NAMES))
            assert res.error is None
            return rec.stopped()

        assert _run(main()) == REVERSE


def test_cancel_after_steady_state_keeps_reverse_order():
    for _ in range(30):

        async def main() -> list[str]:
            cancel = asyncio.Event()
            rec = Recorder()
            sup = Supervisor(
                _registry(
                    OnCancel("alpha"), UntilStop("beta"), OnCancel("gamma"), UntilStop("delta")
                ),
                publisher=rec,
            )
            task = asyncio.create_task(sup.run(cancel, NAMES, _enabled(*NAMES)))
            await asyncio.sleep(0.005)
            cancel.set()
            await task
            return rec.stopped()

        assert _run(main()) == REVERSE


def test_early_self_exit_still_reports_stopped_once():
    async def main() -> None:
        cancel = asyncio.Event()
        rec = Recorder()
        sup = Supervisor(_registry(OnCancel("steady"), Quitter("quitter")), publisher=rec)
        task = asyncio.create_task(
            sup.run(cancel, ["steady", "quitter"], _enabled("steady", "quitter"))
        )
        for _ in range(200):
            if "quitter" in rec.stopped():
                break
            await asyncio.sleep(0.005)
        assert rec.stopped() == ["quitter"]
        cancel.set()
        await task
        assert rec.stopped() == ["quitter", "steady"]

    _run(main())

"""Core probe logic -- HTTP checks, latency, event publishing."""

from __future__ import annotations

import re
import time
from dataclasses import dataclass
from typing import Any
from urllib.error import URLError
from urllib.request import Request, urlopen

from kit_imports import kit_bus, kit_progress

_bus_mod = kit_bus()
_progress_mod = kit_progress()

Bus = _bus_mod.Bus
create_event = _bus_mod.create_event
ProgressReporter = _progress_mod.ProgressReporter
ProgressEvent = _progress_mod.ProgressEvent


# ---------------------------------------------------------------------------
# Result
# ---------------------------------------------------------------------------

@dataclass
class Result:
    target: str
    ok: bool
    status: int
    latency_ms: float
    error: str = ""


# ---------------------------------------------------------------------------
# Parse timeout
# ---------------------------------------------------------------------------

def _parse_timeout_s(s: str) -> float:
    m = re.match(r"^(\d+)(s|ms)$", s)
    if not m:
        return 5.0
    val = int(m.group(1))
    return val if m.group(2) == "s" else val / 1000


# ---------------------------------------------------------------------------
# Single target check
# ---------------------------------------------------------------------------

def _check_target(t: dict) -> Result:
    timeout = _parse_timeout_s(t.get("timeout", "5s"))
    url = t["url"]
    method = t.get("method", "GET")
    expect_status = t.get("expect", {}).get("status", 200)

    start = time.monotonic()
    try:
        req = Request(url, method=method)
        resp = urlopen(req, timeout=timeout)  # noqa: S310
        latency_ms = (time.monotonic() - start) * 1000
        status = resp.status
        ok = status == expect_status
        return Result(
            target=t["name"], ok=ok, status=status,
            latency_ms=round(latency_ms, 1),
        )
    except URLError as exc:
        latency_ms = (time.monotonic() - start) * 1000
        return Result(
            target=t["name"], ok=False, status=0,
            latency_ms=round(latency_ms, 1),
            error=str(exc.reason),
        )
    except Exception as exc:  # noqa: BLE001
        latency_ms = (time.monotonic() - start) * 1000
        return Result(
            target=t["name"], ok=False, status=0,
            latency_ms=round(latency_ms, 1),
            error=str(exc),
        )


# ---------------------------------------------------------------------------
# Run all targets
# ---------------------------------------------------------------------------

def check_targets(
    cfg: dict,
    b: Bus,
    progress: ProgressReporter,
) -> list[Result]:
    targets: list[dict] = cfg.get("targets", [])
    results: list[Result] = []
    prev_failing: set[str] = set()

    for i, t in enumerate(targets):
        progress.emit(ProgressEvent(
            phase="probe",
            step=t["name"],
            current=i + 1,
            total=len(targets),
            percent=round(((i + 1) / len(targets)) * 100),
            message=f"checking {t['url']}",
        ))

        r = _check_target(t)
        results.append(r)

        payload: dict[str, Any] = {
            "target": r.target,
            "ok": r.ok,
            "status": r.status,
            "latency_ms": r.latency_ms,
            "source": "probe/py",
            "method": t.get("method", "GET"),
        }
        if r.error:
            payload["error"] = r.error

        b.publish(create_event("kit.probe.check.executed", "probe/py", payload))

        if not r.ok:
            b.publish(create_event("kit.probe.check.alerted", "probe/py", payload))
            prev_failing.add(t["name"])
        elif t["name"] in prev_failing:
            b.publish(create_event("kit.probe.check.recovered", "probe/py", payload))
            prev_failing.discard(t["name"])

    return results

"""HTTP endpoint monitor using kit SDK packages (no CLI framework).

Demonstrates: config, bus, log, provenance, progress.
"""

from __future__ import annotations

import sys

from kit_imports import kit_bus, kit_config, kit_log, kit_progress

_config = kit_config()
_bus_mod = kit_bus()
_log_mod = kit_log()
_progress_mod = kit_progress()

Options = _config.Options
load = _config.load
create_bus = _bus_mod.create_bus
create_logger = _log_mod.create_logger
ProgressReporter = _progress_mod.ProgressReporter

from core import Result, check_targets  # noqa: E402


def main() -> None:
    import os

    cfg: dict = {"interval": "30s", "targets": []}

    cfg_path = os.path.join(
        os.path.dirname(__file__), '..', 'probe.yaml',
    )
    load(cfg, Options(project_config_path=cfg_path))

    b = create_bus()
    logger = create_logger()
    progress = ProgressReporter(sys.stderr, sys.stderr.isatty())

    # Subscribe to all probe events
    def on_event(event) -> None:  # noqa: ANN001
        p = event.payload
        logger.info(
            "event",
            topic=event.topic,
            target=p.get("target"),
            ok=p.get("ok"),
        )

    b.subscribe("kit.probe.#", on_event)

    results = check_targets(cfg, b, progress)
    print_summary(results)
    b.close()


def print_summary(results: list[Result]) -> None:
    print()
    print("=== Probe Summary ===")
    passed = failed = 0
    for r in results:
        if r.ok:
            status = "PASS"
            passed += 1
        else:
            status = "FAIL"
            failed += 1
        if r.error:
            detail = f'error="{r.error}"'
        else:
            detail = f"status={r.status} latency={r.latency_ms}ms"
        print(f"  [{status}] {r.target:<12} {detail}")
    print(f"\nTotal: {len(results)} | Passed: {passed} | Failed: {failed}")


if __name__ == "__main__":
    main()

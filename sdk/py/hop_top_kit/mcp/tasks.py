"""The ``io.modelcontextprotocol/tasks`` extension.

2026-07-28 moved polling tasks out of the core protocol and into a
negotiated extension. A server that supports it advertises the extension
in ``server/discover``'s ``capabilities.extensions`` map and serves
``tasks/get`` (and, when it opts in, ``tasks/list`` and ``tasks/cancel``).
A server that does not support it omits the map entirely — which is why
the core surface answers ``tasks/*`` with ``-32601`` at 404 until this
extension is mounted.

The extension is opt-in and additive. Mounting it changes exactly two
things: ``server/discover`` grows the ``extensions`` entry, and the
``tasks/*`` methods start resolving instead of 404ing. Every other
response byte is unchanged, so a mount that does not pass an extension
stays byte-identical to a surface that has never heard of tasks.

Task records and their wire field spellings come from ``mcp-types``
rather than being re-declared: ``Task``, ``TaskStatus``, and the
request/result params already carry the exact camelCase aliases the spec
fixes, so re-typing them here would only create a second source of truth.
"""

from __future__ import annotations

import time
from collections.abc import Iterable
from datetime import UTC, datetime
from typing import Any, Protocol

from mcp_types import Task, TaskStatus

from .modern import (
    STATUS_NOT_FOUND,
    STATUS_OK,
    CheckError,
    ModernHandler,
    RequestMeta,
)
from .protocol import (
    ERR_INVALID_PARAMS,
    ERR_METHOD_NOT_FOUND,
    Request,
    Response,
    RPCRequest,
    write_error,
    write_result,
)

#: The reserved extension name, as it appears in ``capabilities.extensions``.
EXTENSION_NAME = "io.modelcontextprotocol/tasks"

#: Terminal statuses. A task in one of these will never change again, so
#: a client can stop polling.
TERMINAL_STATUSES = frozenset({"completed", "failed", "cancelled"})


def now_iso() -> str:
    """An RFC 3339 timestamp in UTC, the spelling the spec's fields use."""
    return datetime.now(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


class TaskStore(Protocol):
    """Where task records live.

    Deliberately a protocol, not a class: an adopter running more than
    one instance needs a shared store, and baking in an in-process dict
    would make the surface silently wrong the moment it is scaled out.
    :class:`InMemoryTaskStore` is the single-instance default.
    """

    def get(self, task_id: str) -> Task | None:
        """The task with this id, or ``None``."""
        ...

    def list(self) -> Iterable[Task]:
        """Every known task."""
        ...

    def cancel(self, task_id: str) -> Task | None:
        """Move a task to ``cancelled``; ``None`` if it does not exist."""
        ...


class InMemoryTaskStore:
    """A process-local :class:`TaskStore`, suitable for one instance.

    Expired tasks are evicted lazily on read rather than by a background
    sweeper: this surface holds no threads of its own, and a task nobody
    asks about costs nothing to leave in the dict until it is next
    touched.
    """

    def __init__(self) -> None:
        self._tasks: dict[str, Task] = {}
        self._created: dict[str, float] = {}

    def put(self, task: Task) -> Task:
        """Insert or replace a task record."""
        self._tasks[task.task_id] = task
        self._created[task.task_id] = time.time()
        return task

    def create(
        self,
        task_id: str,
        *,
        status: TaskStatus = "working",
        ttl: int = 60_000,
        poll_interval: int | None = None,
        status_message: str | None = None,
    ) -> Task:
        """Create and store a new task record."""
        stamp = now_iso()
        return self.put(
            Task(
                taskId=task_id,
                status=status,
                createdAt=stamp,
                lastUpdatedAt=stamp,
                ttl=ttl,
                pollInterval=poll_interval,
                statusMessage=status_message,
            )
        )

    def update(
        self,
        task_id: str,
        *,
        status: TaskStatus | None = None,
        status_message: str | None = None,
    ) -> Task | None:
        """Advance a task's status, refreshing ``lastUpdatedAt``."""
        task = self.get(task_id)
        if task is None:
            return None
        updated = task.model_copy(
            update={
                "status": status if status is not None else task.status,
                "status_message": (
                    status_message if status_message is not None else task.status_message
                ),
                "last_updated_at": now_iso(),
            }
        )
        self._tasks[task_id] = updated
        return updated

    def get(self, task_id: str) -> Task | None:
        task = self._tasks.get(task_id)
        if task is None:
            return None
        if self._expired(task_id, task):
            self._evict(task_id)
            return None
        return task

    def list(self) -> list[Task]:
        for task_id in list(self._tasks):
            self.get(task_id)  # drives lazy eviction
        return list(self._tasks.values())

    def cancel(self, task_id: str) -> Task | None:
        task = self.get(task_id)
        if task is None:
            return None
        if task.status in TERMINAL_STATUSES:
            return task
        return self.update(task_id, status="cancelled")

    def _expired(self, task_id: str, task: Task) -> bool:
        if task.ttl is None:
            return False
        created = self._created.get(task_id, 0.0)
        return (time.time() - created) * 1000.0 > task.ttl

    def _evict(self, task_id: str) -> None:
        self._tasks.pop(task_id, None)
        self._created.pop(task_id, None)


class TasksExtension:
    """Mountable ``io.modelcontextprotocol/tasks`` support.

    Pass an instance to ``mount_mcp(..., extensions=(TasksExtension(),))``
    to advertise the extension and serve its methods.
    """

    name = EXTENSION_NAME

    def __init__(
        self,
        store: TaskStore | None = None,
        *,
        allow_list: bool = True,
        allow_cancel: bool = True,
    ) -> None:
        self.store: TaskStore = store if store is not None else InMemoryTaskStore()
        self._allow_list = allow_list
        self._allow_cancel = allow_cancel
        self._handler: ModernHandler | None = None

    def capability(self) -> dict[str, Any]:
        """The ``capabilities.extensions`` entry for ``server/discover``.

        Only the sub-capabilities actually served are advertised, so a
        client never polls a method this mount will 404.
        """
        capability: dict[str, Any] = {"requests": {"tasks/get": {}}}
        if self._allow_list:
            capability["list"] = {}
        if self._allow_cancel:
            capability["cancel"] = {}
        return capability

    def methods(self) -> dict[str, Any]:
        """The JSON-RPC methods this extension contributes."""
        handlers: dict[str, Any] = {"tasks/get": self._get}
        if self._allow_list:
            handlers["tasks/list"] = self._list
        if self._allow_cancel:
            handlers["tasks/cancel"] = self._cancel
        return handlers

    def bind(self, handler: ModernHandler) -> None:
        """Receive the handler that will stamp this extension's envelopes."""
        self._handler = handler

    # --- methods ---------------------------------------------------------

    def _get(self, _request: Request, rpc: RPCRequest, _meta: RequestMeta) -> Response:
        """``tasks/get`` — one task's current state."""
        try:
            task_id = _require_task_id(rpc.params)
        except CheckError as exc:
            return write_error(rpc.id_raw, exc.code, exc.message, exc.status)
        task = self.store.get(task_id)
        if task is None:
            return write_error(
                rpc.id_raw, ERR_INVALID_PARAMS, f"unknown task: {task_id}", STATUS_OK
            )
        return self._result(rpc, _task_body(task))

    def _list(self, _request: Request, rpc: RPCRequest, _meta: RequestMeta) -> Response:
        """``tasks/list`` — every known task.

        Pagination is not implemented, matching ``tools/list``: a
        ``cursor`` param is ignored and no ``nextCursor`` comes back.
        """
        tasks = [_task_body(task) for task in self.store.list()]
        return self._result(rpc, {"tasks": tasks})

    def _cancel(self, _request: Request, rpc: RPCRequest, _meta: RequestMeta) -> Response:
        """``tasks/cancel`` — request cancellation of a running task.

        Cancelling an already-terminal task is a no-op that returns the
        task unchanged, rather than an error: the caller's intent is
        already satisfied.
        """
        try:
            task_id = _require_task_id(rpc.params)
        except CheckError as exc:
            return write_error(rpc.id_raw, exc.code, exc.message, exc.status)
        task = self.store.cancel(task_id)
        if task is None:
            return write_error(
                rpc.id_raw, ERR_INVALID_PARAMS, f"unknown task: {task_id}", STATUS_OK
            )
        return self._result(rpc, _task_body(task))

    def _result(self, rpc: RPCRequest, body: dict[str, Any]) -> Response:
        """Stamp and write one extension result.

        Task results carry no cache hints: a task's state is the one thing
        on this surface that is expected to change between two identical
        requests.
        """
        if self._handler is None:  # pragma: no cover - mount always binds
            raise RuntimeError("mcp: tasks extension used before bind()")
        return write_result(rpc.id_raw, self._handler.stamp_envelope(body), STATUS_OK)


def _task_body(task: Task) -> dict[str, Any]:
    """Render a task record in its spec wire spelling.

    ``by_alias`` is what turns ``task_id`` into ``taskId``; unset optional
    members are dropped rather than emitted as ``null``.
    """
    return task.model_dump(by_alias=True, exclude_none=True)


def _require_task_id(params: Any) -> str:
    """Read the required ``params.taskId``."""
    if isinstance(params, dict):
        value = params.get("taskId")
        if isinstance(value, str) and value:
            return value
    raise CheckError(
        ERR_INVALID_PARAMS, "missing required params.taskId", STATUS_OK
    )


#: Re-exported so an adopter can answer ``tasks/*`` with the core surface's
#: own not-found shape when the extension is deliberately not mounted.
UNSUPPORTED = (ERR_METHOD_NOT_FOUND, STATUS_NOT_FOUND)

"""Command-tree bridge — the surface-facing view of a CLI.

Ported from ``go/transport/cmdsurface/bridge.go``. The Go bridge projects
a cobra root onto many surfaces; this one projects an equivalent Python
command tree. The MCP handlers only ever touch :class:`Bridge` through
:meth:`Bridge.leaves` and :meth:`Bridge.invoke`, so the tree
representation stays swappable — an adopter driving typer, click, or a
hand-built tree supplies :class:`Command` nodes and the surface works
unchanged.

Leaf discovery, enablement, and the policy gate all mirror Go: leaves are
walked once at construction in depth-first order with children visited
in name order, hidden and deprecated nodes are skipped, and
:meth:`Bridge.invoke` applies the enablement check *then* the destructive
ceiling before delegating to the runner.
"""

from __future__ import annotations

from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from .safety import (
    DestructiveBlockedError,
    Policy,
    SafetyClass,
    Surface,
    SurfaceNotEnabledError,
    UnknownCommandError,
    classify,
    default_policy,
)

#: JSON Schema primitive for each declared flag type, mirroring Go's
#: ``mcpJSONType`` pflag mapping.
_JSON_TYPES = {
    "bool": "boolean",
    "int": "integer",
    "int8": "integer",
    "int16": "integer",
    "int32": "integer",
    "int64": "integer",
    "uint": "integer",
    "uint8": "integer",
    "uint16": "integer",
    "uint32": "integer",
    "uint64": "integer",
    "count": "integer",
    "float32": "number",
    "float64": "number",
    "stringArray": "array",
    "stringSlice": "array",
    "intSlice": "array",
    "boolSlice": "array",
}


def json_type(declared: str) -> str:
    """Map a declared flag type to its JSON Schema primitive.

    Unrecognised types fall back to ``"string"``, matching Go's default
    branch: the bridge forwards flag values as-is and the leaf parses
    them, so a string is always a safe description.
    """
    return _JSON_TYPES.get(declared, "string")


@dataclass
class Flag:
    """One command-line flag, as the schema builder sees it.

    ``type`` is the declared type name (``"bool"``, ``"int"``,
    ``"stringSlice"``, …) rather than a Python type, so the same
    vocabulary Go reflects out of pflag crosses the language boundary
    unchanged.
    """

    name: str
    usage: str = ""
    type: str = "string"
    required: bool = False
    hidden: bool = False
    deprecated: bool = False

    def to_property(self) -> dict[str, Any]:
        """Render this flag as a JSON Schema property object."""
        kind = json_type(self.type)
        prop: dict[str, Any] = {"type": kind, "description": self.usage}
        if kind == "array":
            prop["items"] = {"type": "string"}
        return prop


@dataclass
class Command:
    """A node in the command tree.

    A node with no children and a ``run`` callable is a leaf and becomes
    one MCP tool. ``annotations`` carries the ``kit/*`` safety vocabulary
    :func:`~hop_top_kit.mcp.safety.classify` reads.
    """

    name: str
    short: str = ""
    run: Callable[[Mapping[str, Any]], Result] | None = None
    flags: list[Flag] = field(default_factory=list)
    children: list[Command] = field(default_factory=list)
    annotations: dict[str, str] = field(default_factory=dict)
    hidden: bool = False
    deprecated: bool = False
    #: Flags inherited by every descendant, mirroring cobra's persistent
    #: flags. Local flags of the same name win.
    persistent_flags: list[Flag] = field(default_factory=list)
    #: Whether this command gains a ``--help`` flag on first execution,
    #: mirroring cobra's lazy registration. See :meth:`attach_help_flag`.
    lazy_help_flag: bool = True

    @property
    def has_subcommands(self) -> bool:
        return bool(self.children)

    @property
    def runnable(self) -> bool:
        return self.run is not None

    def attach_help_flag(self) -> None:
        """Register this command's ``--help`` flag, once.

        Models cobra's lazy registration: a command's help flag is not
        created when the command is declared but when it is first
        executed. The consequence is visible on the wire — a leaf's
        ``inputSchema`` gains a ``help`` property after its first
        ``tools/call``, so two byte-identical ``tools/list`` requests
        either side of an invocation legitimately return different bytes.

        Surfacing it rather than hiding it is deliberate. The flag is a
        real, declared input of the command once it exists, and an
        adopter's process is long-lived, so this is the steady state
        their clients actually observe — not an artefact of test
        isolation. Suppressing it would make the schema disagree with the
        command it describes.

        Idempotent, and skipped entirely when a caller declares its own
        ``help`` flag or opts out via :attr:`lazy_help_flag`.
        """
        if not self.lazy_help_flag:
            return
        if any(flag.name == "help" for flag in self.flags):
            return
        self.flags.append(Flag(name="help", usage=f"help for {self.name}", type="bool"))


@dataclass
class Result:
    """What a leaf invocation produced.

    Mirrors Go's ``Result``: captured streams, an exit code, and an
    optional structured payload the modern era surfaces as
    ``structuredContent``.
    """

    stdout: str = ""
    stderr: str = ""
    exit_code: int = 0
    data: Any = None


@dataclass
class Meta:
    """Per-invocation metadata carried alongside the flags."""

    surface: Surface = Surface.LIB
    extra: dict[str, str] = field(default_factory=dict)


@dataclass
class Invocation:
    """One decoded request handed to :meth:`Bridge.invoke`."""

    path: tuple[str, ...] = ()
    flags: dict[str, Any] = field(default_factory=dict)
    meta: Meta = field(default_factory=Meta)


@dataclass
class Leaf:
    """The per-command view a surface needs.

    ``path`` is the command path from the root, excluding the root
    segment; ``enabled`` is the surface allow-set under the current
    configuration.
    """

    path: tuple[str, ...]
    cmd: Command
    cls: SafetyClass
    enabled: dict[Surface, bool]

    @property
    def path_key(self) -> str:
        """The leaf path as a space-joined string."""
        return " ".join(self.path)

    @property
    def tool_name(self) -> str:
        """The leaf path as a dotted MCP tool name (``widget.add``)."""
        return ".".join(self.path)

    def collect_flags(self) -> tuple[dict[str, Any], list[str]]:
        """Return the schema properties and the required-name list.

        Hidden and deprecated flags are excluded. Local flags are visited
        before inherited ones so a locally-overridden flag's annotations
        win, and each name is emitted exactly once — the same ordering
        contract Go's ``collectFlags`` documents.
        """
        props: dict[str, Any] = {}
        required: list[str] = []
        seen: set[str] = set()
        for flag in list(self.cmd.flags) + list(self.cmd.persistent_flags):
            if flag.hidden or flag.deprecated or flag.name in seen:
                continue
            seen.add(flag.name)
            props[flag.name] = flag.to_property()
            if flag.required:
                required.append(flag.name)
        return props, required

    def tool_envelope(self) -> dict[str, Any]:
        """Render this leaf as an MCP tool descriptor.

        Shared verbatim by both eras — ``name``, ``description``,
        ``inputSchema`` — so schema drift between 2024-11-05 and
        2026-07-28 cannot happen, exactly as in Go where one
        ``buildToolEnvelope`` serves both handlers.
        """
        props, required = self.collect_flags()
        schema: dict[str, Any] = {"type": "object", "properties": props}
        if required:
            schema["required"] = required
        return {
            "name": self.tool_name,
            "description": self.cmd.short,
            "inputSchema": schema,
        }


def tool_path(name: str) -> tuple[str, ...]:
    """Split a dotted MCP tool name back into a leaf path."""
    if not name:
        return ()
    return tuple(name.split("."))


class Bridge:
    """Projects a command tree onto surfaces.

    Leaves are discovered once at construction: commands added to the
    tree afterwards are invisible to the bridge, matching Go's
    documented ``New`` contract.
    """

    def __init__(
        self,
        root: Command,
        *,
        runner: Callable[[Invocation], Result] | None = None,
        policy: Policy | None = None,
    ) -> None:
        self._root = root
        self._policy = policy if policy is not None else default_policy()
        self._runner = runner if runner is not None else _in_process_runner(root)
        self._leaves: list[Leaf] = []
        self._by_path: dict[str, Leaf] = {}
        self._discover()

    def _discover(self) -> None:
        """Walk the tree depth-first, recording runnable leaves.

        Children are visited in name order because cobra sorts them by
        default, and ``tools/list`` output order is part of the wire
        contract the fixtures pin.
        """
        defaults = self._policy.resolved_defaults()

        def walk(cmd: Command, path: tuple[str, ...]) -> None:
            if path and not cmd.has_subcommands and cmd.runnable:
                leaf = Leaf(
                    path=path,
                    cmd=cmd,
                    cls=classify(cmd.annotations),
                    enabled={surface: True for surface in defaults},
                )
                self._leaves.append(leaf)
                self._by_path[leaf.path_key] = leaf
            for child in sorted(cmd.children, key=lambda c: c.name):
                if child.hidden or child.deprecated:
                    continue
                walk(child, (*path, child.name))

        walk(self._root, ())

    @property
    def policy(self) -> Policy:
        """The active policy. Surfaces consult it to render capability lists."""
        return self._policy

    def leaves(self) -> list[Leaf]:
        """Leaves in discovery order. The list is a copy; the leaves are not."""
        return list(self._leaves)

    def resolve_leaf(self, path: Sequence[str]) -> Leaf:
        """Return the leaf at ``path`` or raise :class:`UnknownCommandError`."""
        key = " ".join(path)
        leaf = self._by_path.get(key)
        if leaf is None:
            raise UnknownCommandError(f"cmdsurface: unknown command: {' '.join(path)}")
        return leaf

    def expose(self, pattern: str, *surfaces: Surface) -> Bridge:
        """Enable ``surfaces`` on every leaf matching ``pattern``."""
        return self._set_surfaces(pattern, surfaces, True)

    def hide(self, pattern: str, *surfaces: Surface) -> Bridge:
        """Disable ``surfaces`` on every leaf matching ``pattern``."""
        return self._set_surfaces(pattern, surfaces, False)

    def _set_surfaces(self, pattern: str, surfaces: Sequence[Surface], value: bool) -> Bridge:
        if not surfaces:
            return self
        for leaf in self._leaves:
            if not match_pattern(pattern, leaf.path):
                continue
            for surface in surfaces:
                leaf.enabled[surface] = value
        return self

    def invoke(self, inv: Invocation) -> Result:
        """Resolve, gate, and run one invocation.

        Raises :class:`UnknownCommandError` when the path does not
        resolve, :class:`SurfaceNotEnabledError` when the leaf is not
        exposed on the invoking surface, and
        :class:`DestructiveBlockedError` when the policy gate refuses.
        The runner's own exceptions propagate unchanged.
        """
        leaf = self.resolve_leaf(inv.path)
        surface = inv.meta.surface or Surface.LIB
        if not leaf.enabled.get(surface, False):
            raise SurfaceNotEnabledError(
                f"cmdsurface: surface not enabled for command: {leaf.path_key} on {surface}"
            )
        if not self._policy.allowed(leaf.cls, surface):
            raise DestructiveBlockedError(
                f"cmdsurface: destructive command blocked on this surface: "
                f"{leaf.path_key} on {surface}"
            )
        return self._runner(inv)


def match_pattern(pattern: str, path: Sequence[str]) -> bool:
    """Report whether ``path`` matches an expose/hide ``pattern``.

    Forms: an exact space-separated path (``"widget add"``), a wildcard
    tail (``"widget *"``) matching any descendant, or a bare ``"*"``
    matching every leaf.
    """
    pattern = pattern.strip()
    if not pattern:
        return False
    if pattern == "*":
        return True
    parts = pattern.split()
    if not parts:
        return False
    if parts[-1] == "*":
        prefix = parts[:-1]
        if len(path) < len(prefix):
            return False
        return all(path[i] == seg for i, seg in enumerate(prefix))
    if len(parts) != len(path):
        return False
    return all(path[i] == seg for i, seg in enumerate(parts))


def _in_process_runner(root: Command) -> Callable[[Invocation], Result]:
    """Default runner: call the resolved leaf's ``run`` in this process.

    Execution has one side effect beyond running the command: it attaches
    the command's ``--help`` flag if it does not have one yet, mirroring
    cobra's lazy registration (see :meth:`Command.attach_help_flag`). The
    flag is attached *before* ``run`` is called, so it exists even when
    the command raises — cobra registers on the execution path, not on
    success.
    """

    def run(inv: Invocation) -> Result:
        cmd: Command = root
        for segment in inv.path:
            match = next((c for c in cmd.children if c.name == segment), None)
            if match is None:
                raise UnknownCommandError(f"cmdsurface: unknown command: {' '.join(inv.path)}")
            cmd = match
        if cmd.run is None:
            raise UnknownCommandError(f"cmdsurface: unknown command: {' '.join(inv.path)}")
        cmd.attach_help_flag()
        return cmd.run(inv.flags)

    return run

"""MRTR confirmation for the modern ``tools/call`` path.

Multi round-trip requests replace the server-initiated requests the
handshake era ran over open streams. When a mount is given key material,
the first call on a ``kit/requires-confirmation`` leaf comes back as
``resultType: "input_required"`` carrying one ``elicitation/create`` form
request under the reserved ``"confirm"`` key plus an integrity-protected
``requestState``. The client collects the user's decision and retries the
original call with ``params.inputResponses`` and the echoed
``params.requestState``; the gate verifies the state and proceeds only on
``"accept"``.

**Statelessness.** Everything a retry needs lives inside the
``requestState`` itself. There is no pending-request store of any kind,
so any instance holding the same key verifies state minted by any other —
which is exactly why the key must be adopter-supplied and shared, and why
there is deliberately no generated-at-mount default. The corollary is
that an accepted state stays honourable for an identical (leaf,
arguments, principal) until its TTL lapses; single-use redemption would
need the server-side state this design refuses to keep, so a short TTL
bounds the window instead.

**Two verification failures are deliberately distinct.** An expired but
*authentic* state is a routine re-prompt. A state that fails HMAC
verification is never honoured: the rejection is recorded as a
security-relevant audit event first, and only then is a fresh prompt
issued with newly minted state. Tampering can therefore cost a request,
but is never silently treated as a re-prompt.

**MRTR never relaxes the destructive ceiling.** This gate can satisfy
confirmation; the policy check still runs inside the bridge afterwards,
exactly as on every other path.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import time
from enum import Enum
from typing import Any

from ._json import dumps
from .bridge import Bridge, Leaf
from .legacy import error_result_block
from .modern import header_confirmation_gate
from .protocol import (
    META_CLIENT_CAPABILITIES,
    RESULT_TYPE_INPUT_REQUIRED,
    Request,
    RPCRequest,
)

#: The single reserved ``inputRequests`` key this flow uses; the retry's
#: answer is read from the same key in ``inputResponses``.
CONFIRM_KEY = "confirm"

#: Lifetime of a minted ``requestState``. Long enough for a human to read
#: and answer, short enough to bound the replay window statelessness
#: cannot otherwise close.
STATE_TTL_SECONDS = 5 * 60

#: Tags the ``requestState`` wire format so a future change invalidates
#: old state rather than misparsing it.
STATE_VERSION = "v1"

#: Domain separation for the MAC, so this tag can never be confused with
#: another HMAC minted under a shared key.
_MAC_DOMAIN = "cmdsurface-mcp-confirm-" + STATE_VERSION


class StateStatus(Enum):
    """The outcome of verifying a presented ``requestState``."""

    VALID = "valid"
    EXPIRED = "expired"
    INVALID = "invalid"


class Binding:
    """The request context a ``requestState`` is bound to.

    The MAC covers every field plus the expiry, so state presented for a
    different leaf, different arguments, or by a different principal
    fails verification outright.
    """

    __slots__ = ("args_digest", "principal", "tool")

    def __init__(self, tool: str, args_digest: str, principal: str) -> None:
        #: The leaf path key, e.g. ``"widget purge"``.
        self.tool = tool
        #: Hex SHA-256 of the canonically serialised ``params.arguments``.
        self.args_digest = args_digest
        #: Hex SHA-256 of the ``Authorization`` value, or ``""``.
        self.principal = principal


class ElicitationConfirmationGate:
    """The confirmation strategy installed when a mount has key material.

    Clients that did not declare form-mode elicitation keep the
    ``X-Confirm-Token`` header gate: the spec forbids sending
    ``inputRequests`` for a capability the client never declared, and the
    capability stays optional precisely because this fallback exists — so
    a missing elicitation capability is never ``-32021`` here.
    """

    def __init__(self, key: bytes, bridge: Bridge | None = None) -> None:
        if not key:
            raise ValueError("mcp: confirmation key must be non-empty")
        self._key = bytes(key)
        self._bridge = bridge
        #: Audit records of rejected states, in lieu of the Go bridge's
        #: sink fan-out, which this port does not carry.
        self.rejections: list[dict[str, str]] = []

    def __call__(
        self, request: Request, leaf: Leaf, rpc: RPCRequest
    ) -> tuple[dict[str, Any], int] | None:
        if not leaf.cls.requires_confirmation:
            return None
        if not client_supports_form_elicitation(rpc.params):
            return header_confirmation_gate(request, leaf, rpc)

        binding = Binding(
            tool=leaf.path_key,
            args_digest=args_digest(rpc.params),
            principal=principal(request),
        )
        state, action = parse_retry(rpc.params)
        if not state:
            # A first call — or a retry that dropped the state it was
            # required to echo, which is indistinguishable from one and
            # equally unverifiable. Prompt (again).
            return self.input_required(leaf, binding), 200

        status = verify_state(self._key, state, binding, time.time())
        if status is StateStatus.INVALID:
            # Tampered, malformed, or minted for a different request or
            # principal: never honoured, audited, then re-prompted with
            # fresh state.
            self._audit_rejection(leaf)
            return self.input_required(leaf, binding), 200
        if status is StateStatus.EXPIRED:
            # Authentic but past its TTL: a routine re-prompt, no audit.
            return self.input_required(leaf, binding), 200

        if action == "accept":
            return None
        if action in ("decline", "cancel"):
            return error_result_block("confirmation declined"), 200
        # The answer is missing or unusable: re-request the information
        # rather than erroring.
        return self.input_required(leaf, binding), 200

    def input_required(self, leaf: Leaf, binding: Binding) -> dict[str, Any]:
        """Build one ``input_required`` confirmation prompt.

        Carries both ``inputRequests`` and ``requestState`` — the spec
        requires at least one, and this flow always has both. Interim
        results are never cacheable, so no ``ttlMs`` or ``cacheScope``
        member appears here, ever.
        """
        expiry = int(time.time()) + STATE_TTL_SECONDS
        return {
            "resultType": RESULT_TYPE_INPUT_REQUIRED,
            "inputRequests": {
                CONFIRM_KEY: {
                    "method": "elicitation/create",
                    "params": {
                        "mode": "form",
                        "message": f"Approve execution of {dumps(leaf.tool_name)}?",
                        # No form fields: the approval rides the elicit
                        # action, so the requested schema is empty.
                        "requestedSchema": {"type": "object", "properties": {}},
                    },
                }
            },
            "requestState": mint_state(self._key, binding, expiry),
        }

    def _audit_rejection(self, leaf: Leaf) -> None:
        self.rejections.append(
            {
                "path": leaf.path_key,
                "mcp_confirm_rejection": "request_state_verification_failed",
            }
        )


def client_supports_form_elicitation(params: Any) -> bool:
    """Report whether the client declared form-mode elicitation.

    An empty ``elicitation`` object declares form-only support; a
    non-empty one must name ``"form"`` among its modes, since a url-only
    client cannot receive this flow's form request. Anything that is not
    a conforming object declaration counts as undeclared, failing toward
    the header fallback rather than toward sending a request the client
    never said it could handle.
    """
    if not isinstance(params, dict):
        return False
    meta = params.get("_meta")
    if not isinstance(meta, dict):
        return False
    caps = meta.get(META_CLIENT_CAPABILITIES)
    if not isinstance(caps, dict):
        return False
    modes = caps.get("elicitation")
    if not isinstance(modes, dict):
        return False
    if not modes:
        return True
    return "form" in modes


def parse_retry(params: Any) -> tuple[str, str]:
    """Read the MRTR retry members tolerantly.

    Absent or wrongly-typed members stay empty: a missing state means
    "prompt" and a missing action means "re-prompt", so a malformed retry
    converges on a fresh prompt rather than a decode error.
    """
    if not isinstance(params, dict):
        return "", ""
    state = params.get("requestState")
    state_text = state if isinstance(state, str) else ""
    responses = params.get("inputResponses")
    action = ""
    if isinstance(responses, dict):
        entry = responses.get(CONFIRM_KEY)
        if isinstance(entry, dict):
            value = entry.get("action")
            action = value if isinstance(value, str) else ""
    return state_text, action


def principal(request: Request) -> str:
    """Derive the principal component: hex SHA-256 of ``Authorization``.

    Presence-only bearer checking is all this surface does for auth, so
    the raw header value is the closest stable identifier available;
    hashing keeps credential material out of the MAC input.
    """
    auth = request.headers.get("authorization")
    if not auth:
        return ""
    return hashlib.sha256(auth.encode("utf-8")).hexdigest()


def args_digest(params: Any) -> str:
    """Hex SHA-256 of the canonically serialised ``params.arguments``.

    Canonical form sorts object keys at every level, so equal argument
    sets digest identically whatever order the client sent them in;
    absent arguments canonicalise to ``null``. Only the arguments
    participate — ``_meta``, ``inputResponses``, and ``requestState`` all
    legitimately differ between the first call and its retry, and the
    tool name is bound separately.
    """
    arguments = None
    if isinstance(params, dict):
        candidate = params.get("arguments")
        if isinstance(candidate, dict):
            arguments = candidate
    canonical = dumps(arguments)
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _mac(key: bytes, binding: Binding, expiry: int) -> bytes:
    """HMAC-SHA-256 binding a state to its expiry and request context.

    Each component is written length-prefixed so the concatenation is
    unambiguous whatever the contents — no delimiter-injection ambiguity
    — behind a domain-separation constant.
    """
    mac = hmac.new(key, digestmod=hashlib.sha256)
    for part in (_MAC_DOMAIN, str(expiry), binding.tool, binding.args_digest, binding.principal):
        mac.update(f"{len(part)}:{part}".encode())
    return mac.digest()


def mint_state(key: bytes, binding: Binding, expiry: int) -> str:
    """Render an opaque ``requestState``: ``v1.<expiry>.<base64url(mac)>``.

    Only the version and expiry travel in the clear; the full binding is
    reconstructed from the retry request itself at verification time,
    which is what keeps the state small and the flow stateless.
    """
    tag = base64.urlsafe_b64encode(_mac(key, binding, expiry)).rstrip(b"=").decode("ascii")
    return f"{STATE_VERSION}.{expiry}.{tag}"


def verify_state(key: bytes, state: str, binding: Binding, now: float) -> StateStatus:
    """Check a presented state against the current request's binding.

    Authenticity is decided *before* expiry, so ``EXPIRED`` is only ever
    reported for a state that verifiably came from this key and this
    exact binding — a tampered expiry fails the MAC and lands in
    ``INVALID``, never in ``EXPIRED``. Any structural defect is a
    verification failure too: a state that cannot be verified is never
    honoured.
    """
    parts = state.split(".")
    if len(parts) != 3 or parts[0] != STATE_VERSION:
        return StateStatus.INVALID
    try:
        expiry = int(parts[1])
    except ValueError:
        return StateStatus.INVALID
    try:
        padding = "=" * (-len(parts[2]) % 4)
        tag = base64.urlsafe_b64decode(parts[2] + padding)
    except Exception:
        return StateStatus.INVALID
    if not hmac.compare_digest(tag, _mac(key, binding, expiry)):
        return StateStatus.INVALID
    if expiry < now:
        return StateStatus.EXPIRED
    return StateStatus.VALID

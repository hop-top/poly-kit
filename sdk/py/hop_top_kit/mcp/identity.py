"""Who is calling, as both eras' ``tools/call`` gate reads it.

The only source of identity is the mount's verifier. An ``Authorization``
header is presence, not verification, and a caller or scopes the request
claims in its body never become identity. Mirrors the Go surfaces, where
only an established ``Meta`` admits a ``kit/auth-required`` leaf.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING

from .bridge import Meta
from .protocol import Response
from .safety import Surface

if TYPE_CHECKING:
    from .protocol import Request

#: ``Meta.established`` when nothing was established: any caller the
#: request names is a claim.
ESTABLISHED_NONE = ""
#: ``Meta.established`` when the mount's verifier accepted a credential
#: the request presented.
ESTABLISHED_VERIFIED = "verified"

#: The key the verifier's scopes are recorded under in ``Meta.extra``.
SCOPES_EXTRA_KEY = "scopes"

#: The challenge a 401 carries in ``WWW-Authenticate``, as Go's
#: ``api.DefaultAuthChallenge``.
AUTH_CHALLENGE = "Bearer"


@dataclass(frozen=True)
class Identity:
    """The caller a verifier established for one request.

    ``caller`` is the stable principal (a user id, a service account, a
    client id), ``tenant`` the account it acts in, ``scopes`` what it was
    granted. All optional: a verifier may accept a credential that names
    no principal.
    """

    caller: str = ""
    tenant: str = ""
    scopes: tuple[str, ...] = ()


#: Establishes who is calling. The mount calls it once per ``tools/call``
#: with the request as received; it checks a credential the request
#: presents and returns the :class:`Identity`, or ``None`` to refuse.
#: Raising refuses too.
Verifier = Callable[["Request"], "Identity | None"]


def establish_caller(verifier: Verifier | None, request: Request) -> Identity | None:
    """Run the mount's verifier over ``request``.

    Returns the identity it established, or ``None`` when there is no
    verifier, it refused, returned something that is not an
    :class:`Identity`, or raised: every failure fails closed.
    """
    if verifier is None:
        return None
    try:
        ident = verifier(request)
    except Exception:
        return None
    return ident if isinstance(ident, Identity) else None


def invocation_meta(ident: Identity | None, extra: dict[str, str] | None = None) -> Meta:
    """Build one ``tools/call`` invocation's :class:`Meta`.

    The MCP surface, the audit ``extra`` the era supplies, and the
    identity the verifier established — caller, tenant, ``established``
    and the comma-joined ``scopes`` extra entry. Nothing from ``ident``
    is set when it is ``None``, and nothing here reads the request.
    """
    bag = {k: v for k, v in (extra or {}).items() if k != SCOPES_EXTRA_KEY}
    meta = Meta(surface=Surface.MCP, extra=bag)
    if ident is None:
        return meta
    meta.established = ESTABLISHED_VERIFIED
    meta.caller = ident.caller
    meta.tenant = ident.tenant
    if ident.scopes:
        bag[SCOPES_EXTRA_KEY] = ",".join(ident.scopes)
    return meta


def unauthenticated(response: Response) -> Response:
    """Stamp the ``WWW-Authenticate`` challenge on a 401 refusal."""
    response.headers["www-authenticate"] = AUTH_CHALLENGE
    return response
